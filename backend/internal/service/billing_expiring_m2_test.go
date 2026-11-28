package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

// ---------- F5: BatchImageBalanceHoldCommand.Normalize() ----------

func TestBatchImageBalanceHoldCommand_NormalizeQuantizesAfterFingerprint(t *testing.T) {
	cmdA := &BatchImageBalanceHoldCommand{
		RequestID:    "  req-hold-1  ",
		APIKeyID:     7,
		UserID:       42,
		BatchID:      "  batch-1  ",
		HoldAmount:   0.000078125, // rounds to 0.00007813
		ActualAmount: 0.000039064, // rounds to 0.00003906
	}
	cmdA.Normalize()

	require.Equal(t, "req-hold-1", cmdA.RequestID)
	require.Equal(t, "batch-1", cmdA.BatchID)
	require.InDelta(t, 0.00007813, cmdA.HoldAmount, 1e-12)
	require.InDelta(t, 0.00003906, cmdA.ActualAmount, 1e-12)
	require.NotEmpty(t, cmdA.RequestFingerprint)

	// Re-normalizing with raw unquantized HoldAmount must produce the exact same fingerprint.
	cmdB := &BatchImageBalanceHoldCommand{
		RequestID:    "req-hold-1",
		APIKeyID:     7,
		UserID:       42,
		BatchID:      "batch-1",
		HoldAmount:   0.000078125,
		ActualAmount: 0.000039064,
	}
	cmdB.Normalize()
	require.Equal(t, cmdA.RequestFingerprint, cmdB.RequestFingerprint)

	// Distinct raw amounts that round to the same 8-decimal value still preserve distinct fingerprints
	// because fingerprint is computed before quantization.
	cmdC := &BatchImageBalanceHoldCommand{
		RequestID:    "req-hold-1",
		APIKeyID:     7,
		UserID:       42,
		BatchID:      "batch-1",
		HoldAmount:   0.000078126,
		ActualAmount: 0.000039064,
	}
	cmdC.Normalize()
	require.InDelta(t, cmdA.HoldAmount, cmdC.HoldAmount, 1e-12)
	require.NotEqual(t, cmdA.RequestFingerprint, cmdC.RequestFingerprint)
}

// ---------- F5: Batch Image Cache Invalidation ----------

type m2RecordingBillingCacheInvalidator struct {
	mu      sync.Mutex
	userIDs []int64
}

func (m *m2RecordingBillingCacheInvalidator) InvalidateUserBalance(_ context.Context, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userIDs = append(m.userIDs, userID)
	return nil
}

func (m *m2RecordingBillingCacheInvalidator) snapshot() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int64, len(m.userIDs))
	copy(out, m.userIDs)
	return out
}

type m2BatchImageBillingStub struct {
	UsageBillingRepository
	reserves int
	captures int
	releases int
}

func (b *m2BatchImageBillingStub) ReserveBatchImageBalance(_ context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	b.reserves++
	return &BatchImageBalanceHoldResult{Applied: true}, nil
}

func (b *m2BatchImageBillingStub) CaptureBatchImageBalance(_ context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	b.captures++
	return &BatchImageBalanceHoldResult{Applied: true}, nil
}

func (b *m2BatchImageBillingStub) ReleaseBatchImageBalance(_ context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error) {
	b.releases++
	return &BatchImageBalanceHoldResult{Applied: true}, nil
}

type m2BatchImageRepoStub struct {
	BatchImageRepository
	mu        sync.Mutex
	jobs      map[string]*BatchImageJob
	staleJobs []*BatchImageJob
}

func newM2BatchImageRepoStub() *m2BatchImageRepoStub {
	return &m2BatchImageRepoStub{jobs: make(map[string]*BatchImageJob)}
}

func (r *m2BatchImageRepoStub) CreateBatchImageJob(_ context.Context, params CreateBatchImageJobParams) (*BatchImageJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job := &BatchImageJob{
		BatchID:       params.BatchID,
		UserID:        params.UserID,
		APIKeyID:      params.APIKeyID,
		AccountID:     params.AccountID,
		Provider:      params.Provider,
		Model:         params.Model,
		Status:        params.Status,
		ItemCount:     params.ItemCount,
		EstimatedCost: params.EstimatedCost,
		HoldAmount:    params.HoldAmount,
		HoldID:        params.HoldID,
		RequestHash:   params.RequestHash,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	r.jobs[job.BatchID] = job
	return job, nil
}

func (r *m2BatchImageRepoStub) GetBatchImageJobByIdempotencyKey(_ context.Context, _ int64, _ int64, _ string) (*BatchImageJob, error) {
	return nil, ErrBatchImageJobNotFound
}

func (r *m2BatchImageRepoStub) GetBatchImageJobByBatchID(_ context.Context, batchID string) (*BatchImageJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[batchID]
	if !ok {
		return nil, ErrBatchImageJobNotFound
	}
	cp := *job
	return &cp, nil
}

func (r *m2BatchImageRepoStub) GetBatchImageJobByBatchIDForOwner(_ context.Context, userID, _ int64, batchID string) (*BatchImageJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[batchID]
	if !ok || job.UserID != userID {
		return nil, ErrBatchImageJobNotFound
	}
	cp := *job
	return &cp, nil
}

func (r *m2BatchImageRepoStub) BulkCreateBatchImageItems(_ context.Context, _ []CreateBatchImageItemParams) error {
	return errors.New("item create failed")
}

func (r *m2BatchImageRepoStub) TransitionBatchImageJobStatus(_ context.Context, batchID, toStatus string, _ BatchImageTransitionOptions) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if job, ok := r.jobs[batchID]; ok {
		job.Status = toStatus
	}
	return nil
}

func (r *m2BatchImageRepoStub) MarkBatchImageJobSettled(_ context.Context, params MarkBatchImageJobSettledParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if job, ok := r.jobs[params.BatchID]; ok {
		job.Status = BatchImageJobStatusCompleted
		cost := params.ActualCost
		job.ActualCost = &cost
	}
	return nil
}

func (r *m2BatchImageRepoStub) SetBatchImageJobSettlementFailed(_ context.Context, _ string, _ string, _ string) (int, error) {
	return 1, nil
}

func (r *m2BatchImageRepoStub) RecordBatchImageJobSubmitFailure(_ context.Context, _ string, _ string, _ string, _ bool) error {
	return nil
}

func (r *m2BatchImageRepoStub) MarkBatchImageJobUserDeleted(_ context.Context, _, _ int64, _ string, _ time.Time) error {
	return nil
}

func (r *m2BatchImageRepoStub) AppendBatchImageEvent(_ context.Context, _, _ string, _ any) error {
	return nil
}

func (r *m2BatchImageRepoStub) ListStaleUnsubmittedBatchImageJobs(_ context.Context, _ time.Time, _ int) ([]*BatchImageJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.staleJobs, nil
}

func (r *m2BatchImageRepoStub) FailStaleUnsubmittedBatchImageJob(_ context.Context, batchID string, _ time.Time, code, msg string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if job, ok := r.jobs[batchID]; ok {
		job.Status = BatchImageJobStatusFailed
		job.LastErrorCode = &code
		job.LastErrorMessage = &msg
		return true, nil
	}
	return false, nil
}

type m2BatchImageAccountRepoStub struct {
	BatchImageAccountSelectionRepository
	accounts []Account
}

func (a *m2BatchImageAccountRepoStub) ListSchedulableByPlatform(_ context.Context, _ string) ([]Account, error) {
	return a.accounts, nil
}

func (a *m2BatchImageAccountRepoStub) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, _ string) ([]Account, error) {
	return a.accounts, nil
}

type m2BatchImagePricingStub struct{}

func (p *m2BatchImagePricingStub) BatchImageUnitPrice(_ context.Context, _ *BatchImageJob) (float64, error) {
	return 0.05, nil
}

func TestBatchImageServices_InvalidateBillingCacheAcrossAllBoundaries(t *testing.T) {
	ctx := context.Background()

	// 1. Typed-nil guard does not panic and leaves BillingCache nil.
	var typedNil *BillingCacheService
	pubSvc := NewBatchImagePublicService(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	pubSvc.SetBillingCacheService(typedNil)
	require.Nil(t, pubSvc.BillingCache)

	// 2. PublicService Cancel (pre-provider and terminal) invalidates BillingCache.
	inv := &m2RecordingBillingCacheInvalidator{}
	repo := newM2BatchImageRepoStub()
	billing := &m2BatchImageBillingStub{}
	apiKeyID := int64(5)
	holdAmt := 1.0
	reqHash := "hash-1"
	repo.jobs["batch-cancel-pre"] = &BatchImageJob{
		BatchID: "batch-cancel-pre", UserID: 101, APIKeyID: &apiKeyID,
		Status: BatchImageJobStatusCreated, HoldAmount: &holdAmt, RequestHash: &reqHash,
	}
	repo.jobs["batch-cancel-term"] = &BatchImageJob{
		BatchID: "batch-cancel-term", UserID: 102, APIKeyID: &apiKeyID,
		Status: BatchImageJobStatusFailed, HoldAmount: &holdAmt, RequestHash: &reqHash,
	}
	pubSvc = &BatchImagePublicService{
		Repo:        repo,
		BillingRepo: billing,
		Config:      &config.Config{BatchImage: config.BatchImageConfig{Enabled: true}},
	}
	pubSvc.SetBillingCacheService(inv)

	_, err := pubSvc.Cancel(ctx, BatchImageOwner{UserID: 101, APIKeyID: apiKeyID}, "batch-cancel-pre")
	require.NoError(t, err)
	_, err = pubSvc.Cancel(ctx, BatchImageOwner{UserID: 102, APIKeyID: apiKeyID}, "batch-cancel-term")
	require.NoError(t, err)
	require.Equal(t, []int64{101, 102}, inv.snapshot())

	// 3. Submit reserve + Submit item-create failure release both invalidate BillingCache.
	invSubmit := &m2RecordingBillingCacheInvalidator{}
	submitRepo := newM2BatchImageRepoStub()
	accountRepo := &m2BatchImageAccountRepoStub{
		accounts: []Account{{
			ID: 9, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			Credentials: map[string]any{"api_key": "k"},
		}},
	}
	pubSubmit := &BatchImagePublicService{
		Repo:             submitRepo,
		AccountRepo:      accountRepo,
		ProviderRegistry: NewBatchImageProviderRegistry(&m2FailingSubmitProvider{}),
		Pricing:          &m2BatchImagePricingStub{},
		BillingRepo:      billing,
		Config:           &config.Config{BatchImage: config.BatchImageConfig{Enabled: true}},
	}
	pubSubmit.SetBillingCacheService(invSubmit)

	_, submitErr := pubSubmit.Submit(ctx, BatchImageOwner{UserID: 103, APIKeyID: apiKeyID}, BatchImageSubmitRequest{
		Model:    "gemini-2.5-flash-image",
		Provider: BatchImageProviderGeminiAPI,
		Items:    []BatchImageSubmitItem{{CustomID: "c1", Prompt: "draw a cat"}},
	}, "")
	require.Error(t, submitErr)
	// Invoked once after Reserve and once after releaseFailedSubmitHold.
	require.Equal(t, []int64{103, 103}, invSubmit.snapshot())

	// 4. WorkerRuntime.SetBillingCacheService propagates to SettlementService, ProviderProcessor, and BillingRecoveryService.
	invRuntime := &m2RecordingBillingCacheInvalidator{}
	rtRepo := newM2BatchImageRepoStub()
	accID := int64(9)
	rtRepo.jobs["batch-settle"] = &BatchImageJob{
		BatchID: "batch-settle", UserID: 201, APIKeyID: &apiKeyID, AccountID: &accID,
		Status: BatchImageJobStatusSettling, ItemCount: 1, SuccessCount: 1, HoldAmount: &holdAmt,
		PricingSnapshotVersion: 1, BillableUnitPrice: 0.5,
	}
	rtRepo.jobs["batch-exhaust"] = &BatchImageJob{
		BatchID: "batch-exhaust", UserID: 202, APIKeyID: &apiKeyID, AccountID: &accID,
		Status: BatchImageJobStatusSettling, RetryCount: batchImageSettlementMaxRetries,
		LastErrorCode: batchImageStringPtr("SETTLEMENT_BILLING_FAILED"), HoldAmount: &holdAmt, RequestHash: &reqHash,
	}
	rtRepo.jobs["batch-proc-term"] = &BatchImageJob{
		BatchID: "batch-proc-term", UserID: 203, APIKeyID: &apiKeyID,
		Status: BatchImageJobStatusCancelled, HoldAmount: &holdAmt, RequestHash: &reqHash,
	}
	staleJob := &BatchImageJob{
		BatchID: "batch-stale", UserID: 204, APIKeyID: &apiKeyID,
		Status: BatchImageJobStatusCreated, HoldAmount: &holdAmt, RequestHash: &reqHash,
	}
	rtRepo.jobs["batch-stale"] = staleJob
	rtRepo.staleJobs = []*BatchImageJob{staleJob}

	rt := ProvideBatchImageWorkerRuntime(rtRepo, nil, nil, billing, nil, nil, nil, &config.Config{})
	rt.SetBillingCacheService(invRuntime)

	pipeline, ok := rt.worker.processor.(*BatchImagePipelineProcessor)
	require.True(t, ok)

	_, err = pipeline.SettlementService.Settle(ctx, "batch-settle")
	require.NoError(t, err)
	_, err = pipeline.SettlementService.Settle(ctx, "batch-exhaust")
	require.ErrorIs(t, err, ErrBatchImageSettlementBillingFailed)
	_, err = pipeline.ProviderProcessor.Process(ctx, "batch-proc-term")
	require.NoError(t, err)
	released, err := rt.billingRecovery.ReleaseStaleUnsubmittedOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, released)

	require.Equal(t, []int64{201, 202, 203, 204}, invRuntime.snapshot())
}

type m2FailingSubmitProvider struct{}

func (p *m2FailingSubmitProvider) Name() string { return BatchImageProviderGeminiAPI }
func (p *m2FailingSubmitProvider) SupportsAccount(_ *Account) bool {
	return true
}
func (p *m2FailingSubmitProvider) Submit(_ context.Context, _ *BatchImageJob, _ *Account, _ BatchImageInput) (*BatchProviderJob, error) {
	return nil, context.DeadlineExceeded
}
func (p *m2FailingSubmitProvider) Get(_ context.Context, _ *BatchImageJob, _ *Account) (*BatchProviderStatus, error) {
	return nil, nil
}
func (p *m2FailingSubmitProvider) Cancel(_ context.Context, _ *BatchImageJob, _ *Account) error {
	return nil
}
func (p *m2FailingSubmitProvider) OpenResult(_ context.Context, _ *BatchImageJob, _ *Account) (io.ReadCloser, string, error) {
	return nil, "", nil
}
func (p *m2FailingSubmitProvider) Cleanup(_ context.Context, _ *BatchImageJob, _ *Account, _ CleanupTarget) error {
	return nil
}

// ---------- F4: BalanceNotifyService Post-Consumption Boundary & No-Email-on-Expiry ----------

type m2NotifySettingRepoStub struct {
	SettingRepository
	values map[string]string
}

func (s *m2NotifySettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}

func (s *m2NotifySettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

type m2ExpiringRepoArchiveStub struct {
	ExpiringBalanceRepository
	archivedUserIDs []int64
}

func (r *m2ExpiringRepoArchiveStub) ArchiveExpiredLotsBatch(_ context.Context, _ int) ([]int64, int, error) {
	return r.archivedUserIDs, len(r.archivedUserIDs), nil
}

func startM2TestSMTPSink(t *testing.T) (host string, port int, sent *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	var count atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = c.Write([]byte("220 localhost ESMTP\r\n"))
				r := bufio.NewReader(c)
				inData := false
				for {
					line, rErr := r.ReadString('\n')
					if rErr != nil {
						return
					}
					if inData {
						if strings.TrimRight(line, "\r\n") == "." {
							inData = false
							count.Add(1)
							_, _ = c.Write([]byte("250 OK\r\n"))
						}
						continue
					}
					upper := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
						_, _ = c.Write([]byte("250-localhost\r\n250 AUTH PLAIN LOGIN\r\n"))
					case strings.HasPrefix(upper, "AUTH"):
						_, _ = c.Write([]byte("235 2.7.0 Authentication successful\r\n"))
					case strings.HasPrefix(upper, "DATA"):
						inData = true
						_, _ = c.Write([]byte("354 End data with .\r\n"))
					case strings.HasPrefix(upper, "QUIT"):
						_, _ = c.Write([]byte("221 Bye\r\n"))
						return
					default:
						_, _ = c.Write([]byte("250 OK\r\n"))
					}
				}
			}(conn)
		}
	}()

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok)
	return "127.0.0.1", tcpAddr.Port, &count
}

func TestBalanceNotify_PostConsumptionTotalBalanceAndNoEmailOnExpiry(t *testing.T) {
	ctx := context.Background()
	smtpHost, smtpPort, sentCount := startM2TestSMTPSink(t)

	settingRepo := &m2NotifySettingRepoStub{
		values: map[string]string{
			SettingKeyBalanceLowNotifyEnabled:   "true",
			SettingKeyBalanceLowNotifyThreshold: "1.00",
			SettingKeySiteName:                  "TestSite",
			SettingKeySMTPHost:                  smtpHost,
			SettingKeySMTPPort:                  strconv.Itoa(smtpPort),
			SettingKeySMTPUsername:              "user",
			SettingKeySMTPPassword:              "pass",
			SettingKeySMTPFrom:                  "noreply@example.com",
			SettingKeySMTPUseTLS:                "false",
		},
	}
	emailSvc := NewEmailService(settingRepo, nil)
	notifySvc := NewBalanceNotifyService(emailSvc, settingRepo, nil)

	user := &User{
		ID:                   1,
		Email:                "notify@example.com",
		Username:             "notify-user",
		Balance:              0, // check-in-only user: regular balance = 0
		BalanceNotifyEnabled: true,
		BalanceNotifyExtraEmails: []NotifyEmailEntry{
			{Email: "alert@example.com", Verified: true, Disabled: false},
		},
	}

	// Step 1: Total available before deduction = $1.20 (expiring), cost = $0.15 => NewBalance = $1.05 (>= $1.00).
	p1 := &postUsageBillingParams{User: user, Cost: &CostBreakdown{ActualCost: 0.15}}
	newBal1 := 1.05
	old1 := resolveOldBalance(p1, &UsageBillingApplyResult{Applied: true, NewBalance: &newBal1})
	require.InDelta(t, 1.20, old1, 1e-8)
	notifySvc.CheckBalanceAfterDeduction(ctx, user, old1, p1.Cost.ActualCost)
	require.Equal(t, int32(0), sentCount.Load())

	// Step 2: Next consumption of $0.10 => NewBalance = $0.95 (< $1.00), old = $1.05 (>= $1.00) -> sends 1 email!
	p2 := &postUsageBillingParams{User: user, Cost: &CostBreakdown{ActualCost: 0.10}}
	newBal2 := 0.95
	old2 := resolveOldBalance(p2, &UsageBillingApplyResult{Applied: true, NewBalance: &newBal2})
	require.InDelta(t, 1.05, old2, 1e-8)
	notifySvc.CheckBalanceAfterDeduction(ctx, user, old2, p2.Cost.ActualCost)
	require.Equal(t, int32(1), sentCount.Load())

	// Step 3: Expiration alone via ExpiringBalanceArchiver only invalidates caches, never sends low-balance emails.
	billingInv := &m2RecordingBillingCacheInvalidator{}
	archiver := NewExpiringBalanceArchiver(
		&m2ExpiringRepoArchiveStub{archivedUserIDs: []int64{1}},
		billingInv,
		nil,
		time.Minute,
		100,
	)
	require.NoError(t, archiver.RunOnce(ctx))
	require.Equal(t, []int64{1}, billingInv.snapshot())
	require.Equal(t, int32(1), sentCount.Load())
}

// ---------- F7: Legacy BillingCache without BillingCacheWithTTL ----------

type m2LegacyBillingCacheStub struct {
	setCalls atomic.Int32
}

func (c *m2LegacyBillingCacheStub) GetUserBalance(context.Context, int64) (float64, error) {
	return 0, context.DeadlineExceeded
}
func (c *m2LegacyBillingCacheStub) SetUserBalance(context.Context, int64, float64) error {
	c.setCalls.Add(1)
	return nil
}
func (c *m2LegacyBillingCacheStub) DeductUserBalance(context.Context, int64, float64) error {
	return nil
}
func (c *m2LegacyBillingCacheStub) InvalidateUserBalance(context.Context, int64) error {
	return nil
}
func (c *m2LegacyBillingCacheStub) GetSubscriptionCache(context.Context, int64, int64) (*SubscriptionCacheData, error) {
	return nil, nil
}
func (c *m2LegacyBillingCacheStub) SetSubscriptionCache(context.Context, int64, int64, *SubscriptionCacheData) error {
	return nil
}
func (c *m2LegacyBillingCacheStub) UpdateSubscriptionUsage(context.Context, int64, int64, float64) error {
	return nil
}
func (c *m2LegacyBillingCacheStub) InvalidateSubscriptionCache(context.Context, int64, int64) error {
	return nil
}
func (c *m2LegacyBillingCacheStub) GetAPIKeyRateLimit(context.Context, int64) (*APIKeyRateLimitCacheData, error) {
	return nil, nil
}
func (c *m2LegacyBillingCacheStub) SetAPIKeyRateLimit(context.Context, int64, *APIKeyRateLimitCacheData) error {
	return nil
}
func (c *m2LegacyBillingCacheStub) UpdateAPIKeyRateLimitUsage(context.Context, int64, float64) error {
	return nil
}
func (c *m2LegacyBillingCacheStub) InvalidateAPIKeyRateLimit(context.Context, int64) error {
	return nil
}
func (c *m2LegacyBillingCacheStub) GetUserPlatformQuotaCache(context.Context, int64, string) (*UserPlatformQuotaCacheEntry, bool, error) {
	return nil, false, nil
}
func (c *m2LegacyBillingCacheStub) SetUserPlatformQuotaCache(context.Context, int64, string, *UserPlatformQuotaCacheEntry, time.Duration) error {
	return nil
}
func (c *m2LegacyBillingCacheStub) DeleteUserPlatformQuotaCache(context.Context, int64, string) error {
	return nil
}
func (c *m2LegacyBillingCacheStub) IncrUserPlatformQuotaUsageCache(context.Context, int64, string, float64, time.Duration, bool) error {
	return nil
}
func (c *m2LegacyBillingCacheStub) PopDirtyUserPlatformQuotaKeys(context.Context, int) ([]UserPlatformQuotaKey, error) {
	return nil, nil
}
func (c *m2LegacyBillingCacheStub) ReaddDirtyUserPlatformQuotaKeys(context.Context, []UserPlatformQuotaKey) error {
	return nil
}
func (c *m2LegacyBillingCacheStub) BatchGetUserPlatformQuotaCache(context.Context, []UserPlatformQuotaKey) ([]*UserPlatformQuotaCacheEntry, error) {
	return nil, nil
}

type m2UserRepoSimpleStub struct {
	UserRepository
	bal float64
}

func (r *m2UserRepoSimpleStub) GetByID(_ context.Context, id int64) (*User, error) {
	return &User{ID: id, Balance: r.bal}, nil
}

type m2ExpiringSummaryStub struct {
	ExpiringBalanceRepository
	summary ExpiringBalanceSummary
}

func (r *m2ExpiringSummaryStub) GetActiveExpiringSummary(_ context.Context, _ int64) (ExpiringBalanceSummary, error) {
	return r.summary, nil
}

func TestBillingCacheService_LegacyCacheSkipsWriteWhenNearestExpiresAtPresent(t *testing.T) {
	ctx := context.Background()
	legacyCache := &m2LegacyBillingCacheStub{}
	expRepo := &m2ExpiringSummaryStub{}
	svc := NewBillingCacheService(legacyCache, &m2UserRepoSimpleStub{bal: 2.0}, nil, nil, nil, nil, &config.Config{}, nil)
	svc.SetExpiringBalanceRepo(expRepo)
	defer svc.Stop()

	// Case 1: nearest_expires_at != nil and cache lacks BillingCacheWithTTL -> SetUserBalance NOT called.
	expAt := time.Now().Add(time.Minute)
	expRepo.summary = ExpiringBalanceSummary{ActiveBalance: 1.5, NearestExpiresAt: &expAt}
	bal, err := svc.GetUserBalance(ctx, 1)
	require.NoError(t, err)
	require.InDelta(t, 3.5, bal, 1e-8)
	time.Sleep(40 * time.Millisecond)
	require.Equal(t, int32(0), legacyCache.setCalls.Load())

	// Case 2: nearest_expires_at == nil -> SetUserBalance IS called.
	expRepo.summary = ExpiringBalanceSummary{}
	bal2, err := svc.GetUserBalance(ctx, 2)
	require.NoError(t, err)
	require.InDelta(t, 2.0, bal2, 1e-8)
	require.Eventually(t, func() bool {
		return legacyCache.setCalls.Load() == 1
	}, 2*time.Second, 10*time.Millisecond)
}

func TestBillingCacheService_SetExpiringBalanceRepo_TypedNilFallback(t *testing.T) {
	ctx := context.Background()
	var typedNilRepo *m2ExpiringSummaryStub

	billingSvc := NewBillingCacheService(nil, &m2UserRepoSimpleStub{bal: 7.25}, nil, nil, nil, nil, &config.Config{}, nil)
	defer billingSvc.Stop()
	billingSvc.SetExpiringBalanceRepo(typedNilRepo)
	require.Nil(t, billingSvc.expiringBalanceRepo)

	bal, err := billingSvc.GetUserBalance(ctx, 1)
	require.NoError(t, err)
	require.InDelta(t, 7.25, bal, 1e-8)

	paySvc := &PaymentService{}
	paySvc.SetExpiringBalanceRepo(typedNilRepo)
	require.Nil(t, paySvc.expiringBalanceRepo)
}

var _ = pagination.PaginationParams{}
