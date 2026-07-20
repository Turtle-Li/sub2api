package handler

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	openAIRetryProtectionRuntimeCacheTTL = 5 * time.Second
	openAIAbnormalRetryHashLogPrefixLen  = 12
)

type openAIAbnormalRetryRuntime struct {
	enabled                   bool
	congested                 bool
	triggerPct                float64
	rxUtilPct                 float64
	txUtilPct                 float64
	maxUtilPct                float64
	budgetBytes               int64
	fingerprintCandidateBytes int64
	minBodyBytes              int64
	window                    time.Duration
	maxRepeats                int
}

type openAIAbnormalRetryRuntimeCache struct {
	mu        sync.Mutex
	expiresAt time.Time
	runtime   openAIAbnormalRetryRuntime
}

type openAIAbnormalRetryEntry struct {
	count      int
	totalBytes int64
	expiresAt  time.Time
}

type openAIAbnormalRetryBucketStats struct {
	key             string
	count           int
	totalBytes      int64
	distinctHashes  int64
	highCardinality bool
}

type openAIAbnormalRetryRegisterResult struct {
	entry         openAIAbnormalRetryEntry
	bucket        openAIAbnormalRetryBucketStats
	stateStore    string
	redisFallback bool
	redisError    string
}

type openAIAbnormalRetryRequestMeta struct {
	contentLength   int64
	contentEncoding string
}

type openAIAbnormalRetryStore struct {
	mu          sync.Mutex
	entries     map[string]openAIAbnormalRetryEntry
	nextCleanup time.Time
}

var openAIAbnormalRetryProtection = &openAIAbnormalRetryStore{
	entries: make(map[string]openAIAbnormalRetryEntry),
}

func (c *openAIAbnormalRetryRuntimeCache) get(ctx context.Context, ops *service.OpsService) openAIAbnormalRetryRuntime {
	if ops == nil {
		return openAIAbnormalRetryRuntime{}
	}
	now := time.Now()
	c.mu.Lock()
	if now.Before(c.expiresAt) {
		runtime := c.runtime
		c.mu.Unlock()
		return runtime
	}
	c.mu.Unlock()

	runtime := loadOpenAIAbnormalRetryRuntime(ctx, ops)

	c.mu.Lock()
	c.runtime = runtime
	c.expiresAt = now.Add(openAIRetryProtectionRuntimeCacheTTL)
	c.mu.Unlock()
	return runtime
}

func loadOpenAIAbnormalRetryRuntime(ctx context.Context, ops *service.OpsService) openAIAbnormalRetryRuntime {
	if ctx == nil {
		ctx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()

	settings, err := ops.GetNetworkBandwidthSettings(dbCtx)
	if err != nil || settings == nil || !settings.AbnormalRetryProtectionEnabled || !settings.Enabled || !hasConfiguredBandwidthLimit(settings) {
		return openAIAbnormalRetryRuntime{}
	}

	runtime := openAIAbnormalRetryRuntime{
		enabled:      true,
		triggerPct:   settings.AbnormalRetryProtectionTriggerPercent,
		window:       time.Duration(settings.AbnormalRetryProtectionWindowSeconds) * time.Second,
		maxRepeats:   settings.AbnormalRetryProtectionMaxRepeats,
		minBodyBytes: settings.AbnormalRetryProtectionMinBodyBytes,
	}
	if runtime.window <= 0 {
		runtime.window = time.Minute
	}
	if runtime.maxRepeats <= 0 {
		runtime.maxRepeats = 1
	}
	runtime.budgetBytes = computeOpenAIAbnormalRetryBudgetBytes(settings, runtime.window, runtime.triggerPct)
	if runtime.budgetBytes <= 0 {
		return openAIAbnormalRetryRuntime{}
	}
	runtime.fingerprintCandidateBytes = computeOpenAIAbnormalRetryEffectiveCandidateBytes(
		runtime.budgetBytes,
		runtime.maxRepeats,
		runtime.minBodyBytes,
	)

	summary, err := ops.GetNetworkBandwidthSummary(dbCtx)
	if err != nil || summary == nil || !summary.Enabled {
		return runtime
	}
	applyOpenAIAbnormalRetryBandwidthSummary(&runtime, settings, summary)
	return runtime
}

func applyOpenAIAbnormalRetryBandwidthSummary(runtime *openAIAbnormalRetryRuntime, settings *service.OpsNetworkBandwidthSettings, summary *service.OpsNetworkBandwidthSummary) {
	if runtime == nil || settings == nil || summary == nil {
		return
	}
	var maxUtil float64
	var hasUtil bool
	if settings.RXLimitMbps != nil && *settings.RXLimitMbps > 0 {
		if summary.RXUtilizationPercent != nil {
			runtime.rxUtilPct = *summary.RXUtilizationPercent
		} else {
			runtime.rxUtilPct = summary.RXMbps / *settings.RXLimitMbps * 100
		}
		maxUtil = runtime.rxUtilPct
		hasUtil = true
	}
	if summary.TXUtilizationPercent != nil {
		runtime.txUtilPct = *summary.TXUtilizationPercent
	} else if settings.TXLimitMbps != nil && *settings.TXLimitMbps > 0 {
		runtime.txUtilPct = summary.TXMbps / *settings.TXLimitMbps * 100
	}
	if settings.TXLimitMbps != nil && *settings.TXLimitMbps > 0 {
		if !hasUtil || runtime.txUtilPct > maxUtil {
			maxUtil = runtime.txUtilPct
		}
		hasUtil = true
	}
	if hasUtil {
		runtime.maxUtilPct = maxUtil
		runtime.congested = maxUtil >= runtime.triggerPct
	}
}

func hasConfiguredBandwidthLimit(settings *service.OpsNetworkBandwidthSettings) bool {
	return settings != nil &&
		((settings.RXLimitMbps != nil && *settings.RXLimitMbps > 0) ||
			(settings.TXLimitMbps != nil && *settings.TXLimitMbps > 0))
}

func computeOpenAIAbnormalRetryBudgetBytes(settings *service.OpsNetworkBandwidthSettings, window time.Duration, triggerPct float64) int64 {
	if settings == nil || window <= 0 || triggerPct <= 0 {
		return 0
	}
	limitMbps := 0.0
	if settings.RXLimitMbps != nil && *settings.RXLimitMbps > 0 {
		limitMbps = *settings.RXLimitMbps
	}
	if settings.TXLimitMbps != nil && *settings.TXLimitMbps > 0 && (limitMbps <= 0 || *settings.TXLimitMbps < limitMbps) {
		limitMbps = *settings.TXLimitMbps
	}
	if limitMbps <= 0 {
		return 0
	}
	return int64(limitMbps * 1_000_000 * window.Seconds() * (triggerPct / 100) / 8)
}

func computeOpenAIAbnormalRetryFingerprintCandidateBytes(budgetBytes int64, maxRepeats int) int64 {
	if budgetBytes <= 0 {
		return 0
	}
	divider := int64(maxRepeats + 2)
	if divider <= 0 {
		divider = 2
	}
	return budgetBytes / divider
}

func computeOpenAIAbnormalRetryEffectiveCandidateBytes(budgetBytes int64, maxRepeats int, minBodyBytes int64) int64 {
	candidateBytes := computeOpenAIAbnormalRetryFingerprintCandidateBytes(budgetBytes, maxRepeats)
	if minBodyBytes > candidateBytes {
		return minBodyBytes
	}
	return candidateBytes
}

func shouldCandidateOpenAIAbnormalRetry(runtime openAIAbnormalRetryRuntime, bodyBytes int64) bool {
	return runtime.enabled && runtime.fingerprintCandidateBytes > 0 && bodyBytes >= runtime.fingerprintCandidateBytes
}

func shouldBlockOpenAIAbnormalRetry(runtime openAIAbnormalRetryRuntime, entry openAIAbnormalRetryEntry) bool {
	return runtime.congested && entry.count > runtime.maxRepeats && entry.totalBytes > runtime.budgetBytes
}

func openAIAbnormalRetryRequestMetaFromRequest(r *http.Request) openAIAbnormalRetryRequestMeta {
	if r == nil {
		return openAIAbnormalRetryRequestMeta{contentLength: -1}
	}
	return openAIAbnormalRetryRequestMeta{
		contentLength:   r.ContentLength,
		contentEncoding: strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))),
	}
}

func openAIAbnormalRetryBodySizeBucket(bodyBytes int64) string {
	if bodyBytes <= 0 {
		return "0"
	}
	mb := int64(1024 * 1024)
	bucketMB := int64(math.Ceil(float64(bodyBytes) / float64(mb)))
	if bucketMB <= 1 {
		return "le_1mb"
	}
	return fmt.Sprintf("le_%dmb", bucketMB)
}

func openAIAbnormalRetryHashPrefix(hash string) string {
	if len(hash) <= openAIAbnormalRetryHashLogPrefixLen {
		return hash
	}
	return hash[:openAIAbnormalRetryHashLogPrefixLen]
}

func openAIAbnormalRetryRequestFingerprint(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return fmt.Sprintf("xxh64-full:%d:%016x", len(body), xxhash.Sum64(body))
}

func (s *openAIAbnormalRetryStore) register(key string, bodyBytes int64, now time.Time, window time.Duration) openAIAbnormalRetryEntry {
	if key == "" || window <= 0 {
		return openAIAbnormalRetryEntry{count: 1, totalBytes: bodyBytes}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.nextCleanup.IsZero() || now.After(s.nextCleanup) {
		for k, entry := range s.entries {
			if !now.Before(entry.expiresAt) {
				delete(s.entries, k)
			}
		}
		s.nextCleanup = now.Add(window)
	}

	entry := s.entries[key]
	if now.Before(entry.expiresAt) {
		entry.count++
		entry.totalBytes += bodyBytes
	} else {
		entry = openAIAbnormalRetryEntry{count: 1, totalBytes: bodyBytes}
	}
	entry.expiresAt = now.Add(window)
	s.entries[key] = entry
	return entry
}

func registerOpenAIAbnormalRetryFingerprint(ctx context.Context, registrar service.OpenAIAbnormalRetryRegistrar, key string, bucketKey string, bodyHash string, bodyBytes int64, now time.Time, window time.Duration) openAIAbnormalRetryRegisterResult {
	if registrar != nil && key != "" && window > 0 {
		registration, err := registrar.Register(ctx, key, bucketKey, bodyHash, bodyBytes, window)
		if err == nil {
			return openAIAbnormalRetryRegisterResult{
				entry: openAIAbnormalRetryEntry{
					count:      int(registration.Count),
					totalBytes: registration.TotalBytes,
					expiresAt:  now.Add(window),
				},
				bucket: openAIAbnormalRetryBucketStats{
					key:             bucketKey,
					count:           int(registration.BucketCount),
					totalBytes:      registration.BucketBytes,
					distinctHashes:  registration.DistinctHashes,
					highCardinality: registration.DistinctHashes > registration.BucketCount/2 && registration.BucketCount >= 4,
				},
				stateStore: "redis",
			}
		}
		result := registerOpenAIAbnormalRetryMemoryFallback(key, bodyBytes, now, window)
		result.redisFallback = true
		result.redisError = err.Error()
		return result
	}
	return registerOpenAIAbnormalRetryMemoryFallback(key, bodyBytes, now, window)
}

func registerOpenAIAbnormalRetryMemoryFallback(key string, bodyBytes int64, now time.Time, window time.Duration) openAIAbnormalRetryRegisterResult {
	return openAIAbnormalRetryRegisterResult{
		entry:      openAIAbnormalRetryProtection.register(key, bodyBytes, now, window),
		stateStore: "memory",
	}
}

func (h *OpenAIGatewayHandler) enforceOpenAIAbnormalRetryProtection(c *gin.Context, body []byte, reqLog *zap.Logger, anthropicFormat bool, requestMeta openAIAbnormalRetryRequestMeta) bool {
	if h == nil || c == nil {
		return false
	}
	runtime := h.retryProtectionCache.get(c.Request.Context(), h.opsService)
	bodyBytes := int64(len(body))
	if !shouldCandidateOpenAIAbnormalRetry(runtime, bodyBytes) {
		return false
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		return false
	}
	bodyFingerprint := openAIAbnormalRetryRequestFingerprint(body)
	if bodyFingerprint == "" {
		return false
	}
	path := c.Request.URL.Path
	key := fmt.Sprintf("%d:%s:%s", apiKey.ID, path, bodyFingerprint)
	sizeBucket := openAIAbnormalRetryBodySizeBucket(bodyBytes)
	bucketKey := fmt.Sprintf("%d:%s:%s", apiKey.ID, path, sizeBucket)
	now := time.Now()
	result := registerOpenAIAbnormalRetryFingerprint(c.Request.Context(), h.retryProtectionRegistrar, key, bucketKey, bodyFingerprint, bodyBytes, now, runtime.window)
	entry := result.entry
	if reqLog != nil && result.bucket.highCardinality {
		reqLog.Warn("openai.abnormal_retry_protection_high_cardinality_observed",
			zap.Int64("api_key_id", apiKey.ID),
			zap.String("path", path),
			zap.Int("body_bytes", len(body)),
			zap.String("body_size_bucket", sizeBucket),
			zap.Int("bucket_count", result.bucket.count),
			zap.Int64("bucket_total_bytes", result.bucket.totalBytes),
			zap.Int64("bucket_distinct_hashes", result.bucket.distinctHashes),
			zap.String("state_store", result.stateStore),
			zap.Bool("redis_fallback", result.redisFallback),
		)
	}
	if !shouldBlockOpenAIAbnormalRetry(runtime, entry) {
		return false
	}

	retryAfter := int(runtime.window.Seconds())
	if retryAfter <= 0 {
		retryAfter = 60
	}
	if reqLog != nil {
		reqLog.Warn("openai.abnormal_retry_protection_blocked",
			zap.Int64("api_key_id", apiKey.ID),
			zap.String("path", path),
			zap.Int("body_bytes", len(body)),
			zap.Int64("fingerprint_candidate_bytes", runtime.fingerprintCandidateBytes),
			zap.Int64("min_body_bytes", runtime.minBodyBytes),
			zap.Int64("budget_bytes", runtime.budgetBytes),
			zap.Int("repeat_count", entry.count),
			zap.Int64("repeat_total_bytes", entry.totalBytes),
			zap.Int("max_repeats", runtime.maxRepeats),
			zap.String("state_store", result.stateStore),
			zap.Bool("redis_fallback", result.redisFallback),
			zap.String("redis_error", result.redisError),
			zap.String("body_fingerprint_prefix", openAIAbnormalRetryHashPrefix(bodyFingerprint)),
			zap.String("body_size_bucket", sizeBucket),
			zap.Int64("content_length", requestMeta.contentLength),
			zap.String("content_encoding", requestMeta.contentEncoding),
			zap.Bool("content_length_mismatch", requestMeta.contentLength >= 0 && requestMeta.contentEncoding == "" && requestMeta.contentLength != bodyBytes),
			zap.Int("bucket_count", result.bucket.count),
			zap.Int64("bucket_total_bytes", result.bucket.totalBytes),
			zap.Int64("bucket_distinct_hashes", result.bucket.distinctHashes),
			zap.Bool("bucket_high_cardinality", result.bucket.highCardinality),
			zap.Float64("rx_utilization_percent", runtime.rxUtilPct),
			zap.Float64("tx_utilization_percent", runtime.txUtilPct),
			zap.Float64("max_utilization_percent", runtime.maxUtilPct),
			zap.Float64("trigger_percent", runtime.triggerPct),
			zap.Int("retry_after_seconds", retryAfter),
		)
	}
	c.Header("Retry-After", strconv.Itoa(retryAfter))
	message := "Bandwidth protection is temporarily limiting repeated large identical requests. Please wait before retrying."
	if anthropicFormat {
		h.anthropicErrorResponse(c, http.StatusTooManyRequests, "rate_limit_error", message)
	} else {
		h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", message)
	}
	return true
}
