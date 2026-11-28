package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type checkinHandlerSettingRepoStub struct {
	service.SettingRepository
	values map[string]string
}

func (s *checkinHandlerSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	v, ok := s.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return v, nil
}

func (s *checkinHandlerSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (s *checkinHandlerSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	if s.values == nil {
		s.values = make(map[string]string)
	}
	for k, v := range settings {
		s.values[k] = v
	}
	return nil
}

type checkinHandlerRepoStub struct {
	snapshot     service.CheckinUserSnapshot
	last         *service.CheckinRecord
	calendar     []service.CheckinCalendarDay
	records      []service.CheckinRecord
	executeErr   error
	lastExecuted *service.CheckinExecuteInput
}

func (r *checkinHandlerRepoStub) ExecuteCheckin(_ context.Context, in service.CheckinExecuteInput) (service.CheckinRecord, error) {
	cp := in
	r.lastExecuted = &cp
	if r.executeErr != nil {
		return service.CheckinRecord{}, r.executeErr
	}
	return service.CheckinRecord{
		ID:                11,
		UserID:            in.UserID,
		CheckinDate:       in.Today.Format("2006-01-02"),
		StreakDays:        1,
		MilestoneCycleDay: 1,
		BaseAmount:        in.BaseAmount,
		TotalAmount:       in.BaseAmount,
		ValidityDays:      in.ValidityDays,
		ExpiresAt:         in.ExpiresAt,
		ClientIP:          in.ClientIP,
		CreatedAt:         in.Now,
	}, nil
}

func (r *checkinHandlerRepoStub) GetCheckinUserSnapshot(context.Context, int64) (service.CheckinUserSnapshot, error) {
	return r.snapshot, nil
}

func (r *checkinHandlerRepoStub) GetLastCheckinRecord(context.Context, int64) (*service.CheckinRecord, error) {
	return r.last, nil
}

func (r *checkinHandlerRepoStub) GetCheckinRecordByDate(context.Context, int64, time.Time) (*service.CheckinRecord, error) {
	return r.last, nil
}

func (r *checkinHandlerRepoStub) ListCheckinCalendar(context.Context, int64, time.Time, time.Time) ([]service.CheckinCalendarDay, error) {
	return r.calendar, nil
}

func (r *checkinHandlerRepoStub) ListUserCheckinRecords(context.Context, int64, pagination.PaginationParams) ([]service.CheckinRecord, *pagination.PaginationResult, error) {
	return r.records, &pagination.PaginationResult{Total: int64(len(r.records)), Page: 1, PageSize: 20, Pages: 1}, nil
}

func (r *checkinHandlerRepoStub) ListAdminCheckinRecords(context.Context, service.AdminCheckinRecordFilter) ([]service.AdminCheckinRecord, *pagination.PaginationResult, error) {
	return nil, &pagination.PaginationResult{}, nil
}

func (r *checkinHandlerRepoStub) GetAdminCheckinStats(context.Context, int) (service.AdminCheckinStats, error) {
	return service.AdminCheckinStats{}, nil
}

type checkinExpiringRepoStub struct {
	service.ExpiringBalanceRepository
	summary service.ExpiringBalanceSummary
	lots    []service.ExpiringBalanceLot
}

func (s *checkinExpiringRepoStub) GetActiveExpiringSummary(context.Context, int64) (service.ExpiringBalanceSummary, error) {
	return s.summary, nil
}

func (s *checkinExpiringRepoStub) ListUserExpiringLots(context.Context, int64, string, pagination.PaginationParams) ([]service.ExpiringBalanceLot, *pagination.PaginationResult, error) {
	return s.lots, &pagination.PaginationResult{Total: int64(len(s.lots)), Page: 1, PageSize: 20, Pages: 1}, nil
}

type checkinHandlerCaptchaStub struct {
	proof service.CaptchaProof
	ip    string
}

func (s *checkinHandlerCaptchaStub) VerifyCaptcha(_ context.Context, proof service.CaptchaProof, remoteIP string) error {
	s.proof = proof
	s.ip = remoteIP
	return nil
}

type checkinGatewayUserRepoStub struct {
	service.UserRepository
	user *service.User
}

func (r *checkinGatewayUserRepoStub) GetByID(context.Context, int64) (*service.User, error) {
	cp := *r.user
	return &cp, nil
}

func (r *checkinGatewayUserRepoStub) Update(context.Context, *service.User, service.UserUpdateFields) error {
	return nil
}

func (r *checkinGatewayUserRepoStub) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

func (r *checkinGatewayUserRepoStub) ListUserAuthIdentities(context.Context, int64) ([]service.UserAuthIdentityRecord, error) {
	return nil, nil
}

func TestCheckinHandlerEndpointsAndClientIPAndAliyunProof(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfgBytes, err := json.Marshal(&service.CheckinSettings{
		Enabled:      true,
		RewardMode:   service.CheckinRewardModeFixed,
		BaseAmount:   0.25,
		ValidityDays: 2,
		Risk:         service.CheckinRiskConfig{CaptchaEnabled: true},
	})
	require.NoError(t, err)

	settingStore := &checkinHandlerSettingRepoStub{
		values: map[string]string{
			service.SettingKeyCheckinEnabled:               "true",
			service.SettingKeyCheckinConfig:                string(cfgBytes),
			service.SettingKeyAliyunCaptchaEnabled:         "true",
			service.SettingKeyAliyunCaptchaAccessKeyID:     "ak",
			service.SettingKeyAliyunCaptchaAccessKeySecret: "sk",
			service.SettingKeyAliyunCaptchaSceneID:         "scene",
		},
	}
	settingSvc := service.NewSettingService(settingStore, &config.Config{})

	nearest := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	expRepo := &checkinExpiringRepoStub{
		summary: service.ExpiringBalanceSummary{
			ActiveBalance:    0.25,
			NearestExpiresAt: &nearest,
			NearestAmount:    0.25,
		},
		lots: []service.ExpiringBalanceLot{{ID: 1, UserID: 7, InitialAmount: 0.25, RemainingAmount: 0.25, Status: service.ExpiringLotStatusActive}},
	}
	expSvc := service.NewExpiringBalanceService(expRepo)

	repo := &checkinHandlerRepoStub{
		snapshot: service.CheckinUserSnapshot{ID: 7, Status: service.StatusActive, CreatedAt: time.Now().Add(-48 * time.Hour), Balance: 1.0},
		calendar: []service.CheckinCalendarDay{{Date: "2026-11-27", BaseAmount: 0.25, TotalAmount: 0.25}},
		records:  []service.CheckinRecord{{ID: 11, UserID: 7, CheckinDate: "2026-11-27", TotalAmount: 0.25}},
	}
	svc := service.NewCheckinService(settingSvc, repo, expSvc)
	captchaStub := &checkinHandlerCaptchaStub{}
	svc.SetCaptchaVerifier(captchaStub)
	h := NewCheckinHandler(svc)

	// Unauthenticated request -> 401.
	wUnauth := httptest.NewRecorder()
	cUnauth, _ := gin.CreateTestContext(wUnauth)
	cUnauth.Request = httptest.NewRequest(http.MethodGet, "/api/v1/checkin/status", nil)
	h.GetStatus(cUnauth)
	require.Equal(t, http.StatusUnauthorized, wUnauth.Code)

	// Status -> 200.
	wStatus := httptest.NewRecorder()
	cStatus, _ := gin.CreateTestContext(wStatus)
	cStatus.Request = httptest.NewRequest(http.MethodGet, "/api/v1/checkin/status", nil)
	cStatus.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
	h.GetStatus(cStatus)
	require.Equal(t, http.StatusOK, wStatus.Code)
	require.Contains(t, wStatus.Body.String(), `"captcha_required":true`)

	// Checkin with Aliyun param & X-Forwarded-For -> maps aliyun_captcha_verify_param and uses ip.GetClientIP(c).
	wCheckin := httptest.NewRecorder()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
		c.Next()
	})
	r.POST("/checkin", h.Checkin)
	req := httptest.NewRequest(http.MethodPost, "/checkin", bytes.NewBufferString(`{"aliyun_captcha_verify_param":"aliyun-token-123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "198.51.100.42")
	req.RemoteAddr = "127.0.0.1:54321"
	r.ServeHTTP(wCheckin, req)
	require.Equal(t, http.StatusOK, wCheckin.Code, wCheckin.Body.String())
	require.Equal(t, "aliyun-token-123", captchaStub.proof.TurnstileToken)
	require.Equal(t, "198.51.100.42", captchaStub.ip)
	require.NotNil(t, repo.lastExecuted)
	require.Equal(t, "198.51.100.42", repo.lastExecuted.ClientIP)

	// Error propagation (409 already checked in).
	repo.executeErr = infraerrors.Conflict(service.CheckinReasonAlreadyDone, "already checked in today")
	wConflict := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/checkin", bytes.NewBufferString(`{"aliyun_captcha_verify_param":"aliyun-token-123"}`))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wConflict, req2)
	require.Equal(t, http.StatusConflict, wConflict.Code)
	require.Contains(t, wConflict.Body.String(), service.CheckinReasonAlreadyDone)

	// Calendar, Records, Lots -> 200.
	for _, tc := range []struct {
		url string
		fn  func(*gin.Context)
	}{
		{"/api/v1/checkin/calendar?month=2026-11", h.GetCalendar},
		{"/api/v1/checkin/records?page=1&page_size=10", h.ListRecords},
		{"/api/v1/checkin/lots?page=1&page_size=10&status=active", h.ListLots},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, tc.url, nil)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
		tc.fn(c)
		require.Equal(t, http.StatusOK, w.Code, tc.url)
	}
}

func TestSettingHandlerGetPublicSettingsExposesCheckinEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, enabled := range []string{"false", "true"} {
		repo := &checkinHandlerSettingRepoStub{
			values: map[string]string{service.SettingKeyCheckinEnabled: enabled},
		}
		h := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), "v-test")
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)
		h.GetPublicSettings(c)
		require.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data struct {
				CheckinEnabled bool `json:"checkin_enabled"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, enabled == "true", resp.Data.CheckinEnabled)
	}
}

func TestGatewayHandlerUsagePureBalanceAndMultiSubExpiringFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	nearest := time.Now().Add(12 * time.Hour).UTC().Truncate(time.Second)
	expSvc := service.NewExpiringBalanceService(&checkinExpiringRepoStub{
		summary: service.ExpiringBalanceSummary{
			ActiveBalance:    0.75,
			NearestExpiresAt: &nearest,
			NearestAmount:    0.75,
		},
	})
	userRepo := &checkinGatewayUserRepoStub{
		user: &service.User{ID: 7, Balance: 2.25, Status: service.StatusActive},
	}
	userSvc := service.NewUserService(userRepo, nil, nil, nil)
	gw := NewGatewayHandler(nil, nil, nil, nil, userSvc, nil, nil, nil, nil, nil, nil, nil, nil, &config.Config{}, nil)
	gw.SetExpiringBalanceService(expSvc)

	// 1. Pure balance mode.
	wPure := httptest.NewRecorder()
	cPure, _ := gin.CreateTestContext(wPure)
	cPure.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	gw.usageUnrestricted(cPure, context.Background(), &service.APIKey{
		ID:    1,
		Group: &service.Group{ID: 1, Name: "std", SubscriptionType: service.SubscriptionTypeStandard},
	}, middleware2.AuthSubject{UserID: 7}, nil, nil, nil)
	require.Equal(t, http.StatusOK, wPure.Code)

	var pureResp map[string]any
	require.NoError(t, json.Unmarshal(wPure.Body.Bytes(), &pureResp))
	require.InDelta(t, 3.0, pureResp["balance"], 1e-9)
	require.InDelta(t, 3.0, pureResp["remaining"], 1e-9)
	require.InDelta(t, 2.25, pureResp["regular_balance"], 1e-9)
	require.InDelta(t, 0.75, pureResp["expiring_balance"], 1e-9)
	require.NotEmpty(t, pureResp["nearest_expiring_at"])

	// 2. Multi-sub standard group mode: remaining stays subscription remaining, balance becomes total available.
	wMulti := httptest.NewRecorder()
	cMulti, _ := gin.CreateTestContext(wMulti)
	cMulti.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	dailyLimit := 50.0
	stdGroup := &service.Group{
		ID:               1,
		Name:             "std",
		SubscriptionType: service.SubscriptionTypeStandard,
		DailyLimitUSD:    &dailyLimit,
	}
	cMulti.Set(string(middleware2.ContextKeyActiveSubscriptions), []service.UserSubscription{
		{
			ID:            10,
			GroupID:       1,
			Group:         stdGroup,
			DailyUsageUSD: 10.0,
			ExpiresAt:     time.Now().Add(24 * time.Hour),
		},
	})
	gw.usageUnrestricted(cMulti, context.Background(), &service.APIKey{ID: 1, Group: stdGroup}, middleware2.AuthSubject{UserID: 7}, nil, nil, nil)
	require.Equal(t, http.StatusOK, wMulti.Code)

	var multiResp map[string]any
	require.NoError(t, json.Unmarshal(wMulti.Body.Bytes(), &multiResp))
	require.InDelta(t, 40.0, multiResp["remaining"], 1e-9)
	require.InDelta(t, 3.0, multiResp["balance"], 1e-9)
	require.InDelta(t, 2.25, multiResp["regular_balance"], 1e-9)
	require.InDelta(t, 0.75, multiResp["expiring_balance"], 1e-9)
	require.NotEmpty(t, multiResp["nearest_expiring_at"])
}

func TestUserHandlerProfileMutationsIncludeExpiringBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)

	nearest := time.Now().Add(6 * time.Hour).UTC().Truncate(time.Second)
	expSvc := service.NewExpiringBalanceService(&checkinExpiringRepoStub{
		summary: service.ExpiringBalanceSummary{
			ActiveBalance:    1.25,
			FrozenBalance:    0.50,
			NearestExpiresAt: &nearest,
			NearestAmount:    1.25,
		},
	})
	userRepo := &checkinGatewayUserRepoStub{
		user: &service.User{
			ID:      7,
			Email:   "u7@example.com",
			Balance: 3.0,
			Role:    service.RoleUser,
			Status:  service.StatusActive,
		},
	}
	uh := NewUserHandler(service.NewUserService(userRepo, nil, nil, nil), nil, nil, nil, nil, nil)
	uh.SetExpiringBalanceService(expSvc)

	for _, tc := range []struct {
		name string
		req  *http.Request
		fn   func(*gin.Context)
	}{
		{
			name: "GetProfile",
			req:  httptest.NewRequest(http.MethodGet, "/api/v1/user/profile", nil),
			fn:   uh.GetProfile,
		},
		{
			name: "UpdateProfile",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodPut, "/api/v1/user", bytes.NewBufferString(`{"username":"newname"}`))
				r.Header.Set("Content-Type", "application/json")
				return r
			}(),
			fn: uh.UpdateProfile,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = tc.req
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
			tc.fn(c)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())

			var resp struct {
				Data struct {
					Balance               float64    `json:"balance"`
					ExpiringBalance       *float64   `json:"expiring_balance"`
					TotalAvailableBalance *float64   `json:"total_available_balance"`
					ExpiringFrozenBalance *float64   `json:"expiring_frozen_balance"`
					NearestExpiringAt     *time.Time `json:"nearest_expiring_at"`
					NearestExpiringAmount *float64   `json:"nearest_expiring_amount"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.InDelta(t, 3.0, resp.Data.Balance, 1e-9)
			require.NotNil(t, resp.Data.ExpiringBalance)
			require.InDelta(t, 1.25, *resp.Data.ExpiringBalance, 1e-9)
			require.NotNil(t, resp.Data.TotalAvailableBalance)
			require.InDelta(t, 4.25, *resp.Data.TotalAvailableBalance, 1e-9)
			require.NotNil(t, resp.Data.ExpiringFrozenBalance)
			require.InDelta(t, 0.50, *resp.Data.ExpiringFrozenBalance, 1e-9)
			require.NotNil(t, resp.Data.NearestExpiringAt)
			require.NotNil(t, resp.Data.NearestExpiringAmount)
			require.InDelta(t, 1.25, *resp.Data.NearestExpiringAmount, 1e-9)
		})
	}
}
