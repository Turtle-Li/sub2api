//go:build unit

package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	subscriptionExpiryGuidanceNotFoundMessage = "当前分组没有有效订阅，请开通订阅或切换至其他有效订阅分组后重试。"
	subscriptionExpiryGuidanceExpiredMessage  = "订阅已到期，请续费或切换至其他有效订阅分组后重试。"
	subscriptionExpiryGuidanceLookupMessage   = "订阅状态查询失败，请稍后重试。"
)

func TestAPIKeyAuthSubscriptionExpiryGuidanceForResponsesClients(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("missing subscription preserves normal client and adapts Codex", func(t *testing.T) {
		router, apiKey := newSubscriptionExpiryGuidanceAuthRouter(t, func(context.Context, int64, int64) (*service.UserSubscription, error) {
			return nil, service.ErrSubscriptionNotFound
		})

		regular := subscriptionExpiryGuidanceRequest(router, http.MethodPost, "/v1/responses", apiKey, false)
		require.Equal(t, http.StatusForbidden, regular.Code)
		requireAPIKeyAuthError(t, regular, "SUBSCRIPTION_NOT_FOUND", subscriptionExpiryGuidanceNotFoundMessage)

		codex := subscriptionExpiryGuidanceRequest(router, http.MethodPost, "/v1/responses", apiKey, true)
		require.Equal(t, http.StatusBadRequest, codex.Code)
		require.Equal(t, "text/plain; charset=utf-8", codex.Header().Get("Content-Type"))
		require.Equal(t, "SUBSCRIPTION_NOT_FOUND", codex.Header().Get("X-Sub2-Error-Code"))
		require.Equal(t, subscriptionExpiryGuidanceNotFoundMessage, codex.Body.String())
	})

	t.Run("expired subscription record is reported as expiry", func(t *testing.T) {
		for _, record := range []*service.UserSubscription{
			{ID: 714, UserID: 701, GroupID: 702, Status: service.SubscriptionStatusExpired, ExpiresAt: time.Now().Add(-time.Hour)},
			{ID: 715, UserID: 701, GroupID: 702, Status: service.SubscriptionStatusActive, ExpiresAt: time.Now().Add(-time.Minute)},
		} {
			router, apiKey := newSubscriptionExpiryGuidanceAuthRouterWithRecord(t, func(context.Context, int64, int64) (*service.UserSubscription, error) {
				return nil, service.ErrSubscriptionNotFound
			}, record)

			regular := subscriptionExpiryGuidanceRequest(router, http.MethodPost, "/v1/responses", apiKey, false)
			require.Equal(t, http.StatusForbidden, regular.Code)
			requireAPIKeyAuthError(t, regular, "SUBSCRIPTION_EXPIRED", subscriptionExpiryGuidanceExpiredMessage)

			codex := subscriptionExpiryGuidanceRequest(router, http.MethodPost, "/v1/responses", apiKey, true)
			require.Equal(t, http.StatusBadRequest, codex.Code)
			require.Equal(t, "SUBSCRIPTION_EXPIRED", codex.Header().Get("X-Sub2-Error-Code"))
			require.Equal(t, subscriptionExpiryGuidanceExpiredMessage, codex.Body.String())
		}
	})

	t.Run("revoked subscription record stays not found", func(t *testing.T) {
		router, apiKey := newSubscriptionExpiryGuidanceAuthRouterWithRecord(t, func(context.Context, int64, int64) (*service.UserSubscription, error) {
			return nil, service.ErrSubscriptionNotFound
		}, &service.UserSubscription{ID: 716, UserID: 701, GroupID: 702, Status: service.SubscriptionStatusRevoked, ExpiresAt: time.Now().Add(-time.Hour)})

		regular := subscriptionExpiryGuidanceRequest(router, http.MethodPost, "/v1/responses", apiKey, false)
		require.Equal(t, http.StatusForbidden, regular.Code)
		requireAPIKeyAuthError(t, regular, "SUBSCRIPTION_NOT_FOUND", subscriptionExpiryGuidanceNotFoundMessage)
	})

	t.Run("expired active snapshot preserves normal client and adapts Codex", func(t *testing.T) {
		stale := &service.UserSubscription{
			ID:        711,
			UserID:    701,
			GroupID:   702,
			Status:    service.SubscriptionStatusActive,
			ExpiresAt: time.Now().Add(-time.Minute),
		}
		router, apiKey := newSubscriptionExpiryGuidanceAuthRouter(t, func(context.Context, int64, int64) (*service.UserSubscription, error) {
			clone := *stale
			return &clone, nil
		})

		regular := subscriptionExpiryGuidanceRequest(router, http.MethodPost, "/v1/responses", apiKey, false)
		require.Equal(t, http.StatusForbidden, regular.Code)
		requireAPIKeyAuthError(t, regular, "SUBSCRIPTION_EXPIRED", subscriptionExpiryGuidanceExpiredMessage)

		codex := subscriptionExpiryGuidanceRequest(router, http.MethodPost, "/v1/responses", apiKey, true)
		require.Equal(t, http.StatusBadRequest, codex.Code)
		require.Equal(t, "text/plain; charset=utf-8", codex.Header().Get("Content-Type"))
		require.Equal(t, "SUBSCRIPTION_EXPIRED", codex.Header().Get("X-Sub2-Error-Code"))
		require.Equal(t, subscriptionExpiryGuidanceExpiredMessage, codex.Body.String())
	})

	t.Run("lookup failure is never presented as subscription expiry", func(t *testing.T) {
		router, apiKey := newSubscriptionExpiryGuidanceAuthRouter(t, func(context.Context, int64, int64) (*service.UserSubscription, error) {
			return nil, errors.New("subscription repository unavailable")
		})

		codex := subscriptionExpiryGuidanceRequest(router, http.MethodPost, "/v1/responses", apiKey, true)
		require.Equal(t, http.StatusInternalServerError, codex.Code)
		require.Empty(t, codex.Header().Get("X-Sub2-Error-Code"))
		requireAPIKeyAuthError(t, codex, "SUBSCRIPTION_LOOKUP_FAILED", subscriptionExpiryGuidanceLookupMessage)
	})

	t.Run("suspended subscription retains the existing invalid response", func(t *testing.T) {
		suspended := &service.UserSubscription{
			ID:        712,
			UserID:    701,
			GroupID:   702,
			Status:    service.SubscriptionStatusSuspended,
			ExpiresAt: time.Now().Add(time.Hour),
		}
		router, apiKey := newSubscriptionExpiryGuidanceAuthRouter(t, func(context.Context, int64, int64) (*service.UserSubscription, error) {
			clone := *suspended
			return &clone, nil
		})

		codex := subscriptionExpiryGuidanceRequest(router, http.MethodPost, "/v1/responses", apiKey, true)
		require.Equal(t, http.StatusForbidden, codex.Code)
		require.Empty(t, codex.Header().Get("X-Sub2-Error-Code"))
		requireAPIKeyAuthError(t, codex, "SUBSCRIPTION_INVALID", service.ErrSubscriptionSuspended.Error())
	})
}

func TestAPIKeyAuthSubscriptionExpiryGuidanceUsageSkipsBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	lookupCalls := 0
	router, apiKey := newSubscriptionExpiryGuidanceAuthRouter(t, func(context.Context, int64, int64) (*service.UserSubscription, error) {
		lookupCalls++
		return nil, service.ErrSubscriptionNotFound
	})

	usage := subscriptionExpiryGuidanceRequest(router, http.MethodGet, "/v1/usage", apiKey, false)
	require.Equal(t, http.StatusOK, usage.Code)
	require.Equal(t, 1, lookupCalls, "usage keeps the subscription read but skips the billing rejection")
}

func TestAPIKeyAuthSubscriptionExpiryGuidanceGoogleContract(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name         string
		getActive    func(context.Context, int64, int64) (*service.UserSubscription, error)
		record       *service.UserSubscription
		wantStatus   int
		wantMessage  string
		googleStatus string
	}{
		{
			name: "missing subscription",
			getActive: func(context.Context, int64, int64) (*service.UserSubscription, error) {
				return nil, service.ErrSubscriptionNotFound
			},
			wantStatus:   http.StatusForbidden,
			wantMessage:  subscriptionExpiryGuidanceNotFoundMessage,
			googleStatus: "PERMISSION_DENIED",
		},
		{
			name: "expired subscription record",
			getActive: func(context.Context, int64, int64) (*service.UserSubscription, error) {
				return nil, service.ErrSubscriptionNotFound
			},
			record:       &service.UserSubscription{ID: 717, UserID: 701, GroupID: 702, Status: service.SubscriptionStatusExpired, ExpiresAt: time.Now().Add(-time.Hour)},
			wantStatus:   http.StatusForbidden,
			wantMessage:  subscriptionExpiryGuidanceExpiredMessage,
			googleStatus: "PERMISSION_DENIED",
		},
		{
			name: "expired active snapshot",
			getActive: func(context.Context, int64, int64) (*service.UserSubscription, error) {
				return &service.UserSubscription{
					ID:        713,
					UserID:    701,
					GroupID:   702,
					Status:    service.SubscriptionStatusActive,
					ExpiresAt: time.Now().Add(-time.Minute),
				}, nil
			},
			wantStatus:   http.StatusForbidden,
			wantMessage:  subscriptionExpiryGuidanceExpiredMessage,
			googleStatus: "PERMISSION_DENIED",
		},
		{
			name: "lookup failure",
			getActive: func(context.Context, int64, int64) (*service.UserSubscription, error) {
				return nil, errors.New("subscription repository unavailable")
			},
			wantStatus:   http.StatusInternalServerError,
			wantMessage:  subscriptionExpiryGuidanceLookupMessage,
			googleStatus: "INTERNAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, apiKey := newSubscriptionExpiryGuidanceGoogleRouter(t, tt.getActive, tt.record)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
			req.Header.Set("x-goog-api-key", apiKey)
			router.ServeHTTP(rec, req)

			require.Equal(t, tt.wantStatus, rec.Code)
			var response struct {
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
					Status  string `json:"status"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			require.Equal(t, tt.wantStatus, response.Error.Code)
			require.Equal(t, tt.wantMessage, response.Error.Message)
			require.Equal(t, tt.googleStatus, response.Error.Status)
		})
	}
}

func newSubscriptionExpiryGuidanceAuthRouter(
	t *testing.T,
	getActive func(context.Context, int64, int64) (*service.UserSubscription, error),
) (*gin.Engine, string) {
	t.Helper()
	apiKeyService, subscriptionService, cfg, apiKey := newSubscriptionExpiryGuidanceServices(t, getActive, nil)
	return newAuthTestRouter(apiKeyService, subscriptionService, cfg), apiKey
}

func newSubscriptionExpiryGuidanceAuthRouterWithRecord(
	t *testing.T,
	getActive func(context.Context, int64, int64) (*service.UserSubscription, error),
	record *service.UserSubscription,
) (*gin.Engine, string) {
	t.Helper()
	apiKeyService, subscriptionService, cfg, apiKey := newSubscriptionExpiryGuidanceServices(t, getActive, record)
	return newAuthTestRouter(apiKeyService, subscriptionService, cfg), apiKey
}

func newSubscriptionExpiryGuidanceGoogleRouter(
	t *testing.T,
	getActive func(context.Context, int64, int64) (*service.UserSubscription, error),
	record *service.UserSubscription,
) (*gin.Engine, string) {
	t.Helper()
	apiKeyService, subscriptionService, cfg, apiKey := newSubscriptionExpiryGuidanceServices(t, getActive, record)
	router := gin.New()
	router.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, cfg))
	router.GET("/v1beta/models", func(c *gin.Context) { c.Status(http.StatusOK) })
	return router, apiKey
}

func newSubscriptionExpiryGuidanceServices(
	t *testing.T,
	getActive func(context.Context, int64, int64) (*service.UserSubscription, error),
	record *service.UserSubscription,
) (*service.APIKeyService, *service.SubscriptionService, *config.Config, string) {
	t.Helper()

	const (
		userID  int64 = 701
		groupID int64 = 702
	)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	group := &service.Group{
		ID:               groupID,
		Name:             "subscription guidance",
		Platform:         service.PlatformOpenAI,
		Status:           service.StatusActive,
		Hydrated:         true,
		SubscriptionType: service.SubscriptionTypeSubscription,
	}
	user := &service.User{
		ID:          userID,
		Role:        service.RoleUser,
		Status:      service.StatusActive,
		Balance:     10,
		Concurrency: 1,
	}
	apiKey := &service.APIKey{
		ID:      703,
		UserID:  user.ID,
		Key:     "subscription-expiry-guidance",
		Status:  service.StatusActive,
		User:    user,
		Group:   group,
		GroupID: &group.ID,
	}
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(_ context.Context, key string) (*service.APIKey, error) {
			if key != apiKey.Key {
				return nil, service.ErrAPIKeyNotFound
			}
			clone := *apiKey
			userClone := *user
			groupClone := *group
			groupIDClone := group.ID
			clone.User = &userClone
			clone.Group = &groupClone
			clone.GroupID = &groupIDClone
			return &clone, nil
		},
	}
	subscriptionRepo := &stubUserSubscriptionRepo{getActive: getActive}
	if record != nil {
		subscriptionRepo.getByUserGroup = func(context.Context, int64, int64) (*service.UserSubscription, error) {
			clone := *record
			return &clone, nil
		}
	}
	subscriptionService := service.NewSubscriptionService(nil, subscriptionRepo, nil, nil, cfg)
	t.Cleanup(subscriptionService.Stop)
	apiKeyService := service.NewAPIKeyService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	return apiKeyService, subscriptionService, cfg, apiKey.Key
}

func subscriptionExpiryGuidanceRequest(router *gin.Engine, method, path, apiKey string, codex bool) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("x-api-key", apiKey)
	if codex {
		req.Header.Set("User-Agent", "codex_cli_rs/0.145.0")
		req.Header.Set("originator", "codex_cli_rs")
	}
	router.ServeHTTP(recorder, req)
	return recorder
}
