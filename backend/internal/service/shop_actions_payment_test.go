//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/ent/shopnotification"
	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
	"github.com/stretchr/testify/require"
)

func TestShopPaidMalformedSnapshotPreservesMoney(t *testing.T) {
	s, payments, order, _ := newShopUnit(t, ShopAccountDelivery, false)
	ctx := context.Background()
	f := s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(order.ID)).OnlyX(ctx)
	f.ProductSnapshot["currency"] = "INVALID"
	raw, err := json.Marshal(f.ProductSnapshot)
	require.NoError(t, err)
	_, err = s.client.ExecContext(ctx, "UPDATE shop_fulfillments SET product_snapshot=? WHERE id=?", string(raw), f.ID)
	require.NoError(t, err)
	require.NoError(t, payments.toPaid(ctx, order, "bad-snapshot-trusted-payment", order.PayAmount, ""))
	paid := s.client.PaymentOrder.GetX(ctx, order.ID)
	require.Equal(t, OrderStatusPaid, paid.Status)
	require.NotNil(t, paid.PaidAt)
	task := s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(order.ID)).OnlyX(ctx)
	require.Equal(t, shopaction.Review, task.Status)
	require.Equal(t, "SNAPSHOT_INVALID", task.ErrorKind)
	require.Equal(t, 1, s.client.ShopNotification.Query().Where(shopnotification.OrderIDEQ(order.ID), shopnotification.EventEQ("action_review")).CountX(ctx))
	require.NoError(t, s.RecoverNotifications(ctx))
	require.Contains(t, s.feishu.(*shopTestFeishu).messages[0], "待复核")
}

func newPendingShopActionUnit(t *testing.T) (*ShopService, *PaymentService, *dbent.PaymentOrder) {
	t.Helper()
	s, payments, o, _ := newShopUnit(t, ShopAccountDelivery, false)
	ctx := context.Background()
	i := shopaction.Integration{ID: "license", Version: 1, Adapter: shopaction.HTTP, Endpoint: "https://hooks.example.com/issue", CredentialAlias: "license", Enabled: true}
	s.actionsCfg = shopaction.Config{Enabled: true, Integrations: []shopaction.Integration{i}}
	detail, err := s.Order(ctx, o.ID, o.UserID, true)
	require.NoError(t, err)
	p := detail.Product
	p.PaymentActions = []shopaction.Binding{{BindingKey: "primary-delivery", Adapter: i.Adapter, IntegrationRef: i.ID, IntegrationVersion: i.Version}}
	stored, err := s.SaveProduct(ctx, p.ID, p)
	require.NoError(t, err)
	raw, err := json.Marshal(stored)
	require.NoError(t, err)
	_, err = s.client.ExecContext(ctx, "UPDATE shop_fulfillments SET product_snapshot=? WHERE order_id=?", string(raw), o.ID)
	require.NoError(t, err)
	return s, payments, o
}

func TestShopActionPaymentUsesCommittedPaidTimeAndDeduplicatesCallbacks(t *testing.T) {
	s, payments, o := newPendingShopActionUnit(t)
	ctx := context.Background()
	fakeTime := time.Now().Add(-3 * time.Hour)
	o.PaidAt = &fakeTime
	for range 3 {
		require.NoError(t, payments.toPaid(ctx, o, "verified-payment", o.PayAmount, ""))
	}
	paid := s.client.PaymentOrder.GetX(ctx, o.ID)
	task := s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).OnlyX(ctx)
	snapshot, err := decodeShopAction(task)
	require.NoError(t, err)
	require.True(t, paid.PaidAt.Equal(snapshot.Request.Order.PaidAt))
	require.False(t, fakeTime.Equal(snapshot.Request.Order.PaidAt))
	require.True(t, task.DeadlineAt.Equal(paid.PaidAt.Add(24*time.Hour)))
	require.Equal(t, 1, s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).CountX(ctx))
	require.Equal(t, 1, s.client.ShopNotification.Query().Where(shopnotification.OrderIDEQ(o.ID)).CountX(ctx))
}

func TestShopActionDatabaseFailureRollsBackWholePaymentTransaction(t *testing.T) {
	s, payments, o := newPendingShopActionUnit(t)
	ctx := context.Background()
	_, err := s.client.ExecContext(ctx, `CREATE TRIGGER reject_shop_action BEFORE INSERT ON shop_order_actions BEGIN SELECT RAISE(ABORT, 'injected database failure'); END`)
	require.NoError(t, err)
	require.Error(t, payments.toPaid(ctx, o, "verified-payment", o.PayAmount, ""))
	stored := s.client.PaymentOrder.GetX(ctx, o.ID)
	require.Equal(t, OrderStatusPending, stored.Status)
	require.Nil(t, stored.PaidAt)
	f := s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).OnlyX(ctx)
	require.Equal(t, ShopWaitingPayment, f.Status)
	require.Zero(t, s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).CountX(ctx))
	require.Zero(t, s.client.ShopNotification.Query().Where(shopnotification.OrderIDEQ(o.ID)).CountX(ctx))
	_, err = s.client.ExecContext(ctx, "DROP TRIGGER reject_shop_action")
	require.NoError(t, err)
	require.NoError(t, payments.toPaid(ctx, o, "verified-payment", o.PayAmount, ""))
	require.Equal(t, shopaction.Pending, s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).OnlyX(ctx).Status)
}
