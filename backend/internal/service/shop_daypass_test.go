//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/ent/shopnotification"
	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
	"github.com/stretchr/testify/require"
)

func newDaypassUnit(t *testing.T) (*ShopService, *dbent.PaymentOrder, *daypassTestGateway) {
	t.Helper()
	s, _, o, u := newShopUnit(t, ShopAccountDelivery, true)
	ctx := context.Background()
	p, err := s.SaveProduct(ctx, 0, ShopProduct{Name: "DeepSeek日卡", Price: 6, DurationValue: 1, DurationUnit: "day", FulfillmentMode: ShopAccountDelivery, GuideKey: ShopDaypassGuide, PaymentActions: daypassTestActions(s)})
	require.NoError(t, err)
	o = createInvoiceUnitOrder(t, ctx, s.client, u, OrderStatusPaid, true, false)
	o = s.client.PaymentOrder.UpdateOne(o).SetOrderType(payment.OrderTypeManualShop).SetAmount(0).SetPayAmount(6).SaveX(ctx)
	require.NoError(t, createShopFulfillmentTx(ctx, s.client, o, *p))
	require.NoError(t, markShopPaidTx(ctx, s.client, o))
	s.daypassCfg = config.ShopDaypassConfig{Enabled: true, ProductID: p.ID}
	g := &daypassTestGateway{}
	s.daypass = g
	require.Equal(t, u.ID, o.UserID)
	return s, o, g
}

func expireDaypassLease(t *testing.T, s *ShopService, orderID int64) {
	t.Helper()
	s.client.ShopOrderAction.Update().Where(shoporderaction.OrderIDEQ(orderID)).SetLeaseUntil(time.Now().Add(-time.Second)).SetNextAttemptAt(time.Now().Add(-time.Second)).ExecX(context.Background())
}

func TestShopDaypassLostReplyRecoversSameGrantAndBuyerOnlyDelivery(t *testing.T) {
	s, o, g := newDaypassUnit(t)
	ctx := context.Background()
	g.lost = true
	claim, err := s.claimDaypass(ctx, o.ID, time.Now())
	require.NoError(t, err)
	require.ErrorIs(t, s.deliverDaypass(ctx, claim), errShopDaypassUnavailable)
	first := *g.grant
	expireDaypassLease(t, s, o.ID)
	claim, err = s.claimDaypass(ctx, o.ID, time.Now())
	require.NoError(t, err)
	require.NoError(t, s.deliverDaypass(ctx, claim))
	d, err := s.Order(ctx, o.ID, o.UserID, false)
	require.NoError(t, err)
	require.Contains(t, d.DeliveryContent, first.Key)
	require.Contains(t, d.DeliveryContent, time.Unix(first.ExpiresAt, 0).UTC().Format("2006-01-02 15:04:05"))
	admin, err := s.Order(ctx, o.ID, o.UserID, true)
	require.NoError(t, err)
	require.Empty(t, admin.DeliveryContent)
	_, err = s.Order(ctx, o.ID, o.UserID+999, false)
	require.Equal(t, "SHOP_ORDER_NOT_FOUND", infraerrors.Reason(err))
	require.Equal(t, OrderStatusCompleted, s.client.PaymentOrder.GetX(ctx, o.ID).Status)
	require.Equal(t, 1, s.client.ShopNotification.Query().Where(shopnotification.OrderIDEQ(o.ID), shopnotification.ChannelEQ("email")).CountX(ctx))
	require.Equal(t, 2, g.provisions)
	require.Zero(t, g.revocations)
	again, err := s.claimDaypass(ctx, o.ID, time.Now())
	require.NoError(t, err)
	require.Nil(t, again)
	require.NotContains(t, s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).OnlyX(ctx).DeliveryCiphertext, first.Key)
}

func TestShopDaypassLostLeaseCannotShipOrRevokeNewOwner(t *testing.T) {
	s, o, g := newDaypassUnit(t)
	ctx := context.Background()
	old, err := s.claimDaypass(ctx, o.ID, time.Now())
	require.NoError(t, err)
	expireDaypassLease(t, s, o.ID)
	current, err := s.claimDaypass(ctx, o.ID, time.Now())
	require.NoError(t, err)
	require.ErrorIs(t, s.deliverDaypass(ctx, old), errShopDaypassLeaseLost)
	require.Zero(t, g.revocations)
	require.NoError(t, s.deliverDaypass(ctx, current))
	require.Zero(t, g.revocations)
}

func TestShopDaypassFinancialFenceAndExpiredBacklogNeverDeliver(t *testing.T) {
	for _, scenario := range []string{"refund-during-provision", "manual-review", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			s, o, g := newDaypassUnit(t)
			ctx := context.Background()
			switch scenario {
			case "refund-during-provision":
				g.afterProvision = func() { s.client.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRefunding).ExecX(ctx) }
			case "manual-review":
				_, err := s.client.ExecContext(ctx, "INSERT INTO unified_payment_refund_attempts (order_id,needs_manual_review) VALUES (?,?)", o.ID, true)
				require.NoError(t, err)
			case "expired":
				s.client.PaymentOrder.UpdateOneID(o.ID).SetPaidAt(time.Now().Add(-25 * time.Hour)).ExecX(ctx)
			}
			claim, err := s.claimDaypass(ctx, o.ID, time.Now())
			require.NoError(t, err)
			require.NoError(t, s.deliverDaypass(ctx, claim))
			require.Equal(t, 1, g.revocations)
			f := s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).OnlyX(ctx)
			require.Equal(t, ShopClosed, f.Status)
			require.Empty(t, f.DeliveryCiphertext)
			require.Zero(t, s.client.ShopNotification.Query().Where(shopnotification.OrderIDEQ(o.ID), shopnotification.ChannelEQ("email")).CountX(ctx))
			if scenario != "refund-during-provision" {
				require.Zero(t, g.provisions)
			}
		})
	}
}

func TestShopDaypassAdmissionAndManualActionFailClosed(t *testing.T) {
	s, o, _ := newDaypassUnit(t)
	ctx := context.Background()
	d, err := s.Order(ctx, o.ID, o.UserID, true)
	require.NoError(t, err)
	require.False(t, d.CanStart)
	require.False(t, d.CanComplete)
	_, err = s.Action(ctx, o.ID, o.UserID, ShopActionInput{Action: "complete", Revision: d.Revision, DeliveryContent: "manual"})
	require.Equal(t, "SHOP_AUTOMATIC_DELIVERY", infraerrors.Reason(err))
	p := d.Product
	require.NoError(t, s.requireDaypassCheckout(ctx, p))
	s.daypassCfg.Enabled = false
	require.Equal(t, "SHOP_AUTOMATION_UNAVAILABLE", infraerrors.Reason(s.requireDaypassCheckout(ctx, p)))
}

func TestShopDaypassMalformedCandidatesCannotStarveValidOrder(t *testing.T) {
	s, o, g := newDaypassUnit(t)
	ctx := context.Background()
	d, err := s.Order(ctx, o.ID, o.UserID, true)
	require.NoError(t, err)
	u := s.client.User.GetX(ctx, o.UserID)
	for range 4 {
		old := createInvoiceUnitOrder(t, ctx, s.client, u, OrderStatusPaid, true, false)
		old = s.client.PaymentOrder.UpdateOne(old).SetOrderType(payment.OrderTypeManualShop).SetPayAmount(6).SaveX(ctx)
		broken := d.Product
		broken.Price = 99
		require.NoError(t, createShopFulfillmentTx(ctx, s.client, old, broken))
		require.NoError(t, markShopPaidTx(ctx, s.client, old))
		s.client.ShopFulfillment.Update().Where(shopfulfillment.OrderIDEQ(old.ID)).SetUpdatedAt(time.Now().Add(-time.Hour)).ExecX(ctx)
	}
	require.NoError(t, s.RecoverDaypassDeliveries(ctx))
	require.Equal(t, 1, g.provisions)
	require.NoError(t, s.RecoverDaypassDeliveries(ctx))
	require.Equal(t, 1, g.provisions)
	require.Equal(t, 4, s.client.ShopOrderAction.Query().Where(shoporderaction.StatusEQ(shopaction.Review)).CountX(ctx))
}
