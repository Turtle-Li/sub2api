package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type codexBillingCacheStub struct {
	service.BillingCache
	subscriptions []*service.SubscriptionCacheData
	calls         int
}

func (s *codexBillingCacheStub) GetSubscriptionCache(
	context.Context,
	int64,
	int64,
) (*service.SubscriptionCacheData, error) {
	if len(s.subscriptions) == 0 {
		return nil, nil
	}
	index := s.calls
	if index >= len(s.subscriptions) {
		index = len(s.subscriptions) - 1
	}
	s.calls++
	return s.subscriptions[index], nil
}

func TestCodexResponsesQueueWaitDoesNotCommitBeforeBillingRecheck(t *testing.T) {
	cache := &helperConcurrencyCacheStub{
		userSeq:     []bool{false, true},
		waitAllowed: true,
	}
	helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatComment, time.Millisecond)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.145.0")
	c.Request.Header.Set("originator", "codex_cli_rs")

	streamStarted := false
	release, err := helper.acquireUserSlotWithWaitTimeout(
		c,
		10,
		1,
		time.Second,
		responsesQueueStreamEnabled(c, true),
		&streamStarted,
	)
	require.NoError(t, err)
	require.NotNil(t, release)
	release()

	require.False(t, streamStarted)
	require.False(t, w.Result().StatusCode == http.StatusOK && w.Body.Len() > 0)
	require.Empty(t, w.Body.String())
}

func TestOpenAIResponsesWebSocketFinalFundingCheckRejectsCodexSubscriptionBeforeUpgrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	weeklyLimit := 1.0
	groupID := int64(7)
	group := &service.Group{
		ID:               groupID,
		Platform:         service.PlatformOpenAI,
		SubscriptionType: service.SubscriptionTypeSubscription,
		WeeklyLimitUSD:   &weeklyLimit,
	}
	user := &service.User{ID: 11}
	apiKey := &service.APIKey{ID: 13, GroupID: &groupID, Group: group, User: user}
	subscription := &service.UserSubscription{
		ID:        17,
		UserID:    user.ID,
		GroupID:   groupID,
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: time.Now().Add(time.Hour),
	}

	billingCacheSource := &codexBillingCacheStub{subscriptions: []*service.SubscriptionCacheData{
		{
			Status:      service.SubscriptionStatusActive,
			ExpiresAt:   time.Now().Add(time.Hour),
			WeeklyUsage: 0,
		},
		{
			Status:      service.SubscriptionStatusActive,
			ExpiresAt:   time.Now().Add(time.Hour),
			WeeklyUsage: weeklyLimit,
		},
	}}
	billingCache := service.NewBillingCacheService(
		billingCacheSource,
		nil,
		nil,
		nil,
		nil,
		nil,
		&config.Config{},
		nil,
	)
	t.Cleanup(billingCache.Stop)
	subscriptionService := service.NewSubscriptionService(nil, nil, nil, nil, nil)
	subscriptionService.SetResetCardRepository(billingGuidanceResetCardRepoStub{count: 2})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	c.Request.Header.Set("Connection", "Upgrade")
	c.Request.Header.Set("Upgrade", "websocket")
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.145.0")
	c.Request.Header.Set("originator", "codex_cli_rs")
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: user.ID, Concurrency: 1})
	c.Set(string(middleware2.ContextKeySubscription), subscription)

	h := &OpenAIGatewayHandler{
		gatewayService:      &service.OpenAIGatewayService{},
		billingCacheService: billingCache,
		subscriptionService: subscriptionService,
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   NewConcurrencyHelper(service.NewConcurrencyService(&helperConcurrencyCacheStub{}), SSEPingFormatNone, time.Second),
		imageLimiter:        &imageConcurrencyLimiter{},
		maxAccountSwitches:  1,
		cfg:                 &config.Config{},
	}

	h.ResponsesWebSocket(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	require.Equal(t, "USAGE_LIMIT_EXCEEDED", w.Header().Get("X-Sub2-Error-Code"))
	require.Equal(t, 2, billingCacheSource.calls, "funding must be rechecked immediately before upgrade")
	require.Equal(t,
		"订阅每周额度已用完。你当前还有 2 次可用重置次数，请前往「订阅」页面使用后再试。",
		w.Body.String(),
	)
}
