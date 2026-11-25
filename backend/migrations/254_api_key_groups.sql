-- API Key 多分组有序绑定表（支持跨模型厂商多分组按勾选/调整顺序依次调度与失败重试）

CREATE TABLE IF NOT EXISTS api_key_groups (
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    group_id   BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    priority   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (api_key_id, group_id)
);

CREATE INDEX IF NOT EXISTS idx_api_key_groups_api_key_priority
    ON api_key_groups (api_key_id, priority ASC, group_id ASC);

CREATE INDEX IF NOT EXISTS idx_api_key_groups_group_id
    ON api_key_groups (group_id);

INSERT INTO api_key_groups (api_key_id, group_id, priority)
SELECT id, group_id, 0
FROM api_keys
WHERE group_id IS NOT NULL AND deleted_at IS NULL
ON CONFLICT (api_key_id, group_id) DO NOTHING;

COMMENT ON TABLE api_key_groups IS 'API Key 多分组有序绑定关联表，priority 越小越优先调度';
COMMENT ON COLUMN api_key_groups.priority IS '分组尝试优先级（从 0 开始递增，越小越优先）';
