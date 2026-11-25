package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type multiGroupGatewayRepo struct {
	service.APIKeyRepository
	key       *service.APIKey
	rateReads int
}

func (repo *multiGroupGatewayRepo) GetByKeyForAuth(context.Context, string) (*service.APIKey, error) {
	return repo.key, nil
}

func (repo *multiGroupGatewayRepo) UpdateLastUsed(context.Context, int64, time.Time) error {
	return nil
}

func (repo *multiGroupGatewayRepo) GetRateLimitData(context.Context, int64) (*service.APIKeyRateLimitData, error) {
	repo.rateReads++
	now := time.Now()
	return &service.APIKeyRateLimitData{Usage5h: 1, Window5hStart: &now, Window1dStart: &now, Window7dStart: &now}, nil
}

type multiGroupGatewayBalance struct {
	service.UserRepository
	balance float64
}

func (repo multiGroupGatewayBalance) GetByID(_ context.Context, userID int64) (*service.User, error) {
	return &service.User{ID: userID, Balance: repo.balance}, nil
}

type multiGroupGatewaySubscriptions struct {
	service.UserSubscriptionRepository
}

func (repo multiGroupGatewaySubscriptions) GetActiveByUserIDAndGroupID(_ context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	now := time.Now()
	return &service.UserSubscription{
		ID: 1, UserID: userID, GroupID: groupID, Status: service.SubscriptionStatusActive,
		ExpiresAt: now.Add(time.Hour), DailyWindowStart: &now, WeeklyWindowStart: &now, MonthlyWindowStart: &now,
	}, nil
}

type multiGroupGatewayRPM struct {
	service.UserRPMCache
	count int
}

func (rpm *multiGroupGatewayRPM) IncrementUserRPM(context.Context, int64) (int, error) {
	rpm.count++
	return rpm.count, nil
}

type multiGroupGatewayAccounts struct {
	service.AccountRepository
	groups []int64
}

func (repo *multiGroupGatewayAccounts) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, _ string) ([]service.Account, error) {
	repo.groups = append(repo.groups, groupID)
	return nil, nil
}

type multiGroupGatewayAudit struct {
	code   string
	groups chan int64
}

func (audit multiGroupGatewayAudit) EffectiveMode() securityaudit.Mode {
	return securityaudit.ModeBlocking
}

func (audit multiGroupGatewayAudit) Enqueue(context.Context, securityaudit.Request) error {
	return nil
}

func (audit multiGroupGatewayAudit) Evaluate(_ context.Context, req securityaudit.Request) (*securityaudit.PromptDecision, error) {
	audit.groups <- *req.GroupID
	return nil, &securityaudit.GuardError{Code: audit.code}
}

type multiGroupGatewayTaskStore struct {
	service.ImageTaskStore
	attempts int
}

func (store *multiGroupGatewayTaskStore) Save(context.Context, *service.ImageTaskRecord, time.Duration) error {
	store.attempts++
	return errors.New("test task storage failure")
}

type multiGroupGatewayFixture struct {
	key      *service.APIKey
	keys     *multiGroupGatewayRepo
	accounts *multiGroupGatewayAccounts
	balance  float64
	rpm      *multiGroupGatewayRPM
	audit    securityaudit.PromptEngine
	tasks    *service.ImageTaskService
}

func (fixture *multiGroupGatewayFixture) router(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeStandard, Gateway: config.GatewayConfig{MaxBodySize: 1 << 20, TextMaxBodySize: 1 << 20}}
	fixture.keys = &multiGroupGatewayRepo{key: fixture.key}
	fixture.accounts = &multiGroupGatewayAccounts{}
	userRepo := multiGroupGatewayBalance{balance: fixture.balance}
	subRepo := multiGroupGatewaySubscriptions{}
	api := service.NewAPIKeyService(fixture.keys, userRepo, nil, subRepo, nil, nil, cfg)
	var rpmCache service.UserRPMCache
	if fixture.rpm != nil {
		rpmCache = fixture.rpm
	}
	billing := service.NewBillingCacheService(nil, userRepo, subRepo, fixture.keys, rpmCache, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	subscriptions := service.NewSubscriptionService(nil, subRepo, billing, nil, cfg)
	t.Cleanup(subscriptions.Stop)
	concurrency := service.NewConcurrencyService(nil)
	openAIService := service.NewOpenAIGatewayService(fixture.accounts, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, billing, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	audit := securityaudit.NewCoordinator(nil, fixture.audit)
	openAI := handler.ProvideOpenAIGatewayHandler(openAIService, nil, concurrency, billing, api, nil, nil, nil, nil, nil, cfg, audit, nil)
	gateway := handler.ProvideGatewayHandler(&service.GatewayService{}, openAIService, nil, nil, nil, concurrency, billing, nil, api, nil, nil, nil, nil, cfg, nil, audit, nil)
	router := gin.New()
	RegisterGatewayRoutes(router, &handler.Handlers{
		Gateway: gateway, OpenAIGateway: openAI, AsyncImage: handler.NewAsyncImageHandler(fixture.tasks, openAI),
	}, middleware.NewAPIKeyAuthMiddleware(api, subscriptions, cfg), api, subscriptions, nil, nil, nil, cfg)
	return router
}

func (fixture *multiGroupGatewayFixture) post(router *gin.Engine, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+fixture.key.Key)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestMultiGroupGatewayRuntimeBilling(t *testing.T) {
	for _, name := range []string{"api_key_window", "user_rpm_exhausted", "user_rpm_once_across_groups", "balance_to_subscription"} {
		t.Run(name, func(t *testing.T) {
			first := &service.Group{ID: 11, Platform: service.PlatformOpenAI, Status: service.StatusActive}
			second := &service.Group{ID: 12, Platform: service.PlatformOpenAI, Status: service.StatusActive}
			key, _ := makeMultiGroupBaseKey(10, first, second)
			fixture := &multiGroupGatewayFixture{key: key, balance: key.User.Balance, rpm: &multiGroupGatewayRPM{}}
			switch name {
			case "api_key_window":
				key.RateLimit5h = 1
			case "user_rpm_exhausted":
				key.User.RPMLimit = 1
				fixture.rpm.count = 1
			case "user_rpm_once_across_groups":
				key.User.RPMLimit = 1
			case "balance_to_subscription":
				second.SubscriptionType = service.SubscriptionTypeSubscription
				fixture.balance = 0
			}
			router := fixture.router(t)
			recorder := fixture.post(router, "/v1/responses", `{"model":"gpt-4.1","input":"hello"}`)
			switch name {
			case "api_key_window":
				require.Equal(t, http.StatusTooManyRequests, recorder.Code)
				require.Equal(t, 1, fixture.keys.rateReads)
			case "user_rpm_exhausted":
				require.Equal(t, http.StatusTooManyRequests, recorder.Code)
				require.Equal(t, 2, fixture.rpm.count)
			case "user_rpm_once_across_groups":
				require.Equal(t, []int64{first.ID, second.ID}, fixture.accounts.groups)
				require.Equal(t, 1, fixture.rpm.count)
			case "balance_to_subscription":
				require.Equal(t, []int64{second.ID}, fixture.accounts.groups, recorder.Body.String())
			}
		})
	}
}

func TestMultiGroupGatewayGeminiAuditStopsRetry(t *testing.T) {
	for _, code := range []string{securityaudit.ErrorCodeUnavailable, securityaudit.ErrorCodeInvalidResponse} {
		t.Run(code, func(t *testing.T) {
			first := &service.Group{ID: 21, Platform: service.PlatformGemini, Status: service.StatusActive}
			second := &service.Group{ID: 22, Platform: service.PlatformGemini, Status: service.StatusActive}
			key, _ := makeMultiGroupBaseKey(20, first, second)
			groups := make(chan int64, 2)
			fixture := &multiGroupGatewayFixture{key: key, balance: key.User.Balance, audit: multiGroupGatewayAudit{code: code, groups: groups}}
			router := fixture.router(t)
			recorder := fixture.post(router, "/v1beta/models/gemini-2.5-pro:generateContent", `{"contents":[{"parts":[{"text":"hello"}]}]}`)
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.Contains(t, recorder.Body.String(), code)
			require.Len(t, groups, 1)
			require.Equal(t, first.ID, <-groups)
		})
	}
}

func TestMultiGroupGatewayAsyncImageChecksActiveGroup(t *testing.T) {
	first := &service.Group{ID: 31, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true,
		ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"another-model"}}}
	second := &service.Group{ID: 32, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true}
	key, _ := makeMultiGroupBaseKey(30, first, second)
	store := &multiGroupGatewayTaskStore{}
	fixture := &multiGroupGatewayFixture{key: key, tasks: service.NewImageTaskServiceWithUploader(store, nil, time.Minute, time.Second)}
	router := fixture.router(t)
	for _, path := range []string{"/v1/images/generations/async", "/images/generations/async"} {
		recorder := fixture.post(router, path, `{"model":"gpt-image-2","prompt":"test image"}`)
		require.Equal(t, http.StatusNotFound, recorder.Code)
		require.Contains(t, recorder.Body.String(), "not available for this group")
	}
	require.Zero(t, store.attempts)
}

func TestMultiGroupGatewayResponsesWebSocketSelectsFromFirstFrame(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		platform     string
		model        string
		allowedModel string
		wantGroup    int64
	}{
		{name: "first_group_disallows_model", platform: service.PlatformOpenAI, model: "gpt-4.1", allowedModel: "another-model", wantGroup: 42},
		{name: "first_platform_lacks_websocket_support", platform: service.PlatformDeepseek, model: "gpt-4.1", allowedModel: "gpt-4.1", wantGroup: 42},
		{name: "composite_target_lacks_websocket_support", platform: service.PlatformComposite, model: "deepseek-chat", allowedModel: "deepseek-chat", wantGroup: 42},
		{name: "compatible_composite_target_keeps_priority", platform: service.PlatformComposite, model: "gpt-4.1", allowedModel: "gpt-4.1", wantGroup: 41},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			first := &service.Group{ID: 41, Platform: scenario.platform, Status: service.StatusActive,
				ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{scenario.allowedModel}}}
			second := &service.Group{ID: 42, Platform: service.PlatformOpenAI, Status: service.StatusActive,
				ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{scenario.model}}}
			key, _ := makeMultiGroupBaseKey(40, first, second)
			groups := make(chan int64, 2)
			fixture := &multiGroupGatewayFixture{key: key, audit: multiGroupGatewayAudit{code: securityaudit.ErrorCodeUnavailable, groups: groups}}
			server := httptest.NewServer(fixture.router(t))
			t.Cleanup(server.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, _, err := coderws.Dial(ctx, server.URL+"/v1/responses", &coderws.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + key.Key}}})
			require.NoError(t, err)
			defer func() { _ = conn.CloseNow() }()
			frame := `{"type":"response.create","model":` + strconv.Quote(scenario.model) + `,"input":"hello"}`
			require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(frame)))
			_, payload, err := conn.Read(ctx)
			require.NoError(t, err)
			require.Contains(t, string(payload), securityaudit.ErrorCodeUnavailable)
			require.Len(t, groups, 1)
			require.Equal(t, scenario.wantGroup, <-groups)
		})
	}
}
