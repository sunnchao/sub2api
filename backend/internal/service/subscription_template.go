package service

import (
	"context"
	"math"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	SubscriptionTemplateStatusActive   = "active"
	SubscriptionTemplateStatusInactive = "inactive"

	// RedeemSubscriptionModeStack creates a new parallel subscription instance on redeem.
	RedeemSubscriptionModeStack = "stack"
	// RedeemSubscriptionModeExtend extends the user's latest matching template subscription on redeem.
	RedeemSubscriptionModeExtend = "extend"

	// SubscriptionAssignModeStack creates a new parallel subscription instance.
	SubscriptionAssignModeStack = "stack"
	// SubscriptionAssignModeExtend extends an existing subscription (legacy or matching template).
	SubscriptionAssignModeExtend = "extend"
)

var (
	ErrSubscriptionTemplateNotFound = infraerrors.NotFound("SUBSCRIPTION_TEMPLATE_NOT_FOUND", "subscription template not found")
	ErrSubscriptionTemplateExists   = infraerrors.Conflict("SUBSCRIPTION_TEMPLATE_EXISTS", "subscription template name already exists")
	ErrSubscriptionTemplateInactive = infraerrors.BadRequest("SUBSCRIPTION_TEMPLATE_INACTIVE", "subscription template is not active")
	ErrSubscriptionTemplateInUse    = infraerrors.Conflict("SUBSCRIPTION_TEMPLATE_IN_USE", "subscription template is referenced by active plans or in-progress orders")
	ErrSubscriptionTemplateInvalid  = infraerrors.BadRequest("SUBSCRIPTION_TEMPLATE_INVALID", "invalid subscription template configuration")
	ErrInvalidTargetSubscription    = infraerrors.BadRequest("INVALID_TARGET_SUBSCRIPTION", "target subscription is invalid or does not match the selected plan")
)

// SubscriptionLimits represents the effective quota limits for a subscription instance or template.
type SubscriptionLimits struct {
	DailyLimitUSD     *float64
	WeeklyLimitUSD    *float64
	MonthlyLimitUSD   *float64
	HourlyLimitUSD    *float64
	HourlyWindowHours int
}

func (l SubscriptionLimits) HasDailyLimit() bool {
	return l.DailyLimitUSD != nil && *l.DailyLimitUSD > 0
}

func (l SubscriptionLimits) HasWeeklyLimit() bool {
	return l.WeeklyLimitUSD != nil && *l.WeeklyLimitUSD > 0
}

func (l SubscriptionLimits) HasMonthlyLimit() bool {
	return l.MonthlyLimitUSD != nil && *l.MonthlyLimitUSD > 0
}

func (l SubscriptionLimits) HasHourlyLimit() bool {
	return l.HourlyLimitUSD != nil && *l.HourlyLimitUSD > 0
}

func (l SubscriptionLimits) HasAnyLimit() bool {
	return l.HasDailyLimit() || l.HasWeeklyLimit() || l.HasMonthlyLimit() || l.HasHourlyLimit()
}

func (l SubscriptionLimits) NormalizedHourlyWindowHours() int {
	if l.HourlyWindowHours > 0 {
		return l.HourlyWindowHours
	}
	return defaultHourlyWindowHours
}

func (l SubscriptionLimits) HourlyWindowPeriod() time.Duration {
	return time.Duration(l.NormalizedHourlyWindowHours()) * time.Hour
}

// SubscriptionTemplate defines a reusable subscription tier (quota limits + default validity)
// bound to a single underlying group (either standard or subscription type).
type SubscriptionTemplate struct {
	ID                  int64
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
	CreatedAt           time.Time
	UpdatedAt           time.Time
	DeletedAt           *time.Time

	Group *Group
}

func (t *SubscriptionTemplate) IsActive() bool {
	return t != nil && t.Status == SubscriptionTemplateStatusActive && t.DeletedAt == nil
}

func (t *SubscriptionTemplate) Limits() SubscriptionLimits {
	if t == nil {
		return SubscriptionLimits{HourlyWindowHours: defaultHourlyWindowHours}
	}
	return SubscriptionLimits{
		DailyLimitUSD:     cloneFloat64Ptr(t.DailyLimitUSD),
		WeeklyLimitUSD:    cloneFloat64Ptr(t.WeeklyLimitUSD),
		MonthlyLimitUSD:   cloneFloat64Ptr(t.MonthlyLimitUSD),
		HourlyLimitUSD:    cloneFloat64Ptr(t.HourlyLimitUSD),
		HourlyWindowHours: t.HourlyWindowHours,
	}
}

// UserSubscriptionSnapshot stores the snapshotted limits and template lineage for a single
// UserSubscription row in the user_subscription_snapshots sidecar table.
type UserSubscriptionSnapshot struct {
	SubscriptionID    int64
	TemplateID        *int64
	TemplateName      string
	DailyLimitUSD     *float64
	WeeklyLimitUSD    *float64
	MonthlyLimitUSD   *float64
	HourlyLimitUSD    *float64
	HourlyWindowHours int
	UpdatedAt         time.Time
}

// PaymentOrderSubscriptionMeta holds template / renewal target / fulfilled instance metadata
// in the payment_order_subscriptions sidecar table.
type PaymentOrderSubscriptionMeta struct {
	OrderID                 int64
	TemplateID              *int64
	TargetSubscriptionID    *int64
	FulfilledSubscriptionID *int64
}

// RedeemCodeTemplateMeta holds template & redeem-mode metadata in redeem_code_templates.
type RedeemCodeTemplateMeta struct {
	RedeemCodeID int64
	TemplateID   int64
	TemplateName string
	RedeemMode   string
}

// IsSubscriptionBillingActive reports whether a request should be billed against a subscription
// rather than user balance. Both subscription-type groups and standard groups with an active
// subscription bound via a subscription template evaluate to true when subscription != nil.
func IsSubscriptionBillingActive(group *Group, subscription *UserSubscription) bool {
	return group != nil && subscription != nil
}

// EffectiveLimits returns the authoritative quota limits for this subscription instance:
//   - If HasCustomLimits is true (snapshot present), strictly use the snapshotted limits
//     (nil means explicitly unlimited; never fall back to the group's defaults).
//   - Otherwise (legacy direct-group subscription), fall back to group's limits.
func (s *UserSubscription) EffectiveLimits(group *Group) SubscriptionLimits {
	if s != nil && s.HasCustomLimits {
		hours := s.HourlyWindowHours
		if hours <= 0 {
			hours = defaultHourlyWindowHours
		}
		return SubscriptionLimits{
			DailyLimitUSD:     s.DailyLimitUSD,
			WeeklyLimitUSD:    s.WeeklyLimitUSD,
			MonthlyLimitUSD:   s.MonthlyLimitUSD,
			HourlyLimitUSD:    s.HourlyLimitUSD,
			HourlyWindowHours: hours,
		}
	}
	if group == nil && s != nil {
		group = s.Group
	}
	if group == nil {
		return SubscriptionLimits{HourlyWindowHours: defaultHourlyWindowHours}
	}
	hours := group.HourlyWindowHours
	if hours <= 0 {
		hours = defaultHourlyWindowHours
	}
	return SubscriptionLimits{
		DailyLimitUSD:     group.DailyLimitUSD,
		WeeklyLimitUSD:    group.WeeklyLimitUSD,
		MonthlyLimitUSD:   group.MonthlyLimitUSD,
		HourlyLimitUSD:    group.HourlyLimitUSD,
		HourlyWindowHours: hours,
	}
}

// ApplySnapshot copies snapshot fields onto UserSubscription.
func (s *UserSubscription) ApplySnapshot(snap *UserSubscriptionSnapshot) {
	if s == nil || snap == nil {
		return
	}
	s.TemplateID = cloneTemplateInt64Ptr(snap.TemplateID)
	s.TemplateName = snap.TemplateName
	s.HasCustomLimits = true
	s.DailyLimitUSD = cloneFloat64Ptr(snap.DailyLimitUSD)
	s.WeeklyLimitUSD = cloneFloat64Ptr(snap.WeeklyLimitUSD)
	s.MonthlyLimitUSD = cloneFloat64Ptr(snap.MonthlyLimitUSD)
	s.HourlyLimitUSD = cloneFloat64Ptr(snap.HourlyLimitUSD)
	if snap.HourlyWindowHours > 0 {
		s.HourlyWindowHours = snap.HourlyWindowHours
	} else {
		s.HourlyWindowHours = defaultHourlyWindowHours
	}
}

func cloneFloat64Ptr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

func cloneTemplateInt64Ptr(v *int64) *int64 {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

func validateOptionalUSDLimit(name string, v *float64) error {
	if v == nil {
		return nil
	}
	if math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 {
		return infraerrors.BadRequest("SUBSCRIPTION_LIMIT_INVALID", name+" must be a finite non-negative number")
	}
	return nil
}

// NormalizeOptionalLimitPointer converts <= 0 or nil to nil (unlimited) after validating non-negativity.
func NormalizeOptionalLimitPointer(name string, v *float64) (*float64, error) {
	if err := validateOptionalUSDLimit(name, v); err != nil {
		return nil, err
	}
	if v == nil || *v == 0 {
		return nil, nil
	}
	cp := *v
	return &cp, nil
}

// ValidateSubscriptionLimits validates 4-window USD limits and hourly window hours.
func ValidateSubscriptionLimits(limits SubscriptionLimits) (SubscriptionLimits, error) {
	daily, err := NormalizeOptionalLimitPointer("daily_limit_usd", limits.DailyLimitUSD)
	if err != nil {
		return SubscriptionLimits{}, err
	}
	weekly, err := NormalizeOptionalLimitPointer("weekly_limit_usd", limits.WeeklyLimitUSD)
	if err != nil {
		return SubscriptionLimits{}, err
	}
	monthly, err := NormalizeOptionalLimitPointer("monthly_limit_usd", limits.MonthlyLimitUSD)
	if err != nil {
		return SubscriptionLimits{}, err
	}
	hourly, err := NormalizeOptionalLimitPointer("hourly_limit_usd", limits.HourlyLimitUSD)
	if err != nil {
		return SubscriptionLimits{}, err
	}
	hours := limits.HourlyWindowHours
	if hours == 0 {
		hours = defaultHourlyWindowHours
	}
	if hours < 1 || hours > MaxHourlyWindowHours {
		return SubscriptionLimits{}, infraerrors.BadRequest("SUBSCRIPTION_HOURLY_WINDOW_INVALID", "hourly_window_hours must be between 1 and 168")
	}
	return SubscriptionLimits{
		DailyLimitUSD:     daily,
		WeeklyLimitUSD:    weekly,
		MonthlyLimitUSD:   monthly,
		HourlyLimitUSD:    hourly,
		HourlyWindowHours: hours,
	}, nil
}

// NormalizeRedeemSubscriptionMode validates and normalizes redeem_mode (defaults to stack).
func NormalizeRedeemSubscriptionMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		return RedeemSubscriptionModeStack, nil
	}
	switch mode {
	case RedeemSubscriptionModeStack, RedeemSubscriptionModeExtend:
		return mode, nil
	default:
		return "", infraerrors.BadRequest("INVALID_REDEEM_MODE", "redeem_mode must be 'stack' or 'extend'")
	}
}

// SubscriptionTemplateListFilter filters admin template listings.
type SubscriptionTemplateListFilter struct {
	GroupID  *int64
	Status   string
	Platform string
	Search   string
}

// SubscriptionTemplateRepository manages CRUD for subscription_templates.
type SubscriptionTemplateRepository interface {
	Create(ctx context.Context, tpl *SubscriptionTemplate) error
	GetByID(ctx context.Context, id int64) (*SubscriptionTemplate, error)
	GetByIDs(ctx context.Context, ids []int64) (map[int64]*SubscriptionTemplate, error)
	Update(ctx context.Context, tpl *SubscriptionTemplate) error
	Delete(ctx context.Context, id int64) error
	List(ctx context.Context, filter SubscriptionTemplateListFilter) ([]SubscriptionTemplate, error)
	ExistsActiveByName(ctx context.Context, name string, excludeID int64) (bool, error)
	CountForSalePlansByTemplateID(ctx context.Context, templateID int64) (int64, error)
	CountInProgressOrdersByTemplateID(ctx context.Context, templateID int64) (int64, error)
}

// UserSubscriptionMultiRepository is an optional extension interface implemented by
// userSubscriptionRepository without breaking existing UserSubscriptionRepository mocks.
type UserSubscriptionMultiRepository interface {
	ListActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) ([]UserSubscription, error)
	ListActiveByUserIDAndTemplateID(ctx context.Context, userID, templateID int64) ([]UserSubscription, error)
	FindLatestByUserIDAndTemplateID(ctx context.Context, userID, templateID int64) (*UserSubscription, error)
	UpsertSnapshot(ctx context.Context, snap *UserSubscriptionSnapshot) error
	GetSnapshotsBySubscriptionIDs(ctx context.Context, subIDs []int64) (map[int64]*UserSubscriptionSnapshot, error)
}

// SubscriptionExtRepository manages the 3 payment/redeem sidecar tables.
type SubscriptionExtRepository interface {
	SetPlanTemplate(ctx context.Context, planID, templateID int64) error
	ClearPlanTemplate(ctx context.Context, planID int64) error
	GetPlanTemplateID(ctx context.Context, planID int64) (*int64, error)
	GetPlanTemplateIDs(ctx context.Context, planIDs []int64) (map[int64]int64, error)

	UpsertOrderSubscriptionMeta(ctx context.Context, meta *PaymentOrderSubscriptionMeta) error
	GetOrderSubscriptionMeta(ctx context.Context, orderID int64) (*PaymentOrderSubscriptionMeta, error)
	SetOrderFulfilledSubscriptionID(ctx context.Context, orderID, subscriptionID int64) error

	SetRedeemCodeTemplate(ctx context.Context, redeemCodeID, templateID int64, mode string) error
	SetRedeemCodeTemplatesBatch(ctx context.Context, metas []RedeemCodeTemplateMeta) error
	GetRedeemCodeTemplate(ctx context.Context, redeemCodeID int64) (*RedeemCodeTemplateMeta, error)
	GetRedeemCodeTemplates(ctx context.Context, redeemCodeIDs []int64) (map[int64]*RedeemCodeTemplateMeta, error)
}

// SubscriptionInstanceCache is an optional extension interface implemented by billingCache
// to cache and increment subscription usage keyed by individual subscription_id.
type SubscriptionInstanceCache interface {
	GetSubscriptionInstanceCache(ctx context.Context, subscriptionID int64) (*SubscriptionCacheData, error)
	SetSubscriptionInstanceCache(ctx context.Context, subscriptionID int64, data *SubscriptionCacheData) error
	UpdateSubscriptionInstanceUsage(ctx context.Context, subscriptionID int64, cost float64) error
	InvalidateSubscriptionInstanceCache(ctx context.Context, subscriptionID int64) error
}
