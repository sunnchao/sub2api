package admin

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type nullableFloat64Field struct {
	Set   bool
	Value *float64
}

func (f *nullableFloat64Field) UnmarshalJSON(data []byte) error {
	f.Set = true
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		f.Value = nil
		return nil
	}
	var v float64
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return err
	}
	f.Value = &v
	return nil
}

// CreateSubscriptionTemplateRequest represents the create template payload.
type CreateSubscriptionTemplateRequest struct {
	Name                string   `json:"name" binding:"required"`
	Description         string   `json:"description"`
	GroupID             int64    `json:"group_id" binding:"required,gt=0"`
	DailyLimitUSD       *float64 `json:"daily_limit_usd"`
	WeeklyLimitUSD      *float64 `json:"weekly_limit_usd"`
	MonthlyLimitUSD     *float64 `json:"monthly_limit_usd"`
	HourlyLimitUSD      *float64 `json:"hourly_limit_usd"`
	HourlyWindowHours   int      `json:"hourly_window_hours"`
	DefaultValidityDays int      `json:"default_validity_days"`
	Status              string   `json:"status"`
	SortOrder           int      `json:"sort_order"`
}

// UpdateSubscriptionTemplateRequest supports explicit null clearing for limit fields.
type UpdateSubscriptionTemplateRequest struct {
	Name                *string              `json:"name"`
	Description         *string              `json:"description"`
	GroupID             *int64               `json:"group_id"`
	DailyLimitUSD       nullableFloat64Field `json:"daily_limit_usd"`
	WeeklyLimitUSD      nullableFloat64Field `json:"weekly_limit_usd"`
	MonthlyLimitUSD     nullableFloat64Field `json:"monthly_limit_usd"`
	HourlyLimitUSD      nullableFloat64Field `json:"hourly_limit_usd"`
	HourlyWindowHours   *int                 `json:"hourly_window_hours"`
	DefaultValidityDays *int                 `json:"default_validity_days"`
	Status              *string              `json:"status"`
	SortOrder           *int                 `json:"sort_order"`
}

// UpdateSubscriptionLimitsRequest adjusts snapshotted quota limits on a single user subscription.
type UpdateSubscriptionLimitsRequest struct {
	DailyLimitUSD     *float64 `json:"daily_limit_usd"`
	WeeklyLimitUSD    *float64 `json:"weekly_limit_usd"`
	MonthlyLimitUSD   *float64 `json:"monthly_limit_usd"`
	HourlyLimitUSD    *float64 `json:"hourly_limit_usd"`
	HourlyWindowHours int      `json:"hourly_window_hours"`
}

// ListTemplates handles GET /api/v1/admin/subscription-templates.
func (h *SubscriptionHandler) ListTemplates(c *gin.Context) {
	var filter service.SubscriptionTemplateListFilter
	if groupIDStr := c.Query("group_id"); groupIDStr != "" {
		if id, err := strconv.ParseInt(groupIDStr, 10, 64); err == nil && id > 0 {
			filter.GroupID = &id
		}
	}
	filter.Status = c.Query("status")
	filter.Platform = c.Query("platform")
	filter.Search = c.Query("search")

	templates, err := h.subscriptionService.ListTemplates(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]dto.SubscriptionTemplate, 0, len(templates))
	for i := range templates {
		out = append(out, *dto.SubscriptionTemplateFromService(&templates[i]))
	}
	response.Success(c, out)
}

// GetTemplate handles GET /api/v1/admin/subscription-templates/:id.
func (h *SubscriptionHandler) GetTemplate(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid subscription template ID")
		return
	}
	tpl, err := h.subscriptionService.GetTemplate(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.SubscriptionTemplateFromService(tpl))
}

// CreateTemplate handles POST /api/v1/admin/subscription-templates.
func (h *SubscriptionHandler) CreateTemplate(c *gin.Context) {
	var req CreateSubscriptionTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	tpl, err := h.subscriptionService.CreateTemplate(c.Request.Context(), &service.CreateSubscriptionTemplateInput{
		Name:                req.Name,
		Description:         req.Description,
		GroupID:             req.GroupID,
		DailyLimitUSD:       req.DailyLimitUSD,
		WeeklyLimitUSD:      req.WeeklyLimitUSD,
		MonthlyLimitUSD:     req.MonthlyLimitUSD,
		HourlyLimitUSD:      req.HourlyLimitUSD,
		HourlyWindowHours:   req.HourlyWindowHours,
		DefaultValidityDays: req.DefaultValidityDays,
		Status:              req.Status,
		SortOrder:           req.SortOrder,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, dto.SubscriptionTemplateFromService(tpl))
}

// UpdateTemplate handles PUT /api/v1/admin/subscription-templates/:id.
func (h *SubscriptionHandler) UpdateTemplate(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid subscription template ID")
		return
	}
	var req UpdateSubscriptionTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	tpl, err := h.subscriptionService.UpdateTemplate(c.Request.Context(), id, &service.UpdateSubscriptionTemplateInput{
		Name:        req.Name,
		Description: req.Description,
		GroupID:     req.GroupID,
		DailyLimitUSD: service.OptionalFloat64Patch{
			Set:   req.DailyLimitUSD.Set,
			Value: req.DailyLimitUSD.Value,
		},
		WeeklyLimitUSD: service.OptionalFloat64Patch{
			Set:   req.WeeklyLimitUSD.Set,
			Value: req.WeeklyLimitUSD.Value,
		},
		MonthlyLimitUSD: service.OptionalFloat64Patch{
			Set:   req.MonthlyLimitUSD.Set,
			Value: req.MonthlyLimitUSD.Value,
		},
		HourlyLimitUSD: service.OptionalFloat64Patch{
			Set:   req.HourlyLimitUSD.Set,
			Value: req.HourlyLimitUSD.Value,
		},
		HourlyWindowHours:   req.HourlyWindowHours,
		DefaultValidityDays: req.DefaultValidityDays,
		Status:              req.Status,
		SortOrder:           req.SortOrder,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.SubscriptionTemplateFromService(tpl))
}

// DeleteTemplate handles DELETE /api/v1/admin/subscription-templates/:id.
func (h *SubscriptionHandler) DeleteTemplate(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid subscription template ID")
		return
	}
	if err := h.subscriptionService.DeleteTemplate(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Subscription template deleted successfully"})
}

// UpdateLimits handles PUT /api/v1/admin/subscriptions/:id/limits.
func (h *SubscriptionHandler) UpdateLimits(c *gin.Context) {
	subscriptionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || subscriptionID <= 0 {
		response.BadRequest(c, "Invalid subscription ID")
		return
	}
	var req UpdateSubscriptionLimitsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	updated, err := h.subscriptionService.AdminUpdateSubscriptionLimits(c.Request.Context(), subscriptionID, service.SubscriptionLimits{
		DailyLimitUSD:     req.DailyLimitUSD,
		WeeklyLimitUSD:    req.WeeklyLimitUSD,
		MonthlyLimitUSD:   req.MonthlyLimitUSD,
		HourlyLimitUSD:    req.HourlyLimitUSD,
		HourlyWindowHours: req.HourlyWindowHours,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, adminSubscriptionResponse(c, dto.UserSubscriptionFromServiceAdmin(updated)))
}
