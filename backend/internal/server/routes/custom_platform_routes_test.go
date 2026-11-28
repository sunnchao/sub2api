package routes

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCustomPlatformAdminRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	next := func(c *gin.Context) { c.Next() }

	svc := service.NewCustomPlatformService(nil, nil, nil)
	t.Cleanup(svc.Close)

	RegisterAdminRoutes(
		r.Group("/api/v1"),
		&handler.Handlers{Admin: &handler.AdminHandlers{
			CustomPlatform: adminhandler.NewCustomPlatformHandler(svc, nil),
		}},
		middleware.AdminAuthMiddleware(next),
		middleware.AuditLogMiddleware(next),
		middleware.StepUpAuthMiddleware(next),
		nil,
		nil,
	)

	registered := make(map[string]struct{}, len(r.Routes()))
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = struct{}{}
	}
	for _, want := range []string{
		"GET /api/v1/admin/custom-platforms",
		"GET /api/v1/admin/custom-platforms/litellm-providers",
		"POST /api/v1/admin/custom-platforms",
		"PUT /api/v1/admin/custom-platforms/:id",
		"DELETE /api/v1/admin/custom-platforms/:id",
	} {
		_, ok := registered[want]
		require.True(t, ok, "missing route %s", want)
	}
}

func TestCustomPlatformGatewayEmbeddingsAndImagesGating(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(domain.ResetDynamicPlatforms)
	t.Cleanup(service.ResetDynamicProviderProfiles)

	require.NoError(t, domain.SetDynamicPlatforms([]domain.PlatformSpec{
		{
			ID:                 "chat_only_vendor",
			DisplayName:        "Chat Only",
			Gateway:            domain.PlatformGatewayOpenAI,
			Custom:             true,
			SupportsEmbeddings: false,
			SupportsImages:     false,
		},
		{
			ID:                 "media_vendor",
			DisplayName:        "Media Vendor",
			Gateway:            domain.PlatformGatewayOpenAI,
			Custom:             true,
			SupportsEmbeddings: true,
			SupportsImages:     true,
		},
	}))

	newGatewayRouter := func(platform string) *gin.Engine {
		r := gin.New()
		groupID := int64(10)
		apiKey := &service.APIKey{
			ID:      1,
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: platform},
		}
		authMW := middleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyAPIKey), apiKey)
			c.Next()
		})
		RegisterGatewayRoutes(
			r,
			&handler.Handlers{
				Gateway:       &handler.GatewayHandler{},
				OpenAIGateway: &handler.OpenAIGatewayHandler{},
				AsyncImage:    &handler.AsyncImageHandler{},
			},
			authMW,
			nil,
			nil,
			nil,
			nil,
			nil,
			&config.Config{
				Gateway: config.GatewayConfig{
					MaxBodySize:     1 << 20,
					TextMaxBodySize: 1 << 20,
				},
			},
		)
		return r
	}

	// Disabled capabilities return 404 from gateway route gates.
	chatOnlyRouter := newGatewayRouter("chat_only_vendor")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewReader([]byte(`{"model":"bge-m3","input":"hi"}`)))
	req.Header.Set("Content-Type", "application/json")
	chatOnlyRouter.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "Embeddings API is not supported")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader([]byte(`{"model":"flux-schnell","prompt":"cat"}`)))
	req.Header.Set("Content-Type", "application/json")
	chatOnlyRouter.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "Images API is not supported")

	// Enabled capabilities bypass the 404 route gate and reach OpenAIGatewayHandler.
	mediaRouter := newGatewayRouter("media_vendor")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewReader([]byte(`{"model":"bge-m3","input":"hi"}`)))
	req.Header.Set("Content-Type", "application/json")
	mediaRouter.ServeHTTP(rec, req)
	require.NotEqual(t, http.StatusNotFound, rec.Code, "enabled embeddings should pass the 404 platform gate")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader([]byte(`{"model":"flux-schnell","prompt":"cat"}`)))
	req.Header.Set("Content-Type", "application/json")
	mediaRouter.ServeHTTP(rec, req)
	require.NotEqual(t, http.StatusNotFound, rec.Code, "enabled images should pass the 404 platform gate")
}
