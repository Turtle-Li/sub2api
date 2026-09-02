package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
)

const unifiedWebhookProcessingTimeout = 2 * time.Minute

const (
	UnifiedWebhookClaimNew       = "new"
	UnifiedWebhookClaimDuplicate = "duplicate"
	UnifiedWebhookClaimBusy      = "busy"
	UnifiedWebhookClaimConflict  = "conflict"
)

var (
	ErrUnifiedWebhookInvalid   = errors.New("invalid unified payment webhook")
	ErrUnifiedWebhookRetryable = errors.New("unified payment webhook should be retried")
)

type UnifiedWebhookInboxRecord struct {
	EventID        string
	PaymentOrderID string
	ProductOrderNo string
	Sequence       int64
	EventType      string
	BodySHA256     string
	OccurredAt     time.Time
}

// UnifiedWebhookInboxStore is PostgreSQL-backed in production so every Sub2
// replica shares the same event-id and per-order sequence fence.
type UnifiedWebhookInboxStore interface {
	Claim(ctx context.Context, record UnifiedWebhookInboxRecord, staleAfter time.Duration) (string, error)
	MarkProcessed(ctx context.Context, eventID string) error
	MarkRetryableFailure(ctx context.Context, eventID, errorCode string) error
	MarkRejected(ctx context.Context, eventID, errorCode string) error
}

func (s *PaymentService) HandleUnifiedPaymentWebhook(ctx context.Context, headers http.Header, rawBody []byte) error {
	if s == nil || s.unifiedPayment == nil || !s.unifiedPayment.Enabled() || s.unifiedWebhookInbox == nil {
		return fmt.Errorf("%w: integration disabled", ErrUnifiedWebhookRetryable)
	}
	verified, err := s.unifiedPayment.VerifyWebhook(headers, rawBody)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnifiedWebhookInvalid, err)
	}
	record := UnifiedWebhookInboxRecord{
		EventID: verified.EventID, PaymentOrderID: verified.Event.Resource.PaymentOrderID,
		ProductOrderNo: verified.Event.Resource.ProductOrderNo, Sequence: verified.Sequence,
		EventType: verified.Event.EventType, BodySHA256: verified.BodyHash,
		OccurredAt: verified.Event.OccurredAt,
	}
	claim, err := s.unifiedWebhookInbox.Claim(ctx, record, unifiedWebhookProcessingTimeout)
	if err != nil {
		return fmt.Errorf("%w: claim inbox: %v", ErrUnifiedWebhookRetryable, err)
	}
	switch claim {
	case UnifiedWebhookClaimDuplicate:
		return nil
	case UnifiedWebhookClaimBusy:
		return fmt.Errorf("%w: event is already processing", ErrUnifiedWebhookRetryable)
	case UnifiedWebhookClaimConflict:
		// The request was already signature-verified, but its global event ID or
		// per-order sequence collides with retained evidence. Never process it and
		// ACK permanently so an immutable bad event cannot create a retry storm.
		slog.Error("unified payment webhook identity conflict",
			"eventID", verified.EventID,
			"paymentOrderID", verified.Event.Resource.PaymentOrderID,
			"sequence", verified.Sequence,
		)
		return nil
	case UnifiedWebhookClaimNew:
	default:
		return fmt.Errorf("%w: invalid inbox claim", ErrUnifiedWebhookRetryable)
	}

	if err := s.processUnifiedPaymentEvent(ctx, verified); err != nil {
		var permanent *unifiedWebhookPermanentError
		if errors.As(err, &permanent) {
			if markErr := s.unifiedWebhookInbox.MarkRejected(ctx, verified.EventID, permanent.code); markErr != nil {
				return fmt.Errorf("%w: reject inbox: %v", ErrUnifiedWebhookRetryable, markErr)
			}
			return nil
		}
		_ = s.unifiedWebhookInbox.MarkRetryableFailure(ctx, verified.EventID, "processing_failed")
		return fmt.Errorf("%w: process event: %v", ErrUnifiedWebhookRetryable, err)
	}
	if err := s.unifiedWebhookInbox.MarkProcessed(ctx, verified.EventID); err != nil {
		return fmt.Errorf("%w: complete inbox: %v", ErrUnifiedWebhookRetryable, err)
	}
	return nil
}

type unifiedWebhookPermanentError struct{ code string }

func (e *unifiedWebhookPermanentError) Error() string { return e.code }

func permanentUnifiedWebhookError(code string) error {
	return &unifiedWebhookPermanentError{code: code}
}

func (s *PaymentService) processUnifiedPaymentEvent(ctx context.Context, verified unifiedpay.VerifiedWebhook) error {
	event := verified.Event
	resource := event.Resource
	order, err := s.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNo(resource.ProductOrderNo)).Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return permanentUnifiedWebhookError("order_not_found")
		}
		return fmt.Errorf("load local order: %w", err)
	}
	if err := s.validateUnifiedPaymentEventOrder(order, event); err != nil {
		return s.rejectUnifiedPaymentEvent(ctx, order, event, err.Error())
	}
	if err := validateUnifiedPaymentEventSemantics(event); err != nil {
		return s.rejectUnifiedPaymentEvent(ctx, order, event, err.Error())
	}
	if err := s.bindUnifiedPaymentOrderFromWebhook(ctx, order, event); err != nil {
		return err
	}

	switch event.EventType {
	case unifiedpay.EventPaymentPaid:
		metadata := map[string]string{
			"payment_order_id": resource.PaymentOrderID, "environment": event.Environment.String(),
			"organization_id": event.OrganizationID, "product_id": event.ProductID, "app_id": event.AppID,
		}
		if err := s.HandlePaymentNotification(ctx, &payment.PaymentNotification{
			TradeNo: strings.TrimSpace(*resource.ChannelTransactionID), OrderID: resource.ProductOrderNo,
			Amount: payment.MinorUnitToAmount(resource.PaidAmountFen, payment.DefaultPaymentCurrency),
			Status: payment.NotificationStatusSuccess, Metadata: metadata,
		}, payment.TypeUnifiedPay); err != nil {
			return err
		}
	case unifiedpay.EventPaymentPaidAfterClose:
		s.writeAuditLog(ctx, order.ID, "UNIFIED_PAYMENT_PAID_AFTER_CLOSE", payment.TypeUnifiedPay, safeUnifiedEventAudit(event))
	case unifiedpay.EventPaymentConfirmationPending:
		s.writeAuditLog(ctx, order.ID, "UNIFIED_PAYMENT_CONFIRMATION_PENDING", payment.TypeUnifiedPay, safeUnifiedEventAudit(event))
	case unifiedpay.EventPaymentClosed:
		if err := s.applyUnifiedTerminalOrderStatus(ctx, order, OrderStatusCancelled, "UNIFIED_PAYMENT_CLOSED", event); err != nil {
			return err
		}
	case unifiedpay.EventPaymentExpired:
		if err := s.applyUnifiedTerminalOrderStatus(ctx, order, OrderStatusExpired, "UNIFIED_PAYMENT_EXPIRED", event); err != nil {
			return err
		}
	case unifiedpay.EventRefundSucceeded:
		// Refund execution is intentionally not enabled in this Sub2 slice. Keep
		// the signed terminal evidence and audit it without mutating entitlements.
		s.writeAuditLog(ctx, order.ID, "UNIFIED_PAYMENT_REFUND_EVENT", payment.TypeUnifiedPay, safeUnifiedEventAudit(event))
	case unifiedpay.EventRefundFailed:
		// Refund execution is intentionally not enabled in this Sub2 slice. Keep
		// the signed terminal evidence and audit it without mutating entitlements.
		s.writeAuditLog(ctx, order.ID, "UNIFIED_PAYMENT_REFUND_EVENT", payment.TypeUnifiedPay, safeUnifiedEventAudit(event))
	default:
		return s.rejectUnifiedPaymentEvent(ctx, order, event, "unsupported_event_type")
	}
	return nil
}

func validateUnifiedPaymentEventSemantics(event unifiedpay.WebhookEvent) error {
	resource := event.Resource
	hasChannelTransaction := resource.ChannelTransactionID != nil && strings.TrimSpace(*resource.ChannelTransactionID) != ""
	switch event.EventType {
	case unifiedpay.EventPaymentPaid:
		if resource.Status != unifiedpay.StatusPaid || resource.PaidAmountFen != resource.AmountFen || !hasChannelTransaction {
			return errors.New("invalid_paid_event")
		}
	case unifiedpay.EventPaymentPaidAfterClose:
		if resource.Status != unifiedpay.StatusPaidAfterClose || resource.PaidAmountFen != resource.AmountFen || !hasChannelTransaction {
			return errors.New("invalid_paid_after_close_event")
		}
	case unifiedpay.EventPaymentConfirmationPending:
		if resource.Status != unifiedpay.StatusConfirmationPending {
			return errors.New("confirmation_pending_status_mismatch")
		}
	case unifiedpay.EventPaymentClosed:
		if resource.Status != unifiedpay.StatusClosed {
			return errors.New("closed_status_mismatch")
		}
	case unifiedpay.EventPaymentExpired:
		if resource.Status != unifiedpay.StatusExpired {
			return errors.New("expired_status_mismatch")
		}
	case unifiedpay.EventRefundSucceeded:
		if event.Refund == nil || event.Refund.Status != unifiedpay.RefundStatusSucceeded ||
			event.Refund.AmountFen > resource.PaidAmountFen ||
			(resource.Status != unifiedpay.StatusPartiallyRefunded && resource.Status != unifiedpay.StatusRefunded) {
			return errors.New("invalid_refund_succeeded_event")
		}
	case unifiedpay.EventRefundFailed:
		if event.Refund == nil || event.Refund.Status != unifiedpay.RefundStatusFailed ||
			event.Refund.AmountFen > resource.PaidAmountFen {
			return errors.New("invalid_refund_failed_event")
		}
	default:
		return errors.New("unsupported_event_type")
	}
	return nil
}

func (s *PaymentService) rejectUnifiedPaymentEvent(ctx context.Context, order *dbent.PaymentOrder, event unifiedpay.WebhookEvent, code string) error {
	if order != nil {
		s.writeAuditLog(ctx, order.ID, "UNIFIED_PAYMENT_EVENT_REJECTED", payment.TypeUnifiedPay, map[string]any{
			"event_id": event.EventID, "event_type": event.EventType, "sequence": event.Sequence,
			"reason": code,
		})
	}
	return permanentUnifiedWebhookError(code)
}

func (s *PaymentService) validateUnifiedPaymentEventOrder(order *dbent.PaymentOrder, event unifiedpay.WebhookEvent) error {
	if order == nil {
		return errors.New("order_not_found")
	}
	snapshot := psOrderProviderSnapshot(order)
	if snapshot == nil || snapshot.ProviderKey != payment.TypeUnifiedPay {
		return errors.New("provider_mismatch")
	}
	resource := event.Resource
	if snapshot.PaymentOrderID != "" && !strings.EqualFold(snapshot.PaymentOrderID, resource.PaymentOrderID) {
		return errors.New("payment_order_id_mismatch")
	}
	checks := map[string][2]string{
		"environment_mismatch":  {snapshot.Environment, event.Environment.String()},
		"organization_mismatch": {snapshot.OrganizationID, event.OrganizationID},
		"product_mismatch":      {snapshot.ProductID, event.ProductID},
		"app_mismatch":          {snapshot.AppID, event.AppID},
	}
	for code, pair := range checks {
		if pair[0] == "" || !strings.EqualFold(pair[0], pair[1]) {
			return errors.New(code)
		}
	}
	if resource.OrderType != order.OrderType || resource.Currency != payment.DefaultPaymentCurrency ||
		resource.PaymentMethod != unifiedpay.PaymentMethodAlipay {
		return errors.New("order_contract_mismatch")
	}
	expectedFen, err := payment.AmountToMinorUnit(strconv.FormatFloat(order.PayAmount, 'f', -1, 64), payment.DefaultPaymentCurrency)
	if err != nil || expectedFen != resource.AmountFen {
		return errors.New("amount_mismatch")
	}
	return nil
}

// bindUnifiedPaymentOrderFromWebhook closes the small create/commit seam where
// the payment service has durably created an order but Sub2 has not yet saved
// its payment_order_id. The signed event has already passed scope, amount and
// semantic validation. UpdatedAt provides an optimistic fence against a
// concurrent normal create response; a loser reloads and accepts only the same
// immutable payment-order ID.
func (s *PaymentService) bindUnifiedPaymentOrderFromWebhook(ctx context.Context, order *dbent.PaymentOrder, event unifiedpay.WebhookEvent) error {
	if order == nil {
		return errors.New("order_not_found")
	}
	snapshot := psOrderProviderSnapshot(order)
	if snapshot == nil || snapshot.ProviderKey != payment.TypeUnifiedPay {
		return errors.New("provider_mismatch")
	}
	remoteID := event.Resource.PaymentOrderID
	if snapshot.PaymentOrderID != "" {
		if !strings.EqualFold(snapshot.PaymentOrderID, remoteID) {
			return errors.New("payment_order_id_mismatch")
		}
		return nil
	}

	providerSnapshot := make(map[string]any, len(order.ProviderSnapshot)+1)
	for key, value := range order.ProviderSnapshot {
		providerSnapshot[key] = value
	}
	providerSnapshot["payment_order_id"] = remoteID
	changed, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(order.ID),
		paymentorder.UpdatedAtEQ(order.UpdatedAt),
	).SetProviderSnapshot(providerSnapshot).SetPaymentTradeNo(remoteID).Save(ctx)
	if err != nil {
		return fmt.Errorf("persist unified payment order binding: %w", err)
	}
	if changed == 1 {
		order.ProviderSnapshot = providerSnapshot
		order.PaymentTradeNo = remoteID
		return nil
	}

	reloaded, err := s.entClient.PaymentOrder.Get(ctx, order.ID)
	if err != nil {
		return fmt.Errorf("reload unified payment order binding: %w", err)
	}
	reloadedSnapshot := psOrderProviderSnapshot(reloaded)
	if reloadedSnapshot == nil || !strings.EqualFold(reloadedSnapshot.PaymentOrderID, remoteID) {
		return errors.New("unified payment order binding conflict")
	}
	order.ProviderSnapshot = reloaded.ProviderSnapshot
	order.PaymentTradeNo = reloaded.PaymentTradeNo
	order.UpdatedAt = reloaded.UpdatedAt
	return nil
}

func (s *PaymentService) applyUnifiedTerminalOrderStatus(ctx context.Context, order *dbent.PaymentOrder, status, action string, event unifiedpay.WebhookEvent) error {
	changed, err := s.entClient.PaymentOrder.Update().
		Where(paymentorder.IDEQ(order.ID), paymentorder.StatusEQ(OrderStatusPending)).
		SetStatus(status).Save(ctx)
	if err != nil {
		return fmt.Errorf("apply local terminal status: %w", err)
	}
	if changed > 0 {
		s.writeAuditLog(ctx, order.ID, action, payment.TypeUnifiedPay, safeUnifiedEventAudit(event))
	}
	return nil
}

func safeUnifiedEventAudit(event unifiedpay.WebhookEvent) map[string]any {
	detail := map[string]any{
		"event_id": event.EventID, "event_type": event.EventType, "sequence": event.Sequence,
		"payment_order_id": event.Resource.PaymentOrderID,
	}
	if event.Refund != nil {
		detail["refund_request_id"] = event.Refund.RefundRequestID
		detail["refund_status"] = event.Refund.Status
	}
	return detail
}
