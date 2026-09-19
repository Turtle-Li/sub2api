//go:build unit

package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func TestApplyWeChatPaymentResumeClaimsOverridesUntrustedOrderFields(t *testing.T) {
	t.Parallel()

	req := CreateOrderRequest{
		Amount:      0,
		PaymentType: payment.TypeWxpay,
		OrderType:   payment.OrderTypeBalance,
	}

	err := applyWeChatPaymentResumeClaims(&req, &service.WeChatPaymentResumeClaims{
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
		Amount:      "12.50",
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      7,
	})
	if err != nil {
		t.Fatalf("applyWeChatPaymentResumeClaims returned error: %v", err)
	}
	if req.OpenID != "openid-123" {
		t.Fatalf("openid = %q, want %q", req.OpenID, "openid-123")
	}
	if req.Amount != 12.5 {
		t.Fatalf("amount = %v, want 12.5", req.Amount)
	}
	if req.OrderType != payment.OrderTypeSubscription {
		t.Fatalf("order_type = %q, want %q", req.OrderType, payment.OrderTypeSubscription)
	}
	if req.PlanID != 7 {
		t.Fatalf("plan_id = %d, want 7", req.PlanID)
	}
}

type paymentResumeGateSettingRepo struct {
	values map[string]string
}

func (r *paymentResumeGateSettingRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, nil
}

func (r *paymentResumeGateSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	return r.values[key], nil
}

func (r *paymentResumeGateSettingRepo) Set(context.Context, string, string) error { return nil }

func (r *paymentResumeGateSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = r.values[key]
	}
	return values, nil
}

func (r *paymentResumeGateSettingRepo) SetMultiple(context.Context, map[string]string) error {
	return nil
}

func (r *paymentResumeGateSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}

func (r *paymentResumeGateSettingRepo) Delete(context.Context, string) error { return nil }

func TestCreateOrderRejectsSignedSubscriptionResumeWhenQueryClaimsBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const signingKey = "0123456789abcdef0123456789abcdef"
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", signingKey)

	resumeSvc := service.NewPaymentResumeService([]byte(signingKey))
	token, err := resumeSvc.CreateWeChatPaymentResumeToken(service.WeChatPaymentResumeClaims{
		OpenID:      "openid-subscription-resume",
		PaymentType: payment.TypeWxpay,
		Amount:      "0.01",
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      3,
	})
	require.NoError(t, err)

	configSvc := service.NewPaymentConfigService(nil, &paymentResumeGateSettingRepo{values: map[string]string{
		service.SettingPaymentEnabled:         "true",
		service.SettingKeySubscriptionEnabled: "false",
	}}, nil)
	paymentSvc := service.NewPaymentService(nil, payment.NewRegistry(), nil, nil, nil, configSvc, nil, nil, nil)
	h := NewPaymentHandler(paymentSvc, configSvc)

	body, err := json.Marshal(map[string]any{
		"amount":              0.01,
		"payment_type":        payment.TypeWxpay,
		"order_type":          payment.OrderTypeBalance,
		"wechat_resume_token": token,
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/payment/orders", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.CreateOrder(ctx)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	var resp struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, "PLAN_NOT_AVAILABLE", resp.Reason)
}

func TestApplyWeChatPaymentResumeClaimsPreservesResetCardTargetAndIdempotency(t *testing.T) {
	t.Parallel()

	keyHash := service.HashIdempotencyKey("reset-card-wechat-oauth")
	req := CreateOrderRequest{
		PaymentType:            payment.TypeWxpay,
		ResetCardQuantity:      99,
		ResetCardUseOnPurchase: false,
	}
	err := applyWeChatPaymentResumeClaims(&req, &service.WeChatPaymentResumeClaims{
		OpenID:                 "openid-reset-card",
		PaymentType:            payment.TypeWxpay,
		Amount:                 "40.00",
		OrderType:              payment.OrderTypeResetCard,
		PlanID:                 7,
		SubscriptionID:         42,
		ResetCardTierRevision:  "v1:9:gpt:2:123",
		ResetCardQuantity:      3,
		ResetCardUseOnPurchase: true,
		IdempotencyKeyHash:     keyHash,
	})
	require.NoError(t, err)
	require.Equal(t, payment.OrderTypeResetCard, req.OrderType)
	require.EqualValues(t, 7, req.PlanID)
	require.EqualValues(t, 42, req.SubscriptionID)
	require.Equal(t, "v1:9:gpt:2:123", req.ResetCardTierRevision)
	require.Equal(t, 3, req.ResetCardQuantity)
	require.True(t, req.ResetCardUseOnPurchase)
	require.Equal(t, keyHash, req.IdempotencyKeyHash)
}

func TestApplyWeChatPaymentResumeClaimsClearsUnsignedResetCardOptions(t *testing.T) {
	t.Parallel()

	req := CreateOrderRequest{
		PaymentType:            payment.TypeWxpay,
		ResetCardQuantity:      99,
		ResetCardUseOnPurchase: true,
	}
	err := applyWeChatPaymentResumeClaims(&req, &service.WeChatPaymentResumeClaims{
		OpenID:                 "openid-reset-card",
		PaymentType:            payment.TypeWxpay,
		OrderType:              payment.OrderTypeResetCard,
		ResetCardQuantity:      1,
		ResetCardUseOnPurchase: false,
	})
	require.NoError(t, err)
	require.Equal(t, 1, req.ResetCardQuantity)
	require.False(t, req.ResetCardUseOnPurchase)
}

func TestApplyWeChatPaymentResumeClaimsRejectsPaymentTypeMismatch(t *testing.T) {
	t.Parallel()

	req := CreateOrderRequest{
		PaymentType: payment.TypeAlipay,
	}

	err := applyWeChatPaymentResumeClaims(&req, &service.WeChatPaymentResumeClaims{
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
		Amount:      "12.50",
		OrderType:   payment.OrderTypeBalance,
	})
	if err == nil {
		t.Fatal("applyWeChatPaymentResumeClaims should reject mismatched payment types")
	}
}

func TestVerifyOrderPublicReturnsLegacyOrderState(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	db, err := sql.Open("sqlite", "file:payment_handler_public_verify?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	user, err := client.User.Create().
		SetEmail("public-verify@example.com").
		SetPasswordHash("hash").
		SetUsername("public-verify-user").
		Save(context.Background())
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(90.64).
		SetFeeRate(0.03).
		SetRechargeCode("PUBLIC-VERIFY").
		SetOutTradeNo("legacy-order-no").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-public-verify").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(service.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetProviderSnapshot(map[string]any{"currency": "HKD"}).
		Save(context.Background())
	require.NoError(t, err)

	paymentSvc := service.NewPaymentService(client, payment.NewRegistry(), nil, nil, nil, nil, nil, nil, nil)
	h := NewPaymentHandler(paymentSvc, nil)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/payment/public/orders/verify",
		bytes.NewBufferString(`{"out_trade_no":"legacy-order-no"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.VerifyOrderPublic(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "legacy-order-no", resp.Data["out_trade_no"])
	require.Equal(t, service.OrderStatusPending, resp.Data["status"])
	require.Equal(t, false, resp.Data["paid"])
	require.NotEmpty(t, resp.Data["created_at"])
	require.NotEmpty(t, resp.Data["expires_at"])
	for _, field := range []string{
		"id",
		"amount",
		"pay_amount",
		"fee_rate",
		"currency",
		"payment_type",
		"order_type",
		"refund_amount",
		"refund_reason",
		"refund_requested_at",
		"refund_requested_by",
		"refund_request_reason",
		"plan_id",
	} {
		require.NotContains(t, resp.Data, field)
	}
	require.NotZero(t, order.ID)
}

func TestResolveOrderPublicByResumeTokenReturnsFrontendContractFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "0123456789abcdef0123456789abcdef")

	db, err := sql.Open("sqlite", "file:payment_handler_public_resolve?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	user, err := client.User.Create().
		SetEmail("public-resolve@example.com").
		SetPasswordHash("hash").
		SetUsername("public-resolve-user").
		Save(context.Background())
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(103).
		SetFeeRate(0.03).
		SetRechargeCode("PUBLIC-RESOLVE").
		SetOutTradeNo("resolve-order-no").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-public-resolve").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(service.OrderStatusPaid).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetProviderSnapshot(map[string]any{"currency": "USD"}).
		Save(context.Background())
	require.NoError(t, err)

	resumeSvc := service.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := resumeSvc.CreateToken(service.ResumeTokenClaims{
		OrderID:            order.ID,
		UserID:             user.ID,
		PaymentType:        payment.TypeAlipay,
		CanonicalReturnURL: "https://app.example.com/payment/result",
	})
	require.NoError(t, err)

	configSvc := service.NewPaymentConfigService(client, nil, []byte("0123456789abcdef0123456789abcdef"))
	paymentSvc := service.NewPaymentService(client, payment.NewRegistry(), nil, nil, nil, configSvc, nil, nil, nil)
	h := NewPaymentHandler(paymentSvc, nil)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/payment/public/orders/resolve",
		bytes.NewBufferString(`{"resume_token":"`+token+`"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.ResolveOrderPublicByResumeToken(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, float64(order.ID), resp.Data["id"])
	require.Equal(t, "resolve-order-no", resp.Data["out_trade_no"])
	require.Equal(t, 100.0, resp.Data["amount"])
	require.Equal(t, 103.0, resp.Data["pay_amount"])
	require.Equal(t, 0.03, resp.Data["fee_rate"])
	require.Equal(t, "USD", resp.Data["currency"])
	require.Equal(t, payment.TypeAlipay, resp.Data["payment_type"])
	require.Equal(t, payment.OrderTypeBalance, resp.Data["order_type"])
	require.Equal(t, service.OrderStatusPaid, resp.Data["status"])
	require.Contains(t, resp.Data, "created_at")
	require.Contains(t, resp.Data, "expires_at")
	require.Contains(t, resp.Data, "refund_amount")
}

func TestResolveOrderPublicByResumeTokenReturnsBadRequestForMismatchedToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "0123456789abcdef0123456789abcdef")

	db, err := sql.Open("sqlite", "file:payment_handler_public_resolve_mismatch?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	user, err := client.User.Create().
		SetEmail("public-resolve-mismatch@example.com").
		SetPasswordHash("hash").
		SetUsername("public-resolve-mismatch-user").
		Save(context.Background())
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(103).
		SetFeeRate(0.03).
		SetRechargeCode("PUBLIC-RESOLVE-MISMATCH").
		SetOutTradeNo("resolve-order-mismatch-no").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-public-resolve-mismatch").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(service.OrderStatusPaid).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(context.Background())
	require.NoError(t, err)

	resumeSvc := service.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := resumeSvc.CreateToken(service.ResumeTokenClaims{
		OrderID:            order.ID,
		UserID:             user.ID + 999,
		PaymentType:        payment.TypeAlipay,
		CanonicalReturnURL: "https://app.example.com/payment/result",
	})
	require.NoError(t, err)

	configSvc := service.NewPaymentConfigService(client, nil, []byte("0123456789abcdef0123456789abcdef"))
	paymentSvc := service.NewPaymentService(client, payment.NewRegistry(), nil, nil, nil, configSvc, nil, nil, nil)
	h := NewPaymentHandler(paymentSvc, nil)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/payment/public/orders/resolve",
		bytes.NewBufferString(`{"resume_token":"`+token+`"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.ResolveOrderPublicByResumeToken(ctx)

	require.Equal(t, http.StatusBadRequest, recorder.Code)

	var resp struct {
		Code    int    `json:"code"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Equal(t, "INVALID_RESUME_TOKEN", resp.Reason)
}

func TestVerifyOrderPublicRejectsBlankOutTradeNo(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := sql.Open("sqlite", "file:payment_handler_public_verify_blank?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	paymentSvc := service.NewPaymentService(client, payment.NewRegistry(), nil, nil, nil, nil, nil, nil, nil)
	h := NewPaymentHandler(paymentSvc, nil)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/payment/public/orders/verify",
		bytes.NewBufferString(`{"out_trade_no":"   "}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.VerifyOrderPublic(ctx)

	require.Equal(t, http.StatusBadRequest, recorder.Code)

	var resp struct {
		Code   int    `json:"code"`
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Equal(t, "INVALID_OUT_TRADE_NO", resp.Reason)
}

func TestApplyWeChatPaymentResumeClaimsBindsDiscountAndClearsUnsignedDiscount(t *testing.T) {
	req := CreateOrderRequest{PaymentType: "wxpay", CouponCode: "FORGED2026", CouponRevision: "forged"}
	err := applyWeChatPaymentResumeClaims(&req, &service.WeChatPaymentResumeClaims{OpenID: "openid", PaymentType: "wxpay", CouponCode: "SIGNED2026", CouponRevision: "signed-revision"})
	if err != nil {
		t.Fatal(err)
	}
	if req.CouponCode != "SIGNED2026" || req.CouponRevision != "signed-revision" {
		t.Fatal("unsigned coupon replaced signed context")
	}
	err = applyWeChatPaymentResumeClaims(&req, &service.WeChatPaymentResumeClaims{OpenID: "openid", PaymentType: "wxpay"})
	if err != nil {
		t.Fatal(err)
	}
	if req.CouponCode != "" || req.CouponRevision != "" {
		t.Fatal("legacy signed context must not inherit an unsigned coupon")
	}
}
