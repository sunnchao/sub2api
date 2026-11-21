package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"go.uber.org/zap"
)

// NewAPIMigrationService 负责与 New API 站点的额度迁移（当前支持一次性全额迁出 New API 常规余额，预留双向扩展）。
type NewAPIMigrationService struct {
	repo                 NewAPIMigrationRepository
	settingRepo          SettingRepository
	settingService       *SettingService
	userRepo             UserRepository
	redeemCodeRepo       RedeemCodeRepository
	billingCacheService  *BillingCacheService
	authCacheInvalidator APIKeyAuthCacheInvalidator
	encryptor            SecretEncryptor
	client               NewAPIClient
	cfg                  *config.Config
}

// NewNewAPIMigrationService 构造 NewAPIMigrationService。
func NewNewAPIMigrationService(
	repo NewAPIMigrationRepository,
	settingRepo SettingRepository,
	settingService *SettingService,
	userRepo UserRepository,
	redeemCodeRepo RedeemCodeRepository,
	billingCacheService *BillingCacheService,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	encryptor SecretEncryptor,
	client NewAPIClient,
	cfg *config.Config,
) *NewAPIMigrationService {
	if client == nil {
		client = NewNewAPIHTTPClient()
	}
	return &NewAPIMigrationService{
		repo:                 repo,
		settingRepo:          settingRepo,
		settingService:       settingService,
		userRepo:             userRepo,
		redeemCodeRepo:       redeemCodeRepo,
		billingCacheService:  billingCacheService,
		authCacheInvalidator: authCacheInvalidator,
		encryptor:            encryptor,
		client:               client,
		cfg:                  cfg,
	}
}

func defaultStoredNewAPIMigrationConfig() storedNewAPIMigrationConfig {
	return storedNewAPIMigrationConfig{
		Enabled:                false,
		QuotaPerUnit:           DefaultNewAPIQuotaPerUnit,
		AutoDetectQuotaPerUnit: true,
		ExchangeRate:           DefaultNewAPIExchangeRate,
		MigrateFromEnabled:     true,
		MigrateToEnabled:       false,
		AllowInsecureHTTP:      false,
	}
}

func (s *NewAPIMigrationService) loadStoredConfig(ctx context.Context) (*storedNewAPIMigrationConfig, error) {
	def := defaultStoredNewAPIMigrationConfig()
	if s.settingRepo == nil {
		return &def, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyNewAPIMigrationConfig)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return &def, nil
		}
		return nil, fmt.Errorf("load new-api migration config: %w", err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return &def, nil
	}
	cfg := defaultStoredNewAPIMigrationConfig()
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("parse new-api migration config: %w", err)
	}
	if cfg.QuotaPerUnit <= 0 {
		cfg.QuotaPerUnit = DefaultNewAPIQuotaPerUnit
	}
	if cfg.ExchangeRate <= 0 {
		cfg.ExchangeRate = DefaultNewAPIExchangeRate
	}
	// 当前版本仅支持从 New API 迁出，反向迁移字段预留并固定关闭。
	cfg.MigrateToEnabled = false
	return &cfg, nil
}

func (s *NewAPIMigrationService) decryptAdminToken(stored *storedNewAPIMigrationConfig) (string, error) {
	if stored == nil || strings.TrimSpace(stored.AdminAccessTokenEncrypted) == "" {
		return "", nil
	}
	if s.encryptor == nil {
		return "", errors.New("secret encryptor is not available")
	}
	plain, err := s.encryptor.Decrypt(stored.AdminAccessTokenEncrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt new-api admin access token: %w", err)
	}
	return normalizeNewAPIBearerToken(plain), nil
}

func (s *NewAPIMigrationService) clientOptions(allowInsecureHTTP bool) NewAPIClientOptions {
	allowPrivate := allowInsecureHTTP
	validateResolvedIP := true
	if s.cfg != nil {
		if s.cfg.Security.URLAllowlist.AllowPrivateHosts {
			allowPrivate = true
		}
		if !s.cfg.Security.URLAllowlist.Enabled && allowInsecureHTTP {
			validateResolvedIP = false
		}
	}
	return NewAPIClientOptions{
		ValidateResolvedIP: validateResolvedIP,
		AllowPrivateHosts:  allowPrivate,
		Timeout:            defaultNewAPIHTTPTimeout,
	}
}

func (s *NewAPIMigrationService) normalizeBaseURL(raw string, allowInsecureHTTP bool) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", infraerrors.BadRequest("INVALID_BASE_URL", "New API base_url is required")
	}
	allowHTTP := allowInsecureHTTP
	allowPrivate := allowInsecureHTTP
	if s.cfg != nil {
		if s.cfg.Security.URLAllowlist.AllowInsecureHTTP {
			allowHTTP = true
		}
		if s.cfg.Security.URLAllowlist.AllowPrivateHosts {
			allowPrivate = true
		}
	}

	var normalized string
	var err error
	if s.cfg != nil && s.cfg.Security.URLAllowlist.Enabled {
		normalized, err = urlvalidator.ValidateHTTPURL(trimmed, allowHTTP, urlvalidator.ValidationOptions{
			AllowPrivate: allowPrivate,
		})
	} else if allowHTTP {
		normalized, err = urlvalidator.ValidateURLFormat(trimmed, true)
	} else {
		normalized, err = urlvalidator.ValidateHTTPURL(trimmed, false, urlvalidator.ValidationOptions{
			AllowPrivate: allowPrivate,
		})
	}
	if err != nil {
		return "", infraerrors.BadRequest("INVALID_BASE_URL", "invalid New API base_url: "+err.Error())
	}

	parsed, parseErr := url.Parse(normalized)
	if parseErr != nil {
		return "", infraerrors.BadRequest("INVALID_BASE_URL", "invalid New API base_url")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

// GetAdminConfig 返回脱敏后的管理员配置（永远不返回明文管理员访问令牌）。
func (s *NewAPIMigrationService) GetAdminConfig(ctx context.Context) (*NewAPIMigrationConfig, error) {
	stored, err := s.loadStoredConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &NewAPIMigrationConfig{
		Enabled:                stored.Enabled,
		BaseURL:                stored.BaseURL,
		SiteName:               stored.SiteName,
		AdminAccessTokenSet:    strings.TrimSpace(stored.AdminAccessTokenEncrypted) != "",
		QuotaPerUnit:           stored.QuotaPerUnit,
		AutoDetectQuotaPerUnit: stored.AutoDetectQuotaPerUnit,
		ExchangeRate:           stored.ExchangeRate,
		MigrateFromEnabled:     stored.MigrateFromEnabled,
		MigrateToEnabled:       false,
		AllowInsecureHTTP:      stored.AllowInsecureHTTP,
	}, nil
}

// UpdateAdminConfig 校验并保存管理员配置，将管理员访问令牌用 AES-256-GCM 加密后持久化。
func (s *NewAPIMigrationService) UpdateAdminConfig(ctx context.Context, input *NewAPIMigrationConfig) (*NewAPIMigrationConfig, error) {
	if input == nil {
		return nil, infraerrors.BadRequest("INVALID_CONFIG", "config payload is required")
	}
	existing, err := s.loadStoredConfig(ctx)
	if err != nil {
		return nil, err
	}

	if math.IsNaN(input.ExchangeRate) || math.IsInf(input.ExchangeRate, 0) || input.ExchangeRate <= 0 {
		return nil, infraerrors.BadRequest("INVALID_EXCHANGE_RATE", "exchange_rate must be a positive number")
	}
	if math.IsNaN(input.QuotaPerUnit) || math.IsInf(input.QuotaPerUnit, 0) || (!input.AutoDetectQuotaPerUnit && input.QuotaPerUnit <= 0) {
		return nil, infraerrors.BadRequest("INVALID_QUOTA_PER_UNIT", "quota_per_unit must be a positive number")
	}

	quotaPerUnit := input.QuotaPerUnit
	if quotaPerUnit <= 0 {
		quotaPerUnit = DefaultNewAPIQuotaPerUnit
	}

	baseURL := strings.TrimSpace(input.BaseURL)
	if baseURL != "" {
		normalized, err := s.normalizeBaseURL(baseURL, input.AllowInsecureHTTP)
		if err != nil {
			return nil, err
		}
		baseURL = normalized
	}

	encryptedToken := existing.AdminAccessTokenEncrypted
	if input.ClearAdminAccessToken {
		encryptedToken = ""
	}
	if rawToken := normalizeNewAPIBearerToken(input.AdminAccessToken); rawToken != "" {
		if s.encryptor == nil {
			return nil, infraerrors.InternalServer("ENCRYPTOR_UNAVAILABLE", "secret encryptor is not available")
		}
		enc, err := s.encryptor.Encrypt(rawToken)
		if err != nil {
			return nil, infraerrors.InternalServer("ENCRYPT_TOKEN_FAILED", "failed to encrypt admin access token").WithCause(err)
		}
		encryptedToken = enc
	}

	if input.Enabled {
		if baseURL == "" {
			return nil, infraerrors.BadRequest("BASE_URL_REQUIRED", "base_url is required when migration is enabled")
		}
		if strings.TrimSpace(encryptedToken) == "" {
			return nil, infraerrors.BadRequest("ADMIN_TOKEN_REQUIRED", "admin_access_token is required when migration is enabled")
		}
	}

	next := storedNewAPIMigrationConfig{
		Enabled:                   input.Enabled,
		BaseURL:                   baseURL,
		SiteName:                  strings.TrimSpace(input.SiteName),
		AdminAccessTokenEncrypted: encryptedToken,
		QuotaPerUnit:              quotaPerUnit,
		AutoDetectQuotaPerUnit:    input.AutoDetectQuotaPerUnit,
		ExchangeRate:              input.ExchangeRate,
		MigrateFromEnabled:        input.MigrateFromEnabled,
		MigrateToEnabled:          false, // 双向扩展预留，当前版本仅支持迁出 New API
		AllowInsecureHTTP:         input.AllowInsecureHTTP,
	}

	encoded, err := json.Marshal(next)
	if err != nil {
		return nil, fmt.Errorf("marshal new-api migration config: %w", err)
	}

	if err := s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyNewAPIMigrationConfig:  string(encoded),
		SettingKeyNewAPIMigrationEnabled: strconv.FormatBool(next.Enabled),
	}); err != nil {
		return nil, fmt.Errorf("save new-api migration config: %w", err)
	}

	if s.settingService != nil {
		s.settingService.NotifySettingsUpdated()
	}

	return &NewAPIMigrationConfig{
		Enabled:                next.Enabled,
		BaseURL:                next.BaseURL,
		SiteName:               next.SiteName,
		AdminAccessTokenSet:    strings.TrimSpace(next.AdminAccessTokenEncrypted) != "",
		QuotaPerUnit:           next.QuotaPerUnit,
		AutoDetectQuotaPerUnit: next.AutoDetectQuotaPerUnit,
		ExchangeRate:           next.ExchangeRate,
		MigrateFromEnabled:     next.MigrateFromEnabled,
		MigrateToEnabled:       false,
		AllowInsecureHTTP:      next.AllowInsecureHTTP,
	}, nil
}

// TestConnection 校验管理员配置的 New API 站点地址与管理员访问令牌是否连通且具备所需权限。
func (s *NewAPIMigrationService) TestConnection(ctx context.Context, input *NewAPIMigrationTestConnectionInput) (*NewAPIMigrationTestConnectionResult, error) {
	stored, err := s.loadStoredConfig(ctx)
	if err != nil {
		return nil, err
	}

	rawURL := stored.BaseURL
	allowInsecure := stored.AllowInsecureHTTP
	adminToken := ""
	if input != nil {
		if strings.TrimSpace(input.BaseURL) != "" {
			rawURL = strings.TrimSpace(input.BaseURL)
		}
		if input.AllowInsecureHTTP != nil {
			allowInsecure = *input.AllowInsecureHTTP
		}
		if t := normalizeNewAPIBearerToken(input.AdminAccessToken); t != "" {
			adminToken = t
		}
	}
	if adminToken == "" {
		decrypted, err := s.decryptAdminToken(stored)
		if err != nil {
			return nil, infraerrors.InternalServer("ADMIN_TOKEN_DECRYPT_FAILED", "failed to decrypt saved admin access token").WithCause(err)
		}
		adminToken = decrypted
	}
	if strings.TrimSpace(rawURL) == "" {
		return nil, infraerrors.BadRequest("BASE_URL_REQUIRED", "base_url is required")
	}
	if adminToken == "" {
		return nil, infraerrors.BadRequest("ADMIN_TOKEN_REQUIRED", "admin_access_token is required")
	}

	baseURL, err := s.normalizeBaseURL(rawURL, allowInsecure)
	if err != nil {
		return nil, err
	}
	opts := s.clientOptions(allowInsecure)

	status, err := s.client.GetStatus(ctx, baseURL, opts)
	if err != nil {
		return nil, mapNewAPIError(err, false)
	}

	res := &NewAPIMigrationTestConnectionResult{
		Connected:         true,
		SiteURL:           baseURL,
		SystemName:        status.SystemName,
		Version:           status.Version,
		QuotaPerUnit:      status.QuotaPerUnit,
		DisplayInCurrency: status.DisplayInCurrency,
	}

	var warnings []string

	selfUser, selfErr := s.client.GetSelf(ctx, baseURL, adminToken, opts)
	if selfErr != nil {
		var upstreamErr *NewAPIUpstreamError
		if errors.As(selfErr, &upstreamErr) && upstreamErr.Code == "ACCESS_TOKEN_SCOPE_DENIED" {
			warnings = append(warnings, "admin token does not include profile:read scope; role could not be inspected directly")
		} else {
			return nil, mapNewAPIError(selfErr, true)
		}
	} else {
		if selfUser.Status != NewAPIUserStatusEnabled {
			return nil, ErrNewAPIUserDisabled
		}
		if selfUser.Role < NewAPIRoleAdmin {
			return nil, ErrNewAPIAdminPermissionDenied
		}
		res.AdminUserID = selfUser.ID
		res.AdminUsername = selfUser.Username
		res.AdminRole = selfUser.Role
		res.IsRoot = selfUser.Role >= NewAPIRoleRoot
		if !res.IsRoot {
			warnings = append(warnings, "configured token belongs to a regular admin (not Root); it can migrate common users, but migrating New API admin accounts requires a Root token")
		}
	}

	if readErr := s.client.CheckAdminUserRead(ctx, baseURL, adminToken, opts); readErr != nil {
		return nil, mapNewAPIError(readErr, true)
	}
	res.UserReadOK = true
	res.Warning = strings.Join(warnings, "; ")
	return res, nil
}

// GetUserInfo 返回面向登录用户的迁移配置摘要（绝不包含管理员访问令牌）。
func (s *NewAPIMigrationService) GetUserInfo(ctx context.Context) (*NewAPIMigrationUserInfo, error) {
	stored, err := s.loadStoredConfig(ctx)
	if err != nil {
		return nil, err
	}
	quotaPerUnit := stored.QuotaPerUnit
	siteName := stored.SiteName
	if stored.Enabled && stored.BaseURL != "" && (stored.AutoDetectQuotaPerUnit || siteName == "") {
		opts := s.clientOptions(stored.AllowInsecureHTTP)
		opts.Timeout = 5 * time.Second
		if status, err := s.client.GetStatus(ctx, stored.BaseURL, opts); err == nil && status != nil {
			if stored.AutoDetectQuotaPerUnit && status.QuotaPerUnit > 0 {
				quotaPerUnit = status.QuotaPerUnit
			}
			if siteName == "" && status.SystemName != "" {
				siteName = status.SystemName
			}
		}
	}
	if siteName == "" {
		siteName = "New API"
	}
	return &NewAPIMigrationUserInfo{
		Enabled:            stored.Enabled,
		MigrateFromEnabled: stored.Enabled && stored.MigrateFromEnabled,
		MigrateToEnabled:   false,
		SiteName:           siteName,
		SiteURL:            stored.BaseURL,
		QuotaPerUnit:       quotaPerUnit,
		ExchangeRate:       stored.ExchangeRate,
	}, nil
}

func (s *NewAPIMigrationService) loadActiveFromConfig(ctx context.Context) (*storedNewAPIMigrationConfig, string, error) {
	stored, err := s.loadStoredConfig(ctx)
	if err != nil {
		return nil, "", err
	}
	if !stored.Enabled {
		return nil, "", ErrNewAPIMigrationDisabled
	}
	if !stored.MigrateFromEnabled {
		return nil, "", ErrNewAPIMigrationDirectionClosed
	}
	if strings.TrimSpace(stored.BaseURL) == "" {
		return nil, "", ErrNewAPIMigrationNotConfigured
	}
	adminToken, err := s.decryptAdminToken(stored)
	if err != nil {
		logger.L().Error("failed to decrypt new-api admin access token", zap.Error(err))
		return nil, "", ErrNewAPIMigrationNotConfigured
	}
	if adminToken == "" {
		return nil, "", ErrNewAPIMigrationNotConfigured
	}
	return stored, adminToken, nil
}

func (s *NewAPIMigrationService) resolveQuotaPerUnitAndSiteName(
	ctx context.Context,
	stored *storedNewAPIMigrationConfig,
	opts NewAPIClientOptions,
) (float64, string) {
	quotaPerUnit := stored.QuotaPerUnit
	if quotaPerUnit <= 0 {
		quotaPerUnit = DefaultNewAPIQuotaPerUnit
	}
	siteName := stored.SiteName
	if stored.AutoDetectQuotaPerUnit || siteName == "" {
		if status, err := s.client.GetStatus(ctx, stored.BaseURL, opts); err == nil && status != nil {
			if stored.AutoDetectQuotaPerUnit && status.QuotaPerUnit > 0 {
				quotaPerUnit = status.QuotaPerUnit
			}
			if siteName == "" && status.SystemName != "" {
				siteName = status.SystemName
			}
		}
	}
	if siteName == "" {
		siteName = "New API"
	}
	return quotaPerUnit, siteName
}

func roundUSD6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// PreviewFromNewAPI 使用用户提交的 New API 个人访问令牌验证归属并查询可迁移常规余额（只读，不扣款）。
func (s *NewAPIMigrationService) PreviewFromNewAPI(
	ctx context.Context,
	sub2apiUserID int64,
	userAccessToken string,
) (*NewAPIMigrationPreviewResult, error) {
	token := normalizeNewAPIBearerToken(userAccessToken)
	if token == "" {
		return nil, ErrNewAPITokenRequired
	}

	stored, adminToken, err := s.loadActiveFromConfig(ctx)
	if err != nil {
		return nil, err
	}

	subUser, err := s.userRepo.GetByID(ctx, sub2apiUserID)
	if err != nil {
		return nil, err
	}

	opts := s.clientOptions(stored.AllowInsecureHTTP)

	// 1. 使用用户自己的访问令牌调用 New API /api/user/self 证明账号归属。
	selfUser, err := s.client.GetSelf(ctx, stored.BaseURL, token, opts)
	if err != nil {
		return nil, mapNewAPIError(err, false)
	}
	if selfUser.Status != NewAPIUserStatusEnabled {
		return nil, ErrNewAPIUserDisabled
	}

	// 2. 使用管理员访问令牌校验是否有权操作该用户，并读取权威的 users.quota（常规余额）。
	adminUser, err := s.client.GetUserByAdmin(ctx, stored.BaseURL, adminToken, selfUser.ID, opts)
	if err != nil {
		return nil, mapNewAPIError(err, true)
	}
	if adminUser.Status != NewAPIUserStatusEnabled {
		return nil, ErrNewAPIUserDisabled
	}

	quotaPerUnit, siteName := s.resolveQuotaPerUnitAndSiteName(ctx, stored, opts)

	regularQuota := adminUser.RegularQuota
	if regularQuota < 0 {
		regularQuota = 0
	}
	giftQuota := selfUser.GiftQuota
	if giftQuota < 0 {
		giftQuota = 0
	}
	totalQuota := regularQuota + giftQuota
	rawUSD := roundUSD6(float64(regularQuota) / quotaPerUnit)
	estimatedCredit := roundUSD6((float64(regularQuota) / quotaPerUnit) * stored.ExchangeRate)

	blocking, err := s.repo.FindBlockingFromMigration(ctx, stored.BaseURL, selfUser.ID)
	if err != nil {
		return nil, fmt.Errorf("check existing migration record: %w", err)
	}

	alreadyMigrated := false
	canMigrate := true
	cannotReason := ""
	if blocking != nil {
		canMigrate = false
		switch blocking.Status {
		case NewAPIMigrationStatusCompleted:
			alreadyMigrated = true
			cannotReason = "NEW_API_USER_ALREADY_MIGRATED"
		case NewAPIMigrationStatusPending:
			cannotReason = "NEW_API_MIGRATION_IN_PROGRESS"
		default:
			cannotReason = "NEW_API_MIGRATION_NEEDS_REVIEW"
		}
	} else if regularQuota <= 0 || estimatedCredit <= 0 {
		canMigrate = false
		cannotReason = "NEW_API_QUOTA_EMPTY"
	}

	return &NewAPIMigrationPreviewResult{
		SiteURL:             stored.BaseURL,
		SiteName:            siteName,
		NewAPIUserID:        selfUser.ID,
		NewAPIUsername:      firstNonEmpty(adminUser.Username, selfUser.Username),
		NewAPIDisplayName:   firstNonEmpty(adminUser.DisplayName, selfUser.DisplayName),
		NewAPIEmail:         firstNonEmpty(adminUser.Email, selfUser.Email),
		NewAPIRole:          adminUser.Role,
		NewAPIStatus:        adminUser.Status,
		RegularQuota:        regularQuota,
		GiftQuota:           giftQuota,
		TotalQuota:          totalQuota,
		UsedQuota:           adminUser.UsedQuota,
		QuotaPerUnit:        quotaPerUnit,
		ExchangeRate:        stored.ExchangeRate,
		RawUSD:              rawUSD,
		EstimatedCredit:     estimatedCredit,
		CurrentBalance:      subUser.Balance,
		EstimatedNewBalance: roundUSD6(subUser.Balance + estimatedCredit),
		AlreadyMigrated:     alreadyMigrated,
		CanMigrate:          canMigrate,
		CannotMigrateReason: cannotReason,
	}, nil
}

// ExecuteFromNewAPI 执行一次性全额迁出：
// 1. 校验用户 New API 访问令牌归属；
// 2. 用管理员令牌查询权威 regular_quota；
// 3. 插入 pending 记录占位（依赖数据库部分唯一索引防并发与防重迁）；
// 4. 调用 New API POST /api/user/manage (mode="subtract") 扣减对应 regular_quota；
// 5. 原子增加当前 Sub2API 用户余额并写余额流水与完成状态；若入账失败则自动向 New API 发起补偿加回。
func (s *NewAPIMigrationService) ExecuteFromNewAPI(
	ctx context.Context,
	sub2apiUserID int64,
	input NewAPIMigrationExecuteInput,
) (*NewAPIMigrationExecuteResult, error) {
	token := normalizeNewAPIBearerToken(input.AccessToken)
	if token == "" {
		return nil, ErrNewAPITokenRequired
	}

	stored, adminToken, err := s.loadActiveFromConfig(ctx)
	if err != nil {
		return nil, err
	}

	subUser, err := s.userRepo.GetByID(ctx, sub2apiUserID)
	if err != nil {
		return nil, err
	}
	if !subUser.IsActive() {
		return nil, ErrUserNotActive
	}

	opts := s.clientOptions(stored.AllowInsecureHTTP)

	// 1. 用用户个人访问令牌验证归属。
	selfUser, err := s.client.GetSelf(ctx, stored.BaseURL, token, opts)
	if err != nil {
		return nil, mapNewAPIError(err, false)
	}
	if selfUser.Status != NewAPIUserStatusEnabled {
		return nil, ErrNewAPIUserDisabled
	}
	if input.ExpectedNewAPIUserID > 0 && selfUser.ID != input.ExpectedNewAPIUserID {
		return nil, ErrNewAPIUserMismatch
	}

	// 2. 用管理员访问令牌读取权威常规余额。
	adminUser, err := s.client.GetUserByAdmin(ctx, stored.BaseURL, adminToken, selfUser.ID, opts)
	if err != nil {
		return nil, mapNewAPIError(err, true)
	}
	if adminUser.Status != NewAPIUserStatusEnabled {
		return nil, ErrNewAPIUserDisabled
	}

	regularQuota := adminUser.RegularQuota
	if regularQuota <= 0 {
		return nil, ErrNewAPIQuotaEmpty
	}

	quotaPerUnit, siteName := s.resolveQuotaPerUnitAndSiteName(ctx, stored, opts)
	creditAmount := roundUSD6((float64(regularQuota) / quotaPerUnit) * stored.ExchangeRate)
	if creditAmount <= 0 {
		return nil, ErrNewAPIQuotaEmpty
	}

	giftQuota := selfUser.GiftQuota
	if giftQuota < 0 {
		giftQuota = 0
	}

	// 3. 插入 pending 记录以数据库部分唯一索引抢占一次性迁移锁。
	record := &NewAPIMigrationRecord{
		Direction:            NewAPIMigrationDirectionFrom,
		Status:               NewAPIMigrationStatusPending,
		SiteURL:              stored.BaseURL,
		Sub2APIUserID:        subUser.ID,
		Sub2APIUserEmail:     subUser.Email,
		NewAPIUserID:         selfUser.ID,
		NewAPIUsername:       firstNonEmpty(adminUser.Username, selfUser.Username),
		NewAPIDisplayName:    firstNonEmpty(adminUser.DisplayName, selfUser.DisplayName),
		NewAPIEmail:          firstNonEmpty(adminUser.Email, selfUser.Email),
		NewAPIQuotaBefore:    regularQuota,
		NewAPIQuotaDelta:     regularQuota,
		NewAPIQuotaAfter:     0,
		NewAPIGiftQuota:      giftQuota,
		QuotaPerUnit:         quotaPerUnit,
		ExchangeRate:         stored.ExchangeRate,
		Sub2APIBalanceBefore: subUser.Balance,
		Sub2APIBalanceDelta:  creditAmount,
		Sub2APIBalanceAfter:  roundUSD6(subUser.Balance + creditAmount),
		ClientIP:             strings.TrimSpace(input.ClientIP),
	}

	if err := s.repo.CreatePending(ctx, record); err != nil {
		if errors.Is(err, ErrNewAPIMigrationDuplicate) {
			return nil, s.classifyDuplicateMigrationError(ctx, stored.BaseURL, selfUser.ID)
		}
		return nil, fmt.Errorf("create pending migration record: %w", err)
	}

	// 4. 调用 New API 管理接口原子扣减对应 regular_quota。
	deductErr := s.client.AdjustUserQuota(ctx, stored.BaseURL, adminToken, selfUser.ID, "subtract", regularQuota, opts)
	if deductErr != nil {
		return nil, s.handleDeductionError(ctx, stored, adminToken, opts, record, regularQuota, deductErr)
	}

	// 5. 扣减已成功，以下本地入账步骤脱离客户端断连取消，避免中途断网导致双边不一致。
	finalizeCtx, cancelFinalize := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancelFinalize()

	change, balErr := s.userRepo.AdjustBalance(finalizeCtx, subUser.ID, creditAmount)
	if balErr != nil {
		// 本地入账失败，立即自动向 New API 补偿加回已扣减的 regular_quota。
		compErr := s.client.AdjustUserQuota(finalizeCtx, stored.BaseURL, adminToken, selfUser.ID, "add", regularQuota, opts)
		if compErr == nil {
			msg := fmt.Sprintf("local balance credit failed (%v); New API quota (%d) has been compensated", balErr, regularQuota)
			_ = s.repo.MarkCompensated(finalizeCtx, record.ID, msg)
			logger.L().Warn("new-api migration rolled back cleanly after local balance credit failure",
				zap.Int64("record_id", record.ID),
				zap.Int64("sub2api_user_id", subUser.ID),
				zap.Int64("new_api_user_id", selfUser.ID),
				zap.Int64("quota", regularQuota),
				zap.Error(balErr),
			)
			return nil, infraerrors.InternalServer("NEW_API_MIGRATION_COMPENSATED",
				"failed to credit local balance; your New API balance has been automatically restored, please try again later")
		}

		msg := fmt.Sprintf("CRITICAL: local balance credit failed (%v) AND New API compensation failed (%v); manual review required", balErr, compErr)
		_ = s.repo.MarkNeedsManualReview(finalizeCtx, record.ID, msg)
		logger.L().Error("new-api migration requires manual review: credit and compensation both failed",
			zap.Int64("record_id", record.ID),
			zap.Int64("sub2api_user_id", subUser.ID),
			zap.Int64("new_api_user_id", selfUser.ID),
			zap.Int64("quota", regularQuota),
			zap.Float64("credit_amount", creditAmount),
			zap.Error(balErr),
			zap.NamedError("compensation_error", compErr),
		)
		return nil, ErrNewAPIMigrationNeedsReview
	}

	// 6. 写入余额变更流水（redeem_codes），使其在用户与管理员余额记录中清晰可查。
	now := time.Now().UTC()
	var redeemCodeID *int64
	if s.redeemCodeRepo != nil {
		if code, codeErr := GenerateRedeemCode(); codeErr == nil {
			notes := fmt.Sprintf("New API 额度迁移 [%s] (New API UID #%d / %s)", siteName, selfUser.ID, record.NewAPIUsername)
			adj := &RedeemCode{
				Code:   code,
				Type:   AdjustmentTypeAdminBalance,
				Value:  creditAmount,
				Status: StatusUsed,
				UsedBy: &subUser.ID,
				UsedAt: &now,
				Notes:  notes,
			}
			if createErr := s.redeemCodeRepo.Create(finalizeCtx, adj); createErr != nil {
				logger.L().Warn("new-api migration: failed to create redeem history record",
					zap.Int64("record_id", record.ID),
					zap.Error(createErr),
				)
			} else if adj.ID > 0 {
				id := adj.ID
				redeemCodeID = &id
			}
		}
	}

	// 7. 更新迁移记录为 completed。
	if markErr := s.repo.MarkCompleted(finalizeCtx, record.ID, change.Old, creditAmount, change.New, redeemCodeID, now); markErr != nil {
		logger.L().Error("new-api migration: credited balance but failed to mark migration record completed",
			zap.Int64("record_id", record.ID),
			zap.Int64("sub2api_user_id", subUser.ID),
			zap.Float64("credit_amount", creditAmount),
			zap.Error(markErr),
		)
	}

	// 8. 异步清理余额和鉴权缓存，避免多 API Key 逐个删 Redis 缓存阻塞迁移响应导致前端超时。
	if s.authCacheInvalidator != nil {
		go func(uid int64) {
			cacheCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s.authCacheInvalidator.InvalidateAuthCacheByUserID(cacheCtx, uid)
		}(subUser.ID)
	}
	if s.billingCacheService != nil {
		go func(uid int64) {
			cacheCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.billingCacheService.InvalidateUserBalance(cacheCtx, uid); err != nil {
				logger.LegacyPrintf("service.new_api_migration", "invalidate user balance cache failed: user_id=%d err=%v", uid, err)
			}
		}(subUser.ID)
	}

	record.Status = NewAPIMigrationStatusCompleted
	record.Sub2APIBalanceBefore = change.Old
	record.Sub2APIBalanceDelta = creditAmount
	record.Sub2APIBalanceAfter = change.New
	record.RedeemCodeID = redeemCodeID
	record.CompletedAt = &now

	return &NewAPIMigrationExecuteResult{
		Record:     record,
		NewBalance: change.New,
	}, nil
}

func (s *NewAPIMigrationService) classifyDuplicateMigrationError(ctx context.Context, siteURL string, newAPIUserID int64) error {
	existing, err := s.repo.FindBlockingFromMigration(ctx, siteURL, newAPIUserID)
	if err != nil || existing == nil {
		return ErrNewAPIUserAlreadyMigrated
	}
	switch existing.Status {
	case NewAPIMigrationStatusPending:
		return ErrNewAPIMigrationInProgress
	case NewAPIMigrationStatusNeedsManualReview:
		return ErrNewAPIMigrationNeedsReview
	default:
		return ErrNewAPIUserAlreadyMigrated
	}
}

func (s *NewAPIMigrationService) handleDeductionError(
	ctx context.Context,
	stored *storedNewAPIMigrationConfig,
	adminToken string,
	opts NewAPIClientOptions,
	record *NewAPIMigrationRecord,
	expectedQuota int64,
	deductErr error,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	// 明确拒绝（HTTP 4xx/5xx、业务返回 success=false 或连接前失败）：New API 未发生扣款，安全释放唯一锁。
	if !IsNewAPIAmbiguousError(deductErr) {
		_ = s.repo.MarkFailed(cleanupCtx, record.ID, deductErr.Error())
		return mapNewAPIError(deductErr, true)
	}

	// 不确定的超时/EOF：重新查询一次 New API 侧余额确认是否真的发生了扣减。
	probeOpts := opts
	probeOpts.Timeout = 5 * time.Second
	refetched, probeErr := s.client.GetUserByAdmin(cleanupCtx, stored.BaseURL, adminToken, record.NewAPIUserID, probeOpts)
	if probeErr == nil && refetched != nil && refetched.RegularQuota == expectedQuota {
		_ = s.repo.MarkFailed(cleanupCtx, record.ID, fmt.Sprintf("deduction timed out and verified unchanged on New API: %v", deductErr))
		return ErrNewAPIUpstreamUnavailable.WithCause(deductErr)
	}

	// 余额已变化或探测仍不可达：锁定记录为 needs_manual_review，避免用户重试引发重复扣款。
	msg := fmt.Sprintf("ambiguous deduction timeout (%v); verification probe=%v; locked for manual review", deductErr, probeErr)
	if refetched != nil {
		msg = fmt.Sprintf("ambiguous deduction timeout (%v); post-check quota changed from %d to %d; locked for manual review",
			deductErr, expectedQuota, refetched.RegularQuota)
	}
	_ = s.repo.MarkNeedsManualReview(cleanupCtx, record.ID, msg)
	logger.L().Error("new-api migration deduction outcome ambiguous; marked needs_manual_review",
		zap.Int64("record_id", record.ID),
		zap.Int64("new_api_user_id", record.NewAPIUserID),
		zap.String("detail", msg),
	)
	return ErrNewAPIMigrationNeedsReview
}

// ListUserRecords 分页查询当前登录用户的迁移记录。
func (s *NewAPIMigrationService) ListUserRecords(
	ctx context.Context,
	userID int64,
	params pagination.PaginationParams,
) ([]NewAPIMigrationRecord, *pagination.PaginationResult, error) {
	return s.repo.ListByUser(ctx, userID, params)
}

// ListAdminRecords 分页查询全部用户的迁移审计记录。
func (s *NewAPIMigrationService) ListAdminRecords(
	ctx context.Context,
	params pagination.PaginationParams,
	filters NewAPIMigrationRecordFilters,
) ([]NewAPIMigrationRecord, *pagination.PaginationResult, error) {
	return s.repo.ListAdmin(ctx, params, filters)
}

// GetAdminStats 获取迁移审计汇总统计。
func (s *NewAPIMigrationService) GetAdminStats(ctx context.Context) (*NewAPIMigrationStats, error) {
	return s.repo.GetStats(ctx)
}

func mapNewAPIError(err error, isAdminCall bool) error {
	if err == nil {
		return nil
	}
	var upstreamErr *NewAPIUpstreamError
	if !errors.As(err, &upstreamErr) {
		return ErrNewAPIUpstreamUnavailable.WithCause(err)
	}

	code := strings.ToUpper(strings.TrimSpace(upstreamErr.Code))
	msgLower := strings.ToLower(strings.TrimSpace(upstreamErr.Message))

	switch code {
	case "ACCESS_TOKEN_EXPIRED", "AUTH_TOKEN_EXPIRED":
		if isAdminCall {
			return infraerrors.ServiceUnavailable("NEW_API_ADMIN_TOKEN_INVALID", "configured New API admin access token has expired").WithCause(err)
		}
		return ErrNewAPITokenExpired.WithCause(err)
	case "ACCESS_TOKEN_LEGACY_RETIRED", "AUTH_UNAUTHORIZED", "AUTH_SESSION_REVOKED", "AUTH_USER_INVALID":
		if isAdminCall {
			return infraerrors.ServiceUnavailable("NEW_API_ADMIN_TOKEN_INVALID", "configured New API admin access token is invalid").WithCause(err)
		}
		return ErrNewAPITokenInvalid.WithCause(err)
	case "AUTH_USER_DISABLED":
		return ErrNewAPIUserDisabled.WithCause(err)
	case "ACCESS_TOKEN_SCOPE_DENIED", "AUTH_INSUFFICIENT_PRIVILEGE", "AUTH_SESSION_REQUIRED":
		if isAdminCall {
			return infraerrors.Forbidden("NEW_API_ADMIN_PERMISSION_DENIED",
				"New API admin access token lacks required permission: "+upstreamErr.Message).WithCause(err)
		}
		return infraerrors.Forbidden("NEW_API_TOKEN_SCOPE_DENIED",
			"New API access token lacks required permission (profile:read): "+upstreamErr.Message).WithCause(err)
	}

	if upstreamErr.StatusCode == http.StatusUnauthorized {
		if isAdminCall {
			return infraerrors.ServiceUnavailable("NEW_API_ADMIN_TOKEN_INVALID", "configured New API admin access token is invalid or expired").WithCause(err)
		}
		return ErrNewAPITokenInvalid.WithCause(err)
	}
	if upstreamErr.StatusCode == http.StatusForbidden {
		if isAdminCall {
			return infraerrors.Forbidden("NEW_API_ADMIN_PERMISSION_DENIED", upstreamErr.Message).WithCause(err)
		}
		return ErrNewAPITokenScopeDenied.WithCause(err)
	}
	if isAdminCall && (strings.Contains(msgLower, "same or higher") || strings.Contains(msgLower, "no permission")) {
		return ErrNewAPIAdminPermissionDenied.WithCause(err)
	}

	return infraerrors.ServiceUnavailable("NEW_API_UPSTREAM_UNAVAILABLE", upstreamErr.Message).WithCause(err)
}
