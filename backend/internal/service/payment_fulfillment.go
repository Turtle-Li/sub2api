package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	"github.com/shopspring/decimal"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ErrOrderNotFound is returned by HandlePaymentNotification when the webhook
// references an out_trade_no that does not exist in our DB. Callers (webhook
// handlers) should treat this as a terminal, non-retryable condition and still
// respond with a 2xx success to the provider — otherwise the provider will keep
// retrying forever (e.g. when a foreign environment's webhook endpoint is
// misconfigured to point at us, or when our orders table has been wiped).
var (
	ErrOrderNotFound = errors.New("payment order not found")

	errResetCardGrantExpirySnapshotElapsed = errors.New(resetCardGrantExpiryManualReviewReason)
)

const (
	paymentFulfillmentLeaseDuration = 5 * time.Minute

	// resetCardGrantExpiryManualReviewReason is durable operator-facing state
	// for a paid reset-card order whose immutable grant deadline elapsed before
	// fulfillment could create its entitlement. It must remain exact because
	// recovery uses it to keep the order out of automatic retries.
	resetCardGrantExpiryManualReviewReason = "manual review required: reset-card grant expiry snapshot has elapsed"
	resetCardGrantExpiryManualReviewAudit  = "RESET_CARD_GRANT_EXPIRY_MANUAL_REVIEW_REQUIRED"
)

type paymentFulfillmentLease struct {
	version time.Time
}

// paymentFulfillmentLeaseAcquirer selects the durable order snapshot that
// owns a fulfillment lease. Normal callback/admin paths keep the historical
// CAS acquirer; recovery supplies an acquirer that couples the refund fence
// read and claim in one short transaction.
type paymentFulfillmentLeaseAcquirer func(context.Context, *dbent.PaymentOrder) (*dbent.PaymentOrder, *paymentFulfillmentLease, error)

// --- Payment Notification & Fulfillment ---

func (s *PaymentService) HandlePaymentNotification(ctx context.Context, n *payment.PaymentNotification, pk string) error {
	if n.Status != payment.NotificationStatusSuccess {
		return nil
	}
	// Look up order by out_trade_no (the external order ID we sent to the provider)
	order, err := s.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNo(n.OrderID)).Only(ctx)
	if err != nil {
		// Fallback only for true legacy "sub2_N" DB-ID payloads when the
		// current out_trade_no lookup genuinely did not find an order.
		if oid, ok := parseLegacyPaymentOrderID(n.OrderID, err); ok {
			return s.confirmPayment(ctx, oid, n.TradeNo, n.Amount, pk, n.Metadata)
		}
		if dbent.IsNotFound(err) {
			return fmt.Errorf("%w: out_trade_no=%s", ErrOrderNotFound, n.OrderID)
		}
		return fmt.Errorf("lookup order failed for out_trade_no %s: %w", n.OrderID, err)
	}
	return s.confirmPayment(ctx, order.ID, n.TradeNo, n.Amount, pk, n.Metadata)
}

func parseLegacyPaymentOrderID(orderID string, lookupErr error) (int64, bool) {
	if !dbent.IsNotFound(lookupErr) {
		return 0, false
	}
	orderID = strings.TrimSpace(orderID)
	if !strings.HasPrefix(orderID, orderIDPrefix) {
		return 0, false
	}
	trimmed := strings.TrimPrefix(orderID, orderIDPrefix)
	if trimmed == "" || trimmed == orderID {
		return 0, false
	}
	oid, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || oid <= 0 {
		return 0, false
	}
	return oid, true
}

func (s *PaymentService) confirmPayment(ctx context.Context, oid int64, tradeNo string, paid float64, pk string, metadata map[string]string) error {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		if dbent.IsNotFound(err) {
			return fmt.Errorf("%w: id=%d", ErrOrderNotFound, oid)
		}
		return fmt.Errorf("lookup order %d for payment confirmation: %w", oid, err)
	}
	instanceProviderKey := ""
	if inst, instErr := s.getOrderProviderInstance(ctx, o); instErr == nil && inst != nil {
		instanceProviderKey = inst.ProviderKey
	}
	expectedProviderKey := expectedNotificationProviderKeyForOrder(s.registry, o, instanceProviderKey)
	if expectedProviderKey != "" && strings.TrimSpace(pk) != "" && !strings.EqualFold(expectedProviderKey, strings.TrimSpace(pk)) {
		s.writeAuditLog(ctx, o.ID, "PAYMENT_PROVIDER_MISMATCH", pk, map[string]any{
			"expectedProvider": expectedProviderKey,
			"actualProvider":   pk,
			"tradeNo":          tradeNo,
		})
		return fmt.Errorf("provider mismatch: expected %s, got %s", expectedProviderKey, pk)
	}
	if err := validateProviderNotificationMetadata(o, pk, metadata); err != nil {
		s.writeAuditLog(ctx, o.ID, "PAYMENT_PROVIDER_METADATA_MISMATCH", pk, map[string]any{
			"detail":  err.Error(),
			"tradeNo": tradeNo,
		})
		return err
	}
	if !isValidProviderAmount(paid) {
		s.writeAuditLog(ctx, o.ID, "PAYMENT_INVALID_AMOUNT", pk, map[string]any{
			"expected": o.PayAmount,
			"paid":     paid,
			"tradeNo":  tradeNo,
		})
		return fmt.Errorf("invalid paid amount from provider: %v", paid)
	}
	if math.Abs(paid-o.PayAmount) > paymentAmountToleranceForCurrency(PaymentOrderCurrency(o)) {
		if o.Status == OrderStatusCancelled {
			// The locked late-payment path commits an operator-visible manual
			// fence for a trusted but financially inconsistent callback. Do not
			// reject it before that transaction can preserve the evidence.
			return s.toPaid(ctx, o, tradeNo, paid, pk)
		}
		s.writeAuditLog(ctx, o.ID, "PAYMENT_AMOUNT_MISMATCH", pk, map[string]any{"expected": o.PayAmount, "paid": paid, "tradeNo": tradeNo})
		return fmt.Errorf("amount mismatch: expected %s, got %s", strconv.FormatFloat(o.PayAmount, 'f', -1, 64), strconv.FormatFloat(paid, 'f', -1, 64))
	}
	return s.toPaid(ctx, o, tradeNo, paid, pk)
}

func paymentAmountToleranceForCurrency(currency string) float64 {
	minorUnit := payment.CurrencyMinorUnit(currency)
	if minorUnit <= 2 {
		return amountToleranceCNY
	}
	return math.Pow10(-minorUnit) / 2
}

// paymentAmountZeroTolerance is used only when deciding whether a monetary
// value is effectively zero or whether two already-rounded amounts are equal.
// It is intentionally narrower than paymentAmountToleranceForCurrency: the
// latter preserves the historical provider-notification slack, while this
// helper must still allow the smallest valid unit (for example CNY 0.01).
func paymentAmountZeroTolerance(currency string) float64 {
	fractionDigits := payment.CurrencyMaxFractionDigits(currency)
	if fractionDigits < 0 {
		fractionDigits = 2
	}
	return math.Pow10(-fractionDigits) / 2
}

func isValidProviderAmount(amount float64) bool {
	return amount > 0 && !math.IsNaN(amount) && !math.IsInf(amount, 0)
}

func validateProviderNotificationMetadata(order *dbent.PaymentOrder, providerKey string, metadata map[string]string) error {
	return validateProviderSnapshotMetadata(order, providerKey, metadata)
}

func expectedNotificationProviderKey(registry *payment.Registry, orderPaymentType string, orderProviderKey string, instanceProviderKey string) string {
	if key := strings.TrimSpace(instanceProviderKey); key != "" {
		return key
	}
	if key := strings.TrimSpace(orderProviderKey); key != "" {
		return key
	}
	if registry != nil {
		if key := strings.TrimSpace(registry.GetProviderKey(payment.PaymentType(orderPaymentType))); key != "" {
			return key
		}
	}
	return strings.TrimSpace(orderPaymentType)
}

func (s *PaymentService) toPaid(ctx context.Context, o *dbent.PaymentOrder, tradeNo string, paid float64, pk string) error {
	if o == nil {
		return errors.New("payment order is missing")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	locked, err := lockUnifiedRefundOrder(txCtx, client, o.ID)
	if err != nil {
		return err
	}
	if expected := expectedNotificationProviderKeyForOrder(s.registry, locked, ""); expected != "" && strings.TrimSpace(pk) != "" && !strings.EqualFold(expected, strings.TrimSpace(pk)) {
		return fmt.Errorf("provider mismatch: expected %s, got %s", expected, pk)
	}

	// A locally cancelled row is never eligible for PAID. It is a separate,
	// durable late-money claim and retains CANCELLED even when the payment is
	// trusted. This branch executes before the discount ledger so a released
	// coupon can never be reserved or consumed again by a delayed callback.
	if locked.Status == OrderStatusCancelled {
		if err := s.recordCancelledLatePaymentTx(txCtx, client, locked, tradeNo, paid, pk); err != nil {
			return err
		}
		return tx.Commit()
	}
	if !isValidProviderAmount(paid) || math.Abs(paid-locked.PayAmount) > paymentAmountToleranceForCurrency(PaymentOrderCurrency(locked)) {
		return fmt.Errorf("amount mismatch: expected %s, got %s", strconv.FormatFloat(locked.PayAmount, 'f', -1, 64), strconv.FormatFloat(paid, 'f', -1, 64))
	}
	// Historical rows can carry a gateway timestamp while still being PENDING
	// or within the normal expiry-recovery window. Those rows have never won a
	// PAID claim, so preserve the long-standing status-based recovery behavior.
	// Once a row has moved to another paid/fulfillment state, its PaidAt is the
	// authoritative duplicate fence.
	if locked.PaidAt != nil && locked.Status != OrderStatusPending && locked.Status != OrderStatusExpired {
		if err := tx.Commit(); err != nil {
			return err
		}
		return s.alreadyProcessed(ctx, locked)
	}

	if paymentOrderHasDiscount(locked) {
		allowed, changed, err := s.markDiscountOrderPaidTx(txCtx, client, locked, tradeNo, paid)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if !changed || !allowed {
			return s.alreadyProcessed(ctx, locked)
		}
		return s.executeFulfillment(ctx, locked.ID)
	}

	previousStatus := locked.Status
	now := time.Now()
	grace := now.Add(-paymentGraceMinutes * time.Minute)
	// Keep the status and grace fence in the update itself. PostgreSQL holds the
	// locked row while this runs, and the CAS remains essential for SQLite and
	// for callers that reached this code with a stale snapshot. In particular,
	// do not turn an order's database timestamp into an in-memory eligibility
	// decision: SQLite's stored precision and concurrent lifecycle updates are
	// the authority for the historical expiry grace rule.
	eligibleStatus := paymentorder.Or(
		paymentorder.StatusEQ(OrderStatusPending),
		paymentorder.And(
			paymentorder.StatusEQ(OrderStatusExpired),
			paymentorder.UpdatedAtGTE(grace),
		),
	)
	if strings.EqualFold(strings.TrimSpace(pk), payment.TypeUnifiedPay) {
		// A normal signed unified paid event remains authoritative even when
		// delivery is delayed. True late money is routed through PAID_AFTER_CLOSE
		// and the immutable CANCELLED branch above.
		eligibleStatus = paymentorder.StatusIn(OrderStatusPending, OrderStatusExpired)
	}
	// Provider-create responses can race a callback after the gateway accepted
	// payment. Recover only still-unpaid failed rows; a failure after PaidAt is
	// a fulfillment concern, not a new claim.
	eligibleStatus = paymentorder.Or(
		eligibleStatus,
		paymentorder.And(paymentorder.StatusEQ(OrderStatusFailed), paymentorder.PaidAtIsNil()),
	)
	updated, err := client.PaymentOrder.Update().Where(
		paymentorder.IDEQ(locked.ID),
		eligibleStatus,
	).SetStatus(OrderStatusPaid).SetPayAmount(paid).SetPaymentTradeNo(tradeNo).SetPaidAt(now).
		ClearFailedAt().ClearFailedReason().Save(txCtx)
	if err != nil {
		return fmt.Errorf("update to PAID: %w", err)
	}
	if updated == 0 {
		if err := tx.Commit(); err != nil {
			return err
		}
		return s.alreadyProcessed(ctx, locked)
	}
	if err := writePaymentAuditTx(txCtx, client, locked.ID, "ORDER_PAID", pk, map[string]any{"tradeNo": tradeNo, "paidAmount": paid}); err != nil {
		return err
	}
	if previousStatus == OrderStatusExpired || previousStatus == OrderStatusFailed {
		if err := writePaymentAuditTx(txCtx, client, locked.ID, "ORDER_RECOVERED", pk, map[string]any{
			"previous_status": previousStatus,
			"tradeNo":         tradeNo,
			"paidAmount":      paid,
			"reason":          "webhook payment success received after order " + previousStatus,
		}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.executeFulfillment(ctx, locked.ID)
}

func (s *PaymentService) alreadyProcessed(ctx context.Context, o *dbent.PaymentOrder) error {
	cur, err := s.entClient.PaymentOrder.Get(ctx, o.ID)
	if err != nil {
		if dbent.IsNotFound(err) {
			return fmt.Errorf("%w: id=%d", ErrOrderNotFound, o.ID)
		}
		return fmt.Errorf("reload payment order %d after payment status race: %w", o.ID, err)
	}
	switch cur.Status {
	case OrderStatusCompleted, OrderStatusRefunded:
		return nil
	case OrderStatusFailed, OrderStatusPaid, OrderStatusRecharging:
		// A durable manual hold has already recorded trusted payment and its
		// operator-facing reason. A duplicate provider callback must acknowledge
		// that fact rather than re-enter fulfillment; an administrator can still
		// explicitly invoke RetryFulfillment after resolving the order.
		if isResetCardGrantExpiryManualReview(cur) || isPaymentDiscountManualReview(cur) {
			return nil
		}
		return s.executeFulfillment(ctx, o.ID)
	case OrderStatusExpired:
		slog.Warn("webhook payment success for expired order beyond grace period",
			"orderID", o.ID,
			"status", cur.Status,
			"updatedAt", cur.UpdatedAt,
		)
		s.writeAuditLog(ctx, o.ID, "PAYMENT_AFTER_EXPIRY", "system", map[string]any{
			"status":    cur.Status,
			"updatedAt": cur.UpdatedAt,
			"reason":    "payment arrived after expiry grace period",
		})
		return nil
	default:
		return nil
	}
}

func (s *PaymentService) executeFulfillment(ctx context.Context, oid int64) error {
	return s.executeFulfillmentWithLeaseAcquirer(ctx, oid, s.acquireNormalPaymentFulfillmentLease)
}

func (s *PaymentService) executeRecoveryFulfillment(ctx context.Context, oid int64) error {
	return s.executeFulfillmentWithLeaseAcquirer(ctx, oid, s.acquireRecoveryPaymentFulfillmentLease)
}

func (s *PaymentService) executeFulfillmentWithLeaseAcquirer(ctx context.Context, oid int64, acquire paymentFulfillmentLeaseAcquirer) error {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}
	if isPaymentDiscountManualReview(o) {
		return infraerrors.Conflict("COUPON_MANUAL_REVIEW", "coupon capacity must be resolved before fulfillment")
	}
	switch o.OrderType {
	case payment.OrderTypeSubscription:
		return s.executeSubscriptionFulfillmentWithLeaseAcquirer(ctx, oid, acquire)
	case payment.OrderTypeResetCard:
		return s.executeResetCardFulfillmentWithLeaseAcquirer(ctx, oid, acquire)
	case payment.OrderTypeBalance:
		return s.executeBalanceFulfillmentWithLeaseAcquirer(ctx, oid, acquire)
	default:
		return infraerrors.BadRequest("INVALID_ORDER_TYPE", "unsupported payment order type cannot be fulfilled")
	}
}

func (s *PaymentService) executeResetCardFulfillmentWithLeaseAcquirer(ctx context.Context, oid int64, acquire paymentFulfillmentLeaseAcquirer) error {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.Status == OrderStatusCompleted {
		return nil
	}
	if psIsRefundStatus(o.Status) {
		return infraerrors.BadRequest("INVALID_STATUS", "refund-related order cannot fulfill")
	}
	if o.PaidAt == nil || !isValidProviderAmount(o.PayAmount) {
		return infraerrors.BadRequest("PAYMENT_NOT_CONFIRMED", "reset card order has no trusted payment confirmation")
	}
	if o.Status != OrderStatusPaid && o.Status != OrderStatusFailed && o.Status != OrderStatusRecharging {
		return infraerrors.BadRequest("INVALID_STATUS", "order cannot fulfill in status "+o.Status)
	}
	claimedOrder, lease, err := acquire(ctx, o)
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	if err := s.doResetCard(ctx, claimedOrder, lease); err != nil {
		if errors.Is(err, errResetCardGrantExpirySnapshotElapsed) {
			s.markResetCardGrantExpiryManualReview(ctx, oid, lease)
			return err
		}
		s.markFailed(ctx, oid, lease, err)
		return err
	}
	return nil
}

func (s *PaymentService) doResetCard(ctx context.Context, o *dbent.PaymentOrder, lease *paymentFulfillmentLease) error {
	targetID, groupID, err := validateResetCardPaymentOrderSnapshot(o)
	if err != nil {
		return err
	}
	terms, err := resetCardSnapshotPurchaseTerms(o.ProductSnapshot)
	if err != nil {
		return err
	}
	tierSnapshot, err := resetCardTierSnapshotForPaymentOrder(o)
	if err != nil {
		return err
	}
	if _, err := resetCardPaymentOrderGrantExpiry(o, s.resetCardCurrentTime()); err != nil {
		return err
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin reset card fulfillment tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	lockedOrderQuery := tx.Client().PaymentOrder.Query().Where(
		paymentorder.IDEQ(o.ID),
		paymentorder.StatusEQ(OrderStatusRecharging),
		paymentorder.PaidAtNotNil(),
		paymentorder.UpdatedAtEQ(lease.version),
	)
	if tx.Client().Driver().Dialect() == dialect.Postgres {
		lockedOrderQuery.ForUpdate()
	}
	lockedOrder, err := lockedOrderQuery.Only(txCtx)
	if err != nil {
		return fmt.Errorf("lock paid reset card order: %w", err)
	}
	o = lockedOrder
	lockedTargetID, lockedGroupID, err := validateResetCardPaymentOrderSnapshot(o)
	if err != nil {
		return err
	}
	if lockedTargetID != targetID || lockedGroupID != groupID {
		return errors.New("reset card order snapshot changed while fulfillment was being claimed")
	}
	lockedTerms, err := resetCardSnapshotPurchaseTerms(o.ProductSnapshot)
	if err != nil {
		return err
	}
	if lockedTerms != terms {
		return errors.New("reset card order purchase terms changed while fulfillment was being claimed")
	}
	lockedTierSnapshot, err := resetCardTierSnapshotForPaymentOrder(o)
	if err != nil {
		return err
	}
	if !resetCardTierSnapshotsEqual(tierSnapshot, lockedTierSnapshot) {
		return errors.New("reset card tier snapshot changed while fulfillment was being claimed")
	}
	claimed, err := tx.Client().PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(o.ID, 10)), paymentauditlog.ActionEQ("RESET_CARD_GRANTED")).Exist(txCtx)
	if err != nil {
		return fmt.Errorf("check reset card audit: %w", err)
	}
	var (
		existingGrantID    int64
		existingSubID      int64
		existingUserID     int64
		existingGroupID    int64
		existingQuantity   int
		existingGrantFound bool
	)
	grantQuery := `SELECT id,subscription_id,user_id,group_id,quantity FROM subscription_reset_grants WHERE payment_order_id=$1`
	if tx.Client().Driver().Dialect() == dialect.Postgres {
		grantQuery += " FOR UPDATE"
	}
	grantRows, err := tx.Client().QueryContext(txCtx, grantQuery, o.ID)
	if err != nil {
		return fmt.Errorf("load reset card payment grant: %w", err)
	}
	for grantRows.Next() {
		if existingGrantFound {
			_ = grantRows.Close()
			return errors.New("reset card payment order has multiple grants")
		}
		if err := grantRows.Scan(&existingGrantID, &existingSubID, &existingUserID, &existingGroupID, &existingQuantity); err != nil {
			_ = grantRows.Close()
			return fmt.Errorf("scan reset card payment grant: %w", err)
		}
		existingGrantFound = true
	}
	if err := grantRows.Err(); err != nil {
		_ = grantRows.Close()
		return fmt.Errorf("iterate reset card payment grant: %w", err)
	}
	_ = grantRows.Close()
	if claimed != existingGrantFound {
		return errors.New("reset card payment grant evidence is incomplete")
	}
	if existingGrantFound && (existingSubID != targetID || existingUserID != o.UserID || existingGroupID != groupID || existingQuantity != terms.quantity) {
		return errors.New("reset card payment grant evidence does not match the order")
	}
	if existingGrantFound && terms.useOnPurchase {
		if err := validateResetCardAutoUseEvidence(txCtx, tx.Client(), o.ID); err != nil {
			return err
		}
	}
	if !claimed {
		// Eligibility is authoritative at checkout. Fulfillment locks only the
		// immutable subscription identity, so a later suspension, soft delete,
		// catalogue edit, or natural expiry cannot invalidate an already-paid
		// order. The grant keeps the purchased expiry policy from its immutable snapshot.
		target, err := lockResetCardFulfillmentTarget(txCtx, tx.Client(), targetID, o.UserID)
		if err != nil {
			return err
		}
		// The subscription lock may have blocked until the checkout-time expiry
		// snapshot elapsed. Recheck after acquiring it so a grant can never be
		// created already expired.
		issuedAt := s.resetCardCurrentTime()
		grantExpiresAt, err := resetCardPaymentOrderGrantExpiry(o, issuedAt)
		if err != nil {
			return err
		}
		familyKey, tierRank, sourcePlanID := resetCardTierSnapshotValues(tierSnapshot)
		rows, err := tx.Client().QueryContext(txCtx, `INSERT INTO subscription_reset_grants (subscription_id,user_id,group_id,quantity,used_count,expires_at,issued_by,payment_order_id,card_family_key,source_tier_rank,source_plan_id,tier_snapshot_resolved,created_at,updated_at) VALUES ($1,$2,$3,$4,0,$5,NULL,$6,$7,$8,$9,TRUE,$10,$10) RETURNING id`, targetID, o.UserID, groupID, terms.quantity, grantExpiresAt, o.ID, familyKey, tierRank, sourcePlanID, issuedAt)
		if err != nil {
			return fmt.Errorf("grant reset card: %w", err)
		}
		var grantID int64
		if !rows.Next() || rows.Scan(&grantID) != nil {
			_ = rows.Close()
			return errors.New("grant reset card returned no id")
		}
		_ = rows.Close()
		detail, _ := json.Marshal(map[string]any{"subscription_id": targetID, "grant_id": grantID, "payment_order_id": o.ID, "quantity": terms.quantity, "use_on_purchase": terms.useOnPurchase})
		if _, err := tx.Client().PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(o.ID, 10)).SetAction("RESET_CARD_GRANTED").SetDetail(string(detail)).SetOperator("system").Save(txCtx); err != nil {
			return fmt.Errorf("record reset card audit: %w", err)
		}
		if terms.useOnPurchase {
			if reason := resetCardAutoUseSkipReason(target, groupID, issuedAt); reason != "" {
				detail, _ := json.Marshal(map[string]any{"subscription_id": targetID, "grant_id": grantID, "payment_order_id": o.ID, "reason": reason})
				if _, err := tx.Client().PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(o.ID, 10)).SetAction("RESET_CARD_AUTO_USE_NOT_PERFORMED").SetDetail(string(detail)).SetOperator("system").Save(txCtx); err != nil {
					return fmt.Errorf("record reset card auto-use skip audit: %w", err)
				}
			} else {
				if err := consumeNewResetCardGrantAndReset(txCtx, tx.Client(), grantID, o.ID, targetID, o.UserID, groupID, issuedAt); err != nil {
					return err
				}
				detail, _ := json.Marshal(map[string]any{"subscription_id": targetID, "grant_id": grantID, "payment_order_id": o.ID, "quantity_consumed": 1})
				if _, err := tx.Client().PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(o.ID, 10)).SetAction("RESET_CARD_AUTO_USED").SetDetail(string(detail)).SetOperator("system").Save(txCtx); err != nil {
					return fmt.Errorf("record reset card auto-use audit: %w", err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset card fulfillment tx: %w", err)
	}
	if s.subscriptionSvc != nil && groupID > 0 {
		_ = s.subscriptionSvc.invalidateSubscriptionCaches(o.UserID, groupID)
	}
	return s.markCompleted(ctx, o, lease, "RESET_CARD_SUCCESS")
}

type resetCardFulfillmentTarget struct {
	groupID    int64
	status     string
	expiresAt  time.Time
	notDeleted bool
}

func lockResetCardFulfillmentTarget(ctx context.Context, client *dbent.Client, subscriptionID, userID int64) (resetCardFulfillmentTarget, error) {
	if client == nil {
		return resetCardFulfillmentTarget{}, errors.New("reset card subscription client is unavailable")
	}
	query := `SELECT group_id,status,expires_at,deleted_at IS NULL FROM user_subscriptions WHERE id=$1 AND user_id=$2`
	if client.Driver().Dialect() == dialect.Postgres {
		query += " FOR UPDATE"
	}
	rows, err := client.QueryContext(ctx, query, subscriptionID, userID)
	if err != nil {
		return resetCardFulfillmentTarget{}, fmt.Errorf("lock reset card subscription: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return resetCardFulfillmentTarget{}, fmt.Errorf("iterate reset card subscription: %w", err)
		}
		return resetCardFulfillmentTarget{}, errors.New("reset card subscription is unavailable")
	}
	var target resetCardFulfillmentTarget
	if err := rows.Scan(&target.groupID, &target.status, &target.expiresAt, &target.notDeleted); err != nil {
		return resetCardFulfillmentTarget{}, fmt.Errorf("scan reset card subscription: %w", err)
	}
	if err := rows.Err(); err != nil {
		return resetCardFulfillmentTarget{}, fmt.Errorf("iterate reset card subscription: %w", err)
	}
	return target, nil
}

func resetCardAutoUseSkipReason(target resetCardFulfillmentTarget, expectedGroupID int64, now time.Time) string {
	if !target.notDeleted {
		return "subscription_deleted"
	}
	if target.groupID != expectedGroupID {
		return "subscription_group_changed"
	}
	if target.status != SubscriptionStatusActive {
		return "subscription_not_active"
	}
	if !target.expiresAt.After(now) {
		return "subscription_expired"
	}
	return ""
}

func consumeNewResetCardGrantAndReset(ctx context.Context, client *dbent.Client, grantID, orderID, subscriptionID, userID, groupID int64, now time.Time) error {
	if client == nil {
		return errors.New("reset card fulfillment client is unavailable")
	}
	result, err := client.ExecContext(ctx, `
		UPDATE subscription_reset_grants
		SET used_count = used_count + 1, updated_at = $2
		WHERE id = $1 AND payment_order_id = $3 AND subscription_id = $4
			AND user_id = $5 AND group_id = $6 AND used_count = 0
			AND quantity >= 1 AND expires_at > $2
	`, grantID, now, orderID, subscriptionID, userID, groupID)
	if err != nil {
		return fmt.Errorf("consume newly granted reset card: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read newly granted reset card consumption count: %w", err)
	}
	if affected != 1 {
		return errors.New("newly granted reset card is unavailable for automatic use")
	}
	windowStart := startOfDay(now)
	result, err = client.ExecContext(ctx, `
		UPDATE user_subscriptions
		SET daily_usage_usd = 0,
			weekly_usage_usd = 0,
			monthly_usage_usd = 0,
			daily_window_start = $2,
			weekly_window_start = $2,
			monthly_window_start = $2,
			updated_at = $3
		WHERE id = $1 AND user_id = $4 AND group_id = $5
			AND deleted_at IS NULL AND status = $6 AND expires_at > $3
	`, subscriptionID, windowStart, now, userID, groupID, SubscriptionStatusActive)
	if err != nil {
		return fmt.Errorf("reset subscription usage with newly granted reset card: %w", err)
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read subscription auto-use reset count: %w", err)
	}
	if affected != 1 {
		return errors.New("subscription became unavailable for reset card automatic use")
	}
	return nil
}

func validateResetCardAutoUseEvidence(ctx context.Context, client *dbent.Client, orderID int64) error {
	if client == nil {
		return errors.New("reset card audit client is unavailable")
	}
	orderIDText := strconv.FormatInt(orderID, 10)
	used, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(orderIDText),
		paymentauditlog.ActionEQ("RESET_CARD_AUTO_USED"),
	).Exist(ctx)
	if err != nil {
		return fmt.Errorf("check reset card automatic-use audit: %w", err)
	}
	skipped, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(orderIDText),
		paymentauditlog.ActionEQ("RESET_CARD_AUTO_USE_NOT_PERFORMED"),
	).Exist(ctx)
	if err != nil {
		return fmt.Errorf("check reset card auto-use skip audit: %w", err)
	}
	if used == skipped {
		return errors.New("reset card automatic-use evidence is incomplete")
	}
	return nil
}

func resetCardPaymentOrderGrantExpiry(o *dbent.PaymentOrder, now time.Time) (time.Time, error) {
	if o == nil || o.ProductSnapshot == nil {
		return time.Time{}, errors.New("reset card order is missing product snapshot")
	}
	policy, _ := o.ProductSnapshot["grant_expiry_policy"].(string)
	var expiresAt time.Time
	if policy == "paid_duration" {
		days, valid := paymentSnapshotInt64(o.ProductSnapshot["grant_validity_days"])
		if !valid || days != resetCardPurchasedValidityDays || o.PaidAt == nil || o.PaidAt.Before(o.CreatedAt) {
			return time.Time{}, errors.New("reset card order has invalid paid validity terms")
		}
		expiresAt = o.PaidAt.Add(time.Duration(days) * 24 * time.Hour)
	} else {
		// Preserve historical purchases exactly; never reinterpret their term.
		raw, _ := o.ProductSnapshot["subscription_expires_at"].(string)
		var err error
		expiresAt, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
		if (policy != "" && policy != "subscription") || err != nil || !expiresAt.After(o.CreatedAt) {
			return time.Time{}, errors.New("reset card order has an invalid subscription expiry snapshot")
		}
	}
	if !expiresAt.After(now) {
		return time.Time{}, errResetCardGrantExpirySnapshotElapsed
	}
	return expiresAt, nil
}

func isResetCardGrantExpiryManualReview(order *dbent.PaymentOrder) bool {
	return order != nil &&
		order.Status == OrderStatusFailed &&
		order.FailedReason != nil &&
		*order.FailedReason == resetCardGrantExpiryManualReviewReason
}

func validateResetCardPaymentOrderSnapshot(o *dbent.PaymentOrder) (int64, int64, error) {
	if o == nil || o.ProductSnapshot == nil {
		return 0, 0, errors.New("reset card order is missing product snapshot")
	}
	providerSnapshot := psOrderProviderSnapshot(o)
	if o.OrderType != payment.OrderTypeResetCard ||
		(o.PaymentType != payment.TypeAlipay && o.PaymentType != payment.TypeWxpay) ||
		providerSnapshot == nil ||
		!strings.EqualFold(strings.TrimSpace(providerSnapshot.Currency), payment.DefaultPaymentCurrency) {
		return 0, 0, errors.New("reset card order has an invalid payment identity")
	}
	kind, _ := o.ProductSnapshot["kind"].(string)
	quantity, quantityOK := paymentSnapshotInt64(o.ProductSnapshot["quantity"])
	price, priceOK := paymentSnapshotFloat(o.ProductSnapshot["price"])
	orderAmount, orderAmountOK := paymentSnapshotFloat(o.ProductSnapshot["order_amount"])
	snapshotPayAmount, payAmountOK := paymentSnapshotFloat(o.ProductSnapshot["pay_amount"])
	terms, termsErr := resetCardSnapshotPurchaseTerms(o.ProductSnapshot)
	amountsMatch := false
	if termsErr == nil {
		if terms.legacy {
			amountsMatch = math.Abs(price-o.Amount) < paymentAmountZeroTolerance(payment.DefaultPaymentCurrency) &&
				math.Abs(orderAmount-o.Amount) < paymentAmountZeroTolerance(payment.DefaultPaymentCurrency) &&
				math.Abs(snapshotPayAmount-o.PayAmount) < paymentAmountZeroTolerance(payment.DefaultPaymentCurrency)
		} else {
			priceMinor, priceErr := resetCardAmountToMinorUnit(price)
			orderMinor, orderErr := resetCardAmountToMinorUnit(orderAmount)
			amountMinor, amountErr := resetCardAmountToMinorUnit(o.Amount)
			snapshotPayMinor, snapshotPayErr := resetCardAmountToMinorUnit(snapshotPayAmount)
			payMinor, payErr := resetCardAmountToMinorUnit(o.PayAmount)
			amountsMatch = priceErr == nil && orderErr == nil && amountErr == nil && snapshotPayErr == nil && payErr == nil &&
				priceMinor == terms.totalMinor && orderMinor == terms.totalMinor && amountMinor == terms.totalMinor && snapshotPayMinor == payMinor
		}
	}
	currency, _ := o.ProductSnapshot["currency"].(string)
	expiryPolicy, _ := o.ProductSnapshot["grant_expiry_policy"].(string)
	expiresAtText, _ := o.ProductSnapshot["subscription_expires_at"].(string)
	expiresAt, expiresAtErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(expiresAtText))
	idempotencyHash, _ := o.ProductSnapshot["idempotency_key_sha256"].(string)
	if kind != "reset_card" || !quantityOK || termsErr != nil || quantity != int64(terms.quantity) || !priceOK || !orderAmountOK || !payAmountOK ||
		!strings.EqualFold(strings.TrimSpace(currency), payment.DefaultPaymentCurrency) ||
		!validResetCardExpiryPolicy(o.ProductSnapshot, expiryPolicy) || expiresAtErr != nil || !expiresAt.After(o.CreatedAt) ||
		!amountsMatch {
		return 0, 0, errors.New("reset card order has an invalid product snapshot")
	}
	normalizedHash, err := normalizeResetCardIdempotencyKeyHash(idempotencyHash)
	if err != nil || o.OutTradeNo != resetCardOrderOutTradeNo(o.UserID, normalizedHash) {
		return 0, 0, errors.New("reset card order has an invalid idempotency snapshot")
	}
	targetID, ok := paymentSnapshotInt64(o.ProductSnapshot["subscription_id"])
	if !ok || targetID <= 0 {
		return 0, 0, errors.New("reset card order is missing target subscription")
	}
	groupID, groupOK := paymentSnapshotInt64(o.ProductSnapshot["group_id"])
	planID, planOK := paymentSnapshotInt64(o.ProductSnapshot["plan_id"])
	if !groupOK || groupID <= 0 || !planOK || planID <= 0 || o.PlanID == nil || *o.PlanID != planID ||
		o.SubscriptionGroupID == nil || *o.SubscriptionGroupID != groupID {
		return 0, 0, errors.New("reset card order has an invalid target identity")
	}
	if _, err := resetCardTierSnapshotFromProductSnapshot(o.ProductSnapshot, planID); err != nil {
		return 0, 0, err
	}
	return targetID, groupID, nil
}

func validResetCardExpiryPolicy(snapshot map[string]any, policy string) bool {
	if policy == "subscription" {
		return true
	}
	version, versionOK := paymentSnapshotInt64(snapshot["schema_version"])
	days, daysOK := paymentSnapshotInt64(snapshot["grant_validity_days"])
	return policy == "paid_duration" && versionOK && version == 3 && daysOK && days == resetCardPurchasedValidityDays
}

func resetCardTierSnapshotForPaymentOrder(order *dbent.PaymentOrder) (*SubscriptionResetCardTierSnapshot, error) {
	if order == nil || order.PlanID == nil || *order.PlanID <= 0 {
		return nil, errors.New("reset card order is missing plan identity")
	}
	return resetCardTierSnapshotFromProductSnapshot(order.ProductSnapshot, *order.PlanID)
}

func paymentSnapshotInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), v == float64(int64(v))
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	default:
		return 0, false
	}
}

func (s *PaymentService) ExecuteBalanceFulfillment(ctx context.Context, oid int64) error {
	return s.executeBalanceFulfillmentWithLeaseAcquirer(ctx, oid, s.acquireNormalPaymentFulfillmentLease)
}

func (s *PaymentService) executeBalanceFulfillmentWithLeaseAcquirer(ctx context.Context, oid int64, acquire paymentFulfillmentLeaseAcquirer) error {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.Status == OrderStatusCompleted {
		return nil
	}
	if psIsRefundStatus(o.Status) {
		return infraerrors.BadRequest("INVALID_STATUS", "refund-related order cannot fulfill")
	}
	if o.Status != OrderStatusPaid && o.Status != OrderStatusFailed && o.Status != OrderStatusRecharging {
		return infraerrors.BadRequest("INVALID_STATUS", "order cannot fulfill in status "+o.Status)
	}
	claimedOrder, lease, err := acquire(ctx, o)
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	if claimedOrder == nil {
		return errors.New("acquired fulfillment lease without payment order")
	}
	if err := s.doBalance(ctx, claimedOrder, lease); err != nil {
		s.markFailed(ctx, oid, lease, err)
		return err
	}
	return nil
}

func (s *PaymentService) acquireNormalPaymentFulfillmentLease(ctx context.Context, o *dbent.PaymentOrder) (*dbent.PaymentOrder, *paymentFulfillmentLease, error) {
	lease, err := s.acquirePaymentFulfillmentLease(ctx, o)
	return o, lease, err
}

func (s *PaymentService) acquirePaymentFulfillmentLease(ctx context.Context, o *dbent.PaymentOrder) (*paymentFulfillmentLease, error) {
	return acquirePaymentFulfillmentLeaseWithClient(ctx, s.entClient, o)
}

func acquirePaymentFulfillmentLeaseWithClient(ctx context.Context, client *dbent.Client, o *dbent.PaymentOrder) (*paymentFulfillmentLease, error) {
	if isPaymentDiscountManualReview(o) {
		return nil, infraerrors.Conflict("COUPON_MANUAL_REVIEW", "coupon payment requires manual review")
	}
	if o == nil {
		return nil, infraerrors.BadRequest("INVALID_STATUS", "nil payment order")
	}
	if client == nil {
		return nil, errors.New("payment fulfillment lease requires an order store")
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	staleBefore := now.Add(-paymentFulfillmentLeaseDuration)
	updated, err := client.PaymentOrder.Update().
		Where(
			paymentorder.IDEQ(o.ID),
			paymentorder.PaidAtNotNil(),
			paymentorder.Or(paymentorder.FailedReasonIsNil(), paymentorder.FailedReasonNEQ(paymentDiscountManualReviewReason)),
			paymentorder.Or(
				paymentorder.StatusIn(OrderStatusPaid, OrderStatusFailed),
				paymentorder.And(
					paymentorder.StatusEQ(OrderStatusRecharging),
					paymentorder.UpdatedAtLTE(staleBefore),
				),
			),
		).
		SetStatus(OrderStatusRecharging).
		SetUpdatedAt(now).
		ClearFailedAt().
		ClearFailedReason().
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire fulfillment lease: %w", err)
	}
	if updated == 0 {
		current, getErr := client.PaymentOrder.Get(ctx, o.ID)
		if getErr != nil {
			return nil, fmt.Errorf("reload fulfillment lease: %w", getErr)
		}
		if current.Status == OrderStatusCompleted {
			return nil, nil
		}
		if current.Status == OrderStatusRecharging {
			return nil, infraerrors.Conflict("CONFLICT", "order is being processed")
		}
		return nil, infraerrors.Conflict("CONFLICT", "order status changed while acquiring fulfillment lease")
	}

	// Reload the persisted timestamp instead of trusting application clock precision.
	claimed, err := client.PaymentOrder.Get(ctx, o.ID)
	if err != nil {
		return nil, fmt.Errorf("reload acquired fulfillment lease: %w", err)
	}
	if claimed.Status != OrderStatusRecharging || !claimed.UpdatedAt.Equal(now) {
		return nil, infraerrors.Conflict("CONFLICT", "fulfillment lease was lost")
	}
	return &paymentFulfillmentLease{version: now}, nil
}

// redeemAction represents the idempotency decision for balance fulfillment.
type redeemAction int

const (
	// redeemActionCreate: code does not exist — create it, then redeem.
	redeemActionCreate redeemAction = iota
	// redeemActionRedeem: code exists but is unused — skip creation, redeem only.
	redeemActionRedeem
	// redeemActionSkipCompleted: code exists and is already used — skip to mark completed.
	redeemActionSkipCompleted
)

// resolveRedeemAction decides the idempotency action based on an existing redeem code lookup.
// existing is the result of GetByCode; lookupErr is the error from that call.
func resolveRedeemAction(existing *RedeemCode, lookupErr error) (redeemAction, error) {
	if lookupErr != nil {
		if errors.Is(lookupErr, ErrRedeemCodeNotFound) {
			return redeemActionCreate, nil
		}
		return redeemActionCreate, fmt.Errorf("lookup payment redeem code: %w", lookupErr)
	}
	if existing == nil {
		return redeemActionCreate, nil
	}
	if existing.IsUsed() {
		return redeemActionSkipCompleted, nil
	}
	return redeemActionRedeem, nil
}

func validatePaymentRedeemCode(o *dbent.PaymentOrder, code *RedeemCode) error {
	if o == nil || code == nil {
		return errors.New("payment redeem code validation requires an order and code")
	}
	if code.Code != o.RechargeCode {
		return fmt.Errorf("payment redeem code mismatch for order %d", o.ID)
	}
	if code.Type != RedeemTypeBalance {
		return fmt.Errorf("payment redeem code type mismatch for order %d: got %s", o.ID, code.Type)
	}
	if math.IsNaN(code.Value) || math.IsInf(code.Value, 0) || math.Abs(code.Value-o.Amount) > 1e-8 {
		return fmt.Errorf("payment redeem code amount mismatch for order %d: expected %.8f, got %.8f", o.ID, o.Amount, code.Value)
	}
	switch code.Status {
	case StatusUnused:
		if code.UsedBy != nil {
			return fmt.Errorf("unused payment redeem code has a user for order %d", o.ID)
		}
	case StatusUsed:
		if code.UsedBy == nil || *code.UsedBy != o.UserID {
			return fmt.Errorf("payment redeem code user mismatch for order %d", o.ID)
		}
	default:
		return fmt.Errorf("payment redeem code has invalid status for order %d: %s", o.ID, code.Status)
	}
	return nil
}

func (s *PaymentService) doBalance(ctx context.Context, o *dbent.PaymentOrder, lease *paymentFulfillmentLease) error {
	redeemCtx := ctx
	funding, err := PaymentWalletFundingForOrder(o)
	if err != nil {
		if paymentWalletFundingRequired(o) {
			return fmt.Errorf("prepare payment wallet funding: %w", err)
		}
		// Historical orders without an immutable product snapshot must still
		// fulfill, but their credit remains unattributed and therefore manual-only
		// for refunds.
		slog.Warn("balance payment fulfilled without refund provenance", "orderID", o.ID, "error", err)
	} else {
		// Keep the provenance marker scoped to the balance-credit transaction.
		// Later fulfillment work can credit an affiliate or another user and must
		// never inherit this order's paid-principal classification.
		redeemCtx = ContextWithPaymentWalletFunding(ctx, funding)
	}
	// Idempotency: check if redeem code already exists (from a previous partial run)
	existing, lookupErr := s.redeemService.GetByCode(ctx, o.RechargeCode)
	action, err := resolveRedeemAction(existing, lookupErr)
	if err != nil {
		return err
	}
	if existing != nil {
		if err := validatePaymentRedeemCode(o, existing); err != nil {
			return err
		}
	}

	switch action {
	case redeemActionSkipCompleted:
		if err := s.grantPaymentOrderConcurrency(ctx, o); err != nil {
			return err
		}
		s.invalidatePaymentAuthCache(ctx, o.UserID)
		if err := s.applyAffiliateRebateForOrder(ctx, o); err != nil {
			return err
		}
		// Code already created and redeemed — just mark completed
		return s.markCompleted(ctx, o, lease, "RECHARGE_SUCCESS")
	case redeemActionCreate:
		rc := &RedeemCode{Code: o.RechargeCode, Type: RedeemTypeBalance, Value: o.Amount, Status: StatusUnused}
		if err := s.redeemService.CreateCode(ctx, rc); err != nil {
			return fmt.Errorf("create redeem code: %w", err)
		}
	case redeemActionRedeem:
		// Code exists but unused — skip creation, proceed to redeem
	}
	if _, err := s.redeemService.redeemForPaymentFulfillment(redeemCtx, o.UserID, o.RechargeCode); err != nil {
		return fmt.Errorf("redeem balance: %w", err)
	}
	if err := s.grantPaymentOrderConcurrency(ctx, o); err != nil {
		return err
	}
	s.invalidatePaymentAuthCache(ctx, o.UserID)
	if err := s.applyAffiliateRebateForOrder(ctx, o); err != nil {
		return err
	}
	return s.markCompleted(ctx, o, lease, "RECHARGE_SUCCESS")
}

func (s *PaymentService) markCompleted(ctx context.Context, o *dbent.PaymentOrder, lease *paymentFulfillmentLease, auditAction string) error {
	if lease == nil {
		return errors.New("missing payment fulfillment lease")
	}
	now := time.Now()
	updated, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(o.ID),
		paymentorder.StatusEQ(OrderStatusRecharging),
		paymentorder.UpdatedAtEQ(lease.version),
	).SetStatus(OrderStatusCompleted).SetCompletedAt(now).Save(ctx)
	if err != nil {
		return fmt.Errorf("mark completed: %w", err)
	}
	if updated == 0 {
		current, getErr := s.entClient.PaymentOrder.Get(ctx, o.ID)
		if getErr == nil && current.Status == OrderStatusCompleted {
			return nil
		}
		return infraerrors.Conflict("CONFLICT", "fulfillment lease was lost before completion")
	}
	if !s.hasAuditLog(ctx, o.ID, auditAction) {
		s.writeAuditLog(ctx, o.ID, auditAction, "system", map[string]any{
			"rechargeCode":   o.RechargeCode,
			"creditedAmount": o.Amount,
			"payAmount":      o.PayAmount,
		})
		s.dispatchPaymentFulfillmentNotification(o, auditAction)
	}
	return nil
}

func (s *PaymentService) dispatchPaymentFulfillmentNotification(o *dbent.PaymentOrder, auditAction string) {
	if s == nil || s.notificationEmailService == nil || o == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
		defer cancel()
		var err error
		switch auditAction {
		case "RECHARGE_SUCCESS":
			err = s.sendBalanceRechargeSuccessNotification(ctx, o)
		case "SUBSCRIPTION_SUCCESS":
			err = s.sendSubscriptionPurchaseSuccessNotification(ctx, o)
		default:
			return
		}
		if err != nil {
			slog.Warn("payment fulfillment notification email failed", "order_id", o.ID, "action", auditAction, "err", err.Error())
		}
	}()
}

func (s *PaymentService) sendBalanceRechargeSuccessNotification(ctx context.Context, o *dbent.PaymentOrder) error {
	currentBalance := ""
	currentConcurrency := ""
	if s.userRepo != nil {
		if user, err := s.userRepo.GetByID(ctx, o.UserID); err == nil && user != nil {
			currentBalance = fmt.Sprintf("%.2f", user.Balance)
			currentConcurrency = strconv.Itoa(user.Concurrency)
		}
	}
	return s.notificationEmailService.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventBalanceRechargeSuccess,
		RecipientEmail: o.UserEmail,
		RecipientName:  firstNonEmpty(o.UserName, o.UserEmail),
		UserID:         o.UserID,
		SourceType:     "payment_order",
		SourceID:       strconv.FormatInt(o.ID, 10),
		Variables: map[string]string{
			"recharge_amount": fmt.Sprintf("%.2f", o.Amount),
			"current_balance": currentBalance,
			"concurrency":     currentConcurrency,
			"order_id":        strconv.FormatInt(o.ID, 10),
		},
	})
}

func (s *PaymentService) sendSubscriptionPurchaseSuccessNotification(ctx context.Context, o *dbent.PaymentOrder) error {
	variables := map[string]string{
		"subscription_group": "Subscription",
		"subscription_days":  "",
		"expiry_time":        "",
		"balance_bonus":      "0.00",
		"reset_card_count":   "0",
		"concurrency":        strconv.Itoa(paymentOrderEntitlements(o).Concurrency),
		"order_id":           strconv.FormatInt(o.ID, 10),
	}
	entitlements := paymentOrderEntitlements(o)
	variables["balance_bonus"] = fmt.Sprintf("%.2f", entitlements.BalanceBonus)
	variables["reset_card_count"] = strconv.Itoa(entitlements.ResetCardCount)
	if o.SubscriptionDays != nil {
		variables["subscription_days"] = strconv.Itoa(*o.SubscriptionDays)
	}
	if o.SubscriptionGroupID != nil {
		if s.groupRepo != nil {
			if group, err := s.groupRepo.GetByID(ctx, *o.SubscriptionGroupID); err == nil && group != nil && strings.TrimSpace(group.Name) != "" {
				variables["subscription_group"] = group.Name
			}
		}
		if s.subscriptionSvc != nil {
			if sub, err := s.subscriptionSvc.GetActiveSubscription(ctx, o.UserID, *o.SubscriptionGroupID); err == nil && sub != nil {
				variables["expiry_time"] = sub.ExpiresAt.Format("2006-01-02 15:04")
			}
		}
	}
	return s.notificationEmailService.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventSubscriptionPurchaseSuccess,
		RecipientEmail: o.UserEmail,
		RecipientName:  firstNonEmpty(o.UserName, o.UserEmail),
		UserID:         o.UserID,
		SourceType:     "payment_order",
		SourceID:       strconv.FormatInt(o.ID, 10),
		Variables:      variables,
	})
}

func (s *PaymentService) ExecuteSubscriptionFulfillment(ctx context.Context, oid int64) error {
	return s.executeSubscriptionFulfillmentWithLeaseAcquirer(ctx, oid, s.acquireNormalPaymentFulfillmentLease)
}

func (s *PaymentService) executeSubscriptionFulfillmentWithLeaseAcquirer(ctx context.Context, oid int64, acquire paymentFulfillmentLeaseAcquirer) error {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.Status == OrderStatusCompleted {
		return nil
	}
	if psIsRefundStatus(o.Status) {
		return infraerrors.BadRequest("INVALID_STATUS", "refund-related order cannot fulfill")
	}
	if o.Status != OrderStatusPaid && o.Status != OrderStatusFailed && o.Status != OrderStatusRecharging {
		return infraerrors.BadRequest("INVALID_STATUS", "order cannot fulfill in status "+o.Status)
	}
	if o.SubscriptionGroupID == nil || o.SubscriptionDays == nil {
		return infraerrors.BadRequest("INVALID_STATUS", "missing subscription info")
	}
	claimedOrder, lease, err := acquire(ctx, o)
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	if claimedOrder == nil {
		return errors.New("acquired fulfillment lease without payment order")
	}
	if err := s.doSub(ctx, claimedOrder, lease); err != nil {
		s.markFailed(ctx, oid, lease, err)
		return err
	}
	return nil
}

func (s *PaymentService) doSub(ctx context.Context, o *dbent.PaymentOrder, lease *paymentFulfillmentLease) error {
	gid := *o.SubscriptionGroupID
	days := *o.SubscriptionDays
	g, err := s.groupRepo.GetByID(ctx, gid)
	if err != nil || g.Status != payment.EntityStatusActive {
		return fmt.Errorf("group %d no longer exists or inactive", gid)
	}
	if err := s.ensurePaymentSubscriptionAssigned(ctx, o, gid, days); err != nil {
		return err
	}
	if err := s.applyAffiliateRebateForOrder(ctx, o); err != nil {
		return err
	}
	return s.markCompleted(ctx, o, lease, "SUBSCRIPTION_SUCCESS")
}

func (s *PaymentService) ensurePaymentSubscriptionAssigned(ctx context.Context, o *dbent.PaymentOrder, groupID int64, days int) error {
	if s.subscriptionSvc == nil {
		return errors.New("subscription service is unavailable")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin subscription fulfillment tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	txClient := tx.Client()
	alreadyAssigned, err := hasPaymentSubscriptionAssignmentAudit(txCtx, txClient, o.ID)
	if err != nil {
		return fmt.Errorf("check subscription assignment audit: %w", err)
	}

	recoveredFromNote := false
	var assignedSubscription *UserSubscription
	if !alreadyAssigned {
		orderNote := paymentSubscriptionOrderNote(o.ID)
		existing, lookupErr := s.subscriptionSvc.userSubRepo.GetByUserIDAndGroupID(txCtx, o.UserID, groupID)
		switch {
		case lookupErr == nil && existing != nil && hasPaymentSubscriptionOrderNote(existing.Notes, orderNote):
			recoveredFromNote = true
		case lookupErr != nil && !errors.Is(lookupErr, ErrSubscriptionNotFound):
			return fmt.Errorf("check existing subscription assignment: %w", lookupErr)
		default:
			// The assignment operation returns the term start observed while holding
			// the subscription row lock. A preliminary lookup can become stale when
			// another payment creates or renews the same subscription concurrently.
			var termStart time.Time
			assignedSubscription, _, termStart, err = s.subscriptionSvc.assignOrExtendSubscription(txCtx, &AssignSubscriptionInput{
				UserID:       o.UserID,
				GroupID:      groupID,
				ValidityDays: days,
				AssignedBy:   0,
				Notes:        orderNote,
			}, true)
			if err != nil {
				return fmt.Errorf("assign subscription: %w", err)
			}
			if err := insertPaymentSubscriptionGrant(txCtx, txClient, o, assignedSubscription, termStart); err != nil {
				return fmt.Errorf("record subscription term grant: %w", err)
			}
		}

		detail, _ := json.Marshal(map[string]any{
			"groupID":           groupID,
			"validityDays":      days,
			"recoveredFromNote": recoveredFromNote,
		})
		if _, err := txClient.PaymentAuditLog.Create().
			SetOrderID(strconv.FormatInt(o.ID, 10)).
			SetAction("SUBSCRIPTION_ASSIGNED").
			SetDetail(string(detail)).
			SetOperator("system").
			Save(txCtx); err != nil {
			if dbent.IsConstraintError(err) {
				_ = tx.Rollback()
				claimed, checkErr := hasPaymentSubscriptionAssignmentAudit(ctx, s.entClient, o.ID)
				if checkErr == nil && claimed {
					return s.subscriptionSvc.invalidateSubscriptionCaches(o.UserID, groupID)
				}
			}
			return fmt.Errorf("record subscription assignment audit: %w", err)
		}
	} else {
		slog.Info("subscription already assigned for order, skipping", "orderID", o.ID, "groupID", groupID)
	}
	grant, grantedSubscription, grantErr := loadPaymentSubscriptionRefundState(txCtx, txClient, o.ID, false)
	if grantErr != nil && !errors.Is(grantErr, errRefundAccountingMissing) {
		return fmt.Errorf("load exact payment subscription grant: %w", grantErr)
	}
	if err := grantPaymentProductEntitlementsForSubscriptionGrantWithConcurrencyAuthorizationFence(
		txCtx,
		txClient,
		o,
		groupID,
		grant,
		grantedSubscription,
		s.concurrencyAuthorizationFence,
	); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit subscription fulfillment tx: %w", err)
	}
	// Assignment cache invalidation is deferred while this transaction is open,
	// then performed synchronously against the committed subscription.
	if err := s.subscriptionSvc.invalidateSubscriptionCaches(o.UserID, groupID); err != nil {
		// The entitlement transaction has already committed. Cache convergence is
		// recoverable; returning an error here would incorrectly mark a paid,
		// fulfilled order as FAILED and make retries loop on Redis outages.
		slog.Warn("subscription cache invalidation after payment fulfillment failed", "orderID", o.ID, "userID", o.UserID, "groupID", groupID, "err", err)
	}
	s.invalidatePaymentAuthCache(ctx, o.UserID)
	return nil
}

func (s *PaymentService) invalidatePaymentAuthCache(ctx context.Context, userID int64) {
	if s != nil && s.authCacheInvalidator != nil && userID > 0 {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
}

func grantPaymentProductEntitlements(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, groupID int64) error {
	return grantPaymentProductEntitlementsForSubscriptionGrant(ctx, client, order, groupID, nil, nil)
}

// grantPaymentProductEntitlementsForSubscriptionGrant applies an order's
// immutable benefits. Payment fulfillment always supplies the exact refund
// provenance row; the compatibility wrapper above remains only for historical
// direct callers and old focused tests.
func grantPaymentProductEntitlementsForSubscriptionGrant(
	ctx context.Context,
	client *dbent.Client,
	order *dbent.PaymentOrder,
	groupID int64,
	exactGrant *paymentSubscriptionGrant,
	exactSubscription *dbent.UserSubscription,
) error {
	return grantPaymentProductEntitlementsForSubscriptionGrantWithConcurrencyAuthorizationFence(
		ctx, client, order, groupID, exactGrant, exactSubscription, nil,
	)
}

// grantPaymentProductEntitlementsForSubscriptionGrantWithConcurrencyAuthorizationFence
// keeps the user-row lock obtained by createPaymentRefundBenefitSource and
// attaches a post-commit projection marker before a source-backed cap change.
// The public compatibility wrapper intentionally passes nil for old direct
// callers and focused dialect tests.
func grantPaymentProductEntitlementsForSubscriptionGrantWithConcurrencyAuthorizationFence(
	ctx context.Context,
	client *dbent.Client,
	order *dbent.PaymentOrder,
	groupID int64,
	exactGrant *paymentSubscriptionGrant,
	exactSubscription *dbent.UserSubscription,
	fence *ConcurrencyService,
) error {
	if order == nil || client == nil {
		return nil
	}
	entitlements, err := paymentOrderEntitlementsStrict(order)
	if err != nil {
		return err
	}
	claimed, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ("SUBSCRIPTION_BENEFITS_GRANTED"),
		).
		Limit(1).Exist(ctx)
	if err != nil {
		return fmt.Errorf("check subscription benefits audit: %w", err)
	}
	if claimed {
		// The audit keeps balance, concurrency, and immediate benefits
		// idempotent. Monthly reset cards are a separate durable calendar
		// ledger, so a retried fulfillment must still reconcile its current
		// window without replaying the order-level benefits.
		if err := reconcileMonthlyResetCardScheduleForFulfillment(
			ctx,
			client,
			order,
			exactGrant,
			exactSubscription,
			entitlements,
			time.Now().UTC(),
		); err != nil {
			return fmt.Errorf("reconcile monthly reset-card entitlement after prior benefits audit: %w", err)
		}
		return nil
	}
	if entitlements.BalanceBonus <= 0 && entitlements.ResetCardCount <= 0 && entitlements.Concurrency <= 0 {
		return nil
	}
	benefitSource, err := createPaymentRefundBenefitSource(ctx, client, order, entitlements, exactGrant, exactSubscription)
	if err != nil {
		return fmt.Errorf("record payment refund benefit source: %w", err)
	}
	if entitlements.Concurrency > 0 {
		if benefitSource != nil {
			if _, err := BeginUserConcurrencyAuthorizationFenceMutationAfterUserLock(ctx, fence, order.UserID); err != nil {
				return fmt.Errorf("begin source-backed subscription concurrency authorization fence: %w", err)
			}
			before, after, err := applyPaymentRefundBenefitConcurrencyMax(ctx, client, order.UserID, int64(entitlements.Concurrency), benefitSource.ID)
			if err != nil {
				return fmt.Errorf("grant source-backed subscription concurrency: %w", err)
			}
			if benefitSource.ConcurrencyBefore == nil || benefitSource.ConcurrencyAfterGrant == nil ||
				*benefitSource.ConcurrencyBefore != before || *benefitSource.ConcurrencyAfterGrant != after {
				return fmt.Errorf("payment concurrency source snapshot mismatch")
			}
		} else if err := setPaymentUserConcurrencyAtLeast(ctx, client, order.UserID, entitlements.Concurrency); err != nil {
			return fmt.Errorf("grant subscription concurrency: %w", err)
		}
	}
	if entitlements.BalanceBonus > 0 {
		if err := client.User.UpdateOneID(order.UserID).
			AddBalance(entitlements.BalanceBonus).
			AddTotalRecharged(entitlements.BalanceBonus).
			Exec(ctx); err != nil {
			return fmt.Errorf("grant subscription balance bonus: %w", err)
		}
	}
	if entitlements.ResetCardCount > 0 {
		now := time.Now().UTC()
		if entitlements.ResetCardDeliveryMode == resetCardDeliveryModeMonthly {
			// A renewal term starts in the future. Its first card must wait for its
			// own anchor even though the encompassing subscription is active now.
			// Conversely, a paid order with a started term already contains an
			// immutable entitlement promise. The rollout gate controls new orders
			// and the future worker, not this first fulfillment occurrence.
			if err := reconcileMonthlyResetCardScheduleForFulfillment(
				ctx,
				client,
				order,
				exactGrant,
				exactSubscription,
				entitlements,
				now,
			); err != nil {
				return fmt.Errorf("issue monthly reset-card entitlement: %w", err)
			}
		} else {
			tierSnapshot, err := resetCardTierSnapshotForSubscriptionEntitlementOrder(order)
			if err != nil {
				return err
			}
			familyKey, tierRank, sourcePlanID := resetCardTierSnapshotValues(tierSnapshot)
			if exactGrant != nil && exactSubscription != nil {
				if exactGrant.SubscriptionID != exactSubscription.ID || exactGrant.UserID != order.UserID || exactGrant.GroupID != groupID ||
					exactSubscription.Status != SubscriptionStatusActive || !exactSubscription.ExpiresAt.After(now) {
					return fmt.Errorf("exact subscription reset-card recipient is unavailable")
				}
				expiresAt := now.Add(time.Duration(entitlements.ResetCardValidityDays()) * 24 * time.Hour)
				_, err = client.ExecContext(ctx, `INSERT INTO subscription_reset_grants (
				subscription_id, user_id, group_id, quantity, used_count,
				expires_at, issued_by, payment_order_id, card_family_key,
				source_tier_rank, source_plan_id, tier_snapshot_resolved, created_at, updated_at
			) VALUES ($1,$2,$3,$4,0,$5,NULL,$6,$7,$8,$9,TRUE,$10,$10)`,
					exactGrant.SubscriptionID, exactGrant.UserID, exactGrant.GroupID,
					entitlements.ResetCardCount, expiresAt, order.ID, familyKey, tierRank,
					sourcePlanID, now)
				if err != nil {
					return fmt.Errorf("grant exact subscription reset cards: %w", err)
				}
			} else {
				// Older completed orders can predate payment_subscription_grants. Keep
				// their established recovery behavior, but all newly fulfilled orders
				// use the exact path above.
				expiresAt := now.Add(time.Duration(entitlements.ResetCardValidityDays()) * 24 * time.Hour)
				rows, err := client.QueryContext(ctx, `
				INSERT INTO subscription_reset_grants (
					subscription_id, user_id, group_id, quantity, used_count,
					expires_at, issued_by, payment_order_id, card_family_key,
					source_tier_rank, source_plan_id, tier_snapshot_resolved, created_at, updated_at
				)
				SELECT us.id, us.user_id, us.group_id, $3, 0, $4, NULL, $5, $6, $7, $8, TRUE, $9, $9
				FROM user_subscriptions us
				WHERE us.user_id = $1 AND us.group_id = $2
					AND us.deleted_at IS NULL AND us.status = 'active' AND us.expires_at > $9
				RETURNING id
			`, order.UserID, groupID, entitlements.ResetCardCount, expiresAt, order.ID, familyKey, tierRank, sourcePlanID, now)
				if err != nil {
					return fmt.Errorf("grant subscription reset cards: %w", err)
				}
				var grantID int64
				if rows.Next() {
					if err := rows.Scan(&grantID); err != nil {
						_ = rows.Close()
						return fmt.Errorf("scan subscription reset card grant: %w", err)
					}
				}
				if err := rows.Err(); err != nil {
					_ = rows.Close()
					return fmt.Errorf("iterate subscription reset card grant: %w", err)
				}
				if err := rows.Close(); err != nil {
					return fmt.Errorf("close subscription reset card grant: %w", err)
				}
				if grantID == 0 {
					return fmt.Errorf("subscription reset card recipient is unavailable")
				}
			}
		}
	}
	detail, _ := json.Marshal(map[string]any{
		"balanceBonus":   entitlements.BalanceBonus,
		"resetCardCount": entitlements.ResetCardCount,
		"concurrency":    entitlements.Concurrency,
	})
	if _, err := client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("SUBSCRIPTION_BENEFITS_GRANTED").
		SetDetail(string(detail)).
		SetOperator("system").
		Save(ctx); err != nil {
		return fmt.Errorf("record subscription benefits audit: %w", err)
	}
	if benefitSource != nil {
		cards, err := loadPaymentRefundBenefitResetCards(ctx, client, benefitSource.ID, false)
		if err != nil {
			return fmt.Errorf("load payment benefit source cards: %w", err)
		}
		detail, err := json.Marshal(paymentRefundBenefitSourceAuditDetail(benefitSource, cards, 0, 0))
		if err != nil {
			return fmt.Errorf("encode payment benefit source audit: %w", err)
		}
		if _, err := client.PaymentAuditLog.Create().
			SetOrderID(strconv.FormatInt(order.ID, 10)).
			SetAction("PAYMENT_BENEFIT_SOURCE_CAPTURED").
			SetDetail(string(detail)).
			SetOperator("system").
			Save(ctx); err != nil {
			return fmt.Errorf("record payment benefit source audit: %w", err)
		}
	}
	return nil
}

func resetCardTierSnapshotForSubscriptionEntitlementOrder(order *dbent.PaymentOrder) (*SubscriptionResetCardTierSnapshot, error) {
	if order == nil || order.PlanID == nil || *order.PlanID <= 0 {
		return nil, errors.New("subscription entitlement order is missing plan identity")
	}
	return resetCardTierSnapshotFromProductSnapshot(order.ProductSnapshot, *order.PlanID)
}

// grantPaymentOrderConcurrency applies a balance-order concurrency target in
// its own transaction. The audit row and user update commit together, so a
// crash can only leave a retryable order; the monotonic target update is safe
// to repeat and safe if callbacks for different tiers arrive out of order.
func (s *PaymentService) grantPaymentOrderConcurrency(ctx context.Context, order *dbent.PaymentOrder) error {
	if s == nil {
		return nil
	}
	return grantPaymentOrderConcurrencyWithConcurrencyAuthorizationFence(
		ctx, s.entClient, order, s.concurrencyAuthorizationFence,
	)
}

// grantPaymentOrderConcurrency is retained for direct compatibility callers
// that do not have a PaymentService-owned strict fence.
func grantPaymentOrderConcurrency(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder) error {
	return grantPaymentOrderConcurrencyWithConcurrencyAuthorizationFence(ctx, client, order, nil)
}

func grantPaymentOrderConcurrencyWithConcurrencyAuthorizationFence(
	ctx context.Context,
	client *dbent.Client,
	order *dbent.PaymentOrder,
	fence *ConcurrencyService,
) error {
	if client == nil || order == nil {
		return nil
	}
	entitlements, err := paymentOrderEntitlementsStrict(order)
	if err != nil {
		return err
	}
	if entitlements.Concurrency <= 0 {
		return nil
	}
	tx, err := client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin balance concurrency tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	claimed, err := tx.Client().PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ("PAYMENT_CONCURRENCY_GRANTED"),
		).
		Limit(1).Exist(txCtx)
	if err != nil {
		return fmt.Errorf("check payment concurrency audit: %w", err)
	}
	if claimed {
		return tx.Commit()
	}
	benefitSource, err := createPaymentRefundBenefitSource(txCtx, tx.Client(), order, entitlements, nil, nil)
	if err != nil {
		return fmt.Errorf("record balance payment refund benefit source: %w", err)
	}
	detail := map[string]any{"concurrency": entitlements.Concurrency}
	if benefitSource == nil {
		// P19 source evidence is required in PostgreSQL, where refunds can
		// later reclaim this paid cap. Existing non-PostgreSQL test and
		// development dialects retain the historical monotonic grant path.
		if paymentAuditDialect(tx.Client()) == dialect.Postgres {
			return errors.New("balance concurrency source snapshot is unavailable")
		}
		if err := setPaymentUserConcurrencyAtLeast(txCtx, tx.Client(), order.UserID, entitlements.Concurrency); err != nil {
			return fmt.Errorf("grant balance concurrency: %w", err)
		}
	} else {
		if _, err := BeginUserConcurrencyAuthorizationFenceMutationAfterUserLock(txCtx, fence, order.UserID); err != nil {
			return fmt.Errorf("begin source-backed balance concurrency authorization fence: %w", err)
		}
		before, after, err := applyPaymentRefundBenefitConcurrencyMax(txCtx, tx.Client(), order.UserID, int64(entitlements.Concurrency), benefitSource.ID)
		if err != nil {
			return fmt.Errorf("grant balance concurrency: %w", err)
		}
		if benefitSource.ConcurrencyBefore == nil || benefitSource.ConcurrencyAfterGrant == nil ||
			*benefitSource.ConcurrencyBefore != before || *benefitSource.ConcurrencyAfterGrant != after {
			return errors.New("balance payment concurrency source snapshot mismatch")
		}
		detail["benefit_source"] = paymentRefundBenefitSourceAuditDetail(benefitSource, nil, before, after)
	}
	detailJSON, _ := json.Marshal(detail)
	if _, err := tx.Client().PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("PAYMENT_CONCURRENCY_GRANTED").
		SetDetail(string(detailJSON)).
		SetOperator("system").Save(txCtx); err != nil {
		return fmt.Errorf("record balance concurrency audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit balance concurrency tx: %w", err)
	}
	return nil
}

// setPaymentUserConcurrencyAtLeast is atomic and monotonic: a paid target may
// raise the account cap, but a late callback for a lower tier must never reduce
// an administrator-set or later-purchased higher cap.
func setPaymentUserConcurrencyAtLeast(ctx context.Context, client *dbent.Client, userID int64, target int) error {
	if client == nil || userID <= 0 || target <= 0 {
		return nil
	}
	result, err := client.ExecContext(ctx, `
		UPDATE users
		SET concurrency = CASE WHEN concurrency < $1 THEN $1 ELSE concurrency END
		WHERE id = $2
	`, target, userID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("user %d not found while updating concurrency", userID)
	}
	return nil
}

func paymentOrderEntitlements(order *dbent.PaymentOrder) PlanEntitlements {
	entitlements, _ := paymentOrderEntitlementsStrict(order)
	return entitlements
}

func paymentOrderEntitlementsStrict(order *dbent.PaymentOrder) (PlanEntitlements, error) {
	if order == nil || order.ProductSnapshot == nil {
		return PlanEntitlements{}, nil
	}
	raw, ok := order.ProductSnapshot["entitlements"].(map[string]any)
	if !ok {
		if _, exists := order.ProductSnapshot["entitlements"]; !exists {
			return PlanEntitlements{}, nil
		}
		return PlanEntitlements{}, fmt.Errorf("invalid payment order entitlement snapshot")
	}
	_, entitlements, err := normalizePlanEntitlements(raw)
	if err != nil {
		return PlanEntitlements{}, fmt.Errorf("decode payment order entitlements: %w", err)
	}
	return entitlements, nil
}

func hasPaymentSubscriptionAssignmentAudit(ctx context.Context, client *dbent.Client, orderID int64) (bool, error) {
	count, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
			paymentauditlog.ActionIn("SUBSCRIPTION_ASSIGNED", "SUBSCRIPTION_SUCCESS"),
		).
		Limit(1).
		Count(ctx)
	return count > 0, err
}

func paymentSubscriptionOrderNote(orderID int64) string {
	return fmt.Sprintf("payment order %d", orderID)
}

func hasPaymentSubscriptionOrderNote(notes string, orderNote string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == orderNote {
			return true
		}
	}
	return false
}

func (s *PaymentService) hasAuditLog(ctx context.Context, orderID int64, action string) bool {
	oid := strconv.FormatInt(orderID, 10)
	c, _ := s.entClient.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(oid), paymentauditlog.ActionEQ(action)).
		Limit(1).Count(ctx)
	return c > 0
}

func (s *PaymentService) applyAffiliateRebateForOrder(ctx context.Context, o *dbent.PaymentOrder) error {
	baseAmount := affiliateRebateBaseAmount(o)
	if o == nil || baseAmount <= 0 {
		return nil
	}
	if s.affiliateService == nil {
		return nil
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
			"error": fmt.Sprintf("begin affiliate rebate tx: %v", err),
		})
		return fmt.Errorf("begin affiliate rebate tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	claimed, err := s.tryClaimAffiliateRebateAudit(txCtx, tx.Client(), o.ID, baseAmount)
	if err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
			"error": err.Error(),
		})
		return fmt.Errorf("claim affiliate rebate audit: %w", err)
	}
	if !claimed {
		return nil
	}

	sourceOrderID := o.ID
	rebateAmount, err := s.affiliateService.AccrueInviteRebateForOrder(txCtx, o.UserID, baseAmount, &sourceOrderID)
	if err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
			"error": err.Error(),
		})
		return fmt.Errorf("accrue affiliate rebate: %w", err)
	}

	if rebateAmount <= 0 {
		if err := s.updateClaimedAffiliateRebateAudit(txCtx, tx.Client(), o.ID, "AFFILIATE_REBATE_SKIPPED", map[string]any{
			"baseAmount": baseAmount,
			"reason":     "no inviter bound or rebate amount <= 0",
		}); err != nil {
			s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
				"error": err.Error(),
			})
			return fmt.Errorf("update affiliate rebate skipped audit: %w", err)
		}
		if err := tx.Commit(); err != nil {
			s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
				"error": fmt.Sprintf("commit affiliate rebate tx: %v", err),
			})
			return fmt.Errorf("commit affiliate rebate tx: %w", err)
		}
		return nil
	}

	if err := s.updateClaimedAffiliateRebateAudit(txCtx, tx.Client(), o.ID, "AFFILIATE_REBATE_APPLIED", map[string]any{
		"baseAmount":   baseAmount,
		"rebateAmount": rebateAmount,
	}); err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
			"error": err.Error(),
		})
		return fmt.Errorf("update affiliate rebate applied audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_REBATE_FAILED", "system", map[string]any{
			"error": fmt.Sprintf("commit affiliate rebate tx: %v", err),
		})
		return fmt.Errorf("commit affiliate rebate tx: %w", err)
	}
	return nil
}

func affiliateRebateBaseAmount(o *dbent.PaymentOrder) float64 {
	if o == nil {
		return 0
	}
	if paymentOrderHasDiscount(o) {
		if o.OrderType == payment.OrderTypeBalance {
			value, ok := paymentSnapshotFloat(o.ProductSnapshot["paid_credit_amount"])
			if ok && value > 0 {
				return value
			}
			return 0
		}
		if o.OrderType == payment.OrderTypeSubscription {
			raw, ok := o.ProductSnapshot["payment_discount"].(map[string]any)
			if !ok {
				return 0
			}
			originalText, _ := raw["original_amount"].(string)
			original, err := decimal.NewFromString(originalText)
			if err != nil || !original.IsPositive() {
				return 0
			}
			return decimal.NewFromFloat(o.Amount).Mul(decimal.NewFromFloat(o.PayAmount)).Div(original).Truncate(8).InexactFloat64()
		}
		return 0
	}
	switch o.OrderType {
	case payment.OrderTypeBalance, payment.OrderTypeSubscription:
		return o.Amount
	default:
		return 0
	}
}

func (s *PaymentService) tryClaimAffiliateRebateAudit(ctx context.Context, client *dbent.Client, orderID int64, baseAmount float64) (bool, error) {
	if client == nil {
		return false, errors.New("nil payment client")
	}
	oid := strconv.FormatInt(orderID, 10)
	detail, _ := json.Marshal(map[string]any{
		"baseAmount": baseAmount,
		"status":     "reserved",
	})
	query, args := buildAffiliateRebateAuditClaimQuery(client, oid, string(detail))
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	var claimID int64
	if err := rows.Scan(&claimID); err != nil {
		return false, err
	}
	return true, nil
}

func buildAffiliateRebateAuditClaimQuery(client *dbent.Client, orderID, detail string) (string, []any) {
	nowExpr := paymentAuditCurrentTimestampExpr(client)
	if paymentAuditDialect(client) == dialect.Postgres {
		return fmt.Sprintf(`
INSERT INTO payment_audit_logs (order_id, action, detail, operator, created_at)
SELECT $1::text, 'AFFILIATE_REBATE_APPLIED', $2::text, 'system', %s
WHERE NOT EXISTS (
	SELECT 1
	FROM payment_audit_logs
	WHERE order_id = $1::text
	  AND action IN ('AFFILIATE_REBATE_APPLIED', 'AFFILIATE_REBATE_SKIPPED')
)
ON CONFLICT (order_id, action) DO NOTHING
RETURNING id`, nowExpr), []any{orderID, detail}
	}
	return fmt.Sprintf(`
INSERT INTO payment_audit_logs (order_id, action, detail, operator, created_at)
SELECT ?, 'AFFILIATE_REBATE_APPLIED', ?, 'system', %s
WHERE NOT EXISTS (
	SELECT 1
	FROM payment_audit_logs
	WHERE order_id = ?
	  AND action IN ('AFFILIATE_REBATE_APPLIED', 'AFFILIATE_REBATE_SKIPPED')
)
ON CONFLICT (order_id, action) DO NOTHING
RETURNING id`, nowExpr), []any{orderID, detail, orderID}
}

func paymentAuditCurrentTimestampExpr(client *dbent.Client) string {
	if paymentAuditDialect(client) == dialect.Postgres {
		return "NOW()"
	}
	return "CURRENT_TIMESTAMP"
}

func paymentAuditDialect(client *dbent.Client) string {
	if client == nil || client.Driver() == nil {
		return ""
	}
	return client.Driver().Dialect()
}

func (s *PaymentService) updateClaimedAffiliateRebateAudit(ctx context.Context, client *dbent.Client, orderID int64, action string, detail map[string]any) error {
	if client == nil {
		return errors.New("nil payment client")
	}
	oid := strconv.FormatInt(orderID, 10)
	detailJSON, _ := json.Marshal(detail)
	updated, err := client.PaymentAuditLog.Update().
		Where(
			paymentauditlog.OrderIDEQ(oid),
			paymentauditlog.ActionEQ("AFFILIATE_REBATE_APPLIED"),
		).
		SetAction(action).
		SetDetail(string(detailJSON)).
		SetOperator("system").
		Save(ctx)
	if err != nil {
		return err
	}
	if updated == 0 {
		return errors.New("affiliate rebate claim log not found")
	}
	return nil
}

func (s *PaymentService) markFailed(ctx context.Context, oid int64, lease *paymentFulfillmentLease, cause error) {
	if lease == nil {
		slog.Error("mark FAILED without fulfillment lease", "orderID", oid)
		return
	}
	now := time.Now()
	r := psErrMsg(cause)
	// The lease version prevents a stale worker from overwriting a newer owner.
	c, e := s.entClient.PaymentOrder.Update().
		Where(
			paymentorder.IDEQ(oid),
			paymentorder.StatusEQ(OrderStatusRecharging),
			paymentorder.UpdatedAtEQ(lease.version),
		).
		SetStatus(OrderStatusFailed).SetFailedAt(now).SetFailedReason(r).Save(ctx)
	if e != nil {
		slog.Error("mark FAILED", "orderID", oid, "error", e)
	}
	if c > 0 {
		s.writeAuditLog(ctx, oid, "FULFILLMENT_FAILED", "system", map[string]any{"reason": r})
	}
}

// markResetCardGrantExpiryManualReview keeps the paid fact durable while
// making an already-expired immutable grant deadline visible to operators.
// It deliberately retains FAILED because the existing admin retry endpoint
// accepts paid fulfillment failures; automatic recovery has its own exclusion.
func (s *PaymentService) markResetCardGrantExpiryManualReview(ctx context.Context, oid int64, lease *paymentFulfillmentLease) {
	if lease == nil {
		slog.Error("mark reset-card grant expiry manual review without fulfillment lease", "orderID", oid)
		return
	}
	now := time.Now()
	updated, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(oid),
		paymentorder.StatusEQ(OrderStatusRecharging),
		paymentorder.UpdatedAtEQ(lease.version),
	).SetStatus(OrderStatusFailed).
		SetFailedAt(now).
		SetFailedReason(resetCardGrantExpiryManualReviewReason).
		Save(ctx)
	if err != nil {
		slog.Error("mark reset-card grant expiry manual review", "orderID", oid, "error", err)
		return
	}
	if updated > 0 && !s.hasAuditLog(ctx, oid, resetCardGrantExpiryManualReviewAudit) {
		s.writeAuditLog(ctx, oid, resetCardGrantExpiryManualReviewAudit, "system", map[string]any{
			"reason": resetCardGrantExpiryManualReviewReason,
		})
	}
}

func (s *PaymentService) RetryFulfillment(ctx context.Context, oid int64) error {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.PaidAt == nil {
		return infraerrors.BadRequest("INVALID_STATUS", "order is not paid")
	}
	if isPaymentDiscountManualReview(o) {
		return infraerrors.Conflict("COUPON_MANUAL_REVIEW", "coupon capacity must be resolved before fulfillment")
	}
	if psIsRefundStatus(o.Status) {
		return infraerrors.BadRequest("INVALID_STATUS", "refund-related order cannot retry")
	}
	if o.Status == OrderStatusCompleted {
		return infraerrors.BadRequest("INVALID_STATUS", "order already completed")
	}
	if o.Status != OrderStatusFailed && o.Status != OrderStatusPaid && o.Status != OrderStatusRecharging {
		return infraerrors.BadRequest("INVALID_STATUS", "only paid, failed, and recoverable recharging orders can retry")
	}
	s.writeAuditLog(ctx, oid, "RECHARGE_RETRY", "admin", map[string]any{"detail": "admin manual retry"})
	return s.executeFulfillment(ctx, oid)
}
