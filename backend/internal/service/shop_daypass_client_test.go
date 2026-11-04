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
	binding := shopDaypassBinding{Instance: shopDaypassInstance, OrderID: 71, ProductID: 8, PriceFen: 600, Currency: "CNY", PaidAt: time.Now().Unix()}
	for _, scenario := range []string{"valid", "wrong-order", "extended-expiry", "invalid-key", "redirect", "wrong-ready-sku"} {
		t.Run(scenario, func(t *testing.T) {
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
					_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"instance": shopDaypassInstance, "product_id": productID, "status": "ready"}})
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
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": grant})
			}))
			defer server.Close()
			client := newShopDaypassGateway(8, server.URL).(*shopDaypassClient)
			client.endpoint = server.URL
			client.loadBearer = func(context.Context) (string, error) { return strings.Repeat("a", 64), nil }
			ready := client.Ready(context.Background())
			if scenario == "wrong-ready-sku" || scenario == "redirect" {
				require.ErrorIs(t, ready, errShopDaypassUnavailable)
			} else {
				require.NoError(t, ready)
			}
			_, err := client.Provision(context.Background(), binding)
			if scenario == "valid" || scenario == "wrong-ready-sku" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, errShopDaypassUnavailable)
			}
			require.Zero(t, redirected)
		})
	}
}
