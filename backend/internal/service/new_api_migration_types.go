package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// New API 额度迁移方向（预留双向扩展）
const (
	NewAPIMigrationDirectionFrom = "from_new_api" // 从 New API 迁入到当前站点
	NewAPIMigrationDirectionTo   = "to_new_api"   // 从当前站点迁出到 New API（预留）
)

// New API 额度迁移记录状态
const (
	NewAPIMigrationStatusPending           = "pending"
	NewAPIMigrationStatusCompleted         = "completed"
	NewAPIMigrationStatusFailed            = "failed"
	NewAPIMigrationStatusCompensated       = "compensated"
	NewAPIMigrationStatusNeedsManualReview = "needs_manual_review"
)

// New API 角色常量（对应 New API common.Role*）
const (
	NewAPIRoleCommon = 1
	NewAPIRoleAdmin  = 10
	NewAPIRoleRoot   = 100

	NewAPIUserStatusEnabled = 1

	DefaultNewAPIQuotaPerUnit = 500000.0
	DefaultNewAPIExchangeRate = 1.0
)

// New API 迁移业务错误定义
var (
	ErrNewAPIMigrationDisabled        = infraerrors.Forbidden("NEW_API_MIGRATION_DISABLED", "New API balance migration is not enabled")
	ErrNewAPIMigrationDirectionClosed = infraerrors.BadRequest("NEW_API_MIGRATION_DIRECTION_DISABLED", "requested migration direction is not enabled")
	ErrNewAPIMigrationNotConfigured   = infraerrors.ServiceUnavailable("NEW_API_MIGRATION_NOT_CONFIGURED", "New API migration is not properly configured by administrator")
	ErrNewAPITokenRequired            = infraerrors.BadRequest("NEW_API_TOKEN_REQUIRED", "New API access token is required")
	ErrNewAPITokenInvalid             = infraerrors.Unauthorized("NEW_API_TOKEN_INVALID", "invalid or expired New API access token")
	ErrNewAPITokenExpired             = infraerrors.Unauthorized("NEW_API_TOKEN_EXPIRED", "New API access token has expired")
	ErrNewAPITokenScopeDenied         = infraerrors.Forbidden("NEW_API_TOKEN_SCOPE_DENIED", "New API access token lacks required permission scope")
	ErrNewAPIUserDisabled             = infraerrors.Forbidden("NEW_API_USER_DISABLED", "New API account is disabled")
	ErrNewAPIAdminPermissionDenied    = infraerrors.Forbidden("NEW_API_ADMIN_PERMISSION_DENIED", "configured New API admin access token does not have permission to manage this user")
	ErrNewAPIQuotaEmpty               = infraerrors.BadRequest("NEW_API_QUOTA_EMPTY", "no migratable regular balance available on New API")
	ErrNewAPIUserAlreadyMigrated      = infraerrors.Conflict("NEW_API_USER_ALREADY_MIGRATED", "this New API account has already been migrated")
	ErrNewAPIMigrationInProgress      = infraerrors.Conflict("NEW_API_MIGRATION_IN_PROGRESS", "a migration for this New API account is currently in progress")
	ErrNewAPIMigrationNeedsReview     = infraerrors.Conflict("NEW_API_MIGRATION_NEEDS_REVIEW", "previous migration for this New API account requires administrator review")
	ErrNewAPIMigrationDuplicate       = infraerrors.Conflict("NEW_API_MIGRATION_DUPLICATE", "duplicate migration lock conflict")
	ErrNewAPIUpstreamUnavailable      = infraerrors.ServiceUnavailable("NEW_API_UPSTREAM_UNAVAILABLE", "failed to communicate with New API site")
	ErrNewAPIUserMismatch             = infraerrors.BadRequest("NEW_API_USER_MISMATCH", "New API account ID does not match the previewed account")
)

// NewAPIMigrationConfig 是面向管理后台与业务逻辑的配置结构（明文令牌仅用于写入，读取时脱敏）。
type NewAPIMigrationConfig struct {
	Enabled                bool    `json:"enabled"`
	BaseURL                string  `json:"base_url"`
	SiteName               string  `json:"site_name"`
	AdminAccessToken       string  `json:"admin_access_token,omitempty"`
	AdminAccessTokenSet    bool    `json:"admin_access_token_configured"`
	ClearAdminAccessToken  bool    `json:"clear_admin_access_token,omitempty"`
	QuotaPerUnit           float64 `json:"quota_per_unit"`
	AutoDetectQuotaPerUnit bool    `json:"auto_detect_quota_per_unit"`
	ExchangeRate           float64 `json:"exchange_rate"`
	MigrateFromEnabled     bool    `json:"migrate_from_enabled"`
	MigrateToEnabled       bool    `json:"migrate_to_enabled"`
	AllowInsecureHTTP      bool    `json:"allow_insecure_http"`
}

// storedNewAPIMigrationConfig 为持久化在 settings 表中的 JSON 结构（管理员令牌密文存储）。
type storedNewAPIMigrationConfig struct {
	Enabled                   bool    `json:"enabled"`
	BaseURL                   string  `json:"base_url"`
	SiteName                  string  `json:"site_name"`
	AdminAccessTokenEncrypted string  `json:"admin_access_token_encrypted"`
	QuotaPerUnit              float64 `json:"quota_per_unit"`
	AutoDetectQuotaPerUnit    bool    `json:"auto_detect_quota_per_unit"`
	ExchangeRate              float64 `json:"exchange_rate"`
	MigrateFromEnabled        bool    `json:"migrate_from_enabled"`
	MigrateToEnabled          bool    `json:"migrate_to_enabled"`
	AllowInsecureHTTP         bool    `json:"allow_insecure_http"`
}

// NewAPIMigrationUserInfo 返回给已登录前端用户的迁移元信息（不含任何管理员凭证）。
type NewAPIMigrationUserInfo struct {
	Enabled            bool    `json:"enabled"`
	MigrateFromEnabled bool    `json:"migrate_from_enabled"`
	MigrateToEnabled   bool    `json:"migrate_to_enabled"`
	SiteName           string  `json:"site_name"`
	SiteURL            string  `json:"site_url"`
	QuotaPerUnit       float64 `json:"quota_per_unit"`
	ExchangeRate       float64 `json:"exchange_rate"`
}

// NewAPIMigrationTestConnectionInput 管理员测试连接请求（可选覆盖未保存的表单值）。
type NewAPIMigrationTestConnectionInput struct {
	BaseURL           string `json:"base_url"`
	AdminAccessToken  string `json:"admin_access_token"`
	AllowInsecureHTTP *bool  `json:"allow_insecure_http,omitempty"`
}

// NewAPIMigrationTestConnectionResult 管理员测试连接诊断结果。
type NewAPIMigrationTestConnectionResult struct {
	Connected         bool    `json:"connected"`
	SiteURL           string  `json:"site_url"`
	SystemName        string  `json:"system_name"`
	Version           string  `json:"version"`
	QuotaPerUnit      float64 `json:"quota_per_unit"`
	DisplayInCurrency bool    `json:"display_in_currency"`
	AdminUserID       int64   `json:"admin_user_id"`
	AdminUsername     string  `json:"admin_username"`
	AdminRole         int     `json:"admin_role"`
	IsRoot            bool    `json:"is_root"`
	UserReadOK        bool    `json:"user_read_ok"`
	Warning           string  `json:"warning,omitempty"`
}

// NewAPIMigrationPreviewResult 用户提交 New API 访问令牌后的预检结果。
type NewAPIMigrationPreviewResult struct {
	SiteURL             string  `json:"site_url"`
	SiteName            string  `json:"site_name"`
	NewAPIUserID        int64   `json:"new_api_user_id"`
	NewAPIUsername      string  `json:"new_api_username"`
	NewAPIDisplayName   string  `json:"new_api_display_name"`
	NewAPIEmail         string  `json:"new_api_email"`
	NewAPIRole          int     `json:"new_api_role"`
	NewAPIStatus        int     `json:"new_api_status"`
	RegularQuota        int64   `json:"regular_quota"`
	GiftQuota           int64   `json:"gift_quota"`
	TotalQuota          int64   `json:"total_quota"`
	UsedQuota           int64   `json:"used_quota"`
	QuotaPerUnit        float64 `json:"quota_per_unit"`
	ExchangeRate        float64 `json:"exchange_rate"`
	RawUSD              float64 `json:"raw_usd"`
	EstimatedCredit     float64 `json:"estimated_credit"`
	CurrentBalance      float64 `json:"current_balance"`
	EstimatedNewBalance float64 `json:"estimated_new_balance"`
	AlreadyMigrated     bool    `json:"already_migrated"`
	CanMigrate          bool    `json:"can_migrate"`
	CannotMigrateReason string  `json:"cannot_migrate_reason,omitempty"`
}

// NewAPIMigrationExecuteInput 用户确认执行一次性全额迁出请求。
type NewAPIMigrationExecuteInput struct {
	AccessToken          string `json:"access_token"`
	ExpectedNewAPIUserID int64  `json:"expected_new_api_user_id,omitempty"`
	ClientIP             string `json:"-"`
}

// NewAPIMigrationExecuteResult 迁移成功后的返回结果。
type NewAPIMigrationExecuteResult struct {
	Record     *NewAPIMigrationRecord `json:"record"`
	NewBalance float64                `json:"new_balance"`
}

// NewAPIMigrationRecord 对应 new_api_migration_records 表的一条迁移审计记录。
type NewAPIMigrationRecord struct {
	ID                   int64      `json:"id"`
	Direction            string     `json:"direction"`
	Status               string     `json:"status"`
	SiteURL              string     `json:"site_url"`
	Sub2APIUserID        int64      `json:"sub2api_user_id"`
	Sub2APIUserEmail     string     `json:"sub2api_user_email"`
	NewAPIUserID         int64      `json:"new_api_user_id"`
	NewAPIUsername       string     `json:"new_api_username"`
	NewAPIDisplayName    string     `json:"new_api_display_name"`
	NewAPIEmail          string     `json:"new_api_email"`
	NewAPIQuotaBefore    int64      `json:"new_api_quota_before"`
	NewAPIQuotaDelta     int64      `json:"new_api_quota_delta"`
	NewAPIQuotaAfter     int64      `json:"new_api_quota_after"`
	NewAPIGiftQuota      int64      `json:"new_api_gift_quota"`
	QuotaPerUnit         float64    `json:"quota_per_unit"`
	ExchangeRate         float64    `json:"exchange_rate"`
	Sub2APIBalanceBefore float64    `json:"sub2api_balance_before"`
	Sub2APIBalanceDelta  float64    `json:"sub2api_balance_delta"`
	Sub2APIBalanceAfter  float64    `json:"sub2api_balance_after"`
	RedeemCodeID         *int64     `json:"redeem_code_id,omitempty"`
	ErrorMessage         string     `json:"error_message,omitempty"`
	ClientIP             string     `json:"client_ip,omitempty"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// NewAPIMigrationRecordFilters 管理端迁移流水筛选条件。
type NewAPIMigrationRecordFilters struct {
	Status    string
	Direction string
	Search    string
}

// NewAPIMigrationStats 管理端迁移统计摘要。
type NewAPIMigrationStats struct {
	TotalCount             int64   `json:"total_count"`
	CompletedCount         int64   `json:"completed_count"`
	PendingCount           int64   `json:"pending_count"`
	FailedCount            int64   `json:"failed_count"`
	CompensatedCount       int64   `json:"compensated_count"`
	NeedsManualReviewCount int64   `json:"needs_manual_review_count"`
	TotalMigratedBalance   float64 `json:"total_migrated_balance"`
	TotalMigratedQuota     int64   `json:"total_migrated_quota"`
}

// NewAPIMigrationRepository 迁移记录仓储接口。
type NewAPIMigrationRepository interface {
	CreatePending(ctx context.Context, record *NewAPIMigrationRecord) error
	FindBlockingFromMigration(ctx context.Context, siteURL string, newAPIUserID int64) (*NewAPIMigrationRecord, error)
	MarkCompleted(ctx context.Context, id int64, balanceBefore, balanceDelta, balanceAfter float64, redeemCodeID *int64, completedAt time.Time) error
	MarkFailed(ctx context.Context, id int64, errMsg string) error
	MarkCompensated(ctx context.Context, id int64, errMsg string) error
	MarkNeedsManualReview(ctx context.Context, id int64, errMsg string) error
	ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]NewAPIMigrationRecord, *pagination.PaginationResult, error)
	ListAdmin(ctx context.Context, params pagination.PaginationParams, filters NewAPIMigrationRecordFilters) ([]NewAPIMigrationRecord, *pagination.PaginationResult, error)
	GetStats(ctx context.Context) (*NewAPIMigrationStats, error)
}
