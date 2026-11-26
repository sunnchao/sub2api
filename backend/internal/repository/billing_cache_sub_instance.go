package repository

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const billingSubInstanceKeyPrefix = "billing:sub:id:"

func billingSubInstanceKey(subscriptionID int64) string {
	return fmt.Sprintf("%s%d", billingSubInstanceKeyPrefix, subscriptionID)
}

func (c *billingCache) GetSubscriptionInstanceCache(ctx context.Context, subscriptionID int64) (*service.SubscriptionCacheData, error) {
	key := billingSubInstanceKey(subscriptionID)
	result, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, redis.Nil
	}
	return c.parseSubscriptionCache(result)
}

func (c *billingCache) SetSubscriptionInstanceCache(ctx context.Context, subscriptionID int64, data *service.SubscriptionCacheData) error {
	if data == nil || subscriptionID <= 0 {
		return nil
	}
	key := billingSubInstanceKey(subscriptionID)
	fields := map[string]any{
		subFieldStatus:       data.Status,
		subFieldExpiresAt:    data.ExpiresAt.Unix(),
		subFieldDailyUsage:   data.DailyUsage,
		subFieldWeeklyUsage:  data.WeeklyUsage,
		subFieldMonthlyUsage: data.MonthlyUsage,
		subFieldHourlyUsage:  data.HourlyUsage,
		subFieldVersion:      data.Version,
	}
	pipe := c.rdb.Pipeline()
	pipe.HSet(ctx, key, fields)
	pipe.Expire(ctx, key, jitteredTTL())
	_, err := pipe.Exec(ctx)
	return err
}

func (c *billingCache) UpdateSubscriptionInstanceUsage(ctx context.Context, subscriptionID int64, cost float64) error {
	if subscriptionID <= 0 {
		return nil
	}
	key := billingSubInstanceKey(subscriptionID)
	_, err := updateSubUsageScript.Run(ctx, c.rdb, []string{key}, cost, int(jitteredTTL().Seconds())).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		log.Printf("Warning: update subscription instance usage cache failed for subscription %d: %v", subscriptionID, err)
		return err
	}
	return nil
}

func (c *billingCache) InvalidateSubscriptionInstanceCache(ctx context.Context, subscriptionID int64) error {
	if subscriptionID <= 0 {
		return nil
	}
	key := billingSubInstanceKey(subscriptionID)
	return c.rdb.Del(ctx, key).Err()
}

var _ service.SubscriptionInstanceCache = (*billingCache)(nil)
