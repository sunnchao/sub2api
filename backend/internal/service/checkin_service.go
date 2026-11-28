package service

import (
	"context"
	"crypto/rand"
	"errors"
	"math"
	"math/big"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// 签到相关错误码（reason 与前端 i18n / E2E 契约保持一致）。
const (
	CheckinReasonDisabled             = "CHECKIN_DISABLED"
	CheckinReasonAlreadyDone          = "CHECKIN_ALREADY_DONE"
	CheckinReasonIneligibleStatus     = "CHECKIN_INELIGIBLE_STATUS"
	CheckinReasonIneligibleAccountAge = "CHECKIN_INELIGIBLE_ACCOUNT_AGE"
	CheckinReasonIneligibleRecharge   = "CHECKIN_INELIGIBLE_RECHARGE"
	CheckinReasonIneligibleSource     = "CHECKIN_INELIGIBLE_SOURCE"
	CheckinReasonBalanceCapExceeded   = "CHECKIN_BALANCE_CAP_EXCEEDED"
	CheckinReasonIPLimitExceeded      = "CHECKIN_IP_LIMIT_EXCEEDED"
	CheckinReasonDailyBudgetExhausted = "CHECKIN_DAILY_BUDGET_EXHAUSTED"
	CheckinReasonCaptchaNotConfigured = "CHECKIN_CAPTCHA_NOT_CONFIGURED"
	CheckinReasonCaptchaRequired      = "CAPTCHA_REQUIRED"
	CheckinReasonCaptchaInvalid       = "CAPTCHA_INVALID"
	CheckinReasonRepositoryMissing    = "CHECKIN_SERVICE_UNAVAILABLE"
)

// CheckinRecord 描述一条用户签到记录。
type CheckinRecord struct {
	ID                   int64     `json:"id"`
	UserID               int64     `json:"user_id"`
	CheckinDate          string    `json:"checkin_date"`
	StreakDays           int       `json:"streak_days"`
	MilestoneCycleDay    int       `json:"milestone_cycle_day"`
	BaseAmount           float64   `json:"base_amount"`
	MilestoneBonusAmount float64   `json:"milestone_bonus_amount"`
	TotalAmount          float64   `json:"total_amount"`
	ValidityDays         int       `json:"validity_days"`
	ExpiresAt            time.Time `json:"expires_at"`
	ClientIP             string    `json:"client_ip,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

// CheckinCalendarDay 描述签到日历中某一天的奖励明细。
type CheckinCalendarDay struct {
	Date                 string  `json:"date"`
	BaseAmount           float64 `json:"base_amount"`
	MilestoneBonusAmount float64 `json:"milestone_bonus_amount"`
	TotalAmount          float64 `json:"total_amount"`
	MilestoneHit         bool    `json:"milestone_hit"`
}

// CheckinCalendar 描述某个月的签到日历。
type CheckinCalendar struct {
	Month            string               `json:"month"`
	CheckedDates     []CheckinCalendarDay `json:"checked_dates"`
	MonthTotalAmount float64              `json:"month_total_amount"`
	MonthCheckinDays int                  `json:"month_checkin_days"`
}

// CheckinRewardPreview 描述当前生效的奖励规则预览。
type CheckinRewardPreview struct {
	RewardMode      string  `json:"reward_mode"`
	BaseAmount      float64 `json:"base_amount"`
	MinAmount       float64 `json:"min_amount"`
	MaxAmount       float64 `json:"max_amount"`
	AmountPrecision int     `json:"amount_precision"`
	ValidityDays    int     `json:"validity_days"`
}

// CheckinEligibilityStatus 描述当前用户是否满足签到准入门槛。
type CheckinEligibilityStatus struct {
	Eligible     bool   `json:"eligible"`
	ReasonCode   string `json:"reason_code,omitempty"`
	ReasonDetail string `json:"reason_detail,omitempty"`
}

// CheckinStatus 描述签到页概览（用户端 GET /checkin/status）。
type CheckinStatus struct {
	Enabled              bool                     `json:"enabled"`
	CheckedInToday       bool                     `json:"checked_in_today"`
	TodayRecord          *CheckinRecord           `json:"today_record,omitempty"`
	CurrentStreak        int                      `json:"current_streak"`
	MilestoneCycleDay    int                      `json:"milestone_cycle_day"`
	NextMilestone        *CheckinMilestoneRule    `json:"next_milestone,omitempty"`
	Milestones           []CheckinMilestoneRule   `json:"milestones"`
	MilestoneCycleRepeat bool                     `json:"milestone_cycle_repeat"`
	RewardPreview        CheckinRewardPreview     `json:"reward_preview"`
	Eligibility          CheckinEligibilityStatus `json:"eligibility"`
	CaptchaRequired      bool                     `json:"captcha_required"`
	ExpiringSummary      ExpiringBalanceSummary   `json:"expiring_summary"`
	RulesDescription     string                   `json:"rules_description"`
	ServerDate           string                   `json:"server_date"`
	Timezone             string                   `json:"timezone"`
}

// CheckinExecuteResponse 描述一次成功签到后的返回体。
type CheckinExecuteResponse struct {
	Record                CheckinRecord          `json:"record"`
	ExpiringSummary       ExpiringBalanceSummary `json:"expiring_summary"`
	TotalAvailableBalance float64                `json:"total_available_balance"`
}

// CheckinUserSnapshot 描述签到判定所需的用户状态快照。
type CheckinUserSnapshot struct {
	ID             int64
	Status         string
	CreatedAt      time.Time
	TotalRecharged float64
	SignupSource   string
	Balance        float64
	BoundProviders []string
}

// CheckinExecuteInput 描述一次签到事务的全部输入。
// 日期与金额由服务层预先算好，保证事务内的判定完全确定。
type CheckinExecuteInput struct {
	UserID       int64
	ClientIP     string
	Now          time.Time
	Today        time.Time
	Yesterday    time.Time
	BaseAmount   float64
	ValidityDays int
	ExpiresAt    time.Time
	Settings     *CheckinSettings
}

// AdminCheckinRecordFilter 描述管理端签到流水的筛选条件。
type AdminCheckinRecordFilter struct {
	UserID    int64
	Search    string
	ClientIP  string
	StartDate string
	EndDate   string
	Params    pagination.PaginationParams
}

// AdminCheckinRecord 描述管理端签到流水（附带用户标识）。
type AdminCheckinRecord struct {
	CheckinRecord
	UserEmail string `json:"user_email"`
	Username  string `json:"username"`
}

// AdminCheckinDailyTrend 描述管理端统计的每日趋势行。
type AdminCheckinDailyTrend struct {
	Date           string  `json:"date"`
	CheckinCount   int     `json:"checkin_count"`
	GrantedAmount  float64 `json:"granted_amount"`
	ConsumedAmount float64 `json:"consumed_amount"`
	ExpiredAmount  float64 `json:"expired_amount"`
}

// AdminCheckinStats 描述管理端签到统计概览。
type AdminCheckinStats struct {
	TodayCheckins      int                      `json:"today_checkins"`
	TodayGranted       float64                  `json:"today_granted"`
	DailyBudgetLimit   float64                  `json:"daily_budget_limit"`
	DailyBudgetPercent float64                  `json:"daily_budget_percent"`
	RangeDays          int                      `json:"range_days"`
	RangeTotalGranted  float64                  `json:"range_total_granted"`
	RangeTotalConsumed float64                  `json:"range_total_consumed"`
	RangeTotalExpired  float64                  `json:"range_total_expired"`
	ConsumptionRate    float64                  `json:"consumption_rate"`
	ExpirationRate     float64                  `json:"expiration_rate"`
	DailyTrend         []AdminCheckinDailyTrend `json:"daily_trend"`
}

// CheckinRepository 定义签到存储层接口。
// ExecuteCheckin 必须在一个数据库事务内完成锁用户、门槛校验、连签推算、
// IP 限额、日预算熔断、写记录与发放限时余额批次。
type CheckinRepository interface {
	ExecuteCheckin(ctx context.Context, in CheckinExecuteInput) (CheckinRecord, error)
	GetCheckinUserSnapshot(ctx context.Context, userID int64) (CheckinUserSnapshot, error)
	GetLastCheckinRecord(ctx context.Context, userID int64) (*CheckinRecord, error)
	GetCheckinRecordByDate(ctx context.Context, userID int64, date time.Time) (*CheckinRecord, error)
	ListCheckinCalendar(ctx context.Context, userID int64, monthStart, monthEnd time.Time) ([]CheckinCalendarDay, error)
	ListUserCheckinRecords(ctx context.Context, userID int64, params pagination.PaginationParams) ([]CheckinRecord, *pagination.PaginationResult, error)
	ListAdminCheckinRecords(ctx context.Context, filter AdminCheckinRecordFilter) ([]AdminCheckinRecord, *pagination.PaginationResult, error)
	GetAdminCheckinStats(ctx context.Context, days int) (AdminCheckinStats, error)
}

// CheckinCaptchaVerifier 定义签到所需的人机验证窄接口（由 AuthService 实现）。
type CheckinCaptchaVerifier interface {
	VerifyCaptcha(ctx context.Context, proof CaptchaProof, remoteIP string) error
}

// CheckinCaptchaProof 描述用户提交的人机验证凭据。
type CheckinCaptchaProof struct {
	TurnstileToken        string `json:"turnstile_token"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr"`
	AliyunCaptchaParam    string `json:"aliyun_captcha_verify_param"`
}

// CheckinService 提供每日签到、连签推算、限时余额发放与统计查询。
type CheckinService struct {
	settingService *SettingService
	repo           CheckinRepository
	expiringSvc    *ExpiringBalanceService
	captcha        CheckinCaptchaVerifier
	billingCache   BillingCacheInvalidator
	authCache      AuthCacheInvalidator
}

// NewCheckinService 创建签到服务。
func NewCheckinService(
	settingService *SettingService,
	repo CheckinRepository,
	expiringSvc *ExpiringBalanceService,
) *CheckinService {
	return &CheckinService{
		settingService: settingService,
		repo:           repo,
		expiringSvc:    expiringSvc,
	}
}

// SetCaptchaVerifier 注入人机验证实现（AuthService）。
func (s *CheckinService) SetCaptchaVerifier(v CheckinCaptchaVerifier) {
	if s == nil {
		return
	}
	s.captcha = v
}

// SetCacheInvalidators 注入签到成功后的余额缓存清理器。
func (s *CheckinService) SetCacheInvalidators(billing BillingCacheInvalidator, auth AuthCacheInvalidator) {
	if s == nil {
		return
	}
	s.billingCache = billing
	s.authCache = auth
}

func (s *CheckinService) ready() error {
	if s == nil || s.repo == nil || s.settingService == nil {
		return infraerrors.InternalServer(CheckinReasonRepositoryMissing, "checkin service is not configured")
	}
	return nil
}

// GetStatus 返回签到概览、规则预览与资格判定。
func (s *CheckinService) GetStatus(ctx context.Context, userID int64) (*CheckinStatus, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	settings, err := s.settingService.GetCheckinSettings(ctx)
	if err != nil {
		return nil, err
	}

	now := timezone.Now()
	today := timezone.StartOfDay(now)

	status := &CheckinStatus{
		Enabled:              settings.Enabled,
		Milestones:           append([]CheckinMilestoneRule(nil), settings.Milestones...),
		MilestoneCycleRepeat: settings.MilestoneCycleRepeat,
		RewardPreview: CheckinRewardPreview{
			RewardMode:      settings.RewardMode,
			BaseAmount:      settings.BaseAmount,
			MinAmount:       settings.MinAmount,
			MaxAmount:       settings.MaxAmount,
			AmountPrecision: settings.AmountPrecision,
			ValidityDays:    settings.ValidityDays,
		},
		Eligibility:      CheckinEligibilityStatus{Eligible: true},
		CaptchaRequired:  settings.Enabled && settings.Risk.CaptchaEnabled,
		RulesDescription: settings.RulesDescription,
		ServerDate:       today.Format("2006-01-02"),
		Timezone:         timezone.Name(),
	}

	if s.expiringSvc != nil {
		if summary, sumErr := s.expiringSvc.GetUserSummary(ctx, userID); sumErr == nil {
			status.ExpiringSummary = summary
		}
	}

	if s.settingService.IsSimpleMode() || !settings.Enabled {
		status.Enabled = false
		status.CaptchaRequired = false
		status.Eligibility = CheckinEligibilityStatus{
			Eligible:   false,
			ReasonCode: CheckinReasonDisabled,
		}
		return status, nil
	}

	snapshot, err := s.repo.GetCheckinUserSnapshot(ctx, userID)
	if err != nil {
		return nil, err
	}
	last, err := s.repo.GetLastCheckinRecord(ctx, userID)
	if err != nil {
		return nil, err
	}

	todayStr := today.Format("2006-01-02")
	if last != nil {
		status.CheckedInToday = last.CheckinDate == todayStr
		if status.CheckedInToday {
			record := *last
			status.TodayRecord = &record
		}
	}

	streak, cycleDay, nextMilestone := resolveCheckinProgress(settings, last, today)
	status.CurrentStreak = streak
	status.MilestoneCycleDay = cycleDay
	status.NextMilestone = nextMilestone

	status.Eligibility = EvaluateCheckinEligibility(settings, snapshot, now, status.ExpiringSummary.ActiveBalance)
	return status, nil
}

// resolveCheckinProgress 返回用户“当前”的连签天数与里程碑周期内天数。
// 只有当最近一次签到是今天或昨天时连签才仍然有效，否则视为已断签（归零）。
// next 是尚未解锁的下一档里程碑；重复周期开启时封顶后回到第一档。
func resolveCheckinProgress(
	settings *CheckinSettings,
	last *CheckinRecord,
	today time.Time,
) (streak int, cycleDay int, next *CheckinMilestoneRule) {
	todayStr := today.Format("2006-01-02")
	yesterdayStr := today.AddDate(0, 0, -1).Format("2006-01-02")

	if last != nil && (last.CheckinDate == todayStr || last.CheckinDate == yesterdayStr) {
		streak = last.StreakDays
		cycleDay = last.MilestoneCycleDay
	}

	if settings.MaxMilestoneDays() > 0 {
		next = findNextMilestone(settings, cycleDay)
	}
	return streak, cycleDay, next
}

// findNextMilestone 返回下一档尚未解锁的里程碑；全部解锁且未开启循环时返回 nil。
func findNextMilestone(settings *CheckinSettings, cycleDay int) *CheckinMilestoneRule {
	if settings == nil {
		return nil
	}
	for i := range settings.Milestones {
		if settings.Milestones[i].Days > cycleDay {
			rule := settings.Milestones[i]
			return &rule
		}
	}
	if settings.MilestoneCycleRepeat && len(settings.Milestones) > 0 {
		rule := settings.Milestones[0]
		return &rule
	}
	return nil
}

// ResolveCheckinStreak 推算本次签到的连签天数、里程碑周期内天数与命中的加赠额。
// last 为最近一条签到记录（可能为 nil）；yesterday 为今天的上一自然日。
func ResolveCheckinStreak(
	settings *CheckinSettings,
	last *CheckinRecord,
	yesterday time.Time,
) (streak int, cycleDay int, bonus float64) {
	maxDays := settings.MaxMilestoneDays()
	if last != nil && last.CheckinDate == yesterday.Format("2006-01-02") {
		streak = last.StreakDays + 1
		cycleDay = nextCheckinCycleDay(settings, last.MilestoneCycleDay, maxDays)
	} else {
		streak = 1
		cycleDay = 1
	}
	if bonusAmount, ok := settings.MilestoneBonus(cycleDay); ok {
		bonus = bonusAmount
	}
	return streak, cycleDay, bonus
}

// nextCheckinCycleDay 推算下一次签到落在里程碑周期的第几天。
func nextCheckinCycleDay(settings *CheckinSettings, lastCycleDay int, maxDays int) int {
	if maxDays <= 0 {
		return lastCycleDay + 1
	}
	if lastCycleDay >= maxDays && settings.MilestoneCycleRepeat {
		return 1
	}
	return lastCycleDay + 1
}

// EvaluateCheckinEligibility 按配置校验用户是否满足签到准入门槛。
func EvaluateCheckinEligibility(
	settings *CheckinSettings,
	snapshot CheckinUserSnapshot,
	now time.Time,
	activeExpiringBalance float64,
) CheckinEligibilityStatus {
	eligible := CheckinEligibilityStatus{Eligible: true}
	if settings == nil {
		return eligible
	}

	if snapshot.Status != "active" {
		return CheckinEligibilityStatus{
			Eligible:   false,
			ReasonCode: CheckinReasonIneligibleStatus,
		}
	}

	cfg := settings.Eligibility
	if cfg.MinAccountAgeDays > 0 {
		minAge := time.Duration(cfg.MinAccountAgeDays) * 24 * time.Hour
		if now.Sub(snapshot.CreatedAt) < minAge {
			return CheckinEligibilityStatus{
				Eligible:   false,
				ReasonCode: CheckinReasonIneligibleAccountAge,
			}
		}
	}

	if cfg.MinTotalRecharged > 0 && snapshot.TotalRecharged < cfg.MinTotalRecharged {
		return CheckinEligibilityStatus{
			Eligible:   false,
			ReasonCode: CheckinReasonIneligibleRecharge,
		}
	}

	if len(cfg.AllowedSignupSources) > 0 && !matchSignupSource(cfg.AllowedSignupSources, snapshot) {
		return CheckinEligibilityStatus{
			Eligible:   false,
			ReasonCode: CheckinReasonIneligibleSource,
		}
	}

	if cfg.MaxAvailableBalance > 0 {
		total := QuantizeUsageBillingAmount(snapshot.Balance + activeExpiringBalance)
		if total >= cfg.MaxAvailableBalance {
			return CheckinEligibilityStatus{
				Eligible:   false,
				ReasonCode: CheckinReasonBalanceCapExceeded,
			}
		}
	}

	return eligible
}

// matchSignupSource 判定用户的注册来源或已绑定身份是否命中允许列表。
func matchSignupSource(allowed []string, snapshot CheckinUserSnapshot) bool {
	if len(allowed) == 0 {
		return true
	}
	candidates := make([]string, 0, len(snapshot.BoundProviders)+1)
	if src := strings.ToLower(strings.TrimSpace(snapshot.SignupSource)); src != "" {
		candidates = append(candidates, src)
	}
	for _, p := range snapshot.BoundProviders {
		if v := strings.ToLower(strings.TrimSpace(p)); v != "" {
			candidates = append(candidates, v)
		}
	}
	for _, c := range candidates {
		for _, a := range allowed {
			if c == strings.ToLower(strings.TrimSpace(a)) {
				return true
			}
		}
	}
	return false
}

// Checkin 执行今日签到。
func (s *CheckinService) Checkin(ctx context.Context, userID int64, clientIP string, proof CheckinCaptchaProof) (*CheckinExecuteResponse, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	settings, err := s.settingService.GetCheckinSettings(ctx)
	if err != nil {
		return nil, err
	}
	if s.settingService.IsSimpleMode() || !settings.Enabled {
		return nil, infraerrors.Forbidden(CheckinReasonDisabled, "daily check-in is disabled")
	}

	if err := s.verifyCaptcha(ctx, settings, clientIP, proof); err != nil {
		return nil, err
	}

	baseAmount, err := generateCheckinBaseAmount(settings)
	if err != nil {
		return nil, err
	}

	now := timezone.Now()
	today := timezone.StartOfDay(now)
	validityDays := settings.ValidityDays
	if validityDays < 1 {
		validityDays = CheckinValidityDaysDefault
	}
	expiresAt := today.AddDate(0, 0, validityDays)

	record, err := s.repo.ExecuteCheckin(ctx, CheckinExecuteInput{
		UserID:       userID,
		ClientIP:     clientIP,
		Now:          now,
		Today:        today,
		Yesterday:    today.AddDate(0, 0, -1),
		BaseAmount:   baseAmount,
		ValidityDays: validityDays,
		ExpiresAt:    expiresAt,
		Settings:     settings,
	})
	if err != nil {
		return nil, err
	}

	// 事务提交后清理缓存：余额与 API Key 认证缓存都可能持有旧的余额快照。
	if s.billingCache != nil {
		_ = s.billingCache.InvalidateUserBalance(ctx, userID)
	}
	if s.authCache != nil {
		s.authCache.InvalidateAuthCacheByUserID(ctx, userID)
	}

	resp := &CheckinExecuteResponse{Record: record}
	if s.expiringSvc != nil {
		if summary, sumErr := s.expiringSvc.GetUserSummary(ctx, userID); sumErr == nil {
			resp.ExpiringSummary = summary
		}
	}
	regular := 0.0
	if snapshot, snapErr := s.repo.GetCheckinUserSnapshot(ctx, userID); snapErr == nil {
		regular = snapshot.Balance
	}
	resp.TotalAvailableBalance = QuantizeUsageBillingAmount(regular + resp.ExpiringSummary.ActiveBalance)
	return resp, nil
}

// verifyCaptcha 按配置执行人机验证；未开启时直接放行。
func (s *CheckinService) verifyCaptcha(ctx context.Context, settings *CheckinSettings, clientIP string, proof CheckinCaptchaProof) error {
	if settings == nil || !settings.Risk.CaptchaEnabled {
		return nil
	}

	providerConfig, err := s.settingService.GetCaptchaProviderConfig(ctx)
	if err != nil {
		return infraerrors.ServiceUnavailable(CheckinReasonCaptchaNotConfigured, "captcha provider settings are unavailable")
	}
	turnstileEnabled := providerConfig.TurnstileEnabled
	tencentEnabled := providerConfig.Tencent.Enabled
	aliyunEnabled := providerConfig.Aliyun.Enabled
	if !turnstileEnabled && !tencentEnabled && !aliyunEnabled {
		// fail-closed：开启了签到验证码但没有可用服务商时拒绝签到，避免绕过风控。
		return infraerrors.Forbidden(CheckinReasonCaptchaNotConfigured, "captcha is required but no provider is configured")
	}
	if captchaProvidersConflict(turnstileEnabled, tencentEnabled, aliyunEnabled) {
		return infraerrors.Forbidden(CheckinReasonCaptchaNotConfigured, "multiple captcha providers are enabled")
	}

	if !checkinProofSatisfiesProvider(proof, tencentEnabled, aliyunEnabled, turnstileEnabled) {
		return infraerrors.BadRequest(CheckinReasonCaptchaRequired, "captcha verification is required")
	}

	if s.captcha == nil {
		return infraerrors.ServiceUnavailable(CheckinReasonCaptchaNotConfigured, "captcha verifier is not configured")
	}

	token := strings.TrimSpace(proof.TurnstileToken)
	if aliyunEnabled && token == "" {
		// 阿里云验证码复用 turnstile_token 承载 captchaVerifyParam。
		if v := strings.TrimSpace(proof.AliyunCaptchaParam); v != "" {
			token = v
		}
	}

	if err := s.captcha.VerifyCaptcha(ctx, CaptchaProof{
		TurnstileToken: token,
		TencentTicket:  strings.TrimSpace(proof.TencentCaptchaTicket),
		TencentRandstr: strings.TrimSpace(proof.TencentCaptchaRandstr),
	}, clientIP); err != nil {
		if isCaptchaNotConfiguredError(err) {
			return infraerrors.Forbidden(CheckinReasonCaptchaNotConfigured, "captcha provider is not configured")
		}
		return infraerrors.BadRequest(CheckinReasonCaptchaInvalid, "captcha verification failed")
	}
	return nil
}

// checkinProofSatisfiesProvider 校验客户端提交的凭据是否匹配当前启用的验证码服务商。
func checkinProofSatisfiesProvider(proof CheckinCaptchaProof, tencentEnabled, aliyunEnabled, turnstileEnabled bool) bool {
	switch {
	case tencentEnabled:
		return strings.TrimSpace(proof.TencentCaptchaTicket) != ""
	case aliyunEnabled:
		return strings.TrimSpace(proof.AliyunCaptchaParam) != "" || strings.TrimSpace(proof.TurnstileToken) != ""
	case turnstileEnabled:
		return strings.TrimSpace(proof.TurnstileToken) != ""
	default:
		// 未启用任何服务商时不产生额外凭据约束（调用方已在更早的分支 fail-closed）。
		return true
	}
}

// isCaptchaNotConfiguredError 判定验证码服务商未配置类错误。
func isCaptchaNotConfiguredError(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrTurnstileNotConfigured),
		errors.Is(err, ErrTencentCaptchaNotConfigured),
		errors.Is(err, ErrAliyunCaptchaNotConfigured),
		errors.Is(err, ErrCaptchaProviderConflict):
		return true
	}
	return false
}

// generateCheckinBaseAmount 按配置生成当日基础奖励金额。
func generateCheckinBaseAmount(settings *CheckinSettings) (float64, error) {
	if settings == nil {
		return 0, infraerrors.InternalServer(CheckinReasonRepositoryMissing, "checkin settings are missing")
	}
	if !settings.IsRandomReward() {
		return QuantizeUsageBillingAmount(settings.BaseAmount), nil
	}

	precision := settings.AmountPrecision
	if precision < CheckinAmountPrecisionMin {
		precision = CheckinAmountPrecisionDefault
	}
	if precision > CheckinAmountPrecisionMax {
		precision = CheckinAmountPrecisionMax
	}
	amount, err := randomCheckinAmount(settings.MinAmount, settings.MaxAmount, precision)
	if err != nil {
		return 0, infraerrors.InternalServer("CHECKIN_RANDOM_FAILED", "failed to generate random checkin reward")
	}
	return amount, nil
}

// randomCheckinAmount 在 [min, max] 内按指定小数位均匀取值。
func randomCheckinAmount(min, max float64, precision int) (float64, error) {
	if math.IsNaN(min) || math.IsNaN(max) || math.IsInf(min, 0) || math.IsInf(max, 0) {
		return 0, errors.New("invalid amount range")
	}
	if max < min {
		min, max = max, min
	}
	pow := math.Pow10(precision)
	steps := int64(math.Round((max - min) * pow))
	if steps <= 0 {
		return QuantizeUsageBillingAmount(min), nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(steps+1))
	if err != nil {
		return 0, err
	}
	value := min + float64(n.Int64())/pow
	value = math.Round(value*pow) / pow
	if value > max {
		value = max
	}
	return QuantizeUsageBillingAmount(value), nil
}

// GetCalendar 返回指定月份（YYYY-MM，缺省当月）的签到日历。
func (s *CheckinService) GetCalendar(ctx context.Context, userID int64, month string) (*CheckinCalendar, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	monthStart, err := parseCheckinMonth(month)
	if err != nil {
		return nil, err
	}
	monthEnd := monthStart.AddDate(0, 1, 0)

	days, err := s.repo.ListCheckinCalendar(ctx, userID, monthStart, monthEnd)
	if err != nil {
		return nil, err
	}

	out := &CheckinCalendar{
		Month:        monthStart.Format("2006-01"),
		CheckedDates: days,
	}
	if out.CheckedDates == nil {
		out.CheckedDates = []CheckinCalendarDay{}
	}
	for _, d := range days {
		out.MonthTotalAmount = QuantizeUsageBillingAmount(out.MonthTotalAmount + d.TotalAmount)
		if d.TotalAmount > 0 || d.BaseAmount > 0 || d.MilestoneBonusAmount > 0 {
			out.MonthCheckinDays++
		}
	}
	return out, nil
}

// parseCheckinMonth 解析 YYYY-MM 并按全局时区定位到当月 1 日 00:00。
func parseCheckinMonth(month string) (time.Time, error) {
	raw := strings.TrimSpace(month)
	if raw == "" {
		return timezone.StartOfMonth(timezone.Now()), nil
	}
	parsed, err := time.ParseInLocation("2006-01", raw, timezone.Location())
	if err != nil {
		return time.Time{}, infraerrors.BadRequest("CHECKIN_INVALID_MONTH", "month must be formatted as YYYY-MM")
	}
	return parsed, nil
}

// ListRecords 分页返回当前用户的签到历史。
func (s *CheckinService) ListRecords(ctx context.Context, userID int64, params pagination.PaginationParams) ([]CheckinRecord, *pagination.PaginationResult, error) {
	if err := s.ready(); err != nil {
		return nil, nil, err
	}
	if params.Page < 1 {
		params.Page = 1
	}
	params.PageSize = params.Limit()
	return s.repo.ListUserCheckinRecords(ctx, userID, params)
}

// ListLots 分页返回当前用户的限时余额批次明细。
func (s *CheckinService) ListLots(ctx context.Context, userID int64, status string, params pagination.PaginationParams) ([]ExpiringBalanceLot, *pagination.PaginationResult, error) {
	if s.expiringSvc == nil {
		return nil, nil, infraerrors.InternalServer(CheckinReasonRepositoryMissing, "expiring balance service is not configured")
	}
	return s.expiringSvc.ListUserLots(ctx, userID, status, params)
}

// AdminGetConfig 返回管理端签到配置。
func (s *CheckinService) AdminGetConfig(ctx context.Context) (*CheckinSettings, error) {
	if s == nil || s.settingService == nil {
		return nil, infraerrors.InternalServer(CheckinReasonRepositoryMissing, "checkin service is not configured")
	}
	return s.settingService.GetCheckinSettings(ctx)
}

// AdminUpdateConfig 保存管理端签到配置。
func (s *CheckinService) AdminUpdateConfig(ctx context.Context, in *CheckinSettings) (*CheckinSettings, error) {
	if s == nil || s.settingService == nil {
		return nil, infraerrors.InternalServer(CheckinReasonRepositoryMissing, "checkin service is not configured")
	}
	return s.settingService.SetCheckinSettings(ctx, in)
}

// AdminGetStats 返回管理端签到统计概览。
func (s *CheckinService) AdminGetStats(ctx context.Context, days int) (*AdminCheckinStats, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}

	stats, err := s.repo.GetAdminCheckinStats(ctx, days)
	if err != nil {
		return nil, err
	}

	settings, err := s.settingService.GetCheckinSettings(ctx)
	if err != nil {
		return nil, err
	}
	stats.RangeDays = days
	stats.DailyBudgetLimit = settings.Risk.DailyTotalBudgetUSD
	if stats.DailyBudgetLimit > 0 {
		stats.DailyBudgetPercent = math.Round(stats.TodayGranted/stats.DailyBudgetLimit*10000) / 100
	}
	if stats.DailyTrend == nil {
		stats.DailyTrend = []AdminCheckinDailyTrend{}
	}
	return &stats, nil
}

// AdminListRecords 分页返回管理端签到流水。
func (s *CheckinService) AdminListRecords(ctx context.Context, filter AdminCheckinRecordFilter) ([]AdminCheckinRecord, *pagination.PaginationResult, error) {
	if err := s.ready(); err != nil {
		return nil, nil, err
	}
	filter.StartDate = strings.TrimSpace(filter.StartDate)
	filter.EndDate = strings.TrimSpace(filter.EndDate)
	if filter.StartDate != "" {
		if _, err := time.Parse("2006-01-02", filter.StartDate); err != nil {
			return nil, nil, infraerrors.BadRequest("CHECKIN_INVALID_DATE", "start_date must be formatted as YYYY-MM-DD")
		}
	}
	if filter.EndDate != "" {
		if _, err := time.Parse("2006-01-02", filter.EndDate); err != nil {
			return nil, nil, infraerrors.BadRequest("CHECKIN_INVALID_DATE", "end_date must be formatted as YYYY-MM-DD")
		}
	}
	if filter.Params.Page < 1 {
		filter.Params.Page = 1
	}
	filter.Params.PageSize = filter.Params.Limit()
	return s.repo.ListAdminCheckinRecords(ctx, filter)
}
