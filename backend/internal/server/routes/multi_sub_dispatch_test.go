//go:build unit

package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMultiGroupDispatcherMultiSubFailoverAndSkipGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupA := &service.Group{ID: 10, Name: "GroupA", Platform: service.PlatformAnthropic, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeSubscription}
	groupB := &service.Group{ID: 20, Name: "GroupB", Platform: service.PlatformAnthropic, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard}
	sub1 := &service.UserSubscription{ID: 101, UserID: 1, GroupID: 10, Status: service.SubscriptionStatusActive}
	sub2 := &service.UserSubscription{ID: 102, UserID: 1, GroupID: 10, Status: service.SubscriptionStatusActive}

	baseKey := &service.APIKey{
		ID:      1,
		UserID:  1,
		GroupID: &groupA.ID,
		Group:   groupA,
		User:    &service.User{ID: 1, Balance: 50},
	}
	candidates := []middleware.GroupCandidate{
		{Group: groupA, Subscription: sub1},
		{Group: groupA, Subscription: sub2},
		{Group: groupB, Subscription: nil},
	}

	// D1: Subscription quota error (GroupRetryNextSubscription) retries sibling sub2 on SAME GroupA.
	t.Run("subscription_limit_advances_to_sibling_sub", func(t *testing.T) {
		var attempts []int64
		d := newMultiGroupDispatcher(nil, false)
		r := gin.New()
		r.POST("/v1/messages", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyAPIKey), baseKey)
			c.Set(string(middleware.ContextKeyGroupCandidates), candidates)
			c.Next()
		}, d.Wrap(func(c *gin.Context) {
			sub, _ := middleware.GetSubscriptionFromContext(c)
			var subID int64
			if sub != nil {
				subID = sub.ID
			}
			attempts = append(attempts, subID)
			if subID == 101 {
				middleware.SetGroupRetryDecision(c, middleware.GroupRetryNextSubscription)
				c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"code": "usage_limit_exceeded", "message": "daily limit"}})
				return
			}
			c.JSON(http.StatusOK, gin.H{"sub_id": subID})
		}))

		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3-5-sonnet"}`))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, []int64{101, 102}, attempts)
	})

	// D2: Group-level failure (e.g. 503 upstream or GroupRetryNext group RPM) SKIPS sibling sub2 on GroupA and jumps straight to GroupB.
	t.Run("group_failure_skips_same_group_subscriptions", func(t *testing.T) {
		var attempts []string
		d := newMultiGroupDispatcher(nil, false)
		r := gin.New()
		r.POST("/v1/messages", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyAPIKey), baseKey)
			c.Set(string(middleware.ContextKeyGroupCandidates), candidates)
			c.Next()
		}, d.Wrap(func(c *gin.Context) {
			key, _ := middleware.GetAPIKeyFromContext(c)
			sub, _ := middleware.GetSubscriptionFromContext(c)
			if key.Group.ID == 10 {
				attempts = append(attempts, "A:"+string(rune('0'+sub.ID-100)))
				middleware.SetGroupRetryDecision(c, middleware.GroupRetryNext)
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "upstream overloaded"}})
				return
			}
			attempts = append(attempts, "B:balance")
			c.JSON(http.StatusOK, gin.H{"ok": true})
		}))

		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3-5-sonnet"}`))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, []string{"A:1", "B:balance"}, attempts, "must skip A:2 when GroupA fails at group level")
	})

	// D3: WrapModels queries each distinct group_id only once despite multiple subscriptions on GroupA.
	t.Run("wrap_models_deduplicates_group_ids", func(t *testing.T) {
		var queriedGroups []int64
		d := newMultiGroupDispatcher(nil, false)
		r := gin.New()
		r.GET("/v1/models", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyAPIKey), baseKey)
			c.Set(string(middleware.ContextKeyGroupCandidates), candidates)
			c.Next()
		}, d.WrapModels(func(c *gin.Context) {
			key, _ := middleware.GetAPIKeyFromContext(c)
			queriedGroups = append(queriedGroups, key.Group.ID)
			c.JSON(http.StatusOK, gin.H{"object": "list", "data": []gin.H{{"id": key.Group.Name + "-m"}}})
		}))

		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, []int64{10, 20}, queriedGroups)
	})
}
