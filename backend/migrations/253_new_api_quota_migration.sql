-- New API 额度迁移审计与防重迁记录表
-- 预留 direction 字段支持未来双向迁移（from_new_api / to_new_api），当前仅开放 from_new_api 一次性全额迁出。

CREATE TABLE IF NOT EXISTS new_api_migration_records (
    id                      BIGSERIAL PRIMARY KEY,
    direction               VARCHAR(32) NOT NULL,
    status                  VARCHAR(32) NOT NULL,
    site_url                VARCHAR(512) NOT NULL,
    sub2api_user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    sub2api_user_email      VARCHAR(255) NOT NULL DEFAULT '',
    new_api_user_id         BIGINT NOT NULL,
    new_api_username        VARCHAR(128) NOT NULL DEFAULT '',
    new_api_display_name    VARCHAR(128) NOT NULL DEFAULT '',
    new_api_email           VARCHAR(255) NOT NULL DEFAULT '',
    new_api_quota_before    BIGINT NOT NULL DEFAULT 0,
    new_api_quota_delta     BIGINT NOT NULL DEFAULT 0,
    new_api_quota_after     BIGINT NOT NULL DEFAULT 0,
    new_api_gift_quota      BIGINT NOT NULL DEFAULT 0,
    quota_per_unit          DOUBLE PRECISION NOT NULL DEFAULT 500000,
    exchange_rate           DOUBLE PRECISION NOT NULL DEFAULT 1,
    sub2api_balance_before  NUMERIC(20, 8) NOT NULL DEFAULT 0,
    sub2api_balance_delta   NUMERIC(20, 8) NOT NULL DEFAULT 0,
    sub2api_balance_after   NUMERIC(20, 8) NOT NULL DEFAULT 0,
    redeem_code_id          BIGINT NULL,
    error_message           TEXT NOT NULL DEFAULT '',
    client_ip               VARCHAR(64) NOT NULL DEFAULT '',
    completed_at            TIMESTAMPTZ NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 强制每个 New API 站点账号仅可从 New API 迁出一次：
-- 覆盖 pending / completed / needs_manual_review，确保并发请求在扣款前即被唯一索引拦下，
-- 失败(failed)或已自动回滚补偿(compensated)后释放唯一锁，允许重试。
CREATE UNIQUE INDEX IF NOT EXISTS uidx_new_api_migration_from_once
    ON new_api_migration_records (site_url, new_api_user_id)
    WHERE direction = 'from_new_api' AND status IN ('pending', 'completed', 'needs_manual_review');

CREATE INDEX IF NOT EXISTS idx_new_api_migration_user_created
    ON new_api_migration_records (sub2api_user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_new_api_migration_created
    ON new_api_migration_records (created_at DESC);

CREATE INDEX IF NOT EXISTS idx_new_api_migration_status
    ON new_api_migration_records (status, direction, created_at DESC);

COMMENT ON TABLE new_api_migration_records IS 'New API 额度迁移记录与防重复迁移锁表（支持双向扩展）';
COMMENT ON COLUMN new_api_migration_records.direction IS '迁移方向：from_new_api（从 New API 迁入）| to_new_api（迁出至 New API，预留）';
COMMENT ON COLUMN new_api_migration_records.status IS '状态：pending | completed | failed | compensated | needs_manual_review';
