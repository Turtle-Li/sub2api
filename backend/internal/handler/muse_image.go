package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *OpenAIGatewayHandler) MuseImages(c *gin.Context) {
	endpoint := service.MuseImageGenerate
	switch {
	case strings.HasSuffix(c.FullPath(), "/images/tasks"):
		endpoint = service.MuseImageCreate
	case strings.Contains(c.FullPath(), "/images/edits"):
		endpoint = service.MuseImageEdit
	}
	setActualUpstreamEndpoint(c, EndpointImageTasks)
	h.handleGrokMedia(c, endpoint, "")
}

func (h *OpenAIGatewayHandler) MuseImageStatus(c *gin.Context) {
	setActualUpstreamEndpoint(c, EndpointImageTasks)
	h.handleGrokMedia(c, service.MuseImageStatus, c.Param("task_id"))
}

func (h *OpenAIGatewayHandler) MuseImageContent(c *gin.Context) {
	setActualUpstreamEndpoint(c, EndpointImageTasks)
	h.handleGrokMedia(c, service.MuseImageContent, c.Param("task_id"))
}

func (h *OpenAIGatewayHandler) prepareMuseMedia(c *gin.Context, log *zap.Logger, key *service.APIKey, subject middleware.AuthSubject, info service.GrokMediaRequestInfo, req *service.MuseMediaRequest) bool {
	audit := func(stage string) bool {
		decision := h.checkSecurityAuditStage(c, log, key, subject, service.ContentModerationProtocolOpenAIImages, info.Model, info.ModerationBody(), stage)
		if decision != nil && !decision.AllowNextStage {
			h.openAISecurityAuditError(c, decision)
			return false
		}
		return true
	}
	if !audit("muse-prompt") {
		return false
	}
	images, err := req.PrepareReferences(c.Request.Context())
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return false
	}
	for i, image := range images {
		info.InputImageURLs = []string{image}
		if !audit("muse-reference-" + strconv.Itoa(i+1)) {
			return false
		}
	}
	return true
}

func (h *OpenAIGatewayHandler) forwardMuseImage(
	c *gin.Context, ctx context.Context, endpoint service.GrokMediaEndpoint, id, scopedKey string,
	body []byte, req *service.MuseMediaRequest, key *service.APIKey, subject middleware.AuthSubject,
	account *service.Account, log *zap.Logger, releaseAccount, releaseMedia func(),
) {
	creating := endpoint.IsGenerationRequest()
	c.Header("Cache-Control", "private, no-store")
	if creating {
		if err := h.gatewayService.ReserveMuseImageSubmission(ctx, key, scopedKey, account.ID); err != nil {
			h.errorResponse(c, http.StatusConflict, "invalid_request_error", err.Error())
			return
		}
	}
	task, err := h.gatewayService.ForwardMuseImage(ctx, c, account, id, scopedKey, body)
	if creating {
		releaseAccount()
		releaseMedia()
		body = nil
	}
	if err != nil {
		log.Warn("muse_image.forward_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		if !c.Writer.Written() {
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Muse image request failed")
		}
		return
	}
	h.gatewayService.ReportOpenAIAccountScheduleResult(account, service.MuseImageModel, true, nil)
	if creating {
		id = task.ID()
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err = h.gatewayService.BindGrokMediaVideoRequestAccount(saveCtx, key.GroupID, id, subject.UserID, key.ID, account.ID)
		cancel()
		if err != nil {
			log.Error("muse_image.store_owner_failed", zap.String("task_id", id), zap.Error(err))
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Muse accepted the image task but its receipt could not be saved")
			return
		}
		c.Header("X-Muse-Task-ID", id)
		if endpoint == service.MuseImageCreate || req.Async || strings.HasSuffix(c.FullPath(), "/async") {
			c.JSON(http.StatusAccepted, task.TaskResponse(c, false))
			return
		}
		// Reuse the persisted receipt even when a synchronous connection times
		// out. An accepted generation is never replayed.
		wait := 250 * time.Second
		if req.Timeout > 0 {
			wait = time.Duration(req.Timeout+15) * time.Second
		}
		waitCtx, cancel := context.WithTimeout(ctx, wait)
		task, err = h.waitForMuseImage(waitCtx, c, account, id)
		cancel()
		if err != nil {
			if !c.Writer.Written() && ctx.Err() == nil {
				c.JSON(http.StatusAccepted, gin.H{"id": id, "task_id": id, "object": "image.task", "model": service.MuseImageModel, "status": "processing"})
			}
			return
		}
	}
	if task.Status == "completed" {
		if err := h.gatewayService.SettleMuseImage(ctx, &service.OpenAIRecordUsageInput{
			Result: task.UsageResult(), APIKey: key, User: key.User, Account: account,
			InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: EndpointImageTasks,
			UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c),
			RequestPayloadHash: service.HashUsageRequestPayload([]byte(id)),
			APIKeyService:      h.apiKeyService, QuotaPlatform: service.PlatformOpenAI,
			SessionID: service.ExtractClientSessionID(c),
		}); err != nil {
			log.Warn("muse_image.settlement_pending", zap.String("task_id", id), zap.Error(err))
			c.Header("Retry-After", "2")
			h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "Muse image settlement is pending; query the same task again")
			return
		}
	}
	switch {
	case endpoint == service.MuseImageContent:
		if task.Status != "completed" {
			h.errorResponse(c, http.StatusConflict, "invalid_request_error", "Image content is not ready")
			return
		}
		if err := h.gatewayService.WriteMuseImageContent(ctx, c, account, task); err != nil && !c.Writer.Written() {
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Muse image content is unavailable")
		}
	case !creating:
		c.JSON(http.StatusOK, task.TaskResponse(c, true))
	case task.Status == "completed":
		c.JSON(http.StatusOK, task.ImageResponse(c))
	default:
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Muse could not generate this image")
	}
}

func (h *OpenAIGatewayHandler) waitForMuseImage(ctx context.Context, c *gin.Context, account *service.Account, id string) (*service.MuseImageTask, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			task, err := h.gatewayService.ForwardMuseImage(ctx, c, account, id, "", nil)
			if err != nil || task.Status == "completed" || task.Status == "failed" {
				return task, err
			}
		}
	}
}
