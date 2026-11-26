-- 独立「订阅模板」及同分组多订阅 FEFO 并行扣费旁路表迁移
-- 设计目标：不向存量主表追加列、不触发 ent 代码重新生成，最大化降低与上游仓库的合并冲突。

-- 1. 解除 user_subscriptions 的 (user_id, group_id) 唯一约束，支持同用户同分组（含同模板）并行持有多条订阅
DROP INDEX IF EXISTS user_subscriptions_user_group_unique_active;

-- 为 FEFO（先到期先扣）查询建立部分复合索引
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_user_group_fefo
    ON user_subscriptions (user_id, group_id, status, expires_at ASC, created_at ASC, id ASC)
    WHERE deleted_at IS NULL;

-- 2. 独立订阅模板表（承载周期限额、小时窗口、默认有效期并绑定单个目标分组，支持 standard 与 subscription 分组）
CREATE TABLE IF NOT EXISTS subscription_templates (
    id                    BIGSERIAL PRIMARY KEY,
    name                  VARCHAR(100) NOT NULL,
    description           TEXT NOT NULL DEFAULT '',
    group_id              BIGINT NOT NULL REFERENCES groups(id),
    daily_limit_usd       DECIMAL(20,8),
    weekly_limit_usd      DECIMAL(20,8),
    monthly_limit_usd     DECIMAL(20,8),
    hourly_limit_usd      DECIMAL(20,8),
    hourly_window_hours   INT NOT NULL DEFAULT 5,
    default_validity_days INT NOT NULL DEFAULT 30,
    status                VARCHAR(20) NOT NULL DEFAULT 'active',
    sort_order            INT NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at            TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_templates_name_active
    ON subscription_templates (name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_subscription_templates_group_id
    ON subscription_templates (group_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_subscription_templates_status_sort
    ON subscription_templates (status, sort_order ASC, id ASC)
    WHERE deleted_at IS NULL;

COMMENT ON TABLE subscription_templates IS '独立订阅模板：定义周期限额与默认有效期，并绑定底层账号分组（支持普通分组与订阅分组）';

-- 3. 用户订阅限额快照旁路表（1:1 对应 user_subscriptions）
-- 存在记录代表该订阅已锁定独立快照（NULL 列表示显式不限，不回落分组限额）；
-- 无记录代表旧版直绑订阅分组，运行时自动回落读取 groups 上的默认限额。
CREATE TABLE IF NOT EXISTS user_subscription_snapshots (
    subscription_id     BIGINT PRIMARY KEY REFERENCES user_subscriptions(id) ON DELETE CASCADE,
    template_id         BIGINT REFERENCES subscription_templates(id) ON DELETE SET NULL,
    template_name       VARCHAR(100) NOT NULL DEFAULT '',
    daily_limit_usd     DECIMAL(20,8),
    weekly_limit_usd    DECIMAL(20,8),
    monthly_limit_usd   DECIMAL(20,8),
    hourly_limit_usd    DECIMAL(20,8),
    hourly_window_hours INT NOT NULL DEFAULT 5,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_subscription_snapshots_template_id
    ON user_subscription_snapshots (template_id);

COMMENT ON TABLE user_subscription_snapshots IS '用户订阅的限额快照与模板来源旁路表（1:1）';

-- 4. 支付套餐 -> 订阅模板 旁路表（1:1 对应 subscription_plans）
CREATE TABLE IF NOT EXISTS subscription_plan_templates (
    plan_id     BIGINT PRIMARY KEY REFERENCES subscription_plans(id) ON DELETE CASCADE,
    template_id BIGINT NOT NULL REFERENCES subscription_templates(id)
);

CREATE INDEX IF NOT EXISTS idx_subscription_plan_templates_template_id
    ON subscription_plan_templates (template_id);

COMMENT ON TABLE subscription_plan_templates IS '支付订阅套餐关联的订阅模板旁路表（1:1）';

-- 5. 支付订单 -> 订阅模板 / 指定续期目标 / 实际履约订阅 ID 旁路表（1:1 对应 payment_orders）
CREATE TABLE IF NOT EXISTS payment_order_subscriptions (
    order_id                  BIGINT PRIMARY KEY REFERENCES payment_orders(id) ON DELETE CASCADE,
    template_id               BIGINT REFERENCES subscription_templates(id) ON DELETE SET NULL,
    target_subscription_id    BIGINT REFERENCES user_subscriptions(id) ON DELETE SET NULL,
    fulfilled_subscription_id BIGINT REFERENCES user_subscriptions(id) ON DELETE SET NULL,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_payment_order_subscriptions_template_id
    ON payment_order_subscriptions (template_id);

COMMENT ON TABLE payment_order_subscriptions IS '订阅支付订单的模板来源、续期目标及精准履约/退款追踪旁路表（1:1）';

-- 6. 订阅兑换码 -> 订阅模板与兑换模式 旁路表（1:1 对应 redeem_codes）
CREATE TABLE IF NOT EXISTS redeem_code_templates (
    redeem_code_id BIGINT PRIMARY KEY REFERENCES redeem_codes(id) ON DELETE CASCADE,
    template_id    BIGINT NOT NULL REFERENCES subscription_templates(id),
    redeem_mode    VARCHAR(20) NOT NULL DEFAULT 'stack'
);

CREATE INDEX IF NOT EXISTS idx_redeem_code_templates_template_id
    ON redeem_code_templates (template_id);

COMMENT ON TABLE redeem_code_templates IS '订阅兑换码关联模板及兑换模式（stack 新购叠加 / extend 续期同模板）旁路表（1:1）';
