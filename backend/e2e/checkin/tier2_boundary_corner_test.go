//go:build e2e

package checkin_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestTier2_BoundaryAndCornerCases(t *testing.T) {
	// ---------------- RF1: Schema & Constraint Boundaries (5 subtests) ----------------
	t.Run("RF1_B01_RejectInitialAmountNonPositive", func(t *testing.T) {
		env := newE2EEnv(t)
		u := env.createTestUser(0)
		_, err := env.db.ExecContext(context.Background(), `
			INSERT INTO user_expiring_balance_lots
				(user_id, source_type, source_id, initial_amount, remaining_amount, expires_at, status)
			VALUES ($1, 'e2e_seed', 990001, 0, 0, NOW() + INTERVAL '1 day', 'active')
		`, u.ID)
		require.Error(t, err)
		var pqErr *pq.Error
		require.ErrorAs(t, err, &pqErr)
		require.Equal(t, pq.ErrorCode("23514"), pqErr.Code)
	})

	t.Run("RF1_B02_RejectRemainingAmountNegative", func(t *testing.T) {
		env := newE2EEnv(t)
		u := env.createTestUser(0)
		_, err := env.db.ExecContext(context.Background(), `
			INSERT INTO user_expiring_balance_lots
				(user_id, source_type, source_id, initial_amount, remaining_amount, expires_at, status)
			VALUES ($1, 'e2e_seed', 990002, 1.0, -0.01, NOW() + INTERVAL '1 day', 'active')
		`, u.ID)
		require.Error(t, err)
		var pqErr *pq.Error
		require.ErrorAs(t, err, &pqErr)
		require.Equal(t, pq.ErrorCode("23514"), pqErr.Code)
	})

	t.Run("RF1_B03_RejectFrozenAmountNegative", func(t *testing.T) {
		env := newE2EEnv(t)
		u := env.createTestUser(0)
		_, err := env.db.ExecContext(context.Background(), `
			INSERT INTO user_expiring_balance_lots
				(user_id, source_type, source_id, initial_amount, remaining_amount, frozen_amount, expires_at, status)
			VALUES ($1, 'e2e_seed', 990003, 1.0, 1.0, -0.01, NOW() + INTERVAL '1 day', 'active')
		`, u.ID)
		require.Error(t, err)
		var pqErr *pq.Error
		require.ErrorAs(t, err, &pqErr)
		require.Equal(t, pq.ErrorCode("23514"), pqErr.Code)
	})

	t.Run("RF1_B04_RejectDuplicateUserCheckinDate", func(t *testing.T) {
		env := newE2EEnv(t)
		u := env.createTestUser(0)
		env.seedCheckinRecordSQL(u.ID, "2026-11-20", 1, 1, 0.10)
		_, err := env.db.ExecContext(context.Background(), `
			INSERT INTO user_checkin_records
				(user_id, checkin_date, streak_days, milestone_cycle_day, base_amount, milestone_bonus_amount, total_amount, validity_days, expires_at, client_ip)
			VALUES ($1, '2026-11-20'::date, 1, 1, 0.10, 0, 0.10, 1, NOW() + INTERVAL '1 day', '127.0.0.1')
		`, u.ID)
		require.Error(t, err)
		var pqErr *pq.Error
		require.ErrorAs(t, err, &pqErr)
		require.Equal(t, pq.ErrorCode("23505"), pqErr.Code)
	})

	t.Run("RF1_B05_RejectDuplicateAllocationRefSeq", func(t *testing.T) {
		env := newE2EEnv(t)
		u := env.createTestUser(0)
		_, err := env.db.ExecContext(context.Background(), `
			INSERT INTO user_expiring_balance_allocations (user_id, ref_type, ref_key, alloc_seq, lot_id, allocated_amount)
			VALUES ($1, 'payment_order', 'dup-key-1', 1, NULL, 1.00)
		`, u.ID)
		require.NoError(t, err)
		_, err = env.db.ExecContext(context.Background(), `
			INSERT INTO user_expiring_balance_allocations (user_id, ref_type, ref_key, alloc_seq, lot_id, allocated_amount)
			VALUES ($1, 'payment_order', 'dup-key-1', 1, NULL, 1.00)
		`, u.ID)
		require.Error(t, err)
		var pqErr *pq.Error
		require.ErrorAs(t, err, &pqErr)
		require.Equal(t, pq.ErrorCode("23505"), pqErr.Code)
	})

	// ---------------- RF2: Settings, Authz & Simple Mode Boundaries (5 subtests) ----------------
	t.Run("RF2_B01_PostCheckinWhenDisabled403", func(t *testing.T) {
		env := newE2EEnv(t)
		u := env.createTestUser(0)
		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusForbidden, res.Status)
		require.Equal(t, "CHECKIN_DISABLED", res.Env.Reason)
	})

	t.Run("RF2_B02_NonAdminJWTOnAdminCheckin403", func(t *testing.T) {
		env := newE2EEnv(t)
		u := env.createTestUser(0)
		require.Equal(t, http.StatusForbidden, env.apiGet("/api/v1/admin/checkin/config", u.Token).Status)
		require.Equal(t, http.StatusForbidden, env.apiPut("/api/v1/admin/checkin/config", u.Token, defaultEnabledCheckinConfig(0.10)).Status)
		require.Equal(t, http.StatusForbidden, env.apiGet("/api/v1/admin/checkin/stats", u.Token).Status)
		require.Equal(t, http.StatusForbidden, env.apiGet("/api/v1/admin/checkin/records", u.Token).Status)
	})

	t.Run("RF2_B03_SimpleModeBlocksCheckin", func(t *testing.T) {
		env := newE2EEnv(t, withRunMode("simple"))
		u := env.createTestUser(0)

		// Even if admin sets enabled=true in config, simple mode must block check-in.
		cfg := defaultEnabledCheckinConfig(0.10)
		st, _ := env.adminPut("/api/v1/admin/checkin/config", cfg)
		require.Equal(t, http.StatusOK, st)

		pubRes := env.publicGet("/api/v1/settings/public")
		require.Equal(t, http.StatusOK, pubRes.Status)
		pub := decodeData[map[string]any](t, pubRes.Env)
		require.Equal(t, false, pub["checkin_enabled"])

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusForbidden, res.Status)
		require.Equal(t, "CHECKIN_DISABLED", res.Env.Reason)
	})

	t.Run("RF2_B04_InvalidAdminCheckinConfigRejected400", func(t *testing.T) {
		env := newE2EEnv(t)
		cases := []map[string]any{
			func() map[string]any {
				c := defaultEnabledCheckinConfig(0.10)
				c["reward_mode"] = "bogus"
				return c
			}(),
			func() map[string]any {
				c := defaultEnabledCheckinConfig(0.10)
				c["validity_days"] = 0
				return c
			}(),
			func() map[string]any {
				c := defaultEnabledCheckinConfig(0.10)
				c["validity_days"] = 366
				return c
			}(),
			func() map[string]any {
				c := defaultEnabledCheckinConfig(0.10)
				c["milestones"] = []map[string]any{
					{"days": 3, "bonus_amount": 0.50},
					{"days": 3, "bonus_amount": 1.00},
				}
				return c
			}(),
		}
		for idx, tc := range cases {
			st, _ := env.adminPut("/api/v1/admin/checkin/config", tc)
			require.Equal(t, http.StatusBadRequest, st, "case %d should return 400", idx)
		}
	})

	t.Run("RF2_B05_CaptchaEnabledWithoutProviderEmitsWarnings", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["risk"] = map[string]any{"captcha_enabled": true}
		st, putEnv := env.adminPut("/api/v1/admin/checkin/config", cfg)
		require.Equal(t, http.StatusOK, st)
		data := decodeData[struct {
			Warnings []string `json:"warnings"`
		}](t, putEnv)
		require.NotEmpty(t, data.Warnings, "expected warning when captcha_enabled=true without configured provider")
	})

	// ---------------- RF3: Precision, Validity & Expiration Boundaries (5 subtests) ----------------
	t.Run("RF3_B01_EightDecimalPrecisionNoFloatDrift", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		u := env.createTestUser(0)
		// 0.12345678 lot; claude-sonnet-4-5 input_cost_per_token=3e-6, output_cost_per_token=1.5e-5 -> 1 input + 1 output = 0.00001800
		lotID := env.seedExpiringLotSQL(u.ID, 0.12345678, time.Now().Add(24*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		env.anthropic.SetTokens(1, 1) // 0.000003 + 0.000015 = 0.000018
		res := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "8dec"}},
		})
		require.Equal(t, http.StatusOK, res.Status)

		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			return approxEqual(env.getLotDB(lotID).ConsumedAmount, 0.000018)
		}, "lot should consume 0.000018")

		lot := env.getLotDB(lotID)
		require.InDelta(t, 0.12343878, lot.RemainingAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotID)
	})

	t.Run("RF3_B02_ValidityDays1Vs365ExpiresAtCalculation", func(t *testing.T) {
		env := newE2EEnv(t)
		loc := mustLoadLoc(t, env.timezone)

		cfg1 := defaultEnabledCheckinConfig(0.10)
		cfg1["validity_days"] = 1
		env.enableCheckinConfig(cfg1)
		u1 := env.createTestUser(0)
		before1 := time.Now().In(loc)
		r1 := env.apiPost("/api/v1/checkin", u1.Token, map[string]any{})
		require.Equal(t, http.StatusOK, r1.Status)
		d1 := decodeData[struct {
			Record struct {
				ExpiresAt time.Time `json:"expires_at"`
			} `json:"record"`
		}](t, r1.Env)
		require.True(t, d1.Record.ExpiresAt.Equal(startOfDayInLoc(before1, loc).AddDate(0, 0, 1)))

		cfg365 := defaultEnabledCheckinConfig(0.10)
		cfg365["validity_days"] = 365
		env.enableCheckinConfig(cfg365)
		u2 := env.createTestUser(0)
		before2 := time.Now().In(loc)
		r2 := env.apiPost("/api/v1/checkin", u2.Token, map[string]any{})
		require.Equal(t, http.StatusOK, r2.Status)
		d2 := decodeData[struct {
			Record struct {
				ExpiresAt time.Time `json:"expires_at"`
			} `json:"record"`
		}](t, r2.Env)
		require.True(t, d2.Record.ExpiresAt.Equal(startOfDayInLoc(before2, loc).AddDate(0, 0, 365)))

		// Verify timezone.Location() drives expiration boundary when server runs in UTC.
		utcEnv := newE2EEnv(t, withTimezone("UTC"))
		utcLoc := mustLoadLoc(t, "UTC")
		utcEnv.enableCheckinConfig(defaultEnabledCheckinConfig(0.10))
		u3 := utcEnv.createTestUser(0)
		beforeUTC := time.Now().In(utcLoc)
		r3 := utcEnv.apiPost("/api/v1/checkin", u3.Token, map[string]any{})
		require.Equal(t, http.StatusOK, r3.Status)
		d3 := decodeData[struct {
			Record struct {
				ExpiresAt time.Time `json:"expires_at"`
			} `json:"record"`
		}](t, r3.Env)
		require.True(t, d3.Record.ExpiresAt.Equal(startOfDayInLoc(beforeUTC, utcLoc).AddDate(0, 0, 1)))
	})

	t.Run("RF3_B03_HalfOpenExpiresAtBoundary", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		// Lot with expires_at <= NOW() is expired and unusable even if status='active'.
		uExp := env.createTestUser(0)
		env.seedExpiringLotSQL(uExp.ID, 1.00, time.Now().Add(-50*time.Millisecond), "active")
		_, keyExp := env.createAPIKeyForUser(uExp.Token, &groupID)
		resExp := env.gatewayPost("/v1/messages", keyExp, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "expired"}},
		})
		require.Equal(t, http.StatusForbidden, resExp.Status, "expired boundary lot must not be spendable")

		// Lot with expires_at > NOW() (+5s) is active and spendable.
		uAct := env.createTestUser(0)
		env.seedExpiringLotSQL(uAct.ID, 1.00, time.Now().Add(5*time.Second), "active")
		_, keyAct := env.createAPIKeyForUser(uAct.Token, &groupID)
		env.anthropic.SetTokens(100, 100)
		resAct := env.gatewayPost("/v1/messages", keyAct, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "active"}},
		})
		require.Equal(t, http.StatusOK, resAct.Status, string(resAct.Body))
	})

	t.Run("RF3_B04_MinEqualsMaxDegenerateRandomRange", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.25)
		cfg["reward_mode"] = "random"
		cfg["min_amount"] = 0.25
		cfg["max_amount"] = 0.25
		cfg["amount_precision"] = 2
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status)
		out := decodeData[struct {
			Record struct {
				BaseAmount float64 `json:"base_amount"`
			} `json:"record"`
		}](t, res.Env)
		require.InDelta(t, 0.25, out.Record.BaseAmount, 1e-8)
	})

	t.Run("RF3_B05_EmptyListsReturnEmptySliceAndStatusFilter", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.10))
		u := env.createTestUser(0)

		month := time.Now().In(mustLoadLoc(t, env.timezone)).Format("2006-01")
		cal := env.apiGet("/api/v1/checkin/calendar?month="+month, u.Token)
		require.Equal(t, http.StatusOK, cal.Status)
		require.Contains(t, string(cal.Env.Data), `"checked_dates":[]`)

		rec := env.apiGet("/api/v1/checkin/records", u.Token)
		require.Equal(t, http.StatusOK, rec.Status)
		require.Contains(t, string(rec.Env.Data), `"items":[]`)

		lotEmpty := env.apiGet("/api/v1/checkin/lots", u.Token)
		require.Equal(t, http.StatusOK, lotEmpty.Status)
		require.Contains(t, string(lotEmpty.Env.Data), `"items":[]`)

		env.seedExpiringLotSQL(u.ID, 1.0, time.Now().Add(24*time.Hour), "active")
		env.seedExpiringLotSQL(u.ID, 1.0, time.Now().Add(24*time.Hour), "exhausted")
		env.seedExpiringLotSQL(u.ID, 1.0, time.Now().Add(-24*time.Hour), "expired")

		for _, tc := range []struct {
			status string
			want   int64
		}{
			{"all", 3},
			{"active", 1},
			{"exhausted", 1},
			{"expired", 1},
		} {
			r := env.apiGet("/api/v1/checkin/lots?status="+tc.status, u.Token)
			require.Equal(t, http.StatusOK, r.Status)
			p := decodeData[struct {
				Total int64 `json:"total"`
			}](t, r.Env)
			require.Equal(t, tc.want, p.Total, "status=%s", tc.status)
		}
	})

	// ---------------- RF4: Streaks & Milestone Cycle Boundaries (5 subtests) ----------------
	t.Run("RF4_B01_MissedDayResetsStreakTo1", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.10))
		u := env.createTestUser(0)

		twoDaysAgo := time.Now().In(mustLoadLoc(t, env.timezone)).AddDate(0, 0, -2).Format("2006-01-02")
		env.seedCheckinRecordSQL(u.ID, twoDaysAgo, 5, 5, 0.10)

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status)
		out := decodeData[struct {
			Record struct {
				StreakDays        int `json:"streak_days"`
				MilestoneCycleDay int `json:"milestone_cycle_day"`
			} `json:"record"`
		}](t, res.Env)
		require.Equal(t, 1, out.Record.StreakDays)
		require.Equal(t, 1, out.Record.MilestoneCycleDay)
	})

	t.Run("RF4_B02_CycleRepeatFalseStopsAfterHighestMilestone", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["milestones"] = []map[string]any{
			{"days": 2, "bonus_amount": 0.50},
			{"days": 3, "bonus_amount": 1.00},
		}
		cfg["milestone_cycle_repeat"] = false
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		yesterday := time.Now().In(mustLoadLoc(t, env.timezone)).AddDate(0, 0, -1).Format("2006-01-02")
		env.seedCheckinRecordSQL(u.ID, yesterday, 3, 3, 1.10)

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status)
		out := decodeData[struct {
			Record struct {
				StreakDays           int     `json:"streak_days"`
				MilestoneBonusAmount float64 `json:"milestone_bonus_amount"`
			} `json:"record"`
		}](t, res.Env)
		require.Equal(t, 4, out.Record.StreakDays)
		require.InDelta(t, 0.0, out.Record.MilestoneBonusAmount, 1e-8)

		st := env.apiGet("/api/v1/checkin/status", u.Token)
		require.Equal(t, http.StatusOK, st.Status)
		sData := decodeData[struct {
			NextMilestone any `json:"next_milestone"`
		}](t, st.Env)
		require.Nil(t, sData.NextMilestone)
	})

	t.Run("RF4_B03_CycleRepeatTrueWrapsToCycleDay1AndRetriggers", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["milestones"] = []map[string]any{
			{"days": 2, "bonus_amount": 0.50},
			{"days": 3, "bonus_amount": 1.00},
		}
		cfg["milestone_cycle_repeat"] = true
		env.enableCheckinConfig(cfg)

		yesterday := time.Now().In(mustLoadLoc(t, env.timezone)).AddDate(0, 0, -1).Format("2006-01-02")

		// User 1: yesterday was cycle_day=3 -> today wraps to cycle_day=1.
		u1 := env.createTestUser(0)
		env.seedCheckinRecordSQL(u1.ID, yesterday, 3, 3, 1.10)
		r1 := env.apiPost("/api/v1/checkin", u1.Token, map[string]any{})
		require.Equal(t, http.StatusOK, r1.Status)
		d1 := decodeData[struct {
			Record struct {
				StreakDays           int     `json:"streak_days"`
				MilestoneCycleDay    int     `json:"milestone_cycle_day"`
				MilestoneBonusAmount float64 `json:"milestone_bonus_amount"`
			} `json:"record"`
		}](t, r1.Env)
		require.Equal(t, 4, d1.Record.StreakDays)
		require.Equal(t, 1, d1.Record.MilestoneCycleDay)
		require.InDelta(t, 0.0, d1.Record.MilestoneBonusAmount, 1e-8)

		// User 2: yesterday was wrapped cycle_day=1 -> today hits cycle_day=2 and re-triggers 0.50 bonus.
		u2 := env.createTestUser(0)
		env.seedCheckinRecordSQL(u2.ID, yesterday, 4, 1, 0.10)
		r2 := env.apiPost("/api/v1/checkin", u2.Token, map[string]any{})
		require.Equal(t, http.StatusOK, r2.Status)
		d2 := decodeData[struct {
			Record struct {
				StreakDays           int     `json:"streak_days"`
				MilestoneCycleDay    int     `json:"milestone_cycle_day"`
				MilestoneBonusAmount float64 `json:"milestone_bonus_amount"`
			} `json:"record"`
		}](t, r2.Env)
		require.Equal(t, 5, d2.Record.StreakDays)
		require.Equal(t, 2, d2.Record.MilestoneCycleDay)
		require.InDelta(t, 0.50, d2.Record.MilestoneBonusAmount, 1e-8)
	})

	t.Run("RF4_B04_NoRetroactiveCheckinAccepted", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.10))
		u := env.createTestUser(0)

		retro := env.apiPost("/api/v1/checkin/retroactive", u.Token, map[string]any{"date": "2020-01-01"})
		require.Equal(t, http.StatusNotFound, retro.Status)

		today := time.Now().In(mustLoadLoc(t, env.timezone)).Format("2006-01-02")
		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{"checkin_date": "2020-01-01", "date": "2020-01-01"})
		require.Equal(t, http.StatusOK, res.Status)
		out := decodeData[struct {
			Record struct {
				CheckinDate string `json:"checkin_date"`
			} `json:"record"`
		}](t, res.Env)
		require.Equal(t, today, out.Record.CheckinDate)
	})

	t.Run("RF4_B05_DuplicateSameDayReturns409", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.10))
		u := env.createTestUser(0)

		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u.Token, map[string]any{}).Status)
		dup := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusConflict, dup.Status)
		require.Equal(t, "CHECKIN_ALREADY_DONE", dup.Env.Reason)
	})

	// ---------------- RF5: Eligibility Gate Boundaries (5 subtests) ----------------
	t.Run("RF5_B01_MinAccountAgeDaysBoundary", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["eligibility"] = map[string]any{"min_account_age_days": 7}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		_, err := env.db.ExecContext(context.Background(), `UPDATE users SET created_at = NOW() - INTERVAL '7 days' + INTERVAL '10 seconds' WHERE id = $1`, u.ID)
		require.NoError(t, err)

		failRes := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusForbidden, failRes.Status)
		require.Equal(t, "CHECKIN_INELIGIBLE_ACCOUNT_AGE", failRes.Env.Reason)

		_, err = env.db.ExecContext(context.Background(), `UPDATE users SET created_at = NOW() - INTERVAL '7 days' - INTERVAL '10 seconds' WHERE id = $1`, u.ID)
		require.NoError(t, err)
		okRes := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, okRes.Status)
	})

	t.Run("RF5_B02_MinTotalRechargedBoundary", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["eligibility"] = map[string]any{"min_total_recharged": 10.00}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		_, err := env.db.ExecContext(context.Background(), `UPDATE users SET total_recharged = 9.99 WHERE id = $1`, u.ID)
		require.NoError(t, err)
		failRes := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusForbidden, failRes.Status)
		require.Equal(t, "CHECKIN_INELIGIBLE_RECHARGE", failRes.Env.Reason)

		_, err = env.db.ExecContext(context.Background(), `UPDATE users SET total_recharged = 10.00 WHERE id = $1`, u.ID)
		require.NoError(t, err)
		okRes := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, okRes.Status)
	})

	t.Run("RF5_B03_MaxAvailableBalanceExactCapBoundary", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["eligibility"] = map[string]any{"max_available_balance": 5.00}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(3.00)
		env.seedExpiringLotSQL(u.ID, 2.00, time.Now().Add(24*time.Hour), "active")

		failRes := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusForbidden, failRes.Status)
		require.Equal(t, "CHECKIN_BALANCE_CAP_EXCEEDED", failRes.Env.Reason)

		env.setUserRegularBalanceSQL(u.ID, 2.99)
		okRes := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, okRes.Status)
	})

	t.Run("RF5_B04_AllowedSignupSourcesRejectsMismatch", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["eligibility"] = map[string]any{"allowed_signup_sources": []string{"linuxdo"}}
		env.enableCheckinConfig(cfg)

		u := env.registerUser("mismatch_src")
		failRes := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusForbidden, failRes.Status)
		require.Equal(t, "CHECKIN_INELIGIBLE_SOURCE", failRes.Env.Reason)
	})

	t.Run("RF5_B05_SuspendedDisabledUserRejected", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.10))
		u := env.createTestUser(0)

		st, _ := env.adminPut(fmt.Sprintf("/api/v1/admin/users/%d", u.ID), map[string]any{"status": "disabled"})
		require.Equal(t, http.StatusOK, st)

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, res.Status)

		var cnt int
		err := env.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM user_checkin_records WHERE user_id = $1`, u.ID).Scan(&cnt)
		require.NoError(t, err)
		require.Equal(t, 0, cnt)
	})

	// ---------------- RF6: Auth, IP Cap, Daily Budget & Captcha Boundaries (5 subtests) ----------------
	t.Run("RF6_B01_APIKeyRejectedOnAllFiveUserCheckinEndpoints", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.10))
		u := env.createTestUser(1.0)
		_, apiKey := env.createAPIKeyForUser(u.Token, nil)

		require.Equal(t, http.StatusUnauthorized, env.apiGet("/api/v1/checkin/status", apiKey).Status)
		require.Equal(t, http.StatusUnauthorized, env.apiPost("/api/v1/checkin", apiKey, map[string]any{}).Status)
		require.Equal(t, http.StatusUnauthorized, env.apiGet("/api/v1/checkin/calendar", apiKey).Status)
		require.Equal(t, http.StatusUnauthorized, env.apiGet("/api/v1/checkin/records", apiKey).Status)
		require.Equal(t, http.StatusUnauthorized, env.apiGet("/api/v1/checkin/lots", apiKey).Status)
	})

	t.Run("RF6_B02_IPLimitBoundaryNPassesNPlus1Returns429", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["risk"] = map[string]any{"max_accounts_per_ip_day": 2}
		env.enableCheckinConfig(cfg)

		hdr := map[string]string{"X-Forwarded-For": "198.51.100.77"}
		u1 := env.createTestUser(0)
		u2 := env.createTestUser(0)
		u3 := env.createTestUser(0)

		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u1.Token, map[string]any{}, hdr).Status)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u2.Token, map[string]any{}, hdr).Status)
		r3 := env.apiPost("/api/v1/checkin", u3.Token, map[string]any{}, hdr)
		require.Equal(t, http.StatusTooManyRequests, r3.Status)
		require.Equal(t, "CHECKIN_IP_LIMIT_EXCEEDED", r3.Env.Reason)
	})

	t.Run("RF6_B03_DailyBudgetBoundaryExhaustedReturns429AndGrants0", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.30)
		cfg["risk"] = map[string]any{"daily_total_budget_usd": 0.50}
		env.enableCheckinConfig(cfg)

		u1 := env.createTestUser(0)
		u2 := env.createTestUser(0)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u1.Token, map[string]any{}).Status)

		r2 := env.apiPost("/api/v1/checkin", u2.Token, map[string]any{})
		require.Equal(t, http.StatusTooManyRequests, r2.Status)
		require.Equal(t, "CHECKIN_DAILY_BUDGET_EXHAUSTED", r2.Env.Reason)
		require.Empty(t, env.listUserLotsDB(u2.ID))

		today := time.Now().In(mustLoadLoc(t, env.timezone)).Format("2006-01-02")
		var totalGranted float64
		err := env.db.QueryRowContext(context.Background(),
			`SELECT total_granted FROM checkin_daily_budgets WHERE checkin_date = $1::date`, today,
		).Scan(&totalGranted)
		require.NoError(t, err)
		require.InDelta(t, 0.30, totalGranted, 1e-8)
	})

	t.Run("RF6_B04_CaptchaEnabledNoProviderFailsClosed", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["risk"] = map[string]any{"captcha_enabled": true}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Contains(t, []int{http.StatusBadRequest, http.StatusForbidden, http.StatusServiceUnavailable}, res.Status)
		require.Equal(t, "CHECKIN_CAPTCHA_NOT_CONFIGURED", res.Env.Reason)
	})

	t.Run("RF6_B05_CaptchaTencentRejectsMissingAndBogusAndAliyunMapping", func(t *testing.T) {
		env := newE2EEnv(t)
		st, _ := env.adminPut("/api/v1/admin/settings", map[string]any{
			"tencent_captcha_enabled":          true,
			"tencent_captcha_app_id":           "1900000001",
			"tencent_captcha_app_secret_key":   "dummy-app-secret",
			"tencent_captcha_cloud_secret_id":  "dummy-cloud-id",
			"tencent_captcha_cloud_secret_key": "dummy-cloud-key",
			"tencent_captcha_region":           "cn",
		})
		require.Equal(t, http.StatusOK, st)

		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["risk"] = map[string]any{"captcha_enabled": true}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		rMissing := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.NotEqual(t, http.StatusOK, rMissing.Status)
		require.Equal(t, "CAPTCHA_REQUIRED", rMissing.Env.Reason)

		rAliyunParam := env.apiPost("/api/v1/checkin", u.Token, map[string]any{
			"aliyun_captcha_verify_param": "dummy-verify-param",
		})
		require.NotEqual(t, http.StatusOK, rAliyunParam.Status)
		require.Equal(t, "CAPTCHA_REQUIRED", rAliyunParam.Env.Reason)

		rBogus := env.apiPost("/api/v1/checkin", u.Token, map[string]any{
			"tencent_captcha_ticket":  "trerror_e2e_invalid",
			"tencent_captcha_randstr": "bogus-rand",
		})
		require.NotEqual(t, http.StatusOK, rBogus.Status)
		require.Equal(t, "CAPTCHA_INVALID", rBogus.Env.Reason)
	})

	// ---------------- RF7: Gateway Billing & Notification Boundaries (5 subtests) ----------------
	t.Run("RF7_B01_APIKeyRejectedWhenZeroRegularAndExpiredLot", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		u := env.createTestUser(0)
		env.seedExpiringLotSQL(u.ID, 1.00, time.Now().Add(-1*time.Minute), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		res := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		})
		require.Equal(t, http.StatusForbidden, res.Status)
	})

	t.Run("RF7_B02_OverdraftAllowedDoesNotReviveExpiredLot", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		u := env.createTestUser(0.10)
		expLotID := env.seedExpiringLotSQL(u.ID, 1.00, time.Now().Add(-1*time.Minute), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		env.anthropic.SetTokens(10000, 18000) // $0.30
		res := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "overdraft"}},
		})
		require.Equal(t, http.StatusOK, res.Status)

		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			reg, _, _ := env.getUserDBBalance(u.ID)
			return approxEqual(reg, -0.20)
		}, "regular balance should drop to -0.20")

		lot := env.getLotDB(expLotID)
		require.InDelta(t, 1.00, lot.RemainingAmount, 1e-8)
		require.InDelta(t, 0.0, lot.ConsumedAmount, 1e-8)
	})

	t.Run("RF7_B03_ApplyBatchMonotonicNewBalanceAcrossExpiringAndRegular", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		u := env.createTestUser(1.00)
		lotID := env.seedExpiringLotSQL(u.ID, 0.25, time.Now().Add(24*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		// Each request costs $0.15 (5000 input + 9000 output). 3 requests = $0.45 total.
		env.anthropic.SetTokens(5000, 9000)
		errs := make(chan int, 3)
		for i := 0; i < 3; i++ {
			go func() {
				r := env.gatewayPost("/v1/messages", apiKey, map[string]any{
					"model":      "claude-sonnet-4-5",
					"max_tokens": 16,
					"messages":   []map[string]string{{"role": "user", "content": "batch"}},
				})
				errs <- r.Status
			}()
		}
		for i := 0; i < 3; i++ {
			require.Equal(t, http.StatusOK, <-errs)
		}

		eventually(t, 6*time.Second, 50*time.Millisecond, func() bool {
			reg, _, _ := env.getUserDBBalance(u.ID)
			lot := env.getLotDB(lotID)
			return lot.Status == "exhausted" && approxEqual(reg, 0.80)
		}, "3x $0.15 across $0.25 expiring + $1.00 regular => $0.80 regular remaining")
		verifyLotInvariant(t, env.db, lotID)
	})

	t.Run("RF7_B04_BalanceNotifyBoundaryWithFakeSMTP", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		st, _ := env.adminPut("/api/v1/admin/settings", map[string]any{
			"smtp_host":                    env.smtpServer.host,
			"smtp_port":                    env.smtpServer.port,
			"smtp_username":                "e2e",
			"smtp_password":                "e2e",
			"smtp_from_email":              "noreply@sub2api.local",
			"smtp_from_name":               "Sub2API E2E",
			"smtp_use_tls":                 false,
			"balance_low_notify_enabled":   true,
			"balance_low_notify_threshold": 0.50,
		})
		require.Equal(t, http.StatusOK, st)

		u := env.createTestUser(0.30)
		env.seedExpiringLotSQL(u.ID, 0.30, time.Now().Add(24*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		notifyAddr := "alert_" + randomHex(4) + "@example.com"
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/user/notify-email/send-code", u.Token, map[string]any{
			"email": notifyAddr,
		}).Status)

		var code string
		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			var ok bool
			code, ok = env.smtpServer.ExtractLatestCode(notifyAddr)
			return ok
		}, "verification code should arrive at fake SMTP")

		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/user/notify-email/verify", u.Token, map[string]any{
			"email": notifyAddr,
			"code":  code,
		}).Status)
		require.Equal(t, http.StatusOK, env.apiPut("/api/v1/user", u.Token, map[string]any{
			"balance_notify_enabled": true,
		}).Status)
		env.smtpServer.Reset()

		// Total balance = 0.60 >= 0.50. Spend 0.20 -> 0.40 (< 0.50) => sends 1 alert email.
		// claude-sonnet-4-5: 10000*3e-6 + 11333*1.5e-5 -> use exact: 10000 in (0.03) + 11333 out? Better: 10000 in (0.03) + 18000 out (0.27) = 0.30!
		// Or 10000 in (0.03) + 10000 out (0.15) = 0.18! 0.60 - 0.18 = 0.42 < 0.50.
		env.anthropic.SetTokens(10000, 10000) // $0.18
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "cross below"}},
		}).Status)

		eventually(t, 8*time.Second, 100*time.Millisecond, func() bool {
			return len(env.smtpServer.Emails()) == 1
		}, "crossing below threshold must send 1 low-balance email")

		// Second deduction (0.42 -> 0.24, already < 0.50) must NOT send another email.
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "stay below"}},
		}).Status)

		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			reg, _, _ := env.getUserDBBalance(u.ID)
			return approxEqual(reg, 0.24)
		}, "second deduction should complete")
		time.Sleep(300 * time.Millisecond)
		require.Len(t, env.smtpServer.Emails(), 1, "must not re-send low-balance email when already below threshold")
	})

	t.Run("RF7_B05_ZeroCostRequestTouchesZeroLots", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		u := env.createTestUser(0)
		lotID := env.seedExpiringLotSQL(u.ID, 1.00, time.Now().Add(24*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		env.anthropic.SetTokens(0, 0)
		res := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "zero"}},
		})
		require.Equal(t, http.StatusOK, res.Status)

		time.Sleep(300 * time.Millisecond)
		lot := env.getLotDB(lotID)
		require.InDelta(t, 1.00, lot.RemainingAmount, 1e-8)
		require.InDelta(t, 0.0, lot.ConsumedAmount, 1e-8)
		var allocCnt int
		err := env.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM user_expiring_balance_allocations WHERE user_id = $1`, u.ID).Scan(&allocCnt)
		require.NoError(t, err)
		require.Equal(t, 0, allocCnt)
	})

	// ---------------- RF8: Batch Image Hold & Anti-Laundering Boundaries (5 subtests) ----------------
	t.Run("RF8_B01_InsufficientTotalBalanceRejectsBatchHoldWithoutPartialFreeze", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.60)
		u := env.createTestUser(0.20)
		lotID := env.seedExpiringLotSQL(u.ID, 0.30, time.Now().Add(24*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "c1", "prompt": "cat"},
				{"custom_id": "c2", "prompt": "dog"},
			},
		})
		require.NotEqual(t, http.StatusOK, res.Status)
		lot := env.getLotDB(lotID)
		require.InDelta(t, 0.30, lot.RemainingAmount, 1e-8)
		require.InDelta(t, 0.0, lot.FrozenAmount, 1e-8)
		_, frozen, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 0.0, frozen, 1e-8)
	})

	t.Run("RF8_B02_ReleaseAfterLotExpiredForfeitsExpiredSlice", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.60)
		env.vertex.SetMode(vertexModeStayRunning)

		u := env.createTestUser(1.00)
		lotID := env.seedExpiringLotSQL(u.ID, 0.60, time.Now().Add(24*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "c1", "prompt": "cat"},
				{"custom_id": "c2", "prompt": "dog"},
			},
		})
		require.Equal(t, http.StatusOK, res.Status)
		batch := decodeRaw[struct {
			ID string `json:"id"`
		}](t, res.Body)

		// Force lot past expiration before cancellation.
		_, err := env.db.ExecContext(context.Background(), `UPDATE user_expiring_balance_lots SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, lotID)
		require.NoError(t, err)

		cRes := env.gatewayPost("/v1/images/batches/"+batch.ID+"/cancel", apiKey, map[string]any{})
		require.Equal(t, http.StatusOK, cRes.Status, string(cRes.Body))

		reg, frozen, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 1.00, reg, 1e-8, "expired slice must NEVER leak into regular balance")
		require.InDelta(t, 0.0, frozen, 1e-8)

		lot := env.getLotDB(lotID)
		require.Equal(t, "expired", lot.Status)
		require.InDelta(t, 0.0, lot.RemainingAmount, 1e-8)
		require.InDelta(t, 0.0, lot.FrozenAmount, 1e-8)
		require.InDelta(t, 0.60, lot.ExpiredAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotID)

		allocs := env.listAllocationsDB("batch_image_hold", batch.ID)
		require.Len(t, allocs, 2)
		require.InDelta(t, 0.60, allocs[0].ForfeitedExpiredAmount, 1e-8)
		require.InDelta(t, 0.60, allocs[1].ReleasedAmount, 1e-8)
	})

	t.Run("RF8_B03_PartialCaptureAfterLotExpiredForfeitsExcessWithoutCreditingRegular", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.60)
		env.vertex.SetMode(vertexModeStayRunning)

		u := env.createTestUser(0)
		lotA := env.seedExpiringLotSQL(u.ID, 0.60, time.Now().Add(1*time.Hour), "active")
		lotB := env.seedExpiringLotSQL(u.ID, 0.60, time.Now().Add(2*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "c1", "prompt": "cat"},
				{"custom_id": "c2", "prompt": "dog"},
			},
		})
		require.Equal(t, http.StatusOK, res.Status)
		batch := decodeRaw[struct {
			ID string `json:"id"`
		}](t, res.Body)

		// Expire both lots, then switch Vertex mock to partial success ($0.60 captured, $0.60 excess).
		_, err := env.db.ExecContext(context.Background(), `UPDATE user_expiring_balance_lots SET expires_at = NOW() - INTERVAL '1 minute' WHERE id IN ($1, $2)`, lotA, lotB)
		require.NoError(t, err)
		env.vertex.SetMode(vertexModePartialSuccess)

		eventually(t, 20*time.Second, 200*time.Millisecond, func() bool {
			g := env.gatewayGet("/v1/images/batches/"+batch.ID, apiKey)
			if g.Status != http.StatusOK {
				return false
			}
			st := decodeRaw[struct {
				Status string `json:"status"`
			}](t, g.Body)
			return st.Status == "completed"
		}, "partial settlement after expiry should complete")

		reg, frozen, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 0.0, reg, 1e-8)
		require.InDelta(t, 0.0, frozen, 1e-8)

		b := env.getLotDB(lotB)
		require.InDelta(t, 0.0, b.RemainingAmount, 1e-8)
		require.InDelta(t, 0.60, b.ExpiredAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotA)
		verifyLotInvariant(t, env.db, lotB)
	})

	t.Run("RF8_B04_RepeatedCancelIsIdempotent", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.60)
		env.vertex.SetMode(vertexModeStayRunning)

		u := env.createTestUser(0.50)
		lotID := env.seedExpiringLotSQL(u.ID, 1.00, time.Now().Add(24*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "c1", "prompt": "cat"},
				{"custom_id": "c2", "prompt": "dog"},
			},
		})
		require.Equal(t, http.StatusOK, res.Status)
		batch := decodeRaw[struct {
			ID string `json:"id"`
		}](t, res.Body)

		c1 := env.gatewayPost("/v1/images/batches/"+batch.ID+"/cancel", apiKey, map[string]any{})
		require.Equal(t, http.StatusOK, c1.Status)
		_ = env.gatewayPost("/v1/images/batches/"+batch.ID+"/cancel", apiKey, map[string]any{})

		lot := env.getLotDB(lotID)
		require.InDelta(t, 1.00, lot.RemainingAmount, 1e-8)
		require.InDelta(t, 0.0, lot.FrozenAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotID)
		reg, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 0.50, reg, 1e-8)
	})

	t.Run("RF8_B05_LegacyHoldPureRegularWritesZeroAllocationRows", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.60)
		env.vertex.SetMode(vertexModeStayRunning)

		u := env.createTestUser(2.00)
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "c1", "prompt": "cat"},
				{"custom_id": "c2", "prompt": "dog"},
			},
		})
		require.Equal(t, http.StatusOK, res.Status)
		batch := decodeRaw[struct {
			ID string `json:"id"`
		}](t, res.Body)

		require.Empty(t, env.listAllocationsDB("batch_image_hold", batch.ID))
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/images/batches/"+batch.ID+"/cancel", apiKey, map[string]any{}).Status)
		reg, frozen, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 2.00, reg, 1e-8)
		require.InDelta(t, 0.0, frozen, 1e-8)
	})

	// ---------------- RF9: Balance Subscription & Refund Boundaries (5 subtests) ----------------
	t.Run("RF9_B01_InsufficientTotalBalanceRejectsPurchaseAtomically", func(t *testing.T) {
		env := newE2EEnv(t)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		u := env.createTestUser(2.00)
		lotID := env.seedExpiringLotSQL(u.ID, 1.00, time.Now().Add(24*time.Hour), "active")

		res := env.apiPost("/api/v1/payment/orders", u.Token, map[string]any{
			"order_type":   "subscription",
			"payment_type": "balance",
			"plan_id":      planID,
		})
		require.Equal(t, http.StatusBadRequest, res.Status)
		require.Equal(t, "INSUFFICIENT_BALANCE", res.Env.Reason)

		var orderCnt, subCnt int
		require.NoError(t, env.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM payment_orders WHERE user_id = $1`, u.ID).Scan(&orderCnt))
		require.NoError(t, env.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM user_subscriptions WHERE user_id = $1`, u.ID).Scan(&subCnt))
		require.Equal(t, 0, orderCnt)
		require.Equal(t, 0, subCnt)
		require.InDelta(t, 1.00, env.getLotDB(lotID).RemainingAmount, 1e-8)
		reg, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 2.00, reg, 1e-8)
	})

	t.Run("RF9_B02_FullRefundAfterLotExpiredForfeitsExpiringSlice", func(t *testing.T) {
		env := newE2EEnv(t)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		u := env.createTestUser(5.00)
		lotID := env.seedExpiringLotSQL(u.ID, 2.00, time.Now().Add(24*time.Hour), "active")

		res := env.apiPost("/api/v1/payment/orders", u.Token, map[string]any{
			"order_type":   "subscription",
			"payment_type": "balance",
			"plan_id":      planID,
		})
		require.Equal(t, http.StatusOK, res.Status)
		order := decodeData[struct {
			OrderID int64 `json:"order_id"`
		}](t, res.Env)

		_, err := env.db.ExecContext(context.Background(), `UPDATE user_expiring_balance_lots SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, lotID)
		require.NoError(t, err)

		rSt, rEnv := env.adminPost(fmt.Sprintf("/api/v1/admin/payment/orders/%d/refund", order.OrderID), map[string]any{
			"amount": 5.00,
			"reason": "anti-laundering refund",
		})
		require.Equal(t, http.StatusOK, rSt, rEnv.Message)

		// Only the $3.00 regular slice is returned ($2.00 remaining -> $5.00); expired $2.00 is forfeited.
		reg, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 5.00, reg, 1e-8)

		lot := env.getLotDB(lotID)
		require.InDelta(t, 0.0, lot.RemainingAmount, 1e-8)
		require.InDelta(t, 2.00, lot.ExpiredAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotID)

		allocs := env.listAllocationsDB("payment_order", fmt.Sprintf("%d", order.OrderID))
		require.Len(t, allocs, 2)
		require.InDelta(t, 2.00, allocs[0].ForfeitedExpiredAmount, 1e-8)
		require.InDelta(t, 3.00, allocs[1].ReleasedAmount, 1e-8)
	})

	t.Run("RF9_B03_PartialRefundWithinRegularLeavesActiveLotUntouched", func(t *testing.T) {
		env := newE2EEnv(t)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		u := env.createTestUser(5.00)
		lotID := env.seedExpiringLotSQL(u.ID, 2.00, time.Now().Add(24*time.Hour), "active")

		res := env.apiPost("/api/v1/payment/orders", u.Token, map[string]any{
			"order_type":   "subscription",
			"payment_type": "balance",
			"plan_id":      planID,
		})
		require.Equal(t, http.StatusOK, res.Status)
		order := decodeData[struct {
			OrderID int64 `json:"order_id"`
		}](t, res.Env)

		rSt, rEnv := env.adminPost(fmt.Sprintf("/api/v1/admin/payment/orders/%d/refund", order.OrderID), map[string]any{
			"amount": 1.50,
			"reason": "partial regular only",
		})
		require.Equal(t, http.StatusOK, rSt, rEnv.Message)

		reg, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 3.50, reg, 1e-8)
		lot := env.getLotDB(lotID)
		require.Equal(t, "exhausted", lot.Status)
		require.InDelta(t, 0.0, lot.RemainingAmount, 1e-8)
	})

	t.Run("RF9_B04_RefundOnPureRegularOrderUsesRegularBalance", func(t *testing.T) {
		env := newE2EEnv(t)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		u := env.createTestUser(10.00)

		res := env.apiPost("/api/v1/payment/orders", u.Token, map[string]any{
			"order_type":   "subscription",
			"payment_type": "balance",
			"plan_id":      planID,
		})
		require.Equal(t, http.StatusOK, res.Status)
		order := decodeData[struct {
			OrderID int64 `json:"order_id"`
		}](t, res.Env)

		require.Empty(t, env.listAllocationsDB("payment_order", fmt.Sprintf("%d", order.OrderID)))

		rSt, rEnv := env.adminPost(fmt.Sprintf("/api/v1/admin/payment/orders/%d/refund", order.OrderID), map[string]any{
			"amount": 5.00,
			"reason": "pure regular refund",
		})
		require.Equal(t, http.StatusOK, rSt, rEnv.Message)

		reg, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 10.00, reg, 1e-8)
	})

	t.Run("RF9_B05_RefundOnAlreadyRefundedOrderRejected", func(t *testing.T) {
		env := newE2EEnv(t)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		u := env.createTestUser(5.00)
		env.seedExpiringLotSQL(u.ID, 2.00, time.Now().Add(24*time.Hour), "active")

		res := env.apiPost("/api/v1/payment/orders", u.Token, map[string]any{
			"order_type":   "subscription",
			"payment_type": "balance",
			"plan_id":      planID,
		})
		require.Equal(t, http.StatusOK, res.Status)
		order := decodeData[struct {
			OrderID int64 `json:"order_id"`
		}](t, res.Env)

		r1, _ := env.adminPost(fmt.Sprintf("/api/v1/admin/payment/orders/%d/refund", order.OrderID), map[string]any{
			"amount": 5.00,
			"reason": "first refund",
		})
		require.Equal(t, http.StatusOK, r1)

		r2, _ := env.adminPost(fmt.Sprintf("/api/v1/admin/payment/orders/%d/refund", order.OrderID), map[string]any{
			"amount": 5.00,
			"reason": "duplicate refund",
		})
		require.NotEqual(t, http.StatusOK, r2)
		reg, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 5.00, reg, 1e-8)
	})

	// ---------------- RF10: Cache TTL/KEEPTTL, Restricted Keys & Filter Boundaries (5 subtests) ----------------
	t.Run("RF10_B01_RedisTTLTruncatedToNearestExpiresAt", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		u := env.createTestUser(0)
		env.seedExpiringLotSQL(u.ID, 1.00, time.Now().Add(90*time.Second), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		env.anthropic.SetTokens(0, 0)
		res := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "ttl"}},
		})
		require.Equal(t, http.StatusOK, res.Status)

		balKey := fmt.Sprintf("billing:balance:%d", u.ID)
		var ttl time.Duration
		eventually(t, 3*time.Second, 25*time.Millisecond, func() bool {
			var tErr error
			ttl, tErr = env.rdb.TTL(context.Background(), balKey).Result()
			return tErr == nil && ttl > 0
		}, "billing:balance cache key should be populated with positive TTL")
		require.Greater(t, ttl, time.Duration(0))
		require.LessOrEqual(t, ttl, 90*time.Second)
	})

	t.Run("RF10_B02_RedisCacheWriteSkippedWhenNearestExpiresWithin1s", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		u := env.createTestUser(0)
		env.seedExpiringLotSQL(u.ID, 1.00, time.Now().Add(900*time.Millisecond), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		env.anthropic.SetTokens(0, 0)
		res := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "skip cache"}},
		})
		if res.Status == http.StatusOK {
			exists, err := env.rdb.Exists(context.Background(), fmt.Sprintf("billing:balance:%d", u.ID)).Result()
			require.NoError(t, err)
			require.Equal(t, int64(0), exists, "cache write must be skipped when <= 1s remains")
		}
		time.Sleep(1100 * time.Millisecond)
		resAfter := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "after expiry"}},
		})
		require.Equal(t, http.StatusForbidden, resAfter.Status)
	})

	t.Run("RF10_B03_DeductBalanceScriptPreservesTTL_KEEPTTL", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		u := env.createTestUser(0)
		env.seedExpiringLotSQL(u.ID, 1.00, time.Now().Add(90*time.Second), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		// Populate cache with 0-cost call.
		env.anthropic.SetTokens(0, 0)
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "seed cache"}},
		}).Status)

		key := fmt.Sprintf("billing:balance:%d", u.ID)
		var ttlBefore time.Duration
		eventually(t, 3*time.Second, 25*time.Millisecond, func() bool {
			var tErr error
			ttlBefore, tErr = env.rdb.TTL(context.Background(), key).Result()
			return tErr == nil && ttlBefore > 0
		}, "billing:balance cache key should be populated with positive TTL")
		require.Greater(t, ttlBefore, time.Duration(0))
		require.LessOrEqual(t, ttlBefore, 90*time.Second)

		// Deduct $0.30 and ensure KEEPTTL does NOT extend TTL back to ~300s.
		env.anthropic.SetTokens(10000, 18000)
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "deduct keepttl"}},
		}).Status)

		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			val, gErr := env.rdb.Get(context.Background(), key).Float64()
			return gErr == nil && approxEqual(val, 0.70)
		}, "cached balance should decrement to 0.70")

		ttlAfter, err := env.rdb.TTL(context.Background(), key).Result()
		require.NoError(t, err)
		require.Greater(t, ttlAfter, time.Duration(0))
		require.LessOrEqual(t, ttlAfter, ttlBefore, "KEEPTTL must preserve remaining TTL without extending to 5m")
	})

	t.Run("RF10_B04_RestrictedAdminKeyWithoutBalanceReadOmitsExpiringFields", func(t *testing.T) {
		env := newE2EEnv(t)
		u := env.createTestUser(3.00)
		env.seedExpiringLotSQL(u.ID, 1.00, time.Now().Add(24*time.Hour), "active")

		kSt, kEnv := env.adminPost("/api/v1/admin/settings/admin-api-keys", map[string]any{
			"name":    "e2e-users-read-only",
			"enabled": true,
			"policy": map[string]any{
				"full_access":        false,
				"global_permissions": []string{"users.read"},
				"rules":              []any{},
			},
		})
		require.Equal(t, http.StatusOK, kSt, kEnv.Message)
		createdKey := decodeData[struct {
			Key string `json:"key"`
		}](t, kEnv)
		require.NotEmpty(t, createdKey.Key)

		detRes := env.adminKeyGet(fmt.Sprintf("/api/v1/admin/users/%d", u.ID), createdKey.Key)
		require.Equal(t, http.StatusOK, detRes.Status)
		var detMap map[string]any
		require.NoError(t, json.Unmarshal(detRes.Env.Data, &detMap))
		for _, forbidden := range []string{"balance", "expiring_balance", "total_available_balance", "expiring_frozen_balance", "nearest_expiring_at", "nearest_expiring_amount"} {
			_, present := detMap[forbidden]
			require.False(t, present, "restricted admin key without users.balance.read must omit %s", forbidden)
		}

		lstRes := env.adminKeyGet("/api/v1/admin/users?search="+u.Email, createdKey.Key)
		require.Equal(t, http.StatusOK, lstRes.Status)
		lstPage := decodeData[struct {
			Items []map[string]any `json:"items"`
		}](t, lstRes.Env)
		require.Len(t, lstPage.Items, 1)
		for _, forbidden := range []string{"balance", "expiring_balance", "total_available_balance", "expiring_frozen_balance", "nearest_expiring_at", "nearest_expiring_amount"} {
			_, present := lstPage.Items[0][forbidden]
			require.False(t, present, "restricted list item must omit %s", forbidden)
		}
	})

	t.Run("RF10_B05_AdminCheckinRecordsFiltersAndEmptyStats", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.20))

		// Empty range stats check before any check-in.
		emptySt, emptyEnv := env.adminGet("/api/v1/admin/checkin/stats?days=30")
		require.Equal(t, http.StatusOK, emptySt)
		emptyStats := decodeData[struct {
			TodayCheckins   int     `json:"today_checkins"`
			ConsumptionRate float64 `json:"consumption_rate"`
			ExpirationRate  float64 `json:"expiration_rate"`
		}](t, emptyEnv)
		require.Equal(t, 0, emptyStats.TodayCheckins)
		require.InDelta(t, 0.0, emptyStats.ConsumptionRate, 1e-8)
		require.InDelta(t, 0.0, emptyStats.ExpirationRate, 1e-8)

		u1 := env.createTestUser(0)
		u2 := env.createTestUser(0)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u1.Token, map[string]any{}, map[string]string{"X-Forwarded-For": "203.0.113.51"}).Status)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u2.Token, map[string]any{}, map[string]string{"X-Forwarded-For": "203.0.113.52"}).Status)

		today := time.Now().In(mustLoadLoc(t, env.timezone)).Format("2006-01-02")
		for _, query := range []string{
			fmt.Sprintf("user_id=%d", u1.ID),
			"search=" + strings.Split(u1.Email, "@")[0],
			"client_ip=203.0.113.51",
			fmt.Sprintf("start_date=%s&end_date=%s", today, today),
		} {
			st, rEnv := env.adminGet("/api/v1/admin/checkin/records?" + query)
			require.Equal(t, http.StatusOK, st, query)
			p := decodeData[struct {
				Items []struct {
					UserID int64 `json:"user_id"`
				} `json:"items"`
				Total int64 `json:"total"`
			}](t, rEnv)
			require.GreaterOrEqual(t, p.Total, int64(1), "query=%s", query)
			if !strings.HasPrefix(query, "start_date=") {
				require.Equal(t, u1.ID, p.Items[0].UserID, "query=%s", query)
			}
		}
	})
}
