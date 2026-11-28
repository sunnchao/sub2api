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

func TestGroupModelPlazaVisibilityMigrationSQL(t *testing.T) {
	path := filepath.Join(".", "258_group_model_plaza_visibility.sql")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	sqlText := string(raw)

	require.Contains(t, sqlText, "CREATE TABLE IF NOT EXISTS group_model_plaza_visibility")
	require.Contains(t, sqlText, "group_id")
	require.Contains(t, sqlText, "show_in_model_plaza")
	require.NotContains(t, strings.ToUpper(sqlText), "ALTER TABLE")

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.Exec(`CREATE TABLE groups (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`)
	require.NoError(t, err)

	sqliteDDL := strings.ReplaceAll(sqlText, "BIGINT", "INTEGER")
	sqliteDDL = strings.ReplaceAll(sqliteDDL, "TIMESTAMPTZ", "DATETIME")
	sqliteDDL = strings.ReplaceAll(sqliteDDL, "NOW()", "CURRENT_TIMESTAMP")

	for run := 0; run < 2; run++ {
		_, err = db.Exec(sqliteDDL)
		require.NoError(t, err)
	}

	_, err = db.Exec(`INSERT INTO groups (id, name) VALUES (1, 'Token Plan Pro')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO group_model_plaza_visibility (group_id, show_in_model_plaza) VALUES (1, TRUE)`)
	require.NoError(t, err)

	var show bool
	err = db.QueryRow(`SELECT show_in_model_plaza FROM group_model_plaza_visibility WHERE group_id = 1`).Scan(&show)
	require.NoError(t, err)
	require.True(t, show)
}
