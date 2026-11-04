package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ShopFulfillment never owns payment facts or entitlements. Secrets are bound
// to the order, purpose and revision inside authenticated encryption envelopes.
type ShopFulfillment struct{ ent.Schema }

func (ShopFulfillment) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "shop_fulfillments"}}
}

func (ShopFulfillment) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("order_id").Unique().Immutable(),
		field.Int64("user_id").Immutable(),
		field.Int64("product_id").Immutable(),
		field.JSON("product_snapshot", map[string]any{}).Immutable(),
		field.String("status").MaxLen(24).Default("WAITING_PAYMENT"),
		field.Int("revision").Positive().Default(1),
		field.Int("input_revision").NonNegative().Default(0),
		field.Text("input_ciphertext").Optional().Sensitive(),
		field.String("input_account").MaxLen(320).Default(""),
		field.Time("input_submitted_at").Optional().Nillable(),
		field.Text("delivery_ciphertext").Optional().Sensitive(),
		field.String("status_note").MaxLen(2000).Default(""),
		field.Int64("processed_by").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (ShopFulfillment) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "created_at"), index.Fields("status", "created_at")}
}
