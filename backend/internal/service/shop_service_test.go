//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/ent/shopnotification"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type shopTestEncryptor struct{}

func (shopTestEncryptor) Encrypt(s string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func (shopTestEncryptor) Decrypt(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}

type shopTestMail struct {
	bodies   []string
	subjects []string
	err      error
}

func (m *shopTestMail) SendEmail(_ context.Context, _ string, subject, body string) error {
	m.bodies = append(m.bodies, body)
	m.subjects = append(m.subjects, subject)
	return m.err
}

type shopTestFeishu struct {
	messages []string
	err      error
}

func (m *shopTestFeishu) SendText(_ context.Context, message string) error {
	m.messages = append(m.messages, message)
	return m.err
}

func newShopUnit(t *testing.T, mode string, paid bool) (*ShopService, *PaymentService, *dbent.PaymentOrder, *dbent.User) {
	t.Helper()
	ctx := context.Background()
	payments, client := newInvoiceUnitService(t, ctx)
	createInvoiceUnitRefundFenceTables(t, ctx, client)
	u := createInvoiceUnitUser(t, ctx, client, "shop-owner@example.com")
	s := NewShopService(client, shopTestEncryptor{}, &config.Config{Totp: config.TotpConfig{EncryptionKeyConfigured: true}}, nil, nil)
	s.email = &shopTestMail{}
	s.feishu = &shopTestFeishu{}
	payments.shopService = s
	p, err := s.SaveProduct(ctx, 0, ShopProduct{Name: "可配置商品", Price: 108, Enabled: true, FulfillmentMode: mode, GuideKey: "chatgpt-plus-account"})
	require.NoError(t, err)
	o := createInvoiceUnitOrder(t, ctx, client, u, OrderStatusPending, false, false)
	o, err = client.PaymentOrder.UpdateOne(o).SetOrderType(payment.OrderTypeManualShop).SetAmount(0).SetProviderSnapshot(map[string]any{"schema_version": 2, "currency": "CNY"}).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, createShopFulfillmentTx(ctx, client, o, *p))
	if paid {
		require.NoError(t, payments.toPaid(ctx, o, "shop-test-paid", o.PayAmount, ""))
		o, err = client.PaymentOrder.Get(ctx, o.ID)
		require.NoError(t, err)
	}
	return s, payments, o, u
}

func shopValidSession() string {
	return " {\n  \"accessToken\":\"eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjQwNzA5MDg4MDB9.c3ludGhldGljLXNlc3Npb24tc2VjcmV0\",\"user\":{\"email\":\"target@example.com\"},\"expires\":\"2099-01-01T00:00:00Z\"\n}\n"
}

func TestShopOrderFiltersEffectiveStatusBeforePagination(t *testing.T) {
	s, _, initial, u := newShopUnit(t, ShopSessionTopup, true)
	ctx := context.Background()
	detail, err := s.Order(ctx, initial.ID, u.ID, false)
	require.NoError(t, err)
	want := map[string][]int64{ShopWaitingInput: {initial.ID}}
	for _, tc := range []struct {
		payment string
		paid    bool
		stored  string
		visible string
	}{
		{OrderStatusPending, false, ShopWaitingPayment, ShopWaitingPayment},
		{OrderStatusPending, true, ShopWaitingInput, ShopClosed},
		{OrderStatusPaid, false, ShopWaitingInput, ShopClosed},
		{OrderStatusPaid, true, ShopQueued, ShopQueued},
		{OrderStatusPaid, true, ShopProcessing, ShopProcessing},
		{OrderStatusPaid, true, ShopNeedsInput, ShopNeedsInput},
		{OrderStatusCompleted, true, ShopCompleted, ShopCompleted},
		{OrderStatusCancelled, false, ShopWaitingPayment, ShopClosed},
		{OrderStatusExpired, false, ShopWaitingPayment, ShopClosed},
		{OrderStatusFailed, false, ShopWaitingPayment, ShopClosed},
		{OrderStatusRefunding, true, ShopProcessing, ShopClosed},
		{OrderStatusRefunded, true, ShopCompleted, ShopClosed},
		{OrderStatusPaid, true, ShopClosed, ShopClosed},
	} {
		o := createInvoiceUnitOrder(t, ctx, s.client, u, tc.payment, tc.paid, tc.payment == OrderStatusCompleted)
		o = s.client.PaymentOrder.UpdateOne(o).SetOrderType(payment.OrderTypeManualShop).SaveX(ctx)
		require.NoError(t, createShopFulfillmentTx(ctx, s.client, o, detail.Product))
		s.client.ShopFulfillment.Update().Where(shopfulfillment.OrderIDEQ(o.ID)).SetStatus(tc.stored).ExecX(ctx)
		got, err := s.Order(ctx, o.ID, u.ID, false)
		require.NoError(t, err)
		require.Equal(t, tc.visible, got.FulfillmentStatus)
		want[tc.visible] = append(want[tc.visible], o.ID)
	}
	for status, ids := range want {
		t.Run(status, func(t *testing.T) {
			var gotIDs []int64
			for page := 1; page <= (len(ids)+1)/2; page++ {
				list, err := s.Orders(ctx, page, 2, status)
				require.NoError(t, err)
				require.Equal(t, len(ids), list.Total)
				for _, item := range list.Items {
					require.Equal(t, status, item.FulfillmentStatus)
					gotIDs = append(gotIDs, item.OrderID)
				}
			}
			require.ElementsMatch(t, ids, gotIDs)
		})
	}
	all, err := s.Orders(ctx, 1, 100, "")
	require.NoError(t, err)
	require.Equal(t, 14, all.Total)
}

func TestShopPaidManualQueueAndDuplicateCallback(t *testing.T) {
	for _, mode := range []string{ShopSessionTopup, ShopAccountDelivery} {
		t.Run(mode, func(t *testing.T) {
			s, payments, o, u := newShopUnit(t, mode, true)
			ctx := context.Background()
			d, err := s.Order(ctx, o.ID, u.ID, false)
			require.NoError(t, err)
			require.Equal(t, OrderStatusPaid, d.PaymentStatus)
			want := ShopQueued
			if mode == ShopSessionTopup {
				want = ShopWaitingInput
			}
			require.Equal(t, want, d.FulfillmentStatus)
			require.NoError(t, payments.toPaid(ctx, o, "shop-test-paid", o.PayAmount, ""))
			require.Equal(t, 1, s.client.ShopNotification.Query().CountX(ctx))
			require.False(t, isPaymentFulfillmentRecoveryClaimable(o, time.Now().Add(time.Hour)))
			require.Error(t, payments.RetryFulfillment(ctx, o.ID))
			require.NoError(t, payments.executeFulfillment(ctx, o.ID))
			current, err := s.client.PaymentOrder.Get(ctx, o.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusPaid, current.Status)
			require.Zero(t, current.Amount)
		})
	}
}

func TestShopSessionRevisionLockAndCompletionErasure(t *testing.T) {
	s, _, o, u := newShopUnit(t, ShopSessionTopup, true)
	ctx := context.Background()
	d, err := s.Order(ctx, o.ID, u.ID, false)
	require.NoError(t, err)
	d, err = s.SubmitInput(ctx, o.ID, u.ID, d.Revision, shopValidSession())
	require.NoError(t, err)
	require.Equal(t, ShopQueued, d.FulfillmentStatus)
	raw, rev, err := s.RevealInput(ctx, o.ID, 9001, d.Revision)
	require.NoError(t, err)
	require.Equal(t, shopValidSession(), raw)
	require.Equal(t, 1, rev)
	f := s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).OnlyX(ctx)
	require.NotContains(t, f.InputCiphertext, "synthetic-session-secret")
	list, err := s.Orders(ctx, 1, 20, "")
	require.NoError(t, err)
	encoded, err := json.Marshal(list)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "synthetic-session-secret")
	_, err = s.SubmitInput(ctx, o.ID, u.ID, d.Revision-1, shopValidSession())
	require.Equal(t, "SHOP_REVISION_CHANGED", infraerrors.Reason(err))
	d, err = s.Action(ctx, o.ID, 9001, ShopActionInput{Action: "start", Revision: d.Revision, InputRevision: d.InputRevision})
	require.NoError(t, err)
	_, err = s.SubmitInput(ctx, o.ID, u.ID, d.Revision, shopValidSession())
	require.Equal(t, "SHOP_INPUT_NOT_ALLOWED", infraerrors.Reason(err))
	_, err = s.Action(ctx, o.ID, 9001, ShopActionInput{Action: "complete", Revision: d.Revision, InputRevision: 0})
	require.Error(t, err)
	d, err = s.Action(ctx, o.ID, 9001, ShopActionInput{Action: "complete", Revision: d.Revision, InputRevision: d.InputRevision})
	require.NoError(t, err)
	require.Equal(t, ShopCompleted, d.FulfillmentStatus)
	require.Equal(t, OrderStatusCompleted, d.PaymentStatus)
	require.Equal(t, "PENDING", d.EmailStatus)
	f = s.client.ShopFulfillment.GetX(ctx, f.ID)
	require.Empty(t, f.InputCiphertext)
	_, _, err = s.RevealInput(ctx, o.ID, 9001, d.Revision)
	require.Error(t, err)
	_, err = s.Action(ctx, o.ID, 9001, ShopActionInput{Action: "complete", Revision: d.Revision, InputRevision: d.InputRevision})
	require.Error(t, err)
	require.Equal(t, 1, s.client.ShopNotification.Query().Where(shopnotification.ChannelEQ("email")).CountX(ctx))
}

func TestShopRequestInputClearsOldSession(t *testing.T) {
	s, _, o, u := newShopUnit(t, ShopSessionTopup, true)
	ctx := context.Background()
	d, err := s.Order(ctx, o.ID, u.ID, false)
	require.NoError(t, err)
	d, err = s.SubmitInput(ctx, o.ID, u.ID, d.Revision, shopValidSession())
	require.NoError(t, err)
	d, err = s.Action(ctx, o.ID, 9001, ShopActionInput{Action: "request_input", Revision: d.Revision, Note: "请重新登录"})
	require.NoError(t, err)
	require.Equal(t, ShopNeedsInput, d.FulfillmentStatus)
	f := s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).OnlyX(ctx)
	require.Empty(t, f.InputCiphertext)
	require.Empty(t, d.InputAccount)
	d, err = s.SubmitInput(ctx, o.ID, u.ID, d.Revision, strings.ReplaceAll(shopValidSession(), "target@example.com", "updated@example.com"))
	require.NoError(t, err)
	require.Equal(t, 3, d.InputRevision)
	require.NoError(t, s.RecoverNotifications(ctx))
	require.Empty(t, s.email.(*shopTestMail).bodies, "obsolete input-request emails must be suppressed")
}

func TestShopDeliveryOwnerOnlySnapshotAndNotificationRetry(t *testing.T) {
	s, _, o, u := newShopUnit(t, ShopAccountDelivery, true)
	ctx := context.Background()
	d, err := s.Order(ctx, o.ID, u.ID, false)
	require.NoError(t, err)
	_, err = s.Action(ctx, o.ID, 9001, ShopActionInput{Action: "complete", Revision: d.Revision})
	require.Equal(t, "SHOP_DELIVERY_REQUIRED", infraerrors.Reason(err))
	delivery := "账号：buyer@example.com\n密码：<script>fake-secret</script>\n邮箱密码：synthetic-mail"
	d, err = s.Action(ctx, o.ID, 9001, ShopActionInput{Action: "complete", Revision: d.Revision, DeliveryContent: delivery})
	require.NoError(t, err)
	require.Empty(t, d.DeliveryContent, "admin action response must not echo credentials")
	owner, err := s.Order(ctx, o.ID, u.ID, false)
	require.NoError(t, err)
	require.Equal(t, delivery, owner.DeliveryContent)
	_, err = s.Order(ctx, o.ID, u.ID+1, false)
	require.Equal(t, "SHOP_ORDER_NOT_FOUND", infraerrors.Reason(err))
	p, err := s.Product(ctx, d.Product.ID, true)
	require.NoError(t, err)
	p.Name = "已经改名"
	p.Price = 200
	_, err = s.SaveProduct(ctx, p.ID, *p)
	require.NoError(t, err)
	owner, err = s.Order(ctx, o.ID, u.ID, false)
	require.NoError(t, err)
	require.Equal(t, "可配置商品", owner.Product.Name)
	require.Equal(t, 108.0, owner.PayAmount)
	mail := s.email.(*shopTestMail)
	mail.err = errors.New("SMTP fixture unavailable")
	require.NoError(t, s.RecoverNotifications(ctx))
	require.Len(t, mail.bodies, 1)
	require.NotContains(t, mail.bodies[0], "<script>")
	require.Contains(t, mail.bodies[0], "&lt;script&gt;fake-secret&lt;/script&gt;")
	require.Contains(t, mail.bodies[0], "/shop/help/chatgpt-plus-account")
	owner, err = s.Order(ctx, o.ID, u.ID, false)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, owner.PaymentStatus)
	require.Equal(t, "FAILED", owner.EmailStatus)
	mail.err = nil
	_, err = s.RetryNotification(ctx, o.ID, "email")
	require.NoError(t, err)
	require.NoError(t, s.RecoverNotifications(ctx))
	require.Len(t, mail.bodies, 2)
	owner, err = s.Order(ctx, o.ID, u.ID, false)
	require.NoError(t, err)
	require.Equal(t, "SENT", owner.EmailStatus)
	require.Equal(t, delivery, owner.DeliveryContent)
	for _, m := range s.feishu.(*shopTestFeishu).messages {
		require.NotContains(t, m, "fake-secret")
		require.NotContains(t, m, u.Email)
	}
}

func TestShopRejectsUnpaidAndFinancialReviewAndMissingStableKey(t *testing.T) {
	s, _, o, u := newShopUnit(t, ShopSessionTopup, false)
	ctx := context.Background()
	d, err := s.Order(ctx, o.ID, u.ID, false)
	require.NoError(t, err)
	_, err = s.SubmitInput(ctx, o.ID, u.ID, d.Revision, shopValidSession())
	require.Error(t, err)
	_, err = s.Action(ctx, o.ID, 9001, ShopActionInput{Action: "complete", Revision: d.Revision})
	require.Error(t, err)
	s.stableKey = false
	_, err = s.SubmitInput(ctx, o.ID, u.ID, d.Revision, shopValidSession())
	require.Equal(t, "SHOP_ENCRYPTION_UNAVAILABLE", infraerrors.Reason(err))
	s.stableKey = true
	_, err = s.client.PaymentOrder.UpdateOne(o).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).Save(ctx)
	require.NoError(t, err)
	f := s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).OnlyX(ctx)
	_, err = s.client.ShopFulfillment.UpdateOne(f).SetStatus(ShopWaitingInput).Save(ctx)
	require.NoError(t, err)
	_, err = s.client.ExecContext(ctx, "INSERT INTO unified_payment_refund_events (order_id,action) VALUES (?, 'UNIFIED_PAYMENT_EVENT_REJECTED')", o.ID)
	require.NoError(t, err)
	_, err = s.SubmitInput(ctx, o.ID, u.ID, d.Revision, shopValidSession())
	require.Equal(t, "SHOP_FINANCIAL_REVIEW", infraerrors.Reason(err))
}

func TestShopInputValidationAndSecretBindings(t *testing.T) {
	for _, content := range []string{"", "{", `{"user":{"email":"x"}}`, `{"accessToken":"x","expires":"2000-01-01T00:00:00Z"}`, strings.Repeat("x", 128*1024+1)} {
		_, err := validateShopInput(content, "session_json")
		require.Error(t, err)
	}
	account, err := validateShopInput(shopValidSession(), "session_json")
	require.NoError(t, err)
	require.Equal(t, "target@example.com", account)
	s, _, o, _ := newShopUnit(t, ShopSessionTopup, true)
	ciphertext, err := s.encrypt(o.ID, "input", 2, shopValidSession())
	require.NoError(t, err)
	_, err = s.decrypt(o.ID+1, "input", 2, ciphertext)
	require.Error(t, err)
	_, err = s.decrypt(o.ID, "delivery", 2, ciphertext)
	require.Error(t, err)
	_, err = s.decrypt(o.ID, "input", 1, ciphertext)
	require.Error(t, err)
}

func TestShopProductsConfigurableAndCAS(t *testing.T) {
	s, _, _, _ := newShopUnit(t, ShopAccountDelivery, true)
	ctx := context.Background()
	p, err := s.SaveProduct(ctx, 0, ShopProduct{Name: "Gemini", FulfillmentMode: ShopAccountDelivery})
	require.NoError(t, err)
	require.Equal(t, 1, p.DurationValue)
	require.Equal(t, "month", p.DurationUnit)
	require.False(t, p.Enabled)
	list, err := s.Products(ctx, false)
	require.NoError(t, err)
	require.Len(t, list, 1)
	p.Price = 12.345
	_, err = s.SaveProduct(ctx, p.ID, *p)
	require.Error(t, err)
	p.Price = 10
	p.Enabled = true
	updated, err := s.SaveProduct(ctx, p.ID, *p)
	require.NoError(t, err)
	require.Equal(t, 2, updated.Revision)
	_, err = s.SaveProduct(ctx, p.ID, *p)
	require.Equal(t, "SHOP_REVISION_CHANGED", infraerrors.Reason(err))
	_, err = s.SaveProduct(ctx, 0, ShopProduct{Name: "bad link", Price: 10, Enabled: true, FulfillmentMode: ShopSessionTopup, InputURL: "javascript:alert(1)"})
	require.Error(t, err)
}
