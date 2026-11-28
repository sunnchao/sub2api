package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepositoryListWithFilters_SubscriptionID(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM usage_logs WHERE user_id = \\$1 AND subscription_id = \\$2").
		WithArgs(int64(7), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))

	mock.ExpectQuery("SELECT .* FROM usage_logs WHERE user_id = \\$1 AND subscription_id = \\$2 ORDER BY").
		WithArgs(int64(7), int64(42), 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	logs, page, err := repo.ListWithFilters(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 20}, UsageLogFilters{
		UserID:         7,
		SubscriptionID: 42,
		ExactTotal:     true,
	})
	require.NoError(t, err)
	require.Empty(t, logs)
	require.NotNil(t, page)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepositoryGetStatsWithFilters_SubscriptionID(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	mock.ExpectQuery("WHERE user_id = \\$1 AND subscription_id = \\$2").
		WithArgs(int64(7), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"inbound_grouped", "upstream_grouped", "inbound_endpoint", "upstream_endpoint",
			"requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
			"cost", "actual_cost", "account_cost", "avg_duration_ms",
		}))

	stats, err := repo.GetStatsWithFilters(context.Background(), UsageLogFilters{
		UserID:         7,
		SubscriptionID: 42,
	})
	require.NoError(t, err)
	require.NotNil(t, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepositoryTrendModelAndGroupStats_SubscriptionID(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)
	filters := UsageLogFilters{UserID: 7, SubscriptionID: 42}

	mock.ExpectQuery("FROM usage_logs\\s+WHERE created_at >= \\$1 AND created_at < \\$2 AND user_id = \\$3 AND subscription_id = \\$4").
		WithArgs(start, end, int64(7), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"date", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost",
		}))
	_, err := repo.GetUsageTrendWithUsageFilters(context.Background(), start, end, "day", filters)
	require.NoError(t, err)

	mock.ExpectQuery("FROM usage_logs\\s+WHERE created_at >= \\$1 AND created_at < \\$2 AND user_id = \\$3 AND subscription_id = \\$4").
		WithArgs(start, end, int64(7), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"model", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost", "account_cost",
		}))
	_, err = repo.GetModelStatsWithUsageFiltersBySource(context.Background(), start, end, filters, usagestats.ModelSourceRequested)
	require.NoError(t, err)

	mock.ExpectQuery("FROM usage_logs ul\\s+LEFT JOIN groups g ON g.id = ul.group_id\\s+WHERE ul.created_at >= \\$1 AND ul.created_at < \\$2 AND ul.user_id = \\$3 AND ul.subscription_id = \\$4").
		WithArgs(start, end, int64(7), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"group_id", "group_name", "requests", "total_tokens", "cost", "actual_cost", "account_cost",
		}))
	_, err = repo.GetGroupStatsWithUsageFilters(context.Background(), start, end, filters)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepositoryHydrateSubscriptionTemplateNames(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	subs := map[int64]*service.UserSubscription{
		11: {ID: 11},
		12: {ID: 12},
	}

	mock.ExpectQuery("SELECT subscription_id, template_id, template_name FROM user_subscription_snapshots WHERE subscription_id IN \\(\\$1, \\$2\\)").
		WithArgs(int64(11), int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"subscription_id", "template_id", "template_name"}).
			AddRow(int64(11), int64(5), "Token Plan Pro"))

	repo.hydrateSubscriptionTemplateNames(context.Background(), []int64{11, 12}, subs)
	require.Equal(t, "Token Plan Pro", subs[11].TemplateName)
	require.NotNil(t, subs[11].TemplateID)
	require.Equal(t, int64(5), *subs[11].TemplateID)
	require.Empty(t, subs[12].TemplateName)
	require.NoError(t, mock.ExpectationsWereMet())

	// Graceful fallback if table is unavailable.
	mock.ExpectQuery("SELECT subscription_id, template_id, template_name FROM user_subscription_snapshots").
		WithArgs(int64(12)).
		WillReturnError(errors.New("no such table"))
	repo.hydrateSubscriptionTemplateNames(context.Background(), []int64{12}, subs)
	require.Empty(t, subs[12].TemplateName)
	require.NoError(t, mock.ExpectationsWereMet())
}
