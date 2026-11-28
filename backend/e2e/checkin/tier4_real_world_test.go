//go:build e2e

package checkin_e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func t4SetupOpenAIUpstream(t *testing.T, env *e2eEnv, inTok, outTok int) int64 {
	t.Helper()
	env.openai.mu.Lock()
	env.openai.inputTokens = inTok
	env.openai.outputTokens = outTok
	env.openai.mu.Unlock()
	t.Cleanup(env.openai.Reset)

	groupID := env.createGroup(map[string]any{
		"name":              "e2e-t4-oai-" + randomHex(4),
		"platform":          "openai",
		"rate_multiplier":   1.0,
		"subscription_type": "standard",
	})
	_ = env.createAccount(map[string]any{
		"name":     "e2e-t4-oai-acc-" + randomHex(4),
		"platform": "openai",
		"type":     "apikey",
		"credentials": map[string]any{
			"api_key":  "sk-openai-mock-t4",
			"base_url": env.openai.srv.URL,
		},
		"extra": map[string]any{
			"openai_responses_mode":      "force_chat_completions",
			"openai_responses_supported": false,
		},
		"concurrency": 50,
		"priority":    1,
		"group_ids":   []int64{groupID},
	})
	return groupID
}

func t4ConfigureSMTPAndNotify(t *testing.T, env *e2eEnv, threshold float64) {
	t.Helper()
	st, putEnv := env.adminPut("/api/v1/admin/settings", map[string]any{
		"smtp_host":                    env.smtpServer.host,
		"smtp_port":                    env.smtpServer.port,
		"smtp_username":                "e2e",
		"smtp_password":                "e2e",
		"smtp_from_email":              "noreply@sub2api.local",
		"smtp_from_name":               "Sub2API E2E",
		"smtp_use_tls":                 false,
		"balance_low_notify_enabled":   true,
		"balance_low_notify_threshold": threshold,
	})
	require.Equal(t, http.StatusOK, st, putEnv.Message)
}

func t4EnableUserNotifyEmail(t *testing.T, env *e2eEnv, userToken string) {
	t.Helper()
	notifyAddr := "t4_notify_" + randomHex(4) + "@example.com"
	require.Equal(t, http.StatusOK, env.apiPost("/api/v1/user/notify-email/send-code", userToken, map[string]any{
		"email": notifyAddr,
	}).Status)

	var code string
	eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
		var ok bool
		code, ok = env.smtpServer.ExtractLatestCode(notifyAddr)
		return ok
	}, "verification code should arrive on fake SMTP")

	require.Equal(t, http.StatusOK, env.apiPost("/api/v1/user/notify-email/verify", userToken, map[string]any{
		"email": notifyAddr,
		"code":  code,
	}).Status)
	require.Equal(t, http.StatusOK, env.apiPut("/api/v1/user", userToken, map[string]any{
		"balance_notify_enabled": true,
	}).Status)
	env.smtpServer.Reset()
}

func t4WaitBatchCompleted(t *testing.T, env *e2eEnv, batchID, apiKey string) {
	t.Helper()
	eventually(t, 20*time.Second, 200*time.Millisecond, func() bool {
		g := env.gatewayGet("/v1/images/batches/"+batchID, apiKey)
		if g.Status != http.StatusOK {
			return false
		}
		st := decodeRaw[struct {
			Status string `json:"status"`
		}](t, g.Body)
		return st.Status == "completed"
	}, "batch job %s should complete", batchID)
}

type t4ConcurrentOutcome struct {
	Kind   string
	Status int
	Reason string
	Err    error
}

func t4DoPostConcurrent(client *http.Client, urlStr, bearer string, body any, kind string) t4ConcurrentOutcome {
	b, err := json.Marshal(body)
	if err != nil {
		return t4ConcurrentOutcome{Kind: kind, Err: err}
	}
	req, err := http.NewRequest(http.MethodPost, urlStr, bytes.NewReader(b))
	if err != nil {
		return t4ConcurrentOutcome{Kind: kind, Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return t4ConcurrentOutcome{Kind: kind, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return t4ConcurrentOutcome{Kind: kind, Status: resp.StatusCode, Err: err}
	}
	var env apiEnvelope
	_ = json.Unmarshal(raw, &env)
	return t4ConcurrentOutcome{
		Kind:   kind,
		Status: resp.StatusCode,
		Reason: env.Reason,
	}
}

func TestTier4_RealWorldScenarios(t *testing.T) {
	// T4_01_FullUserCheckinToGatewayAndNotifyLifecycle
	t.Run("T4_01_FullUserCheckinToGatewayAndNotifyLifecycle", func(t *testing.T) {
		env := newE2EEnv(t)
		t4ConfigureSMTPAndNotify(t, env, 0.30)
		stAff, _ := env.adminPut("/api/v1/admin/settings", map[string]any{
			"affiliate_enabled":     true,
			"affiliate_rebate_rate": 10.0,
		})
		require.Equal(t, http.StatusOK, stAff)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.50))

		anthGroupID := env.setupAnthropicUpstream(t)
		// gpt-4o: 10000*2.5e-6 + 7500*1e-5 = 0.025 + 0.075 = $0.10 per request.
		oaiGroupID := t4SetupOpenAIUpstream(t, env, 10000, 7500)

		referrer := env.registerUser("t401_ref")
		affRes := env.apiGet("/api/v1/user/aff", referrer.Token)
		require.Equal(t, http.StatusOK, affRes.Status)
		affCode := decodeData[struct {
			AffCode string `json:"aff_code"`
		}](t, affRes.Env).AffCode

		userEmail := fmt.Sprintf("t401_user_%s@example.com", randomHex(5))
		regRes := env.doJSON(http.MethodPost, "/api/v1/auth/register", "", "", map[string]any{
			"email":    userEmail,
			"password": "UserPass!123",
			"aff_code": affCode,
		})
		require.Equal(t, http.StatusOK, regRes.Status, string(regRes.Body))
		regData := decodeData[struct {
			AccessToken string `json:"access_token"`
			User        struct {
				ID int64 `json:"id"`
			} `json:"user"`
		}](t, regRes.Env)

		t4EnableUserNotifyEmail(t, env, regData.AccessToken)

		// User checks in -> $0.50 expiring lot, $0 regular.
		cRes := env.apiPost("/api/v1/checkin", regData.AccessToken, map[string]any{})
		require.Equal(t, http.StatusOK, cRes.Status, string(cRes.Body))
		lots := env.listUserLotsDB(regData.User.ID)
		require.Len(t, lots, 1)

		// Verify auth/me & user/profile DTOs.
		for _, ep := range []string{"/api/v1/auth/me", "/api/v1/user/profile"} {
			r := env.apiGet(ep, regData.AccessToken)
			require.Equal(t, http.StatusOK, r.Status, ep)
			dto := decodeData[struct {
				Balance               float64 `json:"balance"`
				ExpiringBalance       float64 `json:"expiring_balance"`
				TotalAvailableBalance float64 `json:"total_available_balance"`
			}](t, r.Env)
			require.InDelta(t, 0.0, dto.Balance, 1e-8)
			require.InDelta(t, 0.50, dto.ExpiringBalance, 1e-8)
			require.InDelta(t, 0.50, dto.TotalAvailableBalance, 1e-8)
		}

		_, anthKey := env.createAPIKeyForUser(regData.AccessToken, &anthGroupID)
		_, oaiKey := env.createAPIKeyForUser(regData.AccessToken, &oaiGroupID)

		// Verify /v1/usage before gateway consumption.
		uRes := env.gatewayGet("/v1/usage", anthKey)
		require.Equal(t, http.StatusOK, uRes.Status)
		uDTO := decodeRaw[struct {
			Balance         float64 `json:"balance"`
			RegularBalance  float64 `json:"regular_balance"`
			ExpiringBalance float64 `json:"expiring_balance"`
		}](t, uRes.Body)
		require.InDelta(t, 0.50, uDTO.Balance, 1e-8)
		require.InDelta(t, 0.0, uDTO.RegularBalance, 1e-8)
		require.InDelta(t, 0.50, uDTO.ExpiringBalance, 1e-8)

		// Step 1: Anthropic $0.18 (10000 in + 10000 out) -> 0.50 -> 0.32 (>= 0.30 threshold, zero email yet).
		env.anthropic.SetTokens(10000, 10000)
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/messages", anthKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "step 1"}},
		}).Status)

		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			return approxEqual(env.getLotDB(lots[0].ID).RemainingAmount, 0.32)
		}, "remaining should drop to 0.32")
		time.Sleep(250 * time.Millisecond)
		require.Empty(t, env.smtpServer.Emails(), "must not email while total >= 0.30")

		// Step 2: OpenAI $0.10 -> 0.32 -> 0.22 (< 0.30 threshold, sends 1 alert email).
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/chat/completions", oaiKey, map[string]any{
			"model":    "gpt-4o",
			"messages": []map[string]string{{"role": "user", "content": "step 2"}},
		}).Status)

		eventually(t, 8*time.Second, 50*time.Millisecond, func() bool {
			return approxEqual(env.getLotDB(lots[0].ID).RemainingAmount, 0.22) && len(env.smtpServer.Emails()) == 1
		}, "remaining should drop to 0.22 and send 1 alert email")

		time.Sleep(250 * time.Millisecond)
		require.Len(t, env.smtpServer.Emails(), 1)
		verifyLotInvariant(t, env.db, lots[0].ID)

		_, _, totalRecharged := env.getUserDBBalance(regData.User.ID)
		require.InDelta(t, 0.0, totalRecharged, 1e-8)

		affAfter := env.apiGet("/api/v1/user/aff", referrer.Token)
		require.Equal(t, http.StatusOK, affAfter.Status)
		affStats := decodeData[struct {
			AffQuota        float64 `json:"aff_quota"`
			AffFrozenQuota  float64 `json:"aff_frozen_quota"`
			AffHistoryQuota float64 `json:"aff_history_quota"`
		}](t, affAfter.Env)
		require.InDelta(t, 0.0, affStats.AffQuota, 1e-8)
		require.InDelta(t, 0.0, affStats.AffFrozenQuota, 1e-8)
		require.InDelta(t, 0.0, affStats.AffHistoryQuota, 1e-8)
	})

	// T4_02_MultiDayStreakAndMilestoneCycleWorkflow
	t.Run("T4_02_MultiDayStreakAndMilestoneCycleWorkflow", func(t *testing.T) {
		env := newE2EEnv(t)
		cfg := defaultEnabledCheckinConfig(0.10)
		cfg["validity_days"] = 7
		cfg["milestones"] = []map[string]any{
			{"days": 2, "bonus_amount": 0.50},
			{"days": 4, "bonus_amount": 1.00},
		}
		cfg["milestone_cycle_repeat"] = false
		env.enableCheckinConfig(cfg)

		u1 := env.createTestUser(0)

		type checkinResp struct {
			Record struct {
				StreakDays           int     `json:"streak_days"`
				MilestoneCycleDay    int     `json:"milestone_cycle_day"`
				BaseAmount           float64 `json:"base_amount"`
				MilestoneBonusAmount float64 `json:"milestone_bonus_amount"`
				TotalAmount          float64 `json:"total_amount"`
			} `json:"record"`
		}

		expectedDays := []struct {
			streak int
			cycle  int
			bonus  float64
			total  float64
		}{
			{1, 1, 0.00, 0.10},
			{2, 2, 0.50, 0.60},
			{3, 3, 0.00, 0.10},
			{4, 4, 1.00, 1.10},
			{5, 5, 0.00, 0.10}, // repeat=false: no wrap or bonus on Day 5
		}

		for idx, want := range expectedDays {
			if idx > 0 {
				_, err := env.db.ExecContext(context.Background(),
					`UPDATE user_checkin_records SET checkin_date = checkin_date - INTERVAL '1 day' WHERE user_id = $1`,
					u1.ID,
				)
				require.NoError(t, err)
			}
			res := env.apiPost("/api/v1/checkin", u1.Token, map[string]any{})
			require.Equal(t, http.StatusOK, res.Status, "day %d: %s", idx+1, string(res.Body))
			got := decodeData[checkinResp](t, res.Env)
			require.Equal(t, want.streak, got.Record.StreakDays, "day %d streak", idx+1)
			require.Equal(t, want.cycle, got.Record.MilestoneCycleDay, "day %d cycleDay", idx+1)
			require.InDelta(t, want.bonus, got.Record.MilestoneBonusAmount, 1e-8, "day %d bonus", idx+1)
			require.InDelta(t, want.total, got.Record.TotalAmount, 1e-8, "day %d total", idx+1)
		}

		// Day 6 after a missed day (shift -2 days): streak and cycle_day reset to 1.
		_, err := env.db.ExecContext(context.Background(),
			`UPDATE user_checkin_records SET checkin_date = checkin_date - INTERVAL '2 days' WHERE user_id = $1`,
			u1.ID,
		)
		require.NoError(t, err)
		resMissed := env.apiPost("/api/v1/checkin", u1.Token, map[string]any{})
		require.Equal(t, http.StatusOK, resMissed.Status, string(resMissed.Body))
		gotMissed := decodeData[checkinResp](t, resMissed.Env)
		require.Equal(t, 1, gotMissed.Record.StreakDays)
		require.Equal(t, 1, gotMissed.Record.MilestoneCycleDay)
		require.InDelta(t, 0.0, gotMissed.Record.MilestoneBonusAmount, 1e-8)

		// Verify calendar (current month contains today's check-in), paginated records, and lots audit.
		loc := mustLoadLoc(t, env.timezone)
		now := time.Now().In(loc)
		calRes := env.apiGet("/api/v1/checkin/calendar?month="+now.Format("2006-01"), u1.Token)
		require.Equal(t, http.StatusOK, calRes.Status)
		cal := decodeData[struct {
			MonthCheckinDays int     `json:"month_checkin_days"`
			MonthTotalAmount float64 `json:"month_total_amount"`
			CheckedDates     []struct {
				Date string `json:"date"`
			} `json:"checked_dates"`
		}](t, calRes.Env)
		require.GreaterOrEqual(t, cal.MonthCheckinDays, 1)
		require.GreaterOrEqual(t, cal.MonthTotalAmount, 0.10)

		recRes := env.apiGet("/api/v1/checkin/records?page=1&page_size=4", u1.Token)
		require.Equal(t, http.StatusOK, recRes.Status)
		recPage := decodeData[struct {
			Items []any `json:"items"`
			Total int64 `json:"total"`
			Pages int   `json:"pages"`
		}](t, recRes.Env)
		require.Equal(t, int64(6), recPage.Total)
		require.Equal(t, 2, recPage.Pages)
		require.Len(t, recPage.Items, 4)

		lotRes := env.apiGet("/api/v1/checkin/lots?page=1&page_size=10&status=all", u1.Token)
		require.Equal(t, http.StatusOK, lotRes.Status)
		lotPage := decodeData[struct {
			Total int64 `json:"total"`
		}](t, lotRes.Env)
		require.Equal(t, int64(6), lotPage.Total)
		for _, l := range env.listUserLotsDB(u1.ID) {
			verifyLotInvariant(t, env.db, l.ID)
		}

		// Phase B: milestone_cycle_repeat = true wraps Day 5 -> cycle_day=1 and Day 6 -> cycle_day=2 (+0.50).
		cfg["milestone_cycle_repeat"] = true
		env.enableCheckinConfig(cfg)
		u2 := env.createTestUser(0)
		yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
		env.seedCheckinRecordSQL(u2.ID, yesterday, 4, 4, 1.10)

		r5 := env.apiPost("/api/v1/checkin", u2.Token, map[string]any{})
		require.Equal(t, http.StatusOK, r5.Status)
		d5 := decodeData[checkinResp](t, r5.Env)
		require.Equal(t, 5, d5.Record.StreakDays)
		require.Equal(t, 1, d5.Record.MilestoneCycleDay)
		require.InDelta(t, 0.0, d5.Record.MilestoneBonusAmount, 1e-8)

		_, err = env.db.ExecContext(context.Background(),
			`UPDATE user_checkin_records SET checkin_date = checkin_date - INTERVAL '1 day' WHERE user_id = $1`,
			u2.ID,
		)
		require.NoError(t, err)
		r6 := env.apiPost("/api/v1/checkin", u2.Token, map[string]any{})
		require.Equal(t, http.StatusOK, r6.Status)
		d6 := decodeData[checkinResp](t, r6.Env)
		require.Equal(t, 6, d6.Record.StreakDays)
		require.Equal(t, 2, d6.Record.MilestoneCycleDay)
		require.InDelta(t, 0.50, d6.Record.MilestoneBonusAmount, 1e-8)
		require.InDelta(t, 0.60, d6.Record.TotalAmount, 1e-8)
	})

	// T4_03_BatchImageCrossMidnightAntiLaunderingWorkflow
	t.Run("T4_03_BatchImageCrossMidnightAntiLaunderingWorkflow", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.50)
		env.vertex.SetMode(vertexModeStayRunning)

		u := env.createTestUser(0.70)
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		lotA := env.seedExpiringLotSQL(u.ID, 0.80, time.Now().Add(2*time.Hour).UTC().Truncate(time.Second), "active")
		lotBExp := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
		lotB := env.seedExpiringLotSQL(u.ID, 0.50, lotBExp, "active")

		// Job 1 ($1.00 hold): freezes Lot A ($0.80) + Lot B ($0.20).
		j1Res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "j1_1", "prompt": "a"},
				{"custom_id": "j1_2", "prompt": "b"},
			},
		})
		require.Equal(t, http.StatusOK, j1Res.Status, string(j1Res.Body))
		job1ID := decodeRaw[struct {
			ID string `json:"id"`
		}](t, j1Res.Body).ID

		// Job 2 ($1.00 hold): freezes remaining Lot B ($0.30) + Regular ($0.70).
		j2Res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "j2_1", "prompt": "c"},
				{"custom_id": "j2_2", "prompt": "d"},
			},
		})
		require.Equal(t, http.StatusOK, j2Res.Status, string(j2Res.Body))
		job2ID := decodeRaw[struct {
			ID string `json:"id"`
		}](t, j2Res.Body).ID

		// Seed Redis billing cache before cross-midnight expiry.
		require.Equal(t, http.StatusOK, env.gatewayGet("/v1/usage", apiKey).Status)

		// Lot A expires across midnight while Lot B stays active.
		_, err := env.db.ExecContext(context.Background(),
			`UPDATE user_expiring_balance_lots SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`,
			lotA,
		)
		require.NoError(t, err)

		// Cancel Job 2 -> LIFO returns $0.70 Regular to users.balance and $0.30 back to Lot B.
		cancelRes := env.gatewayPost("/v1/images/batches/"+job2ID+"/cancel", apiKey, map[string]any{})
		require.Equal(t, http.StatusOK, cancelRes.Status, string(cancelRes.Body))

		// Settle Job 1 with partial success: 1 success ($0.50 captured FEFO from Lot A),
		// $0.50 excess released LIFO ($0.20 back to active Lot B, $0.30 from expired Lot A forfeited!).
		env.vertex.SetMode(vertexModePartialSuccess)
		t4WaitBatchCompleted(t, env, job1ID, apiKey)

		reg, frozen, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 0.70, reg, 1e-8, "expired Lot A must never convert into users.balance")
		require.InDelta(t, 0.0, frozen, 1e-8)

		a := env.getLotDB(lotA)
		require.Equal(t, "expired", a.Status)
		require.InDelta(t, 0.50, a.ConsumedAmount, 1e-8)
		require.InDelta(t, 0.30, a.ExpiredAmount, 1e-8)
		require.InDelta(t, 0.0, a.RemainingAmount, 1e-8)
		require.InDelta(t, 0.0, a.FrozenAmount, 1e-8)

		b := env.getLotDB(lotB)
		require.Equal(t, "active", b.Status)
		require.InDelta(t, 0.50, b.RemainingAmount, 1e-8)
		require.InDelta(t, 0.0, b.FrozenAmount, 1e-8)
		require.True(t, b.ExpiresAt.UTC().Equal(lotBExp))

		verifyLotInvariant(t, env.db, lotA)
		verifyLotInvariant(t, env.db, lotB)

		allocs1 := env.listAllocationsDB("batch_image_hold", job1ID)
		require.Len(t, allocs1, 2)
		require.InDelta(t, 0.30, allocs1[0].ForfeitedExpiredAmount, 1e-8)
		require.InDelta(t, 0.20, allocs1[1].ReleasedAmount, 1e-8)

		// Subsequent /v1/usage reflects invalidated cache and exact $1.20 total ($0.70 regular + $0.50 Lot B).
		uAfter := env.gatewayGet("/v1/usage", apiKey)
		require.Equal(t, http.StatusOK, uAfter.Status)
		uDTO := decodeRaw[struct {
			Balance         float64 `json:"balance"`
			RegularBalance  float64 `json:"regular_balance"`
			ExpiringBalance float64 `json:"expiring_balance"`
		}](t, uAfter.Body)
		require.InDelta(t, 1.20, uDTO.Balance, 1e-8)
		require.InDelta(t, 0.70, uDTO.RegularBalance, 1e-8)
		require.InDelta(t, 0.50, uDTO.ExpiringBalance, 1e-8)
	})

	// T4_04_BalanceSubscriptionPurchaseAndRefundAuditWorkflow
	t.Run("T4_04_BalanceSubscriptionPurchaseAndRefundAuditWorkflow", func(t *testing.T) {
		env := newE2EEnv(t)
		stAff, _ := env.adminPut("/api/v1/admin/settings", map[string]any{
			"affiliate_enabled":     true,
			"affiliate_rebate_rate": 10.0,
		})
		require.Equal(t, http.StatusOK, stAff)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(2.00))

		referrer := env.registerUser("t404_ref")
		affCode := decodeData[struct {
			AffCode string `json:"aff_code"`
		}](t, env.apiGet("/api/v1/user/aff", referrer.Token).Env).AffCode

		// User 1: Pre-expiry refund restores Regular + active Lot A with original expires_at.
		u1Email := fmt.Sprintf("t404_u1_%s@example.com", randomHex(4))
		reg1 := env.doJSON(http.MethodPost, "/api/v1/auth/register", "", "", map[string]any{
			"email": u1Email, "password": "UserPass!123", "aff_code": affCode,
		})
		require.Equal(t, http.StatusOK, reg1.Status)
		u1Data := decodeData[struct {
			AccessToken string `json:"access_token"`
			User        struct {
				ID int64 `json:"id"`
			} `json:"user"`
		}](t, reg1.Env)
		env.setUserRegularBalanceSQL(u1Data.User.ID, 4.00)

		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u1Data.AccessToken, map[string]any{}).Status)
		lot1 := env.listUserLotsDB(u1Data.User.ID)[0]
		origExp1 := lot1.ExpiresAt

		ord1 := env.apiPost("/api/v1/payment/orders", u1Data.AccessToken, map[string]any{
			"order_type": "subscription", "payment_type": "balance", "plan_id": planID,
		})
		require.Equal(t, http.StatusOK, ord1.Status)
		o1ID := decodeData[struct {
			OrderID int64 `json:"order_id"`
		}](t, ord1.Env).OrderID

		r1St, _ := env.adminPost(fmt.Sprintf("/api/v1/admin/payment/orders/%d/refund", o1ID), map[string]any{
			"amount": 5.00, "reason": "pre-expiry full refund",
		})
		require.Equal(t, http.StatusOK, r1St)

		regBal1, _, recharged1 := env.getUserDBBalance(u1Data.User.ID)
		require.InDelta(t, 4.00, regBal1, 1e-8)
		require.InDelta(t, 0.0, recharged1, 1e-8)
		lot1After := env.getLotDB(lot1.ID)
		require.Equal(t, "active", lot1After.Status)
		require.InDelta(t, 2.00, lot1After.RemainingAmount, 1e-8)
		require.True(t, lot1After.ExpiresAt.Equal(origExp1))
		verifyLotInvariant(t, env.db, lot1.ID)

		// User 2: Post-expiry refund restores Regular only and forfeits expired Lot B.
		u2 := env.createTestUser(4.00)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u2.Token, map[string]any{}).Status)
		lot2 := env.listUserLotsDB(u2.ID)[0]

		ord2 := env.apiPost("/api/v1/payment/orders", u2.Token, map[string]any{
			"order_type": "subscription", "payment_type": "balance", "plan_id": planID,
		})
		require.Equal(t, http.StatusOK, ord2.Status)
		o2ID := decodeData[struct {
			OrderID int64 `json:"order_id"`
		}](t, ord2.Env).OrderID

		_, err := env.db.ExecContext(context.Background(), `UPDATE user_expiring_balance_lots SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, lot2.ID)
		require.NoError(t, err)

		r2St, _ := env.adminPost(fmt.Sprintf("/api/v1/admin/payment/orders/%d/refund", o2ID), map[string]any{
			"amount": 5.00, "reason": "post-expiry full refund",
		})
		require.Equal(t, http.StatusOK, r2St)

		regBal2, _, recharged2 := env.getUserDBBalance(u2.ID)
		require.InDelta(t, 4.00, regBal2, 1e-8)
		require.InDelta(t, 0.0, recharged2, 1e-8)
		lot2After := env.getLotDB(lot2.ID)
		require.Equal(t, "expired", lot2After.Status)
		require.InDelta(t, 2.00, lot2After.ExpiredAmount, 1e-8)
		verifyLotInvariant(t, env.db, lot2.ID)

		detSt, detEnv := env.adminGet(fmt.Sprintf("/api/v1/admin/payment/orders/%d", o2ID))
		require.Equal(t, http.StatusOK, detSt)
		det := decodeData[struct {
			AuditLogs []struct {
				Action string `json:"action"`
			} `json:"auditLogs"`
		}](t, detEnv)
		var hasSubAudit, hasRefundAudit bool
		for _, l := range det.AuditLogs {
			if l.Action == "SUBSCRIPTION_SUCCESS" {
				hasSubAudit = true
			}
			if l.Action == "BALANCE_REFUND_CREDITED" {
				hasRefundAudit = true
			}
		}
		require.True(t, hasSubAudit)
		require.True(t, hasRefundAudit)

		affStats := decodeData[struct {
			AffHistoryQuota float64 `json:"aff_history_quota"`
		}](t, env.apiGet("/api/v1/user/aff", referrer.Token).Env)
		require.InDelta(t, 0.0, affStats.AffHistoryQuota, 1e-8)
	})

	// T4_05_ConcurrencyIdempotencyBudgetAndLockOrderStorm
	t.Run("T4_05_ConcurrencyIdempotencyBudgetAndLockOrderStorm", func(t *testing.T) {
		env := newE2EEnv(t)

		// 1. 20 concurrent POST /api/v1/checkin calls with the same user JWT -> 1x 200, 19x 409 CHECKIN_ALREADY_DONE.
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.25))
		uSingle := env.createTestUser(0)

		const nDup = 20
		dupCh := make(chan t4ConcurrentOutcome, nDup)
		start1 := make(chan struct{})
		var wg1 sync.WaitGroup
		for i := 0; i < nDup; i++ {
			wg1.Add(1)
			go func() {
				defer wg1.Done()
				<-start1
				dupCh <- t4DoPostConcurrent(env.httpClient, env.baseURL+"/api/v1/checkin", uSingle.Token, map[string]any{}, "dup")
			}()
		}
		close(start1)
		wg1.Wait()
		close(dupCh)

		okCount1, conflictCount1 := 0, 0
		for out := range dupCh {
			require.NoError(t, out.Err)
			switch out.Status {
			case http.StatusOK:
				okCount1++
			case http.StatusConflict:
				require.Equal(t, "CHECKIN_ALREADY_DONE", out.Reason)
				conflictCount1++
			default:
				t.Fatalf("unexpected status=%d reason=%s", out.Status, out.Reason)
			}
		}
		require.Equal(t, 1, okCount1)
		require.Equal(t, nDup-1, conflictCount1)
		require.Len(t, env.listUserLotsDB(uSingle.ID), 1)

		// 2. 10 distinct users concurrently calling POST /api/v1/checkin against tight daily_total_budget_usd.
		cfgBudget := defaultEnabledCheckinConfig(0.30)
		cfgBudget["risk"] = map[string]any{"daily_total_budget_usd": 1.00}
		env.enableCheckinConfig(cfgBudget)

		const nUsers = 10
		users := make([]testUser, nUsers)
		for i := 0; i < nUsers; i++ {
			users[i] = env.createTestUser(0)
		}

		// Account for uSingle ($0.25) already granted today => remaining budget $0.75 => at most 2 more ($0.60).
		budCh := make(chan t4ConcurrentOutcome, nUsers)
		start2 := make(chan struct{})
		var wg2 sync.WaitGroup
		for i := 0; i < nUsers; i++ {
			wg2.Add(1)
			go func(tok string) {
				defer wg2.Done()
				<-start2
				budCh <- t4DoPostConcurrent(env.httpClient, env.baseURL+"/api/v1/checkin", tok, map[string]any{}, "budget")
			}(users[i].Token)
		}
		close(start2)
		wg2.Wait()
		close(budCh)

		okCount2, exhaustedCount2 := 0, 0
		for out := range budCh {
			require.NoError(t, out.Err)
			switch out.Status {
			case http.StatusOK:
				okCount2++
			case http.StatusTooManyRequests:
				require.Equal(t, "CHECKIN_DAILY_BUDGET_EXHAUSTED", out.Reason)
				exhaustedCount2++
			default:
				t.Fatalf("unexpected budget storm status=%d reason=%s", out.Status, out.Reason)
			}
		}
		require.Equal(t, 2, okCount2)
		require.Equal(t, nUsers-2, exhaustedCount2)

		today := time.Now().In(mustLoadLoc(t, env.timezone)).Format("2006-01-02")
		var totalGranted float64
		err := env.db.QueryRowContext(context.Background(),
			`SELECT total_granted FROM checkin_daily_budgets WHERE checkin_date = $1::date`, today,
		).Scan(&totalGranted)
		require.NoError(t, err)
		require.LessOrEqual(t, totalGranted, 1.00+1e-8)

		// 3. Concurrent gateway /v1/messages + /v1/images/batches + /api/v1/payment/orders + /api/v1/checkin
		// for the SAME user -> zero deadlocks (40P01) / 500s, and all lot invariants hold.
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.50))
		anthGrp := env.setupAnthropicUpstream(t)
		env.anthropic.SetTokens(10000, 18000) // $0.30
		batchGrp := env.setupVertexBatchImageUpstream(t, 0.50)
		env.vertex.SetMode(vertexModeStayRunning)
		planID := env.enableBalancePaymentAndCreatePlan(2.00)

		uStorm := env.createTestUser(20.00)
		env.seedExpiringLotSQL(uStorm.ID, 3.00, time.Now().Add(24*time.Hour), "active")
		_, anthKey := env.createAPIKeyForUser(uStorm.Token, &anthGrp)
		_, batchKey := env.createAPIKeyForUser(uStorm.Token, &batchGrp)

		stormCh := make(chan t4ConcurrentOutcome, 11)
		start3 := make(chan struct{})
		var wg3 sync.WaitGroup

		for i := 0; i < 4; i++ {
			wg3.Add(1)
			go func() {
				defer wg3.Done()
				<-start3
				stormCh <- t4DoPostConcurrent(env.httpClient, env.baseURL+"/v1/messages", anthKey, map[string]any{
					"model":      "claude-sonnet-4-5",
					"max_tokens": 16,
					"messages":   []map[string]string{{"role": "user", "content": "storm"}},
				}, "gateway")
			}()
		}
		for i := 0; i < 2; i++ {
			wg3.Add(1)
			go func(idx int) {
				defer wg3.Done()
				<-start3
				stormCh <- t4DoPostConcurrent(env.httpClient, env.baseURL+"/v1/images/batches", batchKey, map[string]any{
					"model": "gemini-2.5-flash-image",
					"items": []map[string]any{{"custom_id": fmt.Sprintf("s%d", idx), "prompt": "storm"}},
				}, "batch_image")
			}(i)
		}
		for i := 0; i < 2; i++ {
			wg3.Add(1)
			go func() {
				defer wg3.Done()
				<-start3
				stormCh <- t4DoPostConcurrent(env.httpClient, env.baseURL+"/api/v1/payment/orders", uStorm.Token, map[string]any{
					"order_type": "subscription", "payment_type": "balance", "plan_id": planID,
				}, "payment")
			}()
		}
		for i := 0; i < 3; i++ {
			wg3.Add(1)
			go func() {
				defer wg3.Done()
				<-start3
				stormCh <- t4DoPostConcurrent(env.httpClient, env.baseURL+"/api/v1/checkin", uStorm.Token, map[string]any{}, "checkin")
			}()
		}
		close(start3)
		wg3.Wait()
		close(stormCh)

		gatewayOK, batchOK, paymentOK, checkinOK := 0, 0, 0, 0
		for out := range stormCh {
			require.NoError(t, out.Err, "kind=%s", out.Kind)
			require.Less(t, out.Status, 500, "unexpected 5xx/deadlock on kind=%s status=%d reason=%s", out.Kind, out.Status, out.Reason)
			switch out.Kind {
			case "gateway":
				require.Equal(t, http.StatusOK, out.Status, "unexpected gateway storm status=%d reason=%s", out.Status, out.Reason)
				gatewayOK++
			case "batch_image":
				if out.Status == http.StatusOK {
					batchOK++
				} else {
					require.Equal(t, http.StatusPaymentRequired, out.Status, "unexpected batch_image storm status=%d reason=%s", out.Status, out.Reason)
					require.Equal(t, "BATCH_IMAGE_INSUFFICIENT_BALANCE", out.Reason)
				}
			case "payment":
				if out.Status == http.StatusOK {
					paymentOK++
				} else {
					require.Equal(t, "INSUFFICIENT_BALANCE", out.Reason, "unexpected payment storm status=%d reason=%s", out.Status, out.Reason)
				}
			case "checkin":
				if out.Status == http.StatusOK {
					checkinOK++
				} else {
					require.Equal(t, http.StatusConflict, out.Status, "unexpected checkin storm status=%d reason=%s", out.Status, out.Reason)
					require.Equal(t, "CHECKIN_ALREADY_DONE", out.Reason)
				}
			}
		}
		require.Equal(t, 4, gatewayOK)
		require.GreaterOrEqual(t, batchOK, 1, "at least one batch_image hold must succeed during storm")
		require.GreaterOrEqual(t, paymentOK, 1, "at least one balance subscription purchase must succeed during storm")
		require.Equal(t, 1, checkinOK)

		// Let async gateway usage tasks settle before verifying lot invariants.
		eventually(t, 6*time.Second, 100*time.Millisecond, func() bool {
			lots := env.listUserLotsDB(uStorm.ID)
			totalConsumed := 0.0
			totalFrozen := 0.0
			for _, l := range lots {
				totalConsumed += l.ConsumedAmount
				totalFrozen += l.FrozenAmount
			}
			return totalConsumed >= 1.20-1e-8 && totalFrozen >= 0.0
		}, "storm billing should settle")

		for _, l := range env.listUserLotsDB(uStorm.ID) {
			verifyLotInvariant(t, env.db, l.ID)
		}
		regStorm, frozenStorm, _ := env.getUserDBBalance(uStorm.ID)
		require.GreaterOrEqual(t, regStorm, 0.0)
		require.GreaterOrEqual(t, frozenStorm, 0.0)
	})

	// T4_06_AdminOperationsEligibilityAndAntiAbuseWorkflow
	t.Run("T4_06_AdminOperationsEligibilityAndAntiAbuseWorkflow", func(t *testing.T) {
		env := newE2EEnv(t)

		// 1. Admin configures check-in & verify audit log.
		uniqueDesc := "T4_06 audit test " + randomHex(6)
		cfg := defaultEnabledCheckinConfig(0.25)
		cfg["rules_description"] = uniqueDesc
		cfg["eligibility"] = map[string]any{
			"min_account_age_days":   7,
			"min_total_recharged":    10.00,
			"max_available_balance":  5.00,
			"allowed_signup_sources": []string{"linuxdo"},
		}
		cfg["risk"] = map[string]any{
			"captcha_enabled":         false,
			"max_accounts_per_ip_day": 1,
		}
		env.enableCheckinConfig(cfg)

		env.requireCheckinAuditLogRecorded(t, uniqueDesc)

		// 2. Test all 4 eligibility gates sequentially on an applicant user.
		uEmail := env.registerUser("t406_gate")
		// Gate 1: min_account_age_days=7 fails on brand new user.
		rAge := env.apiPost("/api/v1/checkin", uEmail.Token, map[string]any{})
		require.Equal(t, http.StatusForbidden, rAge.Status)
		require.Equal(t, "CHECKIN_INELIGIBLE_ACCOUNT_AGE", rAge.Env.Reason)

		_, err := env.db.ExecContext(context.Background(), `UPDATE users SET created_at = NOW() - INTERVAL '8 days' WHERE id = $1`, uEmail.ID)
		require.NoError(t, err)

		// Gate 2: min_total_recharged=10.00 fails at 9.99.
		_, err = env.db.ExecContext(context.Background(), `UPDATE users SET total_recharged = 9.99 WHERE id = $1`, uEmail.ID)
		require.NoError(t, err)
		rRech := env.apiPost("/api/v1/checkin", uEmail.Token, map[string]any{})
		require.Equal(t, http.StatusForbidden, rRech.Status)
		require.Equal(t, "CHECKIN_INELIGIBLE_RECHARGE", rRech.Env.Reason)

		_, err = env.db.ExecContext(context.Background(), `UPDATE users SET total_recharged = 10.00 WHERE id = $1`, uEmail.ID)
		require.NoError(t, err)

		// Gate 3: allowed_signup_sources=["linuxdo"] fails for email-only user.
		rSrc := env.apiPost("/api/v1/checkin", uEmail.Token, map[string]any{})
		require.Equal(t, http.StatusForbidden, rSrc.Status)
		require.Equal(t, "CHECKIN_INELIGIBLE_SOURCE", rSrc.Env.Reason)

		bSt, bEnv := env.adminPost(fmt.Sprintf("/api/v1/admin/users/%d/auth-identities", uEmail.ID), map[string]any{
			"provider_type":    "linuxdo",
			"provider_key":     "linuxdo",
			"provider_subject": "ld_" + randomHex(4),
		})
		require.Equal(t, http.StatusOK, bSt, bEnv.Message)

		// Gate 4: max_available_balance=5.00 fails when regular+expiring >= 5.00.
		env.setUserRegularBalanceSQL(uEmail.ID, 5.00)
		rCap := env.apiPost("/api/v1/checkin", uEmail.Token, map[string]any{})
		require.Equal(t, http.StatusForbidden, rCap.Status)
		require.Equal(t, "CHECKIN_BALANCE_CAP_EXCEEDED", rCap.Env.Reason)

		// Lower balance below cap -> passes!
		env.setUserRegularBalanceSQL(uEmail.ID, 4.99)
		hdrIP := map[string]string{"X-Forwarded-For": "198.51.100.200"}
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", uEmail.Token, map[string]any{}, hdrIP).Status)

		// 3. IP cap (max_accounts_per_ip_day=1) rejects 2nd eligible user on same IP.
		u2 := env.createTestUser(0)
		_, err = env.db.ExecContext(context.Background(),
			`UPDATE users SET created_at = NOW() - INTERVAL '8 days', total_recharged = 10.00, signup_source = 'linuxdo' WHERE id = $1`,
			u2.ID,
		)
		require.NoError(t, err)
		rIP := env.apiPost("/api/v1/checkin", u2.Token, map[string]any{}, hdrIP)
		require.Equal(t, http.StatusTooManyRequests, rIP.Status)
		require.Equal(t, "CHECKIN_IP_LIMIT_EXCEEDED", rIP.Env.Reason)

		// 4. Captcha fail-closed: enabled without provider -> CHECKIN_CAPTCHA_NOT_CONFIGURED;
		// with Tencent provider -> CAPTCHA_REQUIRED and CAPTCHA_INVALID.
		cfg["risk"] = map[string]any{"captcha_enabled": true}
		env.enableCheckinConfig(cfg)
		rNoProv := env.apiPost("/api/v1/checkin", u2.Token, map[string]any{})
		require.Equal(t, "CHECKIN_CAPTCHA_NOT_CONFIGURED", rNoProv.Env.Reason)

		stTen, _ := env.adminPut("/api/v1/admin/settings", map[string]any{
			"tencent_captcha_enabled":          true,
			"tencent_captcha_app_id":           "1900000002",
			"tencent_captcha_app_secret_key":   "dummy-app-secret",
			"tencent_captcha_cloud_secret_id":  "dummy-cloud-id",
			"tencent_captcha_cloud_secret_key": "dummy-cloud-key",
			"tencent_captcha_region":           "cn",
		})
		require.Equal(t, http.StatusOK, stTen)

		rReq := env.apiPost("/api/v1/checkin", u2.Token, map[string]any{})
		require.Equal(t, "CAPTCHA_REQUIRED", rReq.Env.Reason)

		rInv := env.apiPost("/api/v1/checkin", u2.Token, map[string]any{
			"tencent_captcha_ticket":  "trerror_e2e_invalid",
			"tencent_captcha_randstr": "bogus",
		})
		require.Equal(t, "CAPTCHA_INVALID", rInv.Env.Reason)

		// 5. Admin stats & filtered records reflect uEmail's check-in.
		statSt, statEnv := env.adminGet("/api/v1/admin/checkin/stats?days=30")
		require.Equal(t, http.StatusOK, statSt)
		stats := decodeData[struct {
			TodayCheckins int     `json:"today_checkins"`
			TodayGranted  float64 `json:"today_granted"`
		}](t, statEnv)
		require.Equal(t, 1, stats.TodayCheckins)
		require.InDelta(t, 0.25, stats.TodayGranted, 1e-8)

		recSt, recEnv := env.adminGet("/api/v1/admin/checkin/records?client_ip=198.51.100.200")
		require.Equal(t, http.StatusOK, recSt)
		recPage := decodeData[struct {
			Total int64 `json:"total"`
		}](t, recEnv)
		require.Equal(t, int64(1), recPage.Total)
	})

	// T4_07_MidnightExpiryBoundaryAndCacheRecoveryWorkflow
	t.Run("T4_07_MidnightExpiryBoundaryAndCacheRecoveryWorkflow", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)
		loc := mustLoadLoc(t, env.timezone)

		cfg := defaultEnabledCheckinConfig(1.00)
		cfg["validity_days"] = 1
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		// Day 1 check-in -> expires_at == next 00:00:00 in Asia/Shanghai.
		before := time.Now().In(loc)
		c1 := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, c1.Status)
		d1 := decodeData[struct {
			Record struct {
				ID        int64     `json:"id"`
				ExpiresAt time.Time `json:"expires_at"`
			} `json:"record"`
		}](t, c1.Env)

		expectedMidnight := startOfDayInLoc(before, loc).AddDate(0, 0, 1)
		if !d1.Record.ExpiresAt.Equal(expectedMidnight) {
			require.True(t, d1.Record.ExpiresAt.Equal(startOfDayInLoc(time.Now().In(loc), loc).AddDate(0, 0, 1)))
		}

		// Simulate 23:59:57 -> 3 seconds before midnight rollover, and shift checkin_date to yesterday.
		lots := env.listUserLotsDB(u.ID)
		require.Len(t, lots, 1)
		lot1 := lots[0]
		_, err := env.db.ExecContext(context.Background(),
			`UPDATE user_expiring_balance_lots SET expires_at = NOW() + INTERVAL '3 seconds' WHERE id = $1`,
			lot1.ID,
		)
		require.NoError(t, err)
		_, err = env.db.ExecContext(context.Background(),
			`UPDATE user_checkin_records SET checkin_date = checkin_date - INTERVAL '1 day' WHERE id = $1`,
			d1.Record.ID,
		)
		require.NoError(t, err)
		balKey := fmt.Sprintf("billing:balance:%d", u.ID)
		_ = env.rdb.Del(context.Background(), balKey).Err()

		// Populate Redis billing:balance:{id} (TTL truncated <= 3s) and deduct $0.30 keeping KEEPTTL.
		env.anthropic.SetTokens(10000, 18000) // $0.30
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "pre-midnight"}},
		}).Status)

		var ttl time.Duration
		eventually(t, 3*time.Second, 25*time.Millisecond, func() bool {
			var tErr error
			ttl, tErr = env.rdb.TTL(context.Background(), balKey).Result()
			return tErr == nil && ttl > 0
		}, "billing:balance cache key should be populated with positive TTL")
		require.Greater(t, ttl, time.Duration(0), "TTL must be positive after cache write")
		require.LessOrEqual(t, ttl, 3*time.Second, "TTL must be truncated to <= remaining lifetime")

		eventually(t, 3*time.Second, 50*time.Millisecond, func() bool {
			return approxEqual(env.getLotDB(lot1.ID).ConsumedAmount, 0.30)
		}, "pre-midnight deduction should settle $0.30")

		// Cross 00:00:00 expiry boundary.
		time.Sleep(3500 * time.Millisecond)

		// Gateway must immediately reject stale balance (403), with zero stale cache hit.
		rejected := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "post-midnight rejected"}},
		})
		require.Equal(t, http.StatusForbidden, rejected.Status)
		verifyLotInvariant(t, env.db, lot1.ID)

		// User can immediately check in for the new day (streak_days == 2) and resume gateway calls.
		c2 := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, c2.Status, string(c2.Body))
		d2 := decodeData[struct {
			Record struct {
				StreakDays int `json:"streak_days"`
			} `json:"record"`
		}](t, c2.Env)
		require.Equal(t, 2, d2.Record.StreakDays)

		resumed := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "new-day active"}},
		})
		require.Equal(t, http.StatusOK, resumed.Status, string(resumed.Body))

		lotsAfter := env.listUserLotsDB(u.ID)
		require.Len(t, lotsAfter, 2)
		for _, l := range lotsAfter {
			verifyLotInvariant(t, env.db, l.ID)
		}
	})
}
