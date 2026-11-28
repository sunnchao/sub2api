package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// checkinDateLayout 是 checkin_date（DATE 列）的传输格式，按全局时区计算的自然日。
const checkinDateLayout = "2006-01-02"

// CheckinRepo 实现 service.CheckinRepository。
// 旁路表全部通过原生 SQL 访问，不引入 ent schema 变更。
type CheckinRepo struct {
	client *dbent.Client
	sqlDB  *sql.DB
}

// NewCheckinRepository 创建签到仓储。
func NewCheckinRepository(client *dbent.Client, sqlDB *sql.DB) *CheckinRepo {
	return &CheckinRepo{client: client, sqlDB: sqlDB}
}

func (r *CheckinRepo) executor(ctx context.Context) (sqlExecutor, string, error) {
	if r == nil {
		return nil, "", errors.New("checkin repository is nil")
	}
	if tx := dbent.TxFromContext(ctx); tx != nil {
		c := tx.Client()
		return c, expiringSQLDialect(ctx, c), nil
	}
	if r.client != nil {
		return r.client, expiringSQLDialect(ctx, r.client), nil
	}
	if r.sqlDB != nil {
		return r.sqlDB, expiringSQLDialect(ctx, r.sqlDB), nil
	}
	return nil, "", errors.New("checkin sql executor is not configured")
}

func (r *CheckinRepo) withTx(ctx context.Context, fn func(txCtx context.Context, exec sqlExecutor) error) error {
	if r == nil {
		return errors.New("checkin repository is nil")
	}
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(ctx, tx.Client())
	}
	if r.client != nil {
		tx, err := r.client.Tx(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := fn(ctx, tx.Client()); err != nil {
			return err
		}
		return tx.Commit()
	}
	if r.sqlDB != nil {
		d := expiringSQLDialect(ctx, r.sqlDB)
		tx, err := r.sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		txCtx := withExpiringSQLDialect(ctx, d)
		if err := fn(txCtx, tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	return errors.New("checkin sql executor is not configured")
}

// ExecuteCheckin 在单个事务内完成签到判定与发放。
func (r *CheckinRepo) ExecuteCheckin(ctx context.Context, in service.CheckinExecuteInput) (service.CheckinRecord, error) {
	var record service.CheckinRecord
	err := r.withTx(ctx, func(txCtx context.Context, exec sqlExecutor) error {
		var txErr error
		record, txErr = executeCheckinTx(txCtx, exec, in)
		return txErr
	})
	return record, err
}

func executeCheckinTx(ctx context.Context, exec sqlExecutor, in service.CheckinExecuteInput) (service.CheckinRecord, error) {
	if in.Settings == nil {
		return service.CheckinRecord{}, infraerrors.InternalServer(service.CheckinReasonRepositoryMissing, "checkin settings are missing")
	}
	if in.UserID <= 0 {
		return service.CheckinRecord{}, infraerrors.BadRequest("INVALID_USER_ID", "user_id must be positive")
	}

	dialectName := expiringSQLDialect(ctx, exec)
	now := in.Now
	if now.IsZero() {
		now = timezone.Now()
	}
	today := in.Today
	if today.IsZero() {
		today = timezone.StartOfDay(now)
	}
	yesterday := in.Yesterday
	if yesterday.IsZero() {
		yesterday = today.AddDate(0, 0, -1)
	}
	todayStr := today.Format(checkinDateLayout)
	clientIP := strings.TrimSpace(in.ClientIP)

	// 1. 锁定用户行（与扣费链路保持一致的锁序：先 users，再旁路表）。
	snapshot, err := lockCheckinUserTx(ctx, exec, dialectName, in.UserID)
	if err != nil {
		return service.CheckinRecord{}, err
	}

	// 2. 准入门槛校验。
	activeExpiring, err := sumActiveExpiringTx(ctx, exec, in.UserID, now)
	if err != nil {
		return service.CheckinRecord{}, err
	}
	if eligibility := service.EvaluateCheckinEligibility(in.Settings, snapshot, now, activeExpiring); !eligibility.Eligible {
		return service.CheckinRecord{}, checkinEligibilityError(eligibility.ReasonCode)
	}

	// 3. 当日重复签到校验 + 连签推算。
	last, err := latestCheckinRecordTx(ctx, exec, in.UserID)
	if err != nil {
		return service.CheckinRecord{}, err
	}
	if last != nil && last.CheckinDate == todayStr {
		return service.CheckinRecord{}, infraerrors.Conflict(service.CheckinReasonAlreadyDone, "already checked in today")
	}
	streak, cycleDay, bonus := service.ResolveCheckinStreak(in.Settings, last, yesterday)
	total := service.QuantizeUsageBillingAmount(in.BaseAmount + bonus)

	// 4. 单 IP 每日账号数上限（在 Postgres 下通过事务级 advisory lock 串行化同日同 IP 计数）。
	if in.Settings.Risk.MaxAccountsPerIPDay > 0 && clientIP != "" {
		if dialectName == dialect.Postgres {
			h := fnv.New64a()
			_, _ = h.Write([]byte("checkin_ip:" + todayStr + ":" + clientIP))
			lockKey := int64(h.Sum64())
			if _, lockErr := exec.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); lockErr != nil {
				return service.CheckinRecord{}, fmt.Errorf("acquire checkin ip advisory lock: %w", lockErr)
			}
		}
		var ipAccounts int
		err := scanSingleRow(ctx, exec, `
			SELECT COUNT(DISTINCT user_id)
			FROM user_checkin_records
			WHERE checkin_date = $1 AND client_ip = $2
		`, []any{todayStr, clientIP}, &ipAccounts)
		if err != nil {
			return service.CheckinRecord{}, fmt.Errorf("count checkin ip accounts: %w", err)
		}
		if ipAccounts >= in.Settings.Risk.MaxAccountsPerIPDay {
			return service.CheckinRecord{}, infraerrors.TooManyRequests(service.CheckinReasonIPLimitExceeded,
				"too many accounts have checked in from this IP today")
		}
	}

	// 5. 全站每日预算原子熔断。
	if err := reserveCheckinDailyBudgetTx(ctx, exec, dialectName, todayStr, total, in.Settings.Risk.DailyTotalBudgetUSD, now); err != nil {
		return service.CheckinRecord{}, err
	}

	// 6. 写入签到记录。
	createdAt := now
	in.ClientIP = clientIP
	recordID, err := insertCheckinRecordTx(ctx, exec, dialectName, in, todayStr, streak, cycleDay, bonus, total, createdAt)
	if err != nil {
		return service.CheckinRecord{}, err
	}

	// 7. 发放限时余额批次。
	if total > 0 {
		if err := insertCheckinLotTx(ctx, exec, in.UserID, recordID, total, in.ExpiresAt, createdAt); err != nil {
			return service.CheckinRecord{}, err
		}
	}

	return service.CheckinRecord{
		ID:                   recordID,
		UserID:               in.UserID,
		CheckinDate:          todayStr,
		StreakDays:           streak,
		MilestoneCycleDay:    cycleDay,
		BaseAmount:           service.QuantizeUsageBillingAmount(in.BaseAmount),
		MilestoneBonusAmount: service.QuantizeUsageBillingAmount(bonus),
		TotalAmount:          total,
		ValidityDays:         in.ValidityDays,
		ExpiresAt:            in.ExpiresAt,
		ClientIP:             clientIP,
		CreatedAt:            createdAt,
	}, nil
}

// lockCheckinUserTx 锁定用户行并返回签到判定所需的快照。
func lockCheckinUserTx(ctx context.Context, exec sqlExecutor, dialectName string, userID int64) (service.CheckinUserSnapshot, error) {
	query := `
		SELECT id, COALESCE(status, ''), created_at, COALESCE(total_recharged, 0), COALESCE(signup_source, ''), COALESCE(balance, 0)
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`
	if dialectName == dialect.Postgres {
		query += ` FOR UPDATE`
	}

	var (
		snapshot  service.CheckinUserSnapshot
		createdAt flexTime
	)
	err := scanSingleRow(ctx, exec, query, []any{userID},
		&snapshot.ID,
		&snapshot.Status,
		&createdAt,
		&snapshot.TotalRecharged,
		&snapshot.SignupSource,
		&snapshot.Balance,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return service.CheckinUserSnapshot{}, infraerrors.NotFound("USER_NOT_FOUND", "user not found")
	}
	if err != nil {
		return service.CheckinUserSnapshot{}, fmt.Errorf("lock checkin user: %w", err)
	}
	snapshot.CreatedAt = createdAt.Time

	providers, err := boundAuthProvidersTx(ctx, exec, userID)
	if err != nil {
		return service.CheckinUserSnapshot{}, err
	}
	snapshot.BoundProviders = providers
	return snapshot, nil
}

// boundAuthProvidersTx 返回用户已绑定的登录身份提供方列表。
func boundAuthProvidersTx(ctx context.Context, exec sqlExecutor, userID int64) ([]string, error) {
	rows, err := exec.QueryContext(ctx, `
		SELECT DISTINCT provider_type FROM auth_identities WHERE user_id = $1
	`, userID)
	if err != nil {
		// 旧库可能没有 auth_identities，降级为仅按 signup_source 判定。
		dialectName := expiringSQLDialect(ctx, exec)
		if isMissingExpiringBalanceTable(err, dialectName) ||
			(dialectName != dialect.Postgres && strings.Contains(err.Error(), "no such table: auth_identities")) {
			return nil, nil
		}
		return nil, fmt.Errorf("list auth identities: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]string, 0, 4)
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			return nil, fmt.Errorf("scan auth identity provider: %w", err)
		}
		out = append(out, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate auth identities: %w", err)
	}
	return out, nil
}

// sumActiveExpiringTx 返回用户当前有效限时余额合计（含冻结额度，用于余额封顶判定）。
func sumActiveExpiringTx(ctx context.Context, exec sqlExecutor, userID int64, now time.Time) (float64, error) {
	var total sql.NullFloat64
	err := scanSingleRow(ctx, exec, `
		SELECT COALESCE(SUM(remaining_amount), 0)
		FROM user_expiring_balance_lots
		WHERE user_id = $1 AND status = 'active' AND remaining_amount > 0 AND expires_at > $2
	`, []any{userID, now.UTC()}, &total)
	if err != nil {
		if isMissingExpiringBalanceTable(err, expiringSQLDialect(ctx, exec)) {
			return 0, nil
		}
		return 0, fmt.Errorf("sum active expiring balance: %w", err)
	}
	if !total.Valid {
		return 0, nil
	}
	return total.Float64, nil
}

// latestCheckinRecordTx 返回用户最近一条签到记录。
func latestCheckinRecordTx(ctx context.Context, exec sqlExecutor, userID int64) (*service.CheckinRecord, error) {
	const latestSQL = `
		SELECT id, user_id, checkin_date, streak_days, milestone_cycle_day,
		       base_amount, milestone_bonus_amount, total_amount, validity_days,
		       expires_at, COALESCE(client_ip, ''), created_at
		FROM user_checkin_records
		WHERE user_id = $1
		ORDER BY checkin_date DESC, id DESC
		LIMIT 1
	`

	record, err := scanCheckinRecordRow(ctx, exec, latestSQL, []any{userID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return record, nil
}

// reserveCheckinDailyBudgetTx 原子累加当日预算；超限时返回 429 并回滚。
func reserveCheckinDailyBudgetTx(ctx context.Context, exec sqlExecutor, dialectName, todayStr string, amount, budgetLimit float64, now time.Time) error {
	amount = service.QuantizeUsageBillingAmount(amount)
	if now.IsZero() {
		now = timezone.Now()
	}
	nowUTC := now.UTC()

	if budgetLimit <= 0 {
		res, err := exec.ExecContext(ctx, `
			INSERT INTO checkin_daily_budgets (checkin_date, total_granted, checkin_count, updated_at)
			VALUES ($1, $2, 1, $3)
			ON CONFLICT (checkin_date) DO UPDATE
			SET total_granted = checkin_daily_budgets.total_granted + EXCLUDED.total_granted,
			    checkin_count = checkin_daily_budgets.checkin_count + 1,
			    updated_at = EXCLUDED.updated_at
		`, todayStr, amount, nowUTC)
		if err != nil {
			return fmt.Errorf("reserve checkin daily budget: %w", err)
		}
		if _, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("checkin daily budget rows affected: %w", err)
		}
		return nil
	}

	budgetLimit = service.QuantizeUsageBillingAmount(budgetLimit)
	if _, err := exec.ExecContext(ctx, `
		INSERT INTO checkin_daily_budgets (checkin_date, total_granted, checkin_count, updated_at)
		VALUES ($1, 0, 0, $2)
		ON CONFLICT (checkin_date) DO NOTHING
	`, todayStr, nowUTC); err != nil {
		return fmt.Errorf("init checkin daily budget: %w", err)
	}

	if dialectName == dialect.Postgres {
		res, err := exec.ExecContext(ctx, `
			UPDATE checkin_daily_budgets
			SET total_granted = total_granted + $2,
			    checkin_count = checkin_count + 1,
			    updated_at = $3
			WHERE checkin_date = $1 AND total_granted + $2 <= $4
		`, todayStr, amount, nowUTC, budgetLimit)
		if err != nil {
			return fmt.Errorf("reserve checkin daily budget: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("checkin daily budget rows affected: %w", err)
		}
		if affected == 0 {
			return infraerrors.TooManyRequests(service.CheckinReasonDailyBudgetExhausted,
				"today's global check-in budget has been exhausted")
		}
		return nil
	}

	var curGranted float64
	if err := scanSingleRow(ctx, exec, `
		SELECT COALESCE(total_granted, 0)
		FROM checkin_daily_budgets
		WHERE checkin_date = $1
	`, []any{todayStr}, &curGranted); err != nil {
		return fmt.Errorf("read checkin daily budget: %w", err)
	}
	nextGranted := decFromFloat(curGranted).Add(decFromFloat(amount))
	if nextGranted.GreaterThan(decFromFloat(budgetLimit)) {
		return infraerrors.TooManyRequests(service.CheckinReasonDailyBudgetExhausted,
			"today's global check-in budget has been exhausted")
	}
	res, err := exec.ExecContext(ctx, `
		UPDATE checkin_daily_budgets
		SET total_granted = $1,
		    checkin_count = checkin_count + 1,
		    updated_at = $2
		WHERE checkin_date = $3
	`, floatFromDec(nextGranted), nowUTC, todayStr)
	if err != nil {
		return fmt.Errorf("update checkin daily budget: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checkin daily budget rows affected: %w", err)
	}
	if affected == 0 {
		return infraerrors.TooManyRequests(service.CheckinReasonDailyBudgetExhausted,
			"today's global check-in budget has been exhausted")
	}
	return nil
}

// insertCheckinRecordTx 写入签到记录并返回主键。
func insertCheckinRecordTx(
	ctx context.Context,
	exec sqlExecutor,
	dialectName string,
	in service.CheckinExecuteInput,
	todayStr string,
	streak, cycleDay int,
	bonus, total float64,
	createdAt time.Time,
) (int64, error) {
	const insertSQL = `
		INSERT INTO user_checkin_records
			(user_id, checkin_date, streak_days, milestone_cycle_day, base_amount,
			 milestone_bonus_amount, total_amount, validity_days, expires_at, client_ip, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	args := []any{
		in.UserID,
		todayStr,
		streak,
		cycleDay,
		service.QuantizeUsageBillingAmount(in.BaseAmount),
		service.QuantizeUsageBillingAmount(bonus),
		total,
		in.ValidityDays,
		in.ExpiresAt.UTC(),
		in.ClientIP,
		createdAt.UTC(),
	}

	if dialectName == dialect.Postgres {
		var id int64
		if err := scanSingleRow(ctx, exec, insertSQL+` RETURNING id`, args, &id); err != nil {
			if isCheckinUniqueViolation(err) {
				return 0, infraerrors.Conflict(service.CheckinReasonAlreadyDone, "already checked in today")
			}
			return 0, fmt.Errorf("insert checkin record: %w", err)
		}
		return id, nil
	}

	res, err := exec.ExecContext(ctx, insertSQL, args...)
	if err != nil {
		if isCheckinUniqueViolation(err) {
			return 0, infraerrors.Conflict(service.CheckinReasonAlreadyDone, "already checked in today")
		}
		return 0, fmt.Errorf("insert checkin record: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("checkin record last insert id: %w", err)
	}
	return id, nil
}

// insertCheckinLotTx 为本次签到发放一条限时余额批次。
func insertCheckinLotTx(
	ctx context.Context,
	exec sqlExecutor,
	userID, sourceID int64,
	amount float64,
	expiresAt, createdAt time.Time,
) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO user_expiring_balance_lots
			(user_id, source_type, source_id, initial_amount, remaining_amount,
			 frozen_amount, consumed_amount, expired_amount, expires_at, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4, 0, 0, 0, $5, 'active', $6, $6)
	`, userID, service.ExpiringLotSourceCheckin, sourceID, service.QuantizeUsageBillingAmount(amount),
		expiresAt.UTC(), createdAt.UTC())
	if err != nil {
		return fmt.Errorf("insert checkin expiring lot: %w", err)
	}
	return nil
}

// checkinEligibilityError 把资格拒绝原因转换为 HTTP 语义错误。
func checkinEligibilityError(reasonCode string) error {
	switch reasonCode {
	case service.CheckinReasonIneligibleAccountAge:
		return infraerrors.Forbidden(reasonCode, "account is too new to check in")
	case service.CheckinReasonIneligibleRecharge:
		return infraerrors.Forbidden(reasonCode, "total recharged amount does not meet the requirement")
	case service.CheckinReasonIneligibleSource:
		return infraerrors.Forbidden(reasonCode, "signup source is not allowed to check in")
	case service.CheckinReasonBalanceCapExceeded:
		return infraerrors.Forbidden(reasonCode, "available balance is above the check-in cap")
	case service.CheckinReasonIneligibleStatus:
		return infraerrors.Forbidden(reasonCode, "account status does not allow check-in")
	default:
		return infraerrors.Forbidden(reasonCode, "not eligible for check-in")
	}
}

// isCheckinUniqueViolation 判定唯一约束冲突（Postgres 23505 / SQLite UNIQUE constraint）。
func isCheckinUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if isUniqueViolation(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "constraint failed: unique")
}

// GetCheckinUserSnapshot 返回用户状态快照（只读，用于资格展示）。
func (r *CheckinRepo) GetCheckinUserSnapshot(ctx context.Context, userID int64) (service.CheckinUserSnapshot, error) {
	exec, _, err := r.executor(ctx)
	if err != nil {
		return service.CheckinUserSnapshot{}, err
	}
	return readCheckinUserSnapshot(ctx, exec, userID)
}

func readCheckinUserSnapshot(ctx context.Context, exec sqlExecutor, userID int64) (service.CheckinUserSnapshot, error) {
	var (
		snapshot  service.CheckinUserSnapshot
		createdAt flexTime
	)
	err := scanSingleRow(ctx, exec, `
		SELECT id, COALESCE(status, ''), created_at, COALESCE(total_recharged, 0), COALESCE(signup_source, ''), COALESCE(balance, 0)
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, []any{userID},
		&snapshot.ID,
		&snapshot.Status,
		&createdAt,
		&snapshot.TotalRecharged,
		&snapshot.SignupSource,
		&snapshot.Balance,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return service.CheckinUserSnapshot{}, infraerrors.NotFound("USER_NOT_FOUND", "user not found")
	}
	if err != nil {
		return service.CheckinUserSnapshot{}, fmt.Errorf("get checkin user snapshot: %w", err)
	}
	snapshot.CreatedAt = createdAt.Time

	providers, err := boundAuthProvidersTx(ctx, exec, userID)
	if err != nil {
		return service.CheckinUserSnapshot{}, err
	}
	snapshot.BoundProviders = providers
	return snapshot, nil
}

// GetLastCheckinRecord 返回用户最近一条签到记录（不存在时返回 nil）。
func (r *CheckinRepo) GetLastCheckinRecord(ctx context.Context, userID int64) (*service.CheckinRecord, error) {
	exec, _, err := r.executor(ctx)
	if err != nil {
		return nil, err
	}
	return latestCheckinRecordTx(ctx, exec, userID)
}

// GetCheckinRecordByDate 返回用户指定自然日的签到记录。
func (r *CheckinRepo) GetCheckinRecordByDate(ctx context.Context, userID int64, date time.Time) (*service.CheckinRecord, error) {
	exec, _, err := r.executor(ctx)
	if err != nil {
		return nil, err
	}
	const byDateSQL = `
		SELECT id, user_id, checkin_date, streak_days, milestone_cycle_day,
		       base_amount, milestone_bonus_amount, total_amount, validity_days,
		       expires_at, COALESCE(client_ip, ''), created_at
		FROM user_checkin_records
		WHERE user_id = $1 AND checkin_date = $2
	`

	record, err := scanCheckinRecordRow(ctx, exec, byDateSQL, []any{userID, date.Format(checkinDateLayout)})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return record, nil
}

// ListCheckinCalendar 返回用户指定月份区间的签到日历明细。
func (r *CheckinRepo) ListCheckinCalendar(ctx context.Context, userID int64, monthStart, monthEnd time.Time) ([]service.CheckinCalendarDay, error) {
	exec, _, err := r.executor(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := exec.QueryContext(ctx, `
		SELECT checkin_date, base_amount, milestone_bonus_amount, total_amount
		FROM user_checkin_records
		WHERE user_id = $1 AND checkin_date >= $2 AND checkin_date < $3
		ORDER BY checkin_date ASC
	`, userID, monthStart.Format(checkinDateLayout), monthEnd.Format(checkinDateLayout))
	if err != nil {
		return nil, fmt.Errorf("list checkin calendar: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.CheckinCalendarDay, 0, 31)
	for rows.Next() {
		var (
			day   flexTime
			base  float64
			bonus float64
			total float64
		)
		if err := rows.Scan(&day, &base, &bonus, &total); err != nil {
			return nil, fmt.Errorf("scan checkin calendar row: %w", err)
		}
		out = append(out, service.CheckinCalendarDay{
			Date:                 day.Time.Format(checkinDateLayout),
			BaseAmount:           base,
			MilestoneBonusAmount: bonus,
			TotalAmount:          total,
			MilestoneHit:         bonus > 0,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate checkin calendar: %w", err)
	}
	return out, nil
}

// ListUserCheckinRecords 分页返回用户签到历史。
func (r *CheckinRepo) ListUserCheckinRecords(ctx context.Context, userID int64, params pagination.PaginationParams) ([]service.CheckinRecord, *pagination.PaginationResult, error) {
	exec, _, err := r.executor(ctx)
	if err != nil {
		return nil, nil, err
	}

	var total int64
	if err := scanSingleRow(ctx, exec,
		`SELECT COUNT(*) FROM user_checkin_records WHERE user_id = $1`, []any{userID}, &total); err != nil {
		return nil, nil, fmt.Errorf("count user checkin records: %w", err)
	}

	rows, err := exec.QueryContext(ctx, `
		SELECT id, user_id, checkin_date, streak_days, milestone_cycle_day,
		       base_amount, milestone_bonus_amount, total_amount, validity_days,
		       expires_at, COALESCE(client_ip, ''), created_at
		FROM user_checkin_records
		WHERE user_id = $1
		ORDER BY checkin_date DESC, id DESC
		LIMIT $2 OFFSET $3
	`, userID, params.Limit(), params.Offset())
	if err != nil {
		return nil, nil, fmt.Errorf("list user checkin records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.CheckinRecord, 0, params.Limit())
	for rows.Next() {
		record, scanErr := scanCheckinRecord(rows)
		if scanErr != nil {
			return nil, nil, scanErr
		}
		out = append(out, *record)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate user checkin records: %w", err)
	}

	return out, &pagination.PaginationResult{
		Total:    total,
		Page:     params.Page,
		PageSize: params.Limit(),
		Pages:    checkinPages(total, params.Limit()),
	}, nil
}

// ListAdminCheckinRecords 分页返回全站签到流水（附带用户标识）。
func (r *CheckinRepo) ListAdminCheckinRecords(ctx context.Context, filter service.AdminCheckinRecordFilter) ([]service.AdminCheckinRecord, *pagination.PaginationResult, error) {
	exec, _, err := r.executor(ctx)
	if err != nil {
		return nil, nil, err
	}

	where := make([]string, 0, 5)
	args := make([]any, 0, 6)
	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if filter.UserID > 0 {
		where = append(where, "r.user_id = "+addArg(filter.UserID))
	}
	if term := strings.TrimSpace(filter.Search); term != "" {
		pattern := "%" + strings.ToLower(term) + "%"
		p := addArg(pattern)
		where = append(where, fmt.Sprintf("(LOWER(u.email) LIKE %s OR LOWER(COALESCE(u.username, '')) LIKE %s)", p, p))
	}
	if ip := strings.TrimSpace(filter.ClientIP); ip != "" {
		where = append(where, "r.client_ip = "+addArg(ip))
	}
	if start := strings.TrimSpace(filter.StartDate); start != "" {
		where = append(where, "r.checkin_date >= "+addArg(start))
	}
	if end := strings.TrimSpace(filter.EndDate); end != "" {
		where = append(where, "r.checkin_date <= "+addArg(end))
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	var total int64
	if err := scanSingleRow(ctx, exec,
		`SELECT COUNT(*) FROM user_checkin_records r LEFT JOIN users u ON u.id = r.user_id`+whereSQL,
		args, &total); err != nil {
		return nil, nil, fmt.Errorf("count admin checkin records: %w", err)
	}

	limitArg := addArg(filter.Params.Limit())
	offsetArg := addArg(filter.Params.Offset())
	rows, err := exec.QueryContext(ctx, `
		SELECT r.id, r.user_id, r.checkin_date, r.streak_days, r.milestone_cycle_day,
		       r.base_amount, r.milestone_bonus_amount, r.total_amount, r.validity_days,
		       r.expires_at, COALESCE(r.client_ip, ''), r.created_at,
		       COALESCE(u.email, ''), COALESCE(u.username, '')
		FROM user_checkin_records r
		LEFT JOIN users u ON u.id = r.user_id`+whereSQL+`
		ORDER BY r.checkin_date DESC, r.id DESC
		LIMIT `+limitArg+` OFFSET `+offsetArg,
		args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list admin checkin records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.AdminCheckinRecord, 0, filter.Params.Limit())
	for rows.Next() {
		var (
			item      service.AdminCheckinRecord
			day       flexTime
			expiresAt flexTime
			createdAt flexTime
		)
		if err := rows.Scan(
			&item.ID, &item.UserID, &day, &item.StreakDays, &item.MilestoneCycleDay,
			&item.BaseAmount, &item.MilestoneBonusAmount, &item.TotalAmount, &item.ValidityDays,
			&expiresAt, &item.ClientIP, &createdAt, &item.UserEmail, &item.Username,
		); err != nil {
			return nil, nil, fmt.Errorf("scan admin checkin record: %w", err)
		}
		item.CheckinDate = day.Time.Format(checkinDateLayout)
		item.ExpiresAt = expiresAt.Time
		item.CreatedAt = createdAt.Time
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate admin checkin records: %w", err)
	}

	return out, &pagination.PaginationResult{
		Total:    total,
		Page:     filter.Params.Page,
		PageSize: filter.Params.Limit(),
		Pages:    checkinPages(total, filter.Params.Limit()),
	}, nil
}

// GetAdminCheckinStats 汇总签到统计（今日、区间累计与每日趋势）。
func (r *CheckinRepo) GetAdminCheckinStats(ctx context.Context, days int) (service.AdminCheckinStats, error) {
	exec, _, err := r.executor(ctx)
	if err != nil {
		return service.AdminCheckinStats{}, err
	}

	stats := service.AdminCheckinStats{RangeDays: days}

	// 今日汇总来自预算表（O(1)，避免全表扫描）。
	var (
		todayGranted sql.NullFloat64
		todayCount   sql.NullInt64
	)
	err = scanSingleRow(ctx, exec, `
		SELECT COALESCE(total_granted, 0), COALESCE(checkin_count, 0)
		FROM checkin_daily_budgets
		WHERE checkin_date = $1
	`, []any{timezone.StartOfDay(timezone.Now()).Format(checkinDateLayout)}, &todayGranted, &todayCount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return service.AdminCheckinStats{}, fmt.Errorf("get today checkin budget: %w", err)
	}
	if todayGranted.Valid {
		stats.TodayGranted = todayGranted.Float64
	}
	if todayCount.Valid {
		stats.TodayCheckins = int(todayCount.Int64)
	}

	// 区间累计发放额来自签到记录。
	rangeStart := checkinRangeStart(days)
	var rangeGranted sql.NullFloat64
	if err := scanSingleRow(ctx, exec, `
		SELECT COALESCE(SUM(total_amount), 0)
		FROM user_checkin_records
		WHERE checkin_date >= $1
	`, []any{rangeStart}, &rangeGranted); err != nil {
		return service.AdminCheckinStats{}, fmt.Errorf("sum checkin granted: %w", err)
	}
	if rangeGranted.Valid {
		stats.RangeTotalGranted = rangeGranted.Float64
	}

	// 消耗率与过期率来自限时余额批次（签到发放口径：优先按关联签到记录自然日归集，兼容孤立批次）。
	rangeStartUTC := rangeStartTime(days).UTC()
	var (
		sumInitial  sql.NullFloat64
		sumConsumed sql.NullFloat64
		sumExpired  sql.NullFloat64
	)
	if err := scanSingleRow(ctx, exec, `
		SELECT COALESCE(SUM(l.initial_amount), 0), COALESCE(SUM(l.consumed_amount), 0), COALESCE(SUM(l.expired_amount), 0)
		FROM user_expiring_balance_lots l
		INNER JOIN user_checkin_records r
		        ON l.source_type = $1 AND l.source_id = r.id
		WHERE r.checkin_date >= $2
	`, []any{service.ExpiringLotSourceCheckin, rangeStart}, &sumInitial, &sumConsumed, &sumExpired); err != nil {
		if isMissingExpiringBalanceTable(err, expiringSQLDialect(ctx, exec)) {
			return stats, nil
		}
		return service.AdminCheckinStats{}, fmt.Errorf("sum expiring lot stats: %w", err)
	}

	var (
		orphanInitial  sql.NullFloat64
		orphanConsumed sql.NullFloat64
		orphanExpired  sql.NullFloat64
	)
	if err := scanSingleRow(ctx, exec, `
		SELECT COALESCE(SUM(l.initial_amount), 0), COALESCE(SUM(l.consumed_amount), 0), COALESCE(SUM(l.expired_amount), 0)
		FROM user_expiring_balance_lots l
		WHERE l.source_type = $1
		  AND l.created_at >= $2
		  AND NOT EXISTS (
		      SELECT 1 FROM user_checkin_records r WHERE r.id = l.source_id
		  )
	`, []any{service.ExpiringLotSourceCheckin, rangeStartUTC}, &orphanInitial, &orphanConsumed, &orphanExpired); err != nil {
		if isMissingExpiringBalanceTable(err, expiringSQLDialect(ctx, exec)) {
			return stats, nil
		}
		return service.AdminCheckinStats{}, fmt.Errorf("sum orphan expiring lot stats: %w", err)
	}

	totalInitial := 0.0
	if sumInitial.Valid {
		totalInitial += sumInitial.Float64
	}
	if orphanInitial.Valid {
		totalInitial += orphanInitial.Float64
	}
	if sumConsumed.Valid {
		stats.RangeTotalConsumed += sumConsumed.Float64
	}
	if orphanConsumed.Valid {
		stats.RangeTotalConsumed += orphanConsumed.Float64
	}
	if sumExpired.Valid {
		stats.RangeTotalExpired += sumExpired.Float64
	}
	if orphanExpired.Valid {
		stats.RangeTotalExpired += orphanExpired.Float64
	}
	stats.RangeTotalConsumed = service.QuantizeUsageBillingAmount(stats.RangeTotalConsumed)
	stats.RangeTotalExpired = service.QuantizeUsageBillingAmount(stats.RangeTotalExpired)
	if totalInitial > 0 {
		stats.ConsumptionRate = roundRate(stats.RangeTotalConsumed / totalInitial)
		stats.ExpirationRate = roundRate(stats.RangeTotalExpired / totalInitial)
	}

	trend, err := adminCheckinDailyTrend(ctx, exec, days)
	if err != nil {
		return service.AdminCheckinStats{}, err
	}
	stats.DailyTrend = trend
	return stats, nil
}

// adminCheckinDailyTrend 返回近 days 天的每日签到人数、发放额、消耗额与过期额。
func adminCheckinDailyTrend(ctx context.Context, exec sqlExecutor, days int) ([]service.AdminCheckinDailyTrend, error) {
	rangeStart := checkinRangeStart(days)
	rangeStartUTC := rangeStartTime(days).UTC()

	rows, err := exec.QueryContext(ctx, `
		SELECT checkin_date, COUNT(*), COALESCE(SUM(total_amount), 0)
		FROM user_checkin_records
		WHERE checkin_date >= $1
		GROUP BY checkin_date
		ORDER BY checkin_date ASC
	`, rangeStart)
	if err != nil {
		return nil, fmt.Errorf("group checkin daily trend: %w", err)
	}
	defer func() { _ = rows.Close() }()

	byDate := make(map[string]*service.AdminCheckinDailyTrend, days)
	for rows.Next() {
		var (
			day     flexTime
			count   int
			granted float64
		)
		if err := rows.Scan(&day, &count, &granted); err != nil {
			return nil, fmt.Errorf("scan checkin daily trend: %w", err)
		}
		key := day.Time.Format(checkinDateLayout)
		byDate[key] = &service.AdminCheckinDailyTrend{
			Date:          key,
			CheckinCount:  count,
			GrantedAmount: granted,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate checkin daily trend: %w", err)
	}

	dialectName := expiringSQLDialect(ctx, exec)
	lotRows, err := exec.QueryContext(ctx, `
		SELECT r.checkin_date,
		       COALESCE(SUM(l.consumed_amount), 0),
		       COALESCE(SUM(l.expired_amount), 0)
		FROM user_expiring_balance_lots l
		INNER JOIN user_checkin_records r
		        ON l.source_type = $1 AND l.source_id = r.id
		WHERE r.checkin_date >= $2
		GROUP BY r.checkin_date
	`, service.ExpiringLotSourceCheckin, rangeStart)
	if err != nil {
		if isMissingExpiringBalanceTable(err, dialectName) {
			return sortedCheckinTrend(byDate), nil
		}
		return nil, fmt.Errorf("group expiring lot trend: %w", err)
	}
	defer func() { _ = lotRows.Close() }()

	for lotRows.Next() {
		var (
			day      flexTime
			consumed float64
			expired  float64
		)
		if err := lotRows.Scan(&day, &consumed, &expired); err != nil {
			return nil, fmt.Errorf("scan expiring lot trend: %w", err)
		}
		key := day.Time.Format(checkinDateLayout)
		item, ok := byDate[key]
		if !ok {
			item = &service.AdminCheckinDailyTrend{Date: key}
			byDate[key] = item
		}
		item.ConsumedAmount = service.QuantizeUsageBillingAmount(item.ConsumedAmount + consumed)
		item.ExpiredAmount = service.QuantizeUsageBillingAmount(item.ExpiredAmount + expired)
	}
	if err := lotRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expiring lot trend: %w", err)
	}

	orphanRows, err := exec.QueryContext(ctx, `
		SELECT l.created_at,
		       l.consumed_amount,
		       l.expired_amount
		FROM user_expiring_balance_lots l
		WHERE l.source_type = $1
		  AND l.created_at >= $2
		  AND NOT EXISTS (
		      SELECT 1 FROM user_checkin_records r WHERE r.id = l.source_id
		  )
	`, service.ExpiringLotSourceCheckin, rangeStartUTC)
	if err != nil {
		if isMissingExpiringBalanceTable(err, dialectName) {
			return sortedCheckinTrend(byDate), nil
		}
		return nil, fmt.Errorf("query orphan expiring lot trend: %w", err)
	}
	defer func() { _ = orphanRows.Close() }()

	loc := timezone.Location()
	for orphanRows.Next() {
		var (
			createdAt flexTime
			consumed  float64
			expired   float64
		)
		if err := orphanRows.Scan(&createdAt, &consumed, &expired); err != nil {
			return nil, fmt.Errorf("scan orphan expiring lot trend: %w", err)
		}
		key := createdAt.Time.In(loc).Format(checkinDateLayout)
		item, ok := byDate[key]
		if !ok {
			item = &service.AdminCheckinDailyTrend{Date: key}
			byDate[key] = item
		}
		item.ConsumedAmount = service.QuantizeUsageBillingAmount(item.ConsumedAmount + consumed)
		item.ExpiredAmount = service.QuantizeUsageBillingAmount(item.ExpiredAmount + expired)
	}
	if err := orphanRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orphan expiring lot trend: %w", err)
	}

	return sortedCheckinTrend(byDate), nil
}

func sortedCheckinTrend(byDate map[string]*service.AdminCheckinDailyTrend) []service.AdminCheckinDailyTrend {
	out := make([]service.AdminCheckinDailyTrend, 0, len(byDate))
	for _, item := range byDate {
		out = append(out, *item)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Date > out[j].Date; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// scanCheckinRecordRow 查询单行并扫描出一条签到记录。
func scanCheckinRecordRow(ctx context.Context, exec sqlExecutor, query string, args []any) (*service.CheckinRecord, error) {
	rows, err := exec.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, sql.ErrNoRows
	}
	return scanCheckinRecord(rows)
}

// scanCheckinRecord 从行扫描出一条签到记录（兼容 *sql.Row 与 *sql.Rows）。
func scanCheckinRecord(scanner interface{ Scan(dest ...any) error }) (*service.CheckinRecord, error) {
	var (
		record    service.CheckinRecord
		day       flexTime
		expiresAt flexTime
		createdAt flexTime
	)
	if err := scanner.Scan(
		&record.ID, &record.UserID, &day, &record.StreakDays, &record.MilestoneCycleDay,
		&record.BaseAmount, &record.MilestoneBonusAmount, &record.TotalAmount, &record.ValidityDays,
		&expiresAt, &record.ClientIP, &createdAt,
	); err != nil {
		return nil, err
	}
	record.CheckinDate = day.Time.Format(checkinDateLayout)
	record.ExpiresAt = expiresAt.Time
	record.CreatedAt = createdAt.Time
	return &record, nil
}

func roundRate(v float64) float64 {
	if v <= 0 {
		return 0
	}
	return float64(int64(v*10000+0.5)) / 10000
}

// checkinPages 计算总页数。
func checkinPages(total int64, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	return int(math.Ceil(float64(total) / float64(pageSize)))
}

// checkinRangeStart 返回近 days 天区间的起始自然日（含当天）。
func checkinRangeStart(days int) string {
	return rangeStartTime(days).Format(checkinDateLayout)
}

// rangeStartTime 返回近 days 天区间起点的 00:00（全局时区）。
func rangeStartTime(days int) time.Time {
	if days <= 0 {
		days = 30
	}
	start := timezone.StartOfDay(timezone.Now()).AddDate(0, 0, -(days - 1))
	return start
}
