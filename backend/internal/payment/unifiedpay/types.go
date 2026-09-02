// Package unifiedpay implements the product-side pay-v1 contract used by
// Sub2 to call the standalone unified payment service.
//
// Contract provenance: the types and signature rules mirror
// tottools-pay/sdk/go/payclient. The copy is intentionally local until that
// SDK is published as a versioned module. Production configuration contains
// only a Vault reference; a colocated memory-only agent supplies the key
// directly to this process at startup.
package unifiedpay

import (
	"crypto/ed25519"
	"errors"
	"net/http"
	"time"
)

type Environment string

const (
	EnvironmentSandbox Environment = "sandbox"
	EnvironmentLive    Environment = "live"

	PaymentMethodAlipay = "alipay"

	StatusCreated             = "CREATED"
	StatusPendingPayment      = "PENDING_PAYMENT"
	StatusConfirmationPending = "PAYMENT_CONFIRMATION_PENDING"
	StatusPaid                = "PAID"
	StatusPaidAfterClose      = "PAID_AFTER_CLOSE"
	StatusClosed              = "CLOSED"
	StatusExpired             = "EXPIRED"
	StatusPartiallyRefunded   = "PARTIALLY_REFUNDED"
	StatusRefunded            = "REFUNDED"

	EventPaymentPaid                = "payment.order.paid"
	EventPaymentPaidAfterClose      = "payment.order.paid_after_close"
	EventPaymentConfirmationPending = "payment.order.confirmation_pending"
	EventPaymentClosed              = "payment.order.closed"
	EventPaymentExpired             = "payment.order.expired"
	EventRefundSucceeded            = "payment.refund.succeeded"
	EventRefundFailed               = "payment.refund.failed"

	RefundStatusSucceeded = "SUCCEEDED"
	RefundStatusFailed    = "FAILED"
)

const (
	HeaderAppID          = "X-Pay-App-Id"
	HeaderKeyID          = "X-Pay-Key-Id"
	HeaderTimestamp      = "X-Pay-Timestamp"
	HeaderNonce          = "X-Pay-Nonce"
	HeaderAudience       = "X-Pay-Audience"
	HeaderSignature      = "X-Pay-Signature"
	HeaderIdempotencyKey = "Idempotency-Key"

	HeaderWebhookKeyID     = "X-Pay-Webhook-Key-Id"
	HeaderWebhookTimestamp = "X-Pay-Webhook-Timestamp"
	HeaderWebhookEventID   = "X-Pay-Webhook-Event-Id"
	HeaderWebhookSignature = "X-Pay-Webhook-Signature"

	AudienceSandbox = "totools-pay-sandbox"
	AudienceLive    = "totools-pay-live"
)

const MaximumClockSkew = 300 * time.Second

var (
	ErrDisabled             = errors.New("unified payment is disabled")
	ErrInvalidConfiguration = errors.New("invalid unified payment configuration")
	ErrInvalidRequest       = errors.New("invalid unified payment request")
	// ErrCreateStateUnconfirmed means the create request may have committed at
	// the payment service but Sub2 could not prove the resulting order state.
	// Callers must keep the local order pending for signed-Webhook recovery.
	ErrCreateStateUnconfirmed  = errors.New("unified payment create state is unconfirmed")
	ErrRequestFailed           = errors.New("unified payment request failed")
	ErrInvalidResponse         = errors.New("invalid unified payment response")
	ErrResponseTooLarge        = errors.New("unified payment response too large")
	ErrMissingWebhookHeader    = errors.New("missing unified payment webhook header")
	ErrDuplicateWebhookHeader  = errors.New("duplicate unified payment webhook header")
	ErrInvalidWebhookHeader    = errors.New("invalid unified payment webhook header")
	ErrInvalidWebhookSignature = errors.New("invalid unified payment webhook signature")
	ErrWebhookTimestampWindow  = errors.New("unified payment webhook timestamp outside allowed window")
	ErrWebhookScopeMismatch    = errors.New("unified payment webhook scope mismatch")
	ErrWebhookEventIDMismatch  = errors.New("unified payment webhook event id mismatch")
	ErrInvalidWebhookEvent     = errors.New("invalid unified payment webhook event")
	ErrInvalidJSON             = errors.New("invalid JSON")
	ErrDuplicateJSONKey        = errors.New("duplicate JSON key")
	ErrTrailingJSON            = errors.New("trailing JSON data")
)

type Config struct {
	Enabled           bool
	BaseURL           string
	Environment       Environment
	OrganizationID    string
	ProductID         string
	AppID             string
	RequestKeyID      string
	RequestPrivateKey ed25519.PrivateKey
	WebhookPublicKeys map[string]ed25519.PublicKey
	ReturnURL         string
	HTTPClient        *http.Client
	Clock             func() time.Time
	NonceSource       func() (string, error)
}

type createPaymentOrderRequest struct {
	ProductOrderNo   string            `json:"product_order_no"`
	OrderType        string            `json:"order_type"`
	AmountFen        int64             `json:"amount_fen"`
	Currency         string            `json:"currency"`
	Subject          string            `json:"subject"`
	PaymentMethod    string            `json:"payment_method"`
	ExpiresInSeconds int               `json:"expires_in_seconds"`
	ReturnURL        *string           `json:"return_url,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type paymentOrderResponse struct {
	Environment             Environment `json:"environment"`
	OrganizationID          string      `json:"organization_id"`
	ProductID               string      `json:"product_id"`
	AppID                   string      `json:"app_id"`
	PaymentOrderID          string      `json:"payment_order_id"`
	ProductOrderNo          string      `json:"product_order_no"`
	OrderType               string      `json:"order_type"`
	AmountFen               int64       `json:"amount_fen"`
	PaidAmountFen           int64       `json:"paid_amount_fen"`
	RefundedAmountFen       int64       `json:"refunded_amount_fen"`
	ReservedRefundAmountFen int64       `json:"reserved_refund_amount_fen"`
	RefundableAmountFen     int64       `json:"refundable_amount_fen"`
	Currency                string      `json:"currency"`
	PaymentMethod           string      `json:"payment_method"`
	Status                  string      `json:"status"`
	CheckoutURL             *string     `json:"checkout_url"`
	CheckoutExpiresAt       *time.Time  `json:"checkout_expires_at"`
	ChannelOutTradeNo       *string     `json:"channel_out_trade_no"`
	ChannelTransactionID    *string     `json:"channel_transaction_id"`
	PaidAt                  *time.Time  `json:"paid_at"`
	ClosedAt                *time.Time  `json:"closed_at"`
	NeedsManualReview       bool        `json:"needs_manual_review"`
	CreatedAt               time.Time   `json:"created_at"`
	UpdatedAt               time.Time   `json:"updated_at"`
	ExpiresAt               time.Time   `json:"expires_at"`
}

type closePaymentOrderRequest struct {
	ReasonCode string `json:"reason_code"`
}

type WebhookEvent struct {
	SchemaVersion   string                 `json:"schema_version"`
	EventID         string                 `json:"event_id"`
	EventType       string                 `json:"event_type"`
	OccurredAt      time.Time              `json:"occurred_at"`
	Environment     Environment            `json:"environment"`
	OrganizationID  string                 `json:"organization_id"`
	ProductID       string                 `json:"product_id"`
	ProductCode     string                 `json:"product_code"`
	AppID           string                 `json:"app_id"`
	Sequence        int64                  `json:"sequence"`
	OriginRequestID string                 `json:"origin_request_id"`
	Resource        PaymentOrderResource   `json:"resource"`
	Refund          *WebhookRefundResource `json:"refund,omitempty"`
}

type PaymentOrderResource struct {
	PaymentOrderID          string     `json:"payment_order_id"`
	ProductOrderNo          string     `json:"product_order_no"`
	OrderType               string     `json:"order_type"`
	AmountFen               int64      `json:"amount_fen"`
	PaidAmountFen           int64      `json:"paid_amount_fen"`
	RefundedAmountFen       int64      `json:"refunded_amount_fen"`
	ReservedRefundAmountFen int64      `json:"reserved_refund_amount_fen"`
	RefundableAmountFen     int64      `json:"refundable_amount_fen"`
	Currency                string     `json:"currency"`
	PaymentMethod           string     `json:"payment_method"`
	Status                  string     `json:"status"`
	ChannelOutTradeNo       string     `json:"channel_out_trade_no"`
	ChannelTransactionID    *string    `json:"channel_transaction_id"`
	PaidAt                  *time.Time `json:"paid_at"`
	ClosedAt                *time.Time `json:"closed_at"`
}

type WebhookRefundResource struct {
	RefundRequestID    string     `json:"refund_request_id"`
	ProductRefundNo    string     `json:"product_refund_no"`
	ChannelOutRefundNo string     `json:"channel_out_refund_no"`
	AmountFen          int64      `json:"amount_fen"`
	PaymentMethod      string     `json:"payment_method"`
	Status             string     `json:"status"`
	ProviderRefundID   *string    `json:"provider_refund_id"`
	ProviderStatus     *string    `json:"provider_status"`
	FailureCode        *string    `json:"failure_code"`
	CompletedAt        *time.Time `json:"completed_at"`
}

type VerifiedWebhook struct {
	KeyID     string
	Timestamp time.Time
	EventID   string
	Sequence  int64
	BodyHash  string
	Event     WebhookEvent
}

// APIError intentionally keeps only safe, bounded metadata from an upstream
// error response and never retains the raw response body.
type APIError struct {
	StatusCode int
	Code       string
	Retryable  bool
}

func (e *APIError) Error() string {
	if e != nil && e.Code != "" {
		return "unified payment request failed: " + e.Code
	}
	return ErrRequestFailed.Error()
}
