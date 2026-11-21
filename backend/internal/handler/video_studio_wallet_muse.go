package handler

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) forwardStudioWalletMuse(c *gin.Context, ctx context.Context, endpoint service.GrokMediaEndpoint, id string, j *service.DurableVideoJob, key *service.APIKey, a *service.Account, info service.GrokMediaRequestInfo, body []byte) {
	if endpoint != service.MuseVideoCreate {
		_, r, err := h.gatewayService.ForwardMuseVideo(ctx, c, a, endpoint, id, nil)
		if err == nil {
			status, _ := r["status"].(string)
			h.gatewayService.ObserveMuseStudioJob(ctx, j, status)
		} else if !c.Writer.Written() && !service.IsResponseCommitted(c) {
			h.errorResponse(c, 502, "upstream_error", "Could not read Muse receipt")
		}
		return
	}
	r, err := h.gatewayService.SubmitMuseStudioJob(ctx, c, key, a, info, body)
	if err == nil {
		c.JSON(202, r)
		return
	}
	if c.Writer.Written() || service.IsResponseCommitted(c) {
		return
	}
	switch {
	case errors.Is(err, service.ErrVideoStudioWalletUnavailable):
		h.errorResponse(c, 503, "studio_wallet_unavailable", "Video wallet unavailable before submission")
	case errors.Is(err, service.ErrGoodPutVideoFunds):
		h.errorResponse(c, 402, "insufficient_balance", "Insufficient available wallet balance")
	case errors.Is(err, service.ErrGoodPutVideoLimit):
		h.errorResponse(c, 429, "video_wallet_limit", "A video is already awaiting settlement")
	case errors.Is(err, service.ErrGoodPutVideoGroup):
		h.errorResponse(c, 403, "group_unavailable", "Video wallet group unavailable")
	case errors.Is(err, service.ErrVideoStudioQuoteChanged):
		h.errorResponse(c, 409, "studio_quote_changed", "Confirm the updated video quote")
	default:
		h.errorResponse(c, 502, "studio_receipt_uncertain", "Could not confirm the Muse submission; do not submit again")
	}
}
