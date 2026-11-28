package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// CheckinHandler 处理用户端每日签到相关请求。
type CheckinHandler struct {
	checkinService *service.CheckinService
}

// NewCheckinHandler 创建签到处理器。
func NewCheckinHandler(checkinService *service.CheckinService) *CheckinHandler {
	return &CheckinHandler{checkinService: checkinService}
}

// GetStatus 返回签到概览、规则预览与资格判定。
// GET /api/v1/checkin/status
func (h *CheckinHandler) GetStatus(c *gin.Context) {
	userID, ok := checkinUserID(c)
	if !ok {
		return
	}
	status, err := h.checkinService.GetStatus(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

// Checkin 执行今日签到。
// POST /api/v1/checkin
func (h *CheckinHandler) Checkin(c *gin.Context) {
	userID, ok := checkinUserID(c)
	if !ok {
		return
	}

	// 请求体全部字段可选：未开启人机验证时前端会提交空对象。
	var proof service.CheckinCaptchaProof
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&proof); err != nil && !strings.Contains(err.Error(), "EOF") {
			response.BadRequest(c, "invalid request body")
			return
		}
	}

	result, err := h.checkinService.Checkin(c.Request.Context(), userID, ip.GetClientIP(c), proof)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// GetCalendar 返回指定月份的签到日历。
// GET /api/v1/checkin/calendar?month=YYYY-MM
func (h *CheckinHandler) GetCalendar(c *gin.Context) {
	userID, ok := checkinUserID(c)
	if !ok {
		return
	}
	calendar, err := h.checkinService.GetCalendar(c.Request.Context(), userID, c.Query("month"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, calendar)
}

// ListRecords 分页返回当前用户的签到历史。
// GET /api/v1/checkin/records
func (h *CheckinHandler) ListRecords(c *gin.Context) {
	userID, ok := checkinUserID(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	records, result, err := h.checkinService.ListRecords(c.Request.Context(), userID, pagination.PaginationParams{
		Page:     page,
		PageSize: pageSize,
	})
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

// ListLots 分页返回当前用户的限时余额批次明细。
// GET /api/v1/checkin/lots
func (h *CheckinHandler) ListLots(c *gin.Context) {
	userID, ok := checkinUserID(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	lots, result, err := h.checkinService.ListLots(c.Request.Context(), userID, c.Query("status"), pagination.PaginationParams{
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.PaginatedWithResult(c, lots, &response.PaginationResult{
		Total:    result.Total,
		Page:     result.Page,
		PageSize: result.PageSize,
		Pages:    result.Pages,
	})
}

// checkinUserID 从 JWT 上下文中取出用户 ID。
func checkinUserID(c *gin.Context) (int64, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return 0, false
	}
	return subject.UserID, true
}
