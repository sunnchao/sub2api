package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
	_ "modernc.org/sqlite"
)

func setupExpiringTestSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ddl := `
		PRAGMA foreign_keys = ON;
		CREATE TABLE users (
			id             INTEGER PRIMARY KEY,
			balance        REAL NOT NULL DEFAULT 0,
			frozen_balance REAL NOT NULL DEFAULT 0,
			deleted_at     DATETIME NULL,
			updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE user_expiring_balance_lots (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			source_type      TEXT NOT NULL DEFAULT 'checkin',
			source_id        INTEGER NOT NULL,
			initial_amount   REAL NOT NULL,
			remaining_amount REAL NOT NULL,
			frozen_amount    REAL NOT NULL DEFAULT 0,
			consumed_amount  REAL NOT NULL DEFAULT 0,
			expired_amount   REAL NOT NULL DEFAULT 0,
			expires_at       DATETIME NOT NULL,
			status           TEXT NOT NULL DEFAULT 'active',
			created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE UNIQUE INDEX idx_expiring_lots_source ON user_expiring_balance_lots (source_type, source_id);
		CREATE TABLE user_expiring_balance_allocations (
			id                       INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id                  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			ref_type                 TEXT NOT NULL,
			ref_key                  TEXT NOT NULL,
			alloc_seq                INTEGER NOT NULL,
			lot_id                   INTEGER NULL REFERENCES user_expiring_balance_lots(id) ON DELETE RESTRICT,
			allocated_amount         REAL NOT NULL,
			captured_amount          REAL NOT NULL DEFAULT 0,
			released_amount          REAL NOT NULL DEFAULT 0,
			forfeited_expired_amount REAL NOT NULL DEFAULT 0,
			created_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE UNIQUE INDEX idx_expiring_alloc_ref_seq ON user_expiring_balance_allocations (ref_type, ref_key, alloc_seq);
	`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("create test sqlite schema: %v", err)
	}
	return db
}

func insertTestUser(t *testing.T, db *sql.DB, id int64, balance, frozen float64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users (id, balance, frozen_balance) VALUES (?, ?, ?)`,
		id, balance, frozen,
	); err != nil {
		t.Fatalf("insert test user %d: %v", id, err)
	}
}

func insertTestLot(t *testing.T, db *sql.DB, userID, sourceID int64, amount float64, expiresAt time.Time) int64 {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO user_expiring_balance_lots
			(user_id, source_type, source_id, initial_amount, remaining_amount, frozen_amount, consumed_amount, expired_amount, expires_at, status, created_at, updated_at)
		VALUES (?, 'checkin', ?, ?, ?, 0, 0, 0, ?, 'active', ?, ?)`,
		userID, sourceID, amount, amount, expiresAt.UTC(), time.Now().UTC(), time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("insert test lot source=%d: %v", sourceID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("lot lastInsertId: %v", err)
	}
	return id
}

func assertLotAccountingInvariant(t *testing.T, db *sql.DB, lotID int64) (rem, froz, cons, exp float64, status string, expiresAt time.Time) {
	t.Helper()
	var initAmt float64
	var expTime flexTime
	err := db.QueryRow(`
		SELECT initial_amount, remaining_amount, frozen_amount, consumed_amount, expired_amount, status, expires_at
		FROM user_expiring_balance_lots
		WHERE id = ?`, lotID).Scan(&initAmt, &rem, &froz, &cons, &exp, &status, &expTime)
	if err != nil {
		t.Fatalf("query lot %d: %v", lotID, err)
	}
	dInit := decFromFloat(initAmt)
	dSum := decFromFloat(rem).Add(decFromFloat(froz)).Add(decFromFloat(cons)).Add(decFromFloat(exp))
	if !dInit.Equal(dSum) {
		t.Fatalf("lot %d accounting invariant violated: initial=%s != rem(%s)+froz(%s)+cons(%s)+exp(%s)=%s",
			lotID, dInit, decFromFloat(rem), decFromFloat(froz), decFromFloat(cons), decFromFloat(exp), dSum)
	}
	return rem, froz, cons, exp, status, expTime.Time
}

func readUserBalances(t *testing.T, db *sql.DB, userID int64) (balance, frozen float64) {
	t.Helper()
	if err := db.QueryRow(`SELECT balance, frozen_balance FROM users WHERE id = ?`, userID).Scan(&balance, &frozen); err != nil {
		t.Fatalf("read user %d balances: %v", userID, err)
	}
	return balance, frozen
}

func TestDeductUserTotalBalanceTx_FEFOAndCrossPool(t *testing.T) {
	db := setupExpiringTestSQLiteDB(t)
	repo := NewExpiringBalanceRepository(nil, db)
	ctx := context.Background()

	insertTestUser(t, db, 1, 4.0, 0)
	now := time.Now().UTC()
	lot1 := insertTestLot(t, db, 1, 101, 2.0, now.Add(24*time.Hour))
	lot2 := insertTestLot(t, db, 1, 102, 3.0, now.Add(48*time.Hour))
	lot3 := insertTestLot(t, db, 1, 103, 5.0, now.Add(72*time.Hour))

	// 1. Deduct 4.5 -> exhausts lot1 (2.0) and takes 2.5 from lot2.
	res1, err := repo.DeductTotalBalance(ctx, 1, 4.5, false, service.ExpiringAllocRefUsageBilling, "req-1")
	if err != nil {
		t.Fatalf("deduct 4.5: %v", err)
	}
	if !res1.Sufficient || res1.ExpiringDeducted != 4.5 || res1.RegularDeducted != 0 ||
		res1.NewExpiringBalance != 5.5 || res1.NewRegularBalance != 4.0 || res1.NewTotalBalance != 9.5 {
		t.Fatalf("unexpected res1: %+v", res1)
	}

	rem1, _, cons1, _, st1, _ := assertLotAccountingInvariant(t, db, lot1)
	if rem1 != 0 || cons1 != 2.0 || st1 != service.ExpiringLotStatusExhausted {
		t.Fatalf("unexpected lot1 state: rem=%v cons=%v status=%s", rem1, cons1, st1)
	}
	rem2, _, cons2, _, st2, _ := assertLotAccountingInvariant(t, db, lot2)
	if rem2 != 0.5 || cons2 != 2.5 || st2 != service.ExpiringLotStatusActive {
		t.Fatalf("unexpected lot2 state: rem=%v cons=%v status=%s", rem2, cons2, st2)
	}

	// 2. Deduct 8.0 -> takes remaining 0.5 from lot2 + 5.0 from lot3 + 2.5 from regular balance.
	res2, err := repo.DeductTotalBalance(ctx, 1, 8.0, false, service.ExpiringAllocRefPaymentOrder, "1001")
	if err != nil {
		t.Fatalf("deduct 8.0: %v", err)
	}
	if !res2.Sufficient || res2.ExpiringDeducted != 5.5 || res2.RegularDeducted != 2.5 ||
		res2.NewExpiringBalance != 0 || res2.NewRegularBalance != 1.5 || res2.NewTotalBalance != 1.5 {
		t.Fatalf("unexpected res2: %+v", res2)
	}
	assertLotAccountingInvariant(t, db, lot2)
	rem3, _, cons3, _, st3, _ := assertLotAccountingInvariant(t, db, lot3)
	if rem3 != 0 || cons3 != 5.0 || st3 != service.ExpiringLotStatusExhausted {
		t.Fatalf("unexpected lot3 state: rem=%v cons=%v status=%s", rem3, cons3, st3)
	}
	bal, _ := readUserBalances(t, db, 1)
	if bal != 1.5 {
		t.Fatalf("expected regular balance 1.5, got %v", bal)
	}

	// Verify allocation rows for payment_order 1001 preserve FEFO order followed by lot_id=NULL.
	rows, err := db.Query(`
		SELECT alloc_seq, lot_id, allocated_amount, captured_amount
		FROM user_expiring_balance_allocations
		WHERE ref_type = ? AND ref_key = ?
		ORDER BY alloc_seq ASC`, service.ExpiringAllocRefPaymentOrder, "1001")
	if err != nil {
		t.Fatalf("query allocations: %v", err)
	}
	defer func() { _ = rows.Close() }()
	type allocCheck struct {
		seq   int
		lotID sql.NullInt64
		alloc float64
		capt  float64
	}
	var got []allocCheck
	for rows.Next() {
		var a allocCheck
		if err := rows.Scan(&a.seq, &a.lotID, &a.alloc, &a.capt); err != nil {
			t.Fatalf("scan alloc: %v", err)
		}
		got = append(got, a)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 allocation rows, got %d: %+v", len(got), got)
	}
	if !got[0].lotID.Valid || got[0].lotID.Int64 != lot2 || got[0].alloc != 0.5 || got[0].capt != 0.5 ||
		!got[1].lotID.Valid || got[1].lotID.Int64 != lot3 || got[1].alloc != 5.0 || got[1].capt != 5.0 ||
		got[2].lotID.Valid || got[2].alloc != 2.5 || got[2].capt != 2.5 {
		t.Fatalf("unexpected allocations: %+v", got)
	}
}

func TestDeductUserTotalBalanceTx_OverdraftAndStrictExpiry(t *testing.T) {
	db := setupExpiringTestSQLiteDB(t)
	repo := NewExpiringBalanceRepository(nil, db)
	ctx := context.Background()

	insertTestUser(t, db, 2, 1.0, 0)
	now := time.Now().UTC()
	expiredLot := insertTestLot(t, db, 2, 201, 5.0, now.Add(-1*time.Second))
	liveLot := insertTestLot(t, db, 2, 202, 1.5, now.Add(1*time.Hour))

	// Expired un-archived lot must be ignored by summary and deduction.
	sum, err := repo.GetActiveExpiringSummary(ctx, 2)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.ActiveBalance != 1.5 || sum.NearestAmount != 1.5 {
		t.Fatalf("expected active 1.5 ignoring expired lot, got %+v", sum)
	}

	// Non-overdraft rejection must leave balances untouched.
	_, err = repo.DeductTotalBalance(ctx, 2, 3.0, false, service.ExpiringAllocRefPaymentOrder, "2001")
	if !errors.Is(err, service.ErrBalanceNegative) {
		t.Fatalf("expected ErrBalanceNegative, got %v", err)
	}
	remExp, _, _, _, stExp, _ := assertLotAccountingInvariant(t, db, expiredLot)
	remLive, _, _, _, stLive, _ := assertLotAccountingInvariant(t, db, liveLot)
	if remExp != 5.0 || stExp != service.ExpiringLotStatusActive || remLive != 1.5 || stLive != service.ExpiringLotStatusActive {
		t.Fatalf("expected rollback on insufficient funds")
	}

	// Overdraft allowed: exhausts liveLot (1.5) and pushes regular balance negative (-0.5), ignores expiredLot.
	res, err := repo.DeductTotalBalance(ctx, 2, 3.0, true, service.ExpiringAllocRefUsageBilling, "req-overdraft")
	if err != nil {
		t.Fatalf("overdraft deduct: %v", err)
	}
	if res.Sufficient || res.ExpiringDeducted != 1.5 || res.RegularDeducted != 1.5 ||
		res.NewRegularBalance != -0.5 || res.NewExpiringBalance != 0 || res.NewTotalBalance != -0.5 {
		t.Fatalf("unexpected overdraft result: %+v", res)
	}
	assertLotAccountingInvariant(t, db, expiredLot)
	assertLotAccountingInvariant(t, db, liveLot)
}

func TestDeductUserTotalBalanceTx_EightDecimalPrecision(t *testing.T) {
	db := setupExpiringTestSQLiteDB(t)
	repo := NewExpiringBalanceRepository(nil, db)
	ctx := context.Background()

	insertTestUser(t, db, 3, 1.0, 0)
	now := time.Now().UTC()
	lot1 := insertTestLot(t, db, 3, 301, 0.03333333, now.Add(time.Hour))
	lot2 := insertTestLot(t, db, 3, 302, 0.00007813, now.Add(2*time.Hour))

	res, err := repo.DeductTotalBalance(ctx, 3, 0.03333333+0.000078125, false, service.ExpiringAllocRefUsageBilling, "req-prec")
	if err != nil {
		t.Fatalf("precision deduct: %v", err)
	}
	expectedCost := service.QuantizeUsageBillingAmount(0.03333333 + 0.000078125)
	if !decimal.NewFromFloat(res.ExpiringDeducted).Equal(decimal.NewFromFloat(expectedCost)) {
		t.Fatalf("expected expiring deducted %v, got %v", expectedCost, res.ExpiringDeducted)
	}
	assertLotAccountingInvariant(t, db, lot1)
	assertLotAccountingInvariant(t, db, lot2)
}

func TestBatchImageHoldCaptureRelease_PureRegularAndMixedAntiLaundering(t *testing.T) {
	db := setupExpiringTestSQLiteDB(t)
	ctx := withExpiringSQLDialect(context.Background(), dialect.SQLite)
	now := time.Now().UTC()

	// Case 1: Pure regular balance hold -> zero allocations written, legacy frozen_balance path.
	insertTestUser(t, db, 10, 10.0, 0)
	tx1, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	if err := ReserveBatchImageTotalBalanceTx(ctx, tx1, 10, 4.0, "batch-reg"); err != nil {
		t.Fatalf("reserve pure regular: %v", err)
	}
	_ = tx1.Commit()

	var allocCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_expiring_balance_allocations WHERE ref_key = 'batch-reg'`).Scan(&allocCount); err != nil {
		t.Fatalf("count allocs: %v", err)
	}
	if allocCount != 0 {
		t.Fatalf("expected 0 allocations for pure regular hold, got %d", allocCount)
	}

	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx2: %v", err)
	}
	expUsed, err := CaptureBatchImageTotalBalanceTx(ctx, tx2, 10, 4.0, 2.5, "batch-reg")
	if err != nil || expUsed {
		t.Fatalf("capture pure regular: expUsed=%v err=%v", expUsed, err)
	}
	_ = tx2.Commit()
	bal10, froz10 := readUserBalances(t, db, 10)
	if bal10 != 7.5 || froz10 != 0 {
		t.Fatalf("expected user 10 balance=7.5 frozen=0, got bal=%v froz=%v", bal10, froz10)
	}

	// Case 2: Mixed hold (Lot1=$2.0, Lot2=$3.0, Regular=$2.0 -> total hold $7.0).
	// Expire Lot1 while frozen, keep Lot2 live, settle actual=$3.5.
	insertTestUser(t, db, 11, 5.0, 0)
	lot1OrigExp := now.Add(1 * time.Hour)
	lot2OrigExp := now.Add(2 * time.Hour)
	lot1 := insertTestLot(t, db, 11, 1101, 2.0, lot1OrigExp)
	lot2 := insertTestLot(t, db, 11, 1102, 3.0, lot2OrigExp)

	tx3, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx3: %v", err)
	}
	if err := ReserveBatchImageTotalBalanceTx(ctx, tx3, 11, 7.0, "batch-mix-1"); err != nil {
		t.Fatalf("reserve mixed: %v", err)
	}
	_ = tx3.Commit()

	// Expire lot1 before capture.
	if _, err := db.Exec(`UPDATE user_expiring_balance_lots SET expires_at = ? WHERE id = ?`, now.Add(-time.Minute), lot1); err != nil {
		t.Fatalf("expire lot1: %v", err)
	}

	// Over-hold capture must return ErrBatchImageSettlementCostExceedsHold.
	txErr, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin txErr: %v", err)
	}
	_, capErr := CaptureBatchImageTotalBalanceTx(ctx, txErr, 11, 7.0, 7.1, "batch-mix-1")
	_ = txErr.Rollback()
	if !errors.Is(capErr, service.ErrBatchImageSettlementCostExceedsHold) {
		t.Fatalf("expected ErrBatchImageSettlementCostExceedsHold, got %v", capErr)
	}

	// Settle actual=3.5: FEFO captures Lot1(2.0) + Lot2(1.5). LIFO releases Regular(2.0) + Lot2(1.5).
	tx4, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx4: %v", err)
	}
	expUsed, err = CaptureBatchImageTotalBalanceTx(ctx, tx4, 11, 7.0, 3.5, "batch-mix-1")
	if err != nil || !expUsed {
		t.Fatalf("capture mixed: expUsed=%v err=%v", expUsed, err)
	}
	_ = tx4.Commit()

	bal11, froz11 := readUserBalances(t, db, 11)
	if bal11 != 5.0 || froz11 != 0 {
		t.Fatalf("expected user 11 regular balance=5.0 frozen=0, got bal=%v froz=%v", bal11, froz11)
	}
	rem1, froz1, cons1, exp1, st1, _ := assertLotAccountingInvariant(t, db, lot1)
	if rem1 != 0 || froz1 != 0 || cons1 != 2.0 || exp1 != 0 || st1 != service.ExpiringLotStatusExpired {
		t.Fatalf("unexpected lot1 after capture: rem=%v froz=%v cons=%v exp=%v status=%s", rem1, froz1, cons1, exp1, st1)
	}
	rem2, froz2, cons2, exp2, st2, gotLot2Exp := assertLotAccountingInvariant(t, db, lot2)
	if rem2 != 1.5 || froz2 != 0 || cons2 != 1.5 || exp2 != 0 || st2 != service.ExpiringLotStatusActive {
		t.Fatalf("unexpected lot2 after capture: rem=%v froz=%v cons=%v exp=%v status=%s", rem2, froz2, cons2, exp2, st2)
	}
	if !gotLot2Exp.Equal(lot2OrigExp.UTC()) {
		t.Fatalf("lot2 expires_at mutated: want %v got %v", lot2OrigExp.UTC(), gotLot2Exp)
	}

	// Case 3: Full release after Lot2 expires -> regular released to balance, Lot2 forfeited (anti-laundering!).
	tx5, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx5: %v", err)
	}
	if err := ReserveBatchImageTotalBalanceTx(ctx, tx5, 11, 4.0, "batch-mix-2"); err != nil {
		t.Fatalf("reserve batch-mix-2: %v", err)
	}
	_ = tx5.Commit()

	if _, err := db.Exec(`UPDATE user_expiring_balance_lots SET expires_at = ? WHERE id = ?`, now.Add(-time.Minute), lot2); err != nil {
		t.Fatalf("expire lot2: %v", err)
	}

	tx6, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx6: %v", err)
	}
	expUsed, err = ReleaseBatchImageTotalBalanceTx(ctx, tx6, 11, 4.0, "batch-mix-2")
	if err != nil || !expUsed {
		t.Fatalf("release batch-mix-2: expUsed=%v err=%v", expUsed, err)
	}
	_ = tx6.Commit()

	bal11, froz11 = readUserBalances(t, db, 11)
	if bal11 != 5.0 || froz11 != 0 {
		t.Fatalf("anti-laundering failed: user 11 regular balance must stay 5.0, got bal=%v froz=%v", bal11, froz11)
	}
	rem2, froz2, cons2, exp2, st2, _ = assertLotAccountingInvariant(t, db, lot2)
	if rem2 != 0 || froz2 != 0 || cons2 != 1.5 || exp2 != 1.5 || st2 != service.ExpiringLotStatusExpired {
		t.Fatalf("expected lot2 forfeited 1.5 expired, got rem=%v froz=%v cons=%v exp=%v status=%s", rem2, froz2, cons2, exp2, st2)
	}
}

func TestRefundPaymentOrderBalanceTx_LIFOPartialAndExpiredForfeit(t *testing.T) {
	db := setupExpiringTestSQLiteDB(t)
	repo := NewExpiringBalanceRepository(nil, db)
	ctx := context.Background()

	insertTestUser(t, db, 20, 5.0, 0)
	now := time.Now().UTC()
	lot1Exp := now.Add(24 * time.Hour)
	lot2Exp := now.Add(48 * time.Hour)
	lot1 := insertTestLot(t, db, 20, 2001, 2.0, lot1Exp)
	lot2 := insertTestLot(t, db, 20, 2002, 3.0, lot2Exp)

	// Legacy order with no allocations returns hasAlloc=false.
	hasAlloc, _, err := repo.RefundPaymentOrderBalance(ctx, 20, 999, 1.0)
	if err != nil || hasAlloc {
		t.Fatalf("expected legacy order hasAlloc=false, err=nil; got hasAlloc=%v err=%v", hasAlloc, err)
	}

	// Purchase order 42 for $10.0: Lot1 ($2.0) + Lot2 ($3.0) + Regular ($5.0).
	res, err := repo.DeductTotalBalance(ctx, 20, 10.0, false, service.ExpiringAllocRefPaymentOrder, "42")
	if err != nil {
		t.Fatalf("purchase order 42: %v", err)
	}
	if res.ExpiringDeducted != 5.0 || res.RegularDeducted != 5.0 {
		t.Fatalf("unexpected deduct split: %+v", res)
	}

	// Partial refund #1 ($3.0) -> LIFO refunds $3.0 regular.
	hasAlloc, r1, err := repo.RefundPaymentOrderBalance(ctx, 20, 42, 3.0)
	if err != nil || !hasAlloc {
		t.Fatalf("refund #1: hasAlloc=%v err=%v", hasAlloc, err)
	}
	if r1.RegularRefunded != 3.0 || r1.ExpiringRefunded != 0 || r1.ExpiringForfeited != 0 {
		t.Fatalf("unexpected r1: %+v", r1)
	}

	// Expire lot1 while lot2 stays live.
	if _, err := db.Exec(`UPDATE user_expiring_balance_lots SET expires_at = ? WHERE id = ?`, now.Add(-time.Minute), lot1); err != nil {
		t.Fatalf("expire lot1: %v", err)
	}

	// Partial refund #2 ($4.0) -> LIFO refunds remaining $2.0 regular + $2.0 from lot2 (live, restores status='active').
	hasAlloc, r2, err := repo.RefundPaymentOrderBalance(ctx, 20, 42, 4.0)
	if err != nil || !hasAlloc {
		t.Fatalf("refund #2: hasAlloc=%v err=%v", hasAlloc, err)
	}
	if r2.RegularRefunded != 2.0 || r2.ExpiringRefunded != 2.0 || r2.ExpiringForfeited != 0 {
		t.Fatalf("unexpected r2: %+v", r2)
	}
	rem2, _, cons2, _, st2, gotExp2 := assertLotAccountingInvariant(t, db, lot2)
	if rem2 != 2.0 || cons2 != 1.0 || st2 != service.ExpiringLotStatusActive || !gotExp2.Equal(lot2Exp.UTC()) {
		t.Fatalf("unexpected lot2 after refund #2: rem=%v cons=%v status=%s exp=%v", rem2, cons2, st2, gotExp2)
	}

	// Partial refund #3 ($2.5) -> remaining $1.0 from lot2 (live) + $1.5 from lot1 (expired -> forfeited!).
	hasAlloc, r3, err := repo.RefundPaymentOrderBalance(ctx, 20, 42, 2.5)
	if err != nil || !hasAlloc {
		t.Fatalf("refund #3: hasAlloc=%v err=%v", hasAlloc, err)
	}
	if r3.RegularRefunded != 0 || r3.ExpiringRefunded != 1.0 || r3.ExpiringForfeited != 1.5 {
		t.Fatalf("unexpected r3: %+v", r3)
	}
	rem1, _, cons1, exp1, st1, _ := assertLotAccountingInvariant(t, db, lot1)
	if rem1 != 0 || cons1 != 0.5 || exp1 != 1.5 || st1 != service.ExpiringLotStatusExpired {
		t.Fatalf("unexpected lot1 after refund #3: rem=%v cons=%v exp=%v status=%s", rem1, cons1, exp1, st1)
	}

	// Regular balance must equal $5.0 (initial regular) — never inflated by forfeited lot1!
	bal20, _ := readUserBalances(t, db, 20)
	if bal20 != 5.0 {
		t.Fatalf("expected user 20 regular balance 5.0, got %v", bal20)
	}

	// Over-refund exceeding remaining $0.5 must fail cleanly.
	if _, _, err := repo.RefundPaymentOrderBalance(ctx, 20, 42, 1.0); err == nil {
		t.Fatalf("expected over-refund to fail")
	}
}

func TestExpiringBalanceRepo_SummaryBatchListAndArchive(t *testing.T) {
	db := setupExpiringTestSQLiteDB(t)
	repo := NewExpiringBalanceRepository(nil, db)
	ctx := context.Background()

	insertTestUser(t, db, 30, 1.0, 0)
	insertTestUser(t, db, 31, 2.0, 0)

	now := time.Now().UTC()
	nearestExp := now.Add(3 * time.Hour).Truncate(time.Second)
	laterExp := now.Add(24 * time.Hour).Truncate(time.Second)
	pastExp := now.Add(-2 * time.Minute).Truncate(time.Second)

	// User 30: two lots sharing nearestExp (1.2 + 0.8 = 2.0), one later lot (4.0), one expired-with-freeze lot, one expired-unfrozen lot.
	insertTestLot(t, db, 30, 3001, 1.2, nearestExp)
	insertTestLot(t, db, 30, 3002, 0.8, nearestExp)
	insertTestLot(t, db, 30, 3003, 4.0, laterExp)
	expiredUnfrozen := insertTestLot(t, db, 30, 3004, 2.5, pastExp)
	expiredFrozen := insertTestLot(t, db, 30, 3005, 3.0, pastExp)
	if _, err := db.Exec(`UPDATE user_expiring_balance_lots SET remaining_amount = 1.0, frozen_amount = 2.0 WHERE id = ?`, expiredFrozen); err != nil {
		t.Fatalf("update frozen expired lot: %v", err)
	}
	// User 31: one live lot.
	insertTestLot(t, db, 31, 3101, 5.5, laterExp)

	// Batch summary (with duplicate IDs and non-positive IDs).
	summaries, err := repo.GetActiveExpiringSummariesBatch(ctx, []int64{0, -1, 30, 31, 30, 999})
	if err != nil {
		t.Fatalf("batch summaries: %v", err)
	}
	s30 := summaries[30]
	if s30.ActiveBalance != 6.0 || s30.FrozenBalance != 2.0 || s30.NearestAmount != 2.0 || s30.NearestExpiresAt == nil || !s30.NearestExpiresAt.Equal(nearestExp) {
		t.Fatalf("unexpected user 30 summary: %+v", s30)
	}
	s31 := summaries[31]
	if s31.ActiveBalance != 5.5 || s31.FrozenBalance != 0 || s31.NearestAmount != 5.5 {
		t.Fatalf("unexpected user 31 summary: %+v", s31)
	}

	// ListUserExpiringLots normalizes expiredUnfrozen as expired before archival.
	expiredLots, pag, err := repo.ListUserExpiringLots(ctx, 30, service.ExpiringLotStatusExpired, pagination.PaginationParams{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list expired lots: %v", err)
	}
	if pag.Total != 1 || len(expiredLots) != 1 || expiredLots[0].ID != expiredUnfrozen || expiredLots[0].ExpiredAmount != 2.5 || expiredLots[0].RemainingAmount != 0 {
		t.Fatalf("unexpected normalized expired lots: pag=%+v lots=%+v", pag, expiredLots)
	}

	// ArchiveExpiredLotsBatch archives both expiredUnfrozen (-> status='expired') and expiredFrozen (remaining->expired, stays 'active').
	affected, count, err := repo.ArchiveExpiredLotsBatch(ctx, 10)
	if err != nil {
		t.Fatalf("archive batch 1: %v", err)
	}
	if count != 2 || len(affected) != 1 || affected[0] != 30 {
		t.Fatalf("unexpected archive batch 1 result: affected=%v count=%d", affected, count)
	}

	remU, frozU, _, expU, stU, _ := assertLotAccountingInvariant(t, db, expiredUnfrozen)
	if remU != 0 || frozU != 0 || expU != 2.5 || stU != service.ExpiringLotStatusExpired {
		t.Fatalf("unexpected expiredUnfrozen state: rem=%v froz=%v exp=%v st=%s", remU, frozU, expU, stU)
	}
	remF, frozF, _, expF, stF, _ := assertLotAccountingInvariant(t, db, expiredFrozen)
	if remF != 0 || frozF != 2.0 || expF != 1.0 || stF != service.ExpiringLotStatusActive {
		t.Fatalf("unexpected expiredFrozen state: rem=%v froz=%v exp=%v st=%s", remF, frozF, expF, stF)
	}

	// Second archive sweep must NOT re-select expiredFrozen (no infinite loop!).
	affected2, count2, err := repo.ArchiveExpiredLotsBatch(ctx, 10)
	if err != nil {
		t.Fatalf("archive batch 2: %v", err)
	}
	if count2 != 0 || len(affected2) != 0 {
		t.Fatalf("expected 0 rows on second archive sweep, got affected=%v count=%d", affected2, count2)
	}
}

func TestExpiringBalanceRepo_MissingTableFallbackOnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, balance REAL NOT NULL DEFAULT 5.0, frozen_balance REAL NOT NULL DEFAULT 0, deleted_at DATETIME NULL, updated_at DATETIME NULL); INSERT INTO users (id, balance) VALUES (1, 5.0);`); err != nil {
		t.Fatalf("seed users: %v", err)
	}

	repo := NewExpiringBalanceRepository(nil, db)
	ctx := context.Background()

	sum, err := repo.GetActiveExpiringSummary(ctx, 1)
	if err != nil || sum.ActiveBalance != 0 {
		t.Fatalf("expected zero summary on missing bypass table, got sum=%+v err=%v", sum, err)
	}
	batch, err := repo.GetActiveExpiringSummariesBatch(ctx, []int64{1})
	if err != nil || len(batch) != 0 {
		t.Fatalf("expected empty batch map on missing bypass table, got batch=%+v err=%v", batch, err)
	}
	lots, pag, err := repo.ListUserExpiringLots(ctx, 1, "", pagination.PaginationParams{Page: 1, PageSize: 20})
	if err != nil || len(lots) != 0 || pag.Total != 0 {
		t.Fatalf("expected empty lots on missing bypass table, got lots=%+v pag=%+v err=%v", lots, pag, err)
	}
	res, err := repo.DeductTotalBalance(ctx, 1, 2.0, false, service.ExpiringAllocRefPaymentOrder, "10")
	if err != nil || res.NewRegularBalance != 3.0 || res.RegularDeducted != 2.0 {
		t.Fatalf("expected regular balance deduct fallback on missing bypass table, got res=%+v err=%v", res, err)
	}
}

func TestExpiringBalanceRepo_PostgresSQLMockQueries(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := NewExpiringBalanceRepository(nil, db)
	ctx := context.Background()

	// Verify DeductTotalBalance emits Postgres FOR UPDATE & expires_at > NOW().
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT balance, COALESCE(frozen_balance, 0) FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(10.0, 0.0))
	mock.ExpectQuery(`SELECT id, remaining_amount, frozen_amount, consumed_amount, expired_amount, expires_at, status\s+FROM user_expiring_balance_lots\s+WHERE user_id = \$1 AND status = 'active' AND remaining_amount > 0 AND expires_at > NOW\(\)\s+ORDER BY expires_at ASC, id ASC\s+FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "remaining_amount", "frozen_amount", "consumed_amount", "expired_amount", "expires_at", "status"}).
			AddRow(int64(55), 1.5, 0.0, 0.0, 0.0, time.Now().Add(time.Hour), "active"))
	mock.ExpectExec(`UPDATE user_expiring_balance_lots\s+SET remaining_amount = \$1, frozen_amount = \$2, consumed_amount = \$3, expired_amount = \$4, status = \$5, updated_at = NOW\(\)\s+WHERE id = \$6`).
		WithArgs(0.0, 0.0, 1.5, 0.0, "exhausted", int64(55)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE users SET balance = balance - $1, updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL`)).
		WithArgs(2.5, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO user_expiring_balance_allocations`).
		WithArgs(int64(7), service.ExpiringAllocRefPaymentOrder, "77", 1, int64(55), 1.5, 1.5).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO user_expiring_balance_allocations`).
		WithArgs(int64(7), service.ExpiringAllocRefPaymentOrder, "77", 2, nil, 2.5, 2.5).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	res, err := repo.DeductTotalBalance(ctx, 7, 4.0, false, service.ExpiringAllocRefPaymentOrder, "77")
	if err != nil {
		t.Fatalf("pg sqlmock deduct: %v", err)
	}
	if res.ExpiringDeducted != 1.5 || res.RegularDeducted != 2.5 || res.NewRegularBalance != 7.5 || res.NewExpiringBalance != 0 {
		t.Fatalf("unexpected pg deduct result: %+v", res)
	}

	// Verify ArchiveExpiredLotsBatch emits FOR UPDATE SKIP LOCKED and (remaining_amount > 0 OR frozen_amount = 0).
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id, user_id, remaining_amount, frozen_amount, expired_amount\s+FROM user_expiring_balance_lots\s+WHERE status = 'active'\s+AND expires_at <= NOW\(\)\s+AND \(remaining_amount > 0 OR frozen_amount = 0\)\s+ORDER BY expires_at ASC, id ASC\s+LIMIT \$1\s+FOR UPDATE SKIP LOCKED`).
		WithArgs(500).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "remaining_amount", "frozen_amount", "expired_amount"}).
			AddRow(int64(88), int64(7), 3.0, 0.0, 0.0))
	mock.ExpectExec(`UPDATE user_expiring_balance_lots\s+SET remaining_amount = 0,\s+expired_amount = \$1,\s+status = \$2,\s+updated_at = NOW\(\)\s+WHERE id = \$3`).
		WithArgs(3.0, "expired", int64(88)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	affected, count, err := repo.ArchiveExpiredLotsBatch(ctx, 0)
	if err != nil {
		t.Fatalf("pg sqlmock archive: %v", err)
	}
	if count != 1 || len(affected) != 1 || affected[0] != 7 {
		t.Fatalf("unexpected pg archive result: affected=%v count=%d", affected, count)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", fmt.Errorf("%w", err))
	}
}

func TestExpiringBalanceTx_NonFiniteAndNegativeInputs(t *testing.T) {
	db := setupExpiringTestSQLiteDB(t)
	repo := NewExpiringBalanceRepository(nil, db)
	ctx := withExpiringSQLDialect(context.Background(), dialect.SQLite)
	now := time.Now().UTC()

	insertTestUser(t, db, 50, 10.0, 0)
	lotID := insertTestLot(t, db, 50, 5001, 5.0, now.Add(time.Hour))

	invalidFloats := []struct {
		name string
		val  float64
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
		{"negative", -2.0},
		{"tiny-negative", -0.000000001},
	}

	for _, tc := range invalidFloats {
		if _, err := repo.DeductTotalBalance(ctx, 50, tc.val, false, service.ExpiringAllocRefUsageBilling, "bad-"+tc.name); err == nil {
			t.Fatalf("DeductTotalBalance(%s) expected error, got nil", tc.name)
		}
		if _, _, err := repo.RefundPaymentOrderBalance(ctx, 50, 1, tc.val); err == nil {
			t.Fatalf("RefundPaymentOrderBalance(%s) expected error, got nil", tc.name)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		if err := ReserveBatchImageTotalBalanceTx(ctx, tx, 50, tc.val, "b-invalid"); err == nil {
			_ = tx.Rollback()
			t.Fatalf("ReserveBatchImageTotalBalanceTx(%s) expected error, got nil", tc.name)
		}
		if _, err := CaptureBatchImageTotalBalanceTx(ctx, tx, 50, 5.0, tc.val, "b-invalid"); err == nil {
			_ = tx.Rollback()
			t.Fatalf("CaptureBatchImageTotalBalanceTx(actual=%s) expected error, got nil", tc.name)
		}
		if _, err := CaptureBatchImageTotalBalanceTx(ctx, tx, 50, tc.val, 1.0, "b-invalid"); err == nil {
			_ = tx.Rollback()
			t.Fatalf("CaptureBatchImageTotalBalanceTx(hold=%s) expected error, got nil", tc.name)
		}
		if _, err := ReleaseBatchImageTotalBalanceTx(ctx, tx, 50, tc.val, "b-invalid"); err == nil {
			_ = tx.Rollback()
			t.Fatalf("ReleaseBatchImageTotalBalanceTx(%s) expected error, got nil", tc.name)
		}
		_ = tx.Rollback()
	}

	// Specifically verify negative actualAmount on an existing pure-regular hold cannot inflate balance.
	insertTestUser(t, db, 51, 10.0, 0)
	txRes, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin reserve: %v", err)
	}
	if err := ReserveBatchImageTotalBalanceTx(ctx, txRes, 51, 5.0, "b-neg-actual"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	_ = txRes.Commit()

	txCap, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin capture: %v", err)
	}
	if _, err := CaptureBatchImageTotalBalanceTx(ctx, txCap, 51, 5.0, -2.0, "b-neg-actual"); err == nil {
		_ = txCap.Rollback()
		t.Fatalf("expected negative actualAmount to fail on pure-regular hold")
	}
	_ = txCap.Rollback()

	bal51, froz51 := readUserBalances(t, db, 51)
	if bal51 != 5.0 || froz51 != 5.0 {
		t.Fatalf("user 51 balances mutated by rejected negative capture: bal=%v froz=%v", bal51, froz51)
	}

	// Zero amounts remain safe no-ops across Reserve/Capture/Release/Refund/Deduct.
	txZero, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin zero tx: %v", err)
	}
	if err := ReserveBatchImageTotalBalanceTx(ctx, txZero, 50, 0, "b-zero"); err != nil {
		t.Fatalf("zero reserve: %v", err)
	}
	if _, err := CaptureBatchImageTotalBalanceTx(ctx, txZero, 50, 0, 0, "b-zero"); err != nil {
		t.Fatalf("zero capture: %v", err)
	}
	if _, err := ReleaseBatchImageTotalBalanceTx(ctx, txZero, 50, 0, "b-zero"); err != nil {
		t.Fatalf("zero release: %v", err)
	}
	if hasAlloc, _, err := RefundPaymentOrderBalanceTx(ctx, txZero, 50, 1, 0); err != nil || hasAlloc {
		t.Fatalf("zero refund: hasAlloc=%v err=%v", hasAlloc, err)
	}
	_ = txZero.Commit()

	bal50, froz50 := readUserBalances(t, db, 50)
	if bal50 != 10.0 || froz50 != 0 {
		t.Fatalf("user 50 balances mutated unexpectedly: bal=%v froz=%v", bal50, froz50)
	}
	assertLotAccountingInvariant(t, db, lotID)
}

func TestBatchImageHoldCaptureRelease_SQLiteFractionalFrozenDrift(t *testing.T) {
	db := setupExpiringTestSQLiteDB(t)
	ctx := withExpiringSQLDialect(context.Background(), dialect.SQLite)

	insertTestUser(t, db, 60, 10.0, 0)

	// Reserve 0.05, then repeatedly reserve + release 0.33333333 on SQLite REAL columns.
	txInit, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin txInit: %v", err)
	}
	if err := ReserveBatchImageTotalBalanceTx(ctx, txInit, 60, 0.05, "b-drift-base"); err != nil {
		t.Fatalf("reserve 0.05: %v", err)
	}
	_ = txInit.Commit()

	for i := 0; i < 20; i++ {
		batchID := fmt.Sprintf("b-drift-frac-%d", i)
		txR, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin reserve %d: %v", i, err)
		}
		if err := ReserveBatchImageTotalBalanceTx(ctx, txR, 60, 0.33333333, batchID); err != nil {
			t.Fatalf("reserve 0.33333333 iter %d: %v", i, err)
		}
		_ = txR.Commit()

		txRel, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin release %d: %v", i, err)
		}
		if _, err := ReleaseBatchImageTotalBalanceTx(ctx, txRel, 60, 0.33333333, batchID); err != nil {
			t.Fatalf("release 0.33333333 iter %d: %v", i, err)
		}
		_ = txRel.Commit()
	}

	// Releasing the original 0.05 hold must succeed without SQLite REAL drift error.
	txFinal, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin final release: %v", err)
	}
	if _, err := ReleaseBatchImageTotalBalanceTx(ctx, txFinal, 60, 0.05, "b-drift-base"); err != nil {
		t.Fatalf("final release 0.05 failed: %v", err)
	}
	_ = txFinal.Commit()

	bal60, froz60 := readUserBalances(t, db, 60)
	if !decimal.NewFromFloat(bal60).Equal(decimal.NewFromFloat(10.0)) || !decimal.NewFromFloat(froz60).IsZero() {
		t.Fatalf("expected user 60 balance=10.0 frozen=0, got bal=%.17g froz=%.17g", bal60, froz60)
	}
}

func TestBatchImageCapture_DecimalTolerance1e8(t *testing.T) {
	db := setupExpiringTestSQLiteDB(t)
	ctx := withExpiringSQLDialect(context.Background(), dialect.SQLite)
	now := time.Now().UTC()

	// 1. Legacy pure-regular path: hold=100.0, actual=100.00000001 succeeds; 100.00000002 fails.
	insertTestUser(t, db, 70, 150.0, 0)
	tx1, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	if err := ReserveBatchImageTotalBalanceTx(ctx, tx1, 70, 100.0, "b-tol-reg"); err != nil {
		t.Fatalf("reserve reg: %v", err)
	}
	_ = tx1.Commit()

	tx1Reject, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx1Reject: %v", err)
	}
	_, capErr := CaptureBatchImageTotalBalanceTx(ctx, tx1Reject, 70, 100.0, 100.00000002, "b-tol-reg")
	_ = tx1Reject.Rollback()
	if !errors.Is(capErr, service.ErrBatchImageSettlementCostExceedsHold) {
		t.Fatalf("expected ErrBatchImageSettlementCostExceedsHold for +2e-8 on legacy path, got %v", capErr)
	}

	tx1OK, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx1OK: %v", err)
	}
	expUsed, err := CaptureBatchImageTotalBalanceTx(ctx, tx1OK, 70, 100.0, 100.00000001, "b-tol-reg")
	if err != nil || expUsed {
		t.Fatalf("expected legacy +1e-8 capture to succeed: expUsed=%v err=%v", expUsed, err)
	}
	_ = tx1OK.Commit()

	bal70, froz70 := readUserBalances(t, db, 70)
	want70 := decimal.RequireFromString("49.99999999")
	if !decFromFloat(bal70).Equal(want70) || !decFromFloat(froz70).IsZero() {
		t.Fatalf("expected user 70 bal=49.99999999 frozen=0, got bal=%.10f froz=%.10f", bal70, froz70)
	}

	// 2. Expiring-lot path: hold=100.0 (from lot), actual=100.00000001 deducts extra 1e-8 from users.balance; 100.00000002 fails.
	insertTestUser(t, db, 71, 5.0, 0)
	lotID := insertTestLot(t, db, 71, 7101, 100.0, now.Add(time.Hour))

	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx2: %v", err)
	}
	if err := ReserveBatchImageTotalBalanceTx(ctx, tx2, 71, 100.0, "b-tol-exp"); err != nil {
		t.Fatalf("reserve exp: %v", err)
	}
	_ = tx2.Commit()

	tx2Reject, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx2Reject: %v", err)
	}
	_, capErr = CaptureBatchImageTotalBalanceTx(ctx, tx2Reject, 71, 100.0, 100.00000002, "b-tol-exp")
	_ = tx2Reject.Rollback()
	if !errors.Is(capErr, service.ErrBatchImageSettlementCostExceedsHold) {
		t.Fatalf("expected ErrBatchImageSettlementCostExceedsHold for +2e-8 on expiring path, got %v", capErr)
	}

	tx2OK, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx2OK: %v", err)
	}
	expUsed, err = CaptureBatchImageTotalBalanceTx(ctx, tx2OK, 71, 100.0, 100.00000001, "b-tol-exp")
	if err != nil || !expUsed {
		t.Fatalf("expected expiring +1e-8 capture to succeed: expUsed=%v err=%v", expUsed, err)
	}
	_ = tx2OK.Commit()

	bal71, froz71 := readUserBalances(t, db, 71)
	want71 := decimal.RequireFromString("4.99999999")
	if !decFromFloat(bal71).Equal(want71) || !decFromFloat(froz71).IsZero() {
		t.Fatalf("expected user 71 bal=4.99999999 frozen=0, got bal=%.10f froz=%.10f", bal71, froz71)
	}
	rem, froz, cons, expAmt, st, _ := assertLotAccountingInvariant(t, db, lotID)
	if rem != 0 || froz != 0 || cons != 100.0 || expAmt != 0 || st != service.ExpiringLotStatusExhausted {
		t.Fatalf("unexpected lot state after +1e-8 capture: rem=%v froz=%v cons=%v exp=%v st=%s", rem, froz, cons, expAmt, st)
	}
}

func TestBatchImageHoldCaptureRelease_PostgresSQLMock(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Legacy Capture emits SET balance = $1, frozen_balance = $2 without float >= predicate.
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT balance, COALESCE(frozen_balance, 0) FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`)).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(50.0, 100.0))
	mock.ExpectQuery(`SELECT id, alloc_seq, lot_id, allocated_amount, captured_amount, released_amount, forfeited_expired_amount\s+FROM user_expiring_balance_allocations\s+WHERE user_id = \$1 AND ref_type = \$2 AND ref_key = \$3\s+ORDER BY alloc_seq ASC FOR UPDATE`).
		WithArgs(int64(9), service.ExpiringAllocRefBatchImageHold, "pg-cap-legacy").
		WillReturnRows(sqlmock.NewRows([]string{"id", "alloc_seq", "lot_id", "allocated_amount", "captured_amount", "released_amount", "forfeited_expired_amount"}))
	mock.ExpectExec(`UPDATE users\s+SET balance = \$1,\s+frozen_balance = \$2,\s+updated_at = NOW\(\)\s+WHERE id = \$3 AND deleted_at IS NULL`).
		WithArgs(49.99999999, 0.0, int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	tx1, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	expUsed, err := CaptureBatchImageTotalBalanceTx(ctx, tx1, 9, 100.0, 100.00000001, "pg-cap-legacy")
	if err != nil || expUsed {
		t.Fatalf("pg legacy capture: expUsed=%v err=%v", expUsed, err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit tx1: %v", err)
	}

	// Legacy Release emits SET balance = $1, frozen_balance = $2 without float >= predicate.
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT balance, COALESCE(frozen_balance, 0) FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`)).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(10.0, 0.05))
	mock.ExpectQuery(`SELECT id, alloc_seq, lot_id, allocated_amount, captured_amount, released_amount, forfeited_expired_amount\s+FROM user_expiring_balance_allocations\s+WHERE user_id = \$1 AND ref_type = \$2 AND ref_key = \$3\s+ORDER BY alloc_seq DESC FOR UPDATE`).
		WithArgs(int64(9), service.ExpiringAllocRefBatchImageHold, "pg-rel-legacy").
		WillReturnRows(sqlmock.NewRows([]string{"id", "alloc_seq", "lot_id", "allocated_amount", "captured_amount", "released_amount", "forfeited_expired_amount"}))
	mock.ExpectExec(`UPDATE users\s+SET balance = \$1,\s+frozen_balance = \$2,\s+updated_at = NOW\(\)\s+WHERE id = \$3 AND deleted_at IS NULL`).
		WithArgs(10.05, 0.0, int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx2: %v", err)
	}
	expUsed, err = ReleaseBatchImageTotalBalanceTx(ctx, tx2, 9, 0.05, "pg-rel-legacy")
	if err != nil || expUsed {
		t.Fatalf("pg legacy release: expUsed=%v err=%v", expUsed, err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit tx2: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", fmt.Errorf("%w", err))
	}
}
