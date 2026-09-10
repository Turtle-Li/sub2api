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

	"entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/paymentproviderinstance"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
)

// --- Refund Flow ---

var createPaymentProviderFromInstance = provider.CreateProvider

// getOrderProviderInstance looks up the provider instance that processed this order.
// For legacy orders without provider_instance_id, it resolves only when the
// historical instance is uniquely identifiable from the stored order fields.
func (s *PaymentService) getOrderProviderInstance(ctx context.Context, o *dbent.PaymentOrder) (*dbent.PaymentProviderInstance, error) {
	if s == nil || s.entClient == nil || o == nil {
		return nil, nil
	}

	if snapshot := psOrderProviderSnapshot(o); snapshot != nil {
		return s.resolveSnapshotOrderProviderInstance(ctx, o, snapshot)
	}

	instIDStr := strings.TrimSpace(psStringValue(o.ProviderInstanceID))
	if instIDStr == "" {
		return s.resolveUniqueLegacyOrderProviderInstance(ctx, o)
	}

	instID, err := strconv.ParseInt(instIDStr, 10, 64)
	if err != nil {
		return nil, nil
	}
	return s.entClient.PaymentProviderInstance.Get(ctx, instID)
}

// getRefundOrderProviderInstance resolves the provider instance for refund paths.
// Refunds must be pinned to an explicit historical binding, so legacy
// "best-effort" provider guessing is intentionally not allowed here.
func (s *PaymentService) getRefundOrderProviderInstance(ctx context.Context, o *dbent.PaymentOrder) (*dbent.PaymentProviderInstance, error) {
	if s == nil || s.entClient == nil || o == nil {
		return nil, nil
	}

	if snapshot := psOrderProviderSnapshot(o); snapshot != nil {
		return s.resolveSnapshotOrderProviderInstance(ctx, o, snapshot)
	}

	instIDStr := strings.TrimSpace(psStringValue(o.ProviderInstanceID))
	if instIDStr == "" {
		return nil, nil
	}

	instID, err := strconv.ParseInt(instIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("order %d refund provider instance id is invalid: %s", o.ID, instIDStr)
	}
	inst, err := s.entClient.PaymentProviderInstance.Get(ctx, instID)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, fmt.Errorf("order %d refund provider instance %s is missing", o.ID, instIDStr)
		}
		return nil, err
	}
	return inst, nil
}

func (s *PaymentService) resolveUniqueLegacyOrderProviderInstance(ctx context.Context, o *dbent.PaymentOrder) (*dbent.PaymentProviderInstance, error) {
	paymentType := payment.GetBasePaymentType(strings.TrimSpace(o.PaymentType))
	providerKey := strings.TrimSpace(psStringValue(o.ProviderKey))
	if providerKey != "" {
		instances, err := s.entClient.PaymentProviderInstance.Query().
			Where(paymentproviderinstance.ProviderKeyEQ(providerKey)).
			All(ctx)
		if err != nil {
			return nil, err
		}
		matched := psFilterLegacyOrderProviderInstances(paymentType, instances)
		if len(matched) == 1 {
			return matched[0], nil
		}
		return nil, nil
	}

	if paymentType == "" {
		return nil, nil
	}

	instances, err := s.entClient.PaymentProviderInstance.Query().
		All(ctx)
	if err != nil {
		return nil, err
	}

	matched := psFilterLegacyOrderProviderInstances(paymentType, instances)
	if len(matched) == 1 {
		return matched[0], nil
	}
	return nil, nil
}

func psFilterLegacyOrderProviderInstances(orderPaymentType string, instances []*dbent.PaymentProviderInstance) []*dbent.PaymentProviderInstance {
	if len(instances) == 0 {
		return nil
	}
	if strings.TrimSpace(orderPaymentType) == "" {
		return instances
	}
	var matched []*dbent.PaymentProviderInstance
	for _, inst := range instances {
		if psLegacyOrderMatchesInstance(orderPaymentType, inst) {
			matched = append(matched, inst)
		}
	}
	return matched
}

func psLegacyOrderMatchesInstance(orderPaymentType string, inst *dbent.PaymentProviderInstance) bool {
	if inst == nil {
		return false
	}

	baseType := payment.GetBasePaymentType(strings.TrimSpace(orderPaymentType))
	instanceProviderKey := strings.TrimSpace(inst.ProviderKey)
	if baseType == "" {
		return false
	}

	if baseType == payment.TypeStripe {
		return instanceProviderKey == payment.TypeStripe
	}
	if instanceProviderKey == payment.TypeStripe {
		return false
	}
	if instanceProviderKey == baseType {
		return true
	}
	return payment.InstanceSupportsType(inst.SupportedTypes, baseType)
}

func (s *PaymentService) RequestRefund(ctx context.Context, oid, uid int64, reason string) error {
	o, err := s.validateRefundRequest(ctx, oid, uid)
	if err != nil {
		return err
	}
	if !refundStateValid(o) {
		return infraerrors.BadRequest("INVALID_REFUND_STATE", "stored order refund amounts are invalid")
	}
	settled, _ := refundOrderAmounts(o)
	remaining := refundRemainingAmount(o, settled)
	if remaining <= paymentAmountZeroTolerance(PaymentOrderCurrency(o)) {
		return infraerrors.Conflict("REFUND_ALREADY_SETTLED", "the order has no refundable amount remaining")
	}
	nr := strings.TrimSpace(reason)
	now := time.Now()
	by := fmt.Sprintf("%d", uid)
	claim := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(oid),
		paymentorder.UserIDEQ(uid),
		paymentorder.RefundAmountEQ(o.RefundAmount),
		paymentorder.RefundRequestedAmountEQ(o.RefundRequestedAmount),
		paymentorder.StatusIn(OrderStatusCompleted, OrderStatusPartiallyRefunded),
		paymentorder.OrderTypeEQ(payment.OrderTypeBalance),
	)
	if paymentAuditDialect(s.entClient) == "postgres" {
		claim = claim.Where(paymentorder.UpdatedAtEQ(o.UpdatedAt))
	}
	c, err := claim.SetStatus(OrderStatusRefundRequested).
		SetRefundRequestedAt(now).
		SetRefundRequestReason(nr).
		SetRefundRequestedBy(by).
		SetRefundAmount(settled).
		SetRefundRequestedAmount(remaining).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if c == 0 {
		return infraerrors.Conflict("CONFLICT", "order status changed")
	}
	s.writeRefundAuditLog(ctx, oid, "REFUND_REQUESTED", fmt.Sprintf("user:%d", uid), map[string]any{
		"amount": remaining, "settledRefundAmount": settled, "reason": nr,
	})
	return nil
}

// refundOrderAmounts separates settled money from an in-flight request.  A
// few pre-240 rows (and old fixtures) stored the requested amount in
// refund_amount; the fallback keeps those rows readable while the migration
// moves them to refund_requested_amount.
func refundOrderAmounts(o *dbent.PaymentOrder) (settled, requested float64) {
	if o == nil {
		return 0, 0
	}
	if isFiniteNonNegativeRefundAmount(o.RefundAmount) {
		settled = o.RefundAmount
	} else {
		// Preserve a fail-closed sentinel. Treating NaN as zero would make the
		// remaining amount look fully refundable.
		settled = math.Inf(1)
	}
	if isFiniteNonNegativeRefundAmount(o.RefundRequestedAmount) {
		requested = o.RefundRequestedAmount
	} else {
		requested = math.Inf(1)
	}
	// Pre-240 rows put an in-flight amount in refund_amount. Migration 240
	// moves those rows to the new column. Keep a narrow fallback for rows that
	// still carry the original request timestamp. In particular, do not infer
	// an old request from REFUNDING alone: a crash after the new status CAS but
	// before the request columns are persisted must be treated as settled money,
	// otherwise a retry could refund the same partial amount twice.
	legacyInFlight := o.Status == OrderStatusRefundRequested || o.Status == OrderStatusRefundPending
	if o.Status == OrderStatusRefundFailed && o.RefundAt == nil && o.RefundRequestedAt != nil {
		legacyInFlight = true
	}
	if legacyInFlight && requested <= 0 && isFinitePositiveRefundAmount(settled) {
		requested, settled = settled, 0
	}
	return settled, requested
}

func refundAmountsValid(o *dbent.PaymentOrder) bool {
	return o != nil &&
		isFiniteNonNegativeRefundAmount(o.RefundAmount) &&
		isFiniteNonNegativeRefundAmount(o.RefundRequestedAmount)
}

// refundStateValid is the stronger order-level guard used before any refund
// decision or provider call. Field-level finiteness alone is not enough: a
// corrupted row could contain a finite settled/requested amount larger than
// the original order and still pass the cumulative cap comparisons below.
// Legacy rows are normalized through refundOrderAmounts first so an old
// in-flight value in refund_amount is checked as a request, not as settled
// money.
func refundStateValid(o *dbent.PaymentOrder) bool {
	if o == nil || !isFinitePositiveRefundAmount(o.Amount) || !refundAmountsValid(o) {
		return false
	}
	settled, requested := refundOrderAmounts(o)
	if !isFiniteNonNegativeRefundAmount(settled) || !isFiniteNonNegativeRefundAmount(requested) {
		return false
	}
	tolerance := paymentAmountZeroTolerance(PaymentOrderCurrency(o))
	if settled > o.Amount+tolerance {
		return false
	}
	remaining := o.Amount - settled
	return requested <= remaining+tolerance
}

// refundAmountsForClaim reads the amounts from the row that won the refund
// status CAS.  The pre-claim status is needed only for the narrow pre-240
// compatibility case where an in-flight request was stored in refund_amount;
// current rows always keep settled and requested money in separate columns.
func refundAmountsForClaim(preClaim, claimed *dbent.PaymentOrder) (settled, requested float64) {
	if claimed == nil {
		return refundOrderAmounts(preClaim)
	}
	settled, requested = refundOrderAmounts(claimed)
	// Apply the compatibility fallback only when the monetary columns still
	// match the row read before the CAS.  If another worker completed a partial
	// refund between that read and our claim, its cumulative amount is
	// authoritative and must not be reinterpreted as an old request merely
	// because the pre-read status was REFUND_REQUESTED.
	amountsUnchanged := preClaim != nil &&
		math.Abs(preClaim.RefundAmount-claimed.RefundAmount) < paymentAmountZeroTolerance(PaymentOrderCurrency(claimed)) &&
		math.Abs(preClaim.RefundRequestedAmount-claimed.RefundRequestedAmount) < paymentAmountZeroTolerance(PaymentOrderCurrency(claimed))
	legacyInFlight := preClaim != nil &&
		(preClaim.Status == OrderStatusRefundRequested || preClaim.Status == OrderStatusRefundPending)
	if preClaim != nil && preClaim.Status == OrderStatusRefundFailed && preClaim.RefundAt == nil && preClaim.RefundRequestedAt != nil {
		legacyInFlight = true
	}
	if amountsUnchanged && legacyInFlight && requested <= 0 && settled > 0 {
		requested, settled = settled, 0
	}
	return settled, requested
}

func refundRemainingAmount(o *dbent.PaymentOrder, settled float64) float64 {
	if o == nil || !isFinitePositiveRefundAmount(o.Amount) || !isFiniteNonNegativeRefundAmount(settled) {
		return 0
	}
	remaining := o.Amount - settled
	if !isFiniteNonNegativeRefundAmount(remaining) {
		return 0
	}
	return remaining
}

func normalizeRefundAmount(amount, remaining, tolerance float64) float64 {
	if math.Abs(amount-remaining) <= tolerance {
		return remaining
	}
	return amount
}

// settledRefundTotal validates and combines the amount for the current
// attempt with the amount already confirmed by the payment channel.  The
// order's refund_amount column is deliberately cumulative; a caller must not
// be able to overwrite it with a smaller partial attempt or push it beyond the
// original order amount.
func settledRefundTotal(o *dbent.PaymentOrder, settled, attempt float64) (float64, string, error) {
	if o == nil {
		return 0, "", infraerrors.BadRequest("INVALID_REFUND", "refund order is missing")
	}
	currency := PaymentOrderCurrency(o)
	zeroTolerance := paymentAmountZeroTolerance(currency)
	if !isFinitePositiveRefundAmount(o.Amount) {
		return 0, "", infraerrors.BadRequest("INVALID_REFUND_STATE", "stored order amount is invalid")
	}
	if math.IsNaN(settled) || math.IsInf(settled, 0) || settled < -zeroTolerance {
		return 0, "", infraerrors.BadRequest("INVALID_REFUND_STATE", "stored refund amount is invalid")
	}
	if math.IsNaN(attempt) || math.IsInf(attempt, 0) || attempt <= zeroTolerance {
		return 0, "", infraerrors.BadRequest("INVALID_AMOUNT", "refund amount must be greater than zero")
	}
	settled = math.Max(0, settled)
	remaining := refundRemainingAmount(o, settled)
	if remaining <= zeroTolerance {
		return 0, "", infraerrors.Conflict("REFUND_ALREADY_SETTLED", "the order has no refundable amount remaining")
	}
	if attempt-remaining > zeroTolerance {
		return 0, "", infraerrors.BadRequest("REFUND_AMOUNT_EXCEEDED", "refund amount exceeds the remaining refundable amount")
	}
	attempt = normalizeRefundAmount(attempt, remaining, zeroTolerance)
	total := settled + attempt
	if math.Abs(total-o.Amount) < zeroTolerance {
		total = o.Amount
		return total, OrderStatusRefunded, nil
	}
	if total > o.Amount+zeroTolerance {
		return 0, "", infraerrors.BadRequest("REFUND_AMOUNT_EXCEEDED", "cumulative refund amount exceeds the order amount")
	}
	return total, OrderStatusPartiallyRefunded, nil
}

// nextRefundAuditAction works around the historical unique (order_id,
// action) index while retaining the stable action names for the first event.
// Subsequent partial attempts remain append-only and queryable by prefix.
func nextRefundAuditAction(ctx context.Context, client *dbent.Client, oid int64, prefix string) string {
	if client == nil {
		return prefix
	}
	base := strings.TrimSpace(prefix)
	if base == "" {
		base = "REFUND_EVENT"
	}
	exists, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(oid, 10)), paymentauditlog.ActionEQ(base)).
		Limit(1).Exist(ctx)
	if err != nil || !exists {
		return base
	}
	// UnixNano is sufficient here because the order row transition serializes
	// successful attempts; keep the suffix below Ent's 50-character limit.
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	maxSuffix := 50 - len(base) - 1
	if maxSuffix < 1 {
		return base[:50]
	}
	if len(suffix) > maxSuffix {
		suffix = suffix[len(suffix)-maxSuffix:]
	}
	return base + "_" + suffix
}

func (s *PaymentService) writeRefundSuccessAudit(ctx context.Context, p *RefundPlan, previous, cumulative float64) {
	if s == nil || s.entClient == nil || p == nil {
		return
	}
	detail, err := json.Marshal(map[string]any{
		"refundAmount": p.RefundAmount, "previousRefundAmount": previous,
		"cumulativeRefundAmount": cumulative, "reason": p.Reason,
		"balanceDeducted": p.BalanceToDeduct, "force": p.Force,
	})
	if err != nil {
		slog.Error("refund success audit marshal failed", "orderID", p.OrderID, "error", err)
		return
	}
	action := nextRefundAuditAction(ctx, s.entClient, p.OrderID, "REFUND_SUCCESS")
	if _, err := s.entClient.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(p.OrderID, 10)).SetAction(action).
		SetDetail(string(detail)).SetOperator("admin").Save(ctx); err != nil {
		slog.Error("refund success audit failed", "orderID", p.OrderID, "action", action, "error", err)
	}
}

func (s *PaymentService) writeRefundPendingAudit(ctx context.Context, oid int64, detail map[string]any) {
	s.writeRefundAuditLog(ctx, oid, "REFUND_PENDING", "admin", detail)
}

// writeRefundAuditLog is append-only for refund events. PaymentAuditLog keeps
// a historical unique (order_id, action) index for fulfillment idempotency;
// refund attempts, however, may legitimately repeat after a partial success
// or a gateway retry. The first event retains its stable action name and later
// events receive a bounded timestamp suffix.
func (s *PaymentService) writeRefundAuditLog(ctx context.Context, oid int64, action, operator string, detail map[string]any) {
	if s == nil || s.entClient == nil {
		return
	}
	dj, err := json.Marshal(detail)
	if err != nil {
		slog.Error("refund audit marshal failed", "orderID", oid, "action", action, "error", err)
		return
	}
	actual := nextRefundAuditAction(ctx, s.entClient, oid, action)
	if _, err := s.entClient.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(oid, 10)).SetAction(actual).
		SetDetail(string(dj)).SetOperator(operator).Save(ctx); err != nil {
		slog.Error("refund audit failed", "orderID", oid, "action", actual, "error", err)
	}
}

func (s *PaymentService) validateRefundRequest(ctx context.Context, oid, uid int64) (*dbent.PaymentOrder, error) {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.UserID != uid {
		return nil, infraerrors.Forbidden("FORBIDDEN", "no permission")
	}
	if o.OrderType != payment.OrderTypeBalance {
		return nil, infraerrors.BadRequest("INVALID_ORDER_TYPE", "only balance orders can request refund")
	}
	if o.Status != OrderStatusCompleted && o.Status != OrderStatusPartiallyRefunded {
		return nil, infraerrors.BadRequest("INVALID_STATUS", "only completed or partially refunded orders can request refund")
	}
	if manual, err := paymentOrderRequiresManualRefund(o); err != nil {
		return nil, infraerrors.BadRequest("INVALID_PRODUCT_SNAPSHOT", "payment order entitlement snapshot is invalid")
	} else if manual {
		// Product bonuses and account upgrades are not reversible yet. A
		// self-service refund must not leave paid benefits behind.
		return nil, infraerrors.Forbidden("REFUND_REQUIRES_MANUAL_REVIEW", "orders with non-reversible entitlements require manual refund review")
	}
	// Check provider instance allows user refund
	inst, err := s.getRefundOrderProviderInstance(ctx, o)
	if err != nil || inst == nil {
		return nil, infraerrors.Forbidden("USER_REFUND_DISABLED", "refund is not available for this order")
	}
	if !inst.AllowUserRefund {
		return nil, infraerrors.Forbidden("USER_REFUND_DISABLED", "user refund is not enabled for this provider")
	}
	return o, nil
}

func (s *PaymentService) PrepareRefund(ctx context.Context, oid int64, amt float64, reason string, force, deduct bool) (*RefundPlan, *RefundResult, error) {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return nil, nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if !refundStateValid(o) {
		return nil, nil, infraerrors.BadRequest("INVALID_REFUND_STATE", "stored order refund amounts are invalid")
	}
	if manual, entitlementErr := paymentOrderRequiresManualRefund(o); entitlementErr != nil {
		return nil, nil, infraerrors.BadRequest("INVALID_PRODUCT_SNAPSHOT", "payment order entitlement snapshot is invalid")
	} else if manual {
		return nil, nil, infraerrors.BadRequest("REFUND_REQUIRES_MANUAL_REVIEW", "orders with non-reversible entitlements require manual entitlement rollback")
	}
	ok := []string{OrderStatusCompleted, OrderStatusRefundRequested, OrderStatusRefundPending, OrderStatusRefundFailed, OrderStatusPartiallyRefunded}
	if !psSliceContains(ok, o.Status) {
		return nil, nil, infraerrors.BadRequest("INVALID_STATUS", "order status does not allow refund")
	}
	settled, requested := refundOrderAmounts(o)
	remaining := refundRemainingAmount(o, settled)
	zeroTolerance := paymentAmountZeroTolerance(PaymentOrderCurrency(o))
	if remaining <= zeroTolerance {
		return nil, nil, infraerrors.Conflict("REFUND_ALREADY_SETTLED", "the order has no refundable amount remaining")
	}
	// Unified orders use their historical app binding and the server runtime;
	// they never need a fabricated local provider credential row.
	if paymentOrderUsesUnifiedPay(o) {
		if err := s.validateUnifiedRefundOrder(o); err != nil {
			return nil, nil, err
		}
	} else {
		inst, instErr := s.getRefundOrderProviderInstance(ctx, o)
		if instErr != nil {
			slog.Warn("refund: provider instance lookup failed", "orderID", oid, "error", instErr)
			return nil, nil, infraerrors.InternalServer("PROVIDER_LOOKUP_FAILED", "failed to look up payment provider for this order")
		}
		if inst == nil {
			// Legacy order without provider_instance_id — block refund
			return nil, nil, infraerrors.Forbidden("REFUND_DISABLED", "refund is not available for this order")
		}
		if !inst.RefundEnabled {
			return nil, nil, infraerrors.Forbidden("REFUND_DISABLED", "refund is not enabled for this provider")
		}
	}
	if math.IsNaN(amt) || math.IsInf(amt, 0) {
		return nil, nil, infraerrors.BadRequest("INVALID_AMOUNT", "invalid refund amount")
	}
	if amt <= 0 {
		// A request/failed attempt carries its suggested amount.  A pending
		// attempt is immutable and can only be queried/finalized.
		if requested > zeroTolerance {
			amt = requested
		} else {
			amt = remaining
		}
	}
	if o.Status == OrderStatusRefundPending && requested > zeroTolerance && math.Abs(amt-requested) >= zeroTolerance {
		return nil, nil, infraerrors.Conflict("REFUND_IN_PROGRESS", "another refund request is already pending")
	}
	if amt <= zeroTolerance {
		return nil, nil, infraerrors.BadRequest("INVALID_AMOUNT", "refund amount must be greater than zero")
	}
	if amt-remaining > zeroTolerance {
		return nil, nil, infraerrors.BadRequest("REFUND_AMOUNT_EXCEEDED", "refund amount exceeds the remaining refundable amount")
	}
	amt = normalizeRefundAmount(amt, remaining, zeroTolerance)
	// Subscription fulfillment is a single dated entitlement.  The current
	// schema has no per-day refund ledger, and a proportional deduction would be
	// unsafe once a user has renewed or combined plans.  Do not let an admin
	// issue a partial cash refund while removing the entire subscription; route
	// that case through the explicit manual entitlement-review workflow.
	if o.OrderType == payment.OrderTypeSubscription && amt+zeroTolerance < remaining {
		return nil, nil, infraerrors.BadRequest("REFUND_PARTIAL_UNSUPPORTED", "partial subscription refunds require manual entitlement review")
	}
	orderCurrency := PaymentOrderCurrency(o)
	ga := calculateGatewayRefundDelta(o.Amount, o.PayAmount, settled, amt, orderCurrency)
	if ga <= 0 {
		return nil, nil, infraerrors.BadRequest("INVALID_AMOUNT", "refund amount is below the payment channel precision")
	}
	rr := strings.TrimSpace(reason)
	if rr == "" && o.RefundRequestReason != nil {
		rr = *o.RefundRequestReason
	}
	if rr == "" {
		rr = fmt.Sprintf("refund order:%d", o.ID)
	}
	p := &RefundPlan{
		OrderID: oid, Order: o, RefundAmount: amt, SettledRefundAmount: settled,
		RemainingRefundable: remaining, GatewayAmount: ga, Reason: rr, Force: force,
		DeductBalance: deduct, DeductionType: payment.DeductionTypeNone,
	}
	if deduct {
		if er := s.prepDeduct(ctx, o, p, force); er != nil {
			return nil, er, nil
		}
	}
	return p, nil, nil
}

func (s *PaymentService) prepDeduct(ctx context.Context, o *dbent.PaymentOrder, p *RefundPlan, force bool) *RefundResult {
	if o.OrderType == payment.OrderTypeSubscription {
		p.DeductionType = payment.DeductionTypeSubscription
		if o.SubscriptionGroupID != nil && o.SubscriptionDays != nil {
			p.SubDaysToDeduct = *o.SubscriptionDays
			sub, err := s.subscriptionSvc.GetActiveSubscription(ctx, o.UserID, *o.SubscriptionGroupID)
			if err == nil && sub != nil {
				p.SubscriptionID = sub.ID
			} else if !force {
				return &RefundResult{Success: false, Warning: "cannot find active subscription for deduction, use force", RequireForce: true}
			}
		}
		return nil
	}
	u, err := s.userRepo.GetByID(ctx, o.UserID)
	if err != nil {
		if !force {
			return &RefundResult{Success: false, Warning: "cannot fetch user balance, use force", RequireForce: true}
		}
		return nil
	}
	p.DeductionType = payment.DeductionTypeBalance
	if u.Balance < p.RefundAmount && !force {
		return &RefundResult{Success: false, Warning: "user balance is insufficient for deduction, use force", RequireForce: true}
	}
	p.BalanceToDeduct = math.Max(0, math.Min(p.RefundAmount, u.Balance))
	return nil
}

type availableBalanceDeductor interface {
	DeductAvailableBalance(ctx context.Context, id int64, amount float64) (float64, error)
}

func (s *PaymentService) deductAvailableBalance(ctx context.Context, userID int64, amount float64) (float64, error) {
	repo, ok := s.userRepo.(availableBalanceDeductor)
	if !ok {
		return 0, errors.New("user repository does not support available balance deduction")
	}
	return repo.DeductAvailableBalance(ctx, userID, amount)
}

func (s *PaymentService) ExecuteRefund(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	if p == nil || p.Order == nil {
		return nil, infraerrors.BadRequest("INVALID_REFUND", "refund plan is missing")
	}
	if paymentOrderUsesUnifiedPay(p.Order) {
		return s.executeUnifiedRefund(ctx, p)
	}
	// A plan may have been prepared before another partial refund committed.
	// Read the authoritative row immediately before the CAS and use that row's
	// status/settled amount for the attempt.  Relying on p.Order here would let
	// a stale full-refund plan add money on top of a newer partial refund.
	preClaim, err := s.entClient.PaymentOrder.Get(ctx, p.OrderID)
	if err != nil {
		return nil, fmt.Errorf("reload refund order before claim: %w", err)
	}
	if !refundStateValid(preClaim) {
		return nil, infraerrors.BadRequest("INVALID_REFUND_STATE", "stored order refund amounts are invalid")
	}
	originalStatus := preClaim.Status
	// Status plus both monetary columns form the portable optimistic claim
	// predicate.  On PostgreSQL we also compare the row timestamp; SQLite's
	// time adapter does not support equality predicates for scanned timestamps,
	// so the monetary predicates keep the unit/in-memory path safe as well.
	claim := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(p.OrderID),
		paymentorder.RefundAmountEQ(preClaim.RefundAmount),
		paymentorder.RefundRequestedAmountEQ(preClaim.RefundRequestedAmount),
		paymentorder.StatusIn(OrderStatusCompleted, OrderStatusRefundRequested, OrderStatusRefundFailed, OrderStatusPartiallyRefunded),
	)
	if paymentAuditDialect(s.entClient) == "postgres" {
		claim = claim.Where(paymentorder.UpdatedAtEQ(preClaim.UpdatedAt))
	}
	c, err := claim.SetStatus(OrderStatusRefunding).Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	if c == 0 {
		return nil, infraerrors.Conflict("CONFLICT", "order status changed")
	}
	// The status transition is the single-writer fence for a legacy admin
	// refund. Reload the row after claiming it, then persist the settled and
	// current-request amounts separately before any deduction or gateway call.
	// This is important for a second partial attempt: a settled
	// refund_amount must never be mistaken for the new in-flight request.
	current, err := s.entClient.PaymentOrder.Get(ctx, p.OrderID)
	if err != nil {
		return nil, fmt.Errorf("reload claimed refund order: %w", err)
	}
	// The compatibility reader intentionally treats a legacy REFUNDING row's
	// refund_amount as an in-flight request.  After a new partial claim the row
	// is REFUNDING too, but its refund_amount is settled cumulative money.  The
	// persisted pre-claim status disambiguates those cases; importantly, it is
	// read from the database rather than from the possibly stale plan.
	settled, _ := refundAmountsForClaim(preClaim, current)
	remaining := refundRemainingAmount(current, settled)
	zeroTolerance := paymentAmountZeroTolerance(PaymentOrderCurrency(current))
	restoreClaimed := func() {
		original := *preClaim
		s.restoreStatus(ctx, &RefundPlan{OrderID: p.OrderID, Order: &original})
	}
	if p.RefundAmount <= zeroTolerance || p.RefundAmount-remaining > zeroTolerance {
		restoreClaimed()
		return nil, infraerrors.Conflict("REFUND_AMOUNT_CHANGED", "refund amount exceeds the remaining refundable amount")
	}
	if math.Abs(p.RefundAmount-remaining) < zeroTolerance {
		p.RefundAmount = remaining
	}
	p.Order = current
	// Keep the pre-claim status on the in-memory plan so rollback paths restore
	// PARTIALLY_REFUNDED/REFUND_FAILED/REFUND_REQUESTED rather than defaulting
	// every failed attempt to COMPLETED. The database row remains REFUNDING
	// until the gateway result is handled.
	p.Order.Status = originalStatus
	p.SettledRefundAmount = settled
	p.RemainingRefundable = remaining
	p.GatewayAmount = calculateGatewayRefundDelta(current.Amount, current.PayAmount, settled, p.RefundAmount, PaymentOrderCurrency(current))
	if p.GatewayAmount <= 0 {
		restoreClaimed()
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "refund amount is below the payment channel precision")
	}
	if _, err := s.entClient.PaymentOrder.UpdateOneID(p.OrderID).
		SetRefundAmount(settled).
		SetRefundRequestedAmount(p.RefundAmount).
		Save(ctx); err != nil {
		restoreClaimed()
		return nil, fmt.Errorf("record refund request: %w", err)
	}
	if p.DeductionType == payment.DeductionTypeBalance && p.BalanceToDeduct > 0 {
		// Skip balance deduction on retry if previous attempt already deducted
		// but failed to roll back (REFUND_ROLLBACK_FAILED in audit log).
		if !s.hasAuditLog(ctx, p.OrderID, "REFUND_ROLLBACK_FAILED") {
			deducted, err := s.deductAvailableBalance(ctx, p.Order.UserID, p.BalanceToDeduct)
			if err != nil {
				s.restoreStatus(ctx, p)
				return nil, fmt.Errorf("deduction: %w", err)
			}
			p.BalanceToDeduct = deducted
		} else {
			slog.Warn("skipping balance deduction on retry (previous rollback failed)", "orderID", p.OrderID)
			p.BalanceToDeduct = 0
		}
	}
	if p.DeductionType == payment.DeductionTypeSubscription && p.SubDaysToDeduct > 0 && p.SubscriptionID > 0 {
		if !s.hasAuditLog(ctx, p.OrderID, "REFUND_ROLLBACK_FAILED") {
			_, err := s.subscriptionSvc.ExtendSubscription(ctx, p.SubscriptionID, -p.SubDaysToDeduct)
			if err != nil {
				if errors.Is(err, ErrAdjustWouldExpire) {
					// Deduction would expire the subscription — revoke it entirely
					slog.Info("subscription deduction would expire, revoking", "orderID", p.OrderID, "subID", p.SubscriptionID, "days", p.SubDaysToDeduct)
					if revokeErr := s.subscriptionSvc.RevokeSubscription(ctx, p.SubscriptionID); revokeErr != nil {
						s.restoreStatus(ctx, p)
						return nil, fmt.Errorf("revoke subscription: %w", revokeErr)
					}
				} else {
					// Other errors (DB failure, not found) — abort refund
					s.restoreStatus(ctx, p)
					return nil, fmt.Errorf("deduct subscription days: %w", err)
				}
			}
		} else {
			slog.Warn("skipping subscription deduction on retry (previous rollback failed)", "orderID", p.OrderID)
			p.SubDaysToDeduct = 0
		}
	}
	resp, err := s.gwRefund(ctx, p)
	if err != nil {
		return s.handleGwFail(ctx, p, err)
	}
	return s.finishRefund(ctx, p, resp)
}

func (s *PaymentService) gwRefund(ctx context.Context, p *RefundPlan) (*payment.RefundResponse, error) {
	if paymentOrderUsesUnifiedPay(p.Order) {
		return nil, errors.New("unified refunds require the durable asynchronous refund flow")
	}
	if p.Order.PaymentTradeNo == "" {
		s.writeRefundAuditLog(ctx, p.Order.ID, "REFUND_NO_TRADE_NO", "admin", map[string]any{"detail": "skipped"})
		return &payment.RefundResponse{Status: payment.ProviderStatusSuccess}, nil
	}

	// Use the exact provider instance that created this order, not a random one
	// from the registry. Each instance has its own merchant credentials.
	prov, err := s.getRefundProvider(ctx, p.Order)
	if err != nil {
		return nil, fmt.Errorf("get refund provider: %w", err)
	}
	if err := validateProviderSnapshotMetadata(p.Order, prov.ProviderKey(), providerMerchantIdentityMetadata(prov)); err != nil {
		s.writeRefundAuditLog(ctx, p.Order.ID, "REFUND_PROVIDER_METADATA_MISMATCH", "admin", map[string]any{
			"detail": err.Error(),
		})
		return nil, err
	}
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	resp, err := prov.Refund(ctx, payment.RefundRequest{
		TradeNo: p.Order.PaymentTradeNo,
		OrderID: p.Order.OutTradeNo,
		Amount:  formatGatewayRefundAmount(p.GatewayAmount, p.Order),
		Reason:  p.Reason,
	})
	finishProviderCall()
	if err != nil {
		if resp != nil && strings.TrimSpace(resp.Status) == payment.ProviderStatusPending {
			return resp, nil
		}
		return nil, err
	}
	if err := validateRefundProviderResponse(resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func formatGatewayRefundAmount(amount float64, order *dbent.PaymentOrder) string {
	return payment.FormatAmountForCurrency(amount, PaymentOrderCurrency(order))
}

func validateRefundProviderResponse(resp *payment.RefundResponse) error {
	if resp == nil {
		return fmt.Errorf("payment refund response missing")
	}
	status := strings.TrimSpace(resp.Status)
	switch status {
	case payment.ProviderStatusSuccess, payment.ProviderStatusRefunded, payment.ProviderStatusPending:
		return nil
	case payment.ProviderStatusFailed:
		return fmt.Errorf("payment refund failed: status %s", status)
	default:
		return fmt.Errorf("payment refund returned unknown status: %s", status)
	}
}

func (s *PaymentService) finishRefund(ctx context.Context, p *RefundPlan, resp *payment.RefundResponse) (*RefundResult, error) {
	if err := validateRefundProviderResponse(resp); err != nil {
		return s.handleGwFail(ctx, p, err)
	}
	switch strings.TrimSpace(resp.Status) {
	case payment.ProviderStatusSuccess, payment.ProviderStatusRefunded:
		return s.markRefundOk(ctx, p)
	case payment.ProviderStatusPending:
		return s.markRefundPending(ctx, p, resp)
	default:
		return s.handleGwFail(ctx, p, fmt.Errorf("payment refund returned unknown status: %s", strings.TrimSpace(resp.Status)))
	}
}

func (s *PaymentService) QueryAndFinalizeRefund(ctx context.Context, oid int64) (*RefundResult, error) {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if !refundStateValid(o) {
		return nil, infraerrors.BadRequest("INVALID_REFUND_STATE", "stored order refund amounts are invalid")
	}
	if o.Status != OrderStatusRefundPending {
		return nil, infraerrors.BadRequest("INVALID_STATUS", "only refund pending orders can be finalized")
	}
	if manual, snapshotErr := paymentOrderRequiresManualRefund(o); snapshotErr != nil {
		return nil, infraerrors.BadRequest("INVALID_PRODUCT_SNAPSHOT", "payment order entitlement snapshot is invalid")
	} else if manual {
		// A pending row may predate the current refund fence. Do not finalize it
		// automatically if fulfillment granted a benefit that this flow cannot
		// reverse atomically; leave the order for explicit entitlement review.
		return nil, infraerrors.BadRequest("REFUND_REQUIRES_MANUAL_REVIEW", "orders with non-reversible entitlements require manual entitlement rollback")
	}
	if paymentOrderUsesUnifiedPay(o) {
		return s.queryUnifiedRefund(ctx, o)
	}

	prov, err := s.getRefundProvider(ctx, o)
	if err != nil {
		return nil, fmt.Errorf("get refund provider: %w", err)
	}
	queryProvider, ok := prov.(payment.RefundQueryProvider)
	if !ok {
		return nil, infraerrors.BadRequest("REFUND_QUERY_UNSUPPORTED", "this payment provider does not support refund status query; please verify manually")
	}

	pendingDetail := s.latestRefundPendingDetail(ctx, oid)
	queryAmount := pendingDetail.GatewayAmount
	queryAmountString := ""
	if queryAmount > paymentAmountZeroTolerance(PaymentOrderCurrency(o)) {
		// The pending audit stores GatewayAmount after the credited amount has
		// already been converted to the provider's currency/channel amount.
		// Format it directly here; converting it a second time would under-query
		// orders with bonuses or a non-1:1 credited-to-paid ratio.
		queryAmountString = payment.FormatAmountForCurrency(queryAmount, PaymentOrderCurrency(o))
	} else {
		// Older pending audits may not have persisted a gateway amount. Their
		// order field is still the credited amount, so apply the conversion once.
		queryAmount = refundOrderQueryAmount(o)
		queryAmountString = formatGatewayRefundAmount(queryAmount, o)
	}
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	resp, err := queryProvider.QueryRefund(ctx, payment.RefundQueryRequest{
		TradeNo:  o.PaymentTradeNo,
		OrderID:  o.OutTradeNo,
		RefundID: pendingDetail.RefundID,
		Amount:   queryAmountString,
	})
	finishProviderCall()
	if err != nil {
		return nil, fmt.Errorf("query refund: %w", err)
	}
	if err := validateRefundProviderResponse(resp); err != nil {
		return s.finalizeRefundFailed(ctx, o, err)
	}

	plan := s.refundFinalizePlan(o)
	if !pendingDetail.DeductBalance {
		// The original pending attempt explicitly opted out of local content
		// reclamation. Preserve that choice across the asynchronous gateway
		// boundary instead of silently deducting on a later query.
		plan.DeductBalance = false
		plan.DeductionType = payment.DeductionTypeNone
		plan.BalanceToDeduct = 0
		plan.SubDaysToDeduct = 0
		plan.SubscriptionID = 0
	}
	if !pendingDetail.DeductionRollbackOK {
		// The original attempt already deducted locally and could not roll it
		// back. Do not perform a second deduction during finalization; the zero
		// amount below makes that explicit while the balance deduction type keeps
		// the resulting shortfall visible to the warning/audit path.
		plan.BalanceToDeduct = 0
		plan.SubDaysToDeduct = 0
		plan.SubscriptionID = 0
	} else if o.OrderType == payment.OrderTypeSubscription {
		if early := s.prepDeduct(ctx, o, plan, true); early != nil {
			return early, nil
		}
	}
	switch strings.TrimSpace(resp.Status) {
	case payment.ProviderStatusSuccess, payment.ProviderStatusRefunded:
		result, finalizeErr := s.finalizePendingRefundSuccess(ctx, plan)
		if finalizeErr == nil && result != nil && !pendingDetail.DeductionRollbackOK {
			if result.Warning == "" {
				result.Warning = "refund succeeded; previous local entitlement rollback requires manual review"
			} else {
				result.Warning += "; previous local entitlement rollback requires manual review"
			}
		}
		return result, finalizeErr
	case payment.ProviderStatusPending:
		s.writeRefundAuditLog(ctx, oid, "REFUND_QUERY_PENDING", "admin", map[string]any{"refundID": resp.RefundID})
		return &RefundResult{Success: false, Warning: "gateway refund is still pending confirmation"}, nil
	default:
		return s.finalizeRefundFailed(ctx, o, fmt.Errorf("payment refund returned unknown status: %s", strings.TrimSpace(resp.Status)))
	}
}

func refundOrderQueryAmount(o *dbent.PaymentOrder) float64 {
	settled, requested := refundOrderAmounts(o)
	if requested > paymentAmountZeroTolerance(PaymentOrderCurrency(o)) {
		return requested
	}
	return refundRemainingAmount(o, settled)
}

func (s *PaymentService) finalizePendingRefundSuccess(ctx context.Context, p *RefundPlan) (_ *RefundResult, err error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin refund finalization: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	txCtx := dbent.NewTxContext(ctx, tx)

	claimed, err := tx.PaymentOrder.Update().
		Where(paymentorder.IDEQ(p.OrderID), paymentorder.StatusEQ(OrderStatusRefundPending)).
		SetStatus(OrderStatusRefunding).
		Save(txCtx)
	if err != nil {
		return nil, fmt.Errorf("claim pending refund: %w", err)
	}
	if claimed == 0 {
		return nil, infraerrors.Conflict("CONFLICT", "order status changed")
	}

	if err := s.applyRefundFinalDeduction(txCtx, p); err != nil {
		return nil, err
	}
	result, err := s.markRefundOkTx(txCtx, tx.Client(), p)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit refund finalization: %w", err)
	}
	return result, nil
}

func (s *PaymentService) refundFinalizePlan(o *dbent.PaymentOrder) *RefundPlan {
	settled, requested := refundOrderAmounts(o)
	refundAmount := requested
	if refundAmount <= paymentAmountZeroTolerance(PaymentOrderCurrency(o)) {
		refundAmount = refundRemainingAmount(o, settled)
	}
	reason := strings.TrimSpace(psStringValue(o.RefundReason))
	if reason == "" {
		reason = fmt.Sprintf("refund order:%d", o.ID)
	}
	return &RefundPlan{
		OrderID:             o.ID,
		Order:               o,
		RefundAmount:        refundAmount,
		SettledRefundAmount: settled,
		RemainingRefundable: refundRemainingAmount(o, settled),
		GatewayAmount:       calculateGatewayRefundDelta(o.Amount, o.PayAmount, settled, refundAmount, PaymentOrderCurrency(o)),
		Reason:              reason,
		Force:               o.ForceRefund,
		DeductBalance:       true,
		DeductionType:       payment.DeductionTypeBalance,
		BalanceToDeduct: func() float64 {
			if o.OrderType == payment.OrderTypeBalance {
				return refundAmount
			}
			return 0
		}(),
	}
}

func (s *PaymentService) applyRefundFinalDeduction(ctx context.Context, p *RefundPlan) error {
	if p.DeductionType == payment.DeductionTypeBalance && p.BalanceToDeduct > 0 {
		deducted, err := s.deductAvailableBalance(ctx, p.Order.UserID, p.BalanceToDeduct)
		if err != nil {
			return fmt.Errorf("deduction: %w", err)
		}
		p.BalanceToDeduct = deducted
	}
	if p.DeductionType == payment.DeductionTypeSubscription && p.SubDaysToDeduct > 0 && p.SubscriptionID > 0 {
		if _, err := s.subscriptionSvc.ExtendSubscription(ctx, p.SubscriptionID, -p.SubDaysToDeduct); err != nil {
			if errors.Is(err, ErrAdjustWouldExpire) {
				if revokeErr := s.subscriptionSvc.RevokeSubscription(ctx, p.SubscriptionID); revokeErr != nil {
					return fmt.Errorf("revoke subscription: %w", revokeErr)
				}
			} else {
				return fmt.Errorf("deduct subscription days: %w", err)
			}
		}
	}
	return nil
}

func (s *PaymentService) finalizeRefundFailed(ctx context.Context, o *dbent.PaymentOrder, gErr error) (*RefundResult, error) {
	now := time.Now()
	_, _ = s.entClient.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRefundFailed).SetRefundRequestedAmount(o.RefundRequestedAmount).SetFailedAt(now).SetFailedReason(psErrMsg(gErr)).Save(ctx)
	s.writeRefundAuditLog(ctx, o.ID, "REFUND_FAILED", "admin", map[string]any{"detail": psErrMsg(gErr)})
	return &RefundResult{Success: false, Warning: "gateway refund failed: " + psErrMsg(gErr)}, nil
}

type refundPendingAuditDetail struct {
	RefundID            string  `json:"refundID"`
	GatewayAmount       float64 `json:"gatewayAmount"`
	DeductionRollbackOK bool    `json:"deductionRollbackOK"`
	DeductBalance       bool    `json:"deductBalance"`
}

func (s *PaymentService) latestRefundPendingDetail(ctx context.Context, oid int64) refundPendingAuditDetail {
	logEntry, err := s.entClient.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(oid, 10)), paymentauditlog.ActionHasPrefix("REFUND_PENDING")).
		Order(paymentauditlog.ByCreatedAt(sql.OrderDesc())).
		First(ctx)
	if err != nil || logEntry == nil {
		// Missing legacy detail historically meant that finalization should
		// perform the normal balance reclaim.  Keep that default explicit so a
		// migrated pending row cannot silently become a no-deduction refund.
		return refundPendingAuditDetail{DeductionRollbackOK: true, DeductBalance: true}
	}
	detail := refundPendingAuditDetail{DeductionRollbackOK: true}
	// Older pending audit rows did not persist the admin's deduction choice;
	// retain their historical behavior (deduct on successful finalization).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(logEntry.Detail), &raw); err == nil {
		if _, ok := raw["deductBalance"]; !ok {
			if _, ok = raw["deduct_balance"]; !ok {
				detail.DeductBalance = true
			}
		}
	} else {
		// A malformed legacy detail must not turn a previously requested
		// deduction into an implicit opt-out. Treat the rollback as unknown so
		// finalization will not deduct a second time and will surface a warning.
		detail.DeductBalance = true
		detail.DeductionRollbackOK = false
	}
	if err := json.Unmarshal([]byte(logEntry.Detail), &detail); err != nil {
		detail.DeductionRollbackOK = false
	}
	if value, ok := raw["deduct_balance"]; ok {
		_ = json.Unmarshal(value, &detail.DeductBalance)
	}
	detail.RefundID = strings.TrimSpace(detail.RefundID)
	return detail
}

// getRefundProvider creates a provider using the order's original instance config.
// Delegates to getOrderProvider which handles instance lookup and fallback.
func (s *PaymentService) getRefundProvider(ctx context.Context, o *dbent.PaymentOrder) (payment.Provider, error) {
	inst, err := s.getRefundOrderProviderInstance(ctx, o)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, fmt.Errorf("refund provider instance is unavailable for order %d", o.ID)
	}
	return s.createProviderFromInstance(ctx, inst)
}

func (s *PaymentService) handleGwFail(ctx context.Context, p *RefundPlan, gErr error) (*RefundResult, error) {
	if s.RollbackRefund(ctx, p, gErr) {
		s.restoreStatus(ctx, p)
		s.writeRefundAuditLog(ctx, p.OrderID, "REFUND_GATEWAY_FAILED", "admin", map[string]any{"detail": psErrMsg(gErr)})
		return &RefundResult{Success: false, Warning: "gateway failed: " + psErrMsg(gErr) + ", rolled back"}, nil
	}
	now := time.Now()
	// A rollback failure means the gateway outcome is unknown and the local
	// deduction may already be irreversible. Reload the row that is about to be
	// marked failed before writing any monetary fields. The plan can be stale
	// (for example, an admin prepared it before another partial refund settled),
	// and using its settled amount here could erase a confirmed refund or turn a
	// later retry into an over-refund.
	update := s.entClient.PaymentOrder.Update().
		Where(paymentorder.IDEQ(p.OrderID), paymentorder.StatusEQ(OrderStatusRefunding)).
		SetStatus(OrderStatusRefundFailed).
		SetFailedAt(now).
		SetFailedReason(psErrMsg(gErr))
	if current, reloadErr := s.entClient.PaymentOrder.Get(ctx, p.OrderID); reloadErr == nil {
		settled, requested := refundOrderAmounts(current)
		update = update.SetRefundAmount(settled).SetRefundRequestedAmount(requested)
	} else {
		// If the authoritative read failed, preserve the database's monetary
		// values instead of falling back to an untrusted in-memory plan. The
		// status/error marker still gives reconciliation a durable signal.
		slog.Error("could not reload refund order after rollback failure; preserving monetary fields", "orderID", p.OrderID, "error", reloadErr)
	}
	_, _ = update.Save(ctx)
	s.writeRefundAuditLog(ctx, p.OrderID, "REFUND_FAILED", "admin", map[string]any{
		"detail": psErrMsg(gErr), "settledRefundAmount": p.SettledRefundAmount, "refundRequestedAmount": p.RefundAmount,
	})
	return nil, infraerrors.InternalServer("REFUND_FAILED", psErrMsg(gErr))
}

func refundRecoveryWarning(p *RefundPlan) string {
	if p == nil || !p.DeductBalance || p.DeductionType != payment.DeductionTypeBalance {
		return ""
	}
	// Recovery compares already-rounded balance amounts. Keep the check narrow
	// enough to flag a missing smallest-unit deduction (for example a CNY
	// 0.01 shortfall); the broader provider-notification tolerance would hide
	// exactly that case.
	tolerance := paymentAmountZeroTolerance(PaymentOrderCurrency(p.Order))
	if p.RefundAmount > tolerance && p.BalanceToDeduct+tolerance < p.RefundAmount {
		return "refund succeeded; remaining balance recovery requires manual review"
	}
	return ""
}

func (s *PaymentService) markRefundOk(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	if p == nil || p.Order == nil {
		return nil, infraerrors.BadRequest("INVALID_REFUND", "refund plan is missing")
	}
	current, err := s.entClient.PaymentOrder.Get(ctx, p.OrderID)
	if err != nil {
		return nil, fmt.Errorf("reload refund order: %w", err)
	}
	settled, _ := refundOrderAmounts(current)
	newTotal, fs, err := settledRefundTotal(current, settled, p.RefundAmount)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	updated, err := s.entClient.PaymentOrder.Update().
		Where(paymentorder.IDEQ(p.OrderID), paymentorder.StatusEQ(OrderStatusRefunding)).
		SetStatus(fs).
		SetRefundAmount(newTotal).
		SetRefundRequestedAmount(0).
		SetRefundReason(p.Reason).
		SetRefundAt(now).
		SetForceRefund(p.Force).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("mark refund: %w", err)
	}
	if updated == 0 {
		return nil, infraerrors.Conflict("CONFLICT", "order status changed before refund finalization")
	}
	warning := refundRecoveryWarning(p)
	s.writeRefundSuccessAudit(ctx, p, settled, newTotal)
	return &RefundResult{Success: true, Warning: warning, BalanceDeducted: p.BalanceToDeduct, SubDaysDeducted: p.SubDaysToDeduct}, nil
}

func (s *PaymentService) markRefundOkTx(ctx context.Context, client *dbent.Client, p *RefundPlan) (*RefundResult, error) {
	if p == nil || p.Order == nil {
		return nil, infraerrors.BadRequest("INVALID_REFUND", "refund plan is missing")
	}
	current, err := client.PaymentOrder.Get(ctx, p.OrderID)
	if err != nil {
		return nil, fmt.Errorf("reload refund order: %w", err)
	}
	settled, _ := refundOrderAmounts(current)
	newTotal, fs, err := settledRefundTotal(current, settled, p.RefundAmount)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	updated, err := client.PaymentOrder.Update().
		Where(paymentorder.IDEQ(p.OrderID), paymentorder.StatusIn(OrderStatusRefunding, OrderStatusRefundPending)).
		SetStatus(fs).
		SetRefundAmount(newTotal).
		SetRefundRequestedAmount(0).
		SetRefundReason(p.Reason).
		SetRefundAt(now).
		SetForceRefund(p.Force).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("mark refund: %w", err)
	}
	if updated == 0 {
		return nil, infraerrors.Conflict("CONFLICT", "order status changed before refund finalization")
	}
	detail, err := json.Marshal(map[string]any{
		"refundAmount": p.RefundAmount, "previousRefundAmount": settled,
		"cumulativeRefundAmount": newTotal, "reason": p.Reason,
		"balanceDeducted": p.BalanceToDeduct, "force": p.Force,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal refund audit: %w", err)
	}
	action := nextRefundAuditAction(ctx, client, p.OrderID, "REFUND_SUCCESS")
	if _, err := client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(p.OrderID, 10)).
		SetAction(action).
		SetDetail(string(detail)).
		SetOperator("admin").
		Save(ctx); err != nil {
		return nil, fmt.Errorf("write refund audit: %w", err)
	}
	return &RefundResult{Success: true, Warning: refundRecoveryWarning(p), BalanceDeducted: p.BalanceToDeduct, SubDaysDeducted: p.SubDaysToDeduct}, nil
}

func (s *PaymentService) markRefundPending(ctx context.Context, p *RefundPlan, resp *payment.RefundResponse) (*RefundResult, error) {
	if p == nil || p.Order == nil {
		return nil, infraerrors.BadRequest("INVALID_REFUND", "refund plan is missing")
	}
	// Reload the order before persisting the pending result.  This keeps a
	// stale plan from replacing a cumulative settled amount written by an
	// earlier attempt, while preserving the current attempt as the immutable
	// requested amount.
	current, err := s.entClient.PaymentOrder.Get(ctx, p.OrderID)
	if err != nil {
		return nil, fmt.Errorf("reload pending refund order: %w", err)
	}
	settled, _ := refundOrderAmounts(current)
	balanceDeducted := p.BalanceToDeduct
	subDaysDeducted := p.SubDaysToDeduct
	rollbackOK := s.RollbackRefund(ctx, p, nil)
	if rollbackOK {
		p.BalanceToDeduct = 0
		p.SubDaysToDeduct = 0
	}

	updated, err := s.entClient.PaymentOrder.Update().
		Where(paymentorder.IDEQ(p.OrderID), paymentorder.StatusEQ(OrderStatusRefunding)).
		SetStatus(OrderStatusRefundPending).
		SetRefundAmount(settled).
		SetRefundRequestedAmount(p.RefundAmount).
		SetRefundReason(p.Reason).
		ClearRefundAt().
		SetForceRefund(p.Force).
		ClearFailedAt().
		ClearFailedReason().
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("mark refund pending: %w", err)
	}
	if updated == 0 {
		return nil, infraerrors.Conflict("CONFLICT", "order status changed before refund became pending")
	}

	detail := map[string]any{
		"refundID":            refundResponseID(resp),
		"refundAmount":        p.RefundAmount,
		"gatewayAmount":       p.GatewayAmount,
		"reason":              p.Reason,
		"force":               p.Force,
		"balanceDeducted":     p.BalanceToDeduct,
		"subDaysDeducted":     p.SubDaysToDeduct,
		"balanceRolledBack":   balanceDeducted,
		"subDaysRolledBack":   subDaysDeducted,
		"deductionRollbackOK": rollbackOK,
		"deductBalance":       p.DeductBalance,
	}
	s.writeRefundPendingAudit(ctx, p.OrderID, detail)

	warning := "gateway refund is pending confirmation"
	if !rollbackOK {
		warning += "; refund deduction rollback failed"
	}
	return &RefundResult{Success: false, Warning: warning}, nil
}

func refundResponseID(resp *payment.RefundResponse) string {
	if resp == nil {
		return ""
	}
	return strings.TrimSpace(resp.RefundID)
}

func (s *PaymentService) RollbackRefund(ctx context.Context, p *RefundPlan, gErr error) bool {
	if p.DeductionType == payment.DeductionTypeBalance && p.BalanceToDeduct > 0 {
		if err := s.userRepo.UpdateBalance(ctx, p.Order.UserID, p.BalanceToDeduct); err != nil {
			slog.Error("[CRITICAL] rollback failed", "orderID", p.OrderID, "amount", p.BalanceToDeduct, "error", err)
			s.writeAuditLog(ctx, p.OrderID, "REFUND_ROLLBACK_FAILED", "admin", map[string]any{"gatewayError": psErrMsg(gErr), "rollbackError": psErrMsg(err), "balanceDeducted": p.BalanceToDeduct})
			return false
		}
	}
	if p.DeductionType == payment.DeductionTypeSubscription && p.SubDaysToDeduct > 0 && p.SubscriptionID > 0 {
		if _, err := s.subscriptionSvc.ExtendSubscription(ctx, p.SubscriptionID, p.SubDaysToDeduct); err != nil {
			slog.Error("[CRITICAL] subscription rollback failed", "orderID", p.OrderID, "subID", p.SubscriptionID, "days", p.SubDaysToDeduct, "error", err)
			s.writeAuditLog(ctx, p.OrderID, "REFUND_ROLLBACK_FAILED", "admin", map[string]any{"gatewayError": psErrMsg(gErr), "rollbackError": psErrMsg(err), "subDaysDeducted": p.SubDaysToDeduct})
			return false
		}
	}
	return true
}

func (s *PaymentService) restoreStatus(ctx context.Context, p *RefundPlan) {
	rs := OrderStatusCompleted
	if p == nil || p.Order == nil {
		return
	}
	if p.Order.Status == OrderStatusRefundRequested {
		rs = OrderStatusRefundRequested
	} else if p.Order.Status == OrderStatusPartiallyRefunded {
		rs = OrderStatusPartiallyRefunded
	} else if p.Order.Status == OrderStatusRefundFailed {
		rs = OrderStatusRefundFailed
	}
	settled, requested := refundOrderAmounts(p.Order)
	// A known gateway failure after a direct admin attempt is no longer an
	// in-flight request. Clear it when restoring a normal completed/partial row;
	// requests that were explicitly queued by a user or a prior failed attempt
	// retain their suggested amount for the admin retry path.
	if rs == OrderStatusCompleted || rs == OrderStatusPartiallyRefunded {
		requested = 0
	}
	updated, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(p.OrderID),
		paymentorder.StatusEQ(OrderStatusRefunding),
	).SetStatus(rs).
		SetRefundAmount(settled).
		SetRefundRequestedAmount(requested).
		Save(ctx)
	if err != nil {
		slog.Warn("restore refund status failed", "orderID", p.OrderID, "error", err)
	} else if updated == 0 {
		slog.Warn("refund status changed before restore", "orderID", p.OrderID)
	}
}
