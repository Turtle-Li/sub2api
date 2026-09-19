package service

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type resetCardDispatchProviderStub struct {
	createCalls    int
	queryCalls     int
	providerKey    string
	createResponse *payment.CreatePaymentResponse
	queryResponse  *payment.QueryOrderResponse
}

func (p *resetCardDispatchProviderStub) Name() string { return "reset-card-dispatch-test" }

func (p *resetCardDispatchProviderStub) ProviderKey() string {
	if strings.TrimSpace(p.providerKey) != "" {
		return p.providerKey
	}
	return payment.TypeAlipay
}

func (p *resetCardDispatchProviderStub) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.PaymentType(p.ProviderKey())}
}

func (p *resetCardDispatchProviderStub) CreatePayment(context.Context, payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	p.createCalls++
	return p.createResponse, nil
}

func (p *resetCardDispatchProviderStub) QueryOrder(context.Context, string) (*payment.QueryOrderResponse, error) {
	p.queryCalls++
	return p.queryResponse, nil
}

func (*resetCardDispatchProviderStub) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	return nil, nil
}

func (*resetCardDispatchProviderStub) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, nil
}

type resetCardDispatchLoadBalancer struct {
	config map[string]string
}

func (l resetCardDispatchLoadBalancer) GetInstanceConfig(context.Context, int64) (map[string]string, error) {
	return l.config, nil
}

func (resetCardDispatchLoadBalancer) SelectInstance(context.Context, string, payment.PaymentType, payment.Strategy, float64) (*payment.InstanceSelection, error) {
	return nil, nil
}

type resetCardPaymentEnabledSequenceRepo struct {
	paymentConfigSettingRepoStub
	configReads int
}

func (r *resetCardPaymentEnabledSequenceRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.configReads++
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = r.values[key]
	}
	if r.configReads > 1 {
		values[SettingPaymentEnabled] = "false"
	}
	return values, nil
}

func replaceResetCardPaymentProviderFactoryForTest(t *testing.T, providerStub payment.Provider) {
	t.Helper()
	previous := createPaymentProviderFromInstance
	createPaymentProviderFromInstance = func(string, string, map[string]string) (payment.Provider, error) {
		return providerStub, nil
	}
	t.Cleanup(func() { createPaymentProviderFromInstance = previous })
}

func TestCreateOrderRejectsUnknownOrderTypeBeforeDependencies(t *testing.T) {
	_, err := (&PaymentService{}).CreateOrder(context.Background(), CreateOrderRequest{
		OrderType:   "unexpected",
		PaymentType: payment.TypeAlipay,
		Amount:      10,
	})
	require.Equal(t, "INVALID_ORDER_TYPE", infraerrors.Reason(err))
}

func TestCreateResetCardOrderRejectsNonFiniteAmountBeforeReplayLookup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		amount float64
	}{
		{name: "zero", amount: 0},
		{name: "negative", amount: -1},
		{name: "nan", amount: math.NaN()},
		{name: "infinite", amount: math.Inf(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&PaymentService{}).CreateOrder(context.Background(), CreateOrderRequest{
				Amount:         tc.amount,
				PaymentType:    payment.TypeAlipay,
				OrderType:      payment.OrderTypeResetCard,
				SubscriptionID: 7,
				IdempotencyKey: "invalid-amount-must-not-replay",
			})
			require.Equal(t, "INVALID_AMOUNT", infraerrors.Reason(err))
		})
	}
}

func TestValidateResetCardOrderRejectsNonWalletExternalMethods(t *testing.T) {
	for _, method := range []string{payment.TypeStripe, payment.TypeAirwallex, payment.TypeEasyPay} {
		_, err := (&PaymentService{subscriptionSvc: &SubscriptionService{}}).validateResetCardOrder(
			context.Background(),
			CreateOrderRequest{SubscriptionID: 7, PaymentType: method},
		)
		require.Equal(t, "RESET_CARD_PAYMENT_METHOD_UNSUPPORTED", infraerrors.Reason(err), method)
	}
}

func TestCreateResetCardOrderRejectsEasyPayBackingProvider(t *testing.T) {
	err := validateResetCardSelectedProvider(&payment.InstanceSelection{ProviderKey: payment.TypeEasyPay})
	require.Equal(t, "RESET_CARD_PAYMENT_PROVIDER_UNSUPPORTED", infraerrors.Reason(err))
	for _, providerKey := range []string{payment.TypeAlipay, payment.TypeWxpay, payment.TypeUnifiedPay} {
		require.NoError(t, validateResetCardSelectedProvider(&payment.InstanceSelection{ProviderKey: providerKey}), providerKey)
	}
}

func TestResetCardReplayReconcilesButDoesNotCreateCheckoutAfterPaymentDisabled(t *testing.T) {
	ctx := context.Background()
	svc, order, req := newResetCardDispatchTestOrder(t, false)
	instance, err := svc.entClient.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("Reset card replay provider").
		SetConfig(`{}`).
		SetSupportedTypes(payment.TypeAlipay).
		SetPaymentMode("popup").
		SetEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	snapshot := clonePaymentOrderSnapshot(order.ProviderSnapshot)
	snapshot["provider_instance_id"] = strconv.FormatInt(instance.ID, 10)
	order, err = svc.entClient.PaymentOrder.UpdateOneID(order.ID).
		SetProviderInstanceID(strconv.FormatInt(instance.ID, 10)).
		SetProviderKey(payment.TypeAlipay).
		SetProviderSnapshot(snapshot).
		Save(ctx)
	require.NoError(t, err)

	providerStub := &resetCardDispatchProviderStub{
		createResponse: &payment.CreatePaymentResponse{
			TradeNo: "reset-card-unexpected-create",
			PayURL:  "https://pay.example.test/reset-card-unexpected-create",
		},
		queryResponse: &payment.QueryOrderResponse{
			Status:   payment.ProviderStatusPending,
			Metadata: map[string]string{payment.QueryMetadataOrderNotFound: "true"},
		},
	}
	replaceResetCardPaymentProviderFactoryForTest(t, providerStub)
	svc.loadBalancer = resetCardDispatchLoadBalancer{config: map[string]string{}}
	configRepo := &resetCardPaymentEnabledSequenceRepo{paymentConfigSettingRepoStub: paymentConfigSettingRepoStub{values: map[string]string{
		SettingPaymentEnabled: "true",
	}}}
	svc.configService = &PaymentConfigService{settingRepo: configRepo}

	_, err = svc.replayResetCardOrderRecord(ctx, order, req)
	require.Equal(t, "PAYMENT_DISABLED", infraerrors.Reason(err))
	require.GreaterOrEqual(t, configRepo.configReads, 2, "provider creation must re-read payment_enabled after replay loaded its configuration")
	require.Equal(t, 1, providerStub.queryCalls, "an uncertain existing checkout may still be reconciled")
	require.Zero(t, providerStub.createCalls, "a disabled payment system must not create a new upstream checkout")
}

func TestResetCardReplayDoesNotCreateProviderCheckoutWithLessThanMinimumLifetime(t *testing.T) {
	ctx := context.Background()
	svc, order, req := newResetCardDispatchTestOrder(t, false)
	order = bindResetCardDispatchTestProvider(t, ctx, svc, order, payment.TypeAlipay, nil)
	var err error
	order, err = svc.entClient.PaymentOrder.UpdateOneID(order.ID).
		SetExpiresAt(time.Now().Add(resetCardProviderMinimumLifetime - time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	providerStub := &resetCardDispatchProviderStub{
		queryResponse: &payment.QueryOrderResponse{
			Status:   payment.ProviderStatusPending,
			Metadata: map[string]string{payment.QueryMetadataOrderNotFound: "true"},
		},
		createResponse: &payment.CreatePaymentResponse{TradeNo: "must-not-create", PayURL: "https://pay.example.test/must-not-create"},
	}
	configureResetCardDispatchTestProvider(t, svc, providerStub, map[string]string{})

	_, err = svc.replayResetCardOrderRecord(ctx, order, req)
	require.Equal(t, "RESET_CARD_PAYMENT_CREATE_UNCONFIRMED", infraerrors.Reason(err))
	require.Equal(t, 1, providerStub.queryCalls, "an uncertain deterministic order must still be queried")
	require.Zero(t, providerStub.createCalls, "a provider checkout must not outlive the local reset-card order")
	reloaded, reloadErr := svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, reloadErr)
	dispatch, present, dispatchErr := resetCardDispatchFromOrder(reloaded)
	require.NoError(t, dispatchErr)
	require.True(t, present)
	require.True(t, dispatch.NeedsReconcile, "the next retry must query the deterministic provider order again")
}

func TestResetCardWechatReplayQueriesOriginalOrderBeforeExpiredOAuthBinding(t *testing.T) {
	ctx := context.Background()
	svc, order, req := newResetCardDispatchTestOrder(t, false)
	req.PaymentType = payment.TypeWxpay
	wxConfig := map[string]string{"appId": "wx-base-app", "mpAppId": "wx-mp-app", "mchId": "wx-merchant"}
	order = bindResetCardDispatchTestProvider(t, ctx, svc, order, payment.TypeWxpay, map[string]any{
		"merchant_app_id": "wx-mp-app",
		"merchant_id":     "wx-merchant",
		"checkout_mode":   "jsapi",
	})
	providerStub := &resetCardDispatchProviderStub{
		providerKey: payment.TypeWxpay,
		queryResponse: &payment.QueryOrderResponse{
			Status: payment.ProviderStatusPending,
			Amount: order.PayAmount,
		},
	}
	configureResetCardDispatchTestProvider(t, svc, providerStub, wxConfig)

	_, err := svc.replayResetCardOrderRecord(ctx, order, req)
	require.Equal(t, "RESET_CARD_PAYMENT_CREATE_UNCONFIRMED", infraerrors.Reason(err))
	require.Equal(t, 1, providerStub.queryCalls, "provider lookup must not depend on an expired OAuth OpenID")
	require.Zero(t, providerStub.createCalls)
}

func TestResetCardWechatReplayRequiresOAuthBindingBeforeRecreate(t *testing.T) {
	ctx := context.Background()
	svc, order, req := newResetCardDispatchTestOrder(t, false)
	req.PaymentType = payment.TypeWxpay
	wxConfig := map[string]string{"appId": "wx-base-app", "mpAppId": "wx-mp-app", "mchId": "wx-merchant"}
	order = bindResetCardDispatchTestProvider(t, ctx, svc, order, payment.TypeWxpay, map[string]any{
		"merchant_app_id": "wx-mp-app",
		"merchant_id":     "wx-merchant",
		"checkout_mode":   "jsapi",
	})
	providerStub := &resetCardDispatchProviderStub{
		providerKey: payment.TypeWxpay,
		queryResponse: &payment.QueryOrderResponse{
			Status:   payment.ProviderStatusPending,
			Metadata: map[string]string{payment.QueryMetadataOrderNotFound: "true"},
		},
	}
	configureResetCardDispatchTestProvider(t, svc, providerStub, wxConfig)

	_, err := svc.replayResetCardOrderRecord(ctx, order, req)
	require.Equal(t, "RESET_CARD_PAYMENT_BINDING_CHANGED", infraerrors.Reason(err))
	require.Equal(t, 1, providerStub.queryCalls, "absence must be proved before considering another JSAPI create")
	require.Zero(t, providerStub.createCalls, "a new JSAPI checkout requires the trusted OAuth OpenID binding")
}

func TestResetCardWechatReplayRequiresJSAPIOpenIDWhenMerchantAppIDsMatch(t *testing.T) {
	ctx := context.Background()
	svc, order, req := newResetCardDispatchTestOrder(t, false)
	req.PaymentType = payment.TypeWxpay
	wxConfig := map[string]string{"appId": "wx-shared-app", "mchId": "wx-merchant"}
	order = bindResetCardDispatchTestProvider(t, ctx, svc, order, payment.TypeWxpay, map[string]any{
		"merchant_app_id": "wx-shared-app",
		"merchant_id":     "wx-merchant",
		"checkout_mode":   "jsapi",
	})
	providerStub := &resetCardDispatchProviderStub{
		providerKey: payment.TypeWxpay,
		queryResponse: &payment.QueryOrderResponse{
			Status:   payment.ProviderStatusPending,
			Metadata: map[string]string{payment.QueryMetadataOrderNotFound: "true"},
		},
	}
	configureResetCardDispatchTestProvider(t, svc, providerStub, wxConfig)

	_, err := svc.replayResetCardOrderRecord(ctx, order, req)
	require.Equal(t, "RESET_CARD_PAYMENT_BINDING_CHANGED", infraerrors.Reason(err))
	require.Equal(t, 1, providerStub.queryCalls, "the original deterministic order must be queried before OAuth binding is required")
	require.Zero(t, providerStub.createCalls, "matching AppIDs cannot turn a JSAPI order into a native checkout")
}

func TestValidateResetCardDispatchBindingAllowsOriginalWxpayJSAPIMode(t *testing.T) {
	order := &dbent.PaymentOrder{
		PaymentType: payment.TypeWxpay,
		ProviderSnapshot: map[string]any{
			"schema_version":       2,
			"provider_instance_id": "88",
			"provider_key":         payment.TypeWxpay,
			"currency":             payment.DefaultPaymentCurrency,
			"merchant_app_id":      "wx-shared-app",
			"merchant_id":          "wx-merchant",
			"checkout_mode":        "jsapi",
		},
	}
	selection := &payment.InstanceSelection{
		InstanceID:  "88",
		ProviderKey: payment.TypeWxpay,
		Config: map[string]string{
			"appId": "wx-shared-app",
			"mchId": "wx-merchant",
		},
	}

	require.NoError(t, validateResetCardDispatchBinding(order, CreateOrderRequest{OpenID: "openid-88", IsMobile: true}, selection))
}

func TestValidateResetCardDispatchBindingRejectsLegacyWxpaySnapshotBeforeRecreate(t *testing.T) {
	order := &dbent.PaymentOrder{
		PaymentType: payment.TypeWxpay,
		ProviderSnapshot: map[string]any{
			"schema_version":       2,
			"provider_instance_id": "88",
			"provider_key":         payment.TypeWxpay,
			"currency":             payment.DefaultPaymentCurrency,
			"merchant_app_id":      "wx-shared-app",
			"merchant_id":          "wx-merchant",
		},
	}
	selection := &payment.InstanceSelection{
		InstanceID:  "88",
		ProviderKey: payment.TypeWxpay,
		Config: map[string]string{
			"appId": "wx-shared-app",
			"mchId": "wx-merchant",
		},
	}

	err := validateResetCardDispatchBinding(order, CreateOrderRequest{OpenID: "openid-88"}, selection)
	require.Equal(t, "RESET_CARD_PAYMENT_BINDING_CHANGED", infraerrors.Reason(err))
}

func TestResetCardOrderEffectiveDeadlineReservesProviderAndDispatchHeadroom(t *testing.T) {
	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	for _, timeoutMinutes := range []int{5, 6, 7, 8} {
		t.Run(strconv.Itoa(timeoutMinutes)+" minutes", func(t *testing.T) {
			expiresAt, err := resetCardOrderEffectiveDeadline(now, timeoutMinutes, now.Add(24*time.Hour))
			require.NoError(t, err)
			require.Equal(t, now.Add(resetCardMinimumExternalCheckoutLifetime), expiresAt)
			providerCallAt := now.Add(resetCardDispatchLeaseDuration + resetCardOrderExpirySafetyMargin - time.Nanosecond)
			require.GreaterOrEqual(t, paymentOrderExpiresInSeconds(expiresAt, providerCallAt), int(resetCardProviderMinimumLifetime/time.Second))
		})
	}

	expiresAt, err := resetCardOrderEffectiveDeadline(now, 9, now.Add(24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, now.Add(9*time.Minute), expiresAt)

	_, err = resetCardOrderEffectiveDeadline(now, 30, now.Add(resetCardMinimumExternalCheckoutLifetime))
	require.ErrorIs(t, err, ErrResetCardPurchaseUnavailable, "a subscription cutoff cannot shorten a provider checkout below its safe lifetime")

	cutoff := now.Add(resetCardMinimumExternalCheckoutLifetime + time.Second)
	expiresAt, err = resetCardOrderEffectiveDeadline(now, 30, cutoff)
	require.NoError(t, err)
	require.Equal(t, cutoff, expiresAt)
}

func TestNormalizeResetCardOrderIdempotency(t *testing.T) {
	req := CreateOrderRequest{
		OrderType:      payment.OrderTypeResetCard,
		IdempotencyKey: " reset-card-attempt-001 ",
	}
	require.NoError(t, normalizeResetCardOrderIdempotency(&req))
	require.Empty(t, req.IdempotencyKey)
	require.Equal(t, HashIdempotencyKey("reset-card-attempt-001"), req.IdempotencyKeyHash)

	missing := CreateOrderRequest{OrderType: payment.OrderTypeResetCard}
	require.ErrorIs(t, normalizeResetCardOrderIdempotency(&missing), ErrIdempotencyKeyRequired)

	invalidHash := CreateOrderRequest{OrderType: payment.OrderTypeResetCard, IdempotencyKeyHash: "not-a-hash"}
	require.ErrorIs(t, normalizeResetCardOrderIdempotency(&invalidHash), ErrIdempotencyKeyInvalid)

	conflict := CreateOrderRequest{
		OrderType:          payment.OrderTypeResetCard,
		IdempotencyKey:     "reset-card-attempt-001",
		IdempotencyKeyHash: strings.Repeat("a", 64),
	}
	require.ErrorIs(t, normalizeResetCardOrderIdempotency(&conflict), ErrIdempotencyKeyConflict)
}

func TestResetCardOrderOutTradeNoIsStableAndActorScoped(t *testing.T) {
	hash := HashIdempotencyKey("reset-card-attempt-002")
	first := resetCardOrderOutTradeNo(11, hash)
	require.Equal(t, first, resetCardOrderOutTradeNo(11, hash))
	require.NotEqual(t, first, resetCardOrderOutTradeNo(12, hash))
	require.NotEqual(t, first, resetCardOrderOutTradeNo(11, HashIdempotencyKey("reset-card-attempt-003")))
	require.LessOrEqual(t, len(first), 64)
	require.True(t, strings.HasPrefix(first, "sub2_reset_"))
}

func TestValidateResetCardOrderRecordRejectsPayloadReuse(t *testing.T) {
	keyHash := HashIdempotencyKey("reset-card-attempt-004")
	req := CreateOrderRequest{
		UserID:             11,
		Amount:             40,
		PaymentType:        payment.TypeAlipay,
		OrderType:          payment.OrderTypeResetCard,
		PlanID:             7,
		SubscriptionID:     42,
		IdempotencyKeyHash: keyHash,
	}
	order := &dbent.PaymentOrder{
		UserID:      req.UserID,
		Amount:      req.Amount,
		PaymentType: req.PaymentType,
		OrderType:   req.OrderType,
		OutTradeNo:  resetCardOrderOutTradeNo(req.UserID, keyHash),
		ProductSnapshot: map[string]any{
			"kind":                   "reset_card",
			"subscription_id":        req.SubscriptionID,
			"plan_id":                req.PlanID,
			"price":                  req.Amount,
			"idempotency_key_sha256": keyHash,
		},
	}
	require.NoError(t, validateResetCardOrderRecord(order, req, nil))

	changedTarget := req
	changedTarget.SubscriptionID++
	require.ErrorIs(t, validateResetCardOrderRecord(order, changedTarget, nil), ErrIdempotencyKeyConflict)

	changedAmount := req
	changedAmount.Amount++
	require.ErrorIs(t, validateResetCardOrderRecord(order, changedAmount, nil), ErrIdempotencyKeyConflict)

	changedMethod := req
	changedMethod.PaymentType = payment.TypeWxpay
	require.ErrorIs(t, validateResetCardOrderRecord(order, changedMethod, nil), ErrIdempotencyKeyConflict)
}

func TestValidateResetCardPaymentOrderSnapshotRequiresBoundPaymentFacts(t *testing.T) {
	planID, groupID := int64(7), int64(4)
	createdAt := time.Date(2026, time.September, 13, 2, 0, 0, 0, time.UTC)
	keyHash := HashIdempotencyKey("reset-card-attempt-005")
	order := &dbent.PaymentOrder{
		ID:                  9,
		UserID:              11,
		Amount:              40,
		PayAmount:           40,
		PaymentType:         payment.TypeAlipay,
		OrderType:           payment.OrderTypeResetCard,
		OutTradeNo:          resetCardOrderOutTradeNo(11, keyHash),
		PlanID:              &planID,
		SubscriptionGroupID: &groupID,
		CreatedAt:           createdAt,
		ProviderSnapshot: map[string]any{
			"schema_version": 2,
			"provider_key":   payment.TypeAlipay,
			"currency":       payment.DefaultPaymentCurrency,
		},
		ProductSnapshot: map[string]any{
			"kind":                    "reset_card",
			"subscription_id":         int64(42),
			"plan_id":                 planID,
			"group_id":                groupID,
			"currency":                payment.DefaultPaymentCurrency,
			"price":                   float64(40),
			"order_amount":            float64(40),
			"pay_amount":              float64(40),
			"quantity":                1,
			"grant_expiry_policy":     "subscription",
			"subscription_expires_at": createdAt.Add(24 * time.Hour).Format(time.RFC3339Nano),
			"idempotency_key_sha256":  keyHash,
		},
	}
	targetID, gotGroupID, err := validateResetCardPaymentOrderSnapshot(order)
	require.NoError(t, err)
	require.Equal(t, int64(42), targetID)
	require.Equal(t, groupID, gotGroupID)

	for name, mutate := range map[string]func(*dbent.PaymentOrder){
		"wrong order type":          func(o *dbent.PaymentOrder) { o.OrderType = payment.OrderTypeBalance },
		"missing provider currency": func(o *dbent.PaymentOrder) { delete(o.ProviderSnapshot, "currency") },
		"foreign currency":          func(o *dbent.PaymentOrder) { o.ProviderSnapshot["currency"] = "USD" },
		"unbound idempotency":       func(o *dbent.PaymentOrder) { o.OutTradeNo = "sub2_reset_wrong" },
		"wrong group":               func(o *dbent.PaymentOrder) { o.ProductSnapshot["group_id"] = int64(5) },
		"expired at creation": func(o *dbent.PaymentOrder) {
			o.ProductSnapshot["subscription_expires_at"] = createdAt.Add(-time.Second).Format(time.RFC3339Nano)
		},
	} {
		t.Run(name, func(t *testing.T) {
			copyOrder := *order
			copyOrder.ProductSnapshot = clonePaymentOrderSnapshot(order.ProductSnapshot)
			copyOrder.ProviderSnapshot = clonePaymentOrderSnapshot(order.ProviderSnapshot)
			mutate(&copyOrder)
			_, _, err := validateResetCardPaymentOrderSnapshot(&copyOrder)
			require.Error(t, err)
		})
	}
}

func TestResetCardProviderSelectionRevalidationUsesLockedInstance(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	configService := &PaymentConfigService{entClient: client}
	baseConfig := map[string]string{
		"appId":      "alipay-original-app",
		"privateKey": "alipay-original-private-key",
		"notifyUrl":  "https://merchant.example.test/original",
	}
	encodedConfig, err := configService.encryptConfig(baseConfig)
	require.NoError(t, err)
	instance, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("reset-card-selection-lock").
		SetConfig(encodedConfig).
		SetSupportedTypes(payment.TypeAlipay).
		SetPaymentMode("popup").
		SetEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	service := &PaymentService{entClient: client, configService: configService}
	selection := &payment.InstanceSelection{
		InstanceID:     strconv.FormatInt(instance.ID, 10),
		ProviderKey:    payment.TypeAlipay,
		Config:         map[string]string{"appId": "alipay-original-app", "privateKey": "alipay-original-private-key", "notifyUrl": "https://merchant.example.test/stale"},
		SupportedTypes: payment.TypeAlipay,
		PaymentMode:    "popup",
	}
	req := CreateOrderRequest{OrderType: payment.OrderTypeResetCard, PaymentType: payment.TypeAlipay}

	t.Run("adopts the locked configuration instead of a stale safe field", func(t *testing.T) {
		tx, txErr := client.Tx(ctx)
		require.NoError(t, txErr)
		defer func() { _ = tx.Rollback() }()

		locked, lockErr := service.revalidateProviderSelectionInTx(ctx, tx, req, selection)
		require.NoError(t, lockErr)
		require.Equal(t, "https://merchant.example.test/original", locked.Config["notifyUrl"])
		require.Equal(t, "popup", locked.Config["paymentMode"])
	})

	t.Run("rejects a protected identity change observed after selection", func(t *testing.T) {
		rotatedConfig := map[string]string{
			"appId":      "alipay-rotated-app",
			"privateKey": "alipay-original-private-key",
			"notifyUrl":  "https://merchant.example.test/original",
		}
		encodedRotatedConfig, encodeErr := configService.encryptConfig(rotatedConfig)
		require.NoError(t, encodeErr)
		_, updateErr := client.PaymentProviderInstance.UpdateOneID(instance.ID).SetConfig(encodedRotatedConfig).Save(ctx)
		require.NoError(t, updateErr)

		tx, txErr := client.Tx(ctx)
		require.NoError(t, txErr)
		defer func() { _ = tx.Rollback() }()
		_, lockErr := service.revalidateProviderSelectionInTx(ctx, tx, req, selection)
		require.Equal(t, "RESET_CARD_PAYMENT_BINDING_CHANGED", infraerrors.Reason(lockErr))
	})

	t.Run("unified payment has no provider-instance row to lock", func(t *testing.T) {
		unified := &payment.InstanceSelection{ProviderKey: payment.TypeUnifiedPay}
		locked, lockErr := (&PaymentService{}).revalidateProviderSelectionInTx(ctx, nil, req, unified)
		require.NoError(t, lockErr)
		require.Same(t, unified, locked)
	})
}

func TestOrdinaryPaymentProviderSelectionIsRevalidatedBeforeOrderInsert(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	configService := &PaymentConfigService{entClient: client}
	storedConfig := map[string]string{
		"appId":      "alipay-current-app",
		"privateKey": "alipay-private-key",
		"notifyUrl":  "https://merchant.example.test/current",
	}
	encodedConfig, err := configService.encryptConfig(storedConfig)
	require.NoError(t, err)
	instance, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("ordinary-payment-selection-lock").
		SetConfig(encodedConfig).
		SetSupportedTypes(payment.TypeAlipay).
		SetPaymentMode("popup").
		SetEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	staleSelection := &payment.InstanceSelection{
		InstanceID:     strconv.FormatInt(instance.ID, 10),
		ProviderKey:    payment.TypeAlipay,
		Config:         map[string]string{"appId": "alipay-stale-app", "privateKey": "alipay-private-key"},
		SupportedTypes: payment.TypeAlipay,
		PaymentMode:    "popup",
	}
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	_, err = (&PaymentService{entClient: client, configService: configService}).revalidateProviderSelectionInTx(
		ctx,
		tx,
		CreateOrderRequest{OrderType: payment.OrderTypeBalance, PaymentType: payment.TypeAlipay},
		staleSelection,
	)
	require.Equal(t, "PAYMENT_PROVIDER_BINDING_CHANGED", infraerrors.Reason(err))
}

func TestAcquirePaymentFulfillmentLeaseRejectsUnpaidFailedOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().SetEmail("unpaid-reset@example.test").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName("unpaid-reset").
		SetAmount(40).
		SetPayAmount(40).
		SetFeeRate(0).
		SetRechargeCode("UNPAID-RESET").
		SetOutTradeNo("sub2_unpaid_reset").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeResetCard).
		SetStatus(OrderStatusFailed).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.test").
		Save(ctx)
	require.NoError(t, err)

	lease, err := (&PaymentService{entClient: client}).acquirePaymentFulfillmentLease(ctx, order)
	require.Nil(t, lease)
	require.Equal(t, "CONFLICT", infraerrors.Reason(err))
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusFailed, reloaded.Status)
	require.Nil(t, reloaded.PaidAt)
}

func TestLateResetCardPaymentCanRecoverLegacyUnpaidFailedOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().
		SetEmail("late-reset-payment@example.test").
		SetPasswordHash("hash").
		Save(ctx)
	require.NoError(t, err)
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName("late-reset-payment").
		SetAmount(40).
		SetPayAmount(40).
		SetFeeRate(0).
		SetRechargeCode("LATE-RESET").
		SetOutTradeNo("sub2_late_reset").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeResetCard).
		SetStatus(OrderStatusFailed).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.test").
		Save(ctx)
	require.NoError(t, err)

	err = (&PaymentService{entClient: client}).toPaid(ctx, order, "provider-late-reset", 40, payment.TypeAlipay)
	require.Error(t, err, "the intentionally incomplete fixture should fail entitlement fulfillment")
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, reloaded.PaidAt, "trusted late payment must not be discarded because an older binary marked create FAILED")
	require.Equal(t, "provider-late-reset", reloaded.PaymentTradeNo)
}

func TestTrustedResetCardPaymentRecoversFailedRowAfterStalePendingRead(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().
		SetEmail("stale-reset-payment@example.test").
		SetPasswordHash("hash").
		Save(ctx)
	require.NoError(t, err)
	stalePending, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName("stale-reset-payment").
		SetAmount(40).
		SetPayAmount(40).
		SetFeeRate(0).
		SetRechargeCode("STALE-RESET").
		SetOutTradeNo("sub2_stale_reset").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeResetCard).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.test").
		Save(ctx)
	require.NoError(t, err)

	// Model the provider-create finalizer winning after confirmPayment loaded
	// the row but before toPaid executes its conditional update.
	_, err = client.PaymentOrder.UpdateOneID(stalePending.ID).
		SetStatus(OrderStatusFailed).
		SetFailedAt(time.Now()).
		SetFailedReason("provider create closed concurrently").
		Save(ctx)
	require.NoError(t, err)

	err = (&PaymentService{entClient: client}).toPaid(ctx, stalePending, "provider-stale-reset", 40, payment.TypeAlipay)
	require.Error(t, err, "the intentionally incomplete fixture should fail entitlement fulfillment")
	reloaded, err := client.PaymentOrder.Get(ctx, stalePending.ID)
	require.NoError(t, err)
	require.NotNil(t, reloaded.PaidAt, "the trusted callback must recover the current unpaid FAILED row")
	require.Equal(t, OrderStatusFailed, reloaded.Status, "fulfillment failure happens only after the payment fact is durable")
	require.Equal(t, "provider-stale-reset", reloaded.PaymentTradeNo)
}

func TestCheckDailyLimitCountsPaidFulfillmentFailure(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().SetEmail("paid-failed-limit@example.test").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName("paid-failed-limit").
		SetAmount(90).
		SetPayAmount(90).
		SetFeeRate(0).
		SetRechargeCode("PAID-FAILED-LIMIT").
		SetOutTradeNo("sub2_paid_failed_limit").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("provider-paid-failed-limit").
		SetOrderType(payment.OrderTypeResetCard).
		SetStatus(OrderStatusFailed).
		SetPaidAt(time.Now()).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.test").
		Save(ctx)
	require.NoError(t, err)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	err = (&PaymentService{}).checkDailyLimit(ctx, tx, user.ID, 20, 100)
	require.Equal(t, "DAILY_LIMIT_EXCEEDED", infraerrors.Reason(err))
}

func TestResetCardOrderAlwaysRequiresManualRefund(t *testing.T) {
	manual, err := paymentOrderRequiresManualRefund(&dbent.PaymentOrder{
		OrderType: payment.OrderTypeResetCard,
		ProductSnapshot: map[string]any{
			"kind": "reset_card",
		},
	})
	require.NoError(t, err)
	require.True(t, manual)

	manual, err = paymentOrderRequiresManualRefund(&dbent.PaymentOrder{
		OrderType: payment.OrderTypeBalance,
		ProductSnapshot: map[string]any{
			"kind": "reset_card",
		},
	})
	require.NoError(t, err)
	require.True(t, manual, "snapshot identity must fence legacy or malformed order_type values")
}

func newResetCardDispatchTestOrder(t *testing.T, withLedger bool) (*PaymentService, *dbent.PaymentOrder, CreateOrderRequest) {
	t.Helper()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().
		SetEmail("reset-dispatch@example.test").
		SetPasswordHash("hash").
		Save(ctx)
	require.NoError(t, err)

	keyHash := HashIdempotencyKey("reset-card-dispatch-test-key")
	req := CreateOrderRequest{
		UserID:             user.ID,
		Amount:             40,
		PaymentType:        payment.TypeAlipay,
		OrderType:          payment.OrderTypeResetCard,
		PlanID:             7,
		SubscriptionID:     42,
		IdempotencyKeyHash: keyHash,
	}
	providerSnapshot := map[string]any{
		"schema_version": 2,
		"provider_key":   payment.TypeAlipay,
		"currency":       payment.DefaultPaymentCurrency,
		"payment_mode":   "popup",
	}
	if withLedger {
		providerSnapshot = withInitialResetCardDispatch(providerSnapshot)
	}
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName("reset-dispatch").
		SetAmount(req.Amount).
		SetPayAmount(req.Amount).
		SetFeeRate(0).
		SetRechargeCode("RESET-DISPATCH").
		SetOutTradeNo(resetCardOrderOutTradeNo(user.ID, keyHash)).
		SetPaymentType(req.PaymentType).
		SetPaymentTradeNo("").
		SetOrderType(req.OrderType).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.test").
		SetProviderSnapshot(providerSnapshot).
		SetProductSnapshot(map[string]any{
			"kind":                   "reset_card",
			"subscription_id":        req.SubscriptionID,
			"plan_id":                req.PlanID,
			"price":                  req.Amount,
			"idempotency_key_sha256": keyHash,
		}).
		Save(ctx)
	require.NoError(t, err)
	return &PaymentService{entClient: client}, order, req
}

func bindResetCardDispatchTestProvider(
	t *testing.T,
	ctx context.Context,
	svc *PaymentService,
	order *dbent.PaymentOrder,
	providerKey string,
	snapshotFields map[string]any,
) *dbent.PaymentOrder {
	t.Helper()
	instance, err := svc.entClient.PaymentProviderInstance.Create().
		SetProviderKey(providerKey).
		SetName("Reset card replay provider " + providerKey).
		SetConfig(`{}`).
		SetSupportedTypes(providerKey).
		SetPaymentMode("popup").
		SetEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	snapshot := clonePaymentOrderSnapshot(order.ProviderSnapshot)
	if snapshot == nil {
		snapshot = make(map[string]any)
	}
	snapshot["schema_version"] = 2
	snapshot["provider_instance_id"] = strconv.FormatInt(instance.ID, 10)
	snapshot["provider_key"] = providerKey
	snapshot["currency"] = payment.DefaultPaymentCurrency
	snapshot["payment_mode"] = "popup"
	for key, value := range snapshotFields {
		snapshot[key] = value
	}

	updated, err := svc.entClient.PaymentOrder.UpdateOneID(order.ID).
		SetPaymentType(providerKey).
		SetProviderInstanceID(strconv.FormatInt(instance.ID, 10)).
		SetProviderKey(providerKey).
		SetProviderSnapshot(snapshot).
		Save(ctx)
	require.NoError(t, err)
	return updated
}

func configureResetCardDispatchTestProvider(t *testing.T, svc *PaymentService, providerStub payment.Provider, config map[string]string) {
	t.Helper()
	replaceResetCardPaymentProviderFactoryForTest(t, providerStub)
	svc.loadBalancer = resetCardDispatchLoadBalancer{config: config}
	svc.configService = &PaymentConfigService{settingRepo: &paymentConfigSettingRepoStub{values: map[string]string{
		SettingPaymentEnabled: "true",
	}}}
}

func TestResetCardDispatchLeaseSerializesProviderCreate(t *testing.T) {
	ctx := context.Background()
	svc, order, req := newResetCardDispatchTestOrder(t, true)

	claimed, firstLease, dispatch, err := svc.claimResetCardDispatch(ctx, order, req)
	require.NoError(t, err)
	require.True(t, dispatch)
	require.NotNil(t, firstLease)
	require.False(t, firstLease.previouslyUncertain)

	_, _, _, err = svc.claimResetCardDispatch(ctx, order, req)
	require.Equal(t, "RESET_CARD_ORDER_IN_PROGRESS", infraerrors.Reason(err))

	released, err := svc.releaseResetCardDispatch(ctx, claimed, firstLease, false)
	require.NoError(t, err)
	require.True(t, released)
	reloaded, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	_, secondLease, dispatch, err := svc.claimResetCardDispatch(ctx, reloaded, req)
	require.NoError(t, err)
	require.True(t, dispatch)
	require.Equal(t, int64(2), secondLease.generation)
	require.False(t, secondLease.previouslyUncertain)
}

func TestResetCardDispatchTreatsLegacyOrderAsUncertain(t *testing.T) {
	svc, order, req := newResetCardDispatchTestOrder(t, false)
	_, lease, dispatch, err := svc.claimResetCardDispatch(context.Background(), order, req)
	require.NoError(t, err)
	require.True(t, dispatch)
	require.True(t, lease.previouslyUncertain)
}

func TestReopenLegacyResetCardFailureRequiresProviderReconciliation(t *testing.T) {
	ctx := context.Background()
	svc, order, _ := newResetCardDispatchTestOrder(t, false)
	order, err := svc.entClient.PaymentOrder.UpdateOneID(order.ID).
		SetStatus(OrderStatusFailed).
		SetFailedAt(time.Now()).
		SetFailedReason("legacy create error").
		Save(ctx)
	require.NoError(t, err)

	reopened, err := svc.reopenUnconfirmedResetCardOrder(ctx, order)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, reopened.Status)
	require.Nil(t, reopened.PaidAt)
	dispatch, present, err := resetCardDispatchFromOrder(reopened)
	require.NoError(t, err)
	require.True(t, present)
	require.True(t, dispatch.NeedsReconcile)
}

func TestResetCardDispatchStaleLeaseCannotFinalizeNewClaim(t *testing.T) {
	ctx := context.Background()
	svc, order, req := newResetCardDispatchTestOrder(t, true)
	firstOrder, firstLease, _, err := svc.claimResetCardDispatch(ctx, order, req)
	require.NoError(t, err)

	current, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	currentDispatch, _, err := resetCardDispatchFromOrder(current)
	require.NoError(t, err)
	expired := resetCardDispatchTimestamp(time.Now().Add(-time.Second))
	currentDispatch.LeaseExpiresAt = &expired
	snapshot := clonePaymentOrderSnapshot(current.ProviderSnapshot)
	snapshot[resetCardDispatchSnapshotKey] = currentDispatch
	_, err = svc.entClient.PaymentOrder.UpdateOneID(order.ID).
		SetProviderSnapshot(snapshot).
		SetUpdatedAt(resetCardDispatchNextVersion(current.UpdatedAt, time.Now())).
		Save(ctx)
	require.NoError(t, err)

	current, err = svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	_, secondLease, dispatch, err := svc.claimResetCardDispatch(ctx, current, req)
	require.NoError(t, err)
	require.True(t, dispatch)
	require.Equal(t, firstLease.generation+1, secondLease.generation)

	released, err := svc.releaseResetCardDispatch(ctx, firstOrder, firstLease, false)
	require.NoError(t, err)
	require.False(t, released)
	latest, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	latestDispatch, _, err := resetCardDispatchFromOrder(latest)
	require.NoError(t, err)
	require.Equal(t, secondLease.token, latestDispatch.ClaimToken)
}

func TestResetCardProviderSuccessUsesDispatchVersionFence(t *testing.T) {
	ctx := context.Background()
	svc, order, req := newResetCardDispatchTestOrder(t, true)
	claimed, lease, _, err := svc.claimResetCardDispatch(ctx, order, req)
	require.NoError(t, err)
	selection := &payment.InstanceSelection{ProviderKey: payment.TypeAlipay, PaymentMode: "popup"}
	first, err := svc.finishResetCardProviderSuccess(
		ctx,
		claimed,
		lease,
		req,
		selection,
		payment.CreatePaymentRequest{},
		&payment.CreatePaymentResponse{
			TradeNo:   "provider-reset-1",
			PayURL:    "https://pay.example.test/reset-1",
			Currency:  payment.DefaultPaymentCurrency,
			ExpiresAt: order.ExpiresAt.Add(time.Hour),
		},
		"resume-token",
	)
	require.NoError(t, err)
	require.Equal(t, "https://pay.example.test/reset-1", first.PayURL)
	require.Equal(t, "resume-token", first.ResumeToken)

	second, err := svc.finishResetCardProviderSuccess(
		ctx,
		claimed,
		lease,
		req,
		selection,
		payment.CreatePaymentRequest{},
		&payment.CreatePaymentResponse{
			TradeNo: "provider-reset-2",
			PayURL:  "https://pay.example.test/reset-2",
		},
		"",
	)
	require.NoError(t, err)
	require.Equal(t, first.PayURL, second.PayURL)
	reloaded, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, "provider-reset-1", reloaded.PaymentTradeNo)
	require.WithinDuration(t, order.ExpiresAt, reloaded.ExpiresAt, time.Microsecond, "provider response must not extend the local reset-card checkout")
}

func TestResetCardProviderSuccessPersistsWechatJSAPIForIdempotentReplay(t *testing.T) {
	ctx := context.Background()
	svc, order, req := newResetCardDispatchTestOrder(t, true)
	req.PaymentType = payment.TypeWxpay
	providerSnapshot := withInitialResetCardDispatch(map[string]any{
		"schema_version": 2,
		"provider_key":   payment.TypeWxpay,
		"currency":       payment.DefaultPaymentCurrency,
		"payment_mode":   "popup",
	})
	var err error
	order, err = svc.entClient.PaymentOrder.UpdateOneID(order.ID).
		SetPaymentType(payment.TypeWxpay).
		SetProviderSnapshot(providerSnapshot).
		Save(ctx)
	require.NoError(t, err)

	claimed, lease, _, err := svc.claimResetCardDispatch(ctx, order, req)
	require.NoError(t, err)
	payload := &payment.WechatJSAPIPayload{
		AppID:     "wx-reset-app",
		TimeStamp: "1789276800",
		NonceStr:  "reset-card-nonce",
		Package:   "prepay_id=wx-reset-card",
		SignType:  "RSA",
		PaySign:   "signed-reset-card-payload",
	}
	response, err := svc.finishResetCardProviderSuccess(
		ctx,
		claimed,
		lease,
		req,
		&payment.InstanceSelection{ProviderKey: payment.TypeWxpay, PaymentMode: "popup"},
		payment.CreatePaymentRequest{},
		&payment.CreatePaymentResponse{
			TradeNo:    claimed.OutTradeNo,
			Currency:   payment.DefaultPaymentCurrency,
			ResultType: payment.CreatePaymentResultJSAPIReady,
			JSAPI:      payload,
		},
		"",
	)
	require.NoError(t, err)
	require.Equal(t, payment.CreatePaymentResultJSAPIReady, response.ResultType)
	require.Equal(t, payload, response.JSAPI)

	reloaded, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	replayed := buildResetCardOrderResponse(reloaded)
	require.Equal(t, payment.CreatePaymentResultJSAPIReady, replayed.ResultType)
	require.Equal(t, payload, replayed.JSAPI)
	require.Equal(t, payload, replayed.JSAPIPayload)
	require.True(t, resetCardOrderHasReusableResponse(reloaded))
}

func TestResetCardGrantExpiryRejectsSnapshotExpiredBeforeFulfillment(t *testing.T) {
	createdAt := time.Now().Add(-48 * time.Hour).UTC()
	expected := createdAt.Add(24 * time.Hour)
	order := &dbent.PaymentOrder{
		CreatedAt: createdAt,
		ProductSnapshot: map[string]any{
			"subscription_expires_at": expected.Format(time.RFC3339Nano),
		},
	}
	_, err := resetCardPaymentOrderGrantExpiry(order, time.Now())
	require.Error(t, err, "a paid order whose promised card already expired must require manual handling")
}

func TestResetCardGrantExpiryKeepsFutureCheckoutSnapshot(t *testing.T) {
	createdAt := time.Now().Add(-time.Hour).UTC()
	expected := createdAt.Add(24 * time.Hour)
	order := &dbent.PaymentOrder{
		CreatedAt: createdAt,
		ProductSnapshot: map[string]any{
			"subscription_expires_at": expected.Format(time.RFC3339Nano),
		},
	}
	expiresAt, err := resetCardPaymentOrderGrantExpiry(order, time.Now())
	require.NoError(t, err)
	require.Equal(t, expected, expiresAt)
}

func TestPaymentOrderExpiresInSecondsUsesRemainingDeadline(t *testing.T) {
	now := time.Date(2026, time.September, 13, 12, 0, 0, 500_000_000, time.UTC)
	require.Equal(t, 300, paymentOrderExpiresInSeconds(now.Add(300*time.Second+900*time.Millisecond), now))
	require.Zero(t, paymentOrderExpiresInSeconds(now, now))
}

func TestResetCardUnifiedDefiniteRejectionReleasesOnlyFreshDispatch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		present     bool
		providerErr error
		released    bool
	}{
		{"fresh central rejection", true, &unifiedpay.APIError{StatusCode: 400, Code: "invalid_request"}, true},
		{"fresh local validation", true, unifiedpay.ErrInvalidRequest, true},
		{"prior unknown remains fenced", false, &unifiedpay.APIError{StatusCode: 400, Code: "invalid_request"}, false},
		{"remote idempotency conflict remains fenced", true, &unifiedpay.APIError{StatusCode: 409, Code: "idempotency_conflict"}, false},
		{"retryable invalid request remains fenced", true, &unifiedpay.APIError{StatusCode: 400, Code: "invalid_request", Retryable: true}, false},
		{"server error remains fenced", true, &unifiedpay.APIError{StatusCode: 503, Code: "service_unavailable", Retryable: true}, false},
		{"transport error remains fenced", true, errors.New("transport unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc, order, req := newResetCardDispatchTestOrder(t, tc.present)
			claimed, lease, dispatch, err := svc.claimResetCardDispatch(ctx, order, req)
			require.NoError(t, err)
			require.True(t, dispatch)
			_, err = svc.handleResetCardProviderCreateError(ctx, claimed, lease, nil, req, &payment.InstanceSelection{ProviderKey: payment.TypeUnifiedPay}, tc.providerErr)
			if tc.released {
				require.Equal(t, "RESET_CARD_PAYMENT_REJECTED", infraerrors.Reason(err))
			} else {
				require.Equal(t, "RESET_CARD_PAYMENT_CREATE_UNCONFIRMED", infraerrors.Reason(err))
			}
			current, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			_, nextLease, nextDispatch, err := svc.claimResetCardDispatch(ctx, current, req)
			if tc.released {
				require.NoError(t, err)
				require.True(t, nextDispatch)
				require.False(t, nextLease.previouslyUncertain)
				require.Equal(t, int64(2), nextLease.generation)
			} else {
				require.Equal(t, "RESET_CARD_ORDER_IN_PROGRESS", infraerrors.Reason(err))
			}
			require.Equal(t, 1, svc.entClient.PaymentOrder.Query().CountX(ctx))
		})
	}
}
