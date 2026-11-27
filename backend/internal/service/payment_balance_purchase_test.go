//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type sqliteTestUserRepo struct {
	UserRepository
	client *dbent.Client
}

func (r *sqliteTestUserRepo) GetByID(ctx context.Context, id int64) (*User, error) {
	c := entClientFromContext(ctx, r.client)
	u, err := c.User.Get(ctx, id)
	if err != nil {
		return nil, ErrUserNotFound
	}
	return &User{
		ID:       u.ID,
		Email:    u.Email,
		Username: u.Username,
		Role:     u.Role,
		Status:   u.Status,
		Balance:  u.Balance,
		Notes:    u.Notes,
	}, nil
}

func (r *sqliteTestUserRepo) AdjustBalance(ctx context.Context, id int64, delta float64) (BalanceChange, error) {
	c := entClientFromContext(ctx, r.client)
	u, err := c.User.Get(ctx, id)
	if err != nil {
		return BalanceChange{}, ErrUserNotFound
	}
	next := u.Balance + delta
	if next < 0 {
		return BalanceChange{Old: u.Balance, New: next}, ErrBalanceNegative
	}
	updated, err := c.User.UpdateOneID(id).SetBalance(next).Save(ctx)
	if err != nil {
		return BalanceChange{}, err
	}
	return BalanceChange{Old: u.Balance, New: updated.Balance}, nil
}

func TestBalanceSubscriptionPurchase_StackRenewInsufficientAndRefund(t *testing.T) {
	ctx := context.Background()
	client, _ := newSubTemplateSQLiteClient(t, "balance_sub_purchase_lifecycle")

	u, err := client.User.Create().
		SetEmail("balance-buyer@example.com").
		SetUsername("balance-buyer").
		SetPasswordHash("hash").
		SetRole(RoleUser).
		SetStatus(StatusActive).
		SetBalance(100.0).
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

	groupRepo := &sqliteTestGroupRepo{client: client}
	subRepo := &sqliteTestUserSubRepo{client: client}
	userRepo := &sqliteTestUserRepo{client: client}
	subSvc := NewSubscriptionService(groupRepo, subRepo, nil, client, &config.Config{RunMode: config.RunModeStandard})
	defer subSvc.Stop()

	d25 := 25.0
	tpl, err := subSvc.CreateTemplate(ctx, &CreateSubscriptionTemplateInput{
		Name:                "Claude Pro Pack",
		GroupID:             stdGroup.ID,
		DailyLimitUSD:       &d25,
		HourlyWindowHours:   5,
		DefaultValidityDays: 30,
	})
	require.NoError(t, err)

	// Notification email stub + payment config settings (with CNY rate & fee rate to prove balance payment ignores both).
	settingRepo := newNotificationEmailMemorySettingRepo()
	smtpServer := startNotificationEmailTestSMTPServer(t)
	settings := smtpServer.settings()
	settings[SettingPaymentEnabled] = "true"
	settings[SettingSubscriptionUSDToCNYRate] = "7.2"
	settings[SettingRechargeFeeRate] = "5"
	require.NoError(t, settingRepo.SetMultiple(ctx, settings))
	emailSvc := NewEmailService(settingRepo, nil)
	notifSvc := NewNotificationEmailService(settingRepo, emailSvc)

	// NOTE: zero online payment providers configured (no PaymentProviderInstance rows, registry/loadBalancer are nil).
	cfgSvc := NewPaymentConfigService(client, settingRepo, nil)
	paySvc := &PaymentService{
		entClient:       client,
		subscriptionSvc: subSvc,
		configService:   cfgSvc,
		groupRepo:       groupRepo,
		userRepo:        userRepo,
	}
	paySvc.SetNotificationEmailService(notifSvc)

	plan, err := cfgSvc.CreatePlan(ctx, CreatePlanRequest{
		TemplateID:   &tpl.ID,
		Name:         "Claude Pro Monthly",
		Price:        29.5,
		ValidityDays: 30,
		ValidityUnit: "days",
		ForSale:      true,
	})
	require.NoError(t, err)

	// B1: First balance purchase succeeds with zero online gateways, charges exact USD price (0 fee, no CNY conversion).
	resp1, err := paySvc.CreateOrder(ctx, CreateOrderRequest{
		UserID:      u.ID,
		Amount:      plan.Price,
		PaymentType: payment.TypeBalance,
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      plan.ID,
		ClientIP:    "127.0.0.1",
		SrcHost:     "example.com",
	})
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, resp1.Status)
	require.Equal(t, payment.CreatePaymentResultCompleted, resp1.ResultType)
	require.Equal(t, payment.TypeBalance, resp1.PaymentType)
	require.Equal(t, "USD", resp1.Currency)
	require.InDelta(t, 29.5, resp1.Amount, 1e-6)
	require.InDelta(t, 29.5, resp1.PayAmount, 1e-6)
	require.Zero(t, resp1.FeeRate)

	reloadedUser, err := userRepo.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.InDelta(t, 70.5, reloadedUser.Balance, 1e-6)

	order1, err := client.PaymentOrder.Get(ctx, resp1.OrderID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, order1.Status)
	require.Equal(t, payment.TypeBalance, order1.PaymentType)
	require.Equal(t, "USD", PaymentOrderCurrency(order1))

	audits1, err := client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(fmt.Sprintf("%d", order1.ID))).All(ctx)
	require.NoError(t, err)
	actions1 := make([]string, 0, len(audits1))
	for _, a := range audits1 {
		actions1 = append(actions1, a.Action)
	}
	require.Contains(t, actions1, "SUBSCRIPTION_ASSIGNED")
	require.Contains(t, actions1, "SUBSCRIPTION_SUCCESS")

	require.Eventually(t, func() bool {
		return smtpServer.messageCount() >= 1
	}, 3*time.Second, 20*time.Millisecond)
	body := smtpServer.lastMessageBody(t)
	require.Contains(t, body, "Claude Pro Monthly")
	require.NotContains(t, body, order1.OutTradeNo)

	// B2: Second balance purchase (stack mode) stacks a second independent subscription with snapshotted limits.
	resp2, err := paySvc.CreateOrder(ctx, CreateOrderRequest{
		UserID:      u.ID,
		Amount:      plan.Price,
		PaymentType: payment.TypeBalance,
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      plan.ID,
	})
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, resp2.Status)

	reloadedUser, err = userRepo.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.InDelta(t, 41.0, reloadedUser.Balance, 1e-6)

	activeSubs, err := subSvc.ListActiveSubscriptions(ctx, u.ID, stdGroup.ID)
	require.NoError(t, err)
	require.Len(t, activeSubs, 2)
	sub1 := activeSubs[0]
	sub2 := activeSubs[1]
	require.NotEqual(t, sub1.ID, sub2.ID)
	require.True(t, sub1.HasCustomLimits)
	require.Equal(t, 25.0, *sub1.DailyLimitUSD)
	require.True(t, sub2.HasCustomLimits)
	require.Equal(t, 25.0, *sub2.DailyLimitUSD)

	// B3: Balance purchase with TargetSubscriptionID renews ONLY sub1 (+30d), leaving sub2 untouched.
	sub1ExpBefore := sub1.ExpiresAt
	sub2ExpBefore := sub2.ExpiresAt
	resp3, err := paySvc.CreateOrder(ctx, CreateOrderRequest{
		UserID:               u.ID,
		Amount:               plan.Price,
		PaymentType:          payment.TypeBalance,
		OrderType:            payment.OrderTypeSubscription,
		PlanID:               plan.ID,
		TargetSubscriptionID: &sub1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, resp3.Status)

	reloadedUser, err = userRepo.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.InDelta(t, 11.5, reloadedUser.Balance, 1e-6)

	reloadedSub1, err := subSvc.GetByID(ctx, sub1.ID)
	require.NoError(t, err)
	reloadedSub2, err := subSvc.GetByID(ctx, sub2.ID)
	require.NoError(t, err)
	require.True(t, reloadedSub1.ExpiresAt.Sub(sub1ExpBefore) >= 29*24*time.Hour)
	require.Equal(t, sub2ExpBefore.Unix(), reloadedSub2.ExpiresAt.Unix())

	// B4: Insufficient balance ($11.5 < $29.5) fails atomically with INSUFFICIENT_BALANCE (no order or sub created).
	ordersBefore, err := client.PaymentOrder.Query().Count(ctx)
	require.NoError(t, err)
	_, err = paySvc.CreateOrder(ctx, CreateOrderRequest{
		UserID:      u.ID,
		Amount:      plan.Price,
		PaymentType: payment.TypeBalance,
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      plan.ID,
	})
	require.Error(t, err)
	require.Equal(t, "INSUFFICIENT_BALANCE", infraerrors.Reason(err))

	reloadedUser, err = userRepo.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.InDelta(t, 11.5, reloadedUser.Balance, 1e-6)
	ordersAfter, err := client.PaymentOrder.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, ordersBefore, ordersAfter)

	// B5: Balance payment is rejected for top-up (order_type = "balance") orders.
	_, err = paySvc.CreateOrder(ctx, CreateOrderRequest{
		UserID:      u.ID,
		Amount:      5.0,
		PaymentType: payment.TypeBalance,
		OrderType:   payment.OrderTypeBalance,
	})
	require.Error(t, err)
	require.Equal(t, "INVALID_PAYMENT_TYPE", infraerrors.Reason(err))

	// B6: Admin refund of balance-paid subscription order2 revokes sub2 ONLY and credits $29.5 back to user balance.
	refundPlan, warnRes, err := paySvc.PrepareRefund(ctx, resp2.OrderID, plan.Price, "admin balance refund", false, true)
	require.NoError(t, err)
	require.Nil(t, warnRes)
	require.NotNil(t, refundPlan)
	require.Equal(t, sub2.ID, refundPlan.SubscriptionID)
	require.Equal(t, 30, refundPlan.SubDaysToDeduct)
	require.InDelta(t, 29.5, refundPlan.GatewayAmount, 1e-6)

	refundRes, err := paySvc.ExecuteRefund(ctx, refundPlan)
	require.NoError(t, err)
	require.NotNil(t, refundRes)
	require.True(t, refundRes.Success)

	// Balance restored from $11.5 to $41.0.
	reloadedUser, err = userRepo.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.InDelta(t, 41.0, reloadedUser.Balance, 1e-6)

	// Order2 marked REFUNDED and has BALANCE_REFUND_CREDITED audit log.
	refundedOrder2, err := client.PaymentOrder.Query().Where(paymentorder.IDEQ(resp2.OrderID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefunded, refundedOrder2.Status)

	hasRefundCreditAudit := paySvc.hasAuditLog(ctx, resp2.OrderID, "BALANCE_REFUND_CREDITED")
	require.True(t, hasRefundCreditAudit)

	// sub2 revoked, sub1 still active!
	remainingSubs, err := subSvc.ListActiveSubscriptions(ctx, u.ID, stdGroup.ID)
	require.NoError(t, err)
	require.Len(t, remainingSubs, 1)
	require.Equal(t, sub1.ID, remainingSubs[0].ID)
}

func TestBalanceSubscriptionPurchase_LegacySubscriptionGroup(t *testing.T) {
	ctx := context.Background()
	client, _ := newSubTemplateSQLiteClient(t, "balance_sub_purchase_legacy")
	settingRepo := newNotificationEmailMemorySettingRepo()
	require.NoError(t, settingRepo.SetMultiple(ctx, map[string]string{
		SettingPaymentEnabled: "true",
	}))

	u, err := client.User.Create().
		SetEmail("legacy-balance@example.com").
		SetUsername("legacy-balance").
		SetPasswordHash("hash").
		SetRole(RoleUser).
		SetStatus(StatusActive).
		SetBalance(50.0).
		Save(ctx)
	require.NoError(t, err)

	subGroup, err := client.Group.Create().
		SetName("Legacy Sub Group").
		SetPlatform(PlatformOpenAI).
		SetSubscriptionType(SubscriptionTypeSubscription).
		SetRateMultiplier(1.0).
		SetStatus(StatusActive).
		Save(ctx)
	require.NoError(t, err)

	groupRepo := &sqliteTestGroupRepo{client: client}
	subRepo := &sqliteTestUserSubRepo{client: client}
	userRepo := &sqliteTestUserRepo{client: client}
	subSvc := NewSubscriptionService(groupRepo, subRepo, nil, client, &config.Config{RunMode: config.RunModeStandard})
	defer subSvc.Stop()

	cfgSvc := NewPaymentConfigService(client, settingRepo, nil)
	paySvc := &PaymentService{
		entClient:       client,
		subscriptionSvc: subSvc,
		configService:   cfgSvc,
		groupRepo:       groupRepo,
		userRepo:        userRepo,
	}

	plan, err := cfgSvc.CreatePlan(ctx, CreatePlanRequest{
		GroupID:      subGroup.ID,
		Name:         "OpenAI Sub Monthly",
		Price:        20.0,
		ValidityDays: 30,
		ValidityUnit: "days",
		ForSale:      true,
	})
	require.NoError(t, err)

	resp, err := paySvc.CreateOrder(ctx, CreateOrderRequest{
		UserID:      u.ID,
		Amount:      plan.Price,
		PaymentType: payment.TypeBalance,
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      plan.ID,
	})
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, resp.Status)
	require.Equal(t, payment.CreatePaymentResultCompleted, resp.ResultType)
	require.Equal(t, "USD", resp.Currency)

	reloadedUser, err := userRepo.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.InDelta(t, 30.0, reloadedUser.Balance, 1e-6)

	activeSubs, err := subSvc.ListActiveSubscriptions(ctx, u.ID, subGroup.ID)
	require.NoError(t, err)
	require.Len(t, activeSubs, 1)
	require.Equal(t, subGroup.ID, activeSubs[0].GroupID)
}
