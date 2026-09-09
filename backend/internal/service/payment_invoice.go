package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/mail"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentinvoicedocument"
	"github.com/Wei-Shaw/sub2api/ent/paymentinvoicerequest"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/google/uuid"
)

const (
	InvoiceStatusPending    = "PENDING"
	InvoiceStatusProcessing = "PROCESSING"
	InvoiceStatusIssued     = "ISSUED"
	InvoiceStatusRejected   = "REJECTED"

	InvoiceTitleTypePersonal   = "personal"
	InvoiceTitleTypeEnterprise = "enterprise"

	InvoiceStatusFilterNone = "NONE"
	InvoiceStatusFilterAny  = "HAS_INVOICE"

	InvoiceEmailDeliveryNotSent = "NOT_SENT"
	InvoiceEmailDeliveryPending = "PENDING"
	InvoiceEmailDeliverySending = "SENDING"
	InvoiceEmailDeliverySent    = "SENT"
	InvoiceEmailDeliveryFailed  = "FAILED"

	InvoiceFeishuNotificationNotSent = "NOT_SENT"
	InvoiceFeishuNotificationPending = "PENDING"
	InvoiceFeishuNotificationSending = "SENDING"
	InvoiceFeishuNotificationSent    = "SENT"
	InvoiceFeishuNotificationFailed  = "FAILED"

	InvoicePaymentStatusPaid   = "PAID"
	InvoicePaymentStatusUnpaid = "UNPAID"

	InvoiceFulfillmentStatusFulfilled    = "FULFILLED"
	InvoiceFulfillmentStatusFailed       = "FAILED"
	InvoiceFulfillmentStatusPending      = "PENDING"
	InvoiceFulfillmentStatusNotStarted   = "NOT_STARTED"
	InvoiceFulfillmentStatusManualReview = "MANUAL_REVIEW"

	MaxInvoicePDFBytes = 10 << 20

	invoiceDeliveryClaimTTL       = 5 * time.Minute
	invoiceDeliveryRetryBaseDelay = 5 * time.Minute
	invoiceDeliveryRetryMaxDelay  = time.Hour
	invoiceDeliveryRecoveryLimit  = 50
	invoiceDeliverySendTimeout    = 30 * time.Second
)

var (
	invoiceTaxIdentifierPattern = regexp.MustCompile(`^[0-9A-Za-z-]{8,64}$`)
	invoicePhonePattern         = regexp.MustCompile(`^[0-9+(). -]{6,32}$`)
)

// CreateInvoiceRequestInput contains buyer-provided delivery data. Amount and
// currency are never client inputs; they are snapshotted from the owned order.
type CreateInvoiceRequestInput struct {
	TitleType      string `json:"title_type"`
	Title          string `json:"title"`
	TaxIdentifier  string `json:"tax_identifier"`
	RecipientEmail string `json:"recipient_email"`
	RecipientPhone string `json:"recipient_phone"`
	Remark         string `json:"remark"`
}

// AdminUpdateInvoiceInput applies one explicit lifecycle transition.
type AdminUpdateInvoiceInput struct {
	Status            string           `json:"status" form:"status"`
	Provider          string           `json:"provider" form:"provider"`
	ProviderInvoiceID string           `json:"provider_invoice_id" form:"provider_invoice_id"`
	InvoiceItemName   string           `json:"invoice_item_name" form:"invoice_item_name"`
	InvoiceCode       string           `json:"invoice_code" form:"invoice_code"`
	InvoiceNumber     string           `json:"invoice_number" form:"invoice_number"`
	RejectionReason   string           `json:"rejection_reason" form:"rejection_reason"`
	Document          *InvoicePDFInput `json:"-" form:"-"`
}

// InvoicePDFInput is private to the admin upload boundary. Its bytes are saved
// only in the restricted document table and never included in a JSON response.
type InvoicePDFInput struct {
	Filename string
	Data     []byte
}

// PaymentInvoiceRecord is returned only by authenticated owner/admin invoice
// endpoints and trusted order views. It intentionally contains no PDF bytes.
type PaymentInvoiceRecord struct {
	ID                       int64      `json:"id"`
	OrderID                  int64      `json:"order_id"`
	UserID                   int64      `json:"user_id"`
	TitleType                string     `json:"title_type"`
	Title                    string     `json:"title"`
	TaxIdentifier            *string    `json:"tax_identifier,omitempty"`
	RecipientEmail           string     `json:"recipient_email"`
	RecipientPhone           *string    `json:"recipient_phone,omitempty"`
	Remark                   *string    `json:"remark,omitempty"`
	Amount                   float64    `json:"amount"`
	Currency                 string     `json:"currency"`
	Status                   string     `json:"status"`
	Revision                 int        `json:"revision"`
	Provider                 string     `json:"provider"`
	ProviderInvoiceID        *string    `json:"provider_invoice_id,omitempty"`
	InvoiceItemName          *string    `json:"invoice_item_name,omitempty"`
	InvoiceCode              *string    `json:"invoice_code,omitempty"`
	InvoiceNumber            *string    `json:"invoice_number,omitempty"`
	DocumentFilename         *string    `json:"document_filename,omitempty"`
	DocumentSizeBytes        *int64     `json:"document_size_bytes,omitempty"`
	DocumentSHA256           *string    `json:"document_sha256,omitempty"`
	EmailDeliveryStatus      string     `json:"email_delivery_status"`
	EmailDeliveryAttempts    int        `json:"email_delivery_attempts"`
	EmailDeliveryErrorKind   *string    `json:"email_delivery_error_kind,omitempty"`
	EmailDeliveryAttemptedAt *time.Time `json:"email_delivery_attempted_at,omitempty"`
	EmailDeliveredAt         *time.Time `json:"email_delivered_at,omitempty"`
	EmailRetryable           bool       `json:"email_retryable"`
	FeishuNotificationStatus string     `json:"feishu_notification_status,omitempty"`
	FeishuNotificationRev    int        `json:"feishu_notification_revision,omitempty"`
	FeishuNotificationTries  int        `json:"feishu_notification_attempts,omitempty"`
	FeishuNotificationError  *string    `json:"feishu_notification_error_kind,omitempty"`
	FeishuNotifiedAt         *time.Time `json:"feishu_notified_at,omitempty"`
	FeishuRetryable          bool       `json:"feishu_retryable,omitempty"`
	RejectionReason          *string    `json:"rejection_reason,omitempty"`
	RequestedAt              time.Time  `json:"requested_at"`
	ProcessedAt              *time.Time `json:"processed_at,omitempty"`
	IssuedAt                 *time.Time `json:"issued_at,omitempty"`
	CreatedAt                time.Time  `json:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at"`
}

// PaymentOrderInvoicePresentation is the trusted, derived portion of an order
// response. Payment and fulfillment truth remain on PaymentOrder; this merely
// presents trusted timestamps plus the durable refund-review fence.
type PaymentOrderInvoicePresentation struct {
	Invoice           *PaymentInvoiceRecord
	ProductSnapshot   map[string]any
	PaymentStatus     string
	FulfillmentStatus string
	NeedsManualReview bool
	InvoiceEligible   bool
}

func PaymentInvoiceRecordFromEntity(invoice *dbent.PaymentInvoiceRequest) *PaymentInvoiceRecord {
	if invoice == nil {
		return nil
	}
	return &PaymentInvoiceRecord{
		ID:                       invoice.ID,
		OrderID:                  invoice.OrderID,
		UserID:                   invoice.UserID,
		TitleType:                invoice.TitleType,
		Title:                    invoice.Title,
		TaxIdentifier:            invoice.TaxIdentifier,
		RecipientEmail:           invoice.RecipientEmail,
		RecipientPhone:           invoice.RecipientPhone,
		Remark:                   invoice.Remark,
		Amount:                   invoice.Amount,
		Currency:                 invoice.Currency,
		Status:                   invoice.Status,
		Revision:                 invoice.Revision,
		Provider:                 invoice.Provider,
		ProviderInvoiceID:        invoice.ProviderInvoiceID,
		InvoiceItemName:          invoice.InvoiceItemName,
		InvoiceCode:              invoice.InvoiceCode,
		InvoiceNumber:            invoice.InvoiceNumber,
		DocumentFilename:         invoice.DocumentFilename,
		DocumentSizeBytes:        invoice.DocumentSizeBytes,
		DocumentSHA256:           invoice.DocumentSha256,
		EmailDeliveryStatus:      invoice.EmailDeliveryStatus,
		EmailDeliveryAttempts:    invoice.EmailDeliveryAttempts,
		EmailDeliveryErrorKind:   invoice.EmailDeliveryErrorKind,
		EmailDeliveryAttemptedAt: invoice.EmailDeliveryAttemptedAt,
		EmailDeliveredAt:         invoice.EmailDeliveredAt,
		EmailRetryable:           invoiceEmailRetryable(invoice, time.Now().UTC()),
		FeishuNotificationStatus: invoice.FeishuNotificationStatus,
		FeishuNotificationRev:    invoice.FeishuNotificationRevision,
		FeishuNotificationTries:  invoice.FeishuNotificationAttempts,
		FeishuNotificationError:  invoice.FeishuNotificationErrorKind,
		FeishuNotifiedAt:         invoice.FeishuNotifiedAt,
		FeishuRetryable:          invoiceFeishuRetryable(invoice, time.Now().UTC()),
		RejectionReason:          invoice.RejectionReason,
		RequestedAt:              invoice.RequestedAt,
		ProcessedAt:              invoice.ProcessedAt,
		IssuedAt:                 invoice.IssuedAt,
		CreatedAt:                invoice.CreatedAt,
		UpdatedAt:                invoice.UpdatedAt,
	}
}

func PaymentOrderInvoiceRecord(order *dbent.PaymentOrder) *PaymentInvoiceRecord {
	if order == nil {
		return nil
	}
	return PaymentInvoiceRecordFromEntity(order.Edges.InvoiceRequest)
}

// InvoiceOrderPresentations resolves the refund-review signal in one database
// query for the complete response page. It deliberately does not perform an
// invoice lookup per row; trusted list queries eager-load that edge.
func (s *PaymentService) InvoiceOrderPresentations(ctx context.Context, orders []*dbent.PaymentOrder) (map[int64]PaymentOrderInvoicePresentation, error) {
	presentations := make(map[int64]PaymentOrderInvoicePresentation, len(orders))
	if len(orders) == 0 {
		return presentations, nil
	}
	ids := make([]int64, 0, len(orders))
	for _, order := range orders {
		if order != nil {
			ids = append(ids, order.ID)
		}
	}
	reviewIDs, err := s.paymentOrderRefundReviewIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, order := range orders {
		if order == nil {
			continue
		}
		needsReview := reviewIDs[order.ID]
		presentations[order.ID] = PaymentOrderInvoicePresentation{
			Invoice:           PaymentOrderInvoiceRecord(order),
			ProductSnapshot:   SanitizedPaymentOrderProductSnapshot(order),
			PaymentStatus:     PaymentOrderPaymentStatus(order),
			FulfillmentStatus: PaymentOrderFulfillmentStatus(order, needsReview),
			NeedsManualReview: needsReview,
			InvoiceEligible:   invoiceOrderEligible(order, needsReview),
		}
	}
	return presentations, nil
}

func (s *PaymentService) paymentOrderRefundReviewIDs(ctx context.Context, ids []int64) (map[int64]bool, error) {
	result := make(map[int64]bool)
	if len(ids) == 0 {
		return result, nil
	}
	if s == nil || s.entClient == nil {
		return nil, errors.New("invoice order projection requires an order store")
	}
	reviewIDs, err := s.entClient.PaymentOrder.Query().
		Where(paymentorder.IDIn(ids...), paymentOrderHasUnifiedRefundReview()).
		IDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("load invoice refund-review flags: %w", err)
	}
	for _, id := range reviewIDs {
		result[id] = true
	}
	return result, nil
}

func PaymentOrderPaymentStatus(order *dbent.PaymentOrder) string {
	if order != nil && order.PaidAt != nil {
		return InvoicePaymentStatusPaid
	}
	return InvoicePaymentStatusUnpaid
}

func PaymentOrderFulfillmentStatus(order *dbent.PaymentOrder, needsManualReview bool) string {
	if order == nil || order.PaidAt == nil {
		return InvoiceFulfillmentStatusNotStarted
	}
	// completed_at is the durable fulfillment fact and stays authoritative even
	// if a later refund-review warning is raised.
	if order.CompletedAt != nil {
		return InvoiceFulfillmentStatusFulfilled
	}
	if needsManualReview {
		return InvoiceFulfillmentStatusManualReview
	}
	if order.Status == OrderStatusFailed {
		return InvoiceFulfillmentStatusFailed
	}
	return InvoiceFulfillmentStatusPending
}

// SanitizedPaymentOrderProductSnapshot returns only immutable purchase evidence
// that product creation writes into product_snapshot. Unrecognized historical
// keys, nested objects, and absent snapshots are intentionally omitted.
func SanitizedPaymentOrderProductSnapshot(order *dbent.PaymentOrder) map[string]any {
	if order == nil || len(order.ProductSnapshot) == 0 {
		return nil
	}
	out := make(map[string]any)
	for _, key := range []string{"kind", "label", "description", "name", "product_name", "currency", "validity_unit", "group_name"} {
		if value, ok := sanitizedInvoiceSnapshotString(order.ProductSnapshot[key]); ok {
			out[key] = value
		}
	}
	if features, ok := sanitizedInvoiceSnapshotStrings(order.ProductSnapshot["features"]); ok {
		out["features"] = features
	}
	for _, key := range []string{
		"request_amount", "list_price", "price", "discount_percent", "credited_amount", "pay_amount",
		"estimated_rate_multiplier", "estimated_tokens", "plan_id", "order_amount", "validity_days",
		"subscription_days", "group_id", "daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd",
	} {
		if value, ok := sanitizedInvoiceSnapshotNumber(order.ProductSnapshot[key]); ok {
			out[key] = value
		}
	}
	if raw, ok := order.ProductSnapshot["entitlements"].(map[string]any); ok {
		entitlements := make(map[string]any)
		for _, key := range []string{"balance_bonus", "concurrency", "reset_card_count", "reset_card_expiry_days"} {
			if value, ok := sanitizedInvoiceSnapshotNumber(raw[key]); ok {
				entitlements[key] = value
			}
		}
		if message, ok := sanitizedInvoiceSnapshotString(raw["message"]); ok {
			entitlements["message"] = message
		}
		if len(entitlements) > 0 {
			out["entitlements"] = entitlements
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sanitizedInvoiceSnapshotString(value any) (string, bool) {
	text, ok := value.(string)
	if !ok || utf8.RuneCountInString(text) > 1000 {
		return "", false
	}
	return text, true
}

func sanitizedInvoiceSnapshotStrings(value any) ([]string, bool) {
	var values []string
	switch typed := value.(type) {
	case []string:
		values = typed
	case []any:
		values = make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			values = append(values, text)
		}
	default:
		return nil, false
	}
	if len(values) > 100 {
		return nil, false
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if utf8.RuneCountInString(value) > 500 {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}

func sanitizedInvoiceSnapshotNumber(value any) (any, bool) {
	switch number := value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return number, true
	case float32:
		if math.IsNaN(float64(number)) || math.IsInf(float64(number), 0) {
			return nil, false
		}
		return number, true
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, false
		}
		return number, true
	default:
		return nil, false
	}
}

// CreateOrResubmitInvoiceRequest creates the first request or replaces a
// rejected revision. It runs under the payment-order lock used by unified
// refunds, and reads the durable review fence while that lock is held.
func (s *PaymentService) CreateOrResubmitInvoiceRequest(ctx context.Context, orderID, userID int64, input CreateInvoiceRequestInput) (*PaymentInvoiceRecord, error) {
	input, err := normalizeInvoiceRequestInput(input)
	if err != nil {
		return nil, err
	}

	var result *PaymentInvoiceRecord
	err = s.withLockedInvoiceOrder(ctx, orderID, func(txCtx context.Context, client *dbent.Client, order *dbent.PaymentOrder) error {
		if order.UserID != userID {
			// Do not confirm an invoice request exists for an order the caller does
			// not own.
			return infraerrors.NotFound("INVOICE_ORDER_NOT_FOUND", "order not found")
		}
		needsReview, reviewErr := unifiedRefundOrderNeedsReview(txCtx, client, order.ID)
		if reviewErr != nil {
			return fmt.Errorf("read invoice refund-review fence: %w", reviewErr)
		}
		if !invoiceOrderEligible(order, needsReview) {
			return infraerrors.BadRequest("INVOICE_ORDER_NOT_ELIGIBLE", "only completed, unrefunded orders outside refund review can be invoiced")
		}

		now := time.Now().UTC()
		if existing := order.Edges.InvoiceRequest; existing != nil {
			if existing.Status != InvoiceStatusRejected {
				return infraerrors.Conflict("INVOICE_ALREADY_REQUESTED", "this order already has an active invoice request").
					WithMetadata(map[string]string{"status": existing.Status})
			}
			newRevision := existing.Revision + 1
			updated, updateErr := client.PaymentInvoiceRequest.Update().
				Where(
					paymentinvoicerequest.IDEQ(existing.ID),
					paymentinvoicerequest.StatusEQ(InvoiceStatusRejected),
					paymentinvoicerequest.RevisionEQ(existing.Revision),
				).
				SetTitleType(input.TitleType).
				SetTitle(input.Title).
				ClearTaxIdentifier().
				SetNillableTaxIdentifier(optionalInvoiceString(input.TaxIdentifier)).
				SetRecipientEmail(input.RecipientEmail).
				ClearRecipientPhone().
				SetNillableRecipientPhone(optionalInvoiceString(input.RecipientPhone)).
				ClearRemark().
				SetNillableRemark(optionalInvoiceString(input.Remark)).
				SetAmount(order.PayAmount).
				SetCurrency(PaymentOrderCurrency(order)).
				SetStatus(InvoiceStatusPending).
				SetRevision(newRevision).
				SetProvider("manual").
				ClearProviderInvoiceID().
				ClearInvoiceItemName().
				ClearInvoiceCode().
				ClearInvoiceNumber().
				ClearDocumentFilename().
				ClearDocumentSizeBytes().
				ClearDocumentSha256().
				SetEmailDeliveryStatus(InvoiceEmailDeliveryNotSent).
				SetEmailDeliveryAttempts(0).
				ClearEmailDeliveryErrorKind().
				ClearEmailDeliveryAttemptedAt().
				ClearEmailDeliveredAt().
				ClearEmailDeliveryClaimToken().
				ClearEmailDeliveryClaimedAt().
				ClearEmailDeliveryNextAttemptAt().
				SetFeishuNotificationStatus(InvoiceFeishuNotificationPending).
				SetFeishuNotificationRevision(newRevision).
				SetFeishuNotificationAttempts(0).
				ClearFeishuNotificationErrorKind().
				ClearFeishuNotificationAttemptedAt().
				ClearFeishuNotifiedAt().
				ClearFeishuNotificationClaimToken().
				ClearFeishuNotificationClaimedAt().
				SetFeishuNotificationNextAttemptAt(now).
				ClearRejectionReason().
				ClearProcessedBy().
				SetRequestedAt(now).
				ClearProcessedAt().
				ClearIssuedAt().
				Save(txCtx)
			if updateErr != nil {
				return fmt.Errorf("resubmit invoice request: %w", updateErr)
			}
			if updated != 1 {
				return infraerrors.Conflict("INVOICE_STATUS_CONFLICT", "invoice request changed; reload and try again")
			}
			if _, deleteErr := client.PaymentInvoiceDocument.Delete().
				Where(paymentinvoicedocument.InvoiceRequestIDEQ(existing.ID)).Exec(txCtx); deleteErr != nil {
				return fmt.Errorf("clear prior invoice document: %w", deleteErr)
			}
			invoice, getErr := client.PaymentInvoiceRequest.Get(txCtx, existing.ID)
			if getErr != nil {
				return fmt.Errorf("reload invoice request: %w", getErr)
			}
			if auditErr := writeInvoiceAudit(txCtx, client, order.ID, invoice, "RESUBMITTED", fmt.Sprintf("user:%d", userID)); auditErr != nil {
				return auditErr
			}
			result = PaymentInvoiceRecordFromEntity(invoice)
			return nil
		}

		invoice, createErr := client.PaymentInvoiceRequest.Create().
			SetOrderID(order.ID).
			SetUserID(userID).
			SetTitleType(input.TitleType).
			SetTitle(input.Title).
			SetNillableTaxIdentifier(optionalInvoiceString(input.TaxIdentifier)).
			SetRecipientEmail(input.RecipientEmail).
			SetNillableRecipientPhone(optionalInvoiceString(input.RecipientPhone)).
			SetNillableRemark(optionalInvoiceString(input.Remark)).
			SetAmount(order.PayAmount).
			SetCurrency(PaymentOrderCurrency(order)).
			SetStatus(InvoiceStatusPending).
			SetRevision(1).
			SetRequestedAt(now).
			SetFeishuNotificationStatus(InvoiceFeishuNotificationPending).
			SetFeishuNotificationRevision(1).
			SetFeishuNotificationNextAttemptAt(now).
			Save(txCtx)
		if createErr != nil {
			if dbent.IsConstraintError(createErr) {
				return infraerrors.Conflict("INVOICE_ALREADY_REQUESTED", "this order already has an invoice request")
			}
			return fmt.Errorf("create invoice request: %w", createErr)
		}
		if auditErr := writeInvoiceAudit(txCtx, client, order.ID, invoice, "REQUESTED", fmt.Sprintf("user:%d", userID)); auditErr != nil {
			return auditErr
		}
		result = PaymentInvoiceRecordFromEntity(invoice)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result != nil {
		s.scheduleInvoiceFeishuDelivery(ctx, result.ID)
	}
	return result, nil
}

func (s *PaymentService) GetOrderInvoiceRequest(ctx context.Context, orderID, userID int64) (*PaymentInvoiceRecord, error) {
	invoice, err := s.entClient.PaymentInvoiceRequest.Query().
		Where(paymentinvoicerequest.OrderIDEQ(orderID), paymentinvoicerequest.UserIDEQ(userID)).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, infraerrors.NotFound("INVOICE_NOT_FOUND", "invoice request not found")
		}
		return nil, fmt.Errorf("get invoice request: %w", err)
	}
	return PaymentInvoiceRecordFromEntity(invoice), nil
}

// AdminUpdateOrderInvoiceRequest advances a request without touching payment or
// refund facts. PROCESSING and ISSUED re-read the unified refund-review fence
// under the same payment-order row lock used by refund writers.
func (s *PaymentService) AdminUpdateOrderInvoiceRequest(ctx context.Context, orderID, adminID int64, input AdminUpdateInvoiceInput) (*PaymentInvoiceRecord, error) {
	input, err := normalizeAdminInvoiceInput(input)
	if err != nil {
		return nil, err
	}

	var result *PaymentInvoiceRecord
	err = s.withLockedInvoiceOrder(ctx, orderID, func(txCtx context.Context, client *dbent.Client, order *dbent.PaymentOrder) error {
		invoice := order.Edges.InvoiceRequest
		if invoice == nil {
			return infraerrors.NotFound("INVOICE_NOT_FOUND", "invoice request not found")
		}
		if input.Status != InvoiceStatusRejected {
			needsReview, reviewErr := unifiedRefundOrderNeedsReview(txCtx, client, order.ID)
			if reviewErr != nil {
				return fmt.Errorf("read invoice refund-review fence: %w", reviewErr)
			}
			if !invoiceOrderEligible(order, needsReview) {
				return infraerrors.BadRequest("INVOICE_ORDER_NOT_ELIGIBLE", "a refunded or refund-reviewed order cannot be processed or issued")
			}
		}
		if !invoiceTransitionAllowed(invoice.Status, input.Status) {
			return infraerrors.Conflict("INVOICE_STATUS_CONFLICT", "invoice status transition is not allowed").
				WithMetadata(map[string]string{"current_status": invoice.Status, "target_status": input.Status})
		}

		now := time.Now().UTC()
		update := client.PaymentInvoiceRequest.Update().
			Where(
				paymentinvoicerequest.IDEQ(invoice.ID),
				paymentinvoicerequest.StatusEQ(invoice.Status),
				paymentinvoicerequest.RevisionEQ(invoice.Revision),
			).
			SetStatus(input.Status).
			SetProcessedBy(adminID).
			SetProcessedAt(now)

		switch input.Status {
		case InvoiceStatusProcessing:
			update = update.
				ClearRejectionReason().
				SetEmailDeliveryStatus(InvoiceEmailDeliveryNotSent).
				SetEmailDeliveryAttempts(0).
				ClearEmailDeliveryErrorKind().
				ClearEmailDeliveryAttemptedAt().
				ClearEmailDeliveredAt().
				ClearEmailDeliveryClaimToken().
				ClearEmailDeliveryClaimedAt().
				ClearEmailDeliveryNextAttemptAt()
		case InvoiceStatusIssued:
			documentHash := fmt.Sprintf("%x", sha256.Sum256(input.Document.Data))
			update = update.
				SetProvider(input.Provider).
				SetNillableProviderInvoiceID(optionalInvoiceString(input.ProviderInvoiceID)).
				SetInvoiceItemName(input.InvoiceItemName).
				SetNillableInvoiceCode(optionalInvoiceString(input.InvoiceCode)).
				SetInvoiceNumber(input.InvoiceNumber).
				SetDocumentFilename(input.Document.Filename).
				SetDocumentSizeBytes(int64(len(input.Document.Data))).
				SetDocumentSha256(documentHash).
				SetEmailDeliveryStatus(InvoiceEmailDeliveryPending).
				SetEmailDeliveryAttempts(0).
				ClearEmailDeliveryErrorKind().
				ClearEmailDeliveryAttemptedAt().
				ClearEmailDeliveredAt().
				ClearEmailDeliveryClaimToken().
				ClearEmailDeliveryClaimedAt().
				SetEmailDeliveryNextAttemptAt(now).
				SetIssuedAt(now).
				ClearRejectionReason()
		case InvoiceStatusRejected:
			update = update.
				SetRejectionReason(input.RejectionReason).
				ClearProviderInvoiceID().
				ClearInvoiceItemName().
				ClearInvoiceCode().
				ClearInvoiceNumber().
				ClearDocumentFilename().
				ClearDocumentSizeBytes().
				ClearDocumentSha256().
				SetEmailDeliveryStatus(InvoiceEmailDeliveryPending).
				SetEmailDeliveryAttempts(0).
				ClearEmailDeliveryErrorKind().
				ClearEmailDeliveryAttemptedAt().
				ClearEmailDeliveredAt().
				ClearEmailDeliveryClaimToken().
				ClearEmailDeliveryClaimedAt().
				SetEmailDeliveryNextAttemptAt(now).
				ClearIssuedAt()
		}

		updated, updateErr := update.Save(txCtx)
		if updateErr != nil {
			return fmt.Errorf("update invoice request: %w", updateErr)
		}
		if updated != 1 {
			return infraerrors.Conflict("INVOICE_STATUS_CONFLICT", "invoice request changed; reload and try again")
		}
		if input.Status == InvoiceStatusIssued {
			documentHash := fmt.Sprintf("%x", sha256.Sum256(input.Document.Data))
			if _, createErr := client.PaymentInvoiceDocument.Create().
				SetInvoiceRequestID(invoice.ID).
				SetFilename(input.Document.Filename).
				SetContentType("application/pdf").
				SetSizeBytes(int64(len(input.Document.Data))).
				SetSha256(documentHash).
				SetData(input.Document.Data).
				Save(txCtx); createErr != nil {
				return fmt.Errorf("store invoice PDF: %w", createErr)
			}
		} else if input.Status == InvoiceStatusRejected {
			if _, deleteErr := client.PaymentInvoiceDocument.Delete().
				Where(paymentinvoicedocument.InvoiceRequestIDEQ(invoice.ID)).Exec(txCtx); deleteErr != nil {
				return fmt.Errorf("clear rejected invoice document: %w", deleteErr)
			}
		}
		updatedInvoice, getErr := client.PaymentInvoiceRequest.Get(txCtx, invoice.ID)
		if getErr != nil {
			return fmt.Errorf("reload invoice request: %w", getErr)
		}
		if auditErr := writeInvoiceAudit(txCtx, client, orderID, updatedInvoice, input.Status, fmt.Sprintf("admin:%d", adminID)); auditErr != nil {
			return auditErr
		}
		result = PaymentInvoiceRecordFromEntity(updatedInvoice)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result != nil && (result.Status == InvoiceStatusIssued || result.Status == InvoiceStatusRejected) {
		s.scheduleInvoiceEmailDelivery(ctx, result.ID)
	}
	return result, nil
}

// AdminRetryInvoiceEmail makes a terminal customer result eligible for an
// immediate durable claim. A live SENDING claim is never stolen; only PENDING,
// FAILED, and stale SENDING records are retryable.
func (s *PaymentService) AdminRetryInvoiceEmail(ctx context.Context, orderID, adminID int64) (*PaymentInvoiceRecord, error) {
	var result *PaymentInvoiceRecord
	err := s.withLockedInvoiceOrder(ctx, orderID, func(txCtx context.Context, client *dbent.Client, order *dbent.PaymentOrder) error {
		invoice := order.Edges.InvoiceRequest
		if invoice == nil {
			return infraerrors.NotFound("INVOICE_NOT_FOUND", "invoice request not found")
		}
		if invoice.Status != InvoiceStatusIssued && invoice.Status != InvoiceStatusRejected {
			return infraerrors.Conflict("INVOICE_EMAIL_RETRY_NOT_ALLOWED", "only a completed invoice result can be emailed")
		}
		if !invoiceEmailRetryable(invoice, time.Now().UTC()) {
			return infraerrors.Conflict("INVOICE_EMAIL_RETRY_NOT_ALLOWED", "invoice email is already being delivered or was sent")
		}
		preds := []predicate.PaymentInvoiceRequest{
			paymentinvoicerequest.IDEQ(invoice.ID),
			paymentinvoicerequest.RevisionEQ(invoice.Revision),
			paymentinvoicerequest.StatusEQ(invoice.Status),
			paymentinvoicerequest.EmailDeliveryStatusEQ(invoice.EmailDeliveryStatus),
			invoiceEmailClaimTokenPredicate(invoice),
		}
		now := time.Now().UTC()
		updated, updateErr := client.PaymentInvoiceRequest.Update().Where(preds...).
			SetEmailDeliveryStatus(InvoiceEmailDeliveryPending).
			ClearEmailDeliveryErrorKind().
			ClearEmailDeliveryAttemptedAt().
			ClearEmailDeliveredAt().
			ClearEmailDeliveryClaimToken().
			ClearEmailDeliveryClaimedAt().
			SetEmailDeliveryNextAttemptAt(now).
			Save(txCtx)
		if updateErr != nil {
			return fmt.Errorf("prepare invoice email retry: %w", updateErr)
		}
		if updated != 1 {
			return infraerrors.Conflict("INVOICE_STATUS_CONFLICT", "invoice request changed; reload and try again")
		}
		updatedInvoice, getErr := client.PaymentInvoiceRequest.Get(txCtx, invoice.ID)
		if getErr != nil {
			return fmt.Errorf("reload invoice request for email retry: %w", getErr)
		}
		if auditErr := writeInvoiceAudit(txCtx, client, order.ID, updatedInvoice, "EMAIL_RETRY", fmt.Sprintf("admin:%d", adminID)); auditErr != nil {
			return auditErr
		}
		result = PaymentInvoiceRecordFromEntity(updatedInvoice)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result != nil {
		s.scheduleInvoiceEmailDelivery(ctx, result.ID)
	}
	return result, nil
}

// AdminRetryInvoiceFeishuNotification retries the privacy-minimal request
// signal. It does not reclassify the request as an incident and does not alter
// invoice, payment, or refund state.
func (s *PaymentService) AdminRetryInvoiceFeishuNotification(ctx context.Context, orderID, adminID int64) (*PaymentInvoiceRecord, error) {
	var result *PaymentInvoiceRecord
	err := s.withLockedInvoiceOrder(ctx, orderID, func(txCtx context.Context, client *dbent.Client, order *dbent.PaymentOrder) error {
		invoice := order.Edges.InvoiceRequest
		if invoice == nil {
			return infraerrors.NotFound("INVOICE_NOT_FOUND", "invoice request not found")
		}
		if !invoiceFeishuRetryable(invoice, time.Now().UTC()) {
			return infraerrors.Conflict("INVOICE_FEISHU_RETRY_NOT_ALLOWED", "invoice request notification is already being delivered or was sent")
		}
		preds := []predicate.PaymentInvoiceRequest{
			paymentinvoicerequest.IDEQ(invoice.ID),
			paymentinvoicerequest.RevisionEQ(invoice.Revision),
			paymentinvoicerequest.FeishuNotificationRevisionEQ(invoice.FeishuNotificationRevision),
			paymentinvoicerequest.FeishuNotificationStatusEQ(invoice.FeishuNotificationStatus),
			invoiceFeishuClaimTokenPredicate(invoice),
		}
		now := time.Now().UTC()
		updated, updateErr := client.PaymentInvoiceRequest.Update().Where(preds...).
			SetFeishuNotificationStatus(InvoiceFeishuNotificationPending).
			ClearFeishuNotificationErrorKind().
			ClearFeishuNotificationAttemptedAt().
			ClearFeishuNotifiedAt().
			ClearFeishuNotificationClaimToken().
			ClearFeishuNotificationClaimedAt().
			SetFeishuNotificationNextAttemptAt(now).
			Save(txCtx)
		if updateErr != nil {
			return fmt.Errorf("prepare invoice Feishu retry: %w", updateErr)
		}
		if updated != 1 {
			return infraerrors.Conflict("INVOICE_STATUS_CONFLICT", "invoice request changed; reload and try again")
		}
		updatedInvoice, getErr := client.PaymentInvoiceRequest.Get(txCtx, invoice.ID)
		if getErr != nil {
			return fmt.Errorf("reload invoice request for Feishu retry: %w", getErr)
		}
		if auditErr := writeInvoiceAudit(txCtx, client, order.ID, updatedInvoice, "FEISHU_RETRY", fmt.Sprintf("admin:%d", adminID)); auditErr != nil {
			return auditErr
		}
		result = PaymentInvoiceRecordFromEntity(updatedInvoice)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result != nil {
		s.scheduleInvoiceFeishuDelivery(ctx, result.ID)
	}
	return result, nil
}

func invoiceOrderEligible(order *dbent.PaymentOrder, needsReview bool) bool {
	if order == nil || needsReview || order.PaidAt == nil || order.CompletedAt == nil || order.PayAmount <= 0 {
		return false
	}
	if order.RefundAmount > 0 || psIsRefundStatus(order.Status) {
		return false
	}
	return true
}

func (s *PaymentService) withLockedInvoiceOrder(ctx context.Context, orderID int64, fn func(context.Context, *dbent.Client, *dbent.PaymentOrder) error) error {
	if s == nil || s.entClient == nil {
		return errors.New("invoice workflow requires an order store")
	}
	if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
		order, err := queryInvoiceOrderForUpdate(ctx, existingTx.Client(), orderID)
		if err != nil {
			return err
		}
		return fn(ctx, existingTx.Client(), order)
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin invoice transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	txCtx := dbent.NewTxContext(ctx, tx)
	order, err := queryInvoiceOrderForUpdate(txCtx, tx.Client(), orderID)
	if err != nil {
		return err
	}
	if err := fn(txCtx, tx.Client(), order); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit invoice transaction: %w", err)
	}
	committed = true
	return nil
}

func queryInvoiceOrderForUpdate(ctx context.Context, client *dbent.Client, orderID int64) (*dbent.PaymentOrder, error) {
	query := client.PaymentOrder.Query().Where(paymentorder.IDEQ(orderID)).WithInvoiceRequest()
	if client.Driver() != nil && client.Driver().Dialect() == dialect.Postgres {
		query = query.ForUpdate()
	}
	order, err := query.Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, infraerrors.NotFound("INVOICE_ORDER_NOT_FOUND", "order not found")
		}
		return nil, fmt.Errorf("lock invoice order: %w", err)
	}
	return order, nil
}

func writeInvoiceAudit(ctx context.Context, client *dbent.Client, orderID int64, invoice *dbent.PaymentInvoiceRequest, action, operator string) error {
	if invoice == nil {
		return nil
	}
	detail, err := json.Marshal(map[string]any{
		"invoiceRequestID": invoice.ID,
		"revision":         invoice.Revision,
		"status":           invoice.Status,
	})
	if err != nil {
		return fmt.Errorf("marshal invoice audit: %w", err)
	}
	if _, err := client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(orderID, 10)).
		SetAction(fmt.Sprintf("INVOICE_%s_%d", strings.ToUpper(action), invoice.Revision)).
		SetOperator(operator).
		SetDetail(string(detail)).
		Save(ctx); err != nil {
		return fmt.Errorf("write invoice audit: %w", err)
	}
	return nil
}

func normalizeInvoiceRequestInput(input CreateInvoiceRequestInput) (CreateInvoiceRequestInput, error) {
	input.TitleType = strings.ToLower(strings.TrimSpace(input.TitleType))
	input.Title = strings.TrimSpace(input.Title)
	input.TaxIdentifier = strings.ToUpper(strings.TrimSpace(input.TaxIdentifier))
	input.RecipientEmail = strings.TrimSpace(input.RecipientEmail)
	input.RecipientPhone = strings.TrimSpace(input.RecipientPhone)
	input.Remark = strings.TrimSpace(input.Remark)

	if input.TitleType != InvoiceTitleTypePersonal && input.TitleType != InvoiceTitleTypeEnterprise {
		return input, invoiceValidationError("title_type", "title type must be personal or enterprise")
	}
	if input.Title == "" || utf8.RuneCountInString(input.Title) > 200 {
		return input, invoiceValidationError("title", "invoice title is required and must not exceed 200 characters")
	}
	if input.TitleType == InvoiceTitleTypeEnterprise {
		if !invoiceTaxIdentifierPattern.MatchString(input.TaxIdentifier) {
			return input, invoiceValidationError("tax_identifier", "enterprise taxpayer identifier is required and invalid")
		}
	} else {
		input.TaxIdentifier = ""
	}
	if len(input.RecipientEmail) > 255 || strings.ContainsAny(input.RecipientEmail, "\r\n") {
		return input, invoiceValidationError("recipient_email", "recipient email is invalid")
	}
	parsed, err := mail.ParseAddress(input.RecipientEmail)
	if err != nil || parsed.Address != input.RecipientEmail {
		return input, invoiceValidationError("recipient_email", "recipient email is invalid")
	}
	if input.RecipientPhone != "" && !invoicePhonePattern.MatchString(input.RecipientPhone) {
		return input, invoiceValidationError("recipient_phone", "recipient phone is invalid")
	}
	if utf8.RuneCountInString(input.Remark) > 1000 {
		return input, invoiceValidationError("remark", "remark must not exceed 1000 characters")
	}
	return input, nil
}

func normalizeAdminInvoiceInput(input AdminUpdateInvoiceInput) (AdminUpdateInvoiceInput, error) {
	input.Status = strings.ToUpper(strings.TrimSpace(input.Status))
	input.Provider = strings.TrimSpace(input.Provider)
	input.ProviderInvoiceID = strings.TrimSpace(input.ProviderInvoiceID)
	input.InvoiceItemName = strings.TrimSpace(input.InvoiceItemName)
	input.InvoiceCode = strings.TrimSpace(input.InvoiceCode)
	input.InvoiceNumber = strings.TrimSpace(input.InvoiceNumber)
	input.RejectionReason = strings.TrimSpace(input.RejectionReason)

	if input.Status != InvoiceStatusProcessing && input.Status != InvoiceStatusIssued && input.Status != InvoiceStatusRejected {
		return input, invoiceValidationError("status", "target invoice status is invalid")
	}
	if input.Status == InvoiceStatusIssued {
		if input.Provider == "" {
			input.Provider = "manual"
		}
		if utf8.RuneCountInString(input.Provider) > 50 || len(input.ProviderInvoiceID) > 128 {
			return input, invoiceValidationError("provider", "invoice provider reference is too long")
		}
		if input.InvoiceItemName == "" || utf8.RuneCountInString(input.InvoiceItemName) > 200 {
			return input, invoiceValidationError("invoice_item_name", "invoice item name is required and must not exceed 200 characters")
		}
		if input.InvoiceNumber == "" || len(input.InvoiceNumber) > 64 || len(input.InvoiceCode) > 64 {
			return input, invoiceValidationError("invoice_number", "invoice number is required and invalid")
		}
		document, err := normalizeInvoicePDF(input.Document)
		if err != nil {
			return input, err
		}
		input.Document = document
	}
	if input.Status == InvoiceStatusRejected && (input.RejectionReason == "" || utf8.RuneCountInString(input.RejectionReason) > 500) {
		return input, invoiceValidationError("rejection_reason", "rejection reason is required and must not exceed 500 characters")
	}
	return input, nil
}

func normalizeInvoicePDF(input *InvoicePDFInput) (*InvoicePDFInput, error) {
	if input == nil || len(input.Data) == 0 {
		return nil, invoiceValidationError("invoice_pdf", "a PDF invoice attachment is required")
	}
	if len(input.Data) > MaxInvoicePDFBytes {
		return nil, invoiceValidationError("invoice_pdf", "the PDF invoice attachment exceeds the 10 MiB limit")
	}
	filename := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(input.Filename))
	filename = path.Base(strings.ReplaceAll(filename, "\\", "/"))
	if filename == "." || filename == "" || utf8.RuneCountInString(filename) > 255 || !strings.HasSuffix(strings.ToLower(filename), ".pdf") {
		return nil, invoiceValidationError("invoice_pdf", "the invoice attachment must have a valid .pdf filename")
	}
	if !bytes.HasPrefix(input.Data, []byte("%PDF-")) || http.DetectContentType(input.Data) != "application/pdf" {
		return nil, invoiceValidationError("invoice_pdf", "the uploaded file is not a valid PDF document")
	}
	return &InvoicePDFInput{Filename: filename, Data: input.Data}, nil
}

func invoiceTransitionAllowed(current, target string) bool {
	switch current {
	case InvoiceStatusPending:
		return target == InvoiceStatusProcessing || target == InvoiceStatusRejected
	case InvoiceStatusProcessing:
		return target == InvoiceStatusIssued || target == InvoiceStatusRejected
	default:
		return false
	}
}

func invoiceValidationError(field, message string) error {
	return infraerrors.BadRequest("INVOICE_VALIDATION_ERROR", message).WithMetadata(map[string]string{"field": field})
}

func optionalInvoiceString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func normalizeInvoiceStatusFilter(raw string) (string, error) {
	status := strings.ToUpper(strings.TrimSpace(raw))
	switch status {
	case "", InvoiceStatusFilterNone, InvoiceStatusFilterAny, InvoiceStatusPending, InvoiceStatusProcessing, InvoiceStatusIssued, InvoiceStatusRejected:
		return status, nil
	default:
		return "", invoiceValidationError("invoice_status", "invoice status filter is invalid")
	}
}

func normalizeInvoiceEmailStatusFilter(raw string) (string, error) {
	status := strings.ToUpper(strings.TrimSpace(raw))
	switch status {
	case "", InvoiceEmailDeliveryNotSent, InvoiceEmailDeliveryPending, InvoiceEmailDeliverySending, InvoiceEmailDeliverySent, InvoiceEmailDeliveryFailed:
		return status, nil
	default:
		return "", invoiceValidationError("invoice_email_status", "invoice email status filter is invalid")
	}
}

func normalizeInvoicePaymentStatusFilter(raw string) (string, error) {
	status := strings.ToUpper(strings.TrimSpace(raw))
	if status == "" || status == InvoicePaymentStatusPaid || status == InvoicePaymentStatusUnpaid {
		return status, nil
	}
	return "", invoiceValidationError("payment_status", "payment status filter is invalid")
}

func normalizeInvoiceFulfillmentStatusFilter(raw string) (string, error) {
	status := strings.ToUpper(strings.TrimSpace(raw))
	switch status {
	case "", InvoiceFulfillmentStatusFulfilled, InvoiceFulfillmentStatusFailed, InvoiceFulfillmentStatusPending, InvoiceFulfillmentStatusNotStarted, InvoiceFulfillmentStatusManualReview:
		return status, nil
	default:
		return "", invoiceValidationError("fulfillment_status", "fulfillment status filter is invalid")
	}
}

// paymentOrderHasUnifiedRefundReview is deliberately the same durable source
// as unifiedRefundOrderNeedsReview. It is used only to form list predicates;
// each lifecycle mutation re-reads the authoritative state under a row lock.
func paymentOrderHasUnifiedRefundReview() predicate.PaymentOrder {
	return predicate.PaymentOrder(func(selector *entsql.Selector) {
		selector.Where(entsql.P(func(builder *entsql.Builder) {
			builder.WriteString(`(EXISTS (
				SELECT 1 FROM unified_payment_refund_attempts AS refund_attempt
				WHERE refund_attempt.order_id = `).
				Ident(selector.C(paymentorder.FieldID)).
				WriteString(` AND refund_attempt.needs_manual_review = TRUE
			) OR EXISTS (
				SELECT 1 FROM unified_payment_refund_events AS refund_event
				WHERE refund_event.order_id = `).
				Ident(selector.C(paymentorder.FieldID)).
				WriteString(` AND refund_event.action IN ('UNIFIED_REFUND_UNCORRELATED', 'UNIFIED_PAYMENT_EVENT_REJECTED')
			))`)
		}))
	})
}

func invoiceEmailRetryable(invoice *dbent.PaymentInvoiceRequest, now time.Time) bool {
	if invoice == nil || (invoice.Status != InvoiceStatusIssued && invoice.Status != InvoiceStatusRejected) {
		return false
	}
	switch invoice.EmailDeliveryStatus {
	case InvoiceEmailDeliveryPending, InvoiceEmailDeliveryFailed:
		return true
	case InvoiceEmailDeliverySending:
		return invoice.EmailDeliveryClaimedAt == nil || !invoice.EmailDeliveryClaimedAt.After(now.Add(-invoiceDeliveryClaimTTL))
	default:
		return false
	}
}

func invoiceFeishuRetryable(invoice *dbent.PaymentInvoiceRequest, now time.Time) bool {
	if invoice == nil || invoice.FeishuNotificationRevision != invoice.Revision {
		return false
	}
	switch invoice.FeishuNotificationStatus {
	case InvoiceFeishuNotificationPending, InvoiceFeishuNotificationFailed:
		return true
	case InvoiceFeishuNotificationSending:
		return invoice.FeishuNotificationClaimedAt == nil || !invoice.FeishuNotificationClaimedAt.After(now.Add(-invoiceDeliveryClaimTTL))
	default:
		return false
	}
}

func invoiceEmailClaimTokenPredicate(invoice *dbent.PaymentInvoiceRequest) predicate.PaymentInvoiceRequest {
	if invoice == nil || invoice.EmailDeliveryClaimToken == nil {
		return paymentinvoicerequest.EmailDeliveryClaimTokenIsNil()
	}
	return paymentinvoicerequest.EmailDeliveryClaimTokenEQ(*invoice.EmailDeliveryClaimToken)
}

func invoiceFeishuClaimTokenPredicate(invoice *dbent.PaymentInvoiceRequest) predicate.PaymentInvoiceRequest {
	if invoice == nil || invoice.FeishuNotificationClaimToken == nil {
		return paymentinvoicerequest.FeishuNotificationClaimTokenIsNil()
	}
	return paymentinvoicerequest.FeishuNotificationClaimTokenEQ(*invoice.FeishuNotificationClaimToken)
}

type invoiceEmailDeliveryClaim struct {
	Invoice *dbent.PaymentInvoiceRequest
	Token   string
}

type invoiceFeishuDeliveryClaim struct {
	Invoice *dbent.PaymentInvoiceRequest
	Token   string
}

// scheduleInvoiceEmailDelivery schedules only after an outer Ent transaction
// commits. Delivery failure updates its own durable state and never rolls back
// an issued/rejected invoice.
func (s *PaymentService) scheduleInvoiceEmailDelivery(ctx context.Context, invoiceID int64) {
	if s == nil || invoiceID <= 0 {
		return
	}
	dispatch := func() { s.dispatchInvoiceEmailDelivery(invoiceID) }
	if tx := dbent.TxFromContext(ctx); tx != nil {
		tx.OnCommit(func(next dbent.Committer) dbent.Committer {
			return dbent.CommitFunc(func(commitCtx context.Context, committedTx *dbent.Tx) error {
				if err := next.Commit(commitCtx, committedTx); err != nil {
					return err
				}
				dispatch()
				return nil
			})
		})
		return
	}
	dispatch()
}

func (s *PaymentService) scheduleInvoiceFeishuDelivery(ctx context.Context, invoiceID int64) {
	if s == nil || invoiceID <= 0 {
		return
	}
	dispatch := func() { s.dispatchInvoiceFeishuDelivery(invoiceID) }
	if tx := dbent.TxFromContext(ctx); tx != nil {
		tx.OnCommit(func(next dbent.Committer) dbent.Committer {
			return dbent.CommitFunc(func(commitCtx context.Context, committedTx *dbent.Tx) error {
				if err := next.Commit(commitCtx, committedTx); err != nil {
					return err
				}
				dispatch()
				return nil
			})
		})
		return
	}
	dispatch()
}

func (s *PaymentService) dispatchInvoiceEmailDelivery(invoiceID int64) {
	if !runtimegate.SharedWorkAllowed() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), invoiceDeliverySendTimeout)
		defer cancel()
		if _, err := s.deliverInvoiceEmail(ctx, invoiceID); err != nil {
			slog.Warn("invoice customer email dispatch failed", "invoice_request_id", invoiceID, "error_kind", invoiceDeliveryErrorKind(err))
		}
	}()
}

func (s *PaymentService) dispatchInvoiceFeishuDelivery(invoiceID int64) {
	if !runtimegate.SharedWorkAllowed() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), invoiceDeliverySendTimeout)
		defer cancel()
		if _, err := s.deliverInvoiceFeishu(ctx, invoiceID); err != nil {
			slog.Warn("invoice Feishu notification dispatch failed", "invoice_request_id", invoiceID, "error_kind", invoiceDeliveryErrorKind(err))
		}
	}()
}

// RecoverInvoiceNotifications is called by the isolated, leader-gated invoice
// notifier. It reclaims durable PENDING/FAILED work and SENDING claims that outlived a
// crashed worker. Candidate predicates run before LIMIT so not-yet-due rows do
// not starve older eligible notifications.
func (s *PaymentService) RecoverInvoiceNotifications(ctx context.Context) (int, error) {
	if !runtimegate.SharedWorkAllowed() {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s == nil || s.entClient == nil {
		return 0, errors.New("invoice notification recovery requires an order store")
	}
	now := time.Now().UTC()
	ids, err := s.entClient.PaymentInvoiceRequest.Query().
		Where(paymentinvoicerequest.Or(invoiceEmailDeliveryDuePredicate(now), invoiceFeishuDeliveryDuePredicate(now))).
		Order(dbent.Asc(paymentinvoicerequest.FieldUpdatedAt), dbent.Asc(paymentinvoicerequest.FieldID)).
		Limit(invoiceDeliveryRecoveryLimit).
		IDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("list invoice notification recovery candidates: %w", err)
	}

	var (
		wg        sync.WaitGroup
		sem       = make(chan struct{}, 4)
		mu        sync.Mutex
		recovered int
		failures  []error
	)
	recordResult := func(done bool, err error) {
		mu.Lock()
		defer mu.Unlock()
		if done {
			recovered++
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
launchLoop:
	for _, invoiceID := range ids {
		if !runtimegate.SharedWorkAllowed() {
			break launchLoop
		}
		if err := ctx.Err(); err != nil {
			recordResult(false, err)
			break launchLoop
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			recordResult(false, ctx.Err())
			break launchLoop
		}
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			defer func() { <-sem }()
			emailDone, emailErr := s.deliverInvoiceEmail(ctx, id)
			feishuDone, feishuErr := s.deliverInvoiceFeishu(ctx, id)
			recordResult(emailDone || feishuDone, errors.Join(emailErr, feishuErr))
		}(invoiceID)
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	return recovered, errors.Join(failures...)
}

func invoiceEmailDeliveryDuePredicate(now time.Time) predicate.PaymentInvoiceRequest {
	stale := now.Add(-invoiceDeliveryClaimTTL)
	return paymentinvoicerequest.And(
		paymentinvoicerequest.StatusIn(InvoiceStatusIssued, InvoiceStatusRejected),
		paymentinvoicerequest.Or(
			paymentinvoicerequest.And(
				paymentinvoicerequest.EmailDeliveryStatusIn(InvoiceEmailDeliveryPending, InvoiceEmailDeliveryFailed),
				paymentinvoicerequest.Or(
					paymentinvoicerequest.EmailDeliveryNextAttemptAtIsNil(),
					paymentinvoicerequest.EmailDeliveryNextAttemptAtLTE(now),
				),
			),
			paymentinvoicerequest.And(
				paymentinvoicerequest.EmailDeliveryStatusEQ(InvoiceEmailDeliverySending),
				paymentinvoicerequest.Or(
					paymentinvoicerequest.EmailDeliveryClaimedAtIsNil(),
					paymentinvoicerequest.EmailDeliveryClaimedAtLTE(stale),
				),
			),
		),
	)
}

func invoiceFeishuDeliveryDuePredicate(now time.Time) predicate.PaymentInvoiceRequest {
	stale := now.Add(-invoiceDeliveryClaimTTL)
	return paymentinvoicerequest.And(
		paymentinvoicerequest.Or(
			paymentinvoicerequest.And(
				paymentinvoicerequest.FeishuNotificationStatusIn(InvoiceFeishuNotificationPending, InvoiceFeishuNotificationFailed),
				paymentinvoicerequest.Or(
					paymentinvoicerequest.FeishuNotificationNextAttemptAtIsNil(),
					paymentinvoicerequest.FeishuNotificationNextAttemptAtLTE(now),
				),
			),
			paymentinvoicerequest.And(
				paymentinvoicerequest.FeishuNotificationStatusEQ(InvoiceFeishuNotificationSending),
				paymentinvoicerequest.Or(
					paymentinvoicerequest.FeishuNotificationClaimedAtIsNil(),
					paymentinvoicerequest.FeishuNotificationClaimedAtLTE(stale),
				),
			),
		),
		// A changed revision receives its own notification state. This predicate
		// avoids resending a completed old revision during a later resubmission.
		paymentinvoicerequest.FeishuNotificationRevisionGT(0),
	)
}

func (s *PaymentService) deliverInvoiceEmail(ctx context.Context, invoiceID int64) (bool, error) {
	claim, err := s.claimInvoiceEmailDelivery(ctx, invoiceID)
	if err != nil || claim == nil {
		return false, err
	}
	var sendErr error
	if !runtimegate.SharedWorkAllowed() {
		sendErr = context.Canceled
	} else if ctx.Err() != nil {
		sendErr = ctx.Err()
	} else {
		sendErr = s.sendInvoiceTerminalEmail(ctx, claim.Invoice)
	}
	if finishErr := s.finishInvoiceEmailDelivery(ctx, claim, sendErr); finishErr != nil {
		return true, finishErr
	}
	if sendErr != nil {
		slog.Warn("invoice customer email failed", "invoice_request_id", claim.Invoice.ID, "order_id", claim.Invoice.OrderID, "revision", claim.Invoice.Revision, "error_kind", invoiceDeliveryErrorKind(sendErr))
	}
	return true, nil
}

func (s *PaymentService) claimInvoiceEmailDelivery(ctx context.Context, invoiceID int64) (*invoiceEmailDeliveryClaim, error) {
	if !runtimegate.SharedWorkAllowed() {
		return nil, nil
	}
	if s == nil || s.entClient == nil || invoiceID <= 0 {
		return nil, nil
	}
	invoice, err := s.entClient.PaymentInvoiceRequest.Get(ctx, invoiceID)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load invoice email candidate: %w", err)
	}
	now := time.Now().UTC()
	if !invoiceEmailAutoClaimable(invoice, now) {
		return nil, nil
	}
	token := uuid.NewString()
	preds := []predicate.PaymentInvoiceRequest{
		paymentinvoicerequest.IDEQ(invoice.ID),
		paymentinvoicerequest.RevisionEQ(invoice.Revision),
		paymentinvoicerequest.StatusEQ(invoice.Status),
		paymentinvoicerequest.EmailDeliveryStatusEQ(invoice.EmailDeliveryStatus),
		invoiceEmailClaimTokenPredicate(invoice),
	}
	updated, err := s.entClient.PaymentInvoiceRequest.Update().Where(preds...).
		SetEmailDeliveryStatus(InvoiceEmailDeliverySending).
		AddEmailDeliveryAttempts(1).
		SetEmailDeliveryAttemptedAt(now).
		SetEmailDeliveryClaimToken(token).
		SetEmailDeliveryClaimedAt(now).
		ClearEmailDeliveryNextAttemptAt().
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("claim invoice email delivery: %w", err)
	}
	if updated != 1 {
		return nil, nil
	}
	claimed, err := s.entClient.PaymentInvoiceRequest.Get(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("reload claimed invoice email delivery: %w", err)
	}
	return &invoiceEmailDeliveryClaim{Invoice: claimed, Token: token}, nil
}

func invoiceEmailAutoClaimable(invoice *dbent.PaymentInvoiceRequest, now time.Time) bool {
	if invoice == nil || (invoice.Status != InvoiceStatusIssued && invoice.Status != InvoiceStatusRejected) {
		return false
	}
	switch invoice.EmailDeliveryStatus {
	case InvoiceEmailDeliveryPending, InvoiceEmailDeliveryFailed:
		return invoice.EmailDeliveryNextAttemptAt == nil || !invoice.EmailDeliveryNextAttemptAt.After(now)
	case InvoiceEmailDeliverySending:
		return invoice.EmailDeliveryClaimedAt == nil || !invoice.EmailDeliveryClaimedAt.After(now.Add(-invoiceDeliveryClaimTTL))
	default:
		return false
	}
}

func (s *PaymentService) finishInvoiceEmailDelivery(ctx context.Context, claim *invoiceEmailDeliveryClaim, sendErr error) error {
	if s == nil || s.entClient == nil || claim == nil || claim.Invoice == nil || claim.Token == "" {
		return nil
	}
	invoice := claim.Invoice
	now := time.Now().UTC()
	preds := []predicate.PaymentInvoiceRequest{
		paymentinvoicerequest.IDEQ(invoice.ID),
		paymentinvoicerequest.RevisionEQ(invoice.Revision),
		paymentinvoicerequest.StatusEQ(invoice.Status),
		paymentinvoicerequest.EmailDeliveryStatusEQ(InvoiceEmailDeliverySending),
		paymentinvoicerequest.EmailDeliveryClaimTokenEQ(claim.Token),
	}
	update := s.entClient.PaymentInvoiceRequest.Update().Where(preds...).
		SetEmailDeliveryAttemptedAt(now).
		ClearEmailDeliveryClaimToken().
		ClearEmailDeliveryClaimedAt()
	if sendErr == nil {
		update = update.
			SetEmailDeliveryStatus(InvoiceEmailDeliverySent).
			SetEmailDeliveredAt(now).
			ClearEmailDeliveryErrorKind().
			ClearEmailDeliveryNextAttemptAt()
	} else {
		update = update.
			SetEmailDeliveryStatus(InvoiceEmailDeliveryFailed).
			SetEmailDeliveryErrorKind(invoiceDeliveryErrorKind(sendErr)).
			ClearEmailDeliveredAt().
			SetEmailDeliveryNextAttemptAt(invoiceDeliveryRetryAt(invoice.EmailDeliveryAttempts, now))
	}
	updated, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("record invoice email delivery result: %w", err)
	}
	if updated != 1 {
		// Another process won the fence after a stale claim; never overwrite it.
		return nil
	}
	return nil
}

func (s *PaymentService) sendInvoiceTerminalEmail(ctx context.Context, invoice *dbent.PaymentInvoiceRequest) error {
	if s == nil || s.notificationEmailService == nil {
		return errors.New("invoice email service is not configured")
	}
	if invoice == nil {
		return errors.New("missing invoice email candidate")
	}
	variables := invoiceCustomerEmailVariables(s.notificationEmailService, ctx, invoice)
	input := NotificationEmailSendInput{
		RecipientEmail: invoice.RecipientEmail,
		RecipientName:  firstNonEmpty(invoice.Title, invoice.RecipientEmail),
		UserID:         invoice.UserID,
		SourceType:     "payment_invoice_request",
		SourceID:       strconv.FormatInt(invoice.ID, 10),
		ReminderKey:    fmt.Sprintf("revision-%d", invoice.Revision),
		Variables:      variables,
	}
	switch invoice.Status {
	case InvoiceStatusIssued:
		document, err := s.entClient.PaymentInvoiceDocument.Query().
			Where(paymentinvoicedocument.InvoiceRequestIDEQ(invoice.ID)).
			Only(ctx)
		if err != nil {
			return fmt.Errorf("load invoice PDF attachment: %w", err)
		}
		input.Event = NotificationEmailEventInvoiceIssued
		input.Attachments = []EmailAttachment{{
			Filename: document.Filename, ContentType: document.ContentType, Data: document.Data,
		}}
	case InvoiceStatusRejected:
		input.Event = NotificationEmailEventInvoiceRejected
	default:
		return errors.New("invoice is not in a terminal email state")
	}
	return s.notificationEmailService.Send(ctx, input)
}

func invoiceCustomerEmailVariables(emailService *NotificationEmailService, ctx context.Context, invoice *dbent.PaymentInvoiceRequest) map[string]string {
	if invoice == nil {
		return map[string]string{}
	}
	return map[string]string{
		"order_id":           strconv.FormatInt(invoice.OrderID, 10),
		"invoice_title":      invoice.Title,
		"invoice_amount":     fmt.Sprintf("%.2f", invoice.Amount),
		"invoice_currency":   invoice.Currency,
		"invoice_item_name":  invoiceString(invoice.InvoiceItemName),
		"invoice_code":       invoiceString(invoice.InvoiceCode),
		"invoice_number":     invoiceString(invoice.InvoiceNumber),
		"invoice_filename":   invoiceString(invoice.DocumentFilename),
		"invoice_orders_url": invoiceEmailFrontendPageURL(emailService, ctx, "/orders"),
		"rejection_reason":   invoiceString(invoice.RejectionReason),
	}
}

func invoiceString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func invoiceEmailFrontendPageURL(emailService *NotificationEmailService, ctx context.Context, pagePath string) string {
	if !strings.HasPrefix(pagePath, "/") {
		pagePath = "/" + pagePath
	}
	if emailService == nil {
		return pagePath
	}
	baseURL := strings.TrimRight(emailService.baseURL(ctx), "/")
	if baseURL == "" {
		return pagePath
	}
	return baseURL + pagePath
}

func (s *PaymentService) deliverInvoiceFeishu(ctx context.Context, invoiceID int64) (bool, error) {
	claim, err := s.claimInvoiceFeishuDelivery(ctx, invoiceID)
	if err != nil || claim == nil {
		return false, err
	}
	var sendErr error
	if !runtimegate.SharedWorkAllowed() {
		sendErr = context.Canceled
	} else if ctx.Err() != nil {
		sendErr = ctx.Err()
	} else if s.invoiceFeishuSender == nil {
		sendErr = errors.New("invoice Feishu sender is not configured")
	} else {
		sendErr = s.invoiceFeishuSender.SendText(ctx, invoiceFeishuRequestMessage(claim.Invoice))
	}
	if finishErr := s.finishInvoiceFeishuDelivery(ctx, claim, sendErr); finishErr != nil {
		return true, finishErr
	}
	if sendErr != nil {
		slog.Warn("invoice Feishu request notification failed", "invoice_request_id", claim.Invoice.ID, "order_id", claim.Invoice.OrderID, "revision", claim.Invoice.Revision, "error_kind", invoiceDeliveryErrorKind(sendErr))
	}
	return true, nil
}

func (s *PaymentService) claimInvoiceFeishuDelivery(ctx context.Context, invoiceID int64) (*invoiceFeishuDeliveryClaim, error) {
	if !runtimegate.SharedWorkAllowed() {
		return nil, nil
	}
	if s == nil || s.entClient == nil || invoiceID <= 0 {
		return nil, nil
	}
	invoice, err := s.entClient.PaymentInvoiceRequest.Get(ctx, invoiceID)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load invoice Feishu candidate: %w", err)
	}
	now := time.Now().UTC()
	if !invoiceFeishuAutoClaimable(invoice, now) {
		return nil, nil
	}
	token := uuid.NewString()
	preds := []predicate.PaymentInvoiceRequest{
		paymentinvoicerequest.IDEQ(invoice.ID),
		paymentinvoicerequest.RevisionEQ(invoice.Revision),
		paymentinvoicerequest.FeishuNotificationRevisionEQ(invoice.FeishuNotificationRevision),
		paymentinvoicerequest.FeishuNotificationStatusEQ(invoice.FeishuNotificationStatus),
		invoiceFeishuClaimTokenPredicate(invoice),
	}
	updated, err := s.entClient.PaymentInvoiceRequest.Update().Where(preds...).
		SetFeishuNotificationStatus(InvoiceFeishuNotificationSending).
		AddFeishuNotificationAttempts(1).
		SetFeishuNotificationAttemptedAt(now).
		SetFeishuNotificationClaimToken(token).
		SetFeishuNotificationClaimedAt(now).
		ClearFeishuNotificationNextAttemptAt().
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("claim invoice Feishu delivery: %w", err)
	}
	if updated != 1 {
		return nil, nil
	}
	claimed, err := s.entClient.PaymentInvoiceRequest.Get(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("reload claimed invoice Feishu delivery: %w", err)
	}
	return &invoiceFeishuDeliveryClaim{Invoice: claimed, Token: token}, nil
}

func invoiceFeishuAutoClaimable(invoice *dbent.PaymentInvoiceRequest, now time.Time) bool {
	if invoice == nil || invoice.FeishuNotificationRevision != invoice.Revision {
		return false
	}
	switch invoice.FeishuNotificationStatus {
	case InvoiceFeishuNotificationPending, InvoiceFeishuNotificationFailed:
		return invoice.FeishuNotificationNextAttemptAt == nil || !invoice.FeishuNotificationNextAttemptAt.After(now)
	case InvoiceFeishuNotificationSending:
		return invoice.FeishuNotificationClaimedAt == nil || !invoice.FeishuNotificationClaimedAt.After(now.Add(-invoiceDeliveryClaimTTL))
	default:
		return false
	}
}

func (s *PaymentService) finishInvoiceFeishuDelivery(ctx context.Context, claim *invoiceFeishuDeliveryClaim, sendErr error) error {
	if s == nil || s.entClient == nil || claim == nil || claim.Invoice == nil || claim.Token == "" {
		return nil
	}
	invoice := claim.Invoice
	now := time.Now().UTC()
	preds := []predicate.PaymentInvoiceRequest{
		paymentinvoicerequest.IDEQ(invoice.ID),
		paymentinvoicerequest.RevisionEQ(invoice.Revision),
		paymentinvoicerequest.FeishuNotificationRevisionEQ(invoice.Revision),
		paymentinvoicerequest.FeishuNotificationStatusEQ(InvoiceFeishuNotificationSending),
		paymentinvoicerequest.FeishuNotificationClaimTokenEQ(claim.Token),
	}
	update := s.entClient.PaymentInvoiceRequest.Update().Where(preds...).
		SetFeishuNotificationAttemptedAt(now).
		ClearFeishuNotificationClaimToken().
		ClearFeishuNotificationClaimedAt()
	if sendErr == nil {
		update = update.
			SetFeishuNotificationStatus(InvoiceFeishuNotificationSent).
			SetFeishuNotifiedAt(now).
			ClearFeishuNotificationErrorKind().
			ClearFeishuNotificationNextAttemptAt()
	} else {
		update = update.
			SetFeishuNotificationStatus(InvoiceFeishuNotificationFailed).
			SetFeishuNotificationErrorKind(invoiceDeliveryErrorKind(sendErr)).
			ClearFeishuNotifiedAt().
			SetFeishuNotificationNextAttemptAt(invoiceDeliveryRetryAt(invoice.FeishuNotificationAttempts, now))
	}
	updated, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("record invoice Feishu delivery result: %w", err)
	}
	if updated != 1 {
		return nil
	}
	return nil
}

func invoiceFeishuRequestMessage(invoice *dbent.PaymentInvoiceRequest) string {
	if invoice == nil {
		return ""
	}
	// This operational message intentionally contains only numeric internal
	// locators and a queue URL. It must not include tax identifiers, buyer email,
	// phone, title, invoice metadata, or the official PDF.
	return strings.Join([]string{
		"【Sub2】待处理发票申请",
		"订单：" + strconv.FormatInt(invoice.OrderID, 10),
		"申请：" + strconv.FormatInt(invoice.ID, 10),
		"修订：" + strconv.Itoa(invoice.Revision),
		"处理入口：https://www.turtleligpt.com/admin/orders/invoices?invoice_status=PENDING",
	}, "\n")
}

func invoiceDeliveryRetryAt(attempt int, now time.Time) time.Time {
	if attempt < 1 {
		attempt = 1
	}
	delay := invoiceDeliveryRetryBaseDelay
	for i := 1; i < attempt && delay < invoiceDeliveryRetryMaxDelay; i++ {
		delay *= 2
	}
	if delay > invoiceDeliveryRetryMaxDelay {
		delay = invoiceDeliveryRetryMaxDelay
	}
	return now.Add(delay)
}

func invoiceDeliveryErrorKind(err error) string {
	if err == nil {
		return ""
	}
	if isNotificationEmailDeliveryError(err) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "delivery"
	}
	// Do not persist provider, SMTP, Vault, or transport error text in this
	// restricted workflow record; the generic kind is enough for an operator.
	return "delivery"
}
