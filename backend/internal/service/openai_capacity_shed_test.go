package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// --- mock: 只记录临时不可调度写入，其余方法不应被调用 ---

type capacityShedAccountRepoStub struct {
	AccountRepository // 嵌入接口，未实现的方法会 panic（不应被调用）

	tempUnschedCalls int
	overloadCalls    int
	overloadUntil    time.Time
}

func (r *capacityShedAccountRepoStub) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, _ string) error {
	r.tempUnschedCalls++
	return nil
}

func (r *capacityShedAccountRepoStub) SetOverloaded(_ context.Context, _ int64, until time.Time) error {
	r.overloadCalls++
	r.overloadUntil = until
	return nil
}

// 上游容量降载是请求级信号：故障因素（客户端身份、模型容量）与账号无关，
// 同账号重试用尽后不得把账号临时摘掉——否则一个被降载的请求会顺着 failover
// 把整池账号逐个封禁，而每个账号都会以同一个错误失败。
func TestTempUnscheduleRetryableErrorSkipsRequestScopedTransient(t *testing.T) {
	t.Run("请求级瞬时故障不写账号状态", func(t *testing.T) {
		repo := &capacityShedAccountRepoStub{}
		svc := &GatewayService{accountRepo: repo}

		svc.TempUnscheduleRetryableError(context.Background(), 1, &UpstreamFailoverError{
			StatusCode:             http.StatusBadGateway,
			RetryableOnSameAccount: true,
			RequestScopedTransient: true,
		})

		require.Zero(t, repo.tempUnschedCalls)
	})

	// 对照组：同样的 502 在未标记请求级瞬时故障时仍按原有语义临时摘号，
	// 确认上面的断言来自新增守卫而非其他前置条件。
	t.Run("未标记时保持原有临时摘号语义", func(t *testing.T) {
		repo := &capacityShedAccountRepoStub{}
		svc := &GatewayService{accountRepo: repo}

		svc.TempUnscheduleRetryableError(context.Background(), 1, &UpstreamFailoverError{
			StatusCode:             http.StatusBadGateway,
			RetryableOnSameAccount: true,
		})

		require.Equal(t, 1, repo.tempUnschedCalls)
	})
}

// 非池模式账号同样要先在同账号重试：换号不改变降载因素。
func TestStreamFailedEventCapacityShedRetriesOnSameAccount(t *testing.T) {
	nonPool := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	for _, code := range []string{"server_is_overloaded", "slow_down"} {
		payload := []byte(`{"type":"response.failed","response":{"error":{"code":"` + code + `"}}}`)
		require.True(t, isOpenAIUpstreamCapacityShedEvent(payload), code)
		require.True(t, openAIStreamFailedEventRetryableOnSameAccount(nonPool, payload, "overloaded"), code)
	}

	// 非降载的 failed 事件在非池模式下仍不做同账号重试，避免放大改动面。
	other := []byte(`{"type":"response.failed","response":{"error":{"code":"server_error"}}}`)
	require.False(t, isOpenAIUpstreamCapacityShedEvent(other))
	require.False(t, openAIStreamFailedEventRetryableOnSameAccount(nonPool, other, "boom"))
}

func TestCapacityShedDoesNotPenalizeSchedulerHealthBeforeCooldown(t *testing.T) {
	require.False(t, (&UpstreamFailoverError{
		RequestScopedTransient: true,
	}).ShouldReportAccountScheduleFailure())
	require.True(t, (&UpstreamFailoverError{}).ShouldReportAccountScheduleFailure())
}

func TestOAuth529RetriesBeforeFailover(t *testing.T) {
	account := &Account{Type: AccountTypeOAuth}
	svc := &GatewayService{}
	require.True(t, svc.shouldRetryUpstreamError(account, 529), "529 overload should receive bounded same-account retries")
	require.True(t, svc.shouldRetryUpstreamError(account, http.StatusForbidden))
	require.False(t, svc.shouldRetryUpstreamError(account, http.StatusTooManyRequests))
}

func TestOpenAICapacityMessageIsRequestScopedAcrossHTTPAndStreamForms(t *testing.T) {
	body := []byte(`{"error":{"message":"Selected model is at capacity. Please try a different model.","type":"invalid_request_error"}}`)
	require.True(t, isOpenAICapacityShedError(http.StatusBadRequest, "", body))
	require.True(t, isOpenAIUpstreamCapacityShedEvent(body))
	require.Equal(t, http.StatusServiceUnavailable, openAIStreamFailedEventSemanticStatus(body, ""))

	failoverErr := newOpenAIUpstreamFailoverError(
		http.StatusBadRequest,
		http.Header{},
		body,
		"Selected model is at capacity. Please try a different model.",
		true,
	)
	require.True(t, failoverErr.RequestScopedTransient)
	require.True(t, failoverErr.RetryableOnSameAccount)
}

func TestOpenAICapacityMessageEnablesRetryForOAuthAccount(t *testing.T) {
	body := []byte(`{"error":{"message":"Selected model is at capacity. Please try a different model.","type":"invalid_request_error"}}`)
	account := &Account{ID: 99, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	require.True(t, isOpenAICapacityShedError(http.StatusBadRequest, "", body))
	require.True(t, openAIStreamFailedEventRetryableOnSameAccount(account, body, "Selected model is at capacity. Please try a different model."))
}

func TestOpenAI529UsesCapacityShedRetryThenRepeatedFailureCooldown(t *testing.T) {
	repo := &capacityShedAccountRepoStub{}
	cfg := &config.Config{}
	cfg.RateLimit.OverloadCooldownMinutes = 6
	rateLimitService := NewRateLimitService(repo, nil, cfg, nil, nil)
	svc := &OpenAIGatewayService{
		cfg:                cfg,
		rateLimitService:   rateLimitService,
		openaiCapacityShed: newOpenAICapacityShedState(8),
	}
	rateLimitService.SetAccountRuntimeBlocker(svc)
	account := &Account{ID: 101, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	require.True(t, isOpenAICapacityShedError(529, "", nil))
	require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, 529, http.Header{}, nil))
	require.Zero(t, repo.overloadCalls, "the first 529 must not skip bounded same-account retries")

	failoverErr := newOpenAIUpstreamFailoverError(529, http.Header{}, nil, "", true)
	require.True(t, failoverErr.RequestScopedTransient)
	require.True(t, failoverErr.RetryableOnSameAccount)
	for i := 0; i < openAICapacityShedThreshold; i++ {
		svc.RecordOpenAICapacityShedRetryExhausted(context.Background(), account, failoverErr)
	}
	require.Equal(t, 1, repo.overloadCalls)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestNonStreamingSSECapacityFailureReturnsRetryableFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{ID: 1302, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	body := []byte("data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"type\":\"invalid_request_error\",\"message\":\"Selected model is at capacity. Please try a different model.\"}}}\n\ndata: [DONE]\n")

	for _, tc := range []struct {
		name string
		call func(*OpenAIGatewayService, *http.Response, *gin.Context, []byte, *Account) (*UpstreamFailoverError, error)
	}{
		{
			name: "responses",
			call: func(svc *OpenAIGatewayService, resp *http.Response, c *gin.Context, payload []byte, account *Account) (*UpstreamFailoverError, error) {
				_, err := svc.handleSSEToJSON(resp, c, payload, "gpt-5.6", "gpt-5.6", account)
				var failoverErr *UpstreamFailoverError
				require.ErrorAs(t, err, &failoverErr)
				return failoverErr, err
			},
		},
		{
			name: "passthrough",
			call: func(svc *OpenAIGatewayService, resp *http.Response, c *gin.Context, payload []byte, account *Account) (*UpstreamFailoverError, error) {
				_, err := svc.handlePassthroughSSEToJSON(resp, c, payload, "gpt-5.6", "gpt-5.6", account)
				var failoverErr *UpstreamFailoverError
				require.ErrorAs(t, err, &failoverErr)
				return failoverErr, err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			}
			svc := &OpenAIGatewayService{cfg: &config.Config{}}

			failoverErr, err := tc.call(svc, resp, c, body, account)
			require.Error(t, err)
			require.True(t, failoverErr.RequestScopedTransient)
			require.True(t, failoverErr.RetryableOnSameAccount)
			require.Empty(t, recorder.Body.String(), "a pre-output capacity failure must remain retryable")
		})
	}
}

func TestOpenAIWSV2CapacityBeforeOutputReturnsRetryableFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0")

	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0

	captureConn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_capacity_1","model":"gpt-5.6"}}`),
		[]byte(`{"type":"response.failed","response":{"id":"resp_capacity_1","error":{"type":"invalid_request_error","message":"Selected model is at capacity. Please try a different model."}}}`),
	}}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: captureConn})

	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}
	account := &Account{
		ID:          1303,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
	}

	_, err := svc.Forward(
		context.Background(),
		c,
		account,
		[]byte(`{"model":"gpt-5.6","stream":true,"input":"hello"}`),
	)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.True(t, failoverErr.RequestScopedTransient)
	require.Empty(t, recorder.Body.String(), "preamble is buffered so the handler can retry without splicing streams")
}

func TestOpenAIWSV2CapacityAfterOutputIsObservableAndCounted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0")

	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0

	captureConn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_capacity_partial","model":"gpt-5.6"}}`),
		[]byte(`{"type":"response.output_text.delta","response_id":"resp_capacity_partial","delta":"partial sentence"}`),
		[]byte(`{"type":"response.failed","response":{"id":"resp_capacity_partial","error":{"type":"invalid_request_error","message":"Selected model is at capacity. Please try a different model."}}}`),
	}}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: captureConn})

	svc := &OpenAIGatewayService{
		cfg:                  cfg,
		httpUpstream:         &httpUpstreamRecorder{},
		cache:                &stubGatewayCache{},
		openaiWSResolver:     NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:        NewCodexToolCorrector(),
		openaiWSPool:         pool,
		openaiCapacityShed:   newOpenAICapacityShedState(8),
		openaiModelTransient: newOpenAIAccountModelTransientState(8),
	}
	account := &Account{
		ID:          1304,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
	}

	result, err := svc.Forward(
		context.Background(),
		c,
		account,
		[]byte(`{"model":"gpt-5.6","stream":true,"input":"hello"}`),
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "response.failed", result.UpstreamTerminalEvent)
	require.Contains(t, recorder.Body.String(), "partial sentence")
	require.Contains(t, recorder.Body.String(), "Selected model is at capacity")

	streamErr, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.True(t, streamErr.CountTowardsSLA)
	require.Equal(t, http.StatusServiceUnavailable, streamErr.IntendedStatus)
	require.Equal(t, 1, svc.getOpenAICapacityShedState().failureStreak(account.ID, time.Now()))
}

func TestOpenAICapacityShedStateTripsOnlyAfterRepeatedLogicalFailures(t *testing.T) {
	state := newOpenAICapacityShedState(8)
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	first := state.recordFailure(42, now)
	// A successful request between two intermittent capacity failures must not
	// erase the recent evidence; otherwise the user can see repeated breaks
	// across several turns without ever reaching the cooldown threshold.
	state.recordSuccess(42)
	second := state.recordFailure(42, now.Add(5*time.Minute))
	third := state.recordFailure(42, now.Add(10*time.Minute))
	fourth := state.recordFailure(42, now.Add(11*time.Minute))

	require.Equal(t, 1, first.FailureCount)
	require.False(t, first.TripCooldown)
	require.Equal(t, 2, second.FailureCount)
	require.False(t, second.TripCooldown)
	require.Equal(t, 3, third.FailureCount)
	require.True(t, third.TripCooldown)
	require.Equal(t, 3, fourth.FailureCount, "the rolling state only retains the threshold-sized failure sample")
	require.False(t, fourth.TripCooldown, "an already-tripped streak must not keep extending the persisted cooldown")

	state.recordSuccess(42)
	require.Equal(t, 3, state.failureStreak(42, now.Add(11*time.Minute)), "a success must not clear failures still inside the rolling window")

	afterWindow := state.recordFailure(42, now.Add(openAICapacityShedFailureWindow+12*time.Minute))
	require.Equal(t, 1, afterWindow.FailureCount)
	require.False(t, afterWindow.TripCooldown)
}

func TestRecordOpenAICapacityShedRetryExhaustedPersistsConfiguredCooldown(t *testing.T) {
	repo := &capacityShedAccountRepoStub{}
	cfg := &config.Config{}
	cfg.RateLimit.OverloadCooldownMinutes = 6
	rateLimitService := NewRateLimitService(repo, nil, cfg, nil, nil)
	svc := &OpenAIGatewayService{
		cfg:                  cfg,
		rateLimitService:     rateLimitService,
		openaiCapacityShed:   newOpenAICapacityShedState(8),
		openaiModelTransient: newOpenAIAccountModelTransientState(8),
	}
	rateLimitService.SetAccountRuntimeBlocker(svc)
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	failoverErr := &UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		RetryableOnSameAccount: true,
		RequestScopedTransient: true,
	}

	before := time.Now()
	for i := 0; i < openAICapacityShedThreshold; i++ {
		svc.RecordOpenAICapacityShedRetryExhausted(context.Background(), account, failoverErr)
	}

	require.Equal(t, 1, repo.overloadCalls)
	require.WithinDuration(t, before.Add(6*time.Minute), repo.overloadUntil, 2*time.Second)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account), "runtime scheduler must stop before repository snapshots refresh")
}

func TestOpenAIStreamingPassthroughCapacityShedAfterOutputIsObservableAndCountsOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &OpenAIGatewayService{
		cfg:                cfg,
		openaiCapacityShed: newOpenAICapacityShedState(8),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	account := &Account{ID: 77, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Name: "pro-account"}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"rid-partial-overload"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"partial sentence"}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"error":{"type":"invalid_request_error","code":"server_is_overloaded","message":"Please retry later."}}}`,
			"",
		}, "\n"))),
	}

	_, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.6", "gpt-5.6")
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "output already reached the client, so replay must not splice a second stream")
	require.Contains(t, recorder.Body.String(), "partial sentence")
	require.Contains(t, recorder.Body.String(), "response.failed")

	eventsValue, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := eventsValue.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, http.StatusServiceUnavailable, events[0].UpstreamStatusCode)
	require.Equal(t, "request_error", events[0].Kind)

	streamErr, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.True(t, streamErr.CountTowardsSLA)
	require.Equal(t, http.StatusServiceUnavailable, streamErr.IntendedStatus)
	require.Equal(t, "server_is_overloaded", streamErr.Code)
	require.Equal(t, 1, svc.getOpenAICapacityShedState().failureStreak(account.ID, time.Now()))
}

// 出站身份的版本声明只能有一个来源：UA 的版本段、version 头、探针版本三处必须同源，
// 各自硬编码会漂移成互相矛盾的身份，而自相矛盾或陈旧的身份会被上游优先降载。
func TestCodexOutboundVersionHasSingleSource(t *testing.T) {
	require.True(t,
		strings.HasPrefix(codexCLIUserAgent, "codex_cli_rs/"+codexCLIVersion+" "),
		"codexCLIUserAgent=%q 必须以 codexCLIVersion=%q 作为版本段", codexCLIUserAgent, codexCLIVersion,
	)
	require.Equal(t, codexCLIVersion, openAICodexProbeVersion)
	require.GreaterOrEqual(t, CompareVersions(codexCLIVersion, codexUpstreamMinVersion), 0,
		"codexCLIVersion=%q 不得低于上游最低门槛 %q", codexCLIVersion, codexUpstreamMinVersion,
	)
}
