package service

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"strconv"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

func (s *PaymentService) validateUnifiedRefundOrder(o *dbent.PaymentOrder) error {
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
	if o.OrderType != payment.OrderTypeBalance {
		return infraerrors.BadRequest("REFUND_REQUIRES_MANUAL_REVIEW", "unified refunds currently support plain balance orders")
	}
	entitlements, err := paymentOrderEntitlementsStrict(o)
	if err != nil || paymentEntitlementsRequireManualRefund(entitlements) {
		return infraerrors.BadRequest("REFUND_REQUIRES_MANUAL_REVIEW", "order entitlements require manual refund review")
	}
	return nil
}

// Convert the legacy product decimal boundary once, then calculate proportional
// channel refunds exclusively with integers, including half-up rounding.
func unifiedRefundAmounts(o *dbent.PaymentOrder, amount float64) (balanceMinor, gatewayFen int64, err error) {
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
	if balanceMinor <= 0 || totalMinor <= 0 || paidFen <= 0 || balanceMinor > totalMinor {
		return 0, 0, errors.New("invalid unified refund amount")
	}
	numerator := new(big.Int).Mul(big.NewInt(paidFen), big.NewInt(balanceMinor))
	numerator.Add(numerator, big.NewInt(totalMinor/2))
	numerator.Quo(numerator, big.NewInt(totalMinor))
	if !numerator.IsInt64() || numerator.Sign() <= 0 || numerator.Int64() > paidFen {
		return 0, 0, errors.New("invalid unified gateway refund amount")
	}
	return balanceMinor, numerator.Int64(), nil
}

func (s *PaymentService) executeUnifiedRefund(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	a, err := s.reserveUnifiedRefundAttempt(ctx, p)
	if err != nil {
		return nil, err
	}
	return s.advanceUnifiedRefund(ctx, a)
}

func (s *PaymentService) reserveUnifiedRefundAttempt(ctx context.Context, p *RefundPlan) (*unifiedRefundAttempt, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	o, err := lockUnifiedRefundOrder(txCtx, client, p.OrderID)
	if err != nil {
		return nil, err
	}
	if err := s.validateUnifiedRefundOrder(o); err != nil {
		return nil, err
	}
	manual, err := unifiedRefundOrderNeedsReview(txCtx, client, o.ID)
	if err != nil {
		return nil, err
	}
	if manual {
		return nil, infraerrors.Conflict("REFUND_REQUIRES_MANUAL_REVIEW", "a refund for this order requires manual review")
	}
	balanceMinor, gatewayFen, err := unifiedRefundAmounts(o, p.RefundAmount)
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
	if !psSliceContains([]string{OrderStatusCompleted, OrderStatusRefundRequested, OrderStatusRefundFailed}, o.Status) {
		return nil, infraerrors.Conflict("CONFLICT", "order status does not allow another refund")
	}
	snapshot := psOrderProviderSnapshot(o)
	method, _ := unifiedpay.PaymentMethodForPaymentType(o.PaymentType)
	id := uuid.NewString()
	a := &unifiedRefundAttempt{
		ProductRefundNo: "sub2-refund-" + id, IdempotencyKey: "sub2:refund:" + id,
		OrderID: o.ID, PaymentOrderID: snapshot.PaymentOrderID, Environment: snapshot.Environment,
		OrganizationID: snapshot.OrganizationID, ProductID: snapshot.ProductID, AppID: snapshot.AppID,
		PaymentMethod: method, AmountFen: gatewayFen, BalanceAmountMinor: balanceMinor,
		DeductBalance: p.DeductBalance, Force: p.Force, ReasonSummary: "Sub2 administrator refund", Status: unifiedRefundPending,
	}
	if err := insertUnifiedRefundAttempt(txCtx, client, a); err != nil {
		return nil, err
	}
	if _, err := client.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRefundPending).
		SetRefundAmount(p.RefundAmount).SetRefundReason(p.Reason).SetForceRefund(p.Force).
		ClearRefundAt().ClearFailedAt().ClearFailedReason().Save(txCtx); err != nil {
		return nil, err
	}
	if err := writeUnifiedRefundAudit(txCtx, client, o.ID, "UNIFIED_REFUND_REQUESTED", map[string]any{
		"product_refund_no": a.ProductRefundNo, "amount_fen": a.AmountFen,
		"balance_amount_minor": a.BalanceAmountMinor, "deduct_balance": a.DeductBalance,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
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

func (s *PaymentService) queryUnifiedRefund(ctx context.Context, o *dbent.PaymentOrder) (*RefundResult, error) {
	a, err := loadUnifiedRefundAttempt(ctx, s.entClient, o.ID, "")
	if err != nil {
		return nil, err
	}
	return s.advanceUnifiedRefund(ctx, a)
}

func (s *PaymentService) advanceUnifiedRefund(ctx context.Context, a *unifiedRefundAttempt) (*RefundResult, error) {
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
	if err := s.validateUnifiedRefundOrder(o); err != nil {
		return nil, err
	}
	if a.Status != unifiedRefundPending {
		return &RefundResult{Success: a.Status == unifiedpay.RefundStatusSucceeded}, nil
	}
	var result *payment.UnifiedRefundResource
	if a.RefundRequestID == "" {
		result, err = s.unifiedPayment.CreateUnifiedRefund(ctx, payment.UnifiedRefundRequest{
			PaymentOrderID: a.PaymentOrderID, ProductRefundNo: a.ProductRefundNo, IdempotencyKey: a.IdempotencyKey,
			AmountFen: a.AmountFen, ReasonCode: "other", ReasonSummary: &a.ReasonSummary,
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
	defer tx.Rollback()
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
	manual, err := unifiedRefundOrderNeedsReview(txCtx, client, orderID)
	if err != nil {
		return nil, err
	}
	a.NeedsManualReview = a.NeedsManualReview || manual
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
	a.NeedsManualReview = a.NeedsManualReview || result.NeedsManualReview
	terminal := result.Status == unifiedpay.RefundStatusSucceeded || result.Status == unifiedpay.RefundStatusFailed
	previousStatus := a.Status
	if terminal {
		a.Status = result.Status
	}
	response := pendingUnifiedRefundResult(a.NeedsManualReview)
	if terminal && previousStatus == unifiedRefundPending && !a.NeedsManualReview {
		if o.Status != OrderStatusRefundPending {
			a.NeedsManualReview = true
			response = pendingUnifiedRefundResult(true)
		} else if result.Status == unifiedpay.RefundStatusSucceeded {
			amount := payment.MinorUnitToAmount(a.BalanceAmountMinor, payment.DefaultPaymentCurrency)
			plan := &RefundPlan{OrderID: o.ID, Order: o, RefundAmount: amount,
				Reason: psStringValue(o.RefundReason), Force: a.Force, DeductBalance: a.DeductBalance,
				DeductionType: payment.DeductionTypeNone}
			if a.DeductBalance {
				plan.DeductionType, plan.BalanceToDeduct = payment.DeductionTypeBalance, amount
			}
			if err := s.applyRefundFinalDeduction(txCtx, plan); err != nil {
				return nil, err
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
			if _, err := client.PaymentOrder.UpdateOneID(orderID).SetStatus(OrderStatusRefundFailed).
				SetFailedAt(time.Now()).SetFailedReason("unified payment refund failed").Save(txCtx); err != nil {
				return nil, err
			}
			response = &RefundResult{Success: false, Warning: "unified payment refund failed"}
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
	defer tx.Rollback()
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
