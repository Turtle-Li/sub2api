package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// ProxyPool defines an operator-managed pool used for automatic account import assignment.
// Existing account bindings stay on accounts.proxy_id; pool membership only affects future picks.
type ProxyPool struct {
	ent.Schema
}

func (ProxyPool) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "proxy_pools"},
	}
}

func (ProxyPool) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (ProxyPool) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").MaxLen(100).NotEmpty(),
		field.String("notes").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
	}
}

func (ProxyPool) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("proxies", Proxy.Type).Ref("pool"),
	}
}
