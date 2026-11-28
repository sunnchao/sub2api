package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

// checkinRepoStub 是内存版 CheckinRepository，仅用于服务层单测。
type checkinRepoStub struct {
	mu sync.Mutex

	snapshot CheckinUserSnapshot
	last     *CheckinRecord
	records  []CheckinRecord
	calendar []CheckinCalendarDay
	stats    AdminCheckinStats

	executeErr error
	executed   []CheckinExecuteInput
}

func (r *checkinRepoStub) ExecuteCheckin(_ context.Context, in CheckinExecuteInput) (CheckinRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executed = append(r.executed, in)
	if r.executeErr != nil {
		return CheckinRecord{}, r.executeErr
	}
	streak, cycleDay, bonus := ResolveCheckinStreak(in.Settings, r.last, in.Yesterday)
	return CheckinRecord{
		ID:                   1,
		UserID:               in.UserID,
		CheckinDate:          in.Today.Format("2006-01-02"),
		StreakDays:           streak,
		MilestoneCycleDay:    cycleDay,
		BaseAmount:           in.BaseAmount,
		MilestoneBonusAmount: bonus,
		TotalAmount:          in.BaseAmount + bonus,
		ValidityDays:         in.ValidityDays,
		ExpiresAt:            in.ExpiresAt,
		ClientIP:             in.ClientIP,
	}, nil
}

func (r *checkinRepoStub) GetCheckinUserSnapshot(context.Context, int64) (CheckinUserSnapshot, error) {
	return r.snapshot, nil
}

func (r *checkinRepoStub) GetLastCheckinRecord(context.Context, int64) (*CheckinRecord, error) {
	return r.last, nil
}

func (r *checkinRepoStub) GetCheckinRecordByDate(context.Context, int64, time.Time) (*CheckinRecord, error) {
	return r.last, nil
}

func (r *checkinRepoStub) ListCheckinCalendar(context.Context, int64, time.Time, time.Time) ([]CheckinCalendarDay, error) {
	return r.calendar, nil
}

func (r *checkinRepoStub) ListUserCheckinRecords(context.Context, int64, pagination.PaginationParams) ([]CheckinRecord, *pagination.PaginationResult, error) {
	return r.records, &pagination.PaginationResult{Total: int64(len(r.records)), Page: 1, PageSize: 20, Pages: 1}, nil
}

func (r *checkinRepoStub) ListAdminCheckinRecords(context.Context, AdminCheckinRecordFilter) ([]AdminCheckinRecord, *pagination.PaginationResult, error) {
	return nil, &pagination.PaginationResult{}, nil
}

func (r *checkinRepoStub) GetAdminCheckinStats(context.Context, int) (AdminCheckinStats, error) {
	return r.stats, nil
}

// checkinCaptchaStub 记录 VerifyCaptcha 调用并返回预设错误。
type checkinCaptchaStub struct {
	err   error
	calls int
	proof CaptchaProof
	ip    string
}

func (c *checkinCaptchaStub) VerifyCaptcha(_ context.Context, proof CaptchaProof, remoteIP string) error {
	c.calls++
	c.proof = proof
	c.ip = remoteIP
	return c.err
}

func newCheckinTestService(t *testing.T, settings *CheckinSettings, repo CheckinRepository) (*CheckinService, *checkinSettingRepoStub) {
	t.Helper()
	repoValues := map[string]string{}
	if settings != nil {
		enabled := "false"
		if settings.Enabled {
			enabled = "true"
		}
		repoValues[SettingKeyCheckinEnabled] = enabled
		repoValues[SettingKeyCheckinConfig] = mustMarshalCheckinSettings(t, settings)
	}
	store := newCheckinSettingRepoStub(repoValues)
	settingSvc := NewSettingService(store, nil)
	return NewCheckinService(settingSvc, repo, nil), store
}

func mustMarshalCheckinSettings(t *testing.T, settings *CheckinSettings) string {
	t.Helper()
	data, err := json.Marshal(settings)
	require.NoError(t, err)
	return string(data)
}

func activeSnapshot() CheckinUserSnapshot {
	return CheckinUserSnapshot{
		ID:           7,
		Status:       "active",
		CreatedAt:    time.Now().Add(-30 * 24 * time.Hour),
		Balance:      1.5,
		SignupSource: "email",
	}
}

func TestResolveCheckinStreak(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Milestones = []CheckinMilestoneRule{{Days: 3, BonusAmount: 0.5}, {Days: 7, BonusAmount: 1}}
	today := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	yesterday := today.AddDate(0, 0, -1)

	streak, cycleDay, bonus := ResolveCheckinStreak(settings, nil, yesterday)
	require.Equal(t, 1, streak)
	require.Equal(t, 1, cycleDay)
	require.InDelta(t, 0.0, bonus, 1e-9)

	// 昨天签过 → 连签 +1，未命中里程碑。
	last := &CheckinRecord{CheckinDate: yesterday.Format("2006-01-02"), StreakDays: 1, MilestoneCycleDay: 1}
	streak, cycleDay, bonus = ResolveCheckinStreak(settings, last, yesterday)
	require.Equal(t, 2, streak)
	require.Equal(t, 2, cycleDay)
	require.InDelta(t, 0.0, bonus, 1e-9)

	// 昨天签过且命中 3 天里程碑。
	last = &CheckinRecord{CheckinDate: yesterday.Format("2006-01-02"), StreakDays: 2, MilestoneCycleDay: 2}
	streak, cycleDay, bonus = ResolveCheckinStreak(settings, last, yesterday)
	require.Equal(t, 3, streak)
	require.Equal(t, 3, cycleDay)
	require.InDelta(t, 0.5, bonus, 1e-9)

	// 断签（前天签的）→ 重置为 1。
	last = &CheckinRecord{CheckinDate: today.AddDate(0, 0, -2).Format("2006-01-02"), StreakDays: 5, MilestoneCycleDay: 5}
	streak, cycleDay, bonus = ResolveCheckinStreak(settings, last, yesterday)
	require.Equal(t, 1, streak)
	require.Equal(t, 1, cycleDay)
	require.InDelta(t, 0.0, bonus, 1e-9)
}

func TestResolveCheckinStreakCycleRepeat(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Milestones = []CheckinMilestoneRule{{Days: 3, BonusAmount: 0.5}}
	today := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	yesterday := today.AddDate(0, 0, -1)
	last := &CheckinRecord{CheckinDate: yesterday.Format("2006-01-02"), StreakDays: 3, MilestoneCycleDay: 3}

	// 未开启循环：周期天数继续递增，不再触发里程碑。
	streak, cycleDay, bonus := ResolveCheckinStreak(settings, last, yesterday)
	require.Equal(t, 4, streak)
	require.Equal(t, 4, cycleDay)
	require.InDelta(t, 0.0, bonus, 1e-9)

	// 开启循环：回到第 1 天。
	settings.MilestoneCycleRepeat = true
	streak, cycleDay, bonus = ResolveCheckinStreak(settings, last, yesterday)
	require.Equal(t, 4, streak)
	require.Equal(t, 1, cycleDay)
	require.InDelta(t, 0.0, bonus, 1e-9)
}

func TestEvaluateCheckinEligibility(t *testing.T) {
	now := time.Now()
	settings := DefaultCheckinSettings()

	eligible := EvaluateCheckinEligibility(settings, activeSnapshot(), now, 0)
	require.True(t, eligible.Eligible)

	inactive := activeSnapshot()
	inactive.Status = "banned"
	require.Equal(t, CheckinReasonIneligibleStatus,
		EvaluateCheckinEligibility(settings, inactive, now, 0).ReasonCode)

	settings.Eligibility.MinAccountAgeDays = 7
	fresh := activeSnapshot()
	fresh.CreatedAt = now.Add(-24 * time.Hour)
	require.Equal(t, CheckinReasonIneligibleAccountAge,
		EvaluateCheckinEligibility(settings, fresh, now, 0).ReasonCode)
	require.True(t, EvaluateCheckinEligibility(settings, activeSnapshot(), now, 0).Eligible)

	settings.Eligibility.MinAccountAgeDays = 0
	settings.Eligibility.MinTotalRecharged = 10
	require.Equal(t, CheckinReasonIneligibleRecharge,
		EvaluateCheckinEligibility(settings, activeSnapshot(), now, 0).ReasonCode)

	settings.Eligibility.MinTotalRecharged = 0
	settings.Eligibility.AllowedSignupSources = []string{"github"}
	require.Equal(t, CheckinReasonIneligibleSource,
		EvaluateCheckinEligibility(settings, activeSnapshot(), now, 0).ReasonCode)

	// 已绑定 github 身份时命中允许来源。
	bound := activeSnapshot()
	bound.BoundProviders = []string{"github"}
	require.True(t, EvaluateCheckinEligibility(settings, bound, now, 0).Eligible)

	settings.Eligibility.AllowedSignupSources = nil
	settings.Eligibility.MaxAvailableBalance = 2
	require.Equal(t, CheckinReasonBalanceCapExceeded,
		EvaluateCheckinEligibility(settings, activeSnapshot(), now, 0.5).ReasonCode)
	// 刚好低于封顶值时仍可签到。
	require.True(t, EvaluateCheckinEligibility(settings, activeSnapshot(), now, 0.4).Eligible)
}

func TestGenerateCheckinBaseAmount(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.RewardMode = CheckinRewardModeFixed
	settings.BaseAmount = 0.25

	amount, err := generateCheckinBaseAmount(settings)
	require.NoError(t, err)
	require.InDelta(t, 0.25, amount, 1e-9)

	settings.RewardMode = CheckinRewardModeRandom
	settings.MinAmount = 0.15
	settings.MaxAmount = 0.35
	settings.AmountPrecision = 2
	for i := 0; i < 200; i++ {
		amount, err = generateCheckinBaseAmount(settings)
		require.NoError(t, err)
		require.GreaterOrEqual(t, amount, 0.15)
		require.LessOrEqual(t, amount, 0.35)
		// 精度必须严格对齐 2 位小数。
		require.InDelta(t, amount, checkinRoundTo(amount, 2), 1e-12)
	}

	// 单点区间直接返回该值。
	settings.MinAmount = 0.2
	settings.MaxAmount = 0.2
	amount, err = generateCheckinBaseAmount(settings)
	require.NoError(t, err)
	require.InDelta(t, 0.2, amount, 1e-9)
}

func checkinRoundTo(v float64, precision int) float64 {
	pow := 1.0
	for i := 0; i < precision; i++ {
		pow *= 10
	}
	return float64(int64(v*pow+0.5)) / pow
}

func TestCheckinProofSatisfiesProvider(t *testing.T) {
	require.True(t, checkinProofSatisfiesProvider(CheckinCaptchaProof{}, false, false, false))
	require.False(t, checkinProofSatisfiesProvider(CheckinCaptchaProof{}, true, false, false))
	require.True(t, checkinProofSatisfiesProvider(CheckinCaptchaProof{TencentCaptchaTicket: "t"}, true, false, false))
	require.False(t, checkinProofSatisfiesProvider(CheckinCaptchaProof{TurnstileToken: "x"}, true, false, false))
	require.True(t, checkinProofSatisfiesProvider(CheckinCaptchaProof{TurnstileToken: "x"}, false, true, false))
	require.True(t, checkinProofSatisfiesProvider(CheckinCaptchaProof{AliyunCaptchaParam: "p"}, false, true, false))
	require.False(t, checkinProofSatisfiesProvider(CheckinCaptchaProof{TencentCaptchaTicket: "t"}, false, true, false))
	require.True(t, checkinProofSatisfiesProvider(CheckinCaptchaProof{TurnstileToken: "x"}, false, false, true))
}

func TestCheckinServiceStatusForFreshUser(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true
	settings.RulesDescription = "rules"
	settings.BaseAmount = 0.42
	settings.ValidityDays = 2
	settings.Milestones = []CheckinMilestoneRule{{Days: 2, BonusAmount: 0.5}}

	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	svc, _ := newCheckinTestService(t, settings, repo)

	status, err := svc.GetStatus(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, status.Enabled)
	require.False(t, status.CheckedInToday)
	require.Nil(t, status.TodayRecord)
	require.Equal(t, 0, status.CurrentStreak)
	require.Equal(t, 0, status.MilestoneCycleDay)
	require.NotNil(t, status.NextMilestone)
	require.Equal(t, 2, status.NextMilestone.Days)
	require.Equal(t, "fixed", status.RewardPreview.RewardMode)
	require.InDelta(t, 0.42, status.RewardPreview.BaseAmount, 1e-9)
	require.Equal(t, 2, status.RewardPreview.ValidityDays)
	require.Equal(t, "rules", status.RulesDescription)
	require.NotEmpty(t, status.ServerDate)
	require.NotEmpty(t, status.Timezone)
	require.True(t, status.Eligibility.Eligible)
	require.False(t, status.CaptchaRequired)
}

func TestCheckinServiceStatusReportsYesterdayStreak(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true
	settings.Milestones = []CheckinMilestoneRule{{Days: 2, BonusAmount: 0.5}, {Days: 5, BonusAmount: 1}}

	today := timezoneStartOfToday()
	repo := &checkinRepoStub{
		snapshot: activeSnapshot(),
		last: &CheckinRecord{
			CheckinDate:       today.AddDate(0, 0, -1).Format("2006-01-02"),
			StreakDays:        1,
			MilestoneCycleDay: 1,
		},
	}
	svc, _ := newCheckinTestService(t, settings, repo)

	status, err := svc.GetStatus(context.Background(), 7)
	require.NoError(t, err)
	require.False(t, status.CheckedInToday)
	require.Equal(t, 1, status.CurrentStreak)
	require.Equal(t, 1, status.MilestoneCycleDay)
	require.NotNil(t, status.NextMilestone)
	require.Equal(t, 2, status.NextMilestone.Days)
	require.InDelta(t, 0.5, status.NextMilestone.BonusAmount, 1e-9)
}

func TestCheckinServiceStatusDisabled(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = false

	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	svc, _ := newCheckinTestService(t, settings, repo)

	status, err := svc.GetStatus(context.Background(), 7)
	require.NoError(t, err)
	require.False(t, status.Enabled)
	require.False(t, status.Eligibility.Eligible)
	require.Equal(t, CheckinReasonDisabled, status.Eligibility.ReasonCode)
}

func TestCheckinServiceRejectsWhenDisabled(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = false

	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	svc, _ := newCheckinTestService(t, settings, repo)

	_, err := svc.Checkin(context.Background(), 7, "1.2.3.4", CheckinCaptchaProof{})
	require.Error(t, err)
	require.Equal(t, CheckinReasonDisabled, reasonOf(err))
	require.Empty(t, repo.executed)
}

func TestCheckinServiceCreatesLotWithConfiguredValidity(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true
	settings.BaseAmount = 0.3
	settings.ValidityDays = 3

	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	svc, _ := newCheckinTestService(t, settings, repo)

	resp, err := svc.Checkin(context.Background(), 7, "203.0.113.9", CheckinCaptchaProof{})
	require.NoError(t, err)
	require.Len(t, repo.executed, 1)

	executed := repo.executed[0]
	require.Equal(t, int64(7), executed.UserID)
	require.Equal(t, "203.0.113.9", executed.ClientIP)
	require.Equal(t, 3, executed.ValidityDays)
	require.InDelta(t, 0.3, executed.BaseAmount, 1e-9)

	today := timezoneStartOfToday()
	require.Equal(t, today.AddDate(0, 0, 3), executed.ExpiresAt)
	require.InDelta(t, 0.3, resp.Record.TotalAmount, 1e-9)
	require.InDelta(t, 1.5, resp.TotalAvailableBalance, 1e-9)
}

func TestCheckinServiceCaptchaRequiredButNoProvider(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true
	settings.Risk.CaptchaEnabled = true

	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	svc, _ := newCheckinTestService(t, settings, repo)
	captcha := &checkinCaptchaStub{}
	svc.SetCaptchaVerifier(captcha)

	_, err := svc.Checkin(context.Background(), 7, "1.2.3.4", CheckinCaptchaProof{})
	require.Error(t, err)
	require.Equal(t, CheckinReasonCaptchaNotConfigured, reasonOf(err))
	require.Zero(t, captcha.calls)
}

func TestCheckinServiceCaptchaMissingProof(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true
	settings.Risk.CaptchaEnabled = true

	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	svc, _ := newCheckinTestService(t, settings, repo)

	store, ok := svc.settingService.settingRepo.(*checkinSettingRepoStub)
	require.True(t, ok)
	require.NoError(t, store.Set(context.Background(), SettingKeyTencentCaptchaEnabled, "true"))
	require.NoError(t, store.Set(context.Background(), SettingKeyTencentCaptchaAppID, "1900000001"))

	captcha := &checkinCaptchaStub{}
	svc.SetCaptchaVerifier(captcha)

	_, err := svc.Checkin(context.Background(), 7, "1.2.3.4", CheckinCaptchaProof{})
	require.Error(t, err)
	require.Equal(t, CheckinReasonCaptchaRequired, reasonOf(err))
	require.Zero(t, captcha.calls)

	// 阿里云参数在腾讯验证码下不算有效凭据。
	_, err = svc.Checkin(context.Background(), 7, "1.2.3.4", CheckinCaptchaProof{AliyunCaptchaParam: "p"})
	require.Error(t, err)
	require.Equal(t, CheckinReasonCaptchaRequired, reasonOf(err))
}

func TestCheckinServiceCaptchaInvalid(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true
	settings.Risk.CaptchaEnabled = true

	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	svc, _ := newCheckinTestService(t, settings, repo)

	store, ok := svc.settingService.settingRepo.(*checkinSettingRepoStub)
	require.True(t, ok)
	require.NoError(t, store.Set(context.Background(), SettingKeyTencentCaptchaEnabled, "true"))
	require.NoError(t, store.Set(context.Background(), SettingKeyTencentCaptchaAppID, "1900000001"))

	captcha := &checkinCaptchaStub{err: errors.New("bad ticket")}
	svc.SetCaptchaVerifier(captcha)

	_, err := svc.Checkin(context.Background(), 7, "1.2.3.4", CheckinCaptchaProof{
		TencentCaptchaTicket:  "trerror_invalid",
		TencentCaptchaRandstr: "rand",
	})
	require.Error(t, err)
	require.Equal(t, CheckinReasonCaptchaInvalid, reasonOf(err))
	require.Equal(t, 1, captcha.calls)
	require.Equal(t, "1.2.3.4", captcha.ip)
	require.Equal(t, "trerror_invalid", captcha.proof.TencentTicket)
}

func TestCheckinServicePropagatesRepoErrors(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true

	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	svc, _ := newCheckinTestService(t, settings, repo)
	repo.executeErr = errCheckinStub

	_, err := svc.Checkin(context.Background(), 7, "1.2.3.4", CheckinCaptchaProof{})
	require.ErrorIs(t, err, errCheckinStub)
}

var errCheckinStub = errors.New("stub failure")

func TestCheckinServiceCalendarAggregates(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true

	repo := &checkinRepoStub{
		snapshot: activeSnapshot(),
		calendar: []CheckinCalendarDay{
			{Date: "2026-11-01", BaseAmount: 0.1, TotalAmount: 0.1},
			{Date: "2026-11-02", BaseAmount: 0.1, MilestoneBonusAmount: 0.5, TotalAmount: 0.6, MilestoneHit: true},
		},
	}
	svc, _ := newCheckinTestService(t, settings, repo)

	calendar, err := svc.GetCalendar(context.Background(), 7, "2026-11")
	require.NoError(t, err)
	require.Equal(t, "2026-11", calendar.Month)
	require.Len(t, calendar.CheckedDates, 2)
	require.Equal(t, 2, calendar.MonthCheckinDays)
	require.InDelta(t, 0.7, calendar.MonthTotalAmount, 1e-9)
	require.True(t, calendar.CheckedDates[1].MilestoneHit)

	_, err = svc.GetCalendar(context.Background(), 7, "2026/11")
	require.Error(t, err)
	require.Equal(t, "CHECKIN_INVALID_MONTH", reasonOf(err))
}

func TestCheckinServiceAdminStatsBudgetPercent(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true
	settings.Risk.DailyTotalBudgetUSD = 10

	repo := &checkinRepoStub{
		snapshot: activeSnapshot(),
		stats:    AdminCheckinStats{TodayCheckins: 1, TodayGranted: 1},
	}
	svc, _ := newCheckinTestService(t, settings, repo)

	stats, err := svc.AdminGetStats(context.Background(), 30)
	require.NoError(t, err)
	require.Equal(t, 30, stats.RangeDays)
	require.InDelta(t, 10, stats.DailyBudgetLimit, 1e-9)
	require.InDelta(t, 10, stats.DailyBudgetPercent, 1e-9)
	require.NotNil(t, stats.DailyTrend)

	// 非法 days 会被夹到默认区间。
	stats, err = svc.AdminGetStats(context.Background(), -5)
	require.NoError(t, err)
	require.Equal(t, 30, stats.RangeDays)
}

func TestCheckinServiceUnavailableWithoutRepo(t *testing.T) {
	svc := NewCheckinService(NewSettingService(newCheckinSettingRepoStub(nil), nil), nil, nil)
	_, err := svc.GetStatus(context.Background(), 1)
	require.Error(t, err)
	require.Equal(t, CheckinReasonRepositoryMissing, reasonOf(err))
}

func timezoneStartOfToday() time.Time {
	return timezone.StartOfDay(timezone.Now())
}

type checkinBillingCacheStub struct {
	mu      sync.Mutex
	userIDs []int64
}

func (s *checkinBillingCacheStub) InvalidateUserBalance(_ context.Context, userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userIDs = append(s.userIDs, userID)
	return nil
}

type checkinAuthCacheStub struct {
	mu      sync.Mutex
	userIDs []int64
}

func (s *checkinAuthCacheStub) InvalidateAuthCacheByUserID(_ context.Context, userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userIDs = append(s.userIDs, userID)
}

func TestCheckinServiceSimpleModeEnforced(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true
	settings.Risk.CaptchaEnabled = true

	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	store := newCheckinSettingRepoStub(map[string]string{
		SettingKeyCheckinEnabled: "true",
		SettingKeyCheckinConfig:  mustMarshalCheckinSettings(t, settings),
	})
	settingSvc := NewSettingService(store, &config.Config{RunMode: config.RunModeSimple})
	svc := NewCheckinService(settingSvc, repo, nil)

	status, err := svc.GetStatus(context.Background(), 7)
	require.NoError(t, err)
	require.False(t, status.Enabled)
	require.False(t, status.CaptchaRequired)
	require.False(t, status.Eligibility.Eligible)
	require.Equal(t, CheckinReasonDisabled, status.Eligibility.ReasonCode)

	_, err = svc.Checkin(context.Background(), 7, "1.2.3.4", CheckinCaptchaProof{})
	require.Error(t, err)
	require.Equal(t, CheckinReasonDisabled, reasonOf(err))
	require.Empty(t, repo.executed)
}

func TestCheckinServiceCaptchaConflictTakesPrecedenceOverMissingProof(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true
	settings.Risk.CaptchaEnabled = true

	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	svc, store := newCheckinTestService(t, settings, repo)
	require.NoError(t, store.Set(context.Background(), SettingKeyTurnstileEnabled, "true"))
	require.NoError(t, store.Set(context.Background(), SettingKeyTurnstileSecretKey, "secret"))
	require.NoError(t, store.Set(context.Background(), SettingKeyTencentCaptchaEnabled, "true"))
	require.NoError(t, store.Set(context.Background(), SettingKeyTencentCaptchaAppID, "app-id"))

	captcha := &checkinCaptchaStub{}
	svc.SetCaptchaVerifier(captcha)

	_, err := svc.Checkin(context.Background(), 7, "1.2.3.4", CheckinCaptchaProof{})
	require.Error(t, err)
	require.Equal(t, CheckinReasonCaptchaNotConfigured, reasonOf(err))
	require.Zero(t, captcha.calls)
}

func TestCheckinServiceAliyunCaptchaParamMapsToTurnstileToken(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true
	settings.Risk.CaptchaEnabled = true

	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	svc, store := newCheckinTestService(t, settings, repo)
	require.NoError(t, store.Set(context.Background(), SettingKeyAliyunCaptchaEnabled, "true"))
	require.NoError(t, store.Set(context.Background(), SettingKeyAliyunCaptchaAccessKeyID, "ak"))
	require.NoError(t, store.Set(context.Background(), SettingKeyAliyunCaptchaAccessKeySecret, "sk"))
	require.NoError(t, store.Set(context.Background(), SettingKeyAliyunCaptchaSceneID, "scene"))

	captcha := &checkinCaptchaStub{}
	svc.SetCaptchaVerifier(captcha)

	_, err := svc.Checkin(context.Background(), 7, "198.51.100.2", CheckinCaptchaProof{
		AliyunCaptchaParam: "aliyun-verify-param-xyz",
	})
	require.NoError(t, err)
	require.Equal(t, 1, captcha.calls)
	require.Equal(t, "aliyun-verify-param-xyz", captcha.proof.TurnstileToken)
	require.Equal(t, "198.51.100.2", captcha.ip)
}

func TestCheckinServicePassesNowAndInvalidatesCachesOnlyOnSuccess(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.Enabled = true
	settings.BaseAmount = 0.25

	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	svc, _ := newCheckinTestService(t, settings, repo)
	billingCache := &checkinBillingCacheStub{}
	authCache := &checkinAuthCacheStub{}
	svc.SetCacheInvalidators(billingCache, authCache)

	_, err := svc.Checkin(context.Background(), 7, "198.51.100.2", CheckinCaptchaProof{})
	require.NoError(t, err)
	require.Len(t, repo.executed, 1)
	require.False(t, repo.executed[0].Now.IsZero())
	require.Equal(t, []int64{7}, billingCache.userIDs)
	require.Equal(t, []int64{7}, authCache.userIDs)

	// Repo error must not trigger cache invalidation.
	repo.executeErr = errCheckinStub
	_, err = svc.Checkin(context.Background(), 8, "198.51.100.3", CheckinCaptchaProof{})
	require.ErrorIs(t, err, errCheckinStub)
	require.Equal(t, []int64{7}, billingCache.userIDs)
	require.Equal(t, []int64{7}, authCache.userIDs)
}

func TestGenerateCheckinBaseAmountClampsPrecisionUpperBound(t *testing.T) {
	settings := DefaultCheckinSettings()
	settings.RewardMode = CheckinRewardModeRandom
	settings.MinAmount = 0.01
	settings.MaxAmount = 0.09
	settings.AmountPrecision = 12 // > CheckinAmountPrecisionMax (4)

	for i := 0; i < 100; i++ {
		amount, err := generateCheckinBaseAmount(settings)
		require.NoError(t, err)
		require.GreaterOrEqual(t, amount, 0.01)
		require.LessOrEqual(t, amount, 0.09)
		require.InDelta(t, amount, checkinRoundTo(amount, CheckinAmountPrecisionMax), 1e-12)
	}
}

func TestCheckinServiceAdminListRecordsValidatesDates(t *testing.T) {
	settings := DefaultCheckinSettings()
	repo := &checkinRepoStub{snapshot: activeSnapshot()}
	svc, _ := newCheckinTestService(t, settings, repo)

	_, _, err := svc.AdminListRecords(context.Background(), AdminCheckinRecordFilter{
		StartDate: "2026/11/01",
		Params:    pagination.PaginationParams{Page: 1, PageSize: 20},
	})
	require.Error(t, err)
	require.Equal(t, "CHECKIN_INVALID_DATE", reasonOf(err))

	_, _, err = svc.AdminListRecords(context.Background(), AdminCheckinRecordFilter{
		StartDate: "2026-11-01",
		EndDate:   "bad-date",
		Params:    pagination.PaginationParams{Page: 1, PageSize: 20},
	})
	require.Error(t, err)
	require.Equal(t, "CHECKIN_INVALID_DATE", reasonOf(err))

	_, _, err = svc.AdminListRecords(context.Background(), AdminCheckinRecordFilter{
		StartDate: "2026-11-01",
		EndDate:   "2026-11-30",
		Params:    pagination.PaginationParams{Page: 1, PageSize: 20},
	})
	require.NoError(t, err)
}
