package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type stubBillingCacheUserRepo struct {
	service.UserRepository
	users map[int64]*service.User
}

func (r *stubBillingCacheUserRepo) GetByID(_ context.Context, id int64) (*service.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, service.ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

func TestBillingCache_DeductUserBalance_KEEPTTL(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cache := NewBillingCache(rdb)
	ttlCache, ok := cache.(service.BillingCacheWithTTL)
	require.True(t, ok)

	// Missing key: DeductUserBalance is a no-op and must not create the key.
	require.NoError(t, cache.DeductUserBalance(ctx, 100, 1.0))
	require.False(t, mr.Exists(billingBalanceKey(100)))

	// Key with short 12s TTL: DeductUserBalance must preserve TTL (<= 12s, not reset to 5m).
	require.NoError(t, ttlCache.SetUserBalanceWithTTL(ctx, 101, 5.0, 12*time.Second))
	initialTTL := mr.TTL(billingBalanceKey(101))
	require.Greater(t, initialTTL, time.Duration(0))
	require.LessOrEqual(t, initialTTL, 12*time.Second)

	require.NoError(t, cache.DeductUserBalance(ctx, 101, 1.25))
	got, err := cache.GetUserBalance(ctx, 101)
	require.NoError(t, err)
	require.InDelta(t, 3.75, got, 1e-8)

	afterTTL := mr.TTL(billingBalanceKey(101))
	require.Greater(t, afterTTL, time.Duration(0))
	require.LessOrEqual(t, afterTTL, 12*time.Second)

	// Persistent key (no TTL): DeductUserBalance must keep no TTL (mr.TTL == 0) without error.
	require.NoError(t, rdb.Set(ctx, billingBalanceKey(102), 8.0, 0).Err())
	require.NoError(t, cache.DeductUserBalance(ctx, 102, 2.0))
	got2, err := cache.GetUserBalance(ctx, 102)
	require.NoError(t, err)
	require.InDelta(t, 6.0, got2, 1e-8)
	require.Equal(t, time.Duration(0), mr.TTL(billingBalanceKey(102)))
	rdbTTL, err := rdb.TTL(ctx, billingBalanceKey(102)).Result()
	require.NoError(t, err)
	require.Equal(t, time.Duration(-1), rdbTTL)
}

func TestBillingCache_SetUserBalanceWithTTL_Clamping(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cache, ok := NewBillingCache(rdb).(service.BillingCacheWithTTL)
	require.True(t, ok)

	// 1. Short TTL honored.
	require.NoError(t, cache.SetUserBalanceWithTTL(ctx, 1, 1.5, 10*time.Second))
	require.LessOrEqual(t, mr.TTL(billingBalanceKey(1)), 10*time.Second)
	require.Greater(t, mr.TTL(billingBalanceKey(1)), time.Duration(0))

	// 2. Excessively long TTL clamped to <= billingCacheTTL (5m).
	require.NoError(t, cache.SetUserBalanceWithTTL(ctx, 2, 2.5, 2*time.Hour))
	require.LessOrEqual(t, mr.TTL(billingBalanceKey(2)), billingCacheTTL)
	require.Greater(t, mr.TTL(billingBalanceKey(2)), 4*time.Minute)

	// 3. Non-positive TTL clamped to jitteredTTL() (never persistent -1).
	require.NoError(t, cache.SetUserBalanceWithTTL(ctx, 3, 3.5, 0))
	require.LessOrEqual(t, mr.TTL(billingBalanceKey(3)), billingCacheTTL)
	require.Greater(t, mr.TTL(billingBalanceKey(3)), 4*time.Minute)
}

func TestBillingCacheService_TotalBalanceAndTTLTruncation_WithMiniredis(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	db := setupExpiringTestSQLiteDB(t)
	db.SetMaxOpenConns(1)
	insertTestUser(t, db, 1, 1.50, 0)
	insertTestUser(t, db, 2, 2.00, 0)

	now := time.Now().UTC()
	// User 1: active lot expiring in 20s + expired lot ignored.
	insertTestLot(t, db, 1, 501, 0.50, now.Add(20*time.Second))
	insertTestLot(t, db, 1, 502, 10.0, now.Add(-1*time.Minute))
	// User 2: active lot expiring in 700ms (<= 1s) -> must skip Redis write.
	insertTestLot(t, db, 2, 503, 0.80, now.Add(700*time.Millisecond))

	expRepo := NewExpiringBalanceRepository(nil, db)
	expSvc := service.NewExpiringBalanceService(expRepo)
	userRepo := &stubBillingCacheUserRepo{
		users: map[int64]*service.User{
			1: {ID: 1, Balance: 1.50},
			2: {ID: 2, Balance: 2.00},
		},
	}

	svc := service.NewBillingCacheService(NewBillingCache(rdb), userRepo, nil, nil, nil, nil, &config.Config{}, nil)
	svc.SetExpiringBalanceService(expSvc)
	t.Cleanup(svc.Stop)

	// User 1: total = 1.50 + 0.50 = 2.00, cached in Redis with TTL <= 20s.
	bal1, err := svc.GetUserBalance(ctx, 1)
	require.NoError(t, err)
	require.InDelta(t, 2.00, bal1, 1e-8)

	require.Eventually(t, func() bool {
		return mr.Exists(billingBalanceKey(1))
	}, 2*time.Second, 10*time.Millisecond)
	ttl1 := mr.TTL(billingBalanceKey(1))
	require.Greater(t, ttl1, time.Duration(0))
	require.LessOrEqual(t, ttl1, 20*time.Second)

	// Deduct balance via QueueDeductBalance -> KEEPTTL must preserve <= 20s TTL!
	svc.QueueDeductBalance(1, 0.25)
	require.Eventually(t, func() bool {
		v, gErr := NewBillingCache(rdb).GetUserBalance(ctx, 1)
		return gErr == nil && v < 1.80
	}, 2*time.Second, 10*time.Millisecond)
	ttlAfterDeduct := mr.TTL(billingBalanceKey(1))
	require.Greater(t, ttlAfterDeduct, time.Duration(0))
	require.LessOrEqual(t, ttlAfterDeduct, 20*time.Second)

	// User 2: total = 2.00 + 0.80 = 2.80, but nearest_expires_at <= 1s -> NOT cached.
	bal2, err := svc.GetUserBalance(ctx, 2)
	require.NoError(t, err)
	require.InDelta(t, 2.80, bal2, 1e-8)
	// Give worker time to run if anything had been queued, then verify key still absent.
	time.Sleep(50 * time.Millisecond)
	require.False(t, mr.Exists(billingBalanceKey(2)))
}

func TestBillingCacheService_MissingTableToleranceVsPostgresStrict(t *testing.T) {
	ctx := context.Background()

	// 1. Unmigrated SQLite DB (only users table, no expiring tables): graceful fallback to user.Balance.
	sqliteDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqliteDB.Close() })
	_, err = sqliteDB.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, balance REAL NOT NULL DEFAULT 0)`)
	require.NoError(t, err)

	expRepoSQLite := NewExpiringBalanceRepository(nil, sqliteDB)
	userRepo := &stubBillingCacheUserRepo{
		users: map[int64]*service.User{
			7: {ID: 7, Balance: 4.25},
		},
	}
	svcSQLite := service.NewBillingCacheService(nil, userRepo, nil, nil, nil, nil, &config.Config{}, nil)
	svcSQLite.SetExpiringBalanceRepo(expRepoSQLite)
	t.Cleanup(svcSQLite.Stop)

	bal, err := svcSQLite.GetUserBalance(ctx, 7)
	require.NoError(t, err)
	require.InDelta(t, 4.25, bal, 1e-8)

	// 2. Postgres sqlmock returning an error from user_expiring_balance_lots: must NOT swallow error!
	pgDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgDB.Close() })
	mock.ExpectQuery(`(?s)SELECT remaining_amount, frozen_amount, expires_at, \(expires_at > NOW\(\)\) AS is_live\s+FROM user_expiring_balance_lots\s+WHERE user_id = \$1 AND status = 'active'\s+ORDER BY expires_at ASC, id ASC`).
		WithArgs(int64(7)).
		WillReturnError(errors.New("pq: relation \"user_expiring_balance_lots\" does not exist"))

	expRepoPG := NewExpiringBalanceRepository(nil, pgDB)
	svcPG := service.NewBillingCacheService(nil, userRepo, nil, nil, nil, nil, &config.Config{}, nil)
	svcPG.SetExpiringBalanceRepo(expRepoPG)
	t.Cleanup(svcPG.Stop)

	_, err = svcPG.GetUserBalance(ctx, 7)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
