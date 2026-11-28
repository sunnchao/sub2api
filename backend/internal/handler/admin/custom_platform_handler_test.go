package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubCustomPlatformRepo struct {
	platforms map[string]*service.CustomPlatform
	usage     service.CustomPlatformUsageCounts
	listErr   error
}

func (r *stubCustomPlatformRepo) List(context.Context) ([]*service.CustomPlatform, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	out := make([]*service.CustomPlatform, 0, len(r.platforms))
	for _, platform := range r.platforms {
		out = append(out, platform)
	}
	return out, nil
}

func (r *stubCustomPlatformRepo) Get(_ context.Context, id string) (*service.CustomPlatform, error) {
	return r.platforms[id], nil
}

func (r *stubCustomPlatformRepo) Exists(_ context.Context, id string) (bool, error) {
	_, ok := r.platforms[id]
	return ok, nil
}

func (r *stubCustomPlatformRepo) Create(_ context.Context, platform *service.CustomPlatform) (*service.CustomPlatform, error) {
	r.platforms[platform.ID] = platform
	return platform, nil
}

func (r *stubCustomPlatformRepo) Update(_ context.Context, platform *service.CustomPlatform) (*service.CustomPlatform, error) {
	r.platforms[platform.ID] = platform
	return platform, nil
}

func (r *stubCustomPlatformRepo) Delete(_ context.Context, id string) error {
	delete(r.platforms, id)
	return nil
}

func (r *stubCustomPlatformRepo) CountUsage(context.Context, string) (service.CustomPlatformUsageCounts, error) {
	return r.usage, nil
}

func newCustomPlatformTestServer(t *testing.T) (*gin.Engine, *stubCustomPlatformRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Cleanup(domain.ResetDynamicPlatforms)
	t.Cleanup(service.ResetDynamicProviderProfiles)
	repo := &stubCustomPlatformRepo{platforms: map[string]*service.CustomPlatform{}}
	pricingSvc := service.NewPricingService(&config.Config{}, nil)
	_ = pricingSvc.Initialize()
	t.Cleanup(pricingSvc.Stop)
	svc := service.NewCustomPlatformService(repo, nil, nil)
	t.Cleanup(svc.Close)
	handler := NewCustomPlatformHandler(svc, pricingSvc)
	r := gin.New()
	g := r.Group("/api/v1/admin")
	g.GET("/custom-platforms", handler.List)
	g.POST("/custom-platforms", handler.Create)
	g.PUT("/custom-platforms/:id", handler.Update)
	g.DELETE("/custom-platforms/:id", handler.Delete)
	g.GET("/custom-platforms/litellm-providers", handler.ListLiteLLMProviders)
	return r, repo
}

func validCustomPlatformPayload(id string) map[string]any {
	return map[string]any{
		"id":                  id,
		"display_name":        "SiliconFlow",
		"enabled":             true,
		"color_preset":        "violet",
		"supports_embeddings": true,
		"icon":                map[string]any{"type": "letter", "value": "S"},
		"multi_protocol": map[string]any{
			"default_mode": "default",
			"routing":      "by_inbound",
			"modes": []map[string]any{{
				"mode":  "default",
				"label": "默认端点",
				"base_urls": map[string]any{
					"chat_completions": "https://api.siliconflow.cn/v1",
					"anthropic":        "https://api.siliconflow.cn/anthropic",
				},
			}},
		},
	}
}

func performJSON(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, req)
	return recorder
}

func TestCustomPlatformHandlerCreateAndList(t *testing.T) {
	r, _ := newCustomPlatformTestServer(t)

	recorder := performJSON(r, http.MethodPost, "/api/v1/admin/custom-platforms", validCustomPlatformPayload("siliconflow"))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/admin/custom-platforms", nil))
	require.Equal(t, http.StatusOK, list.Code)
	var payload struct {
		Data struct {
			Builtin []domain.PlatformSpec    `json:"builtin_platforms"`
			Custom  []service.CustomPlatform `json:"custom_platforms"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &payload))
	require.NotEmpty(t, payload.Data.Builtin)
	require.Len(t, payload.Data.Custom, 1)
	require.Equal(t, "siliconflow", payload.Data.Custom[0].ID)
}

func TestCustomPlatformHandlerRejectsInvalidPayload(t *testing.T) {
	r, _ := newCustomPlatformTestServer(t)

	badID := validCustomPlatformPayload("SiliconFlow")
	recorder := performJSON(r, http.MethodPost, "/api/v1/admin/custom-platforms", badID)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var payload struct {
		Reason string `json:"reason"`
		Data   struct {
			Field string `json:"field"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "INVALID_PLATFORM", payload.Reason)
	require.Equal(t, "id", payload.Data.Field)

	missingName := validCustomPlatformPayload("siliconflow")
	delete(missingName, "display_name")
	recorder = performJSON(r, http.MethodPost, "/api/v1/admin/custom-platforms", missingName)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestCustomPlatformHandlerDeleteConflict(t *testing.T) {
	r, repo := newCustomPlatformTestServer(t)
	recorder := performJSON(r, http.MethodPost, "/api/v1/admin/custom-platforms", validCustomPlatformPayload("siliconflow"))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	repo.usage = service.CustomPlatformUsageCounts{Accounts: 2, Groups: 1}
	recorder = performJSON(r, http.MethodDelete, "/api/v1/admin/custom-platforms/siliconflow", nil)
	require.Equal(t, http.StatusConflict, recorder.Code)
	var payload struct {
		Reason string `json:"reason"`
		Data   struct {
			UsageCounts service.CustomPlatformUsageCounts `json:"usage_counts"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "PLATFORM_IN_USE", payload.Reason)
	require.Equal(t, int64(2), payload.Data.UsageCounts.Accounts)
	require.Equal(t, int64(1), payload.Data.UsageCounts.Groups)

	repo.usage = service.CustomPlatformUsageCounts{}
	recorder = performJSON(r, http.MethodDelete, "/api/v1/admin/custom-platforms/siliconflow", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestCustomPlatformHandlerListLiteLLMProvidersAndSanitizedErrors(t *testing.T) {
	r, repo := newCustomPlatformTestServer(t)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/custom-platforms/litellm-providers", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var providersResp struct {
		Data struct {
			Providers []string `json:"providers"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &providersResp))
	require.Contains(t, providersResp.Data.Providers, "openai")
	require.Contains(t, providersResp.Data.Providers, "anthropic")
	require.Greater(t, len(providersResp.Data.Providers), 10, "should include providers from pricing catalog, not just builtin platforms")

	// Internal DB errors must not leak raw driver details to HTTP clients.
	repo.listErr = errors.New("pq: password authentication failed for user secret_user")
	errRec := httptest.NewRecorder()
	r.ServeHTTP(errRec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/custom-platforms", nil))
	require.Equal(t, http.StatusInternalServerError, errRec.Code)
	require.NotContains(t, errRec.Body.String(), "secret_user")
	require.NotContains(t, errRec.Body.String(), "pq:")
}

func TestDisabledCustomPlatformBlockedOnAccountAndGroupCreate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(domain.ResetDynamicPlatforms)
	require.NoError(t, domain.SetDynamicPlatforms([]domain.PlatformSpec{{
		ID:          "disabled_vendor",
		DisplayName: "Disabled Vendor",
		Gateway:     domain.PlatformGatewayOpenAI,
		Custom:      true,
		Disabled:    true,
	}}))

	r := gin.New()
	acctHandler := NewAccountHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	grpHandler := NewGroupHandler(nil, nil, nil)
	r.POST("/accounts", acctHandler.Create)
	r.POST("/groups", grpHandler.Create)
	r.POST("/groups/:id/composite-routes", grpHandler.CreateCompositeRoute)

	rec := performJSON(r, http.MethodPost, "/accounts", map[string]any{
		"name":        "acc1",
		"platform":    "disabled_vendor",
		"type":        "apikey",
		"credentials": map[string]any{"api_key": "sk-test"},
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "disabled")

	rec = performJSON(r, http.MethodPost, "/groups", map[string]any{
		"name":     "grp1",
		"platform": "disabled_vendor",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "disabled")

	rec = performJSON(r, http.MethodPost, "/groups/1/composite-routes", map[string]any{
		"public_model":    "my-model",
		"target_platform": "disabled_vendor",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "disabled")
}
