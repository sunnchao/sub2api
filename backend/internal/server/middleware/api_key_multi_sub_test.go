//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type multiSubRepoStub struct {
	service.UserSubscriptionRepository
	subs []service.UserSubscription
}

func (r *multiSubRepoStub) GetByID(_ context.Context, id int64) (*service.UserSubscription, error) {
	for i := range r.subs {
		if r.subs[i].ID == id {
			cp := r.subs[i]
			return &cp, nil
		}
	}
	return nil, service.ErrSubscriptionNotFound
}

func (r *multiSubRepoStub) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	list, err := r.ListActiveByUserIDAndGroupID(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, service.ErrSubscriptionNotFound
	}
	return &list[0], nil
}

func (r *multiSubRepoStub) ListActiveByUserIDAndGroupID(_ context.Context, userID, groupID int64) ([]service.UserSubscription, error) {
	out := make([]service.UserSubscription, 0)
	for _, s := range r.subs {
		if s.UserID == userID && s.GroupID == groupID && s.Status == service.SubscriptionStatusActive {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ExpiresAt.Equal(out[j].ExpiresAt) {
			return out[i].ExpiresAt.Before(out[j].ExpiresAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *multiSubRepoStub) ListActiveByUserIDAndTemplateID(_ context.Context, _ int64, _ int64) ([]service.UserSubscription, error) {
	return nil, nil
}
func (r *multiSubRepoStub) FindLatestByUserIDAndTemplateID(_ context.Context, _ int64, _ int64) (*service.UserSubscription, error) {
	return nil, service.ErrSubscriptionNotFound
}
func (r *multiSubRepoStub) UpsertSnapshot(_ context.Context, _ *service.UserSubscriptionSnapshot) error {
	return nil
}
func (r *multiSubRepoStub) GetSnapshotsBySubscriptionIDs(_ context.Context, _ []int64) (map[int64]*service.UserSubscriptionSnapshot, error) {
	return nil, nil
}

func f64(v float64) *float64 { return &v }

func runAPIKeyAuthRequest(
	apiKey *service.APIKey,
	subs []service.UserSubscription,
	path string,
) (status int, cands []GroupCandidate, chosenSub *service.UserSubscription, allActive []service.UserSubscription) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	keyRepo := &multiGroupAuthRepoStub{byKey: map[string]*service.APIKey{apiKey.Key: apiKey}}
	subRepo := &multiSubRepoStub{subs: subs}
	apiKeySvc := service.NewAPIKeyService(keyRepo, nil, nil, subRepo, nil, nil, cfg)
	subSvc := service.NewSubscriptionService(nil, subRepo, nil, nil, cfg)
	defer subSvc.Stop()

	r := gin.New()
	r.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeySvc, subSvc, cfg)))
	r.Any(path, func(c *gin.Context) {
		if cs, ok := GetGroupCandidatesFromContext(c); ok {
			cands = cs
		}
		if s, ok := GetSubscriptionFromContext(c); ok {
			chosenSub = s
		}
		if all, ok := GetActiveSubscriptionsFromContext(c); ok {
			allActive = all
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("x-api-key", apiKey.Key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, cands, chosenSub, allActive
}

func TestAPIKeyAuthMultiSubFEFOAndFallback(t *testing.T) {
	now := time.Now()
	gid := int64(10)

	subGroup := &service.Group{
		ID:               gid,
		Name:             "SubGrp",
		Platform:         service.PlatformAnthropic,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
	}
	stdGroup := &service.Group{
		ID:               gid,
		Name:             "StdGrp",
		Platform:         service.PlatformAnthropic,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}
	exclusiveStdGroup := &service.Group{
		ID:               gid,
		Name:             "ExclusiveStd",
		Platform:         service.PlatformAnthropic,
		Status:           service.StatusActive,
		IsExclusive:      true,
		SubscriptionType: service.SubscriptionTypeStandard,
	}

	makeKey := func(group *service.Group, balance float64) *service.APIKey {
		return &service.APIKey{
			ID:      1,
			UserID:  50,
			Key:     "sk-multi-sub",
			Status:  service.StatusActive,
			GroupID: &gid,
			Group:   group,
			User: &service.User{
				ID:          50,
				Status:      service.StatusActive,
				Role:        service.RoleUser,
				Balance:     balance,
				Concurrency: 5,
			},
		}
	}

	subEarly := service.UserSubscription{
		ID: 101, UserID: 50, GroupID: gid, Status: service.SubscriptionStatusActive,
		StartsAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(5 * 24 * time.Hour),
		DailyWindowStart: &now, WeeklyWindowStart: &now, MonthlyWindowStart: &now,
		HasCustomLimits: true, DailyLimitUSD: f64(10), DailyUsageUSD: 2,
	}
	subLate := service.UserSubscription{
		ID: 102, UserID: 50, GroupID: gid, Status: service.SubscriptionStatusActive,
		StartsAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(20 * 24 * time.Hour),
		DailyWindowStart: &now, WeeklyWindowStart: &now, MonthlyWindowStart: &now,
		HasCustomLimits: true, DailyLimitUSD: f64(50), DailyUsageUSD: 0,
	}

	// A1: Two parallel active subscriptions expand into 2 FEFO candidates.
	status, cands, chosen, all := runAPIKeyAuthRequest(makeKey(subGroup, 0), []service.UserSubscription{subLate, subEarly}, "/v1/messages")
	require.Equal(t, http.StatusOK, status)
	require.Len(t, cands, 2)
	require.Equal(t, int64(101), cands[0].Subscription.ID)
	require.Equal(t, int64(102), cands[1].Subscription.ID)
	require.Equal(t, int64(101), chosen.ID)
	require.Len(t, all, 2)

	// A2: FEFO #1 daily limit exhausted -> seamlessly selects #2 on fast path (no candidate slice needed).
	subEarlyExhausted := subEarly
	subEarlyExhausted.DailyUsageUSD = 10.01
	status, cands, chosen, _ = runAPIKeyAuthRequest(makeKey(subGroup, 0), []service.UserSubscription{subEarlyExhausted, subLate}, "/v1/messages")
	require.Equal(t, http.StatusOK, status)
	require.Empty(t, cands)
	require.NotNil(t, chosen)
	require.Equal(t, int64(102), chosen.ID)

	// A3: Both subs exhausted on subscription group, even with balance=100 -> 429, NEVER falls back to balance.
	subLateExhausted := subLate
	subLateExhausted.DailyUsageUSD = 50.01
	status, _, _, _ = runAPIKeyAuthRequest(makeKey(subGroup, 100), []service.UserSubscription{subEarlyExhausted, subLateExhausted}, "/v1/messages")
	require.Equal(t, http.StatusTooManyRequests, status)

	// A4: Standard group + 1 active template sub + positive balance -> expands [sub, balance].
	status, cands, chosen, _ = runAPIKeyAuthRequest(makeKey(stdGroup, 20), []service.UserSubscription{subEarly}, "/v1/messages")
	require.Equal(t, http.StatusOK, status)
	require.Len(t, cands, 2)
	require.NotNil(t, cands[0].Subscription)
	require.Equal(t, int64(101), cands[0].Subscription.ID)
	require.Nil(t, cands[1].Subscription)
	require.Equal(t, int64(101), chosen.ID)

	// A5: Standard group + exhausted sub + positive balance -> seamlessly falls back to balance (Subscription == nil).
	status, cands, chosen, _ = runAPIKeyAuthRequest(makeKey(stdGroup, 20), []service.UserSubscription{subEarlyExhausted}, "/v1/messages")
	require.Equal(t, http.StatusOK, status)
	require.Empty(t, cands)
	require.Nil(t, chosen)

	// A6: Standard group + exhausted sub + zero balance -> returns 429 USAGE_LIMIT_EXCEEDED (not 403 balance).
	status, _, _, _ = runAPIKeyAuthRequest(makeKey(stdGroup, 0), []service.UserSubscription{subEarlyExhausted}, "/v1/messages")
	require.Equal(t, http.StatusTooManyRequests, status)

	// A7: Exclusive standard group without AllowedGroups -> allowed when holding active template sub, denied when none.
	status, _, chosen, _ = runAPIKeyAuthRequest(makeKey(exclusiveStdGroup, 0), []service.UserSubscription{subEarly}, "/v1/messages")
	require.Equal(t, http.StatusOK, status)
	require.NotNil(t, chosen)
	require.Equal(t, int64(101), chosen.ID)

	status, _, _, _ = runAPIKeyAuthRequest(makeKey(exclusiveStdGroup, 100), nil, "/v1/messages")
	require.Equal(t, http.StatusForbidden, status)

	// A8: Multi-group key (Group A with 2 subs + Group B with balance) -> expands 3 candidates in order.
	gA := &service.Group{ID: 11, Name: "GA", Platform: service.PlatformAnthropic, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeSubscription}
	gB := &service.Group{ID: 12, Name: "GB", Platform: service.PlatformAnthropic, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard}
	gAID := gA.ID
	mgKey := &service.APIKey{
		ID: 2, UserID: 50, Key: "sk-mg-subs", Status: service.StatusActive,
		GroupID: &gAID, Group: gA, GroupIDs: []int64{11, 12}, Groups: []*service.Group{gA, gB},
		User: &service.User{ID: 50, Status: service.StatusActive, Role: service.RoleUser, Balance: 15, Concurrency: 5},
	}
	subA1 := subEarly
	subA1.GroupID = 11
	subA2 := subLate
	subA2.GroupID = 11
	status, cands, _, _ = runAPIKeyAuthRequest(mgKey, []service.UserSubscription{subA1, subA2}, "/v1/messages")
	require.Equal(t, http.StatusOK, status)
	require.Len(t, cands, 3)
	require.Equal(t, int64(101), cands[0].Subscription.ID)
	require.Equal(t, int64(102), cands[1].Subscription.ID)
	require.Equal(t, int64(12), cands[2].Group.ID)
	require.Nil(t, cands[2].Subscription)
}
