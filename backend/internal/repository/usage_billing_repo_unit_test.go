//go:build unit

package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	lockUserBalancesSQL         = `(?s)SELECT balance, COALESCE\(frozen_balance, 0\) FROM users WHERE id = \$1 AND deleted_at IS NULL FOR UPDATE`
	selectActiveLiveLotsSQL     = `(?s)SELECT id, remaining_amount, frozen_amount, consumed_amount, expired_amount, expires_at, status\s+FROM user_expiring_balance_lots\s+WHERE user_id = \$1 AND status = 'active' AND remaining_amount > 0 AND expires_at > NOW\(\)\s+ORDER BY expires_at ASC, id ASC\s+FOR UPDATE`
	deductRegularUserBalanceSQL = `(?s)UPDATE users SET balance = balance - \$1, updated_at = NOW\(\) WHERE id = \$2 AND deleted_at IS NULL`
	updateUserBalanceFrozenSQL  = `(?s)UPDATE users\s+SET balance = \$1,\s+frozen_balance = \$2,\s+updated_at = NOW\(\)\s+WHERE id = \$3 AND deleted_at IS NULL`
	selectBatchImageAllocsSQL   = `(?s)SELECT id, alloc_seq, lot_id, allocated_amount, captured_amount, released_amount, forfeited_expired_amount\s+FROM user_expiring_balance_allocations\s+WHERE user_id = \$1 AND ref_type = \$2 AND ref_key = \$3\s+ORDER BY alloc_seq (ASC|DESC) FOR UPDATE`
	selectExpiringSummarySQL    = `(?s)SELECT remaining_amount, frozen_amount, expires_at, \(expires_at > NOW\(\)\) AS is_live\s+FROM user_expiring_balance_lots\s+WHERE user_id = \$1 AND status = 'active'\s+ORDER BY expires_at ASC, id ASC`
	apiKeyQuotaIncrementSQL     = `(?s)UPDATE api_keys\s+SET quota_used = quota_used \+ \$1,.*WHERE id = \$2 AND deleted_at IS NULL\s+RETURNING`
	apiKeyRateLimitIncrementSQL = `(?s)UPDATE api_keys SET\s+usage_5h = .*WHERE id = \$2 AND deleted_at IS NULL`
	accountQuotaIncrementSQL    = `(?s)UPDATE accounts SET extra = .*WHERE id = \$2 AND deleted_at IS NULL.*RETURNING`
)

func expectRegularOnlyDeductSQL(mock sqlmock.Sqlmock, userID int64, startBal, deductAmt float64) {
	mock.ExpectQuery(lockUserBalancesSQL).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(startBal, 0.0))
	mock.ExpectQuery(selectActiveLiveLotsSQL).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "remaining_amount", "frozen_amount", "consumed_amount", "expired_amount", "expires_at", "status"}))
	mock.ExpectExec(deductRegularUserBalanceSQL).
		WithArgs(deductAmt, userID).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectPostHoldBalanceQuerySQL(mock sqlmock.Sqlmock, userID int64, bal, frozen float64) {
	mock.ExpectQuery(lockUserBalancesSQL).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(bal, frozen))
	mock.ExpectQuery(selectExpiringSummarySQL).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"remaining_amount", "frozen_amount", "expires_at", "is_live"}))
}

func TestDeductUsageBillingBalance_UsesSufficientBalanceGuard(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	expectRegularOnlyDeductSQL(mock, 42, 10.0, 2.5)
	mock.ExpectCommit()

	newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, 42, 2.5, "req-1", 7)
	require.NoError(t, err)
	require.True(t, sufficient)
	require.InDelta(t, 7.5, newBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeductUsageBillingBalance_RecordsOverdraftWhenGuardMisses(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	expectRegularOnlyDeductSQL(mock, 42, 5.0, 10.0)
	mock.ExpectCommit()

	newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, 42, 10, "req-2", 7)
	require.NoError(t, err)
	require.False(t, sufficient)
	require.InDelta(t, -5.0, newBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_FlagsBalanceOverdraft(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	expectRegularOnlyDeductSQL(mock, 42, 5.0, 10.0)
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:      42,
		BalanceCost: 10,
	}, result)
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, -5.0, *result.NewBalance, 0.000001)
	require.True(t, result.BalanceOverdrafted)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_DeletedAPIKeyStillBillsBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	expectRegularOnlyDeductSQL(mock, 42, 100.0, 10.0)
	mock.ExpectQuery(apiKeyQuotaIncrementSQL).
		WithArgs(10.0, int64(7), service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(apiKeyRateLimitIncrementSQL).
		WithArgs(10.0, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:              42,
		APIKeyID:            7,
		BalanceCost:         10,
		APIKeyQuotaCost:     10,
		APIKeyRateLimitCost: 10,
	}, result)
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 90.0, *result.NewBalance, 0.000001)
	require.False(t, result.APIKeyQuotaExhausted)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_DeletedAccountStillBillsBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	expectRegularOnlyDeductSQL(mock, 42, 100.0, 10.0)
	mock.ExpectQuery(accountQuotaIncrementSQL).
		WithArgs(4.0, int64(9)).
		WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns))
	mock.ExpectQuery(accountQuotaIncrementSQL).
		WithArgs(4.0, int64(9)).
		WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns))
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:           42,
		AccountID:        9,
		AccountType:      service.AccountTypeServiceAccount,
		BalanceCost:      10,
		AccountQuotaCost: 4,
	}, result)
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 90.0, *result.NewBalance, 0.000001)
	require.Nil(t, result.QuotaState)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_APIKeyCounterErrorStillFails(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectExec(apiKeyRateLimitIncrementSQL).
		WithArgs(10.0, int64(7)).
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:              42,
		APIKeyID:            7,
		APIKeyRateLimitCost: 10,
	}, &service.UsageBillingApplyResult{Applied: true})
	require.ErrorIs(t, err, sql.ErrConnDone)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeductUsageBillingBalance_ReturnsUserNotFoundWhenNoUserUpdated(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(lockUserBalancesSQL).
		WithArgs(int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	_, _, err = deductUsageBillingBalance(ctx, tx, 42, 10, "", 0)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

var accountQuotaStateColumns = []string{"quota_used", "quota_limit", "quota_daily_used", "quota_daily_limit", "quota_weekly_used", "quota_weekly_limit"}

func TestAccountQuotaIncrementSQLs_GuardOmittedDimensions(t *testing.T) {
	for _, daily := range []bool{false, true} {
		for _, weekly := range []bool{false, true} {
			query := accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(daily, weekly)]
			set, where, ok := strings.Cut(query, "WHERE ")
			require.True(t, ok)
			require.Equal(t, !daily || !weekly, strings.Contains(where, accountQuotaExtraIsObjectCond), "daily=%v weekly=%v", daily, weekly)
			require.Equal(t, daily, strings.Contains(set, "'quota_daily_used'"), "daily=%v weekly=%v", daily, weekly)
			require.Equal(t, weekly, strings.Contains(set, "'quota_weekly_used'"), "daily=%v weekly=%v", daily, weekly)
			require.Equal(t, !daily, strings.Contains(where, accountQuotaDailyLimitUnsetCond), "daily=%v weekly=%v", daily, weekly)
			require.Equal(t, !weekly, strings.Contains(where, accountQuotaWeeklyLimitUnsetCond), "daily=%v weekly=%v", daily, weekly)
		}
	}
}

func TestIncrementUsageBillingAccountQuota_UsesStatementMatchingHints(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(true, false)]).
		WithArgs(2.5, int64(9)).
		WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns).AddRow(12.5, 100.0, 2.5, 50.0, 0.0, 0.0))
	mock.ExpectCommit()

	state, err := incrementUsageBillingAccountQuota(ctx, tx, 9, 2.5, true, false)
	require.NoError(t, err)
	require.NotNil(t, state)
	require.InDelta(t, 12.5, state.TotalUsed, 0.000001)
	require.InDelta(t, 2.5, state.DailyUsed, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIncrementUsageBillingAccountQuota_FallsBackToFullStatementWhenGuardMisses(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(false, false)]).
		WithArgs(2.5, int64(9)).
		WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns))
	mock.ExpectQuery(accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(true, true)]).
		WithArgs(2.5, int64(9)).
		WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns).AddRow(2.5, 0.0, 0.0, 0.0, 2.5, 20.0))
	mock.ExpectCommit()

	state, err := incrementUsageBillingAccountQuota(ctx, tx, 9, 2.5, false, false)
	require.NoError(t, err)
	require.NotNil(t, state)
	require.InDelta(t, 2.5, state.WeeklyUsed, 0.000001)
	require.InDelta(t, 20.0, state.WeeklyLimit, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIncrementUsageBillingAccountQuota_ReturnsNotFoundWhenFullStatementMisses(t *testing.T) {
	for _, hints := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		ctx := context.Background()
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, err)

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(hints[0], hints[1])]).
			WithArgs(2.5, int64(9)).
			WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns))
		if !(hints[0] && hints[1]) {
			mock.ExpectQuery(accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(true, true)]).
				WithArgs(2.5, int64(9)).
				WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns))
		}
		mock.ExpectRollback()

		state, err := incrementUsageBillingAccountQuota(ctx, tx, 9, 2.5, hints[0], hints[1])
		require.ErrorIs(t, err, service.ErrAccountNotFound, "hints=%v", hints)
		require.Nil(t, state)
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet(), "hints=%v", hints)
		_ = db.Close()
	}
}

func TestReserveUsageBillingBatchImageBalance_MovesAvailableToFrozen(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(lockUserBalancesSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(10.0, 0.0))
	mock.ExpectQuery(selectActiveLiveLotsSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "remaining_amount", "frozen_amount", "consumed_amount", "expired_amount", "expires_at", "status"}))
	mock.ExpectExec(updateUserBalanceFrozenSQL).
		WithArgs(7.5, 2.5, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectPostHoldBalanceQuerySQL(mock, 42, 7.5, 2.5)
	mock.ExpectCommit()

	result, err := reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 2.5})
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.NotNil(t, result.FrozenBalance)
	require.InDelta(t, 7.5, *result.NewBalance, 0.000001)
	require.InDelta(t, 2.5, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveUsageBillingBatchImageBalance_InsufficientBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(lockUserBalancesSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(5.0, 0.0))
	mock.ExpectQuery(selectActiveLiveLotsSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "remaining_amount", "frozen_amount", "consumed_amount", "expired_amount", "expires_at", "status"}))
	mock.ExpectRollback()

	_, err = reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 10})
	require.ErrorIs(t, err, service.ErrBatchImageInsufficientBalance)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureUsageBillingBatchImageBalance_ReleasesRemainder(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(lockUserBalancesSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(9.0, 1.0))
	mock.ExpectQuery(selectBatchImageAllocsSQL).
		WithArgs(int64(42), service.ExpiringAllocRefBatchImageHold, "").
		WillReturnRows(sqlmock.NewRows([]string{"id", "alloc_seq", "lot_id", "allocated_amount", "captured_amount", "released_amount", "forfeited_expired_amount"}))
	mock.ExpectExec(updateUserBalanceFrozenSQL).
		WithArgs(9.75, 0.0, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectPostHoldBalanceQuerySQL(mock, 42, 9.75, 0.0)
	mock.ExpectCommit()

	result, err := captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 1, ActualAmount: 0.25})
	require.NoError(t, err)
	require.InDelta(t, 9.75, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureUsageBillingBatchImageBalance_RejectsActualCostOverHold(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectRollback()

	_, err = captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 0.5, ActualAmount: 1})
	require.ErrorIs(t, err, service.ErrBatchImageSettlementCostExceedsHold)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseUsageBillingBatchImageBalance_ReturnsFrozenToAvailable(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_release"), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectQuery(lockUserBalancesSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(9.0, 1.0))
	mock.ExpectQuery(selectBatchImageAllocsSQL).
		WithArgs(int64(42), service.ExpiringAllocRefBatchImageHold, "imgbatch_release").
		WillReturnRows(sqlmock.NewRows([]string{"id", "alloc_seq", "lot_id", "allocated_amount", "captured_amount", "released_amount", "forfeited_expired_amount"}))
	mock.ExpectExec(updateUserBalanceFrozenSQL).
		WithArgs(10.0, 0.0, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectPostHoldBalanceQuerySQL(mock, 42, 10.0, 0.0)
	mock.ExpectCommit()

	result, err := releaseUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, APIKeyID: 7, BatchID: "imgbatch_release", HoldAmount: 1})
	require.NoError(t, err)
	require.InDelta(t, 10.0, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseUsageBillingBatchImageBalance_SkipsWhenHoldNeverReserved(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// dedup 与归档表均无 hold claim：说明该 job 从未成功冻结，
	// 释放必须跳过，不得从他人冻结资金池中凭空生成余额。
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup_archive\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()

	result, err := releaseUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, APIKeyID: 7, BatchID: "imgbatch_phantom", HoldAmount: 1})
	require.NoError(t, err)
	require.Nil(t, result.NewBalance)
	require.Nil(t, result.FrozenBalance)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}
