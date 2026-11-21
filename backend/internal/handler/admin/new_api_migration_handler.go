package admin

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// NewAPIMigrationHandler 处理管理员对 New API 额度迁移的配置、连通性测试与流水审计接口。
type NewAPIMigrationHandler struct {
	migrationService *service.NewAPIMigrationService
}

// NewNewAPIMigrationHandler 创建管理端 NewAPIMigrationHandler。
func NewNewAPIMigrationHandler(migrationService *service.NewAPIMigrationService) *NewAPIMigrationHandler {
	return &NewAPIMigrationHandler{
		migrationService: migrationService,
	}
}

// GetConfig 获取 New API 额度迁移配置（管理员访问令牌已脱敏）。
// GET /api/v1/admin/new-api-migration/config
func (h *NewAPIMigrationHandler) GetConfig(c *gin.Context) {
	cfg, err := h.migrationService.GetAdminConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

// UpdateConfig 更新 New API 额度迁移配置。
// PUT /api/v1/admin/new-api-migration/config
func (h *NewAPIMigrationHandler) UpdateConfig(c *gin.Context) {
	var req service.NewAPIMigrationConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	updated, err := h.migrationService.UpdateAdminConfig(c.Request.Context(), &req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, updated)
}

// TestConnection 测试 New API 站点连通性与管理员访问令牌权限。
// POST /api/v1/admin/new-api-migration/test-connection
func (h *NewAPIMigrationHandler) TestConnection(c *gin.Context) {
	var req service.NewAPIMigrationTestConnectionInput
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.BadRequest(c, "Invalid request: "+err.Error())
			return
		}
	}
	result, err := h.migrationService.TestConnection(c.Request.Context(), &req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// ListRecords 分页查询全站 New API 额度迁移流水。
// GET /api/v1/admin/new-api-migration/records
func (h *NewAPIMigrationHandler) ListRecords(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	if pageSize > 100 {
		pageSize = 100
	}
	filters := service.NewAPIMigrationRecordFilters{
		Status:    strings.TrimSpace(c.Query("status")),
		Direction: strings.TrimSpace(c.Query("direction")),
		Search:    strings.TrimSpace(c.Query("search")),
	}
	items, result, err := h.migrationService.ListAdminRecords(
		c.Request.Context(),
		pagination.PaginationParams{Page: page, PageSize: pageSize},
		filters,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, result.Total, page, pageSize)
}

// GetStats 获取全站 New API 额度迁移汇总统计。
// GET /api/v1/admin/new-api-migration/stats
func (h *NewAPIMigrationHandler) GetStats(c *gin.Context) {
	stats, err := h.migrationService.GetAdminStats(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, stats)
}
