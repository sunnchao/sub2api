//go:build unit

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type fakeInstanceBillingCache struct {
	BillingCache
	mu   sync.Mutex
	subs map[int64]*SubscriptionCacheData
}

func newFakeInstanceBillingCache() *fakeInstanceBillingCache {
	return &fakeInstanceBillingCache{subs: make(map[int64]*SubscriptionCacheData)}
}

func (c *fakeInstanceBillingCache) GetSubscriptionInstanceCache(_ context.Context, subID int64) (*SubscriptionCacheData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d, ok := c.subs[subID]; ok {
		cp := *d
		return &cp, nil
	}
	return nil, ErrSubscriptionNotFound
}

func (c *fakeInstanceBillingCache) SetSubscriptionInstanceCache(_ context.Context, subID int64, data *SubscriptionCacheData) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *data
	c.subs[subID] = &cp
	return nil
}

func (c *fakeInstanceBillingCache) UpdateSubscriptionInstanceUsage(_ context.Context, subID int64, cost float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d, ok := c.subs[subID]; ok {
		d.DailyUsage += cost
		d.WeeklyUsage += cost
		d.MonthlyUsage += cost
		d.HourlyUsage += cost
	}
	return nil
}

func (c *fakeInstanceBillingCache) InvalidateSubscriptionInstanceCache(_ context.Context, subID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.subs, subID)
	return nil
}

func TestBillingMultiSubInstanceCacheAndEffectiveLimits(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	stdGroup := &Group{
		ID:               10,
		Name:             "StandardGroup",
		Status:           StatusActive,
		SubscriptionType: SubscriptionTypeStandard,
		DailyLimitUSD:    f64Ptr(100), // group default (ignored when snapshot present)
	}
	user := &User{ID: 1, Balance: 0} // zero balance proves subscription mode is active on standard group

	sub1 := &UserSubscription{
		ID: 201, UserID: 1, GroupID: 10, Status: SubscriptionStatusActive,
		ExpiresAt: now.Add(24 * time.Hour), HasCustomLimits: true, DailyLimitUSD: f64Ptr(10), DailyUsageUSD: 10,
	}
	sub2 := &UserSubscription{
		ID: 202, UserID: 1, GroupID: 10, Status: SubscriptionStatusActive,
		ExpiresAt: now.Add(48 * time.Hour), HasCustomLimits: true, DailyLimitUSD: f64Ptr(50), DailyUsageUSD: 5,
	}
	subUnlimited := &UserSubscription{
		ID: 203, UserID: 1, GroupID: 10, Status: SubscriptionStatusActive,
		ExpiresAt: now.Add(72 * time.Hour), HasCustomLimits: true, DailyLimitUSD: nil, DailyUsageUSD: 500,
	}

	// D4: IsSubscriptionBillingActive handles standard group with active subscription.
	require.True(t, IsSubscriptionBillingActive(stdGroup, sub1))
	require.False(t, IsSubscriptionBillingActive(stdGroup, nil))

	subRepo := newFakeMultiUserSubRepo()
	subRepo.subs[sub1.ID] = sub1
	subRepo.subs[sub2.ID] = sub2
	subRepo.subs[subUnlimited.ID] = subUnlimited

	cache := newFakeInstanceBillingCache()
	svc := NewBillingCacheService(cache, nil, subRepo, nil, nil, nil, &config.Config{RunMode: config.RunModeStandard}, nil)
	t.Cleanup(svc.Stop)

	// D5: sub1 hits its snapshotted $10 limit, while sub2 ($5/$50) and subUnlimited (nil limit, $500 used) pass independently!
	err := svc.CheckBillingEligibility(ctx, user, nil, stdGroup, sub1, PlatformAnthropic)
	require.ErrorIs(t, err, ErrDailyLimitExceeded)

	err = svc.CheckBillingEligibility(ctx, user, nil, stdGroup, sub2, PlatformAnthropic)
	require.NoError(t, err)

	err = svc.CheckBillingEligibility(ctx, user, nil, stdGroup, subUnlimited, PlatformAnthropic)
	require.NoError(t, err, "explicit nil snapshot limit must not fall back to group $100 daily limit")

	// Wait for async cache warm-up, then verify D6: incrementing sub2 does NOT affect sub1.
	require.Eventually(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		return cache.subs[sub2.ID] != nil
	}, 2*time.Second, 10*time.Millisecond)

	svc.QueueUpdateSubscriptionInstanceUsage(user.ID, stdGroup.ID, sub2.ID, 45)
	require.Eventually(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		return cache.subs[sub2.ID] != nil && cache.subs[sub2.ID].DailyUsage >= 50
	}, 2*time.Second, 10*time.Millisecond)

	err = svc.CheckBillingEligibility(ctx, user, nil, stdGroup, sub2, PlatformAnthropic)
	require.ErrorIs(t, err, ErrDailyLimitExceeded)

	// D7: ReserveFixedCharge on standard group + active subscription validates subscription snapshot limits, not balance.
	sub2.DailyUsageUSD = 40
	subRepo.subs[sub2.ID] = sub2
	res, err := svc.ReserveFixedCharge(ctx, user, nil, stdGroup, sub2, 15)
	require.Nil(t, res)
	require.ErrorIs(t, err, ErrDailyLimitExceeded)

	res, err = svc.ReserveFixedCharge(ctx, user, nil, stdGroup, sub2, 5)
	require.NoError(t, err)
	if res != nil {
		res.Release()
	}
}
