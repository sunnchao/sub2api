package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestGroupModelPlazaVisibility_SQLiteRoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.Exec(`CREATE TABLE group_model_plaza_visibility (
		group_id INTEGER PRIMARY KEY,
		show_in_model_plaza BOOLEAN NOT NULL DEFAULT FALSE,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	require.NoError(t, err)

	ctx := context.Background()

	// Enable for group 10 and verify idempotent upsert.
	require.NoError(t, saveGroupModelPlazaVisibility(ctx, db, 10, true))
	require.NoError(t, saveGroupModelPlazaVisibility(ctx, db, 10, true))

	groups := []service.Group{
		{ID: 10, Name: "sub-pro", IsExclusive: true},
		{ID: 20, Name: "sub-hidden", IsExclusive: true},
	}
	hydrateGroupModelPlazaVisibility(ctx, db, groups)
	require.True(t, groups[0].ShowInModelPlaza)
	require.False(t, groups[1].ShowInModelPlaza)

	single := &service.Group{ID: 10, IsExclusive: true}
	hydrateSingleGroupModelPlazaVisibility(ctx, db, single)
	require.True(t, single.ShowInModelPlaza)

	// Disable for group 10 and verify row is removed and flag hydrates to false.
	require.NoError(t, saveGroupModelPlazaVisibility(ctx, db, 10, false))
	after := &service.Group{ID: 10, IsExclusive: true}
	hydrateSingleGroupModelPlazaVisibility(ctx, db, after)
	require.False(t, after.ShowInModelPlaza)
}

func TestGroupModelPlazaVisibility_MissingTableDegradesGracefully(t *testing.T) {
	db, mock := newSQLMock(t)
	ctx := context.Background()

	// Turning off on an unmigrated schema ignores missing table error.
	mock.ExpectExec("DELETE FROM group_model_plaza_visibility WHERE group_id = \\$1").
		WithArgs(int64(9)).
		WillReturnError(errors.New(`no such table: group_model_plaza_visibility`))
	require.NoError(t, saveGroupModelPlazaVisibility(ctx, db, 9, false))

	// Real DB errors are still propagated.
	mock.ExpectExec("DELETE FROM group_model_plaza_visibility WHERE group_id = \\$1").
		WithArgs(int64(9)).
		WillReturnError(errors.New("connection refused"))
	require.Error(t, saveGroupModelPlazaVisibility(ctx, db, 9, false))

	// Hydration degrades gracefully when table is missing.
	mock.ExpectQuery("SELECT group_id, show_in_model_plaza FROM group_model_plaza_visibility WHERE group_id IN \\(\\$1\\)").
		WithArgs(int64(9)).
		WillReturnError(errors.New(`relation "group_model_plaza_visibility" does not exist`))
	groups := []service.Group{{ID: 9, IsExclusive: true}}
	hydrateGroupModelPlazaVisibility(ctx, db, groups)
	require.False(t, groups[0].ShowInModelPlaza)

	require.NoError(t, mock.ExpectationsWereMet())
}
