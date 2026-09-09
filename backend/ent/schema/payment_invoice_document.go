package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// PaymentInvoiceDocument keeps private PDF bytes outside list-facing request
// metadata. Standard order queries eager-load only the request edge, never this
// document edge, so a list cannot accidentally serialize the attachment.
type PaymentInvoiceDocument struct {
	ent.Schema
}

func (PaymentInvoiceDocument) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "payment_invoice_documents"}}
}

func (PaymentInvoiceDocument) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("invoice_request_id").Unique(),
		field.String("filename").MaxLen(255),
		field.String("content_type").MaxLen(64).Default("application/pdf"),
		field.Int64("size_bytes"),
		field.String("sha256").MaxLen(64),
		field.Bytes("data").Sensitive().SchemaType(map[string]string{dialect.Postgres: "bytea"}),
		field.Time("created_at").Immutable().Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (PaymentInvoiceDocument) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("invoice_request", PaymentInvoiceRequest.Type).Ref("document").Field("invoice_request_id").Unique().Required(),
	}
}
