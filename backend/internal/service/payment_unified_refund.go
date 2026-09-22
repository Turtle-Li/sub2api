package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/google/uuid"
)

// ReviewedRefundRolloutLockID serializes rollout CAS with reviewed reservations.
// Acquire before order/entitlement locks in every reviewed reservation transaction.
const ReviewedRefundRolloutLockID int64 = 0x535542325246

const (
	unifiedRefundBalanceInsufficientProviderStatus = "HTTP_403_NOT_ENOUGH"
	unifiedRefundProviderRejectedFailureCode       = "refund_submit_provider_rejected"
	unifiedRefundManualExternalConfirmedStatus     = "MANUAL_EXTERNAL_CONFIRMED"
)

var externalRefundReferencePattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,160}$`)
var externalRefundPhonePattern = regexp.MustCompile(`(?:^|[^0-9])1[3-9][0-9]{9}(?:$|[^0-9])`)
var externalRefundResidentIDPattern = regexp.MustCompile(`(?:^|[^0-9])(?:[0-9]{15}|[0-9]{17}[0-9Xx])(?:$|[^0-9])`)
var externalRefundSensitiveDigitRunPattern = regexp.MustCompile(`(^|[^0-9])[0-9]{15,18}([0-9Xx])?([^0-9]|$)`)

// ExternalRefundConfirmationInput is the administrator-supplied evidence for
// a refund completed outside the provider API. Amount and refund identity are
// deliberately absent: they are loaded from the durable pending attempt.
type ExternalRefundConfirmationInput struct {
	MethodCode        string
	ExternalReference string
	RefundedAt        time.Time
	EvidenceDetail    string
}

func (s *PaymentService) requireReviewedRefundAdmission(ctx context.Context) error {
	if !runtimegate.SharedWorkAllowed() {
		return infraerrors.ServiceUnavailable("REFUND_ADMISSION_DRAINING", "reviewed refunds are paused while this application generation is draining")
	}
	if s == nil || s.configService == nil || !s.configService.IsReviewedRefundsEnabled(ctx) {
		return infraerrors.ServiceUnavailable("REVIEWED_REFUNDS_DISABLED", "reviewed refunds are not enabled for this application generation")
	}
	return nil
}

func (s *PaymentService) validateUnifiedRefundOrder(o *dbent.PaymentOrder) error {
	return s.validateUnifiedRefundOrderWithBenefitExtras(o, false)
}

// validateUnifiedRefundOrderWithBenefitExtras keeps the legacy blanket fence
// for every caller except the reviewed P19 path. The reviewed path performs
// the stronger source/card/concurrency proof before it reaches the provider.
func (s *PaymentService) validateUnifiedRefundOrderWithBenefitExtras(o *dbent.PaymentOrder, allowBenefitExtras bool) error {
	if !refundStateValid(o) {
		return infraerrors.BadRequest("INVALID_REFUND_STATE", "stored order refund amounts are invalid")
	}
	if s.unifiedPayment == nil || !s.unifiedPayment.Enabled() {
		return infraerrors.BadRequest("REFUND_UNAVAILABLE", "unified payment runtime is unavailable")
	}
	snapshot := psOrderProviderSnapshot(o)
	if snapshot == nil || snapshot.ProviderKey != payment.TypeUnifiedPay || snapshot.PaymentOrderID == "" || o.PaymentTradeNo == "" {
		return infraerrors.BadRequest("REFUND_UNAVAILABLE", "unified payment order binding is incomplete")
	}
	if _, err := uuid.Parse(snapshot.PaymentOrderID); err != nil {
		return infraerrors.BadRequest("REFUND_UNAVAILABLE", "unified payment order binding is invalid")
	}
	scope := s.unifiedPayment.ScopeMetadata()
	if snapshot.Environment != scope["environment"] || snapshot.OrganizationID != scope["organization_id"] ||
		snapshot.ProductID != scope["product_id"] || snapshot.AppID != scope["app_id"] {
		return infraerrors.BadRequest("REFUND_UNAVAILABLE", "unified payment runtime does not match the historical order")
	}
	if _, ok := unifiedpay.PaymentMethodForPaymentType(o.PaymentType); !ok || PaymentOrderCurrency(o) != payment.DefaultPaymentCurrency {
		return infraerrors.BadRequest("REFUND_UNAVAILABLE", "unified payment order method or currency is invalid")
	}
	if o.OrderType != payment.OrderTypeBalance && o.OrderType != payment.OrderTypeSubscription {
		return infraerrors.BadRequest("REFUND_REQUIRES_MANUAL_REVIEW", "this unified-payment order type requires manual review")
	}
	manual, err := paymentOrderRequiresManualRefund(o)
	if err != nil || (manual && (!allowBenefitExtras || !paymentRefundBenefitOrderTypeSupported(o) || !paymentRefundBenefitHasOnlyAutomaticExtras(o))) {
		return infraerrors.BadRequest("REFUND_REQUIRES_MANUAL_REVIEW", "order entitlements require manual refund review")
	}
	return nil
}

// Convert the legacy product decimal boundary once, then calculate proportional
// channel refunds exclusively with integers, including half-up rounding.
func unifiedRefundAmounts(o *dbent.PaymentOrder, amount float64) (balanceMinor, gatewayFen int64, err error) {
	return unifiedRefundAmountsAfterSettled(o, 0, amount)
}

// unifiedRefundAmountsAfterSettled converts one partial attempt to integer
// channel units by subtracting the already-settled cumulative target.  This
// avoids rounding each attempt independently and guarantees that the sum of
// channel refunds never exceeds the original paid amount.
func unifiedRefundAmountsAfterSettled(o *dbent.PaymentOrder, settled, amount float64) (balanceMinor, gatewayFen int64, err error) {
	if o == nil {
		return 0, 0, errors.New("invalid unified refund order")
	}
	toMinor := func(value float64) (int64, error) {
		return payment.AmountToMinorUnit(strconv.FormatFloat(value, 'f', -1, 64), payment.DefaultPaymentCurrency)
	}
	balanceMinor, err = toMinor(amount)
	if err != nil {
		return 0, 0, err
	}
	totalMinor, err := toMinor(o.Amount)
	if err != nil {
		return 0, 0, err
	}
	paidFen, err := toMinor(o.PayAmount)
	if err != nil {
		return 0, 0, err
	}
	settledMinor, err := toMinor(settled)
	if err != nil {
		return 0, 0, err
	}
	if balanceMinor <= 0 || totalMinor <= 0 || paidFen <= 0 || balanceMinor > totalMinor || settledMinor < 0 || settledMinor > totalMinor || balanceMinor+settledMinor > totalMinor {
		return 0, 0, errors.New("invalid unified refund amount")
	}
	roundedTarget := func(minor int64) int64 {
		numerator := new(big.Int).Mul(big.NewInt(paidFen), big.NewInt(minor))
		numerator.Add(numerator, big.NewInt(totalMinor/2))
		numerator.Quo(numerator, big.NewInt(totalMinor))
		if !numerator.IsInt64() {
			return 0
		}
		return numerator.Int64()
	}
	target := roundedTarget(settledMinor + balanceMinor)
	previous := roundedTarget(settledMinor)
	delta := target - previous
	if delta <= 0 || delta > paidFen || previous < 0 || target < previous {
		return 0, 0, errors.New("invalid unified gateway refund amount")
	}
	return balanceMinor, delta, nil
}

func (s *PaymentService) executeUnifiedRefund(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	a, err := s.reserveUnifiedRefundAttempt(ctx, p)
	if err != nil {
		return nil, err
	}
	return s.advanceUnifiedRefund(ctx, a)
}

func (s *PaymentService) reserveUnifiedRefundAttempt(ctx context.Context, p *RefundPlan) (*unifiedRefundAttempt, error) {
	reason, err := normalizedRefundReasonForPlan(p)
	if err != nil {
		return nil, err
	}
	// Keep the payment-order audit text and every later provider call derived
	// from the same normalized contract, including plans assembled by older
	// internal callers rather than the reviewed admin endpoint.
	p.ReasonCode = reason.Code
	p.ReasonSummary = reason.Summary
	p.Reason = reason.AuditText
	reviewed := p != nil && strings.TrimSpace(p.QuoteRevision) != ""
	if reviewed {
		if err := s.requireReviewedRefundAdmission(ctx); err != nil {
			return nil, err
		}
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	if reviewed && paymentAuditDialect(client) == "postgres" {
		if _, err := client.ExecContext(txCtx, "SELECT pg_advisory_xact_lock_shared($1)", ReviewedRefundRolloutLockID); err != nil {
			return nil, err
		}
	}
	o, err := lockUnifiedRefundOrder(txCtx, client, p.OrderID)
	if err != nil {
		return nil, err
	}
	if err := s.validateUnifiedRefundOrderWithBenefitExtras(o, reviewed); err != nil {
		return nil, err
	}
	manual, err := unifiedRefundOrderNeedsReview(txCtx, client, o.ID)
	if err != nil {
		return nil, err
	}
	if manual {
		return nil, infraerrors.Conflict("REFUND_REQUIRES_MANUAL_REVIEW", "a refund for this order requires manual review")
	}
	if o.OrderType == payment.OrderTypeSubscription && strings.TrimSpace(p.QuoteRevision) == "" {
		return nil, infraerrors.Conflict("REFUND_REVIEW_REQUIRED", "subscription refunds require a fresh server review")
	}
	if reviewed {
		// Re-read both rollout gates under the financial lock immediately before
		// entitlement reservation. A generation that starts draining between the
		// request and this boundary must not create new provider work.
		if err := s.requireReviewedRefundAdmission(txCtx); err != nil {
			return nil, err
		}
		a, err := s.reserveReviewedUnifiedRefundAttemptTx(txCtx, client, o, p, reason)
		if err != nil {
			if errors.Is(err, errRefundQuoteStale) {
				return nil, infraerrors.Conflict("REFUND_QUOTE_STALE", "refund review changed; refresh before submitting")
			}
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		switch a.RefundKind {
		case refundReviewKindSubscription:
			s.invalidateReviewedSubscriptionRefundCaches(ctx, a.OrderID)
		case refundReviewKindBalance:
			s.invalidateReviewedBalanceRefundAuthorizationCache(ctx, a.OrderID, o.UserID)
		}
		return a, nil
	}
	settled, requested := refundOrderAmounts(o)
	remaining := refundRemainingAmount(o, settled)
	zeroTolerance := paymentAmountZeroTolerance(PaymentOrderCurrency(o))
	if remaining <= zeroTolerance {
		return nil, infraerrors.Conflict("REFUND_ALREADY_SETTLED", "the order has no refundable amount remaining")
	}
	if p.RefundAmount <= zeroTolerance || p.RefundAmount-remaining > zeroTolerance {
		return nil, infraerrors.BadRequest("REFUND_AMOUNT_EXCEEDED", "refund amount exceeds the remaining refundable amount")
	}
	if o.Status == OrderStatusRefundPending && requested > zeroTolerance && math.Abs(p.RefundAmount-requested) >= zeroTolerance {
		return nil, infraerrors.Conflict("REFUND_IN_PROGRESS", "another refund request is already pending")
	}
	if math.Abs(p.RefundAmount-remaining) < zeroTolerance {
		p.RefundAmount = remaining
	}
	balanceMinor, gatewayFen, err := unifiedRefundAmountsAfterSettled(o, settled, p.RefundAmount)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "invalid unified refund amount")
	}
	if o.Status == OrderStatusRefundPending {
		a, err := loadUnifiedRefundAttempt(txCtx, client, o.ID, "")
		if err != nil {
			return nil, err
		}
		if a.Status != unifiedRefundPending || a.AmountFen != gatewayFen || a.BalanceAmountMinor != balanceMinor ||
			a.DeductBalance != p.DeductBalance || a.Force != p.Force {
			return nil, infraerrors.Conflict("REFUND_IN_PROGRESS", "another refund request is already pending")
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return a, nil
	}
	if refundAlreadySettled(o) {
		return nil, refundAlreadySettledError()
	}
	if !psSliceContains([]string{OrderStatusCompleted, OrderStatusRefundRequested, OrderStatusRefundFailed}, o.Status) {
		return nil, infraerrors.Conflict("CONFLICT", "order status does not allow another refund")
	}
	if err := ensureRefundInvoiceAllowed(txCtx, client, o.ID); err != nil {
		return nil, err
	}
	snapshot := psOrderProviderSnapshot(o)
	method, _ := unifiedpay.PaymentMethodForPaymentType(o.PaymentType)
	id := uuid.NewString()
	a := &unifiedRefundAttempt{
		ProductRefundNo: "sub2-refund-" + id, IdempotencyKey: "sub2:refund:" + id,
		OrderID: o.ID, PaymentOrderID: snapshot.PaymentOrderID, Environment: snapshot.Environment,
		OrganizationID: snapshot.OrganizationID, ProductID: snapshot.ProductID, AppID: snapshot.AppID,
		PaymentMethod: method, AmountFen: gatewayFen, BalanceAmountMinor: balanceMinor,
		DeductBalance: p.DeductBalance, Force: p.Force, ReasonCode: reason.Code, ReasonSummary: reason.Summary, Status: unifiedRefundPending,
	}
	if err := insertUnifiedRefundAttempt(txCtx, client, a); err != nil {
		return nil, err
	}
	if _, err := client.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRefundPending).
		SetRefundAmount(settled).SetRefundRequestedAmount(p.RefundAmount).
		SetRefundReason(p.Reason).SetForceRefund(p.Force).
		ClearRefundAt().ClearFailedAt().ClearFailedReason().Save(txCtx); err != nil {
		return nil, err
	}
	if err := writeUnifiedRefundAudit(txCtx, client, o.ID, "UNIFIED_REFUND_REQUESTED", map[string]any{
		"product_refund_no": a.ProductRefundNo, "amount_fen": a.AmountFen,
		"balance_amount_minor": a.BalanceAmountMinor, "deduct_balance": a.DeductBalance,
		"reason_code": a.ReasonCode, "reason_summary": a.ReasonSummary,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return a, nil
}

// reserveCancelledLateUnifiedRefundAttemptTx persists the one allowed refund
// path for money that arrives after an immediate local cancellation. It never
// changes the order back to a payable or fulfillable status and intentionally
// carries no balance, subscription, or benefit entitlement reservation.
func (s *PaymentService) reserveCancelledLateUnifiedRefundAttemptTx(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, actualPaid float64) (*unifiedRefundAttempt, error) {
	if err := s.validateCancelledLateUnifiedRefundOrder(order); err != nil {
		return nil, err
	}
	snapshot := psOrderProviderSnapshot(order)
	method, _ := unifiedpay.PaymentMethodForPaymentType(order.PaymentType)
	amountFen, err := payment.AmountToMinorUnit(strconv.FormatFloat(actualPaid, 'f', -1, 64), payment.DefaultPaymentCurrency)
	if err != nil || amountFen <= 0 {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "late unified payment amount is invalid")
	}
	expectedFen, err := payment.AmountToMinorUnit(strconv.FormatFloat(order.PayAmount, 'f', -1, 64), payment.DefaultPaymentCurrency)
	if err != nil || expectedFen != amountFen {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "late unified payment amount does not match the cancelled order")
	}
	productRefundNo := "sub2_cancel_" + strconv.FormatInt(order.ID, 10)
	existing, err := loadUnifiedRefundAttempt(ctx, client, order.ID, productRefundNo)
	if err == nil {
		if existing.PaymentOrderID != snapshot.PaymentOrderID || existing.AmountFen != amountFen ||
			existing.PaymentMethod != method || existing.RefundKind != cancelLatePaymentRefundKind ||
			existing.BalanceAmountMinor != 0 || existing.DeductBalance || existing.EntitlementReserved {
			existing.NeedsManualReview = true
			if saveErr := saveUnifiedRefundAttempt(ctx, client, existing); saveErr != nil {
				return nil, saveErr
			}
			if auditErr := writeUnifiedRefundAudit(ctx, client, order.ID, "UNIFIED_CANCEL_LATE_REFUND_CONFLICT", map[string]any{
				"product_refund_no": productRefundNo, "reason": "persisted_late_refund_contract_mismatch",
			}); auditErr != nil {
				return nil, auditErr
			}
			// Return the fenced attempt without an error so the enclosing paid
			// claim commits the evidence and provider callbacks can be safely ACKed.
			return existing, nil
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var otherRefundNo string
	rows, queryErr := client.QueryContext(ctx, `
		SELECT product_refund_no FROM unified_payment_refund_attempts
		WHERE order_id = $1 AND status = 'PENDING' AND product_refund_no <> $2
		ORDER BY created_at ASC, product_refund_no ASC LIMIT 1`, order.ID, productRefundNo)
	if queryErr != nil {
		return nil, queryErr
	}
	if rows.Next() {
		if err := rows.Scan(&otherRefundNo); err != nil {
			_ = rows.Close()
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if otherRefundNo != "" {
		other, loadErr := loadUnifiedRefundAttempt(ctx, client, order.ID, otherRefundNo)
		if loadErr != nil {
			return nil, loadErr
		}
		other.NeedsManualReview = true
		if saveErr := saveUnifiedRefundAttempt(ctx, client, other); saveErr != nil {
			return nil, saveErr
		}
		if auditErr := writeUnifiedRefundAudit(ctx, client, order.ID, "UNIFIED_CANCEL_LATE_REFUND_CONFLICT", map[string]any{
			"product_refund_no": otherRefundNo, "reason": "other_pending_refund_attempt",
		}); auditErr != nil {
			return nil, auditErr
		}
		return other, nil
	}
	manual, err := unifiedRefundOrderNeedsReview(ctx, client, order.ID)
	if err != nil {
		return nil, err
	}
	a := &unifiedRefundAttempt{
		ProductRefundNo:    productRefundNo,
		IdempotencyKey:     localCancellationDirectRefundIdempotencyKey(order.ID),
		OrderID:            order.ID,
		PaymentOrderID:     snapshot.PaymentOrderID,
		Environment:        snapshot.Environment,
		OrganizationID:     snapshot.OrganizationID,
		ProductID:          snapshot.ProductID,
		AppID:              snapshot.AppID,
		PaymentMethod:      method,
		AmountFen:          amountFen,
		BalanceAmountMinor: 0,
		DeductBalance:      false,
		Force:              false,
		ReasonCode:         "service_not_delivered",
		ReasonSummary:      "payment accepted after local cancellation",
		Status:             unifiedRefundPending,
		NeedsManualReview:  manual,
		RefundKind:         cancelLatePaymentRefundKind,
	}
	if err := insertUnifiedRefundAttempt(ctx, client, a); err != nil {
		return nil, err
	}
	if err := writeUnifiedRefundAudit(ctx, client, order.ID, "UNIFIED_CANCEL_LATE_REFUND_QUEUED", map[string]any{
		"product_refund_no":   a.ProductRefundNo,
		"payment_order_id":    a.PaymentOrderID,
		"amount_fen":          a.AmountFen,
		"reason_code":         a.ReasonCode,
		"needs_manual_review": a.NeedsManualReview,
	}); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *PaymentService) validateCancelledLateUnifiedRefundOrder(order *dbent.PaymentOrder) error {
	if order == nil || order.Status != OrderStatusCancelled || order.PaidAt == nil {
		return infraerrors.Conflict("INVALID_STATUS", "late unified refund requires a cancelled paid order")
	}
	if s == nil || s.unifiedPayment == nil || !s.unifiedPayment.Enabled() {
		return infraerrors.BadRequest("REFUND_UNAVAILABLE", "unified payment runtime is unavailable")
	}
	snapshot := psOrderProviderSnapshot(order)
	if snapshot == nil || snapshot.ProviderKey != payment.TypeUnifiedPay || snapshot.PaymentOrderID == "" {
		return infraerrors.BadRequest("REFUND_UNAVAILABLE", "unified payment order binding is incomplete")
	}
	if _, err := uuid.Parse(snapshot.PaymentOrderID); err != nil {
		return infraerrors.BadRequest("REFUND_UNAVAILABLE", "unified payment order binding is invalid")
	}
	scope := s.unifiedPayment.ScopeMetadata()
	if snapshot.Environment != scope["environment"] || snapshot.OrganizationID != scope["organization_id"] ||
		snapshot.ProductID != scope["product_id"] || snapshot.AppID != scope["app_id"] {
		return infraerrors.BadRequest("REFUND_UNAVAILABLE", "unified payment runtime does not match the historical order")
	}
	if _, ok := unifiedpay.PaymentMethodForPaymentType(order.PaymentType); !ok || PaymentOrderCurrency(order) != payment.DefaultPaymentCurrency {
		return infraerrors.BadRequest("REFUND_UNAVAILABLE", "unified payment order method or currency is invalid")
	}
	return nil
}

func isCancelledLateUnifiedRefund(a *unifiedRefundAttempt) bool {
	return a != nil && a.RefundKind == cancelLatePaymentRefundKind
}

func (s *PaymentService) reserveReviewedUnifiedRefundAttemptTx(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, plan *RefundPlan, reason normalizedRefundReason) (*unifiedRefundAttempt, error) {
	if refundAlreadySettled(order) {
		return nil, refundAlreadySettledError()
	}
	if !psSliceContains([]string{OrderStatusCompleted, OrderStatusRefundRequested, OrderStatusRefundFailed}, order.Status) {
		return nil, infraerrors.Conflict("CONFLICT", "order status does not allow another refund")
	}
	if err := ensureRefundInvoiceAllowed(ctx, client, order.ID); err != nil {
		return nil, err
	}
	now := s.refundValuationTime()
	review, err := s.reserveReviewedRefundEntitlement(ctx, client, order, plan, now)
	if err != nil {
		return nil, err
	}
	amountFen, err := payment.AmountToMinorUnit(strconv.FormatFloat(review.DefaultRefundAmount, 'f', 2, 64), review.Currency)
	if err != nil || amountFen <= 0 {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "reviewed cash refund amount is invalid")
	}
	entitlementMinor, err := payment.AmountToMinorUnit(strconv.FormatFloat(review.EntitlementAmount, 'f', 2, 64), payment.DefaultPaymentCurrency)
	if err != nil || entitlementMinor <= 0 {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "reviewed entitlement refund amount is invalid")
	}
	snapshot := psOrderProviderSnapshot(order)
	method, _ := unifiedpay.PaymentMethodForPaymentType(order.PaymentType)
	id := uuid.NewString()
	valuationAt := now
	grantOrderID := int64(0)
	if review.Subscription != nil {
		valuationAt = review.Subscription.NewExpiresAt.UTC()
		grantOrderID = order.ID
	}
	a := &unifiedRefundAttempt{
		ProductRefundNo: "sub2-refund-" + id, IdempotencyKey: "sub2:refund:" + id,
		OrderID: order.ID, PaymentOrderID: snapshot.PaymentOrderID, Environment: snapshot.Environment,
		OrganizationID: snapshot.OrganizationID, ProductID: snapshot.ProductID, AppID: snapshot.AppID,
		PaymentMethod: method, AmountFen: amountFen, BalanceAmountMinor: entitlementMinor,
		DeductBalance: review.Balance != nil, Force: false, ReasonCode: reason.Code, ReasonSummary: reason.Summary,
		Status: unifiedRefundPending, RefundKind: review.OrderType, QuoteRevision: review.QuoteRevision,
		WalletPaidAmount: plan.WalletPaidToReserve, WalletGiftAmount: plan.WalletGiftToReserve,
		SubscriptionSeconds:      plan.SubscriptionSecondsToReserve,
		SubscriptionGrantOrderID: grantOrderID, EntitlementReserved: true, ValuationAt: &valuationAt,
	}
	if err := insertUnifiedRefundAttempt(ctx, client, a); err != nil {
		return nil, err
	}
	// The base wallet/subscription reserve already holds the historical lock
	// prefix. Reserve reset-card and concurrency evidence only after the durable
	// attempt exists so its source lifecycle is tied to this exact provider id.
	if err := reserveReviewedRefundBenefits(ctx, client, s.concurrencyAuthorizationFence, order, review, a); err != nil {
		return nil, err
	}
	settled, _ := refundOrderAmounts(order)
	if _, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefundPending).
		SetRefundAmount(settled).SetRefundRequestedAmount(review.EntitlementAmount).
		SetRefundReason(plan.Reason).SetForceRefund(false).
		ClearRefundAt().ClearFailedAt().ClearFailedReason().Save(ctx); err != nil {
		return nil, err
	}
	if err := writeUnifiedRefundAudit(ctx, client, order.ID, "UNIFIED_REFUND_REQUESTED", map[string]any{
		"product_refund_no": a.ProductRefundNo, "amount_fen": a.AmountFen,
		"entitlement_amount_minor": a.BalanceAmountMinor, "refund_kind": a.RefundKind,
		"wallet_paid_amount": a.WalletPaidAmount, "wallet_gift_amount": a.WalletGiftAmount,
		"subscription_seconds": a.SubscriptionSeconds, "quote_revision": a.QuoteRevision,
		"benefit_proof_digest": a.BenefitProofDigest,
		"reason_code":          a.ReasonCode, "reason_summary": a.ReasonSummary,
	}); err != nil {
		return nil, err
	}
	return a, nil
}

func pendingUnifiedRefundResult(manual bool) *RefundResult {
	warning := "unified payment refund is pending confirmation"
	if manual {
		warning = "unified payment refund requires manual review; automatic processing is paused"
	}
	return &RefundResult{Success: false, Warning: warning}
}

// pendingReviewedRefundForCacheBoundary records a stable audit marker without
// leaking Redis details. The reservation is durable, so callers retry this
// same attempt after the authorization fence converges.
func (s *PaymentService) pendingReviewedRefundForCacheBoundary(ctx context.Context, a *unifiedRefundAttempt) (*RefundResult, error) {
	auditCtx, auditCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	auditErr := writeUnifiedRefundAudit(auditCtx, s.entClient, a.OrderID, "UNIFIED_REFUND_CACHE_INVALIDATION_PENDING", map[string]any{
		"product_refund_no": a.ProductRefundNo,
		"code":              "cache_invalidation_unconfirmed",
	})
	auditCancel()
	if auditErr != nil {
		return nil, auditErr
	}
	return pendingUnifiedRefundResult(false), nil
}

func (s *PaymentService) queryUnifiedRefund(ctx context.Context, o *dbent.PaymentOrder) (*RefundResult, error) {
	a, err := loadUnifiedRefundAttempt(ctx, s.entClient, o.ID, "")
	if err != nil {
		return nil, err
	}
	return s.advanceUnifiedRefund(ctx, a)
}

// ResumeUnifiedRefund releases a provider-balance pause in the central money
// authority. The original refund request, provider number, amount, and
// entitlement reservation are reused; this method never creates a new refund.
func (s *PaymentService) ResumeUnifiedRefund(ctx context.Context, orderID, operatorID int64) (*RefundResult, error) {
	if !runtimegate.SharedWorkAllowed() {
		return nil, infraerrors.ServiceUnavailable("REFUND_ADMISSION_DRAINING", "refund recovery is paused while this application generation is draining")
	}
	if operatorID <= 0 {
		return nil, infraerrors.Forbidden("REFUND_RECOVERY_OPERATOR_REQUIRED", "a human administrator is required")
	}
	a, err := s.loadBalancePausedRefund(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if err := writeUnifiedRefundAudit(ctx, s.entClient, orderID, "UNIFIED_REFUND_RETRY_REQUESTED", map[string]any{
		"product_refund_no": a.ProductRefundNo,
		"refund_request_id": a.RefundRequestID,
		"operator_id":       operatorID,
		"reason_code":       "provider_balance_insufficient",
	}); err != nil {
		return nil, err
	}
	result, err := s.unifiedPayment.ResumeUnifiedRefund(ctx, payment.UnifiedRefundResumeRequest{
		RefundRequestID: a.RefundRequestID,
		IdempotencyKey:  unifiedRefundRecoveryIdempotencyKey("resume", a),
		OperatorRef:     "admin:" + strconv.FormatInt(operatorID, 10),
		Expected:        unifiedRefundExpectation(a),
	})
	if err != nil {
		if recovered, converged, recoveryErr := s.recoverBalancePausedUnifiedRefundAfterUnconfirmedCommand(ctx, a, "admin_resume_recovery_query"); recoveryErr != nil {
			return nil, recoveryErr
		} else if converged {
			return recovered, nil
		}
		if auditErr := writeUnifiedRefundAudit(ctx, s.entClient, orderID, "UNIFIED_REFUND_RETRY_UNCONFIRMED", map[string]any{
			"product_refund_no": a.ProductRefundNo,
			"refund_request_id": a.RefundRequestID,
			"operator_id":       operatorID,
			"code":              "central_result_unconfirmed",
		}); auditErr != nil {
			return nil, auditErr
		}
		return &RefundResult{Success: false, Warning: "refund retry request is unconfirmed; refresh the order before trying again"}, nil
	}
	return s.applyUnifiedRefundResource(ctx, orderID, result, "admin_resume")
}

// ConfirmExternalUnifiedRefund records an externally completed refund in the
// central money authority, then consumes its normal trusted success resource
// to reclaim the already-reserved entitlement exactly once.
func (s *PaymentService) ConfirmExternalUnifiedRefund(ctx context.Context, orderID, operatorID int64, input ExternalRefundConfirmationInput) (*RefundResult, error) {
	if !runtimegate.SharedWorkAllowed() {
		return nil, infraerrors.ServiceUnavailable("REFUND_ADMISSION_DRAINING", "refund recovery is paused while this application generation is draining")
	}
	if operatorID <= 0 {
		return nil, infraerrors.Forbidden("REFUND_RECOVERY_OPERATOR_REQUIRED", "a human administrator is required")
	}
	normalized, err := normalizeExternalRefundConfirmation(input)
	if err != nil {
		return nil, err
	}
	a, err := s.loadBalancePausedRefund(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if err := writeUnifiedRefundAudit(ctx, s.entClient, orderID, "UNIFIED_REFUND_EXTERNAL_CONFIRMATION_REQUESTED", map[string]any{
		"product_refund_no":  a.ProductRefundNo,
		"refund_request_id":  a.RefundRequestID,
		"operator_id":        operatorID,
		"method_code":        normalized.MethodCode,
		"external_reference": normalized.ExternalReference,
		"refunded_at":        normalized.RefundedAt,
		"evidence_detail":    normalized.EvidenceDetail,
	}); err != nil {
		return nil, err
	}
	result, err := s.unifiedPayment.ConfirmExternalUnifiedRefund(ctx, payment.UnifiedExternalRefundConfirmation{
		RefundRequestID:   a.RefundRequestID,
		IdempotencyKey:    unifiedRefundExternalConfirmationIdempotencyKey(a, normalized),
		OperatorRef:       "admin:" + strconv.FormatInt(operatorID, 10),
		MethodCode:        normalized.MethodCode,
		ExternalReference: normalized.ExternalReference,
		RefundedAt:        normalized.RefundedAt,
		EvidenceDetail:    normalized.EvidenceDetail,
		Expected:          unifiedRefundExpectation(a),
	})
	if err != nil {
		if recovered, converged, recoveryErr := s.recoverBalancePausedUnifiedRefundAfterUnconfirmedCommand(ctx, a, "admin_external_confirmation_recovery_query"); recoveryErr != nil {
			return nil, recoveryErr
		} else if converged {
			return recovered, nil
		}
		if auditErr := writeUnifiedRefundAudit(ctx, s.entClient, orderID, "UNIFIED_REFUND_EXTERNAL_CONFIRMATION_UNCONFIRMED", map[string]any{
			"product_refund_no": a.ProductRefundNo,
			"refund_request_id": a.RefundRequestID,
			"operator_id":       operatorID,
			"code":              "central_result_unconfirmed",
		}); auditErr != nil {
			return nil, auditErr
		}
		return &RefundResult{Success: false, Warning: "external refund confirmation is unconfirmed; refresh the order before trying again"}, nil
	}
	return s.applyUnifiedRefundResource(ctx, orderID, result, "admin_external_confirmation")
}

func (s *PaymentService) loadBalancePausedRefund(ctx context.Context, orderID int64) (*unifiedRefundAttempt, error) {
	if s == nil || s.entClient == nil {
		return nil, infraerrors.ServiceUnavailable("REFUND_RECOVERY_UNAVAILABLE", "refund recovery is unavailable")
	}
	o, err := s.entClient.PaymentOrder.Get(ctx, orderID)
	if dbent.IsNotFound(err) {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if err != nil {
		return nil, err
	}
	if s.unifiedPayment == nil || !s.unifiedPayment.Enabled() {
		return nil, infraerrors.ServiceUnavailable("REFUND_RECOVERY_UNAVAILABLE", "refund recovery is unavailable")
	}
	if o.Status != OrderStatusRefundPending {
		return nil, infraerrors.Conflict("REFUND_RECOVERY_STATE_INVALID", "only a pending refund can be recovered")
	}
	a, err := loadUnifiedRefundAttempt(ctx, s.entClient, orderID, "")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("REFUND_RECOVERY_NOT_FOUND", "pending unified refund attempt not found")
	}
	if err != nil {
		return nil, err
	}
	if !unifiedRefundBalanceRecoveryEligible(a) {
		return nil, infraerrors.Conflict("REFUND_RECOVERY_NOT_ALLOWED", "this refund is not paused for a proven provider balance shortage")
	}
	otherManual, err := unifiedRefundOrderHasOtherReview(ctx, s.entClient, orderID, a.ProductRefundNo)
	if err != nil {
		return nil, err
	}
	if otherManual {
		return nil, infraerrors.Conflict("REFUND_RECOVERY_CONFLICT", "another refund reconciliation conflict must be resolved first")
	}
	conflicted, err := unifiedRefundOrderHasRecoveryConflict(ctx, s.entClient, orderID)
	if err != nil {
		return nil, err
	}
	if conflicted {
		return nil, infraerrors.Conflict("REFUND_RECOVERY_CONFLICT", "a refund terminal conflict must be resolved first")
	}
	return a, nil
}

func unifiedRefundBalanceRecoveryEligible(a *unifiedRefundAttempt) bool {
	return a != nil && a.Status == unifiedRefundPending && a.NeedsManualReview && a.EntitlementReserved &&
		a.RefundRequestID != "" && a.ProviderRefundID == "" &&
		a.PaymentMethod == unifiedpay.PaymentMethodWechatPay && a.ProviderUpdatedAt != nil &&
		a.ProviderStatus == unifiedRefundBalanceInsufficientProviderStatus &&
		a.FailureCode == unifiedRefundProviderRejectedFailureCode
}

// recoverBalancePausedUnifiedRefundAfterUnconfirmedCommand is intentionally a
// read-after-write recovery seam for the two operator recovery commands only.
// A response loss or another operator's idempotency conflict cannot prove that
// central did not commit, but ordinary manual-review attempts must never become
// queryable just because a caller saw an error. Re-read the exact persisted
// attempt and its central request ID before issuing one scoped GET, then let
// the normal correlated observation/fence path decide whether it can settle.
func (s *PaymentService) recoverBalancePausedUnifiedRefundAfterUnconfirmedCommand(ctx context.Context, original *unifiedRefundAttempt, source string) (*RefundResult, bool, error) {
	if s == nil || s.entClient == nil || s.unifiedPayment == nil || !s.unifiedPayment.Enabled() ||
		original == nil || !unifiedRefundBalanceRecoveryEligible(original) {
		return nil, false, nil
	}
	// The recovery must still converge after the original handler context has
	// been cancelled by a lost response. It is bounded and performs no provider
	// mutation: the central request ID was persisted before either command.
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	o, err := s.entClient.PaymentOrder.Get(recoveryCtx, original.OrderID)
	if err != nil {
		return nil, false, err
	}
	if o.Status != OrderStatusRefundPending {
		return nil, false, nil
	}
	a, err := loadUnifiedRefundAttempt(recoveryCtx, s.entClient, original.OrderID, original.ProductRefundNo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil || !unifiedRefundBalanceRecoveryEligible(a) || a.RefundRequestID != original.RefundRequestID {
		return nil, false, err
	}
	otherManual, err := unifiedRefundOrderHasOtherReview(recoveryCtx, s.entClient, a.OrderID, a.ProductRefundNo)
	if err != nil {
		return nil, false, err
	}
	if otherManual {
		return nil, false, nil
	}
	conflicted, err := unifiedRefundOrderHasRecoveryConflict(recoveryCtx, s.entClient, a.OrderID)
	if err != nil {
		return nil, false, err
	}
	if conflicted {
		return nil, false, nil
	}
	resource, err := s.unifiedPayment.GetUnifiedRefund(recoveryCtx, a.RefundRequestID, unifiedRefundExpectation(a))
	if err != nil {
		// A failed GET is still unconfirmed. The caller retains the exact local
		// pause, audit, and reservation rather than interpreting it as a failed
		// central command.
		return nil, false, nil
	}
	result, err := s.applyUnifiedRefundResource(recoveryCtx, a.OrderID, resource, source)
	if err != nil {
		return nil, false, err
	}
	return result, true, nil
}

// A contradictory terminal resource remains a durable order-level fence. It
// cannot be safely distinguished from a later operator recovery by parsing
// append-only JSON evidence, so recovery remains fail-closed until that
// conflict is resolved through the existing review process.
func unifiedRefundOrderHasRecoveryConflict(ctx context.Context, client *dbent.Client, orderID int64) (bool, error) {
	rows, err := client.QueryContext(ctx, `SELECT COUNT(*) FROM unified_payment_refund_events
		WHERE order_id = $1 AND action = 'UNIFIED_REFUND_CONFLICT'`, orderID)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false, errors.New("refund recovery conflict state unavailable")
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, rows.Err()
}

func unifiedRefundExpectation(a *unifiedRefundAttempt) payment.UnifiedRefundExpectation {
	return payment.UnifiedRefundExpectation{
		PaymentOrderID:  a.PaymentOrderID,
		ProductRefundNo: a.ProductRefundNo,
		AmountFen:       a.AmountFen,
	}
}

func unifiedRefundRecoveryIdempotencyKey(action string, a *unifiedRefundAttempt) string {
	key := "sub2:refund:" + action + ":" + strings.TrimPrefix(a.ProductRefundNo, "sub2-refund-")
	// Each recovery action is one command for one proven balance-shortage
	// generation. If the request reaches another pause generation, the central
	// resource receives a later provider_updated_at and a new human action must
	// get a new key. Network retries for the same generation retain the key.
	if a.ProviderUpdatedAt != nil {
		key += ":" + strconv.FormatInt(a.ProviderUpdatedAt.UTC().UnixNano(), 36)
	}
	return key
}

func unifiedRefundExternalConfirmationIdempotencyKey(a *unifiedRefundAttempt, input ExternalRefundConfirmationInput) string {
	base := unifiedRefundRecoveryIdempotencyKey("external", a)
	canonical := input.MethodCode + "\x00" + input.ExternalReference + "\x00" +
		input.RefundedAt.UTC().Format(time.RFC3339Nano) + "\x00" + input.EvidenceDetail
	digest := sha256.Sum256([]byte(canonical))
	// A 128-bit suffix keeps the signed header below the central 128-character
	// limit while distinguishing a corrected confirmation from a cached 409.
	return base + ":" + hex.EncodeToString(digest[:16])
}

func normalizeExternalRefundConfirmation(input ExternalRefundConfirmationInput) (ExternalRefundConfirmationInput, error) {
	input.MethodCode = strings.TrimSpace(input.MethodCode)
	input.ExternalReference = strings.TrimSpace(input.ExternalReference)
	input.EvidenceDetail = strings.TrimSpace(input.EvidenceDetail)
	switch input.MethodCode {
	case "wechat_transfer", "original_channel_manual", "bank_transfer", "other":
	default:
		return input, infraerrors.BadRequest("EXTERNAL_REFUND_METHOD_INVALID", "external refund method is invalid")
	}
	if !externalRefundReferencePattern.MatchString(input.ExternalReference) {
		return input, infraerrors.BadRequest("EXTERNAL_REFUND_REFERENCE_INVALID", "external refund reference is invalid")
	}
	if !validExternalRefundEvidenceDetail(input.EvidenceDetail) {
		return input, infraerrors.BadRequest("EXTERNAL_REFUND_EVIDENCE_INVALID", "external refund evidence is invalid")
	}
	if input.RefundedAt.IsZero() || input.RefundedAt.Before(time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)) ||
		input.RefundedAt.After(time.Now().UTC().Add(5*time.Minute)) {
		return input, infraerrors.BadRequest("EXTERNAL_REFUND_TIME_INVALID", "external refund time is invalid")
	}
	input.RefundedAt = input.RefundedAt.UTC()
	return input, nil
}

func validExternalRefundEvidenceDetail(value string) bool {
	if !utf8.ValidString(value) || value == "" || utf8.RuneCountInString(value) > 240 ||
		strings.ContainsAny(value, "\x00\r\n{}[]\"<>") {
		return false
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"authorization", "cookie", "password", "passwd", "private_key", "private key",
		"secret", "access_token", "access token", "api_key", "api key", "bearer ", "-----begin",
		"身份证", "手机号", "密码", "私钥", "token",
	} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return !externalRefundPhonePattern.MatchString(value) &&
		!externalRefundResidentIDPattern.MatchString(value) &&
		!externalRefundSensitiveDigitRunPattern.MatchString(value)
}

func (s *PaymentService) advanceUnifiedRefund(ctx context.Context, a *unifiedRefundAttempt) (*RefundResult, error) {
	if a == nil {
		return nil, errors.New("unified refund attempt is missing")
	}
	// Recheck the durable review fence immediately before a network operation.
	manual, err := unifiedRefundOrderNeedsReview(ctx, s.entClient, a.OrderID)
	if err != nil {
		return nil, err
	}
	if manual || a.NeedsManualReview {
		return pendingUnifiedRefundResult(true), nil
	}
	o, err := s.entClient.PaymentOrder.Get(ctx, a.OrderID)
	if err != nil {
		return nil, err
	}
	if isCancelledLateUnifiedRefund(a) {
		err = s.validateCancelledLateUnifiedRefundOrder(o)
	} else {
		err = s.validateUnifiedRefundOrderWithBenefitExtras(o, strings.TrimSpace(a.BenefitProofDigest) != "")
	}
	if err != nil {
		return nil, err
	}
	if a.Status != unifiedRefundPending {
		return &RefundResult{Success: a.Status == unifiedpay.RefundStatusSucceeded}, nil
	}
	// The private compatibility gate protects provider creation as well as the
	// earlier reservation boundary. A standby recovery worker may run while an
	// older request-serving generation still exists, so a missing remote refund
	// ID must remain pending until the rollout is explicitly enabled. Once a
	// remote ID is known, read-only provider queries stay allowed while disabled
	// so already-moved money can converge to a durable terminal state.
	if a.RefundRequestID == "" && !isCancelledLateUnifiedRefund(a) && (a.EntitlementReserved || strings.TrimSpace(a.QuoteRevision) != "" || strings.TrimSpace(a.RefundKind) != "") {
		if s == nil || s.configService == nil || !s.configService.IsReviewedRefundsEnabled(ctx) {
			return nil, infraerrors.ServiceUnavailable("REVIEWED_REFUNDS_DISABLED", "reviewed refunds are not enabled for this application generation")
		}
	}
	if a.RefundKind == refundReviewKindSubscription {
		cacheCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		cacheErr := s.ensureReviewedSubscriptionRefundAuthorizationCaches(cacheCtx, a.OrderID)
		cancel()
		if cacheErr != nil {
			// Keep both the provider request and the reserved subscription term
			// pending. Durable reconciliation will retry this same boundary. The
			// audit deliberately records only a stable code, never a Redis error.
			return s.pendingReviewedRefundForCacheBoundary(ctx, a)
		}
	}
	if a.RefundKind == refundReviewKindBalance {
		cacheCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		cacheErr := s.ensureReviewedBalanceRefundAuthorizationCache(cacheCtx, o.UserID)
		cancel()
		if cacheErr != nil {
			// The wallet entitlement is already frozen. Do not create or query
			// a provider refund until the shared balance generation invalidates
			// every compatible cache value that could still authorize it.
			return s.pendingReviewedRefundForCacheBoundary(ctx, a)
		}
	}
	if strings.TrimSpace(a.BenefitProofDigest) != "" {
		cacheCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		cacheErr := s.ensureReviewedRefundBenefitConcurrencyAuthorizationFence(cacheCtx, a)
		cancel()
		if cacheErr != nil {
			// This is the strict P19 boundary: source state and its effective
			// cap are already durable, but no provider request may be created
			// while a stale user admission cache could still exceed that cap.
			return s.pendingReviewedRefundForCacheBoundary(ctx, a)
		}
	}
	var result *payment.UnifiedRefundResource
	if a.RefundRequestID == "" {
		result, err = s.unifiedPayment.CreateUnifiedRefund(ctx, payment.UnifiedRefundRequest{
			PaymentOrderID: a.PaymentOrderID, ProductRefundNo: a.ProductRefundNo, IdempotencyKey: a.IdempotencyKey,
			AmountFen: a.AmountFen, ReasonCode: a.ReasonCode, ReasonSummary: &a.ReasonSummary,
		})
	} else {
		result, err = s.unifiedPayment.GetUnifiedRefund(ctx, a.RefundRequestID, payment.UnifiedRefundExpectation{
			PaymentOrderID: a.PaymentOrderID, ProductRefundNo: a.ProductRefundNo, AmountFen: a.AmountFen,
		})
	}
	if err != nil {
		// HTTP failure, including a lost create response, cannot prove that money
		// was not refunded. A subsequent attempt reuses the exact persisted POST.
		if auditErr := writeUnifiedRefundAudit(ctx, s.entClient, a.OrderID, "UNIFIED_REFUND_UNCONFIRMED", map[string]any{
			"product_refund_no": a.ProductRefundNo, "code": "upstream_result_unconfirmed",
		}); auditErr != nil {
			return nil, auditErr
		}
		return pendingUnifiedRefundResult(false), nil
	}
	return s.applyUnifiedRefundResource(ctx, a.OrderID, result, "query")
}

func normalizedRefundReasonForPlan(plan *RefundPlan) (normalizedRefundReason, error) {
	if plan == nil {
		return normalizedRefundReason{}, infraerrors.BadRequest("INVALID_REFUND", "refund plan is missing")
	}
	if strings.TrimSpace(plan.ReasonCode) != "" || strings.TrimSpace(plan.ReasonSummary) != "" {
		return normalizeRefundReason(RefundReasonInput{Code: plan.ReasonCode, Detail: plan.ReasonSummary})
	}
	return normalizeRefundReason(RefundReasonInput{LegacyReason: plan.Reason})
}

func (s *PaymentService) applyUnifiedRefundResource(ctx context.Context, orderID int64, result *payment.UnifiedRefundResource, source string) (*RefundResult, error) {
	return s.applyUnifiedRefundObservation(ctx, orderID, result, source, nil)
}

func (s *PaymentService) applyUnifiedRefundObservation(ctx context.Context, orderID int64, result *payment.UnifiedRefundResource, source string, event *unifiedpay.WebhookEvent) (*RefundResult, error) {
	if result == nil {
		return nil, errors.New("unified refund result missing")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	o, err := lockUnifiedRefundOrder(txCtx, client, orderID)
	if err != nil {
		return nil, err
	}
	a, err := loadUnifiedRefundAttempt(txCtx, client, orderID, result.ProductRefundNo)
	if errors.Is(err, sql.ErrNoRows) {
		if err := writeUnifiedRefundAudit(txCtx, client, orderID, "UNIFIED_REFUND_UNCORRELATED", unifiedRefundEvidence(result, source, event)); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, permanentUnifiedWebhookError("refund_attempt_not_found")
	}
	if err != nil {
		return nil, err
	}
	otherManual, err := unifiedRefundOrderHasOtherReview(txCtx, client, orderID, a.ProductRefundNo)
	if err != nil {
		return nil, err
	}
	conflicted, err := unifiedRefundOrderHasRecoveryConflict(txCtx, client, orderID)
	if err != nil {
		return nil, err
	}
	trustedManualCompletion := result.Status == unifiedpay.RefundStatusSucceeded &&
		result.ProviderStatus != nil && *result.ProviderStatus == unifiedRefundManualExternalConfirmedStatus &&
		!result.NeedsManualReview
	trustedBalanceResume := a.NeedsManualReview && a.ProviderStatus == unifiedRefundBalanceInsufficientProviderStatus &&
		a.FailureCode == unifiedRefundProviderRejectedFailureCode && !result.NeedsManualReview
	if (trustedManualCompletion || trustedBalanceResume) && !otherManual && !conflicted {
		a.NeedsManualReview = false
	} else {
		a.NeedsManualReview = a.NeedsManualReview || otherManual || conflicted
	}
	conflict := unifiedRefundResultConflict(a, result)
	if conflict != "" {
		a.NeedsManualReview = true
		if err := saveUnifiedRefundAttempt(txCtx, client, a); err != nil {
			return nil, err
		}
		evidence := unifiedRefundEvidence(result, source, event)
		evidence["reason"], evidence["retained_status"] = conflict, a.Status
		if err := writeUnifiedRefundAudit(txCtx, client, orderID, "UNIFIED_REFUND_CONFLICT", evidence); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return pendingUnifiedRefundResult(true), nil
	}
	a.RefundRequestID, a.ChannelOutRefundNo = result.RefundRequestID, result.ChannelOutRefundNo
	if result.ProviderRefundID != nil {
		a.ProviderRefundID = *result.ProviderRefundID
	}
	if result.ProviderStatus != nil {
		a.ProviderStatus = *result.ProviderStatus
	} else {
		a.ProviderStatus = ""
	}
	if result.FailureCode != nil {
		a.FailureCode = *result.FailureCode
	} else {
		a.FailureCode = ""
	}
	if !result.UpdatedAt.IsZero() {
		updatedAt := result.UpdatedAt.UTC()
		a.ProviderUpdatedAt = &updatedAt
	}
	a.NeedsManualReview = a.NeedsManualReview || result.NeedsManualReview
	terminal := result.Status == unifiedpay.RefundStatusSucceeded || result.Status == unifiedpay.RefundStatusFailed
	previousStatus := a.Status
	if terminal {
		a.Status = result.Status
	}
	response := pendingUnifiedRefundResult(a.NeedsManualReview)
	if isCancelledLateUnifiedRefund(a) {
		if terminal && !a.NeedsManualReview {
			if o.Status != OrderStatusCancelled || o.PaidAt == nil {
				a.NeedsManualReview = true
				response = pendingUnifiedRefundResult(true)
			} else if result.Status == unifiedpay.RefundStatusSucceeded {
				// The original local cancellation is immutable. Channel money has
				// been returned, but no product entitlement or order lifecycle is
				// ever recreated from this late-payment refund.
				response = &RefundResult{Success: true}
				if err := writeUnifiedRefundAudit(txCtx, client, orderID, "UNIFIED_CANCEL_LATE_REFUND_SUCCEEDED", map[string]any{
					"product_refund_no": a.ProductRefundNo, "amount_fen": a.AmountFen,
				}); err != nil {
					return nil, err
				}
			} else {
				// A terminal failure after trusted accepted money must remain
				// operator-visible. Retrying a terminal direct/central failure
				// without fresh evidence could hide or duplicate a refund.
				a.NeedsManualReview = true
				response = pendingUnifiedRefundResult(true)
				if err := writeUnifiedRefundAudit(txCtx, client, orderID, "UNIFIED_CANCEL_LATE_REFUND_FAILED", map[string]any{
					"product_refund_no": a.ProductRefundNo, "amount_fen": a.AmountFen,
				}); err != nil {
					return nil, err
				}
			}
		} else if a.Status == unifiedpay.RefundStatusSucceeded && !a.NeedsManualReview {
			response = &RefundResult{Success: true}
		}
	} else if terminal && !a.NeedsManualReview && (previousStatus == unifiedRefundPending ||
		(result.Status == unifiedpay.RefundStatusSucceeded && a.EntitlementReserved)) {
		if o.Status != OrderStatusRefundPending {
			a.NeedsManualReview = true
			response = pendingUnifiedRefundResult(true)
		} else if result.Status == unifiedpay.RefundStatusSucceeded {
			amount := payment.MinorUnitToAmount(a.BalanceAmountMinor, payment.DefaultPaymentCurrency)
			settled, _ := refundOrderAmounts(o)
			plan := &RefundPlan{OrderID: o.ID, Order: o, RefundAmount: amount,
				SettledRefundAmount: settled, RemainingRefundable: refundRemainingAmount(o, settled),
				Reason: psStringValue(o.RefundReason), Force: a.Force, DeductBalance: a.DeductBalance,
				DeductionType: payment.DeductionTypeNone}
			if a.EntitlementReserved {
				plan.ReviewKind = a.RefundKind
				plan.WalletPaidToReserve = a.WalletPaidAmount
				plan.WalletGiftToReserve = a.WalletGiftAmount
				plan.SubscriptionSecondsToReserve = a.SubscriptionSeconds
				plan.BalanceToDeduct = a.WalletPaidAmount + a.WalletGiftAmount
				if err := finalizeReviewedRefundEntitlement(txCtx, client, o, a); err != nil {
					return nil, err
				}
				if err := captureReviewedRefundBenefits(txCtx, client, s.concurrencyAuthorizationFence, o, a); err != nil {
					return nil, err
				}
			} else if a.DeductBalance {
				plan.DeductionType, plan.BalanceToDeduct = payment.DeductionTypeBalance, amount
				if err := s.applyRefundFinalDeduction(txCtx, plan); err != nil {
					return nil, err
				}
			}
			response, err = s.markRefundOkTx(txCtx, client, plan)
			if err != nil {
				return nil, err
			}
			if a.DeductBalance && plan.BalanceToDeduct < amount {
				a.NeedsManualReview = true
				response.Warning = "refund succeeded; remaining balance recovery requires manual review"
			}
		} else {
			if a.EntitlementReserved {
				if releaseErr := releaseReviewedRefundEntitlement(txCtx, client, o, a); releaseErr != nil {
					a.NeedsManualReview = true
					response = pendingUnifiedRefundResult(true)
					if err := writeUnifiedRefundAudit(txCtx, client, orderID, "UNIFIED_REFUND_RELEASE_FAILED", map[string]any{
						"product_refund_no": a.ProductRefundNo, "reason": releaseErr.Error(),
					}); err != nil {
						return nil, err
					}
				} else if releaseErr := releaseReviewedRefundBenefits(txCtx, client, s.concurrencyAuthorizationFence, o, a); releaseErr != nil {
					a.NeedsManualReview = true
					response = pendingUnifiedRefundResult(true)
					if err := writeUnifiedRefundAudit(txCtx, client, orderID, "UNIFIED_REFUND_RELEASE_FAILED", map[string]any{
						"product_refund_no": a.ProductRefundNo, "reason": releaseErr.Error(),
					}); err != nil {
						return nil, err
					}
				} else if _, err := client.PaymentOrder.UpdateOneID(orderID).SetStatus(OrderStatusRefundFailed).
					SetFailedAt(time.Now()).SetFailedReason("unified payment refund failed").Save(txCtx); err != nil {
					return nil, err
				} else {
					response = &RefundResult{Success: false, Warning: "unified payment refund failed"}
				}
			} else {
				if _, err := client.PaymentOrder.UpdateOneID(orderID).SetStatus(OrderStatusRefundFailed).
					SetFailedAt(time.Now()).SetFailedReason("unified payment refund failed").Save(txCtx); err != nil {
					return nil, err
				}
				response = &RefundResult{Success: false, Warning: "unified payment refund failed"}
			}
		}
	} else if a.Status == unifiedpay.RefundStatusSucceeded && !a.NeedsManualReview {
		response = &RefundResult{Success: true}
	}
	if err := saveUnifiedRefundAttempt(txCtx, client, a); err != nil {
		return nil, err
	}
	evidence := unifiedRefundEvidence(result, source, event)
	evidence["retained_status"], evidence["needs_manual_review"] = a.Status, a.NeedsManualReview
	if err := writeUnifiedRefundAudit(txCtx, client, orderID, "UNIFIED_REFUND_RESULT", evidence); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.BenefitProofDigest) != "" {
		cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if syncErr := s.syncPaymentRefundBenefitConcurrencyAuthorizationFence(cacheCtx, a); syncErr != nil {
			slog.Warn("sync payment refund concurrency authorization fence failed", "orderID", a.OrderID, "err", syncErr)
		}
		cancel()
	}
	switch a.RefundKind {
	case refundReviewKindSubscription:
		s.invalidateReviewedSubscriptionRefundCaches(ctx, a.OrderID)
	case refundReviewKindBalance:
		s.invalidateReviewedBalanceRefundAuthorizationCache(ctx, a.OrderID, o.UserID)
	}
	return response, nil
}

func unifiedRefundResultConflict(a *unifiedRefundAttempt, result *payment.UnifiedRefundResource) string {
	if a.PaymentOrderID != result.PaymentOrderID || a.ProductRefundNo != result.ProductRefundNo ||
		a.AmountFen != result.AmountFen || a.PaymentMethod != result.PaymentMethod || result.Currency != payment.DefaultPaymentCurrency {
		return "refund_contract_mismatch"
	}
	if (a.RefundRequestID != "" && a.RefundRequestID != result.RefundRequestID) ||
		(a.ChannelOutRefundNo != "" && a.ChannelOutRefundNo != result.ChannelOutRefundNo) ||
		(a.ProviderRefundID != "" && result.ProviderRefundID != nil && a.ProviderRefundID != *result.ProviderRefundID) {
		return "refund_identity_mismatch"
	}
	if (a.Status == unifiedpay.RefundStatusSucceeded && result.Status == unifiedpay.RefundStatusFailed) ||
		(a.Status == unifiedpay.RefundStatusFailed && result.Status == unifiedpay.RefundStatusSucceeded) {
		return "refund_terminal_conflict"
	}
	return ""
}

func (s *PaymentService) applyUnifiedRefundEvent(ctx context.Context, o *dbent.PaymentOrder, event unifiedpay.WebhookEvent) error {
	if event.Refund == nil {
		return permanentUnifiedWebhookError("refund_resource_missing")
	}
	_, err := s.applyUnifiedRefundObservation(ctx, o.ID, unifiedRefundEventResource(event), "webhook", &event)
	return err
}

func unifiedRefundEventResource(event unifiedpay.WebhookEvent) *payment.UnifiedRefundResource {
	r := event.Refund
	if r == nil {
		return nil
	}
	return &payment.UnifiedRefundResource{
		RefundRequestID: r.RefundRequestID, PaymentOrderID: event.Resource.PaymentOrderID,
		ProductRefundNo: r.ProductRefundNo, ChannelOutRefundNo: r.ChannelOutRefundNo,
		AmountFen: r.AmountFen, Currency: event.Resource.Currency, PaymentMethod: r.PaymentMethod,
		Status: r.Status, ProviderRefundID: r.ProviderRefundID, ProviderStatus: r.ProviderStatus,
		FailureCode: r.FailureCode, CompletedAt: r.CompletedAt,
	}
}

// Only normalized contract fields enter the append-only evidence table. The
// inbox body hash can be joined by event_id; raw bodies and user data stay out.
func unifiedRefundEvidence(result *payment.UnifiedRefundResource, source string, event *unifiedpay.WebhookEvent) map[string]any {
	detail := map[string]any{"source": source}
	if result != nil {
		detail["payment_order_id"], detail["refund_request_id"] = result.PaymentOrderID, result.RefundRequestID
		detail["product_refund_no"], detail["channel_out_refund_no"] = result.ProductRefundNo, result.ChannelOutRefundNo
		detail["amount_fen"], detail["currency"], detail["payment_method"] = result.AmountFen, result.Currency, result.PaymentMethod
		detail["status"], detail["provider_refund_id"], detail["provider_status"] = result.Status, result.ProviderRefundID, result.ProviderStatus
		detail["failure_code"], detail["completed_at"] = result.FailureCode, result.CompletedAt
		if !result.CreatedAt.IsZero() {
			detail["created_at"] = result.CreatedAt
		}
		if !result.UpdatedAt.IsZero() {
			detail["updated_at"] = result.UpdatedAt
		}
	}
	if event != nil {
		detail["event_id"], detail["event_type"], detail["sequence"] = event.EventID, event.EventType, event.Sequence
		detail["occurred_at"], detail["origin_request_id"] = event.OccurredAt, event.OriginRequestID
		detail["environment"], detail["organization_id"], detail["product_id"], detail["app_id"] = event.Environment, event.OrganizationID, event.ProductID, event.AppID
		detail["product_order_no"], detail["payment_order_status"] = event.Resource.ProductOrderNo, event.Resource.Status
		detail["payment_order_amount_fen"], detail["paid_amount_fen"] = event.Resource.AmountFen, event.Resource.PaidAmountFen
		detail["channel_out_trade_no"] = event.Resource.ChannelOutTradeNo
	}
	return detail
}

// A signature-verified but contradictory refund must leave durable evidence
// and freeze subsequent refunds before the inbox can permanently ACK it.
func (s *PaymentService) rejectUnifiedRefundEvent(ctx context.Context, o *dbent.PaymentOrder, event unifiedpay.WebhookEvent, code string) error {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	if _, err := lockUnifiedRefundOrder(txCtx, client, o.ID); err != nil {
		return err
	}
	if event.Refund != nil {
		a, err := loadUnifiedRefundAttempt(txCtx, client, o.ID, event.Refund.ProductRefundNo)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			a.NeedsManualReview = true
			if err := saveUnifiedRefundAttempt(txCtx, client, a); err != nil {
				return err
			}
		}
	}
	evidence := unifiedRefundEvidence(unifiedRefundEventResource(event), "webhook", &event)
	evidence["reason"] = code
	if err := writeUnifiedRefundAudit(txCtx, client, o.ID, "UNIFIED_PAYMENT_EVENT_REJECTED", evidence); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return permanentUnifiedWebhookError(code)
}
