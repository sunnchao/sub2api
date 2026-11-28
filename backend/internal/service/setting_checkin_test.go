package service

import (
	"context"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// reasonOf 返回 ApplicationError 的 reason（非 ApplicationError 返回空串）。
func reasonOf(err error) string {
	return infraerrors.Reason(err)
}

// checkinSettingRepoStub 是内存版 SettingRepository，仅实现签到配置读取所需的子集。
type checkinSettingRepoStub struct {
	SettingRepository

	mu               sync.Mutex
	values           map[string]string
	setMultipleCalls int
}

func newCheckinSettingRepoStub(values map[string]string) *checkinSettingRepoStub {
	copied := make(map[string]string, len(values))
	for k, v := range values {
		copied[k] = v
	}
	return &checkinSettingRepoStub{values: copied}
}

func (s *checkinSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}

func (s *checkinSettingRepoStub) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[key] = value
	return nil
}

func (s *checkinSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]string)
	}
	for k, v := range settings {
		s.values[k] = v
	}
	s.setMultipleCalls++
	return nil
}

func (s *checkinSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if v, ok := s.values[key]; ok {
			out[key] = v
		}
	}
	return out, nil
}

func (s *checkinSettingRepoStub) snapshot() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for k, v := range s.values {
		out[k] = v
	}
	return out
}

func TestDefaultCheckinSettings(t *testing.T) {
	settings := DefaultCheckinSettings()
	require.False(t, settings.Enabled)
	require.Equal(t, CheckinRewardModeFixed, settings.RewardMode)
	require.Equal(t, CheckinValidityDaysDefault, settings.ValidityDays)
	require.Equal(t, CheckinAmountPrecisionDefault, settings.AmountPrecision)
	require.Empty(t, settings.Milestones)
	require.False(t, settings.IsRandomReward())
	require.Equal(t, 0, settings.MaxMilestoneDays())
}

func TestNormalizeCheckinSettingsDefaultsAndClamps(t *testing.T) {
	normalized, err := NormalizeCheckinSettings(&CheckinSettings{
		RewardMode:      "FIXED",
		BaseAmount:      0.123456,
		AmountPrecision: 99,
		ValidityDays:    -3,
	})
	require.NoError(t, err)
	require.Equal(t, CheckinRewardModeFixed, normalized.RewardMode)
	require.Equal(t, CheckinAmountPrecisionMax, normalized.AmountPrecision)
	require.InDelta(t, 0.1235, normalized.BaseAmount, 1e-9)
	require.Equal(t, CheckinValidityDaysMin, normalized.ValidityDays)

	normalized, err = NormalizeCheckinSettings(&CheckinSettings{
		RewardMode:   CheckinRewardModeFixed,
		ValidityDays: 10000,
	})
	require.NoError(t, err)
	require.Equal(t, CheckinValidityDaysMax, normalized.ValidityDays)
	// 零值精度回落到默认 2 位。
	require.Equal(t, CheckinAmountPrecisionDefault, normalized.AmountPrecision)
}

func TestNormalizeCheckinSettingsRejectsInvalidInput(t *testing.T) {
	_, err := NormalizeCheckinSettings(&CheckinSettings{RewardMode: "lottery"})
	require.Error(t, err)
	require.Equal(t, "CHECKIN_INVALID_REWARD_MODE", reasonOf(err))

	_, err = NormalizeCheckinSettings(&CheckinSettings{
		RewardMode: CheckinRewardModeRandom,
		MinAmount:  0.50,
		MaxAmount:  0.10,
	})
	require.Error(t, err)
	require.Equal(t, "CHECKIN_INVALID_RANDOM_RANGE", reasonOf(err))

	_, err = NormalizeCheckinSettings(&CheckinSettings{
		RewardMode: CheckinRewardModeRandom,
		MinAmount:  0.20,
		MaxAmount:  0,
	})
	require.Error(t, err)
	require.Equal(t, "CHECKIN_INVALID_RANDOM_RANGE", reasonOf(err))

	_, err = NormalizeCheckinSettings(&CheckinSettings{
		RewardMode: CheckinRewardModeRandom,
		MinAmount:  0,
		MaxAmount:  0.20,
	})
	require.Error(t, err)
	require.Equal(t, "CHECKIN_INVALID_AMOUNT", reasonOf(err))

	// Both <= 0 defaults random range to [0.01, 0.10].
	defaultRandom, err := NormalizeCheckinSettings(&CheckinSettings{
		RewardMode: CheckinRewardModeRandom,
	})
	require.NoError(t, err)
	require.InDelta(t, 0.01, defaultRandom.MinAmount, 1e-9)
	require.InDelta(t, 0.10, defaultRandom.MaxAmount, 1e-9)

	_, err = NormalizeCheckinSettings(&CheckinSettings{
		RewardMode: CheckinRewardModeFixed,
		BaseAmount: -1,
	})
	require.Error(t, err)
	require.Equal(t, "CHECKIN_INVALID_AMOUNT", reasonOf(err))

	_, err = NormalizeCheckinSettings(&CheckinSettings{
		Milestones: []CheckinMilestoneRule{
			{Days: 3, BonusAmount: 0.5},
			{Days: 3, BonusAmount: 1},
		},
	})
	require.Error(t, err)
	require.Equal(t, "CHECKIN_DUPLICATE_MILESTONE", reasonOf(err))

	_, err = NormalizeCheckinSettings(&CheckinSettings{
		Milestones: []CheckinMilestoneRule{{Days: 0, BonusAmount: 0.5}},
	})
	require.Error(t, err)
	require.Equal(t, "CHECKIN_INVALID_MILESTONE", reasonOf(err))

	_, err = NormalizeCheckinSettings(&CheckinSettings{
		Milestones: []CheckinMilestoneRule{{Days: 1, BonusAmount: 0.5}},
	})
	require.Error(t, err)
	require.Equal(t, "CHECKIN_INVALID_MILESTONE", reasonOf(err))

	_, err = NormalizeCheckinSettings(&CheckinSettings{
		Milestones: []CheckinMilestoneRule{{Days: 3, BonusAmount: 0}},
	})
	require.Error(t, err)
	require.Equal(t, "CHECKIN_INVALID_MILESTONE_BONUS", reasonOf(err))
}

func TestNormalizeCheckinSettingsSortsMilestonesAndSources(t *testing.T) {
	normalized, err := NormalizeCheckinSettings(&CheckinSettings{
		Milestones: []CheckinMilestoneRule{
			{Days: 30, BonusAmount: 2},
			{Days: 3, BonusAmount: 0.5},
			{Days: 7, BonusAmount: 1},
		},
		Eligibility: CheckinEligibilityConfig{
			AllowedSignupSources: []string{" GitHub ", "email", "email", ""},
		},
	})
	require.NoError(t, err)
	require.Len(t, normalized.Milestones, 3)
	require.Equal(t, []int{3, 7, 30}, []int{
		normalized.Milestones[0].Days,
		normalized.Milestones[1].Days,
		normalized.Milestones[2].Days,
	})
	require.Equal(t, []string{"email", "github"}, normalized.Eligibility.AllowedSignupSources)
	require.Equal(t, 30, normalized.MaxMilestoneDays())

	bonus, ok := normalized.MilestoneBonus(7)
	require.True(t, ok)
	require.InDelta(t, 1.0, bonus, 1e-9)
	_, ok = normalized.MilestoneBonus(8)
	require.False(t, ok)
}

func TestGetCheckinSettingsFallsBackToDefaults(t *testing.T) {
	svc := NewSettingService(newCheckinSettingRepoStub(nil), nil)

	settings, err := svc.GetCheckinSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.Equal(t, CheckinRewardModeFixed, settings.RewardMode)
	require.Equal(t, CheckinValidityDaysDefault, settings.ValidityDays)
}

func TestGetCheckinSettingsIgnoresCorruptedJSON(t *testing.T) {
	svc := NewSettingService(newCheckinSettingRepoStub(map[string]string{
		SettingKeyCheckinEnabled: "true",
		SettingKeyCheckinConfig:  "{not-json",
	}), nil)

	settings, err := svc.GetCheckinSettings(context.Background())
	require.NoError(t, err)
	// 总开关仍以独立布尔键为准，其余回落到默认值。
	require.True(t, settings.Enabled)
	require.Equal(t, CheckinRewardModeFixed, settings.RewardMode)
}

func TestSetCheckinSettingsPersistsEnabledFlag(t *testing.T) {
	repo := newCheckinSettingRepoStub(nil)
	svc := NewSettingService(repo, nil)

	saved, err := svc.SetCheckinSettings(context.Background(), &CheckinSettings{
		Enabled:      true,
		RewardMode:   CheckinRewardModeRandom,
		MinAmount:    0.1,
		MaxAmount:    0.3,
		ValidityDays: 2,
	})
	require.NoError(t, err)
	require.True(t, saved.Enabled)
	require.Equal(t, CheckinRewardModeRandom, saved.RewardMode)
	require.Nil(t, saved.Warnings)

	values := repo.snapshot()
	require.Equal(t, "true", values[SettingKeyCheckinEnabled])
	require.Contains(t, values[SettingKeyCheckinConfig], `"reward_mode":"random"`)

	reloaded, err := svc.GetCheckinSettings(context.Background())
	require.NoError(t, err)
	require.True(t, reloaded.Enabled)
	require.Equal(t, CheckinRewardModeRandom, reloaded.RewardMode)
	require.Equal(t, 2, reloaded.ValidityDays)

	require.True(t, svc.IsCheckinEnabled(context.Background()))

	_, err = svc.SetCheckinSettings(context.Background(), &CheckinSettings{Enabled: false})
	require.NoError(t, err)
	require.Equal(t, "false", repo.snapshot()[SettingKeyCheckinEnabled])
	require.False(t, svc.IsCheckinEnabled(context.Background()))
}

func TestSetCheckinSettingsRejectsInvalidConfig(t *testing.T) {
	repo := newCheckinSettingRepoStub(nil)
	svc := NewSettingService(repo, nil)

	_, err := svc.SetCheckinSettings(context.Background(), &CheckinSettings{RewardMode: "nope"})
	require.Error(t, err)
	require.Empty(t, repo.snapshot())
}

func TestCheckinConfigWarningsWhenCaptchaMissingProvider(t *testing.T) {
	svc := NewSettingService(newCheckinSettingRepoStub(nil), nil)

	settings, err := svc.SetCheckinSettings(context.Background(), &CheckinSettings{
		Enabled: true,
		Risk:    CheckinRiskConfig{CaptchaEnabled: true},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"captcha_enabled_without_provider"}, settings.Warnings)
}

func TestSetCheckinSettingsCallsSetMultipleAndNotifySettingsUpdated(t *testing.T) {
	repo := newCheckinSettingRepoStub(nil)
	svc := NewSettingService(repo, &config.Config{})
	notifyCount := 0
	svc.SetOnUpdateCallback(func() {
		notifyCount++
	})

	_, err := svc.SetCheckinSettings(context.Background(), &CheckinSettings{
		Enabled:    true,
		BaseAmount: 0.25,
	})
	require.NoError(t, err)
	require.Equal(t, 1, repo.setMultipleCalls)
	require.Equal(t, 1, notifyCount)
}

func TestSettingServiceIsSimpleModeAndDefaultCheckinEnabled(t *testing.T) {
	repo := newCheckinSettingRepoStub(nil)
	simpleSvc := NewSettingService(repo, &config.Config{RunMode: config.RunModeSimple})
	require.True(t, simpleSvc.IsSimpleMode())

	stdSvc := NewSettingService(repo, &config.Config{RunMode: config.RunModeStandard})
	require.False(t, stdSvc.IsSimpleMode())
	require.NoError(t, stdSvc.InitializeDefaultSettings(context.Background()))
	require.Equal(t, "false", repo.snapshot()[SettingKeyCheckinEnabled])
}
