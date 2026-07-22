package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

type responsesAttachmentPreparation struct {
	Body            []byte
	BudgetViolation *responsesAttachmentBudgetViolation
}

type responsesAttachmentBudgetViolation struct {
	Reason   string
	Observed int
	Limit    int
}

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
	maxImagesToInspect, maxColdEncodes := responsesAttachmentOptimizerLimits(experiment)
	optimizer, err := attachmentgateway.New(attachmentgateway.Config{
		Enabled:                           experiment.AttachmentOptimizerEnabled,
		RequestBudgetEnabled:              experiment.RequestBudgetEnabled,
		ThresholdBytes:                    experiment.ThresholdBytes,
		AggregateSmallImageEnabled:        experiment.AggregateSmallImageEnabled,
		AggregateSmallImageTriggerBytes:   experiment.AggregateSmallImageTriggerBytes,
		AggregateSmallImageTriggerCount:   experiment.AggregateSmallImageTriggerCount,
		AggregateSmallImageThresholdBytes: experiment.AggregateSmallImageThresholdBytes,
		MaxImageBytes:                     experiment.MaxImageBytes,
		MaxTotalImageBytes:                experiment.MaxTotalImageBytesPerRequest,
		MaxPixels:                         experiment.MaxPixels,
		Quality:                           experiment.Quality,
		SpecialQuality:                    experiment.SpecialQuality,
		MinSavingsRatio:                   experiment.MinSavingsRatio,
		CacheDir:                          experiment.CacheDir,
		CacheTTL:                          time.Duration(experiment.CacheTTLSeconds) * time.Second,
		CacheMaxBytes:                     experiment.CacheMaxBytes,
		CacheCleanupInterval:              time.Duration(experiment.CacheCleanupIntervalSeconds) * time.Second,
		NegativeCacheTTL:                  time.Duration(experiment.NegativeCacheTTLSeconds) * time.Second,
		NegativeCacheMaxEntries:           experiment.NegativeCacheMaxEntries,
		MaxImagesPerRequest:               maxImagesToInspect,
		MaxColdEncodesPerRequest:          maxColdEncodes,
		MaxConcurrentEncode:               experiment.MaxConcurrentEncodes,
	})
	if err != nil {
		// Config validation should normally catch this. Fail closed on the feature
		// itself so a malformed experiment config cannot disrupt Responses.
		logger.L().Warn("attachment_gateway.initialization_failed", zap.Error(err))
		return nil
	}
	return optimizer
}

func responsesAttachmentOptimizerLimits(experiment config.AttachmentGatewayConfig) (maxImagesToInspect, maxColdEncodes int) {
	maxImagesToInspect = experiment.MaxImagesPerRequest
	maxColdEncodes = experiment.MaxImagesPerRequest
	if experiment.URLRewriteEnabled && experiment.URLRewriteMaxImagesPerRequest > maxImagesToInspect {
		// URL externalization may intentionally cover more accumulated images
		// than one request is allowed to cold-encode. Inspect the same range so
		// positive and negative compression-cache hits remain reusable.
		maxImagesToInspect = experiment.URLRewriteMaxImagesPerRequest
	}
	return maxImagesToInspect, maxColdEncodes
}

func newResponsesAttachmentURLExternalizer(
	cfg *config.Config,
	storage service.ImageStorage,
) responsesAttachmentURLExternalizer {
	if cfg == nil || !cfg.Gateway.AttachmentGateway.URLRewriteEnabled || storage == nil {
		return nil
	}
	experiment := cfg.Gateway.AttachmentGateway
	externalizer, err := attachmentgateway.NewURLExternalizer(attachmentgateway.URLConfig{
		Enabled:              experiment.URLRewriteEnabled,
		MinBodyBytes:         experiment.URLRewriteMinBodyBytes,
		ObjectPrefix:         experiment.URLObjectPrefix,
		URLCacheTTL:          time.Duration(experiment.URLCacheTTLSeconds) * time.Second,
		MaxImageBytes:        experiment.MaxImageBytes,
		MaxImagesPerRequest:  experiment.URLRewriteMaxImagesPerRequest,
		MaxConcurrentUploads: experiment.MaxConcurrentURLUploads,
	}, storage)
	if err != nil {
		logger.L().Warn("attachment_gateway.url_initialization_failed", zap.Error(err))
		return nil
	}
	return externalizer
}

func (h *OpenAIGatewayHandler) optimizeResponsesAttachments(
	ctx context.Context,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	body []byte,
) []byte {
	return h.prepareResponsesAttachments(ctx, reqLog, apiKey, body).Body
}

func (h *OpenAIGatewayHandler) prepareResponsesAttachments(
	ctx context.Context,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	body []byte,
) responsesAttachmentPreparation {
	passthrough := responsesAttachmentPreparation{Body: body}
	if h == nil || h.attachmentOptimizer == nil || !h.attachmentOptimizer.Enabled() {
		return passthrough
	}
	experiment := h.attachmentGatewayConfig()
	if !responsesAttachmentOptimizerInScope(experiment, apiKey) || experiment.OptimizeTimeoutMilliseconds <= 0 {
		return passthrough
	}
	rolloutMode := resolveAttachmentGatewayRolloutMode(experiment)
	if rolloutMode == attachmentGatewayRolloutOff {
		return passthrough
	}
	dryRun := rolloutMode == attachmentGatewayRolloutDryRun
	if experiment.RequestBudgetEnabled && experiment.RequestBudgetEnforce && rolloutMode == attachmentGatewayRolloutRewrite {
		inlineStats, inspectErr := attachmentgateway.InspectInlineAttachments(body)
		if inspectErr == nil {
			if budgetViolation := evaluateResponsesAttachmentBudgetPreflight(experiment, len(body), inlineStats); budgetViolation != nil {
				if reqLog == nil {
					reqLog = logger.L()
				}
				reqLog.Warn("openai.attachment_gateway_experiment",
					zap.Bool("experimental", true),
					zap.String("rollout_mode", string(rolloutMode)),
					zap.String("budget_stage", "preflight"),
					zap.Bool("payload_rewritten", false),
					zap.Int("original_body_bytes", len(body)),
					zap.Int("forward_body_bytes", 0),
					zap.Bool("request_budget_enabled", true),
					zap.Bool("request_budget_enforce", true),
					zap.Int("original_inline_attachment_count", inlineStats.Count),
					zap.Int("original_inline_attachment_bytes", inlineStats.Bytes),
					zap.Int("original_unsupported_attachment_count", inlineStats.UnsupportedCount),
					zap.Bool("budget_would_reject", true),
					zap.Bool("budget_enforced", true),
					zap.String("budget_reason", budgetViolation.Reason),
					zap.Int("budget_observed", budgetViolation.Observed),
					zap.Int("budget_limit", budgetViolation.Limit),
				)
				return responsesAttachmentPreparation{Body: body, BudgetViolation: budgetViolation}
			}
		}
	}

	optimizeCtx, cancel := context.WithTimeout(ctx, time.Duration(experiment.OptimizeTimeoutMilliseconds)*time.Millisecond)
	defer cancel()
	result := h.attachmentOptimizer.Optimize(optimizeCtx, body)
	metrics := result.Metrics
	contextErr := optimizeCtx.Err()
	urlMetrics := attachmentgateway.URLMetrics{}
	if !dryRun && contextErr == nil && experiment.URLRewriteEnabled && h.attachmentURLExternalizer != nil && h.attachmentURLExternalizer.Enabled() {
		uploadCtx, uploadCancel := context.WithTimeout(ctx, time.Duration(experiment.URLUploadTimeoutMilliseconds)*time.Millisecond)
		externalized := h.attachmentURLExternalizer.Externalize(uploadCtx, result.Body)
		uploadContextErr := uploadCtx.Err()
		uploadCancel()
		urlMetrics = externalized.Metrics
		result.Body = externalized.Body
		metrics.OptimizedBodyBytes = len(externalized.Body)
		if experiment.RequestBudgetEnabled || experiment.AggregateSmallImageEnabled {
			if inlineStats, inspectErr := attachmentgateway.InspectInlineAttachments(externalized.Body); inspectErr == nil {
				metrics.CandidateInlineAttachmentCount = inlineStats.Count
				metrics.CandidateInlineAttachmentBytes = inlineStats.Bytes
				metrics.CandidateUnsupportedAttachmentCount = inlineStats.UnsupportedCount
			} else {
				urlMetrics.Errors++
			}
		}
		if errors.Is(uploadContextErr, context.DeadlineExceeded) {
			urlMetrics.TimedOut = true
		}
	}
	timedOut := metrics.TimedOut || errors.Is(contextErr, context.DeadlineExceeded) || urlMetrics.TimedOut
	budgetViolation := evaluateResponsesAttachmentBudget(experiment, metrics)
	budgetEnforced := budgetViolation != nil && experiment.RequestBudgetEnforce && rolloutMode == attachmentGatewayRolloutRewrite
	forwardBody := result.Body
	if dryRun || contextErr != nil || budgetEnforced {
		forwardBody = body
	}
	payloadRewritten := !budgetEnforced && !dryRun && contextErr == nil && !bytes.Equal(result.Body, body)
	fields := []zap.Field{
		zap.Bool("experimental", true),
		zap.String("rollout_mode", string(rolloutMode)),
		zap.Bool("dry_run", dryRun),
		zap.Bool("payload_rewritten", payloadRewritten),
		zap.Int("original_body_bytes", metrics.OriginalBodyBytes),
		zap.Int("optimized_body_bytes", metrics.OptimizedBodyBytes),
		zap.Int("forward_body_bytes", len(forwardBody)),
		zap.Int("image_count", metrics.ImageCount),
		zap.Int("optimized_image_count", metrics.OptimizedImageCount),
		zap.Int("original_image_bytes", metrics.OriginalImageBytes),
		zap.Int("optimized_image_bytes", metrics.OptimizedImageBytes),
		zap.Bool("cache_hit", metrics.CacheHit),
		zap.Int("cache_hits", metrics.CacheHits),
		zap.Int("cache_shared", metrics.CacheShared),
		zap.Bool("negative_cache_hit", metrics.NegativeCacheHit),
		zap.Int("negative_cache_hits", metrics.NegativeCacheHits),
		zap.Int("negative_cache_shared", metrics.NegativeCacheShared),
		zap.Int("cold_encode_count", metrics.ColdEncodeCount),
		zap.Bool("timed_out", timedOut),
		zap.Int("skipped_below_threshold", metrics.SkippedBelowThreshold),
		zap.Int("skipped_unsupported", metrics.SkippedUnsupported),
		zap.Int("skipped_not_smaller", metrics.SkippedNotSmaller),
		zap.Int("skipped_request_image_limit", metrics.SkippedRequestImageLimit),
		zap.Int("skipped_cold_encode_limit", metrics.SkippedColdEncodeLimit),
		zap.Int("skipped_total_image_bytes", metrics.SkippedTotalImageBytes),
		zap.Bool("aggregate_pressure", metrics.AggregatePressure),
		zap.Int("effective_threshold_bytes", metrics.EffectiveThresholdBytes),
		zap.Bool("request_budget_enabled", experiment.RequestBudgetEnabled),
		zap.Bool("request_budget_enforce", experiment.RequestBudgetEnforce),
		zap.Int("original_inline_attachment_count", metrics.OriginalInlineAttachmentCount),
		zap.Int("original_inline_attachment_bytes", metrics.OriginalInlineAttachmentBytes),
		zap.Int("original_unsupported_attachment_count", metrics.OriginalUnsupportedAttachmentCount),
		zap.Int("candidate_inline_attachment_count", metrics.CandidateInlineAttachmentCount),
		zap.Int("candidate_inline_attachment_bytes", metrics.CandidateInlineAttachmentBytes),
		zap.Int("candidate_unsupported_attachment_count", metrics.CandidateUnsupportedAttachmentCount),
		zap.Bool("budget_would_reject", budgetViolation != nil),
		zap.Bool("budget_enforced", budgetEnforced),
		zap.Float64("optimize_duration_ms", metrics.OptimizeDurationMS),
		zap.Bool("url_rewrite_enabled", experiment.URLRewriteEnabled),
		zap.Bool("url_storage_ready", urlMetrics.StorageReady),
		zap.Bool("url_storage_unavailable", urlMetrics.StorageUnavailable),
		zap.Int("url_externalized_count", urlMetrics.ExternalizedCount),
		zap.Int("url_upload_count", urlMetrics.UploadCount),
		zap.Int("url_cache_hits", urlMetrics.CacheHits),
		zap.Int("url_cache_shared", urlMetrics.CacheShared),
		zap.Bool("url_skipped_below_trigger", urlMetrics.SkippedBelowTrigger),
		zap.Bool("url_timed_out", urlMetrics.TimedOut),
		zap.Float64("url_duration_ms", urlMetrics.DurationMS),
		zap.Int("url_errors", urlMetrics.Errors),
		zap.Int("errors", metrics.Errors),
	}
	if budgetViolation != nil {
		fields = append(fields,
			zap.String("budget_reason", budgetViolation.Reason),
			zap.Int("budget_observed", budgetViolation.Observed),
			zap.Int("budget_limit", budgetViolation.Limit),
		)
	}
	if reqLog == nil {
		reqLog = logger.L()
	}
	if metrics.Errors > 0 || urlMetrics.Errors > 0 || timedOut || budgetViolation != nil {
		reqLog.Warn("openai.attachment_gateway_experiment", fields...)
	} else {
		reqLog.Info("openai.attachment_gateway_experiment", fields...)
	}
	if budgetEnforced {
		return responsesAttachmentPreparation{Body: body, BudgetViolation: budgetViolation}
	}
	return responsesAttachmentPreparation{Body: forwardBody}
}

func evaluateResponsesAttachmentBudgetPreflight(experiment config.AttachmentGatewayConfig, bodyBytes int, stats attachmentgateway.InlineAttachmentStats) *responsesAttachmentBudgetViolation {
	if !experiment.RequestBudgetEnabled || stats.Count == 0 {
		return nil
	}
	if stats.Count > experiment.MaxInlineAttachmentCount {
		return &responsesAttachmentBudgetViolation{
			Reason:   "inline_attachment_count",
			Observed: stats.Count,
			Limit:    experiment.MaxInlineAttachmentCount,
		}
	}
	// If there is no image the optimizer can transform, neither the inline
	// payload nor the body can shrink. Rejecting here avoids spending the
	// optimization timeout on PDF/audio/video or bare file payloads.
	if stats.OptimizableImageCount == 0 {
		if stats.Bytes > experiment.MaxInlineAttachmentBytes {
			return &responsesAttachmentBudgetViolation{
				Reason:   "inline_attachment_bytes",
				Observed: stats.Bytes,
				Limit:    experiment.MaxInlineAttachmentBytes,
			}
		}
		if bodyBytes > experiment.MaxForwardBodyBytes {
			return &responsesAttachmentBudgetViolation{
				Reason:   "forward_body_bytes",
				Observed: bodyBytes,
				Limit:    experiment.MaxForwardBodyBytes,
			}
		}
	}
	return nil
}

func evaluateResponsesAttachmentBudget(experiment config.AttachmentGatewayConfig, metrics attachmentgateway.Metrics) *responsesAttachmentBudgetViolation {
	if !experiment.RequestBudgetEnabled || metrics.CandidateInlineAttachmentCount == 0 {
		return nil
	}
	if metrics.CandidateInlineAttachmentCount > experiment.MaxInlineAttachmentCount {
		return &responsesAttachmentBudgetViolation{
			Reason:   "inline_attachment_count",
			Observed: metrics.CandidateInlineAttachmentCount,
			Limit:    experiment.MaxInlineAttachmentCount,
		}
	}
	if metrics.CandidateInlineAttachmentBytes > experiment.MaxInlineAttachmentBytes {
		return &responsesAttachmentBudgetViolation{
			Reason:   "inline_attachment_bytes",
			Observed: metrics.CandidateInlineAttachmentBytes,
			Limit:    experiment.MaxInlineAttachmentBytes,
		}
	}
	if metrics.OptimizedBodyBytes > experiment.MaxForwardBodyBytes {
		return &responsesAttachmentBudgetViolation{
			Reason:   "forward_body_bytes",
			Observed: metrics.OptimizedBodyBytes,
			Limit:    experiment.MaxForwardBodyBytes,
		}
	}
	return nil
}

func responsesAttachmentBudgetMessage(violation *responsesAttachmentBudgetViolation) string {
	if violation == nil {
		return "Attachment request exceeds the configured budget."
	}
	switch violation.Reason {
	case "inline_attachment_count":
		return fmt.Sprintf("Too many inline attachments after optimization; limit is %d.", violation.Limit)
	case "inline_attachment_bytes":
		return fmt.Sprintf("Inline attachment payload remains too large after optimization; limit is %s.", formatBodyLimit(int64(violation.Limit)))
	case "forward_body_bytes":
		return fmt.Sprintf("Request body remains too large after attachment optimization; limit is %s.", formatBodyLimit(int64(violation.Limit)))
	default:
		return "Attachment request exceeds the configured budget."
	}
}

func (h *OpenAIGatewayHandler) openAIResponsesMaxForwardBodyBytes() int64 {
	if h == nil || h.cfg == nil {
		return 0
	}
	return h.cfg.Gateway.OpenAIResponsesMaxForwardBodySize
}

func openAIResponsesForwardBodyLimitMessage(_ int64) string {
	return "当前请求体过大，请新建对话后继续执行任务。"
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
