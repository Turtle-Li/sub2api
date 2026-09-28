package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestImageUpscaleScaleIsModelIndependent(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		sizeTier  string
		wantScale int
		wantOK    bool
	}{
		{name: "flash image 2K", model: "gemini-2.5-flash-image", sizeTier: "2K", wantScale: 2, wantOK: true},
		{name: "flash image preview 4K", model: "gemini-2.5-flash-image-preview", sizeTier: "4K", wantScale: 4, wantOK: true},
		{name: "one K bypasses upscale", model: "gemini-2.5-flash-image", sizeTier: "1K", wantScale: 0, wantOK: false},
		{name: "unsupported size bypasses upscale", model: "gemini-2.5-flash-image", sizeTier: "8K", wantScale: 0, wantOK: false},
		{name: "future image model uses requested tier", model: "image-3", sizeTier: "2K", wantScale: 2, wantOK: true},
		{name: "other image model uses requested tier", model: "gpt-image-2.5-sunburst", sizeTier: "4K", wantScale: 4, wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotScale, gotOK := RequestedImageUpscaleScale(tt.sizeTier)
			require.Equal(t, tt.wantScale, gotScale)
			require.Equal(t, tt.wantOK, gotOK)
		})
	}
}

func TestImageUpscaleScaleUsesActualDimensions(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		width     int
		height    int
		wantScale int
		want      bool
	}{
		{name: "portrait 1K needs 2x for 2K", requested: "2K", width: 1024, height: 1536, wantScale: 2, want: true},
		{name: "native 2K skips adapter", requested: "2K", width: 1536, height: 2048},
		{name: "portrait 1K needs 4x for 4K", requested: "4K", width: 1024, height: 1536, wantScale: 4, want: true},
		{name: "native 2K needs 2x for 4K", requested: "4K", width: 2048, height: 3072, wantScale: 2, want: true},
		{name: "native 4K skips adapter", requested: "4K", width: 2160, height: 3840},
		{name: "1K never invokes adapter", requested: "1K", width: 512, height: 768},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scale, required := imageUpscaleScaleForDimensions(tt.requested, tt.width, tt.height)
			require.Equal(t, tt.want, required)
			require.Equal(t, tt.wantScale, scale)
		})
	}
}

func TestRequestedImageUpscaleScaleRequiresExplicitClassifiableSize(t *testing.T) {
	for _, size := range []string{"", "auto", "unknown"} {
		scale, ok := RequestedImageUpscaleScale(size)
		require.False(t, ok, "size %q must keep the existing provider path", size)
		require.Zero(t, scale)
	}

	tests := []struct {
		name         string
		model        string
		requested    string
		wantProvider string
		wantScale    int
		wantOK       bool
	}{
		{name: "literal 2K", model: "gemini-2.5-flash-image", requested: "2K", wantProvider: "1K", wantScale: 2, wantOK: true},
		{name: "literal 4K", model: "gemini-2.5-flash-image-preview", requested: "4K", wantProvider: "1K", wantScale: 4, wantOK: true},
		{name: "2K dimensions keep provider path", model: "gemini-2.5-flash-image", requested: "2048x1152", wantOK: false},
		{name: "4K dimensions keep provider path", model: "gemini-2.5-flash-image", requested: "3840x2160", wantOK: false},
		{name: "1K dimensions keep provider path", model: "gemini-2.5-flash-image", requested: "1024x768", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scale, ok := RequestedImageUpscaleScale(tt.requested)
			require.Equal(t, tt.wantOK, ok)
			if ok {
				require.Equal(t, ImageBillingSize1K, tt.wantProvider)
			} else {
				require.Empty(t, tt.wantProvider)
			}
			require.Equal(t, tt.wantScale, scale)
		})
	}
}

func TestImage25SupportedAspectRatiosMatchModelContract(t *testing.T) {
	for _, ratio := range []string{"", "1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"} {
		require.True(t, isImage25SupportedAspectRatio(ratio), ratio)
	}
	for _, ratio := range []string{"1:4", "4:1", "1:8", "8:1", "16:10", "invalid"} {
		require.False(t, isImage25SupportedAspectRatio(ratio), ratio)
	}
}

func TestImageUpscaleRunsSubmitPollAndDownloadForSupportedScales(t *testing.T) {
	tests := []struct {
		name  string
		scale int
	}{
		{name: "two times", scale: 2},
		{name: "four times", scale: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := imageUpscaleTestPNG(t, 3, 2)
			resultData := imageUpscaleTestPNG(t, 3*tt.scale, 2*tt.scale)
			const bearer = "loader-provided-bearer"

			var submits atomic.Int32
			var polls atomic.Int32
			var downloads atomic.Int32
			var loaderCalls atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer "+bearer {
					t.Errorf("Authorization = %q, want injected Bearer token", got)
					http.Error(w, "unexpected authorization", http.StatusUnauthorized)
					return
				}
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/upscale":
					submits.Add(1)
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Errorf("ParseMultipartForm() error = %v", err)
						http.Error(w, "invalid multipart request", http.StatusBadRequest)
						return
					}
					if got := r.FormValue("scale"); got != strconv.Itoa(tt.scale) {
						t.Errorf("scale = %q, want %d", got, tt.scale)
					}
					if got := r.FormValue("preset"); got != "faithful" {
						t.Errorf("preset = %q, want faithful", got)
					}
					file, _, err := r.FormFile("image")
					if err != nil {
						t.Errorf("FormFile(image) error = %v", err)
						http.Error(w, "missing image", http.StatusBadRequest)
						return
					}
					gotSource, readErr := io.ReadAll(file)
					_ = file.Close()
					if readErr != nil {
						t.Errorf("reading submitted image: %v", readErr)
						http.Error(w, "reading image", http.StatusBadRequest)
						return
					}
					if !bytes.Equal(gotSource, source) {
						t.Errorf("submitted image does not match source")
					}
					imageUpscaleTestWriteJSON(w, http.StatusAccepted, `{"job":{"id":"job-123"}}`)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job-123":
					if polls.Add(1) == 1 {
						imageUpscaleTestWriteJSON(w, http.StatusOK, `{"job":{"state":"running"}}`)
						return
					}
					imageUpscaleTestWriteJSON(w, http.StatusOK, `{"job":{"state":"completed"}}`)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job-123/result":
					downloads.Add(1)
					w.Header().Set("Content-Type", "image/png; charset=binary")
					_, _ = w.Write(resultData)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			cfg := imageUpscaleTestConfig(server.URL)
			service := newImageUpscaleTestService(cfg, server.Client(), func(context.Context) ([]byte, error) {
				loaderCalls.Add(1)
				return []byte(bearer), nil
			})

			result, err := service.Upscale(context.Background(), source, tt.scale)
			require.NoError(t, err)
			require.Equal(t, resultData, result.Data)
			require.Equal(t, "image/png", result.MimeType)
			require.Equal(t, 3*tt.scale, result.Width)
			require.Equal(t, 2*tt.scale, result.Height)
			require.Equal(t, tt.scale, result.Scale)
			require.Equal(t, int32(1), submits.Load())
			require.Equal(t, int32(2), polls.Load(), "the running job must be polled again")
			require.Equal(t, int32(1), downloads.Load())
			require.Equal(t, int32(1), loaderCalls.Load(), "the bearer must come from the injected loader once per submission")
		})
	}
}

func TestImageUpscaleRejectsMismatchedResultMetadata(t *testing.T) {
	tests := []struct {
		name         string
		contentType  string
		resultWidth  int
		resultHeight int
		wantCode     string
	}{
		{
			name:         "declared MIME differs from image bytes",
			contentType:  "image/jpeg",
			resultWidth:  6,
			resultHeight: 4,
			wantCode:     "INVALID_RESULT_MIME",
		},
		{
			name:         "image dimensions do not match requested scale",
			contentType:  "image/png",
			resultWidth:  5,
			resultHeight: 4,
			wantCode:     "INVALID_RESULT_DIMENSIONS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := imageUpscaleTestPNG(t, 3, 2)
			resultData := imageUpscaleTestPNG(t, tt.resultWidth, tt.resultHeight)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/upscale":
					imageUpscaleTestWriteJSON(w, http.StatusAccepted, `{"job":{"id":"job-metadata"}}`)
				case "/v1/jobs/job-metadata":
					imageUpscaleTestWriteJSON(w, http.StatusOK, `{"job":{"state":"completed"}}`)
				case "/v1/jobs/job-metadata/result":
					w.Header().Set("Content-Type", tt.contentType)
					_, _ = w.Write(resultData)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			service := newImageUpscaleTestService(imageUpscaleTestConfig(server.URL), server.Client(), imageUpscaleTestStaticLoader)
			_, err := service.Upscale(context.Background(), source, 2)
			upscaleErr := requireImageUpscaleError(t, err, tt.wantCode)
			require.False(t, upscaleErr.Temporary)
		})
	}
}

func TestImageUpscaleSubmitRetriesRateLimitWithinConfiguredBudget(t *testing.T) {
	var submits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/upscale" {
			http.NotFound(w, r)
			return
		}
		submits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := imageUpscaleTestConfig(server.URL)
	cfg.RetryMax = 1
	service := newImageUpscaleTestService(cfg, server.Client(), imageUpscaleTestStaticLoader)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := service.submit(ctx, imageUpscaleTestPNG(t, 2, 2), 2, []byte("test-token"))
	upscaleErr := requireImageUpscaleError(t, err, "SUBMIT_RATE_LIMITED")
	require.Equal(t, http.StatusTooManyRequests, upscaleErr.StatusCode)
	require.True(t, upscaleErr.Temporary)
	require.Equal(t, int32(2), submits.Load(), "RetryMax=1 must limit submission to the initial attempt and one retry")
}

func TestImageUpscalePollRetriesServerError(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/jobs/job-retry" {
			http.NotFound(w, r)
			return
		}
		if polls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		imageUpscaleTestWriteJSON(w, http.StatusOK, `{"job":{"state":"completed"}}`)
	}))
	defer server.Close()

	cfg := imageUpscaleTestConfig(server.URL)
	cfg.RetryMax = 1
	service := newImageUpscaleTestService(cfg, server.Client(), imageUpscaleTestStaticLoader)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, service.waitForCompletion(ctx, "job-retry", []byte("test-token")))
	require.Equal(t, int32(2), polls.Load(), "a transient 5xx poll response must be retried once")
}

func TestImageUpscaleRemoteJobFailureDoesNotExposeSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		imageUpscaleTestWriteJSON(w, http.StatusOK, `{"job":{"state":"failed"}}`)
	}))
	defer server.Close()

	service := newImageUpscaleTestService(imageUpscaleTestConfig(server.URL), server.Client(), imageUpscaleTestStaticLoader)
	err := service.waitForCompletion(context.Background(), "job-failed", []byte("test-token"))
	upscaleErr := requireImageUpscaleError(t, err, "REMOTE_JOB_FAILED")
	require.Zero(t, upscaleErr.StatusCode, "a failed job inside a successful poll response must map to a gateway error")
}

func TestImageUpscaleQueueBackpressure(t *testing.T) {
	cfg := imageUpscaleTestConfig("http://upscale.test")
	cfg.MaxQueue = 1
	service := newImageUpscaleTestService(cfg, &http.Client{}, imageUpscaleTestStaticLoader)
	service.slots <- struct{}{}
	defer func() { <-service.slots }()

	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	defer cancelQueued()
	queuedResult := make(chan error, 1)
	go func() {
		queuedResult <- service.acquire(queuedCtx)
	}()
	require.Eventually(t, func() bool { return service.waiting.Load() == 1 }, time.Second, time.Millisecond)

	upscaleErr := requireImageUpscaleError(t, service.acquire(context.Background()), "BACKPRESSURE")
	require.Equal(t, http.StatusTooManyRequests, upscaleErr.StatusCode)
	require.True(t, upscaleErr.Temporary)

	cancelQueued()
	select {
	case err := <-queuedResult:
		queuedErr := requireImageUpscaleError(t, err, "QUEUE_TIMEOUT")
		require.True(t, queuedErr.Temporary)
	case <-time.After(time.Second):
		t.Fatal("queued acquire did not leave after its context was canceled")
	}
	require.Zero(t, service.waiting.Load())
}

func TestImageUpscaleQueueTimeout(t *testing.T) {
	cfg := imageUpscaleTestConfig("http://upscale.test")
	cfg.MaxQueue = 1
	service := newImageUpscaleTestService(cfg, &http.Client{}, imageUpscaleTestStaticLoader)
	service.slots <- struct{}{}
	defer func() { <-service.slots }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	upscaleErr := requireImageUpscaleError(t, service.acquire(ctx), "QUEUE_TIMEOUT")
	require.True(t, upscaleErr.Temporary)
	require.ErrorIs(t, upscaleErr, context.DeadlineExceeded)
	require.Zero(t, service.waiting.Load())
}

func TestImageUpscaleQueueGivesBatchOneTurnAfterTwoInteractiveJobs(t *testing.T) {
	cfg := imageUpscaleTestConfig("http://upscale.test")
	cfg.MaxQueue = 4
	service := newImageUpscaleTestService(cfg, &http.Client{}, imageUpscaleTestStaticLoader)
	service.slots <- struct{}{}

	order := make(chan string, 4)
	start := func(name string, class imageUpscaleWorkClass) {
		expectedWaiting := service.waiting.Load() + 1
		go func() {
			require.NoError(t, service.acquireClass(context.Background(), class))
			order <- name
			service.release()
		}()
		require.Eventually(t, func() bool { return service.waiting.Load() == expectedWaiting }, time.Second, time.Millisecond)
	}

	start("interactive-1", imageUpscaleWorkInteractive)
	start("interactive-2", imageUpscaleWorkInteractive)
	start("interactive-3", imageUpscaleWorkInteractive)
	start("batch-1", imageUpscaleWorkBatch)
	require.Eventually(t, func() bool { return service.waiting.Load() == 4 }, time.Second, time.Millisecond)

	service.release()
	got := []string{<-order, <-order, <-order, <-order}
	require.Equal(t, []string{"interactive-1", "interactive-2", "batch-1", "interactive-3"}, got)
	require.Zero(t, service.waiting.Load())
}

func TestPrepareUpscaleSourceBase64RejectsDecodedByteLimit(t *testing.T) {
	_, err := prepareUpscaleSourceBase64(base64.StdEncoding.EncodeToString(make([]byte, 17)), 16)
	upscaleErr := requireImageUpscaleError(t, err, "SOURCE_LIMIT_EXCEEDED")
	require.False(t, upscaleErr.Temporary)
}

func TestImageUpscaleRejectsSourceDimensionAndPixelLimits(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
	}{
		{name: "dimension", width: imageUpscaleMaxSourceDimension + 1, height: 1},
		{name: "pixels", width: imageUpscaleMaxSourceDimension, height: imageUpscaleMaxSourceDimension},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := imageUpscaleTestPNG(t, tt.width, tt.height)
			_, _, _, err := validateUpscaleSource(source)
			_ = requireImageUpscaleError(t, err, "SOURCE_LIMIT_EXCEEDED")
		})
	}
}

func TestImageUpscaleAdmissionPrecedesSourceLoader(t *testing.T) {
	cfg := imageUpscaleTestConfig("http://upscale.test")
	cfg.MaxQueue = 0
	service := newImageUpscaleTestService(cfg, &http.Client{}, imageUpscaleTestStaticLoader)
	service.slots <- struct{}{}
	defer func() { <-service.slots }()

	var loaderCalls atomic.Int32
	_, err := service.upscaleBase64FromLoader(context.Background(), 2, func(context.Context) (string, error) {
		loaderCalls.Add(1)
		return base64.StdEncoding.EncodeToString(imageUpscaleTestPNG(t, 1, 1)), nil
	})
	_ = requireImageUpscaleError(t, err, "BACKPRESSURE")
	require.Zero(t, loaderCalls.Load())
}

func imageUpscaleTestConfig(baseURL string) config.ImageUpscaleConfig {
	return config.ImageUpscaleConfig{
		Enabled:            true,
		BaseURL:            baseURL,
		APIKeyVaultRef:     "vault://image/upscale#token",
		VaultAgentSocket:   imageUpscaleVaultSocket,
		JobTimeoutSeconds:  2,
		PollIntervalMillis: 1,
		RetryMax:           0,
		MaxConcurrent:      1,
		MaxQueue:           1,
		MaxResultBytes:     1 << 20,
	}
}

func newImageUpscaleTestService(cfg config.ImageUpscaleConfig, client *http.Client, loader imageUpscaleAPIKeyLoader) *ImageUpscaleService {
	return &ImageUpscaleService{
		cfg:        cfg,
		httpClient: client,
		loadAPIKey: loader,
		slots:      make(chan struct{}, cfg.MaxConcurrent),
	}
}

func imageUpscaleTestStaticLoader(context.Context) ([]byte, error) {
	return []byte("test-upscale-key"), nil
}

func imageUpscaleTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, image.NewRGBA(image.Rect(0, 0, width, height))))
	return data.Bytes()
}

func imageUpscaleTestWriteJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func requireImageUpscaleError(t *testing.T, err error, wantCode string) *ImageUpscaleError {
	t.Helper()
	var upscaleErr *ImageUpscaleError
	require.ErrorAs(t, err, &upscaleErr)
	require.Equal(t, wantCode, upscaleErr.Code)
	return upscaleErr
}
