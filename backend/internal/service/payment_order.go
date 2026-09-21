package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/paymentinvoicerequest"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionplan"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/shopspring/decimal"
)

// --- Order Creation ---

func (s *PaymentService) CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error) {
	if req.OrderType == "" {
		req.OrderType = payment.OrderTypeBalance
	}
	if normalized := NormalizeVisibleMethod(req.PaymentType); normalized != "" {
		req.PaymentType = normalized
	}
	switch req.OrderType {
	case payment.OrderTypeBalance, payment.OrderTypeSubscription, payment.OrderTypeResetCard:
	default:
		return nil, infraerrors.BadRequest("INVALID_ORDER_TYPE", "unsupported payment order type")
	}
	if err := normalizeResetCardPurchaseOptions(&req); err != nil {
		return nil, err
	}
	if err := normalizePaymentDiscountRequest(&req); err != nil {
		return nil, err
	}
	if req.CouponCode != "" {
		if err := checkPaymentDiscountRate(ctx, s.entClient, req.UserID); err != nil {
			return nil, err
		}
		if replay, found, err := s.replayPaymentDiscount(ctx, req); err != nil || found {
			return replay, err
		}
	}
	if req.OrderType == payment.OrderTypeResetCard {
		if !isValidProviderAmount(req.Amount) {
			return nil, infraerrors.BadRequest("INVALID_AMOUNT", "amount must be a positive finite number")
		}
		if err := normalizeResetCardOrderIdempotency(&req); err != nil {
			return nil, err
		}
		if existing, found, replayErr := s.findResetCardOrderRecord(ctx, req); replayErr != nil {
			return nil, replayErr
		} else if found {
			return s.replayResetCardOrderRecord(ctx, existing, req)
		}
		// The reset-card quote is the server-side source of truth. The client
		// only repeats the quoted amount so stale prices can be rejected below.
		if s.subscriptionSvc == nil {
			return nil, infraerrors.ServiceUnavailable("RESET_CARD_UNAVAILABLE", "reset card purchase is unavailable")
		}
		quote, quoteErr := s.subscriptionSvc.GetResetCardQuote(ctx, req.UserID, req.SubscriptionID)
		if quoteErr != nil {
			return nil, quoteErr
		}
		if req.PlanID == 0 {
			req.PlanID = quote.PlanID
		}
		total, amountErr := validateResetCardRequestAmount(req.Amount, quote.Price, req.ResetCardQuantity)
		if amountErr != nil {
			return nil, amountErr
		}
		if err := validateResetCardTierQuoteRevision(req.ResetCardTierRevision, quote.ResetCardTier); err != nil {
			return nil, err
		}
		req.Amount = total
	}
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get payment config: %w", err)
	}
	if !cfg.Enabled {
		return nil, infraerrors.Forbidden("PAYMENT_DISABLED", "payment system is disabled")
	}
	// The signed WeChat resume token is applied by the handler before this
	// boundary. Authorize the resulting trusted order type here so a client
	// cannot disguise a subscription resume as a balance query parameter, and
	// so direct API calls obey the same site billing mode as the UI.
	if req.OrderType == payment.OrderTypeSubscription && !cfg.SubscriptionEnabled {
		return nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "subscription purchasing is disabled")
	}
	return s.createOrderWithConfig(ctx, req, cfg, nil)
}

// createOrderWithConfig is the shared, private creation core. The regular
// customer path supplies nil options and retains its existing configuration and
// provider-selection behavior. Narrow administrative flows can supply an
// immutable, already-validated configuration copy and a pinned provider
// selection without changing persisted payment settings.
func (s *PaymentService) createOrderWithConfig(ctx context.Context, req CreateOrderRequest, cfg *PaymentConfig, opts *createOrderOptions) (*CreateOrderResponse, error) {
	if cfg == nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_CONFIG_UNAVAILABLE", "payment configuration is unavailable")
	}
	var (
		plan *dbent.SubscriptionPlan
		err  error
	)
	if opts != nil && opts.ownerTest != nil {
		plan, err = validateOwnerTestOrderInput(req, cfg, opts.ownerTest)
	} else {
		plan, err = s.validateOrderInput(ctx, req, cfg)
	}
	if err != nil {
		return nil, err
	}
	if opts == nil || opts.ownerTest == nil {
		if err := s.checkCancelRateLimit(ctx, req.UserID, cfg); err != nil {
			return nil, err
		}
	}
	user, err := s.userRepo.GetByID(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if user.Status != payment.EntityStatusActive {
		return nil, infraerrors.Forbidden("USER_INACTIVE", "user account is disabled")
	}
	if opts != nil && opts.ownerTest != nil && !user.IsAdmin() {
		return nil, infraerrors.Forbidden("OWNER_TEST_ADMIN_REQUIRED", "an active administrator is required for an owner test order")
	}
	if (opts == nil || opts.ownerTest == nil) && s.notificationEmailService != nil {
		s.notificationEmailService.RememberRecipientLocale(ctx, req.UserID, user.Email, req.Locale)
	}
	orderAmount := req.Amount
	limitAmount := req.Amount
	if req.OrderType == payment.OrderTypeResetCard {
		orderAmount = req.Amount
		limitAmount = req.Amount
	} else if plan != nil {
		orderAmount = plan.Price
		limitAmount = plan.Price
	} else if req.OrderType == payment.OrderTypeBalance {
		orderAmount = calculateRechargeCreditedAmount(req.Amount, cfg.BalanceRechargeMultiplier, cfg.RechargeOptions)
	}
	feeRate := cfg.RechargeFeeRate
	methodCurrency := payment.DefaultPaymentCurrency
	if opts != nil && opts.ownerTest != nil {
		methodCurrency = payment.DefaultPaymentCurrency
	} else if s.configService != nil {
		methodCurrency, err = s.configService.ValidateMethodCurrencyConsistency(ctx, req.PaymentType)
		if err != nil {
			return nil, err
		}
	}
	payAmountStr := ""
	payAmount := float64(0)
	if opts != nil && opts.ownerTest != nil {
		payAmountStr = opts.ownerTest.amountDecimal
		payAmount = opts.ownerTest.amount
	} else {
		payAmountStr, payAmount, err = calculateCreateOrderPayAmountForOrderType(limitAmount, feeRate, methodCurrency, req.OrderType, cfg.SubscriptionUSDToCNYRate)
		if err != nil {
			return nil, err
		}
	}
	if req.CouponCode != "" {
		if opts != nil && opts.ownerTest != nil {
			return nil, infraerrors.BadRequest("COUPON_INVALID", "owner tests cannot use coupons")
		}
		original, parseErr := decimal.NewFromString(payAmountStr)
		if parseErr != nil {
			return nil, parseErr
		}
		quote, quoteErr := quotePaymentDiscount(ctx, s.entClient, req.UserID, req.CouponCode, original, methodCurrency, req.OrderType, req.PlanID, paymentDiscountRequestBinding(req), false)
		if quoteErr != nil {
			return nil, quoteErr
		}
		if quote.Revision != req.CouponRevision {
			return nil, infraerrors.Conflict("COUPON_QUOTE_CHANGED", "coupon quote changed; apply it again")
		}
		req.couponQuote = quote
		payAmountStr = quote.PayAmount
		final, _ := decimal.NewFromString(quote.PayAmount)
		payAmount = final.InexactFloat64()
	}
	var sel *payment.InstanceSelection
	if opts != nil && opts.ownerTest != nil {
		sel = opts.ownerTest.selection
	} else {
		sel, err = s.selectCreateOrderInstance(ctx, req, cfg, payAmount)
		if err != nil {
			return nil, err
		}
	}
	if err := s.validateSelectedCreateOrderInstance(ctx, req, sel); err != nil {
		return nil, err
	}
	if req.OrderType == payment.OrderTypeResetCard {
		if err := validateResetCardSelectedProvider(sel); err != nil {
			return nil, err
		}
	}
	selectedCurrency := payment.DefaultPaymentCurrency
	if sel != nil {
		selectedCurrency = paymentProviderConfigCurrency(sel.ProviderKey, sel.Config)
	}
	if req.OrderType == payment.OrderTypeResetCard && selectedCurrency != payment.DefaultPaymentCurrency {
		return nil, ErrResetCardCurrencyUnsupported
	}
	if selectedCurrency != methodCurrency {
		if req.CouponCode != "" {
			return nil, infraerrors.Conflict("COUPON_QUOTE_CHANGED", "payment currency changed; apply the coupon again")
		}
		if opts != nil && opts.ownerTest != nil {
			return nil, infraerrors.ServiceUnavailable("OWNER_TEST_UNIFIED_PAYMENT_UNAVAILABLE", "owner test payment must use the configured CNY unified gateway")
		}
		payAmountStr, payAmount, err = calculateCreateOrderPayAmountForOrderType(limitAmount, feeRate, selectedCurrency, req.OrderType, cfg.SubscriptionUSDToCNYRate)
		if err != nil {
			return nil, err
		}
	}
	if err := validateSelectedCreateOrderAmountCurrency(payAmountStr, sel); err != nil {
		return nil, err
	}
	if opts == nil || opts.ownerTest == nil {
		oauthResp, oauthErr := s.maybeBuildWeChatOAuthRequiredResponseForSelection(ctx, req, limitAmount, payAmount, feeRate, sel)
		if oauthErr != nil {
			return nil, oauthErr
		}
		if oauthResp != nil {
			return oauthResp, nil
		}
	}
	dbOpts := createOrderDatabaseOptionsFrom(opts)
	if req.OrderType == payment.OrderTypeResetCard {
		dbOpts = &createOrderDatabaseOptions{
			fixedOutTradeNo:     resetCardOrderOutTradeNo(req.UserID, req.IdempotencyKeyHash),
			resetCardIdempotent: true,
		}
	}
	order, created, err := s.createOrderInTxWithOptions(ctx, req, user, plan, cfg, orderAmount, limitAmount, feeRate, payAmount, sel, dbOpts)
	if err != nil {
		if req.CouponCode != "" {
			if replay, found, replayErr := s.replayPaymentDiscount(ctx, req); found {
				return replay, replayErr
			}
		}
		if opts != nil && opts.ownerTest != nil && isOwnerTestOrderInsertConflict(err) {
			return s.replayOwnerTestOrder(ctx, opts.ownerTest)
		}
		if req.OrderType == payment.OrderTypeResetCard && errors.Is(err, errResetCardOrderInsertConflict) {
			return s.replayResetCardOrder(ctx, req)
		}
		return nil, err
	}
	if opts != nil && opts.ownerTest != nil {
		if !created {
			return s.replayOwnerTestOrderRecord(ctx, order, opts.ownerTest)
		}
		s.writeAuditLog(ctx, order.ID, "OWNER_TEST_ORDER_CREATED", fmt.Sprintf("admin:%d", opts.ownerTest.input.AdminUserID), ownerTestAuditDetail(opts.ownerTest))
		return s.invokeOwnerTestProvider(ctx, order, opts.ownerTest)
	}
	if req.OrderType == payment.OrderTypeResetCard && !created {
		return s.replayResetCardOrderRecord(ctx, order, req)
	}
	if req.OrderType == payment.OrderTypeResetCard {
		resp, invokeErr := s.invokeResetCardProvider(ctx, order, req, cfg)
		if invokeErr == nil && req.CouponCode != "" {
			if saveErr := savePaymentDiscountResponse(ctx, s.entClient, order.ID, resp); saveErr != nil {
				return nil, saveErr
			}
		}
		return resp, invokeErr
	}
	resp, err := s.invokeProvider(ctx, order, req, cfg, limitAmount, payAmountStr, payAmount, plan, sel)
	if err != nil {
		if errors.Is(err, unifiedpay.ErrCreateStateUnconfirmed) {
			s.writeAuditLog(ctx, order.ID, "UNIFIED_PAYMENT_CREATE_UNCONFIRMED", payment.TypeUnifiedPay, map[string]any{
				"out_trade_no": order.OutTradeNo,
			})
			return nil, err
		}
		// A failed HTTP request is not proof the provider rejected a discounted order.
		// Retain its reservation until reconciliation confirms payment or closure.
		if req.CouponCode != "" {
			return nil, err
		}
		_, _ = s.entClient.PaymentOrder.Update().Where(paymentorder.IDEQ(order.ID), paymentorder.StatusEQ(OrderStatusPending), paymentorder.PaidAtIsNil()).
			SetStatus(OrderStatusFailed).
			Save(ctx)
		return nil, err
	}
	if req.CouponCode != "" {
		if err := savePaymentDiscountResponse(ctx, s.entClient, order.ID, resp); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func (s *PaymentService) validateOrderInput(ctx context.Context, req CreateOrderRequest, cfg *PaymentConfig) (*dbent.SubscriptionPlan, error) {
	useUnified, err := s.usesUnifiedPayment(ctx, req.PaymentType)
	if err != nil {
		return nil, err
	}
	if useUnified {
		timeoutMinutes := cfg.OrderTimeoutMin
		if timeoutMinutes <= 0 {
			timeoutMinutes = defaultOrderTimeoutMin
		}
		if timeoutMinutes < 5 || timeoutMinutes > 120 {
			return nil, infraerrors.ServiceUnavailable("UNIFIED_PAYMENT_INVALID_TIMEOUT", "unified payment order timeout must be between 5 and 120 minutes")
		}
	}
	if req.OrderType == payment.OrderTypeBalance && cfg.BalanceDisabled {
		return nil, infraerrors.Forbidden("BALANCE_PAYMENT_DISABLED", "balance recharge has been disabled")
	}
	if req.OrderType == payment.OrderTypeSubscription {
		return s.validateSubOrder(ctx, req)
	}
	if req.OrderType == payment.OrderTypeResetCard {
		return s.validateResetCardOrder(ctx, req)
	}
	if math.IsNaN(req.Amount) || math.IsInf(req.Amount, 0) || req.Amount <= 0 {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "amount must be a positive number")
	}
	if (cfg.MinAmount > 0 && req.Amount < cfg.MinAmount) || (cfg.MaxAmount > 0 && req.Amount > cfg.MaxAmount) {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "amount out of range").
			WithMetadata(map[string]string{"min": fmt.Sprintf("%.2f", cfg.MinAmount), "max": fmt.Sprintf("%.2f", cfg.MaxAmount)})
	}
	// Fail closed on a corrupted preset list. Falling through would drop the
	// fixed-tier requirement below and quietly reopen arbitrary top-up amounts
	// that carry no entitlements.
	if cfg.RechargeOptionsInvalid {
		return nil, infraerrors.BadRequest("RECHARGE_OPTIONS_UNAVAILABLE", "recharge tiers are misconfigured; balance top-up is temporarily unavailable")
	}
	if len(EnabledRechargeOptionsForCheckout(cfg.RechargeOptions)) > 0 {
		option, ok := rechargeOptionForAmount(cfg.RechargeOptions, req.Amount)
		if !ok {
			return nil, infraerrors.BadRequest("INVALID_RECHARGE_OPTION", "amount must match an enabled recharge option")
		}
		if err := validatePurchaseRulesForUser(ctx, s.paymentEligibilityClient(), req.UserID, option.PurchaseRules); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (s *PaymentService) validateResetCardOrder(ctx context.Context, req CreateOrderRequest) (*dbent.SubscriptionPlan, error) {
	if req.SubscriptionID <= 0 || s.subscriptionSvc == nil {
		return nil, infraerrors.BadRequest("INVALID_INPUT", "reset card order requires a subscription")
	}
	if req.PaymentType != payment.TypeAlipay && req.PaymentType != payment.TypeWxpay {
		return nil, infraerrors.BadRequest("RESET_CARD_PAYMENT_METHOD_UNSUPPORTED", "reset cards require Alipay or WeChat Pay")
	}
	quote, err := s.subscriptionSvc.GetResetCardQuote(ctx, req.UserID, req.SubscriptionID)
	if err != nil {
		return nil, err
	}
	if req.PlanID != 0 && req.PlanID != quote.PlanID {
		return nil, infraerrors.Conflict("RESET_CARD_QUOTE_CHANGED", "reset card quote changed; request a new quote")
	}
	if _, err := validateResetCardRequestAmount(req.Amount, quote.Price, req.ResetCardQuantity); err != nil {
		return nil, err
	}
	if err := validateResetCardTierQuoteRevision(req.ResetCardTierRevision, quote.ResetCardTier); err != nil {
		return nil, err
	}
	plan, err := s.configService.GetPlan(ctx, quote.PlanID)
	if err != nil {
		return nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "reset card source plan is no longer available")
	}
	return plan, nil
}

const (
	resetCardDefaultQuantity = 1
	resetCardMaximumQuantity = 99
)

// normalizeResetCardPurchaseOptions keeps the optional request fields scoped to
// reset-card purchases. A zero quantity is the wire-compatible omitted value
// for reset cards; ordinary orders retain zero because quantity is not part of
// their product contract.
func normalizeResetCardPurchaseOptions(req *CreateOrderRequest) error {
	if req == nil {
		return infraerrors.BadRequest("INVALID_INPUT", "payment order request is missing")
	}
	quantity, useOnPurchase, err := normalizeResetCardPurchaseTerms(req.OrderType, req.ResetCardQuantity, req.ResetCardUseOnPurchase)
	if err != nil {
		return err
	}
	req.ResetCardQuantity = quantity
	req.ResetCardUseOnPurchase = useOnPurchase
	return nil
}

func normalizeResetCardPurchaseTerms(orderType string, quantity int, useOnPurchase bool) (int, bool, error) {
	if orderType != payment.OrderTypeResetCard {
		if (quantity != 0 && quantity != resetCardDefaultQuantity) || useOnPurchase {
			return 0, false, infraerrors.BadRequest("RESET_CARD_OPTIONS_UNSUPPORTED", "reset card purchase options are only supported for reset card orders")
		}
		return 0, false, nil
	}
	if quantity == 0 {
		quantity = resetCardDefaultQuantity
	}
	if quantity < resetCardDefaultQuantity || quantity > resetCardMaximumQuantity {
		return 0, false, infraerrors.BadRequest("RESET_CARD_QUANTITY_INVALID", "reset_card_quantity must be between 1 and 99")
	}
	return quantity, useOnPurchase, nil
}

func resetCardAmountToMinorUnit(amount float64) (int64, error) {
	if !isValidProviderAmount(amount) {
		return 0, ErrResetCardPriceInvalid
	}
	minor, err := payment.AmountToMinorUnit(strconv.FormatFloat(amount, 'f', -1, 64), payment.DefaultPaymentCurrency)
	if err != nil || minor <= 0 {
		return 0, ErrResetCardPriceInvalid
	}
	return minor, nil
}

// resetCardTotalFromUnitPrice computes the order amount in CNY minor units so
// every quantity sees the same exact two-decimal contract as the quote.
func resetCardTotalFromUnitPrice(unitPrice float64, quantity int) (float64, int64, error) {
	if quantity < resetCardDefaultQuantity || quantity > resetCardMaximumQuantity {
		return 0, 0, ErrResetCardPriceInvalid
	}
	unit, err := normalizeResetCardExpectedPrice(unitPrice)
	if err != nil {
		return 0, 0, err
	}
	unitMinor, err := payment.AmountToMinorUnit(unit.StringFixed(resetCardPurchasePriceScale), payment.DefaultPaymentCurrency)
	if err != nil || unitMinor <= 0 || unitMinor > (int64(^uint64(0)>>1)/int64(quantity)) {
		return 0, 0, ErrResetCardPriceInvalid
	}
	totalMinor := unitMinor * int64(quantity)
	return payment.MinorUnitToAmount(totalMinor, payment.DefaultPaymentCurrency), totalMinor, nil
}

func validateResetCardRequestAmount(amount, unitPrice float64, quantity int) (float64, error) {
	requestMinor, err := resetCardAmountToMinorUnit(amount)
	if err != nil {
		return 0, infraerrors.BadRequest("INVALID_AMOUNT", "amount must be a positive CNY amount with at most two decimal places")
	}
	total, totalMinor, err := resetCardTotalFromUnitPrice(unitPrice, quantity)
	if err != nil {
		return 0, ErrResetCardQuoteChanged
	}
	if requestMinor != totalMinor {
		return 0, ErrResetCardQuoteChanged
	}
	return total, nil
}

func validateResetCardSelectedProvider(sel *payment.InstanceSelection) error {
	if sel == nil {
		return infraerrors.ServiceUnavailable("RESET_CARD_PAYMENT_PROVIDER_UNSUPPORTED", "reset card payment provider is unavailable")
	}
	providerKey := strings.TrimSpace(sel.ProviderKey)
	if providerKey == payment.TypeAlipay || providerKey == payment.TypeWxpay || providerKey == payment.TypeUnifiedPay {
		return nil
	}
	return infraerrors.ServiceUnavailable(
		"RESET_CARD_PAYMENT_PROVIDER_UNSUPPORTED",
		"reset cards require an official or unified Alipay or WeChat payment provider",
	)
}

func normalizeResetCardOrderIdempotency(req *CreateOrderRequest) error {
	if req == nil || req.OrderType != payment.OrderTypeResetCard {
		return nil
	}
	hash := strings.TrimSpace(req.IdempotencyKeyHash)
	if raw := strings.TrimSpace(req.IdempotencyKey); raw != "" {
		key, err := NormalizeIdempotencyKey(raw)
		if err != nil {
			return err
		}
		derived := HashIdempotencyKey(key)
		if hash != "" && !strings.EqualFold(hash, derived) {
			return ErrIdempotencyKeyConflict
		}
		hash = derived
	}
	normalizedHash, err := normalizeResetCardIdempotencyKeyHash(hash)
	if err != nil {
		return err
	}
	req.IdempotencyKey = ""
	req.IdempotencyKeyHash = normalizedHash
	return nil
}

func normalizeResetCardIdempotencyKeyHash(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", ErrIdempotencyKeyRequired
	}
	if len(raw) != sha256.Size*2 {
		return "", ErrIdempotencyKeyInvalid
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != sha256.Size {
		return "", ErrIdempotencyKeyInvalid
	}
	return raw, nil
}

func resetCardOrderOutTradeNo(userID int64, idempotencyKeyHash string) string {
	digest := sha256.Sum256([]byte("sub2-reset-card-v1\n" + strconv.FormatInt(userID, 10) + "\n" + idempotencyKeyHash))
	return "sub2_reset_" + hex.EncodeToString(digest[:24])
}

func (s *PaymentService) findResetCardOrderRecord(ctx context.Context, req CreateOrderRequest) (*dbent.PaymentOrder, bool, error) {
	if s == nil || s.entClient == nil {
		return nil, false, infraerrors.ServiceUnavailable("RESET_CARD_UNAVAILABLE", "reset card purchase is unavailable")
	}
	outTradeNo := resetCardOrderOutTradeNo(req.UserID, req.IdempotencyKeyHash)
	order, err := s.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(outTradeNo)).Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("lookup reset card payment replay: %w", err)
	}
	if err := validateResetCardOrderRecord(order, req, nil); err != nil {
		return nil, false, err
	}
	return order, true, nil
}

func (s *PaymentService) replayResetCardOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error) {
	if s == nil || s.entClient == nil {
		return nil, infraerrors.ServiceUnavailable("RESET_CARD_UNAVAILABLE", "reset card purchase is unavailable")
	}
	order, err := s.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(resetCardOrderOutTradeNo(req.UserID, req.IdempotencyKeyHash))).Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, infraerrors.ServiceUnavailable("RESET_CARD_ORDER_RETRY", "reset card order is still being recorded; retry with the same Idempotency-Key")
		}
		return nil, fmt.Errorf("reload reset card payment replay: %w", err)
	}
	return s.replayResetCardOrderRecord(ctx, order, req)
}

func (s *PaymentService) replayResetCardOrderRecord(ctx context.Context, order *dbent.PaymentOrder, req CreateOrderRequest) (*CreateOrderResponse, error) {
	if err := validateResetCardOrderRecord(order, req, nil); err != nil {
		return nil, err
	}
	if order.PaidAt == nil && (order.Status == OrderStatusFailed || order.Status == OrderStatusExpired) {
		_ = s.reconcilePaid(ctx, order)
		reloaded, err := s.entClient.PaymentOrder.Get(ctx, order.ID)
		if err != nil {
			return nil, fmt.Errorf("reload reset card payment replay: %w", err)
		}
		order = reloaded
		if order.Status == OrderStatusFailed && order.PaidAt == nil {
			order, err = s.reopenUnconfirmedResetCardOrder(ctx, order)
			if err != nil {
				return nil, err
			}
		}
	}
	if resetCardOrderHasReusableResponse(order) {
		response := buildResetCardOrderResponse(order)
		s.hydrateCheckoutFrameURL(ctx, order, response)
		return response, nil
	}
	if s.configService == nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_CONFIG_UNAVAILABLE", "payment configuration is unavailable")
	}
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get payment config for reset card replay: %w", err)
	}
	return s.invokeResetCardProvider(ctx, order, req, cfg)
}

func validateResetCardOrderRecord(order *dbent.PaymentOrder, req CreateOrderRequest, sel *payment.InstanceSelection) error {
	if order == nil || order.UserID != req.UserID || order.OrderType != payment.OrderTypeResetCard ||
		order.OutTradeNo != resetCardOrderOutTradeNo(req.UserID, req.IdempotencyKeyHash) ||
		NormalizeVisibleMethod(order.PaymentType) != NormalizeVisibleMethod(req.PaymentType) ||
		order.ProductSnapshot == nil {
		return ErrIdempotencyKeyConflict
	}
	if raw, ok := order.ProductSnapshot["payment_discount"].(map[string]any); ok {
		if code, _ := raw["code"].(string); code != strings.ToUpper(strings.TrimSpace(req.CouponCode)) {
			return ErrIdempotencyKeyConflict
		}
	} else if req.CouponCode != "" {
		return ErrIdempotencyKeyConflict
	}
	snapshot := order.ProductSnapshot
	kind, _ := snapshot["kind"].(string)
	storedHash, _ := snapshot["idempotency_key_sha256"].(string)
	subscriptionID, subscriptionOK := paymentSnapshotInt64(snapshot["subscription_id"])
	planID, planOK := paymentSnapshotInt64(snapshot["plan_id"])
	terms, termsErr := resetCardSnapshotPurchaseTerms(snapshot)
	requestQuantity, requestUseOnPurchase, requestTermsErr := normalizeResetCardPurchaseTerms(req.OrderType, req.ResetCardQuantity, req.ResetCardUseOnPurchase)
	requestMinor, requestMinorErr := resetCardAmountToMinorUnit(req.Amount)
	orderMinor, orderMinorErr := resetCardAmountToMinorUnit(order.Amount)
	if kind != "reset_card" || !strings.EqualFold(strings.TrimSpace(storedHash), req.IdempotencyKeyHash) ||
		!subscriptionOK || subscriptionID != req.SubscriptionID || !planOK ||
		(req.PlanID > 0 && planID != req.PlanID) || termsErr != nil || requestTermsErr != nil ||
		requestMinorErr != nil || orderMinorErr != nil || requestMinor != orderMinor ||
		requestMinor != terms.totalMinor || terms.quantity != requestQuantity ||
		terms.useOnPurchase != requestUseOnPurchase {
		return ErrIdempotencyKeyConflict
	}
	if sel != nil {
		providerSnapshot := psOrderProviderSnapshot(order)
		if providerSnapshot == nil ||
			!strings.EqualFold(providerSnapshot.ProviderKey, strings.TrimSpace(sel.ProviderKey)) ||
			!strings.EqualFold(providerSnapshot.ProviderInstanceID, strings.TrimSpace(sel.InstanceID)) {
			return infraerrors.Conflict("RESET_CARD_PAYMENT_BINDING_CHANGED", "the existing reset card order belongs to a different payment route")
		}
	}
	return nil
}

type resetCardPaymentSnapshotTerms struct {
	quantity      int
	unitPrice     float64
	totalAmount   float64
	totalMinor    int64
	useOnPurchase bool
	legacy        bool
}

// resetCardSnapshotPurchaseTerms reads the immutable terms from a payment
// snapshot. Pre-quantity rows are a deliberate compatibility case: they are
// restricted to one card with no automatic use. All newer rows prove their
// total from the frozen unit price in CNY minor units.
func resetCardSnapshotPurchaseTerms(snapshot map[string]any) (resetCardPaymentSnapshotTerms, error) {
	if snapshot == nil {
		return resetCardPaymentSnapshotTerms{}, errors.New("reset card order is missing purchase terms")
	}
	price, priceOK := paymentSnapshotFloat(snapshot["price"])
	if !priceOK {
		return resetCardPaymentSnapshotTerms{}, errors.New("reset card order has invalid total price")
	}
	quantity := resetCardDefaultQuantity
	quantityRaw, hasQuantity := snapshot["quantity"]
	if hasQuantity {
		parsed, ok := paymentSnapshotInt64(quantityRaw)
		if !ok || parsed < resetCardDefaultQuantity || parsed > resetCardMaximumQuantity {
			return resetCardPaymentSnapshotTerms{}, errors.New("reset card order has invalid quantity")
		}
		quantity = int(parsed)
	}
	_, hasUnitPrice := snapshot["unit_price"]
	_, hasUseOnPurchase := snapshot["use_on_purchase"]
	schemaVersion, hasSchemaVersion := paymentSnapshotInt64(snapshot["schema_version"])
	modern := (hasSchemaVersion && schemaVersion >= 2) || hasUnitPrice || hasUseOnPurchase
	if !modern {
		if quantity != resetCardDefaultQuantity {
			return resetCardPaymentSnapshotTerms{}, errors.New("legacy reset card order has invalid quantity")
		}
		totalMinor, err := resetCardAmountToMinorUnit(price)
		if err != nil {
			return resetCardPaymentSnapshotTerms{}, err
		}
		return resetCardPaymentSnapshotTerms{
			quantity:    resetCardDefaultQuantity,
			unitPrice:   payment.MinorUnitToAmount(totalMinor, payment.DefaultPaymentCurrency),
			totalAmount: payment.MinorUnitToAmount(totalMinor, payment.DefaultPaymentCurrency),
			totalMinor:  totalMinor,
			legacy:      true,
		}, nil
	}
	if !hasQuantity || !hasUnitPrice || !hasUseOnPurchase {
		return resetCardPaymentSnapshotTerms{}, errors.New("reset card order is missing frozen purchase terms")
	}
	unitPrice, unitPriceOK := paymentSnapshotFloat(snapshot["unit_price"])
	useOnPurchase, useOnPurchaseOK := paymentSnapshotBool(snapshot["use_on_purchase"])
	if !unitPriceOK || !useOnPurchaseOK {
		return resetCardPaymentSnapshotTerms{}, errors.New("reset card order has invalid frozen purchase terms")
	}
	unitMinor, err := resetCardAmountToMinorUnit(unitPrice)
	if err != nil {
		return resetCardPaymentSnapshotTerms{}, err
	}
	totalAmount, totalMinor, err := resetCardTotalFromUnitPrice(unitPrice, quantity)
	if err != nil {
		return resetCardPaymentSnapshotTerms{}, err
	}
	priceMinor, err := resetCardAmountToMinorUnit(price)
	if err != nil || priceMinor != totalMinor {
		return resetCardPaymentSnapshotTerms{}, errors.New("reset card order total does not match frozen unit price")
	}
	return resetCardPaymentSnapshotTerms{
		quantity:      quantity,
		unitPrice:     payment.MinorUnitToAmount(unitMinor, payment.DefaultPaymentCurrency),
		totalAmount:   totalAmount,
		totalMinor:    totalMinor,
		useOnPurchase: useOnPurchase,
	}, nil
}

func paymentSnapshotBool(value any) (bool, bool) {
	valueBool, ok := value.(bool)
	return valueBool, ok
}

func resetCardOrderHasReusableResponse(order *dbent.PaymentOrder) bool {
	if order == nil {
		return false
	}
	if order.Status != OrderStatusPending || !order.ExpiresAt.After(time.Now()) {
		return true
	}
	if strings.TrimSpace(psStringValue(order.PayURL)) != "" || strings.TrimSpace(psStringValue(order.QrCode)) != "" {
		return true
	}
	// A JSAPI launch has no URL/QR column. Its snapshot must fence another
	// provider create even if storage corruption prevents reconstructing it.
	_, present, _ := resetCardCheckoutFromOrder(order)
	return present
}

func buildResetCardOrderResponse(order *dbent.PaymentOrder) *CreateOrderResponse {
	if order == nil {
		return nil
	}
	paymentMode := ""
	if snapshot := psOrderProviderSnapshot(order); snapshot != nil {
		paymentMode = snapshot.PaymentMode
	}
	payURL, checkoutFrameURL, qrCode := "", "", ""
	resultType := payment.CreatePaymentResultOrderCreated
	var jsapi *payment.WechatJSAPIPayload
	if order.Status == OrderStatusPending && order.ExpiresAt.After(time.Now()) {
		payURL = psStringValue(order.PayURL)
		checkoutFrameURL = paymentOrderCheckoutFrameURLFromSnapshot(order)
		qrCode = psStringValue(order.QrCode)
		if checkout, _, err := resetCardCheckoutFromOrder(order); err == nil && checkout != nil {
			resultType = checkout.ResultType
			jsapi = checkout.JSAPI
		}
	}
	return &CreateOrderResponse{
		OrderID:          order.ID,
		Amount:           order.Amount,
		PayAmount:        order.PayAmount,
		FeeRate:          order.FeeRate,
		Status:           order.Status,
		ResultType:       resultType,
		PaymentType:      order.PaymentType,
		OutTradeNo:       order.OutTradeNo,
		PayURL:           payURL,
		CheckoutFrameURL: checkoutFrameURL,
		QRCode:           qrCode,
		JSAPI:            jsapi,
		JSAPIPayload:     jsapi,
		Currency:         PaymentOrderCurrency(order),
		ExpiresAt:        order.ExpiresAt,
		PaymentMode:      paymentMode,
		PaymentDiscount:  paymentOrderResponsePaymentDiscount(order),
	}
}

// revalidateResetCardOrderInTx locks the purchased subscription and the exact
// monthly plan before an externally payable order becomes durable. The
// provider call happens only after this transaction commits.
func (s *PaymentService) revalidateResetCardOrderInTx(ctx context.Context, tx *dbent.Tx, req CreateOrderRequest, expectedPlan *dbent.SubscriptionPlan) (*resetCardOrderSnapshotSource, error) {
	if tx == nil || expectedPlan == nil || req.SubscriptionID <= 0 {
		return nil, ErrResetCardPurchaseUnavailable
	}
	txCtx := dbent.NewTxContext(ctx, tx)
	subscription, found, err := loadResetCardPurchaseSubscription(txCtx, tx.Client(), req.UserID, req.SubscriptionID, true)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSubscriptionNotFound
	}
	now := time.Now()
	if s.subscriptionSvc != nil {
		now = s.subscriptionSvc.resetCardPurchaseNow()
	}
	if err := validateResetCardPurchaseSubscription(subscription, now); err != nil {
		return nil, err
	}

	// Prevent a concurrent insert/removal from changing the exact-one monthly
	// source-plan invariant while this order is being recorded.
	if tx.Client().Driver().Dialect() == dialect.Postgres {
		if _, err := tx.Client().ExecContext(txCtx, "LOCK TABLE subscription_plans IN SHARE MODE"); err != nil {
			return nil, fmt.Errorf("lock reset card plan source: %w", err)
		}
	}
	lockedSourcePlan, err := loadSingleMonthlyResetCardPlan(txCtx, tx.Client(), subscription.groupID, true)
	if err != nil {
		return nil, err
	}
	if err := validateResetCardPurchasePlanCurrency(lockedSourcePlan.currency); err != nil {
		return nil, err
	}
	lockedPrice, err := resetCardPurchasePriceForPlan(lockedSourcePlan)
	if err != nil {
		return nil, err
	}
	lockedUnitPrice := lockedPrice.InexactFloat64()
	lockedTotal, lockedTotalMinor, err := resetCardTotalFromUnitPrice(lockedUnitPrice, req.ResetCardQuantity)
	if err != nil {
		return nil, ErrResetCardQuoteChanged
	}
	requestMinor, err := resetCardAmountToMinorUnit(req.Amount)
	if err != nil || requestMinor != lockedTotalMinor {
		return nil, ErrResetCardQuoteChanged
	}
	if lockedSourcePlan.id != req.PlanID || lockedSourcePlan.id != expectedPlan.ID {
		return nil, ErrResetCardQuoteChanged
	}
	_, lockedEntitlements, err := normalizePlanEntitlements(lockedSourcePlan.entitlements)
	if err != nil {
		return nil, ErrPurchaseRulesUnavailable
	}
	if err := validateResetCardPurchaseRules(txCtx, tx.Client(), req.UserID, lockedEntitlements); err != nil {
		return nil, err
	}

	planQuery := tx.SubscriptionPlan.Query().Where(subscriptionplan.IDEQ(lockedSourcePlan.id))
	if tx.Client().Driver().Dialect() == dialect.Postgres {
		planQuery.ForUpdate()
	}
	lockedPlan, err := planQuery.Only(txCtx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, ErrResetCardPurchaseUnavailable
		}
		return nil, fmt.Errorf("lock reset card monthly plan: %w", err)
	}
	expectedNormalized, _, expectedErr := normalizePlanEntitlements(expectedPlan.Entitlements)
	lockedNormalized, _, lockedErr := normalizePlanEntitlements(lockedPlan.Entitlements)
	if expectedErr != nil || lockedErr != nil ||
		lockedPlan.ID != expectedPlan.ID || lockedPlan.GroupID != expectedPlan.GroupID ||
		lockedPlan.GroupID != subscription.groupID || !lockedPlan.ForSale ||
		!strings.EqualFold(strings.TrimSpace(lockedPlan.Currency), payment.DefaultPaymentCurrency) ||
		!decimal.NewFromFloat(lockedPlan.Price).Equal(lockedSourcePlan.price) ||
		!reflect.DeepEqual(expectedNormalized, lockedNormalized) {
		return nil, ErrResetCardQuoteChanged
	}
	tierPolicy, err := loadSubscriptionResetCardTierPolicy(txCtx, tx.Client(), subscription.groupID, true)
	if err != nil {
		return nil, err
	}
	if err := validateResetCardTierQuoteRevision(req.ResetCardTierRevision, tierPolicy); err != nil {
		return nil, err
	}
	sourcePlanID := lockedPlan.ID
	tierSnapshot := resetCardTierSnapshotFromPolicy(tierPolicy, &sourcePlanID)

	return &resetCardOrderSnapshotSource{
		plan:                lockedPlan,
		subscriptionID:      req.SubscriptionID,
		groupID:             subscription.groupID,
		monthlyPrice:        lockedSourcePlan.price.InexactFloat64(),
		unitPrice:           lockedUnitPrice,
		price:               lockedTotal,
		quantity:            req.ResetCardQuantity,
		useOnPurchase:       req.ResetCardUseOnPurchase,
		subscriptionExpires: subscription.expiresAt,
		idempotencyKeyHash:  req.IdempotencyKeyHash,
		tierSnapshot:        tierSnapshot,
	}, nil
}

// Recharge pricing modes reported to clients so they stop inferring the mode
// from an empty tier list. "fixed" means the server accepts only the tiers it
// returned; "custom" means any amount inside the configured min/max is valid.
const (
	RechargeModeFixed  = "fixed"
	RechargeModeCustom = "custom"
)

// RechargeModeForConfig mirrors the rule validateOrderInput enforces, so the
// checkout contract and order validation can never drift apart.
func RechargeModeForConfig(cfg *PaymentConfig) string {
	if cfg == nil {
		return RechargeModeCustom
	}
	if cfg.RechargeOptionsInvalid || len(EnabledRechargeOptionsForCheckout(cfg.RechargeOptions)) > 0 {
		return RechargeModeFixed
	}
	return RechargeModeCustom
}

func (s *PaymentService) validateSubOrder(ctx context.Context, req CreateOrderRequest) (*dbent.SubscriptionPlan, error) {
	if req.PlanID == 0 {
		return nil, infraerrors.BadRequest("INVALID_INPUT", "subscription order requires a plan")
	}
	plan, err := s.configService.GetPlan(ctx, req.PlanID)
	if err != nil || !plan.ForSale {
		return nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
	}
	group, err := s.groupRepo.GetByID(ctx, plan.GroupID)
	if err != nil || group.Status != payment.EntityStatusActive {
		return nil, infraerrors.NotFound("GROUP_NOT_FOUND", "subscription group is no longer available")
	}
	if !isNewSubscriptionCheckoutPlatform(group.Platform) {
		return nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan is not available for new subscription checkout")
	}
	if !group.IsSubscriptionType() {
		return nil, infraerrors.BadRequest("GROUP_TYPE_MISMATCH", "group is not a subscription type")
	}
	_, entitlements, err := normalizePlanEntitlements(plan.Entitlements)
	if err != nil {
		return nil, ErrPurchaseRulesUnavailable
	}
	if err := validatePurchaseRulesForUser(ctx, s.paymentEligibilityClient(), req.UserID, entitlements.PurchaseRules); err != nil {
		return nil, err
	}
	return plan, nil
}

func (s *PaymentService) paymentEligibilityClient() *dbent.Client {
	if s != nil && s.entClient != nil {
		return s.entClient
	}
	if s != nil && s.configService != nil {
		return s.configService.entClient
	}
	return nil
}

// revalidateSubscriptionOrderInTx closes the interval between the optimistic
// catalog read and durable order creation. Locking group then plan matches the
// reset-card flow's policy lock order. The initial group ID is intentionally
// used for the first lock: if the plan moved, the second locked read detects
// it and rejects the stale checkout instead of pairing a new plan group with
// an old price or snapshot.
func (s *PaymentService) revalidateSubscriptionOrderInTx(ctx context.Context, tx *dbent.Tx, req CreateOrderRequest, expected *dbent.SubscriptionPlan) (*dbent.SubscriptionPlan, *Group, error) {
	if tx == nil || expected == nil {
		return nil, nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
	}

	groupQuery := tx.Group.Query().Where(group.IDEQ(expected.GroupID), group.DeletedAtIsNil())
	if tx.Client().Driver().Dialect() == dialect.Postgres {
		groupQuery.ForUpdate()
	}
	currentGroup, err := groupQuery.Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil, infraerrors.NotFound("GROUP_NOT_FOUND", "subscription group is no longer available")
		}
		return nil, nil, fmt.Errorf("lock subscription checkout group: %w", err)
	}

	planQuery := tx.SubscriptionPlan.Query().Where(subscriptionplan.IDEQ(expected.ID))
	if tx.Client().Driver().Dialect() == dialect.Postgres {
		planQuery.ForUpdate()
	}
	currentPlan, err := planQuery.Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
		}
		return nil, nil, fmt.Errorf("lock subscription checkout plan: %w", err)
	}
	if currentPlan.GroupID != expected.GroupID || !currentPlan.ForSale {
		return nil, nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
	}
	if currentGroup.Status != payment.EntityStatusActive {
		return nil, nil, infraerrors.NotFound("GROUP_NOT_FOUND", "subscription group is no longer available")
	}
	if !isNewSubscriptionCheckoutPlatform(currentGroup.Platform) {
		return nil, nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan is not available for new subscription checkout")
	}
	if currentGroup.SubscriptionType != SubscriptionTypeSubscription {
		return nil, nil, infraerrors.BadRequest("GROUP_TYPE_MISMATCH", "group is not a subscription type")
	}
	if !subscriptionPlanCheckoutSourceEqual(expected, currentPlan) {
		return nil, nil, infraerrors.Conflict("PLAN_CHANGED", "plan changed; request current checkout information")
	}

	_, entitlements, err := normalizePlanEntitlements(currentPlan.Entitlements)
	if err != nil {
		return nil, nil, ErrPurchaseRulesUnavailable
	}
	if err := validatePurchaseRulesForUser(ctx, tx.Client(), req.UserID, entitlements.PurchaseRules); err != nil {
		return nil, nil, err
	}
	return currentPlan, paymentOrderSnapshotGroup(currentGroup), nil
}

// subscriptionPlanCheckoutSourceEqual covers every plan field frozen into an
// order snapshot or used to calculate the charged amount. A configuration
// edit therefore produces a fresh catalog/order attempt instead of silently
// charging against one version and recording another.
func subscriptionPlanCheckoutSourceEqual(expected, current *dbent.SubscriptionPlan) bool {
	if expected == nil || current == nil {
		return false
	}
	return expected.ID == current.ID &&
		expected.GroupID == current.GroupID &&
		expected.Name == current.Name &&
		expected.Description == current.Description &&
		expected.Price == current.Price &&
		sameOptionalCheckoutPrice(expected.OriginalPrice, current.OriginalPrice) &&
		expected.Currency == current.Currency &&
		expected.ValidityDays == current.ValidityDays &&
		expected.ValidityUnit == current.ValidityUnit &&
		expected.Features == current.Features &&
		subscriptionPlanSnapshotEntitlementsEqual(expected.Entitlements, current.Entitlements) &&
		expected.ProductName == current.ProductName &&
		expected.ForSale == current.ForSale &&
		expected.SortOrder == current.SortOrder
}

// Audience and threshold rules are admission policy, not purchased benefits.
// Their current values are deliberately revalidated below rather than treated
// as a stale-product conflict. Everything frozen in the product snapshot must
// still match before an order can be created.
func subscriptionPlanSnapshotEntitlementsEqual(left, right map[string]any) bool {
	return reflect.DeepEqual(paymentSnapshotEntitlements(left), paymentSnapshotEntitlements(right))
}

func sameOptionalCheckoutPrice(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func paymentOrderSnapshotGroup(current *dbent.Group) *Group {
	if current == nil {
		return nil
	}
	return &Group{
		ID:              current.ID,
		Name:            current.Name,
		DailyLimitUSD:   current.DailyLimitUsd,
		WeeklyLimitUSD:  current.WeeklyLimitUsd,
		MonthlyLimitUSD: current.MonthlyLimitUsd,
	}
}

// revalidateRechargeOrderInTx locks and rereads the persisted preset row for
// every ordinary balance checkout. The outer configuration may have been in
// custom mode when the request was validated, then switched to fixed mode
// before this transaction began. Compare modes first so that change cannot
// fall through as an unrestricted custom top-up; fixed tiers then revalidate
// their current product and purchase rules under the same lock.
func (s *PaymentService) revalidateRechargeOrderInTx(ctx context.Context, tx *dbent.Tx, req CreateOrderRequest, cfg *PaymentConfig) error {
	if tx == nil || cfg == nil {
		return ErrPurchaseRulesUnavailable
	}
	if cfg.RechargeOptionsInvalid {
		return ErrPurchaseRulesUnavailable
	}

	// An absent row is the legacy custom-amount default. Materialize that exact
	// default inside this transaction before locking it: PostgreSQL cannot lock a
	// missing row, while this INSERT ... ON CONFLICT DO NOTHING holds the unique
	// key against a concurrent first admin insert until the checkout commits.
	if err := tx.Setting.Create().
		SetKey(SettingRechargeOptions).
		SetValue("[]").
		OnConflictColumns(setting.FieldKey).
		DoNothing().
		Exec(ctx); err != nil && !isSQLNoRowsError(err) {
		return fmt.Errorf("ensure recharge options setting: %w", err)
	}

	settingQuery := tx.Setting.Query().Where(setting.KeyEQ(SettingRechargeOptions))
	if tx.Client().Driver().Dialect() == dialect.Postgres {
		settingQuery.ForUpdate()
	}
	stored, err := settingQuery.Only(ctx)
	if err != nil {
		return fmt.Errorf("lock recharge options: %w", err)
	}
	currentOptions, intact := normalizeRechargeOptions(stored.Value)
	if !intact {
		return ErrPurchaseRulesUnavailable
	}
	outerMode := RechargeModeForConfig(cfg)
	currentMode := RechargeModeForConfig(&PaymentConfig{RechargeOptions: currentOptions})
	if outerMode != currentMode {
		return infraerrors.Conflict("RECHARGE_OPTION_CHANGED", "recharge option changed; request current checkout information")
	}
	if currentMode != RechargeModeFixed {
		return nil
	}

	expected, expectedFixedTier := rechargeOptionForAmount(cfg.RechargeOptions, req.Amount)
	if !expectedFixedTier {
		return infraerrors.Conflict("RECHARGE_OPTION_CHANGED", "recharge option changed; request current checkout information")
	}
	current, found := rechargeOptionForAmount(currentOptions, req.Amount)
	if !found {
		return infraerrors.Conflict("RECHARGE_OPTION_CHANGED", "recharge option changed; request current checkout information")
	}
	if !rechargeOptionProductEqual(expected, current) {
		return infraerrors.Conflict("RECHARGE_OPTION_CHANGED", "recharge option changed; request current checkout information")
	}
	return validatePurchaseRulesForUser(ctx, tx.Client(), req.UserID, current.PurchaseRules)
}

func rechargeOptionProductEqual(expected, current RechargeOption) bool {
	return expected.Amount == current.Amount &&
		expected.OriginalPrice == current.OriginalPrice &&
		expected.Label == current.Label &&
		expected.Description == current.Description &&
		expected.BalanceBonus == current.BalanceBonus &&
		expected.EstimatedRateMultiplier == current.EstimatedRateMultiplier &&
		expected.EstimatedTokens == current.EstimatedTokens &&
		expected.Concurrency == current.Concurrency &&
		expected.Recommended == current.Recommended &&
		expected.SortOrder == current.SortOrder &&
		expected.Enabled == current.Enabled
}

// createOrderDatabaseOptions applies only to private creation paths. It keeps
// the normal customer order builder's data shape and transaction boundaries
// intact while allowing an already-persisted owner-test ledger to use a
// deterministic out_trade_no.
type createOrderDatabaseOptions struct {
	fixedOutTradeNo     string
	providerSnapshot    map[string]any
	lockOwnerTestUser   bool
	resetCardIdempotent bool
}

var errResetCardOrderInsertConflict = errors.New("reset card order insert conflict")

type resetCardOrderSnapshotSource struct {
	plan                *dbent.SubscriptionPlan
	subscriptionID      int64
	groupID             int64
	monthlyPrice        float64
	unitPrice           float64
	price               float64
	quantity            int
	useOnPurchase       bool
	subscriptionExpires time.Time
	idempotencyKeyHash  string
	tierSnapshot        *SubscriptionResetCardTierSnapshot
}

func (s *PaymentService) createOrderInTx(ctx context.Context, req CreateOrderRequest, user *User, plan *dbent.SubscriptionPlan, cfg *PaymentConfig, orderAmount, limitAmount, feeRate, payAmount float64, sel *payment.InstanceSelection) (*dbent.PaymentOrder, error) {
	order, _, err := s.createOrderInTxWithOptions(ctx, req, user, plan, cfg, orderAmount, limitAmount, feeRate, payAmount, sel, nil)
	return order, err
}

// resetCardOrderEffectiveDeadline preserves the configured payment timeout
// when it is already safe, but reset-card replays may need to wait out one
// dispatch lease before opening their upstream checkout. The effective local
// deadline therefore leaves the provider's minimum lifetime plus one minute
// for flooring and local execution after that lease.
func resetCardOrderEffectiveDeadline(now time.Time, timeoutMinutes int, subscriptionExpires time.Time) (time.Time, error) {
	if timeoutMinutes <= 0 {
		timeoutMinutes = defaultOrderTimeoutMin
	}
	minimumDeadline := now.Add(resetCardMinimumExternalCheckoutLifetime)
	if !subscriptionExpires.After(minimumDeadline) {
		return time.Time{}, ErrResetCardPurchaseUnavailable
	}
	effectiveDeadline := now.Add(time.Duration(timeoutMinutes) * time.Minute)
	if effectiveDeadline.Before(minimumDeadline) {
		effectiveDeadline = minimumDeadline
	}
	if subscriptionExpires.Before(effectiveDeadline) {
		effectiveDeadline = subscriptionExpires
	}
	return effectiveDeadline, nil
}

// revalidateProviderSelectionInTx serializes every new provider-backed order
// with provider-instance rotation. The load balancer selection is made before
// the order transaction, so its credential and route identity must be checked
// again under the same provider-row lock that configuration updates use.
func (s *PaymentService) revalidateProviderSelectionInTx(ctx context.Context, tx *dbent.Tx, req CreateOrderRequest, selected *payment.InstanceSelection) (*payment.InstanceSelection, error) {
	if selected == nil {
		if req.OrderType == payment.OrderTypeResetCard {
			return nil, paymentProviderSelectionChangedError(req, "the selected reset card payment route is no longer available")
		}
		// Narrow internal order-building helpers may intentionally omit a provider
		// selection. Public CreateOrder always supplies one before reaching here.
		return nil, nil
	}
	providerKey := strings.TrimSpace(selected.ProviderKey)
	if providerKey == payment.TypeUnifiedPay {
		// UnifiedPay is not represented by a payment_provider_instances row.
		return selected, nil
	}
	instanceID := strings.TrimSpace(selected.InstanceID)
	parsedInstanceID, err := strconv.ParseInt(instanceID, 10, 64)
	if err != nil || parsedInstanceID <= 0 || strconv.FormatInt(parsedInstanceID, 10) != instanceID {
		return nil, paymentProviderSelectionChangedError(req, "the selected payment route is no longer available")
	}
	locked, err := loadPaymentProviderInstanceForUpdate(ctx, tx, parsedInstanceID)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, paymentProviderSelectionChangedError(req, "the selected payment route is no longer available")
		}
		return nil, fmt.Errorf("lock payment provider instance: %w", err)
	}
	if s.configService == nil {
		return nil, paymentProviderSelectionUnavailableError(req)
	}
	lockedConfig, err := s.configService.decryptConfig(locked.Config)
	if err != nil {
		return nil, fmt.Errorf("decrypt locked payment provider config: %w", err)
	}
	if lockedConfig == nil {
		return nil, paymentProviderSelectionUnavailableError(req)
	}
	if !locked.Enabled ||
		!strings.EqualFold(strings.TrimSpace(locked.ProviderKey), providerKey) ||
		strconv.FormatInt(locked.ID, 10) != instanceID ||
		hasPendingOrderProtectedConfigChange(locked.ProviderKey, selected.Config, lockedConfig) ||
		strings.TrimSpace(locked.SupportedTypes) != strings.TrimSpace(selected.SupportedTypes) ||
		!strings.EqualFold(strings.TrimSpace(locked.PaymentMode), strings.TrimSpace(selected.PaymentMode)) ||
		!payment.InstanceSupportsType(locked.SupportedTypes, payment.PaymentType(req.PaymentType)) {
		return nil, paymentProviderSelectionChangedError(req, "the selected payment route changed; request a new checkout")
	}

	// Use the locked copy even after the comparison succeeds. This prevents a
	// stale load-balancer configuration from becoming the immutable snapshot or
	// the provider credentials for the newly persisted order.
	freshConfig := make(map[string]string, len(lockedConfig)+1)
	for key, value := range lockedConfig {
		freshConfig[key] = value
	}
	if locked.PaymentMode != "" {
		freshConfig["paymentMode"] = locked.PaymentMode
	} else {
		delete(freshConfig, "paymentMode")
	}
	return &payment.InstanceSelection{
		InstanceID:     strconv.FormatInt(locked.ID, 10),
		ProviderKey:    locked.ProviderKey,
		Config:         freshConfig,
		SupportedTypes: locked.SupportedTypes,
		PaymentMode:    locked.PaymentMode,
	}, nil
}

func paymentProviderSelectionChangedError(req CreateOrderRequest, message string) error {
	if req.OrderType == payment.OrderTypeResetCard {
		return infraerrors.Conflict("RESET_CARD_PAYMENT_BINDING_CHANGED", message)
	}
	return infraerrors.Conflict("PAYMENT_PROVIDER_BINDING_CHANGED", message)
}

func paymentProviderSelectionUnavailableError(req CreateOrderRequest) error {
	if req.OrderType == payment.OrderTypeResetCard {
		return infraerrors.ServiceUnavailable("RESET_CARD_PAYMENT_ROUTE_UNAVAILABLE", "the selected reset card payment route is unavailable")
	}
	return infraerrors.ServiceUnavailable("PAYMENT_PROVIDER_CONFIG_UNAVAILABLE", "payment provider configuration is unavailable")
}

// createOrderInTxWithOptions writes all locally authoritative order data,
// including an owner-test idempotency ledger, before any provider call. A
// deterministic owner-test key is checked while holding the actor row lock so
// replays bypass ordinary pending-order checks.
func (s *PaymentService) createOrderInTxWithOptions(ctx context.Context, req CreateOrderRequest, userRecord *User, plan *dbent.SubscriptionPlan, cfg *PaymentConfig, orderAmount, limitAmount, feeRate, payAmount float64, sel *payment.InstanceSelection, opts *createOrderDatabaseOptions) (*dbent.PaymentOrder, bool, error) {
	if req.OrderType == payment.OrderTypeResetCard && strings.TrimSpace(req.CouponCode) != "" {
		return nil, false, ErrPaymentDiscountInvalid
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	if opts != nil && opts.resetCardIdempotent {
		if opts.fixedOutTradeNo == "" {
			return nil, false, ErrIdempotencyKeyRequired
		}
		existing, lookupErr := tx.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(opts.fixedOutTradeNo)).Only(ctx)
		if lookupErr == nil {
			return existing, false, nil
		}
		if !dbent.IsNotFound(lookupErr) {
			return nil, false, fmt.Errorf("lookup reset card payment replay: %w", lookupErr)
		}
	}
	if opts != nil && opts.lockOwnerTestUser {
		lockedUserQuery := tx.User.Query().Where(user.IDEQ(req.UserID))
		if tx.Client().Driver().Dialect() == dialect.Postgres {
			lockedUserQuery.ForUpdate()
		}
		lockedUser, lockErr := lockedUserQuery.Only(ctx)
		if lockErr != nil {
			return nil, false, fmt.Errorf("lock owner test user: %w", lockErr)
		}
		if lockedUser.Status != StatusActive {
			return nil, false, infraerrors.Forbidden("USER_INACTIVE", "user account is disabled")
		}
		if lockedUser.Role != RoleAdmin {
			return nil, false, infraerrors.Forbidden("OWNER_TEST_ADMIN_REQUIRED", "an active administrator is required for an owner test order")
		}
		if opts.fixedOutTradeNo == "" {
			return nil, false, fmt.Errorf("owner test order missing deterministic out_trade_no")
		}
		existing, lookupErr := tx.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(opts.fixedOutTradeNo)).Only(ctx)
		if lookupErr == nil {
			return existing, false, nil
		}
		if !dbent.IsNotFound(lookupErr) {
			return nil, false, fmt.Errorf("lookup owner test order: %w", lookupErr)
		}
	}
	var (
		lockedSnapshotGroup      *Group
		resetCardSource          *resetCardOrderSnapshotSource
		subscriptionTierSnapshot *SubscriptionResetCardTierSnapshot
	)
	if opts == nil || !opts.lockOwnerTestUser {
		if plan != nil && req.OrderType == payment.OrderTypeSubscription {
			lockedPlan, lockedGroup, lockErr := s.revalidateSubscriptionOrderInTx(ctx, tx, req, plan)
			if lockErr != nil {
				return nil, false, lockErr
			}
			if orderAmount != lockedPlan.Price || limitAmount != lockedPlan.Price {
				return nil, false, infraerrors.Conflict("PLAN_CHANGED", "plan changed; request current checkout information")
			}
			// Keep the existing snapshot contract for narrow internal callers that
			// deliberately omit GroupRepository: policy still revalidates against
			// the locked Ent row, but those callers did not previously promise group
			// display evidence in their immutable product snapshots.
			if s.groupRepo != nil {
				lockedSnapshotGroup = lockedGroup
			}
		} else if plan != nil && req.OrderType == payment.OrderTypeResetCard {
			lockedSource, lockErr := s.revalidateResetCardOrderInTx(ctx, tx, req, plan)
			if lockErr != nil {
				return nil, false, lockErr
			}
			if orderAmount != lockedSource.price || limitAmount != lockedSource.price {
				return nil, false, ErrResetCardQuoteChanged
			}
			plan = lockedSource.plan
			resetCardSource = lockedSource
		} else if plan == nil && req.OrderType == payment.OrderTypeBalance {
			if lockErr := s.revalidateRechargeOrderInTx(ctx, tx, req, cfg); lockErr != nil {
				return nil, false, lockErr
			}
		}
	}
	if plan != nil && req.OrderType == payment.OrderTypeSubscription {
		sourcePlanID := plan.ID
		subscriptionTierSnapshot, err = resetCardTierSnapshotForGroup(txCtx, tx.Client(), plan.GroupID, &sourcePlanID, true)
		if err != nil {
			return nil, false, err
		}
	}
	if sel != nil || req.OrderType == payment.OrderTypeResetCard {
		lockedSelection, lockErr := s.revalidateProviderSelectionInTx(ctx, tx, req, sel)
		if lockErr != nil {
			return nil, false, lockErr
		}
		if sel != nil && lockedSelection != nil {
			// The caller invokes the provider after this transaction commits. Update
			// its request-local selection to the same locked copy persisted below.
			*sel = *lockedSelection
		}
		sel = lockedSelection
	}
	if err := s.checkPendingLimit(ctx, tx, req.UserID, cfg.MaxPendingOrders); err != nil {
		return nil, false, err
	}
	if err := s.checkDailyLimit(ctx, tx, req.UserID, limitAmount, cfg.DailyLimit); err != nil {
		return nil, false, err
	}
	tm := cfg.OrderTimeoutMin
	if tm <= 0 {
		tm = defaultOrderTimeoutMin
	}
	now := time.Now()
	exp := now.Add(time.Duration(tm) * time.Minute)
	if resetCardSource != nil {
		var deadlineErr error
		exp, deadlineErr = resetCardOrderEffectiveDeadline(now, tm, resetCardSource.subscriptionExpires)
		if deadlineErr != nil {
			return nil, false, deadlineErr
		}
	}
	outTradeNo := ""
	if opts != nil && opts.fixedOutTradeNo != "" {
		outTradeNo = opts.fixedOutTradeNo
	} else {
		outTradeNo, err = s.allocateOutTradeNo(ctx, tx)
		if err != nil {
			return nil, false, err
		}
	}
	providerSnapshot := buildPaymentOrderProviderSnapshot(sel, req)
	if req.OrderType == payment.OrderTypeResetCard {
		providerSnapshot = withInitialResetCardDispatch(providerSnapshot)
	}
	if opts != nil && opts.providerSnapshot != nil {
		providerSnapshot = clonePaymentOrderSnapshot(opts.providerSnapshot)
	}
	selectedInstanceID := ""
	selectedProviderKey := ""
	if sel != nil {
		selectedInstanceID = strings.TrimSpace(sel.InstanceID)
		selectedProviderKey = strings.TrimSpace(sel.ProviderKey)
	}
	b := tx.PaymentOrder.Create().
		SetUserID(req.UserID).
		SetUserEmail(userRecord.Email).
		SetUserName(userRecord.Username).
		SetNillableUserNotes(psNilIfEmpty(userRecord.Notes)).
		SetAmount(orderAmount).
		SetPayAmount(payAmount).
		SetFeeRate(feeRate).
		SetRechargeCode("").
		SetOutTradeNo(outTradeNo).
		SetPaymentType(req.PaymentType).
		SetPaymentTradeNo("").
		SetOrderType(req.OrderType).
		SetStatus(OrderStatusPending).
		SetExpiresAt(exp).
		SetClientIP(req.ClientIP).
		SetSrcHost(req.SrcHost)
	if req.SrcURL != "" {
		b.SetSrcURL(req.SrcURL)
	}
	if selectedInstanceID != "" {
		b.SetProviderInstanceID(selectedInstanceID)
	}
	if selectedProviderKey != "" {
		b.SetProviderKey(selectedProviderKey)
	}
	if providerSnapshot != nil {
		b.SetProviderSnapshot(providerSnapshot)
	}
	if plan != nil {
		subscriptionDays := psComputeValidityDays(plan.ValidityDays, plan.ValidityUnit)
		b.SetPlanID(plan.ID).SetSubscriptionGroupID(plan.GroupID)
		if req.OrderType == payment.OrderTypeSubscription {
			b.SetSubscriptionDays(subscriptionDays)
		}
		// The plan is already the immutable product selected for this order. A
		// small current-group read records the promised limits alongside it, so a
		// later group edit cannot rewrite what this purchase represented.
		snapshotGroup := lockedSnapshotGroup
		if snapshotGroup == nil && s.groupRepo != nil {
			if group, groupErr := s.groupRepo.GetByID(ctx, plan.GroupID); groupErr == nil {
				snapshotGroup = group
			}
		}
		if req.OrderType == payment.OrderTypeResetCard {
			if resetCardSource == nil {
				return nil, false, errors.New("reset card order snapshot source is missing")
			}
			b.SetProductSnapshot(buildPaymentResetCardProductSnapshot(resetCardSource, payAmount))
		} else {
			b.SetProductSnapshot(buildPaymentProductSnapshotWithGroupAndResetCardTier(plan, orderAmount, payAmount, subscriptionDays, snapshotGroup, subscriptionTierSnapshot))
		}
	} else if req.OrderType == payment.OrderTypeBalance {
		// Resolve balance entitlements only from the server-side configured
		// preset. The client-provided amount can select a preset, but can never
		// provide the concurrency target itself.
		b.SetProductSnapshot(buildPaymentBalanceProductSnapshot(req.Amount, orderAmount, payAmount, cfg.RechargeOptions))
	}
	if req.CouponCode != "" {
		if req.couponQuote == nil {
			return nil, false, infraerrors.BadRequest("COUPON_QUOTE_REQUIRED", "coupon quote required")
		}
		original, parseErr := decimal.NewFromString(req.couponQuote.OriginalAmount)
		if parseErr != nil {
			return nil, false, parseErr
		}
		locked, quoteErr := quotePaymentDiscount(txCtx, tx.Client(), req.UserID, req.CouponCode, original, req.couponQuote.Currency, req.OrderType, req.PlanID, paymentDiscountRequestBinding(req), true)
		if quoteErr != nil {
			return nil, false, quoteErr
		}
		if locked.Revision != req.CouponRevision {
			return nil, false, infraerrors.Conflict("COUPON_QUOTE_CHANGED", "coupon quote changed; apply it again")
		}
		req.couponQuote = locked
	}
	order, err := b.Save(ctx)
	if err != nil {
		if opts != nil && opts.fixedOutTradeNo != "" && dbent.IsConstraintError(err) {
			if opts.resetCardIdempotent {
				return nil, false, errResetCardOrderInsertConflict
			}
			return nil, false, errOwnerTestOrderInsertConflict
		}
		return nil, false, fmt.Errorf("create order: %w", err)
	}
	code := fmt.Sprintf("PAY-%d-%d", order.ID, time.Now().UnixNano()%100000)
	order, err = tx.PaymentOrder.UpdateOneID(order.ID).SetRechargeCode(code).Save(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("set recharge code: %w", err)
	}
	if req.CouponCode != "" {
		if err := applyPaymentDiscountSnapshot(order, req.couponQuote); err != nil {
			return nil, false, err
		}
		order, err = tx.PaymentOrder.UpdateOneID(order.ID).SetProductSnapshot(order.ProductSnapshot).Save(txCtx)
		if err != nil {
			return nil, false, err
		}
		if err := reservePaymentDiscount(txCtx, tx.Client(), order.ID, req.UserID, req.couponQuote, paymentDiscountRequestBinding(req), req.IdempotencyKeyHash); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit order transaction: %w", err)
	}
	return order, true, nil
}

func buildPaymentResetCardProductSnapshot(source *resetCardOrderSnapshotSource, payAmount float64) map[string]any {
	if source == nil || source.plan == nil {
		return nil
	}
	quantity := source.quantity
	if quantity == 0 {
		quantity = resetCardDefaultQuantity
	}
	unitPrice := source.unitPrice
	if unitPrice <= 0 {
		// Private callers that construct an old one-card source retain the
		// historical price as their unit price. Normal creation always supplies
		// the independently locked unit value above.
		unitPrice = source.price
	}
	snapshot := map[string]any{
		"schema_version":          3,
		"kind":                    "reset_card",
		"subscription_id":         source.subscriptionID,
		"plan_id":                 source.plan.ID,
		"group_id":                source.groupID,
		"name":                    "Subscription reset card",
		"description":             "One GPT subscription quota reset card",
		"currency":                payment.DefaultPaymentCurrency,
		"monthly_price":           source.monthlyPrice,
		"unit_price":              unitPrice,
		"price":                   source.price,
		"order_amount":            source.price,
		"pay_amount":              payAmount,
		"quantity":                quantity,
		"use_on_purchase":         source.useOnPurchase,
		"grant_expiry_policy":     "paid_duration",
		"grant_validity_days":     resetCardPurchasedValidityDays,
		"subscription_expires_at": source.subscriptionExpires.UTC().Format(time.RFC3339Nano),
		"idempotency_key_sha256":  source.idempotencyKeyHash,
	}
	if tier := resetCardTierSnapshotForProductSnapshot(source.tierSnapshot); tier != nil {
		snapshot["reset_card_tier"] = tier
	}
	return snapshot
}

func buildPaymentBalanceProductSnapshot(requestAmount, creditedAmount, payAmount float64, options []RechargeOption) map[string]any {
	option, _ := rechargeOptionForAmount(options, requestAmount)
	giftCreditAmount := decimal.NewFromFloat(option.BalanceBonus).Round(2).InexactFloat64()
	paidCreditAmount := decimal.NewFromFloat(creditedAmount).
		Sub(decimal.NewFromFloat(giftCreditAmount)).
		Round(2).
		InexactFloat64()
	return map[string]any{
		"schema_version":            2,
		"kind":                      "balance",
		"label":                     option.Label,
		"description":               option.Description,
		"request_amount":            requestAmount,
		"list_price":                option.OriginalPrice,
		"price":                     requestAmount,
		"discount_percent":          rechargeOptionDiscountPercent(option),
		"credited_amount":           creditedAmount,
		"paid_credit_amount":        paidCreditAmount,
		"gift_credit_amount":        giftCreditAmount,
		"pay_amount":                payAmount,
		"estimated_rate_multiplier": option.EstimatedRateMultiplier,
		"estimated_tokens":          option.EstimatedTokens,
		"entitlements": map[string]any{
			"balance_bonus": option.BalanceBonus,
			"concurrency":   option.Concurrency,
		},
	}
}

func buildPaymentProductSnapshot(plan *dbent.SubscriptionPlan, orderAmount, payAmount float64, subscriptionDays int) map[string]any {
	return buildPaymentProductSnapshotWithGroup(plan, orderAmount, payAmount, subscriptionDays, nil)
}

// buildPaymentProductSnapshotWithGroup freezes the purchasable plan and the
// limited group evidence consulted while the order was created. It does not
// pull mutable entitlement state at read time.
func buildPaymentProductSnapshotWithGroup(plan *dbent.SubscriptionPlan, orderAmount, payAmount float64, subscriptionDays int, group *Group) map[string]any {
	return buildPaymentProductSnapshotWithGroupAndResetCardTier(plan, orderAmount, payAmount, subscriptionDays, group, nil)
}

func buildPaymentProductSnapshotWithGroupAndResetCardTier(plan *dbent.SubscriptionPlan, orderAmount, payAmount float64, subscriptionDays int, group *Group, tierSnapshot *SubscriptionResetCardTierSnapshot) map[string]any {
	if plan == nil {
		return nil
	}
	snapshot := map[string]any{
		"kind":              "subscription",
		"plan_id":           plan.ID,
		"group_id":          plan.GroupID,
		"name":              plan.Name,
		"product_name":      plan.ProductName,
		"description":       plan.Description,
		"features":          paymentSnapshotFeatures(plan.Features),
		"currency":          plan.Currency,
		"list_price":        plan.OriginalPrice,
		"price":             plan.Price,
		"order_amount":      orderAmount,
		"pay_amount":        payAmount,
		"discount_percent":  PlanDiscountPercent(plan.Price, plan.OriginalPrice),
		"validity_days":     plan.ValidityDays,
		"validity_unit":     plan.ValidityUnit,
		"subscription_days": subscriptionDays,
		"entitlements":      paymentSnapshotEntitlements(plan.Entitlements),
	}
	if group != nil {
		snapshot["group_name"] = group.Name
		if group.DailyLimitUSD != nil {
			snapshot["daily_limit_usd"] = *group.DailyLimitUSD
		}
		if group.WeeklyLimitUSD != nil {
			snapshot["weekly_limit_usd"] = *group.WeeklyLimitUSD
		}
		if group.MonthlyLimitUSD != nil {
			snapshot["monthly_limit_usd"] = *group.MonthlyLimitUSD
		}
	}
	if tier := resetCardTierSnapshotForProductSnapshot(tierSnapshot); tier != nil {
		snapshot["reset_card_tier"] = tier
	}
	return snapshot
}

// paymentSnapshotEntitlements preserves fulfillment benefits while omitting
// administrator-only audience metadata. A historical order needs its benefit
// snapshot, never a list of users who could have purchased it at the time.
func paymentSnapshotEntitlements(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	copy := make(map[string]any, len(raw))
	for key, value := range raw {
		copy[key] = value
	}
	delete(copy, "purchase_rules")
	delete(copy, "reset_card_purchase_rules")
	return copy
}

func paymentSnapshotFeatures(raw string) []string {
	var features []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &features); err != nil {
		return nil
	}
	out := make([]string, 0, len(features))
	for _, feature := range features {
		feature = strings.TrimSpace(feature)
		if feature != "" {
			out = append(out, feature)
		}
	}
	return out
}

func (s *PaymentService) allocateOutTradeNo(ctx context.Context, tx *dbent.Tx) (string, error) {
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		candidate := generateOutTradeNo()
		exists, err := tx.PaymentOrder.Query().Where(paymentorder.OutTradeNo(candidate)).Exist(ctx)
		if err != nil {
			return "", fmt.Errorf("check out_trade_no uniqueness: %w", err)
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("generate unique out_trade_no: exhausted %d attempts", maxAttempts)
}

func (s *PaymentService) checkPendingLimit(ctx context.Context, tx *dbent.Tx, userID int64, max int) error {
	if max <= 0 {
		max = defaultMaxPendingOrders
	}
	c, err := tx.PaymentOrder.Query().Where(paymentorder.UserIDEQ(userID), paymentorder.StatusEQ(OrderStatusPending)).Count(ctx)
	if err != nil {
		return fmt.Errorf("count pending orders: %w", err)
	}
	if c >= max {
		return infraerrors.TooManyRequests("TOO_MANY_PENDING", "too_many_pending").
			WithMetadata(map[string]string{"max": strconv.Itoa(max)})
	}
	return nil
}

func buildPaymentOrderProviderSnapshot(sel *payment.InstanceSelection, req CreateOrderRequest) map[string]any {
	if sel == nil {
		return nil
	}

	snapshot := map[string]any{}
	snapshot["schema_version"] = 2

	instanceID := strings.TrimSpace(sel.InstanceID)
	if instanceID != "" {
		snapshot["provider_instance_id"] = instanceID
	}

	providerKey := strings.TrimSpace(sel.ProviderKey)
	if providerKey != "" {
		snapshot["provider_key"] = providerKey
	}

	paymentMode := strings.TrimSpace(sel.PaymentMode)
	if paymentMode != "" {
		snapshot["payment_mode"] = paymentMode
	}

	if providerKey == payment.TypeWxpay {
		snapshot["checkout_mode"] = paymentOrderWxpayCheckoutMode(req)
		if merchantAppID := paymentOrderSnapshotWxpayAppID(sel, req); merchantAppID != "" {
			snapshot["merchant_app_id"] = merchantAppID
		}
		if merchantID := strings.TrimSpace(sel.Config["mchId"]); merchantID != "" {
			snapshot["merchant_id"] = merchantID
		}
		snapshot["currency"] = payment.DefaultPaymentCurrency
	}
	if providerKey == payment.TypeAlipay {
		if merchantAppID := strings.TrimSpace(sel.Config["appId"]); merchantAppID != "" {
			snapshot["merchant_app_id"] = merchantAppID
		}
		snapshot["currency"] = payment.DefaultPaymentCurrency
	}
	if providerKey == payment.TypeEasyPay {
		if merchantID := strings.TrimSpace(sel.Config["pid"]); merchantID != "" {
			snapshot["merchant_id"] = merchantID
		}
		snapshot["currency"] = payment.DefaultPaymentCurrency
	}
	if providerKey == payment.TypeStripe {
		snapshot["currency"] = paymentProviderConfigCurrency(providerKey, sel.Config)
	}
	if providerKey == payment.TypeAirwallex {
		if accountID := strings.TrimSpace(sel.Config["accountId"]); accountID != "" {
			snapshot["merchant_id"] = accountID
		}
		snapshot["currency"] = paymentProviderConfigCurrency(providerKey, sel.Config)
	}

	if len(snapshot) == 1 {
		return nil
	}
	if providerKey == payment.TypeUnifiedPay {
		snapshot["currency"] = payment.DefaultPaymentCurrency
		for _, key := range []string{"environment", "organization_id", "product_id", "app_id"} {
			if value := strings.TrimSpace(sel.Config[key]); value != "" {
				snapshot[key] = value
			}
		}
	}
	return snapshot
}

func paymentOrderWxpayCheckoutMode(req CreateOrderRequest) string {
	if strings.TrimSpace(req.OpenID) != "" {
		return "jsapi"
	}
	if req.IsMobile {
		return "h5"
	}
	return "native"
}

func paymentOrderSnapshotWxpayAppID(sel *payment.InstanceSelection, req CreateOrderRequest) string {
	if sel == nil || strings.TrimSpace(sel.ProviderKey) != payment.TypeWxpay {
		return ""
	}
	if strings.TrimSpace(req.OpenID) != "" {
		return strings.TrimSpace(provider.ResolveWxpayJSAPIAppID(sel.Config))
	}
	return strings.TrimSpace(sel.Config["appId"])
}

func (s *PaymentService) checkDailyLimit(ctx context.Context, tx *dbent.Tx, userID int64, amount, limit float64) error {
	if limit <= 0 {
		return nil
	}
	ts := psStartOfDayUTC(time.Now())
	orders, err := tx.PaymentOrder.Query().Where(
		paymentorder.UserIDEQ(userID),
		paymentorder.StatusIn(OrderStatusPaid, OrderStatusRecharging, OrderStatusCompleted, OrderStatusFailed),
		paymentorder.PaidAtGTE(ts),
	).All(ctx)
	if err != nil {
		return fmt.Errorf("query daily usage: %w", err)
	}
	var used float64
	for _, o := range orders {
		if o.OrderType == payment.OrderTypeBalance {
			used += o.PayAmount
			continue
		}
		used += o.Amount
	}
	if used+amount > limit {
		return infraerrors.TooManyRequests("DAILY_LIMIT_EXCEEDED", "daily_limit_exceeded").
			WithMetadata(map[string]string{"remaining": fmt.Sprintf("%.2f", math.Max(0, limit-used))})
	}
	return nil
}

func (s *PaymentService) selectCreateOrderInstance(ctx context.Context, req CreateOrderRequest, cfg *PaymentConfig, payAmount float64) (*payment.InstanceSelection, error) {
	providerKey, err := s.createOrderProviderKey(ctx, req.PaymentType)
	if err != nil {
		return nil, err
	}
	if providerKey == payment.TypeUnifiedPay {
		return s.unifiedPayment.Selection(req.PaymentType), nil
	}
	selectCtx, err := s.prepareCreateOrderSelectionContext(ctx, req)
	if err != nil {
		return nil, err
	}
	sel, err := s.loadBalancer.SelectInstance(selectCtx, providerKey, req.PaymentType, payment.Strategy(cfg.LoadBalanceStrategy), payAmount)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_GATEWAY_ERROR", "method_not_configured").
			WithMetadata(map[string]string{"payment_type": req.PaymentType})
	}
	if sel == nil {
		return nil, infraerrors.TooManyRequests("NO_AVAILABLE_INSTANCE", "no_available_instance")
	}
	return sel, nil
}

func (s *PaymentService) prepareCreateOrderSelectionContext(ctx context.Context, req CreateOrderRequest) (context.Context, error) {
	if !requestNeedsWeChatJSAPICompatibility(req) {
		return ctx, nil
	}
	if !s.usesOfficialWxpayVisibleMethod(ctx) {
		return ctx, nil
	}
	expectedAppID, _, err := s.getWeChatPaymentOAuthCredential(ctx)
	if err != nil {
		return nil, err
	}
	return payment.WithWxpayJSAPIAppID(ctx, expectedAppID), nil
}

func requestNeedsWeChatJSAPICompatibility(req CreateOrderRequest) bool {
	if payment.GetBasePaymentType(req.PaymentType) != payment.TypeWxpay {
		return false
	}
	return req.IsWeChatBrowser || strings.TrimSpace(req.OpenID) != ""
}

func (s *PaymentService) usesOfficialWxpayVisibleMethod(ctx context.Context) bool {
	if s == nil || s.configService == nil {
		return false
	}
	inst, err := s.configService.resolveEnabledVisibleMethodInstance(ctx, payment.TypeWxpay)
	if err != nil {
		return false
	}
	if inst == nil {
		return false
	}
	return inst.ProviderKey == payment.TypeWxpay
}

func (s *PaymentService) invokeProvider(ctx context.Context, order *dbent.PaymentOrder, req CreateOrderRequest, cfg *PaymentConfig, limitAmount float64, payAmountStr string, payAmount float64, plan *dbent.SubscriptionPlan, sel *payment.InstanceSelection) (*CreateOrderResponse, error) {
	var prov payment.Provider
	var err error
	if sel != nil && sel.ProviderKey == payment.TypeUnifiedPay {
		if s.unifiedPayment == nil || !s.unifiedPayment.Enabled() {
			return nil, unifiedpay.ErrDisabled
		}
		prov = s.unifiedPayment
	} else {
		prov, err = provider.CreateProvider(sel.ProviderKey, sel.InstanceID, sel.Config)
	}
	if err != nil {
		slog.Error("[PaymentService] CreateProvider failed", "provider", sel.ProviderKey, "instance", sel.InstanceID, "error", err)
		// If the provider returned a structured ApplicationError (e.g. WXPAY_CONFIG_MISSING_KEY),
		// pass it through with provider context added to metadata. Otherwise wrap as PAYMENT_PROVIDER_MISCONFIGURED.
		if appErr := new(infraerrors.ApplicationError); errors.As(err, &appErr) {
			md := map[string]string{"provider": sel.ProviderKey, "instance_id": sel.InstanceID}
			for k, v := range appErr.Metadata {
				md[k] = v
			}
			return nil, appErr.WithMetadata(md)
		}
		return nil, infraerrors.ServiceUnavailable("PAYMENT_PROVIDER_MISCONFIGURED", "provider_misconfigured").
			WithMetadata(map[string]string{"provider": sel.ProviderKey, "instance_id": sel.InstanceID})
	}
	subject := s.buildPaymentSubject(plan, limitAmount, cfg, sel)
	if req.OrderType == payment.OrderTypeResetCard {
		subject = "订阅重置卡"
	}
	outTradeNo := order.OutTradeNo
	canonicalReturnURL, err := CanonicalizeReturnURL(req.ReturnURL, req.SrcHost, req.SrcURL)
	if err != nil {
		return nil, err
	}
	resumeToken := ""
	if resume := s.paymentResume(); resume != nil {
		if canonicalReturnURL != "" && resume.isSigningConfigured() {
			resumeToken, err = resume.CreateToken(ResumeTokenClaims{
				OrderID:            order.ID,
				UserID:             order.UserID,
				ProviderInstanceID: sel.InstanceID,
				ProviderKey:        sel.ProviderKey,
				PaymentType:        req.PaymentType,
				CanonicalReturnURL: canonicalReturnURL,
			})
			if err != nil {
				return nil, fmt.Errorf("create payment resume token: %w", err)
			}
		}
	}
	providerReturnURL := canonicalReturnURL
	if sel.ProviderKey == payment.TypeUnifiedPay {
		if providerReturnURL == "" || providerReturnURL != s.unifiedPayment.ReturnURL() {
			return nil, infraerrors.BadRequest("INVALID_RETURN_URL", "return URL must match the configured Sub2 payment result page")
		}
	} else {
		providerReturnURL, err = buildPaymentReturnURL(canonicalReturnURL, order.ID, outTradeNo, resumeToken)
		if err != nil {
			return nil, err
		}
	}
	providerReq := buildProviderCreatePaymentRequest(CreateOrderRequest{
		PaymentType: req.PaymentType,
		OrderType:   req.OrderType,
		OpenID:      req.OpenID,
		ClientIP:    req.ClientIP,
		IsMobile:    req.IsMobile,
		ReturnURL:   providerReturnURL,
	}, sel, outTradeNo, payAmountStr, subject)
	providerReq.ExpiresInSeconds = paymentOrderExpiresInSeconds(order.ExpiresAt, time.Now())
	providerReq.AlipayMobilePrecreate = shouldUseAlipayMobilePrecreate(req, cfg, sel)
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	pr, err := prov.CreatePayment(ctx, providerReq)
	finishProviderCall()
	if err != nil {
		slog.Error("[PaymentService] CreatePayment failed", "provider", sel.ProviderKey, "instance", sel.InstanceID, "error", err)
		if sel.ProviderKey == payment.TypeUnifiedPay && errors.Is(err, unifiedpay.ErrCreateStateUnconfirmed) {
			return nil, err
		}
		if appErr := new(infraerrors.ApplicationError); errors.As(err, &appErr) {
			return nil, appErr
		}
		return nil, classifyCreatePaymentError(req, sel.ProviderKey, err)
	}
	if payment.GetBasePaymentType(req.PaymentType) == payment.TypeAlipay && strings.TrimSpace(pr.QRCode) != "" && !provider.ValidateAlipayQRCode(pr.QRCode) {
		slog.Warn("[PaymentService] discarded invalid Alipay QR payload", "provider", sel.ProviderKey, "instance", sel.InstanceID)
		pr.QRCode = ""
	}
	if sel.ProviderKey == payment.TypeAlipay && strings.TrimSpace(pr.PayURL) != "" && !provider.ValidateAlipayPayURL(pr.PayURL) {
		slog.Warn("[PaymentService] discarded invalid Alipay checkout URL", "provider", sel.ProviderKey, "instance", sel.InstanceID)
		pr.PayURL = ""
	}
	sanitizeCreatePaymentResponseDetails(pr)
	providerSnapshot := order.ProviderSnapshot
	if sel.ProviderKey == payment.TypeUnifiedPay || pr.CheckoutFrameURL != "" {
		providerSnapshot = clonePaymentOrderSnapshot(providerSnapshot)
		if providerSnapshot == nil {
			providerSnapshot = make(map[string]any)
		}
		if sel.ProviderKey == payment.TypeUnifiedPay {
			providerSnapshot["payment_order_id"] = strings.TrimSpace(pr.TradeNo)
		}
		if frameURL := paymentOrderCheckoutFrameURLFromProviderResponse(sel, req.PaymentType, pr); frameURL != "" {
			providerSnapshot[paymentOrderCheckoutFrameURLSnapshotKey] = frameURL
		}
	}
	update := s.entClient.PaymentOrder.UpdateOneID(order.ID).
		SetNillablePaymentTradeNo(psNilIfEmpty(pr.TradeNo)).
		SetNillablePayURL(psNilIfEmpty(pr.PayURL)).
		SetNillableQrCode(psNilIfEmpty(pr.QRCode)).
		SetNillableProviderInstanceID(psNilIfEmpty(sel.InstanceID)).
		SetNillableProviderKey(psNilIfEmpty(sel.ProviderKey))
	if providerSnapshot != nil {
		update.SetProviderSnapshot(providerSnapshot)
	}
	_, err = update.Save(ctx)
	if err != nil {
		if sel.ProviderKey == payment.TypeUnifiedPay {
			return nil, fmt.Errorf("%w: persist remote order binding", unifiedpay.ErrCreateStateUnconfirmed)
		}
		return nil, fmt.Errorf("update order with payment details: %w", err)
	}
	s.writeAuditLog(ctx, order.ID, "ORDER_CREATED", fmt.Sprintf("user:%d", req.UserID), map[string]any{
		"paymentAmount":  req.Amount,
		"creditedAmount": order.Amount,
		"payAmount":      order.PayAmount,
		"paymentType":    req.PaymentType,
		"orderType":      req.OrderType,
		"paymentSource":  NormalizePaymentSource(req.PaymentSource),
	})
	resultType := pr.ResultType
	if resultType == "" {
		resultType = payment.CreatePaymentResultOrderCreated
	}
	resp := buildCreateOrderResponse(order, req, payAmount, sel, pr, resultType)
	resp.ResumeToken = resumeToken
	resp.AlipayMobilePrecreateDeepLink = providerReq.AlipayMobilePrecreate && strings.TrimSpace(pr.QRCode) != ""
	return resp, nil
}

func paymentOrderExpiresInSeconds(expiresAt, now time.Time) int {
	if !expiresAt.After(now) {
		return 0
	}
	// Providers interpret this as a lifetime starting when they receive the
	// request. Floor the remaining local deadline so their checkout can never
	// be advertised as valid after Sub2 has already expired the order.
	return int(math.Floor(expiresAt.Sub(now).Seconds()))
}

func shouldUseAlipayMobilePrecreate(req CreateOrderRequest, cfg *PaymentConfig, sel *payment.InstanceSelection) bool {
	return cfg != nil &&
		cfg.AlipayMobilePrecreateDeepLink &&
		req.IsMobile &&
		sel != nil &&
		strings.EqualFold(strings.TrimSpace(sel.ProviderKey), payment.TypeAlipay)
}

func sanitizeCreatePaymentResponseDetails(pr *payment.CreatePaymentResponse) {
	if pr == nil {
		return
	}
	pr.TradeNo = removePostgresTextNUL(pr.TradeNo)
	pr.PayURL = removePostgresTextNUL(pr.PayURL)
	pr.QRCode = removePostgresTextNUL(pr.QRCode)
	if pr.JSAPI != nil {
		pr.JSAPI.AppID = removePostgresTextNUL(pr.JSAPI.AppID)
		pr.JSAPI.TimeStamp = removePostgresTextNUL(pr.JSAPI.TimeStamp)
		pr.JSAPI.NonceStr = removePostgresTextNUL(pr.JSAPI.NonceStr)
		pr.JSAPI.Package = removePostgresTextNUL(pr.JSAPI.Package)
		pr.JSAPI.SignType = removePostgresTextNUL(pr.JSAPI.SignType)
		pr.JSAPI.PaySign = removePostgresTextNUL(pr.JSAPI.PaySign)
	}
}

func removePostgresTextNUL(value string) string {
	if !strings.ContainsRune(value, 0) {
		return value
	}
	return strings.ReplaceAll(value, "\x00", "")
}

func buildProviderCreatePaymentRequest(req CreateOrderRequest, sel *payment.InstanceSelection, orderID, amount, subject string) payment.CreatePaymentRequest {
	return payment.CreatePaymentRequest{
		OrderID:            orderID,
		Amount:             amount,
		PaymentType:        req.PaymentType,
		OrderType:          req.OrderType,
		Subject:            subject,
		ReturnURL:          req.ReturnURL,
		OpenID:             strings.TrimSpace(req.OpenID),
		ClientIP:           req.ClientIP,
		IsMobile:           req.IsMobile,
		InstanceSubMethods: selectedInstanceSupportedTypes(sel),
	}
}

func selectedInstanceSupportedTypes(sel *payment.InstanceSelection) string {
	if sel == nil {
		return ""
	}
	return sel.SupportedTypes
}

// Customer-facing channel descriptions deliberately omit internal branding and
// legacy prefix/suffix configuration. They do not determine entitlement tiers.
func (s *PaymentService) buildPaymentSubject(plan *dbent.SubscriptionPlan, _ float64, _ *PaymentConfig, _ *payment.InstanceSelection) string {
	if plan == nil {
		return "余额充值"
	}
	words := strings.FieldsFunc(strings.ToLower(plan.Name+" "+plan.ProductName), func(r rune) bool {
		return r < 'a' || r > 'z'
	})
	for _, word := range words {
		if word == "pro" {
			return "Pro订阅"
		}
		if word == "plus" {
			return "Plus订阅"
		}
	}
	return "订阅"
}

func hasPaymentProductNameAffix(cfg *PaymentConfig) bool {
	if cfg == nil {
		return false
	}
	pf := strings.TrimSpace(cfg.ProductNamePrefix)
	sf := strings.TrimSpace(cfg.ProductNameSuffix)
	return pf != "" || sf != ""
}

func applyPaymentProductNameAffix(productName string, cfg *PaymentConfig) string {
	if !hasPaymentProductNameAffix(cfg) {
		return productName
	}
	pf := strings.TrimSpace(cfg.ProductNamePrefix)
	sf := strings.TrimSpace(cfg.ProductNameSuffix)
	return strings.TrimSpace(pf + " " + productName + " " + sf)
}

func (s *PaymentService) maybeBuildWeChatOAuthRequiredResponse(ctx context.Context, req CreateOrderRequest, amount, payAmount, feeRate float64) (*CreateOrderResponse, error) {
	return s.maybeBuildWeChatOAuthRequiredResponseForSelection(ctx, req, amount, payAmount, feeRate, nil)
}

func (s *PaymentService) maybeBuildWeChatOAuthRequiredResponseForSelection(ctx context.Context, req CreateOrderRequest, amount, payAmount, feeRate float64, sel *payment.InstanceSelection) (*CreateOrderResponse, error) {
	if sel != nil && sel.ProviderKey != "" && sel.ProviderKey != payment.TypeWxpay {
		return nil, nil
	}
	if strings.TrimSpace(req.OpenID) != "" || !req.IsWeChatBrowser || payment.GetBasePaymentType(req.PaymentType) != payment.TypeWxpay {
		return nil, nil
	}
	return s.buildWeChatOAuthRequiredResponse(ctx, req, amount, payAmount, feeRate)
}

func (s *PaymentService) buildWeChatOAuthRequiredResponse(ctx context.Context, req CreateOrderRequest, amount, payAmount, feeRate float64) (*CreateOrderResponse, error) {
	appID, _, err := s.getWeChatPaymentOAuthCredential(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.paymentResume().ensureSigningKey(); err != nil {
		return nil, err
	}

	authorizeURL, err := buildWeChatPaymentOAuthStartURL(req, "snsapi_base")
	if err != nil {
		return nil, err
	}

	return &CreateOrderResponse{
		Amount:      amount,
		PayAmount:   payAmount,
		FeeRate:     feeRate,
		ResultType:  payment.CreatePaymentResultOAuthRequired,
		PaymentType: req.PaymentType,
		OAuth: &payment.WechatOAuthInfo{
			AuthorizeURL: authorizeURL,
			AppID:        appID,
			Scope:        "snsapi_base",
			RedirectURL:  "/auth/wechat/payment/callback",
		},
	}, nil
}

func (s *PaymentService) validateSelectedCreateOrderInstance(ctx context.Context, req CreateOrderRequest, sel *payment.InstanceSelection) error {
	if !requiresWeChatJSAPICompatibleSelection(req, sel) {
		return nil
	}
	expectedAppID, _, err := s.getWeChatPaymentOAuthCredential(ctx)
	if err != nil {
		return err
	}
	selectedAppID := provider.ResolveWxpayJSAPIAppID(sel.Config)
	if selectedAppID == "" || selectedAppID != expectedAppID {
		return infraerrors.TooManyRequests("NO_AVAILABLE_INSTANCE", "selected payment instance is not compatible with the current WeChat OAuth app")
	}
	return nil
}

func calculateCreateOrderPayAmount(limitAmount, feeRate float64, currency string) (string, float64, error) {
	if err := validateCreateOrderAmountCurrency(limitAmount, currency); err != nil {
		return "", 0, err
	}
	payAmountStr := payment.CalculatePayAmountForCurrency(limitAmount, feeRate, currency)
	if _, err := payment.AmountToMinorUnit(payAmountStr, currency); err != nil {
		return "", 0, infraerrors.BadRequest("INVALID_AMOUNT", err.Error()).
			WithMetadata(map[string]string{"currency": currency})
	}
	payAmount, err := strconv.ParseFloat(payAmountStr, 64)
	if err != nil {
		return "", 0, infraerrors.BadRequest("INVALID_AMOUNT", "invalid payment amount").
			WithMetadata(map[string]string{"currency": currency})
	}
	return payAmountStr, payAmount, nil
}

func calculateCreateOrderPayAmountForOrderType(limitAmount, feeRate float64, currency, orderType string, usdToCnyRate float64) (string, float64, error) {
	paymentAmount := limitAmount
	if orderType == payment.OrderTypeSubscription {
		paymentAmount = calculateSubscriptionGatewayBaseAmount(limitAmount, usdToCnyRate, currency)
	}
	return calculateCreateOrderPayAmount(paymentAmount, feeRate, currency)
}

// calculateSubscriptionGatewayBaseAmount 计算订阅订单的网关扣款基数。
// 换算是显式 opt-in：仅当管理员配置了订阅汇率（rate > 0，1 USD = rate CNY）
// 且网关币种为 CNY 时，按 price × rate 换算；未配置时保持 price 直付的存量行为。
func calculateSubscriptionGatewayBaseAmount(amount, usdToCnyRate float64, currency string) float64 {
	rate := normalizeSubscriptionUSDToCNYRate(usdToCnyRate)
	if rate <= 0 || currency != payment.DefaultPaymentCurrency {
		return amount
	}
	return decimal.NewFromFloat(amount).
		Mul(decimal.NewFromFloat(rate)).
		Round(int32(payment.CurrencyMaxFractionDigits(currency))).
		InexactFloat64()
}

func validateCreateOrderAmountCurrency(amount float64, currency string) error {
	amountStr := strconv.FormatFloat(amount, 'f', -1, 64)
	if _, err := payment.AmountToMinorUnit(amountStr, currency); err != nil {
		return infraerrors.BadRequest("INVALID_AMOUNT", err.Error()).
			WithMetadata(map[string]string{"currency": currency})
	}
	return nil
}

func validateSelectedCreateOrderAmountCurrency(payAmount string, sel *payment.InstanceSelection) error {
	if sel == nil {
		return nil
	}
	currency := paymentProviderConfigCurrency(sel.ProviderKey, sel.Config)
	if _, err := payment.AmountToMinorUnit(payAmount, currency); err != nil {
		return infraerrors.BadRequest("INVALID_AMOUNT", err.Error()).
			WithMetadata(map[string]string{"currency": currency})
	}
	return nil
}

func requiresWeChatJSAPICompatibleSelection(req CreateOrderRequest, sel *payment.InstanceSelection) bool {
	if sel == nil || sel.ProviderKey != payment.TypeWxpay || payment.GetBasePaymentType(req.PaymentType) != payment.TypeWxpay {
		return false
	}
	return req.IsWeChatBrowser || strings.TrimSpace(req.OpenID) != ""
}

func (s *PaymentService) getWeChatPaymentOAuthCredential(ctx context.Context) (string, string, error) {
	if s == nil || s.configService == nil || s.configService.settingRepo == nil {
		return "", "", infraerrors.ServiceUnavailable(
			"WECHAT_PAYMENT_MP_NOT_CONFIGURED",
			"wechat in-app payment requires a complete WeChat MP OAuth credential",
		)
	}
	cfg, err := (&SettingService{settingRepo: s.configService.settingRepo}).GetWeChatConnectOAuthConfig(ctx)
	appID := strings.TrimSpace(cfg.AppIDForMode("mp"))
	appSecret := strings.TrimSpace(cfg.AppSecretForMode("mp"))
	if err != nil || !cfg.SupportsMode("mp") || appID == "" || appSecret == "" {
		return "", "", infraerrors.ServiceUnavailable(
			"WECHAT_PAYMENT_MP_NOT_CONFIGURED",
			"wechat in-app payment requires a complete WeChat MP OAuth credential",
		)
	}
	return appID, appSecret, nil
}

func classifyCreatePaymentError(req CreateOrderRequest, providerKey string, err error) error {
	if err == nil {
		return nil
	}
	if providerKey == payment.TypeWxpay &&
		payment.GetBasePaymentType(req.PaymentType) == payment.TypeWxpay &&
		strings.Contains(err.Error(), "wxpay h5 payments are not authorized for this merchant") {
		return infraerrors.ServiceUnavailable(
			"WECHAT_H5_NOT_AUTHORIZED",
			"wechat h5 payment is not available for this merchant",
		).WithMetadata(map[string]string{
			"action": "open_in_wechat_or_scan_qr",
		})
	}
	return infraerrors.ServiceUnavailable("PAYMENT_GATEWAY_ERROR", fmt.Sprintf("payment gateway error: %s", err.Error()))
}

func buildCreateOrderResponse(order *dbent.PaymentOrder, req CreateOrderRequest, payAmount float64, sel *payment.InstanceSelection, pr *payment.CreatePaymentResponse, resultType payment.CreatePaymentResultType) *CreateOrderResponse {
	return &CreateOrderResponse{
		OrderID:          order.ID,
		Amount:           order.Amount,
		PayAmount:        payAmount,
		FeeRate:          order.FeeRate,
		Status:           OrderStatusPending,
		ResultType:       resultType,
		PaymentType:      req.PaymentType,
		OutTradeNo:       order.OutTradeNo,
		PayURL:           pr.PayURL,
		CheckoutFrameURL: paymentOrderCheckoutFrameURLFromProviderResponse(sel, req.PaymentType, pr),
		QRCode:           pr.QRCode,
		ClientSecret:     pr.ClientSecret,
		IntentID:         pr.IntentID,
		Currency:         pr.Currency,
		CountryCode:      pr.CountryCode,
		PaymentEnv:       pr.PaymentEnv,
		OAuth:            pr.OAuth,
		JSAPI:            pr.JSAPI,
		JSAPIPayload:     pr.JSAPI,
		ExpiresAt:        order.ExpiresAt,
		PaymentMode:      sel.PaymentMode,
		PaymentDiscount:  paymentOrderResponsePaymentDiscount(order),
	}
}

// paymentOrderResponsePaymentDiscount exposes only the public immutable
// settlement facts already persisted on the order. It deliberately derives
// from the same sanitizer used by order history rather than a request quote.
func paymentOrderResponsePaymentDiscount(order *dbent.PaymentOrder) map[string]any {
	snapshot := SanitizedPaymentOrderProductSnapshot(order)
	if snapshot == nil {
		return nil
	}
	discount, ok := snapshot["payment_discount"].(map[string]any)
	if !ok || len(discount) == 0 {
		return nil
	}
	return discount
}

func buildWeChatPaymentOAuthStartURL(req CreateOrderRequest, scope string) (string, error) {
	u, err := url.Parse("/api/v1/auth/oauth/wechat/payment/start")
	if err != nil {
		return "", fmt.Errorf("build wechat payment oauth start url: %w", err)
	}
	q := u.Query()
	q.Set("payment_type", strings.TrimSpace(req.PaymentType))
	if req.CouponCode != "" {
		q.Set("coupon_code", req.CouponCode)
		q.Set("coupon_revision", req.CouponRevision)
	}
	if req.Amount > 0 {
		q.Set("amount", strconv.FormatFloat(req.Amount, 'f', -1, 64))
	}
	if orderType := strings.TrimSpace(req.OrderType); orderType != "" {
		q.Set("order_type", orderType)
	}
	if req.PlanID > 0 {
		q.Set("plan_id", strconv.FormatInt(req.PlanID, 10))
	}
	if req.SubscriptionID > 0 {
		q.Set("subscription_id", strconv.FormatInt(req.SubscriptionID, 10))
	}
	if revision := strings.TrimSpace(req.ResetCardTierRevision); revision != "" {
		q.Set("reset_card_tier_revision", revision)
	}
	if req.OrderType == payment.OrderTypeResetCard {
		q.Set("reset_card_quantity", strconv.Itoa(req.ResetCardQuantity))
		if req.ResetCardUseOnPurchase {
			q.Set("reset_card_use_on_purchase", "true")
		}
	}
	if (req.OrderType == payment.OrderTypeResetCard || req.CouponCode != "") && req.IdempotencyKeyHash != "" {
		q.Set("idempotency_key_hash", req.IdempotencyKeyHash)
	}
	if scope = strings.TrimSpace(scope); scope != "" {
		q.Set("scope", scope)
	}
	if redirectTo := paymentRedirectPathFromURL(req.SrcURL); redirectTo != "" {
		q.Set("redirect", redirectTo)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func paymentRedirectPathFromURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "/purchase"
	}
	if strings.HasPrefix(rawURL, "/") && !strings.HasPrefix(rawURL, "//") {
		return normalizePaymentRedirectPath(rawURL)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "/purchase"
	}
	path := strings.TrimSpace(u.EscapedPath())
	if path == "" {
		path = strings.TrimSpace(u.Path)
	}
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "/purchase"
	}
	if strings.TrimSpace(u.RawQuery) != "" {
		path += "?" + u.RawQuery
	}
	return normalizePaymentRedirectPath(path)
}

func normalizePaymentRedirectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/purchase"
	}
	if path == "/payment" {
		return "/purchase"
	}
	if strings.HasPrefix(path, "/payment?") {
		return "/purchase" + strings.TrimPrefix(path, "/payment")
	}
	return path
}

// --- Order Queries ---

func (s *PaymentService) GetOrder(ctx context.Context, orderID, userID int64) (*dbent.PaymentOrder, error) {
	o, err := s.entClient.PaymentOrder.Query().Where(paymentorder.IDEQ(orderID)).WithInvoiceRequest().Only(ctx)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.UserID != userID {
		return nil, infraerrors.Forbidden("FORBIDDEN", "no permission for this order")
	}
	return o, nil
}

func (s *PaymentService) GetOrderByID(ctx context.Context, orderID int64) (*dbent.PaymentOrder, error) {
	o, err := s.entClient.PaymentOrder.Query().Where(paymentorder.IDEQ(orderID)).WithInvoiceRequest().Only(ctx)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	return o, nil
}

func (s *PaymentService) GetUserOrders(ctx context.Context, userID int64, p OrderListParams) ([]*dbent.PaymentOrder, int, error) {
	q := s.entClient.PaymentOrder.Query().Where(paymentorder.UserIDEQ(userID))
	if err := applyPaymentOrderListFilters(q, p, false); err != nil {
		return nil, 0, err
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count user orders: %w", err)
	}
	ps, pg := applyPagination(p.PageSize, p.Page)
	orders, err := q.WithInvoiceRequest().Order(dbent.Desc(paymentorder.FieldCreatedAt)).Limit(ps).Offset((pg - 1) * ps).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("query user orders: %w", err)
	}
	return orders, total, nil
}

// AdminListOrders returns a paginated list of orders. If userID > 0, filters by user.
func (s *PaymentService) AdminListOrders(ctx context.Context, userID int64, p OrderListParams) ([]*dbent.PaymentOrder, int, error) {
	q := s.entClient.PaymentOrder.Query()
	if userID > 0 {
		q = q.Where(paymentorder.UserIDEQ(userID))
	}
	if err := applyPaymentOrderListFilters(q, p, true); err != nil {
		return nil, 0, err
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count admin orders: %w", err)
	}
	ps, pg := applyPagination(p.PageSize, p.Page)
	orders, err := q.WithInvoiceRequest().Order(dbent.Desc(paymentorder.FieldCreatedAt)).Limit(ps).Offset((pg - 1) * ps).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("query admin orders: %w", err)
	}
	return orders, total, nil
}

// applyPaymentOrderListFilters adds all predicates before Count and pagination.
// User routes call it with adminOnly=false, so only the user-approved invoice
// and fulfillment filters apply there; admin routes additionally get payment
// and customer-email delivery filters.
func applyPaymentOrderListFilters(q *dbent.PaymentOrderQuery, p OrderListParams, adminOnly bool) error {
	if q == nil {
		return errors.New("nil payment order query")
	}
	if p.Status != "" {
		q = q.Where(paymentorder.StatusEQ(p.Status))
	}
	if p.OrderType != "" {
		q = q.Where(paymentorder.OrderTypeEQ(p.OrderType))
	}
	if p.PaymentType != "" {
		q = q.Where(paymentorder.PaymentTypeEQ(p.PaymentType))
	}
	invoiceStatus, err := normalizeInvoiceStatusFilter(p.InvoiceStatus)
	if err != nil {
		return err
	}
	switch invoiceStatus {
	case InvoiceStatusFilterNone:
		q.Where(paymentorder.Not(paymentorder.HasInvoiceRequest()))
	case InvoiceStatusFilterAny:
		q.Where(paymentorder.HasInvoiceRequest())
	case InvoiceStatusPending, InvoiceStatusProcessing, InvoiceStatusIssued, InvoiceStatusRejected:
		q.Where(paymentorder.HasInvoiceRequestWith(paymentinvoicerequest.StatusEQ(invoiceStatus)))
	}
	fulfillmentStatus, err := normalizeInvoiceFulfillmentStatusFilter(p.FulfillmentStatus)
	if err != nil {
		return err
	}
	if fulfillmentStatus != "" {
		q.Where(paymentOrderFulfillmentStatusPredicate(fulfillmentStatus))
	}
	if adminOnly {
		paymentStatus, err := normalizeInvoicePaymentStatusFilter(p.PaymentStatus)
		if err != nil {
			return err
		}
		switch paymentStatus {
		case InvoicePaymentStatusPaid:
			q.Where(paymentorder.PaidAtNotNil())
		case InvoicePaymentStatusUnpaid:
			q.Where(paymentorder.PaidAtIsNil())
		}
		emailStatus, err := normalizeInvoiceEmailStatusFilter(p.InvoiceEmailStatus)
		if err != nil {
			return err
		}
		if emailStatus != "" {
			q.Where(paymentorder.HasInvoiceRequestWith(paymentinvoicerequest.EmailDeliveryStatusEQ(emailStatus)))
		}
	}
	if adminOnly && strings.TrimSpace(p.Keyword) != "" {
		keyword := strings.TrimSpace(p.Keyword)
		if orderID, ok := exactPaymentOrderIDKeyword(keyword); ok {
			q.Where(paymentorder.IDEQ(orderID))
			return nil
		}
		q.Where(paymentorder.Or(
			paymentorder.OutTradeNoContainsFold(keyword),
			paymentorder.UserEmailContainsFold(keyword),
			paymentorder.UserNameContainsFold(keyword),
		))
	}
	return nil
}

func paymentOrderFulfillmentStatusPredicate(status string) predicate.PaymentOrder {
	switch status {
	case InvoiceFulfillmentStatusFulfilled:
		return paymentorder.CompletedAtNotNil()
	case InvoiceFulfillmentStatusNotStarted:
		return paymentorder.PaidAtIsNil()
	case InvoiceFulfillmentStatusFailed:
		return paymentorder.And(paymentorder.PaidAtNotNil(), paymentorder.CompletedAtIsNil(), paymentorder.StatusEQ(OrderStatusFailed))
	case RefundEntitlementStatusManualReview:
		// Keep the former fulfillment query value as a read-only compatibility
		// alias. New presentations expose this state separately from delivery.
		return paymentorder.And(paymentorder.PaidAtNotNil(), paymentorder.CompletedAtIsNil(), paymentOrderHasUnifiedRefundReview())
	case InvoiceFulfillmentStatusPending:
		return paymentorder.And(
			paymentorder.PaidAtNotNil(),
			paymentorder.CompletedAtIsNil(),
			paymentorder.StatusNEQ(OrderStatusFailed),
		)
	default:
		return nil
	}
}

func exactPaymentOrderIDKeyword(keyword string) (int64, bool) {
	if !strings.HasPrefix(keyword, "#") || len(keyword) == 1 {
		return 0, false
	}
	for _, r := range keyword[1:] {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(keyword[1:], 10, 64)
	return id, err == nil && id > 0
}
