//go:build unit

package service

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func newSubTemplateSQLiteClient(t *testing.T, dbName string) (*dbent.Client, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbName+"?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	ddl := []string{
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
		`CREATE TABLE IF NOT EXISTS subscription_plan_templates (
			plan_id INTEGER PRIMARY KEY REFERENCES subscription_plans(id) ON DELETE CASCADE,
			template_id INTEGER NOT NULL REFERENCES subscription_templates(id)
		)`,
		`CREATE TABLE IF NOT EXISTS payment_order_subscriptions (
			order_id INTEGER PRIMARY KEY REFERENCES payment_orders(id) ON DELETE CASCADE,
			template_id INTEGER REFERENCES subscription_templates(id) ON DELETE SET NULL,
			target_subscription_id INTEGER REFERENCES user_subscriptions(id) ON DELETE SET NULL,
			fulfilled_subscription_id INTEGER REFERENCES user_subscriptions(id) ON DELETE SET NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS redeem_code_templates (
			redeem_code_id INTEGER PRIMARY KEY REFERENCES redeem_codes(id) ON DELETE CASCADE,
			template_id INTEGER NOT NULL REFERENCES subscription_templates(id),
			redeem_mode VARCHAR(20) NOT NULL DEFAULT 'stack'
		)`,
	}
	for _, stmt := range ddl {
		_, err := db.Exec(stmt)
		require.NoError(t, err)
	}
	return client, db
}

type sqliteTestGroupRepo struct {
	GroupRepository
	client *dbent.Client
}

func (r *sqliteTestGroupRepo) GetByID(ctx context.Context, id int64) (*Group, error) {
	c := entClientFromContext(ctx, r.client)
	g, err := c.Group.Get(ctx, id)
	if err != nil {
		return nil, ErrGroupNotFound
	}
	desc := ""
	if g.Description != nil {
		desc = *g.Description
	}
	return &Group{
		ID:                  g.ID,
		Name:                g.Name,
		Description:         desc,
		Platform:            g.Platform,
		RateMultiplier:      g.RateMultiplier,
		IsExclusive:         g.IsExclusive,
		Status:              g.Status,
		SubscriptionType:    g.SubscriptionType,
		DailyLimitUSD:       g.DailyLimitUsd,
		WeeklyLimitUSD:      g.WeeklyLimitUsd,
		MonthlyLimitUSD:     g.MonthlyLimitUsd,
		HourlyLimitUSD:      g.HourlyLimitUsd,
		HourlyWindowHours:   g.HourlyWindowHours,
		DefaultValidityDays: g.DefaultValidityDays,
	}, nil
}

type sqliteTestUserSubRepo struct {
	UserSubscriptionRepository
	client *dbent.Client
}

func (r *sqliteTestUserSubRepo) toService(ctx context.Context, c *dbent.Client, m *dbent.UserSubscription) *UserSubscription {
	notes := ""
	if m.Notes != nil {
		notes = *m.Notes
	}
	out := &UserSubscription{
		ID:                 m.ID,
		UserID:             m.UserID,
		GroupID:            m.GroupID,
		StartsAt:           m.StartsAt,
		ExpiresAt:          m.ExpiresAt,
		Status:             m.Status,
		DailyWindowStart:   m.DailyWindowStart,
		WeeklyWindowStart:  m.WeeklyWindowStart,
		MonthlyWindowStart: m.MonthlyWindowStart,
		HourlyWindowStart:  m.HourlyWindowStart,
		DailyUsageUSD:      m.DailyUsageUsd,
		WeeklyUsageUSD:     m.WeeklyUsageUsd,
		MonthlyUsageUSD:    m.MonthlyUsageUsd,
		HourlyUsageUSD:     m.HourlyUsageUsd,
		AssignedBy:         m.AssignedBy,
		AssignedAt:         m.AssignedAt,
		Notes:              notes,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
		DeletedAt:          m.DeletedAt,
	}
	if snaps, err := r.getSnapshots(ctx, c, []int64{out.ID}); err == nil {
		if snap, ok := snaps[out.ID]; ok {
			out.ApplySnapshot(snap)
		}
	}
	return out
}

func (r *sqliteTestUserSubRepo) getSnapshots(ctx context.Context, c *dbent.Client, ids []int64) (map[int64]*UserSubscriptionSnapshot, error) {
	out := make(map[int64]*UserSubscriptionSnapshot)
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	q := fmt.Sprintf(`SELECT subscription_id, template_id, template_name, daily_limit_usd, weekly_limit_usd, monthly_limit_usd, hourly_limit_usd, hourly_window_hours, updated_at FROM user_subscription_snapshots WHERE subscription_id IN (%s)`, extSQLInPlaceholders(c.Driver().Dialect(), 1, len(ids)))
	rows, err := c.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			snap       UserSubscriptionSnapshot
			tplID      sql.NullInt64
			d, w, m, h sql.NullFloat64
		)
		if err := rows.Scan(&snap.SubscriptionID, &tplID, &snap.TemplateName, &d, &w, &m, &h, &snap.HourlyWindowHours, &snap.UpdatedAt); err != nil {
			return nil, err
		}
		snap.TemplateID = nullableInt64Ptr(tplID)
		snap.DailyLimitUSD = nullableFloat64Ptr(d)
		snap.WeeklyLimitUSD = nullableFloat64Ptr(w)
		snap.MonthlyLimitUSD = nullableFloat64Ptr(m)
		snap.HourlyLimitUSD = nullableFloat64Ptr(h)
		cp := snap
		out[snap.SubscriptionID] = &cp
	}
	return out, rows.Err()
}

func (r *sqliteTestUserSubRepo) Create(ctx context.Context, sub *UserSubscription) error {
	c := entClientFromContext(ctx, r.client)
	startsAt := sub.StartsAt
	if startsAt.IsZero() {
		startsAt = time.Now().UTC()
	}
	status := sub.Status
	if status == "" {
		status = SubscriptionStatusActive
	}
	created, err := c.UserSubscription.Create().
		SetUserID(sub.UserID).
		SetGroupID(sub.GroupID).
		SetStartsAt(startsAt).
		SetExpiresAt(sub.ExpiresAt).
		SetStatus(status).
		SetNillableAssignedBy(sub.AssignedBy).
		SetNotes(sub.Notes).
		Save(ctx)
	if err != nil {
		return err
	}
	sub.ID = created.ID
	sub.StartsAt = created.StartsAt
	sub.ExpiresAt = created.ExpiresAt
	sub.Status = created.Status
	sub.CreatedAt = created.CreatedAt
	sub.UpdatedAt = created.UpdatedAt
	return nil
}

func (r *sqliteTestUserSubRepo) GetByID(ctx context.Context, id int64) (*UserSubscription, error) {
	c := entClientFromContext(ctx, r.client)
	m, err := c.UserSubscription.Query().Where(usersubscription.IDEQ(id)).Only(ctx)
	if err != nil {
		return nil, ErrSubscriptionNotFound
	}
	return r.toService(ctx, c, m), nil
}

func (r *sqliteTestUserSubRepo) GetByIDForUpdate(ctx context.Context, id int64) (*UserSubscription, error) {
	return r.GetByID(ctx, id)
}

func (r *sqliteTestUserSubRepo) GetByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*UserSubscription, error) {
	c := entClientFromContext(ctx, r.client)
	m, err := c.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID), usersubscription.GroupIDEQ(groupID)).
		Order(dbent.Desc(usersubscription.FieldExpiresAt), dbent.Desc(usersubscription.FieldID)).
		First(ctx)
	if err != nil {
		return nil, ErrSubscriptionNotFound
	}
	return r.toService(ctx, c, m), nil
}

func (r *sqliteTestUserSubRepo) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*UserSubscription, error) {
	list, err := r.ListActiveByUserIDAndGroupID(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrSubscriptionNotFound
	}
	return &list[0], nil
}

func (r *sqliteTestUserSubRepo) ListActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) ([]UserSubscription, error) {
	c := entClientFromContext(ctx, r.client)
	rows, err := c.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(userID),
			usersubscription.GroupIDEQ(groupID),
			usersubscription.StatusEQ(SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		Order(dbent.Asc(usersubscription.FieldExpiresAt), dbent.Asc(usersubscription.FieldCreatedAt), dbent.Asc(usersubscription.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserSubscription, 0, len(rows))
	for _, m := range rows {
		out = append(out, *r.toService(ctx, c, m))
	}
	return out, nil
}

func (r *sqliteTestUserSubRepo) listByTemplate(ctx context.Context, userID, templateID int64, activeOnly bool) ([]UserSubscription, error) {
	c := entClientFromContext(ctx, r.client)
	q := `SELECT us.id FROM user_subscriptions us JOIN user_subscription_snapshots uss ON uss.subscription_id = us.id WHERE us.user_id = ? AND uss.template_id = ? AND us.deleted_at IS NULL`
	args := []any{userID, templateID}
	if activeOnly {
		q += ` AND us.status = ? AND us.expires_at > ?`
		args = append(args, SubscriptionStatusActive, time.Now().UTC())
	}
	q += ` ORDER BY us.expires_at DESC, us.id DESC`
	rows, err := c.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return []UserSubscription{}, nil
	}
	models, err := c.UserSubscription.Query().
		Where(usersubscription.IDIn(ids...)).
		Order(dbent.Desc(usersubscription.FieldExpiresAt), dbent.Desc(usersubscription.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserSubscription, 0, len(models))
	for _, m := range models {
		out = append(out, *r.toService(ctx, c, m))
	}
	return out, nil
}

func (r *sqliteTestUserSubRepo) ListActiveByUserIDAndTemplateID(ctx context.Context, userID, templateID int64) ([]UserSubscription, error) {
	return r.listByTemplate(ctx, userID, templateID, true)
}

func (r *sqliteTestUserSubRepo) FindLatestByUserIDAndTemplateID(ctx context.Context, userID, templateID int64) (*UserSubscription, error) {
	list, err := r.listByTemplate(ctx, userID, templateID, false)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrSubscriptionNotFound
	}
	return &list[0], nil
}

func (r *sqliteTestUserSubRepo) UpsertSnapshot(ctx context.Context, snap *UserSubscriptionSnapshot) error {
	c := entClientFromContext(ctx, r.client)
	hours := snap.HourlyWindowHours
	if hours <= 0 {
		hours = 5
	}
	_, err := c.ExecContext(ctx, `
		INSERT INTO user_subscription_snapshots (
			subscription_id, template_id, template_name,
			daily_limit_usd, weekly_limit_usd, monthly_limit_usd, hourly_limit_usd,
			hourly_window_hours, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (subscription_id) DO UPDATE SET
			template_id = EXCLUDED.template_id,
			template_name = EXCLUDED.template_name,
			daily_limit_usd = EXCLUDED.daily_limit_usd,
			weekly_limit_usd = EXCLUDED.weekly_limit_usd,
			monthly_limit_usd = EXCLUDED.monthly_limit_usd,
			hourly_limit_usd = EXCLUDED.hourly_limit_usd,
			hourly_window_hours = EXCLUDED.hourly_window_hours,
			updated_at = EXCLUDED.updated_at`,
		snap.SubscriptionID,
		int64PtrToSQLArg(snap.TemplateID),
		snap.TemplateName,
		float64PtrToSQLArg(snap.DailyLimitUSD),
		float64PtrToSQLArg(snap.WeeklyLimitUSD),
		float64PtrToSQLArg(snap.MonthlyLimitUSD),
		float64PtrToSQLArg(snap.HourlyLimitUSD),
		hours,
		time.Now().UTC(),
	)
	return err
}

func (r *sqliteTestUserSubRepo) GetSnapshotsBySubscriptionIDs(ctx context.Context, subIDs []int64) (map[int64]*UserSubscriptionSnapshot, error) {
	return r.getSnapshots(ctx, entClientFromContext(ctx, r.client), subIDs)
}

func (r *sqliteTestUserSubRepo) ExtendExpiry(ctx context.Context, id int64, newExpiresAt time.Time) error {
	c := entClientFromContext(ctx, r.client)
	_, err := c.UserSubscription.UpdateOneID(id).SetExpiresAt(newExpiresAt).Save(ctx)
	return err
}

func (r *sqliteTestUserSubRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	c := entClientFromContext(ctx, r.client)
	_, err := c.UserSubscription.UpdateOneID(id).SetStatus(status).Save(ctx)
	return err
}

func (r *sqliteTestUserSubRepo) UpdateNotes(ctx context.Context, id int64, notes string) error {
	c := entClientFromContext(ctx, r.client)
	_, err := c.UserSubscription.UpdateOneID(id).SetNotes(notes).Save(ctx)
	return err
}

func (r *sqliteTestUserSubRepo) Delete(ctx context.Context, id int64) error {
	c := entClientFromContext(ctx, r.client)
	_, err := c.UserSubscription.UpdateOneID(id).SetDeletedAt(time.Now().UTC()).Save(ctx)
	return err
}

func (r *sqliteTestUserSubRepo) ExistsActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (bool, error) {
	list, err := r.ListActiveByUserIDAndGroupID(ctx, userID, groupID)
	return len(list) > 0, err
}

var _ UserSubscriptionMultiRepository = (*sqliteTestUserSubRepo)(nil)

func TestPaymentSubscriptionTemplate_LifecycleStackRenewRefund(t *testing.T) {
	ctx := context.Background()
	client, _ := newSubTemplateSQLiteClient(t, "payment_sub_template_lifecycle")

	u, err := client.User.Create().
		SetEmail("buyer@example.com").
		SetUsername("buyer").
		SetPasswordHash("hash").
		SetRole(RoleUser).
		SetStatus(StatusActive).
		Save(ctx)
	require.NoError(t, err)

	otherUser, err := client.User.Create().
		SetEmail("other@example.com").
		SetUsername("other").
		SetPasswordHash("hash").
		SetRole(RoleUser).
		SetStatus(StatusActive).
		Save(ctx)
	require.NoError(t, err)

	stdGroup, err := client.Group.Create().
		SetName("Claude Standard").
		SetPlatform(PlatformAnthropic).
		SetSubscriptionType(SubscriptionTypeStandard).
		SetRateMultiplier(1.0).
		SetStatus(StatusActive).
		Save(ctx)
	require.NoError(t, err)

	inst, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("Alipay Test").
		SetConfig("{}").
		SetSupportedTypes(payment.TypeAlipay).
		SetEnabled(true).
		SetRefundEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	groupRepo := &sqliteTestGroupRepo{client: client}
	subRepo := &sqliteTestUserSubRepo{client: client}
	subSvc := NewSubscriptionService(groupRepo, subRepo, nil, client, &config.Config{RunMode: config.RunModeStandard})
	defer subSvc.Stop()

	d15 := 15.0
	tpl, err := subSvc.CreateTemplate(ctx, &CreateSubscriptionTemplateInput{
		Name:                "Claude Starter",
		GroupID:             stdGroup.ID,
		DailyLimitUSD:       &d15,
		HourlyWindowHours:   5,
		DefaultValidityDays: 30,
	})
	require.NoError(t, err)

	d80 := 80.0
	otherTpl, err := subSvc.CreateTemplate(ctx, &CreateSubscriptionTemplateInput{
		Name:                "Claude Pro",
		GroupID:             stdGroup.ID,
		DailyLimitUSD:       &d80,
		HourlyWindowHours:   5,
		DefaultValidityDays: 30,
	})
	require.NoError(t, err)

	cfgSvc := NewPaymentConfigService(client, nil, nil)
	paySvc := &PaymentService{
		entClient:       client,
		subscriptionSvc: subSvc,
		configService:   cfgSvc,
		groupRepo:       groupRepo,
	}

	// P1: CreatePlan with TemplateID on a standard group auto-fills group_id & default validity_days.
	plan, err := cfgSvc.CreatePlan(ctx, CreatePlanRequest{
		TemplateID:   &tpl.ID,
		Name:         "Starter Monthly",
		Price:        29.9,
		ValidityDays: 0, // should inherit template's 30 days
		ValidityUnit: "days",
		ForSale:      true,
	})
	require.NoError(t, err)
	require.Equal(t, stdGroup.ID, plan.GroupID)
	require.Equal(t, 30, plan.ValidityDays)

	tplMap := cfgSvc.GetPlanTemplateMap(ctx, []*dbent.SubscriptionPlan{plan})
	require.Contains(t, tplMap, plan.ID)
	require.Equal(t, tpl.ID, tplMap[plan.ID].TemplateID)
	require.NotNil(t, tplMap[plan.ID].DailyLimitUSD)
	require.Equal(t, 15.0, *tplMap[plan.ID].DailyLimitUSD)

	// Plain plan bound to a standard group WITHOUT a template must be rejected by validateSubOrder.
	plainStdPlan, err := cfgSvc.CreatePlan(ctx, CreatePlanRequest{
		GroupID:      stdGroup.ID,
		Name:         "Broken Standard Plan",
		Price:        10,
		ValidityDays: 30,
		ValidityUnit: "days",
		ForSale:      true,
	})
	require.NoError(t, err)

	// P2: validateSubOrder passes for template-bound standard plan, rejects plain standard plan.
	gotPlan, err := paySvc.validateSubOrder(ctx, CreateOrderRequest{
		UserID:    u.ID,
		OrderType: payment.OrderTypeSubscription,
		PlanID:    plan.ID,
	})
	require.NoError(t, err)
	require.Equal(t, plan.ID, gotPlan.ID)

	_, err = paySvc.validateSubOrder(ctx, CreateOrderRequest{
		UserID:    u.ID,
		OrderType: payment.OrderTypeSubscription,
		PlanID:    plainStdPlan.ID,
	})
	require.Error(t, err)

	// Helper to create a paid subscription order and persist payment_order_subscriptions meta.
	createPaidOrder := func(outTradeNo string, targetSubID *int64) *dbent.PaymentOrder {
		o, err := client.PaymentOrder.Create().
			SetUserID(u.ID).
			SetUserEmail(u.Email).
			SetUserName(u.Username).
			SetAmount(plan.Price).
			SetPayAmount(plan.Price).
			SetFeeRate(0).
			SetRechargeCode("RC-" + outTradeNo).
			SetOutTradeNo(outTradeNo).
			SetPaymentType(payment.TypeAlipay).
			SetPaymentTradeNo("TX-" + outTradeNo).
			SetOrderType(payment.OrderTypeSubscription).
			SetPlanID(plan.ID).
			SetSubscriptionGroupID(plan.GroupID).
			SetSubscriptionDays(30).
			SetProviderInstanceID(fmt.Sprintf("%d", inst.ID)).
			SetProviderKey(payment.TypeAlipay).
			SetStatus(OrderStatusPaid).
			SetExpiresAt(time.Now().Add(time.Hour)).
			SetClientIP("127.0.0.1").
			SetSrcHost("example.com").
			Save(ctx)
		require.NoError(t, err)
		require.NoError(t, paySvc.persistOrderSubscriptionMetaInTx(ctx, o.ID, plan.ID, targetSubID))
		return o
	}

	// P3: Buying the same template twice stacks 2 independent active subscriptions with snapshots.
	order1 := createPaidOrder("sub2_tpl_stack_1", nil)
	require.NoError(t, paySvc.ExecuteSubscriptionFulfillment(ctx, order1.ID))

	order2 := createPaidOrder("sub2_tpl_stack_2", nil)
	require.NoError(t, paySvc.ExecuteSubscriptionFulfillment(ctx, order2.ID))

	activeSubs, err := subSvc.ListActiveSubscriptions(ctx, u.ID, stdGroup.ID)
	require.NoError(t, err)
	require.Len(t, activeSubs, 2)
	sub1 := activeSubs[0]
	sub2 := activeSubs[1]
	require.NotEqual(t, sub1.ID, sub2.ID)
	require.True(t, sub1.HasCustomLimits)
	require.Equal(t, 15.0, *sub1.DailyLimitUSD)
	require.True(t, sub2.HasCustomLimits)
	require.Equal(t, 15.0, *sub2.DailyLimitUSD)

	// P2 (continued): validateSubOrder rejects target_subscription_id belonging to another user or another template.
	_, err = paySvc.validateSubOrder(ctx, CreateOrderRequest{
		UserID:               otherUser.ID,
		OrderType:            payment.OrderTypeSubscription,
		PlanID:               plan.ID,
		TargetSubscriptionID: &sub1.ID,
	})
	require.ErrorIs(t, err, ErrInvalidTargetSubscription)

	otherTplSub, _, err := subSvc.AssignByTemplate(ctx, &AssignTemplateSubscriptionInput{
		UserID:       u.ID,
		TemplateID:   otherTpl.ID,
		ValidityDays: 30,
		Mode:         SubscriptionAssignModeStack,
	})
	require.NoError(t, err)
	_, err = paySvc.validateSubOrder(ctx, CreateOrderRequest{
		UserID:               u.ID,
		OrderType:            payment.OrderTypeSubscription,
		PlanID:               plan.ID,
		TargetSubscriptionID: &otherTplSub.ID,
	})
	require.ErrorIs(t, err, ErrInvalidTargetSubscription)

	// P4: Buying with TargetSubscriptionID = &sub1.ID renews ONLY sub1 (+30 days), leaving sub2 untouched.
	_, err = paySvc.validateSubOrder(ctx, CreateOrderRequest{
		UserID:               u.ID,
		OrderType:            payment.OrderTypeSubscription,
		PlanID:               plan.ID,
		TargetSubscriptionID: &sub1.ID,
	})
	require.NoError(t, err)

	sub1ExpiryBefore := sub1.ExpiresAt
	sub2ExpiryBefore := sub2.ExpiresAt
	orderRenew := createPaidOrder("sub2_tpl_renew_sub1", &sub1.ID)
	require.NoError(t, paySvc.ExecuteSubscriptionFulfillment(ctx, orderRenew.ID))

	reloadedSub1, err := subSvc.GetByID(ctx, sub1.ID)
	require.NoError(t, err)
	reloadedSub2, err := subSvc.GetByID(ctx, sub2.ID)
	require.NoError(t, err)
	require.True(t, reloadedSub1.ExpiresAt.Sub(sub1ExpiryBefore) >= 29*24*time.Hour, "sub1 must be extended by ~30 days")
	require.Equal(t, sub2ExpiryBefore.Unix(), reloadedSub2.ExpiresAt.Unix(), "sub2 must remain untouched")

	// Still only 2 subscriptions for tpl (sub1 + sub2; plus 1 for otherTpl).
	tplSubsAfterRenew, err := subRepo.ListActiveByUserIDAndTemplateID(ctx, u.ID, tpl.ID)
	require.NoError(t, err)
	require.Len(t, tplSubsAfterRenew, 2)

	// P5: Fulfillment idempotency — re-calling ExecuteSubscriptionFulfillment or fulfillOrderSubscriptionWithMeta is a no-op.
	require.NoError(t, paySvc.ExecuteSubscriptionFulfillment(ctx, order2.ID))
	idempotentSubID, handled, err := paySvc.fulfillOrderSubscriptionWithMeta(ctx, order2, 30, paymentSubscriptionOrderNote(order2.ID))
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, sub2.ID, idempotentSubID)
	tplSubsAfterRetry, err := subRepo.ListActiveByUserIDAndTemplateID(ctx, u.ID, tpl.ID)
	require.NoError(t, err)
	require.Len(t, tplSubsAfterRetry, 2)

	// P6: PrepareRefund on order2 targets exact fulfilled_subscription_id (sub2.ID), NOT sub1.ID.
	refundPlan, warnRes, err := paySvc.PrepareRefund(ctx, order2.ID, plan.Price, "customer refund", false, true)
	require.NoError(t, err)
	require.Nil(t, warnRes)
	require.NotNil(t, refundPlan)
	require.Equal(t, sub2.ID, refundPlan.SubscriptionID)
	require.Equal(t, 30, refundPlan.SubDaysToDeduct)

	// ClearTemplate removes the plan-template binding cleanly.
	_, err = cfgSvc.UpdatePlan(ctx, plan.ID, UpdatePlanRequest{ClearTemplate: true})
	require.NoError(t, err)
	tplMapAfterClear := cfgSvc.GetPlanTemplateMap(ctx, []*dbent.SubscriptionPlan{plan})
	require.Empty(t, tplMapAfterClear)

	// P7: Subscription purchase notification email shows plan title & exact fulfilled subscription expiry without leaking raw order ID.
	settingRepo := newNotificationEmailMemorySettingRepo()
	smtpServer := startNotificationEmailTestSMTPServer(t)
	require.NoError(t, settingRepo.SetMultiple(ctx, smtpServer.settings()))
	emailSvc := NewEmailService(settingRepo, nil)
	notifSvc := NewNotificationEmailService(settingRepo, emailSvc)
	notifSvc.RememberRecipientLocale(ctx, u.ID, u.Email, "zh-CN")
	paySvc.SetNotificationEmailService(notifSvc)

	require.NoError(t, paySvc.sendSubscriptionPurchaseSuccessNotification(ctx, order2))
	require.Equal(t, int64(1), smtpServer.messageCount())
	body := smtpServer.lastMessageBody(t)
	require.Contains(t, body, "Starter Monthly")
	require.Contains(t, body, "Claude Standard")
	require.Contains(t, body, reloadedSub2.ExpiresAt.Format("2006-01-02 15:04"))
	require.NotContains(t, body, "订单号")
	require.NotContains(t, body, "Order ID")
	require.NotContains(t, body, order2.OutTradeNo)
}
