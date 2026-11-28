package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/wire"
)

// JWTAuthMiddleware JWT 认证中间件类型
type JWTAuthMiddleware gin.HandlerFunc

// OptionalJWTAuthMiddleware 可选 JWT 认证中间件类型：匿名放行，带 token 严格校验
type OptionalJWTAuthMiddleware gin.HandlerFunc

// AdminAuthMiddleware 管理员认证中间件类型
type AdminAuthMiddleware gin.HandlerFunc

// APIKeyAuthMiddleware API Key 认证中间件类型
type APIKeyAuthMiddleware gin.HandlerFunc

// ProvideAPIKeyAuthMiddleware constructs the API Key auth middleware and registers
// BillingCacheService as the global fallback balance reader (also used by Google/multi-sub auth).
func ProvideAPIKeyAuthMiddleware(
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	billingCacheService *service.BillingCacheService,
	cfg *config.Config,
) APIKeyAuthMiddleware {
	SetAPIKeyAuthBalanceReader(billingCacheService)
	return NewAPIKeyAuthMiddlewareWithBalanceReader(apiKeyService, subscriptionService, billingCacheService, cfg)
}

// ProviderSet 中间件层的依赖注入
var ProviderSet = wire.NewSet(
	NewJWTAuthMiddleware,
	NewOptionalJWTAuthMiddleware,
	NewAdminAuthMiddleware,
	ProvideAPIKeyAuthMiddleware,
	NewAuditLogMiddleware,
	NewStepUpAuthMiddleware,
)
