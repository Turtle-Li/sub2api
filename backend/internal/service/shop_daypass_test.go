//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

func TestShopDaypassSnapshotsOnlyProvenLegacyOrderForBuyer(t *testing.T) {
	s, old, g := newDaypassUnit(t)
	ctx := context.Background()
	row := s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(old.ID)).OnlyX(ctx)
	historical, err := decodeShopAction(row)
	require.NoError(t, err)
	historical.DaypassBuyer = nil
	historical.Request.Version = shopaction.PaidRequestVersion
	raw, err := json.Marshal(historical)
	require.NoError(t, err)
	_, err = s.client.ExecContext(ctx, "UPDATE shop_order_actions SET snapshot=?, snapshot_hash=? WHERE id=?",
		string(raw), fmt.Sprintf("%x", sha256.Sum256(raw)), row.ID)
	require.NoError(t, err)
	claim, err := s.claimDaypass(ctx, old.ID, time.Now())
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.NoError(t, s.deliverDaypass(ctx, claim))
	require.Zero(t, g.lastBinding.Sub2UserID, "historic actions must keep their original receiver binding")
	require.Equal(t, OrderStatusCompleted, s.client.PaymentOrder.GetX(ctx, old.ID).Status)

	detail, err := s.Order(ctx, old.ID, old.UserID, true)
	require.NoError(t, err)
	newPaidOrder := func(user *dbent.User) *dbent.PaymentOrder {
		order := createInvoiceUnitOrder(t, ctx, s.client, user, OrderStatusPaid, true, false)
		order = s.client.PaymentOrder.UpdateOne(order).SetOrderType(payment.OrderTypeManualShop).SetAmount(0).SetPayAmount(old.PayAmount).SaveX(ctx)
		require.NoError(t, createShopFulfillmentTx(ctx, s.client, order, detail.Product))
		require.NoError(t, markShopPaidTx(ctx, s.client, order))
		return order
	}
	other := createInvoiceUnitUser(t, ctx, s.client, "unrelated-daypass-buyer@example.test")
	catalog, err := s.Product(ctx, detail.Product.ID, true)
	require.NoError(t, err)
	catalog.Enabled = true
	_, err = s.SaveProduct(ctx, catalog.ID, *catalog)
	require.NoError(t, err)
	// The receiver returns no duration label after an old admin extension.
	g.cards = []ShopDaypassCard{{CardID: old.ID, KeySuffix: "dEAd", Status: "expired", ExpiresAt: time.Now().Add(-time.Hour).Unix()}}
	_, err = s.BuyerDaypassCards(ctx, other.ID, detail.Product.ID)
	require.NoError(t, err)
	require.Empty(t, g.cardQuery.LegacyOrders)
	cards, err := s.BuyerDaypassCards(ctx, old.UserID, detail.Product.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{old.ID}, g.cardQuery.LegacyOrders)
	require.Len(t, cards.Cards, 1)
	require.Equal(t, 1, cards.Cards[0].LastDays)
	foreign := newPaidOrder(other)
	foreignClaim, err := s.claimDaypass(ctx, foreign.ID, time.Now())
	require.NoError(t, err)
	require.NotNil(t, foreignClaim)
	require.Equal(t, other.ID, foreignClaim.snapshot.DaypassBuyer.UserID)
	require.Zero(t, foreignClaim.snapshot.DaypassBuyer.PriorOrderID)

	user := s.client.User.GetX(ctx, old.UserID)
	renewal := newPaidOrder(user)
	s.client.PaymentOrder.UpdateOne(renewal).SetUserName("name-changed-after-payment").ExecX(ctx)
	renewClaim, err := s.claimDaypass(ctx, renewal.ID, time.Now())
	require.NoError(t, err)
	require.NotNil(t, renewClaim)
	require.Equal(t, shopaction.DaypassPaidRequestVersion, renewClaim.snapshot.Request.Version)
	require.Equal(t, &shopaction.DaypassBuyer{UserID: user.ID, Username: renewal.UserName, PriorOrderID: old.ID}, renewClaim.snapshot.DaypassBuyer)
}

func TestShopDaypassLostReplyRecoversSameGrantAndBuyerOnlyDelivery(t *testing.T) {
	s, o, g := newDaypassUnit(t)
	ctx := context.Background()
	g.lost = true
	claim, err := s.claimDaypass(ctx, o.ID, time.Now())
	require.NoError(t, err)
	require.ErrorIs(t, s.deliverDaypass(ctx, claim), errShopDaypassUnavailable)
	first := *g.grant
	require.Equal(t, o.UserID, g.lastBinding.Sub2UserID)
	require.Equal(t, o.UserName, g.lastBinding.Sub2Username)
	require.Equal(t, 3, g.lastBinding.SKUVersion)
	expireDaypassLease(t, s, o.ID)
	claim, err = s.claimDaypass(ctx, o.ID, time.Now())
	require.NoError(t, err)
	require.NoError(t, s.deliverDaypass(ctx, claim))
	d, err := s.Order(ctx, o.ID, o.UserID, false)
	require.NoError(t, err)
	require.Contains(t, d.DeliveryContent, first.Key)
	require.NotContains(t, d.DeliveryContent, "无需注册或登录New API。")
	require.Contains(t, d.DeliveryContent, time.Unix(first.ExpiresAt, 0).In(time.FixedZone("UTC+8", 8*60*60)).Format("2006-01-02 15:04:05")+"（北京时间 UTC+8）")
	require.NotContains(t, d.DeliveryContent, "（UTC）")
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
