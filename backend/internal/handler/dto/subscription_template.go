package dto

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SubscriptionTemplate is the admin DTO for reusable subscription templates.
type SubscriptionTemplate struct {
	ID                  int64     `json:"id"`
	Name                string    `json:"name"`
	Description         string    `json:"description"`
	GroupID             int64     `json:"group_id"`
	DailyLimitUSD       *float64  `json:"daily_limit_usd"`
	WeeklyLimitUSD      *float64  `json:"weekly_limit_usd"`
	MonthlyLimitUSD     *float64  `json:"monthly_limit_usd"`
	HourlyLimitUSD      *float64  `json:"hourly_limit_usd"`
	HourlyWindowHours   int       `json:"hourly_window_hours"`
	DefaultValidityDays int       `json:"default_validity_days"`
	Status              string    `json:"status"`
	SortOrder           int       `json:"sort_order"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`

	Group *Group `json:"group,omitempty"`
}

// SubscriptionTemplateFromService converts a service SubscriptionTemplate into a DTO.
func SubscriptionTemplateFromService(tpl *service.SubscriptionTemplate) *SubscriptionTemplate {
	if tpl == nil {
		return nil
	}
	return &SubscriptionTemplate{
		ID:                  tpl.ID,
		Name:                tpl.Name,
		Description:         tpl.Description,
		GroupID:             tpl.GroupID,
		DailyLimitUSD:       tpl.DailyLimitUSD,
		WeeklyLimitUSD:      tpl.WeeklyLimitUSD,
		MonthlyLimitUSD:     tpl.MonthlyLimitUSD,
		HourlyLimitUSD:      tpl.HourlyLimitUSD,
		HourlyWindowHours:   tpl.HourlyWindowHours,
		DefaultValidityDays: tpl.DefaultValidityDays,
		Status:              tpl.Status,
		SortOrder:           tpl.SortOrder,
		CreatedAt:           tpl.CreatedAt,
		UpdatedAt:           tpl.UpdatedAt,
		Group:               GroupFromServiceShallow(tpl.Group),
	}
}
