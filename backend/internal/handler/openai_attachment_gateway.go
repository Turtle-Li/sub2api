package handler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	attachmentgateway "github.com/Wei-Shaw/sub2api/internal/service/attachment_gateway"
	"go.uber.org/zap"
)

type attachmentGatewayRolloutMode string

const (
	attachmentGatewayRolloutOff      attachmentGatewayRolloutMode = "off"
	attachmentGatewayRolloutDryRun   attachmentGatewayRolloutMode = "dry_run"
	attachmentGatewayRolloutRewrite  attachmentGatewayRolloutMode = "rewrite"
	attachmentGatewayControlMaxBytes                              = 64
)

func newResponsesAttachmentOptimizer(cfg *config.Config) responsesAttachmentOptimizer {
	if cfg == nil {
		return nil
	}
	experiment := cfg.Gateway.AttachmentGateway
	if !experiment.AttachmentOptimizerEnabled {
		// The leaf gate wins over every dormant tuning value. Constructing the
		// disabled zero-I/O gateway also avoids warning logs for unused settings.
		optimizer, _ := attachmentgateway.New(attachmentgateway.DefaultConfig())
		return optimizer
	}
	optimizer, err := attachmentgateway.New(attachmentgateway.Config{
		Enabled:              experiment.AttachmentOptimizerEnabled,
		ThresholdBytes:       experiment.ThresholdBytes,
		MaxImageBytes:        experiment.MaxImageBytes,
		MaxTotalImageBytes:   experiment.MaxTotalImageBytesPerRequest,
		MaxPixels:            experiment.MaxPixels,
		Quality:              experiment.Quality,
		SpecialQuality:       experiment.SpecialQuality,
		MinSavingsRatio:      experiment.MinSavingsRatio,
		CacheDir:             experiment.CacheDir,
		CacheTTL:             time.Duration(experiment.CacheTTLSeconds) * time.Second,
		CacheMaxBytes:        experiment.CacheMaxBytes,
		CacheCleanupInterval: time.Duration(experiment.CacheCleanupIntervalSeconds) * time.Second,
		MaxImagesPerRequest:  experiment.MaxImagesPerRequest,
		MaxConcurrentEncode:  experiment.MaxConcurrentEncodes,
	})
	if err != nil {
		// Config validation should normally catch this. Fail closed on the feature
		// itself so a malformed experiment config cannot disrupt Responses.
		logger.L().Warn("attachment_gateway.initialization_failed", zap.Error(err))
		return nil
	}
	return optimizer
}

func (h *OpenAIGatewayHandler) optimizeResponsesAttachments(
	ctx context.Context,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	body []byte,
) []byte {
	if h == nil || h.attachmentOptimizer == nil || !h.attachmentOptimizer.Enabled() {
		return body
	}
	experiment := h.attachmentGatewayConfig()
	if !responsesAttachmentOptimizerInScope(experiment, apiKey) || experiment.OptimizeTimeoutMilliseconds <= 0 {
		return body
	}
	rolloutMode := resolveAttachmentGatewayRolloutMode(experiment)
	if rolloutMode == attachmentGatewayRolloutOff {
		return body
	}
	dryRun := rolloutMode == attachmentGatewayRolloutDryRun

	optimizeCtx, cancel := context.WithTimeout(ctx, time.Duration(experiment.OptimizeTimeoutMilliseconds)*time.Millisecond)
	defer cancel()
	result := h.attachmentOptimizer.Optimize(optimizeCtx, body)
	metrics := result.Metrics
	contextErr := optimizeCtx.Err()
	timedOut := metrics.TimedOut || errors.Is(contextErr, context.DeadlineExceeded)
	payloadRewritten := !dryRun && contextErr == nil && !bytes.Equal(result.Body, body)
	fields := []zap.Field{
		zap.Bool("experimental", true),
		zap.String("rollout_mode", string(rolloutMode)),
		zap.Bool("dry_run", dryRun),
		zap.Bool("payload_rewritten", payloadRewritten),
		zap.Int("original_body_bytes", metrics.OriginalBodyBytes),
		zap.Int("optimized_body_bytes", metrics.OptimizedBodyBytes),
		zap.Int("forward_body_bytes", chooseAttachmentForwardBodyBytes(dryRun, contextErr, body, result.Body)),
		zap.Int("image_count", metrics.ImageCount),
		zap.Int("optimized_image_count", metrics.OptimizedImageCount),
		zap.Int("original_image_bytes", metrics.OriginalImageBytes),
		zap.Int("optimized_image_bytes", metrics.OptimizedImageBytes),
		zap.Bool("cache_hit", metrics.CacheHit),
		zap.Int("cache_hits", metrics.CacheHits),
		zap.Int("cache_shared", metrics.CacheShared),
		zap.Bool("timed_out", timedOut),
		zap.Int("skipped_below_threshold", metrics.SkippedBelowThreshold),
		zap.Int("skipped_unsupported", metrics.SkippedUnsupported),
		zap.Int("skipped_not_smaller", metrics.SkippedNotSmaller),
		zap.Int("skipped_request_image_limit", metrics.SkippedRequestImageLimit),
		zap.Int("skipped_total_image_bytes", metrics.SkippedTotalImageBytes),
		zap.Float64("optimize_duration_ms", metrics.OptimizeDurationMS),
		zap.Int("errors", metrics.Errors),
	}
	if reqLog == nil {
		reqLog = logger.L()
	}
	if metrics.Errors > 0 || timedOut {
		reqLog.Warn("openai.attachment_gateway_experiment", fields...)
	} else {
		reqLog.Info("openai.attachment_gateway_experiment", fields...)
	}
	if dryRun || contextErr != nil {
		return body
	}
	return result.Body
}

// resolveAttachmentGatewayRolloutMode keeps the static config behavior when no
// control file is configured. When a control file is configured, missing,
// oversized or invalid content fails closed to off. This makes dry-run,
// rewrite and emergency disable changes effective without recycling a live
// container or interrupting long-lived Responses streams.
func resolveAttachmentGatewayRolloutMode(experiment config.AttachmentGatewayConfig) attachmentGatewayRolloutMode {
	if !experiment.AttachmentOptimizerEnabled {
		return attachmentGatewayRolloutOff
	}
	controlFile := strings.TrimSpace(experiment.RolloutControlFile)
	if controlFile == "" {
		if experiment.AttachmentOptimizerDryRun {
			return attachmentGatewayRolloutDryRun
		}
		return attachmentGatewayRolloutRewrite
	}
	file, err := os.Open(controlFile)
	if err != nil {
		return attachmentGatewayRolloutOff
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, attachmentGatewayControlMaxBytes+1))
	if err != nil || len(content) > attachmentGatewayControlMaxBytes {
		return attachmentGatewayRolloutOff
	}
	switch attachmentGatewayRolloutMode(strings.TrimSpace(string(content))) {
	case attachmentGatewayRolloutDryRun:
		return attachmentGatewayRolloutDryRun
	case attachmentGatewayRolloutRewrite:
		return attachmentGatewayRolloutRewrite
	case attachmentGatewayRolloutOff:
		return attachmentGatewayRolloutOff
	default:
		return attachmentGatewayRolloutOff
	}
}

func (h *OpenAIGatewayHandler) attachmentGatewayConfig() config.AttachmentGatewayConfig {
	if h == nil || h.cfg == nil {
		return config.AttachmentGatewayConfig{}
	}
	return h.cfg.Gateway.AttachmentGateway
}

func responsesAttachmentOptimizerInScope(experiment config.AttachmentGatewayConfig, apiKey *service.APIKey) bool {
	if !experiment.AttachmentOptimizerEnabled {
		return false
	}
	if experiment.AllowUnscoped {
		return true
	}
	if apiKey == nil {
		return false
	}
	for _, allowedID := range experiment.AllowedAPIKeyIDs {
		if apiKey.ID == allowedID {
			return true
		}
	}
	for _, allowedID := range experiment.AllowedUserIDs {
		if apiKey.UserID == allowedID {
			return true
		}
	}
	groupID := int64(0)
	if apiKey.GroupID != nil {
		groupID = *apiKey.GroupID
	} else if apiKey.Group != nil {
		groupID = apiKey.Group.ID
	}
	for _, allowedID := range experiment.AllowedGroupIDs {
		if groupID == allowedID {
			return true
		}
	}
	return false
}

func chooseAttachmentForwardBodyBytes(dryRun bool, contextErr error, original, optimized []byte) int {
	if dryRun || contextErr != nil {
		return len(original)
	}
	return len(optimized)
}
