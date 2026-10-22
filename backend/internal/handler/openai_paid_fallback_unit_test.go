//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type paidFallbackHandlerSettings struct {
	service.SettingRepository
	mu      sync.RWMutex
	enabled bool
}

func (s *paidFallbackHandlerSettings) GetValue(_ context.Context, key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch key {
	case "paid_fallback_settings":
		if s.enabled {
			return `{"enabled":true,"group_ids":[7]}`, nil
		}
		return `{"enabled":false}`, nil
	case "openai_advanced_scheduler_enabled":
		return "false", nil
	default:
		return "", service.ErrSettingNotFound
	}
}

func (s *paidFallbackHandlerSettings) Get(ctx context.Context, key string) (*service.Setting, error) {
	value, err := s.GetValue(ctx, key)
	return &service.Setting{Key: key, Value: value}, err
}

func (s *paidFallbackHandlerSettings) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string)
	for _, key := range keys {
		if value, err := s.GetValue(ctx, key); err == nil {
			values[key] = value
		}
	}
	return values, nil
}

type paidFallbackHandlerAccounts struct {
	openAIImagesFailoverAccountRepo
}

func (r paidFallbackHandlerAccounts) ListAllWithFilters(_ context.Context, _, _, status, _ string, groupID int64, _ string) ([]service.Account, error) {
	var accounts []service.Account
	for _, account := range r.accounts {
		if status != "" && account.Status != status {
			continue
		}
		for _, id := range account.GroupIDs {
			if id == groupID {
				accounts = append(accounts, account)
				break
			}
		}
	}
	return accounts, nil
}

type paidFallbackHandlerUpstream struct {
	service.HTTPUpstream
	settings   *paidFallbackHandlerSettings
	revoke     bool
	beforeSend func()
	selected   []int64
	sent       int
}

type paidFallbackHandlerAuditStore struct {
	service.PaidFallbackOpsStore
	attempts []service.PaidFallbackAttempt
}

func (s *paidFallbackHandlerAuditStore) StartAttempt(_ context.Context, attempt service.PaidFallbackAttempt) error {
	s.attempts = append(s.attempts, attempt)
	return nil
}

func (s *paidFallbackHandlerAuditStore) FinishAttempt(context.Context, string, string, int) error {
	return nil
}

func (u *paidFallbackHandlerUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.selected = append(u.selected, accountID)
	if u.revoke {
		u.settings.mu.Lock()
		u.settings.enabled = false
		u.settings.mu.Unlock()
	}
	if u.beforeSend != nil {
		u.beforeSend()
	}
	finish, err := service.BeginPaidFallbackHTTPAttempt(req.Context(), accountID)
	if err != nil {
		return nil, err
	}
	u.sent++
	finish(http.StatusServiceUnavailable, nil)
	return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(`{"error":{"message":"fixture upstream unavailable"}}`))}, nil
}

func TestOpenAIChatCompletionsPaidFallbackFinalSendAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, scenario := range []string{"enabled", "revoked_after_selection", "ordinary_recovers_after_selection"} {
		t.Run(scenario, func(t *testing.T) {
			settingsRepo := &paidFallbackHandlerSettings{enabled: true}
			settings := service.NewSettingService(settingsRepo, &config.Config{})
			audit := &paidFallbackHandlerAuditStore{}
			settings.SetPaidFallbackOpsStore(audit)
			until := time.Now().Add(time.Hour)
			accounts := paidFallbackHandlerAccounts{openAIImagesFailoverAccountRepo{accounts: []service.Account{
				{ID: 1, Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{7}, RateLimitResetAt: &until, Credentials: map[string]any{"model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}}},
				{ID: 2, Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{7}, Credentials: map[string]any{"api_key": "fixture-token", "base_url": "https://fixture.invalid", "model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}}, Extra: map[string]any{service.PaidFallbackExtraKey: true}},
			}}}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			upstream := &paidFallbackHandlerUpstream{settings: settingsRepo, revoke: scenario == "revoked_after_selection"}
			if scenario == "ordinary_recovers_after_selection" {
				upstream.beforeSend = func() { accounts.accounts[0].RateLimitResetAt = nil }
			}
			concurrency := service.NewConcurrencyService(&fakeConcurrencyCache{})
			gateway := service.NewOpenAIGatewayService(accounts, nil, nil, nil, nil, nil, nil, cfg, nil, concurrency, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, settings, nil)
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			handler := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			handler.maxAccountSwitches = 0
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hello"}]}`))
			groupID := int64(7)
			group := &service.Group{ID: 7, Platform: service.PlatformDeepseek, Hydrated: true}
			ctx := context.WithValue(service.WithPaidFallbackAPIKey(c.Request.Context(), 999), ctxkey.Group, group)
			c.Request = c.Request.WithContext(ctx)
			c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 999, GroupID: &groupID, Group: group, User: &service.User{ID: 100}})
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100})
			handler.ChatCompletions(c)
			require.Equal(t, []int64{2}, upstream.selected, "response: %s", rec.Body.String())
			if scenario != "enabled" {
				require.Zero(t, upstream.sent, "policy revocation or ordinary recovery must stop the selected paid request at the transport boundary")
				require.Empty(t, audit.attempts)
			} else {
				require.Equal(t, 1, upstream.sent)
				require.Len(t, audit.attempts, 1)
				require.Equal(t, "deepseek-v4.1-flash", audit.attempts[0].PublicModel)
				require.Equal(t, int64(2), audit.attempts[0].AccountID)
				require.Equal(t, int64(999), audit.attempts[0].APIKeyID)
			}
			require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest)
		})
	}
}
