package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ShopNotification is a durable outbox, containing references and no secrets.
type ShopNotification struct{ ent.Schema }

func (ShopNotification) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "shop_notifications"}}
}

func (ShopNotification) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("order_id").Immutable(),
		field.String("event").MaxLen(32).Immutable(),
		field.Int("revision").Immutable(),
		field.String("channel").MaxLen(16).Immutable(),
		field.String("status").MaxLen(16).Default("PENDING"),
		field.Int("attempts").NonNegative().Default(0),
		field.String("claim_token").MaxLen(36).Default(""),
		field.Time("claimed_at").Optional().Nillable(),
		field.Time("next_attempt_at").Default(time.Now),
		field.String("error_kind").MaxLen(48).Default(""),
		field.Time("sent_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (ShopNotification) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("order_id", "event", "revision", "channel").Unique(),
		index.Fields("status", "next_attempt_at"),
	}
}
