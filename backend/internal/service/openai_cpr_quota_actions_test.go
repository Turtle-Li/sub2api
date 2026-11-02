package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type cprActionServer struct {
	*httptest.Server
	consumeBodies []map[string]string
	refreshCalls  int
}

func newCPRActionServer(t *testing.T, refreshFails bool) *cprActionServer {
	t.Helper()
	srv := &cprActionServer{}
	refreshedAt := time.Now().In(time.FixedZone("CST", 8*3600)).Truncate(time.Second)
	reset5h := refreshedAt.Add(2 * time.Hour).Format("2006-01-02 15:04:05")
	reset7d := refreshedAt.Add(72 * time.Hour).Format("2006-01-02 15:04:05")
	balance := "1250.5"
	account := map[string]any{
		"id": "acct_1", "provider": "openai", "email": "a@example.com", "accountId": "ws-1", "planType": "plus",
		"quota": map[string]any{
			"refreshedAt":  refreshedAt.Format(time.RFC3339),
			"limitReached": false,
			"windows": []map[string]any{
				{"key": "codex:18000s", "limitId": "codex", "windowSeconds": 18000, "usedPercent": 42.0, "limitReached": false, "resetAtDisplay": reset5h},
				{"key": "codex:604800s", "limitId": "codex", "windowSeconds": 604800, "usedPercent": 70.0, "limitReached": false, "resetAtDisplay": reset7d},
				{"key": "codex_bengalfox:18000s", "limitId": "codex_bengalfox", "limitName": "GPT-5.3-Codex-Spark", "windowSeconds": 18000, "usedPercent": 5.0, "limitReached": false, "resetAtDisplay": reset5h},
			},
			"credits": map[string]any{"hasCredits": true, "unlimited": false, "balance": balance},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin/client-keys", func(w http.ResponseWriter, r *http.Request) {
		writeCPREnvelope(w, map[string]any{"items": []map[string]any{{"id": "key_1", "prefix": "sk-cpr-tri", "groups": []map[string]any{{"id": "grp_1"}}}}})
	})
	mux.HandleFunc("/api/admin/accounts", func(w http.ResponseWriter, r *http.Request) {
		writeCPREnvelope(w, map[string]any{"items": []map[string]any{{"id": "acct_1"}}})
	})
	mux.HandleFunc("/api/admin/accounts/quota/refresh", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		srv.refreshCalls++
		if refreshFails {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 50201, "message": "upstream unavailable", "data": nil})
			return
		}
		writeCPREnvelope(w, map[string]any{"account": account})
	})
	mux.HandleFunc("/api/admin/accounts/quota", func(w http.ResponseWriter, r *http.Request) {
		writeCPREnvelope(w, map[string]any{"account": account})
	})
	mux.HandleFunc("/api/admin/accounts/reset-credits", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, cprTestAdminKey, r.Header.Get("x-api-key"))
		if r.Method == http.MethodPost {
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			srv.consumeBodies = append(srv.consumeBodies, body)
			writeCPREnvelope(w, map[string]any{"code": "reset", "credit": map[string]any{"id": "credit_a", "status": "redeemed", "expiresAt": "2026-12-01T00:00:00+00:00", "resetType": "codex_rate_limits"}})
			return
		}
		require.Equal(t, "acct_1", r.URL.Query().Get("accountId"))
		writeCPREnvelope(w, map[string]any{
			"availableCount": 2,
			"credits": []map[string]any{
				{"id": "credit_a", "status": "available", "expiresAt": "2026-12-01T00:00:00+00:00", "resetType": "codex_rate_limits"},
				{"id": "credit_b", "status": "available", "expiresAt": "2026-12-15T00:00:00+00:00", "resetType": "codex_rate_limits"},
				{"id": "credit_c", "status": "redeemed", "expiresAt": "2026-11-20T00:00:00+00:00", "resetType": "codex_rate_limits"},
			},
		})
	})
	srv.Server = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newCPRQuotaServiceForTest(t *testing.T, srv *cprActionServer, id int64) (*OpenAIQuotaService, *stubQuotaAccountRepo) {
	t.Helper()
	useCPRQuotaSettings(t, srv.URL, cprTestAdminKey)
	resetCPRQuotaState(id)
	t.Cleanup(func() { resetCPRQuotaState(id) })
	repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{id: newCPRTestAccount(id, srv.URL)}}
	// 不提供 token provider / client factory：cpr 账号绝不能走 ChatGPT 直连路径。
	return &OpenAIQuotaService{accountRepo: repo}, repo
}

func TestOpenAIQuotaServiceQueryUsageViaCPR(t *testing.T) {
	srv := newCPRActionServer(t, false)
	svc, repo := newCPRQuotaServiceForTest(t, srv, 9201)

	usage, err := svc.QueryUsage(context.Background(), 9201)
	require.NoError(t, err)
	require.Equal(t, 1, srv.refreshCalls)
	require.Equal(t, "plus", usage.PlanType)
	require.Equal(t, "a@example.com", usage.Email)

	require.NotNil(t, usage.RateLimit)
	require.True(t, usage.RateLimit.Allowed)
	require.InDelta(t, 42.0, usage.RateLimit.PrimaryWindow.UsedPercent, 0.001)
	require.Equal(t, int64(18000), usage.RateLimit.PrimaryWindow.LimitWindowSeconds)
	require.InDelta(t, 2*3600, usage.RateLimit.PrimaryWindow.ResetAfterSeconds, 5)
	require.InDelta(t, 70.0, usage.RateLimit.SecondaryWindow.UsedPercent, 0.001)

	require.Len(t, usage.AdditionalRateLimits, 1)
	require.Equal(t, "codex_bengalfox", usage.AdditionalRateLimits[0].MeteredFeature)
	require.Equal(t, "GPT-5.3-Codex-Spark", usage.AdditionalRateLimits[0].LimitName)

	require.NotNil(t, usage.Credits)
	require.Equal(t, "1250.5", *usage.Credits.Balance)

	require.NotNil(t, usage.RateLimitResetCredits)
	require.Equal(t, 2, usage.RateLimitResetCredits.AvailableCount)
	require.Equal(t, []OpenAIRateLimitResetCreditDetail{{ExpiresAt: "2026-12-01T00:00:00+00:00"}, {ExpiresAt: "2026-12-15T00:00:00+00:00"}}, usage.RateLimitResetCredits.Credits)
	require.Len(t, usage.autoResetCandidates, 2)
	require.Equal(t, "credit_a", usage.autoResetCandidates[0].ID)

	require.Zero(t, repo.extraUpdateCalls, "read-only query must not write account state")

	// 刷新卡/点数缓存与 OAuth 共用同一套缓存逻辑。
	require.NoError(t, svc.CacheResetCreditsSnapshot(context.Background(), 9201, usage.RateLimitResetCredits))
	require.NoError(t, svc.CacheCreditsSnapshot(context.Background(), 9201, usage))
}

func TestOpenAIQuotaServiceQueryUsageViaCPRFallsBackToSnapshot(t *testing.T) {
	srv := newCPRActionServer(t, true)
	svc, _ := newCPRQuotaServiceForTest(t, srv, 9202)

	usage, err := svc.QueryUsage(context.Background(), 9202)
	require.NoError(t, err)
	require.Equal(t, 1, srv.refreshCalls)
	require.InDelta(t, 42.0, usage.RateLimit.PrimaryWindow.UsedPercent, 0.001)
}

func TestOpenAIQuotaServiceResetCreditViaCPR(t *testing.T) {
	srv := newCPRActionServer(t, false)
	svc, _ := newCPRQuotaServiceForTest(t, srv, 9203)

	result, err := svc.ResetCredit(context.Background(), 9203)
	require.NoError(t, err)
	require.Equal(t, "reset", result.Code)
	require.Equal(t, "credit_a", result.Credit.ID)
	require.Len(t, srv.consumeBodies, 1)
	require.Equal(t, "acct_1", srv.consumeBodies[0]["accountId"])
	_, hasCredit := srv.consumeBodies[0]["creditId"]
	require.False(t, hasCredit)
	parsed, err := uuid.Parse(srv.consumeBodies[0]["redeemRequestId"])
	require.NoError(t, err)
	require.Equal(t, uuid.Version(4), parsed.Version())

	// 自动用卡传入的是 UUID v5 幂等键：映射成稳定的 v4，重试时保持同一个值。
	v5 := uuid.NewSHA1(uuid.NameSpaceURL, []byte("oarc:9203:abc:def")).String()
	_, err = svc.ResetCreditTargeted(context.Background(), 9203, "credit_b", v5)
	require.NoError(t, err)
	_, err = svc.ResetCreditTargeted(context.Background(), 9203, "credit_b", v5)
	require.NoError(t, err)
	require.Equal(t, "credit_b", srv.consumeBodies[1]["creditId"])
	require.Equal(t, srv.consumeBodies[1]["redeemRequestId"], srv.consumeBodies[2]["redeemRequestId"])
	parsed, err = uuid.Parse(srv.consumeBodies[1]["redeemRequestId"])
	require.NoError(t, err)
	require.Equal(t, uuid.Version(4), parsed.Version())
}

func TestOpenAIAutoResetCreditConfigAcceptsCPRAccounts(t *testing.T) {
	useCPRQuotaSettings(t, "http://cpr.local:18080", cprTestAdminKey)
	enabledExtra := map[string]any{OpenAIAutoResetCreditEnabledExtraKey: true}

	cpr := newCPRTestAccount(9204, "http://cpr.local:18080")
	cpr.Extra = enabledExtra
	require.True(t, ResolveOpenAIAutoResetCreditConfig(cpr).Enabled)

	plain := newCPRTestAccount(9205, "https://relay.example.com")
	plain.Extra = enabledExtra
	require.False(t, ResolveOpenAIAutoResetCreditConfig(plain).Enabled, "非 cpr 的 API Key 账号不能自动用卡")

	_, err := normalizeOpenAIAutoResetCreditExtra(PlatformOpenAI, AccountTypeAPIKey, false, map[string]any{OpenAIAutoResetCreditEnabledExtraKey: true})
	require.NoError(t, err)
	_, err = normalizeOpenAIAutoResetCreditExtra(PlatformAnthropic, AccountTypeAPIKey, false, map[string]any{OpenAIAutoResetCreditEnabledExtraKey: true})
	require.Error(t, err)
}
