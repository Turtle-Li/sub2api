//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
	"github.com/stretchr/testify/require"
)

func TestShopActionPublicResponsesOmitPrivateConfiguration(t *testing.T) {
	s, o, _ := newDaypassUnit(t)
	ctx := context.Background()
	admin, err := s.Order(ctx, o.ID, o.UserID, true)
	require.NoError(t, err)
	p := admin.Product
	p.Enabled = true
	_, err = s.SaveProduct(ctx, p.ID, p)
	require.NoError(t, err)
	products, err := s.Products(ctx, false)
	require.NoError(t, err)
	public, err := s.Product(ctx, p.ID, false)
	require.NoError(t, err)
	buyer, err := s.Order(ctx, o.ID, o.UserID, false)
	require.NoError(t, err)
	for _, value := range []any{products, public, buyer} {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		for _, field := range []string{"payment_actions", "integration_ref", "credential_alias", "endpoint", "params", "claim_revision", "error_kind"} {
			require.NotContains(t, string(raw), `"`+field+`"`)
		}
	}
	require.True(t, buyer.Product.AutomaticFulfillment)
	require.NotEmpty(t, admin.Product.PaymentActions)
	require.False(t, admin.CanComplete)
	require.False(t, admin.CanStart)
}

func TestShopActionRepairUsesOriginalSnapshotAndStableIdentity(t *testing.T) {
	s, o, g := newDaypassUnit(t)
	ctx := context.Background()
	original := s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).OnlyX(ctx)
	detail, err := s.Order(ctx, o.ID, o.UserID, true)
	require.NoError(t, err)
	latest := detail.Product
	latest.Price = 12
	latest.PaymentActions = nil
	latest.GuideKey = ""
	latest.DurationUnit = "month"
	_, err = s.SaveProduct(ctx, latest.ID, latest)
	require.NoError(t, err)
	s.client.ShopOrderAction.DeleteOne(original).ExecX(ctx)
	require.NoError(t, s.repairShopActionOutbox(ctx))
	recovered := s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).OnlyX(ctx)
	require.Equal(t, original.ActionKey, recovered.ActionKey)
	require.Equal(t, original.Snapshot, recovered.Snapshot)
	require.True(t, original.DeadlineAt.Equal(recovered.DeadlineAt))
	require.NoError(t, s.repairShopActionOutbox(ctx))
	require.Equal(t, 1, s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).CountX(ctx))
	claim, err := s.claimShopAction(ctx, o.ID, time.Now())
	require.NoError(t, err)
	require.NoError(t, s.executeShopAction(ctx, claim))
	require.Equal(t, 1, g.provisions)
}

func TestShopActionIntegrationVersionCannotRetargetExistingOrder(t *testing.T) {
	s, o, g := newDaypassUnit(t)
	ctx := context.Background()
	s.actionsCfg.Integrations[0].Endpoint = "http://100.64.0.2:17867"
	claim, err := s.claimShopAction(ctx, o.ID, time.Now())
	require.NoError(t, err)
	err = s.executeShopAction(ctx, claim)
	require.Error(t, err)
	require.Equal(t, "INTEGRATION_VERSION_CHANGED", shopaction.Classify(err).Kind)
	require.Zero(t, g.provisions)
	row := s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).OnlyX(ctx)
	require.Equal(t, shopaction.Review, row.Status)
}

type shopTestActionAdapter struct{ received []shopaction.Request }

func (*shopTestActionAdapter) Ready(context.Context) error { return nil }
func (a *shopTestActionAdapter) Execute(_ context.Context, r shopaction.Request) (shopaction.Result, error) {
	a.received = append(a.received, r)
	return shopaction.Result{Version: 1, ActionID: r.ActionID, OrderID: r.Order.ID, Status: "completed", ReceiptID: "receipt-example", Delivery: shopaction.Delivery{Kind: "none"}}, nil
}

func TestShopActionOtherProductCompletesWithoutCredentialPanel(t *testing.T) {
	s, _, _, u := newShopUnit(t, ShopAccountDelivery, false)
	ctx := context.Background()
	i := shopaction.Integration{ID: "license", Version: 1, Adapter: shopaction.HTTP, Endpoint: "https://hooks.example.com/issue", CredentialAlias: "license", Enabled: true}
	s.actionsCfg = shopaction.Config{Enabled: true, Integrations: []shopaction.Integration{i}}
	b := []shopaction.Binding{{BindingKey: "primary-delivery", Adapter: i.Adapter, IntegrationRef: i.ID, IntegrationVersion: i.Version, Params: json.RawMessage(`{"sku":"license-basic"}`)}}
	_, err := s.SaveProduct(ctx, 0, ShopProduct{Name: "需要资料的商品", Price: 3, FulfillmentMode: ShopSessionTopup, PaymentActions: b})
	require.Error(t, err)
	p, err := s.SaveProduct(ctx, 0, ShopProduct{Name: "其他服务", Price: 3, FulfillmentMode: ShopAccountDelivery, PaymentActions: b})
	require.NoError(t, err)
	o := createInvoiceUnitOrder(t, ctx, s.client, u, OrderStatusPaid, true, false)
	o = s.client.PaymentOrder.UpdateOne(o).SetOrderType("manual_shop").SetPayAmount(3).SaveX(ctx)
	require.NoError(t, createShopFulfillmentTx(ctx, s.client, o, *p))
	require.NoError(t, markShopPaidTx(ctx, s.client, o))
	adapter := &shopTestActionAdapter{}
	s.webhook = func(shopaction.Integration) shopaction.Adapter { return adapter }
	require.NoError(t, s.RecoverShopActions(ctx))
	require.Len(t, adapter.received, 1)
	detail, err := s.Order(ctx, o.ID, o.UserID, false)
	require.NoError(t, err)
	require.Equal(t, ShopCompleted, detail.FulfillmentStatus)
	require.Equal(t, "none", detail.DeliveryKind)
	require.Empty(t, detail.DeliveryContent)
	require.Equal(t, "license-basic", func() string {
		var p struct {
			SKU string `json:"sku"`
		}
		require.NoError(t, json.Unmarshal(adapter.received[0].Params, &p))
		return p.SKU
	}())
	require.NoError(t, s.RecoverNotifications(ctx))
	require.Len(t, adapter.received, 1)
	_, err = s.ManageAutomation(ctx, o.ID, u.ID, ShopAutomationInput{Action: "revoke"})
	require.Error(t, err)
}
