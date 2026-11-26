//go:build unit

package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/redeemcode"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type sqliteTestRedeemRepo struct {
	RedeemCodeRepository
	client *dbent.Client
}

func (r *sqliteTestRedeemRepo) Create(ctx context.Context, code *RedeemCode) error {
	c := entClientFromContext(ctx, r.client)
	created, err := c.RedeemCode.Create().
		SetCode(code.Code).
		SetType(code.Type).
		SetValue(code.Value).
		SetStatus(code.Status).
		SetNotes(code.Notes).
		SetValidityDays(code.ValidityDays).
		SetNillableExpiresAt(code.ExpiresAt).
		SetNillableGroupID(code.GroupID).
		Save(ctx)
	if err != nil {
		return err
	}
	code.ID = created.ID
	code.CreatedAt = created.CreatedAt
	if code.TemplateID != nil && *code.TemplateID > 0 {
		mode := code.RedeemMode
		if mode == "" {
			mode = RedeemSubscriptionModeStack
		}
		_, err := c.ExecContext(ctx,
			`INSERT INTO redeem_code_templates (redeem_code_id, template_id, redeem_mode) VALUES (?, ?, ?)`,
			code.ID, *code.TemplateID, mode,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *sqliteTestRedeemRepo) hydrate(ctx context.Context, c *dbent.Client, m *dbent.RedeemCode) (*RedeemCode, error) {
	notes := ""
	if m.Notes != nil {
		notes = *m.Notes
	}
	out := &RedeemCode{
		ID:           m.ID,
		Code:         m.Code,
		Type:         m.Type,
		Value:        m.Value,
		Status:       m.Status,
		UsedBy:       m.UsedBy,
		UsedAt:       m.UsedAt,
		Notes:        notes,
		CreatedAt:    m.CreatedAt,
		ExpiresAt:    m.ExpiresAt,
		GroupID:      m.GroupID,
		ValidityDays: m.ValidityDays,
	}
	rows, err := c.QueryContext(ctx, `
		SELECT rct.template_id, COALESCE(st.name, ''), rct.redeem_mode
		FROM redeem_code_templates rct
		LEFT JOIN subscription_templates st ON st.id = rct.template_id
		WHERE rct.redeem_code_id = ?`, out.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var tid sql.NullInt64
		if err := rows.Scan(&tid, &out.TemplateName, &out.RedeemMode); err != nil {
			return nil, err
		}
		out.TemplateID = nullableInt64Ptr(tid)
	}
	return out, rows.Err()
}

func (r *sqliteTestRedeemRepo) GetByID(ctx context.Context, id int64) (*RedeemCode, error) {
	c := entClientFromContext(ctx, r.client)
	m, err := c.RedeemCode.Get(ctx, id)
	if err != nil {
		return nil, ErrRedeemCodeNotFound
	}
	return r.hydrate(ctx, c, m)
}

func (r *sqliteTestRedeemRepo) GetByCode(ctx context.Context, code string) (*RedeemCode, error) {
	c := entClientFromContext(ctx, r.client)
	m, err := c.RedeemCode.Query().Where(redeemcode.CodeEQ(code)).Only(ctx)
	if err != nil {
		return nil, ErrRedeemCodeNotFound
	}
	return r.hydrate(ctx, c, m)
}

func (r *sqliteTestRedeemRepo) Use(ctx context.Context, id, userID int64) error {
	c := entClientFromContext(ctx, r.client)
	n, err := c.RedeemCode.Update().
		Where(redeemcode.IDEQ(id), redeemcode.StatusEQ(StatusUnused)).
		SetStatus(StatusUsed).
		SetUsedBy(userID).
		SetUsedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrRedeemCodeUsed
	}
	return nil
}

type sqliteTestAdminGroupRepo struct {
	AdminGroupRepository
	base *sqliteTestGroupRepo
}

func (r *sqliteTestAdminGroupRepo) GetByID(ctx context.Context, id int64) (*Group, error) {
	return r.base.GetByID(ctx, id)
}

func TestRedeemSubscriptionTemplate_StackExtendReduceAndLegacy(t *testing.T) {
	ctx := context.Background()
	client, _ := newSubTemplateSQLiteClient(t, "redeem_sub_template_flow")

	u, err := client.User.Create().
		SetEmail("redeemer@example.com").
		SetUsername("redeemer").
		SetPasswordHash("hash").
		SetRole(RoleUser).
		SetStatus(StatusActive).
		Save(ctx)
	require.NoError(t, err)

	stdGroup, err := client.Group.Create().
		SetName("Standard Claude").
		SetPlatform(PlatformAnthropic).
		SetSubscriptionType(SubscriptionTypeStandard).
		SetRateMultiplier(1.0).
		SetStatus(StatusActive).
		Save(ctx)
	require.NoError(t, err)

	subGroup, err := client.Group.Create().
		SetName("Legacy Sub Group").
		SetPlatform(PlatformAnthropic).
		SetSubscriptionType(SubscriptionTypeSubscription).
		SetRateMultiplier(1.0).
		SetStatus(StatusActive).
		Save(ctx)
	require.NoError(t, err)

	groupRepo := &sqliteTestGroupRepo{client: client}
	subRepo := &sqliteTestUserSubRepo{client: client}
	redeemRepo := &sqliteTestRedeemRepo{client: client}
	userRepo := &mockUserRepo{
		getByIDUser: &User{ID: u.ID, Email: u.Email, Username: u.Username, Status: StatusActive},
	}

	subSvc := NewSubscriptionService(groupRepo, subRepo, nil, client, &config.Config{RunMode: config.RunModeStandard})
	defer subSvc.Stop()

	redeemSvc := NewRedeemService(redeemRepo, userRepo, subSvc, nil, nil, client, nil, nil)
	adminSvc := &adminServiceImpl{
		groupRepo:      &sqliteTestAdminGroupRepo{base: groupRepo},
		redeemCodeRepo: redeemRepo,
		entClient:      client,
	}

	d20 := 20.0
	tplA, err := subSvc.CreateTemplate(ctx, &CreateSubscriptionTemplateInput{
		Name:                "Starter 20",
		GroupID:             stdGroup.ID,
		DailyLimitUSD:       &d20,
		HourlyWindowHours:   5,
		DefaultValidityDays: 30,
	})
	require.NoError(t, err)

	d90 := 90.0
	tplB, err := subSvc.CreateTemplate(ctx, &CreateSubscriptionTemplateInput{
		Name:                "Pro 90",
		GroupID:             stdGroup.ID,
		DailyLimitUSD:       &d90,
		HourlyWindowHours:   5,
		DefaultValidityDays: 30,
	})
	require.NoError(t, err)

	// R1: GenerateRedeemCodes with template on a standard group + mode=stack creates 2 stacked subscriptions.
	stackCodes, err := adminSvc.GenerateRedeemCodes(ctx, &GenerateRedeemCodesInput{
		Count:        2,
		Type:         RedeemTypeSubscription,
		TemplateID:   &tplA.ID,
		RedeemMode:   RedeemSubscriptionModeStack,
		ValidityDays: 0, // inherit template default 30 days
	})
	require.NoError(t, err)
	require.Len(t, stackCodes, 2)
	require.Equal(t, 30, stackCodes[0].ValidityDays)
	require.NotNil(t, stackCodes[0].GroupID)
	require.Equal(t, stdGroup.ID, *stackCodes[0].GroupID)

	for _, c := range stackCodes {
		used, err := redeemSvc.Redeem(ctx, u.ID, c.Code)
		require.NoError(t, err)
		require.Equal(t, StatusUsed, used.Status)
		require.NotNil(t, used.TemplateID)
		require.Equal(t, tplA.ID, *used.TemplateID)
	}

	// Also redeem 1 code for tplB on the SAME standard group.
	proCodes, err := adminSvc.GenerateRedeemCodes(ctx, &GenerateRedeemCodesInput{
		Count:        1,
		Type:         RedeemTypeSubscription,
		TemplateID:   &tplB.ID,
		RedeemMode:   RedeemSubscriptionModeStack,
		ValidityDays: 30,
	})
	require.NoError(t, err)
	_, err = redeemSvc.Redeem(ctx, u.ID, proCodes[0].Code)
	require.NoError(t, err)

	allStdSubs, err := subSvc.ListActiveSubscriptions(ctx, u.ID, stdGroup.ID)
	require.NoError(t, err)
	require.Len(t, allStdSubs, 3, "expected 2 Starter + 1 Pro stacked on standard group")

	// R2: Template redeem code with mode=extend extends ONLY tplA's latest sub (no 4th row created, tplB untouched).
	tplASubsBefore, err := subRepo.ListActiveByUserIDAndTemplateID(ctx, u.ID, tplA.ID)
	require.NoError(t, err)
	require.Len(t, tplASubsBefore, 2)
	tplALatestBefore := tplASubsBefore[0]

	tplBSubsBefore, err := subRepo.ListActiveByUserIDAndTemplateID(ctx, u.ID, tplB.ID)
	require.NoError(t, err)
	tplBExpiryBefore := tplBSubsBefore[0].ExpiresAt

	extendCodes, err := adminSvc.GenerateRedeemCodes(ctx, &GenerateRedeemCodesInput{
		Count:        1,
		Type:         RedeemTypeSubscription,
		TemplateID:   &tplA.ID,
		RedeemMode:   RedeemSubscriptionModeExtend,
		ValidityDays: 15,
	})
	require.NoError(t, err)
	_, err = redeemSvc.Redeem(ctx, u.ID, extendCodes[0].Code)
	require.NoError(t, err)

	allStdSubsAfterExtend, err := subSvc.ListActiveSubscriptions(ctx, u.ID, stdGroup.ID)
	require.NoError(t, err)
	require.Len(t, allStdSubsAfterExtend, 3, "extend must NOT create a new subscription row")

	tplALatestAfter, err := subRepo.GetByID(ctx, tplALatestBefore.ID)
	require.NoError(t, err)
	require.True(t, tplALatestAfter.ExpiresAt.Sub(tplALatestBefore.ExpiresAt) >= 14*24*time.Hour)

	tplBAfter, err := subRepo.GetByID(ctx, tplBSubsBefore[0].ID)
	require.NoError(t, err)
	require.Equal(t, tplBExpiryBefore.Unix(), tplBAfter.ExpiresAt.Unix(), "tplB must remain untouched")

	// R3: Negative validity_days reduces/expires ONLY the target template's subscription (tplB), leaving tplA intact.
	reduceCode := &RedeemCode{
		Code:         "REDUCE-PRO-10D",
		Type:         RedeemTypeSubscription,
		Value:        -10,
		Status:       StatusUnused,
		GroupID:      &stdGroup.ID,
		TemplateID:   &tplB.ID,
		RedeemMode:   RedeemSubscriptionModeExtend,
		ValidityDays: -10,
	}
	require.NoError(t, redeemRepo.Create(ctx, reduceCode))
	_, err = redeemSvc.Redeem(ctx, u.ID, reduceCode.Code)
	require.NoError(t, err)

	tplBReduced, err := subRepo.GetByID(ctx, tplBSubsBefore[0].ID)
	require.NoError(t, err)
	require.True(t, tplBExpiryBefore.Sub(tplBReduced.ExpiresAt) >= 9*24*time.Hour)

	cancelCode := &RedeemCode{
		Code:         "CANCEL-PRO-100D",
		Type:         RedeemTypeSubscription,
		Value:        -100,
		Status:       StatusUnused,
		GroupID:      &stdGroup.ID,
		TemplateID:   &tplB.ID,
		RedeemMode:   RedeemSubscriptionModeExtend,
		ValidityDays: -100,
	}
	require.NoError(t, redeemRepo.Create(ctx, cancelCode))
	_, err = redeemSvc.Redeem(ctx, u.ID, cancelCode.Code)
	require.NoError(t, err)

	tplBCancelled, err := subRepo.GetByID(ctx, tplBSubsBefore[0].ID)
	require.NoError(t, err)
	require.Equal(t, SubscriptionStatusExpired, tplBCancelled.Status)

	// tplA's 2 subscriptions are still active and untouched!
	tplAActiveAfter, err := subRepo.ListActiveByUserIDAndTemplateID(ctx, u.ID, tplA.ID)
	require.NoError(t, err)
	require.Len(t, tplAActiveAfter, 2)

	// R4: Legacy group-only redeem code rejects standard group, and on subscription group preserves AssignOrExtend.
	_, err = adminSvc.GenerateRedeemCodes(ctx, &GenerateRedeemCodesInput{
		Count:        1,
		Type:         RedeemTypeSubscription,
		GroupID:      &stdGroup.ID,
		ValidityDays: 30,
	})
	require.Error(t, err, "legacy group-only redeem code must reject standard group")

	legacyCodes, err := adminSvc.GenerateRedeemCodes(ctx, &GenerateRedeemCodesInput{
		Count:        2,
		Type:         RedeemTypeSubscription,
		GroupID:      &subGroup.ID,
		ValidityDays: 10,
	})
	require.NoError(t, err)
	require.Nil(t, legacyCodes[0].TemplateID)

	_, err = redeemSvc.Redeem(ctx, u.ID, legacyCodes[0].Code)
	require.NoError(t, err)
	legacySubs1, err := subSvc.ListActiveSubscriptions(ctx, u.ID, subGroup.ID)
	require.NoError(t, err)
	require.Len(t, legacySubs1, 1)
	firstLegacyExpiry := legacySubs1[0].ExpiresAt

	_, err = redeemSvc.Redeem(ctx, u.ID, legacyCodes[1].Code)
	require.NoError(t, err)
	legacySubs2, err := subSvc.ListActiveSubscriptions(ctx, u.ID, subGroup.ID)
	require.NoError(t, err)
	require.Len(t, legacySubs2, 1, "legacy group-only redeem extends existing subscription rather than stacking")
	require.True(t, legacySubs2[0].ExpiresAt.Sub(firstLegacyExpiry) >= 9*24*time.Hour)
}
