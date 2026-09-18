package service

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/shopspring/decimal"
)

var errRefundAccountingMissing = errors.New("refund accounting provenance is missing")

func parseRefundDecimal(raw string) (decimal.Decimal, error) {
	value, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return decimal.Zero, err
	}
	if value.IsNegative() {
		return decimal.Zero, errors.New("refund accounting amount is negative")
	}
	return value, nil
}

func loadPaymentWalletRefundState(ctx context.Context, client *dbent.Client, orderID, userID int64, lock bool) (*paymentWalletFunding, *dbent.User, error) {
	userQuery := client.User.Query().Where(user.IDEQ(userID), user.DeletedAtIsNil())
	if lock && paymentAuditDialect(client) == dialect.Postgres {
		userQuery.ForUpdate()
	}
	user, err := userQuery.Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil, errRefundAccountingMissing
		}
		return nil, nil, err
	}
	query := `SELECT CAST(paid_credit_amount AS TEXT), CAST(gift_credit_amount AS TEXT),
		cash_paid_minor, currency, CAST(refunded_paid_amount AS TEXT),
		CAST(reclaimed_gift_amount AS TEXT), refunded_cash_minor,
		CAST(reserved_paid_amount AS TEXT), CAST(reserved_gift_amount AS TEXT),
		reserved_cash_minor, version, updated_at
		FROM payment_wallet_fundings WHERE payment_order_id = $1 AND user_id = $2`
	if lock && paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, orderID, userID)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
		return nil, nil, errRefundAccountingMissing
	}
	var paid, gift, refundedPaid, reclaimedGift, reservedPaid, reservedGift string
	funding := &paymentWalletFunding{OrderID: orderID, UserID: userID}
	if err := rows.Scan(&paid, &gift, &funding.CashPaidMinor, &funding.Currency,
		&refundedPaid, &reclaimedGift, &funding.RefundedCashMinor,
		&reservedPaid, &reservedGift, &funding.ReservedCashMinor,
		&funding.Version, &funding.UpdatedAt); err != nil {
		return nil, nil, err
	}
	if funding.Paid, err = parseRefundDecimal(paid); err != nil {
		return nil, nil, err
	}
	if funding.Gift, err = parseRefundDecimal(gift); err != nil {
		return nil, nil, err
	}
	if funding.RefundedPaid, err = parseRefundDecimal(refundedPaid); err != nil {
		return nil, nil, err
	}
	if funding.ReclaimedGift, err = parseRefundDecimal(reclaimedGift); err != nil {
		return nil, nil, err
	}
	if funding.ReservedPaid, err = parseRefundDecimal(reservedPaid); err != nil {
		return nil, nil, err
	}
	if funding.ReservedGift, err = parseRefundDecimal(reservedGift); err != nil {
		return nil, nil, err
	}
	return funding, user, rows.Err()
}

func loadPaymentSubscriptionRefundState(ctx context.Context, client *dbent.Client, orderID int64, lock bool) (*paymentSubscriptionGrant, *dbent.UserSubscription, error) {
	query := `SELECT subscription_id, user_id, group_id, term_start_at,
		original_term_end_at, current_term_end_at, refunded_seconds,
		reserved_seconds, refunded_cash_minor, reserved_cash_minor,
		CAST(balance_bonus AS TEXT), reset_card_count, concurrency_target,
		version FROM payment_subscription_grants WHERE payment_order_id = $1`
	if lock && paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, orderID)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
		return nil, nil, errRefundAccountingMissing
	}
	grant := &paymentSubscriptionGrant{OrderID: orderID}
	var bonus string
	if err := rows.Scan(&grant.SubscriptionID, &grant.UserID, &grant.GroupID,
		&grant.TermStart, &grant.OriginalEnd, &grant.CurrentEnd,
		&grant.RefundedSeconds, &grant.ReservedSeconds,
		&grant.RefundedCashMinor, &grant.ReservedCashMinor, &bonus,
		&grant.ResetCardCount, &grant.ConcurrencyTarget, &grant.Version); err != nil {
		return nil, nil, err
	}
	// A transaction uses one physical PostgreSQL connection. Finish consuming
	// this result before Ent issues the subscription query below; lib/pq cannot
	// start another statement while the prior result still owns the connection.
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	grant.BalanceBonus, err = parseRefundDecimal(bonus)
	if err != nil {
		return nil, nil, err
	}
	subQuery := client.UserSubscription.Query().Where(func(s *entsql.Selector) {
		s.Where(entsql.EQ(s.C("id"), grant.SubscriptionID))
	})
	if lock && paymentAuditDialect(client) == dialect.Postgres {
		subQuery.ForUpdate()
	}
	sub, err := subQuery.Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil, errRefundAccountingMissing
		}
		return nil, nil, err
	}
	return grant, sub, nil
}

// PaymentWalletFundingInput travels only inside trusted payment fulfillment.
// The user repository consumes it in the same redeem transaction that credits
// users.balance, so aggregate money and provenance cannot diverge.
type PaymentWalletFundingInput struct {
	OrderID, UserID int64
	PaidCredit      float64
	GiftCredit      float64
	CashPaidMinor   int64
	Currency        string
}

type paymentWalletFundingContextKey struct{}

func ContextWithPaymentWalletFunding(ctx context.Context, input PaymentWalletFundingInput) context.Context {
	return context.WithValue(ctx, paymentWalletFundingContextKey{}, input)
}

func PaymentWalletFundingFromContext(ctx context.Context) (PaymentWalletFundingInput, bool) {
	input, ok := ctx.Value(paymentWalletFundingContextKey{}).(PaymentWalletFundingInput)
	return input, ok
}

func PaymentWalletFundingForOrder(order *dbent.PaymentOrder) (PaymentWalletFundingInput, error) {
	if order == nil || order.OrderType != payment.OrderTypeBalance || order.ProductSnapshot == nil {
		return PaymentWalletFundingInput{}, errors.New("balance payment funding snapshot is missing")
	}
	requiresExplicitSplit := false
	if rawVersion, hasVersion := order.ProductSnapshot["schema_version"]; hasVersion {
		version, ok := paymentSnapshotFloat(rawVersion)
		if !ok || !finiteRefundReviewAmount(version) || version < 0 {
			return PaymentWalletFundingInput{}, errors.New("balance payment snapshot version is invalid")
		}
		requiresExplicitSplit = version >= 2
	}
	credited, ok := paymentSnapshotFloat(order.ProductSnapshot["credited_amount"])
	if !ok || !finiteRefundReviewAmount(credited) || credited <= 0 || math.Abs(credited-order.Amount) > paymentAmountZeroTolerance(PaymentOrderCurrency(order)) {
		return PaymentWalletFundingInput{}, errors.New("balance payment credited amount is invalid")
	}
	bonus := paymentOrderEntitlements(order).BalanceBonus
	if rawGift, exists := order.ProductSnapshot["gift_credit_amount"]; exists {
		explicit, ok := paymentSnapshotFloat(rawGift)
		if !ok {
			return PaymentWalletFundingInput{}, errors.New("balance payment gift credit amount is invalid")
		}
		bonus = explicit
	} else if requiresExplicitSplit {
		return PaymentWalletFundingInput{}, errors.New("balance payment gift credit amount is missing")
	}
	paid := credited - bonus
	if rawPaid, exists := order.ProductSnapshot["paid_credit_amount"]; exists {
		explicit, ok := paymentSnapshotFloat(rawPaid)
		if !ok {
			return PaymentWalletFundingInput{}, errors.New("balance payment paid credit amount is invalid")
		}
		paid = explicit
	} else if requiresExplicitSplit {
		return PaymentWalletFundingInput{}, errors.New("balance payment paid credit amount is missing")
	}
	if !finiteRefundReviewAmount(paid) || !finiteRefundReviewAmount(bonus) || paid <= 0 || bonus < 0 || math.Abs(paid+bonus-credited) > paymentAmountZeroTolerance(PaymentOrderCurrency(order)) {
		return PaymentWalletFundingInput{}, errors.New("balance payment paid/gift split is invalid")
	}
	cashMinor, err := payment.AmountToMinorUnit(strconv.FormatFloat(order.PayAmount, 'f', -1, 64), PaymentOrderCurrency(order))
	if err != nil || cashMinor <= 0 {
		return PaymentWalletFundingInput{}, errors.New("balance payment cash amount is invalid")
	}
	return PaymentWalletFundingInput{
		OrderID: order.ID, UserID: order.UserID, PaidCredit: paid, GiftCredit: bonus,
		CashPaidMinor: cashMinor, Currency: PaymentOrderCurrency(order),
	}, nil
}

// paymentWalletFundingRequired distinguishes new snapshots, whose paid/gift
// provenance is part of the fulfillment contract, from older partial or empty
// snapshots. Historical orders must remain fulfillable, but an invalid new
// snapshot must fail before crediting the wallet.
func paymentWalletFundingRequired(order *dbent.PaymentOrder) bool {
	if order == nil || order.ProductSnapshot == nil {
		return false
	}
	if rawVersion, exists := order.ProductSnapshot["schema_version"]; exists {
		version, ok := paymentSnapshotFloat(rawVersion)
		// A malformed version is not historical evidence. Route it through the
		// strict path so fulfillment stops instead of inventing principal.
		return !ok || !finiteRefundReviewAmount(version) || version < 0 || version >= 2
	}
	_, hasPaid := order.ProductSnapshot["paid_credit_amount"]
	_, hasGift := order.ProductSnapshot["gift_credit_amount"]
	return hasPaid || hasGift
}

// RecordPaymentWalletFunding inserts the immutable order split after the same
// transaction has explicitly added PaidCredit to wallet_available_paid.
func RecordPaymentWalletFunding(ctx context.Context, client *dbent.Client, input PaymentWalletFundingInput) error {
	if client == nil || input.OrderID <= 0 || input.UserID <= 0 || input.PaidCredit <= 0 || input.CashPaidMinor <= 0 {
		return errors.New("invalid payment wallet funding input")
	}
	res, err := client.ExecContext(ctx, `INSERT INTO payment_wallet_fundings
		(payment_order_id, user_id, paid_credit_amount, gift_credit_amount, cash_paid_minor, currency)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (payment_order_id) DO NOTHING`,
		input.OrderID, input.UserID, input.PaidCredit, input.GiftCredit, input.CashPaidMinor, input.Currency)
	if err != nil {
		return err
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 1 {
		return nil
	}
	rows, err := client.QueryContext(ctx, `SELECT user_id, CAST(paid_credit_amount AS TEXT),
		CAST(gift_credit_amount AS TEXT), cash_paid_minor, currency
		FROM payment_wallet_fundings WHERE payment_order_id = $1`, input.OrderID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return errors.New("payment wallet funding conflict could not be verified")
	}
	var userID, cashMinor int64
	var paidRaw, giftRaw, currency string
	if err := rows.Scan(&userID, &paidRaw, &giftRaw, &cashMinor, &currency); err != nil {
		return err
	}
	paid, err := parseRefundDecimal(paidRaw)
	if err != nil {
		return err
	}
	gift, err := parseRefundDecimal(giftRaw)
	if err != nil {
		return err
	}
	if userID != input.UserID || cashMinor != input.CashPaidMinor || currency != input.Currency ||
		!paid.Equal(decimal.NewFromFloat(input.PaidCredit)) || !gift.Equal(decimal.NewFromFloat(input.GiftCredit)) {
		return errors.New("payment wallet funding idempotency conflict")
	}
	return rows.Err()
}

func insertPaymentSubscriptionGrant(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, sub *UserSubscription, termStart time.Time) error {
	if client == nil || order == nil || sub == nil || termStart.IsZero() {
		return errors.New("invalid payment subscription grant input")
	}
	termEnd := sub.ExpiresAt
	termStart = termStart.UTC()
	termEnd = termEnd.UTC()
	if !termEnd.After(termStart) {
		return errors.New("payment subscription term is empty")
	}
	entitlements, err := paymentOrderEntitlementsStrict(order)
	if err != nil {
		return err
	}
	resetCardCommitment, err := entitlements.ResetCardTotalCommitment()
	if err != nil {
		return err
	}
	_, err = client.ExecContext(ctx, `INSERT INTO payment_subscription_grants
		(payment_order_id, subscription_id, user_id, group_id, term_start_at,
		 original_term_end_at, current_term_end_at, balance_bonus,
		 reset_card_count, concurrency_target)
		VALUES ($1,$2,$3,$4,$5,$6,$6,$7,$8,$9)
	ON CONFLICT (payment_order_id) DO NOTHING`, order.ID, sub.ID, order.UserID,
		sub.GroupID, termStart, termEnd, entitlements.BalanceBonus,
		resetCardCommitment, entitlements.Concurrency)
	return err
}

func setWalletEventKind(ctx context.Context, client *dbent.Client, kind string) {
	if client == nil || paymentAuditDialect(client) != dialect.Postgres {
		return
	}
	_, _ = client.ExecContext(ctx, `SELECT set_config('sub2api.wallet_event_kind', $1, true)`, kind)
}

// MarkPaymentWalletFundingMutation labels the following trusted user-balance
// update inside the caller's transaction for the low-volume component audit.
func MarkPaymentWalletFundingMutation(ctx context.Context, client *dbent.Client) {
	setWalletEventKind(ctx, client, "payment_funding")
}

func setSubscriptionRefundMutationKind(ctx context.Context, client *dbent.Client, kind string) {
	if client == nil || paymentAuditDialect(client) != dialect.Postgres {
		return
	}
	_, _ = client.ExecContext(ctx, `SELECT set_config('sub2api.subscription_refund_mutation', $1, true)`, kind)
}

func timeEqualToSecond(a, b time.Time) bool {
	return a.UTC().Truncate(time.Second).Equal(b.UTC().Truncate(time.Second))
}

// capturedSubscriptionRefundExpiry returns the exact boundary that may be
// persisted when a reserved subscription refund succeeds. Older binaries
// truncated a future-term reservation to whole seconds. A term can start with
// sub-second precision, so that historical value can be just before its own
// grant boundary even though it represents the same recorded second.
//
// Only that narrowly provable legacy shape is repaired. Larger drift remains
// an integrity error rather than silently changing a subscription timeline.
func capturedSubscriptionRefundExpiry(grant *paymentSubscriptionGrant, expiresAt time.Time) (time.Time, bool, error) {
	if grant == nil {
		return time.Time{}, false, errors.New("subscription refund grant is missing")
	}
	effective := expiresAt.UTC()
	termStart := grant.TermStart.UTC()
	if !effective.Before(termStart) {
		return effective, false, nil
	}
	if !timeEqualToSecond(effective, termStart) {
		return time.Time{}, false, errors.New("reserved subscription expiry precedes its grant boundary")
	}
	return termStart, true, nil
}

func requireSingleAffected(res stdsql.Result, action string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%s affected %d rows", action, n)
	}
	return nil
}

var errRefundQuoteStale = errors.New("refund quote is stale")

func (s *PaymentService) reserveReviewedRefundEntitlement(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, plan *RefundPlan, now time.Time) (*RefundReview, error) {
	if order == nil || plan == nil || strings.TrimSpace(plan.QuoteRevision) == "" {
		return nil, errors.New("reviewed refund reservation is missing its quote")
	}
	review, err := s.reviewRefundWithClient(ctx, client, order.ID, now, true)
	if err != nil {
		return nil, err
	}
	if review.RequiresManualReview {
		return nil, fmt.Errorf("%w: refund now requires manual review (%s)", errRefundQuoteStale, review.ReasonCode)
	}
	if !review.CanRefund {
		return nil, fmt.Errorf("%w: refund is no longer available (%s)", errRefundQuoteStale, review.ReasonCode)
	}
	if selectedRefundRevision(review.QuoteRevision, plan.RequestedCashMinor) != plan.QuoteRevision {
		return nil, fmt.Errorf("%w: revision changed from %s to %s", errRefundQuoteStale, plan.QuoteRevision, review.QuoteRevision)
	}

	if err := selectRefundReviewAmount(review, plan.RequestedCashMinor); err != nil {
		return nil, err
	}
	plan.Order = order
	plan.RefundAmount = review.EntitlementAmount
	plan.GatewayAmount = review.DefaultRefundAmount
	plan.ReviewKind = review.OrderType
	settled, _ := refundOrderAmounts(order)
	plan.SettledRefundAmount = settled
	plan.RemainingRefundable = refundRemainingAmount(order, settled)

	switch review.OrderType {
	case refundReviewKindBalance:
		if review.Balance == nil {
			return nil, errors.New("balance refund review is incomplete")
		}
		funding, _, err := loadPaymentWalletRefundState(ctx, client, order.ID, order.UserID, true)
		if err != nil {
			return nil, err
		}
		entitlement := decimal.NewFromFloat(review.EntitlementAmount)
		paid := decimal.NewFromFloat(review.Balance.PaidCreditToReclaim)
		gift := decimal.NewFromFloat(review.Balance.GiftCreditToReclaim)
		cashMinor := int64(math.Round(review.DefaultRefundAmount * 100))
		setWalletEventKind(ctx, client, "refund_reserve")
		updated, err := client.User.Update().Where(
			user.IDEQ(order.UserID), user.DeletedAtIsNil(),
			user.BalanceGTE(entitlement.InexactFloat64()),
			user.WalletAvailablePaidGTE(paid.InexactFloat64()),
		).AddBalance(-entitlement.InexactFloat64()).
			AddFrozenBalance(entitlement.InexactFloat64()).
			AddWalletAvailablePaid(-paid.InexactFloat64()).
			AddWalletFrozenPaid(paid.InexactFloat64()).
			Save(ctx)
		if err != nil {
			return nil, err
		}
		if updated != 1 {
			return nil, fmt.Errorf("%w: wallet balance changed", errRefundQuoteStale)
		}
		res, err := client.ExecContext(ctx, `UPDATE payment_wallet_fundings SET
			reserved_paid_amount = reserved_paid_amount + $2,
			reserved_gift_amount = reserved_gift_amount + $3,
			reserved_cash_minor = reserved_cash_minor + $4,
			version = version + 1, updated_at = CURRENT_TIMESTAMP
			WHERE payment_order_id = $1 AND version = $5
			  AND refunded_paid_amount + reserved_paid_amount + $2 <= paid_credit_amount
			  AND reclaimed_gift_amount + reserved_gift_amount + $3 <= gift_credit_amount
			  AND refunded_cash_minor + reserved_cash_minor + $4 <= cash_paid_minor`,
			order.ID, paid.String(), gift.String(), cashMinor, funding.Version)
		if err != nil {
			return nil, err
		}
		if err := requireSingleAffected(res, "reserve wallet refund"); err != nil {
			return nil, fmt.Errorf("%w: wallet funding changed", errRefundQuoteStale)
		}
		plan.DeductBalance = true
		plan.DeductionType = payment.DeductionTypeBalance
		plan.BalanceToDeduct = entitlement.InexactFloat64()
		plan.WalletPaidToReserve = paid.InexactFloat64()
		plan.WalletGiftToReserve = gift.InexactFloat64()
	case refundReviewKindSubscription:
		if review.Subscription == nil {
			return nil, errors.New("subscription refund review is incomplete")
		}
		grant, sub, err := loadPaymentSubscriptionRefundState(ctx, client, order.ID, true)
		if err != nil {
			return nil, err
		}
		// Preserve the exact grant boundary. Future terms can begin with
		// sub-second precision; truncating here makes the aggregate subscription
		// end slightly precede the preceding grant and later breaks provenance
		// checks for otherwise exact purchased durations.
		newExpiry := review.Subscription.NewExpiresAt.UTC()
		cashMinor := int64(math.Round(review.DefaultRefundAmount * 100))
		res, err := client.ExecContext(ctx, `UPDATE payment_subscription_grants SET
			reserved_seconds = $2, reserved_cash_minor = $3,
			version = version + 1, updated_at = CURRENT_TIMESTAMP
			WHERE payment_order_id = $1 AND version = $4 AND reserved_seconds = 0 AND reserved_cash_minor = 0`,
			order.ID, review.Subscription.SecondsToReclaim, cashMinor, grant.Version)
		if err != nil {
			return nil, err
		}
		if err := requireSingleAffected(res, "reserve subscription refund"); err != nil {
			return nil, fmt.Errorf("%w: subscription grant changed", errRefundQuoteStale)
		}
		// The grant hold is durable before the aggregate expiry moves. A database
		// guard blocks renewals and manual term edits until this hold is captured
		// or released; this transaction is the only trusted reserve mutation.
		setSubscriptionRefundMutationKind(ctx, client, "reserve")
		updated, err := client.UserSubscription.Update().Where(
			usersubscription.IDEQ(sub.ID), usersubscription.DeletedAtIsNil(),
			usersubscription.ExpiresAtEQ(sub.ExpiresAt),
		).SetExpiresAt(newExpiry).SetUpdatedAt(now).Save(ctx)
		if err != nil {
			return nil, err
		}
		if updated != 1 {
			return nil, fmt.Errorf("%w: subscription expiry changed", errRefundQuoteStale)
		}
		plan.DeductBalance = false
		plan.DeductionType = payment.DeductionTypeSubscription
		plan.SubscriptionID = sub.ID
		plan.SubscriptionSecondsToReserve = review.Subscription.SecondsToReclaim
		plan.SubscriptionNewExpiry = newExpiry
	default:
		return nil, errors.New("unsupported reviewed refund kind")
	}
	return review, nil
}

func finalizeReviewedRefundEntitlement(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, attempt *unifiedRefundAttempt) error {
	if order == nil || attempt == nil || !attempt.EntitlementReserved {
		return nil
	}
	switch attempt.RefundKind {
	case refundReviewKindBalance:
		entitlement := decimal.NewFromFloat(attempt.WalletPaidAmount + attempt.WalletGiftAmount)
		paid := decimal.NewFromFloat(attempt.WalletPaidAmount)
		setWalletEventKind(ctx, client, "refund_capture")
		updated, err := client.User.Update().Where(
			user.IDEQ(order.UserID), user.DeletedAtIsNil(),
			user.FrozenBalanceGTE(entitlement.InexactFloat64()),
			user.WalletFrozenPaidGTE(paid.InexactFloat64()),
		).AddFrozenBalance(-entitlement.InexactFloat64()).
			AddWalletFrozenPaid(-paid.InexactFloat64()).Save(ctx)
		if err != nil {
			return err
		}
		if updated != 1 {
			return errors.New("reserved wallet entitlement is unavailable")
		}
		res, err := client.ExecContext(ctx, `UPDATE payment_wallet_fundings SET
			reserved_paid_amount = reserved_paid_amount - $2,
			reserved_gift_amount = reserved_gift_amount - $3,
			reserved_cash_minor = reserved_cash_minor - $4,
			refunded_paid_amount = refunded_paid_amount + $2,
			reclaimed_gift_amount = reclaimed_gift_amount + $3,
			refunded_cash_minor = refunded_cash_minor + $4,
			version = version + 1, updated_at = CURRENT_TIMESTAMP
			WHERE payment_order_id = $1
			  AND reserved_paid_amount >= $2 AND reserved_gift_amount >= $3
			  AND reserved_cash_minor >= $4`, order.ID, attempt.WalletPaidAmount,
			attempt.WalletGiftAmount, attempt.AmountFen)
		if err != nil {
			return err
		}
		if err := requireSingleAffected(res, "capture wallet refund"); err != nil {
			return err
		}
	case refundReviewKindSubscription:
		grant, sub, err := loadPaymentSubscriptionRefundState(ctx, client, order.ID, true)
		if err != nil {
			return err
		}
		if grant.ReservedSeconds != attempt.SubscriptionSeconds || grant.ReservedCashMinor != attempt.AmountFen ||
			attempt.ValuationAt == nil || !timeEqualToSecond(sub.ExpiresAt, *attempt.ValuationAt) {
			return errors.New("reserved subscription entitlement changed")
		}
		effectiveExpiry, repairedLegacyBoundary, err := capturedSubscriptionRefundExpiry(grant, sub.ExpiresAt)
		if err != nil {
			return err
		}
		res, err := client.ExecContext(ctx, `UPDATE payment_subscription_grants SET
			current_term_end_at = $2,
			reserved_seconds = 0, reserved_cash_minor = 0,
			refunded_seconds = refunded_seconds + $3,
			refunded_cash_minor = refunded_cash_minor + $4,
			version = version + 1, updated_at = CURRENT_TIMESTAMP
			WHERE payment_order_id = $1 AND reserved_seconds = $3`,
			order.ID, effectiveExpiry, attempt.SubscriptionSeconds, attempt.AmountFen)
		if err != nil {
			return err
		}
		if err := requireSingleAffected(res, "capture subscription refund"); err != nil {
			return err
		}
		if repairedLegacyBoundary {
			// The grant hold has just been atomically captured, so the database
			// guard permits this matching correction to the live subscription. If
			// it races a different writer, the enclosing transaction rolls back
			// rather than committing a mismatched grant/subscription pair.
			updated, err := client.UserSubscription.Update().Where(
				usersubscription.IDEQ(sub.ID), usersubscription.DeletedAtIsNil(),
				usersubscription.ExpiresAtEQ(sub.ExpiresAt),
			).SetExpiresAt(effectiveExpiry).SetUpdatedAt(time.Now()).Save(ctx)
			if err != nil {
				return err
			}
			if updated != 1 {
				return errors.New("reserved subscription expiry changed while repairing its grant boundary")
			}
		}
	default:
		return errors.New("unsupported refund reservation kind")
	}
	attempt.EntitlementReserved = false
	return nil
}

func releaseReviewedRefundEntitlement(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, attempt *unifiedRefundAttempt) error {
	if order == nil || attempt == nil || !attempt.EntitlementReserved {
		return nil
	}
	switch attempt.RefundKind {
	case refundReviewKindBalance:
		entitlement := decimal.NewFromFloat(attempt.WalletPaidAmount + attempt.WalletGiftAmount)
		paid := decimal.NewFromFloat(attempt.WalletPaidAmount)
		setWalletEventKind(ctx, client, "refund_release")
		updated, err := client.User.Update().Where(
			user.IDEQ(order.UserID), user.DeletedAtIsNil(),
			user.FrozenBalanceGTE(entitlement.InexactFloat64()),
			user.WalletFrozenPaidGTE(paid.InexactFloat64()),
		).AddBalance(entitlement.InexactFloat64()).
			AddFrozenBalance(-entitlement.InexactFloat64()).
			AddWalletAvailablePaid(paid.InexactFloat64()).
			AddWalletFrozenPaid(-paid.InexactFloat64()).Save(ctx)
		if err != nil {
			return err
		}
		if updated != 1 {
			return errors.New("reserved wallet entitlement cannot be released")
		}
		res, err := client.ExecContext(ctx, `UPDATE payment_wallet_fundings SET
			reserved_paid_amount = reserved_paid_amount - $2,
			reserved_gift_amount = reserved_gift_amount - $3,
			reserved_cash_minor = reserved_cash_minor - $4,
			version = version + 1, updated_at = CURRENT_TIMESTAMP
			WHERE payment_order_id = $1
			  AND reserved_paid_amount >= $2 AND reserved_gift_amount >= $3
			  AND reserved_cash_minor >= $4`, order.ID, attempt.WalletPaidAmount,
			attempt.WalletGiftAmount, attempt.AmountFen)
		if err != nil {
			return err
		}
		if err := requireSingleAffected(res, "release wallet refund"); err != nil {
			return err
		}
	case refundReviewKindSubscription:
		grant, sub, err := loadPaymentSubscriptionRefundState(ctx, client, order.ID, true)
		if err != nil {
			return err
		}
		if grant.ReservedSeconds != attempt.SubscriptionSeconds || grant.ReservedCashMinor != attempt.AmountFen || attempt.ValuationAt == nil ||
			!timeEqualToSecond(sub.ExpiresAt, *attempt.ValuationAt) {
			return errors.New("reserved subscription entitlement cannot be restored automatically")
		}
		setSubscriptionRefundMutationKind(ctx, client, "release")
		updated, err := client.UserSubscription.Update().Where(
			usersubscription.IDEQ(sub.ID), usersubscription.DeletedAtIsNil(),
			usersubscription.ExpiresAtEQ(sub.ExpiresAt),
		).SetExpiresAt(grant.CurrentEnd).SetUpdatedAt(time.Now()).Save(ctx)
		if err != nil {
			return err
		}
		if updated != 1 {
			return errors.New("subscription expiry changed while refund was pending")
		}
		res, err := client.ExecContext(ctx, `UPDATE payment_subscription_grants SET
			reserved_seconds = 0, reserved_cash_minor = 0,
			version = version + 1, updated_at = CURRENT_TIMESTAMP
			WHERE payment_order_id = $1 AND reserved_seconds = $2`, order.ID, attempt.SubscriptionSeconds)
		if err != nil {
			return err
		}
		if err := requireSingleAffected(res, "release subscription refund"); err != nil {
			return err
		}
	default:
		return errors.New("unsupported refund reservation kind")
	}
	attempt.EntitlementReserved = false
	return nil
}
