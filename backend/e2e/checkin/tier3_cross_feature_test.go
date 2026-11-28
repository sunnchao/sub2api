//go:build e2e

package checkin_e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func t3SetupOpenAIUpstream(t *testing.T, env *e2eEnv, inTok, outTok int) int64 {
	t.Helper()
	env.openai.mu.Lock()
	env.openai.inputTokens = inTok
	env.openai.outputTokens = outTok
	env.openai.mu.Unlock()
	t.Cleanup(env.openai.Reset)

	groupID := env.createGroup(map[string]any{
		"name":              "e2e-oai-" + randomHex(4),
		"platform":          "openai",
		"rate_multiplier":   1.0,
		"subscription_type": "standard",
	})
	_ = env.createAccount(map[string]any{
		"name":     "e2e-oai-acc-" + randomHex(4),
		"platform": "openai",
		"type":     "apikey",
		"credentials": map[string]any{
			"api_key":  "sk-openai-mock-e2e",
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

func t3ConfigureSMTPAndLowNotify(t *testing.T, env *e2eEnv, threshold float64) {
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

func t3EnableUserNotifyEmail(t *testing.T, env *e2eEnv, userToken string) string {
	t.Helper()
	notifyAddr := "notify_" + randomHex(4) + "@example.com"
	sendRes := env.apiPost("/api/v1/user/notify-email/send-code", userToken, map[string]any{
		"email": notifyAddr,
	})
	require.Equal(t, http.StatusOK, sendRes.Status, string(sendRes.Body))

	var code string
	eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
		var ok bool
		code, ok = env.smtpServer.ExtractLatestCode(notifyAddr)
		return ok
	}, "verification code must arrive on fake SMTP")

	verRes := env.apiPost("/api/v1/user/notify-email/verify", userToken, map[string]any{
		"email": notifyAddr,
		"code":  code,
	})
	require.Equal(t, http.StatusOK, verRes.Status, string(verRes.Body))

	updRes := env.apiPut("/api/v1/user", userToken, map[string]any{
		"balance_notify_enabled": true,
	})
	require.Equal(t, http.StatusOK, updRes.Status, string(updRes.Body))
	env.smtpServer.Reset()
	return notifyAddr
}

func t3PostConcurrentJSON(client *http.Client, urlStr, bearer string, body any) (int, string, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequest(http.MethodPost, urlStr, bytes.NewReader(b))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", err
	}
	var env apiEnvelope
	_ = json.Unmarshal(raw, &env)
	return resp.StatusCode, env.Reason, nil
}

func t3WaitBatchCompleted(t *testing.T, env *e2eEnv, batchID, apiKey string) {
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

func t3RestartIsolatedServer(t *testing.T, env *e2eEnv) {
	t.Helper()
	require.NotNil(t, sharedHarness)
	require.NotEmpty(t, sharedHarness.serverBin, "serverBin required for isolated restart")
	require.NotEmpty(t, env.dbName, "isolated env dbName required")

	env.stopProcess()

	port, err := pickFreePort()
	require.NoError(t, err)

	dbDSN := replaceDSNDatabase(sharedHarness.pgDSN, env.dbName)
	pgHost, pgPort, pgUser, pgPass, pgDBName, pgSSL := parsePostgresDSN(dbDSN)
	rHost, rPort := splitHostPortDefault(sharedHarness.redisAddr, "6379")
	fallbackPricing := filepath.Join(sharedHarness.backendDir, "resources", "model-pricing", "model_prices_and_context_window.json")
	pricingDir := filepath.Join(env.dataDir, "pricing")

	logPath := filepath.Join(env.dataDir, "server-restart.log")
	logFile, err := os.Create(logPath)
	require.NoError(t, err)

	cmd := exec.Command(sharedHarness.serverBin)
	cmd.Dir = sharedHarness.backendDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(),
		"DATA_DIR="+env.dataDir,
		"AUTO_SETUP=true",
		"ADMIN_EMAIL="+env.adminEmail,
		"ADMIN_PASSWORD="+env.adminPassword,
		"SERVER_HOST=127.0.0.1",
		"SERVER_PORT="+strconv.Itoa(port),
		"SERVER_MODE=release",
		"RUN_MODE="+env.runMode,
		"TZ="+env.timezone,
		"TIMEZONE="+env.timezone,
		"JWT_SECRET="+env.jwtSecret,
		"DATABASE_HOST="+pgHost,
		"DATABASE_PORT="+pgPort,
		"DATABASE_USER="+pgUser,
		"DATABASE_PASSWORD="+pgPass,
		"DATABASE_DBNAME="+pgDBName,
		"DATABASE_SSLMODE="+pgSSL,
		"REDIS_HOST="+rHost,
		"REDIS_PORT="+rPort,
		"REDIS_DB="+strconv.Itoa(env.redisDB),
		"PRICING_REMOTE_URL=",
		"PRICING_HASH_URL=",
		"PRICING_DATA_DIR="+pricingDir,
		"PRICING_FALLBACK_FILE="+fallbackPricing,
		"BATCH_IMAGE_ENABLED=true",
		"BATCH_IMAGE_QUEUE_ENABLED=true",
		"BATCH_IMAGE_VERTEX_ENABLED=true",
		"BATCH_IMAGE_VERTEX_PROJECT_ID=e2e-vertex-proj",
		"BATCH_IMAGE_VERTEX_LOCATION=global",
		"BATCH_IMAGE_VERTEX_MANAGED_GCS_BUCKET=e2e-vertex-bucket",
		"BATCH_IMAGE_DEFAULT_REQUEUE_DELAY_SECONDS=1",
		"BATCH_IMAGE_DELAYED_MOVER_INTERVAL_SECONDS=1",
		"BATCH_IMAGE_VERTEX_BATCH_PREDICTION_BASE_URL="+env.vertex.URL(),
		"BATCH_IMAGE_VERTEX_GCS_BASE_URL="+env.vertex.URL(),
	)
	require.NoError(t, cmd.Start())
	_ = logFile.Close()

	env.cmd = cmd
	env.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	if waitErr := waitForServerReady(env.baseURL, 45*time.Second); waitErr != nil {
		logs, _ := os.ReadFile(logPath)
		t.Fatalf("restarted server not ready at %s: %v\nlogs:\n%s", env.baseURL, waitErr, string(logs))
	}
	env.loginAdmin()
}

func TestTier3_CrossFeatureCombinations(t *testing.T) {
	// T3_01 (Check-in + Gateway FEFO + API Key Auth w/ 0 Regular Balance)
	t.Run("T3_01_CheckinPlusGatewayFEFOAndAPIKeyAuthZeroRegular", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)
		u := env.createTestUser(0)
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		// First check-in with validity_days=1 ($0.25), then shift its checkin_date to yesterday
		// so the same user can perform a second check-in today with validity_days=3 ($0.25).
		cfg1 := defaultEnabledCheckinConfig(0.25)
		cfg1["validity_days"] = 1
		env.enableCheckinConfig(cfg1)
		c1 := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, c1.Status, string(c1.Body))
		r1 := decodeData[struct {
			Record struct {
				ID int64 `json:"id"`
			} `json:"record"`
		}](t, c1.Env)

		loc := mustLoadLoc(t, env.timezone)
		yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
		_, err := env.db.ExecContext(context.Background(),
			`UPDATE user_checkin_records SET checkin_date = $1::date WHERE id = $2`,
			yesterday, r1.Record.ID,
		)
		require.NoError(t, err)

		cfg2 := defaultEnabledCheckinConfig(0.25)
		cfg2["validity_days"] = 3
		env.enableCheckinConfig(cfg2)
		c2 := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, c2.Status, string(c2.Body))

		lots := env.listUserLotsDB(u.ID)
		require.Len(t, lots, 2)
		lotEarly := lots[0]
		lotLate := lots[1]
		require.True(t, lotEarly.ExpiresAt.Before(lotLate.ExpiresAt))

		// Verify /v1/usage before spending.
		usageBefore := env.gatewayGet("/v1/usage", apiKey)
		require.Equal(t, http.StatusOK, usageBefore.Status, string(usageBefore.Body))
		uPre := decodeRaw[struct {
			Balance         float64 `json:"balance"`
			RegularBalance  float64 `json:"regular_balance"`
			ExpiringBalance float64 `json:"expiring_balance"`
		}](t, usageBefore.Body)
		require.InDelta(t, 0.50, uPre.Balance, 1e-8)
		require.InDelta(t, 0.0, uPre.RegularBalance, 1e-8)
		require.InDelta(t, 0.50, uPre.ExpiringBalance, 1e-8)

		// claude-sonnet-4-5: 10000*3e-6 + 20000*1.5e-5 = 0.03 + 0.30 = $0.33.
		env.anthropic.SetTokens(10000, 20000)
		gwRes := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 64,
			"messages":   []map[string]string{{"role": "user", "content": "fefo zero regular"}},
		})
		require.Equal(t, http.StatusOK, gwRes.Status, string(gwRes.Body))

		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			return env.getLotDB(lotEarly.ID).Status == "exhausted" &&
				approxEqual(env.getLotDB(lotLate.ID).ConsumedAmount, 0.08)
		}, "lotEarly ($0.25) should exhaust first and lotLate consume $0.08")

		a := env.getLotDB(lotEarly.ID)
		b := env.getLotDB(lotLate.ID)
		require.InDelta(t, 0.0, a.RemainingAmount, 1e-8)
		require.InDelta(t, 0.25, a.ConsumedAmount, 1e-8)
		require.InDelta(t, 0.17, b.RemainingAmount, 1e-8)
		require.InDelta(t, 0.08, b.ConsumedAmount, 1e-8)
		verifyLotInvariant(t, env.db, a.ID)
		verifyLotInvariant(t, env.db, b.ID)

		reg, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 0.0, reg, 1e-8)

		usageAfter := env.gatewayGet("/v1/usage", apiKey)
		require.Equal(t, http.StatusOK, usageAfter.Status)
		uPost := decodeRaw[struct {
			Balance         float64 `json:"balance"`
			RegularBalance  float64 `json:"regular_balance"`
			ExpiringBalance float64 `json:"expiring_balance"`
		}](t, usageAfter.Body)
		require.InDelta(t, 0.17, uPost.Balance, 1e-8)
		require.InDelta(t, 0.0, uPost.RegularBalance, 1e-8)
		require.InDelta(t, 0.17, uPost.ExpiringBalance, 1e-8)
	})

	// T3_02 (Check-in + OpenAI Gateway + ApplyBatch Lane + Low-Balance Email)
	t.Run("T3_02_CheckinPlusOpenAIGatewayApplyBatchAndLowBalanceEmail", func(t *testing.T) {
		env := newE2EEnv(t)
		t3ConfigureSMTPAndLowNotify(t, env, 0.50)

		// Enable affiliate to confirm check-in + gateway usage never trigger affiliate rebates.
		stAff, _ := env.adminPut("/api/v1/admin/settings", map[string]any{
			"affiliate_enabled":     true,
			"affiliate_rebate_rate": 10.0,
		})
		require.Equal(t, http.StatusOK, stAff)

		// gpt-4o: 20000*2.5e-6 + 15000*1e-5 = 0.05 + 0.15 = $0.20 per request.
		groupID := t3SetupOpenAIUpstream(t, env, 20000, 15000)

		referrer := env.registerUser("t302_ref")
		affRes := env.apiGet("/api/v1/user/aff", referrer.Token)
		require.Equal(t, http.StatusOK, affRes.Status)
		affCode := decodeData[struct {
			AffCode string `json:"aff_code"`
		}](t, affRes.Env).AffCode

		email := fmt.Sprintf("t302_u_%s@example.com", randomHex(5))
		regRes := env.doJSON(http.MethodPost, "/api/v1/auth/register", "", "", map[string]any{
			"email":    email,
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

		env.setUserRegularBalanceSQL(regData.User.ID, 0.40)
		t3EnableUserNotifyEmail(t, env, regData.AccessToken)
		_, apiKey := env.createAPIKeyForUser(regData.AccessToken, &groupID)

		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.30))
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", regData.AccessToken, map[string]any{}).Status)
		lots := env.listUserLotsDB(regData.User.ID)
		require.Len(t, lots, 1)

		// Total = $0.30 expiring + $0.40 regular = $0.70 (>= $0.50).
		// 2 concurrent OpenAI requests ($0.20 each = $0.40) drop total to $0.30 (< $0.50).
		var wg sync.WaitGroup
		statuses := make(chan int, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				st, _, err := t3PostConcurrentJSON(env.httpClient, env.baseURL+"/v1/chat/completions", apiKey, map[string]any{
					"model": "gpt-4o",
					"messages": []map[string]string{
						{"role": "user", "content": "hello openai batch"},
					},
				})
				if err != nil {
					statuses <- 0
					return
				}
				statuses <- st
			}()
		}
		wg.Wait()
		close(statuses)
		for st := range statuses {
			require.Equal(t, http.StatusOK, st)
		}

		eventually(t, 8*time.Second, 50*time.Millisecond, func() bool {
			reg, _, _ := env.getUserDBBalance(regData.User.ID)
			lot := env.getLotDB(lots[0].ID)
			return lot.Status == "exhausted" && approxEqual(reg, 0.30) && len(env.smtpServer.Emails()) == 1
		}, "2x OpenAI requests should exhaust lot ($0.30), drop regular to $0.30, and emit 1 low-balance email")

		time.Sleep(250 * time.Millisecond)
		require.Len(t, env.smtpServer.Emails(), 1, "low-balance email must fire exactly once")
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

	// T3_03 (Check-in + Batch Image Hold -> Partial Capture -> LIFO Active Release)
	t.Run("T3_03_CheckinPlusBatchImageHoldPartialCaptureLIFOActiveRelease", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.40)
		env.vertex.SetMode(vertexModeStayRunning)

		u := env.createTestUser(0.50)
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.30))
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u.Token, map[string]any{}).Status)
		lotA := env.listUserLotsDB(u.ID)[0]

		lotBExp := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
		lotBID := env.seedExpiringLotSQL(u.ID, 0.50, lotBExp, "active")

		// Seed Redis billing:balance:{id} before hold.
		require.Equal(t, http.StatusOK, env.gatewayGet("/v1/usage", apiKey).Status)

		// 3 items * $0.40 = $1.20 hold => Lot A ($0.30) + Lot B ($0.50) + Regular ($0.40).
		res := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "i1", "prompt": "one"},
				{"custom_id": "i2", "prompt": "two"},
				{"custom_id": "i3", "prompt": "three"},
			},
		})
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		batch := decodeRaw[struct {
			ID         string  `json:"id"`
			HoldAmount float64 `json:"hold_amount"`
		}](t, res.Body)
		require.InDelta(t, 1.20, batch.HoldAmount, 1e-8)

		// Partial settlement: 1 success ($0.40 captured FEFO: Lot A $0.30 + Lot B $0.10),
		// 2 failed ($0.80 released LIFO: Regular $0.40 + Lot B $0.40).
		env.vertex.SetMode(vertexModePartialSuccess)
		t3WaitBatchCompleted(t, env, batch.ID, apiKey)

		aAfter := env.getLotDB(lotA.ID)
		bAfter := env.getLotDB(lotBID)
		require.Equal(t, "exhausted", aAfter.Status)
		require.InDelta(t, 0.30, aAfter.ConsumedAmount, 1e-8)
		require.InDelta(t, 0.0, aAfter.FrozenAmount, 1e-8)

		require.Equal(t, "active", bAfter.Status)
		require.InDelta(t, 0.10, bAfter.ConsumedAmount, 1e-8)
		require.InDelta(t, 0.40, bAfter.RemainingAmount, 1e-8)
		require.InDelta(t, 0.0, bAfter.FrozenAmount, 1e-8)
		require.True(t, bAfter.ExpiresAt.UTC().Equal(lotBExp), "Lot B original expires_at must be preserved")
		verifyLotInvariant(t, env.db, aAfter.ID)
		verifyLotInvariant(t, env.db, bAfter.ID)

		reg, frozen, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 0.50, reg, 1e-8)
		require.InDelta(t, 0.0, frozen, 1e-8)

		// Redis cache should have been invalidated by batch image settlement (either deleted or refreshed to 0.90).
		balKey := fmt.Sprintf("billing:balance:%d", u.ID)
		val, gErr := env.rdb.Get(context.Background(), balKey).Float64()
		if gErr == nil {
			require.InDelta(t, 0.90, val, 1e-8)
		}
		// Subsequent /v1/usage reflects exact $0.90 total ($0.50 regular + $0.40 expiring).
		uRes := env.gatewayGet("/v1/usage", apiKey)
		require.Equal(t, http.StatusOK, uRes.Status)
		uDTO := decodeRaw[struct {
			Balance         float64 `json:"balance"`
			RegularBalance  float64 `json:"regular_balance"`
			ExpiringBalance float64 `json:"expiring_balance"`
		}](t, uRes.Body)
		require.InDelta(t, 0.90, uDTO.Balance, 1e-8)
		require.InDelta(t, 0.50, uDTO.RegularBalance, 1e-8)
		require.InDelta(t, 0.40, uDTO.ExpiringBalance, 1e-8)
	})

	// T3_04 (Check-in + Batch Image Hold -> Cross-Midnight Expiry -> Partial Capture & Cancel Forfeiture)
	t.Run("T3_04_CheckinPlusBatchImageHoldCrossMidnightExpiryPartialCaptureAndCancel", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupVertexBatchImageUpstream(t, 0.50)

		// Part A: Cross-midnight expiry + partial capture ($1.00 hold -> $0.50 capture, $0.50 excess: $0.20 regular + $0.30 expired Lot A forfeited).
		env.vertex.SetMode(vertexModeStayRunning)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.80))

		uA := env.createTestUser(0.50)
		_, keyA := env.createAPIKeyForUser(uA.Token, &groupID)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", uA.Token, map[string]any{}).Status)
		lotA := env.listUserLotsDB(uA.ID)[0]

		resA := env.gatewayPost("/v1/images/batches", keyA, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "a1", "prompt": "cat"},
				{"custom_id": "a2", "prompt": "dog"},
			},
		})
		require.Equal(t, http.StatusOK, resA.Status, string(resA.Body))
		batchA := decodeRaw[struct {
			ID string `json:"id"`
		}](t, resA.Body)

		_, err := env.db.ExecContext(context.Background(), `UPDATE user_expiring_balance_lots SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, lotA.ID)
		require.NoError(t, err)
		env.vertex.SetMode(vertexModePartialSuccess)
		t3WaitBatchCompleted(t, env, batchA.ID, keyA)

		regA, frozenA, _ := env.getUserDBBalance(uA.ID)
		require.InDelta(t, 0.50, regA, 1e-8)
		require.InDelta(t, 0.0, frozenA, 1e-8)

		lotAAfter := env.getLotDB(lotA.ID)
		require.Equal(t, "expired", lotAAfter.Status)
		require.InDelta(t, 0.50, lotAAfter.ConsumedAmount, 1e-8)
		require.InDelta(t, 0.30, lotAAfter.ExpiredAmount, 1e-8)
		require.InDelta(t, 0.0, lotAAfter.RemainingAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotA.ID)

		allocsA := env.listAllocationsDB("batch_image_hold", batchA.ID)
		require.Len(t, allocsA, 2)
		require.InDelta(t, 0.30, allocsA[0].ForfeitedExpiredAmount, 1e-8)
		require.InDelta(t, 0.20, allocsA[1].ReleasedAmount, 1e-8)

		// Part B: Cross-midnight expiry + job cancellation ($1.00 hold: $0.50 Lot C + $0.50 Regular -> cancel forfeits Lot C, returns Regular).
		env.vertex.SetMode(vertexModeStayRunning)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(0.50))

		uB := env.createTestUser(0.50)
		_, keyB := env.createAPIKeyForUser(uB.Token, &groupID)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", uB.Token, map[string]any{}).Status)
		lotC := env.listUserLotsDB(uB.ID)[0]

		resB := env.gatewayPost("/v1/images/batches", keyB, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "b1", "prompt": "sun"},
				{"custom_id": "b2", "prompt": "moon"},
			},
		})
		require.Equal(t, http.StatusOK, resB.Status, string(resB.Body))
		batchB := decodeRaw[struct {
			ID string `json:"id"`
		}](t, resB.Body)

		_, err = env.db.ExecContext(context.Background(), `UPDATE user_expiring_balance_lots SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, lotC.ID)
		require.NoError(t, err)

		cancelRes := env.gatewayPost("/v1/images/batches/"+batchB.ID+"/cancel", keyB, map[string]any{})
		require.Equal(t, http.StatusOK, cancelRes.Status, string(cancelRes.Body))

		regB, frozenB, _ := env.getUserDBBalance(uB.ID)
		require.InDelta(t, 0.50, regB, 1e-8, "expired lot must never convert to users.balance")
		require.InDelta(t, 0.0, frozenB, 1e-8)

		lotCAfter := env.getLotDB(lotC.ID)
		require.Equal(t, "expired", lotCAfter.Status)
		require.InDelta(t, 0.50, lotCAfter.ExpiredAmount, 1e-8)
		require.InDelta(t, 0.0, lotCAfter.RemainingAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotC.ID)

		allocsB := env.listAllocationsDB("batch_image_hold", batchB.ID)
		require.Len(t, allocsB, 2)
		require.InDelta(t, 0.50, allocsB[0].ForfeitedExpiredAmount, 1e-8)
		require.InDelta(t, 0.50, allocsB[1].ReleasedAmount, 1e-8)
	})

	// T3_05 (Check-in + Balance Subscription Purchase + LIFO Refund Before Expiry)
	t.Run("T3_05_CheckinPlusBalanceSubscriptionPurchaseAndLIFORefundBeforeExpiry", func(t *testing.T) {
		env := newE2EEnv(t)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(2.00))

		u := env.createTestUser(4.00)
		cRes := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, cRes.Status)
		lotBefore := env.listUserLotsDB(u.ID)[0]
		origExpiry := lotBefore.ExpiresAt

		ordRes := env.apiPost("/api/v1/payment/orders", u.Token, map[string]any{
			"order_type":   "subscription",
			"payment_type": "balance",
			"plan_id":      planID,
		})
		require.Equal(t, http.StatusOK, ordRes.Status, string(ordRes.Body))
		order := decodeData[struct {
			OrderID int64  `json:"order_id"`
			Status  string `json:"status"`
		}](t, ordRes.Env)
		require.Equal(t, "COMPLETED", order.Status)

		// Verify subscription created atomically, allocations written, and SUBSCRIPTION_SUCCESS audit recorded.
		subSt, subEnv := env.adminGet(fmt.Sprintf("/api/v1/admin/users/%d/subscriptions", u.ID))
		require.Equal(t, http.StatusOK, subSt)
		subs := decodeData[[]struct {
			Status string `json:"status"`
		}](t, subEnv)
		require.Len(t, subs, 1)
		require.Equal(t, "active", subs[0].Status)

		allocs := env.listAllocationsDB("payment_order", strconv.FormatInt(order.OrderID, 10))
		require.Len(t, allocs, 2)
		require.True(t, allocs[0].LotID.Valid && allocs[0].LotID.Int64 == lotBefore.ID)
		require.InDelta(t, 2.00, allocs[0].AllocatedAmount, 1e-8)
		require.False(t, allocs[1].LotID.Valid)
		require.InDelta(t, 3.00, allocs[1].AllocatedAmount, 1e-8)

		detSt, detEnv := env.adminGet(fmt.Sprintf("/api/v1/admin/payment/orders/%d", order.OrderID))
		require.Equal(t, http.StatusOK, detSt)
		det := decodeData[struct {
			AuditLogs []struct {
				Action string `json:"action"`
				Detail string `json:"detail"`
			} `json:"auditLogs"`
		}](t, detEnv)
		var subAuditFound bool
		for _, a := range det.AuditLogs {
			if a.Action == "SUBSCRIPTION_SUCCESS" {
				subAuditFound = true
				var parsed map[string]any
				require.NoError(t, json.Unmarshal([]byte(a.Detail), &parsed))
				require.InDelta(t, 2.00, parsed["expiringAmountUsed"], 1e-8)
				require.InDelta(t, 3.00, parsed["regularAmountUsed"], 1e-8)
			}
		}
		require.True(t, subAuditFound)

		// Admin partial refund $4.50 before expiry -> LIFO returns $3.00 regular first, then $1.50 to Lot A preserving expires_at.
		rSt, rEnv := env.adminPost(fmt.Sprintf("/api/v1/admin/payment/orders/%d/refund", order.OrderID), map[string]any{
			"amount": 4.50,
			"reason": "pre-expiry LIFO refund",
		})
		require.Equal(t, http.StatusOK, rSt, rEnv.Message)

		reg, _, _ := env.getUserDBBalance(u.ID)
		require.InDelta(t, 4.00, reg, 1e-8)

		lotAfter := env.getLotDB(lotBefore.ID)
		require.Equal(t, "active", lotAfter.Status)
		require.InDelta(t, 1.50, lotAfter.RemainingAmount, 1e-8)
		require.InDelta(t, 0.50, lotAfter.ConsumedAmount, 1e-8)
		require.True(t, lotAfter.ExpiresAt.Equal(origExpiry), "original expires_at must be preserved on active refund")
		verifyLotInvariant(t, env.db, lotBefore.ID)
	})

	// T3_06 (Check-in + Balance Subscription Purchase + Anti-Laundering Refund After Expiry)
	t.Run("T3_06_CheckinPlusBalanceSubscriptionPurchaseAndAntiLaunderingRefundAfterExpiry", func(t *testing.T) {
		env := newE2EEnv(t)
		planID := env.enableBalancePaymentAndCreatePlan(5.00)
		env.enableCheckinConfig(defaultEnabledCheckinConfig(2.00))

		u := env.createTestUser(4.00)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u.Token, map[string]any{}).Status)
		lot := env.listUserLotsDB(u.ID)[0]

		ordRes := env.apiPost("/api/v1/payment/orders", u.Token, map[string]any{
			"order_type":   "subscription",
			"payment_type": "balance",
			"plan_id":      planID,
		})
		require.Equal(t, http.StatusOK, ordRes.Status)
		orderID := decodeData[struct {
			OrderID int64 `json:"order_id"`
		}](t, ordRes.Env).OrderID

		_, err := env.db.ExecContext(context.Background(), `UPDATE user_expiring_balance_lots SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, lot.ID)
		require.NoError(t, err)

		rSt, rEnv := env.adminPost(fmt.Sprintf("/api/v1/admin/payment/orders/%d/refund", orderID), map[string]any{
			"amount": 5.00,
			"reason": "post-expiry anti-laundering refund",
		})
		require.Equal(t, http.StatusOK, rSt, rEnv.Message)

		reg, _, totalRecharged := env.getUserDBBalance(u.ID)
		require.InDelta(t, 4.00, reg, 1e-8, "expired lot slice must not convert into regular balance")
		require.InDelta(t, 0.0, totalRecharged, 1e-8)

		lotAfter := env.getLotDB(lot.ID)
		require.Equal(t, "expired", lotAfter.Status)
		require.InDelta(t, 0.0, lotAfter.RemainingAmount, 1e-8)
		require.InDelta(t, 2.00, lotAfter.ExpiredAmount, 1e-8)
		verifyLotInvariant(t, env.db, lot.ID)

		allocs := env.listAllocationsDB("payment_order", strconv.FormatInt(orderID, 10))
		require.Len(t, allocs, 2)
		require.InDelta(t, 2.00, allocs[0].ForfeitedExpiredAmount, 1e-8)
		require.InDelta(t, 3.00, allocs[1].ReleasedAmount, 1e-8)

		detSt, detEnv := env.adminGet(fmt.Sprintf("/api/v1/admin/payment/orders/%d", orderID))
		require.Equal(t, http.StatusOK, detSt)
		det := decodeData[struct {
			AuditLogs []struct {
				Action string `json:"action"`
			} `json:"auditLogs"`
		}](t, detEnv)
		var refundAuditFound bool
		for _, a := range det.AuditLogs {
			if a.Action == "BALANCE_REFUND_CREDITED" {
				refundAuditFound = true
			}
		}
		require.True(t, refundAuditFound, "expected BALANCE_REFUND_CREDITED audit log")
	})

	// T3_07 (Redis TTL Truncation + KEEPTTL + Expiry Enforcement)
	t.Run("T3_07_RedisTTLTruncationKeepTTLAndExpiryEnforcement", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)

		u := env.createTestUser(0)
		// 90s lot first: verify TTL <= 90s and KEEPTTL on second deduction, then test short 4s expiry enforcement.
		lot90 := env.seedExpiringLotSQL(u.ID, 1.00, time.Now().Add(90*time.Second), "active")
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		env.anthropic.SetTokens(0, 0)
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "populate cache"}},
		}).Status)

		balKey := fmt.Sprintf("billing:balance:%d", u.ID)
		var ttl1 time.Duration
		eventually(t, 3*time.Second, 25*time.Millisecond, func() bool {
			var tErr error
			ttl1, tErr = env.rdb.TTL(context.Background(), balKey).Result()
			return tErr == nil && ttl1 > 0
		}, "billing:balance cache key should be populated with positive TTL")
		require.Greater(t, ttl1, time.Duration(0))
		require.LessOrEqual(t, ttl1, 90*time.Second)

		time.Sleep(1100 * time.Millisecond)

		env.anthropic.SetTokens(10000, 18000) // $0.30
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "deduct keepttl"}},
		}).Status)

		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			v, gErr := env.rdb.Get(context.Background(), balKey).Float64()
			return gErr == nil && approxEqual(v, 0.70)
		}, "cached balance should decrement to 0.70")

		ttl2, err := env.rdb.TTL(context.Background(), balKey).Result()
		require.NoError(t, err)
		require.Greater(t, ttl2, time.Duration(0))
		require.LessOrEqual(t, ttl2, ttl1, "KEEPTTL must not reset TTL back to ~300s")

		// Now shorten lot90 to expire in 3s, clear cache so next request truncates TTL to <= 3s,
		// then wait for natural expiry and confirm /v1/messages rejects with 403 (no stale cache hit).
		_, err = env.db.ExecContext(context.Background(), `UPDATE user_expiring_balance_lots SET expires_at = NOW() + INTERVAL '3 seconds' WHERE id = $1`, lot90)
		require.NoError(t, err)
		_ = env.rdb.Del(context.Background(), balKey).Err()

		env.anthropic.SetTokens(0, 0)
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "short ttl seed"}},
		}).Status)

		var ttlShort time.Duration
		eventually(t, 3*time.Second, 25*time.Millisecond, func() bool {
			var tErr error
			ttlShort, tErr = env.rdb.TTL(context.Background(), balKey).Result()
			return tErr == nil && ttlShort > 0
		}, "billing:balance cache key should be populated with short positive TTL")
		require.Greater(t, ttlShort, time.Duration(0))
		require.LessOrEqual(t, ttlShort, 3*time.Second)

		time.Sleep(3500 * time.Millisecond)
		rejected := env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "after natural expiry"}},
		})
		require.Equal(t, http.StatusForbidden, rejected.Status)
		verifyLotInvariant(t, env.db, lot90)
	})

	// T3_08 (Check-in + Streak Milestone + Eligibility max_available_balance)
	t.Run("T3_08_CheckinPlusStreakMilestoneAndMaxAvailableBalanceCap", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)
		cfg := defaultEnabledCheckinConfig(0.20)
		cfg["validity_days"] = 7
		cfg["milestones"] = []map[string]any{
			{"days": 2, "bonus_amount": 0.80},
		}
		cfg["eligibility"] = map[string]any{
			"max_available_balance": 1.00,
		}
		env.enableCheckinConfig(cfg)

		u := env.createTestUser(0)
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)
		loc := mustLoadLoc(t, env.timezone)
		yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
		env.seedCheckinRecordSQL(u.ID, yesterday, 1, 1, 0.20)

		// Day 2 check-in hits milestone (+0.80) => total reward $1.00 -> reaches balance cap $1.00.
		c2 := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, c2.Status, string(c2.Body))
		d2 := decodeData[struct {
			Record struct {
				ID          int64   `json:"id"`
				StreakDays  int     `json:"streak_days"`
				TotalAmount float64 `json:"total_amount"`
			} `json:"record"`
		}](t, c2.Env)
		require.Equal(t, 2, d2.Record.StreakDays)
		require.InDelta(t, 1.00, d2.Record.TotalAmount, 1e-8)

		// Advance into next day by shifting records back 1 day.
		_, err := env.db.ExecContext(context.Background(),
			`UPDATE user_checkin_records SET checkin_date = checkin_date - INTERVAL '1 day' WHERE user_id = $1`,
			u.ID,
		)
		require.NoError(t, err)

		stBlocked := env.apiGet("/api/v1/checkin/status", u.Token)
		require.Equal(t, http.StatusOK, stBlocked.Status)
		elBlocked := decodeData[struct {
			Eligibility struct {
				Eligible   bool   `json:"eligible"`
				ReasonCode string `json:"reason_code"`
			} `json:"eligibility"`
		}](t, stBlocked.Env)
		require.False(t, elBlocked.Eligibility.Eligible)
		require.Equal(t, "CHECKIN_BALANCE_CAP_EXCEEDED", elBlocked.Eligibility.ReasonCode)

		postBlocked := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusForbidden, postBlocked.Status)
		require.Equal(t, "CHECKIN_BALANCE_CAP_EXCEEDED", postBlocked.Env.Reason)

		// Spend $0.30 via gateway so available drops to $0.70 < $1.00.
		env.anthropic.SetTokens(10000, 18000)
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/messages", apiKey, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "spend below cap"}},
		}).Status)

		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			lots := env.listUserLotsDB(u.ID)
			return len(lots) == 1 && approxEqual(lots[0].RemainingAmount, 0.70)
		}, "lot should drop to 0.70")

		stRestored := env.apiGet("/api/v1/checkin/status", u.Token)
		require.Equal(t, http.StatusOK, stRestored.Status)
		elRestored := decodeData[struct {
			Eligibility struct {
				Eligible bool `json:"eligible"`
			} `json:"eligibility"`
		}](t, stRestored.Env)
		require.True(t, elRestored.Eligibility.Eligible)

		c3 := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, c3.Status, string(c3.Body))
		d3 := decodeData[struct {
			Record struct {
				StreakDays int `json:"streak_days"`
			} `json:"record"`
		}](t, c3.Env)
		require.Equal(t, 3, d3.Record.StreakDays)
	})

	// T3_09 (Public Settings / SSR + Feature Flag + Simple Mode + Admin Config Audit)
	t.Run("T3_09_PublicSettingsSSRFeatureFlagSimpleModeAndConfigAudit", func(t *testing.T) {
		stdEnv := newE2EEnv(t)
		uniqueDesc := "T3_09 audit test " + randomHex(6)
		cfg := defaultEnabledCheckinConfig(0.25)
		cfg["rules_description"] = uniqueDesc
		stdEnv.enableCheckinConfig(cfg)

		pubRes := stdEnv.publicGet("/api/v1/settings/public")
		require.Equal(t, http.StatusOK, pubRes.Status)
		pub := decodeData[map[string]any](t, pubRes.Env)
		require.Equal(t, true, pub["checkin_enabled"])

		ssrRes := stdEnv.publicGet("/")
		if ssrRes.Status != http.StatusNotFound {
			require.Equal(t, http.StatusOK, ssrRes.Status)
			re := regexp.MustCompile(`window\.__APP_CONFIG__\s*=\s*(\{.*?\});`)
			m := re.FindStringSubmatch(string(ssrRes.Body))
			require.Len(t, m, 2, "expected window.__APP_CONFIG__ in SSR HTML")
			var injected map[string]any
			require.NoError(t, json.Unmarshal([]byte(m[1]), &injected))
			require.Equal(t, true, injected["checkin_enabled"])
		}

		stdEnv.requireCheckinAuditLogRecorded(t, uniqueDesc)

		simpleEnv := newE2EEnv(t, withRunMode("simple"))
		u := simpleEnv.createTestUser(0)
		simpleEnv.enableCheckinConfig(defaultEnabledCheckinConfig(0.25))

		simplePub := simpleEnv.publicGet("/api/v1/settings/public")
		require.Equal(t, http.StatusOK, simplePub.Status)
		simpleMap := decodeData[map[string]any](t, simplePub.Env)
		require.Equal(t, false, simpleMap["checkin_enabled"])

		blocked := simpleEnv.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusForbidden, blocked.Status)
		require.Equal(t, "CHECKIN_DISABLED", blocked.Env.Reason)
	})

	// T3_10 (Background Archiver + In-Flight Frozen Hold + No Expiry Email)
	t.Run("T3_10_BackgroundArchiverInFlightFrozenHoldAndZeroExpiryEmail", func(t *testing.T) {
		env := newE2EEnv(t, withTimezone(defaultTimezone))
		t3ConfigureSMTPAndLowNotify(t, env, 1.00)
		groupID := env.setupVertexBatchImageUpstream(t, 0.50)
		env.vertex.SetMode(vertexModeStayRunning)

		u := env.createTestUser(0)
		t3EnableUserNotifyEmail(t, env, u.Token)
		_, apiKey := env.createAPIKeyForUser(u.Token, &groupID)

		lotFrozenID := env.seedExpiringLotSQL(u.ID, 0.50, time.Now().Add(1*time.Hour), "active")
		lotUnfrozenID := env.seedExpiringLotSQL(u.ID, 0.80, time.Now().Add(2*time.Hour), "active")

		// Hold 1 item ($0.50) -> freezes lotFrozenID ($0.50).
		holdRes := env.gatewayPost("/v1/images/batches", apiKey, map[string]any{
			"model": "gemini-2.5-flash-image",
			"items": []map[string]any{
				{"custom_id": "arch1", "prompt": "keep frozen"},
			},
		})
		require.Equal(t, http.StatusOK, holdRes.Status, string(holdRes.Body))
		batch := decodeRaw[struct {
			ID string `json:"id"`
		}](t, holdRes.Body)

		// Populate Redis billing cache so archiver invalidation can be observed.
		require.Equal(t, http.StatusOK, env.gatewayGet("/v1/usage", apiKey).Status)
		balKey := fmt.Sprintf("billing:balance:%d", u.ID)
		require.NoError(t, env.rdb.Set(context.Background(), balKey, "0.80", 2*time.Minute).Err())

		// Expire both lots in DB, then restart isolated server so ExpiringBalanceArchiver.Start()
		// immediately executes RunOnce() on startup.
		_, err := env.db.ExecContext(context.Background(),
			`UPDATE user_expiring_balance_lots SET expires_at = NOW() - INTERVAL '1 minute' WHERE id IN ($1, $2)`,
			lotFrozenID, lotUnfrozenID,
		)
		require.NoError(t, err)

		if sharedHarness.serverBin != "" {
			t3RestartIsolatedServer(t, env)

			eventually(t, 10*time.Second, 100*time.Millisecond, func() bool {
				return env.getLotDB(lotUnfrozenID).Status == "expired"
			}, "archiver should mark unfrozen expired lot as expired")

			unfrozen := env.getLotDB(lotUnfrozenID)
			require.InDelta(t, 0.0, unfrozen.RemainingAmount, 1e-8)
			require.InDelta(t, 0.80, unfrozen.ExpiredAmount, 1e-8)
			verifyLotInvariant(t, env.db, lotUnfrozenID)

			exists, rErr := env.rdb.Exists(context.Background(), balKey).Result()
			require.NoError(t, rErr)
			require.Equal(t, int64(0), exists, "archiver must invalidate billing:balance cache")

			frozen := env.getLotDB(lotFrozenID)
			require.Equal(t, "active", frozen.Status, "frozen expired lot must stay active until batch settles")
			require.InDelta(t, 0.0, frozen.RemainingAmount, 1e-8)
			require.InDelta(t, 0.50, frozen.FrozenAmount, 1e-8)
		}

		// Expiration alone must emit zero low-balance emails.
		require.Empty(t, env.smtpServer.Emails(), "expiration must never send low-balance email")

		// Now cancel batch job -> frozen lot forfeits & transitions to expired, still 0 emails.
		cancelRes := env.gatewayPost("/v1/images/batches/"+batch.ID+"/cancel", apiKey, map[string]any{})
		require.Equal(t, http.StatusOK, cancelRes.Status, string(cancelRes.Body))

		frozenAfter := env.getLotDB(lotFrozenID)
		require.Equal(t, "expired", frozenAfter.Status)
		require.InDelta(t, 0.0, frozenAfter.FrozenAmount, 1e-8)
		require.InDelta(t, 0.50, frozenAfter.ExpiredAmount, 1e-8)
		verifyLotInvariant(t, env.db, lotFrozenID)
		require.Empty(t, env.smtpServer.Emails())
	})

	// T3_11 (Check-in + Multi-Sub Standard Group /v1/usage + User/Admin DTOs)
	t.Run("T3_11_CheckinPlusMultiSubStandardGroupUsageAndDTOs", func(t *testing.T) {
		env := newE2EEnv(t)
		stdGroupID := env.setupAnthropicUpstream(t)

		// Create subscription template on the standard group and assign it to user.
		tplSt, tplEnv := env.adminPost("/api/v1/admin/subscription-templates", map[string]any{
			"name":                  "E2E Std MultiSub " + randomHex(3),
			"description":           "standard group template",
			"group_id":              stdGroupID,
			"daily_limit_usd":       20.0,
			"default_validity_days": 30,
			"status":                "active",
		})
		require.Contains(t, []int{http.StatusOK, http.StatusCreated}, tplSt, tplEnv.Message)
		tplID := decodeData[struct {
			ID int64 `json:"id"`
		}](t, tplEnv).ID

		u := env.createTestUser(3.00)
		assignSt, assignEnv := env.adminPost("/api/v1/admin/subscriptions/assign", map[string]any{
			"user_id":       u.ID,
			"group_id":      stdGroupID,
			"template_id":   tplID,
			"validity_days": 30,
		})
		require.Contains(t, []int{http.StatusOK, http.StatusCreated}, assignSt, assignEnv.Message)

		env.enableCheckinConfig(defaultEnabledCheckinConfig(1.50))
		cRes := env.apiPost("/api/v1/checkin", u.Token, map[string]any{})
		require.Equal(t, http.StatusOK, cRes.Status)
		lotExp := env.listUserLotsDB(u.ID)[0].ExpiresAt

		_, apiKey := env.createAPIKeyForUser(u.Token, &stdGroupID)

		// /v1/usage on standard group with active multi-sub returns total balance + breakdown + subscriptions.
		uRes := env.gatewayGet("/v1/usage", apiKey)
		require.Equal(t, http.StatusOK, uRes.Status, string(uRes.Body))
		usage := decodeRaw[struct {
			Mode              string     `json:"mode"`
			Balance           float64    `json:"balance"`
			RegularBalance    float64    `json:"regular_balance"`
			ExpiringBalance   float64    `json:"expiring_balance"`
			NearestExpiringAt *time.Time `json:"nearest_expiring_at"`
			Subscriptions     []any      `json:"subscriptions"`
		}](t, uRes.Body)
		require.Equal(t, "unrestricted", usage.Mode)
		require.NotEmpty(t, usage.Subscriptions)
		require.InDelta(t, 4.50, usage.Balance, 1e-8)
		require.InDelta(t, 3.00, usage.RegularBalance, 1e-8)
		require.InDelta(t, 1.50, usage.ExpiringBalance, 1e-8)
		require.NotNil(t, usage.NearestExpiringAt)
		require.True(t, usage.NearestExpiringAt.UTC().Equal(lotExp.UTC()))

		// /api/v1/auth/me, /api/v1/user/profile, /api/v1/admin/users/:id preserve user.balance = regular ($3.00).
		for _, ep := range []string{"/api/v1/auth/me", "/api/v1/user/profile"} {
			r := env.apiGet(ep, u.Token)
			require.Equal(t, http.StatusOK, r.Status, ep)
			dto := decodeData[struct {
				Balance               float64    `json:"balance"`
				ExpiringBalance       float64    `json:"expiring_balance"`
				TotalAvailableBalance float64    `json:"total_available_balance"`
				NearestExpiringAt     *time.Time `json:"nearest_expiring_at"`
				NearestExpiringAmount float64    `json:"nearest_expiring_amount"`
			}](t, r.Env)
			require.InDelta(t, 3.00, dto.Balance, 1e-8)
			require.InDelta(t, 1.50, dto.ExpiringBalance, 1e-8)
			require.InDelta(t, 4.50, dto.TotalAvailableBalance, 1e-8)
			require.NotNil(t, dto.NearestExpiringAt)
			require.InDelta(t, 1.50, dto.NearestExpiringAmount, 1e-8)
		}

		admSt, admEnv := env.adminGet(fmt.Sprintf("/api/v1/admin/users/%d", u.ID))
		require.Equal(t, http.StatusOK, admSt)
		admDTO := decodeData[struct {
			Balance               float64    `json:"balance"`
			ExpiringBalance       float64    `json:"expiring_balance"`
			TotalAvailableBalance float64    `json:"total_available_balance"`
			NearestExpiringAt     *time.Time `json:"nearest_expiring_at"`
			NearestExpiringAmount float64    `json:"nearest_expiring_amount"`
		}](t, admEnv)
		require.InDelta(t, 3.00, admDTO.Balance, 1e-8)
		require.InDelta(t, 1.50, admDTO.ExpiringBalance, 1e-8)
		require.InDelta(t, 4.50, admDTO.TotalAvailableBalance, 1e-8)
		require.NotNil(t, admDTO.NearestExpiringAt)
		require.InDelta(t, 1.50, admDTO.NearestExpiringAmount, 1e-8)
	})

	// T3_12 (Admin Stats Consumption/Expiration Rates + Filtered Records + Multi-User IP Limit)
	t.Run("T3_12_AdminStatsRatesFilteredRecordsAndMultiUserIPLimit", func(t *testing.T) {
		env := newE2EEnv(t)
		groupID := env.setupAnthropicUpstream(t)
		cfg := defaultEnabledCheckinConfig(1.00)
		cfg["risk"] = map[string]any{
			"max_accounts_per_ip_day": 2,
			"daily_total_budget_usd":  10.00,
		}
		env.enableCheckinConfig(cfg)

		u1 := env.createTestUser(0)
		u2 := env.createTestUser(0)
		u3 := env.createTestUser(0)
		u4 := env.createTestUser(0)

		ipA := "203.0.113.88"
		ipB := "203.0.113.99"
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u1.Token, map[string]any{}, map[string]string{"X-Forwarded-For": ipA}).Status)
		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u2.Token, map[string]any{}, map[string]string{"X-Forwarded-For": ipA}).Status)

		r3 := env.apiPost("/api/v1/checkin", u3.Token, map[string]any{}, map[string]string{"X-Forwarded-For": ipA})
		require.Equal(t, http.StatusTooManyRequests, r3.Status)
		require.Equal(t, "CHECKIN_IP_LIMIT_EXCEEDED", r3.Env.Reason)

		require.Equal(t, http.StatusOK, env.apiPost("/api/v1/checkin", u4.Token, map[string]any{}, map[string]string{"X-Forwarded-For": ipB}).Status)

		// u1 consumes $0.30 via Anthropic gateway.
		_, key1 := env.createAPIKeyForUser(u1.Token, &groupID)
		env.anthropic.SetTokens(10000, 18000) // $0.30
		require.Equal(t, http.StatusOK, env.gatewayPost("/v1/messages", key1, map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "stats consume"}},
		}).Status)
		eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
			lots := env.listUserLotsDB(u1.ID)
			return len(lots) == 1 && approxEqual(lots[0].ConsumedAmount, 0.30)
		}, "u1 lot should consume $0.30")

		// Seed yesterday expired lot for u3 ($1.00 initial, $1.00 expired) so both rates are non-zero.
		loc := mustLoadLoc(t, env.timezone)
		yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
		today := time.Now().In(loc).Format("2006-01-02")
		rec3 := env.seedCheckinRecordSQL(u3.ID, yesterday, 1, 1, 1.00)
		_, err := env.db.ExecContext(context.Background(), `
			INSERT INTO user_expiring_balance_lots (user_id, source_type, source_id, initial_amount, remaining_amount, consumed_amount, expired_amount, expires_at, status)
			VALUES ($1, 'checkin', $2, 1.00, 0, 0, 1.00, NOW() - INTERVAL '1 hour', 'expired')
		`, u3.ID, rec3)
		require.NoError(t, err)

		st, sEnv := env.adminGet("/api/v1/admin/checkin/stats?days=30")
		require.Equal(t, http.StatusOK, st)
		stats := decodeData[struct {
			TodayCheckins      int     `json:"today_checkins"`
			TodayGranted       float64 `json:"today_granted"`
			DailyBudgetLimit   float64 `json:"daily_budget_limit"`
			DailyBudgetPercent float64 `json:"daily_budget_percent"`
			RangeTotalGranted  float64 `json:"range_total_granted"`
			RangeTotalConsumed float64 `json:"range_total_consumed"`
			RangeTotalExpired  float64 `json:"range_total_expired"`
			ConsumptionRate    float64 `json:"consumption_rate"`
			ExpirationRate     float64 `json:"expiration_rate"`
		}](t, sEnv)
		require.Equal(t, 3, stats.TodayCheckins)
		require.InDelta(t, 3.00, stats.TodayGranted, 1e-8)
		require.InDelta(t, 10.00, stats.DailyBudgetLimit, 1e-8)
		require.InDelta(t, 30.0, stats.DailyBudgetPercent, 1e-4)
		require.InDelta(t, 0.30, stats.RangeTotalConsumed, 1e-8)
		require.InDelta(t, 1.00, stats.RangeTotalExpired, 1e-8)
		require.Greater(t, stats.ConsumptionRate, 0.0)
		require.Greater(t, stats.ExpirationRate, 0.0)

		// Verify filtered admin records queries.
		for _, tc := range []struct {
			query   string
			wantMin int64
			wantUID int64
		}{
			{fmt.Sprintf("user_id=%d", u1.ID), 1, u1.ID},
			{"search=" + u4.Username, 1, u4.ID},
			{"client_ip=" + ipA, 2, 0},
			{"client_ip=" + ipB, 1, u4.ID},
			{fmt.Sprintf("start_date=%s&end_date=%s", today, today), 3, 0},
			{fmt.Sprintf("start_date=%s&end_date=%s", yesterday, yesterday), 1, u3.ID},
		} {
			rSt, rEnv := env.adminGet("/api/v1/admin/checkin/records?" + tc.query)
			require.Equal(t, http.StatusOK, rSt, tc.query)
			page := decodeData[struct {
				Items []struct {
					UserID int64 `json:"user_id"`
				} `json:"items"`
				Total int64 `json:"total"`
			}](t, rEnv)
			require.Equal(t, tc.wantMin, page.Total, "query=%s", tc.query)
			if tc.wantUID > 0 {
				require.Equal(t, tc.wantUID, page.Items[0].UserID, "query=%s", tc.query)
			}
		}
	})
}
