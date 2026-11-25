package attachment_gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func assertOptimizerParity(t testing.TB, want, got Metrics) {
	t.Helper()
	want.OptimizeDurationMS, got.OptimizeDurationMS = 0, 0
	require.Equal(t, want, got)
}

func assertURLParity(t testing.TB, want, got URLMetrics) {
	t.Helper()
	want.DurationMS, got.DurationMS = 0, 0
	require.Equal(t, want, got)
}

func TestRewritePlanWarmParity(t *testing.T) {
	for _, count := range []int{50, 250} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			g, e, store, body, expected := warmRewriteFixture(t, count, 129<<10, 8<<10)
			ctx := t.Context()
			baseline := g.Optimize(ctx, body)
			require.Equal(t, count, baseline.Metrics.CacheHits)
			reference := e.Externalize(ctx, baseline.Body)
			require.Equal(t, expected, reference.Body)
			warmURLReference := e.Externalize(ctx, baseline.Body)
			require.Equal(t, count, warmURLReference.Metrics.CacheHits)

			// Prime the source-token index using persisted, hash-verified hits.
			prime := g.PlanRewrite(body, e, true)
			assertOptimizerParity(t, baseline.Metrics, prime.Optimize(ctx))
			require.Equal(t, baseline.Body, prime.InlineBody())
			require.Equal(t, count, g.cache.warm.lru.Len())
			// Neither phase may wait for decode slots for verified warm hits.
			for range cap(g.transformSlots) {
				g.transformSlots <- struct{}{}
			}
			for range cap(e.parseSlots) {
				e.parseSlots <- struct{}{}
			}
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			plan := g.PlanRewrite(body, e, true)
			wantStats, err := InspectInlineAttachments(body)
			require.NoError(t, err)
			gotStats, err := plan.OriginalInlineStats()
			require.NoError(t, err)
			require.Equal(t, wantStats, gotStats)
			assertOptimizerParity(t, baseline.Metrics, plan.Optimize(bounded))
			actual := plan.Externalize(bounded, func() bool { return false })
			assertURLParity(t, warmURLReference.Metrics, actual.Metrics)
			require.Equal(t, expected, actual.Body)
			require.Equal(t, count, store.callCount())
		})
	}
}

func BenchmarkWarmAttachmentRewriteCombined(b *testing.B) {
	for _, count := range []int{50, 250} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			source, optimized := warmRewriteSizes(count)
			gateway, externalizer, store, body, expected := warmRewriteFixture(b, count, source, optimized)
			ctx := context.Background()
			rewrite := func() URLResult {
				plan := gateway.PlanRewrite(body, externalizer, true)
				if _, err := plan.OriginalInlineStats(); err != nil {
					b.Fatal(err)
				}
				plan.Optimize(ctx)
				result := plan.Externalize(ctx, nil)
				if _, err := InspectInlineAttachments(result.Body); err != nil {
					b.Fatal(err)
				}
				return result
			}
			require.Equal(b, expected, rewrite().Body)
			require.Equal(b, count, gateway.cache.warm.lru.Len())
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				result := rewrite()
				if result.Metrics.CacheHits != count {
					b.Fatal(result.Metrics)
				}
			}
			require.Equal(b, count, store.callCount())
		})
	}
}

type planFixtureEncoder struct{}

func (planFixtureEncoder) ID() string { return "rewrite-plan-fixture" }
func (planFixtureEncoder) Encode(image image.Image, _ encodeOptions) ([]byte, error) {
	r, _, _, _ := image.At(0, 0).RGBA()
	return planFixtureEncoding(byte(r >> 8)), nil
}

func planFixtureEncoding(seed byte) []byte {
	size := 25 << 10
	if seed == 2 {
		size = 512 << 10
	}
	return bytes.Repeat([]byte{seed}, size)
}

func planFixturePNG(t testing.TB, seed byte, size int) []byte {
	t.Helper()
	var original bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{R: seed, A: 255})
	require.NoError(t, png.Encode(&original, img))
	return append(original.Bytes(), make([]byte, size-original.Len())...)
}

func planFixtureBody(sources ...[]byte) []byte {
	var body strings.Builder
	_, _ = body.WriteString(`{ "input" : [`)
	for i, source := range sources {
		if i != 0 {
			_ = body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"type":"input_image","image_url":"data:image/png;base64,%s"}`, base64.StdEncoding.EncodeToString(source))
	}
	_, _ = body.WriteString(`], "digits":90904828492212310, "text":"x \u0069 y" } `)
	return []byte(body.String())
}

func planFixtureGateway(t *testing.T) *Gateway {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Enabled, cfg.RequestBudgetEnabled = true, true
	cfg.CacheDir = t.TempDir()
	cfg.ThresholdBytes = 128 << 10
	cfg.MaxColdEncodesPerRequest = 2
	cfg.MaxConcurrentEncode = 1
	g, err := newWithEncoder(cfg, planFixtureEncoder{})
	require.NoError(t, err)
	g.cache.lastCleanup = g.cache.now()
	return g
}

func TestRewritePlanUsesExactPostCompressionTrigger(t *testing.T) {
	g := planFixtureGateway(t)
	body := planFixtureBody(planFixturePNG(t, 1, 5<<20))
	store := &recordingObjectStorage{}
	e, err := NewURLExternalizer(URLConfig{Enabled: true, MinBodyBytes: 512 << 10}, store)
	require.NoError(t, err)
	baseline := g.Optimize(t.Context(), body)
	reference := e.Externalize(t.Context(), baseline.Body)
	require.Greater(t, len(body), e.config.MinBodyBytes)
	require.Less(t, len(baseline.Body), e.config.MinBodyBytes)
	require.True(t, reference.Metrics.SkippedBelowTrigger)
	warmReference := g.Optimize(t.Context(), body)
	for range 2 { // neither request has a published URL
		plan := g.PlanRewrite(body, e, false)
		assertOptimizerParity(t, warmReference.Metrics, plan.Optimize(t.Context()))
		result := plan.Externalize(t.Context(), nil)
		assertURLParity(t, reference.Metrics, result.Metrics)
		require.Equal(t, baseline.Body, result.Body)
	}
	require.Contains(t, string(baseline.Body), webpDataURLPrefix)
	require.Zero(t, store.callCount())
	e.config.MinBodyBytes = len(baseline.Body)
	plan := g.PlanRewrite(body, e, false)
	plan.Optimize(t.Context())
	result := plan.Externalize(t.Context(), nil)
	require.False(t, result.Metrics.SkippedBelowTrigger)
	require.Equal(t, 1, result.Metrics.ExternalizedCount)
}

type splitFailureStore struct{ failedKey string }

func (s splitFailureStore) Save(_ context.Context, key, _ string, data []byte) (string, error) {
	if strings.Contains(key, s.failedKey) {
		data[0] ^= 0xff // publication must not mutate the inline fallback
		return "", errors.New("test upload failure")
	}
	return "https://r2.example.test/" + key, nil
}

func TestRewritePlanMixedBatchParity(t *testing.T) {
	positive := planFixturePNG(t, 1, 160<<10)
	negative := planFixturePNG(t, 2, 160<<10)
	small := planFixturePNG(t, 3, 64<<10)
	coldSuccess := planFixturePNG(t, 4, 160<<10)
	coldFailure := planFixturePNG(t, 5, 160<<10)
	body := planFixtureBody(positive, negative, small, coldSuccess, coldFailure)
	setup := func() (*Gateway, *URLExternalizer) {
		g := planFixtureGateway(t)
		e, err := NewURLExternalizer(URLConfig{Enabled: true, MinBodyBytes: 1, MaxConcurrentUploads: 1},
			splitFailureStore{failedKey: optimizedHash(planFixtureEncoding(5))})
		require.NoError(t, err)
		prepared := g.Optimize(t.Context(), planFixtureBody(positive, negative))
		require.Equal(t, 1, prepared.Metrics.SkippedNotSmaller)
		plan := g.PlanRewrite(planFixtureBody(positive), e, false)
		require.Equal(t, 1, plan.Optimize(t.Context()).CacheHits)
		require.Equal(t, 1, plan.Externalize(t.Context(), nil).Metrics.UploadCount)
		return g, e
	}
	baselineGateway, baselineURL := setup()
	baseline := baselineGateway.Optimize(t.Context(), body)
	reference := baselineURL.Externalize(t.Context(), baseline.Body)
	g, e := setup()
	plan := g.PlanRewrite(body, e, true)
	metrics := plan.Optimize(t.Context())
	assertOptimizerParity(t, baseline.Metrics, metrics)
	require.Equal(t, 1, metrics.CacheHits)
	require.Equal(t, 1, metrics.NegativeCacheHits)
	require.Equal(t, 1, metrics.SkippedBelowThreshold)
	require.Equal(t, 2, metrics.ColdEncodeCount)
	result := plan.Externalize(t.Context(), nil)
	assertURLParity(t, reference.Metrics, result.Metrics)
	require.Equal(t, reference.Body, result.Body)
	require.Equal(t, 4, result.Metrics.ExternalizedCount)
	require.Equal(t, 1, result.Metrics.Errors)
	require.Contains(t, string(result.Body), webpDataURLPrefix+base64.StdEncoding.EncodeToString(planFixtureEncoding(5)))
}

func TestRewritePlanWarmIndexInvalidatesOnDiskChange(t *testing.T) {
	g, e, _, body, _ := warmRewriteFixture(t, 1, 160<<10, 25<<10)
	plan := g.PlanRewrite(body, e, false)
	require.Equal(t, 1, plan.Optimize(t.Context()).CacheHits)
	require.Equal(t, 1, g.cache.warm.lru.Len())
	metadata := plan.images[0].Metadata
	imagePath, _ := g.cache.paths(metadata.OriginalHash)
	info, err := os.Stat(imagePath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(imagePath, bytes.Repeat([]byte{'!'}, metadata.OptimizedSize), 0o600))
	require.NoError(t, os.Chtimes(imagePath, info.ModTime(), info.ModTime()))
	// The admitted request still owns its verified candidate; a new request
	// must not trust the stale entry even though size and mtime match.
	require.Contains(t, string(plan.InlineBody()), webpDataURLPrefix)
	next := g.PlanRewrite(body, e, false)
	require.Zero(t, next.Optimize(t.Context()).CacheHits)
	require.Equal(t, body, next.InlineBody())
	require.Zero(t, g.cache.warm.lru.Len())
}

type switchingPlanStore struct {
	version atomic.Uint64
	calls   atomic.Int32
}

func (s *switchingPlanStore) CacheVersion() uint64 { return s.version.Load() }
func (s *switchingPlanStore) Save(_ context.Context, key, _ string, _ []byte) (string, error) {
	if s.calls.Add(1) == 2 {
		s.version.Add(1)
	}
	return "https://r2.example.test/" + key, nil
}

func TestRewritePlanStorageVersionChangeUsesEntireInlineCandidate(t *testing.T) {
	g := planFixtureGateway(t)
	first, second := planFixturePNG(t, 1, 160<<10), planFixturePNG(t, 4, 160<<10)
	store := &switchingPlanStore{}
	e, err := NewURLExternalizer(URLConfig{Enabled: true, MinBodyBytes: 1, MaxConcurrentUploads: 1}, store)
	require.NoError(t, err)
	firstBody := planFixtureBody(first)
	compressed := g.Optimize(t.Context(), firstBody)
	require.Equal(t, 1, e.Externalize(t.Context(), compressed.Body).Metrics.UploadCount)
	prime := g.PlanRewrite(firstBody, e, false)
	require.Equal(t, 1, prime.Optimize(t.Context()).CacheHits)
	body := planFixtureBody(first, second)
	plan := g.PlanRewrite(body, e, false)
	require.Equal(t, 2, plan.Optimize(t.Context()).OptimizedImageCount)
	expected := plan.InlineBody()
	result := plan.Externalize(t.Context(), nil)
	require.Equal(t, expected, result.Body)
	require.Equal(t, 1, result.Metrics.CacheHits)
	require.Equal(t, 2, result.Metrics.Errors)
	require.NotContains(t, string(result.Body), "https://r2.example.test/")
}

func TestRewritePlanKeepsIndependentCapsAndDiscoverySemantics(t *testing.T) {
	g := planFixtureGateway(t)
	g.config.MaxImagesPerRequest = 1
	e, err := NewURLExternalizer(URLConfig{Enabled: true, MinBodyBytes: 1, MaxImagesPerRequest: 2}, &recordingObjectStorage{})
	require.NoError(t, err)
	source := planFixturePNG(t, 1, 160<<10)
	other := planFixturePNG(t, 4, 160<<10)
	first := base64.StdEncoding.EncodeToString(source)
	second := base64.StdEncoding.EncodeToString(other)
	body := []byte(fmt.Sprintf(`{"input":[
        {"type":"INPUT_IMAGE","image_url":"data:image/png;base64,%s"},
        {"type":"input_image","image_url":"data:image/png;base64,AA==","im\u0061ge_url":"d\u0061ta:IMAGE/PNG;BASE64,%s"},
        {"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}},
        {"type":"input_video","data":"bare","video_url":"data:video/mp4;base64,AA==","file_data":"bare file"}
    ]}`, first, first, second))
	wantStats := InlineAttachmentStats{Count: 6, Bytes: 3*len("data:image/png;base64,") + len(first) + len(second) +
		len("AA==") + len("bare") + len("data:video/mp4;base64,AA==") + len("bare file"),
		UnsupportedCount: 3, OptimizableImageCount: 3, OptimizableImageBytes: len(source) + len(other) + 1}
	// Warm the disk separately. The optimizer selects the last (escaped) URL,
	// whereas the inspector classifies the first duplicate URL and the
	// case-folded type as before. The URL cap is wider than the optimizer cap.
	prepared := g.Optimize(t.Context(), body)
	require.Equal(t, 1, prepared.Metrics.OptimizedImageCount)
	baseline := g.Optimize(t.Context(), body)
	_ = e.Externalize(t.Context(), baseline.Body)
	reference := e.Externalize(t.Context(), baseline.Body)
	plan := g.PlanRewrite(body, e, true)
	stats, inspectErr := plan.OriginalInlineStats()
	require.NoError(t, inspectErr)
	require.Equal(t, wantStats, stats)
	assertOptimizerParity(t, baseline.Metrics, plan.Optimize(t.Context()))
	result := plan.Externalize(t.Context(), nil)
	assertURLParity(t, reference.Metrics, result.Metrics)
	require.Equal(t, reference.Body, result.Body)
	require.Equal(t, 2, result.Metrics.ExternalizedCount)
}

func TestWarmIndexBoundsAndExpires(t *testing.T) {
	g, e, _, body, _ := warmRewriteFixture(t, 1, 160<<10, 25<<10)
	ctx := t.Context()
	plan := g.PlanRewrite(body, e, false)
	require.Equal(t, 1, plan.Optimize(ctx).CacheHits)
	entry, ok := g.cache.warm.lru.Front().Value.(warmImageEntry)
	require.True(t, ok)
	// Reuse a verified fixture's entry to exercise the index's accounting
	// without generating thousands of files; cap() accounts retained bytes.
	g.cache.maxBytes = int64(cap(entry.image.Bytes)) * 2
	for i := range 10 {
		entry.tokenHash[0] = byte(i)
		g.cache.rememberWarmImage(entry)
	}
	require.Equal(t, 2, g.cache.warm.lru.Len())
	require.LessOrEqual(t, g.cache.warm.bytes, g.cache.maxBytes)
	then := entry.image.Metadata.ExpiresAt
	g.cache.now = func() time.Time { return then }
	_, hit := g.cache.warmImage(entry.tokenHash)
	require.False(t, hit)
	require.Equal(t, 1, g.cache.warm.lru.Len())
}

func TestImageURLTokenFastValueAndMalformedString(t *testing.T) {
	require := require.New(t)
	for _, test := range []struct{ raw, want string }{
		{`"data:image/png;base64,AA=="`, "data:image/png;base64,AA=="},
		{`"d\u0061ta:image/png;base64,AA=="`, "data:image/png;base64,AA=="},
		{"\"\xff\"", "�"},
	} {
		got, err := imageURLTokenValue([]byte(test.raw), imageURLToken{end: len(test.raw)})
		require.NoError(err)
		require.Equal(test.want, got)
	}
	for _, malformed := range []string{"null", "\"a\nb\"", "\"a\"b\""} {
		_, err := imageURLTokenValue([]byte(malformed), imageURLToken{end: len(malformed)})
		require.Error(err)
	}
}

func TestRewritePlanMissingPublishedURLUsesVerifiedDiskPath(t *testing.T) {
	g, e, store, body, _ := warmRewriteFixture(t, 1, 160<<10, 25<<10)
	e.config.MinBodyBytes = 1
	plan := g.PlanRewrite(body, e, false)
	require.Equal(t, 1, plan.Optimize(t.Context()).CacheHits)
	require.Equal(t, 1, plan.Externalize(t.Context(), nil).Metrics.UploadCount)
	require.Equal(t, 1, g.cache.warm.lru.Len())
	// Only the signed URL expires, not the cached WebP. A warm index hit
	// cannot skip disk/transform admission when the URL must be republished.
	future := e.now().Add(e.config.URLCacheTTL + time.Second)
	e.now = func() time.Time { return future }
	for range cap(g.transformSlots) {
		g.transformSlots <- struct{}{}
	}
	short, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	retry := g.PlanRewrite(body, e, false)
	require.True(t, retry.Optimize(short).TimedOut)
	require.Equal(t, body, retry.InlineBody())
	require.Equal(t, 1, store.callCount())
	for range cap(g.transformSlots) {
		<-g.transformSlots
	}
	retry = g.PlanRewrite(body, e, false)
	require.Equal(t, 1, retry.Optimize(t.Context()).CacheHits)
	require.Equal(t, 1, retry.Externalize(t.Context(), nil).Metrics.UploadCount)
	require.Equal(t, 2, store.callCount())
}

func TestRewritePlanURLExpiresAfterWarmAdmission(t *testing.T) {
	g, e, store, body, expected := warmRewriteFixture(t, 1, 160<<10, 25<<10)
	e.config.MinBodyBytes = 1
	prime := g.PlanRewrite(body, e, false)
	require.Equal(t, 1, prime.Optimize(t.Context()).CacheHits)
	require.Equal(t, 1, prime.Externalize(t.Context(), nil).Metrics.UploadCount)

	// Match the sequential pipeline: once Optimize has admitted a verified
	// candidate, a concurrent disk LRU eviction and URL-cache expiry before
	// Externalize must still publish the request-owned verified WebP bytes
	// cleanly rather than failing or poisoning concurrent singleflight waiters.
	plan := g.PlanRewrite(body, e, false)
	require.Equal(t, 1, plan.Optimize(t.Context()).CacheHits)
	future := e.now().Add(e.config.URLCacheTTL + time.Second)
	e.now = func() time.Time { return future }
	imagePath, _ := g.cache.paths(plan.images[0].Metadata.OriginalHash)
	require.NoError(t, os.Remove(imagePath))

	result := plan.Externalize(t.Context(), nil)
	require.Equal(t, expected, result.Body)
	require.Zero(t, result.Metrics.Errors)
	require.Equal(t, 1, result.Metrics.ExternalizedCount)
	require.Equal(t, 1, result.Metrics.UploadCount)
	require.Equal(t, 2, store.callCount())

	// Subsequent requests observe the evicted disk file during Optimize and
	// invalidate the stale warm entry instead of trusting it.
	next := g.PlanRewrite(body, e, false)
	require.Zero(t, next.Optimize(t.Context()).CacheHits)
	require.Zero(t, g.cache.warm.lru.Len())
}

func TestRewritePlanSameRequestDuplicatesAndSkipLimitsParity(t *testing.T) {
	dup := planFixturePNG(t, 1, 160<<10)
	coldOK := planFixturePNG(t, 3, 160<<10)
	coldSkip := planFixturePNG(t, 4, 160<<10)
	totalSkip := planFixturePNG(t, 5, 220<<10)
	body := planFixtureBody(dup, dup, coldOK, coldSkip, totalSkip)

	newPair := func() (*Gateway, *URLExternalizer) {
		cfg := DefaultConfig()
		cfg.Enabled, cfg.RequestBudgetEnabled = true, true
		cfg.CacheDir = t.TempDir()
		cfg.ThresholdBytes = 128 << 10
		cfg.MaxColdEncodesPerRequest = 2
		cfg.MaxTotalImageBytes = 4*160<<10 + 1024 // admits first 4 images, skips 5th by total bytes
		cfg.MaxConcurrentEncode = 1
		g, err := newWithEncoder(cfg, planFixtureEncoder{})
		require.NoError(t, err)
		g.cache.lastCleanup = g.cache.now()
		e, err := NewURLExternalizer(URLConfig{
			Enabled:              true,
			MinBodyBytes:         1,
			MaxImagesPerRequest:  8,
			MaxConcurrentUploads: 2,
		}, &recordingObjectStorage{})
		require.NoError(t, err)
		return g, e
	}

	baseG, baseE := newPair()
	baseOpt := baseG.Optimize(t.Context(), body)
	baseExt := baseE.Externalize(t.Context(), baseOpt.Body)

	planG, planE := newPair()
	plan := planG.PlanRewrite(body, planE, true)
	planOpt := plan.Optimize(t.Context())
	require.Equal(t, 2, planOpt.ColdEncodeCount)
	require.Equal(t, 1, planOpt.SkippedColdEncodeLimit)
	require.Equal(t, 1, planOpt.SkippedTotalImageBytes)
	assertOptimizerParity(t, baseOpt.Metrics, planOpt)

	planExt := plan.Externalize(t.Context(), nil)
	require.Equal(t, baseExt.Body, planExt.Body)
	require.Equal(t, 5, planExt.Metrics.ExternalizedCount)
	require.Zero(t, planExt.Metrics.Errors)
	// Unencoded PNGs (cold-encode-skipped and total-bytes-skipped) still externalize under .png keys.
	require.Equal(t, 2, strings.Count(string(planExt.Body), ".png?signature=private"))
	require.Equal(t, 3, strings.Count(string(planExt.Body), ".webp?signature=private"))
}
