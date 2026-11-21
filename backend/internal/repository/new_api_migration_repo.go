package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type newAPIMigrationRepository struct {
	db *sql.DB
}

// NewNewAPIMigrationRepository 创建 New API 额度迁移记录仓储。
func NewNewAPIMigrationRepository(db *sql.DB) service.NewAPIMigrationRepository {
	return &newAPIMigrationRepository{db: db}
}

const newAPIMigrationSelectColumns = `
	id, direction, status, site_url,
	sub2api_user_id, sub2api_user_email,
	new_api_user_id, new_api_username, new_api_display_name, new_api_email,
	new_api_quota_before, new_api_quota_delta, new_api_quota_after, new_api_gift_quota,
	quota_per_unit, exchange_rate,
	sub2api_balance_before, sub2api_balance_delta, sub2api_balance_after,
	redeem_code_id, error_message, client_ip, completed_at, created_at, updated_at
`

type newAPIMigrationRowScanner interface {
	Scan(dest ...any) error
}

func scanNewAPIMigrationRecord(s newAPIMigrationRowScanner) (service.NewAPIMigrationRecord, error) {
	var (
		rec          service.NewAPIMigrationRecord
		redeemCodeID sql.NullInt64
		completedAt  sql.NullTime
	)
	err := s.Scan(
		&rec.ID,
		&rec.Direction,
		&rec.Status,
		&rec.SiteURL,
		&rec.Sub2APIUserID,
		&rec.Sub2APIUserEmail,
		&rec.NewAPIUserID,
		&rec.NewAPIUsername,
		&rec.NewAPIDisplayName,
		&rec.NewAPIEmail,
		&rec.NewAPIQuotaBefore,
		&rec.NewAPIQuotaDelta,
		&rec.NewAPIQuotaAfter,
		&rec.NewAPIGiftQuota,
		&rec.QuotaPerUnit,
		&rec.ExchangeRate,
		&rec.Sub2APIBalanceBefore,
		&rec.Sub2APIBalanceDelta,
		&rec.Sub2APIBalanceAfter,
		&redeemCodeID,
		&rec.ErrorMessage,
		&rec.ClientIP,
		&completedAt,
		&rec.CreatedAt,
		&rec.UpdatedAt,
	)
	if err != nil {
		return rec, err
	}
	if redeemCodeID.Valid {
		v := redeemCodeID.Int64
		rec.RedeemCodeID = &v
	}
	if completedAt.Valid {
		t := completedAt.Time
		rec.CompletedAt = &t
	}
	return rec, nil
}

func (r *newAPIMigrationRepository) CreatePending(ctx context.Context, record *service.NewAPIMigrationRecord) error {
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = now
	}

	const query = `
		INSERT INTO new_api_migration_records (
			direction, status, site_url,
			sub2api_user_id, sub2api_user_email,
			new_api_user_id, new_api_username, new_api_display_name, new_api_email,
			new_api_quota_before, new_api_quota_delta, new_api_quota_after, new_api_gift_quota,
			quota_per_unit, exchange_rate,
			sub2api_balance_before, sub2api_balance_delta, sub2api_balance_after,
			error_message, client_ip, created_at, updated_at
		) VALUES (
			$1, $2, $3,
			$4, $5,
			$6, $7, $8, $9,
			$10, $11, $12, $13,
			$14, $15,
			$16, $17, $18,
			$19, $20, $21, $22
		)
		RETURNING id, created_at, updated_at
	`
	err := r.db.QueryRowContext(
		ctx, query,
		record.Direction,
		record.Status,
		record.SiteURL,
		record.Sub2APIUserID,
		record.Sub2APIUserEmail,
		record.NewAPIUserID,
		record.NewAPIUsername,
		record.NewAPIDisplayName,
		record.NewAPIEmail,
		record.NewAPIQuotaBefore,
		record.NewAPIQuotaDelta,
		record.NewAPIQuotaAfter,
		record.NewAPIGiftQuota,
		record.QuotaPerUnit,
		record.ExchangeRate,
		record.Sub2APIBalanceBefore,
		record.Sub2APIBalanceDelta,
		record.Sub2APIBalanceAfter,
		record.ErrorMessage,
		record.ClientIP,
		record.CreatedAt,
		record.UpdatedAt,
	).Scan(&record.ID, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return service.ErrNewAPIMigrationDuplicate
		}
		return fmt.Errorf("create pending new-api migration record: %w", err)
	}
	return nil
}

func (r *newAPIMigrationRepository) FindBlockingFromMigration(ctx context.Context, siteURL string, newAPIUserID int64) (*service.NewAPIMigrationRecord, error) {
	const query = `
		SELECT ` + newAPIMigrationSelectColumns + `
		FROM new_api_migration_records
		WHERE site_url = $1
		  AND new_api_user_id = $2
		  AND direction = $3
		  AND status IN ('pending', 'completed', 'needs_manual_review')
		ORDER BY id DESC
		LIMIT 1
	`
	rec, err := scanNewAPIMigrationRecord(r.db.QueryRowContext(
		ctx, query, siteURL, newAPIUserID, service.NewAPIMigrationDirectionFrom,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find blocking new-api migration: %w", err)
	}
	return &rec, nil
}

func (r *newAPIMigrationRepository) MarkCompleted(
	ctx context.Context,
	id int64,
	balanceBefore, balanceDelta, balanceAfter float64,
	redeemCodeID *int64,
	completedAt time.Time,
) error {
	var rcID sql.NullInt64
	if redeemCodeID != nil && *redeemCodeID > 0 {
		rcID = sql.NullInt64{Int64: *redeemCodeID, Valid: true}
	}
	const query = `
		UPDATE new_api_migration_records
		SET status = $1,
		    sub2api_balance_before = $2,
		    sub2api_balance_delta = $3,
		    sub2api_balance_after = $4,
		    redeem_code_id = $5,
		    error_message = '',
		    completed_at = $6,
		    updated_at = NOW()
		WHERE id = $7
	`
	_, err := r.db.ExecContext(
		ctx, query,
		service.NewAPIMigrationStatusCompleted,
		balanceBefore,
		balanceDelta,
		balanceAfter,
		rcID,
		completedAt.UTC(),
		id,
	)
	if err != nil {
		return fmt.Errorf("mark new-api migration completed: %w", err)
	}
	return nil
}

func (r *newAPIMigrationRepository) markTerminalStatus(ctx context.Context, id int64, status, errMsg string) error {
	const query = `
		UPDATE new_api_migration_records
		SET status = $1,
		    error_message = $2,
		    updated_at = NOW()
		WHERE id = $3
	`
	_, err := r.db.ExecContext(ctx, query, status, errMsg, id)
	if err != nil {
		return fmt.Errorf("update new-api migration status to %s: %w", status, err)
	}
	return nil
}

func (r *newAPIMigrationRepository) MarkFailed(ctx context.Context, id int64, errMsg string) error {
	return r.markTerminalStatus(ctx, id, service.NewAPIMigrationStatusFailed, errMsg)
}

func (r *newAPIMigrationRepository) MarkCompensated(ctx context.Context, id int64, errMsg string) error {
	return r.markTerminalStatus(ctx, id, service.NewAPIMigrationStatusCompensated, errMsg)
}

func (r *newAPIMigrationRepository) MarkNeedsManualReview(ctx context.Context, id int64, errMsg string) error {
	return r.markTerminalStatus(ctx, id, service.NewAPIMigrationStatusNeedsManualReview, errMsg)
}

func (r *newAPIMigrationRepository) ListByUser(
	ctx context.Context,
	userID int64,
	params pagination.PaginationParams,
) ([]service.NewAPIMigrationRecord, *pagination.PaginationResult, error) {
	var total int64
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM new_api_migration_records WHERE sub2api_user_id = $1`,
		userID,
	).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("count user new-api migration records: %w", err)
	}

	const query = `
		SELECT ` + newAPIMigrationSelectColumns + `
		FROM new_api_migration_records
		WHERE sub2api_user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.db.QueryContext(ctx, query, userID, params.Limit(), params.Offset())
	if err != nil {
		return nil, nil, fmt.Errorf("list user new-api migration records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.NewAPIMigrationRecord, 0)
	for rows.Next() {
		rec, scanErr := scanNewAPIMigrationRecord(rows)
		if scanErr != nil {
			return nil, nil, fmt.Errorf("scan user new-api migration record: %w", scanErr)
		}
		items = append(items, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate user new-api migration records: %w", err)
	}
	return items, paginationResultFromTotal(total, params), nil
}

func (r *newAPIMigrationRepository) ListAdmin(
	ctx context.Context,
	params pagination.PaginationParams,
	filters service.NewAPIMigrationRecordFilters,
) ([]service.NewAPIMigrationRecord, *pagination.PaginationResult, error) {
	conditions := make([]string, 0, 3)
	args := make([]any, 0, 5)
	nextArg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}

	if status := strings.TrimSpace(filters.Status); status != "" {
		conditions = append(conditions, "status = "+nextArg(status))
	}
	if direction := strings.TrimSpace(filters.Direction); direction != "" {
		conditions = append(conditions, "direction = "+nextArg(direction))
	}
	if search := strings.TrimSpace(filters.Search); search != "" {
		likePattern := "%" + search + "%"
		p := nextArg(likePattern)
		searchCond := fmt.Sprintf("(sub2api_user_email ILIKE %s OR new_api_username ILIKE %s OR new_api_display_name ILIKE %s OR new_api_email ILIKE %s", p, p, p, p)
		if numID, err := strconv.ParseInt(search, 10, 64); err == nil && numID > 0 {
			numP := nextArg(numID)
			searchCond += fmt.Sprintf(" OR sub2api_user_id = %s OR new_api_user_id = %s", numP, numP)
		}
		searchCond += ")"
		conditions = append(conditions, searchCond)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	var total int64
	countQuery := "SELECT COUNT(*) FROM new_api_migration_records " + whereClause
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("count admin new-api migration records: %w", err)
	}

	limitPlaceholder := nextArg(params.Limit())
	offsetPlaceholder := nextArg(params.Offset())
	listQuery := fmt.Sprintf(
		"SELECT %s FROM new_api_migration_records %s ORDER BY created_at DESC, id DESC LIMIT %s OFFSET %s",
		newAPIMigrationSelectColumns,
		whereClause,
		limitPlaceholder,
		offsetPlaceholder,
	)

	rows, err := r.db.QueryContext(ctx, listQuery, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list admin new-api migration records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.NewAPIMigrationRecord, 0)
	for rows.Next() {
		rec, scanErr := scanNewAPIMigrationRecord(rows)
		if scanErr != nil {
			return nil, nil, fmt.Errorf("scan admin new-api migration record: %w", scanErr)
		}
		items = append(items, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate admin new-api migration records: %w", err)
	}
	return items, paginationResultFromTotal(total, params), nil
}

func (r *newAPIMigrationRepository) GetStats(ctx context.Context) (*service.NewAPIMigrationStats, error) {
	const query = `
		SELECT
			COUNT(*) AS total_count,
			COUNT(*) FILTER (WHERE status = 'completed') AS completed_count,
			COUNT(*) FILTER (WHERE status = 'pending') AS pending_count,
			COUNT(*) FILTER (WHERE status = 'failed') AS failed_count,
			COUNT(*) FILTER (WHERE status = 'compensated') AS compensated_count,
			COUNT(*) FILTER (WHERE status = 'needs_manual_review') AS needs_manual_review_count,
			COALESCE(SUM(sub2api_balance_delta) FILTER (WHERE status = 'completed' AND direction = 'from_new_api'), 0) AS total_migrated_balance,
			COALESCE(SUM(new_api_quota_delta) FILTER (WHERE status = 'completed' AND direction = 'from_new_api'), 0) AS total_migrated_quota
		FROM new_api_migration_records
	`
	var stats service.NewAPIMigrationStats
	if err := r.db.QueryRowContext(ctx, query).Scan(
		&stats.TotalCount,
		&stats.CompletedCount,
		&stats.PendingCount,
		&stats.FailedCount,
		&stats.CompensatedCount,
		&stats.NeedsManualReviewCount,
		&stats.TotalMigratedBalance,
		&stats.TotalMigratedQuota,
	); err != nil {
		return nil, fmt.Errorf("get new-api migration stats: %w", err)
	}
	return &stats, nil
}
