//go:build unit

package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingsRechargeBonusRoundTrip(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	h.paymentConfigService = service.NewPaymentConfigService(nil, repo, nil)
	tiers := []any{map[string]any{"min_amount": float64(100), "bonus_percent": float64(10)}}
	rec := doUpdateSettings(t, h, map[string]any{
		"payment_recharge_bonus_tiers":  tiers,
		"payment_recharge_bonus_mode":   "discount",
		"payment_recharge_bonus_notice": "Recharge offer",
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "discount", repo.values[service.SettingRechargeBonusMode])
	require.Equal(t, "Recharge offer", repo.values[service.SettingRechargeBonusNotice])

	assertPayload := func(rec *httptest.ResponseRecorder, wantTiers []any, wantNotice string) {
		t.Helper()
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, "discount", body.Data["payment_recharge_bonus_mode"])
		require.Equal(t, wantTiers, body.Data["payment_recharge_bonus_tiers"])
		require.Equal(t, wantNotice, body.Data["payment_recharge_bonus_notice"])
	}
	assertPayload(rec, tiers, "Recharge offer")
	get := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(get)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	assertPayload(get, tiers, "Recharge offer")

	// Unrelated partial saves preserve the promotion; explicit empty values clear it.
	assertPayload(doUpdateSettings(t, h, map[string]any{"site_name": "Example"}, nil), tiers, "Recharge offer")
	assertPayload(doUpdateSettings(t, h, map[string]any{
		"payment_recharge_bonus_tiers":  []any{},
		"payment_recharge_bonus_notice": "",
	}, nil), []any{}, "")
	rec = doUpdateSettings(t, h, map[string]any{"payment_recharge_bonus_mode": "invalid"}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "discount", repo.values[service.SettingRechargeBonusMode])
}
