package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
)

const (
	// One item per run keeps the longest provider operation inside its lease.
	// The enclosing expiry singleton invokes this every minute and does not hold
	// a batch of provider calls that could begin after a lease has expired.
	localCancellationWorkerBatchSize      = 1
	localCancellationWorkerLease          = time.Minute
	localCancellationWorkerBackoff        = 10 * time.Second
	localCancellationWorkerCleanupTimeout = 5 * time.Second
)

// ReconcileLocalCancellationWork is invoked by the existing singleton expiry
// service. Work is durable and leased in PostgreSQL; the worker itself owns no
// in-memory queue or detached task state.
func (s *PaymentService) ReconcileLocalCancellationWork(ctx context.Context, workerID string) (int, error) {
	if s == nil || s.entClient == nil || !runtimegate.DurableRecoveryWorkAllowed() {
		return 0, nil
	}
	if paymentAuditDialect(s.entClient) != dialect.Postgres {
		return 0, errors.New("local cancellation recovery requires PostgreSQL")
	}
	work, err := claimLocalCancellationWork(ctx, s.entClient, workerID, localCancellationWorkerBatchSize, localCancellationWorkerLease)
	if err != nil {
		return 0, fmt.Errorf("claim local cancellation work: %w", err)
	}
	processed := 0
	var joined error
	for _, candidate := range work {
		if ctx.Err() != nil {
			if releaseErr := s.releaseUnstartedLocalCancellationWork(ctx, candidate); releaseErr != nil {
				joined = errors.Join(joined, fmt.Errorf("release unstarted local cancellation work order=%d kind=%s: %w", candidate.OrderID, candidate.WorkKind, releaseErr))
			}
			return processed, errors.Join(joined, ctx.Err())
		}
		if err := s.processLocalCancellationWork(ctx, candidate); err != nil {
			joined = errors.Join(joined, fmt.Errorf("process local cancellation work order=%d kind=%s: %w", candidate.OrderID, candidate.WorkKind, err))
			continue
		}
		processed++
		if ctx.Err() != nil {
			return processed, errors.Join(joined, ctx.Err())
		}
	}
	return processed, joined
}

func claimLocalCancellationWork(ctx context.Context, client *dbent.Client, workerID string, limit int, lease time.Duration) ([]localCancellationWork, error) {
	if client == nil || workerID == "" {
		return nil, errors.New("local cancellation worker identity is required")
	}
	if limit < 1 {
		limit = 1
	}
	leaseSeconds := int64(lease / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	rows, err := client.QueryContext(ctx, `
		WITH candidates AS (
			SELECT order_id, work_kind
			FROM payment_local_cancellation_work
			WHERE status = 'PENDING'
			  AND available_at <= NOW()
			  AND (claimed_at IS NULL OR claimed_at < NOW() - ($3::BIGINT * INTERVAL '1 second'))
			ORDER BY available_at ASC, created_at ASC, order_id ASC, work_kind ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE payment_local_cancellation_work AS work
		SET claimed_at = NOW(), claimed_by = $1, updated_at = NOW()
		FROM candidates
		WHERE work.order_id = candidates.order_id AND work.work_kind = candidates.work_kind
		RETURNING work.order_id, work.work_kind, work.provider_key, work.payment_trade_no,
			COALESCE(work.provider_refund_id, ''), COALESCE(work.provider_status, ''),
			work.amount_fen, work.currency, work.idempotency_key, work.attempts, work.status,
			work.available_at, work.claimed_at, COALESCE(work.claimed_by, ''), COALESCE(work.last_error, '')`,
		workerID, limit, leaseSeconds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]localCancellationWork, 0, limit)
	for rows.Next() {
		var candidate localCancellationWork
		var claimedAt time.Time
		if err := rows.Scan(&candidate.OrderID, &candidate.WorkKind, &candidate.ProviderKey, &candidate.PaymentTradeNo,
			&candidate.ProviderRefundID, &candidate.ProviderStatus, &candidate.AmountFen, &candidate.Currency,
			&candidate.IdempotencyKey, &candidate.Attempts, &candidate.Status,
			&candidate.AvailableAt, &claimedAt, &candidate.ClaimedBy, &candidate.LastError); err != nil {
			return nil, err
		}
		candidate.ClaimedAt = &claimedAt
		result = append(result, candidate)
	}
	return result, rows.Err()
}

func (s *PaymentService) processLocalCancellationWork(ctx context.Context, work localCancellationWork) error {
	if ctx != nil && ctx.Err() != nil {
		return s.releaseUnstartedLocalCancellationWork(ctx, work)
	}
	if err := s.requireCurrentLocalCancellationWorkLease(ctx, work); err != nil {
		if ctx != nil && ctx.Err() != nil {
			return s.releaseUnstartedLocalCancellationWork(ctx, work)
		}
		return err
	}
	switch work.WorkKind {
	case localCancellationWorkClose:
		return s.processLocalCancellationClose(ctx, work)
	case localCancellationWorkDirectRefund:
		return s.processLocalCancellationDirectRefund(ctx, work)
	default:
		return s.completeLocalCancellationWork(ctx, work, "unknown_work_kind")
	}
}

func (s *PaymentService) processLocalCancellationClose(ctx context.Context, work localCancellationWork) error {
	order, err := s.entClient.PaymentOrder.Get(ctx, work.OrderID)
	if dbent.IsNotFound(err) {
		return s.completeLocalCancellationWork(ctx, work, "order_missing")
	}
	if err != nil {
		return s.retryLocalCancellationWork(ctx, work, err)
	}
	if order.Status != OrderStatusCancelled {
		return s.completeLocalCancellationWork(ctx, work, "order_no_longer_cancelled")
	}
	if paymentOrderUsesUnifiedPay(order) {
		switch s.reconcileMissingUnifiedPaymentOrder(ctx, order) {
		case missingUnifiedPaymentOrderRetry:
			return s.retryLocalCancellationWork(ctx, work, errors.New("unified_payment_binding_unconfirmed"))
		case missingUnifiedPaymentOrderClosed:
			return s.completeLocalCancellationWork(ctx, work, "unified_payment_closed")
		}
		refreshed, reloadErr := s.entClient.PaymentOrder.Get(ctx, order.ID)
		if reloadErr != nil {
			return s.retryLocalCancellationWork(ctx, work, reloadErr)
		}
		order = refreshed
	}
	provider, err := s.getOrderProvider(ctx, order)
	if err != nil {
		return s.retryLocalCancellationWork(ctx, work, err)
	}
	queryRef := paymentOrderQueryReference(order, provider)
	if queryRef == "" {
		return s.retryLocalCancellationWork(ctx, work, errors.New("provider_query_reference_missing"))
	}
	if err := s.requireCurrentLocalCancellationWorkLease(ctx, work); err != nil {
		return err
	}
	response, err := provider.QueryOrder(ctx, queryRef)
	if err != nil {
		return s.retryLocalCancellationWork(ctx, work, err)
	}
	if response == nil {
		return s.retryLocalCancellationWork(ctx, work, errors.New("provider_query_response_missing"))
	}
	// A central PAID_AFTER_CLOSE resource carries needs_manual_review by
	// contract. Record its exact signed/query evidence before applying the
	// generic review gate; all other review-tagged query results remain manual.
	if handled, lateErr := s.recordUnifiedPaidAfterCloseQuery(ctx, order, response); handled {
		if lateErr != nil {
			var validationErr *unifiedPaidAfterCloseQueryValidationError
			var permanentErr *unifiedWebhookPermanentError
			if errors.As(lateErr, &validationErr) || errors.As(lateErr, &permanentErr) {
				return s.manualLocalCancellationWork(ctx, work, boundedLocalCancellationWorkError(lateErr))
			}
			return s.retryLocalCancellationWork(ctx, work, lateErr)
		}
		return s.completeLocalCancellationWork(ctx, work, "paid_late_refund_queued")
	}
	if paymentOrderUsesUnifiedPay(order) && response.Metadata["needs_manual_review"] == "true" {
		return s.manualLocalCancellationWork(ctx, work, "unified_payment_needs_manual_review")
	}
	if response.Status == payment.ProviderStatusPaid {
		err = s.HandlePaymentNotification(ctx, &payment.PaymentNotification{
			TradeNo: response.TradeNo, OrderID: order.OutTradeNo, Amount: response.Amount,
			Status: payment.NotificationStatusSuccess, Metadata: response.Metadata,
		}, provider.ProviderKey())
		if err != nil {
			return s.retryLocalCancellationWork(ctx, work, err)
		}
		return s.completeLocalCancellationWork(ctx, work, "paid_late_refund_queued")
	}
	if paymentOrderUsesUnifiedPay(order) {
		switch response.Metadata["status"] {
		case unifiedpay.StatusClosed, unifiedpay.StatusExpired:
			return s.completeLocalCancellationWork(ctx, work, "unified_payment_closed")
		}
	}
	closer, ok := provider.(payment.CancelableProvider)
	if !ok {
		return s.manualLocalCancellationWork(ctx, work, "provider_close_unsupported")
	}
	if err := s.requireCurrentLocalCancellationWorkLease(ctx, work); err != nil {
		return err
	}
	if err := closer.CancelPayment(ctx, queryRef); err != nil {
		if errors.Is(err, payment.ErrCancellationPending) || errors.Is(err, payment.ErrUpstreamStateUnconfirmed) {
			return s.retryLocalCancellationWork(ctx, work, err)
		}
		return s.retryLocalCancellationWork(ctx, work, err)
	}
	return s.completeLocalCancellationWork(ctx, work, "provider_close_accepted")
}

func (s *PaymentService) processLocalCancellationDirectRefund(ctx context.Context, work localCancellationWork) error {
	order, err := s.entClient.PaymentOrder.Get(ctx, work.OrderID)
	if dbent.IsNotFound(err) {
		return s.completeLocalCancellationWork(ctx, work, "order_missing")
	}
	if err != nil {
		return s.retryLocalCancellationWork(ctx, work, err)
	}
	if order.Status != OrderStatusCancelled || order.PaidAt == nil {
		return s.manualLocalCancellationWork(ctx, work, "late_refund_order_state_invalid")
	}
	if work.AmountFen <= 0 || work.Currency == "" {
		return s.manualLocalCancellationWork(ctx, work, "late_refund_amount_missing")
	}
	provider, err := s.getOrderProvider(ctx, order)
	if err != nil {
		return s.retryLocalCancellationWork(ctx, work, err)
	}
	tradeNo := strings.TrimSpace(work.PaymentTradeNo)
	if tradeNo == "" {
		tradeNo = strings.TrimSpace(order.PaymentTradeNo)
	}
	amount := payment.FormatAmountForCurrency(payment.MinorUnitToAmount(work.AmountFen, work.Currency), work.Currency)
	request := payment.RefundRequest{
		TradeNo: tradeNo, OrderID: order.OutTradeNo, Amount: amount,
		Reason: "service not delivered", RefundReference: localCancellationDirectRefundReference(order.ID),
	}
	if queryProvider, ok := provider.(payment.RefundQueryProvider); ok {
		if err := s.requireCurrentLocalCancellationWorkLease(ctx, work); err != nil {
			return err
		}
		refundID := strings.TrimSpace(work.ProviderRefundID)
		if refundID == "" {
			refundID = request.RefundReference
		}
		if result, queryErr := queryProvider.QueryRefund(ctx, payment.RefundQueryRequest{
			TradeNo: tradeNo, OrderID: order.OutTradeNo, RefundID: refundID, Amount: amount,
		}); queryErr == nil && result != nil {
			switch result.Status {
			case payment.ProviderStatusSuccess:
				return s.completeLocalCancellationDirectRefund(ctx, work, result, "direct_refund_confirmed")
			case payment.ProviderStatusFailed:
				return s.manualLocalCancellationDirectRefund(ctx, work, result, "direct_refund_failed")
			}
		}
	}
	if _, queryable := provider.(payment.RefundQueryProvider); !queryable && !directLateRefundRetrySafe(provider) {
		// A provider with neither a stable refund idempotency contract nor a
		// refund-query API cannot safely resume after a process crash. Leave an
		// explicit manual fence instead of issuing a cash-moving request that
		// another worker could not prove or recover.
		return s.manualLocalCancellationWork(ctx, work, "direct_refund_recovery_unsupported")
	}
	if err := s.requireCurrentLocalCancellationWorkLease(ctx, work); err != nil {
		return err
	}
	result, refundErr := provider.Refund(ctx, request)
	if result != nil {
		switch result.Status {
		case payment.ProviderStatusSuccess:
			return s.completeLocalCancellationDirectRefund(ctx, work, result, "direct_refund_succeeded")
		case payment.ProviderStatusFailed:
			return s.manualLocalCancellationDirectRefund(ctx, work, result, "direct_refund_failed")
		case payment.ProviderStatusPending:
			if err := s.persistLocalCancellationDirectRefundObservation(ctx, work, result); err != nil {
				return err
			}
			if directLateRefundRetrySafe(provider) {
				return s.retryLocalCancellationWork(ctx, work, errors.New("direct_refund_confirmation_pending"))
			}
			return s.manualLocalCancellationWork(ctx, work, "direct_refund_confirmation_unavailable")
		}
	}
	if queryProvider, ok := provider.(payment.RefundQueryProvider); ok {
		if err := s.requireCurrentLocalCancellationWorkLease(ctx, work); err != nil {
			return err
		}
		refundID := strings.TrimSpace(work.ProviderRefundID)
		if result != nil && strings.TrimSpace(result.RefundID) != "" {
			refundID = strings.TrimSpace(result.RefundID)
		}
		if refundID == "" {
			refundID = request.RefundReference
		}
		if observed, queryErr := queryProvider.QueryRefund(ctx, payment.RefundQueryRequest{
			TradeNo: tradeNo, OrderID: order.OutTradeNo, RefundID: refundID, Amount: amount,
		}); queryErr == nil && observed != nil {
			switch observed.Status {
			case payment.ProviderStatusSuccess:
				return s.completeLocalCancellationDirectRefund(ctx, work, observed, "direct_refund_recovered")
			case payment.ProviderStatusFailed:
				return s.manualLocalCancellationDirectRefund(ctx, work, observed, "direct_refund_failed")
			}
		}
	}
	if directLateRefundRetrySafe(provider) {
		return s.retryLocalCancellationWork(ctx, work, refundErr)
	}
	return s.manualLocalCancellationWork(ctx, work, "direct_refund_unconfirmed")
}

// completeLocalCancellationDirectRefund persists a bounded provider
// correlation and the financial audit in the same transaction as terminal
// completion. A later process can therefore distinguish a completed refund
// from a worker that merely started one before it died.
func (s *PaymentService) completeLocalCancellationDirectRefund(ctx context.Context, work localCancellationWork, result *payment.RefundResponse, reason string) error {
	return s.finalizeLocalCancellationDirectRefund(ctx, work, result, localCancellationWorkCompleted, reason, "LOCAL_CANCEL_DIRECT_REFUND_SUCCEEDED")
}

func (s *PaymentService) manualLocalCancellationDirectRefund(ctx context.Context, work localCancellationWork, result *payment.RefundResponse, reason string) error {
	return s.finalizeLocalCancellationDirectRefund(ctx, work, result, localCancellationWorkManualReview, reason, "LOCAL_CANCEL_DIRECT_REFUND_FAILED")
}

func (s *PaymentService) finalizeLocalCancellationDirectRefund(ctx context.Context, work localCancellationWork, result *payment.RefundResponse, status, reason, auditAction string) error {
	if work.ClaimedBy == "" || work.ClaimedAt == nil {
		return errors.New("local cancellation work lease is missing")
	}
	persistenceCtx, cancel := localCancellationWorkPersistenceContext(ctx)
	defer cancel()
	providerRefundID, providerStatus := localCancellationDirectRefundObservation(result)
	tx, err := s.entClient.Tx(persistenceCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(persistenceCtx, tx)
	client := tx.Client()
	leaseSeconds := int64(localCancellationWorkerLease / time.Second)
	update, err := client.ExecContext(txCtx, `
		UPDATE payment_local_cancellation_work
		SET status = $3::VARCHAR(24),
			provider_refund_id = CASE WHEN $4 <> '' THEN $4 ELSE provider_refund_id END,
			provider_status = CASE WHEN $5 <> '' THEN $5 ELSE provider_status END,
			claimed_at = NULL,
			claimed_by = NULL,
			last_error = CASE WHEN $3::VARCHAR(24) = 'COMPLETED' THEN NULL ELSE $6 END,
			completed_at = CASE WHEN $3::VARCHAR(24) = 'COMPLETED' THEN CURRENT_TIMESTAMP ELSE completed_at END,
			updated_at = CURRENT_TIMESTAMP
		WHERE order_id = $1 AND work_kind = $2 AND status = 'PENDING'
		  AND claimed_by = $7 AND claimed_at = $8
		  AND claimed_at >= NOW() - ($9::BIGINT * INTERVAL '1 second')`,
		work.OrderID, work.WorkKind, status, providerRefundID, providerStatus, boundedLocalCancellationWorkError(errors.New(reason)),
		work.ClaimedBy, work.ClaimedAt.UTC(), leaseSeconds)
	if err != nil {
		return err
	}
	changed, err := update.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		// A callback can discover conflicting late-payment evidence while this
		// worker is blocked inside the provider refund call. That callback
		// deliberately revokes the lease and leaves the job in MANUAL_REVIEW.
		// The scheduling lease is no longer authoritative, but a correlated
		// successful provider response is still an immutable cash fact. Retain
		// it without reopening the job or clearing the manual fence.
		if status == localCancellationWorkCompleted && providerStatus == payment.ProviderStatusSuccess {
			handled, preserveErr := preserveLocalCancellationDirectRefundSuccessAfterManualFenceTx(
				txCtx, client, work, providerRefundID, providerStatus, reason, auditAction,
			)
			if preserveErr != nil {
				return preserveErr
			}
			if handled {
				return tx.Commit()
			}
		}
		return fmt.Errorf("local cancellation work lease lost for order %d", work.OrderID)
	}
	if err := writePaymentAuditTx(txCtx, client, work.OrderID, auditAction, work.ProviderKey, map[string]any{
		"work_kind":          work.WorkKind,
		"provider_refund_id": providerRefundID,
		"provider_status":    providerStatus,
		"reason":             reason,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// preserveLocalCancellationDirectRefundSuccessAfterManualFenceTx records a
// successful refund response that arrived after a conflicting trusted payment
// callback revoked this worker's lease. The manual state is the newer
// scheduling decision and remains immutable; only matching cash correlation is
// added. A stale worker must never write into a reclaimed or completed job.
func preserveLocalCancellationDirectRefundSuccessAfterManualFenceTx(
	ctx context.Context,
	client *dbent.Client,
	work localCancellationWork,
	providerRefundID, providerStatus, reason, auditAction string,
) (bool, error) {
	if client == nil || work.WorkKind != localCancellationWorkDirectRefund || providerStatus != payment.ProviderStatusSuccess {
		return false, nil
	}
	query := `
		SELECT status, provider_key, payment_trade_no, amount_fen, currency, idempotency_key,
			COALESCE(provider_refund_id, ''), COALESCE(provider_status, '')
		FROM payment_local_cancellation_work
		WHERE order_id = $1 AND work_kind = $2`
	if paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, work.OrderID, work.WorkKind)
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
	var current localCancellationWork
	if err := rows.Scan(&current.Status, &current.ProviderKey, &current.PaymentTradeNo, &current.AmountFen,
		&current.Currency, &current.IdempotencyKey, &current.ProviderRefundID, &current.ProviderStatus); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if current.Status != localCancellationWorkManualReview {
		return false, nil
	}
	if !localCancellationDirectRefundWorkMatches(work, current) {
		return false, nil
	}

	// A newer observer may already have persisted a different provider result.
	// Do not make an old in-flight response win that conflict. Leave the manual
	// fence in place and record bounded evidence for an operator instead.
	if !localCancellationDirectRefundObservationCompatible(current, providerRefundID, providerStatus) {
		if err := writePaymentAuditTx(ctx, client, work.OrderID, "LOCAL_CANCEL_DIRECT_REFUND_EVIDENCE_CONFLICT", work.ProviderKey, map[string]any{
			"work_kind":                   work.WorkKind,
			"observed_provider_refund_id": providerRefundID,
			"observed_provider_status":    providerStatus,
			"stored_provider_refund_id":   current.ProviderRefundID,
			"stored_provider_status":      current.ProviderStatus,
			"reason":                      reason,
		}); err != nil {
			return false, err
		}
		return true, nil
	}

	updated, err := client.ExecContext(ctx, `
		UPDATE payment_local_cancellation_work
		SET provider_refund_id = CASE WHEN $3 <> '' THEN $3 ELSE provider_refund_id END,
			provider_status = $4,
			updated_at = CURRENT_TIMESTAMP
		WHERE order_id = $1 AND work_kind = $2 AND status = $5::VARCHAR(24)`,
		work.OrderID, work.WorkKind, providerRefundID, providerStatus, localCancellationWorkManualReview)
	if err != nil {
		return false, err
	}
	changed, err := updated.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed != 1 {
		return false, nil
	}
	if err := writePaymentAuditTx(ctx, client, work.OrderID, auditAction, work.ProviderKey, map[string]any{
		"work_kind":              work.WorkKind,
		"provider_refund_id":     providerRefundID,
		"provider_status":        providerStatus,
		"reason":                 reason,
		"manual_fence_preserved": true,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func localCancellationDirectRefundWorkMatches(expected, current localCancellationWork) bool {
	return strings.EqualFold(strings.TrimSpace(expected.ProviderKey), strings.TrimSpace(current.ProviderKey)) &&
		strings.TrimSpace(expected.PaymentTradeNo) == strings.TrimSpace(current.PaymentTradeNo) &&
		expected.AmountFen == current.AmountFen &&
		strings.EqualFold(strings.TrimSpace(expected.Currency), strings.TrimSpace(current.Currency)) &&
		strings.TrimSpace(expected.IdempotencyKey) == strings.TrimSpace(current.IdempotencyKey)
}

func localCancellationDirectRefundObservationCompatible(current localCancellationWork, providerRefundID, providerStatus string) bool {
	storedRefundID := strings.TrimSpace(current.ProviderRefundID)
	observedRefundID := strings.TrimSpace(providerRefundID)
	if storedRefundID != "" && observedRefundID != "" && storedRefundID != observedRefundID {
		return false
	}
	switch strings.TrimSpace(current.ProviderStatus) {
	case "", payment.ProviderStatusPending, payment.ProviderStatusSuccess:
		return providerStatus == payment.ProviderStatusSuccess
	default:
		return false
	}
}

func (s *PaymentService) persistLocalCancellationDirectRefundObservation(ctx context.Context, work localCancellationWork, result *payment.RefundResponse) error {
	if work.ClaimedBy == "" || work.ClaimedAt == nil {
		return errors.New("local cancellation work lease is missing")
	}
	persistenceCtx, cancel := localCancellationWorkPersistenceContext(ctx)
	defer cancel()
	providerRefundID, providerStatus := localCancellationDirectRefundObservation(result)
	if providerRefundID == "" && providerStatus == "" {
		return nil
	}
	update, err := s.entClient.ExecContext(persistenceCtx, `
		UPDATE payment_local_cancellation_work
		SET provider_refund_id = CASE WHEN $3 <> '' THEN $3 ELSE provider_refund_id END,
			provider_status = CASE WHEN $4 <> '' THEN $4 ELSE provider_status END,
			updated_at = CURRENT_TIMESTAMP
		WHERE order_id = $1 AND work_kind = $2 AND status = 'PENDING'
		  AND claimed_by = $5 AND claimed_at = $6
		  AND claimed_at >= NOW() - ($7::BIGINT * INTERVAL '1 second')`,
		work.OrderID, work.WorkKind, providerRefundID, providerStatus, work.ClaimedBy, work.ClaimedAt.UTC(), int64(localCancellationWorkerLease/time.Second))
	if err != nil {
		return err
	}
	changed, err := update.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("local cancellation work lease lost for order %d", work.OrderID)
	}
	return nil
}

func localCancellationDirectRefundObservation(result *payment.RefundResponse) (string, string) {
	if result == nil {
		return "", ""
	}
	refundID := strings.TrimSpace(removePostgresTextNUL(result.RefundID))
	if len(refundID) > 160 {
		refundID = refundID[:160]
	}
	refundID = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, refundID)
	status := strings.ToLower(strings.TrimSpace(result.Status))
	switch status {
	case payment.ProviderStatusSuccess, payment.ProviderStatusPending, payment.ProviderStatusFailed:
		return refundID, status
	default:
		return refundID, ""
	}
}

func directLateRefundRetrySafe(provider payment.Provider) bool {
	if provider == nil {
		return false
	}
	switch payment.GetBasePaymentType(strings.TrimSpace(provider.ProviderKey())) {
	case payment.TypeAlipay, payment.TypeWxpay, payment.TypeStripe, payment.TypeAirwallex:
		return true
	default:
		return false
	}
}

func (s *PaymentService) completeLocalCancellationWork(ctx context.Context, work localCancellationWork, reason string) error {
	return s.updateLocalCancellationWork(ctx, work, localCancellationWorkCompleted, time.Time{}, reason, false)
}

func (s *PaymentService) retryLocalCancellationWork(ctx context.Context, work localCancellationWork, cause error) error {
	message := "provider_result_unconfirmed"
	if cause != nil {
		message = boundedLocalCancellationWorkError(cause)
	}
	delay := localCancellationWorkerBackoff
	if work.Attempts > 0 {
		delay *= time.Duration(1 << localCancellationMin(work.Attempts, 6))
	}
	if delay > 10*time.Minute {
		delay = 10 * time.Minute
	}
	return s.updateLocalCancellationWork(ctx, work, localCancellationWorkPending, time.Now().UTC().Add(delay), message, true)
}

func (s *PaymentService) manualLocalCancellationWork(ctx context.Context, work localCancellationWork, reason string) error {
	return s.updateLocalCancellationWork(ctx, work, localCancellationWorkManualReview, time.Time{}, reason, false)
}

func (s *PaymentService) updateLocalCancellationWork(ctx context.Context, work localCancellationWork, status string, availableAt time.Time, reason string, increment bool) error {
	if work.ClaimedBy == "" || work.ClaimedAt == nil {
		return errors.New("local cancellation work lease is missing")
	}
	persistenceCtx, cancel := localCancellationWorkPersistenceContext(ctx)
	defer cancel()
	leaseSeconds := int64(localCancellationWorkerLease / time.Second)
	setAttempts := "attempts"
	if increment {
		setAttempts = "attempts + 1"
	}
	args := []any{work.OrderID, work.WorkKind, status, reason, work.ClaimedBy, work.ClaimedAt.UTC(), leaseSeconds}
	if !availableAt.IsZero() {
		args = append(args, availableAt.UTC())
	}
	query := fmt.Sprintf(`
		UPDATE payment_local_cancellation_work
		SET status = $3::VARCHAR(24), %s = %s, claimed_at = NULL, claimed_by = NULL,
			last_error = CASE WHEN $3::VARCHAR(24) = 'COMPLETED' THEN NULL ELSE $4 END,
			completed_at = CASE WHEN $3::VARCHAR(24) = 'COMPLETED' THEN CURRENT_TIMESTAMP ELSE completed_at END,
			updated_at = CURRENT_TIMESTAMP
		WHERE order_id = $1 AND work_kind = $2
		  AND claimed_by = $5 AND claimed_at = $6 AND claimed_at >= NOW() - ($7::BIGINT * INTERVAL '1 second')`, setAttempts, setAttempts)
	if !availableAt.IsZero() {
		query = fmt.Sprintf(`
		UPDATE payment_local_cancellation_work
		SET status = $3::VARCHAR(24), attempts = %s, available_at = $8, claimed_at = NULL, claimed_by = NULL,
			last_error = CASE WHEN $3::VARCHAR(24) = 'COMPLETED' THEN NULL ELSE $4 END,
			completed_at = CASE WHEN $3::VARCHAR(24) = 'COMPLETED' THEN CURRENT_TIMESTAMP ELSE completed_at END,
			updated_at = CURRENT_TIMESTAMP
		WHERE order_id = $1 AND work_kind = $2
		  AND claimed_by = $5 AND claimed_at = $6 AND claimed_at >= NOW() - ($7::BIGINT * INTERVAL '1 second')`, setAttempts)
	}
	result, err := s.entClient.ExecContext(persistenceCtx, query, args...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("local cancellation work lease lost for order %d", work.OrderID)
	}
	return nil
}

func (s *PaymentService) requireCurrentLocalCancellationWorkLease(ctx context.Context, work localCancellationWork) error {
	if work.ClaimedBy == "" || work.ClaimedAt == nil {
		return errors.New("local cancellation work lease is missing")
	}
	rows, err := s.entClient.QueryContext(ctx, `
		SELECT 1 FROM payment_local_cancellation_work
		WHERE order_id = $1 AND work_kind = $2 AND status = 'PENDING'
		  AND claimed_by = $3 AND claimed_at = $4
		  AND claimed_at >= NOW() - ($5::BIGINT * INTERVAL '1 second')`,
		work.OrderID, work.WorkKind, work.ClaimedBy, work.ClaimedAt.UTC(), int64(localCancellationWorkerLease/time.Second))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return fmt.Errorf("local cancellation work lease is no longer current for order %d", work.OrderID)
	}
	return rows.Err()
}

// releaseUnstartedLocalCancellationWork gives a claimed item back to the
// durable scheduler when the worker context expires before its provider call
// begins. updateLocalCancellationWork retains the exact claim and lease fence.
func (s *PaymentService) releaseUnstartedLocalCancellationWork(ctx context.Context, work localCancellationWork) error {
	return s.updateLocalCancellationWork(ctx, work, localCancellationWorkPending, time.Now().UTC(), "worker_context_cancelled", false)
}

// localCancellationWorkPersistenceContext is used only after an outbound call
// or when releasing already-claimed work. It deliberately removes a canceled
// caller context but bounds database cleanup so it cannot turn into detached
// background work.
func localCancellationWorkPersistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), localCancellationWorkerCleanupTimeout)
	}
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), localCancellationWorkerCleanupTimeout)
}

func boundedLocalCancellationWorkError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 160 {
		message = message[:160]
	}
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, message)
}

func localCancellationMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
