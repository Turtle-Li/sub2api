package attachment_gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	webp "github.com/gen2brain/webp"
	"github.com/stretchr/testify/require"
)

func TestDisabledIsExactNoopAndDoesNotCreateCache(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "not-created")
	config := DefaultConfig()
	config.CacheDir = cacheDir
	gateway, err := New(config)
	require.NoError(t, err)

	body := []byte("{ \"model\": \"gpt-test\", \"input\": \"unchanged\" }\n")
	result := gateway.Optimize(context.Background(), body)

	require.Equal(t, body, result.Body)
	require.False(t, result.Metrics.Enabled)
	require.Positive(t, result.Metrics.OptimizeDurationMS)
	_, statErr := os.Stat(cacheDir)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestOptimizesResponsesInputImageAndPreservesFields(t *testing.T) {
	source := makePhotoLikePNG(t, 720, 520, 11)
	gateway := newTestGateway(t, nil)
	body := makeResponsesPayload(t, []string{dataURL("image/png", source)}, 0)

	result := gateway.Optimize(context.Background(), body)

	require.Less(t, len(result.Body), len(body))
	require.Equal(t, 1, result.Metrics.ImageCount)
	require.Equal(t, 1, result.Metrics.OptimizedImageCount)
	require.Equal(t, len(source), result.Metrics.OriginalImageBytes)
	require.NotZero(t, result.Metrics.OptimizedImageBytes)

	var rewritten map[string]any
	require.NoError(t, json.Unmarshal(result.Body, &rewritten))
	input, ok := rewritten["input"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, input)
	message, ok := input[0].(map[string]any)
	require.True(t, ok)
	content, ok := message["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 2)
	part, ok := content[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "high", part["detail"])
	rewrittenURL, ok := part["image_url"].(string)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(rewrittenURL, "data:image/webp;base64,"))
	encoded := decodeDataURLForTest(t, rewrittenURL)
	decodedConfig, err := webp.DecodeConfig(bytes.NewReader(encoded))
	require.NoError(t, err)
	require.Equal(t, 720, decodedConfig.Width)
	require.Equal(t, 520, decodedConfig.Height)
}

func TestOptimizationPreservesLargeJSONNumbers(t *testing.T) {
	source := makePhotoLikePNG(t, 720, 520, 29)
	gateway := newTestGateway(t, nil)
	largeNumber := "9007199254740993123456789"
	body := []byte(`{"model":"gpt-test","large_id":` + largeNumber + `,"input":[{"role":"user","content":[{"type":"input_image","image_url":"` + dataURL("image/png", source) + `"}]}]}`)

	result := gateway.Optimize(context.Background(), body)

	require.Equal(t, 1, result.Metrics.OptimizedImageCount)
	require.Contains(t, string(result.Body), `"large_id":`+largeNumber)
}

func TestBelowThresholdIsExactNoop(t *testing.T) {
	source := makePhotoLikePNG(t, 180, 120, 7)
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = len(source) + 1
	gateway, err := New(config)
	require.NoError(t, err)
	body := makeResponsesPayload(t, []string{dataURL("image/png", source)}, 0)

	result := gateway.Optimize(context.Background(), body)

	require.Equal(t, body, result.Body)
	require.Equal(t, 1, result.Metrics.SkippedBelowThreshold)
	require.Zero(t, result.Metrics.OptimizedImageCount)
}

func TestAggregatePressureLowersThresholdForMediumSmallImages(t *testing.T) {
	sourceA := makePhotoLikePNG(t, 420, 300, 71)
	sourceB := makePhotoLikePNG(t, 420, 300, 72)
	config := DefaultConfig()
	config.Enabled = true
	config.RequestBudgetEnabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = max(len(sourceA), len(sourceB)) + 1
	config.AggregateSmallImageEnabled = true
	config.AggregateSmallImageTriggerBytes = 1 << 30
	config.AggregateSmallImageTriggerCount = 2
	config.AggregateSmallImageThresholdBytes = 1
	gateway, err := New(config)
	require.NoError(t, err)
	body := makeResponsesPayload(t, []string{
		dataURL("image/png", sourceA),
		dataURL("image/png", sourceB),
	}, 0)

	coldStarted := time.Now()
	result := gateway.Optimize(context.Background(), body)
	coldDuration := time.Since(coldStarted)
	warmStarted := time.Now()
	warm := gateway.Optimize(context.Background(), body)
	warmDuration := time.Since(warmStarted)

	require.True(t, result.Metrics.AggregatePressure)
	require.Equal(t, 1, result.Metrics.EffectiveThresholdBytes)
	require.Equal(t, 2, result.Metrics.OptimizedImageCount)
	require.Equal(t, 2, result.Metrics.OriginalInlineAttachmentCount)
	require.Equal(t, 2, result.Metrics.CandidateInlineAttachmentCount)
	require.Less(t, result.Metrics.CandidateInlineAttachmentBytes, result.Metrics.OriginalInlineAttachmentBytes)
	require.Equal(t, result.Body, warm.Body)
	require.Equal(t, 2, warm.Metrics.CacheHits)
	t.Logf(
		"aggregate small images: body=%d->%d saved=%.2f%% cold=%s warm=%s cache_hits=%d",
		len(body),
		len(result.Body),
		(1-float64(len(result.Body))/float64(len(body)))*100,
		coldDuration,
		warmDuration,
		warm.Metrics.CacheHits,
	)
}

func TestRequestBudgetInspectionCountsUnsupportedInlineFiles(t *testing.T) {
	source := makePhotoLikePNG(t, 180, 120, 73)
	config := DefaultConfig()
	config.Enabled = true
	config.RequestBudgetEnabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = len(source) + 1
	gateway, err := New(config)
	require.NoError(t, err)
	payload := map[string]any{
		"model": "gpt-test",
		"input": []any{
			map[string]any{"type": "input_image", "image_url": dataURL("image/png", source)},
			map[string]any{"type": "input_file", "filename": "report.pdf", "file_data": "data:application/pdf;base64,QUJDRA=="},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	result := gateway.Optimize(context.Background(), body)

	require.Equal(t, body, result.Body)
	require.Equal(t, 2, result.Metrics.OriginalInlineAttachmentCount)
	require.Equal(t, 1, result.Metrics.OriginalUnsupportedAttachmentCount)
	require.Equal(t, result.Metrics.OriginalInlineAttachmentBytes, result.Metrics.CandidateInlineAttachmentBytes)
}

func TestRequestBudgetInspectionDoesNotCountDataURLInText(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	config.RequestBudgetEnabled = true
	config.CacheDir = t.TempDir()
	gateway, err := New(config)
	require.NoError(t, err)
	body := []byte(`{"model":"gpt-test","input":[{"role":"user","content":[{"type":"input_text","text":"data:image/png;base64,QUJD"}]}]}`)

	result := gateway.Optimize(context.Background(), body)

	require.Equal(t, body, result.Body)
	require.Zero(t, result.Metrics.OriginalInlineAttachmentCount)
}

func TestCacheHitReusesContentAddressedEntry(t *testing.T) {
	cacheDir := t.TempDir()
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = cacheDir
	config.ThresholdBytes = 1
	gateway, err := New(config)
	require.NoError(t, err)
	source := makePhotoLikePNG(t, 640, 420, 19)
	body := makeResponsesPayload(t, []string{dataURL("image/png", source)}, 0)

	first := gateway.Optimize(context.Background(), body)
	second := gateway.Optimize(context.Background(), body)

	require.Equal(t, first.Body, second.Body)
	require.False(t, first.Metrics.CacheHit)
	require.True(t, second.Metrics.CacheHit)
	require.Equal(t, 1, second.Metrics.CacheHits)
	require.Len(t, mustGlob(t, filepath.Join(cacheDir, "*.webp")), 1)
	require.Len(t, mustGlob(t, filepath.Join(cacheDir, "*.json")), 1)
}

func TestTransparentImageUsesLosslessPolicyAndPreservesAlpha(t *testing.T) {
	cacheDir := t.TempDir()
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = cacheDir
	config.ThresholdBytes = 1
	gateway, err := New(config)
	require.NoError(t, err)
	source := makeTransparentPNG(t, 520, 360)
	body := makeResponsesPayload(t, []string{dataURL("image/png", source)}, 0)

	result := gateway.Optimize(context.Background(), body)
	require.Equal(t, 1, result.Metrics.OptimizedImageCount)

	metadataBytes, err := os.ReadFile(mustGlob(t, filepath.Join(cacheDir, "*.json"))[0])
	require.NoError(t, err)
	var metadata Metadata
	require.NoError(t, json.Unmarshal(metadataBytes, &metadata))
	require.True(t, metadata.Lossless)
	require.Equal(t, 100, metadata.Quality)

	var rewritten map[string]any
	require.NoError(t, json.Unmarshal(result.Body, &rewritten))
	input, ok := rewritten["input"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, input)
	message, ok := input[0].(map[string]any)
	require.True(t, ok)
	content, ok := message["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 2)
	part, ok := content[1].(map[string]any)
	require.True(t, ok)
	optimizedURL, ok := part["image_url"].(string)
	require.True(t, ok)
	optimizedBytes := decodeDataURLForTest(t, optimizedURL)
	decoded, err := webp.Decode(bytes.NewReader(optimizedBytes))
	require.NoError(t, err)
	_, _, _, alpha := decoded.At(0, 0).RGBA()
	require.Less(t, alpha, uint32(0xffff))
}

func TestCodeAndUIScreenshotHeuristicUsesConservativeQuality(t *testing.T) {
	image := image.NewNRGBA(image.Rect(0, 0, 900, 600))
	for y := 0; y < image.Bounds().Dy(); y++ {
		for x := 0; x < image.Bounds().Dx(); x++ {
			value := uint8(20)
			if (x/7+y/13)%5 == 0 {
				value = 235
			}
			image.SetNRGBA(x, y, color.NRGBA{R: value, G: value, B: value, A: 255})
		}
	}
	config := DefaultConfig()
	policy := chooseImagePolicy(image, config)
	require.False(t, policy.Lossless)
	require.Equal(t, config.SpecialQuality, policy.Quality)
	require.Equal(t, "text_or_ui", policy.Reason)
}

func TestUnsupportedRemoteFileAndInvalidDataRemainUnchanged(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = 1
	gateway, err := New(config)
	require.NoError(t, err)
	payload := map[string]any{
		"model": "gpt-test",
		"input": []any{
			map[string]any{"type": "input_image", "file_id": "file_123"},
			map[string]any{"type": "input_image", "image_url": "https://example.test/image.png"},
			map[string]any{"type": "input_file", "file_data": "data:application/pdf;base64,AAAA"},
			map[string]any{"type": "input_image", "image_url": "data:image/gif;base64,R0lGODlh"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,not-valid%%"},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	result := gateway.Optimize(context.Background(), body)

	require.Equal(t, body, result.Body)
	require.Equal(t, 2, result.Metrics.ImageCount)
	require.Equal(t, 1, result.Metrics.SkippedUnsupported)
	require.Equal(t, 1, result.Metrics.Errors)
}

func TestLegacyImageURLObjectIsOptimized(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = 1
	gateway, err := New(config)
	require.NoError(t, err)
	source := makePhotoLikeJPEG(t, 680, 420)
	payload := map[string]any{
		"model": "gpt-test",
		"input": []any{map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": dataURL("image/jpeg", source)},
		}},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	result := gateway.Optimize(context.Background(), body)

	require.Equal(t, 1, result.Metrics.ImageCount)
	require.Equal(t, 1, result.Metrics.OptimizedImageCount)
	require.Contains(t, string(result.Body), "data:image/webp;base64,")
}

func TestWebPInputIsDetectedAndDecoded(t *testing.T) {
	pngSource := makePhotoLikePNG(t, 480, 320, 91)
	decoded, err := png.Decode(bytes.NewReader(pngSource))
	require.NoError(t, err)
	var webpSource bytes.Buffer
	require.NoError(t, webp.Encode(&webpSource, decoded, webp.Options{Lossless: true, Quality: 100, Method: 6}))

	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = 1
	gateway, err := newWithEncoder(config, &countingEncoder{})
	require.NoError(t, err)
	body := makeResponsesPayload(t, []string{dataURL("image/webp", webpSource.Bytes())}, 0)

	result := gateway.Optimize(context.Background(), body)

	require.Equal(t, 1, result.Metrics.ImageCount)
	require.Equal(t, 1, result.Metrics.OptimizedImageCount)
	require.Zero(t, result.Metrics.SkippedUnsupported)
}

func TestConcurrentRequestsSingleflightOneEncode(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = 1
	encoder := &countingEncoder{}
	gateway, err := newWithEncoder(config, encoder)
	require.NoError(t, err)
	source := makePhotoLikePNG(t, 400, 300, 3)
	body := makeResponsesPayload(t, []string{dataURL("image/png", source)}, 0)

	const concurrency = 12
	start := make(chan struct{})
	results := make(chan Result, concurrency)
	var workers sync.WaitGroup
	for range concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- gateway.Optimize(context.Background(), body)
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	cacheHits := 0
	cacheShared := 0
	coldCreators := 0
	for result := range results {
		require.Equal(t, 1, result.Metrics.OptimizedImageCount)
		switch {
		case result.Metrics.CacheHit:
			cacheHits++
		case result.Metrics.CacheShared > 0:
			cacheShared++
		default:
			coldCreators++
		}
	}
	require.Equal(t, int32(1), encoder.calls.Load())
	require.LessOrEqual(t, coldCreators, 1)
	require.GreaterOrEqual(t, cacheHits+cacheShared, concurrency-1)
}

func TestMaxImagesPerRequestBoundsWork(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = 1
	config.MaxImagesPerRequest = 1
	gateway, err := New(config)
	require.NoError(t, err)
	sourceA := makePhotoLikePNG(t, 420, 300, 1)
	sourceB := makePhotoLikePNG(t, 420, 300, 2)
	body := makeResponsesPayload(t, []string{
		dataURL("image/png", sourceA),
		dataURL("image/png", sourceB),
	}, 0)

	result := gateway.Optimize(context.Background(), body)

	require.Equal(t, 2, result.Metrics.ImageCount)
	require.Equal(t, 1, result.Metrics.OptimizedImageCount)
	require.Equal(t, 1, result.Metrics.SkippedRequestImageLimit)
}

func TestMaxTotalImageBytesPerRequestBoundsWork(t *testing.T) {
	sourceA := makePhotoLikePNG(t, 420, 300, 31)
	sourceB := makePhotoLikePNG(t, 420, 300, 32)
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = 1
	config.MaxTotalImageBytes = len(sourceA) + len(sourceB) - 1
	gateway, err := New(config)
	require.NoError(t, err)
	body := makeResponsesPayload(t, []string{
		dataURL("image/png", sourceA),
		dataURL("image/png", sourceB),
	}, 0)

	result := gateway.Optimize(context.Background(), body)

	require.Equal(t, 2, result.Metrics.ImageCount)
	require.Equal(t, 1, result.Metrics.OptimizedImageCount)
	require.Equal(t, 1, result.Metrics.SkippedTotalImageBytes)
}

func TestDiskCacheHitSurvivesGatewayRestart(t *testing.T) {
	cacheDir := t.TempDir()
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = cacheDir
	config.ThresholdBytes = 1
	source := makePhotoLikePNG(t, 420, 300, 41)
	body := makeResponsesPayload(t, []string{dataURL("image/png", source)}, 0)

	firstEncoder := &countingEncoder{}
	firstGateway, err := newWithEncoder(config, firstEncoder)
	require.NoError(t, err)
	first := firstGateway.Optimize(context.Background(), body)
	require.Equal(t, 1, first.Metrics.OptimizedImageCount)
	require.Equal(t, int32(1), firstEncoder.calls.Load())

	secondEncoder := &countingEncoder{}
	secondGateway, err := newWithEncoder(config, secondEncoder)
	require.NoError(t, err)
	second := secondGateway.Optimize(context.Background(), body)

	require.Equal(t, first.Body, second.Body)
	require.True(t, second.Metrics.CacheHit)
	require.Equal(t, 1, second.Metrics.CacheHits)
	require.Zero(t, secondEncoder.calls.Load())
}

func TestDecodedHashIgnoresBase64Whitespace(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = 1
	encoder := &countingEncoder{}
	gateway, err := newWithEncoder(config, encoder)
	require.NoError(t, err)
	source := makePhotoLikePNG(t, 420, 300, 51)
	encoded := base64.StdEncoding.EncodeToString(source)
	withWhitespace := strings.Join(splitStringEvery(encoded, 64), "\n")
	firstBody := makeResponsesPayload(t, []string{"data:image/png;base64," + encoded}, 0)
	secondBody := makeResponsesPayload(t, []string{"data:image/png;base64," + withWhitespace}, 0)

	first := gateway.Optimize(context.Background(), firstBody)
	second := gateway.Optimize(context.Background(), secondBody)

	require.Equal(t, 1, first.Metrics.OptimizedImageCount)
	require.True(t, second.Metrics.CacheHit)
	require.Equal(t, int32(1), encoder.calls.Load())
	require.Equal(t, first.Body, second.Body)
}

func TestCorruptCacheEntryIsRejectedAndRebuilt(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		corrupt func(*testing.T, string, string)
	}{
		{
			name: "optimized image hash mismatch",
			corrupt: func(t *testing.T, imagePath, _ string) {
				content, err := os.ReadFile(imagePath)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(imagePath, bytes.Repeat([]byte{0x99}, len(content)), 0o600))
			},
		},
		{
			name: "invalid metadata",
			corrupt: func(t *testing.T, _, metadataPath string) {
				require.NoError(t, os.WriteFile(metadataPath, []byte("{"), 0o600))
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			config := DefaultConfig()
			config.Enabled = true
			config.CacheDir = t.TempDir()
			config.ThresholdBytes = 1
			encoder := &countingEncoder{}
			gateway, err := newWithEncoder(config, encoder)
			require.NoError(t, err)
			disableAsyncCacheCleanupForTest(gateway)
			source := makePhotoLikePNG(t, 420, 300, 61)
			body := makeResponsesPayload(t, []string{dataURL("image/png", source)}, 0)

			first := gateway.Optimize(context.Background(), body)
			require.Equal(t, 1, first.Metrics.OptimizedImageCount)
			imagePath := mustGlob(t, filepath.Join(config.CacheDir, "*.webp"))[0]
			metadataPath := mustGlob(t, filepath.Join(config.CacheDir, "*.json"))[0]
			testCase.corrupt(t, imagePath, metadataPath)

			second := gateway.Optimize(context.Background(), body)

			require.Equal(t, 1, second.Metrics.OptimizedImageCount)
			require.False(t, second.Metrics.CacheHit)
			require.Equal(t, int32(2), encoder.calls.Load())
		})
	}
}

func TestTransformDeadlineFailsOpen(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = 1
	encoder := newBlockingEncoder()
	gateway, err := newWithEncoder(config, encoder)
	require.NoError(t, err)
	source := makePhotoLikePNG(t, 240, 160, 71)
	body := makeResponsesPayload(t, []string{dataURL("image/png", source)}, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := gateway.Optimize(ctx, body)

	require.Equal(t, body, result.Body)
	require.True(t, result.Metrics.TimedOut)
	require.Zero(t, result.Metrics.OptimizedImageCount)
	close(encoder.release)
	select {
	case <-encoder.done:
	case <-time.After(time.Second):
		t.Fatal("blocking encoder did not exit")
	}
	require.Eventually(t, func() bool {
		return len(mustGlob(t, filepath.Join(config.CacheDir, "*.webp"))) == 0
	}, time.Second, 10*time.Millisecond)
}

func TestTimedOutBackgroundEncodeStillHoldsConcurrencySlot(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = 1
	config.MaxConcurrentEncode = 1
	encoder := newFirstCallBlockingEncoder()
	gateway, err := newWithEncoder(config, encoder)
	require.NoError(t, err)
	firstSource := makePhotoLikePNG(t, 240, 160, 72)
	secondSource := makePhotoLikePNG(t, 240, 160, 73)
	firstBody := makeResponsesPayload(t, []string{dataURL("image/png", firstSource)}, 0)
	secondBody := makeResponsesPayload(t, []string{dataURL("image/png", secondSource)}, 0)

	firstCtx, cancelFirst := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelFirst()
	firstDone := make(chan Result, 1)
	go func() {
		firstDone <- gateway.Optimize(firstCtx, firstBody)
	}()
	select {
	case <-encoder.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first blocking encode did not start")
	}

	var first Result
	select {
	case first = <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("timed-out request did not fail open")
	}
	require.Equal(t, firstBody, first.Body)
	require.True(t, first.Metrics.TimedOut)
	require.Equal(t, 1, len(gateway.encodeSlots))

	secondDone := make(chan Result, 1)
	go func() {
		secondDone <- gateway.Optimize(context.Background(), secondBody)
	}()
	require.Never(t, func() bool {
		return encoder.calls.Load() > 1
	}, 150*time.Millisecond, 10*time.Millisecond)

	close(encoder.releaseFirst)
	select {
	case second := <-secondDone:
		require.Equal(t, 1, second.Metrics.OptimizedImageCount)
	case <-time.After(2 * time.Second):
		t.Fatal("second encode did not proceed after the first worker released its slot")
	}
	require.Equal(t, int32(2), encoder.calls.Load())
}

func TestTransformPanicFailsOpen(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = 1
	gateway, err := newWithEncoder(config, panicEncoder{})
	require.NoError(t, err)
	source := makePhotoLikePNG(t, 240, 160, 81)
	body := makeResponsesPayload(t, []string{dataURL("image/png", source)}, 0)

	result := gateway.Optimize(context.Background(), body)

	require.Equal(t, body, result.Body)
	require.Equal(t, 1, result.Metrics.Errors)
	require.Zero(t, result.Metrics.OptimizedImageCount)
}

func TestWebPQuality85And90KeepCodeLikeRasterReadable(t *testing.T) {
	source := makeCodeLikeImage(960, 640)
	encoder := libwebpEncoder{}
	q85, err := encoder.Encode(source, encodeOptions{Quality: 85})
	require.NoError(t, err)
	q90, err := encoder.Encode(source, encodeOptions{Quality: 90})
	require.NoError(t, err)
	decoded85, err := webp.Decode(bytes.NewReader(q85))
	require.NoError(t, err)
	decoded90, err := webp.Decode(bytes.NewReader(q90))
	require.NoError(t, err)

	error85 := meanAbsoluteRGBError(source, decoded85)
	error90 := meanAbsoluteRGBError(source, decoded90)
	// Encoder quality is not guaranteed to be monotonic for every synthetic
	// raster. Both policies must stay inside a conservative visual-error bound;
	// OCR/semantic checks are covered by the external Phase 1 quality fixture.
	require.Less(t, error85, 20.0)
	require.Less(t, error90, 20.0)
	require.GreaterOrEqual(t, len(q90), len(q85))
}

type countingEncoder struct {
	calls atomic.Int32
}

func (e *countingEncoder) ID() string { return "counting-test-encoder" }

func (e *countingEncoder) Encode(_ image.Image, _ encodeOptions) ([]byte, error) {
	e.calls.Add(1)
	time.Sleep(40 * time.Millisecond)
	return bytes.Repeat([]byte{0x42}, 128), nil
}

type blockingEncoder struct {
	release chan struct{}
	done    chan struct{}
}

type firstCallBlockingEncoder struct {
	calls        atomic.Int32
	firstStarted chan struct{}
	releaseFirst chan struct{}
}

func newFirstCallBlockingEncoder() *firstCallBlockingEncoder {
	return &firstCallBlockingEncoder{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
}

func (e *firstCallBlockingEncoder) ID() string { return "first-call-blocking-test-encoder" }

func (e *firstCallBlockingEncoder) Encode(_ image.Image, _ encodeOptions) ([]byte, error) {
	if e.calls.Add(1) == 1 {
		close(e.firstStarted)
		<-e.releaseFirst
	}
	return bytes.Repeat([]byte{0x42}, 128), nil
}

func newBlockingEncoder() *blockingEncoder {
	return &blockingEncoder{release: make(chan struct{}), done: make(chan struct{})}
}

func (e *blockingEncoder) ID() string { return "blocking-test-encoder" }

func (e *blockingEncoder) Encode(_ image.Image, _ encodeOptions) ([]byte, error) {
	defer close(e.done)
	<-e.release
	return bytes.Repeat([]byte{0x42}, 128), nil
}

type panicEncoder struct{}

func (panicEncoder) ID() string { return "panic-test-encoder" }

func (panicEncoder) Encode(_ image.Image, _ encodeOptions) ([]byte, error) {
	panic("test encoder panic")
}

func splitStringEvery(value string, width int) []string {
	parts := make([]string, 0, (len(value)+width-1)/width)
	for len(value) > width {
		parts = append(parts, value[:width])
		value = value[width:]
	}
	if value != "" {
		parts = append(parts, value)
	}
	return parts
}

func disableAsyncCacheCleanupForTest(gateway *Gateway) {
	gateway.cache.cleanupStateMu.Lock()
	gateway.cache.lastCleanup = gateway.cache.now().UTC()
	gateway.cache.cleanupStateMu.Unlock()
}

func newTestGateway(t testing.TB, mutate func(*Config)) *Gateway {
	t.Helper()
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = t.TempDir()
	config.ThresholdBytes = 1
	if mutate != nil {
		mutate(&config)
	}
	gateway, err := New(config)
	require.NoError(t, err)
	return gateway
}

func makeResponsesPayload(t testing.TB, imageURLs []string, contextBytes int) []byte {
	t.Helper()
	content := []any{map[string]any{"type": "input_text", "text": "Describe the images and preserve small text."}}
	for _, imageURL := range imageURLs {
		content = append(content, map[string]any{
			"type":      "input_image",
			"image_url": imageURL,
			"detail":    "high",
		})
	}
	payload := map[string]any{
		"model":        "gpt-test",
		"stream":       true,
		"instructions": strings.Repeat("context ", contextBytes/8),
		"input": []any{map[string]any{
			"type":    "message",
			"role":    "user",
			"content": content,
		}},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return body
}

func dataURL(mimeType string, content []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(content)
}

func decodeDataURLForTest(t testing.TB, raw string) []byte {
	t.Helper()
	_, encoded, found := strings.Cut(raw, ",")
	require.True(t, found)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	return decoded
}

func makePhotoLikePNG(t testing.TB, width, height int, seed uint32) []byte {
	t.Helper()
	image := image.NewNRGBA(image.Rect(0, 0, width, height))
	state := seed
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			state = state*1664525 + 1013904223
			noise := int((state>>28)&0xf) - 8
			image.SetNRGBA(x, y, color.NRGBA{
				R: clampByte(x*255/max(1, width-1) + noise),
				G: clampByte(y*255/max(1, height-1) + noise),
				B: clampByte((x+y)*127/max(1, width+height-2) + 64 + noise),
				A: 255,
			})
		}
	}
	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, image))
	return buffer.Bytes()
}

func makePhotoLikeJPEG(t testing.TB, width, height int) []byte {
	t.Helper()
	image := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			image.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 255 / max(1, width-1)),
				G: uint8(y * 255 / max(1, height-1)),
				B: uint8((x + y) * 255 / max(1, width+height-2)),
				A: 255,
			})
		}
	}
	var buffer bytes.Buffer
	require.NoError(t, jpeg.Encode(&buffer, image, &jpeg.Options{Quality: 100}))
	return buffer.Bytes()
}

func makeTransparentPNG(t testing.TB, width, height int) []byte {
	t.Helper()
	image := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			alpha := uint8(255)
			if (x/24+y/24)%2 == 0 {
				alpha = 80
			}
			image.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*13 + y*3) % 256),
				G: uint8((x*5 + y*11) % 256),
				B: uint8((x + y*7) % 256),
				A: alpha,
			})
		}
	}
	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, image))
	return buffer.Bytes()
}

func makeCodeLikeImage(width, height int) *image.NRGBA {
	output := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixel := color.NRGBA{R: 18, G: 24, B: 34, A: 255}
			line := (y / 22) % 17
			if y%22 >= 5 && y%22 <= 12 && x > 35+line*3 && x < 180+(line*47)%700 {
				pixel = color.NRGBA{R: 225, G: 235, B: 245, A: 255}
			}
			output.SetNRGBA(x, y, pixel)
		}
	}
	return output
}

func meanAbsoluteRGBError(left, right image.Image) float64 {
	bounds := left.Bounds()
	var total uint64
	var samples uint64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			lr, lg, lb, _ := left.At(x, y).RGBA()
			rr, rg, rb, _ := right.At(x, y).RGBA()
			total += absDiff16(lr, rr) + absDiff16(lg, rg) + absDiff16(lb, rb)
			samples += 3
		}
	}
	return float64(total) / float64(samples) / 257
}

func absDiff16(left, right uint32) uint64 {
	if left >= right {
		return uint64(left - right)
	}
	return uint64(right - left)
}

func clampByte(value int) uint8 {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return uint8(value)
}

func mustGlob(t testing.TB, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	require.NoError(t, err)
	return matches
}
