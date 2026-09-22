package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	localCancellationWorkClose        = "CLOSE"
	localCancellationWorkDirectRefund = "DIRECT_REFUND"
	localCancellationWorkPending      = "PENDING"
	localCancellationWorkCompleted    = "COMPLETED"
	localCancellationWorkManualReview = "MANUAL_REVIEW"

	cancelLatePaymentRefundKind = "cancel_late_payment"
)

// localCancellationWork is deliberately a narrow durable work record. It
// contains only provider correlation and lease state; trusted payment facts
// continue to live on payment_orders and unified refund attempts.
type localCancellationWork struct {
	OrderID          int64
	WorkKind         string
	ProviderKey      string
	PaymentTradeNo   string
	ProviderRefundID string
	ProviderStatus   string
	AmountFen        int64
	Currency         string
	IdempotencyKey   string
	Attempts         int
	Status           string
	AvailableAt      time.Time
	ClaimedAt        *time.Time
	ClaimedBy        string
	LastError        string
	CompletedAt      *time.Time
}

// cancelOrderLocally is the product-side admission fence. It never calls a
// payment provider while the order row is locked. The close job is inserted in
// the same transaction so a successful response cannot leave a payable local
// order without durable follow-up work.
func (s *PaymentService) cancelOrderLocally(ctx context.Context, orderID, userID int64, operator, detail string, requireOwner bool) (string, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	order, err := lockUnifiedRefundOrder(txCtx, client, orderID)
	if err != nil {
		if dbent.IsNotFound(err) {
			return "", infraerrors.NotFound("NOT_FOUND", "order not found")
		}
		return "", err
	}
	if requireOwner && order.UserID != userID {
		return "", infraerrors.Forbidden("FORBIDDEN", "no permission for this order")
	}
	// Cancellation is the immutable product decision. A later trusted payment is
	// deliberately recorded as a separate refund fact while the row remains
	// CANCELLED, so replaying the original cancellation must report cancelled
	// rather than letting the late-money evidence redefine the user result.
	if order.Status == OrderStatusCancelled {
		if err := ensureLocalCancellationCloseWorkTx(txCtx, client, order); err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return checkPaidResultCancelled, nil
	}
	if localCancellationOrderIsPaid(order) {
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return checkPaidResultAlreadyPaid, nil
	}
	if order.Status != OrderStatusPending {
		return "", infraerrors.BadRequest("INVALID_STATUS", "order cannot be cancelled in current status")
	}
	if paymentOrderHasDiscount(order) {
		if err := releasePaymentDiscount(txCtx, client, order.ID); err != nil {
			return "", err
		}
	}
	if _, err := client.PaymentOrder.Update().Where(
		paymentorder.IDEQ(order.ID),
		paymentorder.StatusEQ(OrderStatusPending),
		paymentorder.PaidAtIsNil(),
	).SetStatus(OrderStatusCancelled).ClearPayURL().ClearQrCode().ClearQrCodeImg().Save(txCtx); err != nil {
		return "", fmt.Errorf("persist local cancellation: %w", err)
	}
	order.Status = OrderStatusCancelled
	if err := ensureLocalCancellationCloseWorkTx(txCtx, client, order); err != nil {
		return "", err
	}
	if err := writePaymentAuditTx(txCtx, client, order.ID, "ORDER_CANCELLED", operator, map[string]any{
		"detail": detail, "local_only": true,
	}); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return checkPaidResultCancelled, nil
}

func localCancellationOrderIsPaid(order *dbent.PaymentOrder) bool {
	if order == nil {
		return false
	}
	if order.PaidAt != nil {
		return true
	}
	return psSliceContains([]string{
		OrderStatusPaid,
		OrderStatusRecharging,
		OrderStatusCompleted,
		OrderStatusRefundRequested,
		OrderStatusRefunding,
		OrderStatusRefundPending,
		OrderStatusPartiallyRefunded,
		OrderStatusRefunded,
		OrderStatusRefundFailed,
	}, order.Status)
}

func localCancellationProviderKey(order *dbent.PaymentOrder) string {
	if order == nil {
		return ""
	}
	if snapshot := psOrderProviderSnapshot(order); snapshot != nil && strings.TrimSpace(snapshot.ProviderKey) != "" {
		return strings.TrimSpace(snapshot.ProviderKey)
	}
	if key := strings.TrimSpace(psStringValue(order.ProviderKey)); key != "" {
		return key
	}
	return strings.TrimSpace(order.PaymentType)
}

func localCancellationCloseIdempotencyKey(orderID int64) string {
	return "sub2:cancel:" + strconv.FormatInt(orderID, 10) + ":close"
}

func localCancellationDirectRefundIdempotencyKey(orderID int64) string {
	return "sub2:cancel:" + strconv.FormatInt(orderID, 10) + ":late-refund"
}

func localCancellationDirectRefundReference(orderID int64) string {
	return "sub2-cancel-" + strconv.FormatInt(orderID, 10)
}

func ensureLocalCancellationCloseWorkTx(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder) error {
	if order == nil {
		return errors.New("local cancellation work order is missing")
	}
	return ensureLocalCancellationCloseWorkBindingTx(ctx, client, order,
		localCancellationProviderKey(order), strings.TrimSpace(order.PaymentTradeNo))
}

// ensureLocalCancellationCloseWorkBindingTx records provider correlation that
// arrives after a local cancellation, such as a CreatePayment response racing
// the browser's cancel request. It never changes an existing terminal work
// state, so a cancellation replay cannot reopen a completed close job.
func ensureLocalCancellationCloseWorkBindingTx(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, providerKey, paymentTradeNo string) error {
	if order == nil {
		return errors.New("local cancellation work order is missing")
	}
	providerKey = strings.TrimSpace(providerKey)
	paymentTradeNo = strings.TrimSpace(paymentTradeNo)
	_, err := client.ExecContext(ctx, `
		INSERT INTO payment_local_cancellation_work
		(order_id, work_kind, status, provider_key, payment_trade_no, amount_fen, currency, idempotency_key)
		VALUES ($1,$2,$3,$4,$5,0,$6,$7)
		ON CONFLICT (order_id, work_kind) DO UPDATE
		SET provider_key = CASE WHEN excluded.provider_key <> '' THEN excluded.provider_key ELSE payment_local_cancellation_work.provider_key END,
			payment_trade_no = CASE WHEN excluded.payment_trade_no <> '' THEN excluded.payment_trade_no ELSE payment_local_cancellation_work.payment_trade_no END,
			updated_at = CURRENT_TIMESTAMP`,
		order.ID, localCancellationWorkClose, localCancellationWorkPending, providerKey,
		paymentTradeNo, PaymentOrderCurrency(order), localCancellationCloseIdempotencyKey(order.ID),
	)
	return err
}

// bindCancelledProviderCreateResponse persists the only parts of a late
// CreatePayment response that recovery needs. It intentionally clears all
// checkout presentation fields and keeps CANCELLED immutable: callers must
// never return a usable checkout after local cancellation won the row lock.
func (s *PaymentService) bindCancelledProviderCreateResponse(ctx context.Context, orderID int64, sel *payment.InstanceSelection, response *payment.CreatePaymentResponse, responseSnapshot map[string]any) (bool, error) {
	if response == nil {
		return false, errors.New("provider create response is missing")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	order, err := lockUnifiedRefundOrder(txCtx, client, orderID)
	if err != nil {
		return false, err
	}
	if order.Status != OrderStatusCancelled {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}

	snapshot := mergeLocalCancellationProviderResponseSnapshot(order.ProviderSnapshot, responseSnapshot)
	update := client.PaymentOrder.UpdateOneID(order.ID).
		ClearPayURL().ClearQrCode().ClearQrCodeImg()
	if strings.TrimSpace(order.PaymentTradeNo) == "" && strings.TrimSpace(response.TradeNo) != "" {
		update.SetPaymentTradeNo(strings.TrimSpace(response.TradeNo))
	}
	if sel != nil {
		if instanceID := strings.TrimSpace(sel.InstanceID); instanceID != "" {
			update.SetProviderInstanceID(instanceID)
		}
		if providerKey := strings.TrimSpace(sel.ProviderKey); providerKey != "" {
			update.SetProviderKey(providerKey)
		}
	}
	if snapshot != nil {
		update.SetProviderSnapshot(snapshot)
	}
	if _, err := update.Save(txCtx); err != nil {
		return false, fmt.Errorf("persist cancelled provider binding: %w", err)
	}
	providerKey := localCancellationProviderKey(order)
	if sel != nil && strings.TrimSpace(sel.ProviderKey) != "" {
		providerKey = strings.TrimSpace(sel.ProviderKey)
	}
	if err := ensureLocalCancellationCloseWorkBindingTx(txCtx, client, order, providerKey, response.TradeNo); err != nil {
		return false, err
	}
	if err := writePaymentAuditTx(txCtx, client, order.ID, "LOCAL_CANCEL_PROVIDER_CREATE_BOUND", providerKey, map[string]any{
		"provider_key": providerKey,
		"has_trade_no": strings.TrimSpace(response.TradeNo) != "",
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func mergeLocalCancellationProviderResponseSnapshot(current, response map[string]any) map[string]any {
	if response == nil {
		return clonePaymentOrderSnapshot(current)
	}
	merged := clonePaymentOrderSnapshot(current)
	if merged == nil {
		merged = make(map[string]any)
	}
	// Provider route selection is frozen before CreatePayment. Only copy
	// response-time correlation data, never dispatch/checkout state that may
	// have changed after cancellation.
	for _, key := range []string{"payment_order_id", paymentOrderCheckoutFrameURLSnapshotKey} {
		if value, ok := response[key]; ok {
			merged[key] = value
		}
	}
	return merged
}

func ensureLocalCancellationDirectRefundWorkTx(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, paid float64) error {
	if order == nil {
		return errors.New("late direct refund order is missing")
	}
	currency := PaymentOrderCurrency(order)
	amountFen, err := payment.AmountToMinorUnit(strconv.FormatFloat(paid, 'f', -1, 64), currency)
	if err != nil || amountFen <= 0 {
		return fmt.Errorf("late direct refund amount is invalid")
	}
	_, err = client.ExecContext(ctx, `
		INSERT INTO payment_local_cancellation_work
		(order_id, work_kind, status, provider_key, payment_trade_no, amount_fen, currency, idempotency_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (order_id, work_kind) DO UPDATE
		SET provider_key = CASE WHEN excluded.provider_key <> '' THEN excluded.provider_key ELSE payment_local_cancellation_work.provider_key END,
			payment_trade_no = CASE WHEN excluded.payment_trade_no <> '' THEN excluded.payment_trade_no ELSE payment_local_cancellation_work.payment_trade_no END,
			amount_fen = CASE WHEN payment_local_cancellation_work.amount_fen = 0 THEN excluded.amount_fen ELSE payment_local_cancellation_work.amount_fen END,
			currency = CASE WHEN payment_local_cancellation_work.amount_fen = 0 THEN excluded.currency ELSE payment_local_cancellation_work.currency END,
			updated_at = CURRENT_TIMESTAMP`,
		order.ID, localCancellationWorkDirectRefund, localCancellationWorkPending,
		localCancellationProviderKey(order), strings.TrimSpace(order.PaymentTradeNo), amountFen, currency,
		localCancellationDirectRefundIdempotencyKey(order.ID),
	)
	return err
}

// recordCancelledLatePaymentTx saves trusted payment evidence without changing
// the cancellation fence. It is called only after provider identity, scope and
// amount validation, while the same order row is locked by the paid claim.
func (s *PaymentService) recordCancelledLatePaymentTx(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, tradeNo string, paid float64, providerKey string) error {
	if order == nil || order.Status != OrderStatusCancelled {
		return errors.New("late payment order is not locally cancelled")
	}
	firstEvidence := order.PaidAt == nil
	if !isValidProviderAmount(paid) || math.Abs(paid-order.PayAmount) > paymentAmountToleranceForCurrency(PaymentOrderCurrency(order)) {
		if !firstEvidence {
			if err := s.fenceCancelledLatePaymentConflictTx(ctx, client, order, providerKey, "paid_amount_mismatch"); err != nil {
				return err
			}
			return nil
		}
		// A signed callback with an amount that cannot be safely correlated is
		// still evidence that provider-side money may exist. Persist a manual
		// fence and ACK the trusted callback; rolling this transaction back
		// would leave neither a refund job nor an operator-visible record.
		return s.fenceCancelledLatePaymentUnmatchedEvidenceTx(ctx, client, order, providerKey, "paid_amount_mismatch")
	}
	if !firstEvidence && !cancelledLatePaymentTradeMatches(order, tradeNo) {
		if err := s.fenceCancelledLatePaymentConflictTx(ctx, client, order, providerKey, "trade_number_mismatch"); err != nil {
			return err
		}
		return nil
	}
	if firstEvidence {
		now := time.Now()
		if _, err := client.PaymentOrder.UpdateOneID(order.ID).
			SetPaidAt(now).SetPaymentTradeNo(strings.TrimSpace(tradeNo)).SetPayAmount(paid).
			Save(ctx); err != nil {
			return fmt.Errorf("persist cancelled late payment: %w", err)
		}
		order.PaidAt = &now
		order.PaymentTradeNo = strings.TrimSpace(tradeNo)
		order.PayAmount = paid
	}
	if paymentOrderUsesUnifiedPay(order) || strings.EqualFold(strings.TrimSpace(providerKey), payment.TypeUnifiedPay) {
		if _, err := s.reserveCancelledLateUnifiedRefundAttemptTx(ctx, client, order, paid); err != nil {
			return err
		}
	} else if err := ensureLocalCancellationDirectRefundWorkTx(ctx, client, order, paid); err != nil {
		return err
	}
	return writePaymentAuditTx(ctx, client, order.ID, "LOCAL_CANCEL_LATE_PAYMENT_DETECTED", providerKey, map[string]any{
		"paid_amount": paid,
		"trade_no":    strings.TrimSpace(tradeNo),
	})
}

func (s *PaymentService) fenceCancelledLatePaymentUnmatchedEvidenceTx(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, providerKey, reason string) error {
	if err := ensureLocalCancellationCloseWorkTx(ctx, client, order); err != nil {
		return err
	}
	if err := markLocalCancellationWorkManualTx(ctx, client, order.ID, localCancellationWorkClose, reason); err != nil {
		return err
	}
	return writePaymentAuditTx(ctx, client, order.ID, "LOCAL_CANCEL_LATE_PAYMENT_CONFLICT", providerKey, map[string]any{
		"reason": reason,
	})
}

func cancelledLatePaymentTradeMatches(order *dbent.PaymentOrder, incoming string) bool {
	incoming = strings.TrimSpace(incoming)
	if incoming == "" || order == nil {
		return incoming == ""
	}
	current := strings.TrimSpace(order.PaymentTradeNo)
	if current == "" || strings.EqualFold(current, incoming) {
		return true
	}
	if paymentOrderUsesUnifiedPay(order) {
		if snapshot := psOrderProviderSnapshot(order); snapshot != nil && strings.EqualFold(current, snapshot.PaymentOrderID) {
			return true
		}
	}
	return false
}

func (s *PaymentService) fenceCancelledLatePaymentConflictTx(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, providerKey, reason string) error {
	if paymentOrderUsesUnifiedPay(order) || strings.EqualFold(strings.TrimSpace(providerKey), payment.TypeUnifiedPay) {
		attempt, err := s.reserveCancelledLateUnifiedRefundAttemptTx(ctx, client, order, order.PayAmount)
		if err != nil {
			return err
		}
		if attempt != nil {
			attempt.NeedsManualReview = true
			if err := saveUnifiedRefundAttempt(ctx, client, attempt); err != nil {
				return err
			}
		}
	} else {
		if err := ensureLocalCancellationDirectRefundWorkTx(ctx, client, order, order.PayAmount); err != nil {
			return err
		}
		if err := markLocalCancellationWorkManualTx(ctx, client, order.ID, localCancellationWorkDirectRefund, reason); err != nil {
			return err
		}
	}
	return writePaymentAuditTx(ctx, client, order.ID, "LOCAL_CANCEL_LATE_PAYMENT_CONFLICT", providerKey, map[string]any{
		"reason": reason,
	})
}

func markLocalCancellationWorkManualTx(ctx context.Context, client *dbent.Client, orderID int64, workKind, reason string) error {
	_, err := client.ExecContext(ctx, `
		UPDATE payment_local_cancellation_work
		SET status = $3::VARCHAR(24), claimed_at = NULL, claimed_by = NULL, last_error = $4, updated_at = CURRENT_TIMESTAMP
		WHERE order_id = $1 AND work_kind = $2`, orderID, workKind, localCancellationWorkManualReview, reason)
	return err
}

// recordUnifiedPaidAfterClose persists a signed central late-payment event as
// a cancellation/refund fact even when an older local row is still PENDING or
// EXPIRED. The central status itself is never eligible for product fulfillment.
func (s *PaymentService) recordUnifiedPaidAfterClose(ctx context.Context, orderID int64, tradeNo string, paid float64) error {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	order, err := lockUnifiedRefundOrder(txCtx, client, orderID)
	if err != nil {
		return err
	}
	if order.Status == OrderStatusPending || (order.Status == OrderStatusExpired && order.PaidAt == nil) {
		if order.Status == OrderStatusPending && paymentOrderHasDiscount(order) {
			if err := releasePaymentDiscount(txCtx, client, order.ID); err != nil {
				return err
			}
		}
		if _, err := client.PaymentOrder.UpdateOneID(order.ID).
			SetStatus(OrderStatusCancelled).ClearPayURL().ClearQrCode().ClearQrCodeImg().Save(txCtx); err != nil {
			return err
		}
		order.Status = OrderStatusCancelled
		if err := ensureLocalCancellationCloseWorkTx(txCtx, client, order); err != nil {
			return err
		}
	}
	if order.Status != OrderStatusCancelled {
		if err := writePaymentAuditTx(txCtx, client, order.ID, "UNIFIED_PAID_AFTER_CLOSE_CONFLICT", payment.TypeUnifiedPay, map[string]any{
			"status": order.Status,
		}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return permanentUnifiedWebhookError("paid_after_close_local_state_conflict")
	}
	if err := s.recordCancelledLatePaymentTx(txCtx, client, order, tradeNo, paid, payment.TypeUnifiedPay); err != nil {
		return err
	}
	if err := writePaymentAuditTx(txCtx, client, order.ID, "UNIFIED_PAYMENT_PAID_AFTER_CLOSE", payment.TypeUnifiedPay, map[string]any{
		"trade_no": strings.TrimSpace(tradeNo), "paid_amount": paid,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func writePaymentAuditTx(ctx context.Context, client *dbent.Client, orderID int64, action, operator string, detail map[string]any) error {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = client.ExecContext(ctx, `
		INSERT INTO payment_audit_logs (order_id, action, detail, operator, created_at)
		VALUES ($1,$2,$3,$4,CURRENT_TIMESTAMP)
		ON CONFLICT (order_id, action) DO NOTHING`,
		strconv.FormatInt(orderID, 10), action, string(encoded), operator)
	return err
}
