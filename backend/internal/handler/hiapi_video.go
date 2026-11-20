package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *OpenAIGatewayHandler) HiAPIVideoGeneration(c *gin.Context) {
	setActualUpstreamEndpoint(c, "/v1/tasks")
	h.handleGrokMedia(c, service.HiAPIVideoCreate, "")
}

func (h *OpenAIGatewayHandler) HiAPIVideoStatus(c *gin.Context) {
	h.handleGrokMedia(c, service.HiAPIVideoStatus, c.Param("request_id"))
}

func (h *OpenAIGatewayHandler) HiAPIVideoContent(c *gin.Context) {
	setActualUpstreamEndpoint(c, "/v1/tasks")
	h.handleGrokMedia(c, service.HiAPIVideoContent, c.Param("request_id"))
}

func (h *OpenAIGatewayHandler) forwardHiAPIVideo(ctx context.Context, c *gin.Context, endpoint service.GrokMediaEndpoint, key *service.APIKey, account *service.Account, request *service.HiAPIVideoRequest, job *service.DurableVideoJob, log *zap.Logger) {
	c.Header("Cache-Control", "private, no-store")
	var err error
	switch endpoint {
	case service.HiAPIVideoCreate:
		job, err = h.gatewayService.SubmitHiAPIVideoJob(ctx, key, account, request)
		if err == nil {
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, job.Model, true, nil)
			c.JSON(http.StatusAccepted, service.HiAPIVideoPublic(job))
			return
		}
	case service.HiAPIVideoStatus:
		c.JSON(http.StatusOK, service.HiAPIVideoPublic(job))
		return
	case service.HiAPIVideoContent:
		err = h.gatewayService.WriteHiAPIVideoContent(ctx, c, account, job)
		if err == nil {
			return
		}
	default:
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Unknown HiAPI video operation")
		return
	}
	if c.Writer.Written() {
		// A transport/copy error may carry a signed URL; never log it verbatim.
		log.Warn("hiapi_video.content_interrupted")
		return
	}
	var providerError *service.HiAPIVideoError
	if errors.As(err, &providerError) {
		h.errorResponse(c, providerError.Status, providerError.Code, providerError.Message)
		return
	}
	log.Warn("hiapi_video.request_failed", zap.Bool("create", endpoint == service.HiAPIVideoCreate))
	message := "HiAPI video is unavailable"
	if endpoint == service.HiAPIVideoCreate {
		message = "HiAPI submission outcome is unconfirmed; do not resubmit automatically"
	}
	h.errorResponse(c, http.StatusBadGateway, "upstream_error", message)
}
