package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
)

// ExpiringBalanceRepo implements service.ExpiringBalanceRepository over PostgreSQL (and SQLite for tests).
type ExpiringBalanceRepo struct {
	client *dbent.Client
	sqlDB  *sql.DB
}

// NewExpiringBalanceRepository constructs an ExpiringBalanceRepo backed by Ent and/or *sql.DB.
func NewExpiringBalanceRepository(client *dbent.Client, sqlDB *sql.DB) *ExpiringBalanceRepo {
	return &ExpiringBalanceRepo{
		client: client,
		sqlDB:  sqlDB,
	}
}

func (r *ExpiringBalanceRepo) executorFromContext(ctx context.Context) (sqlExecutor, string, error) {
	if r == nil {
		return nil, "", errors.New("expiring balance repository is nil")
	}
	if tx := dbent.TxFromContext(ctx); tx != nil {
		c := tx.Client()
		return c, expiringSQLDialect(ctx, c), nil
	}
	if r.client != nil {
		return r.client, expiringSQLDialect(ctx, r.client), nil
	}
	if r.sqlDB != nil {
		return r.sqlDB, expiringSQLDialect(ctx, r.sqlDB), nil
	}
	return nil, "", errors.New("expiring balance sql executor is not configured")
}

func (r *ExpiringBalanceRepo) withTx(ctx context.Context, fn func(txCtx context.Context, exec sqlExecutor) error) error {
	if r == nil {
		return errors.New("expiring balance repository is nil")
	}
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(ctx, tx.Client())
	}
	if r.client != nil {
		tx, err := r.client.Tx(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := fn(ctx, tx.Client()); err != nil {
			return err
		}
		return tx.Commit()
	}
	if r.sqlDB != nil {
		d := expiringSQLDialect(ctx, r.sqlDB)
		tx, err := r.sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		txCtx := withExpiringSQLDialect(ctx, d)
		if err := fn(txCtx, tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	return errors.New("expiring balance sql executor is not configured")
}

// DeductTotalBalance atomically deducts amount from active expiring lots (FEFO) before users.balance.
func (r *ExpiringBalanceRepo) DeductTotalBalance(
	ctx context.Context,
	userID int64,
	amount float64,
	allowOverdraft bool,
	refType, refKey string,
) (service.DeductBalanceResult, error) {
	if err := validateFiniteFloat(amount, "amount"); err != nil {
		return service.DeductBalanceResult{}, err
	}
	if amount < 0 {
		return service.DeductBalanceResult{}, errors.New("deduct amount cannot be negative")
	}
	var res service.DeductBalanceResult
	err := r.withTx(ctx, func(txCtx context.Context, exec sqlExecutor) error {
		var txErr error
		res, txErr = DeductUserTotalBalanceTx(txCtx, exec, userID, amount, allowOverdraft, refType, refKey)
		return txErr
	})
	return res, err
}

// RefundPaymentOrderBalance refunds a balance-paid payment order in LIFO order, forfeiting expired lots.
func (r *ExpiringBalanceRepo) RefundPaymentOrderBalance(
	ctx context.Context,
	userID, orderID int64,
	refundAmount float64,
) (bool, service.RefundAllocResult, error) {
	if err := validateFiniteFloat(refundAmount, "refund_amount"); err != nil {
		return false, service.RefundAllocResult{}, err
	}
	if refundAmount < 0 {
		return false, service.RefundAllocResult{}, errors.New("refund amount cannot be negative")
	}
	var (
		hasAlloc bool
		res      service.RefundAllocResult
	)
	err := r.withTx(ctx, func(txCtx context.Context, exec sqlExecutor) error {
		var txErr error
		hasAlloc, res, txErr = RefundPaymentOrderBalanceTx(txCtx, exec, userID, orderID, refundAmount)
		return txErr
	})
	return hasAlloc, res, err
}

// GetActiveExpiringSummary returns the active expiring balance summary for a single user.
func (r *ExpiringBalanceRepo) GetActiveExpiringSummary(ctx context.Context, userID int64) (service.ExpiringBalanceSummary, error) {
	if userID <= 0 {
		return service.ExpiringBalanceSummary{}, nil
	}
	exec, _, err := r.executorFromContext(ctx)
	if err != nil {
		return service.ExpiringBalanceSummary{}, err
	}
	return GetActiveExpiringSummaryTx(ctx, exec, userID)
}

// GetActiveExpiringSummariesBatch returns active expiring balance summaries keyed by userID.
func (r *ExpiringBalanceRepo) GetActiveExpiringSummariesBatch(ctx context.Context, userIDs []int64) (map[int64]service.ExpiringBalanceSummary, error) {
	unique := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	out := make(map[int64]service.ExpiringBalanceSummary, len(unique))
	if len(unique) == 0 {
		return out, nil
	}

	exec, d, err := r.executorFromContext(ctx)
	if err != nil {
		return nil, err
	}
	nowUTC := time.Now().UTC()

	var (
		query string
		args  []any
	)
	if d == dialect.Postgres {
		query = `
			SELECT user_id, remaining_amount, frozen_amount, expires_at, (expires_at > NOW()) AS is_live
			FROM user_expiring_balance_lots
			WHERE user_id = ANY($1) AND status = 'active'
			ORDER BY user_id ASC, expires_at ASC, id ASC`
		args = []any{pq.Array(unique)}
	} else {
		query = fmt.Sprintf(`
			SELECT user_id, remaining_amount, frozen_amount, expires_at
			FROM user_expiring_balance_lots
			WHERE user_id IN (%s) AND status = 'active'
			ORDER BY user_id ASC, expires_at ASC, id ASC`,
			sqlInPlaceholders(d, 1, len(unique)),
		)
		args = make([]any, len(unique))
		for i, id := range unique {
			args[i] = id
		}
	}

	rows, err := exec.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingExpiringBalanceTable(err, d) {
			return out, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	type batchRow struct {
		userID    int64
		remaining decimal.Decimal
		frozen    decimal.Decimal
		expiresAt time.Time
		isLive    bool
	}
	grouped := make(map[int64][]batchRow, len(unique))
	for rows.Next() {
		var (
			uid       int64
			rem, froz float64
			expAt     flexTime
			isLive    bool
		)
		if d == dialect.Postgres {
			if scanErr := rows.Scan(&uid, &rem, &froz, &expAt, &isLive); scanErr != nil {
				return nil, scanErr
			}
		} else {
			if scanErr := rows.Scan(&uid, &rem, &froz, &expAt); scanErr != nil {
				return nil, scanErr
			}
			isLive = expAt.Time.After(nowUTC)
		}
		grouped[uid] = append(grouped[uid], batchRow{
			userID:    uid,
			remaining: decFromFloat(rem),
			frozen:    decFromFloat(froz),
			expiresAt: expAt.Time,
			isLive:    isLive,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for uid, userRows := range grouped {
		if d != dialect.Postgres && len(userRows) > 1 {
			sort.Slice(userRows, func(i, j int) bool {
				return userRows[i].expiresAt.Before(userRows[j].expiresAt)
			})
		}
		activeTotal := decimal.Zero
		frozenTotal := decimal.Zero
		nearestAmount := decimal.Zero
		var nearestExpiresAt *time.Time
		for _, row := range userRows {
			if row.frozen.GreaterThan(decimal.Zero) {
				frozenTotal = frozenTotal.Add(row.frozen)
			}
			if !row.isLive || row.remaining.LessThanOrEqual(decimal.Zero) {
				continue
			}
			activeTotal = activeTotal.Add(row.remaining)
			if nearestExpiresAt == nil {
				t := row.expiresAt
				nearestExpiresAt = &t
				nearestAmount = row.remaining
			} else if row.expiresAt.Equal(*nearestExpiresAt) {
				nearestAmount = nearestAmount.Add(row.remaining)
			}
		}
		out[uid] = service.ExpiringBalanceSummary{
			ActiveBalance:    floatFromDec(activeTotal),
			FrozenBalance:    floatFromDec(frozenTotal),
			NearestExpiresAt: nearestExpiresAt,
			NearestAmount:    floatFromDec(nearestAmount),
		}
	}

	return out, nil
}

// ListUserExpiringLots lists paginated expiring balance lots for a user, normalizing un-archived expired rows.
func (r *ExpiringBalanceRepo) ListUserExpiringLots(
	ctx context.Context,
	userID int64,
	status string,
	params pagination.PaginationParams,
) ([]service.ExpiringBalanceLot, *pagination.PaginationResult, error) {
	if params.Page < 1 {
		params.Page = 1
	}
	pageSize := params.Limit()
	offset := params.Offset()

	emptyResult := &pagination.PaginationResult{
		Total:    0,
		Page:     params.Page,
		PageSize: pageSize,
		Pages:    0,
	}
	if userID <= 0 {
		return []service.ExpiringBalanceLot{}, emptyResult, nil
	}

	exec, d, err := r.executorFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}

	if d != dialect.Postgres {
		return r.listUserExpiringLotsSQLite(ctx, exec, d, userID, status, params.Page, pageSize, offset)
	}

	whereClause := "user_id = $1"
	args := []any{userID}
	switch status {
	case "", "all":
	case service.ExpiringLotStatusActive:
		whereClause += " AND status = 'active' AND (expires_at > NOW() OR frozen_amount > 0)"
	case service.ExpiringLotStatusExhausted:
		whereClause += " AND status = 'exhausted'"
	case service.ExpiringLotStatusExpired:
		whereClause += " AND (status = 'expired' OR (status = 'active' AND expires_at <= NOW() AND frozen_amount = 0))"
	default:
		whereClause += " AND status = $2"
		args = append(args, status)
	}

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM user_expiring_balance_lots WHERE %s", whereClause)
	var total int64
	if scanErr := scanSingleRow(ctx, exec, countQuery, args, &total); scanErr != nil {
		return nil, nil, scanErr
	}

	pages := 0
	if total > 0 {
		pages = int(math.Ceil(float64(total) / float64(pageSize)))
	}
	pag := &pagination.PaginationResult{
		Total:    total,
		Page:     params.Page,
		PageSize: pageSize,
		Pages:    pages,
	}
	if total == 0 {
		return []service.ExpiringBalanceLot{}, pag, nil
	}

	limitPlaceholder := sqlPlaceholder(d, len(args)+1)
	offsetPlaceholder := sqlPlaceholder(d, len(args)+2)
	listQuery := fmt.Sprintf(`
		SELECT id, user_id, source_type, source_id, initial_amount, remaining_amount,
		       frozen_amount, consumed_amount, expired_amount, expires_at, status,
		       created_at, updated_at, (expires_at > NOW()) AS is_live
		FROM user_expiring_balance_lots
		WHERE %s
		ORDER BY created_at DESC, id DESC
		LIMIT %s OFFSET %s`,
		whereClause, limitPlaceholder, offsetPlaceholder,
	)
	listArgs := append(append([]any(nil), args...), pageSize, offset)
	rows, err := exec.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	lots := make([]service.ExpiringBalanceLot, 0)
	for rows.Next() {
		var (
			lot                         service.ExpiringBalanceLot
			expAt, createdAt, updatedAt flexTime
			isLive                      bool
		)
		if scanErr := rows.Scan(
			&lot.ID,
			&lot.UserID,
			&lot.SourceType,
			&lot.SourceID,
			&lot.InitialAmount,
			&lot.RemainingAmount,
			&lot.FrozenAmount,
			&lot.ConsumedAmount,
			&lot.ExpiredAmount,
			&expAt,
			&lot.Status,
			&createdAt,
			&updatedAt,
			&isLive,
		); scanErr != nil {
			return nil, nil, scanErr
		}
		lot.ExpiresAt = expAt.Time
		lot.CreatedAt = createdAt.Time
		lot.UpdatedAt = updatedAt.Time
		normalizeLotForDisplay(&lot, isLive)
		lots = append(lots, lot)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return lots, pag, nil
}

func (r *ExpiringBalanceRepo) listUserExpiringLotsSQLite(
	ctx context.Context,
	exec sqlExecutor,
	d string,
	userID int64,
	status string,
	page, pageSize, offset int,
) ([]service.ExpiringBalanceLot, *pagination.PaginationResult, error) {
	nowUTC := time.Now().UTC()
	rows, err := exec.QueryContext(ctx, `
		SELECT id, user_id, source_type, source_id, initial_amount, remaining_amount,
		       frozen_amount, consumed_amount, expired_amount, expires_at, status,
		       created_at, updated_at
		FROM user_expiring_balance_lots
		WHERE user_id = ?
		ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		if isMissingExpiringBalanceTable(err, d) {
			return []service.ExpiringBalanceLot{}, &pagination.PaginationResult{
				Total:    0,
				Page:     page,
				PageSize: pageSize,
				Pages:    0,
			}, nil
		}
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	filtered := make([]service.ExpiringBalanceLot, 0)
	for rows.Next() {
		var (
			lot                         service.ExpiringBalanceLot
			expAt, createdAt, updatedAt flexTime
		)
		if scanErr := rows.Scan(
			&lot.ID,
			&lot.UserID,
			&lot.SourceType,
			&lot.SourceID,
			&lot.InitialAmount,
			&lot.RemainingAmount,
			&lot.FrozenAmount,
			&lot.ConsumedAmount,
			&lot.ExpiredAmount,
			&expAt,
			&lot.Status,
			&createdAt,
			&updatedAt,
		); scanErr != nil {
			return nil, nil, scanErr
		}
		lot.ExpiresAt = expAt.Time
		lot.CreatedAt = createdAt.Time
		lot.UpdatedAt = updatedAt.Time
		isLive := lot.ExpiresAt.After(nowUTC)
		normalizeLotForDisplay(&lot, isLive)
		if status != "" && status != "all" && lot.Status != status {
			continue
		}
		filtered = append(filtered, lot)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	total := int64(len(filtered))
	pages := 0
	if total > 0 {
		pages = int(math.Ceil(float64(total) / float64(pageSize)))
	}
	pag := &pagination.PaginationResult{
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    pages,
	}
	if offset >= len(filtered) {
		return []service.ExpiringBalanceLot{}, pag, nil
	}
	end := offset + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[offset:end], pag, nil
}

func normalizeLotForDisplay(lot *service.ExpiringBalanceLot, isLive bool) {
	if lot == nil {
		return
	}
	rem := decFromFloat(lot.RemainingAmount)
	froz := decFromFloat(lot.FrozenAmount)
	exp := decFromFloat(lot.ExpiredAmount)
	if lot.Status == service.ExpiringLotStatusActive && !isLive && froz.IsZero() {
		lot.Status = service.ExpiringLotStatusExpired
		lot.ExpiredAmount = floatFromDec(exp.Add(rem))
		lot.RemainingAmount = 0
	}
}

// ArchiveExpiredLotsBatch sweeps up to limit expired lots inside a transaction, moving
// un-frozen remaining_amount into expired_amount and transitioning status to 'expired'
// when frozen_amount == 0 (preserving 'active' while frozen_amount > 0).
func (r *ExpiringBalanceRepo) ArchiveExpiredLotsBatch(ctx context.Context, limit int) ([]int64, int, error) {
	if limit <= 0 {
		limit = service.DefaultExpiringBalanceArchiveBatchSize
	}

	var (
		affectedUsers []int64
		archivedCount int
	)

	err := r.withTx(ctx, func(txCtx context.Context, exec sqlExecutor) error {
		d := expiringSQLDialect(txCtx, exec)
		nowUTC := time.Now().UTC()

		type archiveCandidate struct {
			id        int64
			userID    int64
			remaining decimal.Decimal
			frozen    decimal.Decimal
			expired   decimal.Decimal
		}

		candidates := make([]archiveCandidate, 0)

		selectCandidates := func() error {
			var (
				query string
				args  []any
			)
			if d == dialect.Postgres {
				query = `
					SELECT id, user_id, remaining_amount, frozen_amount, expired_amount
					FROM user_expiring_balance_lots
					WHERE status = 'active'
					  AND expires_at <= NOW()
					  AND (remaining_amount > 0 OR frozen_amount = 0)
					ORDER BY expires_at ASC, id ASC
					LIMIT $1
					FOR UPDATE SKIP LOCKED`
				args = []any{limit}
			} else {
				query = `
					SELECT id, user_id, remaining_amount, frozen_amount, expired_amount, expires_at
					FROM user_expiring_balance_lots
					WHERE status = 'active'
					  AND (remaining_amount > 0 OR frozen_amount = 0)
					ORDER BY expires_at ASC, id ASC`
			}

			rows, err := exec.QueryContext(txCtx, query, args...)
			if err != nil {
				if isMissingExpiringBalanceTable(err, d) {
					return nil
				}
				return err
			}
			defer func() { _ = rows.Close() }()

			for rows.Next() {
				var (
					id, uid        int64
					rem, froz, exp float64
					expiresAt      flexTime
				)
				if d == dialect.Postgres {
					if scanErr := rows.Scan(&id, &uid, &rem, &froz, &exp); scanErr != nil {
						return scanErr
					}
				} else {
					if scanErr := rows.Scan(&id, &uid, &rem, &froz, &exp, &expiresAt); scanErr != nil {
						return scanErr
					}
					if expiresAt.Time.After(nowUTC) {
						continue
					}
					if len(candidates) >= limit {
						break
					}
				}
				candidates = append(candidates, archiveCandidate{
					id:        id,
					userID:    uid,
					remaining: decFromFloat(rem),
					frozen:    decFromFloat(froz),
					expired:   decFromFloat(exp),
				})
			}
			return rows.Err()
		}

		if selErr := selectCandidates(); selErr != nil {
			return selErr
		}
		if len(candidates) == 0 {
			return nil
		}

		seenUsers := make(map[int64]struct{}, len(candidates))
		for _, c := range candidates {
			newExpired := c.expired.Add(c.remaining)
			newStatus := service.ExpiringLotStatusActive
			if c.frozen.IsZero() {
				newStatus = service.ExpiringLotStatusExpired
			}

			var updateQuery string
			var args []any
			if d == dialect.Postgres {
				updateQuery = `
					UPDATE user_expiring_balance_lots
					SET remaining_amount = 0,
						expired_amount = $1,
						status = $2,
						updated_at = NOW()
					WHERE id = $3`
				args = []any{floatFromDec(newExpired), newStatus, c.id}
			} else {
				updateQuery = `
					UPDATE user_expiring_balance_lots
					SET remaining_amount = 0,
						expired_amount = ?,
						status = ?,
						updated_at = ?
					WHERE id = ?`
				args = []any{floatFromDec(newExpired), newStatus, nowUTC, c.id}
			}
			if _, execErr := exec.ExecContext(txCtx, updateQuery, args...); execErr != nil {
				return execErr
			}

			archivedCount++
			if c.userID > 0 {
				if _, ok := seenUsers[c.userID]; !ok {
					seenUsers[c.userID] = struct{}{}
					affectedUsers = append(affectedUsers, c.userID)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return affectedUsers, archivedCount, nil
}
