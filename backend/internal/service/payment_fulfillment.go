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

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ErrOrderNotFound is returned by HandlePaymentNotification when the webhook
// references an out_trade_no that does not exist in our DB. Callers (webhook
// handlers) should treat this as a terminal, non-retryable condition and still
// respond with a 2xx success to the provider — otherwise the provider will keep
// retrying forever (e.g. when a foreign environment's webhook endpoint is
// misconfigured to point at us, or when our orders table has been wiped).
var ErrOrderNotFound = errors.New("payment order not found")

const paymentFulfillmentLeaseDuration = 5 * time.Minute

func paymentSnapshotInt64(snapshot map[string]interface{}, key string) (int64, bool) {
	if snapshot == nil {
		return 0, false
	}
	value, ok := snapshot[key]
	if !ok {
		return 0, false
	}
	switch value := value.(type) {
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		if math.Trunc(value) != value || math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, false
		}
		return int64(value), true
	case json.Number:
		parsed, err := value.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func paymentSnapshotInt(snapshot map[string]interface{}, key string) (int, bool) {
	value, ok := paymentSnapshotInt64(snapshot, key)
	if !ok || value > int64(math.MaxInt) || value < int64(math.MinInt) {
		return 0, false
	}
	return int(value), true
}

func paymentSnapshotFloat64(snapshot map[string]interface{}, key string) float64 {
	if snapshot == nil {
		return 0
	}
	value, ok := snapshot[key]
	if !ok {
		return 0
	}
	switch value := value.(type) {
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0
		}
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return 0
		}
		return parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

func paymentSnapshotString(snapshot map[string]interface{}, key string) string {
	if snapshot == nil {
		return ""
	}
	value, ok := snapshot[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

type paymentFulfillmentLease struct {
	version time.Time
}

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
		slog.Error("order not found", "orderID", oid)
		return nil
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
	previousStatus := o.Status
	now := time.Now()
	grace := now.Add(-paymentGraceMinutes * time.Minute)
	c, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(o.ID),
		paymentorder.Or(
			paymentorder.And(
				paymentorder.StatusEQ(OrderStatusPending),
				paymentorder.ExpiresAtGTE(grace),
			),
			paymentorder.And(
				paymentorder.StatusEQ(OrderStatusCancelled),
				paymentorder.UpdatedAtGTE(grace),
			),
			paymentorder.And(
				paymentorder.StatusEQ(OrderStatusExpired),
				paymentorder.UpdatedAtGTE(grace),
			),
			paymentorder.And(
				paymentorder.StatusEQ(OrderStatusFailed),
				paymentorder.PaidAtIsNil(),
				paymentorder.ExpiresAtGTE(grace),
			),
		),
	).SetStatus(OrderStatusPaid).SetPayAmount(paid).SetPaymentTradeNo(tradeNo).SetPaidAt(now).ClearFailedAt().ClearFailedReason().Save(ctx)
	if err != nil {
		return fmt.Errorf("update to PAID: %w", err)
	}
	if c == 0 {
		return s.alreadyProcessed(ctx, o)
	}
	if previousStatus == OrderStatusCancelled || previousStatus == OrderStatusExpired {
		slog.Info("order recovered from webhook payment success",
			"orderID", o.ID,
			"previousStatus", previousStatus,
			"tradeNo", tradeNo,
			"provider", pk,
		)
		s.writeAuditLog(ctx, o.ID, "ORDER_RECOVERED", pk, map[string]any{
			"previous_status": previousStatus,
			"tradeNo":         tradeNo,
			"paidAmount":      paid,
			"reason":          "webhook payment success received after order " + previousStatus,
		})
	}
	s.writeAuditLog(ctx, o.ID, "ORDER_PAID", pk, map[string]any{"tradeNo": tradeNo, "paidAmount": paid})
	return s.executeFulfillment(ctx, o.ID)
}

func (s *PaymentService) alreadyProcessed(ctx context.Context, o *dbent.PaymentOrder) error {
	cur, err := s.entClient.PaymentOrder.Get(ctx, o.ID)
	if err != nil {
		return nil
	}
	switch cur.Status {
	case OrderStatusCompleted, OrderStatusRefunded:
		return nil
	case OrderStatusFailed, OrderStatusPaid, OrderStatusRecharging:
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
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}
	switch o.OrderType {
	case payment.OrderTypeSubscription:
		return s.ExecuteSubscriptionFulfillment(ctx, oid)
	case payment.OrderTypeSubscriptionResetCards:
		return s.ExecuteSubscriptionResetCardsFulfillment(ctx, oid)
	case payment.OrderTypeBalance:
		return s.ExecuteBalanceFulfillment(ctx, oid)
	default:
		return infraerrors.BadRequest("INVALID_ORDER_TYPE", "unsupported payment order type")
	}
}

func (s *PaymentService) ExecuteBalanceFulfillment(ctx context.Context, oid int64) error {
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
	lease, err := s.acquirePaymentFulfillmentLease(ctx, o)
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	if err := s.doBalance(ctx, o, lease); err != nil {
		s.markFailed(ctx, oid, lease, err)
		return err
	}
	return nil
}

func (s *PaymentService) acquirePaymentFulfillmentLease(ctx context.Context, o *dbent.PaymentOrder) (*paymentFulfillmentLease, error) {
	if o == nil {
		return nil, infraerrors.BadRequest("INVALID_STATUS", "nil payment order")
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	staleBefore := now.Add(-paymentFulfillmentLeaseDuration)
	updated, err := s.entClient.PaymentOrder.Update().
		Where(
			paymentorder.IDEQ(o.ID),
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
		current, getErr := s.entClient.PaymentOrder.Get(ctx, o.ID)
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
	claimed, err := s.entClient.PaymentOrder.Get(ctx, o.ID)
	if err != nil {
		return nil, fmt.Errorf("reload acquired fulfillment lease: %w", err)
	}
	if claimed.Status != OrderStatusRecharging {
		return nil, infraerrors.Conflict("CONFLICT", "fulfillment lease was lost")
	}
	return &paymentFulfillmentLease{version: claimed.UpdatedAt}, nil
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
func resolveRedeemAction(existing *RedeemCode, lookupErr error) redeemAction {
	if existing == nil || lookupErr != nil {
		return redeemActionCreate
	}
	if existing.IsUsed() {
		return redeemActionSkipCompleted
	}
	return redeemActionRedeem
}

func (s *PaymentService) doBalance(ctx context.Context, o *dbent.PaymentOrder, lease *paymentFulfillmentLease) error {
	// Idempotency: check if redeem code already exists (from a previous partial run)
	existing, lookupErr := s.redeemService.GetByCode(ctx, o.RechargeCode)
	action := resolveRedeemAction(existing, lookupErr)

	switch action {
	case redeemActionSkipCompleted:
		if err := grantPaymentOrderConcurrency(ctx, s.entClient, o); err != nil {
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
	if _, err := s.redeemService.Redeem(ContextSkipRedeemAffiliate(ctx), o.UserID, o.RechargeCode); err != nil {
		return fmt.Errorf("redeem balance: %w", err)
	}
	if err := grantPaymentOrderConcurrency(ctx, s.entClient, o); err != nil {
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
	lease, err := s.acquirePaymentFulfillmentLease(ctx, o)
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	if err := s.doSub(ctx, o, lease); err != nil {
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

// ExecuteSubscriptionResetCardsFulfillment grants standalone reset cards after
// payment. Its grant audit is committed in the same transaction as the insert,
// so provider retries cannot create an extra batch.
func (s *PaymentService) ExecuteSubscriptionResetCardsFulfillment(ctx context.Context, oid int64) error {
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
	lease, err := s.acquirePaymentFulfillmentLease(ctx, o)
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	if err := s.doSubscriptionResetCards(ctx, o, lease); err != nil {
		s.markFailed(ctx, oid, lease, err)
		return err
	}
	return nil
}

func (s *PaymentService) doSubscriptionResetCards(ctx context.Context, o *dbent.PaymentOrder, lease *paymentFulfillmentLease) error {
	groupID, err := s.grantPurchasedResetCards(ctx, o)
	if err != nil {
		return err
	}
	if err := s.applyAffiliateRebateForOrder(ctx, o); err != nil {
		return err
	}
	if err := s.markCompleted(ctx, o, lease, "SUBSCRIPTION_RESET_CARDS_SUCCESS"); err != nil {
		return err
	}
	if s.subscriptionSvc != nil && groupID > 0 {
		if err := s.subscriptionSvc.invalidateSubscriptionCaches(o.UserID, groupID); err != nil {
			slog.Warn("subscription cache invalidation after reset-card purchase failed", "orderID", o.ID, "userID", o.UserID, "groupID", groupID, "err", err)
		}
	}
	return nil
}

func (s *PaymentService) grantPurchasedResetCards(ctx context.Context, o *dbent.PaymentOrder) (int64, error) {
	if o == nil || o.ProductSnapshot == nil {
		return 0, infraerrors.BadRequest("INVALID_PRODUCT_SNAPSHOT", "reset card purchase is missing product details")
	}
	subscriptionID, ok := paymentSnapshotInt64(o.ProductSnapshot, "subscription_id")
	if !ok || subscriptionID <= 0 {
		return 0, infraerrors.BadRequest("INVALID_PRODUCT_SNAPSHOT", "reset card purchase is missing subscription")
	}
	quantity, ok := paymentSnapshotInt(o.ProductSnapshot, "quantity")
	if !ok || quantity < 1 || quantity > maxResetCardPurchaseQuantity {
		return 0, infraerrors.BadRequest("INVALID_PRODUCT_SNAPSHOT", "reset card purchase has invalid quantity")
	}
	validityDays, ok := paymentSnapshotInt(o.ProductSnapshot, "validity_days")
	if !ok || validityDays != resetCardPurchaseValidityDays {
		return 0, infraerrors.BadRequest("INVALID_PRODUCT_SNAPSHOT", "reset card purchase has invalid validity")
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin reset card purchase tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	auditOrderID := strconv.FormatInt(o.ID, 10)
	audit, auditErr := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(auditOrderID),
		paymentauditlog.ActionEQ("SUBSCRIPTION_RESET_CARDS_GRANTED"),
	).Only(txCtx)
	if auditErr != nil && !dbent.IsNotFound(auditErr) {
		return 0, fmt.Errorf("check reset card purchase audit: %w", auditErr)
	}
	alreadyGranted := auditErr == nil
	if alreadyGranted {
		var detail struct {
			GroupID int64 `json:"groupID"`
		}
		_ = json.Unmarshal([]byte(audit.Detail), &detail)
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit already granted reset card purchase: %w", err)
		}
		return detail.GroupID, nil
	}

	now := time.Now()
	subscription, err := client.UserSubscription.Query().Where(
		usersubscription.IDEQ(subscriptionID),
		usersubscription.UserIDEQ(o.UserID),
		usersubscription.StatusEQ(SubscriptionStatusActive),
		usersubscription.ExpiresAtGT(now),
	).ForUpdate().Only(txCtx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return 0, infraerrors.BadRequest("RESET_CARD_SUBSCRIPTION_UNAVAILABLE", "an active subscription is required to receive reset cards")
		}
		return 0, fmt.Errorf("lock reset card purchase subscription: %w", err)
	}
	// A concurrent worker can have read the missing audit before waiting on the
	// subscription row lock. Re-read after FOR UPDATE so it observes the first
	// worker's committed grant and exits idempotently instead of colliding with
	// the unique (order_id, action) audit key.
	if !alreadyGranted {
		audit, auditErr = client.PaymentAuditLog.Query().Where(
			paymentauditlog.OrderIDEQ(auditOrderID),
			paymentauditlog.ActionEQ("SUBSCRIPTION_RESET_CARDS_GRANTED"),
		).Only(txCtx)
		if auditErr != nil && !dbent.IsNotFound(auditErr) {
			return 0, fmt.Errorf("recheck reset card purchase audit: %w", auditErr)
		}
		if auditErr == nil {
			var detail struct {
				GroupID int64 `json:"groupID"`
			}
			_ = json.Unmarshal([]byte(audit.Detail), &detail)
			if err := tx.Commit(); err != nil {
				return 0, fmt.Errorf("commit already granted reset card purchase: %w", err)
			}
			return detail.GroupID, nil
		}
	}
	if snapshotPlatform := paymentSnapshotString(o.ProductSnapshot, "platform"); snapshotPlatform != "" &&
		!strings.EqualFold(snapshotPlatform, strings.TrimSpace(subscription.Platform)) {
		return 0, infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", "subscription platform changed before reset cards were granted")
	}
	if snapshotGroupID, ok := paymentSnapshotInt64(o.ProductSnapshot, "group_id"); ok && snapshotGroupID != subscription.GroupID {
		return 0, infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", "subscription group changed before reset cards were granted")
	}
	if err := validatePurchasedResetCardSource(subscription, o.ProductSnapshot, o.Amount, quantity); err != nil {
		return 0, err
	}
	expiresAt := now.AddDate(0, 0, validityDays)
	rows, err := client.QueryContext(txCtx, `
		INSERT INTO subscription_reset_grants (
			subscription_id, user_id, group_id, quantity, used_count,
			expires_at, issued_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 0, $5, NULL, $6, $6)
		RETURNING id
	`, subscription.ID, o.UserID, subscription.GroupID, quantity, expiresAt, now)
	if err != nil {
		return 0, fmt.Errorf("insert purchased reset cards: %w", err)
	}
	var grantID int64
	if rows.Next() {
		if err := rows.Scan(&grantID); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan purchased reset card grant: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("iterate purchased reset card grant: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close purchased reset card grant: %w", err)
	}
	if grantID == 0 {
		return 0, fmt.Errorf("purchased reset card grant was not created")
	}
	detail, _ := json.Marshal(map[string]any{
		"subscriptionID": subscription.ID,
		"groupID":        subscription.GroupID,
		"quantity":       quantity,
		"validityDays":   validityDays,
		"grantID":        grantID,
	})
	if _, err := client.PaymentAuditLog.Create().
		SetOrderID(auditOrderID).
		SetAction("SUBSCRIPTION_RESET_CARDS_GRANTED").
		SetDetail(string(detail)).
		SetOperator("system").
		Save(txCtx); err != nil {
		return 0, fmt.Errorf("record reset card purchase audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit reset card purchase: %w", err)
	}
	return subscription.GroupID, nil
}

func validatePurchasedResetCardSource(subscription *dbent.UserSubscription, snapshot map[string]interface{}, orderAmount float64, quantity int) error {
	if subscription == nil || snapshot == nil {
		return infraerrors.BadRequest("INVALID_PRODUCT_SNAPSHOT", "reset card purchase is missing source terms")
	}
	sourcePlanID, hasSourcePlanID := paymentSnapshotInt64(snapshot, "source_plan_id")
	if !hasSourcePlanID || sourcePlanID <= 0 || subscription.PlanID == nil || *subscription.PlanID != sourcePlanID {
		return infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", "subscription plan changed before reset cards were granted")
	}
	_, hasSourcePrice := snapshot["source_plan_price"]
	if !hasSourcePrice {
		return infraerrors.BadRequest("INVALID_PRODUCT_SNAPSHOT", "reset card purchase is missing source price")
	}
	expectedPrice := paymentSnapshotFloat64(snapshot, "source_plan_price")
	if expectedPrice <= 0 || subscription.PlanPrice == nil || math.Abs(*subscription.PlanPrice-expectedPrice) > 0.005 {
		return infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", "subscription price changed before reset cards were granted")
	}
	if sourceDays, ok := paymentSnapshotInt(snapshot, "source_plan_validity_days"); !ok || sourceDays <= 0 || subscription.PlanValidityDays == nil || *subscription.PlanValidityDays != sourceDays {
		return infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", "subscription validity changed before reset cards were granted")
	}
	unitPrice := roundUpPaymentAmount(expectedPrice / 3)
	snapshotUnitPrice := paymentSnapshotFloat64(snapshot, "unit_price")
	expectedAmount := roundUpPaymentAmount(unitPrice * float64(quantity))
	if snapshotUnitPrice <= 0 || math.Abs(snapshotUnitPrice-unitPrice) > 0.005 || math.Abs(orderAmount-expectedAmount) > 0.005 {
		return infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", "reset card purchase price changed before cards were granted")
	}
	return nil
}

func (s *PaymentService) ensurePaymentSubscriptionAssigned(ctx context.Context, o *dbent.PaymentOrder, groupID int64, days int) error {
	if s == nil || s.entClient == nil || o == nil {
		return errors.New("subscription fulfillment is unavailable")
	}
	if s.groupRepo == nil {
		return errors.New("subscription group repository is unavailable")
	}
	group, err := s.groupRepo.GetByID(ctx, groupID)
	if err != nil || group == nil || !group.IsSubscriptionType() {
		return fmt.Errorf("group %d no longer exists or is not a subscription group", groupID)
	}
	platform := strings.TrimSpace(group.Platform)
	if platform == "" {
		return errors.New("subscription group has no platform")
	}
	if snapshotPlatform := paymentSnapshotString(o.ProductSnapshot, "platform"); snapshotPlatform != "" && !strings.EqualFold(snapshotPlatform, platform) {
		return infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", "subscription platform changed before payment completed")
	}
	operation := paymentSnapshotString(o.ProductSnapshot, "operation")
	if operation == "" {
		// Orders created before operation snapshots used the old same-group
		// assign-or-extend semantics. Treat them as renewals for compatibility.
		operation = subscriptionPurchaseOperationRenew
	}
	if operation != subscriptionPurchaseOperationSubscribe &&
		operation != subscriptionPurchaseOperationRenew &&
		operation != subscriptionPurchaseOperationResubscribe &&
		operation != subscriptionPurchaseOperationUpgrade {
		return fmt.Errorf("unsupported subscription purchase operation %q", operation)
	}
	targetPlanID := o.PlanID
	targetPrice := paymentSnapshotFloat64(o.ProductSnapshot, "price")
	if targetPrice <= 0 {
		targetPrice = o.Amount
	}
	targetDays := days
	if snapshotDays, ok := paymentSnapshotInt(o.ProductSnapshot, "subscription_days"); ok && snapshotDays > 0 {
		targetDays = snapshotDays
	}
	if targetDays <= 0 {
		return errors.New("subscription order has invalid validity")
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

	orderNote := paymentSubscriptionOrderNote(o.ID)
	query := txClient.UserSubscription.Query().Where(
		usersubscription.UserIDEQ(o.UserID),
		usersubscription.PlatformEQ(platform),
	).ForUpdate()
	existing, lookupErr := query.Only(txCtx)
	if dbent.IsNotFound(lookupErr) {
		existing = nil
		lookupErr = nil
	}
	if lookupErr != nil {
		return fmt.Errorf("lock existing platform subscription: %w", lookupErr)
	}
	oldGroupID := int64(0)
	if existing != nil {
		oldGroupID = existing.GroupID
	}

	if !alreadyAssigned {
		if existing != nil && hasPaymentSubscriptionOrderNote(subscriptionEntityNotes(existing), orderNote) {
			alreadyAssigned = true
		} else {
			if err := validatePaymentSubscriptionSource(existing, o.ProductSnapshot); err != nil {
				return err
			}
			var sourceID int64
			if parsed, ok := paymentSnapshotInt64(o.ProductSnapshot, "source_subscription_id"); ok {
				sourceID = parsed
			}
			if err := applyPaymentSubscriptionTerm(txCtx, txClient, existing, o, groupID, platform, targetPlanID, targetPrice, targetDays, operation, sourceID, orderNote); err != nil {
				return err
			}
		}
	}

	if !alreadyAssigned {
		detail, _ := json.Marshal(map[string]any{
			"groupID":       groupID,
			"platform":      platform,
			"validityDays":  targetDays,
			"operation":     operation,
			"previousGroup": oldGroupID,
		})
		if _, err := txClient.PaymentAuditLog.Create().
			SetOrderID(strconv.FormatInt(o.ID, 10)).
			SetAction("SUBSCRIPTION_ASSIGNED").
			SetDetail(string(detail)).
			SetOperator("system").
			Save(txCtx); err != nil {
			return fmt.Errorf("record subscription assignment audit: %w", err)
		}
	} else {
		slog.Info("subscription already assigned for order, skipping", "orderID", o.ID, "groupID", groupID, "operation", operation)
	}
	if err := grantPaymentProductEntitlements(txCtx, txClient, o, groupID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit subscription fulfillment tx: %w", err)
	}
	if s.subscriptionSvc != nil {
		for _, cacheGroupID := range uniqueCacheGroupIDs(oldGroupID, groupID) {
			if err := s.subscriptionSvc.invalidateSubscriptionCaches(o.UserID, cacheGroupID); err != nil {
				// The entitlement transaction has already committed. Cache
				// convergence is recoverable and must not fail a paid order.
				slog.Warn("subscription cache invalidation after payment fulfillment failed", "orderID", o.ID, "userID", o.UserID, "groupID", cacheGroupID, "err", err)
			}
		}
	}
	s.invalidatePaymentAuthCache(ctx, o.UserID)
	return nil
}

func applyPaymentSubscriptionTerm(ctx context.Context, client *dbent.Client, existing *dbent.UserSubscription, order *dbent.PaymentOrder, groupID int64, platform string, planID *int64, planPrice float64, days int, operation string, sourceID int64, orderNote string) error {
	if client == nil || order == nil {
		return errors.New("subscription term input is missing")
	}
	now := time.Now()
	if existing != nil && sourceID > 0 && existing.ID != sourceID && operation == subscriptionPurchaseOperationUpgrade {
		return infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", "the source subscription changed before payment completed")
	}
	if operation == subscriptionPurchaseOperationUpgrade {
		if existing == nil || existing.Status != SubscriptionStatusActive || !existing.ExpiresAt.After(now) {
			return infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", "the source subscription is no longer active")
		}
		if existing.PlanPrice == nil || existing.PlanValidityDays == nil ||
			!subscriptionUnitPriceHigher(planPrice, days, *existing.PlanPrice, *existing.PlanValidityDays) {
			return infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", "the target subscription is no longer a higher tier")
		}
		if existing.GroupID == groupID && existing.PlanID != nil && planID != nil && *existing.PlanID == *planID {
			return nil
		}
		if sourceID > 0 && existing.ID != sourceID {
			return infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", "the source subscription changed before payment completed")
		}
		return updatePaymentSubscriptionRow(ctx, client, existing, groupID, platform, planID, planPrice, days, existing.StartsAt, existing.ExpiresAt, false, orderNote)
	}

	if existing != nil && existing.Status == SubscriptionStatusSuspended && existing.ExpiresAt.After(now) {
		return infraerrors.Conflict("SUBSCRIPTION_NOT_ACTIVE", "suspended subscriptions must be restored before purchase")
	}
	if existing != nil && existing.ExpiresAt.After(now) && existing.GroupID != groupID {
		return infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", "another subscription on this platform is already active")
	}
	if existing == nil {
		startsAt := now
		expiresAt := paymentSubscriptionExpiresAt(startsAt, days)
		builder := client.UserSubscription.Create().
			SetUserID(order.UserID).
			SetGroupID(groupID).
			SetPlatform(platform).
			SetStartsAt(startsAt).
			SetExpiresAt(expiresAt).
			SetStatus(SubscriptionStatusActive).
			SetDailyWindowStart(startsAt).
			SetWeeklyWindowStart(startsAt).
			SetMonthlyWindowStart(startsAt).
			SetDailyUsageUsd(0).
			SetWeeklyUsageUsd(0).
			SetMonthlyUsageUsd(0).
			SetNotes(orderNote).
			SetNillablePlanID(planID).
			SetNillablePlanPrice(optionalFloat64(planPrice)).
			SetNillablePlanValidityDays(optionalInt(days))
		if _, err := builder.Save(ctx); err != nil {
			return fmt.Errorf("create paid subscription: %w", err)
		}
		return nil
	}

	if hasPaymentSubscriptionOrderNote(subscriptionEntityNotes(existing), orderNote) {
		return nil
	}
	if existing.ExpiresAt.After(now) {
		newExpiresAt := paymentSubscriptionExpiresAt(existing.ExpiresAt, days)
		return updatePaymentSubscriptionRow(ctx, client, existing, groupID, platform, planID, planPrice, days, existing.StartsAt, newExpiresAt, false, orderNote)
	}
	newExpiresAt := paymentSubscriptionExpiresAt(now, days)
	return updatePaymentSubscriptionRow(ctx, client, existing, groupID, platform, planID, planPrice, days, now, newExpiresAt, true, orderNote)
}

// validatePaymentSubscriptionSource prevents a delayed paid order from
// applying a quote against a subscription that changed after checkout. The
// source fields are present only on newly-created orders, so old orders keep
// their historical fulfillment behavior.
func validatePaymentSubscriptionSource(existing *dbent.UserSubscription, snapshot map[string]interface{}) error {
	if existing == nil || snapshot == nil {
		return nil
	}
	conflict := func(message string) error {
		return infraerrors.Conflict("SUBSCRIPTION_PURCHASE_TERMS_CHANGED", message)
	}
	if sourceID, ok := paymentSnapshotInt64(snapshot, "source_subscription_id"); ok && sourceID > 0 && existing.ID != sourceID {
		return conflict("the source subscription changed before payment completed")
	}
	if sourceGroupID, ok := paymentSnapshotInt64(snapshot, "source_group_id"); ok && sourceGroupID > 0 && existing.GroupID != sourceGroupID {
		return conflict("the source subscription group changed before payment completed")
	}
	if sourcePlanID, ok := paymentSnapshotInt64(snapshot, "source_plan_id"); ok && sourcePlanID > 0 {
		if existing.PlanID == nil || *existing.PlanID != sourcePlanID {
			return conflict("the source subscription plan changed before payment completed")
		}
	}
	if _, hasSourcePrice := snapshot["source_plan_price"]; hasSourcePrice {
		sourcePrice := paymentSnapshotFloat64(snapshot, "source_plan_price")
		if sourcePrice <= 0 || existing.PlanPrice == nil || math.Abs(*existing.PlanPrice-sourcePrice) > 0.005 {
			return conflict("the source subscription price changed before payment completed")
		}
	}
	if sourceDays, ok := paymentSnapshotInt(snapshot, "source_plan_validity_days"); ok && sourceDays > 0 {
		if existing.PlanValidityDays == nil || *existing.PlanValidityDays != sourceDays {
			return conflict("the source subscription validity changed before payment completed")
		}
	}
	return nil
}

func paymentSubscriptionExpiresAt(startsAt time.Time, days int) time.Time {
	expiresAt := startsAt.AddDate(0, 0, days)
	if expiresAt.After(MaxExpiresAt) {
		return MaxExpiresAt
	}
	return expiresAt
}

func updatePaymentSubscriptionRow(ctx context.Context, client *dbent.Client, existing *dbent.UserSubscription, groupID int64, platform string, planID *int64, planPrice float64, days int, startsAt, expiresAt time.Time, resetUsage bool, note string) error {
	update := client.UserSubscription.UpdateOneID(existing.ID).
		SetGroupID(groupID).
		SetPlatform(platform).
		SetStartsAt(startsAt).
		SetExpiresAt(expiresAt).
		SetStatus(SubscriptionStatusActive).
		SetNillablePlanID(planID).
		SetNillablePlanPrice(optionalFloat64(planPrice)).
		SetNillablePlanValidityDays(optionalInt(days)).
		SetNotes(appendSubscriptionNotes(subscriptionEntityNotes(existing), note))
	if resetUsage {
		update.SetDailyWindowStart(startsAt).
			SetWeeklyWindowStart(startsAt).
			SetMonthlyWindowStart(startsAt).
			SetDailyUsageUsd(0).
			SetWeeklyUsageUsd(0).
			SetMonthlyUsageUsd(0)
	}
	if _, err := update.Save(ctx); err != nil {
		return fmt.Errorf("update paid subscription term: %w", err)
	}
	return nil
}

func optionalFloat64(value float64) *float64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func subscriptionEntityNotes(subscription *dbent.UserSubscription) string {
	if subscription == nil || subscription.Notes == nil {
		return ""
	}
	return *subscription.Notes
}

func optionalInt(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}

func uniqueCacheGroupIDs(values ...int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (s *PaymentService) invalidatePaymentAuthCache(ctx context.Context, userID int64) {
	if s != nil && s.authCacheInvalidator != nil && userID > 0 {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
}

func grantPaymentProductEntitlements(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, groupID int64) error {
	if order == nil || client == nil {
		return nil
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
		return nil
	}

	entitlements, err := paymentOrderEntitlementsStrict(order)
	if err != nil {
		return err
	}
	if entitlements.BalanceBonus <= 0 && entitlements.ResetCardCount <= 0 && entitlements.Concurrency <= 0 {
		return nil
	}
	if entitlements.Concurrency > 0 {
		if err := setPaymentUserConcurrencyAtLeast(ctx, client, order.UserID, entitlements.Concurrency); err != nil {
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
		now := time.Now()
		expiresAt := now.Add(time.Duration(entitlements.ResetCardExpiryDays) * 24 * time.Hour)
		rows, err := client.QueryContext(ctx, `
			INSERT INTO subscription_reset_grants (
				subscription_id, user_id, group_id, quantity, used_count,
				expires_at, issued_by, created_at, updated_at
			)
			SELECT us.id, us.user_id, us.group_id, $3, 0, $4, NULL, $5, $5
			FROM user_subscriptions us
			WHERE us.user_id = $1 AND us.group_id = $2
				AND us.deleted_at IS NULL AND us.status = 'active' AND us.expires_at > $5
			RETURNING id
		`, order.UserID, groupID, entitlements.ResetCardCount, expiresAt, now)
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
	return nil
}

// grantPaymentOrderConcurrency applies a balance-order concurrency target in
// its own transaction. The audit row and user update commit together, so a
// crash can only leave a retryable order; the monotonic target update is safe
// to repeat and safe if callbacks for different tiers arrive out of order.
func grantPaymentOrderConcurrency(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder) error {
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
	if err := setPaymentUserConcurrencyAtLeast(txCtx, tx.Client(), order.UserID, entitlements.Concurrency); err != nil {
		return fmt.Errorf("grant balance concurrency: %w", err)
	}
	detail, _ := json.Marshal(map[string]any{"concurrency": entitlements.Concurrency})
	if _, err := tx.Client().PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("PAYMENT_CONCURRENCY_GRANTED").
		SetDetail(string(detail)).
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
	raw, ok := order.ProductSnapshot["entitlements"].(map[string]interface{})
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
	switch o.OrderType {
	case payment.OrderTypeBalance, payment.OrderTypeSubscription, payment.OrderTypeSubscriptionResetCards:
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

func (s *PaymentService) RetryFulfillment(ctx context.Context, oid int64) error {
	o, err := s.entClient.PaymentOrder.Get(ctx, oid)
	if err != nil {
		return infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.PaidAt == nil {
		return infraerrors.BadRequest("INVALID_STATUS", "order is not paid")
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
