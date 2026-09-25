package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bpsConfigStoreStub struct {
	cfg   service.OpenAIBPSUpstreamConfig
	saved *service.OpenAIBPSUpstreamConfig
}

func (s *bpsConfigStoreStub) GetOpenAIBPSUpstreamConfig(context.Context) service.OpenAIBPSUpstreamConfig {
	return s.cfg
}

func (s *bpsConfigStoreStub) UpdateOpenAIBPSUpstreamConfig(_ context.Context, cfg service.OpenAIBPSUpstreamConfig) (service.OpenAIBPSUpstreamConfig, error) {
	s.saved = &cfg
	s.cfg = cfg
	return cfg, nil
}

type bpsAccountLookupStub map[int64]*service.Account

func (s bpsAccountLookupStub) GetAccountsByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	out := make([]*service.Account, 0, len(ids))
	for _, id := range ids {
		if account := s[id]; account != nil {
			out = append(out, account)
		}
	}
	return out, nil
}

func newBPSUpstreamTestRouter(store *bpsConfigStoreStub, accounts bpsAccountLookupStub, snapshot service.BPSMonitorSnapshot, reset func(int64)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &BPSUpstreamHandler{configStore: store, accounts: accounts, snapshot: func() service.BPSMonitorSnapshot { return snapshot }, reset: reset}
	router := gin.New()
	router.GET("/bps", h.GetOverview)
	router.PUT("/bps/config", h.UpdateConfig)
	router.POST("/bps/accounts/:id/reset-breaker", h.ResetBreaker)
	return router
}

func bpsOAuthAccount(id int64) *service.Account {
	return &service.Account{ID: id, Name: "acct", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true}
}

func TestBPSUpstreamHandlerOverview(t *testing.T) {
	store := &bpsConfigStoreStub{cfg: service.OpenAIBPSUpstreamConfig{Enabled: true, AccountIDs: []int64{69, 70}}}
	snapshot := service.BPSMonitorSnapshot{Accounts: []service.BPSAccountStats{{AccountID: 69, Successes: 4}, {AccountID: 12, Fallbacks: 1}}}
	router := newBPSUpstreamTestRouter(store, bpsAccountLookupStub{69: bpsOAuthAccount(69)}, snapshot, nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bps", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Data struct {
			Config   service.OpenAIBPSUpstreamConfig `json:"config"`
			Accounts []struct {
				ID       int64                    `json:"id"`
				Eligible bool                     `json:"eligible"`
				Missing  bool                     `json:"missing"`
				Stats    *service.BPSAccountStats `json:"stats"`
			} `json:"accounts"`
			Unlisted []service.BPSAccountStats `json:"unlisted_stats"`
			Policy   service.BPSUpstreamPolicy `json:"policy"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.True(t, body.Data.Config.Enabled)
	require.Len(t, body.Data.Accounts, 2)
	require.True(t, body.Data.Accounts[0].Eligible)
	require.Equal(t, int64(4), body.Data.Accounts[0].Stats.Successes)
	require.True(t, body.Data.Accounts[1].Missing)
	require.Len(t, body.Data.Unlisted, 1)
	require.Equal(t, int64(12), body.Data.Unlisted[0].AccountID)
	require.Contains(t, body.Data.Policy.Models, "gpt-6-astra")
}

func TestBPSUpstreamHandlerUpdateConfig(t *testing.T) {
	apiKey := bpsOAuthAccount(81)
	apiKey.Type = service.AccountTypeAPIKey
	accounts := bpsAccountLookupStub{69: bpsOAuthAccount(69), 81: apiKey}
	put := func(store *bpsConfigStoreStub, payload string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/bps/config", bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		newBPSUpstreamTestRouter(store, accounts, service.BPSMonitorSnapshot{}, nil).ServeHTTP(rec, req)
		return rec
	}

	store := &bpsConfigStoreStub{}
	require.Equal(t, http.StatusOK, put(store, `{"enabled":true,"account_ids":[69]}`).Code)
	require.Equal(t, service.OpenAIBPSUpstreamConfig{Enabled: true, AccountIDs: []int64{69}}, *store.saved)

	for _, payload := range []string{`{"account_ids":[81]}`, `{"account_ids":[404]}`, `{"account_ids":[0]}`} {
		store := &bpsConfigStoreStub{}
		require.Equal(t, http.StatusBadRequest, put(store, payload).Code, payload)
		require.Nil(t, store.saved)
	}

	// 已在名单中但账号已删除时，仍允许保存（以便随后移除）。
	store = &bpsConfigStoreStub{cfg: service.OpenAIBPSUpstreamConfig{AccountIDs: []int64{404}}}
	require.Equal(t, http.StatusOK, put(store, `{"enabled":false,"account_ids":[404,69]}`).Code)
}

func TestBPSUpstreamHandlerResetBreaker(t *testing.T) {
	var reset int64
	router := newBPSUpstreamTestRouter(&bpsConfigStoreStub{}, nil, service.BPSMonitorSnapshot{}, func(id int64) { reset = id })
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/bps/accounts/69/reset-breaker", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(69), reset)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/bps/accounts/x/reset-breaker", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
