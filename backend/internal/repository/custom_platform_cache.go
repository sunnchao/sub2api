package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	customPlatformPubSubKey = "sub2api:custom_platforms:updated"
)

type customPlatformCache struct {
	rdb *redis.Client
}

// NewCustomPlatformCache 创建自定义平台多实例热生效广播器。
func NewCustomPlatformCache(rdb *redis.Client) service.CustomPlatformCache {
	return &customPlatformCache{rdb: rdb}
}

func (c *customPlatformCache) NotifyUpdate(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Publish(ctx, customPlatformPubSubKey, "1").Err()
}

func (c *customPlatformCache) SubscribeUpdates(ctx context.Context, handler func()) {
	if c == nil || c.rdb == nil || handler == nil {
		return
	}
	sub := c.rdb.Subscribe(ctx, customPlatformPubSubKey)
	go func() {
		defer func() { _ = sub.Close() }()
		channel := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-channel:
				if !ok {
					return
				}
				handler()
			}
		}
	}()
}
