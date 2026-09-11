package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// PaymentInvoiceRequest is the durable manual-invoice workflow for exactly
// one payment order. It stores delivery state but never changes payment or
// refund truth; those remain owned by PaymentOrder and the refund ledger.
type PaymentInvoiceRequest struct {
	ent.Schema
}

func (PaymentInvoiceRequest) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "payment_invoice_requests"}}
}

func (PaymentInvoiceRequest) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("order_id").Unique(),
		field.Int64("user_id"),

		field.String("title_type").MaxLen(20),
		field.String("title").MaxLen(200),
		field.String("tax_identifier").Optional().Nillable().MaxLen(64).Sensitive(),
		field.String("recipient_email").MaxLen(255).Sensitive(),
		field.String("recipient_phone").Optional().Nillable().MaxLen(32).Sensitive(),
		field.String("remark").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),

		field.Float("amount").SchemaType(map[string]string{dialect.Postgres: "decimal(20,2)"}),
		field.String("currency").MaxLen(12),
		field.String("status").MaxLen(20).Default("PENDING"),
		field.Int("revision").Default(1),

		field.String("provider").MaxLen(50).Default("manual"),
		field.String("provider_invoice_id").Optional().Nillable().MaxLen(128),
		field.String("invoice_item_name").Optional().Nillable().MaxLen(200),
		field.String("invoice_code").Optional().Nillable().MaxLen(64),
		field.String("invoice_number").Optional().Nillable().MaxLen(64),
		field.String("document_filename").Optional().Nillable().MaxLen(255),
		field.Int64("document_size_bytes").Optional().Nillable(),
		field.String("document_sha256").Optional().Nillable().MaxLen(64),

		// Customer delivery is claimed using a durable fencing token. PENDING is
		// recoverable after a process exit before claim; SENDING is recoverable
		// after an expired claim. FAILED remains visible and is manually retryable.
		field.String("email_delivery_status").MaxLen(20).Default("NOT_SENT"),
		field.Int("email_delivery_attempts").Default(0),
		field.String("email_delivery_error_kind").Optional().Nillable().MaxLen(32),
		field.Time("email_delivery_attempted_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("email_delivered_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("email_delivery_claim_token").Optional().Nillable().MaxLen(36),
		field.Time("email_delivery_claimed_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("email_delivery_next_attempt_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),

		// A Feishu message is a separate, privacy-minimal operational signal for
		// the request/revision. It is not an incident classification and cannot
		// affect invoice state. The revision field prevents duplicate notices for
		// repeated delivery attempts while allowing a corrected resubmission.
		field.String("feishu_notification_status").MaxLen(20).Default("NOT_SENT"),
		field.Int("feishu_notification_revision").Default(0),
		field.Int("feishu_notification_attempts").Default(0),
		field.String("feishu_notification_error_kind").Optional().Nillable().MaxLen(32),
		field.Time("feishu_notification_attempted_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("feishu_notified_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("feishu_notification_claim_token").Optional().Nillable().MaxLen(36),
		field.Time("feishu_notification_claimed_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("feishu_notification_next_attempt_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),

		field.String("rejection_reason").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Int64("processed_by").Optional().Nillable(),

		field.Time("requested_at").Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("processed_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("issued_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("created_at").Immutable().Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (PaymentInvoiceRequest) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("order", PaymentOrder.Type).Ref("invoice_request").Field("order_id").Unique().Required(),
		edge.To("document", PaymentInvoiceDocument.Type).Unique(),
	}
}

func (PaymentInvoiceRequest) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id"),
		index.Fields("status", "requested_at"),
		index.Fields("email_delivery_status", "email_delivery_next_attempt_at"),
		index.Fields("feishu_notification_status", "feishu_notification_next_attempt_at"),
	}
}
