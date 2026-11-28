package routes

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestCheckinRoutesRegistered 验证签到用户端/管理端路由已挂载，
// 且不提供补签（retroactive）等未纳入需求的接口。
func TestCheckinRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	next := func(c *gin.Context) { c.Next() }

	RegisterUserRoutes(
		r.Group("/api/v1"),
		&handler.Handlers{Checkin: handler.NewCheckinHandler(nil)},
		middleware.JWTAuthMiddleware(next),
		middleware.AuditLogMiddleware(next),
		nil,
		nil,
	)
	RegisterAdminRoutes(
		r.Group("/api/v1"),
		&handler.Handlers{Admin: &handler.AdminHandlers{Checkin: adminhandler.NewCheckinHandler(nil)}},
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
		"GET /api/v1/checkin/status",
		"POST /api/v1/checkin",
		"GET /api/v1/checkin/calendar",
		"GET /api/v1/checkin/records",
		"GET /api/v1/checkin/lots",
		"GET /api/v1/admin/checkin/config",
		"PUT /api/v1/admin/checkin/config",
		"GET /api/v1/admin/checkin/stats",
		"GET /api/v1/admin/checkin/records",
	} {
		_, ok := registered[want]
		require.True(t, ok, "missing route %s", want)
	}

	// 需求明确不支持补签，接口不得存在。
	for route := range registered {
		require.NotContains(t, route, "/checkin/retroactive")
	}
}
