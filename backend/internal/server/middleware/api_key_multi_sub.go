package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ContextKeyActiveSubscriptions stores all active subscriptions (in FEFO order) loaded for
// the authenticated API key's group(s), used by /v1/usage multi-subscription aggregation.
const ContextKeyActiveSubscriptions ContextKey = "active_subscriptions"

// GetActiveSubscriptionsFromContext returns all active subscriptions stored in gin.Context.
func GetActiveSubscriptionsFromContext(c *gin.Context) ([]service.UserSubscription, bool) {
	if c == nil {
		return nil, false
	}
	val, exists := c.Get(string(ContextKeyActiveSubscriptions))
	if !exists {
		return nil, false
	}
	subs, ok := val.([]service.UserSubscription)
	return subs, ok && len(subs) > 0
}

func hasActiveSubscriptionForGroup(ctx context.Context, subscriptionService *service.SubscriptionService, userID, groupID int64) bool {
	if subscriptionService == nil || !subscriptionService.SupportsMultiSubscription() || userID <= 0 || groupID <= 0 {
		return false
	}
	subs, err := subscriptionService.ListActiveSubscriptions(ctx, userID, groupID)
	return err == nil && len(subs) > 0
}

// expandGroupCandidates expands a single bound group into one or more ordered billing candidates:
//   - Active subscriptions for (user, group) in FEFO order (expires_at ASC, created_at ASC, id ASC).
//   - For standard groups, appends a trailing balance candidate ({Group: group, Subscription: nil})
//     when balance meets the auth threshold (or skipBilling/simpleMode is active).
//   - For subscription-type groups, NEVER appends a balance fallback candidate when subscriptionService != nil.
func expandGroupCandidates(
	ctx context.Context,
	apiKey *service.APIKey,
	group *service.Group,
	subscriptionService *service.SubscriptionService,
	cfg *config.Config,
	skipBilling bool,
	billingInfoRequest bool,
) ([]GroupCandidate, []service.UserSubscription, *multiGroupAuthError) {
	if group == nil || strings.EqualFold(group.Status, "deleted") {
		return nil, nil, &multiGroupAuthError{
			Status:       http.StatusForbidden,
			Code:         "GROUP_DELETED",
			Message:      "API Key 所属分组已删除",
			Ingress:      IngressRejectGroupDeleted,
			HasIngress:   true,
			OpsReason:    service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable,
			HasOpsReason: true,
		}
	}
	if !group.IsActive() {
		return nil, nil, &multiGroupAuthError{
			Status:       http.StatusForbidden,
			Code:         "GROUP_DISABLED",
			Message:      "API Key 所属分组已停用",
			Ingress:      IngressRejectGroupDisabled,
			HasIngress:   true,
			OpsReason:    service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable,
			HasOpsReason: true,
		}
	}

	simpleMode := cfg != nil && cfg.RunMode == config.RunModeSimple

	var activeSubs []service.UserSubscription
	var subLookupErr error
	shouldQuerySubs := subscriptionService != nil && apiKey != nil && apiKey.User != nil && !simpleMode && !billingInfoRequest &&
		(group.IsSubscriptionType() || subscriptionService.SupportsMultiSubscription())
	if shouldQuerySubs {
		subs, err := subscriptionService.ListActiveSubscriptions(ctx, apiKey.User.ID, group.ID)
		if err != nil {
			subLookupErr = err
		} else if len(subs) > 0 {
			activeSubs = subs
		}
	}

	// Exclusive group authorization: active subscription automatically grants access even on
	// is_exclusive standard groups.
	if len(activeSubs) == 0 && !group.IsSubscriptionType() && apiKey != nil && apiKey.User != nil &&
		!apiKey.User.CanBindGroup(group.ID, group.IsExclusive) {
		return nil, nil, &multiGroupAuthError{
			Status:       http.StatusForbidden,
			Code:         "GROUP_NOT_ALLOWED",
			Message:      "API Key 所属专属分组不再允许当前用户使用",
			Ingress:      IngressRejectGroupNotAllowed,
			HasIngress:   true,
			OpsReason:    service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable,
			HasOpsReason: true,
		}
	}

	if simpleMode || skipBilling {
		cand := GroupCandidate{Group: group}
		if !simpleMode && !billingInfoRequest && len(activeSubs) > 0 {
			first := activeSubs[0]
			cand.Subscription = &first
		}
		return []GroupCandidate{cand}, activeSubs, nil
	}

	var (
		candidates []GroupCandidate
		firstErr   *multiGroupAuthError
	)
	recordErr := func(err *multiGroupAuthError) {
		if firstErr == nil && err != nil {
			firstErr = err
		}
	}

	if subscriptionService != nil && len(activeSubs) > 0 {
		for i := range activeSubs {
			sub := activeSubs[i]
			needsMaintenance, validateErr := subscriptionService.ValidateAndCheckLimits(&sub, group)
			if needsMaintenance {
				refreshed, maintenanceErr := subscriptionService.EnsureWindowMaintenance(ctx, &sub)
				if maintenanceErr != nil {
					recordErr(&multiGroupAuthError{
						Status:  http.StatusInternalServerError,
						Code:    "SUBSCRIPTION_MAINTENANCE_FAILED",
						Message: "Failed to maintain subscription usage windows",
					})
					continue
				}
				sub = *refreshed
				_, validateErr = subscriptionService.ValidateAndCheckLimits(&sub, group)
			}
			if validateErr != nil {
				code := "SUBSCRIPTION_INVALID"
				status := http.StatusForbidden
				if isSubscriptionQuotaLimitError(validateErr) {
					code = "USAGE_LIMIT_EXCEEDED"
					status = http.StatusTooManyRequests
				}
				recordErr(&multiGroupAuthError{
					Status:  status,
					Code:    code,
					Message: validateErr.Error(),
				})
				continue
			}
			subCopy := sub
			candidates = append(candidates, GroupCandidate{Group: group, Subscription: &subCopy})
		}
	}

	if group.IsSubscriptionType() {
		if subscriptionService == nil {
			// Legacy fallback when subscriptionService is not injected.
			if apiKey != nil && apiKey.User != nil && apiKeyBalanceBelowAuthThreshold(apiKey.User.Balance, cfg) {
				recordErr(&multiGroupAuthError{
					Status:  http.StatusForbidden,
					Code:    "INSUFFICIENT_BALANCE",
					Message: "Insufficient account balance",
				})
				return nil, activeSubs, firstErr
			}
			candidates = append(candidates, GroupCandidate{Group: group})
			return candidates, activeSubs, nil
		}
		if len(activeSubs) == 0 || subLookupErr != nil {
			recordErr(&multiGroupAuthError{
				Status:  http.StatusForbidden,
				Code:    "SUBSCRIPTION_NOT_FOUND",
				Message: "No active subscription found for this group",
			})
		}
		return candidates, activeSubs, firstErr
	}

	// Standard group: if balance meets threshold, append trailing balance fallback candidate.
	// If balance is insufficient and an active subscription already hit quota (429), firstErr
	// remains 429 USAGE_LIMIT_EXCEEDED rather than overwriting with 403 INSUFFICIENT_BALANCE.
	if apiKey != nil && apiKey.User != nil && apiKeyBalanceBelowAuthThreshold(apiKey.User.Balance, cfg) {
		recordErr(&multiGroupAuthError{
			Status:  http.StatusForbidden,
			Code:    "INSUFFICIENT_BALANCE",
			Message: "Insufficient account balance",
		})
	} else {
		candidates = append(candidates, GroupCandidate{Group: group})
	}

	return candidates, activeSubs, firstErr
}

func isSubscriptionQuotaLimitError(err error) bool {
	return errors.Is(err, service.ErrDailyLimitExceeded) ||
		errors.Is(err, service.ErrWeeklyLimitExceeded) ||
		errors.Is(err, service.ErrMonthlyLimitExceeded) ||
		errors.Is(err, service.ErrHourlyLimitExceeded)
}

func abortWithMultiGroupAuthError(c *gin.Context, mgErr *multiGroupAuthError, isGoogle bool) {
	if mgErr == nil {
		return
	}
	if mgErr.HasOpsReason {
		service.MarkOpsClientBusinessLimited(c, mgErr.OpsReason)
	}
	if mgErr.HasIngress {
		MarkIngressRejected(c, mgErr.Ingress)
	}
	if isGoogle {
		abortWithGoogleError(c, mgErr.Status, mgErr.Message)
		return
	}
	AbortWithError(c, mgErr.Status, mgErr.Code, mgErr.Message)
}
