package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// NewAPIMigrationHandler 处理用户自助 New API 额度迁移请求。
type NewAPIMigrationHandler struct {
	migrationService *service.NewAPIMigrationService
}

// NewNewAPIMigrationHandler 创建用户侧 NewAPIMigrationHandler。
func NewNewAPIMigrationHandler(migrationService *service.NewAPIMigrationService) *NewAPIMigrationHandler {
	return &NewAPIMigrationHandler{
		migrationService: migrationService,
	}
}

type newAPIMigrationPreviewRequest struct {
	AccessToken string `json:"access_token" binding:"required"`
}

type newAPIMigrationExecuteRequest struct {
	AccessToken          string `json:"access_token" binding:"required"`
	ExpectedNewAPIUserID int64  `json:"expected_new_api_user_id,omitempty"`
}

// GetInfo 返回面向用户的 New API 迁移配置摘要（站点名、换算比例、功能开关）。
// GET /api/v1/new-api-migration/info
func (h *NewAPIMigrationHandler) GetInfo(c *gin.Context) {
	if _, ok := middleware2.GetAuthSubjectFromContext(c); !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	info, err := h.migrationService.GetUserInfo(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, info)
}

// Preview 使用用户提交的 New API 访问令牌查询可迁移余额与预览入账金额。
// POST /api/v1/new-api-migration/preview
func (h *NewAPIMigrationHandler) Preview(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req newAPIMigrationPreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	result, err := h.migrationService.PreviewFromNewAPI(c.Request.Context(), subject.UserID, req.AccessToken)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// Execute 执行一次性全额迁出 New API 余额并入账到当前登录用户。
// POST /api/v1/new-api-migration/execute
func (h *NewAPIMigrationHandler) Execute(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req newAPIMigrationExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	result, err := h.migrationService.ExecuteFromNewAPI(c.Request.Context(), subject.UserID, service.NewAPIMigrationExecuteInput{
		AccessToken:          req.AccessToken,
		ExpectedNewAPIUserID: req.ExpectedNewAPIUserID,
		ClientIP:             ip.GetClientIP(c),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// 用户侧响应隐藏客户端 IP 审计字段。
	if result != nil && result.Record != nil {
		sanitized := *result.Record
		sanitized.ClientIP = ""
		result = &service.NewAPIMigrationExecuteResult{
			Record:     &sanitized,
			NewBalance: result.NewBalance,
		}
	}
	response.Success(c, result)
}

// ListRecords 分页返回当前登录用户的迁移记录。
// GET /api/v1/new-api-migration/records
func (h *NewAPIMigrationHandler) ListRecords(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	page, pageSize := response.ParsePagination(c)
	if pageSize > 100 {
		pageSize = 100
	}

	items, result, err := h.migrationService.ListUserRecords(
		c.Request.Context(),
		subject.UserID,
		pagination.PaginationParams{Page: page, PageSize: pageSize},
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	for i := range items {
		items[i].ClientIP = ""
	}
	response.Paginated(c, items, result.Total, page, pageSize)
}
