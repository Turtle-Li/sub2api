package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AccountPool 定义账号池实体的 schema。
//
// 账号池只用于后台折叠管理批量账号（例如批量导入的 free 账号），
// 不参与调度与计费；调度仍然只看账号自身绑定的分组。
// 一个账号最多属于一个池（accounts.pool_id）。
type AccountPool struct {
	ent.Schema
}

func (AccountPool) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "account_pools"},
	}
}

func (AccountPool) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (AccountPool) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			MaxLen(100).
			NotEmpty(),
		// platform: 池内账号必须属于同一平台
		field.String("platform").
			MaxLen(50).
			NotEmpty(),
		field.String("notes").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
	}
}

func (AccountPool) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("platform"),
	}
}
