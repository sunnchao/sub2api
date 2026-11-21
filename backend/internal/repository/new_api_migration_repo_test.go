package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestNewAPIMigrationRepository_CreatePendingDuplicate(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewNewAPIMigrationRepository(db)
	mock.ExpectQuery(`INSERT INTO new_api_migration_records`).
		WillReturnError(&pq.Error{Code: "23505"})

	err = repo.CreatePending(context.Background(), &service.NewAPIMigrationRecord{
		Direction:     service.NewAPIMigrationDirectionFrom,
		Status:        service.NewAPIMigrationStatusPending,
		SiteURL:       "https://newapi.example.com",
		Sub2APIUserID: 10,
		NewAPIUserID:  42,
	})
	require.ErrorIs(t, err, service.ErrNewAPIMigrationDuplicate)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNewAPIMigrationRepository_CreateAndLifecycle(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewNewAPIMigrationRepository(db)
	now := time.Now().UTC()

	mock.ExpectQuery(`INSERT INTO new_api_migration_records`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(99), now, now))

	rec := &service.NewAPIMigrationRecord{
		Direction:            service.NewAPIMigrationDirectionFrom,
		Status:               service.NewAPIMigrationStatusPending,
		SiteURL:              "https://newapi.example.com",
		Sub2APIUserID:        10,
		Sub2APIUserEmail:     "alice@sub2api.test",
		NewAPIUserID:         42,
		NewAPIUsername:       "alice",
		NewAPIQuotaBefore:    5000000,
		NewAPIQuotaDelta:     5000000,
		NewAPIQuotaAfter:     0,
		QuotaPerUnit:         500000,
		ExchangeRate:         1.0,
		Sub2APIBalanceBefore: 2.0,
		Sub2APIBalanceDelta:  10.0,
		Sub2APIBalanceAfter:  12.0,
	}
	require.NoError(t, repo.CreatePending(context.Background(), rec))
	require.Equal(t, int64(99), rec.ID)

	redeemID := int64(777)
	mock.ExpectExec(`UPDATE new_api_migration_records`).
		WithArgs(service.NewAPIMigrationStatusCompleted, 2.0, 10.0, 12.0, sql.NullInt64{Int64: 777, Valid: true}, now, int64(99)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkCompleted(context.Background(), 99, 2.0, 10.0, 12.0, &redeemID, now))

	mock.ExpectExec(`UPDATE new_api_migration_records`).
		WithArgs(service.NewAPIMigrationStatusCompensated, "rolled back", int64(99)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkCompensated(context.Background(), 99, "rolled back"))

	mock.ExpectQuery(`SELECT\s+COUNT\(\*\) AS total_count`).
		WillReturnRows(sqlmock.NewRows([]string{
			"total_count", "completed_count", "pending_count", "failed_count",
			"compensated_count", "needs_manual_review_count", "total_migrated_balance", "total_migrated_quota",
		}).AddRow(int64(5), int64(3), int64(0), int64(1), int64(1), int64(0), 25.5, int64(12750000)))

	stats, err := repo.GetStats(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(5), stats.TotalCount)
	require.Equal(t, int64(3), stats.CompletedCount)
	require.Equal(t, 25.5, stats.TotalMigratedBalance)
	require.Equal(t, int64(12750000), stats.TotalMigratedQuota)

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM new_api_migration_records WHERE status = \$1 AND direction = \$2`).
		WithArgs("completed", "from_new_api").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectQuery(`SELECT\s+id, direction, status`).
		WithArgs("completed", "from_new_api", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "direction", "status", "site_url",
			"sub2api_user_id", "sub2api_user_email",
			"new_api_user_id", "new_api_username", "new_api_display_name", "new_api_email",
			"new_api_quota_before", "new_api_quota_delta", "new_api_quota_after", "new_api_gift_quota",
			"quota_per_unit", "exchange_rate",
			"sub2api_balance_before", "sub2api_balance_delta", "sub2api_balance_after",
			"redeem_code_id", "error_message", "client_ip", "completed_at", "created_at", "updated_at",
		}))

	items, pageRes, err := repo.ListAdmin(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 20}, service.NewAPIMigrationRecordFilters{
		Status:    "completed",
		Direction: "from_new_api",
	})
	require.NoError(t, err)
	require.Empty(t, items)
	require.Equal(t, int64(0), pageRes.Total)

	require.NoError(t, mock.ExpectationsWereMet())
}
