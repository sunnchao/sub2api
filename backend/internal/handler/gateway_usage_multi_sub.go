package handler

import (
	"context"
	"net/http"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// usageUnrestrictedWithMultiSub builds the /v1/usage unrestricted response with full
// multi-subscription & snapshotted-limit support (including standard groups that have active
// template subscriptions). Backward-compatible fields planName, remaining, and subscription
// are preserved while adding subscriptions: [...].
func (h *GatewayHandler) usageUnrestrictedWithMultiSub(
	c *gin.Context,
	ctx context.Context,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	usageData gin.H,
	dailyUsage any,
	modelStats any,
) bool {
	if apiKey == nil || apiKey.Group == nil {
		return false
	}

	activeSubs, hasMulti := middleware2.GetActiveSubscriptionsFromContext(c)
	if !hasMulti {
		if single, ok := middleware2.GetSubscriptionFromContext(c); ok && single != nil {
			activeSubs = []service.UserSubscription{*single}
		}
	}

	isSubGroup := apiKey.Group.IsSubscriptionType()
	if !isSubGroup && len(activeSubs) == 0 {
		return false
	}

	planName := apiKey.Group.Name
	if len(activeSubs) == 1 && activeSubs[0].TemplateName != "" {
		planName = activeSubs[0].TemplateName
	}

	resp := gin.H{
		"mode":     "unrestricted",
		"isValid":  true,
		"planName": planName,
		"unit":     "USD",
	}

	if len(activeSubs) > 0 {
		subItems := make([]gin.H, 0, len(activeSubs))
		var totalRemaining float64
		hasUnlimited := false

		for i := range activeSubs {
			sub := &activeSubs[i]
			group := sub.Group
			if group == nil {
				group = apiKey.Group
			}
			limits := sub.EffectiveLimits(group)
			rem := h.calculateSubscriptionRemainingWithLimits(limits, sub)
			if rem < 0 {
				hasUnlimited = true
			} else {
				totalRemaining += rem
			}

			item := gin.H{
				"id":                  sub.ID,
				"group_id":            sub.GroupID,
				"template_id":         sub.TemplateID,
				"template_name":       sub.TemplateName,
				"daily_usage_usd":     sub.DailyUsageUSD,
				"weekly_usage_usd":    sub.WeeklyUsageUSD,
				"monthly_usage_usd":   sub.MonthlyUsageUSD,
				"hourly_usage_usd":    sub.HourlyUsageUSD,
				"daily_limit_usd":     limits.DailyLimitUSD,
				"weekly_limit_usd":    limits.WeeklyLimitUSD,
				"monthly_limit_usd":   limits.MonthlyLimitUSD,
				"hourly_limit_usd":    limits.HourlyLimitUSD,
				"hourly_window_hours": limits.NormalizedHourlyWindowHours(),
				"weekly_window_start": sub.WeeklyWindowStart,
				"hourly_window_start": sub.HourlyWindowStart,
				"expires_at":          sub.ExpiresAt,
				"remaining":           rem,
			}
			subItems = append(subItems, item)
		}

		if hasUnlimited {
			resp["remaining"] = float64(-1)
		} else {
			resp["remaining"] = totalRemaining
		}

		// Backward-compatible primary subscription payload (FEFO first).
		first := &activeSubs[0]
		firstGroup := first.Group
		if firstGroup == nil {
			firstGroup = apiKey.Group
		}
		firstLimits := first.EffectiveLimits(firstGroup)
		resp["subscription"] = gin.H{
			"id":                  first.ID,
			"template_id":         first.TemplateID,
			"template_name":       first.TemplateName,
			"daily_usage_usd":     first.DailyUsageUSD,
			"weekly_usage_usd":    first.WeeklyUsageUSD,
			"monthly_usage_usd":   first.MonthlyUsageUSD,
			"hourly_usage_usd":    first.HourlyUsageUSD,
			"daily_limit_usd":     firstLimits.DailyLimitUSD,
			"weekly_limit_usd":    firstLimits.WeeklyLimitUSD,
			"monthly_limit_usd":   firstLimits.MonthlyLimitUSD,
			"hourly_limit_usd":    firstLimits.HourlyLimitUSD,
			"hourly_window_hours": firstLimits.NormalizedHourlyWindowHours(),
			"weekly_window_start": first.WeeklyWindowStart,
			"hourly_window_start": first.HourlyWindowStart,
			"expires_at":          first.ExpiresAt,
		}
		resp["subscriptions"] = subItems
	}

	// For standard groups with active subscriptions, also expose current wallet balance for visibility.
	if !isSubGroup && h.userService != nil {
		if latestUser, err := h.userService.GetByID(ctx, subject.UserID); err == nil && latestUser != nil {
			for k, v := range h.walletBalanceFields(ctx, latestUser.ID, latestUser.Balance) {
				resp[k] = v
			}
		}
	}

	if usageData != nil {
		resp["usage"] = usageData
	}
	if dailyUsage != nil {
		resp["daily_usage"] = dailyUsage
	}
	if modelStats != nil {
		resp["model_stats"] = modelStats
	}
	c.JSON(http.StatusOK, resp)
	return true
}

func (h *GatewayHandler) calculateSubscriptionRemainingWithLimits(limits service.SubscriptionLimits, sub *service.UserSubscription) float64 {
	var remainingValues []float64

	if limits.HasDailyLimit() {
		remaining := *limits.DailyLimitUSD - sub.DailyUsageUSD
		if remaining <= 0 {
			return 0
		}
		remainingValues = append(remainingValues, remaining)
	}
	if limits.HasWeeklyLimit() {
		remaining := *limits.WeeklyLimitUSD - sub.WeeklyUsageUSD
		if remaining <= 0 {
			return 0
		}
		remainingValues = append(remainingValues, remaining)
	}
	if limits.HasMonthlyLimit() {
		remaining := *limits.MonthlyLimitUSD - sub.MonthlyUsageUSD
		if remaining <= 0 {
			return 0
		}
		remainingValues = append(remainingValues, remaining)
	}
	if limits.HasHourlyLimit() {
		remaining := *limits.HourlyLimitUSD - sub.HourlyUsageUSD
		if remaining <= 0 {
			return 0
		}
		remainingValues = append(remainingValues, remaining)
	}

	if len(remainingValues) == 0 {
		return -1
	}
	min := remainingValues[0]
	for _, v := range remainingValues[1:] {
		if v < min {
			min = v
		}
	}
	return min
}
