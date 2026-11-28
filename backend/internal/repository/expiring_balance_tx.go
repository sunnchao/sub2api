package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
)

// Type aliases re-export service expiring-balance domain models for repository callers.
type (
	ExpiringBalanceSummary = service.ExpiringBalanceSummary
	DeductBalanceResult    = service.DeductBalanceResult
	RefundAllocResult      = service.RefundAllocResult
	ExpiringBalanceLot     = service.ExpiringBalanceLot
)

type expiringSQLDialectCtxKey struct{}

func withExpiringSQLDialect(ctx context.Context, dialectName string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, expiringSQLDialectCtxKey{}, dialectName)
}

func expiringSQLDialect(ctx context.Context, exec sqlExecutor) string {
	if ctx != nil {
		if d, ok := ctx.Value(expiringSQLDialectCtxKey{}).(string); ok && d != "" {
			return d
		}
	}
	if entDriverProvider, ok := exec.(interface{ Driver() dialect.Driver }); ok && entDriverProvider.Driver() != nil {
		if d := entDriverProvider.Driver().Dialect(); d != "" {
			return d
		}
	}
	if sqlDriverProvider, ok := exec.(interface{ Driver() driver.Driver }); ok && sqlDriverProvider.Driver() != nil {
		driverName := strings.ToLower(fmt.Sprintf("%T", sqlDriverProvider.Driver()))
		if strings.Contains(driverName, "sqlite") {
			return dialect.SQLite
		}
	}
	return dialect.Postgres
}

func isMissingExpiringBalanceTable(err error, dialectName string) bool {
	if err == nil || dialectName == dialect.Postgres {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table: user_expiring_balance_lots") ||
		strings.Contains(msg, "no such table: user_expiring_balance_allocations")
}

var errBatchImageFrozenInsufficient = errors.New("batch image frozen balance is insufficient")

func validateFiniteFloat(v float64, field string) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("%s must be a finite number", field)
	}
	return nil
}

func decFromFloat(v float64) decimal.Decimal {
	return decimal.NewFromFloat(v).Round(service.UsageBillingMonetaryScale)
}

func floatFromDec(d decimal.Decimal) float64 {
	f, _ := d.Round(service.UsageBillingMonetaryScale).Float64()
	return f
}

// flexTime scans SQL timestamp columns across PostgreSQL (time.Time) and SQLite (time.Time or text).
type flexTime struct {
	Time time.Time
}

func (ft *flexTime) Scan(src any) error {
	if src == nil {
		ft.Time = time.Time{}
		return nil
	}
	switch v := src.(type) {
	case time.Time:
		ft.Time = v
		return nil
	case []byte:
		return ft.parseString(string(v))
	case string:
		return ft.parseString(v)
	default:
		return fmt.Errorf("unsupported timestamp scan type %T", src)
	}
}

func (ft *flexTime) parseString(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" {
		ft.Time = time.Time{}
		return nil
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			ft.Time = t
			return nil
		}
	}
	return fmt.Errorf("cannot parse timestamp %q", s)
}

type expiringLotTxRow struct {
	id        int64
	remaining decimal.Decimal
	frozen    decimal.Decimal
	consumed  decimal.Decimal
	expired   decimal.Decimal
	expiresAt time.Time
	status    string
	isLive    bool
}

type expiringAllocTxRow struct {
	id               int64
	allocSeq         int
	lotID            sql.NullInt64
	allocated        decimal.Decimal
	captured         decimal.Decimal
	released         decimal.Decimal
	forfeitedExpired decimal.Decimal
}

type pendingAllocInsert struct {
	lotID  *int64
	amount decimal.Decimal
}

func lockUserBalancesTx(ctx context.Context, exec sqlExecutor, d string, userID int64) (regular, frozen decimal.Decimal, err error) {
	query := fmt.Sprintf(
		`SELECT balance, COALESCE(frozen_balance, 0) FROM users WHERE id = %s AND deleted_at IS NULL`,
		sqlPlaceholder(d, 1),
	)
	if d == dialect.Postgres {
		query += " FOR UPDATE"
	}
	var rawBal, rawFrozen float64
	if scanErr := scanSingleRow(ctx, exec, query, []any{userID}, &rawBal, &rawFrozen); scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return decimal.Zero, decimal.Zero, service.ErrUserNotFound
		}
		return decimal.Zero, decimal.Zero, scanErr
	}
	return decFromFloat(rawBal), decFromFloat(rawFrozen), nil
}

func queryActiveLiveLotsForUpdateTx(ctx context.Context, exec sqlExecutor, d string, userID int64, nowUTC time.Time) ([]*expiringLotTxRow, error) {
	var query string
	if d == dialect.Postgres {
		query = `
			SELECT id, remaining_amount, frozen_amount, consumed_amount, expired_amount, expires_at, status
			FROM user_expiring_balance_lots
			WHERE user_id = $1 AND status = 'active' AND remaining_amount > 0 AND expires_at > NOW()
			ORDER BY expires_at ASC, id ASC
			FOR UPDATE`
	} else {
		query = `
			SELECT id, remaining_amount, frozen_amount, consumed_amount, expired_amount, expires_at, status
			FROM user_expiring_balance_lots
			WHERE user_id = ? AND status = 'active' AND remaining_amount > 0
			ORDER BY expires_at ASC, id ASC`
	}

	rows, err := exec.QueryContext(ctx, query, userID)
	if err != nil {
		if isMissingExpiringBalanceTable(err, d) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	lots := make([]*expiringLotTxRow, 0)
	for rows.Next() {
		var (
			id                   int64
			rem, froz, cons, exp float64
			expiresAt            flexTime
			status               string
		)
		if scanErr := rows.Scan(&id, &rem, &froz, &cons, &exp, &expiresAt, &status); scanErr != nil {
			return nil, scanErr
		}
		if d != dialect.Postgres && !expiresAt.Time.After(nowUTC) {
			continue
		}
		lots = append(lots, &expiringLotTxRow{
			id:        id,
			remaining: decFromFloat(rem),
			frozen:    decFromFloat(froz),
			consumed:  decFromFloat(cons),
			expired:   decFromFloat(exp),
			expiresAt: expiresAt.Time,
			status:    status,
			isLive:    true,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if d != dialect.Postgres && len(lots) > 1 {
		sort.Slice(lots, func(i, j int) bool {
			if !lots[i].expiresAt.Equal(lots[j].expiresAt) {
				return lots[i].expiresAt.Before(lots[j].expiresAt)
			}
			return lots[i].id < lots[j].id
		})
	}
	return lots, nil
}

func queryAllocationsTx(ctx context.Context, exec sqlExecutor, d string, userID int64, refType, refKey string, asc bool) ([]*expiringAllocTxRow, error) {
	order := "DESC"
	if asc {
		order = "ASC"
	}
	query := fmt.Sprintf(`
		SELECT id, alloc_seq, lot_id, allocated_amount, captured_amount, released_amount, forfeited_expired_amount
		FROM user_expiring_balance_allocations
		WHERE user_id = %s AND ref_type = %s AND ref_key = %s
		ORDER BY alloc_seq %s`,
		sqlPlaceholder(d, 1), sqlPlaceholder(d, 2), sqlPlaceholder(d, 3), order,
	)
	if d == dialect.Postgres {
		query += " FOR UPDATE"
	}

	rows, err := exec.QueryContext(ctx, query, userID, refType, refKey)
	if err != nil {
		if isMissingExpiringBalanceTable(err, d) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	allocs := make([]*expiringAllocTxRow, 0)
	for rows.Next() {
		var (
			id                     int64
			allocSeq               int
			lotID                  sql.NullInt64
			allocated, captured    float64
			released, forfeitedExp float64
		)
		if scanErr := rows.Scan(&id, &allocSeq, &lotID, &allocated, &captured, &released, &forfeitedExp); scanErr != nil {
			return nil, scanErr
		}
		allocs = append(allocs, &expiringAllocTxRow{
			id:               id,
			allocSeq:         allocSeq,
			lotID:            lotID,
			allocated:        decFromFloat(allocated),
			captured:         decFromFloat(captured),
			released:         decFromFloat(released),
			forfeitedExpired: decFromFloat(forfeitedExp),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return allocs, nil
}

func lockLotsByIDsTx(ctx context.Context, exec sqlExecutor, d string, lotIDs []int64, nowUTC time.Time) (map[int64]*expiringLotTxRow, error) {
	result := make(map[int64]*expiringLotTxRow, len(lotIDs))
	if len(lotIDs) == 0 {
		return result, nil
	}

	unique := make([]int64, 0, len(lotIDs))
	seen := make(map[int64]struct{}, len(lotIDs))
	for _, id := range lotIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return result, nil
	}

	args := make([]any, len(unique))
	for i, id := range unique {
		args[i] = id
	}

	var query string
	if d == dialect.Postgres {
		query = fmt.Sprintf(`
			SELECT id, remaining_amount, frozen_amount, consumed_amount, expired_amount, expires_at, status, (expires_at > NOW()) AS is_live
			FROM user_expiring_balance_lots
			WHERE id IN (%s)
			ORDER BY expires_at ASC, id ASC
			FOR UPDATE`,
			sqlInPlaceholders(d, 1, len(unique)),
		)
	} else {
		query = fmt.Sprintf(`
			SELECT id, remaining_amount, frozen_amount, consumed_amount, expired_amount, expires_at, status
			FROM user_expiring_balance_lots
			WHERE id IN (%s)
			ORDER BY expires_at ASC, id ASC`,
			sqlInPlaceholders(d, 1, len(unique)),
		)
	}

	rows, err := exec.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id                   int64
			rem, froz, cons, exp float64
			expiresAt            flexTime
			status               string
			isLive               bool
		)
		if d == dialect.Postgres {
			if scanErr := rows.Scan(&id, &rem, &froz, &cons, &exp, &expiresAt, &status, &isLive); scanErr != nil {
				return nil, scanErr
			}
		} else {
			if scanErr := rows.Scan(&id, &rem, &froz, &cons, &exp, &expiresAt, &status); scanErr != nil {
				return nil, scanErr
			}
			isLive = expiresAt.Time.After(nowUTC)
		}
		result[id] = &expiringLotTxRow{
			id:        id,
			remaining: decFromFloat(rem),
			frozen:    decFromFloat(froz),
			consumed:  decFromFloat(cons),
			expired:   decFromFloat(exp),
			expiresAt: expiresAt.Time,
			status:    status,
			isLive:    isLive,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func insertAllocationsTx(
	ctx context.Context,
	exec sqlExecutor,
	d string,
	userID int64,
	refType, refKey string,
	entries []pendingAllocInsert,
	capturedAll bool,
	nowUTC time.Time,
) error {
	if len(entries) == 0 {
		return nil
	}
	for i, entry := range entries {
		seq := i + 1
		var lotArg any
		if entry.lotID != nil {
			lotArg = *entry.lotID
		}
		allocated := floatFromDec(entry.amount)
		captured := 0.0
		if capturedAll {
			captured = allocated
		}
		var query string
		var args []any
		if d == dialect.Postgres {
			query = `
				INSERT INTO user_expiring_balance_allocations
					(user_id, ref_type, ref_key, alloc_seq, lot_id, allocated_amount, captured_amount, released_amount, forfeited_expired_amount, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 0, NOW(), NOW())`
			args = []any{userID, refType, refKey, seq, lotArg, allocated, captured}
		} else {
			query = `
				INSERT INTO user_expiring_balance_allocations
					(user_id, ref_type, ref_key, alloc_seq, lot_id, allocated_amount, captured_amount, released_amount, forfeited_expired_amount, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?)`
			args = []any{userID, refType, refKey, seq, lotArg, allocated, captured, nowUTC, nowUTC}
		}
		if _, err := exec.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

func updateLotAfterReserveOrDeductTx(ctx context.Context, exec sqlExecutor, d string, lot *expiringLotTxRow, nowUTC time.Time) error {
	var query string
	var args []any
	if d == dialect.Postgres {
		query = `
			UPDATE user_expiring_balance_lots
			SET remaining_amount = $1, frozen_amount = $2, consumed_amount = $3, expired_amount = $4, status = $5, updated_at = NOW()
			WHERE id = $6`
		args = []any{
			floatFromDec(lot.remaining),
			floatFromDec(lot.frozen),
			floatFromDec(lot.consumed),
			floatFromDec(lot.expired),
			lot.status,
			lot.id,
		}
	} else {
		query = `
			UPDATE user_expiring_balance_lots
			SET remaining_amount = ?, frozen_amount = ?, consumed_amount = ?, expired_amount = ?, status = ?, updated_at = ?
			WHERE id = ?`
		args = []any{
			floatFromDec(lot.remaining),
			floatFromDec(lot.frozen),
			floatFromDec(lot.consumed),
			floatFromDec(lot.expired),
			lot.status,
			nowUTC,
			lot.id,
		}
	}
	_, err := exec.ExecContext(ctx, query, args...)
	return err
}

func updateAllocationAmountsTx(ctx context.Context, exec sqlExecutor, d string, alloc *expiringAllocTxRow, nowUTC time.Time) error {
	var query string
	var args []any
	if d == dialect.Postgres {
		query = `
			UPDATE user_expiring_balance_allocations
			SET captured_amount = $1, released_amount = $2, forfeited_expired_amount = $3, updated_at = NOW()
			WHERE id = $4`
		args = []any{
			floatFromDec(alloc.captured),
			floatFromDec(alloc.released),
			floatFromDec(alloc.forfeitedExpired),
			alloc.id,
		}
	} else {
		query = `
			UPDATE user_expiring_balance_allocations
			SET captured_amount = ?, released_amount = ?, forfeited_expired_amount = ?, updated_at = ?
			WHERE id = ?`
		args = []any{
			floatFromDec(alloc.captured),
			floatFromDec(alloc.released),
			floatFromDec(alloc.forfeitedExpired),
			nowUTC,
			alloc.id,
		}
	}
	_, err := exec.ExecContext(ctx, query, args...)
	return err
}

func recomputeSettledLotStatus(lot *expiringLotTxRow) {
	if !lot.frozen.IsZero() {
		lot.status = service.ExpiringLotStatusActive
		return
	}
	if lot.remaining.GreaterThan(decimal.Zero) {
		if lot.isLive {
			lot.status = service.ExpiringLotStatusActive
		}
		// If remaining > 0 and expired, keep 'active' so ArchiveExpiredLotsBatch sweeps remaining -> expired_amount cleanly.
		return
	}
	if !lot.isLive || lot.expired.GreaterThan(decimal.Zero) {
		lot.status = service.ExpiringLotStatusExpired
		return
	}
	lot.status = service.ExpiringLotStatusExhausted
}

// DeductUserTotalBalanceTx deducts amount from a user's active expiring lots in FEFO order
// (expires_at ASC, id ASC) before falling back to regular users.balance, all within the
// caller's transaction. Lock order: users -> user_expiring_balance_lots.
func DeductUserTotalBalanceTx(
	ctx context.Context,
	exec sqlExecutor,
	userID int64,
	amount float64,
	allowOverdraft bool,
	refType, refKey string,
) (DeductBalanceResult, error) {
	if exec == nil {
		return DeductBalanceResult{}, errors.New("sql executor is nil")
	}
	if userID <= 0 {
		return DeductBalanceResult{}, service.ErrUserNotFound
	}
	if err := validateFiniteFloat(amount, "amount"); err != nil {
		return DeductBalanceResult{}, err
	}
	if amount < 0 {
		return DeductBalanceResult{}, errors.New("deduct amount cannot be negative")
	}

	d := expiringSQLDialect(ctx, exec)
	nowUTC := time.Now().UTC()
	qAmount := decFromFloat(amount)

	regularBal, _, err := lockUserBalancesTx(ctx, exec, d, userID)
	if err != nil {
		return DeductBalanceResult{}, err
	}

	if qAmount.IsZero() {
		summary, sumErr := GetActiveExpiringSummaryTx(ctx, exec, userID)
		if sumErr != nil {
			return DeductBalanceResult{}, sumErr
		}
		expBal := decFromFloat(summary.ActiveBalance)
		return DeductBalanceResult{
			NewRegularBalance:  floatFromDec(regularBal),
			NewExpiringBalance: floatFromDec(expBal),
			NewTotalBalance:    floatFromDec(regularBal.Add(expBal)),
			Sufficient:         true,
		}, nil
	}

	lots, err := queryActiveLiveLotsForUpdateTx(ctx, exec, d, userID, nowUTC)
	if err != nil {
		return DeductBalanceResult{}, err
	}

	activeExpiring := decimal.Zero
	for _, lot := range lots {
		activeExpiring = activeExpiring.Add(lot.remaining)
	}
	totalAvailable := regularBal.Add(activeExpiring)
	sufficient := totalAvailable.GreaterThanOrEqual(qAmount)

	if !allowOverdraft && !sufficient {
		return DeductBalanceResult{
			NewRegularBalance:  floatFromDec(regularBal),
			NewExpiringBalance: floatFromDec(activeExpiring),
			NewTotalBalance:    floatFromDec(totalAvailable),
			Sufficient:         false,
		}, service.ErrBalanceNegative
	}

	need := qAmount
	expiringDeducted := decimal.Zero
	allocEntries := make([]pendingAllocInsert, 0, len(lots)+1)

	for _, lot := range lots {
		if need.LessThanOrEqual(decimal.Zero) {
			break
		}
		slice := decimal.Min(need, lot.remaining)
		if slice.LessThanOrEqual(decimal.Zero) {
			continue
		}
		lot.remaining = lot.remaining.Sub(slice)
		lot.consumed = lot.consumed.Add(slice)
		if lot.remaining.IsZero() && lot.frozen.IsZero() {
			lot.status = service.ExpiringLotStatusExhausted
		}
		if updateErr := updateLotAfterReserveOrDeductTx(ctx, exec, d, lot, nowUTC); updateErr != nil {
			return DeductBalanceResult{}, updateErr
		}
		lotIDCopy := lot.id
		allocEntries = append(allocEntries, pendingAllocInsert{
			lotID:  &lotIDCopy,
			amount: slice,
		})
		need = need.Sub(slice)
		expiringDeducted = expiringDeducted.Add(slice)
	}

	regularSlice := need
	newRegular := regularBal.Sub(regularSlice)
	if regularSlice.GreaterThan(decimal.Zero) {
		var updateQuery string
		var args []any
		if d == dialect.Postgres {
			updateQuery = `UPDATE users SET balance = balance - $1, updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL`
			args = []any{floatFromDec(regularSlice), userID}
		} else {
			updateQuery = `UPDATE users SET balance = balance - ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`
			args = []any{floatFromDec(regularSlice), nowUTC, userID}
		}
		if _, execErr := exec.ExecContext(ctx, updateQuery, args...); execErr != nil {
			return DeductBalanceResult{}, execErr
		}
	}

	if expiringDeducted.GreaterThan(decimal.Zero) && strings.TrimSpace(refType) != "" && strings.TrimSpace(refKey) != "" {
		if regularSlice.GreaterThan(decimal.Zero) {
			allocEntries = append(allocEntries, pendingAllocInsert{
				lotID:  nil,
				amount: regularSlice,
			})
		}
		if insErr := insertAllocationsTx(ctx, exec, d, userID, refType, refKey, allocEntries, true, nowUTC); insErr != nil {
			return DeductBalanceResult{}, insErr
		}
	}

	newExpiring := activeExpiring.Sub(expiringDeducted)
	newTotal := newRegular.Add(newExpiring)
	return DeductBalanceResult{
		NewRegularBalance:  floatFromDec(newRegular),
		NewExpiringBalance: floatFromDec(newExpiring),
		NewTotalBalance:    floatFromDec(newTotal),
		ExpiringDeducted:   floatFromDec(expiringDeducted),
		RegularDeducted:    floatFromDec(regularSlice),
		Sufficient:         sufficient,
	}, nil
}

// ReserveBatchImageTotalBalanceTx freezes holdAmount from active expiring lots (FEFO)
// first and regular users.balance second. Allocation rows are persisted only when at
// least one expiring lot is touched, keeping pure regular-balance holds zero-overhead.
func ReserveBatchImageTotalBalanceTx(
	ctx context.Context,
	exec sqlExecutor,
	userID int64,
	holdAmount float64,
	batchID string,
) error {
	if exec == nil {
		return errors.New("sql executor is nil")
	}
	if userID <= 0 {
		return service.ErrUserNotFound
	}
	if err := validateFiniteFloat(holdAmount, "hold_amount"); err != nil {
		return err
	}
	if holdAmount < 0 {
		return errors.New("hold amount cannot be negative")
	}
	qHold := decFromFloat(holdAmount)
	if qHold.IsZero() {
		return nil
	}

	d := expiringSQLDialect(ctx, exec)
	nowUTC := time.Now().UTC()

	regularBal, regularFrozen, err := lockUserBalancesTx(ctx, exec, d, userID)
	if err != nil {
		return err
	}

	lots, err := queryActiveLiveLotsForUpdateTx(ctx, exec, d, userID, nowUTC)
	if err != nil {
		return err
	}

	activeExpiring := decimal.Zero
	for _, lot := range lots {
		activeExpiring = activeExpiring.Add(lot.remaining)
	}
	if regularBal.Add(activeExpiring).LessThan(qHold) {
		return service.ErrBatchImageInsufficientBalance
	}

	need := qHold
	expiringFrozen := decimal.Zero
	allocEntries := make([]pendingAllocInsert, 0, len(lots)+1)

	for _, lot := range lots {
		if need.LessThanOrEqual(decimal.Zero) {
			break
		}
		slice := decimal.Min(need, lot.remaining)
		if slice.LessThanOrEqual(decimal.Zero) {
			continue
		}
		lot.remaining = lot.remaining.Sub(slice)
		lot.frozen = lot.frozen.Add(slice)
		lot.status = service.ExpiringLotStatusActive
		if updateErr := updateLotAfterReserveOrDeductTx(ctx, exec, d, lot, nowUTC); updateErr != nil {
			return updateErr
		}
		lotIDCopy := lot.id
		allocEntries = append(allocEntries, pendingAllocInsert{
			lotID:  &lotIDCopy,
			amount: slice,
		})
		need = need.Sub(slice)
		expiringFrozen = expiringFrozen.Add(slice)
	}

	regularSlice := need
	if regularSlice.GreaterThan(decimal.Zero) {
		newRegular := regularBal.Sub(regularSlice)
		newFrozen := regularFrozen.Add(regularSlice)
		var updateQuery string
		var args []any
		if d == dialect.Postgres {
			updateQuery = `
				UPDATE users
				SET balance = $1,
					frozen_balance = $2,
					updated_at = NOW()
				WHERE id = $3 AND deleted_at IS NULL`
			args = []any{floatFromDec(newRegular), floatFromDec(newFrozen), userID}
		} else {
			updateQuery = `
				UPDATE users
				SET balance = ?,
					frozen_balance = ?,
					updated_at = ?
				WHERE id = ? AND deleted_at IS NULL`
			args = []any{floatFromDec(newRegular), floatFromDec(newFrozen), nowUTC, userID}
		}
		if _, execErr := exec.ExecContext(ctx, updateQuery, args...); execErr != nil {
			return execErr
		}
	}

	if expiringFrozen.GreaterThan(decimal.Zero) {
		if regularSlice.GreaterThan(decimal.Zero) {
			allocEntries = append(allocEntries, pendingAllocInsert{
				lotID:  nil,
				amount: regularSlice,
			})
		}
		if insErr := insertAllocationsTx(ctx, exec, d, userID, service.ExpiringAllocRefBatchImageHold, batchID, allocEntries, false, nowUTC); insErr != nil {
			return insErr
		}
	}

	return nil
}

// CaptureBatchImageTotalBalanceTx settles a batch image hold:
//   - If no allocation rows exist for batchID, falls back to legacy users.frozen_balance settlement.
//   - Otherwise captures actualAmount in FEFO order (alloc_seq ASC) and releases any remaining
//     excess in LIFO order (alloc_seq DESC), returning regular funds to users.balance, live lots
//     to remaining_amount with original expires_at, and forfeiting expired lots.
func CaptureBatchImageTotalBalanceTx(
	ctx context.Context,
	exec sqlExecutor,
	userID int64,
	holdAmount, actualAmount float64,
	batchID string,
) (bool, error) {
	if exec == nil {
		return false, errors.New("sql executor is nil")
	}
	if userID <= 0 {
		return false, service.ErrUserNotFound
	}
	if err := validateFiniteFloat(holdAmount, "hold_amount"); err != nil {
		return false, err
	}
	if err := validateFiniteFloat(actualAmount, "actual_amount"); err != nil {
		return false, err
	}
	if holdAmount < 0 {
		return false, errors.New("hold amount cannot be negative")
	}
	if actualAmount < 0 {
		return false, errors.New("actual amount cannot be negative")
	}
	qHold := decFromFloat(holdAmount)
	qActual := decFromFloat(actualAmount)
	if qActual.Sub(qHold).GreaterThan(decimal.New(1, -8)) {
		return false, service.ErrBatchImageSettlementCostExceedsHold
	}
	if qHold.IsZero() && qActual.IsZero() {
		return false, nil
	}

	d := expiringSQLDialect(ctx, exec)
	nowUTC := time.Now().UTC()

	regularBal, regularFrozen, err := lockUserBalancesTx(ctx, exec, d, userID)
	if err != nil {
		return false, err
	}

	allocs, err := queryAllocationsTx(ctx, exec, d, userID, service.ExpiringAllocRefBatchImageHold, batchID, true)
	if err != nil {
		return false, err
	}

	if len(allocs) == 0 {
		if regularFrozen.LessThan(qHold) {
			return false, errBatchImageFrozenInsufficient
		}
		newRegular := regularBal.Add(qHold.Sub(qActual))
		newFrozen := regularFrozen.Sub(qHold)
		if newFrozen.IsZero() {
			newFrozen = decimal.Zero
		}
		var updateQuery string
		var args []any
		if d == dialect.Postgres {
			updateQuery = `
				UPDATE users
				SET balance = $1,
					frozen_balance = $2,
					updated_at = NOW()
				WHERE id = $3 AND deleted_at IS NULL`
			args = []any{floatFromDec(newRegular), floatFromDec(newFrozen), userID}
		} else {
			updateQuery = `
				UPDATE users
				SET balance = ?,
					frozen_balance = ?,
					updated_at = ?
				WHERE id = ? AND deleted_at IS NULL`
			args = []any{floatFromDec(newRegular), floatFromDec(newFrozen), nowUTC, userID}
		}
		res, execErr := exec.ExecContext(ctx, updateQuery, args...)
		if execErr != nil {
			return false, execErr
		}
		affected, affErr := res.RowsAffected()
		if affErr == nil && affected == 0 {
			return false, errBatchImageFrozenInsufficient
		}
		return false, nil
	}

	lotIDs := make([]int64, 0, len(allocs))
	for _, a := range allocs {
		if a.lotID.Valid {
			lotIDs = append(lotIDs, a.lotID.Int64)
		}
	}
	lotMap, err := lockLotsByIDsTx(ctx, exec, d, lotIDs, nowUTC)
	if err != nil {
		return false, err
	}

	captureTarget := decimal.Min(qActual, qHold)
	extraOverHold := qActual.Sub(captureTarget)
	needCapture := captureTarget

	regularFrozenDelta := decimal.Zero
	regularBalanceDelta := decimal.Zero
	touchedLots := make(map[int64]*expiringLotTxRow, len(lotMap))
	touchedAllocs := make(map[int64]*expiringAllocTxRow, len(allocs))

	// Pass 1: FEFO capture (alloc_seq ASC).
	for _, alloc := range allocs {
		if needCapture.LessThanOrEqual(decimal.Zero) {
			break
		}
		unsettled := alloc.allocated.Sub(alloc.captured).Sub(alloc.released).Sub(alloc.forfeitedExpired)
		if unsettled.LessThanOrEqual(decimal.Zero) {
			continue
		}
		capSlice := decimal.Min(needCapture, unsettled)
		alloc.captured = alloc.captured.Add(capSlice)
		needCapture = needCapture.Sub(capSlice)
		touchedAllocs[alloc.id] = alloc

		if !alloc.lotID.Valid {
			regularFrozenDelta = regularFrozenDelta.Sub(capSlice)
			continue
		}
		lot, ok := lotMap[alloc.lotID.Int64]
		if !ok {
			return true, fmt.Errorf("expiring lot %d not found for allocation %d", alloc.lotID.Int64, alloc.id)
		}
		if lot.frozen.LessThan(capSlice) {
			return true, fmt.Errorf("expiring lot %d frozen balance insufficient for capture", lot.id)
		}
		lot.frozen = lot.frozen.Sub(capSlice)
		lot.consumed = lot.consumed.Add(capSlice)
		touchedLots[lot.id] = lot
	}

	if needCapture.GreaterThan(decimal.Zero) {
		return true, errBatchImageFrozenInsufficient
	}

	// Pass 2: LIFO release of remaining unsettled excess (alloc_seq DESC).
	for i := len(allocs) - 1; i >= 0; i-- {
		alloc := allocs[i]
		unsettled := alloc.allocated.Sub(alloc.captured).Sub(alloc.released).Sub(alloc.forfeitedExpired)
		if unsettled.LessThanOrEqual(decimal.Zero) {
			continue
		}
		touchedAllocs[alloc.id] = alloc
		if !alloc.lotID.Valid {
			regularFrozenDelta = regularFrozenDelta.Sub(unsettled)
			regularBalanceDelta = regularBalanceDelta.Add(unsettled)
			alloc.released = alloc.released.Add(unsettled)
			continue
		}
		lot, ok := lotMap[alloc.lotID.Int64]
		if !ok {
			return true, fmt.Errorf("expiring lot %d not found for allocation %d", alloc.lotID.Int64, alloc.id)
		}
		if lot.frozen.LessThan(unsettled) {
			return true, fmt.Errorf("expiring lot %d frozen balance insufficient for release", lot.id)
		}
		lot.frozen = lot.frozen.Sub(unsettled)
		if lot.isLive {
			lot.remaining = lot.remaining.Add(unsettled)
			alloc.released = alloc.released.Add(unsettled)
		} else {
			lot.expired = lot.expired.Add(unsettled)
			alloc.forfeitedExpired = alloc.forfeitedExpired.Add(unsettled)
		}
		touchedLots[lot.id] = lot
	}

	if extraOverHold.GreaterThan(decimal.Zero) {
		regularBalanceDelta = regularBalanceDelta.Sub(extraOverHold)
	}
	if regularFrozen.Add(regularFrozenDelta).IsNegative() {
		return true, errBatchImageFrozenInsufficient
	}

	if !regularBalanceDelta.IsZero() || !regularFrozenDelta.IsZero() {
		newRegular := regularBal.Add(regularBalanceDelta)
		newFrozen := regularFrozen.Add(regularFrozenDelta)
		if newFrozen.IsZero() {
			newFrozen = decimal.Zero
		}
		var updateQuery string
		var args []any
		if d == dialect.Postgres {
			updateQuery = `
				UPDATE users
				SET balance = $1,
					frozen_balance = $2,
					updated_at = NOW()
				WHERE id = $3 AND deleted_at IS NULL`
			args = []any{floatFromDec(newRegular), floatFromDec(newFrozen), userID}
		} else {
			updateQuery = `
				UPDATE users
				SET balance = ?,
					frozen_balance = ?,
					updated_at = ?
				WHERE id = ? AND deleted_at IS NULL`
			args = []any{floatFromDec(newRegular), floatFromDec(newFrozen), nowUTC, userID}
		}
		if _, execErr := exec.ExecContext(ctx, updateQuery, args...); execErr != nil {
			return true, execErr
		}
	}

	for _, lot := range touchedLots {
		recomputeSettledLotStatus(lot)
		if updateErr := updateLotAfterReserveOrDeductTx(ctx, exec, d, lot, nowUTC); updateErr != nil {
			return true, updateErr
		}
	}

	for _, alloc := range touchedAllocs {
		if updateErr := updateAllocationAmountsTx(ctx, exec, d, alloc, nowUTC); updateErr != nil {
			return true, updateErr
		}
	}

	return true, nil
}

// ReleaseBatchImageTotalBalanceTx releases a batch image hold in LIFO order:
//   - If no allocation rows exist for batchID, falls back to legacy users.frozen_balance release.
//   - Otherwise reverses allocations in alloc_seq DESC order, returning regular funds to
//     users.balance, live lots to remaining_amount, and forfeiting expired lots.
func ReleaseBatchImageTotalBalanceTx(
	ctx context.Context,
	exec sqlExecutor,
	userID int64,
	holdAmount float64,
	batchID string,
) (bool, error) {
	if exec == nil {
		return false, errors.New("sql executor is nil")
	}
	if userID <= 0 {
		return false, service.ErrUserNotFound
	}
	if err := validateFiniteFloat(holdAmount, "hold_amount"); err != nil {
		return false, err
	}
	if holdAmount < 0 {
		return false, errors.New("hold amount cannot be negative")
	}
	qHold := decFromFloat(holdAmount)
	if qHold.IsZero() {
		return false, nil
	}

	d := expiringSQLDialect(ctx, exec)
	nowUTC := time.Now().UTC()

	regularBal, regularFrozen, err := lockUserBalancesTx(ctx, exec, d, userID)
	if err != nil {
		return false, err
	}

	allocs, err := queryAllocationsTx(ctx, exec, d, userID, service.ExpiringAllocRefBatchImageHold, batchID, false)
	if err != nil {
		return false, err
	}

	if len(allocs) == 0 {
		if regularFrozen.LessThan(qHold) {
			return false, errBatchImageFrozenInsufficient
		}
		newRegular := regularBal.Add(qHold)
		newFrozen := regularFrozen.Sub(qHold)
		if newFrozen.IsZero() {
			newFrozen = decimal.Zero
		}
		var updateQuery string
		var args []any
		if d == dialect.Postgres {
			updateQuery = `
				UPDATE users
				SET balance = $1,
					frozen_balance = $2,
					updated_at = NOW()
				WHERE id = $3 AND deleted_at IS NULL`
			args = []any{floatFromDec(newRegular), floatFromDec(newFrozen), userID}
		} else {
			updateQuery = `
				UPDATE users
				SET balance = ?,
					frozen_balance = ?,
					updated_at = ?
				WHERE id = ? AND deleted_at IS NULL`
			args = []any{floatFromDec(newRegular), floatFromDec(newFrozen), nowUTC, userID}
		}
		res, execErr := exec.ExecContext(ctx, updateQuery, args...)
		if execErr != nil {
			return false, execErr
		}
		affected, affErr := res.RowsAffected()
		if affErr == nil && affected == 0 {
			return false, errBatchImageFrozenInsufficient
		}
		return false, nil
	}

	lotIDs := make([]int64, 0, len(allocs))
	for _, a := range allocs {
		if a.lotID.Valid {
			lotIDs = append(lotIDs, a.lotID.Int64)
		}
	}
	lotMap, err := lockLotsByIDsTx(ctx, exec, d, lotIDs, nowUTC)
	if err != nil {
		return false, err
	}

	regularFrozenDelta := decimal.Zero
	regularBalanceDelta := decimal.Zero
	touchedLots := make(map[int64]*expiringLotTxRow, len(lotMap))
	touchedAllocs := make(map[int64]*expiringAllocTxRow, len(allocs))

	for _, alloc := range allocs {
		unsettled := alloc.allocated.Sub(alloc.captured).Sub(alloc.released).Sub(alloc.forfeitedExpired)
		if unsettled.LessThanOrEqual(decimal.Zero) {
			continue
		}
		touchedAllocs[alloc.id] = alloc
		if !alloc.lotID.Valid {
			regularFrozenDelta = regularFrozenDelta.Sub(unsettled)
			regularBalanceDelta = regularBalanceDelta.Add(unsettled)
			alloc.released = alloc.released.Add(unsettled)
			continue
		}
		lot, ok := lotMap[alloc.lotID.Int64]
		if !ok {
			return true, fmt.Errorf("expiring lot %d not found for allocation %d", alloc.lotID.Int64, alloc.id)
		}
		if lot.frozen.LessThan(unsettled) {
			return true, fmt.Errorf("expiring lot %d frozen balance insufficient for release", lot.id)
		}
		lot.frozen = lot.frozen.Sub(unsettled)
		if lot.isLive {
			lot.remaining = lot.remaining.Add(unsettled)
			alloc.released = alloc.released.Add(unsettled)
		} else {
			lot.expired = lot.expired.Add(unsettled)
			alloc.forfeitedExpired = alloc.forfeitedExpired.Add(unsettled)
		}
		touchedLots[lot.id] = lot
	}

	if regularFrozen.Add(regularFrozenDelta).IsNegative() {
		return true, errBatchImageFrozenInsufficient
	}

	if !regularBalanceDelta.IsZero() || !regularFrozenDelta.IsZero() {
		newRegular := regularBal.Add(regularBalanceDelta)
		newFrozen := regularFrozen.Add(regularFrozenDelta)
		if newFrozen.IsZero() {
			newFrozen = decimal.Zero
		}
		var updateQuery string
		var args []any
		if d == dialect.Postgres {
			updateQuery = `
				UPDATE users
				SET balance = $1,
					frozen_balance = $2,
					updated_at = NOW()
				WHERE id = $3 AND deleted_at IS NULL`
			args = []any{floatFromDec(newRegular), floatFromDec(newFrozen), userID}
		} else {
			updateQuery = `
				UPDATE users
				SET balance = ?,
					frozen_balance = ?,
					updated_at = ?
				WHERE id = ? AND deleted_at IS NULL`
			args = []any{floatFromDec(newRegular), floatFromDec(newFrozen), nowUTC, userID}
		}
		if _, execErr := exec.ExecContext(ctx, updateQuery, args...); execErr != nil {
			return true, execErr
		}
	}

	for _, lot := range touchedLots {
		recomputeSettledLotStatus(lot)
		if updateErr := updateLotAfterReserveOrDeductTx(ctx, exec, d, lot, nowUTC); updateErr != nil {
			return true, updateErr
		}
	}

	for _, alloc := range touchedAllocs {
		if updateErr := updateAllocationAmountsTx(ctx, exec, d, alloc, nowUTC); updateErr != nil {
			return true, updateErr
		}
	}

	return true, nil
}

// RefundPaymentOrderBalanceTx reverses a balance-paid payment order in LIFO order
// (alloc_seq DESC: regular balance first, then original expiring lots preserving expires_at;
// expired lots are forfeited into forfeited_expired_amount without converting to regular balance).
// Returns hasAlloc=false when no allocation rows exist (caller falls back to legacy AdjustBalance).
func RefundPaymentOrderBalanceTx(
	ctx context.Context,
	exec sqlExecutor,
	userID, orderID int64,
	refundAmount float64,
) (bool, RefundAllocResult, error) {
	if exec == nil {
		return false, RefundAllocResult{}, errors.New("sql executor is nil")
	}
	if userID <= 0 {
		return false, RefundAllocResult{}, service.ErrUserNotFound
	}
	if err := validateFiniteFloat(refundAmount, "refund_amount"); err != nil {
		return false, RefundAllocResult{}, err
	}
	if refundAmount < 0 {
		return false, RefundAllocResult{}, errors.New("refund amount cannot be negative")
	}
	if orderID <= 0 {
		return false, RefundAllocResult{}, nil
	}
	qRefund := decFromFloat(refundAmount)
	if qRefund.IsZero() {
		return false, RefundAllocResult{}, nil
	}

	d := expiringSQLDialect(ctx, exec)
	nowUTC := time.Now().UTC()

	if _, _, err := lockUserBalancesTx(ctx, exec, d, userID); err != nil {
		return false, RefundAllocResult{}, err
	}

	refKey := strconv.FormatInt(orderID, 10)
	allocs, err := queryAllocationsTx(ctx, exec, d, userID, service.ExpiringAllocRefPaymentOrder, refKey, false)
	if err != nil {
		return false, RefundAllocResult{}, err
	}
	if len(allocs) == 0 {
		return false, RefundAllocResult{}, nil
	}

	lotIDs := make([]int64, 0, len(allocs))
	for _, a := range allocs {
		if a.lotID.Valid {
			lotIDs = append(lotIDs, a.lotID.Int64)
		}
	}
	lotMap, err := lockLotsByIDsTx(ctx, exec, d, lotIDs, nowUTC)
	if err != nil {
		return true, RefundAllocResult{}, err
	}

	needRefund := qRefund
	regularRefunded := decimal.Zero
	expiringRefunded := decimal.Zero
	expiringForfeited := decimal.Zero
	touchedLots := make(map[int64]*expiringLotTxRow, len(lotMap))
	touchedAllocs := make(map[int64]*expiringAllocTxRow, len(allocs))

	for _, alloc := range allocs {
		if needRefund.LessThanOrEqual(decimal.Zero) {
			break
		}
		refundable := alloc.captured.Sub(alloc.released).Sub(alloc.forfeitedExpired)
		if refundable.LessThanOrEqual(decimal.Zero) {
			continue
		}
		slice := decimal.Min(needRefund, refundable)
		needRefund = needRefund.Sub(slice)
		touchedAllocs[alloc.id] = alloc

		if !alloc.lotID.Valid {
			regularRefunded = regularRefunded.Add(slice)
			alloc.released = alloc.released.Add(slice)
			continue
		}

		lot, ok := lotMap[alloc.lotID.Int64]
		if !ok {
			return true, RefundAllocResult{}, fmt.Errorf("expiring lot %d not found for allocation %d", alloc.lotID.Int64, alloc.id)
		}
		if lot.consumed.LessThan(slice) {
			return true, RefundAllocResult{}, fmt.Errorf("expiring lot %d consumed amount insufficient for refund", lot.id)
		}
		lot.consumed = lot.consumed.Sub(slice)
		if lot.isLive {
			lot.remaining = lot.remaining.Add(slice)
			lot.status = service.ExpiringLotStatusActive
			alloc.released = alloc.released.Add(slice)
			expiringRefunded = expiringRefunded.Add(slice)
		} else {
			lot.expired = lot.expired.Add(slice)
			alloc.forfeitedExpired = alloc.forfeitedExpired.Add(slice)
			expiringForfeited = expiringForfeited.Add(slice)
			if lot.remaining.IsZero() && lot.frozen.IsZero() {
				lot.status = service.ExpiringLotStatusExpired
			}
		}
		touchedLots[lot.id] = lot
	}

	if needRefund.GreaterThan(decimal.Zero) {
		return true, RefundAllocResult{}, fmt.Errorf("refund amount %.8f exceeds refundable allocations for order %d", floatFromDec(qRefund), orderID)
	}

	if regularRefunded.GreaterThan(decimal.Zero) {
		var updateQuery string
		var args []any
		if d == dialect.Postgres {
			updateQuery = `UPDATE users SET balance = balance + $1, updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL`
			args = []any{floatFromDec(regularRefunded), userID}
		} else {
			updateQuery = `UPDATE users SET balance = balance + ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`
			args = []any{floatFromDec(regularRefunded), nowUTC, userID}
		}
		if _, execErr := exec.ExecContext(ctx, updateQuery, args...); execErr != nil {
			return true, RefundAllocResult{}, execErr
		}
	}

	for _, lot := range touchedLots {
		if updateErr := updateLotAfterReserveOrDeductTx(ctx, exec, d, lot, nowUTC); updateErr != nil {
			return true, RefundAllocResult{}, updateErr
		}
	}

	for _, alloc := range touchedAllocs {
		if updateErr := updateAllocationAmountsTx(ctx, exec, d, alloc, nowUTC); updateErr != nil {
			return true, RefundAllocResult{}, updateErr
		}
	}

	return true, RefundAllocResult{
		RegularRefunded:   floatFromDec(regularRefunded),
		ExpiringRefunded:  floatFromDec(expiringRefunded),
		ExpiringForfeited: floatFromDec(expiringForfeited),
	}, nil
}

// GetActiveExpiringSummaryTx reads the user's current active expiring balance summary
// within an existing SQL/Ent executor or transaction.
func GetActiveExpiringSummaryTx(ctx context.Context, exec sqlExecutor, userID int64) (ExpiringBalanceSummary, error) {
	if exec == nil || userID <= 0 {
		return ExpiringBalanceSummary{}, nil
	}
	d := expiringSQLDialect(ctx, exec)
	nowUTC := time.Now().UTC()

	var query string
	if d == dialect.Postgres {
		query = `
			SELECT remaining_amount, frozen_amount, expires_at, (expires_at > NOW()) AS is_live
			FROM user_expiring_balance_lots
			WHERE user_id = $1 AND status = 'active'
			ORDER BY expires_at ASC, id ASC`
	} else {
		query = `
			SELECT remaining_amount, frozen_amount, expires_at
			FROM user_expiring_balance_lots
			WHERE user_id = ? AND status = 'active'
			ORDER BY expires_at ASC, id ASC`
	}

	rows, err := exec.QueryContext(ctx, query, userID)
	if err != nil {
		if isMissingExpiringBalanceTable(err, d) {
			return ExpiringBalanceSummary{}, nil
		}
		return ExpiringBalanceSummary{}, err
	}
	defer func() { _ = rows.Close() }()

	type summaryRow struct {
		remaining decimal.Decimal
		frozen    decimal.Decimal
		expiresAt time.Time
		isLive    bool
	}
	collected := make([]summaryRow, 0)
	for rows.Next() {
		var (
			rem, froz float64
			expAt     flexTime
			isLive    bool
		)
		if d == dialect.Postgres {
			if scanErr := rows.Scan(&rem, &froz, &expAt, &isLive); scanErr != nil {
				return ExpiringBalanceSummary{}, scanErr
			}
		} else {
			if scanErr := rows.Scan(&rem, &froz, &expAt); scanErr != nil {
				return ExpiringBalanceSummary{}, scanErr
			}
			isLive = expAt.Time.After(nowUTC)
		}
		collected = append(collected, summaryRow{
			remaining: decFromFloat(rem),
			frozen:    decFromFloat(froz),
			expiresAt: expAt.Time,
			isLive:    isLive,
		})
	}
	if err := rows.Err(); err != nil {
		return ExpiringBalanceSummary{}, err
	}

	if d != dialect.Postgres && len(collected) > 1 {
		sort.Slice(collected, func(i, j int) bool {
			return collected[i].expiresAt.Before(collected[j].expiresAt)
		})
	}

	activeTotal := decimal.Zero
	frozenTotal := decimal.Zero
	nearestAmount := decimal.Zero
	var nearestExpiresAt *time.Time

	for _, row := range collected {
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

	return ExpiringBalanceSummary{
		ActiveBalance:    floatFromDec(activeTotal),
		FrozenBalance:    floatFromDec(frozenTotal),
		NearestExpiresAt: nearestExpiresAt,
		NearestAmount:    floatFromDec(nearestAmount),
	}, nil
}
