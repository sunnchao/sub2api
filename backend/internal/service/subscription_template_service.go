package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// OptionalFloat64Patch distinguishes between an omitted field and an explicit null/value update.
type OptionalFloat64Patch struct {
	Set   bool
	Value *float64
}

// CreateSubscriptionTemplateInput creates a new reusable subscription template bound to a group.
type CreateSubscriptionTemplateInput struct {
	Name                string
	Description         string
	GroupID             int64
	DailyLimitUSD       *float64
	WeeklyLimitUSD      *float64
	MonthlyLimitUSD     *float64
	HourlyLimitUSD      *float64
	HourlyWindowHours   int
	DefaultValidityDays int
	Status              string
	SortOrder           int
}

// UpdateSubscriptionTemplateInput updates an existing subscription template without mutating
// already-issued UserSubscription snapshots.
type UpdateSubscriptionTemplateInput struct {
	Name                *string
	Description         *string
	GroupID             *int64
	DailyLimitUSD       OptionalFloat64Patch
	WeeklyLimitUSD      OptionalFloat64Patch
	MonthlyLimitUSD     OptionalFloat64Patch
	HourlyLimitUSD      OptionalFloat64Patch
	HourlyWindowHours   *int
	DefaultValidityDays *int
	Status              *string
	SortOrder           *int
}

// AssignTemplateSubscriptionInput assigns (stacks) or extends a subscription using a template.
type AssignTemplateSubscriptionInput struct {
	UserID                 int64
	TemplateID             int64
	ValidityDays           int
	AssignedBy             int64
	Notes                  string
	Mode                   string // "stack" (default) or "extend"
	DeferCacheInvalidation bool
}

// SetTemplateRepository injects a custom SubscriptionTemplateRepository (primarily for tests).
func (s *SubscriptionService) SetTemplateRepository(repo SubscriptionTemplateRepository) {
	if s != nil {
		s.templateRepo = repo
	}
}

func (s *SubscriptionService) templateRepository() SubscriptionTemplateRepository {
	if s == nil {
		return nil
	}
	if s.templateRepo != nil {
		return s.templateRepo
	}
	return newEntSubscriptionTemplateStore(s.entClient)
}

func (s *SubscriptionService) multiSubRepository() UserSubscriptionMultiRepository {
	if s == nil || s.userSubRepo == nil {
		return nil
	}
	multi, _ := s.userSubRepo.(UserSubscriptionMultiRepository)
	return multi
}

// SupportsMultiSubscription reports whether the underlying repository supports multi-subscription
// queries (always true in production with userSubscriptionRepository).
func (s *SubscriptionService) SupportsMultiSubscription() bool {
	return s.multiSubRepository() != nil
}

// subListCacheKey builds the L1 cache key for active multi-subscriptions of a (user, group).
func subListCacheKey(userID, groupID int64) string {
	return "sublist:" + strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(groupID, 10)
}

func cloneUserSubscriptionSlice(in []UserSubscription) []UserSubscription {
	if len(in) == 0 {
		return []UserSubscription{}
	}
	out := make([]UserSubscription, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].TemplateID = cloneTemplateInt64Ptr(in[i].TemplateID)
		out[i].DailyLimitUSD = cloneFloat64Ptr(in[i].DailyLimitUSD)
		out[i].WeeklyLimitUSD = cloneFloat64Ptr(in[i].WeeklyLimitUSD)
		out[i].MonthlyLimitUSD = cloneFloat64Ptr(in[i].MonthlyLimitUSD)
		out[i].HourlyLimitUSD = cloneFloat64Ptr(in[i].HourlyLimitUSD)
	}
	return out
}

func (s *SubscriptionService) invalidateSubscriptionInstanceCaches(userID, groupID, subscriptionID int64) error {
	if err := s.invalidateSubscriptionCaches(userID, groupID); err != nil {
		return err
	}
	if subscriptionID > 0 && s.billingCacheService != nil {
		cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.billingCacheService.InvalidateSubscriptionInstance(cacheCtx, subscriptionID); err != nil {
			return fmt.Errorf("invalidate billing subscription instance cache: %w", err)
		}
	}
	return nil
}

func (s *SubscriptionService) maybeInvalidateInstanceCaches(userID, groupID, subscriptionID int64, deferred bool) {
	if deferred {
		return
	}
	s.InvalidateSubCache(userID, groupID)
	if s.billingCacheService != nil {
		go func() {
			cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.billingCacheService.InvalidateSubscription(cacheCtx, userID, groupID)
			if subscriptionID > 0 {
				_ = s.billingCacheService.InvalidateSubscriptionInstance(cacheCtx, subscriptionID)
			}
		}()
	}
}

func normalizeTemplateStatus(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	if status == "" {
		return SubscriptionTemplateStatusActive, nil
	}
	switch status {
	case SubscriptionTemplateStatusActive, SubscriptionTemplateStatusInactive:
		return status, nil
	default:
		return "", ErrSubscriptionTemplateInvalid
	}
}

func normalizeTemplateValidityDays(days int) (int, error) {
	if days == 0 {
		return 30, nil
	}
	if days < 1 || days > MaxValidityDays {
		return 0, infraerrors.BadRequest("SUBSCRIPTION_VALIDITY_INVALID", "default_validity_days must be between 1 and 36500")
	}
	return days, nil
}

func normalizeAssignMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		return SubscriptionAssignModeStack, nil
	}
	switch mode {
	case SubscriptionAssignModeStack, SubscriptionAssignModeExtend:
		return mode, nil
	default:
		return "", infraerrors.BadRequest("INVALID_ASSIGN_MODE", "mode must be 'stack' or 'extend'")
	}
}

// CreateTemplate creates a new subscription template bound to an active standard or subscription group.
func (s *SubscriptionService) CreateTemplate(ctx context.Context, input *CreateSubscriptionTemplateInput) (*SubscriptionTemplate, error) {
	if input == nil {
		return nil, ErrSubscriptionTemplateInvalid
	}
	repo := s.templateRepository()
	if repo == nil {
		return nil, ErrSubscriptionTemplateInvalid
	}

	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 120 {
		return nil, infraerrors.BadRequest("SUBSCRIPTION_TEMPLATE_NAME_INVALID", "template name is required and must be <= 120 characters")
	}
	if input.GroupID <= 0 {
		return nil, infraerrors.BadRequest("SUBSCRIPTION_TEMPLATE_GROUP_REQUIRED", "group_id is required")
	}

	group, err := s.groupRepo.GetByID(ctx, input.GroupID)
	if err != nil {
		return nil, fmt.Errorf("group not found: %w", err)
	}
	if !group.IsActive() {
		return nil, infraerrors.BadRequest("GROUP_INACTIVE", "target group is not active")
	}

	exists, err := repo.ExistsActiveByName(ctx, name, 0)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrSubscriptionTemplateExists
	}

	limits, err := ValidateSubscriptionLimits(SubscriptionLimits{
		DailyLimitUSD:     input.DailyLimitUSD,
		WeeklyLimitUSD:    input.WeeklyLimitUSD,
		MonthlyLimitUSD:   input.MonthlyLimitUSD,
		HourlyLimitUSD:    input.HourlyLimitUSD,
		HourlyWindowHours: input.HourlyWindowHours,
	})
	if err != nil {
		return nil, err
	}

	validityDays, err := normalizeTemplateValidityDays(input.DefaultValidityDays)
	if err != nil {
		return nil, err
	}

	status, err := normalizeTemplateStatus(input.Status)
	if err != nil {
		return nil, err
	}

	tpl := &SubscriptionTemplate{
		Name:                name,
		Description:         strings.TrimSpace(input.Description),
		GroupID:             input.GroupID,
		DailyLimitUSD:       limits.DailyLimitUSD,
		WeeklyLimitUSD:      limits.WeeklyLimitUSD,
		MonthlyLimitUSD:     limits.MonthlyLimitUSD,
		HourlyLimitUSD:      limits.HourlyLimitUSD,
		HourlyWindowHours:   limits.HourlyWindowHours,
		DefaultValidityDays: validityDays,
		Status:              status,
		SortOrder:           input.SortOrder,
		Group:               group,
	}

	if err := repo.Create(ctx, tpl); err != nil {
		return nil, err
	}
	tpl.Group = group
	return tpl, nil
}

// GetTemplate retrieves an active (non-deleted) subscription template by ID.
func (s *SubscriptionService) GetTemplate(ctx context.Context, id int64) (*SubscriptionTemplate, error) {
	repo := s.templateRepository()
	if repo == nil {
		return nil, ErrSubscriptionTemplateNotFound
	}
	return repo.GetByID(ctx, id)
}

// ListTemplates returns non-deleted subscription templates matching the filter.
func (s *SubscriptionService) ListTemplates(ctx context.Context, filter SubscriptionTemplateListFilter) ([]SubscriptionTemplate, error) {
	repo := s.templateRepository()
	if repo == nil {
		return []SubscriptionTemplate{}, nil
	}
	return repo.List(ctx, filter)
}

// UpdateTemplate modifies a template's configuration without mutating existing subscription snapshots.
func (s *SubscriptionService) UpdateTemplate(ctx context.Context, id int64, input *UpdateSubscriptionTemplateInput) (*SubscriptionTemplate, error) {
	if input == nil || id <= 0 {
		return nil, ErrSubscriptionTemplateInvalid
	}
	repo := s.templateRepository()
	if repo == nil {
		return nil, ErrSubscriptionTemplateInvalid
	}

	tpl, err := repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len([]rune(name)) > 120 {
			return nil, infraerrors.BadRequest("SUBSCRIPTION_TEMPLATE_NAME_INVALID", "template name is required and must be <= 120 characters")
		}
		exists, err := repo.ExistsActiveByName(ctx, name, id)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, ErrSubscriptionTemplateExists
		}
		tpl.Name = name
	}

	if input.Description != nil {
		tpl.Description = strings.TrimSpace(*input.Description)
	}

	boundGroup := tpl.Group
	if input.GroupID != nil && *input.GroupID != tpl.GroupID {
		if *input.GroupID <= 0 {
			return nil, infraerrors.BadRequest("SUBSCRIPTION_TEMPLATE_GROUP_REQUIRED", "group_id is required")
		}
		g, err := s.groupRepo.GetByID(ctx, *input.GroupID)
		if err != nil {
			return nil, fmt.Errorf("group not found: %w", err)
		}
		if !g.IsActive() {
			return nil, infraerrors.BadRequest("GROUP_INACTIVE", "target group is not active")
		}
		tpl.GroupID = *input.GroupID
		boundGroup = g
	}

	candidateLimits := SubscriptionLimits{
		DailyLimitUSD:     tpl.DailyLimitUSD,
		WeeklyLimitUSD:    tpl.WeeklyLimitUSD,
		MonthlyLimitUSD:   tpl.MonthlyLimitUSD,
		HourlyLimitUSD:    tpl.HourlyLimitUSD,
		HourlyWindowHours: tpl.HourlyWindowHours,
	}
	if input.DailyLimitUSD.Set {
		candidateLimits.DailyLimitUSD = input.DailyLimitUSD.Value
	}
	if input.WeeklyLimitUSD.Set {
		candidateLimits.WeeklyLimitUSD = input.WeeklyLimitUSD.Value
	}
	if input.MonthlyLimitUSD.Set {
		candidateLimits.MonthlyLimitUSD = input.MonthlyLimitUSD.Value
	}
	if input.HourlyLimitUSD.Set {
		candidateLimits.HourlyLimitUSD = input.HourlyLimitUSD.Value
	}
	if input.HourlyWindowHours != nil {
		candidateLimits.HourlyWindowHours = *input.HourlyWindowHours
	}
	validatedLimits, err := ValidateSubscriptionLimits(candidateLimits)
	if err != nil {
		return nil, err
	}
	tpl.DailyLimitUSD = validatedLimits.DailyLimitUSD
	tpl.WeeklyLimitUSD = validatedLimits.WeeklyLimitUSD
	tpl.MonthlyLimitUSD = validatedLimits.MonthlyLimitUSD
	tpl.HourlyLimitUSD = validatedLimits.HourlyLimitUSD
	tpl.HourlyWindowHours = validatedLimits.HourlyWindowHours

	if input.DefaultValidityDays != nil {
		days, err := normalizeTemplateValidityDays(*input.DefaultValidityDays)
		if err != nil {
			return nil, err
		}
		tpl.DefaultValidityDays = days
	}

	if input.Status != nil {
		status, err := normalizeTemplateStatus(*input.Status)
		if err != nil {
			return nil, err
		}
		tpl.Status = status
	}

	if input.SortOrder != nil {
		tpl.SortOrder = *input.SortOrder
	}

	if err := repo.Update(ctx, tpl); err != nil {
		return nil, err
	}
	if boundGroup == nil && tpl.GroupID > 0 {
		if g, err := s.groupRepo.GetByID(ctx, tpl.GroupID); err == nil {
			boundGroup = g
		}
	}
	tpl.Group = boundGroup
	return tpl, nil
}

// DeleteTemplate soft-deletes a template if it has no for-sale plans or in-progress orders.
// Existing user subscriptions keep their snapshotted limits and template_name.
func (s *SubscriptionService) DeleteTemplate(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrSubscriptionTemplateNotFound
	}
	repo := s.templateRepository()
	if repo == nil {
		return ErrSubscriptionTemplateNotFound
	}

	if _, err := repo.GetByID(ctx, id); err != nil {
		return err
	}

	planCount, err := repo.CountForSalePlansByTemplateID(ctx, id)
	if err != nil {
		return err
	}
	if planCount > 0 {
		return ErrSubscriptionTemplateInUse
	}

	orderCount, err := repo.CountInProgressOrdersByTemplateID(ctx, id)
	if err != nil {
		return err
	}
	if orderCount > 0 {
		return ErrSubscriptionTemplateInUse
	}

	return repo.Delete(ctx, id)
}

// ListActiveSubscriptions returns all active subscriptions for (userID, groupID) in FEFO order
// (expires_at ASC, created_at ASC, id ASC). Uses L1 cache + singleflight on the hot path.
func (s *SubscriptionService) ListActiveSubscriptions(ctx context.Context, userID, groupID int64) ([]UserSubscription, error) {
	key := subListCacheKey(userID, groupID)

	if s.subCacheL1 != nil {
		if v, ok := s.subCacheL1.Get(key); ok {
			if subs, ok := v.([]UserSubscription); ok {
				return cloneUserSubscriptionSlice(subs), nil
			}
		}
	}

	value, err, _ := s.subCacheGroup.Do(key, func() (any, error) {
		var subs []UserSubscription
		if multi := s.multiSubRepository(); multi != nil {
			list, listErr := multi.ListActiveByUserIDAndGroupID(ctx, userID, groupID)
			if listErr != nil {
				return nil, listErr
			}
			subs = list
		} else {
			single, getErr := s.userSubRepo.GetActiveByUserIDAndGroupID(ctx, userID, groupID)
			if getErr != nil {
				if errors.Is(getErr, ErrSubscriptionNotFound) {
					subs = []UserSubscription{}
				} else {
					return nil, getErr
				}
			} else if single != nil {
				subs = []UserSubscription{*single}
			}
		}
		if s.subCacheL1 != nil {
			_ = s.subCacheL1.SetWithTTL(key, cloneUserSubscriptionSlice(subs), 1, s.jitteredTTL(s.subCacheTTL))
		}
		return subs, nil
	})
	if err != nil {
		return nil, err
	}
	subs, _ := value.([]UserSubscription)
	return cloneUserSubscriptionSlice(subs), nil
}

// AssignByTemplate creates a stacked parallel subscription or extends an existing matching template
// subscription, snapshotting the template's limits and name at assignment time.
// Returns (subscription, isExtended, err).
func (s *SubscriptionService) AssignByTemplate(ctx context.Context, input *AssignTemplateSubscriptionInput) (*UserSubscription, bool, error) {
	if input == nil || input.UserID <= 0 || input.TemplateID <= 0 {
		return nil, false, ErrSubscriptionTemplateInvalid
	}
	mode, err := normalizeAssignMode(input.Mode)
	if err != nil {
		return nil, false, err
	}

	tplRepo := s.templateRepository()
	if tplRepo == nil {
		return nil, false, ErrSubscriptionTemplateNotFound
	}
	tpl, err := tplRepo.GetByID(ctx, input.TemplateID)
	if err != nil {
		return nil, false, err
	}
	if !tpl.IsActive() {
		return nil, false, ErrSubscriptionTemplateInactive
	}

	group, err := s.groupRepo.GetByID(ctx, tpl.GroupID)
	if err != nil {
		return nil, false, fmt.Errorf("group not found: %w", err)
	}
	if !group.IsActive() {
		return nil, false, infraerrors.BadRequest("GROUP_INACTIVE", "target group is not active")
	}

	validityDays := input.ValidityDays
	if validityDays <= 0 {
		validityDays = tpl.DefaultValidityDays
	}
	validityDays = normalizeAssignValidityDays(validityDays)

	multi := s.multiSubRepository()
	if mode == SubscriptionAssignModeExtend && multi != nil {
		existing, findErr := multi.FindLatestByUserIDAndTemplateID(ctx, input.UserID, tpl.ID)
		if findErr != nil && !errors.Is(findErr, ErrSubscriptionNotFound) {
			return nil, false, findErr
		}
		if existing != nil {
			if err := s.updateExistingSubscriptionTerm(ctx, existing.ID, validityDays, input.Notes, false); err != nil {
				return nil, false, err
			}
			s.maybeInvalidateInstanceCaches(input.UserID, tpl.GroupID, existing.ID, input.DeferCacheInvalidation)
			refreshed, getErr := s.userSubRepo.GetByID(ctx, existing.ID)
			return refreshed, true, getErr
		}
	}

	var createdID int64
	err = s.withSubscriptionUpdateTx(ctx, func(txCtx context.Context) error {
		now := time.Now()
		if s.now != nil {
			now = s.now()
		}
		expiresAt := now.AddDate(0, 0, validityDays)
		if expiresAt.After(MaxExpiresAt) {
			expiresAt = MaxExpiresAt
		}
		tplID := tpl.ID
		snap := &UserSubscriptionSnapshot{
			TemplateID:        &tplID,
			TemplateName:      tpl.Name,
			DailyLimitUSD:     cloneFloat64Ptr(tpl.DailyLimitUSD),
			WeeklyLimitUSD:    cloneFloat64Ptr(tpl.WeeklyLimitUSD),
			MonthlyLimitUSD:   cloneFloat64Ptr(tpl.MonthlyLimitUSD),
			HourlyLimitUSD:    cloneFloat64Ptr(tpl.HourlyLimitUSD),
			HourlyWindowHours: tpl.Limits().NormalizedHourlyWindowHours(),
		}

		sub := &UserSubscription{
			UserID:     input.UserID,
			GroupID:    tpl.GroupID,
			StartsAt:   now,
			ExpiresAt:  expiresAt,
			Status:     SubscriptionStatusActive,
			AssignedAt: now,
			Notes:      input.Notes,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if input.AssignedBy > 0 {
			assignedBy := input.AssignedBy
			sub.AssignedBy = &assignedBy
		}
		sub.ApplySnapshot(snap)

		if err := s.userSubRepo.Create(txCtx, sub); err != nil {
			return err
		}
		createdID = sub.ID
		if multi != nil {
			snap.SubscriptionID = sub.ID
			if err := multi.UpsertSnapshot(txCtx, snap); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}

	s.maybeInvalidateInstanceCaches(input.UserID, tpl.GroupID, createdID, input.DeferCacheInvalidation)
	created, err := s.userSubRepo.GetByID(ctx, createdID)
	if err != nil {
		return nil, false, err
	}
	return created, false, nil
}

// RenewSpecificSubscription extends a specific subscription instance by ID.
func (s *SubscriptionService) RenewSpecificSubscription(
	ctx context.Context,
	subscriptionID int64,
	validityDays int,
	notes string,
	deferCacheInvalidation bool,
) (*UserSubscription, error) {
	if subscriptionID <= 0 {
		return nil, ErrSubscriptionNotFound
	}
	days := normalizeAssignValidityDays(validityDays)
	if err := s.updateExistingSubscriptionTerm(ctx, subscriptionID, days, notes, false); err != nil {
		return nil, err
	}
	sub, err := s.userSubRepo.GetByID(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	s.maybeInvalidateInstanceCaches(sub.UserID, sub.GroupID, sub.ID, deferCacheInvalidation)
	return sub, nil
}

// AdminUpdateSubscriptionLimits overrides the snapshotted quota limits for a single user subscription.
// Existing TemplateID/TemplateName lineage is preserved; nil fields become explicitly unlimited.
func (s *SubscriptionService) AdminUpdateSubscriptionLimits(ctx context.Context, subscriptionID int64, limits SubscriptionLimits) (*UserSubscription, error) {
	if subscriptionID <= 0 {
		return nil, ErrSubscriptionNotFound
	}
	validated, err := ValidateSubscriptionLimits(limits)
	if err != nil {
		return nil, err
	}

	sub, err := s.userSubRepo.GetByID(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}

	snap := &UserSubscriptionSnapshot{
		SubscriptionID:    sub.ID,
		TemplateID:        cloneTemplateInt64Ptr(sub.TemplateID),
		TemplateName:      sub.TemplateName,
		DailyLimitUSD:     validated.DailyLimitUSD,
		WeeklyLimitUSD:    validated.WeeklyLimitUSD,
		MonthlyLimitUSD:   validated.MonthlyLimitUSD,
		HourlyLimitUSD:    validated.HourlyLimitUSD,
		HourlyWindowHours: validated.HourlyWindowHours,
	}

	multi := s.multiSubRepository()
	if multi == nil {
		return nil, infraerrors.InternalServer("SUBSCRIPTION_SNAPSHOT_UNSUPPORTED", "subscription repository does not support limit snapshots")
	}
	if err := multi.UpsertSnapshot(ctx, snap); err != nil {
		return nil, err
	}

	if err := s.invalidateSubscriptionInstanceCaches(sub.UserID, sub.GroupID, sub.ID); err != nil {
		return nil, err
	}

	return s.userSubRepo.GetByID(ctx, subscriptionID)
}

// ListActiveSubscriptionsByTemplateID returns a user's active subscriptions originating from a template.
func (s *SubscriptionService) ListActiveSubscriptionsByTemplateID(ctx context.Context, userID, templateID int64) ([]UserSubscription, error) {
	multi := s.multiSubRepository()
	if multi == nil {
		return []UserSubscription{}, nil
	}
	subs, err := multi.ListActiveByUserIDAndTemplateID(ctx, userID, templateID)
	if err != nil {
		return nil, err
	}
	normalizeExpiredWindows(subs)
	return subs, nil
}

// FindLatestSubscriptionByTemplateID returns the user's most recently created subscription for a template.
func (s *SubscriptionService) FindLatestSubscriptionByTemplateID(ctx context.Context, userID, templateID int64) (*UserSubscription, error) {
	multi := s.multiSubRepository()
	if multi == nil {
		return nil, ErrSubscriptionNotFound
	}
	return multi.FindLatestByUserIDAndTemplateID(ctx, userID, templateID)
}
