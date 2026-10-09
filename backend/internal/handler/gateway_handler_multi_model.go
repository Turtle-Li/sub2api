package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Keep the complete native result for billing. The projection is only for the
// existing provider-handler failover, latency and usage submission lifecycle.
func wrapOpenAIForwardResult(result *service.OpenAIForwardResult) *service.ForwardResult {
	if result == nil {
		return nil
	}
	return &service.ForwardResult{
		OpenAIResult: result,
		RequestID:    result.RequestID, UpstreamHeaders: result.UpstreamHeaders,
		Model: result.Model, UpstreamModel: result.UpstreamModel,
		UpstreamResponseModel:         result.UpstreamResponseModel,
		UpstreamResponseModelConflict: result.UpstreamResponseModelConflict,
		UpstreamResponseServiceTier:   result.UpstreamResponseServiceTier,
		Usage: service.ClaudeUsage{
			InputTokens:              max(result.Usage.InputTokens-result.Usage.CacheReadInputTokens-result.Usage.CacheCreationInputTokens, 0),
			OutputTokens:             result.Usage.OutputTokens,
			CacheReadInputTokens:     result.Usage.CacheReadInputTokens,
			CacheCreationInputTokens: result.Usage.CacheCreationInputTokens,
			ImageOutputTokens:        result.Usage.ImageOutputTokens,
		},
		Stream: result.Stream, Duration: result.Duration, FirstTokenMs: result.FirstTokenMs,
		ClientDisconnect: result.ClientDisconnect,
		ReasoningEffort:  result.ReasoningEffort, RequestedReasoningEffort: result.RequestedReasoningEffort,
		ServiceTier: result.ServiceTier, ImageCount: result.ImageCount, ImageSize: result.ImageSize,
		ImageInputSize: result.ImageInputSize, ImageOutputSize: result.ImageOutputSize,
		ImageOutputSizes: result.ImageOutputSizes, ImageSizeSource: result.ImageSizeSource,
		ImageSizeBreakdown: result.ImageSizeBreakdown, SearchCount: result.SearchCount, AudioUsage: result.AudioUsage,
	}
}

func (h *GatewayHandler) forwardMultiModelMessages(ctx context.Context, c *gin.Context, account *service.Account, body []byte) (*service.ForwardResult, error) {
	result, err := h.openAIGatewayService.ForwardAsAnthropic(ctx, c, account, body, "", "")
	return wrapOpenAIForwardResult(result), err
}

func (h *GatewayHandler) recordAccountUsage(ctx context.Context, input *service.RecordUsageInput) error {
	if input == nil || input.Result == nil || input.Result.OpenAIResult == nil {
		return h.gatewayService.RecordUsage(ctx, input)
	}
	result := *input.Result.OpenAIResult
	result.RequestedReasoningEffort = input.Result.RequestedReasoningEffort
	result.ReasoningEffort = input.Result.ReasoningEffort
	if input.ForceCacheBilling {
		result.Usage.CacheReadInputTokens += max(result.Usage.InputTokens-result.Usage.CacheReadInputTokens-result.Usage.CacheCreationInputTokens, 0)
	}
	return h.openAIGatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
		Result: &result, APIKey: input.APIKey, User: input.User, Account: input.Account,
		Subscription: input.Subscription, PricingAt: input.PricingAt,
		InboundEndpoint: input.InboundEndpoint, UpstreamEndpoint: input.UpstreamEndpoint,
		UserAgent: input.UserAgent, IPAddress: input.IPAddress, SessionID: input.SessionID,
		RequestPayloadHash: input.RequestPayloadHash, APIKeyService: input.APIKeyService,
		QuotaPlatform: input.QuotaPlatform, ChannelUsageFields: input.ChannelUsageFields,
	})
}
