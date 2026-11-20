package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *OpenAIGatewayHandler) GoodPutVideoGeneration(c *gin.Context) {
	setActualUpstreamEndpoint(c, EndpointVideos)
	h.handleGrokMedia(c, service.GoodPutVideoCreate, "")
}

func (h *OpenAIGatewayHandler) GoodPutVideoStatus(c *gin.Context) {
	setActualUpstreamEndpoint(c, EndpointVideos)
	h.handleGrokMedia(c, service.GoodPutVideoStatus, c.Param("request_id"))
}

func (h *OpenAIGatewayHandler) GoodPutVideoContent(c *gin.Context) {
	setActualUpstreamEndpoint(c, EndpointVideos)
	h.handleGrokMedia(c, service.GoodPutVideoContent, c.Param("request_id"))
}

type goodPutVideoDispatch struct {
	endpoint service.GrokMediaEndpoint
	id       string
	request  *service.GoodPutVideoRequest
	pending  *service.GrokVideoPendingBilling
	account  *service.Account
	key      *service.APIKey
	subject  middleware.AuthSubject
	log      *zap.Logger
}

func (h *OpenAIGatewayHandler) forwardGoodPutVideo(ctx context.Context, c *gin.Context, opts goodPutVideoDispatch) {
	nativeID := ""
	if opts.pending != nil {
		nativeID = opts.pending.UpstreamTaskID
	}
	task, err := h.gatewayService.ForwardGoodPutVideo(ctx, opts.account, opts.request, nativeID)
	if err != nil {
		h.writeGoodPutVideoError(c, opts.log, err)
		return
	}
	if opts.request == nil && task.ID() != opts.id {
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "GoodPut returned a mismatched video task")
		return
	}
	pending := opts.pending
	if opts.request != nil {
		info := opts.request.Info()
		pending = &service.GrokVideoPendingBilling{
			Model: info.Model, BillingModel: info.Model, UpstreamModel: info.Model, OriginalModel: info.Model,
			VideoResolution: info.Resolution, VideoDurationSeconds: info.DurationSeconds,
			CreatedAt: service.GrokVideoPendingCreatedAtNow(), UpstreamTaskID: task.NativeID(),
		}
		if err := h.storeGoodPutVideoTask(ctx, opts, task.ID(), *pending); err != nil {
			h.writeGoodPutVideoError(c, opts.log, err)
			return
		}
	}
	if task.Completed() {
		if task.Seconds() != pending.VideoDurationSeconds {
			opts.log.Warn("goodput_video.billing_duration_mismatch", zap.Int("reported_seconds", task.Seconds()), zap.Int("expected_seconds", pending.VideoDurationSeconds))
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "GoodPut reported an unexpected billable duration")
			return
		}
		result := &service.OpenAIForwardResult{
			ResponseID: task.ID(), Model: pending.Model, BillingModel: pending.BillingModel, UpstreamModel: pending.UpstreamModel,
			VideoDurationSeconds: task.Seconds(), VideoResolution: pending.VideoResolution, VideoCount: 1,
			Duration: service.GrokVideoE2EDuration(pending.CreatedAt, time.Now()),
		}
		if bill := prepareGrokVideoCompletionBilling(ctx, h, opts.log, opts.key, opts.subject, task.ID(), result); bill != nil {
			recordGrokMediaUsage(c, h, opts.log, opts.key, opts.subject, nil, opts.account, bill, bill.Model, nil, task.ID())
		}
	}
	h.gatewayService.ReportOpenAIAccountScheduleResult(opts.account, pending.Model, true, nil)
	c.Header("Cache-Control", "private, no-store")
	if opts.endpoint == service.GoodPutVideoContent {
		if !task.Completed() {
			h.errorResponse(c, http.StatusConflict, "video_not_ready", "The GoodPut video is not available for download")
			return
		}
		if err := h.gatewayService.WriteGoodPutVideoContent(ctx, c, opts.account, task); err != nil {
			h.writeGoodPutVideoError(c, opts.log, err)
		}
		return
	}
	status := http.StatusOK
	if opts.endpoint == service.GoodPutVideoCreate {
		status = http.StatusAccepted
	}
	c.JSON(status, task.Public(pending.Model))
}

func (h *OpenAIGatewayHandler) storeGoodPutVideoTask(ctx context.Context, opts goodPutVideoDispatch, taskID string, pending service.GrokVideoPendingBilling) error {
	// The request can go away after the upstream accepts. Keep the receipt write bounded.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := h.gatewayService.StoreGrokVideoPendingBilling(ctx, taskID, opts.subject.UserID, opts.key.ID, pending); err != nil {
		return &service.GoodPutVideoError{Status: 502, Code: "upstream_error", Message: "GoodPut accepted the task but its receipt could not be saved; do not resubmit automatically"}
	}
	if err := h.gatewayService.BindGrokMediaVideoRequestAccount(ctx, opts.key.GroupID, taskID, opts.subject.UserID, opts.key.ID, opts.account.ID); err != nil {
		return &service.GoodPutVideoError{Status: 502, Code: "upstream_error", Message: "GoodPut accepted the task but its owner binding could not be saved; do not resubmit automatically"}
	}
	return nil
}

func (h *OpenAIGatewayHandler) writeGoodPutVideoError(c *gin.Context, log *zap.Logger, err error) {
	log.Warn("goodput_video.request_failed", zap.Error(err))
	if c.Writer.Written() {
		return
	}
	var upstream *service.GoodPutVideoError
	if errors.As(err, &upstream) {
		h.errorResponse(c, upstream.Status, upstream.Code, upstream.Message)
		return
	}
	h.errorResponse(c, http.StatusBadGateway, "upstream_error", "GoodPut video upstream request failed")
}
