package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const cprTestAdminKey = "admin-0000000000000000000000000000000000000000000000000000000000000001"

func newCPRTestAccount(id int64, baseURL string) *Account {
	return &Account{
		ID:       id,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":                  "sk-cpr-trial-abcdef123456",
			"base_url":                 baseURL + "/v1",
			cprQuotaAdminKeyCredential: cprTestAdminKey,
		},
		Extra: map[string]any{},
	}
}

func writeCPREnvelope(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "ok", "data": data})
}

type cprTestServer struct {
	*httptest.Server
	quotaCalls  atomic.Int32
	revealCalls atomic.Int32
}

func newCPRTestServer(t *testing.T, keys []map[string]any, reveal map[string]string, quota map[string]any) *cprTestServer {
	t.Helper()
	srv := &cprTestServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin/client-keys", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, cprTestAdminKey, r.Header.Get("x-api-key"))
		writeCPREnvelope(w, map[string]any{"items": keys, "nextCursor": nil, "total": len(keys)})
	})
	mux.HandleFunc("/api/admin/client-keys/reveal", func(w http.ResponseWriter, r *http.Request) {
		srv.revealCalls.Add(1)
		id := r.URL.Query().Get("id")
		writeCPREnvelope(w, map[string]any{"id": id, "plaintextKey": reveal[id]})
	})
	mux.HandleFunc("/api/admin/accounts", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "grp_1", r.URL.Query().Get("groupId"))
		writeCPREnvelope(w, map[string]any{"items": []map[string]any{{"id": "acct_1", "provider": "openai"}}})
	})
	mux.HandleFunc("/api/admin/accounts/quota", func(w http.ResponseWriter, r *http.Request) {
		srv.quotaCalls.Add(1)
		require.Equal(t, "acct_1", r.URL.Query().Get("accountId"))
		writeCPREnvelope(w, map[string]any{"account": map[string]any{"id": "acct_1", "provider": "openai", "quota": quota}})
	})
	srv.Server = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func cprTestQuota(refreshedAt string, fiveHourUsed float64, fiveHourReached bool, fiveHourReset, weekReset string) map[string]any {
	return map[string]any{
		"refreshedAt":      refreshedAt,
		"limitReached":     fiveHourReached,
		"rateLimitedUntil": nil,
		"windows": []map[string]any{
			{"key": "codex:18000s", "limitId": "codex", "windowSeconds": 18000, "usedPercent": fiveHourUsed, "limitReached": fiveHourReached, "resetAtDisplay": fiveHourReset},
			{"key": "codex:604800s", "limitId": "codex", "windowSeconds": 604800, "usedPercent": 41.5, "limitReached": false, "resetAtDisplay": weekReset},
			// 其他模型的独立额度不能覆盖主额度。
			{"key": "codex_bengalfox:18000s", "limitId": "codex_bengalfox", "windowSeconds": 18000, "usedPercent": 99.0, "limitReached": true, "resetAtDisplay": fiveHourReset},
		},
	}
}

func resetCPRQuotaState(id int64) {
	cprQuotaSyncThrottle.Delete(id)
	cprQuotaResolveCache.Delete(id)
}

func TestIsCPRQuotaSyncAccount(t *testing.T) {
	account := newCPRTestAccount(1, "http://cpr.local")
	require.True(t, IsCPRQuotaSyncAccount(account))

	account.Credentials[cprQuotaAdminKeyCredential] = " "
	require.False(t, IsCPRQuotaSyncAccount(account))

	oauth := newCPRTestAccount(2, "http://cpr.local")
	oauth.Type = AccountTypeOAuth
	require.False(t, IsCPRQuotaSyncAccount(oauth))
}

func TestCPRQuotaAdminBaseURL(t *testing.T) {
	account := newCPRTestAccount(1, "http://172.26.0.217:18080")
	require.Equal(t, "http://172.26.0.217:18080", cprQuotaAdminBaseURL(account))

	account.Extra[cprQuotaAdminBaseURLExtra] = "https://cpr.example.com/"
	require.Equal(t, "https://cpr.example.com", cprQuotaAdminBaseURL(account))
}

func TestBuildCPRQuotaSyncResultMapsWindows(t *testing.T) {
	refreshed := "2026-11-02T12:00:00+08:00"
	now := time.Date(2026, 11, 2, 4, 30, 0, 0, time.UTC) // 12:30 +08:00
	result := buildCPRQuotaSyncResult(cprQuotaFromMap(t, cprTestQuota(refreshed, 63.2, false, "2026-11-02 14:00:00", "2026-11-06 08:00:00")), now)

	require.Nil(t, result.exhaustedUntil)
	require.InDelta(t, 63.2, result.updates["codex_5h_used_percent"], 0.001)
	require.InDelta(t, 41.5, result.updates["codex_7d_used_percent"], 0.001)
	require.Equal(t, 300, result.updates["codex_5h_window_minutes"])
	require.Equal(t, 10080, result.updates["codex_7d_window_minutes"])
	require.Equal(t, "2026-11-02T06:00:00Z", result.updates["codex_5h_reset_at"])
	require.Equal(t, "2026-11-06T00:00:00Z", result.updates["codex_7d_reset_at"])
	require.Equal(t, "2026-11-02T04:00:00Z", result.updates["codex_usage_updated_at"])
}

func TestBuildCPRQuotaSyncResultExhausted(t *testing.T) {
	now := time.Date(2026, 11, 2, 4, 30, 0, 0, time.UTC)
	result := buildCPRQuotaSyncResult(cprQuotaFromMap(t, cprTestQuota("2026-11-02T12:00:00+08:00", 100, true, "2026-11-02 14:00:00", "—")), now)

	require.NotNil(t, result.exhaustedUntil)
	require.True(t, result.exhaustedUntil.Equal(time.Date(2026, 11, 2, 6, 0, 0, 0, time.UTC)))
	_, hasWeekReset := result.updates["codex_7d_reset_at"]
	require.False(t, hasWeekReset)
}

func TestSyncCPRQuotaSnapshotResolvesByAPIKey(t *testing.T) {
	keys := []map[string]any{
		{"id": "key_other", "prefix": "sk-other00", "groups": []map[string]any{{"id": "grp_x"}}},
		{"id": "key_1", "prefix": "sk-cpr-tri", "groups": []map[string]any{{"id": "grp_1"}}},
	}
	srv := newCPRTestServer(t, keys, nil, cprTestQuota(time.Now().Format(time.RFC3339), 12, false, "—", "—"))
	account := newCPRTestAccount(9101, srv.URL)
	resetCPRQuotaState(account.ID)
	t.Cleanup(func() { resetCPRQuotaState(account.ID) })

	result, err := syncCPRQuotaSnapshot(context.Background(), nil, account, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "acct_1", account.Extra[cprQuotaResolvedAccountIDExtra])
	require.Equal(t, "", account.Extra[cprQuotaLastErrorExtra])
	require.InDelta(t, 12.0, account.Extra["codex_5h_used_percent"], 0.001)
	require.Equal(t, int32(0), srv.revealCalls.Load())

	// 节流：30 秒内非强制同步不再请求。
	result, err = syncCPRQuotaSnapshot(context.Background(), nil, account, false)
	require.NoError(t, err)
	require.Nil(t, result)
	require.Equal(t, int32(1), srv.quotaCalls.Load())

	_, err = syncCPRQuotaSnapshot(context.Background(), nil, account, true)
	require.NoError(t, err)
	require.Equal(t, int32(2), srv.quotaCalls.Load())
}

func TestSyncCPRQuotaSnapshotDisambiguatesPrefixByReveal(t *testing.T) {
	keys := []map[string]any{
		{"id": "key_a", "prefix": "sk-cpr-tri", "groups": []map[string]any{{"id": "grp_x"}}},
		{"id": "key_b", "prefix": "sk-cpr-tri", "groups": []map[string]any{{"id": "grp_1"}}},
	}
	reveal := map[string]string{"key_a": "sk-cpr-trial-someone-else", "key_b": "sk-cpr-trial-abcdef123456"}
	srv := newCPRTestServer(t, keys, reveal, cprTestQuota(time.Now().Format(time.RFC3339), 5, false, "—", "—"))
	account := newCPRTestAccount(9102, srv.URL)
	resetCPRQuotaState(account.ID)
	t.Cleanup(func() { resetCPRQuotaState(account.ID) })

	_, err := syncCPRQuotaSnapshot(context.Background(), nil, account, true)
	require.NoError(t, err)
	require.Equal(t, "acct_1", account.Extra[cprQuotaResolvedAccountIDExtra])
	require.Equal(t, int32(2), srv.revealCalls.Load())
}

func TestSyncCPRQuotaSnapshotRecordsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 40101, "message": "invalid admin api key", "data": nil})
	}))
	t.Cleanup(srv.Close)
	account := newCPRTestAccount(9103, srv.URL)
	account.Extra[cprQuotaAccountIDExtra] = "acct_1"
	account.Extra["codex_5h_used_percent"] = 30.0
	resetCPRQuotaState(account.ID)
	t.Cleanup(func() { resetCPRQuotaState(account.ID) })

	result, err := syncCPRQuotaSnapshot(context.Background(), nil, account, true)
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, account.Extra[cprQuotaLastErrorExtra], "invalid admin api key")
	// 失败时保留旧快照。
	require.Equal(t, 30.0, account.Extra["codex_5h_used_percent"])
	require.False(t, cprQuotaSnapshotStale(account, time.Now()))
}

func cprQuotaFromMap(t *testing.T, raw map[string]any) cprAdminQuota {
	t.Helper()
	encoded, err := json.Marshal(raw)
	require.NoError(t, err)
	var quota cprAdminQuota
	require.NoError(t, json.Unmarshal(encoded, &quota))
	return quota
}

func TestAccountUsageService_GetUsageForCPRAPIKeyAccount(t *testing.T) {
	keys := []map[string]any{{"id": "key_1", "prefix": "sk-cpr-tri", "groups": []map[string]any{{"id": "grp_1"}}}}
	refreshed := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
	resetAt := refreshed.Add(3 * time.Hour).In(time.FixedZone("CST", 8*3600))
	srv := newCPRTestServer(t, keys, nil, cprTestQuota(refreshed.In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339), 77, false, resetAt.Format("2006-01-02 15:04:05"), "—"))
	account := newCPRTestAccount(9104, srv.URL)
	resetCPRQuotaState(account.ID)
	t.Cleanup(func() { resetCPRQuotaState(account.ID) })

	repo := &accountUsageCodexProbeRepo{updateExtraCh: make(chan map[string]any, 4), rateLimitCh: make(chan time.Time, 1)}
	svc := &AccountUsageService{accountRepo: repo}
	usage, err := svc.getUsageForAccount(context.Background(), account, false)
	require.NoError(t, err)
	require.NotNil(t, usage.FiveHour)
	require.InDelta(t, 77.0, usage.FiveHour.Utilization, 0.001)
	require.NotNil(t, usage.FiveHour.ResetsAt)
	require.True(t, usage.FiveHour.ResetsAt.Equal(resetAt), "resets_at=%v want %v", usage.FiveHour.ResetsAt, resetAt)

	select {
	case updates := <-repo.updateExtraCh:
		require.Equal(t, "acct_1", updates[cprQuotaResolvedAccountIDExtra])
	case <-time.After(2 * time.Second):
		t.Fatal("cpr 快照未写入 extra")
	}
}
