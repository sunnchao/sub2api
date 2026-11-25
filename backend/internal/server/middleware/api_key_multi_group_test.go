package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type multiGroupAuthRepoStub struct {
	service.APIKeyRepository
	byKey map[string]*service.APIKey
}

func (s *multiGroupAuthRepoStub) GetByKeyForAuth(_ context.Context, key string) (*service.APIKey, error) {
	if k, ok := s.byKey[key]; ok {
		return k, nil
	}
	return nil, service.ErrAPIKeyNotFound
}

func (s *multiGroupAuthRepoStub) UpdateLastUsed(context.Context, int64, time.Time) error {
	return nil
}

func TestAPIKeyAuth_MultiGroupSkipsUnavailableAndZeroBalanceToSubscription(t *testing.T) {
	gin.SetMode(gin.TestMode)

	disabledGroup := &service.Group{
		ID:               1,
		Name:             "disabled-g1",
		Platform:         service.PlatformAnthropic,
		Status:           service.StatusDisabled,
		SubscriptionType: service.SubscriptionTypeStandard,
		RateMultiplier:   1.0,
	}
	forbiddenExclusive := &service.Group{
		ID:               2,
		Name:             "exclusive-g2",
		Platform:         service.PlatformOpenAI,
		Status:           service.StatusActive,
		IsExclusive:      true,
		SubscriptionType: service.SubscriptionTypeStandard,
		RateMultiplier:   1.2,
	}
	zeroBalanceStd := &service.Group{
		ID:               3,
		Name:             "std-g3",
		Platform:         service.PlatformOpenAI,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
		RateMultiplier:   1.5,
	}
	activeOpenAI := &service.Group{
		ID:               4,
		Name:             "openai-g4",
		Platform:         service.PlatformOpenAI,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
		RateMultiplier:   2.0,
	}

	g1ID := int64(1)
	repo := &multiGroupAuthRepoStub{
		byKey: map[string]*service.APIKey{
			"sk-mg-auth": {
				ID:       10,
				UserID:   20,
				Key:      "sk-mg-auth",
				Name:     "mg",
				Status:   service.StatusActive,
				GroupID:  &g1ID,
				Group:    disabledGroup,
				GroupIDs: []int64{1, 2, 3, 4},
				Groups:   []*service.Group{disabledGroup, forbiddenExclusive, zeroBalanceStd, activeOpenAI},
				User: &service.User{
					ID:          20,
					Status:      service.StatusActive,
					Role:        service.RoleUser,
					Balance:     5.0,
					Concurrency: 2,
				},
			},
			"sk-mg-all-bad": {
				ID:       11,
				UserID:   20,
				Key:      "sk-mg-all-bad",
				Name:     "all-bad",
				Status:   service.StatusActive,
				GroupID:  &g1ID,
				Group:    disabledGroup,
				GroupIDs: []int64{1, 2},
				Groups:   []*service.Group{disabledGroup, forbiddenExclusive},
				User: &service.User{
					ID:          20,
					Status:      service.StatusActive,
					Role:        service.RoleUser,
					Balance:     5.0,
					Concurrency: 2,
				},
			},
		},
	}

	apiKeySvc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, &config.Config{})
	mw := NewAPIKeyAuthMiddleware(apiKeySvc, nil, &config.Config{RunMode: config.RunModeStandard})

	r := gin.New()
	r.Use(gin.HandlerFunc(mw))
	r.POST("/v1/messages", func(c *gin.Context) {
		cands, ok := GetGroupCandidatesFromContext(c)
		require.True(t, ok)
		require.Len(t, cands, 2)
		require.Equal(t, int64(3), cands[0].Group.ID)
		require.Equal(t, int64(4), cands[1].Group.ID)

		active, ok := GetAPIKeyFromContext(c)
		require.True(t, ok)
		require.NotNil(t, active.Group)
		require.Equal(t, int64(3), active.Group.ID)

		ctxGroup, ok := c.Request.Context().Value(ctxkey.Group).(*service.Group)
		require.True(t, ok)
		require.Equal(t, int64(3), ctxGroup.ID)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-api-key", "sk-mg-auth")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// When all candidate groups are unavailable/disallowed, aborts with primary group's 403 GROUP_DISABLED
	reqBad := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	reqBad.Header.Set("x-api-key", "sk-mg-all-bad")
	wBad := httptest.NewRecorder()
	r.ServeHTTP(wBad, reqBad)
	require.Equal(t, http.StatusForbidden, wBad.Code)
	require.Contains(t, wBad.Body.String(), "GROUP_DISABLED")
}

func TestAPIKeyAuthGoogle_MultiGroupSelectsFirstActiveCandidate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	g1 := &service.Group{
		ID:               10,
		Name:             "disabled-g10",
		Platform:         service.PlatformGemini,
		Status:           service.StatusDisabled,
		SubscriptionType: service.SubscriptionTypeStandard,
	}
	g2 := &service.Group{
		ID:               11,
		Name:             "active-g11",
		Platform:         service.PlatformGemini,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
		RateMultiplier:   1.25,
	}
	g1ID := int64(10)
	repo := &multiGroupAuthRepoStub{
		byKey: map[string]*service.APIKey{
			"sk-google-mg": {
				ID:       12,
				UserID:   21,
				Key:      "sk-google-mg",
				Name:     "g-mg",
				Status:   service.StatusActive,
				GroupID:  &g1ID,
				Group:    g1,
				GroupIDs: []int64{10, 11},
				Groups:   []*service.Group{g1, g2},
				User: &service.User{
					ID:          21,
					Status:      service.StatusActive,
					Role:        service.RoleUser,
					Balance:     5.0,
					Concurrency: 2,
				},
			},
		},
	}

	apiKeySvc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, &config.Config{})
	mw := APIKeyAuthWithSubscriptionGoogle(apiKeySvc, nil, &config.Config{RunMode: config.RunModeStandard})

	r := gin.New()
	r.Use(mw)
	r.GET("/v1beta/models", func(c *gin.Context) {
		active, ok := GetAPIKeyFromContext(c)
		require.True(t, ok)
		require.NotNil(t, active.Group)
		require.Equal(t, int64(11), active.Group.ID)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	req.Header.Set("x-goog-api-key", "sk-google-mg")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}
