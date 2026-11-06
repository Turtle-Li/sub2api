//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestShopDaypassClientRejectsUnboundRepliesAndRedirects(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong-order", "extended-expiry", "invalid-key", "redirect", "wrong-ready-sku", "valid-week", "short-week"} {
		t.Run(scenario, func(t *testing.T) {
			binding := shopDaypassBinding{Instance: shopDaypassInstance, OrderID: 71, ProductID: 8, PriceFen: 600, Currency: "CNY", PaidAt: time.Now().Unix()}
			weekly := scenario == "valid-week" || scenario == "short-week"
			if weekly {
				binding.PriceFen = 3000
			}
			redirected := 0
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected++; w.WriteHeader(500) }))
			defer other.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer "+strings.Repeat("a", 64), r.Header.Get("Authorization"))
				if scenario == "redirect" {
					http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/ready") {
					productID := int64(8)
					if scenario == "wrong-ready-sku" {
						productID = 9
					}
					data := map[string]any{"instance": shopDaypassInstance, "product_id": productID, "status": "ready", "concurrencies": []int{2, 3}}
					if weekly {
						data["duration_days"] = []int{1, 7}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
					return
				}
				var got shopDaypassBinding
				require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
				require.Equal(t, binding, got)
				grant := shopDaypassGrant{Instance: binding.Instance, OrderID: 71, ProductID: 8, PaidAt: binding.PaidAt, ExpiresAt: binding.PaidAt + 86400, TokenID: 19, Status: "active", Key: "sk-" + strings.Repeat("d", 48)}
				switch scenario {
				case "wrong-order":
					grant.OrderID++
				case "extended-expiry":
					grant.ExpiresAt++
				case "invalid-key":
					grant.Key += "\n"
				case "valid-week":
					grant.ExpiresAt = binding.PaidAt + 604800
				}
				if strings.HasSuffix(r.URL.Path, "/revoke") {
					grant.Status, grant.Key = "revoked", ""
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": grant})
			}))
			defer server.Close()
			client := newShopDaypassGateway(8, server.URL).(*shopDaypassClient)
			client.endpoint = server.URL
			client.loadBearer = func(context.Context) (string, error) { return strings.Repeat("a", 64), nil }
			ready := client.Ready(context.Background(), 1)
			if scenario == "wrong-ready-sku" || scenario == "redirect" {
				require.ErrorIs(t, ready, errShopDaypassUnavailable)
			} else {
				require.NoError(t, ready)
			}
			if weekly {
				require.NoError(t, client.Ready(context.Background(), 7))
			} else {
				require.ErrorIs(t, client.Ready(context.Background(), 7), errShopDaypassUnavailable)
			}
			_, err := client.Provision(context.Background(), binding)
			if scenario == "valid" || scenario == "wrong-ready-sku" || scenario == "valid-week" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, errShopDaypassUnavailable)
			}
			if scenario == "valid-week" {
				require.NoError(t, client.Revoke(context.Background(), binding))
			}
			require.Zero(t, redirected)
		})
	}
}
