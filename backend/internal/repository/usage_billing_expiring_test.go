package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func setupUsageBillingExpiringSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	db := setupExpiringTestSQLiteDB(t)
	db.SetMaxOpenConns(1)
	ddl := `
		CREATE TABLE usage_billing_dedup (
			id                  INTEGER PRIMARY KEY AUTOINCREMENT,
			request_id          TEXT NOT NULL,
			api_key_id          INTEGER NOT NULL,
			request_fingerprint TEXT NOT NULL,
			created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE UNIQUE INDEX idx_usage_billing_dedup_req_key ON usage_billing_dedup (request_id, api_key_id);
		CREATE TABLE usage_billing_dedup_archive (
			id                  INTEGER PRIMARY KEY AUTOINCREMENT,
			request_id          TEXT NOT NULL,
			api_key_id          INTEGER NOT NULL,
			request_fingerprint TEXT NOT NULL,
			created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE UNIQUE INDEX idx_usage_billing_dedup_arch_req_key ON usage_billing_dedup_archive (request_id, api_key_id);
	`
	_, err := db.Exec(ddl)
	require.NoError(t, err)
	return db
}

func TestUsageBillingApply_FEFOAndCrossPoolAndOverdraft(t *testing.T) {
	ctx := context.Background()
	db := setupUsageBillingExpiringSQLiteDB(t)
	repo := NewUsageBillingRepository(nil, db)

	insertTestUser(t, db, 1, 2.0, 0)
	now := time.Now().UTC()
	expiredLot := insertTestLot(t, db, 1, 100, 10.0, now.Add(-1*time.Minute))
	lot1 := insertTestLot(t, db, 1, 101, 1.0, now.Add(2*time.Hour))
	lot2 := insertTestLot(t, db, 1, 102, 1.5, now.Add(4*time.Hour))

	// First request costs $2.80: consumes lot1 ($1.00) + lot2 ($1.50) + regular ($0.30).
	// Expired lot ($10.00) must be ignored. Remaining total = $1.70.
	res1, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   "req-fefo-1",
		APIKeyID:    10,
		UserID:      1,
		BalanceCost: 2.80,
	})
	require.NoError(t, err)
	require.True(t, res1.Applied)
	require.False(t, res1.BalanceOverdrafted)
	require.NotNil(t, res1.NewBalance)
	require.InDelta(t, 1.70, *res1.NewBalance, 1e-8)

	regBal, _ := readUserBalances(t, db, 1)
	require.InDelta(t, 1.70, regBal, 1e-8)

	remExp, _, consExp, _, statusExp, _ := assertLotAccountingInvariant(t, db, expiredLot)
	require.InDelta(t, 10.0, remExp, 1e-8)
	require.InDelta(t, 0.0, consExp, 1e-8)
	require.Equal(t, service.ExpiringLotStatusActive, statusExp)

	rem1, _, cons1, _, status1, _ := assertLotAccountingInvariant(t, db, lot1)
	require.InDelta(t, 0.0, rem1, 1e-8)
	require.InDelta(t, 1.0, cons1, 1e-8)
	require.Equal(t, service.ExpiringLotStatusExhausted, status1)

	rem2, _, cons2, _, status2, _ := assertLotAccountingInvariant(t, db, lot2)
	require.InDelta(t, 0.0, rem2, 1e-8)
	require.InDelta(t, 1.5, cons2, 1e-8)
	require.Equal(t, service.ExpiringLotStatusExhausted, status2)

	// Replay with identical request_id and fingerprint must be idempotent.
	replay, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   "req-fefo-1",
		APIKeyID:    10,
		UserID:      1,
		BalanceCost: 2.80,
	})
	require.NoError(t, err)
	require.False(t, replay.Applied)

	// Add a new live lot $0.50 -> total available is $1.70 + $0.50 = $2.20.
	// Deduct $3.00 -> overdraft ($2.20 < $3.00): lot3 exhausted, regular becomes -$0.80, NewBalance = -$0.80.
	lot3 := insertTestLot(t, db, 1, 103, 0.50, now.Add(6*time.Hour))
	res2, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   "req-fefo-2",
		APIKeyID:    10,
		UserID:      1,
		BalanceCost: 3.00,
	})
	require.NoError(t, err)
	require.True(t, res2.Applied)
	require.True(t, res2.BalanceOverdrafted)
	require.NotNil(t, res2.NewBalance)
	require.InDelta(t, -0.80, *res2.NewBalance, 1e-8)

	regBal2, _ := readUserBalances(t, db, 1)
	require.InDelta(t, -0.80, regBal2, 1e-8)
	rem3, _, cons3, _, status3, _ := assertLotAccountingInvariant(t, db, lot3)
	require.InDelta(t, 0.0, rem3, 1e-8)
	require.InDelta(t, 0.50, cons3, 1e-8)
	require.Equal(t, service.ExpiringLotStatusExhausted, status3)
}

func TestUsageBillingApply_RefKeyDisambiguationAndLengthBound(t *testing.T) {
	ctx := context.Background()
	db := setupUsageBillingExpiringSQLiteDB(t)
	repo := NewUsageBillingRepository(nil, db)

	insertTestUser(t, db, 1, 10.0, 0)
	insertTestLot(t, db, 1, 201, 5.0, time.Now().UTC().Add(24*time.Hour))

	longReqID := "client:" + strings.Repeat("x", 180)
	res1, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   longReqID,
		APIKeyID:    11,
		UserID:      1,
		BalanceCost: 1.0,
	})
	require.NoError(t, err)
	require.True(t, res1.Applied)

	// Same long RequestID with a different APIKeyID must NOT collide on idx_expiring_alloc_ref_seq
	// or exceed VARCHAR(128).
	res2, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   longReqID,
		APIKeyID:    12,
		UserID:      1,
		BalanceCost: 2.0,
	})
	require.NoError(t, err)
	require.True(t, res2.Applied)
	require.NotNil(t, res2.NewBalance)
	require.InDelta(t, 12.0, *res2.NewBalance, 1e-8)

	rows, err := db.Query(`SELECT DISTINCT ref_key FROM user_expiring_balance_allocations WHERE ref_type = ?`, service.ExpiringAllocRefUsageBilling)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var keys []string
	for rows.Next() {
		var k string
		require.NoError(t, rows.Scan(&k))
		require.LessOrEqual(t, len(k), 128)
		keys = append(keys, k)
	}
	require.NoError(t, rows.Err())
	require.Len(t, keys, 2)
}

func TestUsageBillingApply_RefKeyInjectiveWithZeroAPIKeyID(t *testing.T) {
	require.NotEqual(t, usageBillingAllocRefKey("req#2", 0), usageBillingAllocRefKey("req", 2))
	require.NotEqual(t, usageBillingAllocRefKey("2:req", 0), usageBillingAllocRefKey("req", 2))
	require.Equal(t, "0:req#2", usageBillingAllocRefKey("req#2", 0))
	require.Equal(t, "2:req", usageBillingAllocRefKey("req", 2))
	require.Equal(t, "0:2:req", usageBillingAllocRefKey("2:req", 0))

	batchKeyA := usageBillingBatchAllocRefKey([]*service.UsageBillingCommand{
		{RequestID: "2:req", APIKeyID: 0},
		{RequestID: "other", APIKeyID: 0},
	}, []int{0, 1})
	batchKeyB := usageBillingBatchAllocRefKey([]*service.UsageBillingCommand{
		{RequestID: "req", APIKeyID: 2},
		{RequestID: "other", APIKeyID: 2},
	}, []int{0, 1})
	require.NotEqual(t, batchKeyA, batchKeyB)
	require.Equal(t, "batch:2:0:2:req", batchKeyA)
	require.Equal(t, "batch:2:2:req", batchKeyB)

	ctx := context.Background()
	db := setupUsageBillingExpiringSQLiteDB(t)
	repo := NewUsageBillingRepository(nil, db)

	insertTestUser(t, db, 1, 10.0, 0)
	insertTestLot(t, db, 1, 202, 5.0, time.Now().UTC().Add(24*time.Hour))

	res1, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   "req#2",
		APIKeyID:    0,
		UserID:      1,
		BalanceCost: 1.0,
	})
	require.NoError(t, err)
	require.True(t, res1.Applied)

	res2, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   "req",
		APIKeyID:    2,
		UserID:      1,
		BalanceCost: 1.0,
	})
	require.NoError(t, err)
	require.True(t, res2.Applied)

	res3, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   "2:req",
		APIKeyID:    0,
		UserID:      1,
		BalanceCost: 1.0,
	})
	require.NoError(t, err)
	require.True(t, res3.Applied)
	require.NotNil(t, res3.NewBalance)
	require.InDelta(t, 12.0, *res3.NewBalance, 1e-8)

	rows, err := db.Query(`SELECT DISTINCT ref_key FROM user_expiring_balance_allocations WHERE ref_type = ? ORDER BY ref_key`, service.ExpiringAllocRefUsageBilling)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var keys []string
	for rows.Next() {
		var k string
		require.NoError(t, rows.Scan(&k))
		keys = append(keys, k)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"0:2:req", "0:req#2", "2:req"}, keys)
}

func TestUsageBillingApplyBatch_MatchesSequentialApplyAndHandlesNegativeRegular(t *testing.T) {
	ctx := withExpiringSQLDialect(context.Background(), dialect.SQLite)
	now := time.Now().UTC()

	// Setup two identical DBs: one runs sequential Apply, one runs applyUsageBillingBatchBalance.
	dbSeq := setupUsageBillingExpiringSQLiteDB(t)
	dbBatch := setupUsageBillingExpiringSQLiteDB(t)

	for _, db := range []*sql.DB{dbSeq, dbBatch} {
		// Negative regular balance (-0.20) + two expiring lots (0.30, 0.40) => total available 0.50.
		insertTestUser(t, db, 1, -0.20, 0)
		insertTestLot(t, db, 1, 301, 0.30, now.Add(2*time.Hour))
		insertTestLot(t, db, 1, 302, 0.40, now.Add(4*time.Hour))
		insertTestLot(t, db, 1, 303, 5.00, now.Add(-1*time.Hour)) // expired, ignored
	}

	makeCmds := func() []*service.UsageBillingCommand {
		cmds := []*service.UsageBillingCommand{
			{RequestID: "b-0", APIKeyID: 1, UserID: 1, BalanceCost: 0},
			{RequestID: "b-1", APIKeyID: 1, UserID: 1, BalanceCost: 0.20},
			{RequestID: "b-2", APIKeyID: 1, UserID: 1, BalanceCost: 0.25},
			{RequestID: "b-3", APIKeyID: 1, UserID: 1, BalanceCost: 0.15}, // overdraft (0.05 < 0.15 -> -0.10)
		}
		for _, c := range cmds {
			c.Normalize()
		}
		return cmds
	}

	seqRepo := NewUsageBillingRepository(nil, dbSeq)
	seqCmds := makeCmds()
	seqResults := make([]*service.UsageBillingApplyResult, len(seqCmds))
	for i, cmd := range seqCmds {
		res, err := seqRepo.Apply(ctx, cmd)
		require.NoError(t, err)
		seqResults[i] = res
	}

	batchCmds := makeCmds()
	applied := []bool{true, true, true, true}
	outcomes := make([]service.UsageBillingBatchOutcome, len(batchCmds))
	for i := range outcomes {
		outcomes[i].Result = &service.UsageBillingApplyResult{Applied: true}
	}
	tx, err := dbBatch.BeginTx(ctx, nil)
	require.NoError(t, err)
	err = applyUsageBillingBatchBalance(ctx, tx, 1, batchCmds, applied, outcomes)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	for i := range seqResults {
		if seqResults[i].NewBalance == nil {
			require.Nil(t, outcomes[i].Result.NewBalance, "cmd %d NewBalance should be nil", i)
		} else {
			require.NotNil(t, outcomes[i].Result.NewBalance, "cmd %d NewBalance should not be nil", i)
			require.InDelta(t, *seqResults[i].NewBalance, *outcomes[i].Result.NewBalance, 1e-8, "cmd %d NewBalance mismatch", i)
		}
		require.Equal(t, seqResults[i].BalanceOverdrafted, outcomes[i].Result.BalanceOverdrafted, "cmd %d overdraft mismatch", i)
	}

	seqReg, _ := readUserBalances(t, dbSeq, 1)
	batchReg, _ := readUserBalances(t, dbBatch, 1)
	require.InDelta(t, seqReg, batchReg, 1e-8)

	var seqExpRem, batchExpRem float64
	require.NoError(t, dbSeq.QueryRow(`SELECT COALESCE(SUM(remaining_amount), 0) FROM user_expiring_balance_lots WHERE user_id = 1 AND expires_at > ?`, now).Scan(&seqExpRem))
	require.NoError(t, dbBatch.QueryRow(`SELECT COALESCE(SUM(remaining_amount), 0) FROM user_expiring_balance_lots WHERE user_id = 1 AND expires_at > ?`, now).Scan(&batchExpRem))
	require.InDelta(t, seqExpRem, batchExpRem, 1e-8)
}

func TestUsageBillingApplyBatch_PostgresSQLMockFEFOAndRefKey(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewUsageBillingRepository(nil, db)
	cmds := []*service.UsageBillingCommand{
		{RequestID: "req-batch-1", APIKeyID: 5, UserID: 42, BalanceCost: 1.0},
		{RequestID: "req-batch-2", APIKeyID: 5, UserID: 42, BalanceCost: 2.0},
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)INSERT INTO usage_billing_dedup`).
		WithArgs(pq.Array([]string{"req-batch-1", "req-batch-2"}), pq.Array([]int64{5, 5}), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "request_id", "api_key_id"}).
			AddRow(1, "req-batch-1", 5).
			AddRow(2, "req-batch-2", 5))
	mock.ExpectQuery(`(?s)SELECT a\.request_id, a\.api_key_id, a\.request_fingerprint\s+FROM usage_billing_dedup_archive`).
		WithArgs(pq.Array([]string{"req-batch-1", "req-batch-2"}), pq.Array([]int64{5, 5})).
		WillReturnRows(sqlmock.NewRows([]string{"request_id", "api_key_id", "request_fingerprint"}))

	expAt := time.Now().UTC().Add(2 * time.Hour)
	mock.ExpectQuery(`(?s)SELECT balance, COALESCE\(frozen_balance, 0\) FROM users WHERE id = \$1 AND deleted_at IS NULL FOR UPDATE`).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(5.0, 0.0))
	mock.ExpectQuery(`(?s)SELECT id, remaining_amount, frozen_amount, consumed_amount, expired_amount, expires_at, status\s+FROM user_expiring_balance_lots\s+WHERE user_id = \$1 AND status = 'active' AND remaining_amount > 0 AND expires_at > NOW\(\)\s+ORDER BY expires_at ASC, id ASC\s+FOR UPDATE`).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "remaining_amount", "frozen_amount", "consumed_amount", "expired_amount", "expires_at", "status"}).
			AddRow(int64(99), 1.5, 0.0, 0.0, 0.0, expAt, service.ExpiringLotStatusActive))
	mock.ExpectExec(`(?s)UPDATE user_expiring_balance_lots`).
		WithArgs(0.0, 0.0, 1.5, 0.0, service.ExpiringLotStatusExhausted, int64(99)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)UPDATE users SET balance = balance - \$1, updated_at = NOW\(\) WHERE id = \$2 AND deleted_at IS NULL`).
		WithArgs(1.5, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Expect batch refKey "batch:2:5:req-batch-1"
	mock.ExpectExec(`(?s)INSERT INTO user_expiring_balance_allocations`).
		WithArgs(int64(42), service.ExpiringAllocRefUsageBilling, "batch:2:5:req-batch-1", 1, int64(99), 1.5, 1.5).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`(?s)INSERT INTO user_expiring_balance_allocations`).
		WithArgs(int64(42), service.ExpiringAllocRefUsageBilling, "batch:2:5:req-batch-1", 2, nil, 1.5, 1.5).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	outcomes, err := repo.ApplyBatch(ctx, cmds)
	require.NoError(t, err)
	require.Len(t, outcomes, 2)
	require.NoError(t, outcomes[0].Err)
	require.NoError(t, outcomes[1].Err)
	require.InDelta(t, 5.5, *outcomes[0].Result.NewBalance, 1e-8)
	require.False(t, outcomes[0].Result.BalanceOverdrafted)
	require.InDelta(t, 3.5, *outcomes[1].Result.NewBalance, 1e-8)
	require.False(t, outcomes[1].Result.BalanceOverdrafted)
	require.NoError(t, mock.ExpectationsWereMet())

	longBatchRef := usageBillingBatchAllocRefKey([]*service.UsageBillingCommand{
		{RequestID: strings.Repeat("a", 200), APIKeyID: 99},
		{RequestID: "second", APIKeyID: 99},
	}, []int{0, 1})
	require.LessOrEqual(t, len(longBatchRef), 128)
	require.True(t, strings.HasPrefix(longBatchRef, "batch:sha256:"))
}

func TestUsageBillingBatchImage_HoldCaptureRelease_ActiveAndExpiredLots(t *testing.T) {
	ctx := context.Background()
	db := setupUsageBillingExpiringSQLiteDB(t)
	repo := NewUsageBillingRepository(nil, db)

	insertTestUser(t, db, 1, 2.0, 0)
	now := time.Now().UTC()
	expFuture := now.Add(12 * time.Hour)
	lotActive := insertTestLot(t, db, 1, 401, 3.0, expFuture)

	// 1. Reserve $4.00 -> freezes $3.00 from lotActive + $1.00 from regular users.balance.
	// Remaining total available = $1.00, total frozen = $4.00.
	holdCmd := &service.BatchImageBalanceHoldCommand{
		RequestID:  service.BatchImageHoldRequestID("batch-active"),
		APIKeyID:   7,
		UserID:     1,
		BatchID:    "batch-active",
		HoldAmount: 4.000000004, // quantized to 4.0 after fingerprint
	}
	resHold, err := repo.ReserveBatchImageBalance(ctx, holdCmd)
	require.NoError(t, err)
	require.True(t, resHold.Applied)
	require.InDelta(t, 1.0, *resHold.NewBalance, 1e-8)
	require.InDelta(t, 4.0, *resHold.FrozenBalance, 1e-8)
	require.InDelta(t, 4.0, holdCmd.HoldAmount, 1e-8)

	// 2. Capture $2.20 while lotActive is still live:
	// Pass 1 (FEFO): captures $2.20 from lotActive.
	// Pass 2 (LIFO): releases $1.00 back to users.balance, and $0.80 back to lotActive (preserving expFuture!).
	capRes, err := repo.CaptureBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
		RequestID:    service.BatchImageCaptureRequestID("batch-active"),
		APIKeyID:     7,
		UserID:       1,
		BatchID:      "batch-active",
		HoldAmount:   4.0,
		ActualAmount: 2.2,
	})
	require.NoError(t, err)
	require.True(t, capRes.Applied)
	require.InDelta(t, 2.8, *capRes.NewBalance, 1e-8) // 2.0 regular + 0.80 live lot
	require.InDelta(t, 0.0, *capRes.FrozenBalance, 1e-8)

	regBal, regFrozen := readUserBalances(t, db, 1)
	require.InDelta(t, 2.0, regBal, 1e-8)
	require.InDelta(t, 0.0, regFrozen, 1e-8)
	rem, froz, cons, expAmt, status, gotExp := assertLotAccountingInvariant(t, db, lotActive)
	require.InDelta(t, 0.80, rem, 1e-8)
	require.InDelta(t, 0.0, froz, 1e-8)
	require.InDelta(t, 2.20, cons, 1e-8)
	require.InDelta(t, 0.0, expAmt, 1e-8)
	require.Equal(t, service.ExpiringLotStatusActive, status)
	require.Equal(t, expFuture.Unix(), gotExp.Unix())

	// 3. Cross-expiration test: reserve from a lot ($0.80) + regular ($1.20) = $2.00 hold,
	// then expire the lot before Release -> regular ($1.20) returns to users.balance,
	// expired lot ($0.80) is forfeited into expired_amount & forfeited_expired_amount!
	holdCross := &service.BatchImageBalanceHoldCommand{
		RequestID:  service.BatchImageHoldRequestID("batch-cross-expire"),
		APIKeyID:   7,
		UserID:     1,
		BatchID:    "batch-cross-expire",
		HoldAmount: 2.0,
	}
	resCross, err := repo.ReserveBatchImageBalance(ctx, holdCross)
	require.NoError(t, err)
	require.InDelta(t, 0.8, *resCross.NewBalance, 1e-8)
	require.InDelta(t, 2.0, *resCross.FrozenBalance, 1e-8)

	// Expire lotActive in DB.
	_, err = db.Exec(`UPDATE user_expiring_balance_lots SET expires_at = ? WHERE id = ?`, now.Add(-1*time.Minute), lotActive)
	require.NoError(t, err)

	relCross, err := repo.ReleaseBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
		RequestID:  service.BatchImageReleaseRequestID("batch-cross-expire"),
		APIKeyID:   7,
		UserID:     1,
		BatchID:    "batch-cross-expire",
		HoldAmount: 2.0,
	})
	require.NoError(t, err)
	require.True(t, relCross.Applied)
	require.InDelta(t, 2.0, *relCross.NewBalance, 1e-8) // ONLY regular 2.0, NOT 2.80!
	require.InDelta(t, 0.0, *relCross.FrozenBalance, 1e-8)

	regAfter, frozAfter := readUserBalances(t, db, 1)
	require.InDelta(t, 2.0, regAfter, 1e-8)
	require.InDelta(t, 0.0, frozAfter, 1e-8)
	rem, froz, cons, expAmt, status, _ = assertLotAccountingInvariant(t, db, lotActive)
	require.InDelta(t, 0.0, rem, 1e-8)
	require.InDelta(t, 0.0, froz, 1e-8)
	require.InDelta(t, 2.20, cons, 1e-8)
	require.InDelta(t, 0.80, expAmt, 1e-8)
	require.Equal(t, service.ExpiringLotStatusExpired, status)

	var forfeited float64
	require.NoError(t, db.QueryRow(`SELECT forfeited_expired_amount FROM user_expiring_balance_allocations WHERE ref_type = ? AND ref_key = ? AND lot_id = ?`,
		service.ExpiringAllocRefBatchImageHold, "batch-cross-expire", lotActive).Scan(&forfeited))
	require.InDelta(t, 0.80, forfeited, 1e-8)

	// 4. Phantom release (never held) must be skipped cleanly.
	phantom, err := repo.ReleaseBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
		RequestID:  service.BatchImageReleaseRequestID("batch-never-held"),
		APIKeyID:   7,
		UserID:     1,
		BatchID:    "batch-never-held",
		HoldAmount: 1.0,
	})
	require.NoError(t, err)
	require.True(t, phantom.Applied)
	require.Nil(t, phantom.NewBalance)
	require.Nil(t, phantom.FrozenBalance)
	regFinal, _ := readUserBalances(t, db, 1)
	require.InDelta(t, 2.0, regFinal, 1e-8)

	// 5. Legacy regular-only hold/capture (zero allocation rows written).
	legacyHold, err := repo.ReserveBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
		RequestID:  service.BatchImageHoldRequestID("batch-legacy"),
		APIKeyID:   7,
		UserID:     1,
		BatchID:    "batch-legacy",
		HoldAmount: 1.0,
	})
	require.NoError(t, err)
	require.InDelta(t, 1.0, *legacyHold.NewBalance, 1e-8)
	require.InDelta(t, 1.0, *legacyHold.FrozenBalance, 1e-8)

	var allocCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM user_expiring_balance_allocations WHERE ref_key = 'batch-legacy'`).Scan(&allocCount))
	require.Zero(t, allocCount)

	legacyCap, err := repo.CaptureBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
		RequestID:    service.BatchImageCaptureRequestID("batch-legacy"),
		APIKeyID:     7,
		UserID:       1,
		BatchID:      "batch-legacy",
		HoldAmount:   1.0,
		ActualAmount: 0.4,
	})
	require.NoError(t, err)
	require.InDelta(t, 1.6, *legacyCap.NewBalance, 1e-8)
	require.InDelta(t, 0.0, *legacyCap.FrozenBalance, 1e-8)
}
