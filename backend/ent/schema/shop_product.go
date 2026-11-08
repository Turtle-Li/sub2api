package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ShopProduct contains owner-managed catalog data. Prices are integer CNY fen.
type ShopProduct struct{ ent.Schema }

func (ShopProduct) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "shop_products"}}
}

func (ShopProduct) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").MaxLen(160),
		field.String("category").MaxLen(80).Default(""),
		field.String("summary").MaxLen(600).Default(""),
		field.Text("description").Default(""),
		field.Int64("price_fen").NonNegative().Default(0),
		field.Int("duration_value").Positive().Default(1),
		field.String("duration_unit").MaxLen(10).Default("month"),
		field.String("fulfillment_mode").MaxLen(24),
		field.String("input_format").MaxLen(16).Default("session_json"),
		field.String("input_url").MaxLen(2048).Default(""),
		field.Text("input_instructions").Default(""),
		field.String("guide_key").MaxLen(64).Default(""),
		field.JSON("payment_actions", json.RawMessage{}).Optional(),
		field.JSON("sku_config", json.RawMessage{}).Optional(),
		field.Bool("enabled").Default(false),
		field.Int("sort_order").Default(0),
		field.Int("revision").Positive().Default(1),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (ShopProduct) Indexes() []ent.Index {
	return []ent.Index{index.Fields("enabled", "sort_order")}
}
