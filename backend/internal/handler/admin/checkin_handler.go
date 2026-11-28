package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// CheckinHandler 处理管理端每日签到配置、统计与流水查询。
type CheckinHandler struct {
	checkinService *service.CheckinService
}

// NewCheckinHandler 创建管理端签到处理器。
func NewCheckinHandler(checkinService *service.CheckinService) *CheckinHandler {
	return &CheckinHandler{checkinService: checkinService}
}

// GetConfig 返回当前签到规则配置。
// GET /api/v1/admin/checkin/config
func (h *CheckinHandler) GetConfig(c *gin.Context) {
	settings, err := h.checkinService.AdminGetConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// UpdateConfig 保存签到规则配置。
// PUT /api/v1/admin/checkin/config
func (h *CheckinHandler) UpdateConfig(c *gin.Context) {
	var req service.CheckinSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}

	settings, err := h.checkinService.AdminUpdateConfig(c.Request.Context(), &req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// GetStats 返回签到统计概览。
// GET /api/v1/admin/checkin/stats?days=30
func (h *CheckinHandler) GetStats(c *gin.Context) {
	days := 30
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			days = parsed
		}
	}

	stats, err := h.checkinService.AdminGetStats(c.Request.Context(), days)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, stats)
}

// ListRecords 分页返回全站签到流水。
// GET /api/v1/admin/checkin/records
func (h *CheckinHandler) ListRecords(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)

	filter := service.AdminCheckinRecordFilter{
		Search:    strings.TrimSpace(c.Query("search")),
		ClientIP:  strings.TrimSpace(c.Query("client_ip")),
		StartDate: strings.TrimSpace(c.Query("start_date")),
		EndDate:   strings.TrimSpace(c.Query("end_date")),
		Params: pagination.PaginationParams{
			Page:     page,
			PageSize: pageSize,
		},
	}
	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			filter.UserID = parsed
		}
	}
	if len(filter.Search) > 200 {
		filter.Search = filter.Search[:200]
	}

	records, result, err := h.checkinService.AdminListRecords(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.PaginatedWithResult(c, records, &response.PaginationResult{
		Total:    result.Total,
		Page:     result.Page,
		PageSize: result.PageSize,
		Pages:    result.Pages,
	})
}
