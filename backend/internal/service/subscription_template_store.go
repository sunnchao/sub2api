package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/group"
)

func entClientFromContext(ctx context.Context, fallback *dbent.Client) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return fallback
}

func extSQLPlaceholder(dialectName string, index int) string {
	if dialectName == dialect.Postgres {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func extSQLInPlaceholders(dialectName string, startIndex, count int) string {
	parts := make([]string, count)
	for i := 0; i < count; i++ {
		parts[i] = extSQLPlaceholder(dialectName, startIndex+i)
	}
	return strings.Join(parts, ", ")
}

func isMissingExtTableErr(err error, dialectName string) bool {
	if err == nil || dialectName == dialect.Postgres {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table: subscription_templates") ||
		strings.Contains(msg, "no such table: user_subscription_snapshots") ||
		strings.Contains(msg, "no such table: subscription_plan_templates") ||
		strings.Contains(msg, "no such table: payment_order_subscriptions") ||
		strings.Contains(msg, "no such table: redeem_code_templates")
}

func nullableFloat64Ptr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func nullableInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

func nullableTimePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}

func float64PtrToSQLArg(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func int64PtrToSQLArg(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// entSubscriptionTemplateStore implements SubscriptionTemplateRepository via raw SQL
// over Ent's database driver, avoiding any ent codegen changes.
type entSubscriptionTemplateStore struct {
	client *dbent.Client
}

func newEntSubscriptionTemplateStore(client *dbent.Client) SubscriptionTemplateRepository {
	if client == nil {
		return nil
	}
	return &entSubscriptionTemplateStore{client: client}
}

func (r *entSubscriptionTemplateStore) Create(ctx context.Context, tpl *SubscriptionTemplate) error {
	if tpl == nil {
		return ErrSubscriptionTemplateInvalid
	}
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	now := time.Now().UTC()
	if tpl.Status == "" {
		tpl.Status = SubscriptionTemplateStatusActive
	}
	if tpl.HourlyWindowHours <= 0 {
		tpl.HourlyWindowHours = defaultHourlyWindowHours
	}
	if tpl.DefaultValidityDays <= 0 {
		tpl.DefaultValidityDays = 30
	}

	args := []any{
		tpl.Name,
		tpl.Description,
		tpl.GroupID,
		float64PtrToSQLArg(tpl.DailyLimitUSD),
		float64PtrToSQLArg(tpl.WeeklyLimitUSD),
		float64PtrToSQLArg(tpl.MonthlyLimitUSD),
		float64PtrToSQLArg(tpl.HourlyLimitUSD),
		tpl.HourlyWindowHours,
		tpl.DefaultValidityDays,
		tpl.Status,
		tpl.SortOrder,
		now,
		now,
	}

	if d == dialect.Postgres {
		query := `
			INSERT INTO subscription_templates (
				name, description, group_id,
				daily_limit_usd, weekly_limit_usd, monthly_limit_usd, hourly_limit_usd,
				hourly_window_hours, default_validity_days, status, sort_order, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			RETURNING id, created_at, updated_at`
		rows, err := client.QueryContext(ctx, query, args...)
		if err != nil {
			if isUniqueConstraintError(err) {
				return ErrSubscriptionTemplateExists
			}
			return err
		}
		defer func() { _ = rows.Close() }()
		if rows.Next() {
			if err := rows.Scan(&tpl.ID, &tpl.CreatedAt, &tpl.UpdatedAt); err != nil {
				return err
			}
		}
		return rows.Err()
	}

	query := `
		INSERT INTO subscription_templates (
			name, description, group_id,
			daily_limit_usd, weekly_limit_usd, monthly_limit_usd, hourly_limit_usd,
			hourly_window_hours, default_validity_days, status, sort_order, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`
	res, err := client.ExecContext(ctx, query, args...)
	if err != nil {
		if isUniqueConstraintError(err) {
			return ErrSubscriptionTemplateExists
		}
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	tpl.ID = id
	tpl.CreatedAt = now
	tpl.UpdatedAt = now
	return nil
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "sqlstate 23505")
}

func (r *entSubscriptionTemplateStore) GetByID(ctx context.Context, id int64) (*SubscriptionTemplate, error) {
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	query := fmt.Sprintf(`
		SELECT id, name, description, group_id,
			daily_limit_usd, weekly_limit_usd, monthly_limit_usd, hourly_limit_usd,
			hourly_window_hours, default_validity_days, status, sort_order, created_at, updated_at, deleted_at
		FROM subscription_templates
		WHERE id = %s AND deleted_at IS NULL`, extSQLPlaceholder(d, 1))
	rows, err := client.QueryContext(ctx, query, id)
	if err != nil {
		if isMissingExtTableErr(err, d) {
			return nil, ErrSubscriptionTemplateNotFound
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, ErrSubscriptionTemplateNotFound
	}
	tpl, err := scanSubscriptionTemplate(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	_ = r.attachGroups(ctx, client, []*SubscriptionTemplate{tpl})
	return tpl, nil
}

func (r *entSubscriptionTemplateStore) GetByIDs(ctx context.Context, ids []int64) (map[int64]*SubscriptionTemplate, error) {
	result := make(map[int64]*SubscriptionTemplate)
	if len(ids) == 0 {
		return result, nil
	}
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	query := fmt.Sprintf(`
		SELECT id, name, description, group_id,
			daily_limit_usd, weekly_limit_usd, monthly_limit_usd, hourly_limit_usd,
			hourly_window_hours, default_validity_days, status, sort_order, created_at, updated_at, deleted_at
		FROM subscription_templates
		WHERE id IN (%s)`, extSQLInPlaceholders(d, 1, len(ids)))
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingExtTableErr(err, d) {
			return result, nil
		}
		return nil, err
	}
	list := make([]*SubscriptionTemplate, 0, len(ids))
	for rows.Next() {
		tpl, scanErr := scanSubscriptionTemplate(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		result[tpl.ID] = tpl
		list = append(list, tpl)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	_ = r.attachGroups(ctx, client, list)
	return result, nil
}

func (r *entSubscriptionTemplateStore) Update(ctx context.Context, tpl *SubscriptionTemplate) error {
	if tpl == nil || tpl.ID <= 0 {
		return ErrSubscriptionTemplateInvalid
	}
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	now := time.Now().UTC()
	query := fmt.Sprintf(`
		UPDATE subscription_templates
		SET name = %s,
			description = %s,
			group_id = %s,
			daily_limit_usd = %s,
			weekly_limit_usd = %s,
			monthly_limit_usd = %s,
			hourly_limit_usd = %s,
			hourly_window_hours = %s,
			default_validity_days = %s,
			status = %s,
			sort_order = %s,
			updated_at = %s
		WHERE id = %s AND deleted_at IS NULL`,
		extSQLPlaceholder(d, 1), extSQLPlaceholder(d, 2), extSQLPlaceholder(d, 3),
		extSQLPlaceholder(d, 4), extSQLPlaceholder(d, 5), extSQLPlaceholder(d, 6), extSQLPlaceholder(d, 7),
		extSQLPlaceholder(d, 8), extSQLPlaceholder(d, 9), extSQLPlaceholder(d, 10), extSQLPlaceholder(d, 11),
		extSQLPlaceholder(d, 12), extSQLPlaceholder(d, 13),
	)
	res, err := client.ExecContext(ctx, query,
		tpl.Name, tpl.Description, tpl.GroupID,
		float64PtrToSQLArg(tpl.DailyLimitUSD),
		float64PtrToSQLArg(tpl.WeeklyLimitUSD),
		float64PtrToSQLArg(tpl.MonthlyLimitUSD),
		float64PtrToSQLArg(tpl.HourlyLimitUSD),
		tpl.HourlyWindowHours, tpl.DefaultValidityDays, tpl.Status, tpl.SortOrder,
		now, tpl.ID,
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return ErrSubscriptionTemplateExists
		}
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrSubscriptionTemplateNotFound
	}
	tpl.UpdatedAt = now
	return nil
}

func (r *entSubscriptionTemplateStore) Delete(ctx context.Context, id int64) error {
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	now := time.Now().UTC()
	query := fmt.Sprintf(`
		UPDATE subscription_templates
		SET deleted_at = %s, updated_at = %s
		WHERE id = %s AND deleted_at IS NULL`,
		extSQLPlaceholder(d, 1), extSQLPlaceholder(d, 2), extSQLPlaceholder(d, 3))
	res, err := client.ExecContext(ctx, query, now, now, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrSubscriptionTemplateNotFound
	}
	return nil
}

func (r *entSubscriptionTemplateStore) List(ctx context.Context, filter SubscriptionTemplateListFilter) ([]SubscriptionTemplate, error) {
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	conds := []string{"st.deleted_at IS NULL"}
	args := make([]any, 0, 4)

	if filter.GroupID != nil && *filter.GroupID > 0 {
		args = append(args, *filter.GroupID)
		conds = append(conds, fmt.Sprintf("st.group_id = %s", extSQLPlaceholder(d, len(args))))
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		args = append(args, status)
		conds = append(conds, fmt.Sprintf("st.status = %s", extSQLPlaceholder(d, len(args))))
	}
	if platform := strings.TrimSpace(filter.Platform); platform != "" {
		args = append(args, platform)
		conds = append(conds, fmt.Sprintf("g.platform = %s", extSQLPlaceholder(d, len(args))))
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		p := extSQLPlaceholder(d, len(args))
		conds = append(conds, fmt.Sprintf("(LOWER(st.name) LIKE %s OR LOWER(st.description) LIKE %s)", p, p))
		if d != dialect.Postgres {
			args = append(args, "%"+strings.ToLower(search)+"%")
		}
	}

	query := `
		SELECT st.id, st.name, st.description, st.group_id,
			st.daily_limit_usd, st.weekly_limit_usd, st.monthly_limit_usd, st.hourly_limit_usd,
			st.hourly_window_hours, st.default_validity_days, st.status, st.sort_order, st.created_at, st.updated_at, st.deleted_at
		FROM subscription_templates st
		LEFT JOIN groups g ON g.id = st.group_id
		WHERE ` + strings.Join(conds, " AND ") + `
		ORDER BY st.sort_order ASC, st.id ASC`

	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingExtTableErr(err, d) {
			return []SubscriptionTemplate{}, nil
		}
		return nil, err
	}
	out := make([]SubscriptionTemplate, 0)
	for rows.Next() {
		tpl, scanErr := scanSubscriptionTemplate(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		out = append(out, *tpl)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	ptrs := make([]*SubscriptionTemplate, len(out))
	for i := range out {
		ptrs[i] = &out[i]
	}
	_ = r.attachGroups(ctx, client, ptrs)
	return out, nil
}

func (r *entSubscriptionTemplateStore) ExistsActiveByName(ctx context.Context, name string, excludeID int64) (bool, error) {
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	query := fmt.Sprintf(`SELECT 1 FROM subscription_templates WHERE name = %s AND deleted_at IS NULL`, extSQLPlaceholder(d, 1))
	args := []any{name}
	if excludeID > 0 {
		query += fmt.Sprintf(` AND id <> %s`, extSQLPlaceholder(d, 2))
		args = append(args, excludeID)
	}
	query += " LIMIT 1"
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingExtTableErr(err, d) {
			return false, nil
		}
		return false, err
	}
	defer func() { _ = rows.Close() }()
	exists := rows.Next()
	return exists, rows.Err()
}

func (r *entSubscriptionTemplateStore) CountForSalePlansByTemplateID(ctx context.Context, templateID int64) (int64, error) {
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	query := fmt.Sprintf(`
		SELECT COUNT(1)
		FROM subscription_plan_templates spt
		JOIN subscription_plans sp ON sp.id = spt.plan_id
		WHERE spt.template_id = %s AND sp.for_sale = TRUE`, extSQLPlaceholder(d, 1))
	return querySingleCount(ctx, client, d, query, templateID)
}

func (r *entSubscriptionTemplateStore) CountInProgressOrdersByTemplateID(ctx context.Context, templateID int64) (int64, error) {
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	query := fmt.Sprintf(`
		SELECT COUNT(1)
		FROM payment_order_subscriptions pos
		JOIN payment_orders po ON po.id = pos.order_id
		WHERE pos.template_id = %s AND po.status IN ('PENDING', 'PAID', 'RECHARGING')`, extSQLPlaceholder(d, 1))
	return querySingleCount(ctx, client, d, query, templateID)
}

func querySingleCount(ctx context.Context, client *dbent.Client, dialectName, query string, args ...any) (int64, error) {
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingExtTableErr(err, dialectName) {
			return 0, nil
		}
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var count int64
	if rows.Next() {
		if scanErr := rows.Scan(&count); scanErr != nil {
			return 0, scanErr
		}
	}
	return count, rows.Err()
}

func scanSubscriptionTemplate(rows interface{ Scan(dest ...any) error }) (*SubscriptionTemplate, error) {
	var (
		tpl                          SubscriptionTemplate
		daily, weekly, monthly, hour sql.NullFloat64
		deletedAt                    sql.NullTime
	)
	if err := rows.Scan(
		&tpl.ID, &tpl.Name, &tpl.Description, &tpl.GroupID,
		&daily, &weekly, &monthly, &hour,
		&tpl.HourlyWindowHours, &tpl.DefaultValidityDays, &tpl.Status, &tpl.SortOrder,
		&tpl.CreatedAt, &tpl.UpdatedAt, &deletedAt,
	); err != nil {
		return nil, err
	}
	tpl.DailyLimitUSD = nullableFloat64Ptr(daily)
	tpl.WeeklyLimitUSD = nullableFloat64Ptr(weekly)
	tpl.MonthlyLimitUSD = nullableFloat64Ptr(monthly)
	tpl.HourlyLimitUSD = nullableFloat64Ptr(hour)
	tpl.DeletedAt = nullableTimePtr(deletedAt)
	if tpl.HourlyWindowHours <= 0 {
		tpl.HourlyWindowHours = defaultHourlyWindowHours
	}
	return &tpl, nil
}

func (r *entSubscriptionTemplateStore) attachGroups(ctx context.Context, client *dbent.Client, templates []*SubscriptionTemplate) error {
	if len(templates) == 0 || client == nil {
		return nil
	}
	seen := make(map[int64]struct{}, len(templates))
	ids := make([]int64, 0, len(templates))
	for _, t := range templates {
		if t == nil || t.GroupID <= 0 {
			continue
		}
		if _, ok := seen[t.GroupID]; ok {
			continue
		}
		seen[t.GroupID] = struct{}{}
		ids = append(ids, t.GroupID)
	}
	if len(ids) == 0 {
		return nil
	}
	groups, err := client.Group.Query().Where(group.IDIn(ids...)).All(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int64]*Group, len(groups))
	for _, g := range groups {
		byID[g.ID] = &Group{
			ID:                   g.ID,
			Name:                 g.Name,
			Description:          derefStringPtr(g.Description),
			I18n:                 g.I18n,
			Platform:             g.Platform,
			RateMultiplier:       g.RateMultiplier,
			PeakRateEnabled:      g.PeakRateEnabled,
			PeakStart:            g.PeakStart,
			PeakEnd:              g.PeakEnd,
			PeakRateMultiplier:   g.PeakRateMultiplier,
			IsExclusive:          g.IsExclusive,
			Status:               g.Status,
			Hydrated:             true,
			SubscriptionType:     g.SubscriptionType,
			DailyLimitUSD:        g.DailyLimitUsd,
			WeeklyLimitUSD:       g.WeeklyLimitUsd,
			MonthlyLimitUSD:      g.MonthlyLimitUsd,
			HourlyLimitUSD:       g.HourlyLimitUsd,
			HourlyWindowHours:    g.HourlyWindowHours,
			DefaultValidityDays:  g.DefaultValidityDays,
			SupportedModelScopes: g.SupportedModelScopes,
		}
	}
	for _, t := range templates {
		if t != nil {
			t.Group = byID[t.GroupID]
		}
	}
	return nil
}

func derefStringPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// entSubscriptionExtStore implements SubscriptionExtRepository over the 3 sidecar tables.
type entSubscriptionExtStore struct {
	client *dbent.Client
}

func newEntSubscriptionExtStore(client *dbent.Client) SubscriptionExtRepository {
	if client == nil {
		return nil
	}
	return &entSubscriptionExtStore{client: client}
}

func (r *entSubscriptionExtStore) SetPlanTemplate(ctx context.Context, planID, templateID int64) error {
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	query := fmt.Sprintf(`
		INSERT INTO subscription_plan_templates (plan_id, template_id)
		VALUES (%s, %s)
		ON CONFLICT (plan_id) DO UPDATE SET template_id = EXCLUDED.template_id`,
		extSQLPlaceholder(d, 1), extSQLPlaceholder(d, 2))
	_, err := client.ExecContext(ctx, query, planID, templateID)
	if isMissingExtTableErr(err, d) {
		return nil
	}
	return err
}

func (r *entSubscriptionExtStore) ClearPlanTemplate(ctx context.Context, planID int64) error {
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	query := fmt.Sprintf(`DELETE FROM subscription_plan_templates WHERE plan_id = %s`, extSQLPlaceholder(d, 1))
	_, err := client.ExecContext(ctx, query, planID)
	if isMissingExtTableErr(err, d) {
		return nil
	}
	return err
}

func (r *entSubscriptionExtStore) GetPlanTemplateID(ctx context.Context, planID int64) (*int64, error) {
	m, err := r.GetPlanTemplateIDs(ctx, []int64{planID})
	if err != nil {
		return nil, err
	}
	if tid, ok := m[planID]; ok {
		return &tid, nil
	}
	return nil, nil
}

func (r *entSubscriptionExtStore) GetPlanTemplateIDs(ctx context.Context, planIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64)
	if len(planIDs) == 0 {
		return out, nil
	}
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	args := make([]any, len(planIDs))
	for i, id := range planIDs {
		args[i] = id
	}
	query := fmt.Sprintf(`SELECT plan_id, template_id FROM subscription_plan_templates WHERE plan_id IN (%s)`, extSQLInPlaceholders(d, 1, len(planIDs)))
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingExtTableErr(err, d) {
			return out, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var planID, tplID int64
		if scanErr := rows.Scan(&planID, &tplID); scanErr != nil {
			return nil, scanErr
		}
		out[planID] = tplID
	}
	return out, rows.Err()
}

func (r *entSubscriptionExtStore) UpsertOrderSubscriptionMeta(ctx context.Context, meta *PaymentOrderSubscriptionMeta) error {
	if meta == nil || meta.OrderID <= 0 {
		return nil
	}
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	now := time.Now().UTC()
	query := fmt.Sprintf(`
		INSERT INTO payment_order_subscriptions (order_id, template_id, target_subscription_id, fulfilled_subscription_id, updated_at)
		VALUES (%s, %s, %s, %s, %s)
		ON CONFLICT (order_id) DO UPDATE SET
			template_id = EXCLUDED.template_id,
			target_subscription_id = EXCLUDED.target_subscription_id,
			fulfilled_subscription_id = COALESCE(EXCLUDED.fulfilled_subscription_id, payment_order_subscriptions.fulfilled_subscription_id),
			updated_at = EXCLUDED.updated_at`,
		extSQLPlaceholder(d, 1), extSQLPlaceholder(d, 2), extSQLPlaceholder(d, 3), extSQLPlaceholder(d, 4), extSQLPlaceholder(d, 5))
	_, err := client.ExecContext(ctx, query,
		meta.OrderID,
		int64PtrToSQLArg(meta.TemplateID),
		int64PtrToSQLArg(meta.TargetSubscriptionID),
		int64PtrToSQLArg(meta.FulfilledSubscriptionID),
		now,
	)
	if isMissingExtTableErr(err, d) {
		return nil
	}
	return err
}

func (r *entSubscriptionExtStore) GetOrderSubscriptionMeta(ctx context.Context, orderID int64) (*PaymentOrderSubscriptionMeta, error) {
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	query := fmt.Sprintf(`
		SELECT order_id, template_id, target_subscription_id, fulfilled_subscription_id
		FROM payment_order_subscriptions
		WHERE order_id = %s`, extSQLPlaceholder(d, 1))
	rows, err := client.QueryContext(ctx, query, orderID)
	if err != nil {
		if isMissingExtTableErr(err, d) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var (
		oid                   int64
		tplID, target, filled sql.NullInt64
	)
	if err := rows.Scan(&oid, &tplID, &target, &filled); err != nil {
		return nil, err
	}
	return &PaymentOrderSubscriptionMeta{
		OrderID:                 oid,
		TemplateID:              nullableInt64Ptr(tplID),
		TargetSubscriptionID:    nullableInt64Ptr(target),
		FulfilledSubscriptionID: nullableInt64Ptr(filled),
	}, nil
}

func (r *entSubscriptionExtStore) SetOrderFulfilledSubscriptionID(ctx context.Context, orderID, subscriptionID int64) error {
	if orderID <= 0 || subscriptionID <= 0 {
		return nil
	}
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	now := time.Now().UTC()
	query := fmt.Sprintf(`
		INSERT INTO payment_order_subscriptions (order_id, fulfilled_subscription_id, updated_at)
		VALUES (%s, %s, %s)
		ON CONFLICT (order_id) DO UPDATE SET
			fulfilled_subscription_id = EXCLUDED.fulfilled_subscription_id,
			updated_at = EXCLUDED.updated_at`,
		extSQLPlaceholder(d, 1), extSQLPlaceholder(d, 2), extSQLPlaceholder(d, 3))
	_, err := client.ExecContext(ctx, query, orderID, subscriptionID, now)
	if isMissingExtTableErr(err, d) {
		return nil
	}
	return err
}

func (r *entSubscriptionExtStore) SetRedeemCodeTemplate(ctx context.Context, redeemCodeID, templateID int64, mode string) error {
	return r.SetRedeemCodeTemplatesBatch(ctx, []RedeemCodeTemplateMeta{{
		RedeemCodeID: redeemCodeID,
		TemplateID:   templateID,
		RedeemMode:   mode,
	}})
}

func (r *entSubscriptionExtStore) SetRedeemCodeTemplatesBatch(ctx context.Context, metas []RedeemCodeTemplateMeta) error {
	if len(metas) == 0 {
		return nil
	}
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	values := make([]string, 0, len(metas))
	args := make([]any, 0, len(metas)*3)
	for _, m := range metas {
		if m.RedeemCodeID <= 0 || m.TemplateID <= 0 {
			continue
		}
		mode := m.RedeemMode
		if mode == "" {
			mode = RedeemSubscriptionModeStack
		}
		base := len(args)
		values = append(values, fmt.Sprintf("(%s, %s, %s)",
			extSQLPlaceholder(d, base+1), extSQLPlaceholder(d, base+2), extSQLPlaceholder(d, base+3)))
		args = append(args, m.RedeemCodeID, m.TemplateID, mode)
	}
	if len(values) == 0 {
		return nil
	}
	query := "INSERT INTO redeem_code_templates (redeem_code_id, template_id, redeem_mode) VALUES " +
		strings.Join(values, ", ") +
		" ON CONFLICT (redeem_code_id) DO UPDATE SET template_id = EXCLUDED.template_id, redeem_mode = EXCLUDED.redeem_mode"
	_, err := client.ExecContext(ctx, query, args...)
	if isMissingExtTableErr(err, d) {
		return nil
	}
	return err
}

func (r *entSubscriptionExtStore) GetRedeemCodeTemplate(ctx context.Context, redeemCodeID int64) (*RedeemCodeTemplateMeta, error) {
	m, err := r.GetRedeemCodeTemplates(ctx, []int64{redeemCodeID})
	if err != nil {
		return nil, err
	}
	return m[redeemCodeID], nil
}

func (r *entSubscriptionExtStore) GetRedeemCodeTemplates(ctx context.Context, redeemCodeIDs []int64) (map[int64]*RedeemCodeTemplateMeta, error) {
	out := make(map[int64]*RedeemCodeTemplateMeta)
	if len(redeemCodeIDs) == 0 {
		return out, nil
	}
	client := entClientFromContext(ctx, r.client)
	d := client.Driver().Dialect()
	args := make([]any, len(redeemCodeIDs))
	for i, id := range redeemCodeIDs {
		args[i] = id
	}
	query := fmt.Sprintf(`
		SELECT rct.redeem_code_id, rct.template_id, COALESCE(st.name, ''), rct.redeem_mode
		FROM redeem_code_templates rct
		LEFT JOIN subscription_templates st ON st.id = rct.template_id
		WHERE rct.redeem_code_id IN (%s)`, extSQLInPlaceholders(d, 1, len(redeemCodeIDs)))
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingExtTableErr(err, d) {
			return out, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m RedeemCodeTemplateMeta
		if scanErr := rows.Scan(&m.RedeemCodeID, &m.TemplateID, &m.TemplateName, &m.RedeemMode); scanErr != nil {
			return nil, scanErr
		}
		out[m.RedeemCodeID] = &m
	}
	return out, rows.Err()
}

var _ = errors.New
