package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 签到奖励模式
const (
	CheckinRewardModeFixed  = "fixed"
	CheckinRewardModeRandom = "random"
)

// 签到配置的合法取值范围。所有边界都在服务层归一化，
// 保证下游（CheckinService / 前端表单）拿到的配置永远可直接使用。
const (
	CheckinValidityDaysDefault    = 1
	CheckinValidityDaysMin        = 1
	CheckinValidityDaysMax        = 365
	CheckinAmountPrecisionDefault = 2
	CheckinAmountPrecisionMin     = 2
	CheckinAmountPrecisionMax     = 4
	CheckinMaxMilestones          = 20
	checkinAmountMax              = 1_000_000.0
)

// CheckinMilestoneRule 描述一档连续签到里程碑加赠规则。
type CheckinMilestoneRule struct {
	// Days 连续签到天数（同一轮内唯一，必须 > 0）。
	Days int `json:"days"`
	// BonusAmount 命中该档时额外赠送的限时余额（USD）。
	BonusAmount float64 `json:"bonus_amount"`
}

// CheckinEligibilityConfig 描述可选的签到准入门槛，全部默认关闭（0 / 空）。
type CheckinEligibilityConfig struct {
	// MinAccountAgeDays 最低注册天数，0 = 不限。
	MinAccountAgeDays int `json:"min_account_age_days"`
	// MinTotalRecharged 最低历史累计充值额，0 = 不限。
	MinTotalRecharged float64 `json:"min_total_recharged"`
	// MaxAvailableBalance 总可用余额封顶（>= 该值时禁止签到），0 = 不限。
	MaxAvailableBalance float64 `json:"max_available_balance"`
	// AllowedSignupSources 允许的注册/绑定来源，空 = 全部允许。
	AllowedSignupSources []string `json:"allowed_signup_sources"`
}

// CheckinRiskConfig 描述防刷与成本熔断配置。
type CheckinRiskConfig struct {
	// CaptchaEnabled 是否要求过人机验证（复用系统已启用的验证码服务商）。
	CaptchaEnabled bool `json:"captcha_enabled"`
	// MaxAccountsPerIPDay 单 IP 每日最多允许多少不同账号签到，0 = 不限。
	MaxAccountsPerIPDay int `json:"max_accounts_per_ip_day"`
	// DailyTotalBudgetUSD 全站每日总预算上限，0 = 不限。
	DailyTotalBudgetUSD float64 `json:"daily_total_budget_usd"`
}

// CheckinSettings 每日签到规则配置。
type CheckinSettings struct {
	Enabled              bool                     `json:"enabled"`
	RewardMode           string                   `json:"reward_mode"`
	BaseAmount           float64                  `json:"base_amount"`
	MinAmount            float64                  `json:"min_amount"`
	MaxAmount            float64                  `json:"max_amount"`
	AmountPrecision      int                      `json:"amount_precision"`
	ValidityDays         int                      `json:"validity_days"`
	Milestones           []CheckinMilestoneRule   `json:"milestones"`
	MilestoneCycleRepeat bool                     `json:"milestone_cycle_repeat"`
	Eligibility          CheckinEligibilityConfig `json:"eligibility"`
	Risk                 CheckinRiskConfig        `json:"risk"`
	RulesDescription     string                   `json:"rules_description"`
	// Warnings 是只读提示（例如开启人机验证但未配置任何验证码服务商），不参与持久化。
	Warnings []string `json:"warnings,omitempty"`
}

// DefaultCheckinSettings 返回默认的签到配置：功能关闭、每日固定 0.05 USD、有效期 1 天、无里程碑。
func DefaultCheckinSettings() *CheckinSettings {
	return &CheckinSettings{
		Enabled:              false,
		RewardMode:           CheckinRewardModeFixed,
		BaseAmount:           0.05,
		MinAmount:            0.01,
		MaxAmount:            0.10,
		AmountPrecision:      CheckinAmountPrecisionDefault,
		ValidityDays:         CheckinValidityDaysDefault,
		Milestones:           []CheckinMilestoneRule{},
		MilestoneCycleRepeat: false,
		Eligibility: CheckinEligibilityConfig{
			AllowedSignupSources: []string{},
		},
		Risk:             CheckinRiskConfig{},
		RulesDescription: "",
	}
}

// Clone 返回配置的深拷贝，避免调用方修改共享切片。
func (s *CheckinSettings) Clone() *CheckinSettings {
	if s == nil {
		return nil
	}
	out := *s
	out.Milestones = append([]CheckinMilestoneRule(nil), s.Milestones...)
	out.Eligibility.AllowedSignupSources = append([]string(nil), s.Eligibility.AllowedSignupSources...)
	out.Warnings = append([]string(nil), s.Warnings...)
	return &out
}

// IsRandomReward 报告当前是否为随机区间奖励模式。
func (s *CheckinSettings) IsRandomReward() bool {
	return s != nil && s.RewardMode == CheckinRewardModeRandom
}

// MaxMilestoneDays 返回已配置里程碑中的最大天数，无配置时返回 0。
func (s *CheckinSettings) MaxMilestoneDays() int {
	if s == nil {
		return 0
	}
	maxDays := 0
	for _, m := range s.Milestones {
		if m.Days > maxDays {
			maxDays = m.Days
		}
	}
	return maxDays
}

// MilestoneBonus 返回连续签到 cycleDay 天时命中的里程碑加赠额。
// 同一轮内每档仅触发一次，因此这里采用精确匹配。
func (s *CheckinSettings) MilestoneBonus(cycleDay int) (float64, bool) {
	if s == nil || cycleDay <= 0 {
		return 0, false
	}
	for _, m := range s.Milestones {
		if m.Days == cycleDay {
			return m.BonusAmount, true
		}
	}
	return 0, false
}

// NormalizeCheckinSettings 归一化并校验签到配置。
// 返回值中的 settings 已填充默认值且所有字段都在合法范围内；
// 非空 error 表示存在无法自动修正的语义冲突（例如随机区间 min > max）。
func NormalizeCheckinSettings(in *CheckinSettings) (*CheckinSettings, error) {
	out := DefaultCheckinSettings()
	if in == nil {
		return out, nil
	}

	out.Enabled = in.Enabled
	out.MilestoneCycleRepeat = in.MilestoneCycleRepeat
	out.RulesDescription = strings.TrimSpace(in.RulesDescription)

	switch strings.ToLower(strings.TrimSpace(in.RewardMode)) {
	case "", CheckinRewardModeFixed:
		out.RewardMode = CheckinRewardModeFixed
	case CheckinRewardModeRandom:
		out.RewardMode = CheckinRewardModeRandom
	default:
		return nil, infraerrors.BadRequest("CHECKIN_INVALID_REWARD_MODE", "reward_mode must be 'fixed' or 'random'")
	}

	out.AmountPrecision = in.AmountPrecision
	if out.AmountPrecision == 0 {
		out.AmountPrecision = CheckinAmountPrecisionDefault
	}
	if out.AmountPrecision < CheckinAmountPrecisionMin {
		out.AmountPrecision = CheckinAmountPrecisionMin
	}
	if out.AmountPrecision > CheckinAmountPrecisionMax {
		out.AmountPrecision = CheckinAmountPrecisionMax
	}

	out.ValidityDays = in.ValidityDays
	if out.ValidityDays == 0 {
		out.ValidityDays = CheckinValidityDaysDefault
	}
	if out.ValidityDays < CheckinValidityDaysMin {
		out.ValidityDays = CheckinValidityDaysMin
	}
	if out.ValidityDays > CheckinValidityDaysMax {
		out.ValidityDays = CheckinValidityDaysMax
	}

	baseAmount, err := normalizeCheckinAmount(in.BaseAmount, out.AmountPrecision)
	if err != nil {
		return nil, err
	}
	minAmount, err := normalizeCheckinAmount(in.MinAmount, out.AmountPrecision)
	if err != nil {
		return nil, err
	}
	maxAmount, err := normalizeCheckinAmount(in.MaxAmount, out.AmountPrecision)
	if err != nil {
		return nil, err
	}
	if out.RewardMode == CheckinRewardModeFixed {
		out.BaseAmount = baseAmount
		out.MinAmount = minAmount
		out.MaxAmount = maxAmount
	} else {
		if in.MinAmount <= 0 && in.MaxAmount <= 0 {
			minAmount = 0.01
			maxAmount = 0.10
		} else {
			if minAmount <= 0 {
				return nil, infraerrors.BadRequest("CHECKIN_INVALID_AMOUNT", "min_amount must be positive in random mode")
			}
			if maxAmount <= 0 || minAmount > maxAmount {
				return nil, infraerrors.BadRequest("CHECKIN_INVALID_RANDOM_RANGE", "min_amount must be less than or equal to max_amount")
			}
		}
		out.BaseAmount = baseAmount
		out.MinAmount = minAmount
		out.MaxAmount = maxAmount
	}
	if out.RewardMode == CheckinRewardModeFixed && out.BaseAmount <= 0 {
		out.BaseAmount = 0
	}

	milestones, err := normalizeCheckinMilestones(in.Milestones, out.AmountPrecision)
	if err != nil {
		return nil, err
	}
	out.Milestones = milestones

	out.Eligibility.MinAccountAgeDays = in.Eligibility.MinAccountAgeDays
	if out.Eligibility.MinAccountAgeDays < 0 {
		out.Eligibility.MinAccountAgeDays = 0
	}
	out.Eligibility.MinTotalRecharged = sanitizeCheckinThreshold(in.Eligibility.MinTotalRecharged)
	out.Eligibility.MaxAvailableBalance = sanitizeCheckinThreshold(in.Eligibility.MaxAvailableBalance)
	out.Eligibility.AllowedSignupSources = normalizeSignupSources(in.Eligibility.AllowedSignupSources)

	out.Risk.CaptchaEnabled = in.Risk.CaptchaEnabled
	out.Risk.MaxAccountsPerIPDay = in.Risk.MaxAccountsPerIPDay
	if out.Risk.MaxAccountsPerIPDay < 0 {
		out.Risk.MaxAccountsPerIPDay = 0
	}
	out.Risk.DailyTotalBudgetUSD = sanitizeCheckinThreshold(in.Risk.DailyTotalBudgetUSD)

	return out, nil
}

// normalizeCheckinAmount 把金额按小数位取整，并拒绝 NaN / Inf / 负数 / 越界值。
func normalizeCheckinAmount(v float64, precision int) (float64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, infraerrors.BadRequest("CHECKIN_INVALID_AMOUNT", "checkin amount must be a finite number")
	}
	if v < 0 {
		return 0, infraerrors.BadRequest("CHECKIN_INVALID_AMOUNT", "checkin amount must not be negative")
	}
	if v > checkinAmountMax {
		return 0, infraerrors.BadRequest("CHECKIN_INVALID_AMOUNT", "checkin amount is too large")
	}
	pow := math.Pow10(precision)
	return math.Round(v*pow) / pow, nil
}

func sanitizeCheckinThreshold(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	if v > checkinAmountMax {
		return checkinAmountMax
	}
	return v
}

// normalizeSignupSources 去重、小写化并丢弃空值。
func normalizeSignupSources(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// normalizeCheckinMilestones 校验里程碑档位：天数唯一且为正、按天数升序、加赠额为正。
func normalizeCheckinMilestones(in []CheckinMilestoneRule, precision int) ([]CheckinMilestoneRule, error) {
	if len(in) == 0 {
		return []CheckinMilestoneRule{}, nil
	}
	if len(in) > CheckinMaxMilestones {
		return nil, infraerrors.BadRequest("CHECKIN_TOO_MANY_MILESTONES",
			fmt.Sprintf("at most %d milestones are allowed", CheckinMaxMilestones))
	}

	out := make([]CheckinMilestoneRule, 0, len(in))
	seen := make(map[int]struct{}, len(in))
	for _, m := range in {
		if m.Days <= 1 {
			return nil, infraerrors.BadRequest("CHECKIN_INVALID_MILESTONE", "milestone days must be greater than 1")
		}
		if _, ok := seen[m.Days]; ok {
			return nil, infraerrors.BadRequest("CHECKIN_DUPLICATE_MILESTONE",
				fmt.Sprintf("duplicate milestone for %d days", m.Days))
		}
		seen[m.Days] = struct{}{}

		bonus, err := normalizeCheckinAmount(m.BonusAmount, precision)
		if err != nil {
			return nil, err
		}
		if bonus <= 0 {
			return nil, infraerrors.BadRequest("CHECKIN_INVALID_MILESTONE_BONUS",
				fmt.Sprintf("milestone bonus for %d days must be positive", m.Days))
		}
		out = append(out, CheckinMilestoneRule{Days: m.Days, BonusAmount: bonus})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Days < out[j].Days })
	return out, nil
}

// GetCheckinSettings 读取签到配置（含总开关），缺失或损坏时回退到默认配置。
func (s *SettingService) GetCheckinSettings(ctx context.Context) (*CheckinSettings, error) {
	values, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyCheckinEnabled, SettingKeyCheckinConfig})
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultCheckinSettings(), nil
		}
		return nil, fmt.Errorf("get checkin settings: %w", err)
	}

	settings := DefaultCheckinSettings()
	if raw := strings.TrimSpace(values[SettingKeyCheckinConfig]); raw != "" {
		var parsed CheckinSettings
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			normalized, normalizeErr := NormalizeCheckinSettings(&parsed)
			if normalizeErr == nil {
				settings = normalized
			}
		}
	}

	// 总开关以独立的布尔键为准（缺失时回落到 JSON 内的 enabled，最终默认关闭）。
	if raw, ok := values[SettingKeyCheckinEnabled]; ok && strings.TrimSpace(raw) != "" {
		settings.Enabled = strings.EqualFold(strings.TrimSpace(raw), "true")
	}

	settings.Warnings = s.checkinConfigWarnings(ctx, settings)
	return settings, nil
}

// checkinConfigWarnings 返回不影响保存、但需要管理员关注的配置提示。
func (s *SettingService) checkinConfigWarnings(ctx context.Context, settings *CheckinSettings) []string {
	if settings == nil || !settings.Enabled || !settings.Risk.CaptchaEnabled {
		return nil
	}
	providerConfig, err := s.GetCaptchaProviderConfig(ctx)
	if err != nil {
		return []string{"captcha_provider_unavailable"}
	}
	if !providerConfig.TurnstileEnabled && !providerConfig.Tencent.Enabled && !providerConfig.Aliyun.Enabled {
		return []string{"captcha_enabled_without_provider"}
	}
	return nil
}

// SetCheckinSettings 归一化并持久化签到配置。
// 同时写入 checkin_enabled 布尔键，便于公开设置与侧边栏低成本读取。
func (s *SettingService) SetCheckinSettings(ctx context.Context, in *CheckinSettings) (*CheckinSettings, error) {
	normalized, err := NormalizeCheckinSettings(in)
	if err != nil {
		return nil, err
	}
	// Warnings 是派生信息，不落库。
	normalized.Warnings = nil

	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal checkin settings: %w", err)
	}

	enabledValue := "false"
	if normalized.Enabled {
		enabledValue = "true"
	}
	if err := s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyCheckinConfig:  string(data),
		SettingKeyCheckinEnabled: enabledValue,
	}); err != nil {
		return nil, fmt.Errorf("set checkin settings: %w", err)
	}
	s.NotifySettingsUpdated()

	out := normalized.Clone()
	out.Warnings = s.checkinConfigWarnings(ctx, normalized)
	return out, nil
}

// IsCheckinEnabled 报告签到功能是否开启（供公开设置与门控使用）。
func (s *SettingService) IsCheckinEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyCheckinEnabled)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(value), "true")
}
