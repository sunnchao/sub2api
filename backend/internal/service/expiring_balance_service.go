package service

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const (
	// ExpiringLotStatusActive 表示限时余额批次仍有效或仍有未结算冻结额度。
	ExpiringLotStatusActive = "active"
	// ExpiringLotStatusExhausted 表示限时余额批次已被全部消费耗尽。
	ExpiringLotStatusExhausted = "exhausted"
	// ExpiringLotStatusExpired 表示限时余额批次已过期且无剩余在途冻结额度。
	ExpiringLotStatusExpired = "expired"

	// ExpiringLotSourceCheckin 标识限时余额批次来源于每日签到。
	ExpiringLotSourceCheckin = "checkin"

	// ExpiringAllocRefUsageBilling 标识分摊记录来源于 API 网关按量计费。
	ExpiringAllocRefUsageBilling = "usage_billing"
	// ExpiringAllocRefBatchImageHold 标识分摊记录来源于批量生图预扣冻结。
	ExpiringAllocRefBatchImageHold = "batch_image_hold"
	// ExpiringAllocRefPaymentOrder 标识分摊记录来源于余额直接购买订阅订单。
	ExpiringAllocRefPaymentOrder = "payment_order"
)

// ExpiringBalanceSummary 描述用户当前有效限时余额及最近到期信息。
type ExpiringBalanceSummary struct {
	ActiveBalance    float64    `json:"total_active"`
	FrozenBalance    float64    `json:"frozen_amount"`
	NearestExpiresAt *time.Time `json:"nearest_expires_at,omitempty"`
	NearestAmount    float64    `json:"nearest_amount"`
}

// ExpiringBalanceLot 描述单笔限时余额批次的生命周期状态。
type ExpiringBalanceLot struct {
	ID              int64     `json:"id"`
	UserID          int64     `json:"user_id"`
	SourceType      string    `json:"source_type"`
	SourceID        int64     `json:"source_id"`
	InitialAmount   float64   `json:"initial_amount"`
	RemainingAmount float64   `json:"remaining_amount"`
	FrozenAmount    float64   `json:"frozen_amount"`
	ConsumedAmount  float64   `json:"consumed_amount"`
	ExpiredAmount   float64   `json:"expired_amount"`
	ExpiresAt       time.Time `json:"expires_at"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// DeductBalanceResult 描述 FEFO 扣减总可用余额（限时优先 + 普通余额兜底）后的余额快照。
type DeductBalanceResult struct {
	NewRegularBalance  float64
	NewExpiringBalance float64
	NewTotalBalance    float64
	ExpiringDeducted   float64
	RegularDeducted    float64
	Sufficient         bool
}

// RefundAllocResult 描述按 LIFO 原路退回订单余额的结果（含过期批次自动作废金额）。
type RefundAllocResult struct {
	RegularRefunded   float64
	ExpiringRefunded  float64
	ExpiringForfeited float64
}

// ExpiringBalanceRepository 定义限时余额存储与事务操作接口。
type ExpiringBalanceRepository interface {
	DeductTotalBalance(ctx context.Context, userID int64, amount float64, allowOverdraft bool, refType, refKey string) (DeductBalanceResult, error)
	RefundPaymentOrderBalance(ctx context.Context, userID, orderID int64, refundAmount float64) (hasAlloc bool, res RefundAllocResult, err error)
	GetActiveExpiringSummary(ctx context.Context, userID int64) (ExpiringBalanceSummary, error)
	GetActiveExpiringSummariesBatch(ctx context.Context, userIDs []int64) (map[int64]ExpiringBalanceSummary, error)
	ListUserExpiringLots(ctx context.Context, userID int64, status string, params pagination.PaginationParams) ([]ExpiringBalanceLot, *pagination.PaginationResult, error)
	ArchiveExpiredLotsBatch(ctx context.Context, limit int) (affectedUserIDs []int64, archivedCount int, err error)
}

// ExpiringBalanceService 提供限时余额查询与到期时间计算服务。
type ExpiringBalanceService struct {
	repo ExpiringBalanceRepository
}

// NewExpiringBalanceService 创建限时余额服务实例。
func NewExpiringBalanceService(repo ExpiringBalanceRepository) *ExpiringBalanceService {
	return &ExpiringBalanceService{repo: repo}
}

// GetUserSummary 获取单个用户的当前有效限时余额汇总。
func (s *ExpiringBalanceService) GetUserSummary(ctx context.Context, userID int64) (ExpiringBalanceSummary, error) {
	if s == nil || s.repo == nil {
		return ExpiringBalanceSummary{}, infraerrors.InternalServer("EXPIRING_BALANCE_SERVICE_UNAVAILABLE", "expiring balance repository is not configured")
	}
	if userID <= 0 {
		return ExpiringBalanceSummary{}, nil
	}
	return s.repo.GetActiveExpiringSummary(ctx, userID)
}

// GetUsersSummaryBatch 批量获取多个用户的有效限时余额汇总（自动去重并过滤非正 ID）。
func (s *ExpiringBalanceService) GetUsersSummaryBatch(ctx context.Context, userIDs []int64) (map[int64]ExpiringBalanceSummary, error) {
	if s == nil || s.repo == nil {
		return nil, infraerrors.InternalServer("EXPIRING_BALANCE_SERVICE_UNAVAILABLE", "expiring balance repository is not configured")
	}
	unique := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return map[int64]ExpiringBalanceSummary{}, nil
	}
	return s.repo.GetActiveExpiringSummariesBatch(ctx, unique)
}

// ListUserLots 分页查询用户的限时余额批次明细。
func (s *ExpiringBalanceService) ListUserLots(
	ctx context.Context,
	userID int64,
	status string,
	params pagination.PaginationParams,
) ([]ExpiringBalanceLot, *pagination.PaginationResult, error) {
	if s == nil || s.repo == nil {
		return nil, nil, infraerrors.InternalServer("EXPIRING_BALANCE_SERVICE_UNAVAILABLE", "expiring balance repository is not configured")
	}
	if userID <= 0 {
		return nil, nil, infraerrors.BadRequest("INVALID_USER_ID", "user_id must be positive")
	}

	normalizedStatus := strings.ToLower(strings.TrimSpace(status))
	switch normalizedStatus {
	case "", "all":
		normalizedStatus = ""
	case ExpiringLotStatusActive, ExpiringLotStatusExhausted, ExpiringLotStatusExpired:
	default:
		return nil, nil, infraerrors.BadRequest("INVALID_LOT_STATUS", "invalid expiring balance lot status")
	}

	if params.Page < 1 {
		params.Page = 1
	}
	params.PageSize = params.Limit()

	return s.repo.ListUserExpiringLots(ctx, userID, normalizedStatus, params)
}

// ComputeExpiresAt 基于全局时区计算有效天数 N 对应的过期时间点（下一有效周期 00:00:00）。
// 采用严格半开区间 [granted_at, expires_at)，确保自然日 23:59:59 到期。
func (s *ExpiringBalanceService) ComputeExpiresAt(now time.Time, validityDays int) time.Time {
	if validityDays < 1 {
		validityDays = 1
	}
	return timezone.StartOfDay(now).AddDate(0, 0, validityDays)
}
