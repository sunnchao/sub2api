//go:build e2e

package checkin_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTier1_FeatureCoverage(t *testing.T) {
	// ---------------- RF1: Migration 257 & Bypass Schema Integrity (5 subtests) ----------------
	t.Run("RF1_01_AllFourBypassTablesExist", func(t *testing.T) {
		env := newE2EEnv(t)
		tables := []string{
			"user_checkin_records",
			"checkin_daily_budgets",
			"user_expiring_balance_lots",
			"user_expiring_balance_allocations",
		}
		for _, tbl := range tables {
			var count int
			err := env.db.QueryRowContext(context.Background(), `
				SELECT COUNT(*) FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			`, tbl).Scan(&count)
			require.NoError(t, err)
			require.Equal(t, 1, count, "expected table %s to exist", tbl)
		}
	})

	t.Run("RF1_02_CheckinRecordsUniqueAndIndexesExist", func(t *testing.T) {
		env := newE2EEnv(t)
		rows, err := env.db.QueryContext(context.Background(), `
			SELECT indexname, indexdef FROM pg_indexes
			WHERE schemaname = 'public' AND tablename = 'user_checkin_records'
		`)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()

		var defs []string
		for rows.Next() {
			var name, def string
			require.NoError(t, rows.Scan(&name, &def))
			defs = append(defs, strings.ToLower(def))
		}
		require.NoError(t, rows.Err())
		joined := strings.Join(defs, "\n")
		require.Contains(t, joined, "unique index")
		require.Contains(t, joined, "(user_id, checkin_date)")
		require.Contains(t, joined, "(checkin_date, client_ip)")
		require.Contains(t, joined, "checkin_date desc")
	})

	t.Run("RF1_03_ExpiringLotsCheckConstraintsAndPartialIndexesExist", func(t *testing.T) {
		env := newE2EEnv(t)
		var checkDef string
		err := env.db.QueryRowContext(context.Background(), `
			SELECT string_agg(pg_get_constraintdef(c.oid), '; ')
			FROM pg_constraint c
			JOIN pg_class cl ON cl.oid = c.conrelid
			WHERE cl.relname = 'user_expiring_balance_lots' AND c.contype = 'c'
		`).Scan(&checkDef)
		require.NoError(t, err)
		lowerCheck := strings.ToLower(checkDef)
		require.Regexp(t, `initial_amount\s*>\s*\(?0`, lowerCheck)
		require.Regexp(t, `remaining_amount\s*>=\s*\(?0`, lowerCheck)
		require.Regexp(t, `frozen_amount\s*>=\s*\(?0`, lowerCheck)
		require.Regexp(t, `consumed_amount\s*>=\s*\(?0`, lowerCheck)
		require.Regexp(t, `expired_amount\s*>=\s*\(?0`, lowerCheck)

		rows, err := env.db.QueryContext(context.Background(), `
			SELECT indexdef FROM pg_indexes
			WHERE schemaname = 'public' AND tablename = 'user_expiring_balance_lots'
		`)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		var defs []string
		for rows.Next() {
			var def string
			require.NoError(t, rows.Scan(&def))
			defs = append(defs, strings.ToLower(def))
		}
		require.NoError(t, rows.Err())
		joined := strings.Join(defs, "\n")
		require.Contains(t, joined, "(user_id, expires_at, id)")
		require.Contains(t, joined, "remaining_amount > (0)")
		require.Contains(t, joined, "(expires_at)")
		require.Contains(t, joined, "(source_type, source_id)")
	})

	t.Run("RF1_04_ExpiringAllocationsUniqueAndIndexesExist", func(t *testing.T) {
		env := newE2EEnv(t)
		rows, err := env.db.QueryContext(context.Background(), `
			SELECT indexdef FROM pg_indexes
			WHERE schemaname = 'public' AND tablename = 'user_expiring_balance_allocations'
		`)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		var defs []string
		for rows.Next() {
			var def string
			require.NoError(t, rows.Scan(&def))
			defs = append(defs, strings.ToLower(def))
		}
		require.NoError(t, rows.Err())
		joined := strings.Join(defs, "\n")
		require.Contains(t, joined, "unique index")
		require.Contains(t, joined, "(ref_type, ref_key, alloc_seq)")
		require.Contains(t, joined, "(user_id, created_at desc)")
		require.Contains(t, joined, "(lot_id)")
	})

	t.Run("RF1_05_LegacyTablesHaveZeroNewColumns", func(t *testing.T) {
		env := newE2EEnv(t)
		for _, tbl := range []string{"users", "payment_orders", "usage_logs", "batch_image_jobs"} {
			var suspicious int
			err := env.db.QueryRowContext(context.Background(), `
				SELECT COUNT(*) FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1
				  AND (column_name LIKE '%checkin%' OR column_name LIKE '%expiring%' OR column_name LIKE '%_lot%')
			`, tbl).Scan(&suspicious)
			require.NoError(t, err)
			require.Equal(t, 0, suspicious, "table %s must remain unpolluted", tbl)
		}
	})

	// ---------------- RF2: Check-in Settings, Feature Flag & Public/SSR Visibility (5 subtests) ----------------
	t.Run("RF2_01_DefaultPublicSettingsCheckinDisabled", func(t *testing.T) {
		env := newE2EEnv(t)
		res := env.publicGet("/api/v1/settings/public")
		require.Equal(t, http.StatusOK, res.Status)
		pub := decodeData[map[string]any](t, res.Env)
		val, ok := pub["checkin_enabled"].(bool)
		require.True(t, ok, "checkin_enabled must exist in public settings")
		require.False(t, val)
	})

	t.Run("RF2_02_SSRHTMLContainsCheckinEnabled", func(t *testing.T) {
		env := newE2EEnv(t)
		res := env.publicGet("/")
		if res.Status == http.StatusNotFound {
			t.Skip("frontend dist not embedded in this build")
		}
		require.Equal(t, http.StatusOK, res.Status)
		html := string(res.Body)
		re := regexp.MustCompile(`window\.__APP_CONFIG__\s*=\s*(\{.*?\});`)
		m := re.FindStringSubmatch(html)
		require.Len(t, m, 2, "expected window.__APP_CONFIG__ in SSR HTML")
		var injected map[string]any
		require.NoError(t, json.Unmarshal([]byte(m[1]), &injected))
		v, ok := injected["checkin_enabled"].(bool)
		require.True(t, ok)
		require.False(t, v)

		cfg := defaultEnabledCheckinConfig(0.20)
		env.enableCheckinConfig(cfg)

		res2 := env.publicGet("/")
		require.Equal(t, http.StatusOK, res2.Status)
		m2 := re.FindStringSubmatch(string(res2.Body))
		require.Len(t, m2, 2)
		var injected2 map[string]any
		require.NoError(t, json.Unmarshal([]byte(m2[1]), &injected2))
		v2, ok2 := injected2["checkin_enabled"].(bool)
		require.True(t, ok2)
		require.True(t, v2)
	})

	t.Run("RF2_03_AdminConfigRoundtripUpdatesConfigAndPublic", func(t *testing.T) {
		env := newE2EEnv(t)
		st, envGet := env.adminGet("/api/v1/admin/checkin/config")
		require.Equal(t, http.StatusOK, st)
		before := decodeData[map[string]any](t, envGet)
		require.Equal(t, false, before["enabled"])

		cfg := defaultEnabledCheckinConfig(0.35)
		cfg["validity_days"] = 3
		cfg["rules_description"] = "Roundtrip rule test"
		env.enableCheckinConfig(cfg)

		st2, envGet2 := env.adminGet("/api/v1/admin/checkin/config")
		require.Equal(t, http.StatusOK, st2)
		after := decodeData[map[string]any](t, envGet2)
		require.Equal(t, true, after["enabled"])
		require.InDelta(t, 0.35, after["base_amount"], 1e-8)
		require.Equal(t, float64(3), after["validity_days"])
		require.Equal(t, "Roundtrip rule test", after["rules_description"])

		pubRes := env.publicGet("/api/v1/settings/public")
		require.Equal(t, http.StatusOK, pubRes.Status)
		pub := decodeData[map[string]any](t, pubRes.Env)
		require.Equal(t, true, pub["checkin_enabled"])
	})

	t.Run("RF2_04_ConfigUpdateCreatesAdminAuditLog", func(t *testing.T) {
		env := newE2EEnv(t)
		uniqueDesc := "Audit trail verification " + randomHex(6)
		cfg := defaultEnabledCheckinConfig(0.15)
		cfg["rules_description"] = uniqueDesc
		env.enableCheckinConfig(cfg)

		env.requireCheckinAuditLogRecorded(t, uniqueDesc)
	})

	t.Run("RF2_05_UserStatusReflectsEnabledConfigAndRewardPreview", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.42)
		cfg["validity_days"] = 2
		cfg["rules_description"] = "Status preview rules"
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		res := env.apiGet("/api/v1/checkin/status", u.Token)
		require.Equal(t, http.StatusOK, res.Status)
		data := decodeData[struct {
			Enabled          bool   `json:"enabled"`
			CheckedInToday   bool   `json:"checked_in_today"`
			CurrentStreak    int    `json:"current_streak"`
			RulesDescription string `json:"rules_description"`
			ServerDate       string `json:"server_date"`
			Timezone         string `json:"timezone"`
			RewardPreview    struct {
				RewardMode      string  `json:"reward_mode"`
				BaseAmount      float64 `json:"base_amount"`
				AmountPrecision int     `json:"amount_precision"`
				ValidityDays    int     `json:"validity_days"`
			} `json:"reward_preview"`
		}](t, res.Env)

		require.True(t, data.Enabled)
		require.False(t, data.CheckedInToday)
		require.Equal(t, 0, data.CurrentStreak)
		require.Equal(t, "Status preview rules", data.RulesDescription)
		require.NotEmpty(t, data.ServerDate)
		require.NotEmpty(t, data.Timezone)
		require.Equal(t, "fixed", data.RewardPreview.RewardMode)
		require.InDelta(t, 0.42, data.RewardPreview.BaseAmount, 1e-8)
		require.Equal(t, 2, data.RewardPreview.ValidityDays)
	})

	// ---------------- RF3: Check-in Rewards, Validity, Timezone & Expiring Lot Lifecycle (5 subtests) ----------------
	t.Run("RF3_01_FixedRewardCreatesRecordAndActiveLot", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.50))
		u := env.createTestUser(0)

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		out := decodeData[struct {
			Record struct {
				ID          int64   `json:"id"`
				BaseAmount  float64 `json:"base_amount"`
				TotalAmount float64 `json:"total_amount"`
			} `json:"record"`
			ExpiringSummary struct {
				TotalActive float64 `json:"total_active"`
			} `json:"expiring_summary"`
			TotalAvailableBalance float64 `json:"total_available_balance"`
		}](t, res.Env)

		require.InDelta(t, 0.50, out.Record.BaseAmount, 1e-8)
		require.InDelta(t, 0.50, out.Record.TotalAmount, 1e-8)
		require.InDelta(t, 0.50, out.ExpiringSummary.TotalActive, 1e-8)
		require.InDelta(t, 0.50, out.TotalAvailableBalance, 1e-8)

		lots := env.listUserLotsDB(u.ID)
		require.Len(t, lots, 1)
		require.Equal(t, "active", lots[0].Status)
		require.InDelta(t, 0.50, lots[0].InitialAmount, 1e-8)
		require.InDelta(t, 0.50, lots[0].RemainingAmount, 1e-8)
		verifyLotInvariant(t, env.db, lots[0].ID)
	})

	t.Run("RF3_02_RandomRewardWithinRangeAndQuantized", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["reward_mode"] = "random"
		cfg["min_amount"] = 0.15
		cfg["max_amount"] = 0.35
		cfg["amount_precision"] = 2
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		out := decodeData[struct {
			Record struct {
				BaseAmount  float64 `json:"base_amount"`
				TotalAmount float64 `json:"total_amount"`
			} `json:"record"`
		}](t, res.Env)

		require.GreaterOrEqual(t, out.Record.BaseAmount, 0.15-1e-8)
		require.LessOrEqual(t, out.Record.BaseAmount, 0.35+1e-8)
		scaled := out.Record.BaseAmount * 100
		require.InDelta(t, math.Round(scaled), scaled, 1e-6, "amount must be quantized to 2 decimals")
	})

	t.Run("RF3_03_ValidityDaysSetsExpiresAtNextMidnightInTimezone", func(t *testing.T) {
		env := newE2EEnv(t)
		loc := mustLoadLoc(t, env.timezone)
		cfg := defaultEnabledCheckinConfig(0.25)
		cfg["validity_days"] = 1
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		before := time.Now().In(loc)
		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status)
		out := decodeData[struct {
			Record struct {
				ExpiresAt time.Time `json:"expires_at"`
			} `json:"record"`
		}](t, res.Env)

		expected := startOfDayInLoc(before, loc).AddDate(0, 0, 1)
		// Allow midnight rollover only if test literally crossed 00:00:00 during the HTTP call.
		if !out.Record.ExpiresAt.Equal(expected) {
			expectedAfter := startOfDayInLoc(time.Now().In(loc), loc).AddDate(0, 0, 1)
			require.True(t, out.Record.ExpiresAt.Equal(expectedAfter), "expected %s, got %s", expected, out.Record.ExpiresAt)
		}
		lots := env.listUserLotsDB(u.ID)
		require.Len(t, lots, 1)
		require.True(t, lots[0].ExpiresAt.Equal(out.Record.ExpiresAt))
	})

	t.Run("RF3_04_CalendarReturnsCheckedDayAndTotals", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.30))
		u := env.createTestUser(0)

		loc := mustLoadLoc(t, env.timezone)
		now := time.Now().In(loc)
		month := now.Format("2006-01")
		today := now.Format("2006-01-02")

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status)

		calRes := env.apiGet("/api/v1/checkin/calendar?month="+month, u.Token)
		require.Equal(t, http.StatusOK, calRes.Status)
		cal := decodeData[struct {
			Month            string  `json:"month"`
			MonthTotalAmount float64 `json:"month_total_amount"`
			MonthCheckinDays int     `json:"month_checkin_days"`
			CheckedDates     []struct {
				Date        string  `json:"date"`
				TotalAmount float64 `json:"total_amount"`
			} `json:"checked_dates"`
		}](t, calRes.Env)

		require.Equal(t, month, cal.Month)
		require.Equal(t, 1, cal.MonthCheckinDays)
		require.InDelta(t, 0.30, cal.MonthTotalAmount, 1e-8)
		require.Len(t, cal.CheckedDates, 1)
		require.Equal(t, today, cal.CheckedDates[0].Date)
		require.InDelta(t, 0.30, cal.CheckedDates[0].TotalAmount, 1e-8)
	})

	t.Run("RF3_05_RecordsAndLotsPaginateAndSatisfyInvariant", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.40))
		u := env.createTestUser(0)

		loc := mustLoadLoc(t, env.timezone)
		d1 := time.Now().In(loc).AddDate(0, 0, -2).Format("2006-01-02")
		d2 := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
		rec1 := env.seedCheckinRecordSQL(u.ID, d1, 1, 1, 0.40)
		rec2 := env.seedCheckinRecordSQL(u.ID, d2, 2, 2, 0.40)
		_, err := env.db.ExecContext(context.Background(), `
			INSERT INTO user_expiring_balance_lots (user_id, source_type, source_id, initial_amount, remaining_amount, expires_at, status)
			VALUES ($1, 'checkin', $2, 0.40, 0.40, NOW() + INTERVAL '2 days', 'active'),
			       ($1, 'checkin', $3, 0.40, 0.40, NOW() + INTERVAL '3 days', 'active')
		`, u.ID, rec1, rec2)
		require.NoError(t, err)

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status)

		recRes := env.apiGet("/api/v1/checkin/records?page=1&page_size=2", u.Token)
		require.Equal(t, http.StatusOK, recRes.Status)
		recPage := decodeData[struct {
			Items []map[string]any `json:"items"`
			Total int64            `json:"total"`
			Pages int              `json:"pages"`
		}](t, recRes.Env)
		require.Equal(t, int64(3), recPage.Total)
		require.Equal(t, 2, recPage.Pages)
		require.Len(t, recPage.Items, 2)

		lotRes := env.apiGet("/api/v1/checkin/lots?page=1&page_size=2&status=all", u.Token)
		require.Equal(t, http.StatusOK, lotRes.Status)
		lotPage := decodeData[struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
			Total int64 `json:"total"`
		}](t, lotRes.Env)
		require.Equal(t, int64(3), lotPage.Total)
		require.Len(t, lotPage.Items, 2)
		for _, lot := range env.listUserLotsDB(u.ID) {
			verifyLotInvariant(t, env.db, lot.ID)
		}
	})

	// ---------------- RF4: Streaks, Milestones & Cycle Repeat (5 subtests) ----------------
	t.Run("RF4_01_Day1SetsStreakDays1", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.10))
		u := env.createTestUser(0)

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

	t.Run("RF4_02_Day2ConsecutiveIncrementsStreakDays2", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.10))
		u := env.createTestUser(0)
		loc := mustLoadLoc(t, env.timezone)
		yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
		env.seedCheckinRecordSQL(u.ID, yesterday, 1, 1, 0.10)

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status)
		out := decodeData[struct {
			Record struct {
				StreakDays int `json:"streak_days"`
			} `json:"record"`
		}](t, res.Env)
		require.Equal(t, 2, out.Record.StreakDays)
	})

	t.Run("RF4_03_MilestoneHitAwardsBonusAndTotalAmount", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["milestones"] = []map[string]any{
			{"days": 2, "bonus_amount": 0.50},
		}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		loc := mustLoadLoc(t, env.timezone)
		yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
		env.seedCheckinRecordSQL(u.ID, yesterday, 1, 1, 0.10)

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status)
		out := decodeData[struct {
			Record struct {
				StreakDays           int     `json:"streak_days"`
				BaseAmount           float64 `json:"base_amount"`
				MilestoneBonusAmount float64 `json:"milestone_bonus_amount"`
				TotalAmount          float64 `json:"total_amount"`
			} `json:"record"`
		}](t, res.Env)
		require.Equal(t, 2, out.Record.StreakDays)
		require.InDelta(t, 0.10, out.Record.BaseAmount, 1e-8)
		require.InDelta(t, 0.50, out.Record.MilestoneBonusAmount, 1e-8)
		require.InDelta(t, 0.60, out.Record.TotalAmount, 1e-8)

		lots := env.listUserLotsDB(u.ID)
		require.Len(t, lots, 1)
		require.InDelta(t, 0.60, lots[0].InitialAmount, 1e-8)
	})

	t.Run("RF4_04_StatusReportsNextMilestoneAndCycleDay", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["milestones"] = []map[string]any{
			{"days": 2, "bonus_amount": 0.50},
			{"days": 5, "bonus_amount": 1.00},
		}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		loc := mustLoadLoc(t, env.timezone)
		yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
		env.seedCheckinRecordSQL(u.ID, yesterday, 1, 1, 0.10)

		res := env.apiGet("/api/v1/checkin/status", u.Token)
		require.Equal(t, http.StatusOK, res.Status)
		data := decodeData[struct {
			CurrentStreak     int `json:"current_streak"`
			MilestoneCycleDay int `json:"milestone_cycle_day"`
			NextMilestone     *struct {
				Days        int     `json:"days"`
				BonusAmount float64 `json:"bonus_amount"`
			} `json:"next_milestone"`
		}](t, res.Env)
		require.Equal(t, 1, data.CurrentStreak)
		require.Equal(t, 1, data.MilestoneCycleDay)
		require.NotNil(t, data.NextMilestone)
		require.Equal(t, 2, data.NextMilestone.Days)
		require.InDelta(t, 0.50, data.NextMilestone.BonusAmount, 1e-8)
	})

	t.Run("RF4_05_CalendarMarksMilestoneHitTrue", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["milestones"] = []map[string]any{
			{"days": 2, "bonus_amount": 0.50},
		}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		loc := mustLoadLoc(t, env.timezone)
		now := time.Now().In(loc)
		env.seedCheckinRecordSQL(u.ID, now.AddDate(0, 0, -1).Format("2006-01-02"), 1, 1, 0.10)

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status)

		calRes := env.apiGet("/api/v1/checkin/calendar?month="+now.Format("2006-01"), u.Token)
		require.Equal(t, http.StatusOK, calRes.Status)
		cal := decodeData[struct {
			CheckedDates []struct {
				Date                 string  `json:"date"`
				MilestoneBonusAmount float64 `json:"milestone_bonus_amount"`
				MilestoneHit         bool    `json:"milestone_hit"`
			} `json:"checked_dates"`
		}](t, calRes.Env)
		todayStr := now.Format("2006-01-02")
		var found bool
		for _, d := range cal.CheckedDates {
			if d.Date == todayStr {
				found = true
				require.True(t, d.MilestoneHit)
				require.InDelta(t, 0.50, d.MilestoneBonusAmount, 1e-8)
			}
		}
		require.True(t, found)
	})

	// ---------------- RF5: Eligibility Gates (5 subtests) ----------------
	t.Run("RF5_01_AllGatesOffEligibleTrue", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.10))
		u := env.createTestUser(0)

		stRes := env.apiGet("/api/v1/checkin/status", u.Token)
		require.Equal(t, http.StatusOK, stRes.Status)
		st := decodeData[struct {
			Eligibility struct {
				Eligible bool `json:"eligible"`
			} `json:"eligibility"`
		}](t, stRes.Env)
		require.True(t, st.Eligibility.Eligible)

		cRes := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, cRes.Status)
	})

	t.Run("RF5_02_MinAccountAgeDaysMetSucceeds", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["eligibility"] = map[string]any{"min_account_age_days": 5}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		_, err := env.db.ExecContext(context.Background(), `UPDATE users SET created_at = NOW() - INTERVAL '6 days' WHERE id = $1`, u.ID)
		require.NoError(t, err)

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	})

	t.Run("RF5_03_MinTotalRechargedMetSucceeds", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["eligibility"] = map[string]any{"min_total_recharged": 5.0}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		_, err := env.db.ExecContext(context.Background(), `UPDATE users SET total_recharged = 5.0 WHERE id = $1`, u.ID)
		require.NoError(t, err)

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	})

	t.Run("RF5_04_MaxAvailableBalanceBelowCapSucceeds", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["eligibility"] = map[string]any{"max_available_balance": 10.0}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(4.0)
		env.seedExpiringLotSQL(u.ID, 5.0, time.Now().Add(24*time.Hour), "active")

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	})

	t.Run("RF5_05_AllowedSignupSourcesMatchingSucceeds", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["eligibility"] = map[string]any{
			"allowed_signup_sources": []string{"email", "linuxdo"},
		}
		env.enableCheckinConfig(cfg)

		u1 := env.registerUser("src_email")
		res1 := env.apiPost("/api/v1/checkin", u1.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res1.Status, string(res1.Body))

		// User with non-matching signup_source but bound linuxdo identity also passes.
		u2 := env.createTestUser(0)
		_, err := env.db.ExecContext(context.Background(), `UPDATE users SET signup_source = 'admin' WHERE id = $1`, u2.ID)
		require.NoError(t, err)
		bSt, bEnv := env.adminPost(fmt.Sprintf("/api/v1/admin/users/%d/auth-identities", u2.ID), map[string]any{
			"provider_type":    "linuxdo",
			"provider_key":     "linuxdo",
			"provider_subject": "ld_" + randomHex(4),
		})
		require.Equal(t, http.StatusOK, bSt, bEnv.Message)

		res2 := env.apiPost("/api/v1/checkin", u2.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res2.Status, string(res2.Body))
	})

	// ---------------- RF6: Anti-Abuse, Auth, IP Limit, Atomic Budget & Captcha (5 subtests) ----------------
	t.Run("RF6_01_JWTUserSucceedsAllFiveEndpoints", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.15))
		u := env.createTestUser(0)

		require.Equal(t, http.StatusOK, env.apiGet("/api/v1/checkin/status", u.Token).Status)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u.Token, map[string]any{}).Status)
		month := time.Now().In(mustLoadLoc(t, env.timezone)).Format("2006-01")
		require.Equal(t, http.StatusOK, env.apiGet("/api/v1/checkin/calendar?month="+month, u.Token).Status)
		require.Equal(t, http.StatusOK, env.apiGet("/api/v1/checkin/records?page=1&page_size=20", u.Token).Status)
		require.Equal(t, http.StatusOK, env.apiGet("/api/v1/checkin/lots?page=1&page_size=20&status=all", u.Token).Status)
	})

	t.Run("RF6_02_SingleCheckinIncrementsDailyBudget", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.25))
		u := env.createTestUser(0)

		res := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, res.Status)

		today := time.Now().In(mustLoadLoc(t, env.timezone)).Format("2006-01-02")
		var count int
		var totalGranted float64
		err := env.db.QueryRowContext(context.Background(),
			`SELECT checkin_count, total_granted FROM checkin_daily_budgets WHERE checkin_date = $1::date`, today,
		).Scan(&count, &totalGranted)
		require.NoError(t, err)
		require.Equal(t, 1, count)
		require.InDelta(t, 0.25, totalGranted, 1e-8)
	})

	t.Run("RF6_03_MaxAccountsPerIPDayAllowsUpToN", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["risk"] = map[string]any{"max_accounts_per_ip_day": 2}
		env.enableCheckinConfig(cfg)

		u1 := env.createTestUser(0)
		u2 := env.createTestUser(0)
		hdr := map[string]string{"X-Forwarded-For": "203.0.113.10"}

		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u1.Token, map[string]any{}, hdr).Status)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u2.Token, map[string]any{}, hdr).Status)
	})

	t.Run("RF6_04_DailyTotalBudgetAllowsWithinBudget", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.40)
		cfg["risk"] = map[string]any{"daily_total_budget_usd": 1.00}
		env.enableCheckinConfig(cfg)

		u1 := env.createTestUser(0)
		u2 := env.createTestUser(0)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u1.Token, map[string]any{}).Status)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u2.Token, map[string]any{}).Status)

		today := time.Now().In(mustLoadLoc(t, env.timezone)).Format("2006-01-02")
		var totalGranted float64
		err := env.db.QueryRowContext(context.Background(),
			`SELECT total_granted FROM checkin_daily_budgets WHERE checkin_date = $1::date`, today,
		).Scan(&totalGranted)
		require.NoError(t, err)
		require.InDelta(t, 0.80, totalGranted, 1e-8)
	})

	t.Run("RF6_05_CaptchaDisabledRequiresNoProof", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.10))
		u := env.createTestUser(0)

		st := env.apiGet("/api/v1/checkin/status", u.Token)
		require.Equal(t, http.StatusOK, st.Status)
		sData := decodeData[struct {
			CaptchaRequired bool `json:"captcha_required"`
		}](t, st.Env)
		require.False(t, sData.CaptchaRequired)

		cRes := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, cRes.Status)
	})

	// ---------------- RF7: Gateway FEFO Usage Billing, API Key Auth & Low-Balance Notifications (5 subtests) ----------------
	t.Run("RF7_01_APIKeyAuthSucceedsZeroRegularPositiveExpiring", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.50))

		u := env.createTestUser(0)
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u.Token, map[string]any{}).Status)

		env.anthropic.SetTokens(1000, 1000)
		gwRes := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 64,
			"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		})
		require.Equal(t, http.StatusOK, gwRes.Status, string(gwRes.Body))
	})

	t.Run("RF7_02_GatewayDeductsActiveExpiringLotBeforeRegular", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		u := env.createTestUser(1.00)
		lotID := env.seedExpiringLotSQL(u.ID, 0.50, time.Now().Add(24*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		// claude-sonnet-4-5: 10000*3e-6 + 18000*1.5e-5 = 0.03 + 0.27 = 0.30 USD.
		env.anthropic.SetTokens(10000, 18000)
		gwRes := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 64,
			"messages":   []map[string]string{{"role": "user", "content": "bill 0.30"}},
		})
		require.Equal(t, http.StatusOK, gwRes.Status, string(gwRes.Body))

		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			return approxEqual(env.getLotDB(lotID).ConsumedAmount, 0.30)
		}, "lot should consume 0.30")

		lot := env.getLotDB(lotID)
		require.InDelta(t, 0.20, lot.RemainingAmount, 1e-8)
		require.Equal(t, "active", lot.Status)
		verifyLotInvariant(t, env.db, lotID)

		regBal, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 1.00, regBal, 1e-8)
	})

	t.Run("RF7_03_MultipleLotsDeductInFEFOOrder", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		u := env.createTestUser(0)
		lotA := env.seedExpiringLotSQL(u.ID, 0.20, time.Now().Add(1*time.Hour), "active")
		lotB := env.seedExpiringLotSQL(u.ID, 0.50, time.Now().Add(72*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		env.anthropic.SetTokens(10000, 18000) // $0.30
		gwRes := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 64,
			"messages":   []map[string]string{{"role": "user", "content": "fefo"}},
		})
		require.Equal(t, http.StatusOK, gwRes.Status)

		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			return env.getLotDB(lotA).Status == "exhausted" && approxEqual(env.getLotDB(lotB).ConsumedAmount, 0.10)
		}, "FEFO exhaustion of lotA and 0.10 from lotB")

		a := env.getLotDB(lotA)
		b := env.getLotDB(lotB)
		require.InDelta(t, 0.0, a.RemainingAmount, 1e-8)
		require.InDelta(t, 0.20, a.ConsumedAmount, 1e-8)
		require.InDelta(t, 0.40, b.RemainingAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotA)
		verifyLotInvariant(t, env.db, lotB)
	})

	t.Run("RF7_04_RegularBalanceTakesOverflowWhenExpiringExhausted", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		u := env.createTestUser(1.00)
		lotID := env.seedExpiringLotSQL(u.ID, 0.20, time.Now().Add(12*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		env.anthropic.SetTokens(10000, 18000) // $0.30
		gwRes := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 64,
			"messages":   []map[string]string{{"role": "user", "content": "overflow"}},
		})
		require.Equal(t, http.StatusOK, gwRes.Status)

		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			reg, _, _ := env.getUserDBBalance(u.ID)
			return approxEqual(reg, 0.90)
		}, "regular balance should take 0.10 overflow")

		lot := env.getLotDB(lotID)
		require.Equal(t, "exhausted", lot.Status)
		require.InDelta(t, 0.20, lot.ConsumedAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotID)
	})

	t.Run("RF7_05_CheckinDoesNotIncrementTotalRechargedOrAffiliate", func(t *testing.T) {
		env := newE2EEnv(t)
		rate := 10.0
		st, _ := env.adminPut("/api/v1/admin/settings", map[string]any{
			"affiliate_enabled":     true,
			"affiliate_rebate_rate": rate,
		})
		require.Equal(t, http.StatusOK, st)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(1.00))

		referrer := env.registerUser("aff_ref")
		affRes := env.apiGet("/api/v1/user/aff", referrer.Token)
		require.Equal(t, http.StatusOK, affRes.Status)
		affData := decodeData[struct {
			AffCode string `json:"aff_code"`
		}](t, affRes.Env)
		require.NotEmpty(t, affData.AffCode)

		refEmail := fmt.Sprintf("aff_invitee_%s@example.com", randomHex(5))
		regRes := env.doJSON(http.MethodPost, "/api/v1/auth/register", "", "", map[string]any{
			"email":    refEmail,
			"password": "UserPass!123",
			"aff_code": affData.AffCode,
		})
		require.Equal(t, http.StatusOK, regRes.Status, string(regRes.Body))
		regData := decodeData[struct {
			AccessToken string `json:"access_token"`
			User        struct {
				ID int64 `json:"id"`
			} `json:"user"`
		}](t, regRes.Env)

		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", regData.AccessToken, map[string]any{}).Status)

		_, _, totalRecharged := env.getUserDBBalance(regData.User.ID)
		require.InDelta(t, 0.0, totalRecharged, 1e-8)

		affAfter := env.apiGet("/api/v1/user/aff", referrer.Token)
		require.Equal(t, http.StatusOK, affAfter.Status)
		afterData := decodeData[struct {
			AffQuota        float64 `json:"aff_quota"`
			AffFrozenQuota  float64 `json:"aff_frozen_quota"`
			AffHistoryQuota float64 `json:"aff_history_quota"`
		}](t, affAfter.Env)
		require.InDelta(t, 0.0, afterData.AffQuota, 1e-8)
		require.InDelta(t, 0.0, afterData.AffFrozenQuota, 1e-8)
		require.InDelta(t, 0.0, afterData.AffHistoryQuota, 1e-8)
	})

	// ---------------- RF8: Batch Image Hold, Capture & LIFO Anti-Laundering Release (5 subtests) ----------------
	t.Run("RF8_01_SubmitFreezesExpiringLotIntoFrozenAmount", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.60)
		env.vertex.SetMode(vertexModeStayRunning)

		u := env.createTestUser(0)
		lotID := env.seedExpiringLotSQL(u.ID, 2.00, time.Now().Add(24*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "c1", "prompt": "cat"},
				{"custom_id": "c2", "prompt": "dog"},
			},
		})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		batch := decodeRaw[struct {
			ID         string  `json:"id"`
			HoldAmount float64 `json:"hold_amount"`
		}](t, res.Body)
		require.InDelta(t, 1.20, batch.HoldAmount, 1e-8)

		lot := env.getLotDB(lotID)
		require.InDelta(t, 0.80, lot.RemainingAmount, 1e-8)
		require.InDelta(t, 1.20, lot.FrozenAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotID)

		allocs := env.listAllocationsDB("batch_image_hold", batch.ID)
		require.Len(t, allocs, 1)
		require.True(t, allocs[0].LotID.Valid && allocs[0].LotID.Int64 == lotID)
		require.InDelta(t, 1.20, allocs[0].AllocatedAmount, 1e-8)
	})

	t.Run("RF8_02_MixedHoldFreezesBothLotAndRegular", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.60)
		env.vertex.SetMode(vertexModeStayRunning)

		u := env.createTestUser(1.50)
		lotID := env.seedExpiringLotSQL(u.ID, 0.50, time.Now().Add(24*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "c1", "prompt": "cat"},
				{"custom_id": "c2", "prompt": "dog"},
			},
		})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		batch := decodeRaw[struct {
			ID string `json:"id"`
		}](t, res.Body)

		lot := env.getLotDB(lotID)
		require.InDelta(t, 0.0, lot.RemainingAmount, 1e-8)
		require.InDelta(t, 0.50, lot.FrozenAmount, 1e-8)
		reg, frozen, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 0.80, reg, 1e-8)
		require.InDelta(t, 0.70, frozen, 1e-8)

		allocs := env.listAllocationsDB("batch_image_hold", batch.ID)
		require.Len(t, allocs, 2)
		require.True(t, allocs[0].LotID.Valid && allocs[0].LotID.Int64 == lotID)
		require.InDelta(t, 0.50, allocs[0].AllocatedAmount, 1e-8)
		require.False(t, allocs[1].LotID.Valid)
		require.InDelta(t, 0.70, allocs[1].AllocatedAmount, 1e-8)
	})

	t.Run("RF8_03_FullSettlementMovesFrozenToConsumedAndExhausts", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.60)
		env.vertex.SetMode(vertexModeAllSuccess)

		u := env.createTestUser(0)
		lotID := env.seedExpiringLotSQL(u.ID, 1.20, time.Now().Add(24*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "c1", "prompt": "cat"},
				{"custom_id": "c2", "prompt": "dog"},
			},
		})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		batch := decodeRaw[struct {
			ID string `json:"id"`
		}](t, res.Body)

		eventually(t, 20*time.Second, 200*time.Millisecond, func() bool {
			g := env.gatewayGet("/v1/images/batches/"+batch.ID, apiKey)
			if g.Status != http.StatusOK {
				return false
			}
			st := decodeRaw[struct {
				Status string `json:"status"`
			}](t, g.Body)
			return st.Status == "completed"
		}, "batch job should reach completed")

		lot := env.getLotDB(lotID)
		require.Equal(t, "exhausted", lot.Status)
		require.InDelta(t, 0.0, lot.FrozenAmount, 1e-8)
		require.InDelta(t, 1.20, lot.ConsumedAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotID)
	})

	t.Run("RF8_04_PartialSettlementConsumesFEFOAndReleasesExcessLIFO", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.60)
		env.vertex.SetMode(vertexModePartialSuccess) // 1 success ($0.60), 1 fail ($0.60 excess)

		u := env.createTestUser(0.80)
		lotID := env.seedExpiringLotSQL(u.ID, 0.80, time.Now().Add(24*time.Hour), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		// Hold $1.20 => $0.80 lot + $0.40 regular. Settle $0.60 => consumes $0.60 lot, releases $0.40 regular first + $0.20 back to lot.
		res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "c1", "prompt": "cat"},
				{"custom_id": "c2", "prompt": "dog"},
			},
		})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		batch := decodeRaw[struct {
			ID string `json:"id"`
		}](t, res.Body)

		eventually(t, 20*time.Second, 200*time.Millisecond, func() bool {
			g := env.gatewayGet("/v1/images/batches/"+batch.ID, apiKey)
			if g.Status != http.StatusOK {
				return false
			}
			st := decodeRaw[struct {
				Status string `json:"status"`
			}](t, g.Body)
			return st.Status == "completed"
		}, "partial batch job should reach completed")

		lot := env.getLotDB(lotID)
		require.Equal(t, "active", lot.Status)
		require.InDelta(t, 0.60, lot.ConsumedAmount, 1e-8)
		require.InDelta(t, 0.20, lot.RemainingAmount, 1e-8)
		require.InDelta(t, 0.0, lot.FrozenAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotID)

		reg, frozen, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 0.80, reg, 1e-8)
		require.InDelta(t, 0.0, frozen, 1e-8)
	})

	t.Run("RF8_05_CancelReleasesHoldLIFOPreservingExpiresAt", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.60)
		env.vertex.SetMode(vertexModeStayRunning)

		u := env.createTestUser(1.00)
		origExpiry := time.Now().Add(36 * time.Hour).UTC().Truncate(time.Second)
		lotID := env.seedExpiringLotSQL(u.ID, 0.50, origExpiry, "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "c1", "prompt": "cat"},
				{"custom_id": "c2", "prompt": "dog"},
			},
		})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		batch := decodeRaw[struct {
			ID string `json:"id"`
		}](t, res.Body)

		cRes := env.gatewayPost("/v1/images/batches/"+batch.ID+"/cancel", apiKey, map[string]any{})
		require.Equal(t, http.StatusOK, cRes.Status, string(cRes.Body))

		lot := env.getLotDB(lotID)
		require.Equal(t, "active", lot.Status)
		require.InDelta(t, 0.50, lot.RemainingAmount, 1e-8)
		require.InDelta(t, 0.0, lot.FrozenAmount, 1e-8)
		require.True(t, lot.ExpiresAt.UTC().Equal(origExpiry))
		verifyLotInvariant(t, env.db, lotID)

		reg, frozen, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 1.00, reg, 1e-8)
		require.InDelta(t, 0.0, frozen, 1e-8)
	})

	// ---------------- RF9: Balance Subscription Purchase & Anti-Laundering Refund (5 subtests) ----------------
	t.Run("RF9_01_BalanceSubscriptionPurchaseDeductsExpiringFirst", func(t *testing.T) {
		env := newE2EEnv(t)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		u := env.createTestUser(2.00)
		lotID := env.seedExpiringLotSQL(u.ID, 5.00, time.Now().Add(24*time.Hour), "active")

		res := env.apiPost("/api/v1/payment/orders", u.Token, map[string]any{
			"order_type":   "subscription",
			"payment_type": "balance",
			"plan_id":      planID,
		})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		order := decodeData[struct {
			OrderID int64  `json:"order_id"`
			Status  string `json:"status"`
		}](t, res.Env)
		require.Equal(t, "COMPLETED", order.Status)

		lot := env.getLotDB(lotID)
		require.Equal(t, "exhausted", lot.Status)
		require.InDelta(t, 5.00, lot.ConsumedAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotID)

		reg, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 2.00, reg, 1e-8)
	})

	t.Run("RF9_02_MixedPurchaseDeductsExpiringAndRegularInSingleTx", func(t *testing.T) {
		env := newE2EEnv(t)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		u := env.createTestUser(5.00)
		lotID := env.seedExpiringLotSQL(u.ID, 2.00, time.Now().Add(24*time.Hour), "active")

		res := env.apiPost("/api/v1/payment/orders", u.Token, map[string]any{
			"order_type":   "subscription",
			"payment_type": "balance",
			"plan_id":      planID,
		})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		order := decodeData[struct {
			OrderID int64 `json:"order_id"`
		}](t, res.Env)

		lot := env.getLotDB(lotID)
		require.Equal(t, "exhausted", lot.Status)
		require.InDelta(t, 2.00, lot.ConsumedAmount, 1e-8)

		reg, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 2.00, reg, 1e-8)

		allocs := env.listAllocationsDB("payment_order", fmt.Sprintf("%d", order.OrderID))
		require.Len(t, allocs, 2)
		require.True(t, allocs[0].LotID.Valid && allocs[0].LotID.Int64 == lotID)
		require.InDelta(t, 2.00, allocs[0].AllocatedAmount, 1e-8)
		require.False(t, allocs[1].LotID.Valid)
		require.InDelta(t, 3.00, allocs[1].AllocatedAmount, 1e-8)
	})

	t.Run("RF9_03_SubscriptionSuccessAuditRecordsExpiringRegularSplit", func(t *testing.T) {
		env := newE2EEnv(t)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		u := env.createTestUser(5.00)
		env.seedExpiringLotSQL(u.ID, 2.00, time.Now().Add(24*time.Hour), "active")

		res := env.apiPost("/api/v1/payment/orders", u.Token, map[string]any{
			"order_type":   "subscription",
			"payment_type": "balance",
			"plan_id":      planID,
		})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		order := decodeData[struct {
			OrderID int64 `json:"order_id"`
		}](t, res.Env)

		st, detEnv := env.adminGet(fmt.Sprintf("/api/v1/admin/payment/orders/%d", order.OrderID))
		require.Equal(t, http.StatusOK, st)
		detail := decodeData[struct {
			AuditLogs []struct {
				Action string `json:"action"`
				Detail string `json:"detail"`
			} `json:"auditLogs"`
		}](t, detEnv)

		var found bool
		for _, l := range detail.AuditLogs {
			if l.Action == "SUBSCRIPTION_SUCCESS" {
				found = true
				var parsed map[string]any
				require.NoError(t, json.Unmarshal([]byte(l.Detail), &parsed))
				require.InDelta(t, 2.00, parsed["expiringAmountUsed"], 1e-8)
				require.InDelta(t, 3.00, parsed["regularAmountUsed"], 1e-8)
			}
		}
		require.True(t, found, "SUBSCRIPTION_SUCCESS audit log not found")
	})

	t.Run("RF9_04_AdminFullRefundOnActiveLotReturnsRegularFirstThenLot", func(t *testing.T) {
		env := newE2EEnv(t)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		u := env.createTestUser(5.00)
		origExpiry := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
		lotID := env.seedExpiringLotSQL(u.ID, 2.00, origExpiry, "active")

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
			"amount": 5.00,
			"reason": "full refund test",
		})
		require.Equal(t, http.StatusOK, rSt, rEnv.Message)

		reg, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 5.00, reg, 1e-8)

		lot := env.getLotDB(lotID)
		require.Equal(t, "active", lot.Status)
		require.InDelta(t, 2.00, lot.RemainingAmount, 1e-8)
		require.True(t, lot.ExpiresAt.UTC().Equal(origExpiry))
		verifyLotInvariant(t, env.db, lotID)
	})

	t.Run("RF9_05_AdminPartialRefundReturnsLIFOAndWritesAudit", func(t *testing.T) {
		env := newE2EEnv(t)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		u := env.createTestUser(5.00)
		lotID := env.seedExpiringLotSQL(u.ID, 2.00, time.Now().Add(48*time.Hour), "active")

		res := env.apiPost("/api/v1/payment/orders", u.Token, map[string]any{
			"order_type":   "subscription",
			"payment_type": "balance",
			"plan_id":      planID,
		})
		require.Equal(t, http.StatusOK, res.Status)
		order := decodeData[struct {
			OrderID int64 `json:"order_id"`
		}](t, res.Env)

		// Partial refund $4.00 => LIFO returns $3.00 regular first, then $1.00 to active lot.
		rSt, rEnv := env.adminPost(fmt.Sprintf("/api/v1/admin/payment/orders/%d/refund", order.OrderID), map[string]any{
			"amount": 4.00,
			"reason": "partial LIFO refund",
		})
		require.Equal(t, http.StatusOK, rSt, rEnv.Message)

		reg, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 5.00, reg, 1e-8)
		lot := env.getLotDB(lotID)
		require.Equal(t, "active", lot.Status)
		require.InDelta(t, 1.00, lot.RemainingAmount, 1e-8)
		require.InDelta(t, 1.00, lot.ConsumedAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotID)

		st, detEnv := env.adminGet(fmt.Sprintf("/api/v1/admin/payment/orders/%d", order.OrderID))
		require.Equal(t, http.StatusOK, st)
		detail := decodeData[struct {
			AuditLogs []struct {
				Action string `json:"action"`
			} `json:"auditLogs"`
		}](t, detEnv)
		var found bool
		for _, l := range detail.AuditLogs {
			if l.Action == "BALANCE_REFUND_CREDITED" {
				found = true
			}
		}
		require.True(t, found)
	})

	// ---------------- RF10: Cache, Archiver, DTOs, /v1/usage & Admin Stats/Records (5 subtests) ----------------
	t.Run("RF10_01_UserDTOAuthMeAndProfileExposeExpiringFields", func(t *testing.T) {
		env := newE2EEnv(t)
		u := env.createTestUser(4.00)
		exp1 := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
		exp2 := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
		env.seedExpiringLotSQL(u.ID, 1.00, exp1, "active")
		env.seedExpiringLotSQL(u.ID, 2.00, exp2, "active")

		for _, ep := range []string{"/api/v1/auth/me", "/api/v1/user/profile"} {
			res := env.apiGet(ep, u.Token)
			require.Equal(t, http.StatusOK, res.Status, ep)
			dto := decodeData[struct {
				Balance               float64    `json:"balance"`
				ExpiringBalance       float64    `json:"expiring_balance"`
				TotalAvailableBalance float64    `json:"total_available_balance"`
				NearestExpiringAt     *time.Time `json:"nearest_expiring_at"`
				NearestExpiringAmount float64    `json:"nearest_expiring_amount"`
			}](t, res.Env)
			require.InDelta(t, 4.00, dto.Balance, 1e-8)
			require.InDelta(t, 3.00, dto.ExpiringBalance, 1e-8)
			require.InDelta(t, 7.00, dto.TotalAvailableBalance, 1e-8)
			require.NotNil(t, dto.NearestExpiringAt)
			require.True(t, dto.NearestExpiringAt.UTC().Equal(exp1))
			require.InDelta(t, 1.00, dto.NearestExpiringAmount, 1e-8)
		}
	})

	t.Run("RF10_02_GatewayUsageReturnsTotalAndBreakdown", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)
		u := env.createTestUser(4.00)
		exp1 := time.Now().Add(12 * time.Hour).UTC().Truncate(time.Second)
		env.seedExpiringLotSQL(u.ID, 3.00, exp1, "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		res := env.gatewayGet("/v1/usage", apiKey)
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		usage := decodeRaw[struct {
			Balance           float64    `json:"balance"`
			RegularBalance    float64    `json:"regular_balance"`
			ExpiringBalance   float64    `json:"expiring_balance"`
			NearestExpiringAt *time.Time `json:"nearest_expiring_at"`
		}](t, res.Body)
		require.InDelta(t, 7.00, usage.Balance, 1e-8)
		require.InDelta(t, 4.00, usage.RegularBalance, 1e-8)
		require.InDelta(t, 3.00, usage.ExpiringBalance, 1e-8)
		require.NotNil(t, usage.NearestExpiringAt)
	})

	t.Run("RF10_03_AdminUsersListAndDetailExposeExpiringFields", func(t *testing.T) {
		env := newE2EEnv(t)
		u := env.createTestUser(4.00)
		exp1 := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
		env.seedExpiringLotSQL(u.ID, 1.50, exp1, "active")

		st, detEnv := env.adminGet(fmt.Sprintf("/api/v1/admin/users/%d", u.ID))
		require.Equal(t, http.StatusOK, st)
		det := decodeData[struct {
			Balance               float64    `json:"balance"`
			ExpiringBalance       float64    `json:"expiring_balance"`
			TotalAvailableBalance float64    `json:"total_available_balance"`
			NearestExpiringAt     *time.Time `json:"nearest_expiring_at"`
			NearestExpiringAmount float64    `json:"nearest_expiring_amount"`
		}](t, detEnv)
		require.InDelta(t, 4.00, det.Balance, 1e-8)
		require.InDelta(t, 1.50, det.ExpiringBalance, 1e-8)
		require.InDelta(t, 5.50, det.TotalAvailableBalance, 1e-8)
		require.NotNil(t, det.NearestExpiringAt)
		require.InDelta(t, 1.50, det.NearestExpiringAmount, 1e-8)

		lstSt, lstEnv := env.adminGet("/api/v1/admin/users?search=" + u.Email)
		require.Equal(t, http.StatusOK, lstSt)
		lst := decodeData[struct {
			Items []struct {
				ID                    int64   `json:"id"`
				ExpiringBalance       float64 `json:"expiring_balance"`
				TotalAvailableBalance float64 `json:"total_available_balance"`
			} `json:"items"`
		}](t, lstEnv)
		require.Len(t, lst.Items, 1)
		require.InDelta(t, 1.50, lst.Items[0].ExpiringBalance, 1e-8)
		require.InDelta(t, 5.50, lst.Items[0].TotalAvailableBalance, 1e-8)
	})

	t.Run("RF10_04_AdminCheckinStatsReturnsTotalsAndRates", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(1.00)
		cfg["risk"] = map[string]any{"daily_total_budget_usd": 10.00}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u.Token, map[string]any{}).Status)

		// Seed an expired lot for stats verification.
		u2 := env.createTestUser(0)
		rec2 := env.seedCheckinRecordSQL(u2.ID, time.Now().In(mustLoadLoc(t, env.timezone)).AddDate(0, 0, -1).Format("2006-01-02"), 1, 1, 1.00)
		_, err := env.db.ExecContext(context.Background(), `
			INSERT INTO user_expiring_balance_lots (user_id, source_type, source_id, initial_amount, remaining_amount, consumed_amount, expired_amount, expires_at, status)
			VALUES ($1, 'checkin', $2, 1.00, 0, 0.50, 0.50, NOW() - INTERVAL '1 hour', 'expired')
		`, u2.ID, rec2)
		require.NoError(t, err)

		st, sEnv := env.adminGet("/api/v1/admin/checkin/stats?days=30")
		require.Equal(t, http.StatusOK, st)
		stats := decodeData[struct {
			TodayCheckins      int     `json:"today_checkins"`
			TodayGranted       float64 `json:"today_granted"`
			DailyBudgetLimit   float64 `json:"daily_budget_limit"`
			DailyBudgetPercent float64 `json:"daily_budget_percent"`
			RangeDays          int     `json:"range_days"`
			RangeTotalGranted  float64 `json:"range_total_granted"`
			RangeTotalConsumed float64 `json:"range_total_consumed"`
			RangeTotalExpired  float64 `json:"range_total_expired"`
			ConsumptionRate    float64 `json:"consumption_rate"`
			ExpirationRate     float64 `json:"expiration_rate"`
			DailyTrend         []any   `json:"daily_trend"`
		}](t, sEnv)

		require.Equal(t, 1, stats.TodayCheckins)
		require.InDelta(t, 1.00, stats.TodayGranted, 1e-8)
		require.InDelta(t, 10.00, stats.DailyBudgetLimit, 1e-8)
		require.InDelta(t, 10.0, stats.DailyBudgetPercent, 1e-4)
		require.Equal(t, 30, stats.RangeDays)
		require.GreaterOrEqual(t, stats.RangeTotalGranted, 1.00)
		require.InDelta(t, 0.50, stats.RangeTotalConsumed, 1e-8)
		require.InDelta(t, 0.50, stats.RangeTotalExpired, 1e-8)
		require.Greater(t, stats.ConsumptionRate, 0.0)
		require.Greater(t, stats.ExpirationRate, 0.0)
		require.NotEmpty(t, stats.DailyTrend)
	})

	t.Run("RF10_05_AdminCheckinRecordsReturnsPaginatedWithUserInfo", func(t *testing.T) {
		env := newE2EEnv(t)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.20))
		u := env.createTestUser(0)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u.Token, map[string]any{}).Status)

		st, rEnv := env.adminGet("/api/v1/admin/checkin/records?page=1&page_size=10")
		require.Equal(t, http.StatusOK, st)
		page := decodeData[struct {
			Items []struct {
				UserID      int64   `json:"user_id"`
				UserEmail   string  `json:"user_email"`
				Username    string  `json:"username"`
				TotalAmount float64 `json:"total_amount"`
			} `json:"items"`
			Total int64 `json:"total"`
		}](t, rEnv)
		require.Equal(t, int64(1), page.Total)
		require.Len(t, page.Items, 1)
		require.Equal(t, u.ID, page.Items[0].UserID)
		require.Equal(t, u.Email, page.Items[0].UserEmail)
		require.InDelta(t, 0.20, page.Items[0].TotalAmount, 1e-8)
	})
}
