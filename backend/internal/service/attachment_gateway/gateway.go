package attachment_gateway

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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

type imageOptimizationJob struct {
	index   int
	image   dataURLImage
	release func()
}

type imageOptimizationOutcome struct {
	index        int
	optimization imageOptimizationResult
	err          error
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
	var coldEncodeCount atomic.Int64
	var admittedColdEncodeCount atomic.Int64
	var cacheLookupCount atomic.Int64
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
		result.Metrics.ColdEncodeCount = int(coldEncodeCount.Load())
		result.Metrics.CacheLookupCount = int(cacheLookupCount.Load())
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

	effectiveThreshold, inspectOK := g.prepareInlineAttachmentMetrics(body, &result.Metrics)
	if !inspectOK {
		return result
	}

	contextFailureRecorded := false
	reserveColdEncode := func() bool {
		limit := int64(g.config.MaxColdEncodesPerRequest)
		for {
			current := admittedColdEncodeCount.Load()
			if current >= limit {
				return false
			}
			if admittedColdEncodeCount.CompareAndSwap(current, current+1) {
				return true
			}
		}
	}
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
	tokens, truncated, rewriteErr := collectImageDataURLTokens(body, g.config.MaxImagesPerRequest)
	if rewriteErr != nil {
		result.Metrics.Errors++
		return result
	}
	if len(tokens) == 0 {
		return result
	}

	workerCount := g.config.MaxConcurrentEncode
	if workerCount > len(tokens) {
		workerCount = len(tokens)
	}
	jobs := make(chan imageOptimizationJob)
	outcomes := make(chan imageOptimizationOutcome, len(tokens))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range jobs {
				func() {
					defer job.release()
					optimization, optimizeErr := g.optimizeImageSafely(
						ctx,
						job.image,
						reserveColdEncode,
						func() { coldEncodeCount.Add(1) },
						func() { cacheLookupCount.Add(1) },
					)
					outcomes <- imageOptimizationOutcome{
						index:        job.index,
						optimization: optimization,
						err:          optimizeErr,
					}
				}()
			}
		}()
	}

	totalImageBytes := 0
	dispatchStopped := false
	for index, token := range tokens {
		if err := ctx.Err(); err != nil {
			recordContextFailure(err)
			dispatchStopped = true
			break
		}
		rawURL, tokenErr := imageURLTokenValue(body, token)
		if tokenErr != nil {
			result.Metrics.Errors++
			dispatchStopped = true
			break
		}
		result.Metrics.ImageCount++
		parsed, release, parseErr := g.parseImageDataURLWithSlot(ctx, rawURL)
		if parseErr != nil {
			if errors.Is(parseErr, errUnsupportedMediaType) {
				result.Metrics.SkippedUnsupported++
			} else if errors.Is(parseErr, context.DeadlineExceeded) || errors.Is(parseErr, context.Canceled) {
				recordContextFailure(parseErr)
				dispatchStopped = true
				break
			} else {
				result.Metrics.Errors++
			}
			release()
			continue
		}

		result.Metrics.OriginalImageBytes += len(parsed.Bytes)
		if len(parsed.Bytes) > g.config.MaxTotalImageBytes-totalImageBytes {
			result.Metrics.SkippedTotalImageBytes++
			release()
			continue
		}
		totalImageBytes += len(parsed.Bytes)
		if len(parsed.Bytes) < effectiveThreshold {
			result.Metrics.SkippedBelowThreshold++
			release()
			continue
		}

		select {
		case jobs <- imageOptimizationJob{index: index, image: parsed, release: release}:
		case <-ctx.Done():
			recordContextFailure(ctx.Err())
			release()
			dispatchStopped = true
		}
		if dispatchStopped {
			break
		}
	}
	close(jobs)
	workers.Wait()
	close(outcomes)

	rewritten := make([]imageURLRewrite, len(tokens))
	for outcome := range outcomes {
		optimization := outcome.optimization
		if optimization.NegativeCacheHit {
			result.Metrics.NegativeCacheHit = true
			result.Metrics.NegativeCacheHits++
		}
		if optimization.NegativeCacheShared {
			result.Metrics.NegativeCacheShared++
		}
		if outcome.err != nil {
			if errors.Is(outcome.err, errColdEncodeLimit) {
				result.Metrics.SkippedColdEncodeLimit++
			} else if errors.Is(outcome.err, errNotSmaller) {
				result.Metrics.SkippedNotSmaller++
			} else if errors.Is(outcome.err, errUnsupportedMediaType) || errors.Is(outcome.err, errAnimatedImage) {
				result.Metrics.SkippedUnsupported++
			} else if errors.Is(outcome.err, context.DeadlineExceeded) || errors.Is(outcome.err, context.Canceled) {
				recordContextFailure(outcome.err)
			} else {
				result.Metrics.Errors++
			}
			continue
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
		rewritten[outcome.index] = imageURLRewrite{
			value:   "data:image/webp;base64," + base64.StdEncoding.EncodeToString(optimization.Image.Bytes),
			changed: true,
		}
	}
	if truncated {
		// The bounded collector observed at least one more eligible image, but
		// intentionally did not retain it. Keep the historical metric shape
		// (the first skipped image is counted) without allocating per-image
		// state for an adversarially long request.
		result.Metrics.ImageCount++
		result.Metrics.SkippedRequestImageLimit++
	}
	if err := ctx.Err(); err != nil {
		recordContextFailure(err)
		result.Metrics.OptimizedImageCount = 0
		result.Metrics.OptimizedImageBytes = 0
		return result
	}

	optimizedBody, changed, rewriteErr := rewriteImageURLTokens(body, tokens, rewritten)
	if rewriteErr != nil {
		result.Metrics.Errors++
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

// RehydrateFromCache rebuilds a Responses body from completed cache entries or
// from encodes that were already in progress when it was called. Unlike
// Optimize, it never admits a new cold encode. It is intended for bounded
// cancellation recovery before URL externalization, so a disconnected client
// cannot create a second wave of raster work while already-admitted work can
// still become reusable.
func (g *Gateway) RehydrateFromCache(ctx context.Context, body []byte) (result Result) {
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
	effectiveThreshold, inspectOK := g.prepareInlineAttachmentMetrics(body, &result.Metrics)
	if !inspectOK {
		return result
	}

	tokens, truncated, rewriteErr := collectImageDataURLTokens(body, g.config.MaxImagesPerRequest)
	if rewriteErr != nil {
		result.Metrics.Errors++
		return result
	}
	if len(tokens) == 0 {
		return result
	}

	rewritten := make([]imageURLRewrite, len(tokens))
	rehydratedIndexes := make([]int, 0, len(tokens))
	totalImageBytes := 0
	for index, token := range tokens {
		if err := ctx.Err(); err != nil {
			recordRehydrateContextFailure(&result.Metrics, err)
			return result
		}
		rawURL, tokenErr := imageURLTokenValue(body, token)
		if tokenErr != nil {
			result.Metrics.Errors++
			return result
		}
		result.Metrics.ImageCount++
		parsed, release, parseErr := g.parseImageDataURLWithSlot(ctx, rawURL)
		if parseErr != nil {
			if errors.Is(parseErr, errUnsupportedMediaType) {
				result.Metrics.SkippedUnsupported++
			} else if errors.Is(parseErr, context.Canceled) || errors.Is(parseErr, context.DeadlineExceeded) {
				recordRehydrateContextFailure(&result.Metrics, parseErr)
				release()
				return result
			} else {
				result.Metrics.Errors++
			}
			release()
			continue
		}
		result.Metrics.OriginalImageBytes += len(parsed.Bytes)
		if len(parsed.Bytes) > g.config.MaxTotalImageBytes-totalImageBytes {
			result.Metrics.SkippedTotalImageBytes++
			release()
			continue
		}
		totalImageBytes += len(parsed.Bytes)
		if len(parsed.Bytes) < effectiveThreshold {
			result.Metrics.SkippedBelowThreshold++
			release()
			continue
		}
		hash := sourceHash(parsed.Bytes)
		release()

		lookup, found, shared, lookupErr := g.cache.loadOrJoin(ctx, hash)
		if lookupErr != nil {
			if errors.Is(lookupErr, context.Canceled) || errors.Is(lookupErr, context.DeadlineExceeded) {
				recordRehydrateContextFailure(&result.Metrics, lookupErr)
				return result
			}
			result.Metrics.Errors++
			continue
		}
		if !found {
			// No producer was present at the time the cancellation recovery began.
			// Do not start one here: this path is deliberately cache-only.
			continue
		}
		if lookup.Negative != nil {
			if lookup.Hit {
				result.Metrics.NegativeCacheHit = true
				result.Metrics.NegativeCacheHits++
			}
			if shared {
				result.Metrics.NegativeCacheShared++
			}
			continue
		}
		if lookup.Hit {
			result.Metrics.CacheHit = true
			result.Metrics.CacheHits++
		}
		if shared {
			result.Metrics.CacheShared++
		}
		result.Metrics.OptimizedImageCount++
		result.Metrics.OptimizedImageBytes += len(lookup.Image.Bytes)
		rewritten[index] = imageURLRewrite{
			value:   "data:image/webp;base64," + base64.StdEncoding.EncodeToString(lookup.Image.Bytes),
			changed: true,
		}
		rehydratedIndexes = append(rehydratedIndexes, index)
	}
	if truncated {
		result.Metrics.ImageCount++
		result.Metrics.SkippedRequestImageLimit++
	}
	if err := ctx.Err(); err != nil {
		recordRehydrateContextFailure(&result.Metrics, err)
		return result
	}
	optimizedBody, changed, rewriteErr := rewriteImageURLTokens(body, tokens, rewritten)
	if rewriteErr != nil {
		result.Metrics.Errors++
		return result
	}
	if !changed {
		return result
	}
	result.Body = optimizedBody
	result.Metrics.OptimizedBodyBytes = len(optimizedBody)
	result.RehydratedImageIndexes = rehydratedIndexes
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

func (g *Gateway) prepareInlineAttachmentMetrics(body []byte, metrics *Metrics) (int, bool) {
	effectiveThreshold := g.config.ThresholdBytes
	metrics.EffectiveThresholdBytes = effectiveThreshold
	if !g.config.RequestBudgetEnabled && !g.config.AggregateSmallImageEnabled {
		return effectiveThreshold, true
	}
	inlineStats, inspectErr := InspectInlineAttachments(body)
	if inspectErr != nil {
		metrics.Errors++
		return effectiveThreshold, false
	}
	metrics.OriginalInlineAttachmentCount = inlineStats.Count
	metrics.OriginalInlineAttachmentBytes = inlineStats.Bytes
	metrics.OriginalUnsupportedAttachmentCount = inlineStats.UnsupportedCount
	metrics.CandidateInlineAttachmentCount = inlineStats.Count
	metrics.CandidateInlineAttachmentBytes = inlineStats.Bytes
	metrics.CandidateUnsupportedAttachmentCount = inlineStats.UnsupportedCount
	if g.config.AggregateSmallImageEnabled &&
		(inlineStats.OptimizableImageBytes >= g.config.AggregateSmallImageTriggerBytes ||
			inlineStats.OptimizableImageCount >= g.config.AggregateSmallImageTriggerCount) {
		metrics.AggregatePressure = true
		effectiveThreshold = g.config.AggregateSmallImageThresholdBytes
		metrics.EffectiveThresholdBytes = effectiveThreshold
	}
	return effectiveThreshold, true
}

func recordRehydrateContextFailure(metrics *Metrics, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		metrics.TimedOut = true
		return
	}
	metrics.Errors++
}

func (g *Gateway) optimizeImageSafely(
	ctx context.Context,
	input dataURLImage,
	reserveColdEncode func() bool,
	recordColdEncodeStarted func(),
	recordCacheLookup func(),
) (optimization imageOptimizationResult, err error) {
	defer func() {
		if recover() != nil {
			optimization = imageOptimizationResult{}
			err = errors.New("attachment gateway: image transform panicked")
		}
	}()
	return g.optimizeImage(ctx, input, reserveColdEncode, recordColdEncodeStarted, recordCacheLookup)
}

// parseImageDataURLWithSlot keeps a transform slot from data URL decoding
// through the worker's raster transform. That bounds decoded-image memory
// across requests, including workers waiting behind a still-running encoder.
// Callers must check the per-request image limit before invoking it.
func (g *Gateway) parseImageDataURLWithSlot(ctx context.Context, rawURL string) (dataURLImage, func(), error) {
	select {
	case g.transformSlots <- struct{}{}:
	case <-ctx.Done():
		return dataURLImage{}, func() {}, ctx.Err()
	}
	released := false
	release := func() {
		if !released {
			released = true
			<-g.transformSlots
		}
	}
	parsed, _, parseErr := parseImageDataURL(rawURL, g.config.MaxImageBytes)
	if parseErr != nil {
		release()
		return parsed, func() {}, parseErr
	}
	return parsed, release, nil
}

func (g *Gateway) optimizeImage(
	ctx context.Context,
	input dataURLImage,
	reserveColdEncode func() bool,
	recordColdEncodeStarted func(),
	recordCacheLookup func(),
) (imageOptimizationResult, error) {
	hash := sourceHash(input.Bytes)
	if recordCacheLookup != nil {
		recordCacheLookup()
	}
	lookup, shared, err := g.cache.getOrCreate(ctx, hash, func() (created cacheLookup, createErr error) {
		workCtx, cancelWork := detachedWorkContext(ctx)
		defer cancelWork()
		defer func() {
			if recover() != nil {
				created = cacheLookup{}
				createErr = errors.New("attachment gateway: image transform panicked")
			}
		}()
		if reserveColdEncode == nil || !reserveColdEncode() {
			return cacheLookup{}, errColdEncodeLimit
		}
		// singleflight.DoChan runs create in its own goroutine. Admission still
		// follows the request context: a canceled request must not queue a new
		// decoded image behind existing work. Once admitted, workCtx lets the
		// bounded encoder finish and populate the reusable cache.
		select {
		case g.encodeSlots <- struct{}{}:
			defer func() { <-g.encodeSlots }()
			if recordColdEncodeStarted != nil {
				recordColdEncodeStarted()
			}
		case <-ctx.Done():
			return cacheLookup{}, ctx.Err()
		}
		decoded, width, height, err := decodeImage(input.Bytes, input.MIMEType, g.config.MaxPixels)
		if err != nil {
			return cacheLookup{}, err
		}
		if err := workCtx.Err(); err != nil {
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
		if err := workCtx.Err(); err != nil {
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
		"body=%d->%d images=%d optimized=%d cache_lookups=%d cold_encodes=%d cache_hits=%d negative_cache_hits=%d duration_ms=%.3f",
		m.OriginalBodyBytes,
		m.OptimizedBodyBytes,
		m.ImageCount,
		m.OptimizedImageCount,
		m.CacheLookupCount,
		m.ColdEncodeCount,
		m.CacheHits,
		m.NegativeCacheHits,
		m.OptimizeDurationMS,
	)
}
