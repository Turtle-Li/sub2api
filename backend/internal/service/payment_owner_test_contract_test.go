//go:build unit

package service

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestOwnerTestContractCanonicalFen(t *testing.T) {
	for _, fen := range []int64{math.MinInt64, -1, 0, 3, 100, math.MaxInt64} {
		_, _, err := ownerTestCanonicalAmount(fen)
		require.Error(t, err, "unsupported amount %d", fen)
	}
	for _, tc := range []struct {
		fen     int64
		decimal string
		amount  float64
	}{{1, "0.01", 0.01}, {2, "0.02", 0.02}} {
		amount, decimal, err := ownerTestCanonicalAmount(tc.fen)
		require.NoError(t, err)
		require.Equal(t, tc.decimal, decimal)
		require.Equal(t, tc.amount, amount)
	}
}

func TestOwnerTestContractPreservesDisabledPublicConfig(t *testing.T) {
	original := &PaymentConfig{
		Enabled: false, BalanceDisabled: true, MinAmount: 10, MaxAmount: 1000,
		RechargeFeeRate: 12, BalanceRechargeMultiplier: 5, OrderTimeoutMin: 20,
		DailyLimit: 100, MaxPendingOrders: 3, RechargeOptions: []RechargeOption{{}},
	}
	before, err := json.Marshal(original)
	require.NoError(t, err)
	copy, err := ownerTestPaymentConfigCopy(original)
	require.NoError(t, err)
	require.True(t, copy.Enabled)
	require.False(t, copy.BalanceDisabled)
	require.Equal(t, 0.01, copy.MinAmount)
	require.Equal(t, 0.02, copy.MaxAmount)
	require.Zero(t, copy.RechargeFeeRate)
	require.Equal(t, float64(1), copy.BalanceRechargeMultiplier)
	require.Empty(t, copy.RechargeOptions)
	require.Equal(t, original.DailyLimit, copy.DailyLimit)
	require.Equal(t, original.MaxPendingOrders, copy.MaxPendingOrders)
	after, err := json.Marshal(original)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	_, err = ownerTestPaymentConfigCopy(nil)
	require.Error(t, err)
	for _, timeout := range []int{1, 4, 121} {
		_, err = ownerTestPaymentConfigCopy(&PaymentConfig{OrderTimeoutMin: timeout})
		require.Error(t, err)
	}
}

func TestOwnerTestContractKeysAndOwnerIsolation(t *testing.T) {
	for _, value := range []string{"", strings.Repeat("a", 15), strings.Repeat("a", 81), "owner-test-12345\n", "owner test 123456", "owner-test-中文123456"} {
		require.False(t, validOwnerTestIdempotencyKey(value), "invalid key accepted")
	}
	for _, value := range []string{strings.Repeat("a", 16), strings.Repeat("z", 80), "owner-test:abc_1234.5678"} {
		require.True(t, validOwnerTestIdempotencyKey(value))
	}
	keyHash := ownerTestSHA256("owner-test-12345678")
	a := ownerTestOutTradeNo(42, keyHash)
	require.Equal(t, a, ownerTestOutTradeNo(42, keyHash))
	require.NotEqual(t, a, ownerTestOutTradeNo(43, keyHash))
	require.LessOrEqual(t, len(a), 64)
}

func ownerTestContractFixture(t *testing.T) (*ownerTestOrderContext, *dbent.PaymentOrder) {
	t.Helper()
	c := &ownerTestOrderContext{
		input:  OwnerTestOrderRequest{AdminUserID: 42, AmountFen: 1, PaymentType: payment.TypeWxpay, IdempotencyKey: "contract-key-123456"},
		amount: 0.01, amountDecimal: "0.01",
		selection: &payment.InstanceSelection{ProviderKey: payment.TypeUnifiedPay, PaymentMode: "qrcode", SupportedTypes: payment.TypeWxpay},
		scope:     ownerTestRuntimeScope{Environment: ownerTestEnvironment, OrganizationID: ownerTestOrganizationID, ProductID: ownerTestProductID, AppID: ownerTestAppID, BaseURL: "https://pay.totools.cn", RequestKeyID: ownerTestRequestKeyID, ReturnURL: ownerTestReturnURL, PaymentType: payment.TypeWxpay, Currency: "CNY"},
	}
	c.idempotencyKeyHash = ownerTestSHA256(c.input.IdempotencyKey)
	c.outTradeNo = ownerTestOutTradeNo(c.input.AdminUserID, c.idempotencyKeyHash)
	c.payloadHash = ownerTestPayloadHash(c.input.AdminUserID, c.input.AmountFen, c.input.PaymentType)
	c.scopeHash = ownerTestRuntimeScopeHash(c.scope)
	require.NoError(t, c.attachLedger(&PaymentConfig{OrderTimeoutMin: 20}))
	provider := payment.TypeUnifiedPay
	order := &dbent.PaymentOrder{ID: 99, UserID: 42, OutTradeNo: c.outTradeNo, OrderType: payment.OrderTypeBalance, PaymentType: payment.TypeWxpay, Amount: 0.01, PayAmount: 0.01, Status: OrderStatusPending, ExpiresAt: time.Now().Add(time.Minute), ProviderKey: &provider, ProviderSnapshot: ownerTestProviderSnapshot(c)}
	// Exercise the actual JSON round trip used by the durable snapshot column.
	raw, err := json.Marshal(order.ProviderSnapshot)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &order.ProviderSnapshot))
	return c, order
}

func TestOwnerTestContractDurableRequestConflicts(t *testing.T) {
	c, order := ownerTestContractFixture(t)
	ledger, err := validateOwnerTestOrderRecord(order, c)
	require.NoError(t, err)
	require.Equal(t, "0.01", ledger.ProviderRequest.Amount)
	require.Equal(t, int64(1), ledger.ProviderRequest.AmountFen)
	require.Equal(t, 1200, ledger.ProviderRequest.ExpiresInSecond)
	for _, tc := range []struct {
		name   string
		mutate func(*ownerTestOrderContext, *dbent.PaymentOrder)
	}{
		{"different amount", func(c *ownerTestOrderContext, _ *dbent.PaymentOrder) {
			c.input.AmountFen = 2
			c.payloadHash = ownerTestPayloadHash(42, 2, payment.TypeWxpay)
		}},
		{"different owner", func(c *ownerTestOrderContext, _ *dbent.PaymentOrder) { c.input.AdminUserID = 43 }},
		{"different method", func(c *ownerTestOrderContext, _ *dbent.PaymentOrder) { c.input.PaymentType = payment.TypeAlipay }},
		{"runtime destination changed", func(c *ownerTestOrderContext, _ *dbent.PaymentOrder) {
			c.scope.BaseURL = "https://different.example"
			c.scopeHash = ownerTestRuntimeScopeHash(c.scope)
		}},
		{"runtime key changed", func(c *ownerTestOrderContext, _ *dbent.PaymentOrder) {
			c.scope.RequestKeyID = "rotated"
			c.scopeHash = ownerTestRuntimeScopeHash(c.scope)
		}},
		{"row beneficiary changed", func(_ *ownerTestOrderContext, order *dbent.PaymentOrder) { order.UserID = 43 }},
		{"fee added", func(_ *ownerTestOrderContext, order *dbent.PaymentOrder) { order.FeeRate = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, order := ownerTestContractFixture(t)
			tc.mutate(c, order)
			_, err := validateOwnerTestOrderRecord(order, c)
			require.Error(t, err)
		})
	}
}

func TestOwnerTestContractOnlyUnexpiredPendingReturnsCheckout(t *testing.T) {
	for _, tc := range []struct {
		status       string
		expired      bool
		wantCheckout bool
	}{
		{OrderStatusPending, false, true}, {OrderStatusPending, true, false},
		{OrderStatusPaid, false, false}, {OrderStatusCompleted, false, false},
		{OrderStatusFailed, false, false}, {OrderStatusRefunded, false, false},
	} {
		c, order := ownerTestContractFixture(t)
		order.Status = tc.status
		if tc.expired {
			order.ExpiresAt = time.Now().Add(-time.Minute)
		}
		url, qr := "https://pay.totools.cn/checkout/test", "weixin://wxpay/bizpayurl?pr=unit-test"
		order.PayURL, order.QrCode = &url, &qr
		response := buildOwnerTestOrderResponse(order, c, &c.ledger)
		require.Equal(t, order.Status, response.Status)
		require.Equal(t, tc.wantCheckout, response.PayURL != "")
		require.Equal(t, tc.wantCheckout, response.QRCode != "")
	}
}
