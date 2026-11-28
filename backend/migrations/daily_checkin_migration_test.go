package migrations

import (
	"database/sql"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

const migration257File = "257_daily_checkin_and_expiring_balance.sql"

func TestMigration257EmbeddedAndValidSQLStructure(t *testing.T) {
	raw, err := fs.ReadFile(FS, migration257File)
	if err != nil {
		t.Fatalf("read embedded migration %s: %v", migration257File, err)
	}
	content := string(raw)
	if strings.TrimSpace(content) == "" {
		t.Fatalf("migration %s is empty", migration257File)
	}

	upper := strings.ToUpper(content)
	if strings.Contains(upper, "CONCURRENTLY") {
		t.Fatalf("migration %s must not contain CONCURRENTLY (transactional migration)", migration257File)
	}

	// Ensure no existing core tables are altered.
	if strings.Contains(upper, "ALTER TABLE") {
		t.Fatalf("migration %s must not ALTER existing tables", migration257File)
	}

	requiredObjects := []string{
		"CREATE TABLE IF NOT EXISTS user_checkin_records",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_user_checkin_records_user_date",
		"CREATE INDEX IF NOT EXISTS idx_user_checkin_records_date_ip",
		"CREATE INDEX IF NOT EXISTS idx_user_checkin_records_date_created",
		"CREATE TABLE IF NOT EXISTS checkin_daily_budgets",
		"CREATE TABLE IF NOT EXISTS user_expiring_balance_lots",
		"chk_expiring_lot_amounts",
		"status IN ('active', 'exhausted', 'expired')",
		"CREATE INDEX IF NOT EXISTS idx_expiring_lots_user_fefo",
		"CREATE INDEX IF NOT EXISTS idx_expiring_lots_expire_scan",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_expiring_lots_source",
		"CREATE INDEX IF NOT EXISTS idx_expiring_lots_user_created",
		"CREATE TABLE IF NOT EXISTS user_expiring_balance_allocations",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_expiring_alloc_ref_seq",
		"CREATE INDEX IF NOT EXISTS idx_expiring_alloc_user_created",
		"CREATE INDEX IF NOT EXISTS idx_expiring_alloc_lot_id",
	}
	for _, needle := range requiredObjects {
		if !strings.Contains(content, needle) {
			t.Fatalf("migration %s missing expected DDL fragment %q", migration257File, needle)
		}
	}

	// Every CREATE TABLE / CREATE [UNIQUE] INDEX must include IF NOT EXISTS for idempotency.
	createStmtRe := regexp.MustCompile(`(?im)^\s*CREATE\s+(?:UNIQUE\s+)?(?:TABLE|INDEX)\b.*`)
	for _, match := range createStmtRe.FindAllString(content, -1) {
		if !strings.Contains(strings.ToUpper(match), "IF NOT EXISTS") {
			t.Fatalf("DDL statement missing IF NOT EXISTS: %s", match)
		}
	}
}

func TestMigration257SQLiteIdempotentDDLAndConstraints(t *testing.T) {
	raw, err := fs.ReadFile(FS, migration257File)
	if err != nil {
		t.Fatalf("read embedded migration %s: %v", migration257File, err)
	}

	sqliteDDL := adaptPostgresDDLForSQLite(string(raw))

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite memory db: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`PRAGMA foreign_keys = ON; CREATE TABLE users (id INTEGER PRIMARY KEY); INSERT INTO users (id) VALUES (1);`); err != nil {
		t.Fatalf("seed users table: %v", err)
	}

	// Apply twice to verify idempotency.
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := db.Exec(sqliteDDL); err != nil {
			t.Fatalf("execute migration DDL attempt %d: %v\nSQL:\n%s", attempt, err, sqliteDDL)
		}
	}

	// Verify all 4 tables exist.
	for _, table := range []string{
		"user_checkin_records",
		"checkin_daily_budgets",
		"user_expiring_balance_lots",
		"user_expiring_balance_allocations",
	} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("expected table %s to exist: %v", table, err)
		}
	}

	// Verify all 9 indexes exist.
	for _, idx := range []string{
		"idx_user_checkin_records_user_date",
		"idx_user_checkin_records_date_ip",
		"idx_user_checkin_records_date_created",
		"idx_expiring_lots_user_fefo",
		"idx_expiring_lots_expire_scan",
		"idx_expiring_lots_source",
		"idx_expiring_lots_user_created",
		"idx_expiring_alloc_ref_seq",
		"idx_expiring_alloc_user_created",
		"idx_expiring_alloc_lot_id",
	} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name = ?`, idx).Scan(&name)
		if err != nil {
			t.Fatalf("expected index %s to exist: %v", idx, err)
		}
	}

	// Verify CHECK constraints on user_expiring_balance_lots.
	if _, err := db.Exec(`
		INSERT INTO user_expiring_balance_lots (user_id, source_type, source_id, initial_amount, remaining_amount, expires_at, status)
		VALUES (1, 'checkin', 1, 0, 0, '2026-11-28T00:00:00Z', 'active')
	`); err == nil {
		t.Fatalf("expected initial_amount = 0 to violate chk_expiring_lot_amounts")
	}

	if _, err := db.Exec(`
		INSERT INTO user_expiring_balance_lots (user_id, source_type, source_id, initial_amount, remaining_amount, expires_at, status)
		VALUES (1, 'checkin', 2, 1.5, -0.1, '2026-11-28T00:00:00Z', 'active')
	`); err == nil {
		t.Fatalf("expected remaining_amount < 0 to violate chk_expiring_lot_amounts")
	}

	if _, err := db.Exec(`
		INSERT INTO user_expiring_balance_lots (user_id, source_type, source_id, initial_amount, remaining_amount, expires_at, status)
		VALUES (1, 'checkin', 3, 1.5, 1.5, '2026-11-28T00:00:00Z', 'invalid_status')
	`); err == nil {
		t.Fatalf("expected invalid status to violate chk_expiring_lot_status")
	}

	// Verify unique constraints.
	if _, err := db.Exec(`
		INSERT INTO user_checkin_records (user_id, checkin_date, expires_at)
		VALUES (1, '2026-11-27', '2026-11-28T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert valid checkin record: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO user_checkin_records (user_id, checkin_date, expires_at)
		VALUES (1, '2026-11-27', '2026-11-28T00:00:00Z')
	`); err == nil {
		t.Fatalf("expected duplicate (user_id, checkin_date) to violate unique constraint")
	}

	if _, err := db.Exec(`
		INSERT INTO user_expiring_balance_lots (user_id, source_type, source_id, initial_amount, remaining_amount, expires_at, status)
		VALUES (1, 'checkin', 10, 2.5, 2.5, '2026-11-28T00:00:00Z', 'active')
	`); err != nil {
		t.Fatalf("insert valid lot: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO user_expiring_balance_lots (user_id, source_type, source_id, initial_amount, remaining_amount, expires_at, status)
		VALUES (1, 'checkin', 10, 1.0, 1.0, '2026-11-28T00:00:00Z', 'active')
	`); err == nil {
		t.Fatalf("expected duplicate (source_type, source_id) to violate unique constraint")
	}

	if _, err := db.Exec(`
		INSERT INTO user_expiring_balance_allocations (user_id, ref_type, ref_key, alloc_seq, lot_id, allocated_amount)
		VALUES (1, 'usage_billing', 'req-1', 1, 1, 1.0)
	`); err != nil {
		t.Fatalf("insert valid allocation: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO user_expiring_balance_allocations (user_id, ref_type, ref_key, alloc_seq, lot_id, allocated_amount)
		VALUES (1, 'usage_billing', 'req-1', 1, 1, 1.0)
	`); err == nil {
		t.Fatalf("expected duplicate (ref_type, ref_key, alloc_seq) to violate unique constraint")
	}
}

func adaptPostgresDDLForSQLite(pgSQL string) string {
	commentRe := regexp.MustCompile(`(?im)^\s*COMMENT\s+ON\s+TABLE\b[^;]*;\s*$`)
	out := commentRe.ReplaceAllString(pgSQL, "")
	out = strings.ReplaceAll(out, "BIGSERIAL PRIMARY KEY", "INTEGER PRIMARY KEY AUTOINCREMENT")
	out = strings.ReplaceAll(out, "TIMESTAMPTZ", "DATETIME")
	out = strings.ReplaceAll(out, "NOW()", "CURRENT_TIMESTAMP")
	return out
}
