package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type m2ExpAPIKeyRepo struct {
	service.APIKeyRepository
	byKey map[string]*service.APIKey
}

func (r *m2ExpAPIKeyRepo) GetByKey(_ context.Context, key string) (*service.APIKey, error) {
	k, ok := r.byKey[key]
	if !ok {
		return nil, service.ErrAPIKeyNotFound
	}
	return k, nil
}

func (r *m2ExpAPIKeyRepo) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	return r.GetByKey(ctx, key)
}

func (r *m2ExpAPIKeyRepo) UpdateLastUsed(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

type m2ExpSubRepo struct {
	service.UserSubscriptionRepository
	subs []service.UserSubscription
}

func (r *m2ExpSubRepo) GetByID(_ context.Context, id int64) (*service.UserSubscription, error) {
	for i := range r.subs {
		if r.subs[i].ID == id {
			cp := r.subs[i]
			return &cp, nil
		}
	}
	return nil, service.ErrSubscriptionNotFound
}

func (r *m2ExpSubRepo) GetActiveByUserIDAndGroupID(_ context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	for i := range r.subs {
		if r.subs[i].UserID == userID && r.subs[i].GroupID == groupID && r.subs[i].Status == service.SubscriptionStatusActive {
			cp := r.subs[i]
			return &cp, nil
		}
	}
	return nil, service.ErrSubscriptionNotFound
}

func (r *m2ExpSubRepo) ListActiveByUserIDAndGroupID(_ context.Context, userID, groupID int64) ([]service.UserSubscription, error) {
	var out []service.UserSubscription
	for _, s := range r.subs {
		if s.UserID == userID && s.GroupID == groupID && s.Status == service.SubscriptionStatusActive {
			out = append(out, s)
		}
	}
	return out, nil
}

type m2ExpBalanceReader struct {
	calls   atomic.Int32
	balance float64
	err     error
}

func (r *m2ExpBalanceReader) GetUserBalance(_ context.Context, _ int64) (float64, error) {
	r.calls.Add(1)
	if r.err != nil {
		return 0, r.err
	}
	return r.balance, nil
}

func newM2TestStandardGroup(id int64) *service.Group {
	return &service.Group{
		ID:               id,
		Name:             "std",
		Platform:         service.PlatformAnthropic,
		SubscriptionType: service.SubscriptionTypeStandard,
		Status:           service.StatusActive,
	}
}

func newM2TestSubGroup(id int64) *service.Group {
	return &service.Group{
		ID:               id,
		Name:             "sub",
		Platform:         service.PlatformAnthropic,
		SubscriptionType: service.SubscriptionTypeSubscription,
		Status:           service.StatusActive,
	}
}

func runM2MiddlewareReq(mw gin.HandlerFunc, path string) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(mw)
	r.Any("/*any", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Authorization", "Bearer sk-exp-test")
	r.ServeHTTP(w, req)
	return w
}

func TestAPIKeyAuth_ExpiringBalancePaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Parallel()
	cfg := &config.Config{RunMode: config.RunModeStandard}

	t.Run("ungrouped standard & google allow expiring-only user", func(t *testing.T) {
		t.Parallel()
		key := &service.APIKey{
			ID: 1, Key: "sk-exp-test", Status: service.StatusAPIKeyActive, UserID: 10,
			User: &service.User{ID: 10, Balance: 0, Status: service.StatusActive, Role: service.RoleUser},
		}
		keySvc := service.NewAPIKeyService(&m2ExpAPIKeyRepo{byKey: map[string]*service.APIKey{key.Key: key}}, nil, nil, nil, nil, nil, cfg)
		reader := &m2ExpBalanceReader{balance: 0.25}

		wStd := runM2MiddlewareReq(gin.HandlerFunc(NewAPIKeyAuthMiddlewareWithBalanceReader(keySvc, nil, reader, cfg)), "/v1/messages")
		require.Equal(t, http.StatusOK, wStd.Code)
		wGoog := runM2MiddlewareReq(APIKeyAuthWithSubscriptionGoogleWithBalanceReader(keySvc, nil, reader, cfg), "/v1beta/models")
		require.Equal(t, http.StatusOK, wGoog.Code)
		require.Equal(t, int32(2), reader.calls.Load())
		require.Equal(t, 0.0, key.User.Balance) // must not mutate snapshot
	})

	t.Run("single standard group allows expiring-only and negative-regular covered by expiring", func(t *testing.T) {
		t.Parallel()
		grp := newM2TestStandardGroup(20)
		key := &service.APIKey{
			ID: 2, Key: "sk-exp-test", Status: service.StatusAPIKeyActive, UserID: 11,
			GroupID: &grp.ID, Group: grp,
			User: &service.User{ID: 11, Balance: -0.10, Status: service.StatusActive, Role: service.RoleUser},
		}
		keySvc := service.NewAPIKeyService(&m2ExpAPIKeyRepo{byKey: map[string]*service.APIKey{key.Key: key}}, nil, nil, nil, nil, nil, cfg)
		reader := &m2ExpBalanceReader{balance: 0.40}

		wStd := runM2MiddlewareReq(gin.HandlerFunc(NewAPIKeyAuthMiddlewareWithBalanceReader(keySvc, nil, reader, cfg)), "/v1/messages")
		require.Equal(t, http.StatusOK, wStd.Code)
		wGoog := runM2MiddlewareReq(APIKeyAuthWithSubscriptionGoogleWithBalanceReader(keySvc, nil, reader, cfg), "/v1beta/models")
		require.Equal(t, http.StatusOK, wGoog.Code)
	})

	t.Run("negative regular + smaller expiring still rejects 403", func(t *testing.T) {
		t.Parallel()
		grp := newM2TestStandardGroup(21)
		key := &service.APIKey{
			ID: 3, Key: "sk-exp-test", Status: service.StatusAPIKeyActive, UserID: 12,
			GroupID: &grp.ID, Group: grp,
			User: &service.User{ID: 12, Balance: -0.50, Status: service.StatusActive, Role: service.RoleUser},
		}
		keySvc := service.NewAPIKeyService(&m2ExpAPIKeyRepo{byKey: map[string]*service.APIKey{key.Key: key}}, nil, nil, nil, nil, nil, cfg)
		reader := &m2ExpBalanceReader{balance: -0.30}

		wStd := runM2MiddlewareReq(gin.HandlerFunc(NewAPIKeyAuthMiddlewareWithBalanceReader(keySvc, nil, reader, cfg)), "/v1/messages")
		require.Equal(t, http.StatusForbidden, wStd.Code)
		wGoog := runM2MiddlewareReq(APIKeyAuthWithSubscriptionGoogleWithBalanceReader(keySvc, nil, reader, cfg), "/v1beta/models")
		require.Equal(t, http.StatusForbidden, wGoog.Code)
	})

	t.Run("multi-group memoizes GetUserBalance to 1 call per request", func(t *testing.T) {
		t.Parallel()
		g1 := newM2TestStandardGroup(31)
		g2 := newM2TestStandardGroup(32)
		g3 := newM2TestStandardGroup(33)
		key := &service.APIKey{
			ID: 4, Key: "sk-exp-test", Status: service.StatusAPIKeyActive, UserID: 13,
			GroupID: &g1.ID, Group: g1,
			GroupIDs: []int64{g1.ID, g2.ID, g3.ID},
			Groups:   []*service.Group{g1, g2, g3},
			User:     &service.User{ID: 13, Balance: 0, Status: service.StatusActive, Role: service.RoleUser},
		}
		keySvc := service.NewAPIKeyService(&m2ExpAPIKeyRepo{byKey: map[string]*service.APIKey{key.Key: key}}, nil, nil, nil, nil, nil, cfg)
		reader := &m2ExpBalanceReader{balance: 0.15}

		w := runM2MiddlewareReq(gin.HandlerFunc(NewAPIKeyAuthMiddlewareWithBalanceReader(keySvc, nil, reader, cfg)), "/v1/messages")
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, int32(1), reader.calls.Load())
	})

	t.Run("subscription-type group with subscriptionService==nil legacy fallback checks expiring balance", func(t *testing.T) {
		t.Parallel()
		subGrp := newM2TestSubGroup(40)
		key := &service.APIKey{
			ID: 5, Key: "sk-exp-test", Status: service.StatusAPIKeyActive, UserID: 14,
			GroupID: &subGrp.ID, Group: subGrp,
			User: &service.User{ID: 14, Balance: 0, Status: service.StatusActive, Role: service.RoleUser},
		}
		keySvc := service.NewAPIKeyService(&m2ExpAPIKeyRepo{byKey: map[string]*service.APIKey{key.Key: key}}, nil, nil, nil, nil, nil, cfg)
		reader := &m2ExpBalanceReader{balance: 0.50}

		w := runM2MiddlewareReq(gin.HandlerFunc(NewAPIKeyAuthMiddlewareWithBalanceReader(keySvc, nil, reader, cfg)), "/v1/messages")
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, int32(1), reader.calls.Load())
	})

	t.Run("regular balance > 0 skips GetUserBalance completely", func(t *testing.T) {
		t.Parallel()
		key := &service.APIKey{
			ID: 6, Key: "sk-exp-test", Status: service.StatusAPIKeyActive, UserID: 15,
			User: &service.User{ID: 15, Balance: 1.0, Status: service.StatusActive, Role: service.RoleUser},
		}
		keySvc := service.NewAPIKeyService(&m2ExpAPIKeyRepo{byKey: map[string]*service.APIKey{key.Key: key}}, nil, nil, nil, nil, nil, cfg)
		reader := &m2ExpBalanceReader{err: errors.New("should not be called")}

		w := runM2MiddlewareReq(gin.HandlerFunc(NewAPIKeyAuthMiddlewareWithBalanceReader(keySvc, nil, reader, cfg)), "/v1/messages")
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, int32(0), reader.calls.Load())
	})

	t.Run("GetUserBalance error returns 503 BILLING_SERVICE_ERROR and preserves 429 USAGE_LIMIT_EXCEEDED", func(t *testing.T) {
		t.Parallel()
		// 1. Ungrouped & Google -> 503
		key := &service.APIKey{
			ID: 7, Key: "sk-exp-test", Status: service.StatusAPIKeyActive, UserID: 16,
			User: &service.User{ID: 16, Balance: 0, Status: service.StatusActive, Role: service.RoleUser},
		}
		keySvc := service.NewAPIKeyService(&m2ExpAPIKeyRepo{byKey: map[string]*service.APIKey{key.Key: key}}, nil, nil, nil, nil, nil, cfg)
		reader := &m2ExpBalanceReader{err: errors.New("db outage")}

		wStd := runM2MiddlewareReq(gin.HandlerFunc(NewAPIKeyAuthMiddlewareWithBalanceReader(keySvc, nil, reader, cfg)), "/v1/messages")
		require.Equal(t, http.StatusServiceUnavailable, wStd.Code)
		var errResp ErrorResponse
		require.NoError(t, json.Unmarshal(wStd.Body.Bytes(), &errResp))
		require.Equal(t, "BILLING_SERVICE_ERROR", errResp.Code)

		wGoog := runM2MiddlewareReq(APIKeyAuthWithSubscriptionGoogleWithBalanceReader(keySvc, nil, reader, cfg), "/v1beta/models")
		require.Equal(t, http.StatusServiceUnavailable, wGoog.Code)

		// 2. Multi-group: first group is exhausted subscription (429), second is standard group where balance lookup fails -> preserves 429!
		subGrp := newM2TestSubGroup(50)
		stdGrp := newM2TestStandardGroup(51)
		dailyLimit := 10.0
		subGrp.DailyLimitUSD = &dailyLimit
		winStart := time.Now().Add(-time.Minute)
		subRepo := &m2ExpSubRepo{
			subs: []service.UserSubscription{{
				ID: 500, UserID: 16, GroupID: subGrp.ID, Status: service.SubscriptionStatusActive,
				ExpiresAt: time.Now().Add(time.Hour), DailyWindowStart: &winStart,
				WeeklyWindowStart: &winStart, MonthlyWindowStart: &winStart, DailyUsageUSD: 10.5,
			}},
		}
		subSvc := service.NewSubscriptionService(nil, subRepo, nil, nil, cfg)
		t.Cleanup(subSvc.Stop)

		mgKey := &service.APIKey{
			ID: 8, Key: "sk-exp-test", Status: service.StatusAPIKeyActive, UserID: 16,
			GroupID: &subGrp.ID, Group: subGrp,
			GroupIDs: []int64{subGrp.ID, stdGrp.ID},
			Groups:   []*service.Group{subGrp, stdGrp},
			User:     &service.User{ID: 16, Balance: 0, Status: service.StatusActive, Role: service.RoleUser},
		}
		mgKeySvc := service.NewAPIKeyService(&m2ExpAPIKeyRepo{byKey: map[string]*service.APIKey{mgKey.Key: mgKey}}, nil, nil, subRepo, nil, nil, cfg)
		wMG := runM2MiddlewareReq(gin.HandlerFunc(NewAPIKeyAuthMiddlewareWithBalanceReader(mgKeySvc, subSvc, reader, cfg)), "/v1/messages")
		require.Equal(t, http.StatusTooManyRequests, wMG.Code)
	})

	t.Run("skipBilling and SimpleMode bypass balance reader even on error", func(t *testing.T) {
		t.Parallel()
		key := &service.APIKey{
			ID: 9, Key: "sk-exp-test", Status: service.StatusAPIKeyActive, UserID: 17,
			User: &service.User{ID: 17, Balance: 0, Status: service.StatusActive, Role: service.RoleUser},
		}
		keySvc := service.NewAPIKeyService(&m2ExpAPIKeyRepo{byKey: map[string]*service.APIKey{key.Key: key}}, nil, nil, nil, nil, nil, cfg)
		reader := &m2ExpBalanceReader{err: errors.New("db outage")}

		wUsage := runM2MiddlewareReq(gin.HandlerFunc(NewAPIKeyAuthMiddlewareWithBalanceReader(keySvc, nil, reader, cfg)), "/v1/usage")
		require.Equal(t, http.StatusOK, wUsage.Code)
		require.Equal(t, int32(0), reader.calls.Load())

		simpleCfg := &config.Config{RunMode: config.RunModeSimple}
		simpleKeySvc := service.NewAPIKeyService(&m2ExpAPIKeyRepo{byKey: map[string]*service.APIKey{key.Key: key}}, nil, nil, nil, nil, nil, simpleCfg)
		wSimple := runM2MiddlewareReq(gin.HandlerFunc(NewAPIKeyAuthMiddlewareWithBalanceReader(simpleKeySvc, nil, reader, simpleCfg)), "/v1/messages")
		require.Equal(t, http.StatusOK, wSimple.Code)
		require.Equal(t, int32(0), reader.calls.Load())
	})
}

func TestAPIKeyAuth_GlobalSetAPIKeyAuthBalanceReaderFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	key := &service.APIKey{
		ID: 10, Key: "sk-exp-test", Status: service.StatusAPIKeyActive, UserID: 18,
		User: &service.User{ID: 18, Balance: 0, Status: service.StatusActive, Role: service.RoleUser},
	}
	keySvc := service.NewAPIKeyService(&m2ExpAPIKeyRepo{byKey: map[string]*service.APIKey{key.Key: key}}, nil, nil, nil, nil, nil, cfg)
	reader := &m2ExpBalanceReader{balance: 0.75}

	SetAPIKeyAuthBalanceReader(reader)
	t.Cleanup(func() { SetAPIKeyAuthBalanceReader(nil) })

	wStd := runM2MiddlewareReq(gin.HandlerFunc(NewAPIKeyAuthMiddleware(keySvc, nil, cfg)), "/v1/messages")
	require.Equal(t, http.StatusOK, wStd.Code)
	wGoog := runM2MiddlewareReq(APIKeyAuthWithSubscriptionGoogle(keySvc, nil, cfg), "/v1beta/models")
	require.Equal(t, http.StatusOK, wGoog.Code)
	require.Equal(t, int32(2), reader.calls.Load())

	// Typed-nil *service.BillingCacheService must not panic when passed to setter.
	var typedNil *service.BillingCacheService
	SetAPIKeyAuthBalanceReader(typedNil)
	wDenied := runM2MiddlewareReq(gin.HandlerFunc(NewAPIKeyAuthMiddleware(keySvc, nil, cfg)), "/v1/messages")
	require.Equal(t, http.StatusForbidden, wDenied.Code)
}

func TestProvideAPIKeyAuthMiddleware_WiresGlobalAndExplicitBalanceReader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)
	t.Cleanup(func() { SetAPIKeyAuthBalanceReader(nil) })

	mw := ProvideAPIKeyAuthMiddleware(nil, nil, billingCacheSvc, cfg)
	require.NotNil(t, mw)
	holder := activeAPIKeyAuthBalanceReader.Load()
	require.NotNil(t, holder)
	require.Equal(t, billingCacheSvc, holder.reader)
}
