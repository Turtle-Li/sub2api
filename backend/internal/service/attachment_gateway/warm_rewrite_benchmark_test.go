package attachment_gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func warmRewriteFixture(t testing.TB, count, sourceBytes, optimizedBytes int) (*Gateway, *URLExternalizer, *recordingObjectStorage, []byte, []byte) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.CacheDir = t.TempDir()
	cfg.ThresholdBytes = 128 << 10
	cfg.MaxImagesPerRequest = count
	cfg.RequestBudgetEnabled = true
	cfg.AggregateSmallImageEnabled = true
	gateway, err := New(cfg)
	require.NoError(t, err)
	// Fixtures are persisted, hash-verified cache hits. No raster decoding is
	// needed to set up the benchmark or to run the historical warm path.
	gateway.cache.lastCleanup = gateway.cache.now()
	store := &recordingObjectStorage{url: "https://r2.example.test/image.webp?signature=test&opaque=one"}
	externalizer, err := NewURLExternalizer(URLConfig{Enabled: true, MinBodyBytes: 512 << 10, MaxImagesPerRequest: count}, store)
	require.NoError(t, err)

	const prefix = `  {"context":{"n":900719925474099312345,"text":"keep \u0061\n\" and https:\/\/example.test"},"input":[{"type":"message","content":[`
	const suffix = "]}], \"tail\": true  }\n"
	const part = ` { "type":"input_image", "image_url":`
	var original, expected bytes.Buffer
	original.Grow(len(prefix) + count*(len(part)+len("data:image/png;base64,")+base64.StdEncoding.EncodedLen(sourceBytes)+4) + len(suffix))
	_, _ = original.WriteString(prefix)
	_, _ = expected.WriteString(prefix)
	encoder := base64.StdEncoding
	source := bytes.Repeat([]byte{0x47}, sourceBytes)
	for i := range count {
		copy(source, fmt.Sprintf("%05d", i))
		candidate := bytes.Repeat([]byte{0x63}, optimizedBytes)
		copy(candidate, fmt.Sprintf("%05d", i))
		seedWarmOptimized(t, gateway, source, candidate)
		if i != 0 {
			_ = original.WriteByte(',')
			_ = expected.WriteByte(',')
		}
		_, _ = original.WriteString(part)
		_, _ = expected.WriteString(part)
		_, _ = original.WriteString(`"data:image/png;base64,`)
		encoded := base64.NewEncoder(encoder, &original)
		_, err := encoded.Write(source)
		require.NoError(t, err)
		require.NoError(t, encoded.Close())
		_, _ = original.WriteString(`" }`)
		_, _ = expected.WriteString(`"https://r2.example.test/image.webp?signature=test\u0026opaque=one" }`)
	}
	_, _ = original.WriteString(suffix)
	_, _ = expected.WriteString(suffix)
	return gateway, externalizer, store, original.Bytes(), expected.Bytes()
}

func seedWarmOptimized(t testing.TB, g *Gateway, source, encoded []byte) {
	t.Helper()
	originalHash := sourceHash(source)
	now := g.cache.now()
	require.NoError(t, g.cache.store(originalHash, optimizedImage{Bytes: encoded, Metadata: Metadata{
		OriginalHash: originalHash, OptimizedHash: optimizedHash(encoded), OriginalSize: len(source),
		OptimizedSize: len(encoded), OriginalMIMEType: "image/png", MIMEType: "image/webp",
		Width: 64, Height: 64, Quality: g.config.Quality, Policy: g.cache.policy, Optimizer: g.encoder.ID(),
		CreatedAt: now, ExpiresAt: now.Add(g.cache.ttl),
	}}))
}

func warmRewriteSizes(count int) (source, optimized int) {
	if count == 50 {
		return 640 << 10, 180 << 10
	}
	return 144 << 10, 72 << 10
}

func BenchmarkWarmAttachmentRewriteReference(b *testing.B) {
	for _, count := range []int{50, 250} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			source, optimized := warmRewriteSizes(count)
			gateway, externalizer, store, body, expected := warmRewriteFixture(b, count, source, optimized)
			ctx := context.Background()
			rewrite := func() URLResult {
				_, err := InspectInlineAttachments(body) // handler budget preflight
				if err != nil {
					b.Fatal(err)
				}
				compressed := gateway.Optimize(ctx, body)
				result := externalizer.Externalize(ctx, compressed.Body)
				_, err = InspectInlineAttachments(result.Body) // final budget
				if err != nil {
					b.Fatal(err)
				}
				return result
			}
			require.Equal(b, expected, rewrite().Body)
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
