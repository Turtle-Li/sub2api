// Package shopaction owns the versioned fulfillment integration contract.
package shopaction

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	Native       = "newapi.daypass.v1"
	HTTP         = "http.webhook.v1"
	Paid         = "shop.order.paid"
	Pending      = "PENDING"
	Running      = "RUNNING"
	Succeeded    = "SUCCEEDED"
	Review       = "REVIEW_REQUIRED"
	Cancelled    = "CANCELLED"
	Compensating = "COMPENSATING"
	Revoked      = "REVOKED"
)

// Integration is deployment-owned. Its immutable version is pinned at checkout.
// CredentialAlias identifies a narrowly allowed Vault field, never its value.
type Integration struct {
	ID              string `json:"id" mapstructure:"id"`
	Version         int    `json:"version" mapstructure:"version"`
	Adapter         string `json:"adapter" mapstructure:"adapter"`
	Endpoint        string `json:"endpoint" mapstructure:"endpoint"`
	ReadyEndpoint   string `json:"ready_endpoint,omitempty" mapstructure:"ready_endpoint"`
	CredentialAlias string `json:"credential_alias,omitempty" mapstructure:"credential_alias"`
	Enabled         bool   `json:"enabled" mapstructure:"enabled"`
}

type Config struct {
	Enabled      bool          `mapstructure:"enabled"`
	Integrations []Integration `mapstructure:"integrations"`
}

type Binding struct {
	BindingKey         string          `json:"binding_key"`
	Adapter            string          `json:"adapter"`
	IntegrationRef     string          `json:"integration_ref"`
	IntegrationVersion int             `json:"integration_version"`
	Params             json.RawMessage `json:"params"`
	Integration        *Integration    `json:"integration,omitempty"`
}

type Order struct {
	ID        int64     `json:"id"`
	ProductID int64     `json:"product_id"`
	AmountFen int64     `json:"amount_fen"`
	Currency  string    `json:"currency"`
	PaidAt    time.Time `json:"paid_at"`
}

type Request struct {
	Version  int             `json:"version"`
	Event    string          `json:"event"`
	ActionID string          `json:"action_id"`
	Source   string          `json:"source"`
	Order    Order           `json:"order"`
	Params   json.RawMessage `json:"params"`
}

type Snapshot struct {
	Binding Binding `json:"binding"`
	Request Request `json:"request"`
}

type Delivery struct {
	Kind    string `json:"kind"`
	Content string `json:"content,omitempty"`
}

type Result struct {
	Version   int      `json:"version"`
	ActionID  string   `json:"action_id"`
	OrderID   int64    `json:"order_id"`
	Status    string   `json:"status"`
	ReceiptID string   `json:"receipt_id"`
	Delivery  Delivery `json:"delivery"`
}

type Adapter interface {
	Ready(context.Context) error
	Execute(context.Context, Request) (Result, error)
}

type Revoker interface {
	Revoke(context.Context, Request) error
}

// Failure contains only a stable safe code, never a remote body or credential.
type Failure struct {
	Kind       string
	Retryable  bool
	RetryAfter time.Duration
}

func (e *Failure) Error() string { return e.Kind }

func Classify(err error) *Failure {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure
	}
	return &Failure{Kind: "INTEGRATION_UNAVAILABLE", Retryable: true}
}
