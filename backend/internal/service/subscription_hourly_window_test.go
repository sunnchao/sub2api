//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

type hourlyResetUserSubRepo struct {
	userSubRepoNoop
	resetCalled bool
	resetAt     time.Time
	sub         *UserSubscription
}

func (r *hourlyResetUserSubRepo) GetByID(_ context.Context, id int64) (*UserSubscription, error) {
	if r.sub == nil || r.sub.ID != id {
		return nil, ErrSubscriptionNotFound
	}
	refreshed := *r.sub
	return &refreshed, nil
}

func (r *hourlyResetUserSubRepo) ResetHourlyUsage(_ context.Context, _ int64, _ *time.Time, resetAt time.Time) error {
	r.resetCalled = true
	r.resetAt = resetAt
	if r.sub != nil {
		r.sub.HourlyWindowStart = &resetAt
		r.sub.HourlyUsageUSD = 0
	}
	return nil
}

func TestHourlyWindowResetsEveryConfiguredHours(t *testing.T) {
	start := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	repo := &hourlyResetUserSubRepo{}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	now := start.Add(5*time.Hour + time.Minute)
	svc.now = func() time.Time { return now }
	hourlyLimit := 12.0
	group := &Group{HourlyLimitUSD: &hourlyLimit, HourlyWindowHours: 5}
	sub := &UserSubscription{
		ID:                1,
		StartsAt:          start,
		ExpiresAt:         start.Add(30 * 24 * time.Hour),
		HourlyWindowStart: &start,
		HourlyUsageUSD:    12,
		Group:             group,
	}

	require.NoError(t, svc.CheckAndResetWindows(context.Background(), sub))

	require.True(t, repo.resetCalled)
	require.Equal(t, start.Add(5*time.Hour), repo.resetAt)
	require.Zero(t, sub.HourlyUsageUSD)
	require.Equal(t, start.Add(5*time.Hour), *sub.HourlyWindowStart)
}

func TestHourlyWindowDoesNotResetBeforeConfiguredHours(t *testing.T) {
	start := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	repo := &hourlyResetUserSubRepo{}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	now := start.Add(4*time.Hour + 59*time.Minute)
	svc.now = func() time.Time { return now }
	hourlyLimit := 12.0
	group := &Group{HourlyLimitUSD: &hourlyLimit, HourlyWindowHours: 5}
	sub := &UserSubscription{
		ID:                1,
		StartsAt:          start,
		ExpiresAt:         start.Add(30 * 24 * time.Hour),
		HourlyWindowStart: &start,
		HourlyUsageUSD:    8,
		Group:             group,
	}

	require.NoError(t, svc.CheckAndResetWindows(context.Background(), sub))

	require.False(t, repo.resetCalled)
	require.Equal(t, 8.0, sub.HourlyUsageUSD)
}

func TestHourlyWindowNeverAdvancesPastExpiry(t *testing.T) {
	start := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	expiresAt := start.Add(9 * time.Hour)
	repo := &hourlyResetUserSubRepo{}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	now := expiresAt
	svc.now = func() time.Time { return now }
	hourlyLimit := 12.0
	group := &Group{HourlyLimitUSD: &hourlyLimit, HourlyWindowHours: 5}
	sub := &UserSubscription{
		ID:                1,
		StartsAt:          start,
		ExpiresAt:         expiresAt,
		HourlyWindowStart: &start,
		HourlyUsageUSD:    12,
		Group:             group,
	}

	require.NoError(t, svc.CheckAndResetWindows(context.Background(), sub))

	require.True(t, repo.resetCalled)
	require.Equal(t, start.Add(5*time.Hour), repo.resetAt, "窗口不得推进到订阅到期之后")
	require.Zero(t, sub.HourlyUsageUSD)
}

func TestHourlyWindowActivationUsesFirstUseAnchor(t *testing.T) {
	startsAt := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	activatedAt := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	repo := &activateWindowUserSubRepo{}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	svc.now = func() time.Time { return activatedAt }
	sub := &UserSubscription{ID: 1, StartsAt: startsAt, ExpiresAt: startsAt.Add(30 * 24 * time.Hour)}

	require.NoError(t, svc.CheckAndActivateWindow(context.Background(), sub))

	require.Equal(t, activatedAt, repo.periodicStart, "小时/周/月窗口应锚定首次使用时刻")
	require.Equal(t, timezone.StartOfDay(activatedAt), repo.dailyStart)
}

func TestCheckHourlyLimitUsesConfiguredWindow(t *testing.T) {
	limit := 12.0
	group := &Group{HourlyLimitUSD: &limit, HourlyWindowHours: 5}
	start := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	sub := &UserSubscription{HourlyUsageUSD: 11, HourlyWindowStart: &start}

	require.True(t, sub.CheckHourlyLimit(group, 1))
	require.False(t, sub.CheckHourlyLimit(group, 1.01))
}

func TestValidateAndCheckLimitsRequestsMaintenanceForLegacyHourlyWindow(t *testing.T) {
	start := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	repo := &hourlyResetUserSubRepo{}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	svc.now = func() time.Time { return start.Add(time.Hour) }
	hourlyLimit := 12.0
	group := &Group{HourlyLimitUSD: &hourlyLimit, HourlyWindowHours: 5}
	sub := &UserSubscription{
		ID:                 1,
		StartsAt:           start,
		ExpiresAt:          start.Add(30 * 24 * time.Hour),
		DailyWindowStart:   &start,
		WeeklyWindowStart:  &start,
		MonthlyWindowStart: &start,
	}
	persisted := *sub
	persisted.Group = group
	repo.sub = &persisted

	needsMaintenance, err := svc.ValidateAndCheckLimits(sub, group)

	require.NoError(t, err)
	require.True(t, needsMaintenance, "存量订阅缺少小时窗口时必须触发维护")

	refreshed, err := svc.EnsureWindowMaintenance(context.Background(), sub)
	require.NoError(t, err)
	require.NotNil(t, refreshed.HourlyWindowStart)
	require.Equal(t, start.Add(time.Hour), *refreshed.HourlyWindowStart)
	require.Zero(t, refreshed.HourlyUsageUSD)
}

func TestEnsureWindowMaintenanceUsesValidatedHourlyPeriod(t *testing.T) {
	start := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	now := start.Add(3 * time.Hour)
	dailyStart := timezone.StartOfDay(start)
	limit := 12.0
	cachedGroup := &Group{HourlyLimitUSD: &limit, HourlyWindowHours: 5}
	currentGroup := &Group{HourlyLimitUSD: &limit, HourlyWindowHours: 2}
	cachedSubscription := UserSubscription{
		ID:                 1,
		Status:             SubscriptionStatusActive,
		StartsAt:           start,
		ExpiresAt:          start.Add(30 * 24 * time.Hour),
		DailyWindowStart:   &dailyStart,
		WeeklyWindowStart:  &start,
		MonthlyWindowStart: &start,
		HourlyWindowStart:  &start,
		HourlyUsageUSD:     limit,
		Group:              cachedGroup,
	}
	persisted := cachedSubscription
	persisted.Group = currentGroup
	repo := &hourlyResetUserSubRepo{sub: &persisted}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }
	requestSubscription := cachedSubscription

	needsMaintenance, err := svc.ValidateAndCheckLimits(&requestSubscription, currentGroup)
	require.NoError(t, err)
	require.True(t, needsMaintenance)
	require.Same(t, cachedGroup, cachedSubscription.Group)
	require.Equal(t, limit, cachedSubscription.HourlyUsageUSD)

	refreshed, err := svc.EnsureWindowMaintenance(context.Background(), &requestSubscription)
	require.NoError(t, err)
	require.True(t, repo.resetCalled)
	require.Equal(t, start.Add(2*time.Hour), repo.resetAt)
	require.Zero(t, refreshed.HourlyUsageUSD)
	require.Equal(t, start.Add(2*time.Hour), *refreshed.HourlyWindowStart)

	needsMaintenance, err = svc.ValidateAndCheckLimits(refreshed, currentGroup)
	require.NoError(t, err)
	require.False(t, needsMaintenance)
}

func TestSubscriptionWithoutHourlyLimitSkipsHourlyMaintenance(t *testing.T) {
	start := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	now := start.Add(6 * time.Hour)
	dailyStart := timezone.StartOfDay(start)
	group := &Group{HourlyWindowHours: 5}
	repo := &hourlyResetUserSubRepo{}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }
	sub := &UserSubscription{
		ID:                 1,
		Status:             SubscriptionStatusActive,
		StartsAt:           start,
		ExpiresAt:          start.Add(30 * 24 * time.Hour),
		DailyWindowStart:   &dailyStart,
		WeeklyWindowStart:  &start,
		MonthlyWindowStart: &start,
		HourlyWindowStart:  &start,
		HourlyUsageUSD:     12,
		Group:              group,
	}

	needsMaintenance, err := svc.ValidateAndCheckLimits(sub, group)
	require.NoError(t, err)
	require.False(t, needsMaintenance)
	require.NoError(t, svc.CheckAndResetWindows(context.Background(), sub))
	require.False(t, repo.resetCalled)
	require.Equal(t, 12.0, sub.HourlyUsageUSD)
	require.Equal(t, start, *sub.HourlyWindowStart)
}

func TestAdminResetDailyQuotaPreservesHourlyUsage(t *testing.T) {
	start := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	repo := &resetQuotaUserSubRepoStub{
		sub: &UserSubscription{
			ID:                1,
			DailyUsageUSD:     7,
			HourlyWindowStart: &start,
			HourlyUsageUSD:    12,
		},
	}
	svc := newResetQuotaSvc(repo)
	svc.now = func() time.Time { return start.Add(time.Hour) }

	refreshed, err := svc.AdminResetQuota(context.Background(), 1, true, false, false)
	require.NoError(t, err)
	require.Zero(t, refreshed.DailyUsageUSD)
	require.Equal(t, 12.0, refreshed.HourlyUsageUSD)
	require.Equal(t, start, *refreshed.HourlyWindowStart)
}
