DELETE FROM api_key_groups AS binding
USING groups AS bound_group
WHERE binding.group_id = bound_group.id AND bound_group.deleted_at IS NOT NULL;

WITH ordered AS (
    SELECT api_key_id, group_id,
           (ROW_NUMBER() OVER (PARTITION BY api_key_id ORDER BY priority, group_id) - 1)::INT AS priority
    FROM api_key_groups
)
UPDATE api_key_groups AS binding
SET priority = ordered.priority
FROM ordered
WHERE binding.api_key_id = ordered.api_key_id AND binding.group_id = ordered.group_id
  AND binding.priority <> ordered.priority;

UPDATE api_keys AS api_key
SET group_id = binding.group_id, updated_at = NOW()
FROM api_key_groups AS binding
WHERE api_key.id = binding.api_key_id AND binding.priority = 0 AND api_key.deleted_at IS NULL
  AND api_key.group_id IS DISTINCT FROM binding.group_id;
