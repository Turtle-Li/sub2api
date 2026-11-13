package handler

import (
	"net/http"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *OpenAIGatewayHandler) MuseVideoGeneration(c *gin.Context) {
	setActualUpstreamEndpoint(c, EndpointVideos)
	h.handleGrokMedia(c, service.MuseVideoCreate, "")
}

func (h *OpenAIGatewayHandler) MuseVideoStatus(c *gin.Context) {
	setActualUpstreamEndpoint(c, EndpointVideos)
	h.handleGrokMedia(c, service.MuseVideoStatus, c.Param("request_id"))
}

func (h *OpenAIGatewayHandler) MuseVideoContent(c *gin.Context) {
	setActualUpstreamEndpoint(c, EndpointVideos)
	h.handleGrokMedia(c, service.MuseVideoContent, c.Param("request_id"))
}

func (h *OpenAIGatewayHandler) completeMuseVideoCreate(c *gin.Context, key *service.APIKey, subject middleware.AuthSubject, result *service.OpenAIForwardResult, accountID int64, startedAt string, log *zap.Logger) {
	ctx := c.Request.Context()
	id := result.ResponseID
	pending := service.GrokVideoPendingBilling{
		Model: result.Model, BillingModel: result.BillingModel, UpstreamModel: result.UpstreamModel,
		VideoResolution: result.VideoResolution, VideoDurationSeconds: result.VideoDurationSeconds,
		OriginalModel: clientRequestedModel(c, result.Model), CreatedAt: startedAt,
	}
	if err := h.gatewayService.StoreGrokVideoPendingBilling(ctx, id, subject.UserID, key.ID, pending); err != nil {
		log.Error("muse_video.store_billing_failed", zap.Error(err), zap.String("task_id", id))
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Muse accepted the task but its receipt could not be saved")
		return
	}
	if err := h.gatewayService.BindGrokMediaVideoRequestAccount(ctx, key.GroupID, id, subject.UserID, key.ID, accountID); err != nil {
		log.Error("muse_video.store_owner_failed", zap.Error(err), zap.String("task_id", id))
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Muse accepted the task but its receipt could not be saved")
		return
	}
	service.WriteMuseVideoReceipt(c, id)
}
