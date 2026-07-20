package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	attachmentgateway "github.com/Wei-Shaw/sub2api/internal/service/attachment_gateway"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type fakeResponsesAttachmentOptimizer struct {
	enabled bool
	result  attachmentgateway.Result
	calls   int
}

func (f *fakeResponsesAttachmentOptimizer) Enabled() bool { return f.enabled }

func (f *fakeResponsesAttachmentOptimizer) Optimize(_ context.Context, _ []byte) attachmentgateway.Result {
	f.calls++
	return f.result
}

func TestResponsesAttachmentOptimizerDisabledByDefault(t *testing.T) {
	optimizer := newResponsesAttachmentOptimizer(&config.Config{})
	require.NotNil(t, optimizer)
	require.False(t, optimizer.Enabled())

	body := []byte("{ \"model\": \"gpt-test\" }\n")
	handler := &OpenAIGatewayHandler{attachmentOptimizer: optimizer}
	require.Equal(t, body, handler.optimizeResponsesAttachments(context.Background(), zap.NewNop(), nil, body))
}

func TestOptimizeResponsesAttachmentsUsesOnlyPrivacySafeLogFields(t *testing.T) {
	secretBody := []byte(`{"model":"gpt-test","input":"TOP_SECRET_BASE64"}`)
	optimizedBody := []byte(`{"model":"gpt-test","input":"optimized"}`)
	fake := &fakeResponsesAttachmentOptimizer{
		enabled: true,
		result: attachmentgateway.Result{
			Body: optimizedBody,
			Metrics: attachmentgateway.Metrics{
				Enabled:             true,
				OriginalBodyBytes:   len(secretBody),
				OptimizedBodyBytes:  len(optimizedBody),
				ImageCount:          1,
				OptimizedImageCount: 1,
				OriginalImageBytes:  1024,
				OptimizedImageBytes: 256,
				CacheHit:            true,
				CacheHits:           1,
				OptimizeDurationMS:  12.5,
			},
		},
	}
	core, observed := observer.New(zap.InfoLevel)
	requestLog := zap.New(core)
	handler := &OpenAIGatewayHandler{
		attachmentOptimizer: fake,
		cfg: attachmentGatewayHandlerTestConfig(config.AttachmentGatewayConfig{
			AttachmentOptimizerEnabled:  true,
			AllowUnscoped:               true,
			OptimizeTimeoutMilliseconds: 1000,
		}),
	}

	result := handler.optimizeResponsesAttachments(context.Background(), requestLog, &service.APIKey{ID: 1}, secretBody)

	require.Equal(t, optimizedBody, result)
	require.Equal(t, 1, fake.calls)
	entries := observed.All()
	require.Len(t, entries, 1)
	require.Equal(t, "openai.attachment_gateway_experiment", entries[0].Message)
	require.NotContains(t, entries[0].ContextMap(), "body")
	require.NotContains(t, entries[0].ContextMap(), "image_data")
	require.NotContains(t, entries[0].ContextMap(), "base64")
	require.NotContains(t, entries[0].ContextMap(), "hash")
	require.NotContains(t, entries[0].Message, "TOP_SECRET_BASE64")
}

func TestOptimizeResponsesAttachmentsSkipsCallWhenGateIsOff(t *testing.T) {
	body := []byte(`{"model":"gpt-test"}`)
	fake := &fakeResponsesAttachmentOptimizer{enabled: false}
	handler := &OpenAIGatewayHandler{attachmentOptimizer: fake}

	result := handler.optimizeResponsesAttachments(context.Background(), zap.NewNop(), nil, body)

	require.Equal(t, body, result)
	require.Zero(t, fake.calls)
}

type attachmentGatewayCaptureUpstream struct {
	requestBody []byte
}

func (u *attachmentGatewayCaptureUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	requestBody, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	u.requestBody = append([]byte(nil), requestBody...)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_attachment_test","object":"response","model":"gpt-test","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
		)),
	}, nil
}

func (u *attachmentGatewayCaptureUpstream) DoWithTLS(
	request *http.Request,
	proxyURL string,
	accountID int64,
	accountConcurrency int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	return u.Do(request, proxyURL, accountID, accountConcurrency)
}

func TestResponsesAttachmentGatewayLocalForwardIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(6120)
	account := service.Account{
		ID:          6121,
		Name:        "attachment-local-upstream",
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
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.AttachmentGateway = config.AttachmentGatewayConfig{
		AttachmentOptimizerEnabled:  true,
		AttachmentOptimizerDryRun:   false,
		AllowUnscoped:               true,
		OptimizeTimeoutMilliseconds: 5000,
		ThresholdBytes:              1,
		MaxImageBytes:               64 * 1024 * 1024,
		MaxPixels:                   50_000_000,
		Quality:                     85,
		SpecialQuality:              90,
		MinSavingsRatio:             0.01,
		CacheDir:                    t.TempDir(),
		CacheTTLSeconds:             3600,
		MaxImagesPerRequest:         5,
		MaxConcurrentEncodes:        1,
	}

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
		nil,
		nil,
		nil,
		nil,
		cfg,
	)

	imageData := localIntegrationPNG(t)
	payload := `{"model":"gpt-test","stream":false,"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(imageData) + `","detail":"high"}]}]}`
	recorder := httptest.NewRecorder()
	requestContext, _ := gin.CreateTestContext(recorder)
	requestContext.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(payload))
	requestContext.Request.Header.Set("Content-Type", "application/json")
	requestContext.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID:      6122,
		GroupID: &groupID,
		User:    &service.User{ID: 6123, Status: service.StatusActive},
		Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
	})
	requestContext.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 6123, Concurrency: 1})

	handler.Responses(requestContext)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, string(upstream.requestBody), "data:image/webp;base64,")
	require.NotContains(t, string(upstream.requestBody), "data:image/png;base64,")
	require.Contains(t, string(upstream.requestBody), `"detail":"high"`)
}

func TestResponsesAttachmentOptimizerScopeIsFailClosed(t *testing.T) {
	groupID := int64(44)
	apiKey := &service.APIKey{ID: 33, UserID: 22, GroupID: &groupID}
	tests := []struct {
		name       string
		experiment config.AttachmentGatewayConfig
		wantCalls  int
	}{
		{
			name: "empty scope",
			experiment: config.AttachmentGatewayConfig{
				AttachmentOptimizerEnabled: true,
			},
		},
		{
			name: "unmatched scope",
			experiment: config.AttachmentGatewayConfig{
				AttachmentOptimizerEnabled: true,
				AllowedAPIKeyIDs:           []int64{99},
				AllowedGroupIDs:            []int64{88},
			},
		},
		{
			name: "api key scope",
			experiment: config.AttachmentGatewayConfig{
				AttachmentOptimizerEnabled: true,
				AllowedAPIKeyIDs:           []int64{apiKey.ID},
			},
			wantCalls: 1,
		},
		{
			name: "user scope",
			experiment: config.AttachmentGatewayConfig{
				AttachmentOptimizerEnabled: true,
				AllowedUserIDs:             []int64{apiKey.UserID},
			},
			wantCalls: 1,
		},
		{
			name: "group scope",
			experiment: config.AttachmentGatewayConfig{
				AttachmentOptimizerEnabled: true,
				AllowedGroupIDs:            []int64{groupID},
			},
			wantCalls: 1,
		},
		{
			name: "explicit unscoped rollout",
			experiment: config.AttachmentGatewayConfig{
				AttachmentOptimizerEnabled: true,
				AllowUnscoped:              true,
			},
			wantCalls: 1,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.experiment.OptimizeTimeoutMilliseconds = 1000
			fake := &fakeResponsesAttachmentOptimizer{
				enabled: true,
				result:  attachmentgateway.Result{Body: []byte(`{"model":"optimized"}`)},
			}
			handler := &OpenAIGatewayHandler{
				attachmentOptimizer: fake,
				cfg:                 attachmentGatewayHandlerTestConfig(testCase.experiment),
			}
			body := []byte(`{"model":"original"}`)

			result := handler.optimizeResponsesAttachments(context.Background(), zap.NewNop(), apiKey, body)

			require.Equal(t, testCase.wantCalls, fake.calls)
			if testCase.wantCalls == 0 {
				require.Equal(t, body, result)
			} else {
				require.JSONEq(t, `{"model":"optimized"}`, string(result))
			}
		})
	}
}

func TestResponsesAttachmentOptimizerDryRunMeasuresButForwardsOriginal(t *testing.T) {
	body := []byte(`{"model":"original"}`)
	optimized := []byte(`{"model":"optimized"}`)
	fake := &fakeResponsesAttachmentOptimizer{
		enabled: true,
		result: attachmentgateway.Result{
			Body: optimized,
			Metrics: attachmentgateway.Metrics{
				Enabled:            true,
				OriginalBodyBytes:  len(body),
				OptimizedBodyBytes: len(optimized),
				ImageCount:         1,
			},
		},
	}
	core, observed := observer.New(zap.InfoLevel)
	handler := &OpenAIGatewayHandler{
		attachmentOptimizer: fake,
		cfg: attachmentGatewayHandlerTestConfig(config.AttachmentGatewayConfig{
			AttachmentOptimizerEnabled:  true,
			AttachmentOptimizerDryRun:   true,
			AllowUnscoped:               true,
			OptimizeTimeoutMilliseconds: 1000,
		}),
	}

	result := handler.optimizeResponsesAttachments(context.Background(), zap.New(core), &service.APIKey{ID: 1}, body)

	require.Equal(t, body, result)
	require.Equal(t, 1, fake.calls)
	entries := observed.All()
	require.Len(t, entries, 1)
	require.Equal(t, true, entries[0].ContextMap()["dry_run"])
	require.Equal(t, false, entries[0].ContextMap()["payload_rewritten"])
	require.EqualValues(t, len(body), entries[0].ContextMap()["forward_body_bytes"])
}

func TestResponsesAttachmentOptimizerRolloutControlSwitchesLiveAndFailsClosed(t *testing.T) {
	body := []byte(`{"model":"original"}`)
	optimized := []byte(`{"model":"optimized"}`)
	controlFile := filepath.Join(t.TempDir(), "attachment-gateway.mode")
	fake := &fakeResponsesAttachmentOptimizer{
		enabled: true,
		result: attachmentgateway.Result{
			Body: optimized,
			Metrics: attachmentgateway.Metrics{
				Enabled:            true,
				OriginalBodyBytes:  len(body),
				OptimizedBodyBytes: len(optimized),
			},
		},
	}
	handler := &OpenAIGatewayHandler{
		attachmentOptimizer: fake,
		cfg: attachmentGatewayHandlerTestConfig(config.AttachmentGatewayConfig{
			AttachmentOptimizerEnabled:  true,
			AttachmentOptimizerDryRun:   false,
			RolloutControlFile:          controlFile,
			AllowUnscoped:               true,
			OptimizeTimeoutMilliseconds: 1000,
		}),
	}

	assertMode := func(name, content string, remove bool, wantCalls int, wantBody []byte) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			if remove {
				require.NoError(t, os.Remove(controlFile))
			} else {
				require.NoError(t, os.WriteFile(controlFile, []byte(content), 0o600))
			}
			fake.calls = 0
			got := handler.optimizeResponsesAttachments(context.Background(), zap.NewNop(), &service.APIKey{ID: 1}, body)
			require.Equal(t, wantCalls, fake.calls)
			require.Equal(t, wantBody, got)
		})
	}

	// Missing and malformed controls never fall back to rewrite.
	require.NoError(t, os.WriteFile(controlFile, []byte("off"), 0o600))
	assertMode("missing", "", true, 0, body)
	assertMode("invalid", "unexpected", false, 0, body)
	assertMode("oversized", strings.Repeat("x", attachmentGatewayControlMaxBytes+1), false, 0, body)
	assertMode("dry run", "dry_run\n", false, 1, body)
	assertMode("rewrite", "rewrite\n", false, 1, optimized)
	assertMode("off", "off\n", false, 0, body)
}

type deadlineResponsesAttachmentOptimizer struct {
	calls int
}

func (o *deadlineResponsesAttachmentOptimizer) Enabled() bool { return true }

func (o *deadlineResponsesAttachmentOptimizer) Optimize(ctx context.Context, body []byte) attachmentgateway.Result {
	o.calls++
	<-ctx.Done()
	return attachmentgateway.Result{
		Body: []byte(`{"model":"must-not-forward"}`),
		Metrics: attachmentgateway.Metrics{
			Enabled:            true,
			OriginalBodyBytes:  len(body),
			OptimizedBodyBytes: len(body) / 2,
		},
	}
}

func TestResponsesAttachmentOptimizerTimeoutFailsOpen(t *testing.T) {
	body := []byte(`{"model":"original"}`)
	optimizer := &deadlineResponsesAttachmentOptimizer{}
	core, observed := observer.New(zap.WarnLevel)
	handler := &OpenAIGatewayHandler{
		attachmentOptimizer: optimizer,
		cfg: attachmentGatewayHandlerTestConfig(config.AttachmentGatewayConfig{
			AttachmentOptimizerEnabled:  true,
			AllowUnscoped:               true,
			OptimizeTimeoutMilliseconds: 10,
		}),
	}
	started := time.Now()

	result := handler.optimizeResponsesAttachments(context.Background(), zap.New(core), &service.APIKey{ID: 1}, body)

	require.Equal(t, body, result)
	require.Equal(t, 1, optimizer.calls)
	require.Less(t, time.Since(started), time.Second)
	entries := observed.All()
	require.Len(t, entries, 1)
	require.Equal(t, true, entries[0].ContextMap()["timed_out"])
	require.Equal(t, false, entries[0].ContextMap()["payload_rewritten"])
}

func attachmentGatewayHandlerTestConfig(experiment config.AttachmentGatewayConfig) *config.Config {
	return &config.Config{Gateway: config.GatewayConfig{AttachmentGateway: experiment}}
}

func TestResponsesAttachmentGatewayWebSocketLocalForwardMatrix(t *testing.T) {
	imageData := localIntegrationPNG(t)
	payload := `{"type":"response.create","model":"gpt-5.4","stream":false,"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(imageData) + `","detail":"high"}]}]}`
	for _, mode := range []string{
		service.OpenAIWSIngressModePassthrough,
		service.OpenAIWSIngressModeCtxPool,
	} {
		for _, dryRun := range []bool{true, false} {
			name := mode + "/rewrite"
			if dryRun {
				name = mode + "/dry_run"
			}
			t.Run(name, func(t *testing.T) {
				experiment := config.AttachmentGatewayConfig{
					AttachmentOptimizerEnabled:  true,
					AttachmentOptimizerDryRun:   dryRun,
					AllowedAPIKeyIDs:            []int64{1801},
					OptimizeTimeoutMilliseconds: 5000,
					ThresholdBytes:              1,
					CacheDir:                    t.TempDir(),
					MinSavingsRatio:             0.01,
					MaxConcurrentEncodes:        1,
				}
				got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
					firstPayload:      payload,
					wsMode:            mode,
					attachmentGateway: &experiment,
				})

				if dryRun {
					require.JSONEq(t, payload, string(got.upstreamFirstPayload))
					require.Contains(t, string(got.upstreamFirstPayload), "data:image/png;base64,")
					require.NotContains(t, string(got.upstreamFirstPayload), "data:image/webp;base64,")
					return
				}
				require.Contains(t, string(got.upstreamFirstPayload), "data:image/webp;base64,")
				require.NotContains(t, string(got.upstreamFirstPayload), "data:image/png;base64,")
				require.Equal(t, "high", gjson.GetBytes(got.upstreamFirstPayload, "input.0.content.0.detail").String())
			})
		}
	}
}

func localIntegrationPNG(t *testing.T) []byte {
	t.Helper()
	fixture := image.NewNRGBA(image.Rect(0, 0, 640, 480))
	state := uint32(20260720)
	nextNoise := func() int {
		state = state*1664525 + 1013904223
		return int((state>>28)&0xf) - 8
	}
	for y := 0; y < fixture.Bounds().Dy(); y++ {
		for x := 0; x < fixture.Bounds().Dx(); x++ {
			fixture.SetNRGBA(x, y, color.NRGBA{
				R: attachmentTestClampByte(x*255/(fixture.Bounds().Dx()-1) + nextNoise()),
				G: attachmentTestClampByte(y*255/(fixture.Bounds().Dy()-1) + nextNoise()),
				B: attachmentTestClampByte((x+y)*127/(fixture.Bounds().Dx()+fixture.Bounds().Dy()-2) + 64 + nextNoise()),
				A: 255,
			})
		}
	}
	var output bytes.Buffer
	require.NoError(t, png.Encode(&output, fixture))
	return output.Bytes()
}

func attachmentTestClampByte(value int) uint8 {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return uint8(value)
}
