package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAttachmentGatewayDefaultsAreSafeAndDisabled(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)

	attachment := cfg.Gateway.AttachmentGateway
	require.False(t, attachment.AttachmentOptimizerEnabled)
	require.True(t, attachment.AttachmentOptimizerDryRun)
	require.False(t, attachment.URLRewriteEnabled)
	require.Equal(t, 512*1024, attachment.URLRewriteMinBodyBytes)
	require.Equal(t, 50, attachment.URLRewriteMaxImagesPerRequest)
	require.Equal(t, 60_000, attachment.URLUploadTimeoutMilliseconds)
	require.Equal(t, "attachments/", attachment.URLObjectPrefix)
	require.Equal(t, 15*60, attachment.URLCacheTTLSeconds)
	require.Equal(t, 2, attachment.MaxConcurrentURLUploads)
	require.False(t, attachment.RequestBudgetEnabled)
	require.False(t, attachment.RequestBudgetEnforce)
	require.Empty(t, attachment.RolloutControlFile)
	require.False(t, attachment.AllowUnscoped)
	require.Empty(t, attachment.AllowedAPIKeyIDs)
	require.Empty(t, attachment.AllowedUserIDs)
	require.Empty(t, attachment.AllowedGroupIDs)
	require.Equal(t, 120_000, attachment.OptimizeTimeoutMilliseconds)
	require.Equal(t, 512*1024, attachment.ThresholdBytes)
	require.False(t, attachment.AggregateSmallImageEnabled)
	require.Equal(t, 4*1024*1024, attachment.AggregateSmallImageTriggerBytes)
	require.Equal(t, 8, attachment.AggregateSmallImageTriggerCount)
	require.Equal(t, 128*1024, attachment.AggregateSmallImageThresholdBytes)
	require.Equal(t, 32, attachment.MaxInlineAttachmentCount)
	require.Equal(t, 12*1024*1024, attachment.MaxInlineAttachmentBytes)
	require.Equal(t, 14*1024*1024, attachment.MaxForwardBodyBytes)
	require.Equal(t, 100_000_000, attachment.MaxImageBytes)
	require.Equal(t, 100_000_000, attachment.MaxTotalImageBytesPerRequest)
	require.Equal(t, int64(50_000_000), attachment.MaxPixels)
	require.Equal(t, 85, attachment.Quality)
	require.Equal(t, 90, attachment.SpecialQuality)
	require.InDelta(t, 0.05, attachment.MinSavingsRatio, 0.000001)
	require.Equal(t, "data/attachment_cache", attachment.CacheDir)
	require.Equal(t, 7*24*60*60, attachment.CacheTTLSeconds)
	require.Equal(t, int64(512*1024*1024), attachment.CacheMaxBytes)
	require.Equal(t, 10*60, attachment.CacheCleanupIntervalSeconds)
	require.Equal(t, 24*60*60, attachment.NegativeCacheTTLSeconds)
	require.Equal(t, 10_000, attachment.NegativeCacheMaxEntries)
	require.Equal(t, 20, attachment.MaxImagesPerRequest)
	require.Equal(t, 2, attachment.MaxConcurrentEncodes)
}

func TestAttachmentGatewayURLRewriteUsesRuntimeStorageAndValidatesSafeValues(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)

	attachment := &cfg.Gateway.AttachmentGateway
	attachment.AttachmentOptimizerEnabled = true
	attachment.URLRewriteEnabled = true
	require.NoError(t, cfg.Validate())

	attachment.URLRewriteMaxImagesPerRequest = 0
	require.ErrorContains(t, cfg.Validate(), "url_rewrite_max_images_per_request")
	attachment.URLRewriteMaxImagesPerRequest = 50

	attachment.URLUploadTimeoutMilliseconds = 0
	require.ErrorContains(t, cfg.Validate(), "url_upload_timeout_ms")
}

func TestAttachmentGatewayValidationRejectsUnsafeValues(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)

	tests := []struct {
		name    string
		mutate  func(*AttachmentGatewayConfig)
		message string
	}{
		{name: "enforce without budget", mutate: func(c *AttachmentGatewayConfig) { c.RequestBudgetEnforce = true; c.RequestBudgetEnabled = false }, message: "request_budget_enforce"},
		{name: "zero timeout", mutate: func(c *AttachmentGatewayConfig) { c.OptimizeTimeoutMilliseconds = 0 }, message: "optimize_timeout_ms"},
		{name: "negative threshold", mutate: func(c *AttachmentGatewayConfig) { c.ThresholdBytes = -1 }, message: "threshold_bytes"},
		{name: "zero image limit", mutate: func(c *AttachmentGatewayConfig) { c.MaxImageBytes = 0 }, message: "max_image_bytes"},
		{name: "zero request image bytes", mutate: func(c *AttachmentGatewayConfig) { c.MaxTotalImageBytesPerRequest = 0 }, message: "max_total_image_bytes_per_request"},
		{name: "zero pixel limit", mutate: func(c *AttachmentGatewayConfig) { c.MaxPixels = 0 }, message: "max_pixels"},
		{name: "invalid quality", mutate: func(c *AttachmentGatewayConfig) { c.Quality = 101 }, message: "quality"},
		{name: "invalid savings", mutate: func(c *AttachmentGatewayConfig) { c.MinSavingsRatio = 1 }, message: "min_savings_ratio"},
		{name: "empty cache", mutate: func(c *AttachmentGatewayConfig) { c.CacheDir = "" }, message: "cache_dir"},
		{name: "zero ttl", mutate: func(c *AttachmentGatewayConfig) { c.CacheTTLSeconds = 0 }, message: "cache_ttl_seconds"},
		{name: "zero cache size", mutate: func(c *AttachmentGatewayConfig) { c.CacheMaxBytes = 0 }, message: "cache_max_bytes"},
		{name: "zero cleanup interval", mutate: func(c *AttachmentGatewayConfig) { c.CacheCleanupIntervalSeconds = 0 }, message: "cache_cleanup_interval_seconds"},
		{name: "zero negative cache ttl", mutate: func(c *AttachmentGatewayConfig) { c.NegativeCacheTTLSeconds = 0 }, message: "negative_cache_ttl_seconds"},
		{name: "zero negative cache entries", mutate: func(c *AttachmentGatewayConfig) { c.NegativeCacheMaxEntries = 0 }, message: "negative_cache_max_entries"},
		{name: "zero image count", mutate: func(c *AttachmentGatewayConfig) { c.MaxImagesPerRequest = 0 }, message: "max_images_per_request"},
		{name: "zero concurrency", mutate: func(c *AttachmentGatewayConfig) { c.MaxConcurrentEncodes = 0 }, message: "max_concurrent_encodes"},
		{name: "invalid API key scope", mutate: func(c *AttachmentGatewayConfig) { c.AllowedAPIKeyIDs = []int64{0} }, message: "allowed IDs"},
		{name: "invalid user scope", mutate: func(c *AttachmentGatewayConfig) { c.AllowedUserIDs = []int64{0} }, message: "allowed IDs"},
		{name: "invalid group scope", mutate: func(c *AttachmentGatewayConfig) { c.AllowedGroupIDs = []int64{-1} }, message: "allowed IDs"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			clone := *cfg
			clone.Gateway.AttachmentGateway.AttachmentOptimizerEnabled = true
			testCase.mutate(&clone.Gateway.AttachmentGateway)
			require.ErrorContains(t, clone.Validate(), testCase.message)
		})
	}
}

func TestAttachmentGatewayBudgetValidationRejectsUnsafeValues(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)

	tests := []struct {
		name    string
		mutate  func(*AttachmentGatewayConfig)
		message string
	}{
		{name: "zero aggregate trigger bytes", mutate: func(c *AttachmentGatewayConfig) {
			c.AggregateSmallImageEnabled = true
			c.AggregateSmallImageTriggerBytes = 0
		}, message: "aggregate_small_image_trigger_bytes"},
		{name: "zero aggregate trigger count", mutate: func(c *AttachmentGatewayConfig) {
			c.AggregateSmallImageEnabled = true
			c.AggregateSmallImageTriggerCount = 0
		}, message: "aggregate_small_image_trigger_count"},
		{name: "aggregate threshold above normal", mutate: func(c *AttachmentGatewayConfig) {
			c.AggregateSmallImageEnabled = true
			c.AggregateSmallImageThresholdBytes = c.ThresholdBytes + 1
		}, message: "aggregate_small_image_threshold_bytes"},
		{name: "zero attachment count", mutate: func(c *AttachmentGatewayConfig) { c.RequestBudgetEnabled = true; c.MaxInlineAttachmentCount = 0 }, message: "max_inline_attachment_count"},
		{name: "zero attachment bytes", mutate: func(c *AttachmentGatewayConfig) { c.RequestBudgetEnabled = true; c.MaxInlineAttachmentBytes = 0 }, message: "max_inline_attachment_bytes"},
		{name: "zero forward bytes", mutate: func(c *AttachmentGatewayConfig) { c.RequestBudgetEnabled = true; c.MaxForwardBodyBytes = 0 }, message: "max_forward_body_bytes"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			clone := *cfg
			clone.Gateway.AttachmentGateway.AttachmentOptimizerEnabled = true
			testCase.mutate(&clone.Gateway.AttachmentGateway)
			require.ErrorContains(t, clone.Validate(), testCase.message)
		})
	}
}

func TestAttachmentGatewayDormantValuesCannotBlockStartup(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)

	cfg.Gateway.AttachmentGateway.AttachmentOptimizerEnabled = false
	cfg.Gateway.AttachmentGateway.CacheDir = ""
	cfg.Gateway.AttachmentGateway.MaxImageBytes = -1

	require.NoError(t, cfg.Validate())
}
