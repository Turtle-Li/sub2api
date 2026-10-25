//go:build unit

package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelDefaults_AuthoredCurrency(t *testing.T) {
	for _, tc := range []struct {
		name, query, currency               string
		input, output, read, write, write1h float64
	}{
		{"native Kimi CNY", "model=kimi-k3.1-pro&currency=CNY", "CNY", 13e-6, 68e-6, 1.3e-6, 13e-6, 26e-6},
		{"existing K3 CNY", "model=kimi-k3&currency=CNY", "CNY", 20e-6, 100e-6, 2e-6, 20e-6, 40e-6},
		{"catalog CNY conversion", "model=claude-sonnet-4&currency=CNY", "CNY", 3e-6 * 6.75, 15e-6 * 6.75, .3e-6 * 6.75, 3.75e-6 * 6.75, 0},
		{"legacy USD", "model=claude-sonnet-4", "USD", 3e-6, 15e-6, .3e-6, 3.75e-6, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			router := setupModelDefaultPricingRouter()
			w := httptest.NewRecorder()
			// When
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/channels/model-pricing?"+tc.query, nil))
			// Then
			require.Equal(t, http.StatusOK, w.Code)
			var body struct {
				Data struct {
					Found    bool    `json:"found"`
					Currency string  `json:"currency"`
					Input    float64 `json:"input_price"`
					Output   float64 `json:"output_price"`
					Read     float64 `json:"cache_read_price"`
					Write    float64 `json:"cache_write_price"`
					Write1h  float64 `json:"cache_write_1h_price"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.True(t, body.Data.Found)
			require.Equal(t, tc.currency, body.Data.Currency)
			require.InDelta(t, tc.input, body.Data.Input, 1e-12)
			require.InDelta(t, tc.output, body.Data.Output, 1e-12)
			require.InDelta(t, tc.read, body.Data.Read, 1e-12)
			require.InDelta(t, tc.write, body.Data.Write, 1e-12)
			require.InDelta(t, tc.write1h, body.Data.Write1h, 1e-12)
		})
	}
}

func TestModelDefaults_RejectsUnsupportedCurrency(t *testing.T) {
	router := setupModelDefaultPricingRouter()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/channels/model-pricing?model=kimi-k3.1-pro&currency=EUR", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
}
