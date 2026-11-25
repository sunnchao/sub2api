package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyGroupsMigrationSQL(t *testing.T) {
	content, err := FS.ReadFile("254_api_key_groups.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS api_key_groups")
	require.Contains(t, sql, "PRIMARY KEY (api_key_id, group_id)")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_api_key_groups_api_key_priority")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_api_key_groups_group_id")
	require.Contains(t, sql, "INSERT INTO api_key_groups (api_key_id, group_id, priority)")
	require.Contains(t, sql, "ON CONFLICT (api_key_id, group_id) DO NOTHING")
}
