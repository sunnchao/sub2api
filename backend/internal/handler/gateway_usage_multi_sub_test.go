//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func float64Ref(v float64) *float64 { return &v }
func int64Ref(v int64) *int64       { return &v }

func TestUsageUnrestrictedMultiSubAggregation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now()
	gid := int64(10)

	h := &GatewayHandler{}

	// U1: Two parallel subscriptions on same group with different snapshotted limits -> aggregated remaining = 46.
	t.Run("aggregates_multiple_parallel_subscriptions", func(t *testing.T) {
		subGroup := &service.Group{
			ID:               gid,
			Name:             "ClaudeSub",
			SubscriptionType: service.SubscriptionTypeSubscription,
			DailyLimitUSD:    float64Ref(999),
		}
		subs := []service.UserSubscription{
			{
				ID: 1, GroupID: gid, TemplateID: int64Ref(11), TemplateName: "Starter",
				HasCustomLimits: true, DailyLimitUSD: float64Ref(10), DailyUsageUSD: 4,
				ExpiresAt: now.Add(5 * 24 * time.Hour),
			},
			{
				ID: 2, GroupID: gid, TemplateID: int64Ref(12), TemplateName: "Pro",
				HasCustomLimits: true, DailyLimitUSD: float64Ref(50), DailyUsageUSD: 10,
				ExpiresAt: now.Add(20 * 24 * time.Hour),
			},
		}
		apiKey := &service.APIKey{ID: 1, UserID: 7, GroupID: &gid, Group: subGroup}

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
		c.Set(string(middleware2.ContextKeySubscription), &subs[0])
		c.Set(string(middleware2.ContextKeyActiveSubscriptions), subs)

		h.usageUnrestricted(c, context.Background(), apiKey, middleware2.AuthSubject{UserID: 7}, nil, nil, nil)
		require.Equal(t, http.StatusOK, w.Code)

		var payload map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		require.InDelta(t, 46.0, payload["remaining"].(float64), 0.001)
		primary := payload["subscription"].(map[string]any)
		require.InDelta(t, 10.0, primary["daily_limit_usd"].(float64), 0.001)
		require.Equal(t, "Starter", primary["template_name"])
		list := payload["subscriptions"].([]any)
		require.Len(t, list, 2)
	})

	// U2: One limited sub + one explicitly unlimited sub -> remaining = -1.
	t.Run("unlimited_subscription_returns_minus_one", func(t *testing.T) {
		subGroup := &service.Group{ID: gid, Name: "ClaudeSub", SubscriptionType: service.SubscriptionTypeSubscription, DailyLimitUSD: float64Ref(10)}
		subs := []service.UserSubscription{
			{ID: 1, GroupID: gid, HasCustomLimits: true, DailyLimitUSD: float64Ref(10), DailyUsageUSD: 8, ExpiresAt: now.Add(24 * time.Hour)},
			{ID: 2, GroupID: gid, HasCustomLimits: true, DailyLimitUSD: nil, DailyUsageUSD: 25, ExpiresAt: now.Add(48 * time.Hour)},
		}
		apiKey := &service.APIKey{ID: 1, UserID: 7, GroupID: &gid, Group: subGroup}

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
		c.Set(string(middleware2.ContextKeyActiveSubscriptions), subs)

		h.usageUnrestricted(c, context.Background(), apiKey, middleware2.AuthSubject{UserID: 7}, nil, nil, nil)
		require.Equal(t, http.StatusOK, w.Code)

		var payload map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		require.Equal(t, float64(-1), payload["remaining"])
	})

	// U3: Standard group with active template subscription uses subscription view with template name.
	t.Run("standard_group_with_active_template_sub", func(t *testing.T) {
		stdGroup := &service.Group{ID: gid, Name: "StdClaude", SubscriptionType: service.SubscriptionTypeStandard}
		subs := []service.UserSubscription{
			{ID: 3, GroupID: gid, TemplateID: int64Ref(21), TemplateName: "StdTier", HasCustomLimits: true, MonthlyLimitUSD: float64Ref(30), MonthlyUsageUSD: 5, ExpiresAt: now.Add(10 * 24 * time.Hour)},
		}
		apiKey := &service.APIKey{ID: 1, UserID: 7, GroupID: &gid, Group: stdGroup}

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
		c.Set(string(middleware2.ContextKeyActiveSubscriptions), subs)

		h.usageUnrestricted(c, context.Background(), apiKey, middleware2.AuthSubject{UserID: 7}, nil, nil, nil)
		require.Equal(t, http.StatusOK, w.Code)

		var payload map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		require.Equal(t, "StdTier", payload["planName"])
		require.InDelta(t, 25.0, payload["remaining"].(float64), 0.001)
	})
}
