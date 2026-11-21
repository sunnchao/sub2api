package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewAPIQuotaMigrationSQL(t *testing.T) {
	content, err := FS.ReadFile("253_new_api_quota_migration.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS new_api_migration_records")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS uidx_new_api_migration_from_once")
	require.Contains(t, sql, "WHERE direction = 'from_new_api' AND status IN ('pending', 'completed', 'needs_manual_review')")
}
