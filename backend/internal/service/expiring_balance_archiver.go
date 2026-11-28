package service

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// DefaultExpiringBalanceArchiveInterval 默认每 5 分钟扫描并归档已过期限时余额批次。
	DefaultExpiringBalanceArchiveInterval = 5 * time.Minute
	// DefaultExpiringBalanceArchiveBatchSize 单次数据库事务最大归档批次数。
	DefaultExpiringBalanceArchiveBatchSize = 500

	expiringBalanceArchiveRunTimeout       = 30 * time.Second
	expiringBalanceArchiveMaxBatchesPerRun = 100
	expiringBalanceCacheInvalidateTimeout  = 5 * time.Second
	expiringBalanceArchiverLeaderLockKey   = "expiring:balance:archiver:leader"
	expiringBalanceArchiverLeaderLockTTL   = 3 * time.Minute
)

// BillingCacheInvalidator 定义计费余额缓存清理的窄接口。
type BillingCacheInvalidator interface {
	InvalidateUserBalance(ctx context.Context, userID int64) error
}

// AuthCacheInvalidator 定义 API Key 认证缓存按用户清理的窄接口。
type AuthCacheInvalidator interface {
	InvalidateAuthCacheByUserID(ctx context.Context, userID int64)
}

// ExpiringBalanceArchiver 后台定时将已过期的限时余额批次归档为 expired，并清理受影响用户缓存。
// 过期本身绝不触发低余额告警邮件，严格遵守不依赖 BalanceNotifyService 的边界约束。
type ExpiringBalanceArchiver struct {
	repo         ExpiringBalanceRepository
	billingCache BillingCacheInvalidator
	authCache    AuthCacheInvalidator
	interval     time.Duration
	batchSize    int

	mu      sync.Mutex
	stopCh  chan struct{}
	started bool
	stopped bool
	wg      sync.WaitGroup

	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
}

// NewExpiringBalanceArchiver 创建限时余额后台归档器。
// 当 interval <= 0 时，Start() 为空操作（便于单元测试按需调用 RunOnce）。
func NewExpiringBalanceArchiver(
	repo ExpiringBalanceRepository,
	billingCache BillingCacheInvalidator,
	authCache AuthCacheInvalidator,
	interval time.Duration,
	batchSize int,
) *ExpiringBalanceArchiver {
	if batchSize <= 0 {
		batchSize = DefaultExpiringBalanceArchiveBatchSize
	}
	return &ExpiringBalanceArchiver{
		repo:         repo,
		billingCache: billingCache,
		authCache:    authCache,
		interval:     interval,
		batchSize:    batchSize,
		stopCh:       make(chan struct{}),
		instanceID:   uuid.NewString(),
	}
}

// SetLeaderLock 注入多实例领导者锁（Redis 优先，Postgres Advisory Lock 兜底）。
// 当两者均为 nil 时（单机或单元测试）不加锁直接执行。
func (a *ExpiringBalanceArchiver) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if a == nil {
		return
	}
	a.lockCache = lockCache
	a.db = db
}

// Start 启动定时归档循环；重复调用幂等，interval <= 0 或已 Stop 时不启动后台协程。
func (a *ExpiringBalanceArchiver) Start() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.stopped || a.started || a.repo == nil || a.interval <= 0 {
		a.mu.Unlock()
		return
	}
	a.started = true
	a.wg.Add(1)
	a.mu.Unlock()
	go a.runLoop()
}

// Stop 优雅停止后台归档协程并等待在途扫描退出。
func (a *ExpiringBalanceArchiver) Stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if !a.stopped {
		a.stopped = true
		if a.stopCh != nil {
			close(a.stopCh)
		}
	}
	started := a.started
	a.mu.Unlock()
	if started {
		a.wg.Wait()
	}
}

func (a *ExpiringBalanceArchiver) runLoop() {
	defer a.wg.Done()
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	a.runScheduledCycle()
	for {
		select {
		case <-ticker.C:
			a.runScheduledCycle()
		case <-a.stopCh:
			return
		}
	}
}

func (a *ExpiringBalanceArchiver) runScheduledCycle() {
	ctx, cancel := context.WithTimeout(context.Background(), expiringBalanceArchiveRunTimeout)
	defer cancel()
	_ = a.RunOnce(ctx)
}

// RunOnce 执行一轮过期批次归档（含多实例领导者锁抢占、分批排空与受影响用户缓存失效）。
func (a *ExpiringBalanceArchiver) RunOnce(ctx context.Context) error {
	if a == nil || a.repo == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	lockCtx, lockCancel := context.WithTimeout(ctx, 2*time.Second)
	release, ok := tryAcquireSingletonLeaderLock(
		lockCtx,
		a.lockCache,
		a.db,
		expiringBalanceArchiverLeaderLockKey,
		a.instanceID,
		expiringBalanceArchiverLeaderLockTTL,
	)
	lockCancel()
	if !ok {
		return nil
	}
	defer release()

	seen := make(map[int64]struct{})
	affectedUsers := make([]int64, 0)
	totalArchived := 0
	var runErr error

	for batchIdx := 0; batchIdx < expiringBalanceArchiveMaxBatchesPerRun; batchIdx++ {
		if a.isStopped() || ctx.Err() != nil {
			break
		}
		userIDs, archived, err := a.repo.ArchiveExpiredLotsBatch(ctx, a.batchSize)
		if err != nil {
			slog.Warn("[ExpiringBalanceArchiver] archive expired lots batch failed", "batch_index", batchIdx, "error", err)
			runErr = err
			break
		}
		for _, uid := range userIDs {
			if uid <= 0 {
				continue
			}
			if _, exists := seen[uid]; exists {
				continue
			}
			seen[uid] = struct{}{}
			affectedUsers = append(affectedUsers, uid)
		}
		totalArchived += archived
		if archived < a.batchSize {
			break
		}
	}

	for _, uid := range affectedUsers {
		invCtx, invCancel := context.WithTimeout(context.Background(), expiringBalanceCacheInvalidateTimeout)
		if a.billingCache != nil {
			if err := a.billingCache.InvalidateUserBalance(invCtx, uid); err != nil {
				slog.Warn("[ExpiringBalanceArchiver] invalidate billing balance cache failed", "user_id", uid, "error", err)
			}
		}
		if a.authCache != nil {
			a.authCache.InvalidateAuthCacheByUserID(invCtx, uid)
		}
		invCancel()
	}

	if totalArchived > 0 {
		slog.Info("[ExpiringBalanceArchiver] archived expired balance lots",
			"archived_count", totalArchived,
			"affected_users", len(affectedUsers),
		)
	}

	return runErr
}

func (a *ExpiringBalanceArchiver) isStopped() bool {
	if a == nil || a.stopCh == nil {
		return true
	}
	select {
	case <-a.stopCh:
		return true
	default:
		return false
	}
}
