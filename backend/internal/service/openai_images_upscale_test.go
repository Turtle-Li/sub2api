package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRewriteOpenAIImagesSizePreservesRequest(t *testing.T) {
	body := []byte(`{"model":"gemini-2.5-flash-image","prompt":"cat","size":"4K","extra":{"keep":true}}`)
	rewritten, contentType, err := rewriteOpenAIImagesSize(body, "application/json", "1K")
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, "1K", gjson.GetBytes(rewritten, "size").String())
	require.True(t, gjson.GetBytes(rewritten, "extra.keep").Bool())
}

func TestRewriteOpenAIImagesMultipartSize(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gemini-2.5-flash-image"))
	require.NoError(t, writer.WriteField("size", "2K"))
	require.NoError(t, writer.WriteField("prompt", "cat"))
	require.NoError(t, writer.Close())

	rewritten, contentType, err := rewriteOpenAIImagesSize(body.Bytes(), writer.FormDataContentType(), "1K")
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(rewritten))
	request.Header.Set("Content-Type", contentType)
	require.NoError(t, request.ParseMultipartForm(1024))
	require.Equal(t, "1K", request.FormValue("size"))
	require.Equal(t, "cat", request.FormValue("prompt"))
}

func TestUpscaleOpenAIImagesResponsePreservesShape(t *testing.T) {
	source := testUpscalePNG(t, 2, 3)
	result := testUpscalePNG(t, 4, 6)
	_, _, _, decodeErr := decodeUpscaleImageConfig(source)
	require.NoError(t, decodeErr)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/upscale":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job":{"id":"job-1"}}`))
		case "/v1/jobs/job-1":
			_, _ = w.Write([]byte(`{"job":{"state":"completed"}}`))
		case "/v1/jobs/job-1/result":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(result)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	upscaler := &ImageUpscaleService{
		cfg:        config.ImageUpscaleConfig{Enabled: true, BaseURL: server.URL, APIKeyVaultRef: "vault://test/key#api_key", VaultAgentSocket: imageUpscaleVaultSocket, RequestTimeoutSeconds: 2, JobTimeoutSeconds: 30, PollIntervalMillis: 1, RetryMax: 0, MaxConcurrent: 1, MaxQueue: 1, MaxResultBytes: 1024 * 1024},
		httpClient: server.Client(),
		loadAPIKey: func(context.Context) ([]byte, error) { return []byte("test-token"), nil },
		slots:      make(chan struct{}, 1),
	}
	svc := &OpenAIGatewayService{imageUpscaler: upscaler}
	body := []byte(`{"created":1,"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(source) + `","revised_prompt":"cat"}],"usage":{"total_tokens":7}}`)
	normalized := normalizeOpenAIImageBase64(gjson.GetBytes(body, "data.0.b64_json").String())
	decodedSource, decodeErr := base64.StdEncoding.DecodeString(normalized)
	require.NoError(t, decodeErr)
	_, _, _, decodeErr = decodeUpscaleImageConfig(decodedSource)
	require.NoError(t, decodeErr)

	got, err := svc.upscaleOpenAIImagesResponse(context.Background(), &Account{}, &OpenAIImagesRequest{Size: "2K", SizeTier: "2K", ResponseFormat: "b64_json"}, body)
	require.NoError(t, err)
	require.Equal(t, "cat", gjson.GetBytes(got, "data.0.revised_prompt").String())
	require.Equal(t, int64(7), gjson.GetBytes(got, "usage.total_tokens").Int())
	decoded, err := base64.StdEncoding.DecodeString(gjson.GetBytes(got, "data.0.b64_json").String())
	require.NoError(t, err)
	w, h, _, err := decodeUpscaleImageConfig(decoded)
	require.NoError(t, err)
	require.Equal(t, 4, w)
	require.Equal(t, 6, h)
}

func TestUpscaleOpenAIImagesResponseSkipsAlreadySatisfiedOutput(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected upscale request", http.StatusInternalServerError)
	}))
	defer server.Close()
	upscaler := &ImageUpscaleService{
		cfg: config.ImageUpscaleConfig{
			Enabled: true, BaseURL: server.URL, APIKeyVaultRef: "vault://test/key#api_key",
			VaultAgentSocket: imageUpscaleVaultSocket, RequestTimeoutSeconds: 2,
			JobTimeoutSeconds: 30, PollIntervalMillis: 1, MaxConcurrent: 1,
			MaxQueue: 1, MaxResultBytes: 1024 * 1024,
		},
		httpClient: server.Client(),
		loadAPIKey: func(context.Context) ([]byte, error) {
			return []byte("test-token"), nil
		},
		slots: make(chan struct{}, 1),
	}
	source := base64.StdEncoding.EncodeToString(testUpscalePNG(t, 2048, 1024))
	body := []byte(`{"data":[{"b64_json":"` + source + `"}]}`)

	got, err := (&OpenAIGatewayService{imageUpscaler: upscaler}).upscaleOpenAIImagesResponse(
		context.Background(), &Account{},
		&OpenAIImagesRequest{N: 1, Size: "2K", SizeTier: "2K", ResponseFormat: "b64_json"}, body,
	)
	require.NoError(t, err)
	require.Equal(t, source, gjson.GetBytes(got, "data.0.b64_json").String())
	require.Equal(t, "2048x1024", gjson.GetBytes(got, "data.0.size").String())
	require.Zero(t, calls.Load())
}

func TestUpscaleOpenAIImagesResponseRejectsSurplusBeforeUpscale(t *testing.T) {
	var upscaleCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upscaleCalls.Add(1)
		http.Error(w, "unexpected upscale request", http.StatusInternalServerError)
	}))
	defer server.Close()
	upscaler := &ImageUpscaleService{
		cfg: config.ImageUpscaleConfig{
			Enabled: true, BaseURL: server.URL, APIKeyVaultRef: "vault://test/key#api_key",
			VaultAgentSocket: imageUpscaleVaultSocket, RequestTimeoutSeconds: 2,
			JobTimeoutSeconds: 30, PollIntervalMillis: 1, MaxConcurrent: 1,
			MaxQueue: 1, MaxResultBytes: 1024 * 1024,
		},
		httpClient: server.Client(),
		loadAPIKey: func(context.Context) ([]byte, error) {
			return []byte("test-token"), nil
		},
		slots: make(chan struct{}, 1),
	}
	source := base64.StdEncoding.EncodeToString(testUpscalePNG(t, 2, 3))
	body := []byte(`{"data":[{"b64_json":"` + source + `"},{"b64_json":"` + source + `"}]}`)

	_, err := (&OpenAIGatewayService{imageUpscaler: upscaler}).upscaleOpenAIImagesResponse(
		context.Background(), &Account{},
		&OpenAIImagesRequest{N: 1, Size: "2K", SizeTier: "2K", ResponseFormat: "b64_json"}, body,
	)
	require.Error(t, err)
	var upscaleErr *ImageUpscaleError
	require.ErrorAs(t, err, &upscaleErr)
	require.Equal(t, "INVALID_IMAGE_COUNT", upscaleErr.Code)
	require.Zero(t, upscaleCalls.Load())
}

func TestUpscaleOpenAIImagesResponseRejectsOversizedEncodedSource(t *testing.T) {
	upscaler := &ImageUpscaleService{
		cfg: config.ImageUpscaleConfig{
			Enabled: true, BaseURL: "http://upscale.test", APIKeyVaultRef: "vault://test/key#api_key",
			VaultAgentSocket: imageUpscaleVaultSocket, JobTimeoutSeconds: 30,
			MaxConcurrent: 1, MaxQueue: 1, MaxResultBytes: 1024 * 1024,
		},
		httpClient: &http.Client{},
		loadAPIKey: func(context.Context) ([]byte, error) {
			return []byte("test-token"), nil
		},
		slots: make(chan struct{}, 1),
	}
	encoded := base64.StdEncoding.EncodeToString(make([]byte, imageUpscaleMaxSourceBytes+1))
	body := []byte(`{"data":[{"b64_json":"` + encoded + `"}]}`)

	_, err := (&OpenAIGatewayService{imageUpscaler: upscaler}).upscaleOpenAIImagesResponse(
		context.Background(), &Account{},
		&OpenAIImagesRequest{N: 1, Size: "2K", SizeTier: "2K", ResponseFormat: "b64_json"}, body,
	)
	_ = requireImageUpscaleError(t, err, "SOURCE_LIMIT_EXCEEDED")
}

func TestHandleOpenAIImagesHighResSkipsB64BackfillBeforeUpscaleAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: b64BackfillImageResponse(http.StatusOK, "image/png", b64BackfillPNGBytes)}
	upscaler := &ImageUpscaleService{
		cfg: config.ImageUpscaleConfig{
			Enabled: true, BaseURL: "http://upscale.test", APIKeyVaultRef: "vault://test/key#api_key",
			VaultAgentSocket: imageUpscaleVaultSocket, JobTimeoutSeconds: 30,
			MaxConcurrent: 1, MaxQueue: 0, MaxResultBytes: 1024 * 1024,
		},
		httpClient: &http.Client{},
		loadAPIKey: func(context.Context) ([]byte, error) {
			return []byte("test-token"), nil
		},
		slots: make(chan struct{}, 1),
	}
	upscaler.slots <- struct{}{}
	defer func() { <-upscaler.slots }()
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, imageUpscaler: upscaler}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"data":[{"url":"https://cdn.example.com/source.png"}]}`,
		)),
	}

	_, _, _, _, err := svc.handleOpenAIImagesNonStreamingResponse(
		context.Background(), response, c, b64BackfillAccount(true),
		&OpenAIImagesRequest{N: 1, Size: "2K", SizeTier: "2K", ResponseFormat: "b64_json"},
		"gemini-2.5-flash-image", true,
	)
	_ = requireImageUpscaleError(t, err, "BACKPRESSURE")
	require.Empty(t, upstream.requests, "URL backfill must not run before upscale admission")
}

func TestUpscaleOpenAIImagesResponseUsesOneLifecycleDeadlineForAllImages(t *testing.T) {
	result := testUpscalePNG(t, 4, 6)
	var resultCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/upscale":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job":{"id":"job-shared-deadline"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job-shared-deadline":
			_, _ = w.Write([]byte(`{"job":{"state":"completed"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job-shared-deadline/result":
			resultCalls.Add(1)
			time.Sleep(600 * time.Millisecond)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(result)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	upscaler := &ImageUpscaleService{
		cfg: config.ImageUpscaleConfig{
			Enabled: true, BaseURL: server.URL, APIKeyVaultRef: "vault://test/key#api_key",
			VaultAgentSocket: imageUpscaleVaultSocket, RequestTimeoutSeconds: 2,
			JobTimeoutSeconds: 1, PollIntervalMillis: 1, RetryMax: 0,
			MaxConcurrent: 1, MaxQueue: 1, MaxResultBytes: 1024 * 1024,
		},
		httpClient: server.Client(),
		loadAPIKey: func(context.Context) ([]byte, error) {
			return []byte("test-token"), nil
		},
		slots: make(chan struct{}, 1),
	}
	source := base64.StdEncoding.EncodeToString(testUpscalePNG(t, 2, 3))
	body := []byte(`{"data":[{"b64_json":"` + source + `"},{"b64_json":"` + source + `"}]}`)
	started := time.Now()

	_, err := (&OpenAIGatewayService{imageUpscaler: upscaler}).upscaleOpenAIImagesResponse(
		context.Background(), &Account{},
		&OpenAIImagesRequest{N: 2, Size: "2K", SizeTier: "2K", ResponseFormat: "b64_json"}, body,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 1500*time.Millisecond)
	require.Equal(t, int32(2), resultCalls.Load())
}

func testUpscalePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.NRGBA{R: 80, G: 120, B: 160, A: 255})
		}
	}
	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, img))
	return buffer.Bytes()
}
