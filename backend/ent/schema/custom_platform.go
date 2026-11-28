package schema

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// CustomPlatform 定义运行时自定义渠道平台的 schema。
//
// 平台清单由内置静态列表（internal/domain/platforms.go）与本表叠加而成；
// id 即平台标识（不可修改），与 user_platform_quotas.platform 的 MaxLen(32) 保持一致。
// 本表不做软删除：删除前由 service 校验引用，无引用即物理删除。
type CustomPlatform struct {
	ent.Schema
}

// Annotations 返回 schema 的注解配置。
func (CustomPlatform) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "custom_platforms"},
	}
}

// Mixin 返回该 schema 使用的混入组件。
func (CustomPlatform) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

// Fields 定义自定义平台实体的所有字段。
func (CustomPlatform) Fields() []ent.Field {
	return []ent.Field{
		// id: 平台标识，创建后不可修改（作为主键，故无需额外唯一索引）
		field.String("id").
			MaxLen(32).
			NotEmpty().
			Immutable(),

		// display_name: 平台展示名
		field.String("display_name").
			MaxLen(64).
			NotEmpty(),

		// enabled: 禁用后隐藏新建入口，但不中断已有账号/分组/路由与历史统计
		field.Bool("enabled").
			Default(true),

		// sort_order: 平台列表排序，越小越靠前
		field.Int("sort_order").
			Default(0),

		// icon_type: 图标类型，仅允许 builtin / url / data_url / letter
		field.String("icon_type").
			MaxLen(16).
			Default("letter").
			Validate(func(s string) error {
				switch s {
				case "builtin", "url", "data_url", "letter":
					return nil
				default:
					return fmt.Errorf("icon_type %q is not supported", s)
				}
			}),

		// icon_value: 图标值（内置图标名 / 外链 URL / data URL / 首字母）
		field.Text("icon_value").
			Default(""),

		// color_preset: 亮/暗主题色预设名
		field.String("color_preset").
			MaxLen(24).
			Default("blue"),

		// litellm_provider: 映射到 LiteLLM 的 provider 名，空串表示无映射
		field.String("litellm_provider").
			MaxLen(64).
			Default(""),

		// default_test_model: 渠道测试默认使用的模型
		field.String("default_test_model").
			MaxLen(128).
			Default(""),

		// supports_embeddings / supports_images: 是否开放 OpenAI 兼容扩展能力
		field.Bool("supports_embeddings").
			Default(false),
		field.Bool("supports_images").
			Default(false),

		// profile_json: ProviderProfile 配置（默认模式、路由策略、模式端点、协议分流规则等）
		field.JSON("profile_json", map[string]any{}).
			Default(map[string]any{}).
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
	}
}

// Indexes 返回该 schema 的索引配置。
func (CustomPlatform) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("sort_order", "id").
			StorageKey("idx_custom_platforms_sort"),
	}
}
