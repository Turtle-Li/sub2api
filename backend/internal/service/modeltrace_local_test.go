//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type modelTraceRouteUpstream struct {
	queuedHTTPUpstream
	accountIDs []int64
	proxies    []string
}

func (u *modelTraceRouteUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.accountIDs = append(u.accountIDs, id)
	u.proxies = append(u.proxies, proxy)
	return u.queuedHTTPUpstream.DoWithTLS(req, proxy, id, concurrency, profile)
}

func modelTraceCompletion(text string) *http.Response {
	return newJSONResponse(http.StatusOK, fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":20,\"output_tokens\":1000}}}\n\n", text))
}

type modelTraceLocalSettings struct {
	SettingRepository
	samples int
}

func (s *modelTraceLocalSettings) GetValue(context.Context, string) (string, error) {
	return fmt.Sprintf(`{"probe_count":%d,"interval_seconds":0,"reasoning_effort":"low"}`, s.samples), nil
}

func TestModelTraceUsesSelectedAccountPromptMappingAndEffort(t *testing.T) {
	account := &Account{ID: 89, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "private-test-token", "model_mapping": map[string]any{"public-gpt": "gpt-5.4"}}}
	repo := &openAIAccountTestRepo{}
	repo.accountsByID = map[int64]*Account{account.ID: account}
	upstream := &modelTraceRouteUpstream{queuedHTTPUpstream: queuedHTTPUpstream{responses: []*http.Response{modelTraceCompletion("fingerprint output")}}}
	tester := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	collector := &modelTraceUsageCollector{startedAt: time.Now()}
	ctx := context.WithValue(context.Background(), modelTraceUsageKey{}, collector)
	text, err := tester.RunModelTraceProbe(ctx, 89, "public-gpt", "specific challenge", "high")
	require.NoError(t, err)
	require.Equal(t, "fingerprint output", text)
	require.Equal(t, []int64{89}, upstream.accountIDs)
	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(body, "model").String())
	require.Equal(t, "specific challenge", gjson.GetBytes(body, "input.0.content.0.text").String())
	require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
	require.True(t, HTTPUpstreamProbeFromContext(req.Context()))
	require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
	require.Equal(t, "Bearer private-test-token", req.Header.Get("Authorization"))
	require.Equal(t, "/backend-api/codex/responses", collector.endpoint)
	require.Equal(t, 20, collector.tokens.InputTokens)
	require.Equal(t, 1000, collector.tokens.OutputTokens)
	require.NotNil(t, collector.firstTokenMs)
	require.Nil(t, repo.updatedExtra)
}

func TestModelTraceAPIKeyUsesChatCompletionsAndConfiguredProxy(t *testing.T) {
	account := &Account{ID: 90, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-private-test", "base_url": "https://compat.example/v1"},
		Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
		ProxyID:     func() *int64 { v := int64(7); return &v }(),
		Proxy:       &Proxy{ID: 7, Protocol: "http", Host: "proxy.example", Port: 8080, Status: StatusActive},
	}
	repo := &openAIAccountTestRepo{}
	repo.accountsByID = map[int64]*Account{account.ID: account}
	chat := "data: {\"choices\":[{\"delta\":{\"content\":\"" + strings.Repeat("123 ", 260) + "\"}}]}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":260}}\n\n" + "data: [DONE]\n\n"
	upstream := &modelTraceRouteUpstream{queuedHTTPUpstream: queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusOK, chat)}}}
	tester := &AccountTestService{accountRepo: repo, httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{}, cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}}
	collector := &modelTraceUsageCollector{startedAt: time.Now()}
	ctx := context.WithValue(context.Background(), modelTraceUsageKey{}, collector)
	text, err := tester.RunModelTraceProbe(ctx, account.ID, "gpt-5.4", "api-key challenge", "low")
	require.NoError(t, err)
	require.Contains(t, text, "123")
	require.Equal(t, []int64{account.ID}, upstream.accountIDs)
	require.Equal(t, "http://proxy.example:8080", upstream.proxies[0])
	require.Equal(t, "/v1/chat/completions", collector.endpoint)
	require.Equal(t, 12, collector.tokens.InputTokens)
	require.Equal(t, 260, collector.tokens.OutputTokens)
	require.Equal(t, "Bearer sk-private-test", upstream.requests[0].Header.Get("Authorization"))
}

func TestModelTraceIncompleteSamplesAreNotAttributed(t *testing.T) {
	for _, valid := range []int{1, 3} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			account := &Account{ID: 89, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test-token"}}
			account.Extra = map[string]any{ModelTraceExtraKey: &ModelTraceAccountConfig{Models: map[string]*ModelTraceModelConfig{"gpt-5.4": {}}}}
			repo := &openAIAccountTestRepo{}
			repo.accountsByID = map[int64]*Account{account.ID: account}
			upstream := &queuedHTTPUpstream{}
			for i := 0; i < 6; i++ {
				text := "refused"
				if i < valid {
					text = strings.Repeat("123 ", 332)
				}
				upstream.responses = append(upstream.responses, modelTraceCompletion(text))
			}
			tester := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
			svc := NewModelTraceService(repo, &modelTraceLocalSettings{samples: 3}, tester, nil)
			defer svc.Stop()
			result := svc.executeProbe(context.Background(), account, "gpt-5.4")
			if valid == 1 {
				require.Contains(t, result.Error, "incomplete samples: 1/3")
				require.Equal(t, "Unknown", result.DetectedModel)
				require.Zero(t, result.Probability)
				require.Len(t, upstream.requests, 6)
			} else {
				require.Empty(t, result.Error)
				require.NotEmpty(t, result.DetectedModel)
				require.Len(t, upstream.requests, 3)
			}
		})
	}
}

func TestModelTraceErrorsDoNotExposeProviderSecrets(t *testing.T) {
	account := &Account{ID: 89, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "private-test-token"}}
	repo := &openAIAccountTestRepo{}
	repo.accountsByID = map[int64]*Account{89: account}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(401, `{"error":{"message":"private-test-token https://user:password@host"}}`)}}
	tester := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	_, err := tester.RunModelTraceProbe(context.Background(), 89, "gpt-5.4", "challenge", "low")
	require.ErrorContains(t, err, "HTTP 401")
	require.NotContains(t, err.Error(), "private-test-token")
	require.NotContains(t, err.Error(), "password")
	require.Zero(t, repo.setErrorID)
}

func TestModelTraceGlobalConcurrencyLimitAndCancellation(t *testing.T) {
	svc := NewModelTraceService(nil, nil, nil, nil)
	defer svc.Stop()
	for i := int64(1); i <= modelTraceConcurrency; i++ {
		unlock, err := svc.acquireAccountLock(context.Background(), i)
		require.NoError(t, err)
		defer unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := svc.acquireAccountLock(ctx, 99)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Len(t, svc.slots, modelTraceConcurrency)
	lock, _ := svc.accountLocks.Load(int64(99))
	require.Empty(t, lock.(chan struct{}))
}

func TestModelTraceRejectsOtherPlatformsBeforeRequests(t *testing.T) {
	account := &Account{ID: 89, Platform: PlatformGemini, Type: AccountTypeAPIKey}
	repo := &openAIAccountTestRepo{}
	repo.accountsByID = map[int64]*Account{89: account}
	upstream := &queuedHTTPUpstream{}
	tester := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	_, err := tester.RunModelTraceProbe(context.Background(), 89, "gemini-2.5-pro", "challenge", "low")
	require.Error(t, err)
	require.Empty(t, upstream.requests)
	svc := NewModelTraceService(repo, nil, tester, nil)
	defer svc.Stop()
	require.Error(t, svc.UpdateAccountConfig(context.Background(), 89, UpdateAccountModelTraceRequest{Models: []string{"gemini-2.5-pro"}}))
	require.Nil(t, repo.updatedExtra)

	oauth := &Account{ID: 90, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test-token"}}
	repo.accountsByID[oauth.ID] = oauth
	require.Error(t, svc.UpdateAccountConfig(context.Background(), oauth.ID, UpdateAccountModelTraceRequest{Models: []string{"deepseek-chat"}}))
	require.Nil(t, repo.updatedExtra)
}

func TestModelTraceStopsOnTerminalUpstreamStatus(t *testing.T) {
	account := &Account{ID: 89, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test-token"}}
	repo := &openAIAccountTestRepo{}
	repo.accountsByID = map[int64]*Account{account.ID: account}
	upstream := &queuedHTTPUpstream{}
	for i := 0; i < 6; i++ {
		upstream.responses = append(upstream.responses, newJSONResponse(http.StatusTooManyRequests, `{"error":{"message":"slow down"}}`))
	}
	tester := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	svc := NewModelTraceService(repo, &modelTraceLocalSettings{samples: 3}, tester, nil)
	defer svc.Stop()
	result := svc.executeProbe(context.Background(), account, "gpt-5.4")
	require.Contains(t, result.Error, "HTTP 429")
	require.Len(t, upstream.requests, 1)
	require.Zero(t, repo.setErrorID)
}

func TestModelTraceOmitsReasoningEffortForGPT4Models(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"public-gpt": "gpt-4o"}}}
	require.Empty(t, modelTraceReasoningEffortFor(account, "public-gpt", "low"))
	require.Empty(t, modelTraceReasoningEffortFor(account, "gpt-4.1", "low"))
	require.Equal(t, "low", modelTraceReasoningEffortFor(account, "gpt-5.4", "low"))
}

func TestModelTraceStatusIgnoresTransportPorts(t *testing.T) {
	require.False(t, modelTraceTerminalStatus(fmt.Errorf("Request failed: dial tcp 1.2.3.4:443: i/o timeout")))
	require.Empty(t, modelTraceHTTPStatus.FindStringSubmatch("dial tcp 1.2.3.4:443"))
	require.True(t, modelTraceTerminalStatus(fmt.Errorf("ModelTrace account request failed (HTTP 401)")))
	require.False(t, modelTraceTerminalStatus(fmt.Errorf("ModelTrace account request failed (HTTP 503)")))
}

func TestModelTraceSkipsCoolingDownAccounts(t *testing.T) {
	future := time.Now().Add(time.Minute)
	require.True(t, modelTraceCoolingDown(&Account{RateLimitResetAt: &future}))
	require.True(t, modelTraceCoolingDown(&Account{TempUnschedulableUntil: &future}))
	require.False(t, modelTraceCoolingDown(&Account{}))
}
