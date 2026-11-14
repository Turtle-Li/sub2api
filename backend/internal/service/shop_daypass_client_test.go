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

	"github.com/Wei-Shaw/sub2api/internal/shopaction"
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
					} else {
						data["duration_days"] = []int{1}
					}
					data["sku_versions"] = []int{3}
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
			ready := client.ReadyRenewal(context.Background(), 1)
			if scenario == "wrong-ready-sku" || scenario == "redirect" {
				require.ErrorIs(t, ready, errShopDaypassUnavailable)
			} else {
				require.NoError(t, ready)
			}
			if weekly {
				require.NoError(t, client.ReadyRenewal(context.Background(), 7))
			} else {
				require.ErrorIs(t, client.ReadyRenewal(context.Background(), 7), errShopDaypassUnavailable)
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

func TestShopDaypassClientRequiresRenewalCapability(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-receiver", true: "configured-receiver"}[supported], func(t *testing.T) {
			binding := shopDaypassBinding{Instance: shopDaypassInstance, OrderID: 71, ProductID: 8, PriceFen: 600, Currency: "CNY", PaidAt: time.Now().Unix(), SKUVersion: 2, DurationSeconds: 604800, Concurrency: 3}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/ready") {
					versions := []int{2}
					if supported {
						versions = append(versions, 3)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"instance": shopDaypassInstance, "product_id": 8, "status": "ready", "concurrencies": []int{2, 3}, "duration_days": []int{1, 7}, "sku_versions": versions}})
					return
				}
				var got shopDaypassBinding
				require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
				require.Equal(t, binding, got)
				grant := shopDaypassGrant{Instance: binding.Instance, OrderID: binding.OrderID, ProductID: binding.ProductID, PaidAt: binding.PaidAt, ExpiresAt: binding.PaidAt + 604800, TokenID: 19, Status: "active", Key: "sk-" + strings.Repeat("d", 48)}
				if strings.HasSuffix(r.URL.Path, "/revoke") {
					grant.Status = "revoked"
					grant.Key = ""
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": grant})
			}))
			defer server.Close()
			client := newShopDaypassGateway(8, server.URL).(*shopDaypassClient)
			client.endpoint = server.URL
			client.loadBearer = func(context.Context) (string, error) { return strings.Repeat("a", 64), nil }
			if !supported {
				require.ErrorIs(t, client.ReadyRenewal(context.Background(), 7), errShopDaypassUnavailable)
				return
			}
			require.NoError(t, client.ReadyRenewal(context.Background(), 7))
			grant, err := client.Provision(context.Background(), binding)
			require.NoError(t, err)
			require.Equal(t, binding.PaidAt+604800, grant.ExpiresAt)
			require.NoError(t, client.Revoke(context.Background(), binding))
		})
	}
}

func TestShopDaypassClientChecksBuyerAndRenewalWindow(t *testing.T) {
	ctx := context.Background()
	binding := shopDaypassBinding{
		Instance: shopDaypassInstance, OrderID: 72, ProductID: 8, Currency: "CNY", PriceFen: 800, PaidAt: time.Now().Unix(),
		SKUVersion: shopDaypassRenewalVersion, DurationSeconds: 86400, Concurrency: 3, Sub2UserID: 501, PriorOrderID: 71,
	}
	for _, scenario := range []string{"queued-renewal", "different-buyer", "short-window", "before-payment", "legacy-response"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got shopDaypassBinding
				require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
				require.Equal(t, binding, got)
				grant := shopDaypassGrant{
					Instance: got.Instance, OrderID: got.OrderID, ProductID: got.ProductID, PaidAt: got.PaidAt,
					StartsAt: got.PaidAt + 3600, ExpiresAt: got.PaidAt + 3600 + 86400, SKUVersion: got.SKUVersion, Sub2UserID: got.Sub2UserID,
					Status: "active", TokenID: 19, Key: "sk-" + strings.Repeat("d", 48),
				}
				switch scenario {
				case "different-buyer":
					grant.Sub2UserID++
				case "short-window":
					grant.ExpiresAt--
				case "before-payment":
					grant.StartsAt, grant.ExpiresAt = got.PaidAt-1, got.PaidAt-1+86400
				case "legacy-response":
					grant.SKUVersion, grant.Sub2UserID, grant.StartsAt, grant.ExpiresAt = 0, 0, 0, got.PaidAt+86400
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
			grant, err := client.Provision(ctx, binding)
			if scenario == "queued-renewal" {
				require.NoError(t, err)
				require.Equal(t, binding.PaidAt+3600, grant.StartsAt)
				require.NoError(t, client.Revoke(ctx, binding))
				return
			}
			require.ErrorIs(t, err, errShopDaypassUnavailable)
			require.ErrorIs(t, client.Revoke(ctx, binding), errShopDaypassUnavailable)
		})
	}
}

func TestShopDaypassClientBindsCardListAndGrantToTheRequestedBuyer(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Unix()
	query := shopDaypassCardQuery{UserID: 501, CardID: 71}
	for _, scenario := range []string{"valid", "wrong-buyer", "wrong-product", "different-card", "unsafe-suffix", "wrong-grant"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) || r.Method != http.MethodPost {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				var result any
				switch r.URL.Path {
				case "/api/daypass/automation/cards":
					var got shopDaypassCardQuery
					if json.NewDecoder(r.Body).Decode(&got) != nil || got.UserID != query.UserID || got.CardID != query.CardID {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					list := shopDaypassCardList{Instance: shopDaypassInstance, ProductID: 8, UserID: query.UserID, ServerTime: now,
						Cards: []shopDaypassCardEntry{{LastOrderID: query.CardID, ShopDaypassCard: ShopDaypassCard{CardID: query.CardID, KeySuffix: "1Aw7", LastDays: 7, ExpiresAt: now + 3600, Status: "active"}}}}
					switch scenario {
					case "wrong-buyer":
						list.UserID++
					case "wrong-product":
						list.ProductID++
					case "different-card":
						list.Cards[0].CardID++
					case "unsafe-suffix":
						list.Cards[0].KeySuffix = "sk-unsafe"
					}
					result = list
				case "/api/daypass/automation/provision":
					var input shopDaypassBinding
					if json.NewDecoder(r.Body).Decode(&input) != nil || input.CardAction != shopaction.DaypassRenewCard || input.CardID != query.CardID {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					grant := shopDaypassGrant{Instance: shopDaypassInstance, OrderID: input.OrderID, ProductID: 8, PaidAt: now,
						StartsAt: now + 3600, ExpiresAt: now + 3600 + 86400, SKUVersion: shopDaypassCardsVersion, Sub2UserID: query.UserID,
						Status: "active", TokenID: 19, Key: "sk-" + strings.Repeat("d", 48), CardID: input.CardID}
					if scenario == "wrong-grant" {
						grant.CardID--
					}
					result = grant
				default:
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": result})
			}))
			defer server.Close()
			client := newShopDaypassGateway(8, server.URL).(*shopDaypassClient)
			client.loadBearer = func(context.Context) (string, error) { return strings.Repeat("a", 64), nil }
			list, err := client.BuyerCards(ctx, query)
			if scenario != "valid" && scenario != "wrong-grant" {
				require.ErrorIs(t, err, errShopDaypassUnavailable)
				return
			}
			require.NoError(t, err)
			require.Len(t, list.Cards, 1)
			require.Equal(t, query.CardID, list.Cards[0].CardID)
			grant, err := client.Provision(ctx, shopDaypassBinding{Instance: shopDaypassInstance, OrderID: 72, ProductID: 8, PaidAt: now,
				Currency: "CNY", PriceFen: 600, SKUVersion: shopDaypassCardsVersion, DurationSeconds: 86400, Concurrency: 2,
				Sub2UserID: query.UserID, CardAction: shopaction.DaypassRenewCard, CardID: query.CardID})
			if scenario == "wrong-grant" {
				require.ErrorIs(t, err, errShopDaypassUnavailable)
			} else {
				require.NoError(t, err)
				require.Equal(t, query.CardID, grant.CardID)
			}
		})
	}
}
