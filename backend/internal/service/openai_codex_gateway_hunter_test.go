package service

import (
	"context"
	"encoding/base64"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func gatewayHunterTestOAILB(gw string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"lb":"` + gw + `","exp":1}`))
	return "eyJhbGciOiJIUzI1NiJ9." + payload + ".sig"
}

func TestGatewayCookieFromHeader(t *testing.T) {
	h := http.Header{}
	h.Add("Set-Cookie", "__cf_bm=x; path=/")
	h.Add("Set-Cookie", "__oailb="+gatewayHunterTestOAILB("unified-42")+"; Path=/; HttpOnly")
	h.Add("Set-Cookie", "__cflb=0H28vzvP5; SameSite=None; Secure")

	gw, cookie := gatewayCookieFromHeader(h)
	require.Equal(t, "unified-42", gw)
	require.Equal(t, "__cflb=0H28vzvP5; __oailb="+gatewayHunterTestOAILB("unified-42"), cookie)

	gw, cookie = gatewayCookieFromHeader(http.Header{"Set-Cookie": {"__cflb=abc"}})
	require.Empty(t, gw)
	require.Empty(t, cookie)
}

func TestClassifyGatewayHunterAnswer(t *testing.T) {
	require.Equal(t, GatewayTierFull, classifyGatewayHunterAnswer("高市早苗"))
	require.Equal(t, GatewayTierPruned, classifyGatewayHunterAnswer("石破茂"))
	require.Equal(t, GatewayTierDegraded, classifyGatewayHunterAnswer("岸田文雄"))
	require.Equal(t, GatewayTierUnknown, classifyGatewayHunterAnswer(""))
}

func TestNormalizeCodexGatewayHunterConfig(t *testing.T) {
	cfg := defaultCodexGatewayHunterConfig()
	cfg.Model = "  "
	cfg.HunterAccountIDs = []int64{9, 9, 0, -1, 15}
	got, err := normalizeCodexGatewayHunterConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", got.Model)
	require.Equal(t, []int64{9, 15}, got.HunterAccountIDs)

	cfg.Workers = 0
	_, err = normalizeCodexGatewayHunterConfig(cfg)
	require.Error(t, err)
}

func TestSanitizeGatewayHunterErrorDropsProxyURL(t *testing.T) {
	got := sanitizeGatewayHunterError("proxyconnect tcp: dial http://user:pass@1.2.3.4:8080 refused\nmore")
	require.NotContains(t, got, "pass")
	require.NotContains(t, got, "more")
}

type gatewayHunterAccountRepoStub struct {
	AccountRepository
	accounts map[int64]*Account
	groups   map[int64][]int64
}

func (r *gatewayHunterAccountRepoStub) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	out := make([]*Account, 0, len(ids))
	for _, id := range ids {
		if a := r.accounts[id]; a != nil {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *gatewayHunterAccountRepoStub) ListByGroup(_ context.Context, groupID int64) ([]Account, error) {
	var out []Account
	for _, id := range r.groups[groupID] {
		out = append(out, *r.accounts[id])
	}
	return out, nil
}

func gatewayHunterTestAccount(id int64) *Account {
	return &Account{ID: id, Name: "acc", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"access_token": "tok", "chatgpt_account_id": "cid"}}
}

func TestCodexGatewayHunterFindsAndTracksWindow(t *testing.T) {
	repo := &gatewayHunterAccountRepoStub{
		accounts: map[int64]*Account{9: gatewayHunterTestAccount(9), 15: gatewayHunterTestAccount(15), 54: gatewayHunterTestAccount(54)},
		groups:   map[int64][]int64{25: {54}},
	}
	svc := NewCodexGatewayHunterService(nil, repo, nil, nil, nil)
	svc.trackEvery = 20 * time.Millisecond

	var mu sync.Mutex
	tracks := 0
	svc.call = func(_ context.Context, account *Account, _, prompt, cookie string, _ time.Duration) gatewayProbeResult {
		if prompt == "hi" {
			return gatewayProbeResult{status: http.StatusBadRequest, gateway: "unified-5", cookie: "__oailb=x"}
		}
		if cookie == "" {
			return gatewayProbeResult{status: http.StatusOK, tier: GatewayTierDegraded, answer: "岸田"}
		}
		mu.Lock()
		defer mu.Unlock()
		if account.ID == 15 {
			tracks++
			if tracks > 2 {
				return gatewayProbeResult{status: http.StatusOK, tier: GatewayTierPruned, answer: "石破"}
			}
		}
		return gatewayProbeResult{status: http.StatusOK, tier: GatewayTierFull, answer: "高市"}
	}

	cfg := defaultCodexGatewayHunterConfig()
	cfg.HunterAccountIDs = []int64{9}
	cfg.TrackerAccountIDs = []int64{15}
	cfg.ScoutGroupID = 25
	cfg.Workers = 1
	cfg.MinProbeIntervalSeconds = 600

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = svc.run(ctx, cfg)
	}()

	require.Eventually(t, func() bool { return len(svc.HotGateways()) == 1 }, 3*time.Second, 5*time.Millisecond)
	require.Equal(t, "unified-5", svc.HotGateways()[0].Gateway)
	require.Eventually(t, func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		return len(svc.windows) == 1 && svc.windows[0].End != nil
	}, 3*time.Second, 5*time.Millisecond)
	cancel()
	<-done

	svc.mu.Lock()
	defer svc.mu.Unlock()
	require.Empty(t, svc.hot)
	require.Equal(t, int64(9), svc.windows[0].FoundBy)
	require.ElementsMatch(t, []int64{9, 15}, svc.windows[0].ConfirmedBy)
	require.Equal(t, 1, svc.counters.Windows)
	require.GreaterOrEqual(t, svc.counters.HuntHits, 1)
	require.Equal(t, "scout", svc.accounts[54].role)
	require.Equal(t, "tracker", svc.accounts[15].role)
}

func TestGatewayHunterTrackerBudgetRetiresTrackerOnly(t *testing.T) {
	cfg := defaultCodexGatewayHunterConfig()
	cfg.TrackerDailyBudget = 3
	now := time.Now()
	st := &gatewayHunterAccountState{role: "tracker", day: now.Format("2006-01-02"), probesToday: 3}
	require.Equal(t, "daily_budget", st.unavailableReason(now, cfg, false))
	require.Empty(t, st.unavailableReason(now, cfg, true))
	high := 99.0
	st.secondary = &high
	require.Equal(t, "weekly_quota", st.unavailableReason(now, cfg, true))
}
