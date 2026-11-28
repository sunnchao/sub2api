package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *groupRepository) plazaSQLExecutor() sqlExecutor {
	if r.sql != nil {
		return r.sql
	}
	if r.client != nil {
		return r.client
	}
	return nil
}

func isMissingGroupPlazaVisibilityTableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "group_model_plaza_visibility") &&
		(strings.Contains(msg, "does not exist") || strings.Contains(msg, "no such table"))
}

// saveGroupModelPlazaVisibility persists the optional Model Plaza showcase flag
// in the dedicated sidecar table. When show is false, any existing row is removed
// so default-false groups keep zero rows and gracefully ignore unmigrated test schemas.
func saveGroupModelPlazaVisibility(ctx context.Context, exec sqlExecutor, groupID int64, show bool) error {
	if exec == nil || groupID <= 0 {
		return nil
	}
	if show {
		_, err := exec.ExecContext(
			ctx,
			`INSERT INTO group_model_plaza_visibility (group_id, show_in_model_plaza, updated_at)
			 VALUES ($1, TRUE, CURRENT_TIMESTAMP)
			 ON CONFLICT (group_id) DO UPDATE SET
			   show_in_model_plaza = TRUE,
			   updated_at = CURRENT_TIMESTAMP`,
			groupID,
		)
		return err
	}

	_, err := exec.ExecContext(
		ctx,
		`DELETE FROM group_model_plaza_visibility WHERE group_id = $1`,
		groupID,
	)
	if isMissingGroupPlazaVisibilityTableError(err) {
		return nil
	}
	return err
}

// hydrateGroupModelPlazaVisibility batch-loads show_in_model_plaza for a slice of groups.
// Errors (such as missing sidecar table in minimal unit-test SQLite schemas) degrade to false.
func hydrateGroupModelPlazaVisibility(ctx context.Context, exec sqlExecutor, groups []service.Group) {
	if exec == nil || len(groups) == 0 {
		return
	}
	ids := make([]int64, 0, len(groups))
	seen := make(map[int64]struct{}, len(groups))
	for i := range groups {
		id := groups[i].ID
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return
	}

	flags := loadGroupModelPlazaVisibilityMap(ctx, exec, ids)
	if len(flags) == 0 {
		return
	}
	for i := range groups {
		if flags[groups[i].ID] {
			groups[i].ShowInModelPlaza = true
		}
	}
}

func hydrateSingleGroupModelPlazaVisibility(ctx context.Context, exec sqlExecutor, g *service.Group) {
	if exec == nil || g == nil || g.ID <= 0 {
		return
	}
	flags := loadGroupModelPlazaVisibilityMap(ctx, exec, []int64{g.ID})
	if flags[g.ID] {
		g.ShowInModelPlaza = true
	}
}

func loadGroupModelPlazaVisibilityMap(ctx context.Context, exec sqlExecutor, groupIDs []int64) map[int64]bool {
	if exec == nil || len(groupIDs) == 0 {
		return nil
	}
	placeholders := make([]string, len(groupIDs))
	args := make([]any, len(groupIDs))
	for i, id := range groupIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	query := fmt.Sprintf(
		`SELECT group_id, show_in_model_plaza FROM group_model_plaza_visibility WHERE group_id IN (%s)`,
		strings.Join(placeholders, ", "),
	)
	rows, err := exec.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64]bool, len(groupIDs))
	for rows.Next() {
		var groupID int64
		var show bool
		if scanErr := rows.Scan(&groupID, &show); scanErr != nil {
			continue
		}
		if show {
			result[groupID] = true
		}
	}
	return result
}
