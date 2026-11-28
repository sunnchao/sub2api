//go:build e2e

package checkin_e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type apiEnvelope struct {
	Code     int               `json:"code"`
	Message  string            `json:"message"`
	Reason   string            `json:"reason"`
	Metadata map[string]string `json:"metadata"`
	Data     json.RawMessage   `json:"data"`
}

type httpResult struct {
	Status int
	Body   []byte
	Env    apiEnvelope
}

type testUser struct {
	ID       int64
	Email    string
	Username string
	Password string
	Token    string
}

type dbLot struct {
	ID              int64
	UserID          int64
	SourceType      string
	SourceID        int64
	InitialAmount   float64
	RemainingAmount float64
	FrozenAmount    float64
	ConsumedAmount  float64
	ExpiredAmount   float64
	ExpiresAt       time.Time
	Status          string
}

type dbAllocation struct {
	ID                     int64
	UserID                 int64
	RefType                string
	RefKey                 string
	AllocSeq               int
	LotID                  sql.NullInt64
	AllocatedAmount        float64
	CapturedAmount         float64
	ReleasedAmount         float64
	ForfeitedExpiredAmount float64
}

func (e *e2eEnv) doJSON(method, path, authHeaderName, authHeaderVal string, body any, extraHeaders ...map[string]string) httpResult {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(e.t, err)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.baseURL+path, reader)
	require.NoError(e.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authHeaderName != "" && authHeaderVal != "" {
		req.Header.Set(authHeaderName, authHeaderVal)
	}
	for _, hdrs := range extraHeaders {
		for k, v := range hdrs {
			req.Header.Set(k, v)
		}
	}
	resp, err := e.httpClient.Do(req)
	require.NoError(e.t, err, "HTTP %s %s", method, path)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(e.t, err)

	var env apiEnvelope
	_ = json.Unmarshal(raw, &env)
	return httpResult{
		Status: resp.StatusCode,
		Body:   raw,
		Env:    env,
	}
}

func (e *e2eEnv) publicGet(path string) httpResult {
	return e.doJSON(http.MethodGet, path, "", "", nil)
}

func (e *e2eEnv) apiGet(path, token string, extraHeaders ...map[string]string) httpResult {
	return e.doJSON(http.MethodGet, path, "Authorization", "Bearer "+token, nil, extraHeaders...)
}

func (e *e2eEnv) apiPost(path, token string, body any, extraHeaders ...map[string]string) httpResult {
	return e.doJSON(http.MethodPost, path, "Authorization", "Bearer "+token, body, extraHeaders...)
}

func (e *e2eEnv) apiPut(path, token string, body any, extraHeaders ...map[string]string) httpResult {
	return e.doJSON(http.MethodPut, path, "Authorization", "Bearer "+token, body, extraHeaders...)
}

func (e *e2eEnv) adminGet(path string) (int, apiEnvelope) {
	res := e.apiGet(path, e.adminToken)
	return res.Status, res.Env
}

func (e *e2eEnv) adminPost(path string, body any) (int, apiEnvelope) {
	res := e.apiPost(path, e.adminToken, body)
	return res.Status, res.Env
}

func (e *e2eEnv) adminPut(path string, body any) (int, apiEnvelope) {
	res := e.apiPut(path, e.adminToken, body)
	return res.Status, res.Env
}

func (e *e2eEnv) adminKeyGet(path, adminAPIKey string) httpResult {
	return e.doJSON(http.MethodGet, path, "x-api-key", adminAPIKey, nil)
}

func (e *e2eEnv) gatewayGet(path, apiKey string) httpResult {
	return e.doJSON(http.MethodGet, path, "Authorization", "Bearer "+apiKey, nil)
}

func (e *e2eEnv) gatewayPost(path, apiKey string, body any) httpResult {
	return e.doJSON(http.MethodPost, path, "Authorization", "Bearer "+apiKey, body)
}

func decodeData[T any](t *testing.T, env apiEnvelope) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(env.Data, &out), "unmarshal data: %s", string(env.Data))
	return out
}

func decodeRaw[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(raw, &out), "unmarshal raw: %s", string(raw))
	return out
}

func (e *e2eEnv) loginAdmin() {
	res := e.doJSON(http.MethodPost, "/api/v1/auth/login", "", "", map[string]any{
		"email":    e.adminEmail,
		"password": e.adminPassword,
	})
	if res.Status != http.StatusOK || res.Env.Code != 0 {
		msg := fmt.Sprintf("admin login failed: status=%d body=%s", res.Status, string(res.Body))
		if e.t != nil {
			e.t.Fatal(msg)
		}
		panic(msg)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(res.Env.Data, &payload); err != nil || payload.AccessToken == "" {
		msg := fmt.Sprintf("decode admin login token failed: %v body=%s", err, string(res.Env.Data))
		if e.t != nil {
			e.t.Fatal(msg)
		}
		panic(msg)
	}
	e.adminToken = payload.AccessToken
}

func (e *e2eEnv) createTestUser(balance float64) testUser {
	e.t.Helper()
	email := fmt.Sprintf("u_%s@example.com", randomHex(6))
	password := "UserPass!123"
	username := "u_" + randomHex(4)

	status, env := e.adminPost("/api/v1/admin/users", map[string]any{
		"email":    email,
		"password": password,
		"username": username,
		"role":     "user",
		"balance":  balance,
	})
	require.Equal(e.t, http.StatusOK, status, "create user body=%s", string(env.Data))
	require.Equal(e.t, 0, env.Code)

	created := decodeData[struct {
		ID       int64  `json:"id"`
		Email    string `json:"email"`
		Username string `json:"username"`
	}](e.t, env)

	// Log in as user to get JWT token.
	loginRes := e.doJSON(http.MethodPost, "/api/v1/auth/login", "", "", map[string]any{
		"email":    email,
		"password": password,
	})
	require.Equal(e.t, http.StatusOK, loginRes.Status, "login user body=%s", string(loginRes.Body))
	loginData := decodeData[struct {
		AccessToken string `json:"access_token"`
	}](e.t, loginRes.Env)

	return testUser{
		ID:       created.ID,
		Email:    created.Email,
		Username: created.Username,
		Password: password,
		Token:    loginData.AccessToken,
	}
}

func (e *e2eEnv) registerUser(emailPrefix string) testUser {
	e.t.Helper()
	email := fmt.Sprintf("%s_%s@example.com", emailPrefix, randomHex(5))
	password := "UserPass!123"
	res := e.doJSON(http.MethodPost, "/api/v1/auth/register", "", "", map[string]any{
		"email":    email,
		"password": password,
	})
	require.Equal(e.t, http.StatusOK, res.Status, "register body=%s", string(res.Body))
	data := decodeData[struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID       int64  `json:"id"`
			Email    string `json:"email"`
			Username string `json:"username"`
		} `json:"user"`
	}](e.t, res.Env)
	return testUser{
		ID:       data.User.ID,
		Email:    data.User.Email,
		Username: data.User.Username,
		Password: password,
		Token:    data.AccessToken,
	}
}

func (e *e2eEnv) createAPIKeyForUser(userToken string, groupID *int64) (int64, string) {
	e.t.Helper()
	body := map[string]any{
		"name": "e2e-key-" + randomHex(4),
	}
	if groupID != nil {
		body["group_id"] = *groupID
	}
	res := e.apiPost("/api/v1/keys", userToken, body)
	require.Equal(e.t, http.StatusOK, res.Status, "create key body=%s", string(res.Body))
	data := decodeData[struct {
		ID  int64  `json:"id"`
		Key string `json:"key"`
	}](e.t, res.Env)
	require.NotEmpty(e.t, data.Key)
	return data.ID, data.Key
}

func (e *e2eEnv) createGroup(req map[string]any) int64 {
	e.t.Helper()
	status, env := e.adminPost("/api/v1/admin/groups", req)
	require.Contains(e.t, []int{http.StatusOK, http.StatusCreated}, status, "create group msg=%s data=%s", env.Message, string(env.Data))
	data := decodeData[struct {
		ID int64 `json:"id"`
	}](e.t, env)
	return data.ID
}

func (e *e2eEnv) createAccount(req map[string]any) int64 {
	e.t.Helper()
	status, env := e.adminPost("/api/v1/admin/accounts", req)
	require.Contains(e.t, []int{http.StatusOK, http.StatusCreated}, status, "create account msg=%s data=%s", env.Message, string(env.Data))
	data := decodeData[struct {
		ID int64 `json:"id"`
	}](e.t, env)
	return data.ID
}

func (e *e2eEnv) setupAnthropicUpstream(t *testing.T) int64 {
	t.Helper()
	groupID := e.createGroup(map[string]any{
		"name":              "e2e-anth-" + randomHex(4),
		"platform":          "anthropic",
		"rate_multiplier":   1.0,
		"subscription_type": "standard",
	})
	_ = e.createAccount(map[string]any{
		"name":     "e2e-anth-acc-" + randomHex(4),
		"platform": "anthropic",
		"type":     "apikey",
		"credentials": map[string]any{
			"api_key":  "sk-ant-mock-e2e",
			"base_url": e.anthropic.URL(),
		},
		"concurrency": 50,
		"priority":    1,
		"group_ids":   []int64{groupID},
	})
	return groupID
}

var (
	e2eVertexRSAOnce sync.Once
	e2eVertexRSAPEM  string
	e2eVertexRSAErr  error
)

func getE2EEphemeralRSAPEM() (string, error) {
	e2eVertexRSAOnce.Do(func() {
		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			e2eVertexRSAErr = err
			return
		}
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			e2eVertexRSAErr = err
			return
		}
		e2eVertexRSAPEM = string(pem.EncodeToMemory(&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: der,
		}))
	})
	return e2eVertexRSAPEM, e2eVertexRSAErr
}

func (e *e2eEnv) setupVertexBatchImageUpstream(t *testing.T, unitPrice float64) int64 {
	t.Helper()
	clientEmail := "e2e@e2e-vertex-proj.iam.gserviceaccount.com"
	privateKeyID := "pk-e2e-1"
	require.NoError(t, seedVertexTokenInRedis(context.Background(), e.rdb, clientEmail, privateKeyID, "ya29.e2e-vertex-token"))

	privPEM, err := getE2EEphemeralRSAPEM()
	require.NoError(t, err)

	groupID := e.createGroup(map[string]any{
		"name":                            "e2e-gem-batch-" + randomHex(4),
		"platform":                        "gemini",
		"rate_multiplier":                 1.0,
		"subscription_type":               "standard",
		"allow_image_generation":          true,
		"allow_batch_image_generation":    true,
		"image_price_1k":                  unitPrice,
		"batch_image_discount_multiplier": 1.0,
		"batch_image_hold_multiplier":     1.0,
	})
	saJSON := map[string]any{
		"type":           "service_account",
		"project_id":     "e2e-vertex-proj",
		"private_key_id": privateKeyID,
		"private_key":    privPEM,
		"client_email":   clientEmail,
	}
	_ = e.createAccount(map[string]any{
		"name":     "e2e-vertex-sa-" + randomHex(4),
		"platform": "gemini",
		"type":     "service_account",
		"credentials": map[string]any{
			"project_id":           "e2e-vertex-proj",
			"location":             "global",
			"service_account_json": saJSON,
		},
		"concurrency": 20,
		"priority":    1,
		"group_ids":   []int64{groupID},
	})
	return groupID
}

func (e *e2eEnv) enableCheckinConfig(cfg map[string]any) {
	e.t.Helper()
	status, env := e.adminPut("/api/v1/admin/checkin/config", cfg)
	require.Equal(e.t, http.StatusOK, status, "update checkin config msg=%s data=%s", env.Message, string(env.Data))
	require.Equal(e.t, 0, env.Code)
}

func (e *e2eEnv) requireCheckinAuditLogRecorded(t *testing.T, marker string) {
	t.Helper()
	eventually(t, 5*time.Second, 100*time.Millisecond, func() bool {
		st, logEnv := e.adminGet("/api/v1/admin/audit-logs?method=PUT&q=checkin&page=1&page_size=20")
		if st != http.StatusOK {
			return false
		}
		page := decodeData[struct {
			Items []struct {
				ID         int64  `json:"id"`
				Path       string `json:"path"`
				StatusCode int    `json:"status_code"`
			} `json:"items"`
			Total int64 `json:"total"`
		}](t, logEnv)
		if page.Total < 1 || len(page.Items) == 0 {
			return false
		}
		limit := len(page.Items)
		if limit > 5 {
			limit = 5
		}
		for _, item := range page.Items[:limit] {
			if !strings.Contains(item.Path, "checkin") || item.StatusCode != http.StatusOK {
				continue
			}
			detSt, detEnv := e.adminGet(fmt.Sprintf("/api/v1/admin/audit-logs/%d", item.ID))
			if detSt != http.StatusOK {
				continue
			}
			det := decodeData[struct {
				RequestBody string `json:"request_body"`
			}](t, detEnv)
			if strings.Contains(det.RequestBody, marker) {
				return true
			}
		}
		return false
	}, "expected audit log entry containing %q for checkin config update", marker)
}

func defaultEnabledCheckinConfig(baseAmount float64) map[string]any {
	return map[string]any{
		"enabled":                true,
		"reward_mode":            "fixed",
		"base_amount":            baseAmount,
		"min_amount":             baseAmount,
		"max_amount":             baseAmount,
		"amount_precision":       2,
		"validity_days":          1,
		"milestones":             []any{},
		"milestone_cycle_repeat": false,
		"eligibility": map[string]any{
			"min_account_age_days":   0,
			"min_total_recharged":    0,
			"max_available_balance":  0,
			"allowed_signup_sources": []string{},
		},
		"risk": map[string]any{
			"captcha_enabled":         false,
			"max_accounts_per_ip_day": 0,
			"daily_total_budget_usd":  0,
		},
		"rules_description": "E2E check-in rules",
	}
}

func (e *e2eEnv) enableBalancePaymentAndCreatePlan(price float64) int64 {
	e.t.Helper()
	status, env := e.adminPut("/api/v1/admin/payment/config", map[string]any{
		"enabled":               true,
		"balance_disabled":      false,
		"enabled_payment_types": []string{"balance"},
	})
	require.Equal(e.t, http.StatusOK, status, "enable payment config: %s", env.Message)

	groupID := e.createGroup(map[string]any{
		"name":              "e2e-sub-grp-" + randomHex(4),
		"platform":          "anthropic",
		"rate_multiplier":   1.0,
		"subscription_type": "subscription",
		"daily_limit_usd":   100.0,
	})

	pStatus, pEnv := e.adminPost("/api/v1/admin/payment/plans", map[string]any{
		"group_id":      groupID,
		"name":          "E2E Sub Plan " + randomHex(3),
		"description":   "E2E subscription plan",
		"price":         price,
		"currency":      "USD",
		"validity_days": 30,
		"validity_unit": "day",
		"for_sale":      true,
		"sort_order":    1,
	})
	require.Equal(e.t, http.StatusCreated, pStatus, "create plan: %s %s", pEnv.Message, string(pEnv.Data))
	plan := decodeData[struct {
		ID int64 `json:"id"`
	}](e.t, pEnv)
	return plan.ID
}

func (e *e2eEnv) setUserRegularBalanceSQL(userID int64, balance float64) {
	e.t.Helper()
	_, err := e.db.ExecContext(context.Background(), `UPDATE users SET balance = $1 WHERE id = $2`, balance, userID)
	require.NoError(e.t, err)
	if e.rdb != nil {
		_ = e.rdb.Del(context.Background(), fmt.Sprintf("billing:balance:%d", userID)).Err()
	}
}

func (e *e2eEnv) getUserDBBalance(userID int64) (balance, frozen, totalRecharged float64) {
	e.t.Helper()
	err := e.db.QueryRowContext(context.Background(),
		`SELECT balance, frozen_balance, total_recharged FROM users WHERE id = $1`, userID,
	).Scan(&balance, &frozen, &totalRecharged)
	require.NoError(e.t, err)
	return balance, frozen, totalRecharged
}

func (e *e2eEnv) seedExpiringLotSQL(userID int64, amount float64, expiresAt time.Time, status string) int64 {
	e.t.Helper()
	ctx := context.Background()
	var recID int64
	// Use a unique synthetic source_id to satisfy idx_expiring_lots_source without colliding with real checkins.
	err := e.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(source_id), 1000000) + 1 FROM user_expiring_balance_lots WHERE source_type = 'e2e_seed'`).Scan(&recID)
	require.NoError(e.t, err)

	var lotID int64
	rem := amount
	consumed := 0.0
	expired := 0.0
	switch status {
	case "exhausted":
		rem = 0
		consumed = amount
	case "expired":
		rem = 0
		expired = amount
	}
	err = e.db.QueryRowContext(ctx, `
		INSERT INTO user_expiring_balance_lots
			(user_id, source_type, source_id, initial_amount, remaining_amount, frozen_amount, consumed_amount, expired_amount, expires_at, status)
		VALUES ($1, 'e2e_seed', $2, $3, $4, 0, $5, $6, $7, $8)
		RETURNING id
	`, userID, recID, amount, rem, consumed, expired, expiresAt, status).Scan(&lotID)
	require.NoError(e.t, err)
	if e.rdb != nil {
		_ = e.rdb.Del(ctx, fmt.Sprintf("billing:balance:%d", userID)).Err()
	}
	return lotID
}

func (e *e2eEnv) seedCheckinRecordSQL(userID int64, checkinDate string, streakDays, cycleDay int, totalAmount float64) int64 {
	e.t.Helper()
	ctx := context.Background()
	loc := mustLoadLoc(e.t, e.timezone)
	d, err := time.ParseInLocation("2006-01-02", checkinDate, loc)
	require.NoError(e.t, err)
	expiresAt := d.AddDate(0, 0, 1)

	var recID int64
	err = e.db.QueryRowContext(ctx, `
		INSERT INTO user_checkin_records
			(user_id, checkin_date, streak_days, milestone_cycle_day, base_amount, milestone_bonus_amount, total_amount, validity_days, expires_at, client_ip)
		VALUES ($1, $2::date, $3, $4, $5, 0, $5, 1, $6, '127.0.0.1')
		RETURNING id
	`, userID, checkinDate, streakDays, cycleDay, totalAmount, expiresAt).Scan(&recID)
	require.NoError(e.t, err)
	return recID
}

func (e *e2eEnv) listUserLotsDB(userID int64) []dbLot {
	e.t.Helper()
	rows, err := e.db.QueryContext(context.Background(), `
		SELECT id, user_id, source_type, source_id, initial_amount, remaining_amount, frozen_amount, consumed_amount, expired_amount, expires_at, status
		FROM user_expiring_balance_lots
		WHERE user_id = $1
		ORDER BY expires_at ASC, id ASC
	`, userID)
	require.NoError(e.t, err)
	defer func() { _ = rows.Close() }()

	var out []dbLot
	for rows.Next() {
		var l dbLot
		require.NoError(e.t, rows.Scan(&l.ID, &l.UserID, &l.SourceType, &l.SourceID, &l.InitialAmount, &l.RemainingAmount, &l.FrozenAmount, &l.ConsumedAmount, &l.ExpiredAmount, &l.ExpiresAt, &l.Status))
		out = append(out, l)
	}
	require.NoError(e.t, rows.Err())
	return out
}

func (e *e2eEnv) getLotDB(lotID int64) dbLot {
	e.t.Helper()
	var l dbLot
	err := e.db.QueryRowContext(context.Background(), `
		SELECT id, user_id, source_type, source_id, initial_amount, remaining_amount, frozen_amount, consumed_amount, expired_amount, expires_at, status
		FROM user_expiring_balance_lots
		WHERE id = $1
	`, lotID).Scan(&l.ID, &l.UserID, &l.SourceType, &l.SourceID, &l.InitialAmount, &l.RemainingAmount, &l.FrozenAmount, &l.ConsumedAmount, &l.ExpiredAmount, &l.ExpiresAt, &l.Status)
	require.NoError(e.t, err)
	return l
}

func (e *e2eEnv) listAllocationsDB(refType, refKey string) []dbAllocation {
	e.t.Helper()
	rows, err := e.db.QueryContext(context.Background(), `
		SELECT id, user_id, ref_type, ref_key, alloc_seq, lot_id, allocated_amount, captured_amount, released_amount, forfeited_expired_amount
		FROM user_expiring_balance_allocations
		WHERE ref_type = $1 AND ref_key = $2
		ORDER BY alloc_seq ASC
	`, refType, refKey)
	require.NoError(e.t, err)
	defer func() { _ = rows.Close() }()

	var out []dbAllocation
	for rows.Next() {
		var a dbAllocation
		require.NoError(e.t, rows.Scan(&a.ID, &a.UserID, &a.RefType, &a.RefKey, &a.AllocSeq, &a.LotID, &a.AllocatedAmount, &a.CapturedAmount, &a.ReleasedAmount, &a.ForfeitedExpiredAmount))
		out = append(out, a)
	}
	require.NoError(e.t, rows.Err())
	return out
}

func verifyLotInvariant(t *testing.T, db *sql.DB, lotID int64) {
	t.Helper()
	var initAmt, remAmt, frozenAmt, consumedAmt, expiredAmt float64
	err := db.QueryRowContext(context.Background(), `
		SELECT initial_amount, remaining_amount, frozen_amount, consumed_amount, expired_amount
		FROM user_expiring_balance_lots
		WHERE id = $1
	`, lotID).Scan(&initAmt, &remAmt, &frozenAmt, &consumedAmt, &expiredAmt)
	require.NoError(t, err)
	sum := remAmt + frozenAmt + consumedAmt + expiredAmt
	require.InDelta(t, initAmt, sum, 1e-8,
		"lot %d accounting invariant violated: initial=%.8f != rem(%.8f)+frozen(%.8f)+consumed(%.8f)+expired(%.8f)=%.8f",
		lotID, initAmt, remAmt, frozenAmt, consumedAmt, expiredAmt, sum)
}

func eventually(t *testing.T, timeout, interval time.Duration, cond func() bool, msgAndArgs ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			require.Fail(t, "eventually timed out", msgAndArgs...)
			return
		}
		time.Sleep(interval)
	}
}

func mustLoadLoc(t *testing.T, tz string) *time.Location {
	t.Helper()
	if strings.TrimSpace(tz) == "" {
		tz = defaultTimezone
	}
	loc, err := time.LoadLocation(tz)
	require.NoError(t, err)
	return loc
}

func startOfDayInLoc(now time.Time, loc *time.Location) time.Time {
	in := now.In(loc)
	return time.Date(in.Year(), in.Month(), in.Day(), 0, 0, 0, 0, loc)
}

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-7
}
