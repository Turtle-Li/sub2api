//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayKeyUsageReportsActualQuotaCurrency(t *testing.T) {
	settings := service.NewSettingService(&settingHandlerPublicRepoStub{values: map[string]string{
		service.SettingKeyPricingCurrencySettings: `{"settlement_currency":"CNY","usd_to_cny_rate":6.75}`,
	}}, &config.Config{})
	h := &GatewayHandler{settingService: settings}
	for _, tt := range []struct{ name, groupType, currency string }{
		{"wallet", "standard", "CNY"}, {"subscription", "subscription", "USD"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			key := &service.APIKey{ID: 1, Status: service.StatusAPIKeyActive, Quota: 100, QuotaUsed: 10, Group: &service.Group{SubscriptionType: tt.groupType}}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			h.usageQuotaLimited(c, context.Background(), key, nil, nil, nil)
			require.Equal(t, 200, w.Code)
			var result map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
			require.Equal(t, tt.currency, result["unit"])
			quota := result["quota"].(map[string]any)
			require.Equal(t, tt.currency, quota["unit"])
			require.Equal(t, float64(100), quota["limit"], "already denominated data must not be converted again")
		})
	}
}
