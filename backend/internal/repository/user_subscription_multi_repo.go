package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func isMissingSubscriptionSnapshotTable(err error, dialectName string) bool {
	if err == nil || dialectName == dialect.Postgres {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table: user_subscription_snapshots") ||
		strings.Contains(msg, "no such table: subscription_templates")
}

// ListActiveByUserIDAndGroupID returns all active, non-expired subscriptions for (userID, groupID)
// ordered strictly by FEFO (expires_at ASC, created_at ASC, id ASC), with snapshotted limits attached.
func (r *userSubscriptionRepository) ListActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) ([]service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	subs, err := client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(userID),
			usersubscription.GroupIDEQ(groupID),
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		WithGroup().
		Order(
			dbent.Asc(usersubscription.FieldExpiresAt),
			dbent.Asc(usersubscription.FieldCreatedAt),
			dbent.Asc(usersubscription.FieldID),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := userSubscriptionEntitiesToService(subs)
	if err := r.attachSubscriptionSnapshots(ctx, client, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListActiveByUserIDAndTemplateID returns active, non-expired subscriptions belonging to a template
// ordered by latest expiry first (used for extend / negative-day reduction).
func (r *userSubscriptionRepository) ListActiveByUserIDAndTemplateID(ctx context.Context, userID, templateID int64) ([]service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	ids, err := r.listSubscriptionIDsByUserAndTemplate(ctx, client, userID, templateID, true)
	if err != nil || len(ids) == 0 {
		return []service.UserSubscription{}, err
	}
	subs, err := client.UserSubscription.Query().
		Where(
			usersubscription.IDIn(ids...),
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		WithGroup().
		Order(
			dbent.Desc(usersubscription.FieldExpiresAt),
			dbent.Desc(usersubscription.FieldID),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := userSubscriptionEntitiesToService(subs)
	if err := r.attachSubscriptionSnapshots(ctx, client, out); err != nil {
		return nil, err
	}
	return out, nil
}

// FindLatestByUserIDAndTemplateID returns the user's latest non-deleted subscription created from templateID.
func (r *userSubscriptionRepository) FindLatestByUserIDAndTemplateID(ctx context.Context, userID, templateID int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	ids, err := r.listSubscriptionIDsByUserAndTemplate(ctx, client, userID, templateID, false)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, service.ErrSubscriptionNotFound
	}
	m, err := client.UserSubscription.Query().
		Where(usersubscription.IDIn(ids...)).
		WithGroup().
		Order(
			dbent.Desc(usersubscription.FieldExpiresAt),
			dbent.Desc(usersubscription.FieldID),
		).
		First(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	out := userSubscriptionEntityToService(m)
	if err := r.attachSubscriptionSnapshotSingle(ctx, client, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *userSubscriptionRepository) listSubscriptionIDsByUserAndTemplate(
	ctx context.Context,
	client *dbent.Client,
	userID, templateID int64,
	activeOnly bool,
) ([]int64, error) {
	d := client.Driver().Dialect()
	query := fmt.Sprintf(`
		SELECT us.id
		FROM user_subscriptions us
		JOIN user_subscription_snapshots uss ON uss.subscription_id = us.id
		WHERE us.user_id = %s AND uss.template_id = %s AND us.deleted_at IS NULL`,
		sqlPlaceholder(d, 1), sqlPlaceholder(d, 2))
	args := []any{userID, templateID}
	if activeOnly {
		query += fmt.Sprintf(` AND us.status = %s AND us.expires_at > %s`,
			sqlPlaceholder(d, 3), sqlPlaceholder(d, 4))
		args = append(args, service.SubscriptionStatusActive, time.Now().UTC())
	}
	query += " ORDER BY us.expires_at DESC, us.id DESC"
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingSubscriptionSnapshotTable(err, d) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, scanErr
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// UpsertSnapshot persists or updates the 1:1 limit snapshot for a UserSubscription.
func (r *userSubscriptionRepository) UpsertSnapshot(ctx context.Context, snap *service.UserSubscriptionSnapshot) error {
	if snap == nil || snap.SubscriptionID <= 0 {
		return nil
	}
	client := clientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	hours := snap.HourlyWindowHours
	if hours <= 0 {
		hours = 5
	}
	now := time.Now().UTC()
	query := fmt.Sprintf(`
		INSERT INTO user_subscription_snapshots (
			subscription_id, template_id, template_name,
			daily_limit_usd, weekly_limit_usd, monthly_limit_usd, hourly_limit_usd,
			hourly_window_hours, updated_at
		) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
		ON CONFLICT (subscription_id) DO UPDATE SET
			template_id = EXCLUDED.template_id,
			template_name = EXCLUDED.template_name,
			daily_limit_usd = EXCLUDED.daily_limit_usd,
			weekly_limit_usd = EXCLUDED.weekly_limit_usd,
			monthly_limit_usd = EXCLUDED.monthly_limit_usd,
			hourly_limit_usd = EXCLUDED.hourly_limit_usd,
			hourly_window_hours = EXCLUDED.hourly_window_hours,
			updated_at = EXCLUDED.updated_at`,
		sqlPlaceholder(d, 1), sqlPlaceholder(d, 2), sqlPlaceholder(d, 3),
		sqlPlaceholder(d, 4), sqlPlaceholder(d, 5), sqlPlaceholder(d, 6), sqlPlaceholder(d, 7),
		sqlPlaceholder(d, 8), sqlPlaceholder(d, 9),
	)
	var tplArg, dailyArg, weeklyArg, monthlyArg, hourlyArg any
	if snap.TemplateID != nil {
		tplArg = *snap.TemplateID
	}
	if snap.DailyLimitUSD != nil {
		dailyArg = *snap.DailyLimitUSD
	}
	if snap.WeeklyLimitUSD != nil {
		weeklyArg = *snap.WeeklyLimitUSD
	}
	if snap.MonthlyLimitUSD != nil {
		monthlyArg = *snap.MonthlyLimitUSD
	}
	if snap.HourlyLimitUSD != nil {
		hourlyArg = *snap.HourlyLimitUSD
	}
	_, err := client.ExecContext(ctx, query,
		snap.SubscriptionID, tplArg, snap.TemplateName,
		dailyArg, weeklyArg, monthlyArg, hourlyArg,
		hours, now,
	)
	if isMissingSubscriptionSnapshotTable(err, d) {
		return nil
	}
	return err
}

// GetSnapshotsBySubscriptionIDs loads snapshots in one batch query.
func (r *userSubscriptionRepository) GetSnapshotsBySubscriptionIDs(ctx context.Context, subIDs []int64) (map[int64]*service.UserSubscriptionSnapshot, error) {
	return loadSubscriptionSnapshots(ctx, clientFromContext(ctx, r.client), subIDs)
}

func loadSubscriptionSnapshots(ctx context.Context, client *dbent.Client, subIDs []int64) (map[int64]*service.UserSubscriptionSnapshot, error) {
	out := make(map[int64]*service.UserSubscriptionSnapshot)
	if client == nil || len(subIDs) == 0 {
		return out, nil
	}
	d := client.Driver().Dialect()
	unique := uniqueInt64s(subIDs)
	args := make([]any, len(unique))
	for i, id := range unique {
		args[i] = id
	}
	query := fmt.Sprintf(`
		SELECT subscription_id, template_id, template_name,
			daily_limit_usd, weekly_limit_usd, monthly_limit_usd, hourly_limit_usd,
			hourly_window_hours, updated_at
		FROM user_subscription_snapshots
		WHERE subscription_id IN (%s)`, sqlInPlaceholders(d, 1, len(unique)))
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingSubscriptionSnapshotTable(err, d) {
			return out, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			snap                         service.UserSubscriptionSnapshot
			tplID                        sql.NullInt64
			daily, weekly, monthly, hour sql.NullFloat64
		)
		if scanErr := rows.Scan(
			&snap.SubscriptionID, &tplID, &snap.TemplateName,
			&daily, &weekly, &monthly, &hour,
			&snap.HourlyWindowHours, &snap.UpdatedAt,
		); scanErr != nil {
			return nil, scanErr
		}
		if tplID.Valid {
			v := tplID.Int64
			snap.TemplateID = &v
		}
		if daily.Valid {
			v := daily.Float64
			snap.DailyLimitUSD = &v
		}
		if weekly.Valid {
			v := weekly.Float64
			snap.WeeklyLimitUSD = &v
		}
		if monthly.Valid {
			v := monthly.Float64
			snap.MonthlyLimitUSD = &v
		}
		if hour.Valid {
			v := hour.Float64
			snap.HourlyLimitUSD = &v
		}
		cp := snap
		out[snap.SubscriptionID] = &cp
	}
	return out, rows.Err()
}

func (r *userSubscriptionRepository) attachSubscriptionSnapshots(ctx context.Context, client *dbent.Client, subs []service.UserSubscription) error {
	if len(subs) == 0 || client == nil {
		return nil
	}
	ids := make([]int64, 0, len(subs))
	for i := range subs {
		if subs[i].ID > 0 {
			ids = append(ids, subs[i].ID)
		}
	}
	snaps, err := loadSubscriptionSnapshots(ctx, client, ids)
	if err != nil {
		return err
	}
	for i := range subs {
		if snap, ok := snaps[subs[i].ID]; ok {
			subs[i].ApplySnapshot(snap)
		}
	}
	return nil
}

func (r *userSubscriptionRepository) attachSubscriptionSnapshotSingle(ctx context.Context, client *dbent.Client, sub *service.UserSubscription) error {
	if sub == nil || sub.ID <= 0 || client == nil {
		return nil
	}
	snaps, err := loadSubscriptionSnapshots(ctx, client, []int64{sub.ID})
	if err != nil {
		return err
	}
	if snap, ok := snaps[sub.ID]; ok {
		sub.ApplySnapshot(snap)
	}
	return nil
}

var _ service.UserSubscriptionMultiRepository = (*userSubscriptionRepository)(nil)
