package attachment_gateway

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// Gateway performs the Phase 1 request-local attachment rewrite. A Gateway is
// safe for concurrent use.
type Gateway struct {
	config         Config
	encoder        imageEncoder
	cache          *imageCache
	transformSlots chan struct{}
	encodeSlots    chan struct{}
}

type imageOptimizationResult struct {
	Image               optimizedImage
	CacheHit            bool
	CacheShared         bool
	NegativeCacheHit    bool
	NegativeCacheShared bool
}

// New creates an attachment gateway without touching the filesystem. Cache
// directories are created lazily only after the experiment is enabled and an
// eligible image produces either a useful optimization or a deterministic
// negative-cache decision.
func New(config Config) (*Gateway, error) {
	return newWithEncoder(config, libwebpEncoder{})
}

func newWithEncoder(config Config, encoder imageEncoder) (*Gateway, error) {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	if encoder == nil {
		return nil, errors.New("attachment gateway: image encoder is required")
	}
	policy := policyFingerprint(config, encoder.ID())
	return &Gateway{
		config:  config,
		encoder: encoder,
		cache: newImageCache(
			config.CacheDir,
			config.CacheTTL,
			config.CacheMaxBytes,
			config.CacheCleanupInterval,
			config.NegativeCacheTTL,
			config.NegativeCacheMaxEntries,
			policy,
			encoder.ID(),
		),
		transformSlots: make(chan struct{}, config.MaxConcurrentEncode),
		encodeSlots:    make(chan struct{}, config.MaxConcurrentEncode),
	}, nil
}

// Enabled reports the experiment gate without performing any parsing or I/O.
func (g *Gateway) Enabled() bool {
	return g != nil && g.config.Enabled
}

// Optimize returns the exact input byte slice when disabled or when no image
// can be safely and usefully optimized. Per-image failures are fail-open.
func (g *Gateway) Optimize(ctx context.Context, body []byte) (result Result) {
	started := time.Now()
	result = Result{
		Body: body,
		Metrics: Metrics{
			Enabled:            g.Enabled(),
			OriginalBodyBytes:  len(body),
			OptimizedBodyBytes: len(body),
		},
	}
	defer func() {
		result.Metrics.OptimizeDurationMS = float64(time.Since(started)) / float64(time.Millisecond)
	}()
	defer func() {
		if recover() != nil {
			result.Body = body
			result.Metrics.OptimizedBodyBytes = len(body)
			result.Metrics.OptimizedImageCount = 0
			result.Metrics.OptimizedImageBytes = 0
			result.Metrics.Errors++
		}
	}()
	if !g.Enabled() {
		return result
	}

	effectiveThreshold := g.config.ThresholdBytes
	result.Metrics.EffectiveThresholdBytes = effectiveThreshold
	if g.config.RequestBudgetEnabled || g.config.AggregateSmallImageEnabled {
		inlineStats, inspectErr := InspectInlineAttachments(body)
		if inspectErr != nil {
			result.Metrics.Errors++
			return result
		}
		result.Metrics.OriginalInlineAttachmentCount = inlineStats.Count
		result.Metrics.OriginalInlineAttachmentBytes = inlineStats.Bytes
		result.Metrics.OriginalUnsupportedAttachmentCount = inlineStats.UnsupportedCount
		result.Metrics.CandidateInlineAttachmentCount = inlineStats.Count
		result.Metrics.CandidateInlineAttachmentBytes = inlineStats.Bytes
		result.Metrics.CandidateUnsupportedAttachmentCount = inlineStats.UnsupportedCount
		if g.config.AggregateSmallImageEnabled &&
			(inlineStats.OptimizableImageBytes >= g.config.AggregateSmallImageTriggerBytes ||
				inlineStats.OptimizableImageCount >= g.config.AggregateSmallImageTriggerCount) {
			result.Metrics.AggregatePressure = true
			effectiveThreshold = g.config.AggregateSmallImageThresholdBytes
			result.Metrics.EffectiveThresholdBytes = effectiveThreshold
		}
	}

	contextFailureRecorded := false
	recordContextFailure := func(err error) {
		if contextFailureRecorded {
			return
		}
		contextFailureRecorded = true
		if errors.Is(err, context.DeadlineExceeded) {
			result.Metrics.TimedOut = true
			return
		}
		result.Metrics.Errors++
	}
	totalImageBytes := 0
	optimizedBody, changed, rewriteErr := rewriteImageURLs(body, func(rawURL string) string {
		if err := ctx.Err(); err != nil {
			recordContextFailure(err)
			return rawURL
		}
		if !isImageDataURL(rawURL) {
			return rawURL
		}
		result.Metrics.ImageCount++
		if result.Metrics.ImageCount > g.config.MaxImagesPerRequest {
			result.Metrics.SkippedRequestImageLimit++
			return rawURL
		}
		// Base64 decoding itself allocates decoded-image bytes, so acquire the
		// same bounded transform slot before parsing rather than only around the
		// raster decoder/encoder.
		select {
		case g.transformSlots <- struct{}{}:
			defer func() { <-g.transformSlots }()
		case <-ctx.Done():
			recordContextFailure(ctx.Err())
			return rawURL
		}
		parsed, _, err := parseImageDataURL(rawURL, g.config.MaxImageBytes)
		if err != nil {
			if errors.Is(err, errUnsupportedMediaType) {
				result.Metrics.SkippedUnsupported++
			} else {
				result.Metrics.Errors++
			}
			return rawURL
		}

		result.Metrics.OriginalImageBytes += len(parsed.Bytes)
		if len(parsed.Bytes) > g.config.MaxTotalImageBytes-totalImageBytes {
			result.Metrics.SkippedTotalImageBytes++
			return rawURL
		}
		totalImageBytes += len(parsed.Bytes)
		if len(parsed.Bytes) < effectiveThreshold {
			result.Metrics.SkippedBelowThreshold++
			return rawURL
		}

		optimization, optimizeErr := g.optimizeImage(ctx, parsed)
		if optimization.NegativeCacheHit {
			result.Metrics.NegativeCacheHit = true
			result.Metrics.NegativeCacheHits++
		}
		if optimization.NegativeCacheShared {
			result.Metrics.NegativeCacheShared++
		}
		if optimizeErr != nil {
			if errors.Is(optimizeErr, errNotSmaller) {
				result.Metrics.SkippedNotSmaller++
			} else if errors.Is(optimizeErr, errUnsupportedMediaType) || errors.Is(optimizeErr, errAnimatedImage) {
				result.Metrics.SkippedUnsupported++
			} else if errors.Is(optimizeErr, context.DeadlineExceeded) || errors.Is(optimizeErr, context.Canceled) {
				recordContextFailure(optimizeErr)
			} else {
				result.Metrics.Errors++
			}
			return rawURL
		}

		if optimization.CacheHit {
			result.Metrics.CacheHit = true
			result.Metrics.CacheHits++
		}
		if optimization.CacheShared {
			result.Metrics.CacheShared++
		}
		result.Metrics.OptimizedImageCount++
		result.Metrics.OptimizedImageBytes += len(optimization.Image.Bytes)
		return "data:image/webp;base64," + base64.StdEncoding.EncodeToString(optimization.Image.Bytes)
	})
	if rewriteErr != nil {
		result.Metrics.Errors++
		result.Metrics.OptimizedImageCount = 0
		result.Metrics.OptimizedImageBytes = 0
		return result
	}
	if err := ctx.Err(); err != nil {
		recordContextFailure(err)
		result.Metrics.OptimizedImageCount = 0
		result.Metrics.OptimizedImageBytes = 0
		return result
	}

	if !changed {
		return result
	}
	result.Body = optimizedBody
	result.Metrics.OptimizedBodyBytes = len(optimizedBody)
	if g.config.RequestBudgetEnabled || g.config.AggregateSmallImageEnabled {
		inlineStats, inspectErr := InspectInlineAttachments(optimizedBody)
		if inspectErr != nil {
			result.Body = body
			result.Metrics.OptimizedBodyBytes = len(body)
			result.Metrics.OptimizedImageCount = 0
			result.Metrics.OptimizedImageBytes = 0
			result.Metrics.Errors++
			return result
		}
		result.Metrics.CandidateInlineAttachmentCount = inlineStats.Count
		result.Metrics.CandidateInlineAttachmentBytes = inlineStats.Bytes
		result.Metrics.CandidateUnsupportedAttachmentCount = inlineStats.UnsupportedCount
	}
	return result
}

func (g *Gateway) optimizeImage(
	ctx context.Context,
	input dataURLImage,
) (imageOptimizationResult, error) {
	hash := sourceHash(input.Bytes)
	lookup, shared, err := g.cache.getOrCreate(ctx, hash, func() (created cacheLookup, createErr error) {
		defer func() {
			if recover() != nil {
				created = cacheLookup{}
				createErr = errors.New("attachment gateway: image transform panicked")
			}
		}()
		// singleflight.DoChan runs create in its own goroutine. If the request
		// context expires while a third-party encoder is already running, the
		// caller can fail open before that encoder returns. Hold a worker-owned
		// slot here so those non-cancellable background transforms remain bounded
		// even after the request-owned transform slot has been released.
		select {
		case g.encodeSlots <- struct{}{}:
			defer func() { <-g.encodeSlots }()
		case <-ctx.Done():
			return cacheLookup{}, ctx.Err()
		}
		decoded, width, height, err := decodeImage(input.Bytes, input.MIMEType, g.config.MaxPixels)
		if err != nil {
			return cacheLookup{}, err
		}
		if err := ctx.Err(); err != nil {
			return cacheLookup{}, err
		}
		policy := chooseImagePolicy(decoded, g.config)

		encoded, err := g.encoder.Encode(decoded, encodeOptions{
			Quality:  policy.Quality,
			Lossless: policy.Lossless,
		})
		if err != nil {
			return cacheLookup{}, err
		}
		if err := ctx.Err(); err != nil {
			return cacheLookup{}, err
		}
		minimumSavings := int(float64(len(input.Bytes)) * g.config.MinSavingsRatio)
		if len(encoded)+minimumSavings >= len(input.Bytes) {
			now := g.cache.now().UTC()
			negative := NegativeMetadata{
				OriginalHash:     hash,
				OriginalSize:     len(input.Bytes),
				OriginalMIMEType: input.MIMEType,
				CandidateSize:    len(encoded),
				Width:            width,
				Height:           height,
				Quality:          policy.Quality,
				Lossless:         policy.Lossless,
				Reason:           negativeCacheReasonNotSmaller,
				Policy:           g.cache.policy,
				Optimizer:        g.encoder.ID(),
				CreatedAt:        now,
				ExpiresAt:        now.Add(g.cache.negativeTTL),
			}
			return cacheLookup{Negative: &negative}, nil
		}

		now := g.cache.now().UTC()
		metadata := Metadata{
			OriginalHash:     hash,
			OptimizedHash:    optimizedHash(encoded),
			OriginalSize:     len(input.Bytes),
			OptimizedSize:    len(encoded),
			OriginalMIMEType: input.MIMEType,
			MIMEType:         "image/webp",
			Width:            width,
			Height:           height,
			Quality:          policy.Quality,
			Lossless:         policy.Lossless,
			Policy:           g.cache.policy,
			Optimizer:        g.encoder.ID(),
			CreatedAt:        now,
			ExpiresAt:        now.Add(g.cache.ttl),
		}
		return cacheLookup{Image: optimizedImage{Bytes: encoded, Metadata: metadata}}, nil
	})
	if err != nil {
		return imageOptimizationResult{}, err
	}
	if lookup.Negative != nil {
		return imageOptimizationResult{
			NegativeCacheHit:    lookup.Hit,
			NegativeCacheShared: shared,
		}, errNotSmaller
	}
	return imageOptimizationResult{
		Image:       lookup.Image,
		CacheHit:    lookup.Hit,
		CacheShared: shared,
	}, nil
}

func (m Metrics) String() string {
	return fmt.Sprintf(
		"body=%d->%d images=%d optimized=%d cache_hits=%d negative_cache_hits=%d duration_ms=%.3f",
		m.OriginalBodyBytes,
		m.OptimizedBodyBytes,
		m.ImageCount,
		m.OptimizedImageCount,
		m.CacheHits,
		m.NegativeCacheHits,
		m.OptimizeDurationMS,
	)
}
