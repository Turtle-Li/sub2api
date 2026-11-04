package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// ShopOrderAction is the transactional outbox for one primary delivery/order.
type ShopOrderAction struct{ ent.Schema }

func (ShopOrderAction) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "shop_order_actions"}}
}

func (ShopOrderAction) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("order_id").Immutable(),
		field.String("action_key").MaxLen(48).Immutable(),
		field.Text("snapshot").Immutable(),
		field.String("snapshot_hash").MaxLen(64).Immutable(),
		field.Time("deadline_at").Immutable(),
		field.String("status").MaxLen(24).Default("PENDING"),
		field.Int("attempts").NonNegative().Default(0),
		field.Int("claim_revision").NonNegative().Default(0),
		field.Time("lease_until").Optional().Nillable(),
		field.Time("next_attempt_at").Default(time.Now),
		field.String("error_kind").MaxLen(48).Default(""),
		field.String("compensation_reason").MaxLen(32).Default(""),
		field.String("receipt_ref").MaxLen(160).Default(""),
		field.String("result_kind").MaxLen(16).Default("none"),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (ShopOrderAction) Indexes() []ent.Index {
	return []ent.Index{index.Fields("order_id").Unique(), index.Fields("action_key").Unique(), index.Fields("status", "next_attempt_at"), index.Fields("status", "lease_until")}
}
