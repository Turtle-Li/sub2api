package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestMultiModelForwardResultPreservesNativeBilling(t *testing.T) {
	result := &service.OpenAIForwardResult{
		Model: "kimi", UpstreamModel: "cline-pass/kimi-k3", BillingModel: "kimi-k3",
		Usage:       service.OpenAIUsage{InputTokens: 100, CacheReadInputTokens: 30, CacheCreationInputTokens: 10, ImageInputTokens: 20, OutputTokens: 12},
		SearchCount: 2, ImageCount: 1, PartialOutput: true,
		UpstreamResponseModel: "kimi-k3", UpstreamEndpoint: "/v1/chat/completions",
	}
	wrapped := wrapOpenAIForwardResult(result)
	// Provider handlers display cache-exclusive input, while native billing
	// must retain the full result, including image/cache and partial usage.
	require.Equal(t, 60, wrapped.Usage.InputTokens)
	require.Equal(t, 30, wrapped.Usage.CacheReadInputTokens)
	require.Equal(t, result.UpstreamModel, wrapped.UpstreamModel)
	require.Same(t, result, wrapped.OpenAIResult)
	require.Equal(t, 20, wrapped.OpenAIResult.Usage.ImageInputTokens)
	require.Equal(t, "kimi-k3", wrapped.OpenAIResult.BillingModel)
	require.True(t, wrapped.OpenAIResult.PartialOutput)
	require.Nil(t, wrapOpenAIForwardResult(nil))
}
