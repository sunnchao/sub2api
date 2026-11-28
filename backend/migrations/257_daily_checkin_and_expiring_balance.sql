-- 每日签到与限时余额旁路表迁移
-- 设计目标：严格遵循零侵入旁路表模式，不向存量 users / payment_orders / usage_logs / batch_image_jobs 表追加列、
-- 不触发 ent 代码重新生成，最大化降低后续同步上游仓库时的合并冲突。

-- 1. 用户签到记录表
CREATE TABLE IF NOT EXISTS user_checkin_records (
    id                     BIGSERIAL PRIMARY KEY,
    user_id                BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    checkin_date           DATE NOT NULL,
    streak_days            INT NOT NULL DEFAULT 1,
    milestone_cycle_day    INT NOT NULL DEFAULT 1,
    base_amount            DECIMAL(20,8) NOT NULL DEFAULT 0,
    milestone_bonus_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
    total_amount           DECIMAL(20,8) NOT NULL DEFAULT 0,
    validity_days          INT NOT NULL DEFAULT 1,
    expires_at             TIMESTAMPTZ NOT NULL,
    client_ip              VARCHAR(64) NOT NULL DEFAULT '',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_checkin_records_user_date
    ON user_checkin_records (user_id, checkin_date);

CREATE INDEX IF NOT EXISTS idx_user_checkin_records_date_ip
    ON user_checkin_records (checkin_date, client_ip)
    WHERE client_ip <> '';

CREATE INDEX IF NOT EXISTS idx_user_checkin_records_date_created
    ON user_checkin_records (checkin_date DESC, id DESC);

COMMENT ON TABLE user_checkin_records IS '用户每日签到记录与奖励明细旁路表';

-- 2. 全站每日签到预算熔断与汇总表
CREATE TABLE IF NOT EXISTS checkin_daily_budgets (
    checkin_date  DATE PRIMARY KEY,
    total_granted DECIMAL(20,8) NOT NULL DEFAULT 0,
    checkin_count INT NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE checkin_daily_budgets IS '全站每日签到预算原子熔断与汇总统计旁路表';

-- 3. 用户限时余额批次表
CREATE TABLE IF NOT EXISTS user_expiring_balance_lots (
    id               BIGSERIAL PRIMARY KEY,
    user_id          BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_type      VARCHAR(32) NOT NULL DEFAULT 'checkin',
    source_id        BIGINT NOT NULL,
    initial_amount   DECIMAL(20,8) NOT NULL,
    remaining_amount DECIMAL(20,8) NOT NULL,
    frozen_amount    DECIMAL(20,8) NOT NULL DEFAULT 0,
    consumed_amount  DECIMAL(20,8) NOT NULL DEFAULT 0,
    expired_amount   DECIMAL(20,8) NOT NULL DEFAULT 0,
    expires_at       TIMESTAMPTZ NOT NULL,
    status           VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_expiring_lot_amounts CHECK (
        initial_amount > 0
        AND remaining_amount >= 0
        AND frozen_amount >= 0
        AND consumed_amount >= 0
        AND expired_amount >= 0
    ),
    CONSTRAINT chk_expiring_lot_status CHECK (
        status IN ('active', 'exhausted', 'expired')
    )
);

CREATE INDEX IF NOT EXISTS idx_expiring_lots_user_fefo
    ON user_expiring_balance_lots (user_id, expires_at ASC, id ASC)
    WHERE status = 'active' AND remaining_amount > 0;

CREATE INDEX IF NOT EXISTS idx_expiring_lots_expire_scan
    ON user_expiring_balance_lots (expires_at ASC)
    WHERE status = 'active';

CREATE UNIQUE INDEX IF NOT EXISTS idx_expiring_lots_source
    ON user_expiring_balance_lots (source_type, source_id);

CREATE INDEX IF NOT EXISTS idx_expiring_lots_user_created
    ON user_expiring_balance_lots (user_id, created_at DESC, id DESC);

COMMENT ON TABLE user_expiring_balance_lots IS '用户限时余额批次表（支持 FEFO 扣费、预扣冻结与过期归档）';

-- 4. 限时余额与普通余额分摊明细表
CREATE TABLE IF NOT EXISTS user_expiring_balance_allocations (
    id                       BIGSERIAL PRIMARY KEY,
    user_id                  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ref_type                 VARCHAR(32) NOT NULL,
    ref_key                  VARCHAR(128) NOT NULL,
    alloc_seq                INT NOT NULL,
    lot_id                   BIGINT REFERENCES user_expiring_balance_lots(id) ON DELETE RESTRICT,
    allocated_amount         DECIMAL(20,8) NOT NULL,
    captured_amount          DECIMAL(20,8) NOT NULL DEFAULT 0,
    released_amount          DECIMAL(20,8) NOT NULL DEFAULT 0,
    forfeited_expired_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_expiring_alloc_ref_seq
    ON user_expiring_balance_allocations (ref_type, ref_key, alloc_seq);

CREATE INDEX IF NOT EXISTS idx_expiring_alloc_user_created
    ON user_expiring_balance_allocations (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_expiring_alloc_lot_id
    ON user_expiring_balance_allocations (lot_id)
    WHERE lot_id IS NOT NULL;

COMMENT ON TABLE user_expiring_balance_allocations IS '消费/预扣/购订阅的限时批次与普通余额分摊追踪旁路表';
