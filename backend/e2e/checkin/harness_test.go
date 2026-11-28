//go:build e2e

package checkin_e2e

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

const (
	e2ePostgresImage = "postgres:18.1-alpine3.23"
	e2eRedisImage    = "redis:8.4-alpine"

	defaultAdminEmail    = "admin-e2e@sub2api.local"
	defaultAdminPassword = "AdminE2E!Pass123"
	defaultTimezone      = "Asia/Shanghai"
)

var (
	sharedHarness *e2eHarness
	skipReason    string
)

type e2eHarness struct {
	repoRoot    string
	backendDir  string
	serverBin   string
	buildDir    string
	pgDSN       string
	pgBaseURL   string
	redisAddr   string
	externalURL string

	db  *sql.DB
	rdb *redis.Client

	smtpServer *fakeSMTPServer
	anthropic  *mockAnthropicUpstream
	openai     *mockOpenAIUpstream
	vertex     *mockVertexBatchUpstream

	defaultEnvMu sync.Mutex
	defaultEnv   *e2eEnv
}

type e2eEnv struct {
	t          *testing.T
	baseURL    string
	db         *sql.DB
	rdb        *redis.Client
	httpClient *http.Client

	adminEmail    string
	adminPassword string
	adminToken    string
	timezone      string
	runMode       string

	smtpServer *fakeSMTPServer
	anthropic  *mockAnthropicUpstream
	openai     *mockOpenAIUpstream
	vertex     *mockVertexBatchUpstream

	cmd       *exec.Cmd
	dataDir   string
	dbName    string
	redisDB   int
	jwtSecret string
	owned     bool
}

type envConfig struct {
	timezone string
	runMode  string
	isolated bool
}

type envOption func(*envConfig)

func withRunMode(mode string) envOption {
	return func(c *envConfig) {
		c.runMode = mode
		c.isolated = true
	}
}

func withTimezone(tz string) envOption {
	return func(c *envConfig) {
		c.timezone = tz
		c.isolated = true
	}
}

func TestMain(m *testing.M) {
	ctx := context.Background()

	backendDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: getwd failed: %v\n", err)
		os.Exit(1)
	}
	// When running `go test ./e2e/checkin/...` from backend/, cwd is backend/e2e/checkin.
	for i := 0; i < 4; i++ {
		if _, statErr := os.Stat(filepath.Join(backendDir, "go.mod")); statErr == nil {
			break
		}
		backendDir = filepath.Dir(backendDir)
	}
	repoRoot := filepath.Dir(backendDir)

	h := &e2eHarness{
		repoRoot:    repoRoot,
		backendDir:  backendDir,
		externalURL: strings.TrimRight(strings.TrimSpace(os.Getenv("E2E_BASE_URL")), "/"),
		pgDSN:       strings.TrimSpace(os.Getenv("E2E_POSTGRES_DSN")),
		redisAddr:   strings.TrimSpace(os.Getenv("E2E_REDIS_ADDR")),
	}

	var pgCleanup func()
	var redisCleanup func()

	if h.externalURL == "" {
		if h.pgDSN == "" {
			if !dockerIsAvailable(ctx) {
				if os.Getenv("CI") != "" || os.Getenv("E2E_REQUIRE_POSTGRES") == "1" {
					fmt.Fprintln(os.Stderr, "FAIL: E2E tests require Docker or E2E_POSTGRES_DSN when CI is set")
					os.Exit(1)
				}
				skipReason = "Docker and E2E_POSTGRES_DSN are unavailable; skipping E2E execution locally"
				fmt.Fprintln(os.Stderr, "SKIP:", skipReason)
				os.Exit(0)
			}

			pgImage := selectEnvImage("SUB2API_TEST_POSTGRES_IMAGE", e2ePostgresImage)
			pgC, pgErr := tcpostgres.Run(
				ctx,
				pgImage,
				tcpostgres.WithDatabase("sub2api_e2e"),
				tcpostgres.WithUsername("postgres"),
				tcpostgres.WithPassword("postgres"),
				tcpostgres.BasicWaitStrategies(),
			)
			if pgErr != nil {
				fmt.Fprintf(os.Stderr, "e2e: failed to start postgres container: %v\n", pgErr)
				os.Exit(1)
			}
			pgCleanup = func() { _ = pgC.Terminate(context.Background()) }
			dsn, dsnErr := pgC.ConnectionString(ctx, "sslmode=disable")
			if dsnErr != nil {
				pgCleanup()
				fmt.Fprintf(os.Stderr, "e2e: failed to get postgres dsn: %v\n", dsnErr)
				os.Exit(1)
			}
			h.pgDSN = dsn
		}

		if h.redisAddr == "" {
			if !dockerIsAvailable(ctx) {
				if pgCleanup != nil {
					pgCleanup()
				}
				fmt.Fprintln(os.Stderr, "FAIL: E2E_REDIS_ADDR unset and Docker unavailable")
				os.Exit(1)
			}
			redisC, rErr := tcredis.Run(ctx, e2eRedisImage)
			if rErr != nil {
				if pgCleanup != nil {
					pgCleanup()
				}
				fmt.Fprintf(os.Stderr, "e2e: failed to start redis container: %v\n", rErr)
				os.Exit(1)
			}
			redisCleanup = func() { _ = redisC.Terminate(context.Background()) }
			rHost, hErr := redisC.Host(ctx)
			rPort, pErr := redisC.MappedPort(ctx, "6379/tcp")
			if hErr != nil || pErr != nil {
				redisCleanup()
				if pgCleanup != nil {
					pgCleanup()
				}
				fmt.Fprintf(os.Stderr, "e2e: failed to resolve redis addr: %v / %v\n", hErr, pErr)
				os.Exit(1)
			}
			h.redisAddr = fmt.Sprintf("%s:%d", rHost, rPort.Int())
		}

		buildDir, mkErr := os.MkdirTemp("", "sub2api-e2e-bin-*")
		if mkErr != nil {
			if redisCleanup != nil {
				redisCleanup()
			}
			if pgCleanup != nil {
				pgCleanup()
			}
			fmt.Fprintf(os.Stderr, "e2e: mkdirtemp failed: %v\n", mkErr)
			os.Exit(1)
		}
		h.buildDir = buildDir
		h.serverBin = filepath.Join(buildDir, "sub2api-e2e")
		buildArgs := []string{"build"}
		if _, statErr := os.Stat(filepath.Join(backendDir, "internal", "web", "dist", "index.html")); statErr == nil {
			buildArgs = append(buildArgs, "-tags", "embed")
		}
		buildArgs = append(buildArgs, "-o", h.serverBin, "./cmd/server")
		buildCmd := exec.CommandContext(ctx, "go", buildArgs...)
		buildCmd.Dir = backendDir
		if out, bErr := buildCmd.CombinedOutput(); bErr != nil {
			_ = os.RemoveAll(buildDir)
			if redisCleanup != nil {
				redisCleanup()
			}
			if pgCleanup != nil {
				pgCleanup()
			}
			fmt.Fprintf(os.Stderr, "e2e: go build ./cmd/server failed: %v\n%s\n", bErr, string(out))
			os.Exit(1)
		}
	}

	if h.pgDSN != "" {
		db, openErr := openSQLWithRetry(ctx, h.pgDSN, 30*time.Second)
		if openErr != nil {
			fmt.Fprintf(os.Stderr, "e2e: open postgres failed: %v\n", openErr)
			os.Exit(1)
		}
		h.db = db
		h.pgBaseURL = h.pgDSN
	}

	if h.redisAddr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: h.redisAddr})
		if pingErr := rdb.Ping(ctx).Err(); pingErr != nil {
			fmt.Fprintf(os.Stderr, "e2e: ping redis failed: %v\n", pingErr)
			os.Exit(1)
		}
		h.rdb = rdb
	}

	smtpSrv, smtpErr := startFakeSMTPServer()
	if smtpErr != nil {
		fmt.Fprintf(os.Stderr, "e2e: fake smtp start failed: %v\n", smtpErr)
		os.Exit(1)
	}
	h.smtpServer = smtpSrv
	h.anthropic = startMockAnthropicUpstream()
	h.openai = startMockOpenAIUpstream()
	h.vertex = startMockVertexBatchUpstream()

	sharedHarness = h
	code := m.Run()

	if h.defaultEnv != nil {
		h.defaultEnv.stopProcess()
	}
	h.anthropic.Close()
	h.openai.Close()
	h.vertex.Close()
	h.smtpServer.Close()
	if h.rdb != nil {
		_ = h.rdb.Close()
	}
	if h.db != nil {
		_ = h.db.Close()
	}
	if h.buildDir != "" {
		_ = os.RemoveAll(h.buildDir)
	}
	if redisCleanup != nil {
		redisCleanup()
	}
	if pgCleanup != nil {
		pgCleanup()
	}
	os.Exit(code)
}

func dockerIsAvailable(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "docker", "info")
	return cmd.Run() == nil
}

func selectEnvImage(envKey, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v
	}
	return fallback
}

func openSQLWithRetry(ctx context.Context, dsn string, timeout time.Duration) (*sql.DB, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		db, err := sql.Open("postgres", dsn)
		if err == nil {
			pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err = db.PingContext(pctx)
			cancel()
			if err == nil {
				return db, nil
			}
			_ = db.Close()
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	return nil, fmt.Errorf("postgres not ready within %s: %w", timeout, lastErr)
}

func newE2EEnv(t *testing.T, opts ...envOption) *e2eEnv {
	t.Helper()
	if skipReason != "" {
		t.Skip(skipReason)
	}
	require.NotNil(t, sharedHarness, "e2e harness not initialized")

	cfg := envConfig{
		timezone: defaultTimezone,
		runMode:  "standard",
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if sharedHarness.externalURL != "" && !cfg.isolated {
		env := &e2eEnv{
			t:             t,
			baseURL:       sharedHarness.externalURL,
			db:            sharedHarness.db,
			rdb:           sharedHarness.rdb,
			httpClient:    &http.Client{Timeout: 15 * time.Second},
			adminEmail:    envOrDefault("E2E_ADMIN_EMAIL", defaultAdminEmail),
			adminPassword: envOrDefault("E2E_ADMIN_PASSWORD", defaultAdminPassword),
			timezone:      cfg.timezone,
			runMode:       cfg.runMode,
			smtpServer:    sharedHarness.smtpServer,
			anthropic:     sharedHarness.anthropic,
			openai:        sharedHarness.openai,
			vertex:        sharedHarness.vertex,
		}
		env.loginAdmin()
		env.resetSharedState()
		return env
	}

	if cfg.isolated {
		env := sharedHarness.startChildEnv(t, cfg)
		t.Cleanup(func() {
			env.stopProcess()
		})
		return env
	}

	sharedHarness.defaultEnvMu.Lock()
	if sharedHarness.defaultEnv == nil {
		sharedHarness.defaultEnv = sharedHarness.startChildEnv(nil, cfg)
	}
	sharedHarness.defaultEnvMu.Unlock()

	env := &e2eEnv{
		t:             t,
		baseURL:       sharedHarness.defaultEnv.baseURL,
		db:            sharedHarness.defaultEnv.db,
		rdb:           sharedHarness.defaultEnv.rdb,
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		adminEmail:    sharedHarness.defaultEnv.adminEmail,
		adminPassword: sharedHarness.defaultEnv.adminPassword,
		adminToken:    sharedHarness.defaultEnv.adminToken,
		timezone:      cfg.timezone,
		runMode:       cfg.runMode,
		redisDB:       sharedHarness.defaultEnv.redisDB,
		jwtSecret:     sharedHarness.defaultEnv.jwtSecret,
		smtpServer:    sharedHarness.smtpServer,
		anthropic:     sharedHarness.anthropic,
		openai:        sharedHarness.openai,
		vertex:        sharedHarness.vertex,
	}
	env.resetSharedState()
	return env
}

func (h *e2eHarness) startChildEnv(t *testing.T, cfg envConfig) *e2eEnv {
	var dataDir string
	if t != nil {
		dataDir = t.TempDir()
	} else {
		var err error
		dataDir, err = os.MkdirTemp("", "sub2api-e2e-data-*")
		if err != nil {
			panic(fmt.Sprintf("mkdirtemp dataDir: %v", err))
		}
	}

	pricingDir := filepath.Join(dataDir, "pricing")
	if err := os.MkdirAll(pricingDir, 0o755); err != nil {
		panic(fmt.Sprintf("mkdir pricingDir: %v", err))
	}
	fallbackPricing := filepath.Join(h.backendDir, "resources", "model-pricing", "model_prices_and_context_window.json")
	if raw, err := os.ReadFile(fallbackPricing); err == nil {
		_ = os.WriteFile(filepath.Join(pricingDir, "model_pricing.json"), raw, 0o644)
	}

	port, err := pickFreePort()
	if err != nil {
		panic(fmt.Sprintf("pickFreePort: %v", err))
	}

	dbDSN := h.pgDSN
	dbName := ""
	redisDB := 0
	var envDB *sql.DB
	var envRDB *redis.Client

	if t != nil && cfg.isolated {
		dbName = "e2e_iso_" + randomHex(6)
		if _, execErr := h.db.ExecContext(context.Background(), fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)); execErr != nil {
			t.Fatalf("create isolated db %s: %v", dbName, execErr)
		}
		dbDSN = replaceDSNDatabase(h.pgDSN, dbName)
		envDB, err = openSQLWithRetry(context.Background(), dbDSN, 10*time.Second)
		require.NoError(t, err)
		redisDB = 2
		envRDB = redis.NewClient(&redis.Options{Addr: h.redisAddr, DB: redisDB})
		require.NoError(t, envRDB.FlushDB(context.Background()).Err())
		t.Cleanup(func() {
			if envRDB != nil {
				_ = envRDB.Close()
			}
			if envDB != nil {
				_ = envDB.Close()
			}
			_, _ = h.db.ExecContext(context.Background(), fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()`, dbName))
			_, _ = h.db.ExecContext(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName))
		})
	} else {
		envDB = h.db
		envRDB = h.rdb
	}

	pgHost, pgPort, pgUser, pgPass, pgDBName, pgSSL := parsePostgresDSN(dbDSN)
	rHost, rPort := splitHostPortDefault(h.redisAddr, "6379")

	jwtSecret := "e2e-jwt-secret-" + randomHex(16)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	logPath := filepath.Join(dataDir, "server.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		panic(fmt.Sprintf("create server.log: %v", err))
	}

	cmd := exec.Command(h.serverBin)
	cmd.Dir = h.backendDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(),
		"DATA_DIR="+dataDir,
		"AUTO_SETUP=true",
		"ADMIN_EMAIL="+defaultAdminEmail,
		"ADMIN_PASSWORD="+defaultAdminPassword,
		"SERVER_HOST=127.0.0.1",
		"SERVER_PORT="+strconv.Itoa(port),
		"SERVER_MODE=release",
		"RUN_MODE="+cfg.runMode,
		"TZ="+cfg.timezone,
		"TIMEZONE="+cfg.timezone,
		"JWT_SECRET="+jwtSecret,
		"DATABASE_HOST="+pgHost,
		"DATABASE_PORT="+pgPort,
		"DATABASE_USER="+pgUser,
		"DATABASE_PASSWORD="+pgPass,
		"DATABASE_DBNAME="+pgDBName,
		"DATABASE_SSLMODE="+pgSSL,
		"REDIS_HOST="+rHost,
		"REDIS_PORT="+rPort,
		"REDIS_DB="+strconv.Itoa(redisDB),
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
		"BATCH_IMAGE_VERTEX_BATCH_PREDICTION_BASE_URL="+h.vertex.URL(),
		"BATCH_IMAGE_VERTEX_GCS_BASE_URL="+h.vertex.URL(),
	)

	if startErr := cmd.Start(); startErr != nil {
		_ = logFile.Close()
		panic(fmt.Sprintf("start sub2api-e2e: %v", startErr))
	}
	_ = logFile.Close()

	env := &e2eEnv{
		t:             t,
		baseURL:       baseURL,
		db:            envDB,
		rdb:           envRDB,
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		adminEmail:    defaultAdminEmail,
		adminPassword: defaultAdminPassword,
		timezone:      cfg.timezone,
		runMode:       cfg.runMode,
		smtpServer:    h.smtpServer,
		anthropic:     h.anthropic,
		openai:        h.openai,
		vertex:        h.vertex,
		cmd:           cmd,
		dataDir:       dataDir,
		dbName:        dbName,
		redisDB:       redisDB,
		jwtSecret:     jwtSecret,
		owned:         true,
	}

	if waitErr := waitForServerReady(baseURL, 45*time.Second); waitErr != nil {
		logs, _ := os.ReadFile(logPath)
		env.stopProcess()
		msg := fmt.Sprintf("server not ready at %s: %v\nlogs:\n%s", baseURL, waitErr, string(logs))
		if t != nil {
			t.Fatal(msg)
		}
		panic(msg)
	}

	env.loginAdmin()
	return env
}

func (e *e2eEnv) stopProcess() {
	if e == nil || e.cmd == nil || e.cmd.Process == nil {
		return
	}
	_ = e.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() {
		done <- e.cmd.Wait()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = e.cmd.Process.Kill()
		<-done
	}
	e.cmd = nil
	if e.t == nil && e.dataDir != "" {
		_ = os.RemoveAll(e.dataDir)
	}
}

func (e *e2eEnv) resetSharedState() {
	e.t.Helper()
	ctx := context.Background()

	if e.db != nil {
		candidates := []string{
			"user_expiring_balance_allocations",
			"user_expiring_balance_lots",
			"user_checkin_records",
			"checkin_daily_budgets",
			"batch_image_items",
			"batch_image_jobs",
			"payment_audit_logs",
			"payment_orders",
			"user_subscriptions",
			"audit_logs",
			"admin_audit_logs",
		}
		var existing []string
		for _, tbl := range candidates {
			var exists bool
			qErr := e.db.QueryRowContext(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM information_schema.tables
					WHERE table_schema = 'public' AND table_name = $1
				)
			`, tbl).Scan(&exists)
			require.NoError(e.t, qErr, "check table %s existence", tbl)
			if exists {
				existing = append(existing, `"`+tbl+`"`)
			}
		}
		if len(existing) > 0 {
			_, err := e.db.ExecContext(ctx, "TRUNCATE TABLE "+strings.Join(existing, ", ")+" RESTART IDENTITY CASCADE")
			require.NoError(e.t, err, "truncate shared tables")
		}

		// Delete protected SMTP / captcha credential rows and check-in keys before refreshing
		// SettingService's in-process cache via PUT /api/v1/admin/settings.
		_, err := e.db.ExecContext(ctx, `
			DELETE FROM settings
			WHERE key IN (
				'checkin_enabled', 'checkin_config',
				'balance_low_notify_enabled', 'balance_low_notify_threshold',
				'affiliate_enabled', 'affiliate_rebate_rate',
				'smtp_host', 'smtp_port', 'smtp_username', 'smtp_password', 'smtp_from', 'smtp_from_name', 'smtp_use_tls',
				'turnstile_enabled', 'turnstile_site_key', 'turnstile_secret_key',
				'tencent_captcha_enabled', 'tencent_captcha_app_id', 'tencent_captcha_app_secret_key', 'tencent_captcha_cloud_secret_id', 'tencent_captcha_cloud_secret_key', 'tencent_captcha_region',
				'aliyun_captcha_enabled', 'aliyun_captcha_access_key_id', 'aliyun_captcha_access_key_secret', 'aliyun_captcha_scene_id', 'aliyun_captcha_prefix', 'aliyun_captcha_region'
			)
		`)
		require.NoError(e.t, err, "reset test settings")
	}

	if e.rdb != nil {
		require.NoError(e.t, e.rdb.FlushDB(ctx).Err())
	}

	e.smtpServer.Reset()
	e.anthropic.Reset()
	e.openai.Reset()
	e.vertex.Reset()

	// Reset settings via admin API so SettingService / PaymentConfigService in-memory caches
	// stay synchronized with PostgreSQL without triggering Turnstile/Aliyun outbound validation.
	st, putEnv := e.adminPut("/api/v1/admin/settings", map[string]any{
		"balance_low_notify_enabled":   false,
		"balance_low_notify_threshold": 0.0,
		"affiliate_enabled":            false,
		"affiliate_rebate_rate":        0.0,
		"smtp_host":                    "",
		"smtp_port":                    587,
		"smtp_username":                "",
		"smtp_password":                "",
		"smtp_from_email":              "",
		"smtp_from_name":               "",
		"smtp_use_tls":                 false,
		"turnstile_enabled":            false,
		"tencent_captcha_enabled":      false,
		"aliyun_captcha_enabled":       false,
		"payment_enabled":              false,
		"payment_balance_disabled":     false,
	})
	require.Equal(e.t, http.StatusOK, st, "reset admin settings msg=%s", putEnv.Message)

	// Disable check-in cleanly via API so in-memory caches (if any) also align.
	_, _ = e.adminPut("/api/v1/admin/checkin/config", map[string]any{
		"enabled":                false,
		"reward_mode":            "fixed",
		"base_amount":            0.10,
		"min_amount":             0.05,
		"max_amount":             0.20,
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
		"rules_description": "",
	})
}

func waitForServerReady(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/api/v1/settings/public")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return lastErr
}

func pickFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = ln.Close() }()
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("unexpected listener addr type")
	}
	return tcpAddr.Port, nil
}

func randomHex(byteLen int) string {
	b := make([]byte, byteLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func envOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func parsePostgresDSN(dsn string) (host, port, user, pass, dbname, sslmode string) {
	host, port, user, pass, dbname, sslmode = "127.0.0.1", "5432", "postgres", "postgres", "sub2api_e2e", "disable"
	u, err := url.Parse(dsn)
	if err != nil {
		return
	}
	if u.Hostname() != "" {
		host = u.Hostname()
	}
	if u.Port() != "" {
		port = u.Port()
	}
	if u.User != nil {
		if u.User.Username() != "" {
			user = u.User.Username()
		}
		if p, ok := u.User.Password(); ok {
			pass = p
		}
	}
	if db := strings.TrimPrefix(u.Path, "/"); db != "" {
		dbname = db
	}
	if s := u.Query().Get("sslmode"); s != "" {
		sslmode = s
	}
	return
}

func replaceDSNDatabase(dsn, newDB string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + newDB
	return u.String()
}

func splitHostPortDefault(addr, defPort string) (string, string) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, defPort
	}
	return h, p
}

// --- Fake Plain-TCP SMTP Server ---

type capturedEmail struct {
	From       string
	Recipients []string
	Raw        string
	Subject    string
}

type fakeSMTPServer struct {
	listener net.Listener
	addr     string
	host     string
	port     int

	mu     sync.Mutex
	emails []capturedEmail
	closed chan struct{}
}

func startFakeSMTPServer() (*fakeSMTPServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, errors.New("unexpected smtp addr")
	}
	s := &fakeSMTPServer{
		listener: ln,
		addr:     ln.Addr().String(),
		host:     "127.0.0.1",
		port:     tcpAddr.Port,
		closed:   make(chan struct{}),
	}
	go s.serve()
	return s, nil
}

func (s *fakeSMTPServer) Close() {
	select {
	case <-s.closed:
		return
	default:
		close(s.closed)
		_ = s.listener.Close()
	}
}

func (s *fakeSMTPServer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emails = nil
}

func (s *fakeSMTPServer) Emails() []capturedEmail {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]capturedEmail, len(s.emails))
	copy(out, s.emails)
	return out
}

func (s *fakeSMTPServer) ExtractLatestCode(recipient string) (string, bool) {
	re := regexp.MustCompile(`\b(\d{6})\b`)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.emails) - 1; i >= 0; i-- {
		em := s.emails[i]
		matchRecipient := recipient == ""
		for _, r := range em.Recipients {
			if strings.EqualFold(r, recipient) {
				matchRecipient = true
				break
			}
		}
		if !matchRecipient {
			continue
		}
		if m := re.FindStringSubmatch(em.Raw); len(m) == 2 {
			return m[1], true
		}
	}
	return "", false
}

func (s *fakeSMTPServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
				continue
			}
		}
		go s.handleConn(conn)
	}
}

func (s *fakeSMTPServer) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	writeLine := func(line string) error {
		if _, err := rw.WriteString(line + "\r\n"); err != nil {
			return err
		}
		return rw.Flush()
	}

	if err := writeLine("220 127.0.0.1 ESMTP sub2api-e2e"); err != nil {
		return
	}

	var from string
	var recipients []string
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "EHLO"):
			// Intentionally do NOT advertise STARTTLS so plain-TCP SMTP proceeds.
			if err := writeLine("250-127.0.0.1"); err != nil {
				return
			}
			if err := writeLine("250 AUTH PLAIN LOGIN"); err != nil {
				return
			}
		case strings.HasPrefix(upper, "HELO"):
			if err := writeLine("250 127.0.0.1"); err != nil {
				return
			}
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			if strings.TrimSpace(line) == "AUTH PLAIN" {
				if err := writeLine("334 "); err != nil {
					return
				}
				_, _ = rw.ReadString('\n')
			}
			if err := writeLine("235 2.7.0 Authentication successful"); err != nil {
				return
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			from = strings.Trim(strings.TrimSpace(line[len("MAIL FROM:"):]), "<>")
			if err := writeLine("250 2.1.0 Ok"); err != nil {
				return
			}
		case strings.HasPrefix(upper, "RCPT TO:"):
			rcpt := strings.Trim(strings.TrimSpace(line[len("RCPT TO:"):]), "<>")
			recipients = append(recipients, rcpt)
			if err := writeLine("250 2.1.5 Ok"); err != nil {
				return
			}
		case upper == "DATA":
			if err := writeLine("354 End data with <CR><LF>.<CR><LF>"); err != nil {
				return
			}
			var body strings.Builder
			for {
				dataLine, dErr := rw.ReadString('\n')
				if dErr != nil {
					return
				}
				trimmed := strings.TrimRight(dataLine, "\r\n")
				if trimmed == "." {
					break
				}
				_, _ = body.WriteString(strings.TrimPrefix(trimmed, "."))
				_ = body.WriteByte('\n')
			}
			raw := body.String()
			subject := extractSubject(raw)
			s.mu.Lock()
			s.emails = append(s.emails, capturedEmail{
				From:       from,
				Recipients: append([]string(nil), recipients...),
				Raw:        raw,
				Subject:    subject,
			})
			s.mu.Unlock()
			from = ""
			recipients = nil
			if err := writeLine("250 2.0.0 Ok"); err != nil {
				return
			}
		case upper == "RSET":
			from = ""
			recipients = nil
			if err := writeLine("250 2.0.0 Ok"); err != nil {
				return
			}
		case upper == "QUIT":
			_ = writeLine("221 2.0.0 Bye")
			return
		default:
			if err := writeLine("250 Ok"); err != nil {
				return
			}
		}
	}
}

func extractSubject(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(strings.ToLower(line), "subject:") {
			return strings.TrimSpace(line[len("subject:"):])
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	return ""
}

// --- Mock Anthropic Upstream ---

type mockAnthropicUpstream struct {
	srv *httptest.Server

	mu           sync.Mutex
	inputTokens  int
	outputTokens int
	requests     int
}

func startMockAnthropicUpstream() *mockAnthropicUpstream {
	m := &mockAnthropicUpstream{
		inputTokens:  10000,
		outputTokens: 18000,
	}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()

		m.mu.Lock()
		inTok := m.inputTokens
		outTok := m.outputTokens
		m.requests++
		m.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_e2e_" + randomHex(4),
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-sonnet-4-5",
			"stop_reason": "end_turn",
			"content": []map[string]any{
				{"type": "text", "text": "ok"},
			},
			"usage": map[string]any{
				"input_tokens":  inTok,
				"output_tokens": outTok,
			},
		})
	}))
	return m
}

func (m *mockAnthropicUpstream) URL() string { return m.srv.URL }
func (m *mockAnthropicUpstream) Close()      { m.srv.Close() }
func (m *mockAnthropicUpstream) Reset() {
	m.SetTokens(10000, 18000)
	m.mu.Lock()
	m.requests = 0
	m.mu.Unlock()
}
func (m *mockAnthropicUpstream) SetTokens(inTok, outTok int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputTokens = inTok
	m.outputTokens = outTok
}

// --- Mock OpenAI Upstream ---

type mockOpenAIUpstream struct {
	srv          *httptest.Server
	mu           sync.Mutex
	inputTokens  int
	outputTokens int
}

func startMockOpenAIUpstream() *mockOpenAIUpstream {
	m := &mockOpenAIUpstream{inputTokens: 1000, outputTokens: 500}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()

		m.mu.Lock()
		inTok := m.inputTokens
		outTok := m.outputTokens
		m.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_e2e_" + randomHex(4),
			"object":  "chat.completion",
			"model":   "gpt-4o",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
			"usage": map[string]any{
				"prompt_tokens":     inTok,
				"completion_tokens": outTok,
				"total_tokens":      inTok + outTok,
			},
		})
	}))
	return m
}

func (m *mockOpenAIUpstream) Close() { m.srv.Close() }
func (m *mockOpenAIUpstream) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputTokens = 1000
	m.outputTokens = 500
}

// --- Mock Vertex Batch Prediction & GCS Upstream ---

type vertexMockMode string

const (
	vertexModeStayRunning    vertexMockMode = "running"
	vertexModeAllSuccess     vertexMockMode = "all_success"
	vertexModePartialSuccess vertexMockMode = "partial_success"
)

type mockVertexJob struct {
	Name            string
	DisplayName     string
	OutputPrefixURI string
	InputKeys       []string
	Cancelled       bool
}

type mockVertexBatchUpstream struct {
	srv *httptest.Server

	mu      sync.Mutex
	mode    vertexMockMode
	seq     int
	jobs    map[string]*mockVertexJob
	objects map[string][]byte
}

func startMockVertexBatchUpstream() *mockVertexBatchUpstream {
	m := &mockVertexBatchUpstream{
		mode:    vertexModeAllSuccess,
		jobs:    make(map[string]*mockVertexJob),
		objects: make(map[string][]byte),
	}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	return m
}

func (m *mockVertexBatchUpstream) URL() string { return m.srv.URL }
func (m *mockVertexBatchUpstream) Close()      { m.srv.Close() }
func (m *mockVertexBatchUpstream) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = vertexModeAllSuccess
	m.seq = 0
	m.jobs = make(map[string]*mockVertexJob)
	m.objects = make(map[string][]byte)
}
func (m *mockVertexBatchUpstream) SetMode(mode vertexMockMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = mode
}

func (m *mockVertexBatchUpstream) handle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	// GCS upload: POST /upload/storage/v1/b/{bucket}/o?uploadType=media&name={object}
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/upload/storage/v1/b/"):
		bucket := extractBucketFromGCSPath(path, "/upload/storage/v1/b/")
		objName := r.URL.Query().Get("name")
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		m.mu.Lock()
		m.objects["gs://"+bucket+"/"+objName] = body
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"` + objName + `"}`))

	// GCS list: GET /storage/v1/b/{bucket}/o?prefix={prefix}
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/storage/v1/b/") && strings.HasSuffix(path, "/o"):
		bucket := extractBucketFromGCSPath(path, "/storage/v1/b/")
		prefix := r.URL.Query().Get("prefix")
		fullPrefix := "gs://" + bucket + "/" + prefix
		m.mu.Lock()
		var items []map[string]string
		for uri := range m.objects {
			if strings.HasPrefix(uri, fullPrefix) {
				objName := strings.TrimPrefix(uri, "gs://"+bucket+"/")
				items = append(items, map[string]string{"name": objName})
			}
		}
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})

	// GCS open object: GET /storage/v1/b/{bucket}/o/{object}?alt=media
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/storage/v1/b/"):
		rest := strings.TrimPrefix(path, "/storage/v1/b/")
		parts := strings.SplitN(rest, "/o/", 2)
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		bucket, _ := url.PathUnescape(parts[0])
		objName, _ := url.PathUnescape(parts[1])
		m.mu.Lock()
		data, ok := m.objects["gs://"+bucket+"/"+objName]
		m.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/jsonl")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)

	// GCS delete object: DELETE /storage/v1/b/{bucket}/o/{object}
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/storage/v1/b/"):
		rest := strings.TrimPrefix(path, "/storage/v1/b/")
		parts := strings.SplitN(rest, "/o/", 2)
		if len(parts) == 2 {
			bucket, _ := url.PathUnescape(parts[0])
			objName, _ := url.PathUnescape(parts[1])
			m.mu.Lock()
			delete(m.objects, "gs://"+bucket+"/"+objName)
			m.mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)

	// Vertex CreateBatchPredictionJob: POST /v1/projects/{p}/locations/{l}/batchPredictionJobs
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/batchPredictionJobs"):
		var req struct {
			DisplayName string `json:"displayName"`
			InputConfig struct {
				GCSSource struct {
					URIs []string `json:"uris"`
				} `json:"gcsSource"`
			} `json:"inputConfig"`
			OutputConfig struct {
				GCSDestination struct {
					OutputURIPrefix string `json:"outputUriPrefix"`
				} `json:"gcsDestination"`
			} `json:"outputConfig"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = r.Body.Close()

		m.mu.Lock()
		m.seq++
		locPrefix := strings.TrimPrefix(path, "/v1/")
		jobName := fmt.Sprintf("%s/e2e-job-%d", locPrefix, m.seq)
		var keys []string
		for _, uri := range req.InputConfig.GCSSource.URIs {
			if raw, ok := m.objects[uri]; ok {
				keys = append(keys, extractVertexInputKeys(raw)...)
			}
		}
		job := &mockVertexJob{
			Name:            jobName,
			DisplayName:     req.DisplayName,
			OutputPrefixURI: req.OutputConfig.GCSDestination.OutputURIPrefix,
			InputKeys:       keys,
		}
		m.jobs[jobName] = job
		m.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":        jobName,
			"displayName": req.DisplayName,
			"state":       "JOB_STATE_PENDING",
			"outputConfig": map[string]any{
				"predictionsFormat": "jsonl",
				"gcsDestination": map[string]any{
					"outputUriPrefix": job.OutputPrefixURI,
				},
			},
		})

	// Vertex CancelBatchPredictionJob: POST /v1/{name}:cancel
	case r.Method == http.MethodPost && strings.HasSuffix(path, ":cancel"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/"), ":cancel")
		m.mu.Lock()
		if job, ok := m.jobs[name]; ok {
			job.Cancelled = true
		}
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))

	// Vertex GetBatchPredictionJob: GET /v1/{name}
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/"):
		name := strings.TrimPrefix(path, "/v1/")
		m.mu.Lock()
		job, ok := m.jobs[name]
		mode := m.mode
		if ok && !job.Cancelled && (mode == vertexModeAllSuccess || mode == vertexModePartialSuccess) {
			m.ensureOutputJSONLLocked(job, mode)
		}
		m.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		state := "JOB_STATE_RUNNING"
		if job.Cancelled {
			state = "JOB_STATE_CANCELLED"
		} else if mode == vertexModeAllSuccess || mode == vertexModePartialSuccess {
			state = "JOB_STATE_SUCCEEDED"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":        job.Name,
			"displayName": job.DisplayName,
			"state":       state,
			"outputConfig": map[string]any{
				"predictionsFormat": "jsonl",
				"gcsDestination": map[string]any{
					"outputUriPrefix": job.OutputPrefixURI,
				},
			},
		})

	default:
		http.NotFound(w, r)
	}
}

func (m *mockVertexBatchUpstream) ensureOutputJSONLLocked(job *mockVertexJob, mode vertexMockMode) {
	outURI := strings.TrimRight(job.OutputPrefixURI, "/") + "/predictions.jsonl"
	if _, exists := m.objects[outURI]; exists {
		return
	}
	tinyPNG := base64.StdEncoding.EncodeToString([]byte("fakepngdata"))
	var buf strings.Builder
	for idx, key := range job.InputKeys {
		var line map[string]any
		if mode == vertexModePartialSuccess && idx > 0 {
			line = map[string]any{
				"key": key,
				"status": map[string]any{
					"code":    3,
					"status":  "INVALID_ARGUMENT",
					"message": "mock item failure",
				},
			}
		} else {
			line = map[string]any{
				"key": key,
				"response": map[string]any{
					"candidates": []any{
						map[string]any{
							"content": map[string]any{
								"parts": []any{
									map[string]any{
										"inlineData": map[string]any{
											"mimeType": "image/png",
											"data":     tinyPNG,
										},
									},
								},
							},
						},
					},
				},
			}
		}
		b, _ := json.Marshal(line)
		_, _ = buf.Write(b)
		_ = buf.WriteByte('\n')
	}
	m.objects[outURI] = []byte(buf.String())
}

func extractBucketFromGCSPath(path, prefix string) string {
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	b, _ := url.PathUnescape(parts[0])
	return b
}

func extractVertexInputKeys(raw []byte) []string {
	var keys []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj struct {
			Key string `json:"key"`
		}
		if json.Unmarshal([]byte(line), &obj) == nil && obj.Key != "" {
			keys = append(keys, obj.Key)
		}
	}
	return keys
}

func seedVertexTokenInRedis(ctx context.Context, rdb *redis.Client, clientEmail, privateKeyID, token string) error {
	sum := sha256.Sum256([]byte(clientEmail + "\x00" + privateKeyID))
	fingerprint := hex.EncodeToString(sum[:8])
	key := "oauth:token:vertex:service_account:" + fingerprint
	return rdb.Set(ctx, key, token, time.Hour).Err()
}
