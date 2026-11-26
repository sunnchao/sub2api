package middleware

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ContextKeyGroupCandidates stores the ordered list of auth-eligible GroupCandidate
// entries when an API key binds multiple groups.
const ContextKeyGroupCandidates ContextKey = "api_key_group_candidates"

// GroupCandidate pairs an ordered bound group with its validated active
// subscription (nil for standard/balance groups or skip-billing paths).
type GroupCandidate struct {
	Group        *service.Group
	Subscription *service.UserSubscription
}

type multiGroupAuthError struct {
	Status       int
	Code         string
	Message      string
	Ingress      IngressRejectReason
	OpsReason    string
	HasIngress   bool
	HasOpsReason bool
}

// GetGroupCandidatesFromContext returns the ordered group candidates prepared
// by auth middleware for multi-group API keys.
func GetGroupCandidatesFromContext(c *gin.Context) ([]GroupCandidate, bool) {
	if c == nil {
		return nil, false
	}
	val, exists := c.Get(string(ContextKeyGroupCandidates))
	if !exists {
		return nil, false
	}
	candidates, ok := val.([]GroupCandidate)
	return candidates, ok
}

// HasMultipleGroupCandidates reports whether this request has >1 group candidates.
func HasMultipleGroupCandidates(c *gin.Context) bool {
	candidates, ok := GetGroupCandidatesFromContext(c)
	return ok && len(candidates) > 1
}

const deferredGroupAdmissionKey = "deferred_group_admission"

func DeferGroupAdmission(c *gin.Context) {
	if HasMultipleGroupCandidates(c) {
		c.Set(deferredGroupAdmissionKey, true)
	}
	c.Next()
}

func GroupAdmissionDeferred(c *gin.Context) bool {
	return c.GetBool(deferredGroupAdmissionKey)
}

type GroupRetryDecision uint8

const (
	GroupRetryDefault GroupRetryDecision = iota
	GroupRetryStop
	GroupRetryNext
	GroupRetryNextSubscription
	GroupRetrySubscriptionOnly
)

const groupRetryDecisionKey = "group_retry_decision"

func SetGroupRetryDecision(c *gin.Context, decision GroupRetryDecision) {
	if c != nil && HasMultipleGroupCandidates(c) {
		c.Set(groupRetryDecisionKey, decision)
	}
}

func GetGroupRetryDecision(c *gin.Context) GroupRetryDecision {
	value, _ := c.Get(groupRetryDecisionKey)
	decision, _ := value.(GroupRetryDecision)
	return decision
}

// SetActiveGroupCandidate switches gin.Context and request.Context to the given
// candidate group without mutating baseAPIKey or the candidate slice.
func SetActiveGroupCandidate(c *gin.Context, baseAPIKey *service.APIKey, cand GroupCandidate) {
	if c == nil || baseAPIKey == nil || cand.Group == nil {
		return
	}
	activeKey := baseAPIKey.CloneWithGroup(cand.Group)
	c.Set(string(ContextKeyAPIKey), activeKey)
	SetOpsFallbackAPIKey(c, activeKey)
	if cand.Subscription != nil {
		c.Set(string(ContextKeySubscription), cand.Subscription)
	} else {
		delete(c.Keys, string(ContextKeySubscription))
	}

	if c.Request != nil {
		ctx := context.WithValue(c.Request.Context(), ctxkey.Group, cand.Group)
		if activeKey.User != nil {
			ctx = service.WithHeaderOverrideRequestScope(ctx, activeKey.User.ID, activeKey.ID, activeKey.GroupID)
		}
		c.Request = c.Request.WithContext(ctx)
	}
}

func handleMultiGroupAuth(
	c *gin.Context,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	cfg *config.Config,
	apiKey *service.APIKey,
	skipBilling bool,
	billingInfoRequest bool,
) {
	if !skipBilling && (cfg == nil || cfg.RunMode != config.RunModeSimple) {
		switch apiKey.Status {
		case service.StatusAPIKeyQuotaExhausted:
			abortWithAPIKeyQuotaError(c)
			return
		case service.StatusAPIKeyExpired:
			AbortWithError(c, http.StatusForbidden, "API_KEY_EXPIRED", "API key 已过期")
			return
		}
		if apiKey.IsExpired() {
			AbortWithError(c, http.StatusForbidden, "API_KEY_EXPIRED", "API key 已过期")
			return
		}
		if apiKey.IsQuotaExhausted() {
			abortWithAPIKeyQuotaError(c)
			return
		}
	}

	candidates, activeSubs, mgErr := resolveMultiGroupCandidatesWithSubs(c.Request.Context(), apiKey, subscriptionService, cfg, skipBilling, billingInfoRequest)
	if mgErr != nil {
		if mgErr.HasOpsReason {
			service.MarkOpsClientBusinessLimited(c, mgErr.OpsReason)
		}
		if mgErr.HasIngress {
			MarkIngressRejected(c, mgErr.Ingress)
		}
		AbortWithError(c, mgErr.Status, mgErr.Code, mgErr.Message)
		return
	}

	ctx := context.WithValue(c.Request.Context(), ctxkey.UserID, apiKey.User.ID)
	c.Request = c.Request.WithContext(ctx)

	if len(activeSubs) > 0 {
		c.Set(string(ContextKeyActiveSubscriptions), activeSubs)
	}
	c.Set(string(ContextKeyGroupCandidates), candidates)
	c.Set(string(ContextKeyUser), AuthSubject{
		UserID:      apiKey.User.ID,
		Concurrency: apiKey.User.Concurrency,
	})
	c.Set(string(ContextKeyUserRole), apiKey.User.Role)
	SetActiveGroupCandidate(c, apiKey, candidates[0])
	if !billingInfoRequest && apiKeyService != nil {
		_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
	}
	c.Next()
}

func handleMultiGroupGoogleAuth(
	c *gin.Context,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	cfg *config.Config,
	apiKey *service.APIKey,
) {
	simpleMode := cfg != nil && cfg.RunMode == config.RunModeSimple
	if !simpleMode {
		switch apiKey.Status {
		case service.StatusAPIKeyQuotaExhausted:
			abortWithGoogleError(c, http.StatusTooManyRequests, "API key 额度已用完")
			return
		case service.StatusAPIKeyExpired:
			abortWithGoogleError(c, http.StatusForbidden, "API key 已过期")
			return
		}
		if apiKey.IsExpired() {
			abortWithGoogleError(c, http.StatusForbidden, "API key 已过期")
			return
		}
		if apiKey.IsQuotaExhausted() {
			abortWithGoogleError(c, http.StatusTooManyRequests, "API key 额度已用完")
			return
		}
	}

	candidates, mgErr := resolveMultiGroupCandidates(c.Request.Context(), apiKey, subscriptionService, cfg, false, false)
	if mgErr != nil {
		if mgErr.HasOpsReason {
			service.MarkOpsClientBusinessLimited(c, mgErr.OpsReason)
		}
		if mgErr.HasIngress {
			MarkIngressRejected(c, mgErr.Ingress)
		}
		abortWithGoogleError(c, mgErr.Status, mgErr.Message)
		return
	}

	ctx := context.WithValue(c.Request.Context(), ctxkey.UserID, apiKey.User.ID)
	c.Request = c.Request.WithContext(ctx)

	c.Set(string(ContextKeyGroupCandidates), candidates)
	c.Set(string(ContextKeyUser), AuthSubject{
		UserID:      apiKey.User.ID,
		Concurrency: apiKey.User.Concurrency,
	})
	c.Set(string(ContextKeyUserRole), apiKey.User.Role)
	SetActiveGroupCandidate(c, apiKey, candidates[0])
	if apiKeyService != nil {
		_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
	}
	c.Next()
}

func resolveMultiGroupCandidates(
	ctx context.Context,
	apiKey *service.APIKey,
	subscriptionService *service.SubscriptionService,
	cfg *config.Config,
	skipBilling bool,
	billingInfoRequest bool,
) ([]GroupCandidate, *multiGroupAuthError) {
	candidates, _, err := resolveMultiGroupCandidatesWithSubs(ctx, apiKey, subscriptionService, cfg, skipBilling, billingInfoRequest)
	return candidates, err
}

func resolveMultiGroupCandidatesWithSubs(
	ctx context.Context,
	apiKey *service.APIKey,
	subscriptionService *service.SubscriptionService,
	cfg *config.Config,
	skipBilling bool,
	billingInfoRequest bool,
) ([]GroupCandidate, []service.UserSubscription, *multiGroupAuthError) {
	boundGroups := apiKey.BoundGroups()
	groupByID := make(map[int64]*service.Group, len(boundGroups))
	for _, g := range boundGroups {
		if g != nil {
			groupByID[g.ID] = g
		}
	}

	orderedIDs := apiKey.BoundGroupIDs()
	if len(orderedIDs) == 0 && len(boundGroups) > 0 {
		orderedIDs = make([]int64, 0, len(boundGroups))
		for _, g := range boundGroups {
			if g != nil {
				orderedIDs = append(orderedIDs, g.ID)
			}
		}
	}

	var firstErr *multiGroupAuthError
	recordErr := func(err *multiGroupAuthError) {
		if firstErr == nil && err != nil {
			firstErr = err
		}
	}

	candidates := make([]GroupCandidate, 0, len(orderedIDs))
	var allActiveSubs []service.UserSubscription

	for _, gid := range orderedIDs {
		group := groupByID[gid]
		groupCands, groupSubs, groupErr := expandGroupCandidates(ctx, apiKey, group, subscriptionService, cfg, skipBilling, billingInfoRequest)
		if len(groupSubs) > 0 {
			allActiveSubs = append(allActiveSubs, groupSubs...)
		}
		if len(groupCands) > 0 {
			candidates = append(candidates, groupCands...)
		} else {
			recordErr(groupErr)
		}
	}

	if len(candidates) == 0 {
		if firstErr == nil {
			firstErr = &multiGroupAuthError{
				Status:       http.StatusForbidden,
				Code:         "GROUP_DELETED",
				Message:      "API Key 所属分组已删除",
				Ingress:      IngressRejectGroupDeleted,
				HasIngress:   true,
				OpsReason:    service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable,
				HasOpsReason: true,
			}
		}
		return nil, allActiveSubs, firstErr
	}
	return candidates, allActiveSubs, nil
}
