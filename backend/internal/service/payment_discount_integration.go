package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

const paymentDiscountManualReviewReason = "manual review required: paid coupon order has no available redemption capacity"

func paymentDiscountRequestBinding(req CreateOrderRequest) string {
	planID := req.PlanID
	if req.OrderType == payment.OrderTypeResetCard {
		planID = 0
	}
	data, _ := json.Marshal([]any{req.UserID, req.OrderType, planID, req.SubscriptionID, req.Amount, req.PaymentType, strings.ToUpper(strings.TrimSpace(req.CouponCode)), req.ResetCardTierRevision})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func normalizePaymentDiscountRequest(req *CreateOrderRequest) error {
	req.CouponCode = strings.ToUpper(strings.TrimSpace(req.CouponCode))
	if req.CouponCode == "" {
		return nil
	}
	if req.OrderType == payment.OrderTypeResetCard {
		return ErrPaymentDiscountInvalid
	}
	if len(req.CouponCode) > 32 || len(req.CouponRevision) != 64 {
		return infraerrors.BadRequest("COUPON_QUOTE_REQUIRED", "apply the coupon before confirming payment")
	}
	if raw := strings.TrimSpace(req.IdempotencyKey); raw != "" {
		key, err := NormalizeIdempotencyKey(raw)
		if err != nil {
			return err
		}
		h := sha256.Sum256([]byte(key))
		if req.IdempotencyKeyHash != "" && req.IdempotencyKeyHash != hex.EncodeToString(h[:]) {
			return ErrIdempotencyKeyConflict
		}
		req.IdempotencyKeyHash = hex.EncodeToString(h[:])
	}
	hash, err := normalizeResetCardIdempotencyKeyHash(req.IdempotencyKeyHash)
	if err != nil {
		return err
	}
	req.IdempotencyKeyHash = hash
	return nil
}

// QuotePaymentDiscount uses the same product, fee and currency rules as creation.
// The quote neither reserves capacity nor accepts a caller-authored price.
func (s *PaymentService) QuotePaymentDiscount(ctx context.Context, req CreateOrderRequest) (*PaymentDiscountQuote, error) {
	if req.OrderType == "" {
		req.OrderType = payment.OrderTypeBalance
	}
	if normalized := NormalizeVisibleMethod(req.PaymentType); normalized != "" {
		req.PaymentType = normalized
	}
	if req.OrderType != payment.OrderTypeBalance && req.OrderType != payment.OrderTypeSubscription && req.OrderType != payment.OrderTypeResetCard {
		return nil, infraerrors.BadRequest("INVALID_ORDER_TYPE", "unsupported order type")
	}
	if req.OrderType == payment.OrderTypeResetCard && strings.TrimSpace(req.CouponCode) != "" {
		return nil, ErrPaymentDiscountInvalid
	}
	if err := checkPaymentDiscountRate(ctx, s.entClient, req.UserID); err != nil {
		return nil, err
	}
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, infraerrors.Forbidden("PAYMENT_DISABLED", "payment system is disabled")
	}
	if req.OrderType == payment.OrderTypeSubscription && !cfg.SubscriptionEnabled {
		return nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "subscription purchasing is disabled")
	}
	actor, err := s.userRepo.GetByID(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if actor.Status != payment.EntityStatusActive {
		return nil, infraerrors.Forbidden("USER_INACTIVE", "user account is disabled")
	}
	if req.OrderType == payment.OrderTypeResetCard {
		if s.subscriptionSvc == nil {
			return nil, ErrResetCardPurchaseUnavailable
		}
		q, e := s.subscriptionSvc.GetResetCardQuote(ctx, req.UserID, req.SubscriptionID)
		if e != nil {
			return nil, e
		}
		if req.PlanID == 0 {
			req.PlanID = q.PlanID
		}
		if !isValidProviderAmount(req.Amount) || math.Abs(req.Amount-q.Price) >= paymentAmountZeroTolerance(payment.DefaultPaymentCurrency) {
			return nil, infraerrors.Conflict("RESET_CARD_QUOTE_CHANGED", "reset card quote changed; request a new quote")
		}
		if err := validateResetCardTierQuoteRevision(req.ResetCardTierRevision, q.ResetCardTier); err != nil {
			return nil, err
		}
		req.Amount = q.Price
	}
	plan, err := s.validateOrderInput(ctx, req, cfg)
	if err != nil {
		return nil, err
	}
	amount := req.Amount
	if plan != nil && req.OrderType == payment.OrderTypeSubscription {
		amount = plan.Price
	}
	currency, err := s.configService.ValidateMethodCurrencyConsistency(ctx, req.PaymentType)
	if err != nil {
		return nil, err
	}
	original, _, err := calculateCreateOrderPayAmountForOrderType(amount, cfg.RechargeFeeRate, currency, req.OrderType, cfg.SubscriptionUSDToCNYRate)
	if err != nil {
		return nil, err
	}
	originalDecimal, err := decimal.NewFromString(original)
	if err != nil {
		return nil, err
	}
	quote, err := quotePaymentDiscount(ctx, s.entClient, req.UserID, req.CouponCode, originalDecimal, currency, req.OrderType, req.PlanID, paymentDiscountRequestBinding(req), false)
	if err != nil {
		return nil, err
	}
	final, err := decimal.NewFromString(quote.PayAmount)
	if err != nil {
		return nil, err
	}
	sel, err := s.selectCreateOrderInstance(ctx, req, cfg, final.InexactFloat64())
	if err != nil {
		return nil, err
	}
	if sel != nil && paymentProviderConfigCurrency(sel.ProviderKey, sel.Config) != currency {
		return nil, infraerrors.Conflict("COUPON_QUOTE_CHANGED", "payment currency changed; request a new quote")
	}
	if err := validateSelectedCreateOrderAmountCurrency(quote.PayAmount, sel); err != nil {
		return nil, err
	}
	return quote, nil
}

func (s *PaymentService) replayPaymentDiscount(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, bool, error) {
	if req.CouponCode == "" {
		return nil, false, nil
	}
	o, found, err := findPaymentDiscountReplay(ctx, s.entClient, req.UserID, req.IdempotencyKeyHash, paymentDiscountRequestBinding(req))
	if err != nil || !found {
		return nil, found, err
	}
	if o.Status != OrderStatusPending || !o.ExpiresAt.After(time.Now()) {
		return buildResetCardOrderResponse(o), true, nil
	}
	resp, err := loadPaymentDiscountResponse(ctx, s.entClient, o.ID)
	if err != nil {
		return nil, true, err
	}
	if resp == nil {
		return nil, true, infraerrors.Conflict("PAYMENT_CONFIRMATION_PENDING", "the existing payment order is being confirmed; view your orders").WithMetadata(map[string]string{"order_id": fmt.Sprint(o.ID)})
	}
	return resp, true, nil
}

func paymentOrderHasDiscount(o *dbent.PaymentOrder) bool {
	if o == nil {
		return false
	}
	_, ok := o.ProductSnapshot["payment_discount"]
	return ok
}
func isPaymentDiscountManualReview(o *dbent.PaymentOrder) bool {
	return o != nil && o.FailedReason != nil && *o.FailedReason == paymentDiscountManualReviewReason
}

func applyPaymentDiscountSnapshot(o *dbent.PaymentOrder, quote *PaymentDiscountQuote) error {
	if o.ProductSnapshot == nil {
		o.ProductSnapshot = map[string]any{}
	}
	raw, err := json.Marshal(quote)
	if err != nil {
		return err
	}
	var snapshot map[string]any
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	o.ProductSnapshot["payment_discount"] = snapshot
	if o.OrderType == payment.OrderTypeBalance {
		original, err := decimal.NewFromString(quote.OriginalAmount)
		if err != nil || !original.IsPositive() {
			return fmt.Errorf("invalid coupon original amount")
		}
		final, err := decimal.NewFromString(quote.PayAmount)
		if err != nil {
			return err
		}
		previousPaid, ok := paymentSnapshotFloat(o.ProductSnapshot["paid_credit_amount"])
		if !ok {
			return fmt.Errorf("missing recharge paid credit snapshot")
		}
		paid := decimal.NewFromFloat(previousPaid).Mul(final).Div(original).Truncate(8)
		if !paid.IsPositive() {
			return infraerrors.BadRequest("COUPON_INVALID", "discount leaves no refundable paid principal")
		}
		gift := decimal.NewFromFloat(o.Amount).Sub(paid)
		snapshot["original_paid_credit_amount"] = previousPaid
		snapshot["paid_credit_amount"] = paid.InexactFloat64()
		snapshot["gift_credit_amount"] = gift.InexactFloat64()
		o.ProductSnapshot["paid_credit_amount"] = paid.InexactFloat64()
		o.ProductSnapshot["gift_credit_amount"] = gift.InexactFloat64()
	}
	return nil
}

// Record trusted payment even when a previously released coupon cannot be reclaimed.
// Such an order is held for operators, never silently fulfilled over the hard cap.
func (s *PaymentService) markDiscountOrderPaid(ctx context.Context, o *dbent.PaymentOrder, tradeNo string, paid float64) (bool, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	locked, err := lockUnifiedRefundOrder(txCtx, tx.Client(), o.ID)
	if err != nil {
		return false, err
	}
	if locked.PaidAt != nil {
		return false, nil
	}
	if psIsRefundStatus(locked.Status) {
		return false, infraerrors.Conflict("INVALID_STATUS", "refund order cannot be paid")
	}
	if !decimal.NewFromFloat(locked.PayAmount).Equal(decimal.NewFromFloat(paid)) {
		return false, fmt.Errorf("coupon payment amount mismatch")
	}
	allowed, err := consumePaymentDiscount(txCtx, tx.Client(), locked.ID)
	if err != nil {
		return false, err
	}
	now := time.Now()
	b := tx.PaymentOrder.UpdateOneID(locked.ID).SetPaidAt(now).SetPaymentTradeNo(tradeNo).SetPayAmount(paid)
	if allowed {
		b.SetStatus(OrderStatusPaid).ClearFailedAt().ClearFailedReason()
	} else {
		b.SetStatus(OrderStatusFailed).SetFailedAt(now).SetFailedReason(paymentDiscountManualReviewReason)
	}
	if _, err = b.Save(txCtx); err != nil {
		return false, err
	}
	action := "PAYMENT_COUPON_CONSUMED"
	if !allowed {
		action = "PAYMENT_COUPON_MANUAL_REVIEW"
	}
	if _, err = tx.PaymentAuditLog.Create().SetOrderID(fmt.Sprint(locked.ID)).SetAction(action).SetOperator("system").SetDetail(`{"trusted_payment":true}`).Save(txCtx); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return allowed, nil
}

func (s *PaymentService) cancelDiscountOrder(ctx context.Context, o *dbent.PaymentOrder, status string) (int, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	locked, err := lockUnifiedRefundOrder(txCtx, tx.Client(), o.ID)
	if err != nil {
		return 0, err
	}
	if locked.Status != OrderStatusPending || locked.PaidAt != nil {
		return 0, nil
	}
	if err = releasePaymentDiscount(txCtx, tx.Client(), locked.ID); err != nil {
		return 0, err
	}
	n, err := tx.PaymentOrder.Update().Where(paymentorder.IDEQ(locked.ID), paymentorder.StatusEQ(OrderStatusPending), paymentorder.PaidAtIsNil()).SetStatus(status).Save(txCtx)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}
