package service

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

func (s *BillingCacheService) subscriptionInstanceCache() SubscriptionInstanceCache {
	if s == nil || s.cache == nil {
		return nil
	}
	inst, _ := s.cache.(SubscriptionInstanceCache)
	return inst
}

// GetSubscriptionInstanceStatus fetches subscription usage status keyed by individual subscription ID.
// When the underlying cache does not implement SubscriptionInstanceCache (e.g., legacy mocks),
// it seamlessly falls back to GetSubscriptionStatus(userID, groupID).
func (s *BillingCacheService) GetSubscriptionInstanceStatus(ctx context.Context, userID, groupID, subscriptionID int64) (*subscriptionCacheData, error) {
	if subscriptionID <= 0 {
		return s.GetSubscriptionStatus(ctx, userID, groupID)
	}
	if s.cache == nil {
		return s.getSubscriptionInstanceFromDB(ctx, userID, groupID, subscriptionID)
	}
	instCache := s.subscriptionInstanceCache()
	if instCache == nil {
		return s.GetSubscriptionStatus(ctx, userID, groupID)
	}

	cacheData, err := instCache.GetSubscriptionInstanceCache(ctx, subscriptionID)
	if err == nil && cacheData != nil {
		return s.convertFromPortsData(cacheData), nil
	}

	data, err := s.getSubscriptionInstanceFromDB(ctx, userID, groupID, subscriptionID)
	if err != nil {
		return nil, err
	}

	_ = s.enqueueCacheWrite(cacheWriteTask{
		kind:             cacheWriteSetSubscriptionInstance,
		userID:           userID,
		groupID:          groupID,
		subscriptionID:   subscriptionID,
		subscriptionData: data,
	})
	return data, nil
}

func (s *BillingCacheService) getSubscriptionInstanceFromDB(ctx context.Context, userID, groupID, subscriptionID int64) (*subscriptionCacheData, error) {
	if s.subRepo == nil {
		return nil, fmt.Errorf("subscription repository unavailable")
	}
	if _, supportsMulti := s.subRepo.(UserSubscriptionMultiRepository); supportsMulti && subscriptionID > 0 {
		sub, err := s.subRepo.GetByID(ctx, subscriptionID)
		if err != nil {
			return nil, fmt.Errorf("get subscription %d: %w", subscriptionID, err)
		}
		return &subscriptionCacheData{
			Status:       sub.Status,
			ExpiresAt:    sub.ExpiresAt,
			DailyUsage:   sub.DailyUsageUSD,
			WeeklyUsage:  sub.WeeklyUsageUSD,
			MonthlyUsage: sub.MonthlyUsageUSD,
			HourlyUsage:  sub.HourlyUsageUSD,
			Version:      sub.UpdatedAt.Unix(),
		}, nil
	}
	return s.getSubscriptionFromDB(ctx, userID, groupID)
}

func (s *BillingCacheService) setSubscriptionInstanceCache(ctx context.Context, subscriptionID int64, data *subscriptionCacheData) {
	instCache := s.subscriptionInstanceCache()
	if instCache == nil || subscriptionID <= 0 || data == nil {
		return
	}
	if err := instCache.SetSubscriptionInstanceCache(ctx, subscriptionID, s.convertToPortsData(data)); err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: set subscription instance cache failed for sub %d: %v", subscriptionID, err)
	}
}

// QueueUpdateSubscriptionInstanceUsage asynchronously increments cached usage for a specific subscription instance.
func (s *BillingCacheService) QueueUpdateSubscriptionInstanceUsage(userID, groupID, subscriptionID int64, cost float64) {
	if s == nil || s.cache == nil {
		return
	}
	instCache := s.subscriptionInstanceCache()
	if instCache == nil || subscriptionID <= 0 {
		s.QueueUpdateSubscriptionUsage(userID, groupID, cost)
		return
	}
	if s.enqueueCacheWrite(cacheWriteTask{
		kind:           cacheWriteUpdateSubscriptionInstanceUsage,
		userID:         userID,
		groupID:        groupID,
		subscriptionID: subscriptionID,
		amount:         cost,
	}) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
	defer cancel()
	if err := instCache.UpdateSubscriptionInstanceUsage(ctx, subscriptionID, cost); err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: update subscription instance cache fallback failed for sub %d: %v", subscriptionID, err)
	}
}

// InvalidateSubscriptionInstance removes the instance-keyed billing cache entry.
func (s *BillingCacheService) InvalidateSubscriptionInstance(ctx context.Context, subscriptionID int64) error {
	instCache := s.subscriptionInstanceCache()
	if instCache == nil || subscriptionID <= 0 {
		return nil
	}
	if err := instCache.InvalidateSubscriptionInstanceCache(ctx, subscriptionID); err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: invalidate subscription instance cache failed for sub %d: %v", subscriptionID, err)
		return err
	}
	return nil
}
