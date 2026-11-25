package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type attachmentPlanHandlerStore struct {
	calls  atomic.Int32
	onSave func() error
}

func (s *attachmentPlanHandlerStore) Save(_ context.Context, key, _ string, _ []byte) (string, error) {
	s.calls.Add(1)
	if s.onSave != nil {
		if err := s.onSave(); err != nil {
			return "", err
		}
	}
	return "https://r2.example.test/" + key, nil
}

func newAttachmentPlanHandler(t *testing.T, trigger int, store *attachmentPlanHandlerStore) (*OpenAIGatewayHandler, string) {
	t.Helper()
	control := filepath.Join(t.TempDir(), "rollout")
	require.NoError(t, os.WriteFile(control, []byte("rewrite"), 0o600))
	cfg := attachmentGatewayHandlerTestConfig(config.AttachmentGatewayConfig{
		AttachmentOptimizerEnabled:    true,
		AllowUnscoped:                 true,
		RolloutControlFile:            control,
		OptimizeTimeoutMilliseconds:   5000,
		URLUploadTimeoutMilliseconds:  2000,
		URLRewriteEnabled:             true,
		URLRewriteMinBodyBytes:        trigger,
		URLRewriteMaxImagesPerRequest: 2,
		MaxConcurrentURLUploads:       1,
		MaxConcurrentEncodes:          1,
		ThresholdBytes:                128 << 10,
		CacheDir:                      t.TempDir(),
		MaxImagesPerRequest:           2,
		RequestBudgetEnabled:          true,
		RequestBudgetEnforce:          true,
		MaxInlineAttachmentCount:      5,
		MaxInlineAttachmentBytes:      40 << 20,
		MaxForwardBodyBytes:           40 << 20,
	})
	h := &OpenAIGatewayHandler{cfg: cfg, attachmentOptimizer: newResponsesAttachmentOptimizer(cfg), attachmentURLExternalizer: newResponsesAttachmentURLExternalizer(cfg, store)}
	require.NotNil(t, h.attachmentOptimizer)
	require.NotNil(t, h.attachmentURLExternalizer)
	return h, control
}

func attachmentPlanHandlerBody(t *testing.T, count int) []byte {
	t.Helper()
	// PNG readers ignore trailing bytes. Distinct trailing bytes produce
	// different source cache keys; use two distinct dimensions as well so
	// the optimized byte hashes are different through WebP and R2.
	var body strings.Builder
	_, _ = body.WriteString(` { "n": 900719925474099312345, "input": [`)
	for i := range count {
		if i != 0 {
			_ = body.WriteByte(',')
		}
		png := planHandlerPNG(t, 64+i)
		padded := make([]byte, 5<<20)
		copy(padded, png)
		_, _ = body.WriteString(`{"type":"input_image","image_url":"data:image/png;base64,`)
		_, _ = body.WriteString(base64.StdEncoding.EncodeToString(padded))
		_, _ = body.WriteString(`"}`)
	}
	_, _ = body.WriteString(`], "keep": "hello \u0078 \n" } `)
	return []byte(body.String())
}

func TestResponsesAttachmentPlanBelowTriggerAndDryRun(t *testing.T) {
	body := attachmentPlanHandlerBody(t, 1)
	store := &attachmentPlanHandlerStore{}
	h, control := newAttachmentPlanHandler(t, 512<<10, store)
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)
	run := func(ctx context.Context) responsesAttachmentPreparation {
		return h.prepareResponsesAttachments(ctx, logger, &service.APIKey{ID: 1}, body)
	}
	optimized := run(t.Context())
	require.Nil(t, optimized.BudgetViolation)
	require.Contains(t, string(optimized.Body), "data:image/webp;base64,")
	require.Less(t, len(optimized.Body), 512<<10)
	require.Equal(t, true, logs.TakeAll()[0].ContextMap()["url_skipped_below_trigger"])
	require.Zero(t, store.calls.Load())

	require.NoError(t, os.WriteFile(control, []byte("dry_run"), 0o600))
	dry := run(t.Context())
	require.Equal(t, body, dry.Body)
	fields := logs.TakeAll()[0].ContextMap()
	require.Equal(t, true, fields["dry_run"])
	require.EqualValues(t, len(optimized.Body), fields["optimized_body_bytes"])
	require.Equal(t, false, fields["url_storage_ready"])
	require.Zero(t, store.calls.Load())

	require.NoError(t, os.WriteFile(control, []byte("rewrite"), 0o600))
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.Equal(t, body, run(canceled).Body)
	fields = logs.TakeAll()[0].ContextMap()
	require.Equal(t, false, fields["payload_rewritten"])
	require.Equal(t, false, fields["url_storage_ready"])
	require.Zero(t, store.calls.Load())

	expired, cancelExpired := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancelExpired()
	require.Equal(t, body, run(expired).Body)
	fields = logs.TakeAll()[0].ContextMap()
	require.Equal(t, true, fields["timed_out"])
	require.Equal(t, false, fields["url_storage_ready"])
	require.Zero(t, store.calls.Load())
}

func TestResponsesAttachmentPlanSuppressesWritesAfterLiveRolloutFlip(t *testing.T) {
	store := &attachmentPlanHandlerStore{}
	h, control := newAttachmentPlanHandler(t, 1, store)
	store.onSave = func() error { return os.WriteFile(control, []byte("off"), 0o600) }
	raw := attachmentPlanHandlerBody(t, 2)
	core, observed := observer.New(zap.InfoLevel)
	prepared := h.prepareResponsesAttachments(t.Context(), zap.New(core), &service.APIKey{ID: 1}, raw)
	require.Nil(t, prepared.BudgetViolation)
	require.Equal(t, int32(1), store.calls.Load())
	require.Equal(t, 1, bytes.Count(prepared.Body, []byte("https://r2.example.test/")))
	require.Equal(t, 1, bytes.Count(prepared.Body, []byte("data:image/webp;base64,")))
	fields := observed.TakeAll()[0].ContextMap()
	require.Equal(t, true, fields["url_write_suppressed"])
	require.EqualValues(t, 1, fields["url_externalized_count"])
	require.EqualValues(t, 1, fields["candidate_inline_attachment_count"])
	// Subsequent requests now short-circuit the entire pipeline.
	require.Equal(t, raw, h.prepareResponsesAttachments(t.Context(), zap.NewNop(), &service.APIKey{ID: 1}, raw).Body)
}

func TestResponsesAttachmentPlanUploadTimeoutForwardsInlineCandidate(t *testing.T) {
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	store := &attachmentPlanHandlerStore{onSave: func() error {
		defer close(returned)
		close(entered)
		<-release
		return nil
	}}
	h, _ := newAttachmentPlanHandler(t, 1, store)
	raw := attachmentPlanHandlerBody(t, 1)
	expected := h.attachmentOptimizer.Optimize(t.Context(), raw).Body
	require.NotEqual(t, raw, expected)
	h.cfg.Gateway.AttachmentGateway.URLUploadTimeoutMilliseconds = 200
	core, observed := observer.New(zap.InfoLevel)
	prepared := h.prepareResponsesAttachments(t.Context(), zap.New(core), &service.APIKey{ID: 1}, raw)
	close(release)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("storage work was never admitted")
	}
	<-returned
	require.Equal(t, expected, prepared.Body)
	fields := observed.TakeAll()[0].ContextMap()
	require.Equal(t, true, fields["url_timed_out"])
	require.EqualValues(t, 0, fields["url_externalized_count"])
}

func planHandlerPNG(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestResponsesAttachmentPlanBelowTriggerDoesNotFalseReportURLTimeout(t *testing.T) {
	body := attachmentPlanHandlerBody(t, 1)
	store := &attachmentPlanHandlerStore{}
	h, _ := newAttachmentPlanHandler(t, 512<<10, store)
	h.cfg.Gateway.AttachmentGateway.URLUploadTimeoutMilliseconds = 1
	core, logs := observer.New(zap.InfoLevel)

	prepared := h.prepareResponsesAttachments(t.Context(), zap.New(core), &service.APIKey{ID: 1}, body)

	require.Nil(t, prepared.BudgetViolation)
	require.Contains(t, string(prepared.Body), "data:image/webp;base64,")
	fields := logs.TakeAll()[0].ContextMap()
	require.Equal(t, true, fields["url_skipped_below_trigger"])
	require.Equal(t, false, fields["url_timed_out"])
	require.Equal(t, false, fields["timed_out"])
	require.Zero(t, store.calls.Load())
}

func TestResponsesAttachmentPlanEndToEndHTTPColdAndWarmTurns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(6220)
	account := service.Account{
		ID:          6221,
		Name:        "attachment-plan-http-upstream",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Priority:    1,
		Credentials: map[string]any{
			"api_key":  "sk-local-test",
			"base_url": "https://api.example.test",
		},
		Extra: map[string]any{"openai_passthrough": true},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.OpenAIResponsesMaxForwardBodySize = 64 * 1024 * 1024
	cfg.Gateway.AttachmentGateway = config.AttachmentGatewayConfig{
		AttachmentOptimizerEnabled:    true,
		AttachmentOptimizerDryRun:     false,
		URLRewriteEnabled:             true,
		URLRewriteMinBodyBytes:        1,
		URLRewriteMaxImagesPerRequest: 4,
		URLUploadTimeoutMilliseconds:  5000,
		URLObjectPrefix:               "files/",
		URLCacheTTLSeconds:            3600,
		MaxConcurrentURLUploads:       2,
		AllowUnscoped:                 true,
		OptimizeTimeoutMilliseconds:   5000,
		ThresholdBytes:                1,
		MaxImageBytes:                 64 * 1024 * 1024,
		MaxTotalImageBytesPerRequest:  64 * 1024 * 1024,
		MaxPixels:                     50_000_000,
		Quality:                       85,
		SpecialQuality:                90,
		MinSavingsRatio:               0.01,
		CacheDir:                      t.TempDir(),
		CacheTTLSeconds:               3600,
		CacheMaxBytes:                 64 * 1024 * 1024,
		CacheCleanupIntervalSeconds:   600,
		NegativeCacheTTLSeconds:       3600,
		NegativeCacheMaxEntries:       100,
		MaxImagesPerRequest:           4,
		MaxColdEncodesPerRequest:      4,
		MaxConcurrentEncodes:          2,
	}

	store := &attachmentPlanHandlerStore{}
	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}
	upstream := &attachmentGatewayCaptureUpstream{}
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gatewayService := service.NewOpenAIGatewayService(
		accountRepo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	concurrencyCache := &concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	handler := NewOpenAIGatewayHandler(
		gatewayService,
		service.NewConcurrencyService(concurrencyCache),
		billingCache,
		&service.APIKeyService{},
		nil, nil, nil, nil,
		cfg,
	)
	handler.attachmentURLExternalizer = newResponsesAttachmentURLExternalizer(cfg, store)

	imageData := localIntegrationPNG(t)
	payload := `{"model":"gpt-test","stream":false,"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(imageData) + `","detail":"high"}]}]}`

	for turn := 1; turn <= 3; turn++ {
		recorder := httptest.NewRecorder()
		requestContext, _ := gin.CreateTestContext(recorder)
		requestContext.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(payload))
		requestContext.Request.Header.Set("Content-Type", "application/json")
		requestContext.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
			ID:      6222,
			GroupID: &groupID,
			User:    &service.User{ID: 6223, Status: service.StatusActive},
			Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
		})
		requestContext.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 6223, Concurrency: 1})

		handler.Responses(requestContext)

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Contains(t, string(upstream.requestBody), "https://r2.example.test/files/")
		require.Contains(t, string(upstream.requestBody), ".webp")
		require.NotContains(t, string(upstream.requestBody), "data:image/")
	}
	require.Equal(t, 3, upstream.calls)
	require.Equal(t, int32(1), store.calls.Load(), "warm turns 2 and 3 must reuse published URL without re-uploading")
}
