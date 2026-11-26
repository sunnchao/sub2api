package migrations

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestSubscriptionTemplatesMigrationSQL(t *testing.T) {
	path := filepath.Join(".", "256_subscription_templates.sql")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	sqlText := string(raw)

	// M1: Drops legacy unique active (user_id, group_id) index and adds FEFO index.
	require.Contains(t, sqlText, "DROP INDEX IF EXISTS user_subscriptions_user_group_unique_active;")
	require.Contains(t, sqlText, "CREATE INDEX IF NOT EXISTS idx_user_subscriptions_user_group_fefo")
	require.Contains(t, sqlText, "user_id, group_id, status, expires_at ASC, created_at ASC, id ASC")

	// M2: Creates all 5 sidecar tables idempotently (no ALTER TABLE on upstream tables).
	for _, table := range []string{
		"CREATE TABLE IF NOT EXISTS subscription_templates",
		"CREATE TABLE IF NOT EXISTS user_subscription_snapshots",
		"CREATE TABLE IF NOT EXISTS subscription_plan_templates",
		"CREATE TABLE IF NOT EXISTS payment_order_subscriptions",
		"CREATE TABLE IF NOT EXISTS redeem_code_templates",
	} {
		require.Contains(t, sqlText, table)
	}
	require.NotContains(t, strings.ToUpper(sqlText), "ALTER TABLE")

	// M3 & M4: Execute SQLite-compatible translation of DDL and verify multiple active
	// user_subscriptions rows for the same (user_id, group_id) succeed idempotently.
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	bootstrap := []string{
		`CREATE TABLE groups (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`,
		`CREATE TABLE user_subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			group_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at DATETIME
		)`,
		`CREATE UNIQUE INDEX user_subscriptions_user_group_unique_active ON user_subscriptions (user_id, group_id) WHERE deleted_at IS NULL`,
		`CREATE TABLE subscription_plans (id INTEGER PRIMARY KEY AUTOINCREMENT, group_id INTEGER NOT NULL)`,
		`CREATE TABLE payment_orders (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL)`,
		`CREATE TABLE redeem_codes (id INTEGER PRIMARY KEY AUTOINCREMENT, code TEXT NOT NULL)`,
	}
	for _, stmt := range bootstrap {
		_, err := db.Exec(stmt)
		require.NoError(t, err)
	}

	sqliteDDL := strings.ReplaceAll(sqlText, "BIGSERIAL PRIMARY KEY", "INTEGER PRIMARY KEY AUTOINCREMENT")
	sqliteDDL = strings.ReplaceAll(sqliteDDL, "BIGINT", "INTEGER")
	sqliteDDL = strings.ReplaceAll(sqliteDDL, "TIMESTAMPTZ", "DATETIME")
	sqliteDDL = strings.ReplaceAll(sqliteDDL, "NOW()", "CURRENT_TIMESTAMP")
	sqliteDDL = strings.ReplaceAll(sqliteDDL, "DECIMAL(20, 8)", "REAL")
	sqliteDDL = strings.ReplaceAll(sqliteDDL, "DECIMAL(20,8)", "REAL")

	// Execute twice to verify idempotency.
	for run := 0; run < 2; run++ {
		for _, part := range strings.Split(sqliteDDL, ";") {
			// Strip leading comment lines inside each statement block.
			lines := strings.Split(part, "\n")
			filtered := make([]string, 0, len(lines))
			for _, line := range lines {
				trimmedLine := strings.TrimSpace(line)
				if trimmedLine == "" || strings.HasPrefix(trimmedLine, "--") {
					continue
				}
				filtered = append(filtered, line)
			}
			stmt := strings.TrimSpace(strings.Join(filtered, "\n"))
			if stmt == "" || strings.HasPrefix(strings.ToUpper(stmt), "COMMENT ON ") {
				continue
			}
			_, err := db.Exec(stmt)
			require.NoError(t, err, "run=%d stmt=%s", run, stmt)
		}
	}

	// Inserting two active rows for the same (user_id, group_id) must now succeed.
	_, err = db.Exec(`INSERT INTO user_subscriptions (user_id, group_id, status, expires_at) VALUES (1, 10, 'active', '2027-01-01')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO user_subscriptions (user_id, group_id, status, expires_at) VALUES (1, 10, 'active', '2027-02-01')`)
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM user_subscriptions WHERE user_id = 1 AND group_id = 10`).Scan(&count))
	require.Equal(t, 2, count)
}
