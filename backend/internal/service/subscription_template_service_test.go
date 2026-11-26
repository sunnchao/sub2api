//go:build unit

package service

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type fakeSubTemplateRepo struct {
	nextID       int64
	templates    map[int64]*SubscriptionTemplate
	forSalePlans map[int64]int64
	activeOrders map[int64]int64
}

func newFakeSubTemplateRepo() *fakeSubTemplateRepo {
	return &fakeSubTemplateRepo{
		nextID:       1,
		templates:    make(map[int64]*SubscriptionTemplate),
		forSalePlans: make(map[int64]int64),
		activeOrders: make(map[int64]int64),
	}
}

func (r *fakeSubTemplateRepo) Create(_ context.Context, tpl *SubscriptionTemplate) error {
	for _, existing := range r.templates {
		if existing.DeletedAt == nil && existing.Name == tpl.Name {
			return ErrSubscriptionTemplateExists
		}
	}
	cp := *tpl
	cp.ID = r.nextID
	r.nextID++
	cp.CreatedAt = time.Now()
	cp.UpdatedAt = cp.CreatedAt
	r.templates[cp.ID] = &cp
	*tpl = cp
	return nil
}

func (r *fakeSubTemplateRepo) GetByID(_ context.Context, id int64) (*SubscriptionTemplate, error) {
	tpl, ok := r.templates[id]
	if !ok || tpl.DeletedAt != nil {
		return nil, ErrSubscriptionTemplateNotFound
	}
	cp := *tpl
	return &cp, nil
}

func (r *fakeSubTemplateRepo) GetByIDs(_ context.Context, ids []int64) (map[int64]*SubscriptionTemplate, error) {
	out := make(map[int64]*SubscriptionTemplate, len(ids))
	for _, id := range ids {
		if tpl, ok := r.templates[id]; ok {
			cp := *tpl
			out[id] = &cp
		}
	}
	return out, nil
}

func (r *fakeSubTemplateRepo) Update(_ context.Context, tpl *SubscriptionTemplate) error {
	cur, ok := r.templates[tpl.ID]
	if !ok || cur.DeletedAt != nil {
		return ErrSubscriptionTemplateNotFound
	}
	cp := *tpl
	cp.UpdatedAt = time.Now()
	r.templates[tpl.ID] = &cp
	return nil
}

func (r *fakeSubTemplateRepo) Delete(_ context.Context, id int64) error {
	cur, ok := r.templates[id]
	if !ok || cur.DeletedAt != nil {
		return ErrSubscriptionTemplateNotFound
	}
	now := time.Now()
	cur.DeletedAt = &now
	return nil
}

func (r *fakeSubTemplateRepo) List(_ context.Context, filter SubscriptionTemplateListFilter) ([]SubscriptionTemplate, error) {
	out := make([]SubscriptionTemplate, 0)
	for _, tpl := range r.templates {
		if tpl.DeletedAt != nil {
			continue
		}
		if filter.GroupID != nil && tpl.GroupID != *filter.GroupID {
			continue
		}
		if filter.Status != "" && tpl.Status != filter.Status {
			continue
		}
		out = append(out, *tpl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *fakeSubTemplateRepo) ExistsActiveByName(_ context.Context, name string, excludeID int64) (bool, error) {
	for _, tpl := range r.templates {
		if tpl.DeletedAt == nil && tpl.Name == name && tpl.ID != excludeID {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeSubTemplateRepo) CountForSalePlansByTemplateID(_ context.Context, templateID int64) (int64, error) {
	return r.forSalePlans[templateID], nil
}

func (r *fakeSubTemplateRepo) CountInProgressOrdersByTemplateID(_ context.Context, templateID int64) (int64, error) {
	return r.activeOrders[templateID], nil
}

type fakeMultiUserSubRepo struct {
	UserSubscriptionRepository
	nextID    int64
	subs      map[int64]*UserSubscription
	snapshots map[int64]*UserSubscriptionSnapshot
}

func newFakeMultiUserSubRepo() *fakeMultiUserSubRepo {
	return &fakeMultiUserSubRepo{
		nextID:    100,
		subs:      make(map[int64]*UserSubscription),
		snapshots: make(map[int64]*UserSubscriptionSnapshot),
	}
}

func (r *fakeMultiUserSubRepo) attach(sub *UserSubscription) *UserSubscription {
	cp := *sub
	if snap, ok := r.snapshots[cp.ID]; ok {
		cp.ApplySnapshot(snap)
	}
	return &cp
}

func (r *fakeMultiUserSubRepo) Create(_ context.Context, sub *UserSubscription) error {
	r.nextID++
	sub.ID = r.nextID
	cp := *sub
	r.subs[cp.ID] = &cp
	return nil
}

func (r *fakeMultiUserSubRepo) GetByID(_ context.Context, id int64) (*UserSubscription, error) {
	sub, ok := r.subs[id]
	if !ok || sub.DeletedAt != nil {
		return nil, ErrSubscriptionNotFound
	}
	return r.attach(sub), nil
}

func (r *fakeMultiUserSubRepo) GetByIDForUpdate(ctx context.Context, id int64) (*UserSubscription, error) {
	return r.GetByID(ctx, id)
}

func (r *fakeMultiUserSubRepo) GetByIDIncludeDeleted(_ context.Context, id int64) (*UserSubscription, error) {
	sub, ok := r.subs[id]
	if !ok {
		return nil, ErrSubscriptionNotFound
	}
	return r.attach(sub), nil
}

func (r *fakeMultiUserSubRepo) Update(_ context.Context, sub *UserSubscription) error {
	cp := *sub
	r.subs[cp.ID] = &cp
	return nil
}

func (r *fakeMultiUserSubRepo) Delete(_ context.Context, id int64) error {
	sub, ok := r.subs[id]
	if !ok {
		return ErrSubscriptionNotFound
	}
	now := time.Now()
	sub.DeletedAt = &now
	return nil
}

func (r *fakeMultiUserSubRepo) Restore(_ context.Context, id int64, status string) (*UserSubscription, error) {
	sub, ok := r.subs[id]
	if !ok {
		return nil, ErrSubscriptionNotFound
	}
	sub.DeletedAt = nil
	sub.Status = status
	return r.attach(sub), nil
}

func (r *fakeMultiUserSubRepo) ExtendExpiry(_ context.Context, id int64, newExpiresAt time.Time) error {
	sub, ok := r.subs[id]
	if !ok {
		return ErrSubscriptionNotFound
	}
	sub.ExpiresAt = newExpiresAt
	return nil
}

func (r *fakeMultiUserSubRepo) UpdateStatus(_ context.Context, id int64, status string) error {
	sub, ok := r.subs[id]
	if !ok {
		return ErrSubscriptionNotFound
	}
	sub.Status = status
	return nil
}

func (r *fakeMultiUserSubRepo) UpdateNotes(_ context.Context, id int64, notes string) error {
	sub, ok := r.subs[id]
	if !ok {
		return ErrSubscriptionNotFound
	}
	sub.Notes = notes
	return nil
}

func (r *fakeMultiUserSubRepo) ExistsActiveByUserIDAndGroupID(_ context.Context, userID, groupID int64) (bool, error) {
	for _, s := range r.subs {
		if s.DeletedAt == nil && s.UserID == userID && s.GroupID == groupID && s.Status == SubscriptionStatusActive {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeMultiUserSubRepo) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*UserSubscription, error) {
	list, err := r.ListActiveByUserIDAndGroupID(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrSubscriptionNotFound
	}
	return &list[0], nil
}

func (r *fakeMultiUserSubRepo) ListActiveByUserIDAndGroupID(_ context.Context, userID, groupID int64) ([]UserSubscription, error) {
	out := make([]UserSubscription, 0)
	for _, s := range r.subs {
		if s.DeletedAt == nil && s.UserID == userID && s.GroupID == groupID && s.Status == SubscriptionStatusActive {
			out = append(out, *r.attach(s))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ExpiresAt.Equal(out[j].ExpiresAt) {
			return out[i].ExpiresAt.Before(out[j].ExpiresAt)
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *fakeMultiUserSubRepo) ListActiveByUserIDAndTemplateID(_ context.Context, userID, templateID int64) ([]UserSubscription, error) {
	out := make([]UserSubscription, 0)
	for _, s := range r.subs {
		if s.DeletedAt != nil || s.UserID != userID || s.Status != SubscriptionStatusActive {
			continue
		}
		att := r.attach(s)
		if att.TemplateID != nil && *att.TemplateID == templateID {
			out = append(out, *att)
		}
	}
	return out, nil
}

func (r *fakeMultiUserSubRepo) FindLatestByUserIDAndTemplateID(_ context.Context, userID, templateID int64) (*UserSubscription, error) {
	var best *UserSubscription
	for _, s := range r.subs {
		if s.DeletedAt != nil || s.UserID != userID {
			continue
		}
		att := r.attach(s)
		if att.TemplateID == nil || *att.TemplateID != templateID {
			continue
		}
		if best == nil || att.ID > best.ID {
			best = att
		}
	}
	if best == nil {
		return nil, ErrSubscriptionNotFound
	}
	return best, nil
}

func (r *fakeMultiUserSubRepo) UpsertSnapshot(_ context.Context, snap *UserSubscriptionSnapshot) error {
	cp := *snap
	cp.TemplateID = cloneTemplateInt64Ptr(snap.TemplateID)
	cp.DailyLimitUSD = cloneFloat64Ptr(snap.DailyLimitUSD)
	cp.WeeklyLimitUSD = cloneFloat64Ptr(snap.WeeklyLimitUSD)
	cp.MonthlyLimitUSD = cloneFloat64Ptr(snap.MonthlyLimitUSD)
	cp.HourlyLimitUSD = cloneFloat64Ptr(snap.HourlyLimitUSD)
	r.snapshots[cp.SubscriptionID] = &cp
	return nil
}

func (r *fakeMultiUserSubRepo) GetSnapshotsBySubscriptionIDs(_ context.Context, subIDs []int64) (map[int64]*UserSubscriptionSnapshot, error) {
	out := make(map[int64]*UserSubscriptionSnapshot)
	for _, id := range subIDs {
		if snap, ok := r.snapshots[id]; ok {
			cp := *snap
			out[id] = &cp
		}
	}
	return out, nil
}

type fakeTemplateGroupRepo struct {
	GroupRepository
	groups map[int64]*Group
}

func (r *fakeTemplateGroupRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	g, ok := r.groups[id]
	if !ok {
		return nil, ErrGroupNotFound
	}
	cp := *g
	return &cp, nil
}

func f64Ptr(v float64) *float64 { return &v }

func TestSubscriptionTemplateCRUDAndSnapshotIsolation(t *testing.T) {
	ctx := context.Background()
	stdGroup := &Group{
		ID:               10,
		Name:             "Standard Claude",
		Platform:         PlatformAnthropic,
		Status:           StatusActive,
		SubscriptionType: SubscriptionTypeStandard,
		DailyLimitUSD:    f64Ptr(99), // group default (should NOT leak into snapshot!)
	}
	subGroup := &Group{
		ID:               20,
		Name:             "Sub Claude",
		Platform:         PlatformAnthropic,
		Status:           StatusActive,
		SubscriptionType: SubscriptionTypeSubscription,
		DailyLimitUSD:    f64Ptr(25),
	}
	inactiveGroup := &Group{
		ID:               30,
		Name:             "Disabled",
		Status:           StatusDisabled,
		SubscriptionType: SubscriptionTypeStandard,
	}

	groupRepo := &fakeTemplateGroupRepo{groups: map[int64]*Group{10: stdGroup, 20: subGroup, 30: inactiveGroup}}
	subRepo := newFakeMultiUserSubRepo()
	tplRepo := newFakeSubTemplateRepo()
	cfg := &config.Config{SubscriptionCache: config.SubscriptionCacheConfig{L1Size: 128, L1TTLSeconds: 60}}
	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, cfg)
	t.Cleanup(svc.Stop)
	svc.SetTemplateRepository(tplRepo)

	// T1: Create templates bound to BOTH standard and subscription groups; reject invalid inputs.
	tplStd, err := svc.CreateTemplate(ctx, &CreateSubscriptionTemplateInput{
		Name:                "Claude Starter",
		GroupID:             10,
		DailyLimitUSD:       f64Ptr(10),
		MonthlyLimitUSD:     f64Ptr(100),
		HourlyLimitUSD:      f64Ptr(3),
		HourlyWindowHours:   4,
		DefaultValidityDays: 30,
	})
	require.NoError(t, err)
	require.Equal(t, int64(10), tplStd.GroupID)
	require.Equal(t, 4, tplStd.HourlyWindowHours)

	_, err = svc.CreateTemplate(ctx, &CreateSubscriptionTemplateInput{Name: "Claude Starter", GroupID: 10})
	require.ErrorIs(t, err, ErrSubscriptionTemplateExists)

	_, err = svc.CreateTemplate(ctx, &CreateSubscriptionTemplateInput{Name: "Inactive Bind", GroupID: 30})
	require.Error(t, err)

	_, err = svc.CreateTemplate(ctx, &CreateSubscriptionTemplateInput{Name: "Bad Limit", GroupID: 20, DailyLimitUSD: f64Ptr(-5)})
	require.Error(t, err)

	_, err = svc.CreateTemplate(ctx, &CreateSubscriptionTemplateInput{Name: "Bad Window", GroupID: 20, HourlyWindowHours: 169})
	require.Error(t, err)

	tplSub, err := svc.CreateTemplate(ctx, &CreateSubscriptionTemplateInput{
		Name:                "Claude Pro",
		GroupID:             20,
		DailyLimitUSD:       f64Ptr(50),
		DefaultValidityDays: 15,
	})
	require.NoError(t, err)
	require.Equal(t, 5, tplSub.HourlyWindowHours)

	// T4: AssignByTemplate(stack) twice for the SAME template creates 2 parallel active subscriptions.
	sub1, isExt, err := svc.AssignByTemplate(ctx, &AssignTemplateSubscriptionInput{
		UserID:     101,
		TemplateID: tplStd.ID,
		Mode:       SubscriptionAssignModeStack,
	})
	require.NoError(t, err)
	require.False(t, isExt)
	require.True(t, sub1.HasCustomLimits)
	require.NotNil(t, sub1.DailyLimitUSD)
	require.Equal(t, 10.0, *sub1.DailyLimitUSD)
	require.Equal(t, "Claude Starter", sub1.TemplateName)

	sub2, isExt, err := svc.AssignByTemplate(ctx, &AssignTemplateSubscriptionInput{
		UserID:       101,
		TemplateID:   tplStd.ID,
		ValidityDays: 60,
		Mode:         SubscriptionAssignModeStack,
	})
	require.NoError(t, err)
	require.False(t, isExt)
	require.NotEqual(t, sub1.ID, sub2.ID)

	activeList, err := svc.ListActiveSubscriptions(ctx, 101, 10)
	require.NoError(t, err)
	require.Len(t, activeList, 2)
	// FEFO: 30-day sub1 expires before 60-day sub2.
	require.Equal(t, sub1.ID, activeList[0].ID)
	require.Equal(t, sub2.ID, activeList[1].ID)

	// T2: Updating template does NOT mutate existing subscription snapshots.
	newName := "Claude Starter V2"
	_, err = svc.UpdateTemplate(ctx, tplStd.ID, &UpdateSubscriptionTemplateInput{
		Name:          &newName,
		DailyLimitUSD: OptionalFloat64Patch{Set: true, Value: f64Ptr(999)},
	})
	require.NoError(t, err)
	reloadedSub1, err := svc.GetByID(ctx, sub1.ID)
	require.NoError(t, err)
	require.Equal(t, 10.0, *reloadedSub1.DailyLimitUSD)
	require.Equal(t, "Claude Starter", reloadedSub1.TemplateName)

	// T5: AssignByTemplate(extend) extends latest matching template subscription (sub2), not creating a 3rd.
	beforeExpiry := sub2.ExpiresAt
	extSub, isExt, err := svc.AssignByTemplate(ctx, &AssignTemplateSubscriptionInput{
		UserID:       101,
		TemplateID:   tplStd.ID,
		ValidityDays: 10,
		Mode:         SubscriptionAssignModeExtend,
	})
	require.NoError(t, err)
	require.True(t, isExt)
	require.Equal(t, sub2.ID, extSub.ID)
	require.True(t, extSub.ExpiresAt.After(beforeExpiry))

	// T6: AdminUpdateSubscriptionLimits overrides single sub; explicit nil = unlimited (does NOT fall back to group's $99).
	editedSub1, err := svc.AdminUpdateSubscriptionLimits(ctx, sub1.ID, SubscriptionLimits{
		DailyLimitUSD:     nil, // explicitly unlimited
		MonthlyLimitUSD:   f64Ptr(200),
		HourlyWindowHours: 6,
	})
	require.NoError(t, err)
	require.True(t, editedSub1.HasCustomLimits)
	require.Nil(t, editedSub1.DailyLimitUSD)
	require.Equal(t, "Claude Starter", editedSub1.TemplateName)
	eff := editedSub1.EffectiveLimits(stdGroup)
	require.False(t, eff.HasDailyLimit(), "explicit nil in snapshot must NOT fall back to group's $99 daily limit")
	require.True(t, eff.HasMonthlyLimit())
	require.Equal(t, 200.0, *eff.MonthlyLimitUSD)
	require.Equal(t, 6, eff.HourlyWindowHours)

	// T7: Legacy subscription (no snapshot) still falls back to group limits.
	legacy := &UserSubscription{ID: 999, UserID: 101, GroupID: 20, HasCustomLimits: false}
	legacyLimits := legacy.EffectiveLimits(subGroup)
	require.True(t, legacyLimits.HasDailyLimit())
	require.Equal(t, 25.0, *legacyLimits.DailyLimitUSD)

	// T3: DeleteTemplate blocked when referenced by for-sale plans or in-progress orders, allowed once clear.
	tplRepo.forSalePlans[tplStd.ID] = 1
	require.ErrorIs(t, svc.DeleteTemplate(ctx, tplStd.ID), ErrSubscriptionTemplateInUse)
	tplRepo.forSalePlans[tplStd.ID] = 0
	tplRepo.activeOrders[tplStd.ID] = 2
	require.ErrorIs(t, svc.DeleteTemplate(ctx, tplStd.ID), ErrSubscriptionTemplateInUse)
	tplRepo.activeOrders[tplStd.ID] = 0
	require.NoError(t, svc.DeleteTemplate(ctx, tplStd.ID))
	// Existing snapshot still intact after soft delete.
	afterDelSub1, err := svc.GetByID(ctx, sub1.ID)
	require.NoError(t, err)
	require.Equal(t, "Claude Starter", afterDelSub1.TemplateName)

	// T8: Revoking sub1 and restoring it succeeds even while sub2 is still active on the same group!
	require.NoError(t, svc.RevokeSubscription(ctx, sub1.ID))
	restored, err := svc.RestoreSubscription(ctx, sub1.ID)
	require.NoError(t, err)
	require.Equal(t, SubscriptionStatusActive, restored.Status)
}
