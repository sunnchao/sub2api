package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupPaymentExpiringTestEnv(t *testing.T, name string) (
	*dbent.Client,
	*sql.DB,
	*service.PaymentService,
	*service.PaymentConfigService,
	*service.SubscriptionService,
	*ExpiringBalanceRepo,
	*miniredis.Miniredis,
) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared&_fk=1")
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
		`CREATE TABLE IF NOT EXISTS user_expiring_balance_lots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			source_type TEXT NOT NULL DEFAULT 'checkin',
			source_id INTEGER NOT NULL,
			initial_amount REAL NOT NULL,
			remaining_amount REAL NOT NULL,
			frozen_amount REAL NOT NULL DEFAULT 0,
			consumed_amount REAL NOT NULL DEFAULT 0,
			expired_amount REAL NOT NULL DEFAULT 0,
			expires_at DATETIME NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_expiring_lots_source ON user_expiring_balance_lots (source_type, source_id)`,
		`CREATE TABLE IF NOT EXISTS user_expiring_balance_allocations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			ref_type TEXT NOT NULL,
			ref_key TEXT NOT NULL,
			alloc_seq INTEGER NOT NULL,
			lot_id INTEGER NULL REFERENCES user_expiring_balance_lots(id) ON DELETE RESTRICT,
			allocated_amount REAL NOT NULL,
			captured_amount REAL NOT NULL DEFAULT 0,
			released_amount REAL NOT NULL DEFAULT 0,
			forfeited_expired_amount REAL NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_expiring_alloc_ref_seq ON user_expiring_balance_allocations (ref_type, ref_key, alloc_seq)`,
	}
	for _, stmt := range ddl {
		_, err := db.Exec(stmt)
		require.NoError(t, err)
	}

	ctx := context.Background()
	settingRepo := NewSettingRepository(client)
	require.NoError(t, settingRepo.Set(ctx, service.SettingPaymentEnabled, "true"))

	userRepo := &sqliteUserRepoNoNow{UserRepository: NewUserRepository(client, db), client: client}
	groupRepo := NewGroupRepository(client, db)
	rawSubRepo := NewUserSubscriptionRepository(client)
	multiSubRepo, ok := rawSubRepo.(service.UserSubscriptionMultiRepository)
	require.True(t, ok)
	subRepo := &sqliteUserSubRepoNoForUpdate{
		UserSubscriptionRepository:      rawSubRepo,
		UserSubscriptionMultiRepository: multiSubRepo,
	}
	redeemRepo := NewRedeemCodeRepository(client)
	expRepo := NewExpiringBalanceRepository(client, db)

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cfg := &config.Config{RunMode: config.RunModeStandard}
	billingCacheSvc := service.NewBillingCacheService(NewBillingCache(rdb), userRepo, subRepo, nil, nil, nil, cfg, nil)
	billingCacheSvc.SetExpiringBalanceRepo(expRepo)
	t.Cleanup(billingCacheSvc.Stop)

	subSvc := NewSubscriptionServiceForTest(groupRepo, subRepo, billingCacheSvc, client, cfg)
	t.Cleanup(subSvc.Stop)
	redeemSvc := service.NewRedeemService(redeemRepo, userRepo, subSvc, nil, billingCacheSvc, client, nil, nil)

	cfgSvc := service.NewPaymentConfigService(client, settingRepo, nil)
	paySvc := service.NewPaymentService(client, nil, nil, redeemSvc, subSvc, cfgSvc, userRepo, groupRepo, nil)
	paySvc.SetExpiringBalanceRepo(expRepo)

	return client, db, paySvc, cfgSvc, subSvc, expRepo, mr
}

// sqliteUserRepoNoNow adapts userRepository.AdjustBalance for SQLite (which lacks NOW()).
type sqliteUserRepoNoNow struct {
	service.UserRepository
	client *dbent.Client
}

func (r *sqliteUserRepoNoNow) AdjustBalance(ctx context.Context, id int64, delta float64) (service.BalanceChange, error) {
	c := clientFromContext(ctx, r.client)
	u, err := c.User.Get(ctx, id)
	if err != nil {
		return service.BalanceChange{}, service.ErrUserNotFound
	}
	next := u.Balance + delta
	if next < 0 {
		return service.BalanceChange{Old: u.Balance, New: next}, service.ErrBalanceNegative
	}
	updated, err := c.User.UpdateOneID(id).SetBalance(next).Save(ctx)
	if err != nil {
		return service.BalanceChange{}, err
	}
	return service.BalanceChange{Old: u.Balance, New: updated.Balance}, nil
}

// sqliteUserSubRepoNoForUpdate adapts UserSubscriptionRepository for SQLite tests
// because SQLite does not support SELECT ... FOR UPDATE.
type sqliteUserSubRepoNoForUpdate struct {
	service.UserSubscriptionRepository
	service.UserSubscriptionMultiRepository
}

func (r *sqliteUserSubRepoNoForUpdate) GetByIDForUpdate(ctx context.Context, id int64) (*service.UserSubscription, error) {
	return r.GetByID(ctx, id)
}

// NewSubscriptionServiceForTest avoids duplicating SubscriptionService setup while allowing clean cleanup.
func NewSubscriptionServiceForTest(
	groupRepo service.GroupRepository,
	subRepo service.UserSubscriptionRepository,
	billingCache *service.BillingCacheService,
	client *dbent.Client,
	cfg *config.Config,
) *service.SubscriptionService {
	return service.NewSubscriptionService(groupRepo, subRepo, billingCache, client, cfg)
}

func TestPaymentBalancePurchaseAndAntiLaunderingRefund_ExpiringLots(t *testing.T) {
	ctx := context.Background()
	client, db, paySvc, cfgSvc, subSvc, expRepo, mr := setupPaymentExpiringTestEnv(t, "payment_expiring_lifecycle")

	u, err := client.User.Create().
		SetEmail("sub-expiring@example.com").
		SetUsername("sub-expiring").
		SetPasswordHash("hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		SetBalance(15.0).
		Save(ctx)
	require.NoError(t, err)

	stdGroup, err := client.Group.Create().
		SetName("Pro Standard").
		SetPlatform(service.PlatformAnthropic).
		SetSubscriptionType(service.SubscriptionTypeStandard).
		SetRateMultiplier(1.0).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	tpl, err := subSvc.CreateTemplate(ctx, &service.CreateSubscriptionTemplateInput{
		Name:                "Pro Monthly",
		GroupID:             stdGroup.ID,
		DefaultValidityDays: 30,
	})
	require.NoError(t, err)

	plan, err := cfgSvc.CreatePlan(ctx, service.CreatePlanRequest{
		TemplateID:   &tpl.ID,
		Name:         "Pro Plan",
		Price:        20.0,
		ValidityDays: 30,
		ValidityUnit: "days",
		ForSale:      true,
	})
	require.NoError(t, err)

	now := time.Now().UTC()
	expSoon := now.Add(6 * time.Hour)
	expLater := now.Add(24 * time.Hour)
	lot1 := insertTestLot(t, db, u.ID, 601, 4.0, expSoon)
	lot2 := insertTestLot(t, db, u.ID, 602, 8.0, expLater)

	// Pre-populate billing:balance cache so we can verify post-commit invalidation.
	require.NoError(t, mr.Set(billingBalanceKey(u.ID), "27.0"))

	// 1. Purchase $20 plan: FEFO uses lot1 ($4) + lot2 ($8) + regular ($8).
	// Remaining: regular = $7.0, lot1 = $0 (exhausted), lot2 = $0 (exhausted).
	resp1, err := paySvc.CreateOrder(ctx, service.CreateOrderRequest{
		UserID:      u.ID,
		Amount:      plan.Price,
		PaymentType: payment.TypeBalance,
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      plan.ID,
	})
	require.NoError(t, err)
	require.Equal(t, service.OrderStatusCompleted, resp1.Status)

	regBal, _ := readUserBalances(t, db, u.ID)
	require.InDelta(t, 7.0, regBal, 1e-8)
	assertLotAccountingInvariant(t, db, lot1)
	assertLotAccountingInvariant(t, db, lot2)

	// Verify SUBSCRIPTION_SUCCESS audit contains expiring_amount_used=12 and regular_amount_used=8.
	audits, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(fmt.Sprintf("%d", resp1.OrderID)), paymentauditlog.ActionEQ("SUBSCRIPTION_SUCCESS")).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, audits, 1)
	var successDetail map[string]any
	require.NoError(t, json.Unmarshal([]byte(audits[0].Detail), &successDetail))
	require.InDelta(t, 12.0, successDetail["expiring_amount_used"], 1e-8)
	require.InDelta(t, 8.0, successDetail["regular_amount_used"], 1e-8)

	// Cache invalidation runs asynchronously in RedeemService; allow it to clear.
	require.Eventually(t, func() bool {
		return !mr.Exists(billingBalanceKey(u.ID))
	}, 2*time.Second, 10*time.Millisecond)

	// 2. Insufficient balance ($7 < $20) rolls back atomically (zero orphaned orders or subscriptions).
	ordersBefore, err := client.PaymentOrder.Query().Count(ctx)
	require.NoError(t, err)
	subsBefore, err := subSvc.ListActiveSubscriptions(ctx, u.ID, stdGroup.ID)
	require.NoError(t, err)
	_, err = paySvc.CreateOrder(ctx, service.CreateOrderRequest{
		UserID:      u.ID,
		Amount:      plan.Price,
		PaymentType: payment.TypeBalance,
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      plan.ID,
	})
	require.Error(t, err)
	require.Equal(t, "INSUFFICIENT_BALANCE", infraerrors.Reason(err))
	ordersAfter, err := client.PaymentOrder.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, ordersBefore, ordersAfter)
	subsAfter, err := subSvc.ListActiveSubscriptions(ctx, u.ID, stdGroup.ID)
	require.NoError(t, err)
	require.Len(t, subsAfter, len(subsBefore))

	// 3. Anti-laundering refund when lot1 has EXPIRED and lot2 is STILL ACTIVE!
	_, err = db.Exec(`UPDATE user_expiring_balance_lots SET expires_at = ? WHERE id = ?`, now.Add(-1*time.Minute), lot1)
	require.NoError(t, err)

	refundPlan, warnRes, err := paySvc.PrepareRefund(ctx, resp1.OrderID, plan.Price, "anti-laundering refund", false, true)
	require.NoError(t, err)
	require.Nil(t, warnRes)
	refundRes, err := paySvc.ExecuteRefund(ctx, refundPlan)
	require.NoError(t, err)
	require.True(t, refundRes.Success)

	// Regular restored by $8 -> $7 + $8 = $15 (NEVER $27!).
	regAfterRefund, _ := readUserBalances(t, db, u.ID)
	require.InDelta(t, 15.0, regAfterRefund, 1e-8)

	// lot1 ($4) was expired -> forfeited into expired_amount, status='expired'.
	rem1, _, cons1, exp1, status1, _ := assertLotAccountingInvariant(t, db, lot1)
	require.InDelta(t, 0.0, rem1, 1e-8)
	require.InDelta(t, 0.0, cons1, 1e-8)
	require.InDelta(t, 4.0, exp1, 1e-8)
	require.Equal(t, service.ExpiringLotStatusExpired, status1)

	// lot2 ($8) was still active -> restored to remaining_amount = $8, status='active', original expires_at preserved!
	rem2, _, cons2, exp2, status2, gotExp2 := assertLotAccountingInvariant(t, db, lot2)
	require.InDelta(t, 8.0, rem2, 1e-8)
	require.InDelta(t, 0.0, cons2, 1e-8)
	require.InDelta(t, 0.0, exp2, 1e-8)
	require.Equal(t, service.ExpiringLotStatusActive, status2)
	require.Equal(t, expLater.Unix(), gotExp2.Unix())

	// Summary reflects active $8 from lot2.
	summary, err := expRepo.GetActiveExpiringSummary(ctx, u.ID)
	require.NoError(t, err)
	require.InDelta(t, 8.0, summary.ActiveBalance, 1e-8)

	// Audit BALANCE_REFUND_CREDITED records exact breakdown: regular=8, expiring=8, forfeited=4.
	refundAudits, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(fmt.Sprintf("%d", resp1.OrderID)), paymentauditlog.ActionEQ("BALANCE_REFUND_CREDITED")).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, refundAudits, 1)
	var refundDetail map[string]any
	require.NoError(t, json.Unmarshal([]byte(refundAudits[0].Detail), &refundDetail))
	require.InDelta(t, 8.0, refundDetail["regular_refunded"], 1e-8)
	require.InDelta(t, 8.0, refundDetail["expiring_refunded"], 1e-8)
	require.InDelta(t, 4.0, refundDetail["expiring_forfeited"], 1e-8)
}

func TestPaymentBalancePurchaseAndPartialRefundAndRenewal_ExpiringLots(t *testing.T) {
	ctx := context.Background()
	client, db, paySvc, cfgSvc, subSvc, expRepo, _ := setupPaymentExpiringTestEnv(t, "payment_expiring_partial_renew")

	u, err := client.User.Create().
		SetEmail("partial-renew@example.com").
		SetUsername("partial-renew").
		SetPasswordHash("hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		SetBalance(14.0).
		Save(ctx)
	require.NoError(t, err)

	stdGroup, err := client.Group.Create().
		SetName("Partial Group").
		SetPlatform(service.PlatformOpenAI).
		SetSubscriptionType(service.SubscriptionTypeStandard).
		SetRateMultiplier(1.0).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	tpl, err := subSvc.CreateTemplate(ctx, &service.CreateSubscriptionTemplateInput{
		Name:                "Partial Plan Tpl",
		GroupID:             stdGroup.ID,
		DefaultValidityDays: 30,
	})
	require.NoError(t, err)

	plan, err := cfgSvc.CreatePlan(ctx, service.CreatePlanRequest{
		TemplateID:   &tpl.ID,
		Name:         "Partial Plan",
		Price:        10.0,
		ValidityDays: 30,
		ValidityUnit: "days",
		ForSale:      true,
	})
	require.NoError(t, err)

	now := time.Now().UTC()
	lot := insertTestLot(t, db, u.ID, 701, 6.0, now.Add(24*time.Hour))

	// Purchase #1 ($10): consumes $6 lot + $4 regular -> regular=$10, lot=$0.
	resp1, err := paySvc.CreateOrder(ctx, service.CreateOrderRequest{
		UserID:      u.ID,
		Amount:      plan.Price,
		PaymentType: payment.TypeBalance,
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      plan.ID,
	})
	require.NoError(t, err)

	subs, err := subSvc.ListActiveSubscriptions(ctx, u.ID, stdGroup.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	targetSubID := subs[0].ID

	// Purchase #2: Renewal of existing subscription (exercises user_subscriptions -> users -> lots lock order)
	// using remaining $10 regular -> regular=$0.
	resp2, err := paySvc.CreateOrder(ctx, service.CreateOrderRequest{
		UserID:               u.ID,
		Amount:               plan.Price,
		PaymentType:          payment.TypeBalance,
		OrderType:            payment.OrderTypeSubscription,
		PlanID:               plan.ID,
		TargetSubscriptionID: &targetSubID,
	})
	require.NoError(t, err)
	require.Equal(t, service.OrderStatusCompleted, resp2.Status)

	regBal, _ := readUserBalances(t, db, u.ID)
	require.InDelta(t, 0.0, regBal, 1e-8)

	// Two-step partial refund on order1 ($3 first -> LIFO refunds regular $3; then $4 -> remaining $1 regular + $3 expiring lot).
	hasAlloc1, resStep1, err := expRepo.RefundPaymentOrderBalance(ctx, u.ID, resp1.OrderID, 3.0)
	require.NoError(t, err)
	require.True(t, hasAlloc1)
	require.InDelta(t, 3.0, resStep1.RegularRefunded, 1e-8)
	require.InDelta(t, 0.0, resStep1.ExpiringRefunded, 1e-8)

	hasAlloc2, resStep2, err := expRepo.RefundPaymentOrderBalance(ctx, u.ID, resp1.OrderID, 4.0)
	require.NoError(t, err)
	require.True(t, hasAlloc2)
	require.InDelta(t, 1.0, resStep2.RegularRefunded, 1e-8)
	require.InDelta(t, 3.0, resStep2.ExpiringRefunded, 1e-8)

	rem, _, _, _, status, _ := assertLotAccountingInvariant(t, db, lot)
	require.InDelta(t, 3.0, rem, 1e-8)
	require.Equal(t, service.ExpiringLotStatusActive, status)

	// Legacy order without expiring allocation rows (order2 paid purely with regular balance):
	// refundBalancePaidOrder falls back cleanly to AdjustBalance ($10 -> $4 + $10 = $14).
	refundPlan2, _, err := paySvc.PrepareRefund(ctx, resp2.OrderID, plan.Price, "legacy fallback refund", false, true)
	require.NoError(t, err)
	refundRes2, err := paySvc.ExecuteRefund(ctx, refundPlan2)
	require.NoError(t, err)
	require.True(t, refundRes2.Success, "refundResult=%+v", refundRes2)
	regFinal, _ := readUserBalances(t, db, u.ID)
	require.InDelta(t, 14.0, regFinal, 1e-8)
}

func TestPaymentBalancePurchase_TypedNilExpiringBalanceRepoFallback(t *testing.T) {
	ctx := context.Background()
	client, db, paySvc, cfgSvc, subSvc, _, _ := setupPaymentExpiringTestEnv(t, "payment_typed_nil_fallback")

	var typedNilRepo *ExpiringBalanceRepo
	paySvc.SetExpiringBalanceRepo(typedNilRepo)

	userRepo := &sqliteUserRepoNoNow{UserRepository: NewUserRepository(client, db), client: client}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	billingCacheSvc := service.NewBillingCacheService(nil, userRepo, nil, nil, nil, nil, cfg, nil)
	billingCacheSvc.SetExpiringBalanceRepo(typedNilRepo)
	t.Cleanup(billingCacheSvc.Stop)

	u, err := client.User.Create().
		SetEmail("typed-nil@example.com").
		SetUsername("typed-nil").
		SetPasswordHash("hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		SetBalance(25.0).
		Save(ctx)
	require.NoError(t, err)

	stdGroup, err := client.Group.Create().
		SetName("TypedNil Group").
		SetPlatform(service.PlatformAnthropic).
		SetSubscriptionType(service.SubscriptionTypeStandard).
		SetRateMultiplier(1.0).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	tpl, err := subSvc.CreateTemplate(ctx, &service.CreateSubscriptionTemplateInput{
		Name:                "TypedNil Monthly",
		GroupID:             stdGroup.ID,
		DefaultValidityDays: 30,
	})
	require.NoError(t, err)

	plan, err := cfgSvc.CreatePlan(ctx, service.CreatePlanRequest{
		TemplateID:   &tpl.ID,
		Name:         "TypedNil Plan",
		Price:        10.0,
		ValidityDays: 30,
		ValidityUnit: "days",
		ForSale:      true,
	})
	require.NoError(t, err)

	// BillingCacheService.GetUserBalance must fall back cleanly to regular balance.
	balBefore, err := billingCacheSvc.GetUserBalance(ctx, u.ID)
	require.NoError(t, err)
	require.InDelta(t, 25.0, balBefore, 1e-8)

	// PaymentService.CreateOrder must fall back cleanly to userRepo.AdjustBalance.
	resp, err := paySvc.CreateOrder(ctx, service.CreateOrderRequest{
		UserID:      u.ID,
		Amount:      plan.Price,
		PaymentType: payment.TypeBalance,
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      plan.ID,
	})
	require.NoError(t, err)
	require.Equal(t, service.OrderStatusCompleted, resp.Status)

	regBal, _ := readUserBalances(t, db, u.ID)
	require.InDelta(t, 15.0, regBal, 1e-8)

	balAfter, err := billingCacheSvc.GetUserBalance(ctx, u.ID)
	require.NoError(t, err)
	require.InDelta(t, 15.0, balAfter, 1e-8)
}
