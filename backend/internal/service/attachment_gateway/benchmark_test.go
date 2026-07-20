package attachment_gateway

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// Run with -benchtime=1x for a cold-cache Phase 1 snapshot. The benchmark is
// local-only and performs no network requests.
func BenchmarkPhase1Scenarios(b *testing.B) {
	large := makeBenchmarkPNG(b, 2500, 2100, 20260720)
	five := make([]string, 0, 5)
	for seed := uint32(1); seed <= 5; seed++ {
		fixture := makeBenchmarkPNG(b, 1100, 800, seed)
		five = append(five, dataURL("image/png", fixture))
	}
	cases := []struct {
		name         string
		imageURLs    []string
		contextBytes int
	}{
		{name: "one_large_png", imageURLs: []string{dataURL("image/png", large)}},
		{name: "five_images", imageURLs: five},
		{name: "large_plus_1m_context", imageURLs: []string{dataURL("image/png", large)}, contextBytes: 1024 * 1024},
	}

	for _, testCase := range cases {
		b.Run(testCase.name+"/cold", func(b *testing.B) {
			body := makeResponsesPayload(b, testCase.imageURLs, testCase.contextBytes)
			baseCacheDir := b.TempDir()
			var last Result
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				config := DefaultConfig()
				config.Enabled = true
				config.CacheDir = filepath.Join(baseCacheDir, strconv.Itoa(index))
				config.ThresholdBytes = 512 * 1024
				gateway, err := New(config)
				require.NoError(b, err)
				last = gateway.Optimize(context.Background(), body)
			}
			b.StopTimer()
			reportPhase1Metrics(b, last)
		})

		b.Run(testCase.name+"/warm", func(b *testing.B) {
			body := makeResponsesPayload(b, testCase.imageURLs, testCase.contextBytes)
			config := DefaultConfig()
			config.Enabled = true
			config.CacheDir = b.TempDir()
			config.ThresholdBytes = 512 * 1024
			gateway, err := New(config)
			require.NoError(b, err)
			primed := gateway.Optimize(context.Background(), body)
			require.NotZero(b, primed.Metrics.OptimizedImageCount)

			var last Result
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				last = gateway.Optimize(context.Background(), body)
			}
			b.StopTimer()
			reportPhase1Metrics(b, last)
		})
	}
}

func BenchmarkPhase1LoopbackForward(b *testing.B) {
	large := makeBenchmarkPNG(b, 2500, 2100, 20260720)
	original := makeResponsesPayload(b, []string{dataURL("image/png", large)}, 0)
	config := DefaultConfig()
	config.Enabled = true
	config.CacheDir = b.TempDir()
	gateway, err := New(config)
	require.NoError(b, err)
	optimized := gateway.Optimize(context.Background(), original).Body

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		writer.WriteHeader(http.StatusNoContent)
	}))
	b.Cleanup(server.Close)
	client := server.Client()

	for _, testCase := range []struct {
		name string
		body []byte
	}{
		{name: "original", body: original},
		{name: "optimized", body: optimized},
	} {
		b.Run(testCase.name, func(b *testing.B) {
			b.SetBytes(int64(len(testCase.body)))
			b.ReportMetric(float64(len(testCase.body)), "body_B")
			b.ResetTimer()
			for range b.N {
				response, err := client.Post(server.URL+"/v1/responses", "application/json", bytes.NewReader(testCase.body))
				require.NoError(b, err)
				_ = response.Body.Close()
				require.Equal(b, http.StatusNoContent, response.StatusCode)
			}
		})
	}
}

// makeBenchmarkPNG adds independent low-amplitude channel noise. This keeps
// the synthetic image visually recognizable while producing the 10+ MB PNGs
// that cause base64 Responses payloads to cross the observed Caddy boundary.
func makeBenchmarkPNG(b testing.TB, width, height int, seed uint32) []byte {
	b.Helper()
	output := image.NewNRGBA(image.Rect(0, 0, width, height))
	state := seed
	nextNoise := func() int {
		state = state*1664525 + 1013904223
		return int((state>>27)&0x1f) - 16
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			output.SetNRGBA(x, y, color.NRGBA{
				R: clampByte(x*255/max(1, width-1) + nextNoise()),
				G: clampByte(y*255/max(1, height-1) + nextNoise()),
				B: clampByte((x+y)*127/max(1, width+height-2) + 64 + nextNoise()),
				A: 255,
			})
		}
	}
	var buffer bytes.Buffer
	require.NoError(b, png.Encode(&buffer, output))
	return buffer.Bytes()
}

func reportPhase1Metrics(b *testing.B, result Result) {
	b.Helper()
	b.ReportMetric(float64(result.Metrics.OriginalBodyBytes), "original_body_B")
	b.ReportMetric(float64(result.Metrics.OptimizedBodyBytes), "optimized_body_B")
	if result.Metrics.OriginalBodyBytes > 0 {
		saved := (1 - float64(result.Metrics.OptimizedBodyBytes)/float64(result.Metrics.OriginalBodyBytes)) * 100
		b.ReportMetric(saved, "saved_%")
	}
	b.ReportMetric(float64(result.Metrics.CacheHits), "cache_hits")
}
