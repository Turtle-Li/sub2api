package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

const (
	refundReviewKindBalance             = "balance"
	refundReviewKindSubscription        = "subscription"
	refundReasonCodeCustomerRequest     = "customer_request"
	refundReasonCodeDuplicateCharge     = "duplicate_charge"
	refundReasonCodeServiceNotDelivered = "service_not_delivered"
	refundReasonCodeServiceError        = "service_error"
	refundReasonCodeOther               = "other"
	refundReasonSummaryMaximumRunes     = 240
	// Subscription effects are frozen within a short review window so the
	// administrator can submit exactly the seconds and expiry they inspected.
	// Crossing the boundary changes the revision and requires a fresh review.
	refundReviewValuationWindow = time.Minute
)

// RefundReasonInput keeps the public admin request compatible with older
// clients while giving new clients the gateway's structured reason contract.
// LegacyReason is used only when a reason_code is not supplied.
type RefundReasonInput struct {
	Code         string
	Detail       string
	LegacyReason string
}

type normalizedRefundReason struct {
	Code      string
	Summary   string
	AuditText string
}

func normalizeRefundReason(input RefundReasonInput) (normalizedRefundReason, error) {
	code := strings.TrimSpace(input.Code)
	detailSource := input.Detail
	if strings.TrimSpace(detailSource) == "" && strings.TrimSpace(input.LegacyReason) != "" {
		detailSource = input.LegacyReason
	}
	if code == "" {
		if strings.TrimSpace(detailSource) == "" {
			return normalizedRefundReason{}, infraerrors.BadRequest("INVALID_REFUND_REASON_CODE", "refund reason code is required")
		}
		code = refundReasonCodeOther
	}
	if !isRefundReasonCode(code) {
		return normalizedRefundReason{}, infraerrors.BadRequest("INVALID_REFUND_REASON_CODE", "refund reason code is invalid")
	}
	detail, err := normalizeRefundReasonDetail(detailSource)
	if err != nil {
		return normalizedRefundReason{}, err
	}
	if code == refundReasonCodeOther && detail == "" {
		return normalizedRefundReason{}, infraerrors.BadRequest("REFUND_REASON_DETAIL_REQUIRED", "a refund reason detail is required when reason code is other")
	}

	summary := refundReasonDefaultSummary(code)
	if detail != "" {
		summary = detail
	}
	return normalizedRefundReason{
		Code:      code,
		Summary:   summary,
		AuditText: code + ": " + summary,
	}, nil
}

func isRefundReasonCode(code string) bool {
	switch code {
	case refundReasonCodeCustomerRequest, refundReasonCodeDuplicateCharge,
		refundReasonCodeServiceNotDelivered, refundReasonCodeServiceError,
		refundReasonCodeOther:
		return true
	default:
		return false
	}
}

func normalizeRefundReasonDetail(value string) (string, error) {
	if !utf8.ValidString(value) || strings.Contains(value, "\x00") {
		return "", infraerrors.BadRequest("INVALID_REFUND_REASON_DETAIL", "refund reason detail is invalid")
	}
	normalized := strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(normalized) > refundReasonSummaryMaximumRunes {
		return "", infraerrors.BadRequest("INVALID_REFUND_REASON_DETAIL", "refund reason detail exceeds the gateway limit")
	}
	return normalized, nil
}

func refundReasonDefaultSummary(code string) string {
	switch code {
	case refundReasonCodeCustomerRequest:
		return "Customer requested a refund"
	case refundReasonCodeDuplicateCharge:
		return "Duplicate charge"
	case refundReasonCodeServiceNotDelivered:
		return "Service not delivered"
	case refundReasonCodeServiceError:
		return "Service error"
	default:
		return ""
	}
}

// RefundReview is the server-authoritative view rendered before an admin can
// initiate a refund. Amounts at the payment boundary are in the receipt
// currency; wallet credit and subscription time are reported separately.
type RefundReview struct {
	subscriptionInputs   *subscriptionRefundInputs
	OrderID              int64                          `json:"order_id"`
	OrderType            string                         `json:"order_type"`
	Currency             string                         `json:"currency"`
	CanRefund            bool                           `json:"can_refund"`
	RequiresManualReview bool                           `json:"requires_manual_review"`
	ReasonCode           string                         `json:"reason_code,omitempty"`
	Reason               string                         `json:"reason,omitempty"`
	QuoteRevision        string                         `json:"quote_revision,omitempty"`
	GeneratedAt          time.Time                      `json:"generated_at"`
	DefaultRefundAmount  float64                        `json:"default_refund_amount"`
	MinRefundAmount      float64                        `json:"min_refund_amount,omitempty"`
	MaxRefundAmount      float64                        `json:"max_refund_amount"`
	EntitlementAmount    float64                        `json:"entitlement_amount"`
	Balance              *BalanceRefundReview           `json:"balance,omitempty"`
	Subscription         *SubscriptionRefundReview      `json:"subscription,omitempty"`
	SubscriptionBackfill *SubscriptionGrantBackfillHint `json:"subscription_backfill,omitempty"`
}

type BalanceRefundReview struct {
	OriginalPaidCredit  float64 `json:"original_paid_credit"`
	OriginalGiftCredit  float64 `json:"original_gift_credit"`
	RemainingPaidCredit float64 `json:"remaining_paid_credit"`
	AvailableBalance    float64 `json:"available_balance"`
	AvailablePaidCredit float64 `json:"available_paid_credit"`
	AvailableGiftCredit float64 `json:"available_gift_credit"`
	PaidCreditToReclaim float64 `json:"paid_credit_to_reclaim"`
	GiftCreditToReclaim float64 `json:"gift_credit_to_reclaim"`
}

type SubscriptionRefundReview struct {
	SubscriptionID   int64     `json:"subscription_id"`
	TermStartAt      time.Time `json:"term_start_at"`
	TermEndAt        time.Time `json:"term_end_at"`
	CurrentExpiresAt time.Time `json:"current_expires_at"`
	NewExpiresAt     time.Time `json:"new_expires_at"`
	PurchasedSeconds int64     `json:"purchased_seconds"`
	UsedSeconds      int64     `json:"used_seconds"`
	RemainingSeconds int64     `json:"remaining_seconds"`
	SecondsToReclaim int64     `json:"seconds_to_reclaim"`
}

type walletRefundInputs struct {
	OriginalPaid, OriginalGift                decimal.Decimal
	RefundedPaid, ReclaimedGift               decimal.Decimal
	ReservedPaid, ReservedGift                decimal.Decimal
	CashPaidMinor, RefundedCash, ReservedCash int64
	AvailableBalance, AvailablePaid           decimal.Decimal
}

type walletRefundQuote struct {
	Principal, GiftReclaimed, Entitlement decimal.Decimal
	CashMinor                             int64
}

func floorDecimal(value decimal.Decimal, places int32) decimal.Decimal {
	return value.Truncate(places)
}

func minDecimal(values ...decimal.Decimal) decimal.Decimal {
	if len(values) == 0 {
		return decimal.Zero
	}
	out := values[0]
	for _, value := range values[1:] {
		if value.LessThan(out) {
			out = value
		}
	}
	return out
}

func nonNegativeDecimal(value decimal.Decimal) decimal.Decimal {
	if value.IsNegative() {
		return decimal.Zero
	}
	return value
}

// calculateWalletRefundQuote applies the account-level paid-first policy. The
// order's gift is reclaimed in the same proportion as its refundable paid
// principal, but never contributes to the cash returned by the provider.
func calculateWalletRefundQuote(in walletRefundInputs) walletRefundQuote {
	if !in.OriginalPaid.IsPositive() || in.CashPaidMinor <= 0 {
		return walletRefundQuote{}
	}
	remainingPaid := nonNegativeDecimal(in.OriginalPaid.Sub(in.RefundedPaid).Sub(in.ReservedPaid))
	availablePaid := nonNegativeDecimal(in.AvailablePaid)
	availableBalance := nonNegativeDecimal(in.AvailableBalance)
	availableGift := nonNegativeDecimal(availableBalance.Sub(availablePaid))
	principal := floorDecimal(minDecimal(remainingPaid, availablePaid), 2)
	if !principal.IsPositive() {
		return walletRefundQuote{}
	}

	cumulativePaid := in.RefundedPaid.Add(in.ReservedPaid).Add(principal)
	giftTarget := decimal.Zero
	if in.OriginalGift.IsPositive() {
		giftTarget = floorDecimal(in.OriginalGift.Mul(cumulativePaid).Div(in.OriginalPaid), 2)
	}
	giftDelta := nonNegativeDecimal(giftTarget.Sub(in.ReclaimedGift).Sub(in.ReservedGift))
	if giftDelta.GreaterThan(availableGift) {
		// A valid paid-first account normally has all proportional gift available
		// while principal remains. Fail closed to the largest provable fraction if
		// historical/manual adjustments made that invariant untrue.
		if in.OriginalGift.IsZero() {
			giftDelta = decimal.Zero
		} else {
			maxGiftTarget := in.ReclaimedGift.Add(in.ReservedGift).Add(availableGift)
			maxCumulativePaid := floorDecimal(maxGiftTarget.Mul(in.OriginalPaid).Div(in.OriginalGift), 2)
			principal = floorDecimal(nonNegativeDecimal(maxCumulativePaid.Sub(in.RefundedPaid).Sub(in.ReservedPaid)), 2)
			principal = minDecimal(principal, remainingPaid, availablePaid)
			cumulativePaid = in.RefundedPaid.Add(in.ReservedPaid).Add(principal)
			giftTarget = floorDecimal(in.OriginalGift.Mul(cumulativePaid).Div(in.OriginalPaid), 2)
			giftDelta = nonNegativeDecimal(giftTarget.Sub(in.ReclaimedGift).Sub(in.ReservedGift))
		}
	}
	if !principal.IsPositive() {
		return walletRefundQuote{}
	}

	cashTarget := decimal.NewFromInt(in.CashPaidMinor).
		Mul(cumulativePaid).
		Div(in.OriginalPaid).
		Floor().IntPart()
	cashDelta := cashTarget - in.RefundedCash - in.ReservedCash
	if cashDelta <= 0 {
		return walletRefundQuote{}
	}
	return walletRefundQuote{
		Principal:     principal,
		GiftReclaimed: giftDelta,
		Entitlement:   principal.Add(giftDelta),
		CashMinor:     cashDelta,
	}
}

type subscriptionRefundInputs struct {
	OrderAmount, PayAmount  decimal.Decimal
	SettledProductAmount    decimal.Decimal
	TermStart, OriginalEnd  time.Time
	CurrentEnd, ValuationAt time.Time
	RefundedSeconds         int64
	RefundedCashMinor       int64
}

type subscriptionRefundQuote struct {
	Seconds       int64
	ProductAmount decimal.Decimal
	CashMinor     int64
	NewExpiry     time.Time
}

func calculateSubscriptionRefundQuote(in subscriptionRefundInputs) subscriptionRefundQuote {
	if !in.OrderAmount.IsPositive() || !in.PayAmount.IsPositive() ||
		!in.OriginalEnd.After(in.TermStart) || in.CurrentEnd.After(in.OriginalEnd) {
		return subscriptionRefundQuote{}
	}
	newExpiry := in.ValuationAt
	if newExpiry.Before(in.TermStart) {
		newExpiry = in.TermStart
	}
	if !in.CurrentEnd.After(newExpiry) {
		return subscriptionRefundQuote{NewExpiry: in.CurrentEnd}
	}
	seconds := int64(in.CurrentEnd.Sub(newExpiry) / time.Second)
	totalSeconds := int64(in.OriginalEnd.Sub(in.TermStart) / time.Second)
	if seconds <= 0 || totalSeconds <= 0 {
		return subscriptionRefundQuote{NewExpiry: newExpiry}
	}
	cumulativeSeconds := in.RefundedSeconds + seconds
	if cumulativeSeconds > totalSeconds {
		return subscriptionRefundQuote{NewExpiry: newExpiry}
	}
	productTarget := floorDecimal(in.OrderAmount.
		Mul(decimal.NewFromInt(cumulativeSeconds)).
		Div(decimal.NewFromInt(totalSeconds)), 2)
	productDelta := nonNegativeDecimal(productTarget.Sub(in.SettledProductAmount))
	if !productDelta.IsPositive() {
		return subscriptionRefundQuote{NewExpiry: newExpiry}
	}
	cashTarget := decimal.NewFromInt(in.PayAmount.Mul(decimal.NewFromInt(100)).Round(0).IntPart()).
		Mul(decimal.NewFromInt(cumulativeSeconds)).
		Div(decimal.NewFromInt(totalSeconds)).
		Floor().IntPart()
	cashDelta := cashTarget - in.RefundedCashMinor
	if cashDelta <= 0 {
		return subscriptionRefundQuote{NewExpiry: newExpiry}
	}
	return subscriptionRefundQuote{
		Seconds: seconds, ProductAmount: productDelta, CashMinor: cashDelta, NewExpiry: newExpiry,
	}
}

type paymentWalletFunding struct {
	OrderID, UserID                                         int64
	Paid, Gift                                              decimal.Decimal
	CashPaidMinor                                           int64
	Currency                                                string
	RefundedPaid, ReclaimedGift, ReservedPaid, ReservedGift decimal.Decimal
	RefundedCashMinor, ReservedCashMinor                    int64
	Version                                                 int64
	UpdatedAt                                               time.Time
}

type paymentSubscriptionGrant struct {
	OrderID, SubscriptionID, UserID, GroupID int64
	TermStart, OriginalEnd, CurrentEnd       time.Time
	RefundedSeconds, ReservedSeconds         int64
	RefundedCashMinor, ReservedCashMinor     int64
	BalanceBonus                             decimal.Decimal
	ResetCardCount, ConcurrencyTarget        int
	Version                                  int64
}

func manualRefundReview(order *dbent.PaymentOrder, now time.Time, code, reason string) *RefundReview {
	review := &RefundReview{GeneratedAt: now, RequiresManualReview: true, ReasonCode: code, Reason: reason}
	if order != nil {
		review.OrderID = order.ID
		review.OrderType = order.OrderType
		review.Currency = PaymentOrderCurrency(order)
	}
	return review
}

func refundAlreadySettledReview(order *dbent.PaymentOrder, now time.Time) *RefundReview {
	review := &RefundReview{
		GeneratedAt: now,
		ReasonCode:  "REFUND_ALREADY_SETTLED",
		Reason:      "the order already has a successful refund",
	}
	if order != nil {
		review.OrderID = order.ID
		review.OrderType = order.OrderType
		review.Currency = PaymentOrderCurrency(order)
	}
	return review
}

func refundReviewRevision(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

func decimalString(value float64) string {
	return decimal.NewFromFloat(value).StringFixed(8)
}

func refundReviewStateAllowed(order *dbent.PaymentOrder) bool {
	return order != nil && psSliceContains([]string{
		OrderStatusCompleted, OrderStatusRefundRequested, OrderStatusRefundFailed,
	}, order.Status)
}

func paymentOrderHasAppliedAffiliateRebate(ctx context.Context, client *dbent.Client, orderID int64) (bool, error) {
	if client == nil || orderID <= 0 {
		return false, nil
	}
	return client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
		paymentauditlog.ActionEQ("AFFILIATE_REBATE_APPLIED"),
	).Limit(1).Exist(ctx)
}

func (s *PaymentService) ReviewRefund(ctx context.Context, orderID int64) (*RefundReview, error) {
	return s.ReviewRefundWithAmount(ctx, orderID, nil)
}

// ensureReviewedSubscriptionRefundAuthorizationCaches invalidates the shared
// subscription authorization snapshot before a reviewed refund may ask the
// payment provider to move money. The entitlement reservation is already
// durable at this point, so any error must leave the attempt pending for the
// reconciliation worker to retry.
func (s *PaymentService) ensureReviewedSubscriptionRefundAuthorizationCaches(ctx context.Context, orderID int64) error {
	if s == nil || s.entClient == nil || s.subscriptionSvc == nil || orderID <= 0 {
		return ErrSubscriptionCacheInvalidationUnavailable
	}
	grant, _, err := loadPaymentSubscriptionRefundState(ctx, s.entClient, orderID, false)
	if err != nil {
		return fmt.Errorf("load subscription refund cache target: %w", err)
	}
	if err := s.subscriptionSvc.EnsureSubscriptionAuthorizationCachesInvalidated(ctx, grant.UserID, grant.GroupID); err != nil {
		return err
	}
	// API-key auth snapshots do not contain the subscription term, but clearing
	// them after the strict subscription fence also refreshes related account
	// presentation such as concurrency without weakening the provider boundary.
	s.invalidatePaymentAuthCache(ctx, grant.UserID)
	return nil
}

// ensureReviewedBalanceRefundAuthorizationCache advances the shared wallet
// generation before a reviewed balance refund may create or query a provider
// refund. The durable reservation already moved the entitlement into frozen
// balance, so an unavailable fence must leave the same attempt pending for
// reconciliation rather than moving money with a stale authorization cache.
func (s *PaymentService) ensureReviewedBalanceRefundAuthorizationCache(ctx context.Context, userID int64) error {
	if s == nil || s.balanceAuthorizationCache == nil || userID <= 0 {
		return ErrBalanceCacheInvalidationUnavailable
	}
	return s.balanceAuthorizationCache.EnsureBalanceAuthorizationCacheInvalidated(ctx, userID)
}

// invalidateReviewedBalanceRefundAuthorizationCache synchronously tries the
// shared fence after a reservation, capture, or release commits. It is best
// effort because the durable state change has already committed; advance still
// requires the strict provider boundary before it can move money. Once a
// provider request has reached a terminal observation, that strict boundary
// means a Redis outage here cannot retain a pre-reservation high balance; at
// worst it leaves the post-reservation low value until a later refresh.
func (s *PaymentService) invalidateReviewedBalanceRefundAuthorizationCache(ctx context.Context, orderID, userID int64) {
	if s == nil || userID <= 0 {
		return
	}
	cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.ensureReviewedBalanceRefundAuthorizationCache(cacheCtx, userID); err != nil {
		slog.Warn("invalidate reviewed balance refund authorization cache failed", "orderID", orderID, "userID", userID, "err", err)
	}
}

// invalidateReviewedSubscriptionRefundCaches is the best-effort terminal
// repair path after a provider observation has been committed. The strict
// pre-provider boundary above owns the money-moving safety decision.
func (s *PaymentService) invalidateReviewedSubscriptionRefundCaches(ctx context.Context, orderID int64) {
	if s == nil || s.entClient == nil || orderID <= 0 {
		return
	}
	cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	grant, _, err := loadPaymentSubscriptionRefundState(cacheCtx, s.entClient, orderID, false)
	if err != nil {
		slog.Warn("load subscription refund cache target failed", "orderID", orderID, "err", err)
		return
	}
	if s.subscriptionSvc != nil {
		if err := s.subscriptionSvc.InvalidateSubscriptionCaches(cacheCtx, grant.UserID, grant.GroupID); err != nil {
			slog.Warn("invalidate subscription refund caches failed", "orderID", orderID, "userID", grant.UserID, "groupID", grant.GroupID, "err", err)
		}
	}
	s.invalidatePaymentAuthCache(cacheCtx, grant.UserID)
}

func (s *PaymentService) refundValuationTime() time.Time {
	now := time.Now()
	if s != nil && s.refundReviewNow != nil {
		now = s.refundReviewNow()
	}
	return now.UTC().Truncate(refundReviewValuationWindow)
}

func (s *PaymentService) reviewRefundWithClient(ctx context.Context, client *dbent.Client, orderID int64, now time.Time, lock bool) (*RefundReview, error) {
	if client == nil {
		return nil, infraerrors.InternalServer("REFUND_REVIEW_UNAVAILABLE", "refund review storage is unavailable")
	}
	q := client.PaymentOrder.Query().Where(paymentorder.IDEQ(orderID))
	if lock && paymentAuditDialect(client) == "postgres" {
		q.ForUpdate()
	}
	order, err := q.Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
		}
		return nil, err
	}
	if err := ensureRefundInvoiceAllowed(ctx, client, order.ID); err != nil {
		if infraerrors.Reason(err) == "REFUND_INVOICED_ORDER" {
			return manualRefundReview(order, now, "REFUND_INVOICED_ORDER", "the order already has an issued invoice and cannot be refunded"), nil
		}
		return nil, err
	}
	if !refundStateValid(order) {
		return manualRefundReview(order, now, "INVALID_REFUND_STATE", "stored refund accounting is invalid"), nil
	}
	if refundAlreadySettled(order) {
		return refundAlreadySettledReview(order, now), nil
	}
	if !refundReviewStateAllowed(order) {
		return manualRefundReview(order, now, "INVALID_STATUS", "order status does not allow a new refund"), nil
	}
	rebateApplied, err := paymentOrderHasAppliedAffiliateRebate(ctx, client, order.ID)
	if err != nil {
		return nil, err
	}
	if rebateApplied {
		return manualRefundReview(order, now, "NON_REVERSIBLE_AFFILIATE_REBATE", "this order has an affiliate rebate that requires manual recovery"), nil
	}
	if manual, entitlementErr := paymentOrderRequiresManualRefund(order); entitlementErr != nil {
		return manualRefundReview(order, now, "INVALID_PRODUCT_SNAPSHOT", "payment order entitlement snapshot is invalid"), nil
	} else if manual {
		return manualRefundReview(order, now, "NON_REVERSIBLE_ENTITLEMENT", "this order contains benefits that require manual rollback"), nil
	}
	if paymentOrderUsesUnifiedPay(order) {
		if err := s.validateUnifiedRefundOrder(order); err != nil {
			return manualRefundReview(order, now, infraerrors.Reason(err), err.Error()), nil
		}
	} else {
		return manualRefundReview(order, now, "LEGACY_PROVIDER_REFUND", "this provider still uses the manual refund workflow"), nil
	}

	switch order.OrderType {
	case payment.OrderTypeBalance:
		return s.reviewBalanceRefund(ctx, client, order, now, lock)
	case payment.OrderTypeSubscription:
		return s.reviewSubscriptionRefund(ctx, client, order, now, lock)
	default:
		return manualRefundReview(order, now, "UNSUPPORTED_ORDER_TYPE", "this order type requires manual review"), nil
	}
}

func (s *PaymentService) reviewBalanceRefund(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, now time.Time, lock bool) (*RefundReview, error) {
	funding, user, err := loadPaymentWalletRefundState(ctx, client, order.ID, order.UserID, lock)
	if errors.Is(err, errRefundAccountingMissing) {
		return manualRefundReview(order, now, "LEGACY_BALANCE_UNATTRIBUTED", "paid and gifted balance cannot be proven for this historical order"), nil
	}
	if err != nil {
		return nil, err
	}
	quote := calculateWalletRefundQuote(walletRefundInputs{
		OriginalPaid: funding.Paid, OriginalGift: funding.Gift,
		RefundedPaid: funding.RefundedPaid, ReclaimedGift: funding.ReclaimedGift,
		ReservedPaid: funding.ReservedPaid, ReservedGift: funding.ReservedGift,
		CashPaidMinor: funding.CashPaidMinor, RefundedCash: funding.RefundedCashMinor,
		ReservedCash:     funding.ReservedCashMinor,
		AvailableBalance: decimal.NewFromFloat(user.Balance), AvailablePaid: decimal.NewFromFloat(user.WalletAvailablePaid),
	})
	availableGift := nonNegativeDecimal(decimal.NewFromFloat(user.Balance).Sub(decimal.NewFromFloat(user.WalletAvailablePaid)))
	remainingPaid := nonNegativeDecimal(funding.Paid.Sub(funding.RefundedPaid).Sub(funding.ReservedPaid))
	revision := refundReviewRevision(
		strconv.FormatInt(order.ID, 10), order.Status, decimalString(order.RefundAmount), decimalString(order.RefundRequestedAmount),
		order.UpdatedAt.UTC().Format(time.RFC3339Nano), strconv.FormatInt(user.WalletComponentVersion, 10),
		strconv.FormatInt(funding.Version, 10), funding.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	review := &RefundReview{
		OrderID: order.ID, OrderType: order.OrderType, Currency: funding.Currency,
		GeneratedAt: now, QuoteRevision: revision,
		DefaultRefundAmount: float64(quote.CashMinor) / 100,
		MaxRefundAmount:     float64(quote.CashMinor) / 100,
		EntitlementAmount:   quote.Entitlement.InexactFloat64(),
		Balance: &BalanceRefundReview{
			OriginalPaidCredit: funding.Paid.InexactFloat64(), OriginalGiftCredit: funding.Gift.InexactFloat64(),
			RemainingPaidCredit: remainingPaid.InexactFloat64(), AvailableBalance: user.Balance,
			AvailablePaidCredit: user.WalletAvailablePaid, AvailableGiftCredit: availableGift.InexactFloat64(),
			PaidCreditToReclaim: quote.Principal.InexactFloat64(), GiftCreditToReclaim: quote.GiftReclaimed.InexactFloat64(),
		},
	}
	if quote.CashMinor <= 0 || !quote.Entitlement.IsPositive() {
		review.ReasonCode = "PAID_BALANCE_CONSUMED"
		review.Reason = "the paid balance has already been consumed; gifted balance is not refundable"
		return review, nil
	}
	review.CanRefund = true
	return review, nil
}

func (s *PaymentService) reviewSubscriptionRefund(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, now time.Time, lock bool) (*RefundReview, error) {
	grant, sub, err := loadReviewedSubscriptionRefundState(ctx, client, order.ID, lock)
	if errors.Is(err, errRefundAccountingMissing) {
		return s.manualSubscriptionGrantBackfillReview(ctx, client, order, now)
	}
	if err != nil {
		return nil, err
	}
	if grant.BalanceBonus.IsPositive() || grant.ResetCardCount > 0 || grant.ConcurrencyTarget > 0 {
		return manualRefundReview(order, now, "NON_REVERSIBLE_ENTITLEMENT", "subscription extras require manual rollback"), nil
	}
	if sub.DeletedAt != nil || sub.Status != SubscriptionStatusActive || sub.GroupID != grant.GroupID || sub.UserID != grant.UserID {
		return manualRefundReview(order, now, "SUBSCRIPTION_CHANGED", "the subscription no longer matches the purchased term"), nil
	}
	otherReservation, err := subscriptionHasOtherRefundReservation(ctx, client, grant.SubscriptionID, order.ID)
	if err != nil {
		return nil, err
	}
	if otherReservation {
		return manualRefundReview(order, now, "SUBSCRIPTION_REFUND_IN_FLIGHT", "another order on this subscription has a refund in progress"), nil
	}
	if !sub.ExpiresAt.Equal(grant.CurrentEnd) || grant.ReservedSeconds > 0 || grant.ReservedCashMinor > 0 {
		return manualRefundReview(order, now, "SUBSCRIPTION_NOT_TAIL", "the purchased term is no longer the current refundable tail"), nil
	}
	settled, _ := refundOrderAmounts(order)
	inputs := subscriptionRefundInputs{
		OrderAmount: decimal.NewFromFloat(order.Amount), PayAmount: decimal.NewFromFloat(order.PayAmount),
		SettledProductAmount: decimal.NewFromFloat(settled), TermStart: grant.TermStart,
		OriginalEnd: grant.OriginalEnd, CurrentEnd: grant.CurrentEnd, ValuationAt: now,
		RefundedSeconds: grant.RefundedSeconds, RefundedCashMinor: grant.RefundedCashMinor,
	}
	quote := calculateSubscriptionRefundQuote(inputs)
	totalSeconds := int64(grant.OriginalEnd.Sub(grant.TermStart) / time.Second)
	usedSeconds := totalSeconds - grant.RefundedSeconds - quote.Seconds
	if usedSeconds < 0 {
		usedSeconds = 0
	}
	revision := refundReviewRevision(
		strconv.FormatInt(order.ID, 10), order.Status, decimalString(order.RefundAmount), decimalString(order.RefundRequestedAmount),
		order.UpdatedAt.UTC().Format(time.RFC3339Nano), strconv.FormatInt(grant.Version, 10),
		sub.ExpiresAt.UTC().Format(time.RFC3339Nano), sub.UpdatedAt.UTC().Format(time.RFC3339Nano),
		strconv.FormatInt(quote.CashMinor, 10), quote.ProductAmount.StringFixed(2),
		strconv.FormatInt(quote.Seconds, 10), quote.NewExpiry.UTC().Format(time.RFC3339Nano),
		now.UTC().Format(time.RFC3339Nano),
	)
	review := &RefundReview{
		OrderID: order.ID, OrderType: order.OrderType, Currency: PaymentOrderCurrency(order),
		subscriptionInputs: &inputs,
		GeneratedAt:        now, QuoteRevision: revision,
		DefaultRefundAmount: float64(quote.CashMinor) / 100,
		MaxRefundAmount:     float64(quote.CashMinor) / 100,
		EntitlementAmount:   quote.ProductAmount.InexactFloat64(),
		Subscription: &SubscriptionRefundReview{
			SubscriptionID: grant.SubscriptionID, TermStartAt: grant.TermStart, TermEndAt: grant.OriginalEnd,
			CurrentExpiresAt: sub.ExpiresAt, NewExpiresAt: quote.NewExpiry,
			PurchasedSeconds: totalSeconds, UsedSeconds: usedSeconds, RemainingSeconds: quote.Seconds, SecondsToReclaim: quote.Seconds,
		},
	}
	if quote.CashMinor <= 0 || quote.Seconds <= 0 || !quote.ProductAmount.IsPositive() {
		review.ReasonCode = "SUBSCRIPTION_FULLY_USED"
		review.Reason = "the subscription term has no refundable unused time"
		return review, nil
	}
	review.CanRefund = true
	return review, nil
}

func subscriptionHasOtherRefundReservation(ctx context.Context, client *dbent.Client, subscriptionID, orderID int64) (bool, error) {
	rows, err := client.QueryContext(ctx, `SELECT COUNT(*) FROM payment_subscription_grants
		WHERE subscription_id = $1 AND payment_order_id <> $2 AND reserved_seconds > 0`, subscriptionID, orderID)
	if err != nil {
		return false, fmt.Errorf("check other subscription refund reservation: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false, rows.Err()
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, rows.Err()
}

// PrepareReviewedRefund preserves the existing service seam for callers that
// still send only the legacy reason field.
func (s *PaymentService) PrepareReviewedRefund(ctx context.Context, orderID int64, quoteRevision, legacyReason string) (*RefundPlan, error) {
	return s.PrepareReviewedRefundRequest(ctx, orderID, quoteRevision, RefundReasonInput{LegacyReason: legacyReason})
}

func (s *PaymentService) PrepareReviewedRefundRequest(ctx context.Context, orderID int64, quoteRevision string, reasonInput RefundReasonInput) (*RefundPlan, error) {
	return s.PrepareReviewedRefundRequestWithAmount(ctx, orderID, quoteRevision, reasonInput, nil)
}

func (s *PaymentService) PrepareReviewedRefundRequestWithAmount(ctx context.Context, orderID int64, quoteRevision string, reasonInput RefundReasonInput, amount *string) (*RefundPlan, error) {
	var selected *int64
	if amount != nil {
		v, err := parseSelectedRefundAmount(*amount)
		if err != nil {
			return nil, err
		}
		selected = &v
	}
	review, err := s.ReviewRefund(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if review.RequiresManualReview {
		return nil, infraerrors.BadRequest("REFUND_REQUIRES_MANUAL_REVIEW", review.Reason)
	}
	if !review.CanRefund {
		return nil, infraerrors.Conflict(review.ReasonCode, review.Reason)
	}
	if strings.TrimSpace(quoteRevision) == "" || quoteRevision != selectedRefundRevision(review.QuoteRevision, selected) {
		return nil, infraerrors.Conflict("REFUND_QUOTE_STALE", "refund review changed; refresh before submitting")
	}
	if err := selectRefundReviewAmount(review, selected); err != nil {
		return nil, err
	}
	order, err := s.entClient.PaymentOrder.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	reason, err := normalizeRefundReason(reasonInput)
	if err != nil {
		return nil, err
	}
	plan := &RefundPlan{
		OrderID: orderID, Order: order, RefundAmount: review.EntitlementAmount,
		GatewayAmount: review.DefaultRefundAmount, Reason: reason.AuditText,
		ReasonCode: reason.Code, ReasonSummary: reason.Summary,
		DeductBalance: order.OrderType == payment.OrderTypeBalance,
		QuoteRevision: review.QuoteRevision, ReviewKind: review.OrderType,
	}
	plan.RequestedCashMinor = selected
	if review.Balance != nil {
		plan.DeductionType = payment.DeductionTypeBalance
		plan.BalanceToDeduct = review.EntitlementAmount
		plan.WalletPaidToReserve = review.Balance.PaidCreditToReclaim
		plan.WalletGiftToReserve = review.Balance.GiftCreditToReclaim
	}
	if review.Subscription != nil {
		plan.DeductionType = payment.DeductionTypeSubscription
		plan.SubscriptionID = review.Subscription.SubscriptionID
		plan.SubscriptionSecondsToReserve = review.Subscription.SecondsToReclaim
		plan.SubscriptionNewExpiry = review.Subscription.NewExpiresAt
	}
	return plan, nil
}

func finiteRefundReviewAmount(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}
