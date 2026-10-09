//go:build unit

package service

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	"github.com/stretchr/testify/require"
)

func TestCollectionCountsPaidCashTowardDailyLimit(t *testing.T) {
	f := newOwnerTestOrderFixture(t)
	ctx := context.Background()
	f.settings.values[SettingDailyRechargeLimit] = "500"
	r, err := f.service.CreateCollectionOrder(ctx, f.request("collection-daily-first", 43200, payment.TypeAlipay))
	require.NoError(t, err)
	o := f.client.PaymentOrder.GetX(ctx, r.OrderID)
	require.NoError(t, f.service.HandlePaymentNotification(ctx, &payment.PaymentNotification{OrderID: o.OutTradeNo, TradeNo: o.PaymentTradeNo, Amount: 432, Status: payment.NotificationStatusSuccess}, payment.TypeUnifiedPay))
	_, err = f.service.CreateCollectionOrder(ctx, f.request("collection-daily-second", 43200, payment.TypeAlipay))
	require.ErrorContains(t, err, "daily_limit_exceeded")
	posts, _, _ := f.central.postSnapshot()
	require.Len(t, posts, 1)
}

func TestCollectionUsesActiveGatewayScopeWhileOwnerTestStaysPinned(t *testing.T) {
	f := newOwnerTestOrderFixture(t)
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	const returnURL = "https://billing.example.test/payment/result"
	gateway, err := unifiedpay.New(unifiedpay.Config{
		Enabled: true, BaseURL: f.central.server.URL, Environment: unifiedpay.EnvironmentLive,
		OrganizationID: ownerTestOrganizationID, ProductID: ownerTestProductID, AppID: ownerTestAppID,
		RequestKeyID: "rotated.request.live.v2", RequestPrivateKey: key,
		WebhookPublicKeys: map[string]ed25519.PublicKey{"sub2.webhook.live.v1": key.Public().(ed25519.PublicKey)},
		ReturnURL:         returnURL, SupportedMethods: []string{payment.TypeAlipay, payment.TypeWxpay}, HTTPClient: f.central.server.Client(),
	})
	require.NoError(t, err)
	f.service.unifiedPayment = gateway
	req := f.request("collection-current-scope", 43200, payment.TypeAlipay)
	r, err := f.service.CreateCollectionOrder(ctx, req)
	require.NoError(t, err)
	again, err := f.service.CreateCollectionOrder(ctx, req)
	require.NoError(t, err)
	require.Equal(t, r.OrderID, again.OrderID)
	ledger, err := ownerTestLedgerFromOrder(f.client.PaymentOrder.GetX(ctx, r.OrderID))
	require.NoError(t, err)
	require.Equal(t, returnURL, ledger.ProviderRequest.ReturnURL)
	require.Equal(t, "rotated.request.live.v2", ledger.RuntimeScope.RequestKeyID)
	posts, _, _ := f.central.postSnapshot()
	require.Len(t, posts, 1)
	var sent ownerTestCentralCreateRequest
	require.NoError(t, json.Unmarshal([]byte(posts[0]), &sent))
	require.Equal(t, returnURL, *sent.ReturnURL)
	_, err = f.service.CreateOwnerTestOrder(ctx, f.request("owner-test-pinned-scope", 1, payment.TypeAlipay))
	require.Error(t, err)
}

func TestCollectionCreatesExactCashWithoutCreditAndReplays(t *testing.T) {
	for _, method := range []string{payment.TypeAlipay, payment.TypeWxpay} {
		t.Run(method, func(t *testing.T) {
			f := newOwnerTestOrderFixture(t)
			ctx := context.Background()
			req := f.request("collection-key-20261009", 43200, method)
			r, err := f.service.CreateCollectionOrder(ctx, req)
			require.NoError(t, err)
			require.Equal(t, float64(0), r.Amount)
			require.Equal(t, float64(432), r.PayAmount)
			again, err := f.service.CreateCollectionOrder(ctx, req)
			require.NoError(t, err)
			require.Equal(t, r.OrderID, again.OrderID)
			posts, _, _ := f.central.postSnapshot()
			require.Len(t, posts, 1)
			var sent ownerTestCentralCreateRequest
			require.NoError(t, json.Unmarshal([]byte(posts[0]), &sent))
			require.Equal(t, "collection", sent.OrderType)
			require.Equal(t, int64(43200), sent.AmountFen)
			req.AmountFen++
			_, err = f.service.CreateCollectionOrder(ctx, req)
			require.Error(t, err)
			o, err := f.client.PaymentOrder.Get(ctx, r.OrderID)
			require.NoError(t, err)
			require.Equal(t, "collection", o.OrderType)
			before, err := f.client.User.Get(ctx, f.userID)
			require.NoError(t, err)
			notification := &payment.PaymentNotification{OrderID: o.OutTradeNo, TradeNo: o.PaymentTradeNo, Amount: 431, Status: payment.NotificationStatusSuccess}
			require.Error(t, f.service.HandlePaymentNotification(ctx, notification, payment.TypeUnifiedPay))
			notification.Amount = 432
			require.NoError(t, f.service.HandlePaymentNotification(ctx, notification, payment.TypeUnifiedPay))
			require.NoError(t, f.service.HandlePaymentNotification(ctx, notification, payment.TypeUnifiedPay))
			after, err := f.client.User.Get(ctx, f.userID)
			require.NoError(t, err)
			require.Equal(t, before.Balance, after.Balance)
			o, err = f.client.PaymentOrder.Get(ctx, o.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusCompleted, o.Status)
			require.Equal(t, 0, f.client.RedeemCode.Query().CountX(ctx))
			require.Equal(t, 0, f.client.UserSubscription.Query().CountX(ctx))
			require.Equal(t, "false", f.settings.values[SettingPaymentEnabled])
			require.False(t, invoiceOrderEligible(o, false))
			review, err := f.service.ReviewRefund(ctx, o.ID)
			require.NoError(t, err)
			require.False(t, review.CanRefund)
		})
	}
}

func TestCollectionRejectsNonAdminAndDisabledAdmin(t *testing.T) {
	for _, role := range []string{RoleUser, RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			f := newOwnerTestOrderFixture(t)
			ctx := context.Background()
			b := f.client.User.UpdateOneID(f.userID).SetRole(role)
			if role == RoleAdmin {
				b.SetStatus(StatusDisabled)
			}
			_, err := b.Save(ctx)
			require.NoError(t, err)
			_, err = f.service.CreateCollectionOrder(ctx, f.request("collection-unauthorized", 43200, payment.TypeAlipay))
			require.Error(t, err)
			require.Equal(t, 0, f.client.PaymentOrder.Query().CountX(ctx))
		})
	}
}

func TestCollectionRejectsInvalidAmountAndPublicCreation(t *testing.T) {
	f := newOwnerTestOrderFixture(t)
	ctx := context.Background()
	for _, fen := range []int64{0, -1, 100000001} {
		_, err := f.service.CreateCollectionOrder(ctx, f.request("collection-invalid-key", fen, payment.TypeAlipay))
		require.Error(t, err)
	}
	_, err := f.service.CreateOrder(ctx, CreateOrderRequest{UserID: f.userID, Amount: 432, PaymentType: payment.TypeAlipay, OrderType: "collection"})
	require.Error(t, err)
	require.Equal(t, 0, f.client.PaymentOrder.Query().CountX(ctx))
	_, err = f.service.CreateOwnerTestOrder(ctx, f.request("owner-test-still-small", 43200, payment.TypeAlipay))
	require.Error(t, err)
}
