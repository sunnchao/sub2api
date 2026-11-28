package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// reasonOfRepoErr 返回 ApplicationError 的 reason（非 ApplicationError 返回空串）。
func reasonOfRepoErr(err error) string {
	return infraerrors.Reason(err)
}

// setupCheckinTestDB 构建签到相关旁路表的 SQLite 内存库。
func setupCheckinTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	// 单连接，保证 :memory: 库在同一连接上可见。
	db.SetMaxOpenConns(1)

	ddl := `
		PRAGMA foreign_keys = ON;
		CREATE TABLE users (
			id             INTEGER PRIMARY KEY,
			email          TEXT NOT NULL DEFAULT '',
			username       TEXT NOT NULL DEFAULT '',
			status         TEXT NOT NULL DEFAULT 'active',
			balance        REAL NOT NULL DEFAULT 0,
			frozen_balance REAL NOT NULL DEFAULT 0,
			total_recharged REAL NOT NULL DEFAULT 0,
			signup_source  TEXT NOT NULL DEFAULT 'email',
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at     DATETIME NULL
		);
		CREATE TABLE auth_identities (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			provider_type TEXT NOT NULL,
			provider_key  TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE user_checkin_records (
			id                     INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id                INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			checkin_date           DATE NOT NULL,
			streak_days            INTEGER NOT NULL DEFAULT 1,
			milestone_cycle_day    INTEGER NOT NULL DEFAULT 1,
			base_amount            REAL NOT NULL DEFAULT 0,
			milestone_bonus_amount REAL NOT NULL DEFAULT 0,
			total_amount           REAL NOT NULL DEFAULT 0,
			validity_days          INTEGER NOT NULL DEFAULT 1,
			expires_at             DATETIME NOT NULL,
			client_ip              TEXT NOT NULL DEFAULT '',
			created_at             DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE UNIQUE INDEX idx_user_checkin_records_user_date
			ON user_checkin_records (user_id, checkin_date);
		CREATE TABLE checkin_daily_budgets (
			checkin_date  DATE PRIMARY KEY,
			total_granted REAL NOT NULL DEFAULT 0,
			checkin_count INTEGER NOT NULL DEFAULT 0,
			updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
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
		CREATE UNIQUE INDEX idx_expiring_lots_source
			ON user_expiring_balance_lots (source_type, source_id);
		CREATE TABLE user_expiring_balance_allocations (
			id                       INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id                  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			ref_type                 TEXT NOT NULL,
			ref_key                  TEXT NOT NULL,
			alloc_seq                INTEGER NOT NULL,
			lot_id                   INTEGER NULL,
			allocated_amount         REAL NOT NULL,
			captured_amount          REAL NOT NULL DEFAULT 0,
			released_amount          REAL NOT NULL DEFAULT 0,
			forfeited_expired_amount REAL NOT NULL DEFAULT 0,
			created_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`
	_, err = db.Exec(ddl)
	require.NoError(t, err)
	return db
}

func insertCheckinTestUser(t *testing.T, db *sql.DB, id int64, balance, totalRecharged float64) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO users (id, email, username, status, balance, total_recharged, signup_source, created_at)
		VALUES (?, ?, ?, 'active', ?, ?, 'email', ?)
	`, id, fmt.Sprintf("user%d@example.com", id), fmt.Sprintf("user%d", id), balance, totalRecharged, time.Now().UTC().Add(-90*24*time.Hour))
	require.NoError(t, err)
}

func checkinSettingsFixture(baseAmount float64) *service.CheckinSettings {
	settings := service.DefaultCheckinSettings()
	settings.Enabled = true
	settings.BaseAmount = baseAmount
	settings.MinAmount = baseAmount
	settings.MaxAmount = baseAmount
	settings.ValidityDays = 1
	return settings
}

func newCheckinTestInput(t *testing.T, userID int64, settings *service.CheckinSettings) service.CheckinExecuteInput {
	t.Helper()
	now := timezone.Now()
	today := timezone.StartOfDay(now)
	return service.CheckinExecuteInput{
		UserID:       userID,
		ClientIP:     "203.0.113.7",
		Now:          now,
		Today:        today,
		Yesterday:    today.AddDate(0, 0, -1),
		BaseAmount:   settings.BaseAmount,
		ValidityDays: settings.ValidityDays,
		ExpiresAt:    today.AddDate(0, 0, settings.ValidityDays),
		Settings:     settings,
	}
}

func TestCheckinRepoExecuteCheckinCreatesRecordAndLot(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	repo := NewCheckinRepository(nil, db)

	settings := checkinSettingsFixture(0.5)
	in := newCheckinTestInput(t, 1, settings)

	record, err := repo.ExecuteCheckin(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, int64(1), record.UserID)
	require.Equal(t, in.Today.Format("2006-01-02"), record.CheckinDate)
	require.Equal(t, 1, record.StreakDays)
	require.Equal(t, 1, record.MilestoneCycleDay)
	require.InDelta(t, 0.5, record.BaseAmount, 1e-9)
	require.InDelta(t, 0.0, record.MilestoneBonusAmount, 1e-9)
	require.InDelta(t, 0.5, record.TotalAmount, 1e-9)
	require.Equal(t, 1, record.ValidityDays)
	require.Equal(t, "203.0.113.7", record.ClientIP)
	require.Positive(t, record.ID)

	var (
		lotCount   int
		initial    float64
		remaining  float64
		status     string
		sourceType string
	)
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*) FROM user_expiring_balance_lots WHERE user_id = 1
	`).Scan(&lotCount))
	require.Equal(t, 1, lotCount)
	require.NoError(t, db.QueryRow(`
		SELECT initial_amount, remaining_amount, status, source_type
		FROM user_expiring_balance_lots WHERE user_id = 1
	`).Scan(&initial, &remaining, &status, &sourceType))
	require.InDelta(t, 0.5, initial, 1e-9)
	require.InDelta(t, 0.5, remaining, 1e-9)
	require.Equal(t, "active", status)
	require.Equal(t, service.ExpiringLotSourceCheckin, sourceType)

	var granted float64
	var count int
	require.NoError(t, db.QueryRow(`
		SELECT total_granted, checkin_count FROM checkin_daily_budgets WHERE checkin_date = ?
	`, in.Today.Format("2006-01-02")).Scan(&granted, &count))
	require.InDelta(t, 0.5, granted, 1e-9)
	require.Equal(t, 1, count)
}

func TestCheckinRepoRejectsDuplicateSameDay(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	repo := NewCheckinRepository(nil, db)

	settings := checkinSettingsFixture(0.2)
	in := newCheckinTestInput(t, 1, settings)
	_, err := repo.ExecuteCheckin(context.Background(), in)
	require.NoError(t, err)

	_, err = repo.ExecuteCheckin(context.Background(), in)
	require.Error(t, err)
	require.Equal(t, service.CheckinReasonAlreadyDone, reasonOfRepoErr(err))

	// 预算表只应累加一次。
	var count int
	require.NoError(t, db.QueryRow(`SELECT checkin_count FROM checkin_daily_budgets`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestCheckinRepoContinuesStreakFromYesterday(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	repo := NewCheckinRepository(nil, db)

	settings := checkinSettingsFixture(0.1)
	settings.Milestones = []service.CheckinMilestoneRule{{Days: 2, BonusAmount: 0.5}}

	now := timezone.Now()
	today := timezone.StartOfDay(now)
	yesterday := today.AddDate(0, 0, -1)
	_, err := db.Exec(`
		INSERT INTO user_checkin_records
			(user_id, checkin_date, streak_days, milestone_cycle_day, base_amount,
			 milestone_bonus_amount, total_amount, validity_days, expires_at, client_ip, created_at)
		VALUES (1, ?, 1, 1, 0.1, 0, 0.1, 1, ?, '', ?)
	`, yesterday.Format("2006-01-02"), yesterday.AddDate(0, 0, 1).UTC(), yesterday.UTC())
	require.NoError(t, err)

	in := newCheckinTestInput(t, 1, settings)
	record, err := repo.ExecuteCheckin(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, 2, record.StreakDays)
	require.Equal(t, 2, record.MilestoneCycleDay)
	require.InDelta(t, 0.5, record.MilestoneBonusAmount, 1e-9)
	require.InDelta(t, 0.6, record.TotalAmount, 1e-9)
}

func TestCheckinRepoResetsStreakAfterGap(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	repo := NewCheckinRepository(nil, db)

	settings := checkinSettingsFixture(0.1)
	now := timezone.Now()
	today := timezone.StartOfDay(now)
	older := today.AddDate(0, 0, -3)
	_, err := db.Exec(`
		INSERT INTO user_checkin_records
			(user_id, checkin_date, streak_days, milestone_cycle_day, base_amount,
			 milestone_bonus_amount, total_amount, validity_days, expires_at, client_ip, created_at)
		VALUES (1, ?, 6, 6, 0.1, 0, 0.1, 1, ?, '', ?)
	`, older.Format("2006-01-02"), older.AddDate(0, 0, 1).UTC(), older.UTC())
	require.NoError(t, err)

	record, err := repo.ExecuteCheckin(context.Background(), newCheckinTestInput(t, 1, settings))
	require.NoError(t, err)
	require.Equal(t, 1, record.StreakDays)
	require.Equal(t, 1, record.MilestoneCycleDay)
}

func TestCheckinRepoEligibilityGates(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	repo := NewCheckinRepository(nil, db)

	settings := checkinSettingsFixture(0.1)
	settings.Eligibility.MinTotalRecharged = 5
	_, err := repo.ExecuteCheckin(context.Background(), newCheckinTestInput(t, 1, settings))
	require.Error(t, err)
	require.Equal(t, service.CheckinReasonIneligibleRecharge, reasonOfRepoErr(err))

	// 满足充值门槛后通过。
	_, err = db.Exec(`UPDATE users SET total_recharged = 10 WHERE id = 1`)
	require.NoError(t, err)
	_, err = repo.ExecuteCheckin(context.Background(), newCheckinTestInput(t, 1, settings))
	require.NoError(t, err)
}

func TestCheckinRepoEligibilitySourceUsesBoundIdentity(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	repo := NewCheckinRepository(nil, db)

	settings := checkinSettingsFixture(0.1)
	settings.Eligibility.AllowedSignupSources = []string{"github"}

	_, err := repo.ExecuteCheckin(context.Background(), newCheckinTestInput(t, 1, settings))
	require.Error(t, err)
	require.Equal(t, service.CheckinReasonIneligibleSource, reasonOfRepoErr(err))

	_, err = db.Exec(`INSERT INTO auth_identities (user_id, provider_type) VALUES (1, 'github')`)
	require.NoError(t, err)
	_, err = repo.ExecuteCheckin(context.Background(), newCheckinTestInput(t, 1, settings))
	require.NoError(t, err)
}

func TestCheckinRepoBalanceCapUsesActiveLots(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 1.0, 0)
	repo := NewCheckinRepository(nil, db)

	now := timezone.Now()
	_, err := db.Exec(`
		INSERT INTO user_expiring_balance_lots
			(user_id, source_type, source_id, initial_amount, remaining_amount, expires_at, status, created_at, updated_at)
		VALUES (1, 'checkin', 999, 1.0, 1.0, ?, 'active', ?, ?)
	`, now.Add(6*time.Hour).UTC(), now.UTC(), now.UTC())
	require.NoError(t, err)

	settings := checkinSettingsFixture(0.1)
	settings.Eligibility.MaxAvailableBalance = 1.5

	_, err = repo.ExecuteCheckin(context.Background(), newCheckinTestInput(t, 1, settings))
	require.Error(t, err)
	require.Equal(t, service.CheckinReasonBalanceCapExceeded, reasonOfRepoErr(err))
}

func TestCheckinRepoIPLimit(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	insertCheckinTestUser(t, db, 2, 0, 0)
	repo := NewCheckinRepository(nil, db)

	settings := checkinSettingsFixture(0.1)
	settings.Risk.MaxAccountsPerIPDay = 1

	in1 := newCheckinTestInput(t, 1, settings)
	_, err := repo.ExecuteCheckin(context.Background(), in1)
	require.NoError(t, err)

	in2 := newCheckinTestInput(t, 2, settings)
	_, err = repo.ExecuteCheckin(context.Background(), in2)
	require.Error(t, err)
	require.Equal(t, service.CheckinReasonIPLimitExceeded, reasonOfRepoErr(err))

	// 换 IP 后可以签到。
	in2.ClientIP = "203.0.113.8"
	_, err = repo.ExecuteCheckin(context.Background(), in2)
	require.NoError(t, err)
}

func TestCheckinRepoDailyBudgetFuse(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	insertCheckinTestUser(t, db, 2, 0, 0)
	repo := NewCheckinRepository(nil, db)

	settings := checkinSettingsFixture(0.6)
	settings.Risk.DailyTotalBudgetUSD = 1.0

	in1 := newCheckinTestInput(t, 1, settings)
	in1.ClientIP = "203.0.113.11"
	_, err := repo.ExecuteCheckin(context.Background(), in1)
	require.NoError(t, err)

	in2 := newCheckinTestInput(t, 2, settings)
	in2.ClientIP = "203.0.113.12"
	_, err = repo.ExecuteCheckin(context.Background(), in2)
	require.Error(t, err)
	require.Equal(t, service.CheckinReasonDailyBudgetExhausted, reasonOfRepoErr(err))

	// 被拒绝的签到不应留下记录或批次。
	var records int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM user_checkin_records`).Scan(&records))
	require.Equal(t, 1, records)

	var granted float64
	require.NoError(t, db.QueryRow(`SELECT total_granted FROM checkin_daily_budgets`).Scan(&granted))
	require.InDelta(t, 0.6, granted, 1e-9)
}

func TestCheckinRepoCalendarAndRecords(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	repo := NewCheckinRepository(nil, db)

	settings := checkinSettingsFixture(0.1)
	settings.Milestones = []service.CheckinMilestoneRule{{Days: 2, BonusAmount: 0.5}}

	today := timezone.StartOfDay(timezone.Now())
	first := today.AddDate(0, 0, -2)
	for i, day := range []time.Time{first, first.AddDate(0, 0, 1)} {
		bonus := 0.0
		if i == 1 {
			bonus = 0.5
		}
		_, err := db.Exec(`
			INSERT INTO user_checkin_records
				(user_id, checkin_date, streak_days, milestone_cycle_day, base_amount,
				 milestone_bonus_amount, total_amount, validity_days, expires_at, client_ip, created_at)
			VALUES (1, ?, ?, ?, 0.1, ?, ?, 1, ?, '203.0.113.20', ?)
		`, day.Format("2006-01-02"), i+1, i+1, bonus, 0.1+bonus, day.AddDate(0, 0, 1).UTC(), day.UTC())
		require.NoError(t, err)
	}

	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
	days, err := repo.ListCheckinCalendar(context.Background(), 1, monthStart, monthStart.AddDate(0, 1, 0))
	require.NoError(t, err)
	require.Len(t, days, 2)
	require.Equal(t, first.Format("2006-01-02"), days[0].Date)
	require.False(t, days[0].MilestoneHit)
	require.True(t, days[1].MilestoneHit)
	require.InDelta(t, 0.5, days[1].MilestoneBonusAmount, 1e-9)

	records, page, err := repo.ListUserCheckinRecords(context.Background(), 1, pagination.PaginationParams{Page: 1, PageSize: 1})
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, int64(2), page.Total)
	require.Equal(t, 2, page.Pages)
	// 默认按日期倒序。
	require.Equal(t, first.AddDate(0, 0, 1).Format("2006-01-02"), records[0].CheckinDate)

	last, err := repo.GetLastCheckinRecord(context.Background(), 1)
	require.NoError(t, err)
	require.NotNil(t, last)
	require.Equal(t, first.AddDate(0, 0, 1).Format("2006-01-02"), last.CheckinDate)

	byDate, err := repo.GetCheckinRecordByDate(context.Background(), 1, first)
	require.NoError(t, err)
	require.NotNil(t, byDate)
	require.Equal(t, 1, byDate.StreakDays)

	missing, err := repo.GetCheckinRecordByDate(context.Background(), 1, first.AddDate(0, 0, -10))
	require.NoError(t, err)
	require.Nil(t, missing)

	noLast, err := repo.GetLastCheckinRecord(context.Background(), 2)
	require.NoError(t, err)
	require.Nil(t, noLast)
}

func TestCheckinRepoAdminRecordsFiltersAndStats(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	insertCheckinTestUser(t, db, 2, 0, 0)
	repo := NewCheckinRepository(nil, db)

	settings := checkinSettingsFixture(0.25)
	today := timezone.StartOfDay(timezone.Now())

	in1 := newCheckinTestInput(t, 1, settings)
	in1.ClientIP = "203.0.113.31"
	_, err := repo.ExecuteCheckin(context.Background(), in1)
	require.NoError(t, err)

	in2 := newCheckinTestInput(t, 2, settings)
	in2.ClientIP = "203.0.113.32"
	_, err = repo.ExecuteCheckin(context.Background(), in2)
	require.NoError(t, err)

	all, page, err := repo.ListAdminCheckinRecords(context.Background(), service.AdminCheckinRecordFilter{
		Params: pagination.PaginationParams{Page: 1, PageSize: 10},
	})
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.Equal(t, int64(2), page.Total)
	for _, item := range all {
		require.NotEmpty(t, item.UserEmail)
		require.NotEmpty(t, item.Username)
	}

	byUser, _, err := repo.ListAdminCheckinRecords(context.Background(), service.AdminCheckinRecordFilter{
		UserID: 1,
		Params: pagination.PaginationParams{Page: 1, PageSize: 10},
	})
	require.NoError(t, err)
	require.Len(t, byUser, 1)
	require.Equal(t, int64(1), byUser[0].UserID)

	bySearch, _, err := repo.ListAdminCheckinRecords(context.Background(), service.AdminCheckinRecordFilter{
		Search: "user2",
		Params: pagination.PaginationParams{Page: 1, PageSize: 10},
	})
	require.NoError(t, err)
	require.Len(t, bySearch, 1)
	require.Equal(t, int64(2), bySearch[0].UserID)

	byIP, _, err := repo.ListAdminCheckinRecords(context.Background(), service.AdminCheckinRecordFilter{
		ClientIP: "203.0.113.31",
		Params:   pagination.PaginationParams{Page: 1, PageSize: 10},
	})
	require.NoError(t, err)
	require.Len(t, byIP, 1)
	require.Equal(t, int64(1), byIP[0].UserID)

	todayStr := today.Format("2006-01-02")
	byDate, _, err := repo.ListAdminCheckinRecords(context.Background(), service.AdminCheckinRecordFilter{
		StartDate: todayStr,
		EndDate:   todayStr,
		Params:    pagination.PaginationParams{Page: 1, PageSize: 10},
	})
	require.NoError(t, err)
	require.Len(t, byDate, 2)

	stats, err := repo.GetAdminCheckinStats(context.Background(), 30)
	require.NoError(t, err)
	require.Equal(t, 2, stats.TodayCheckins)
	require.InDelta(t, 0.5, stats.TodayGranted, 1e-9)
	require.InDelta(t, 0.5, stats.RangeTotalGranted, 1e-9)
	require.NotEmpty(t, stats.DailyTrend)

	// 空区间统计应为零值而不是报错。
	emptyDB := setupCheckinTestDB(t)
	insertCheckinTestUser(t, emptyDB, 1, 0, 0)
	emptyRepo := NewCheckinRepository(nil, emptyDB)
	emptyStats, err := emptyRepo.GetAdminCheckinStats(context.Background(), 30)
	require.NoError(t, err)
	require.Zero(t, emptyStats.TodayCheckins)
	require.Zero(t, emptyStats.ConsumptionRate)
	require.Zero(t, emptyStats.ExpirationRate)
}

func TestCheckinRepoStatsRatesFromLots(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	repo := NewCheckinRepository(nil, db)

	now := timezone.Now()
	_, err := db.Exec(`
		INSERT INTO user_expiring_balance_lots
			(user_id, source_type, source_id, initial_amount, remaining_amount, consumed_amount, expired_amount, expires_at, status, created_at, updated_at)
		VALUES (1, 'checkin', 500, 1.0, 0, 0.5, 0.5, ?, 'expired', ?, ?)
	`, now.Add(-time.Hour).UTC(), now.UTC(), now.UTC())
	require.NoError(t, err)

	stats, err := repo.GetAdminCheckinStats(context.Background(), 30)
	require.NoError(t, err)
	require.InDelta(t, 0.5, stats.RangeTotalConsumed, 1e-9)
	require.InDelta(t, 0.5, stats.RangeTotalExpired, 1e-9)
	require.InDelta(t, 0.5, stats.ConsumptionRate, 1e-9)
	require.InDelta(t, 0.5, stats.ExpirationRate, 1e-9)
}

func TestCheckinRepoSnapshotIncludesBoundProviders(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 2.5, 12)
	repo := NewCheckinRepository(nil, db)

	_, err := db.Exec(`INSERT INTO auth_identities (user_id, provider_type) VALUES (1, 'github'), (1, 'email')`)
	require.NoError(t, err)

	snapshot, err := repo.GetCheckinUserSnapshot(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), snapshot.ID)
	require.Equal(t, "active", snapshot.Status)
	require.InDelta(t, 2.5, snapshot.Balance, 1e-9)
	require.InDelta(t, 12, snapshot.TotalRecharged, 1e-9)
	require.Equal(t, "email", snapshot.SignupSource)
	require.ElementsMatch(t, []string{"github", "email"}, snapshot.BoundProviders)
}

func TestCheckinRepoSnapshotMissingUser(t *testing.T) {
	db := setupCheckinTestDB(t)
	repo := NewCheckinRepository(nil, db)

	_, err := repo.GetCheckinUserSnapshot(context.Background(), 404)
	require.Error(t, err)
	require.Equal(t, "USER_NOT_FOUND", reasonOfRepoErr(err))
}

func TestCheckinRepoDailyBudgetFirstCheckinOverCapAndFloatPrecision(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	insertCheckinTestUser(t, db, 2, 0, 0)
	insertCheckinTestUser(t, db, 3, 0, 0)
	repo := NewCheckinRepository(nil, db)

	// First check-in of the day exceeding budget must be rejected cleanly.
	settingsOver := checkinSettingsFixture(0.10)
	settingsOver.Risk.DailyTotalBudgetUSD = 0.05
	_, err := repo.ExecuteCheckin(context.Background(), newCheckinTestInput(t, 1, settingsOver))
	require.Error(t, err)
	require.Equal(t, service.CheckinReasonDailyBudgetExhausted, reasonOfRepoErr(err))

	var recCount, lotCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM user_checkin_records`).Scan(&recCount))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM user_expiring_balance_lots`).Scan(&lotCount))
	require.Zero(t, recCount)
	require.Zero(t, lotCount)

	// 0.1 + 0.2 == 0.3 must NOT fail due to IEEE-754 float drift on SQLite.
	settingsExact := checkinSettingsFixture(0.10)
	settingsExact.Risk.DailyTotalBudgetUSD = 0.30
	in1 := newCheckinTestInput(t, 1, settingsExact)
	in1.ClientIP = "198.51.100.1"
	_, err = repo.ExecuteCheckin(context.Background(), in1)
	require.NoError(t, err)

	settingsExact2 := checkinSettingsFixture(0.20)
	settingsExact2.Risk.DailyTotalBudgetUSD = 0.30
	in2 := newCheckinTestInput(t, 2, settingsExact2)
	in2.ClientIP = "198.51.100.2"
	_, err = repo.ExecuteCheckin(context.Background(), in2)
	require.NoError(t, err)

	// Next 0.01 must fail.
	settingsExact3 := checkinSettingsFixture(0.01)
	settingsExact3.Risk.DailyTotalBudgetUSD = 0.30
	in3 := newCheckinTestInput(t, 3, settingsExact3)
	in3.ClientIP = "198.51.100.3"
	_, err = repo.ExecuteCheckin(context.Background(), in3)
	require.Error(t, err)
	require.Equal(t, service.CheckinReasonDailyBudgetExhausted, reasonOfRepoErr(err))
}

func TestCheckinRepoUsesNowForAccountAgeAndCreatedAt(t *testing.T) {
	db := setupCheckinTestDB(t)
	now := timezone.Now()
	today := timezone.StartOfDay(now)
	// Ensure now is past today midnight by using an explicit now at 12:00 and createdAt 24h05m before now (after yesterday midnight).
	explicitNow := today.Add(12 * time.Hour)
	createdAt := explicitNow.Add(-24*time.Hour - 5*time.Minute)

	_, err := db.Exec(`
		INSERT INTO users (id, email, username, status, balance, total_recharged, signup_source, created_at)
		VALUES (1, 'age@example.com', 'age', 'active', 0, 0, 'email', ?)
	`, createdAt.UTC())
	require.NoError(t, err)

	repo := NewCheckinRepository(nil, db)
	settings := checkinSettingsFixture(0.1)
	settings.Eligibility.MinAccountAgeDays = 1

	in := service.CheckinExecuteInput{
		UserID:       1,
		ClientIP:     "198.51.100.9",
		Now:          explicitNow,
		Today:        today,
		Yesterday:    today.AddDate(0, 0, -1),
		BaseAmount:   0.1,
		ValidityDays: 1,
		ExpiresAt:    today.AddDate(0, 0, 1),
		Settings:     settings,
	}
	rec, err := repo.ExecuteCheckin(context.Background(), in)
	require.NoError(t, err)
	require.WithinDuration(t, explicitNow.UTC(), rec.CreatedAt.UTC(), time.Second)
}

func TestCheckinRepoMissingAuthIdentitiesTableFallbackOnSQLite(t *testing.T) {
	db := setupCheckinTestDB(t)
	_, err := db.Exec(`DROP TABLE auth_identities`)
	require.NoError(t, err)
	insertCheckinTestUser(t, db, 1, 0, 0)

	repo := NewCheckinRepository(nil, db)
	snap, err := repo.GetCheckinUserSnapshot(context.Background(), 1)
	require.NoError(t, err)
	require.Empty(t, snap.BoundProviders)
	require.Equal(t, "email", snap.SignupSource)
}

func TestCheckinRepoAdminDailyTrendLinkedAndOrphanLots(t *testing.T) {
	db := setupCheckinTestDB(t)
	insertCheckinTestUser(t, db, 1, 0, 0)
	repo := NewCheckinRepository(nil, db)

	settings := checkinSettingsFixture(0.4)
	rec, err := repo.ExecuteCheckin(context.Background(), newCheckinTestInput(t, 1, settings))
	require.NoError(t, err)

	_, err = db.Exec(`
		UPDATE user_expiring_balance_lots
		SET consumed_amount = 0.25, expired_amount = 0.15
		WHERE source_type = 'checkin' AND source_id = ?
	`, rec.ID)
	require.NoError(t, err)

	stats, err := repo.GetAdminCheckinStats(context.Background(), 30)
	require.NoError(t, err)
	require.InDelta(t, 0.25, stats.RangeTotalConsumed, 1e-9)
	require.InDelta(t, 0.15, stats.RangeTotalExpired, 1e-9)
	require.Len(t, stats.DailyTrend, 1)
	require.Equal(t, rec.CheckinDate, stats.DailyTrend[0].Date)
	require.Equal(t, 1, stats.DailyTrend[0].CheckinCount)
	require.InDelta(t, 0.40, stats.DailyTrend[0].GrantedAmount, 1e-9)
	require.InDelta(t, 0.25, stats.DailyTrend[0].ConsumedAmount, 1e-9)
	require.InDelta(t, 0.15, stats.DailyTrend[0].ExpiredAmount, 1e-9)
}

func TestCheckinRepoPostgresAdvisoryLockAndBudgetCondition(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewCheckinRepository(nil, db)
	ctx := withExpiringSQLDialect(context.Background(), dialect.Postgres)

	now := timezone.Now()
	today := timezone.StartOfDay(now)
	todayStr := today.Format("2006-01-02")

	settings := checkinSettingsFixture(0.25)
	settings.Risk.MaxAccountsPerIPDay = 2
	settings.Risk.DailyTotalBudgetUSD = 1.0

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id, COALESCE\(status, ''\), created_at`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status", "created_at", "total_recharged", "signup_source", "balance"}).
			AddRow(int64(7), "active", now.Add(-48*time.Hour), 0.0, "email", 0.0))
	mock.ExpectQuery(`SELECT DISTINCT provider_type FROM auth_identities`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"provider_type"}))
	mock.ExpectQuery(`SELECT COALESCE\(SUM\(remaining_amount\), 0\)\s+FROM user_expiring_balance_lots`).
		WithArgs(int64(7), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"sum"}).AddRow(0.0))
	mock.ExpectQuery(`FROM user_checkin_records\s+WHERE user_id = \$1`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "checkin_date", "streak_days", "milestone_cycle_day", "base_amount", "milestone_bonus_amount", "total_amount", "validity_days", "expires_at", "client_ip", "created_at"}))
	mock.ExpectExec(`SELECT pg_advisory_xact_lock\(\$1\)`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT COUNT\(DISTINCT user_id\)\s+FROM user_checkin_records`).
		WithArgs(todayStr, "198.51.100.77").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`INSERT INTO checkin_daily_budgets .* ON CONFLICT \(checkin_date\) DO NOTHING`).
		WithArgs(todayStr, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE checkin_daily_budgets\s+SET total_granted = total_granted \+ \$2`).
		WithArgs(todayStr, 0.25, sqlmock.AnyArg(), 1.0).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`INSERT INTO user_checkin_records`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(101)))
	mock.ExpectExec(`INSERT INTO user_expiring_balance_lots`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	rec, err := repo.ExecuteCheckin(ctx, service.CheckinExecuteInput{
		UserID:       7,
		ClientIP:     "198.51.100.77",
		Now:          now,
		Today:        today,
		Yesterday:    today.AddDate(0, 0, -1),
		BaseAmount:   0.25,
		ValidityDays: 1,
		ExpiresAt:    today.AddDate(0, 0, 1),
		Settings:     settings,
	})
	require.NoError(t, err)
	require.Equal(t, int64(101), rec.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}
