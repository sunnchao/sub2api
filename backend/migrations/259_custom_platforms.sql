-- 自定义渠道平台表（运行时动态平台清单）
-- 平台清单 = 内置静态列表（internal/domain/platforms.go）+ 本表动态平台。
-- 本表不做软删除：删除前由 service 校验账号/分组/路由/限额四类引用，无引用即物理删除，
-- 允许管理员删除后立即重建同 id。

CREATE TABLE IF NOT EXISTS custom_platforms (
    id                  VARCHAR(32)  PRIMARY KEY,
    display_name        VARCHAR(64)  NOT NULL,
    enabled             BOOLEAN      NOT NULL DEFAULT TRUE,
    sort_order          INTEGER      NOT NULL DEFAULT 0,
    icon_type           VARCHAR(16)  NOT NULL DEFAULT 'letter',
    icon_value          TEXT         NOT NULL DEFAULT '',
    color_preset        VARCHAR(24)  NOT NULL DEFAULT 'blue',
    litellm_provider    VARCHAR(64)  NOT NULL DEFAULT '',
    default_test_model  VARCHAR(128) NOT NULL DEFAULT '',
    supports_embeddings BOOLEAN      NOT NULL DEFAULT FALSE,
    supports_images     BOOLEAN      NOT NULL DEFAULT FALSE,
    profile_json        JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_custom_platforms_sort
    ON custom_platforms (sort_order, id);

COMMENT ON TABLE custom_platforms IS '自定义渠道平台，与内置平台叠加构成运行时平台清单';
COMMENT ON COLUMN custom_platforms.icon_type IS '图标类型：builtin | url | data_url | letter';
COMMENT ON COLUMN custom_platforms.profile_json IS 'ProviderProfile 配置：default_mode / routing / modes(基址与协议分流规则) 等';
