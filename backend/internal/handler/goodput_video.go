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
	job      *service.DurableVideoJob
	account  *service.Account
	key      *service.APIKey
	subject  middleware.AuthSubject
	log      *zap.Logger
}

func (h *OpenAIGatewayHandler) forwardGoodPutVideo(ctx context.Context, c *gin.Context, opts goodPutVideoDispatch) {
	if opts.request != nil {
		task, err := h.gatewayService.SubmitGoodPutVideoJob(ctx, opts.key, opts.account, opts.request)
		if err != nil {
			h.writeGoodPutVideoError(c, opts.log, err)
			return
		}
		info := opts.request.Info()
		model := info.Model
		h.gatewayService.ReportOpenAIAccountScheduleResult(opts.account, model, true, nil)
		c.Header("Cache-Control", "private, no-store")
		h.gatewayService.BindGoodPutStudioPoster(ctx, opts.account, task)
		c.JSON(http.StatusAccepted, task.Public(model, task.BillableSeconds(model, info.DurationSeconds)))
		return
	}
	pending := opts.pending
	task, err := h.gatewayService.GetGoodPutVideoTask(ctx, opts.account, pending.UpstreamTaskID)
	if err != nil {
		h.writeGoodPutVideoError(c, opts.log, err)
		return
	}
	if task.ID() != opts.id {
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "GoodPut returned a mismatched video task")
		return
	}
	billedSeconds := task.BillableSeconds(pending.Model, pending.VideoDurationSeconds)
	if opts.job != nil && opts.job.State == "completed" {
		billedSeconds = opts.job.BilledSeconds
		if billedSeconds == 0 {
			// Before actual-usage settlement, completed rows required an exact match.
			billedSeconds = opts.job.Seconds
		}
	}
	if task.Completed() && opts.job == nil && billedSeconds > 0 {
		result := &service.OpenAIForwardResult{
			ResponseID: task.ID(), Model: pending.Model, BillingModel: pending.BillingModel, UpstreamModel: pending.UpstreamModel,
			VideoDurationSeconds: billedSeconds, VideoResolution: pending.VideoResolution, VideoCount: 1,
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
		err := h.gatewayService.WriteGoodPutVideoContent(ctx, c, opts.account, task)
		h.gatewayService.RecordGoodPutContentOutcome(ctx, opts.job, err != nil)
		if err != nil {
			h.writeGoodPutVideoError(c, opts.log, err)
		}
		return
	}
	h.gatewayService.BindGoodPutStudioPoster(ctx, opts.account, task)
	c.JSON(http.StatusOK, task.Public(pending.Model, billedSeconds))
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
