package routes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newMultiGroupTestRouter(candidates []middleware.GroupCandidate, baseAPIKey *service.APIKey, isGemini bool, setup func(r *gin.Engine, d *multiGroupDispatcher)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	d := newMultiGroupDispatcher(nil, isGemini)
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyGroupCandidates), candidates)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: baseAPIKey.UserID, Concurrency: 5})
		if len(candidates) > 0 {
			middleware.SetActiveGroupCandidate(c, baseAPIKey, candidates[0])
		}
		c.Next()
	})
	r.Use(middleware.DeferGroupAdmission)
	setup(r, d)
	return r
}

func makeMultiGroupBaseKey(userID int64, groups ...*service.Group) (*service.APIKey, []middleware.GroupCandidate) {
	ids := make([]int64, 0, len(groups))
	cands := make([]middleware.GroupCandidate, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.ID)
		cands = append(cands, middleware.GroupCandidate{Group: g})
	}
	var primaryID *int64
	var primary *service.Group
	if len(groups) > 0 {
		id := groups[0].ID
		primaryID = &id
		primary = groups[0]
	}
	return &service.APIKey{
		ID:       99,
		UserID:   userID,
		Key:      "sk-route-test",
		Status:   service.StatusActive,
		GroupID:  primaryID,
		Group:    primary,
		GroupIDs: ids,
		Groups:   groups,
		User:     &service.User{ID: userID, Status: service.StatusActive, Role: service.RoleUser, Balance: 10},
	}, cands
}

func TestMultiGroupDispatcher_OrderedExecutionAndRetryWithBillingRate(t *testing.T) {
	g1 := &service.Group{
		ID:             1,
		Name:           "anthropic-first",
		Platform:       service.PlatformAnthropic,
		Status:         service.StatusActive,
		RateMultiplier: 0.5,
		ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"claude-only"}},
	}
	g2 := &service.Group{
		ID:             2,
		Name:           "openai-second",
		Platform:       service.PlatformOpenAI,
		Status:         service.StatusActive,
		RateMultiplier: 1.75,
	}
	g3 := &service.Group{
		ID:             3,
		Name:           "deepseek-third",
		Platform:       service.PlatformDeepseek,
		Status:         service.StatusActive,
		RateMultiplier: 0.3,
	}
	baseKey, cands := makeMultiGroupBaseKey(7, g1, g2, g3)

	var seenOrder []int64
	var matchedGroupID int64
	var matchedRate float64
	var seenBodies []string

	router := newMultiGroupTestRouter(cands, baseKey, false, func(r *gin.Engine, d *multiGroupDispatcher) {
		r.POST("/v1/messages", middleware.GroupModelAllowlist(), d.Wrap(func(c *gin.Context) {
			activeKey, _ := middleware.GetAPIKeyFromContext(c)
			ctxGroup, _ := c.Request.Context().Value(ctxkey.Group).(*service.Group)
			seenOrder = append(seenOrder, activeKey.Group.ID)
			raw, _ := io.ReadAll(c.Request.Body)
			seenBodies = append(seenBodies, string(raw))

			if activeKey.Group.ID == 2 {
				// Simulate 503 No available accounts + leaked Retry-After header from g2
				c.Header("Retry-After", "99")
				c.JSON(http.StatusServiceUnavailable, gin.H{"type": "error", "error": gin.H{"type": "api_error", "message": "No available accounts"}})
				return
			}
			matchedGroupID = ctxGroup.ID
			matchedRate = activeKey.Group.RateMultiplier
			c.JSON(http.StatusOK, gin.H{"group_id": activeKey.Group.ID, "rate": activeKey.Group.RateMultiplier})
		}))
	})

	body := `{"model":"deepseek-chat"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// g1 was rejected by allowlist (404) before handler; g2 ran and failed with 503; g3 ran and succeeded.
	require.Equal(t, []int64{2, 3}, seenOrder)
	require.Equal(t, int64(3), matchedGroupID)
	require.InDelta(t, 0.3, matchedRate, 1e-9)
	require.Equal(t, []string{body, body}, seenBodies)
	require.Empty(t, w.Header().Get("Retry-After"), "failed candidate header must not leak")
}

func TestMultiGroupDispatcher_NoRetryAfterStreamStarts(t *testing.T) {
	g1 := &service.Group{ID: 1, Name: "g1", Platform: service.PlatformAnthropic, Status: service.StatusActive}
	g2 := &service.Group{ID: 2, Name: "g2", Platform: service.PlatformOpenAI, Status: service.StatusActive}
	baseKey, cands := makeMultiGroupBaseKey(7, g1, g2)

	calls := 0
	router := newMultiGroupTestRouter(cands, baseKey, false, func(r *gin.Engine, d *multiGroupDispatcher) {
		r.POST("/v1/messages", d.Wrap(func(c *gin.Context) {
			calls++
			c.Header("Content-Type", "text/event-stream")
			c.Status(http.StatusOK)
			_, _ = c.Writer.WriteString("data: {\"type\":\"ping\"}\n\n")
			c.Writer.Flush()
			// Subsequent in-band SSE error must not trigger g2 retry
			_, _ = c.Writer.WriteString("data: {\"type\":\"error\"}\n\n")
		}))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude"}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, calls)
	require.Contains(t, w.Body.String(), "ping")
}

func TestMultiGroupDispatcher_FlushCommitsErrorResponse(t *testing.T) {
	first := &service.Group{ID: 1, Platform: service.PlatformOpenAI, Status: service.StatusActive}
	second := &service.Group{ID: 2, Platform: service.PlatformOpenAI, Status: service.StatusActive}
	key, candidates := makeMultiGroupBaseKey(7, first, second)
	calls := 0
	router := newMultiGroupTestRouter(candidates, key, false, func(router *gin.Engine, dispatcher *multiGroupDispatcher) {
		router.POST("/v1/responses", dispatcher.Wrap(func(c *gin.Context) {
			calls++
			c.Status(http.StatusServiceUnavailable)
			c.Writer.Flush()
			_, _ = c.Writer.WriteString("published failure")
		}))
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.True(t, response.Flushed)
	require.Equal(t, "published failure", response.Body.String())
	require.Equal(t, 1, calls)
}

func TestMultiGroupDispatcher_NoRetryOnNonRetryableErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "malformed json 400",
			status: http.StatusBadRequest,
			body:   `{"type":"error","error":{"type":"invalid_request_error","message":"Failed to parse request body"}}`,
		},
		{
			name:   "api key quota exhausted 429",
			status: http.StatusTooManyRequests,
			body:   `{"error":{"code":"api_key_quota_exhausted","message":"API key 额度已用完"}}`,
		},
		{
			name:   "user rpm exceeded 429",
			status: http.StatusTooManyRequests,
			body:   `{"error":{"code":"user_rpm_exceeded","message":"user requests-per-minute limit exceeded"}}`,
		},
		{
			name:   "content moderation 403",
			status: http.StatusForbidden,
			body:   `{"type":"error","error":{"type":"permission_error","code":"content_policy_violation","message":"blocked"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g1 := &service.Group{ID: 1, Name: "g1", Platform: service.PlatformAnthropic, Status: service.StatusActive}
			g2 := &service.Group{ID: 2, Name: "g2", Platform: service.PlatformOpenAI, Status: service.StatusActive}
			baseKey, cands := makeMultiGroupBaseKey(7, g1, g2)

			calls := 0
			router := newMultiGroupTestRouter(cands, baseKey, false, func(r *gin.Engine, d *multiGroupDispatcher) {
				r.POST("/v1/messages", d.Wrap(func(c *gin.Context) {
					calls++
					c.Data(tc.status, "application/json", []byte(tc.body))
				}))
			})

			req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m"}`))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			require.Equal(t, tc.status, w.Code)
			require.Equal(t, 1, calls, "non-retryable error must stop at first group")
			require.Equal(t, tc.body, w.Body.String())
		})
	}
}

func TestMultiGroupDispatcher_BalanceToSubscriptionRetryAndBestFailureSelection(t *testing.T) {
	stdGroup := &service.Group{ID: 1, Name: "std", Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard}
	subGroup := &service.Group{ID: 2, Name: "sub", Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeSubscription}

	// Balance -> Subscription 403 failover succeeds on subGroup
	baseKey, _ := makeMultiGroupBaseKey(7, stdGroup, subGroup)
	cands := []middleware.GroupCandidate{
		{Group: stdGroup},
		{Group: subGroup, Subscription: &service.UserSubscription{ID: 55, GroupID: 2}},
	}
	router := newMultiGroupTestRouter(cands, baseKey, false, func(r *gin.Engine, d *multiGroupDispatcher) {
		r.POST("/v1/responses", d.Wrap(func(c *gin.Context) {
			activeKey, _ := middleware.GetAPIKeyFromContext(c)
			if activeKey.Group.ID == 1 {
				c.JSON(http.StatusForbidden, gin.H{"code": "INSUFFICIENT_BALANCE", "message": "Insufficient account balance"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"ok": true, "group": activeKey.Group.ID})
		}))
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5"}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"group":2`)

	// Best failure ranking: g1 fails with 429 GROUP_RPM_EXCEEDED (rank 3), g2 fails with 404 model_not_found (rank 1) -> returns 429
	g2Allowlist := &service.Group{
		ID:             2,
		Name:           "g2",
		Platform:       service.PlatformAnthropic,
		Status:         service.StatusActive,
		ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"only-other"}},
	}
	baseFail, candsFail := makeMultiGroupBaseKey(7, stdGroup, g2Allowlist)
	routerFail := newMultiGroupTestRouter(candsFail, baseFail, false, func(r *gin.Engine, d *multiGroupDispatcher) {
		r.POST("/v1/responses", middleware.GroupModelAllowlist(), d.Wrap(func(c *gin.Context) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"code": "group_rpm_exceeded", "message": "group rpm exceeded"}})
		}))
	})
	reqFail := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5"}`))
	wFail := httptest.NewRecorder()
	routerFail.ServeHTTP(wFail, reqFail)
	require.Equal(t, http.StatusTooManyRequests, wFail.Code)
	require.Contains(t, wFail.Body.String(), "group_rpm_exceeded")

	// Cancelled request stops immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, shouldRetryNextGroup(http.StatusServiceUnavailable, []byte(`{"error":"no accounts"}`), false, ctx.Err(), middleware.GroupRetryDefault))
}

func TestMultiGroupDispatcher_AggregatesModelsAcrossGroups(t *testing.T) {
	g1 := &service.Group{ID: 1, Name: "anthropic", Platform: service.PlatformAnthropic, Status: service.StatusActive}
	g2 := &service.Group{ID: 2, Name: "openai", Platform: service.PlatformOpenAI, Status: service.StatusActive}
	baseKey, cands := makeMultiGroupBaseKey(7, g1, g2)

	router := newMultiGroupTestRouter(cands, baseKey, false, func(r *gin.Engine, d *multiGroupDispatcher) {
		r.GET("/v1/models", d.WrapModels(func(c *gin.Context) {
			activeKey, _ := middleware.GetAPIKeyFromContext(c)
			if activeKey.Group.ID == 1 {
				c.JSON(http.StatusOK, gin.H{"object": "list", "data": []gin.H{{"id": "claude-sonnet-4"}, {"id": "shared-model"}}})
				return
			}
			c.JSON(http.StatusOK, gin.H{"object": "list", "data": []gin.H{{"id": "shared-model"}, {"id": "gpt-5"}}})
		}))
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "list", resp.Object)
	require.Len(t, resp.Data, 3)
	require.Equal(t, "claude-sonnet-4", resp.Data[0].ID)
	require.Equal(t, "shared-model", resp.Data[1].ID)
	require.Equal(t, "gpt-5", resp.Data[2].ID)
}
