//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func newMultiSubRepoTestClient(t *testing.T, name string) (*dbent.Client, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	sidecarDDL := []string{
		`CREATE TABLE IF NOT EXISTS subscription_templates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name VARCHAR(100) NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			group_id INTEGER NOT NULL REFERENCES groups(id),
			daily_limit_usd REAL,
			weekly_limit_usd REAL,
			monthly_limit_usd REAL,
			hourly_limit_usd REAL,
			hourly_window_hours INT NOT NULL DEFAULT 5,
			default_validity_days INT NOT NULL DEFAULT 30,
			status VARCHAR(20) NOT NULL DEFAULT 'active',
			sort_order INT NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS user_subscription_snapshots (
			subscription_id INTEGER PRIMARY KEY REFERENCES user_subscriptions(id) ON DELETE CASCADE,
			template_id INTEGER REFERENCES subscription_templates(id) ON DELETE SET NULL,
			template_name VARCHAR(100) NOT NULL DEFAULT '',
			daily_limit_usd REAL,
			weekly_limit_usd REAL,
			monthly_limit_usd REAL,
			hourly_limit_usd REAL,
			hourly_window_hours INT NOT NULL DEFAULT 5,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS redeem_code_templates (
			redeem_code_id INTEGER PRIMARY KEY REFERENCES redeem_codes(id) ON DELETE CASCADE,
			template_id INTEGER NOT NULL REFERENCES subscription_templates(id),
			redeem_mode VARCHAR(20) NOT NULL DEFAULT 'stack'
		)`,
	}
	for _, stmt := range sidecarDDL {
		_, err := db.Exec(stmt)
		require.NoError(t, err)
	}
	return client, db
}

func TestUserSubscriptionMultiRepo_FEFOAndSnapshotsAndRedeemTemplates(t *testing.T) {
	ctx := context.Background()
	client, db := newMultiSubRepoTestClient(t, "multi_sub_repo_fefo")

	u, err := client.User.Create().
		SetEmail("fefo-user@example.com").
		SetUsername("fefo-user").
		SetPasswordHash("hashed").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	g, err := client.Group.Create().
		SetName("Claude Standard").
		SetPlatform(service.PlatformAnthropic).
		SetSubscriptionType(service.SubscriptionTypeStandard).
		SetRateMultiplier(1.0).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	res, err := db.Exec(`INSERT INTO subscription_templates (name, group_id, daily_limit_usd, hourly_window_hours, default_validity_days, status) VALUES ('Starter', ?, 15.0, 5, 30, 'active')`, g.ID)
	require.NoError(t, err)
	tplID, err := res.LastInsertId()
	require.NoError(t, err)

	res2, err := db.Exec(`INSERT INTO subscription_templates (name, group_id, daily_limit_usd, hourly_window_hours, default_validity_days, status) VALUES ('Pro', ?, 80.0, 5, 30, 'active')`, g.ID)
	require.NoError(t, err)
	tplID2, err := res2.LastInsertId()
	require.NoError(t, err)

	subRepo := NewUserSubscriptionRepository(client)
	multiRepo, ok := subRepo.(service.UserSubscriptionMultiRepository)
	require.True(t, ok, "userSubscriptionRepository must implement UserSubscriptionMultiRepository")

	now := time.Now().Truncate(time.Second)

	// F1 & F2: Insert multiple active subscriptions for the SAME (user_id, group_id) out of expiry order,
	// plus one expired and one suspended row.
	subLate := &service.UserSubscription{
		UserID:    u.ID,
		GroupID:   g.ID,
		Status:    service.SubscriptionStatusActive,
		StartsAt:  now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	require.NoError(t, subRepo.Create(ctx, subLate))

	subEarly1 := &service.UserSubscription{
		UserID:    u.ID,
		GroupID:   g.ID,
		Status:    service.SubscriptionStatusActive,
		StartsAt:  now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(5 * 24 * time.Hour),
	}
	require.NoError(t, subRepo.Create(ctx, subEarly1))

	subEarly2 := &service.UserSubscription{
		UserID:    u.ID,
		GroupID:   g.ID,
		Status:    service.SubscriptionStatusActive,
		StartsAt:  now.Add(-1 * time.Hour),
		ExpiresAt: now.Add(5 * 24 * time.Hour), // same expires_at as subEarly1, higher id
	}
	require.NoError(t, subRepo.Create(ctx, subEarly2))

	subExpired := &service.UserSubscription{
		UserID:    u.ID,
		GroupID:   g.ID,
		Status:    service.SubscriptionStatusActive,
		StartsAt:  now.Add(-10 * 24 * time.Hour),
		ExpiresAt: now.Add(-24 * time.Hour),
	}
	require.NoError(t, subRepo.Create(ctx, subExpired))

	subSuspended := &service.UserSubscription{
		UserID:    u.ID,
		GroupID:   g.ID,
		Status:    service.SubscriptionStatusSuspended,
		StartsAt:  now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(10 * 24 * time.Hour),
	}
	require.NoError(t, subRepo.Create(ctx, subSuspended))

	// F3: Upsert snapshots: subEarly1 has $15 daily (tplID), subEarly2 has explicit unlimited (NULL daily, tplID2), subLate has NO snapshot (legacy).
	d15 := 15.0
	require.NoError(t, multiRepo.UpsertSnapshot(ctx, &service.UserSubscriptionSnapshot{
		SubscriptionID:    subEarly1.ID,
		TemplateID:        &tplID,
		TemplateName:      "Starter",
		DailyLimitUSD:     &d15,
		HourlyWindowHours: 5,
	}))
	require.NoError(t, multiRepo.UpsertSnapshot(ctx, &service.UserSubscriptionSnapshot{
		SubscriptionID:    subEarly2.ID,
		TemplateID:        &tplID2,
		TemplateName:      "Pro",
		DailyLimitUSD:     nil, // explicit unlimited
		HourlyWindowHours: 6,
	}))

	// Verify ListActiveByUserIDAndGroupID strictly enforces FEFO & attaches snapshots.
	active, err := multiRepo.ListActiveByUserIDAndGroupID(ctx, u.ID, g.ID)
	require.NoError(t, err)
	require.Len(t, active, 3)
	require.Equal(t, subEarly1.ID, active[0].ID)
	require.Equal(t, subEarly2.ID, active[1].ID)
	require.Equal(t, subLate.ID, active[2].ID)

	require.True(t, active[0].HasCustomLimits)
	require.NotNil(t, active[0].TemplateID)
	require.Equal(t, tplID, *active[0].TemplateID)
	require.Equal(t, "Starter", active[0].TemplateName)
	require.NotNil(t, active[0].DailyLimitUSD)
	require.Equal(t, 15.0, *active[0].DailyLimitUSD)

	require.True(t, active[1].HasCustomLimits)
	require.Nil(t, active[1].DailyLimitUSD)
	require.Equal(t, 6, active[1].HourlyWindowHours)

	require.False(t, active[2].HasCustomLimits)
	require.Nil(t, active[2].TemplateID)

	// F2: Single-row getters must NOT fail with NotSingularError when multiple active rows exist.
	firstActive, err := subRepo.GetActiveByUserIDAndGroupID(ctx, u.ID, g.ID)
	require.NoError(t, err)
	require.Equal(t, subEarly1.ID, firstActive.ID)
	require.True(t, firstActive.HasCustomLimits)

	latestAny, err := subRepo.GetByUserIDAndGroupID(ctx, u.ID, g.ID)
	require.NoError(t, err)
	require.Equal(t, subLate.ID, latestAny.ID)

	// F3: Template-scoped queries isolate subscriptions by template_id.
	tplSubs, err := multiRepo.ListActiveByUserIDAndTemplateID(ctx, u.ID, tplID)
	require.NoError(t, err)
	require.Len(t, tplSubs, 1)
	require.Equal(t, subEarly1.ID, tplSubs[0].ID)

	latestTpl, err := multiRepo.FindLatestByUserIDAndTemplateID(ctx, u.ID, tplID2)
	require.NoError(t, err)
	require.Equal(t, subEarly2.ID, latestTpl.ID)

	// UpsertSnapshot updates in place on conflict.
	d25 := 25.0
	require.NoError(t, multiRepo.UpsertSnapshot(ctx, &service.UserSubscriptionSnapshot{
		SubscriptionID:    subEarly1.ID,
		TemplateID:        &tplID,
		TemplateName:      "Starter",
		DailyLimitUSD:     &d25,
		HourlyWindowHours: 4,
	}))
	reloaded, err := subRepo.GetByID(ctx, subEarly1.ID)
	require.NoError(t, err)
	require.NotNil(t, reloaded.DailyLimitUSD)
	require.Equal(t, 25.0, *reloaded.DailyLimitUSD)
	require.Equal(t, 4, reloaded.HourlyWindowHours)

	// F4: RedeemCodeRepository persists & rehydrates redeem_code_templates on Create, CreateBatch, GetByCode, ListWithFilters.
	redeemRepo := NewRedeemCodeRepository(client)
	code1 := &service.RedeemCode{
		Code:         "TPL-SINGLE-001",
		Type:         service.RedeemTypeSubscription,
		Value:        0,
		Status:       service.StatusUnused,
		GroupID:      &g.ID,
		ValidityDays: 30,
		TemplateID:   &tplID,
		RedeemMode:   service.RedeemSubscriptionModeExtend,
	}
	require.NoError(t, redeemRepo.Create(ctx, code1))

	gotSingle, err := redeemRepo.GetByCode(ctx, "TPL-SINGLE-001")
	require.NoError(t, err)
	require.NotNil(t, gotSingle.TemplateID)
	require.Equal(t, tplID, *gotSingle.TemplateID)
	require.Equal(t, "Starter", gotSingle.TemplateName)
	require.Equal(t, service.RedeemSubscriptionModeExtend, gotSingle.RedeemMode)

	batch := []service.RedeemCode{
		{
			Code:         "TPL-BATCH-001",
			Type:         service.RedeemTypeSubscription,
			Status:       service.StatusUnused,
			GroupID:      &g.ID,
			ValidityDays: 15,
			TemplateID:   &tplID2,
			RedeemMode:   service.RedeemSubscriptionModeStack,
		},
		{
			Code:         "LEGACY-BATCH-002",
			Type:         service.RedeemTypeSubscription,
			Status:       service.StatusUnused,
			GroupID:      &g.ID,
			ValidityDays: 15,
		},
	}
	require.NoError(t, redeemRepo.CreateBatch(ctx, batch))

	listed, pag, err := redeemRepo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 20}, service.RedeemTypeSubscription, "", "")
	require.NoError(t, err)
	require.Equal(t, int64(3), pag.Total)
	byCode := map[string]service.RedeemCode{}
	for _, c := range listed {
		byCode[c.Code] = c
	}
	require.NotNil(t, byCode["TPL-BATCH-001"].TemplateID)
	require.Equal(t, tplID2, *byCode["TPL-BATCH-001"].TemplateID)
	require.Equal(t, "Pro", byCode["TPL-BATCH-001"].TemplateName)
	require.Equal(t, service.RedeemSubscriptionModeStack, byCode["TPL-BATCH-001"].RedeemMode)
	require.Nil(t, byCode["LEGACY-BATCH-002"].TemplateID)
}
