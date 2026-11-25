package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type stubMigrationSettingRepo struct {
	data map[string]string
}

func (r *stubMigrationSettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	v, ok := r.data[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: v}, nil
}

func (r *stubMigrationSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	v, ok := r.data[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}

func (r *stubMigrationSettingRepo) Set(_ context.Context, key, value string) error {
	if r.data == nil {
		r.data = map[string]string{}
	}
	r.data[key] = value
	return nil
}

func (r *stubMigrationSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := r.data[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (r *stubMigrationSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	if r.data == nil {
		r.data = map[string]string{}
	}
	for k, v := range settings {
		r.data[k] = v
	}
	return nil
}

func (r *stubMigrationSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.data))
	for k, v := range r.data {
		out[k] = v
	}
	return out, nil
}

func (r *stubMigrationSettingRepo) Delete(_ context.Context, key string) error {
	delete(r.data, key)
	return nil
}

type stubMigrationEncryptor struct{}

func (stubMigrationEncryptor) Encrypt(plaintext string) (string, error) {
	return "enc:" + plaintext, nil
}

func (stubMigrationEncryptor) Decrypt(ciphertext string) (string, error) {
	if !strings.HasPrefix(ciphertext, "enc:") {
		return "", errors.New("invalid ciphertext")
	}
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}

type stubMigrationUserRepo struct {
	UserRepository
	user        *User
	adjustErr   error
	adjustCalls []float64
}

func (r *stubMigrationUserRepo) GetByID(_ context.Context, id int64) (*User, error) {
	if r.user == nil || r.user.ID != id {
		return nil, ErrUserNotFound
	}
	cp := *r.user
	return &cp, nil
}

func (r *stubMigrationUserRepo) AdjustBalance(_ context.Context, id int64, delta float64) (BalanceChange, error) {
	r.adjustCalls = append(r.adjustCalls, delta)
	if r.adjustErr != nil {
		return BalanceChange{}, r.adjustErr
	}
	oldBal := r.user.Balance
	r.user.Balance = roundUSD6(oldBal + delta)
	return BalanceChange{Old: oldBal, New: r.user.Balance}, nil
}

type stubMigrationRedeemRepo struct {
	RedeemCodeRepository
	created []*RedeemCode
}

func (r *stubMigrationRedeemRepo) Create(_ context.Context, code *RedeemCode) error {
	code.ID = int64(len(r.created) + 100)
	cp := *code
	r.created = append(r.created, &cp)
	return nil
}

type stubMigrationAuthInvalidator struct {
	mu          sync.Mutex
	invalidated []int64
}

func (s *stubMigrationAuthInvalidator) InvalidateAuthCacheByKey(_ context.Context, _ string) {}
func (s *stubMigrationAuthInvalidator) InvalidateAuthCacheByUserID(_ context.Context, userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidated = append(s.invalidated, userID)
}
func (s *stubMigrationAuthInvalidator) InvalidateAuthCacheByGroupID(_ context.Context, _ int64) {}

func (s *stubMigrationAuthInvalidator) snapshot() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, len(s.invalidated))
	copy(out, s.invalidated)
	return out
}

type inMemoryMigrationRepo struct {
	records []*NewAPIMigrationRecord
	nextID  int64
}

func (r *inMemoryMigrationRepo) CreatePending(_ context.Context, record *NewAPIMigrationRecord) error {
	for _, existing := range r.records {
		if existing.Direction == NewAPIMigrationDirectionFrom &&
			existing.SiteURL == record.SiteURL &&
			existing.NewAPIUserID == record.NewAPIUserID &&
			(existing.Status == NewAPIMigrationStatusPending ||
				existing.Status == NewAPIMigrationStatusCompleted ||
				existing.Status == NewAPIMigrationStatusNeedsManualReview) {
			return ErrNewAPIMigrationDuplicate
		}
	}
	r.nextID++
	record.ID = r.nextID
	record.CreatedAt = time.Now().UTC()
	record.UpdatedAt = record.CreatedAt
	cp := *record
	r.records = append(r.records, &cp)
	return nil
}

func (r *inMemoryMigrationRepo) FindBlockingFromMigration(_ context.Context, siteURL string, newAPIUserID int64) (*NewAPIMigrationRecord, error) {
	for i := len(r.records) - 1; i >= 0; i-- {
		rec := r.records[i]
		if rec.Direction == NewAPIMigrationDirectionFrom &&
			rec.SiteURL == siteURL &&
			rec.NewAPIUserID == newAPIUserID &&
			(rec.Status == NewAPIMigrationStatusPending ||
				rec.Status == NewAPIMigrationStatusCompleted ||
				rec.Status == NewAPIMigrationStatusNeedsManualReview) {
			cp := *rec
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *inMemoryMigrationRepo) findByID(id int64) *NewAPIMigrationRecord {
	for _, rec := range r.records {
		if rec.ID == id {
			return rec
		}
	}
	return nil
}

func (r *inMemoryMigrationRepo) MarkCompleted(_ context.Context, id int64, before, delta, after float64, redeemCodeID *int64, completedAt time.Time) error {
	if rec := r.findByID(id); rec != nil {
		rec.Status = NewAPIMigrationStatusCompleted
		rec.Sub2APIBalanceBefore = before
		rec.Sub2APIBalanceDelta = delta
		rec.Sub2APIBalanceAfter = after
		rec.RedeemCodeID = redeemCodeID
		rec.CompletedAt = &completedAt
	}
	return nil
}

func (r *inMemoryMigrationRepo) MarkFailed(_ context.Context, id int64, errMsg string) error {
	if rec := r.findByID(id); rec != nil {
		rec.Status = NewAPIMigrationStatusFailed
		rec.ErrorMessage = errMsg
	}
	return nil
}

func (r *inMemoryMigrationRepo) MarkCompensated(_ context.Context, id int64, errMsg string) error {
	if rec := r.findByID(id); rec != nil {
		rec.Status = NewAPIMigrationStatusCompensated
		rec.ErrorMessage = errMsg
	}
	return nil
}

func (r *inMemoryMigrationRepo) MarkNeedsManualReview(_ context.Context, id int64, errMsg string) error {
	if rec := r.findByID(id); rec != nil {
		rec.Status = NewAPIMigrationStatusNeedsManualReview
		rec.ErrorMessage = errMsg
	}
	return nil
}

func (r *inMemoryMigrationRepo) ListByUser(_ context.Context, userID int64, params pagination.PaginationParams) ([]NewAPIMigrationRecord, *pagination.PaginationResult, error) {
	out := make([]NewAPIMigrationRecord, 0)
	for _, rec := range r.records {
		if rec.Sub2APIUserID == userID {
			out = append(out, *rec)
		}
	}
	return out, &pagination.PaginationResult{Total: int64(len(out)), Page: params.Page, PageSize: params.PageSize, Pages: 1}, nil
}

func (r *inMemoryMigrationRepo) ListAdmin(_ context.Context, params pagination.PaginationParams, _ NewAPIMigrationRecordFilters) ([]NewAPIMigrationRecord, *pagination.PaginationResult, error) {
	out := make([]NewAPIMigrationRecord, 0, len(r.records))
	for _, rec := range r.records {
		out = append(out, *rec)
	}
	return out, &pagination.PaginationResult{Total: int64(len(out)), Page: params.Page, PageSize: params.PageSize, Pages: 1}, nil
}

func (r *inMemoryMigrationRepo) GetStats(_ context.Context) (*NewAPIMigrationStats, error) {
	return &NewAPIMigrationStats{TotalCount: int64(len(r.records))}, nil
}

type fakeNewAPIClient struct {
	status         *NewAPIStatus
	statusErr      error
	selfByToken    map[string]*NewAPIUserInfo
	selfErrByToken map[string]error
	adminUsers     map[int64]*NewAPIUserInfo
	adminUserErr   error
	adminReadErr   error
	adjustHook     func(userID int64, mode string, quota int64) error
	adjustCalls    []struct {
		userID int64
		mode   string
		quota  int64
	}
}

func (f *fakeNewAPIClient) GetStatus(_ context.Context, _ string, _ NewAPIClientOptions) (*NewAPIStatus, error) {
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	if f.status != nil {
		return f.status, nil
	}
	return &NewAPIStatus{SystemName: "NewAPI Demo", Version: "1.0.0", QuotaPerUnit: 500000, DisplayInCurrency: true}, nil
}

func (f *fakeNewAPIClient) GetSelf(_ context.Context, _, token string, _ NewAPIClientOptions) (*NewAPIUserInfo, error) {
	if err, ok := f.selfErrByToken[token]; ok && err != nil {
		return nil, err
	}
	u, ok := f.selfByToken[token]
	if !ok {
		return nil, &NewAPIUpstreamError{StatusCode: http.StatusUnauthorized, Code: "AUTH_UNAUTHORIZED", Message: "invalid access token"}
	}
	cp := *u
	return &cp, nil
}

func (f *fakeNewAPIClient) GetUserByAdmin(_ context.Context, _, _ string, id int64, _ NewAPIClientOptions) (*NewAPIUserInfo, error) {
	if f.adminUserErr != nil {
		return nil, f.adminUserErr
	}
	u, ok := f.adminUsers[id]
	if !ok {
		return nil, &NewAPIUpstreamError{StatusCode: http.StatusNotFound, Message: "user not found"}
	}
	cp := *u
	return &cp, nil
}

func (f *fakeNewAPIClient) CheckAdminUserRead(_ context.Context, _, _ string, _ NewAPIClientOptions) error {
	return f.adminReadErr
}

func (f *fakeNewAPIClient) AdjustUserQuota(_ context.Context, _, _ string, userID int64, mode string, quota int64, _ NewAPIClientOptions) error {
	f.adjustCalls = append(f.adjustCalls, struct {
		userID int64
		mode   string
		quota  int64
	}{userID: userID, mode: mode, quota: quota})
	if f.adjustHook != nil {
		return f.adjustHook(userID, mode, quota)
	}
	if u, ok := f.adminUsers[userID]; ok {
		switch mode {
		case "subtract":
			u.RegularQuota -= quota
		case "add":
			u.RegularQuota += quota
		}
	}
	return nil
}

func newTestMigrationService(t *testing.T) (*NewAPIMigrationService, *inMemoryMigrationRepo, *stubMigrationSettingRepo, *stubMigrationUserRepo, *stubMigrationRedeemRepo, *stubMigrationAuthInvalidator, *fakeNewAPIClient) {
	t.Helper()
	repo := &inMemoryMigrationRepo{}
	settingRepo := &stubMigrationSettingRepo{data: map[string]string{}}
	userRepo := &stubMigrationUserRepo{
		user: &User{ID: 10, Email: "alice@sub2api.test", Balance: 2.5, Status: StatusActive},
	}
	redeemRepo := &stubMigrationRedeemRepo{}
	authInvalidator := &stubMigrationAuthInvalidator{}
	client := &fakeNewAPIClient{
		selfByToken: map[string]*NewAPIUserInfo{
			"nap_admin_root": {ID: 1, Username: "root", Role: NewAPIRoleRoot, Status: NewAPIUserStatusEnabled},
			"nap_user_alice": {
				ID: 42, Username: "alice", DisplayName: "Alice", Email: "alice@newapi.test",
				Role: NewAPIRoleCommon, Status: NewAPIUserStatusEnabled,
				TotalQuota: 6000000, RegularQuota: 5000000, GiftQuota: 1000000, UsedQuota: 100000,
			},
		},
		adminUsers: map[int64]*NewAPIUserInfo{
			42: {
				ID: 42, Username: "alice", DisplayName: "Alice", Email: "alice@newapi.test",
				Role: NewAPIRoleCommon, Status: NewAPIUserStatusEnabled,
				TotalQuota: 5000000, RegularQuota: 5000000, UsedQuota: 100000,
			},
		},
	}
	svc := NewNewAPIMigrationService(
		repo, settingRepo, nil, userRepo, redeemRepo, nil, authInvalidator, stubMigrationEncryptor{}, client, nil,
	)

	_, err := svc.UpdateAdminConfig(context.Background(), &NewAPIMigrationConfig{
		Enabled:                true,
		BaseURL:                "https://newapi.example.com/",
		SiteName:               "My NewAPI",
		AdminAccessToken:       "nap_admin_root",
		QuotaPerUnit:           500000,
		AutoDetectQuotaPerUnit: true,
		ExchangeRate:           1.0,
		MigrateFromEnabled:     true,
		MigrateToEnabled:       true, // must be forced false
	})
	require.NoError(t, err)
	return svc, repo, settingRepo, userRepo, redeemRepo, authInvalidator, client
}

func TestNewAPIMigrationService_ConfigEncryptionAndMasking(t *testing.T) {
	svc, _, settingRepo, _, _, _, _ := newTestMigrationService(t)
	ctx := context.Background()

	// Admin token must be stored encrypted, never plaintext
	rawJSON := settingRepo.data[SettingKeyNewAPIMigrationConfig]
	require.NotContains(t, rawJSON, `"nap_admin_root"`)
	require.Contains(t, rawJSON, `"enc:nap_admin_root"`)
	require.Equal(t, "true", settingRepo.data[SettingKeyNewAPIMigrationEnabled])

	cfg, err := svc.GetAdminConfig(ctx)
	require.NoError(t, err)
	require.True(t, cfg.Enabled)
	require.Equal(t, "https://newapi.example.com", cfg.BaseURL)
	require.Empty(t, cfg.AdminAccessToken)
	require.True(t, cfg.AdminAccessTokenSet)
	require.False(t, cfg.MigrateToEnabled)

	// Saving again with empty AdminAccessToken preserves existing encrypted token
	cfg2, err := svc.UpdateAdminConfig(ctx, &NewAPIMigrationConfig{
		Enabled:                true,
		BaseURL:                "https://newapi.example.com",
		SiteName:               "Renamed Site",
		QuotaPerUnit:           500000,
		AutoDetectQuotaPerUnit: true,
		ExchangeRate:           0.8,
		MigrateFromEnabled:     true,
	})
	require.NoError(t, err)
	require.True(t, cfg2.AdminAccessTokenSet)
	require.Equal(t, 0.8, cfg2.ExchangeRate)
	require.Contains(t, settingRepo.data[SettingKeyNewAPIMigrationConfig], `"enc:nap_admin_root"`)
}

func TestNewAPIMigrationService_TestConnection(t *testing.T) {
	svc, _, _, _, _, _, client := newTestMigrationService(t)
	ctx := context.Background()

	res, err := svc.TestConnection(ctx, nil)
	require.NoError(t, err)
	require.True(t, res.Connected)
	require.True(t, res.IsRoot)
	require.True(t, res.UserReadOK)
	require.Empty(t, res.Warning)

	// Non-root admin warns
	client.selfByToken["nap_admin_only"] = &NewAPIUserInfo{ID: 2, Username: "mod", Role: NewAPIRoleAdmin, Status: NewAPIUserStatusEnabled}
	res2, err := svc.TestConnection(ctx, &NewAPIMigrationTestConnectionInput{AdminAccessToken: "nap_admin_only"})
	require.NoError(t, err)
	require.False(t, res2.IsRoot)
	require.NotEmpty(t, res2.Warning)

	// Common user token rejected
	_, err = svc.TestConnection(ctx, &NewAPIMigrationTestConnectionInput{AdminAccessToken: "nap_user_alice"})
	require.ErrorIs(t, err, ErrNewAPIAdminPermissionDenied)
}

func TestNewAPIMigrationService_PreviewAndExecuteHappyPath(t *testing.T) {
	svc, repo, _, userRepo, redeemRepo, authInvalidator, client := newTestMigrationService(t)
	ctx := context.Background()

	preview, err := svc.PreviewFromNewAPI(ctx, 10, "nap_user_alice")
	require.NoError(t, err)
	require.True(t, preview.CanMigrate)
	require.False(t, preview.AlreadyMigrated)
	require.Equal(t, int64(5000000), preview.RegularQuota)
	require.Equal(t, int64(1000000), preview.GiftQuota)
	require.Equal(t, 10.0, preview.EstimatedCredit)
	require.Equal(t, 12.5, preview.EstimatedNewBalance)

	// Execute one-time full migration
	res, err := svc.ExecuteFromNewAPI(ctx, 10, NewAPIMigrationExecuteInput{
		AccessToken:          "nap_user_alice",
		ExpectedNewAPIUserID: 42,
		ClientIP:             "198.51.100.7",
	})
	require.NoError(t, err)
	require.Equal(t, 12.5, res.NewBalance)
	require.Equal(t, NewAPIMigrationStatusCompleted, res.Record.Status)
	require.Equal(t, 10.0, res.Record.Sub2APIBalanceDelta)
	require.Equal(t, int64(5000000), res.Record.NewAPIQuotaDelta)
	require.Equal(t, int64(0), client.adminUsers[42].RegularQuota)
	require.Equal(t, 12.5, userRepo.user.Balance)
	require.Len(t, redeemRepo.created, 1)
	require.Equal(t, AdjustmentTypeAdminBalance, redeemRepo.created[0].Type)
	require.Equal(t, 10.0, redeemRepo.created[0].Value)
	require.Eventually(t, func() bool {
		ids := authInvalidator.snapshot()
		return len(ids) == 1 && ids[0] == 10
	}, time.Second, 10*time.Millisecond)
	require.Len(t, repo.records, 1)

	// Second preview reports already migrated
	preview2, err := svc.PreviewFromNewAPI(ctx, 10, "nap_user_alice")
	require.NoError(t, err)
	require.False(t, preview2.CanMigrate)
	require.True(t, preview2.AlreadyMigrated)
	require.Equal(t, "NEW_API_USER_ALREADY_MIGRATED", preview2.CannotMigrateReason)

	// Even if New API balance is later topped up, one-time migration lock blocks a second migration!
	client.adminUsers[42].RegularQuota = 500000
	_, err = svc.ExecuteFromNewAPI(ctx, 10, NewAPIMigrationExecuteInput{AccessToken: "nap_user_alice"})
	require.ErrorIs(t, err, ErrNewAPIUserAlreadyMigrated)
	require.Len(t, client.adjustCalls, 1) // no second deduction attempt
}

func TestNewAPIMigrationService_DefinitiveDeductionFailureReleasesLock(t *testing.T) {
	svc, repo, _, userRepo, _, _, client := newTestMigrationService(t)
	ctx := context.Background()

	client.adjustHook = func(_ int64, _ string, _ int64) error {
		return &NewAPIUpstreamError{StatusCode: http.StatusForbidden, Code: "ACCESS_TOKEN_SCOPE_DENIED", Message: "missing user:write"}
	}

	_, err := svc.ExecuteFromNewAPI(ctx, 10, NewAPIMigrationExecuteInput{AccessToken: "nap_user_alice"})
	require.Error(t, err)
	require.Equal(t, 2.5, userRepo.user.Balance) // untouched
	require.Len(t, repo.records, 1)
	require.Equal(t, NewAPIMigrationStatusFailed, repo.records[0].Status)

	// After admin fixes scope, retry succeeds because failed status released unique index
	client.adjustHook = nil
	res, err := svc.ExecuteFromNewAPI(ctx, 10, NewAPIMigrationExecuteInput{AccessToken: "nap_user_alice"})
	require.NoError(t, err)
	require.Equal(t, NewAPIMigrationStatusCompleted, res.Record.Status)
}

func TestNewAPIMigrationService_AmbiguousTimeoutChecksQuotaAndLocksIfChanged(t *testing.T) {
	svc, repo, _, userRepo, _, _, client := newTestMigrationService(t)
	ctx := context.Background()

	// Simulate timeout AFTER New API actually deducted the quota
	client.adjustHook = func(uid int64, mode string, quota int64) error {
		client.adminUsers[uid].RegularQuota -= quota
		return &NewAPIUpstreamError{Message: "read timeout", Ambiguous: true}
	}

	_, err := svc.ExecuteFromNewAPI(ctx, 10, NewAPIMigrationExecuteInput{AccessToken: "nap_user_alice"})
	require.ErrorIs(t, err, ErrNewAPIMigrationNeedsReview)
	require.Equal(t, 2.5, userRepo.user.Balance)
	require.Len(t, repo.records, 1)
	require.Equal(t, NewAPIMigrationStatusNeedsManualReview, repo.records[0].Status)
}

func TestNewAPIMigrationService_LocalCreditFailureTriggersAutomaticCompensation(t *testing.T) {
	svc, repo, _, userRepo, _, _, client := newTestMigrationService(t)
	ctx := context.Background()

	userRepo.adjustErr = errors.New("db write timeout")
	_, err := svc.ExecuteFromNewAPI(ctx, 10, NewAPIMigrationExecuteInput{AccessToken: "nap_user_alice"})
	require.Error(t, err)
	require.Len(t, client.adjustCalls, 2)
	require.Equal(t, "subtract", client.adjustCalls[0].mode)
	require.Equal(t, "add", client.adjustCalls[1].mode)
	require.Equal(t, int64(5000000), client.adminUsers[42].RegularQuota) // restored!
	require.Equal(t, NewAPIMigrationStatusCompensated, repo.records[0].Status)
}

func TestNewAPIMigrationService_CompensationFailureMarksNeedsManualReview(t *testing.T) {
	svc, repo, _, userRepo, _, _, client := newTestMigrationService(t)
	ctx := context.Background()

	userRepo.adjustErr = errors.New("db down")
	client.adjustHook = func(uid int64, mode string, quota int64) error {
		if mode == "subtract" {
			client.adminUsers[uid].RegularQuota -= quota
			return nil
		}
		return &NewAPIUpstreamError{StatusCode: http.StatusInternalServerError, Message: "upstream down"}
	}

	_, err := svc.ExecuteFromNewAPI(ctx, 10, NewAPIMigrationExecuteInput{AccessToken: "nap_user_alice"})
	require.ErrorIs(t, err, ErrNewAPIMigrationNeedsReview)
	require.Equal(t, NewAPIMigrationStatusNeedsManualReview, repo.records[0].Status)
}
