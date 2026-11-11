//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionCacheRoundTripsHourlyUsage(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := NewBillingCache(rdb)
	ctx := context.Background()

	require.NoError(t, cache.SetSubscriptionCache(ctx, 7, 9, &service.SubscriptionCacheData{
		Status:       service.SubscriptionStatusActive,
		ExpiresAt:    time.Now().Add(time.Hour),
		DailyUsage:   1,
		WeeklyUsage:  2,
		MonthlyUsage: 3,
		HourlyUsage:  4,
	}))

	got, err := cache.GetSubscriptionCache(ctx, 7, 9)
	require.NoError(t, err)
	require.Equal(t, 4.0, got.HourlyUsage)

	require.NoError(t, cache.UpdateSubscriptionUsage(ctx, 7, 9, 1))
	got, err = cache.GetSubscriptionCache(ctx, 7, 9)
	require.NoError(t, err)
	require.Equal(t, 5.0, got.HourlyUsage)
}

func TestSubscriptionCacheRejectsLegacyEntryWithoutHourlyUsage(t *testing.T) {
	cache := &billingCache{}

	_, err := cache.parseSubscriptionCache(map[string]string{
		subFieldStatus:     service.SubscriptionStatusActive,
		subFieldDailyUsage: "1",
	})
	require.ErrorIs(t, err, redis.Nil)
}
