package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type adminCheckinSettingRepoStub struct {
	service.SettingRepository
	values map[string]string
}

func (s *adminCheckinSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (s *adminCheckinSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	if s.values == nil {
		s.values = make(map[string]string)
	}
	for k, v := range settings {
		s.values[k] = v
	}
	return nil
}

type adminCheckinRepoStub struct {
	service.CheckinRepository
	lastFilter service.AdminCheckinRecordFilter
	records    []service.AdminCheckinRecord
	stats      service.AdminCheckinStats
}

func (r *adminCheckinRepoStub) ListAdminCheckinRecords(_ context.Context, filter service.AdminCheckinRecordFilter) ([]service.AdminCheckinRecord, *pagination.PaginationResult, error) {
	r.lastFilter = filter
	return r.records, &pagination.PaginationResult{Total: int64(len(r.records)), Page: 1, PageSize: 20, Pages: 1}, nil
}

func (r *adminCheckinRepoStub) GetAdminCheckinStats(context.Context, int) (service.AdminCheckinStats, error) {
	return r.stats, nil
}

type adminExpiringRepoStub struct {
	service.ExpiringBalanceRepository
	summaries map[int64]service.ExpiringBalanceSummary
}

func (r *adminExpiringRepoStub) GetActiveExpiringSummary(_ context.Context, userID int64) (service.ExpiringBalanceSummary, error) {
	return r.summaries[userID], nil
}

func (r *adminExpiringRepoStub) GetActiveExpiringSummariesBatch(_ context.Context, userIDs []int64) (map[int64]service.ExpiringBalanceSummary, error) {
	out := make(map[int64]service.ExpiringBalanceSummary)
	for _, id := range userIDs {
		if s, ok := r.summaries[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

func TestAdminCheckinHandlerEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	settingRepo := &adminCheckinSettingRepoStub{values: map[string]string{}}
	settingSvc := service.NewSettingService(settingRepo, &config.Config{})
	repo := &adminCheckinRepoStub{
		records: []service.AdminCheckinRecord{
			{CheckinRecord: service.CheckinRecord{ID: 1, UserID: 7, CheckinDate: "2026-11-27", TotalAmount: 0.5}, UserEmail: "u7@example.com", Username: "u7"},
		},
		stats: service.AdminCheckinStats{TodayCheckins: 1, TodayGranted: 0.5, DailyTrend: []service.AdminCheckinDailyTrend{{Date: "2026-11-27", CheckinCount: 1, GrantedAmount: 0.5}}},
	}
	svc := service.NewCheckinService(settingSvc, repo, nil)
	h := NewCheckinHandler(svc)

	// UpdateConfig -> saves and returns normalized config.
	wPut := httptest.NewRecorder()
	cPut, _ := gin.CreateTestContext(wPut)
	cPut.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/checkin/config", bytes.NewBufferString(`{"enabled":true,"reward_mode":"fixed","base_amount":0.5,"validity_days":3,"risk":{"daily_total_budget_usd":10}}`))
	cPut.Request.Header.Set("Content-Type", "application/json")
	h.UpdateConfig(cPut)
	require.Equal(t, http.StatusOK, wPut.Code, wPut.Body.String())

	// GetConfig -> returns updated config.
	wGet := httptest.NewRecorder()
	cGet, _ := gin.CreateTestContext(wGet)
	cGet.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/checkin/config", nil)
	h.GetConfig(cGet)
	require.Equal(t, http.StatusOK, wGet.Code)
	require.Contains(t, wGet.Body.String(), `"enabled":true`)

	// GetStats -> includes budget percent.
	wStats := httptest.NewRecorder()
	cStats, _ := gin.CreateTestContext(wStats)
	cStats.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/checkin/stats?days=30", nil)
	h.GetStats(cStats)
	require.Equal(t, http.StatusOK, wStats.Code)
	require.Contains(t, wStats.Body.String(), `"daily_budget_limit":10`)
	require.Contains(t, wStats.Body.String(), `"daily_budget_percent":5`)

	// ListRecords invalid date -> 400.
	wBad := httptest.NewRecorder()
	cBad, _ := gin.CreateTestContext(wBad)
	cBad.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/checkin/records?start_date=bad-date", nil)
	h.ListRecords(cBad)
	require.Equal(t, http.StatusBadRequest, wBad.Code)
	require.Contains(t, wBad.Body.String(), "CHECKIN_INVALID_DATE")

	// ListRecords valid filters -> 200.
	wRec := httptest.NewRecorder()
	cRec, _ := gin.CreateTestContext(wRec)
	cRec.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/checkin/records?user_id=7&search=u7&client_ip=1.2.3.4&start_date=2026-11-01&end_date=2026-11-30", nil)
	h.ListRecords(cRec)
	require.Equal(t, http.StatusOK, wRec.Code)
	require.Equal(t, int64(7), repo.lastFilter.UserID)
	require.Equal(t, "u7", repo.lastFilter.Search)
	require.Equal(t, "1.2.3.4", repo.lastFilter.ClientIP)
}

func TestAdminUserHandlerExpiringBalanceListGetUpdateAndScopedKeyPreservation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	nearest := time.Now().Add(10 * time.Hour).UTC().Truncate(time.Second)
	expSvc := service.NewExpiringBalanceService(&adminExpiringRepoStub{
		summaries: map[int64]service.ExpiringBalanceSummary{
			7: {ActiveBalance: 1.5, FrozenBalance: 0.2, NearestExpiresAt: &nearest, NearestAmount: 1.5},
			// User 8 intentionally absent from batch map (0 active lots).
		},
	})

	svc := newStubAdminService()
	svc.users = []service.User{
		{ID: 7, Email: "u7@example.com", Balance: 2.0, FrozenBalance: 0.1, TotalRecharged: 10.0, Status: service.StatusActive},
		{ID: 8, Email: "u8@example.com", Balance: 5.0, Status: service.StatusActive},
	}

	h := NewUserHandler(svc, nil, nil, nil, nil, nil, nil)
	h.SetExpiringBalanceService(expSvc)

	// 1. Unrestricted List: user 7 gets 1.5 expiring, user 8 gets 0 expiring (NOT omitted!).
	wList := httptest.NewRecorder()
	cList, _ := gin.CreateTestContext(wList)
	cList.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	h.List(cList)
	require.Equal(t, http.StatusOK, wList.Code)

	var listResp struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &listResp))
	require.Len(t, listResp.Data.Items, 2)
	require.InDelta(t, 2.0, listResp.Data.Items[0]["balance"], 1e-9)
	require.InDelta(t, 1.5, listResp.Data.Items[0]["expiring_balance"], 1e-9)
	require.InDelta(t, 3.5, listResp.Data.Items[0]["total_available_balance"], 1e-9)
	require.InDelta(t, 5.0, listResp.Data.Items[1]["balance"], 1e-9)
	require.InDelta(t, 0.0, listResp.Data.Items[1]["expiring_balance"], 1e-9)
	require.InDelta(t, 5.0, listResp.Data.Items[1]["total_available_balance"], 1e-9)

	// 2. Unrestricted GetByID: user 8 also gets expiring_balance: 0 and total_available_balance: 5.0.
	wGet := httptest.NewRecorder()
	r := gin.New()
	r.GET("/users/:id", h.GetByID)
	r.PUT("/users/:id", h.Update)
	r.POST("/users/:id/balance", h.UpdateBalance)
	r.ServeHTTP(wGet, httptest.NewRequest(http.MethodGet, "/users/8", nil))
	require.Equal(t, http.StatusOK, wGet.Code)
	var getResp struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &getResp))
	require.InDelta(t, 5.0, getResp.Data["balance"], 1e-9)
	require.InDelta(t, 0.0, getResp.Data["expiring_balance"], 1e-9)
	require.InDelta(t, 5.0, getResp.Data["total_available_balance"], 1e-9)

	// 3. Unrestricted Update includes expiring fields.
	wUpd := httptest.NewRecorder()
	reqUpd := httptest.NewRequest(http.MethodPut, "/users/7", bytes.NewBufferString(`{"username":"u7"}`))
	reqUpd.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wUpd, reqUpd)
	require.Equal(t, http.StatusOK, wUpd.Code)
	var updResp struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(wUpd.Body.Bytes(), &updResp))
	require.Contains(t, updResp.Data, "expiring_balance")
	require.InDelta(t, 1.5, updResp.Data["expiring_balance"], 1e-9)

	// 4. Restricted scoped admin key ("users.balance.read") preserves exact field whitelist (must NOT leak expiring fields).
	wScoped := httptest.NewRecorder()
	rScoped := gin.New()
	rScoped.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 99})
		c.Request = c.Request.WithContext(adminaccess.WithPrincipal(c.Request.Context(), &adminaccess.Principal{
			KeyID: "scoped-key",
			Policy: adminaccess.Policy{
				GlobalPermissions: []string{"users.balance.read"},
			},
		}))
		c.Next()
	})
	rScoped.GET("/users", h.List)
	rScoped.GET("/users/:id", h.GetByID)
	rScoped.ServeHTTP(wScoped, httptest.NewRequest(http.MethodGet, "/users/7", nil))
	require.Equal(t, http.StatusOK, wScoped.Code)
	var scopedGet struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(wScoped.Body.Bytes(), &scopedGet))
	require.InDelta(t, 2.0, scopedGet.Data["balance"], 1e-9)
	require.InDelta(t, 0.1, scopedGet.Data["frozen_balance"], 1e-9)
	require.InDelta(t, 10.0, scopedGet.Data["total_recharged"], 1e-9)
	require.NotContains(t, scopedGet.Data, "expiring_balance")
	require.NotContains(t, scopedGet.Data, "total_available_balance")
	require.NotContains(t, scopedGet.Data, "nearest_expiring_at")
}
