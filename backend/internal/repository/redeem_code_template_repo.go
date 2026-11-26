package repository

import (
	"context"
	"fmt"
	"strings"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func isMissingRedeemTemplateTable(err error, dialectName string) bool {
	if err == nil || dialectName == dialect.Postgres {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table: redeem_code_templates") ||
		strings.Contains(msg, "no such table: subscription_templates")
}

func (r *redeemCodeRepository) saveRedeemCodeTemplates(ctx context.Context, client *dbent.Client, codes []service.RedeemCode) error {
	if client == nil || len(codes) == 0 {
		return nil
	}
	d := client.Driver().Dialect()
	values := make([]string, 0, len(codes))
	args := make([]any, 0, len(codes)*3)
	for i := range codes {
		c := &codes[i]
		if c.ID <= 0 || c.TemplateID == nil || *c.TemplateID <= 0 {
			continue
		}
		mode := c.RedeemMode
		if mode == "" {
			mode = service.RedeemSubscriptionModeStack
		}
		base := len(args)
		values = append(values, fmt.Sprintf("(%s, %s, %s)",
			sqlPlaceholder(d, base+1), sqlPlaceholder(d, base+2), sqlPlaceholder(d, base+3)))
		args = append(args, c.ID, *c.TemplateID, mode)
	}
	if len(values) == 0 {
		return nil
	}
	query := "INSERT INTO redeem_code_templates (redeem_code_id, template_id, redeem_mode) VALUES " +
		strings.Join(values, ", ") +
		" ON CONFLICT (redeem_code_id) DO UPDATE SET template_id = EXCLUDED.template_id, redeem_mode = EXCLUDED.redeem_mode"
	_, err := client.ExecContext(ctx, query, args...)
	if isMissingRedeemTemplateTable(err, d) {
		return nil
	}
	return err
}

func (r *redeemCodeRepository) attachRedeemCodeTemplates(ctx context.Context, client *dbent.Client, codes []service.RedeemCode) error {
	if client == nil || len(codes) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(codes))
	for i := range codes {
		if codes[i].ID > 0 {
			ids = append(ids, codes[i].ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	d := client.Driver().Dialect()
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	query := fmt.Sprintf(`
		SELECT rct.redeem_code_id, rct.template_id, COALESCE(st.name, ''), rct.redeem_mode
		FROM redeem_code_templates rct
		LEFT JOIN subscription_templates st ON st.id = rct.template_id
		WHERE rct.redeem_code_id IN (%s)`, sqlInPlaceholders(d, 1, len(ids)))
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingRedeemTemplateTable(err, d) {
			return nil
		}
		return err
	}
	defer func() { _ = rows.Close() }()

	byID := make(map[int64]service.RedeemCodeTemplateMeta, len(ids))
	for rows.Next() {
		var m service.RedeemCodeTemplateMeta
		if scanErr := rows.Scan(&m.RedeemCodeID, &m.TemplateID, &m.TemplateName, &m.RedeemMode); scanErr != nil {
			return scanErr
		}
		byID[m.RedeemCodeID] = m
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range codes {
		if m, ok := byID[codes[i].ID]; ok {
			tid := m.TemplateID
			codes[i].TemplateID = &tid
			codes[i].TemplateName = m.TemplateName
			codes[i].RedeemMode = m.RedeemMode
		}
	}
	return nil
}

func (r *redeemCodeRepository) attachRedeemCodeTemplateSingle(ctx context.Context, client *dbent.Client, code *service.RedeemCode) error {
	if code == nil || code.ID <= 0 {
		return nil
	}
	slice := []service.RedeemCode{*code}
	if err := r.attachRedeemCodeTemplates(ctx, client, slice); err != nil {
		return err
	}
	code.TemplateID = slice[0].TemplateID
	code.TemplateName = slice[0].TemplateName
	code.RedeemMode = slice[0].RedeemMode
	return nil
}
