package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"entgo.io/ent/dialect"
)

func selectAuthGroupFields(q *dbent.GroupQuery) {
	q.Select(
		group.FieldID,
		group.FieldName,
		group.FieldPlatform,
		group.FieldIsExclusive,
		group.FieldStatus,
		group.FieldSubscriptionType,
		group.FieldRateMultiplier,
		group.FieldHourlyLimitUsd,
		group.FieldHourlyWindowHours,
		group.FieldDailyLimitUsd,
		group.FieldWeeklyLimitUsd,
		group.FieldMonthlyLimitUsd,
		group.FieldAllowImageGeneration,
		group.FieldAllowBatchImageGeneration,
		group.FieldImageCountBilling,
		group.FieldImageRateIndependent,
		group.FieldImageRateMultiplier,
		group.FieldImagePrice1k,
		group.FieldImagePrice2k,
		group.FieldImagePrice4k,
		group.FieldVideoRateIndependent,
		group.FieldVideoRateMultiplier,
		group.FieldVideoPrice480p,
		group.FieldVideoPrice720p,
		group.FieldVideoPrice1080p,
		group.FieldVideoModelPrices,
		group.FieldWebSearchPricePerCall,
		group.FieldSearchPricePer1k,
		group.FieldAudioRealtimePricePerMin,
		group.FieldAudioTtsPricePerMillionChars,
		group.FieldAudioSttPricePerHour,
		group.FieldLongContextPricingEnabled,
		group.FieldModelPricing,
		group.FieldClaudeCodeOnly,
		group.FieldFallbackGroupID,
		group.FieldFallbackGroupIDOnInvalidRequest,
		group.FieldModelRoutingEnabled,
		group.FieldModelRouting,
		group.FieldMcpXMLInject,
		group.FieldSupportedModelScopes,
		group.FieldAllowMessagesDispatch,
		group.FieldAllowLive,
		group.FieldForceOpenaiFast,
		group.FieldForceOpenaiUltrafast,
		group.FieldFreeOpenaiFast,
		group.FieldDefaultMappedModel,
		group.FieldMessagesDispatchModelConfig,
		group.FieldModelAllowlist,
		group.FieldCodexModelsManifestConfig,
		group.FieldRpmLimit,
		group.FieldMaxReasoningEffort,
		group.FieldMaxReasoningEffortOverLimit,
		group.FieldReasoningEffortMappings,
		group.FieldPeakRateEnabled,
		group.FieldPeakStart,
		group.FieldPeakEnd,
		group.FieldPeakRateMultiplier,
		group.FieldProfitControlEnabled,
		group.FieldProfitMinMargin,
		group.FieldProfitSafetyBuffer,
		group.FieldRequirePrivacySet,
	)
}

func sqlPlaceholder(dialectName string, index int) string {
	if dialectName == dialect.Postgres {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func sqlInPlaceholders(dialectName string, startIndex, count int) string {
	parts := make([]string, count)
	for i := 0; i < count; i++ {
		parts[i] = sqlPlaceholder(dialectName, startIndex+i)
	}
	return strings.Join(parts, ", ")
}

func isMissingAPIKeyGroupsTable(err error, dialectName string) bool {
	if err == nil || dialectName == dialect.Postgres {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table: api_key_groups")
}

func deduplicatePositiveGroupIDs(groupIDs []int64) []int64 {
	if len(groupIDs) == 0 {
		return nil
	}
	out := make([]int64, 0, len(groupIDs))
	seen := make(map[int64]struct{}, len(groupIDs))
	for _, id := range groupIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func withAPIKeyGroupTx(ctx context.Context, client *dbent.Client, write func(context.Context, *dbent.Client) error) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return write(ctx, tx.Client())
	}
	tx, err := client.Tx(ctx)
	if errors.Is(err, dbent.ErrTxStarted) {
		return write(ctx, client)
	}
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := write(dbent.NewTxContext(ctx, tx), tx.Client()); err != nil {
		return err
	}
	return tx.Commit()
}

func lockAPIKeyBindingGroups(ctx context.Context, client *dbent.Client, groupIDs []int64) error {
	if len(groupIDs) == 0 || client.Driver().Dialect() != dialect.Postgres {
		return nil
	}
	args := make([]any, len(groupIDs))
	for idx, id := range groupIDs {
		args[idx] = id
	}
	query := "SELECT id FROM groups WHERE deleted_at IS NULL AND id IN (" +
		sqlInPlaceholders(dialect.Postgres, 1, len(groupIDs)) + ") ORDER BY id FOR KEY SHARE"
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	locked := 0
	for rows.Next() {
		locked++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if locked != len(groupIDs) {
		return service.ErrGroupNotFound
	}
	return nil
}

func syncAPIKeyGroups(ctx context.Context, client *dbent.Client, apiKeyID int64, groupIDs []int64) error {
	dialectName := client.Driver().Dialect()
	deleteQuery := fmt.Sprintf("DELETE FROM api_key_groups WHERE api_key_id = %s", sqlPlaceholder(dialectName, 1))
	deleteArgs := []any{apiKeyID}
	if len(groupIDs) > 0 {
		deleteQuery += " AND group_id NOT IN (" + sqlInPlaceholders(dialectName, 2, len(groupIDs)) + ")"
		for _, id := range groupIDs {
			deleteArgs = append(deleteArgs, id)
		}
	}
	if _, err := client.ExecContext(ctx, deleteQuery, deleteArgs...); err != nil {
		if isMissingAPIKeyGroupsTable(err, dialectName) && len(groupIDs) <= 1 {
			return nil
		}
		return err
	}
	if len(groupIDs) == 0 {
		return nil
	}

	values := make([]string, 0, len(groupIDs))
	args := make([]any, 0, len(groupIDs)*3)
	argIdx := 1
	for priority, gid := range groupIDs {
		p1 := sqlPlaceholder(dialectName, argIdx)
		p2 := sqlPlaceholder(dialectName, argIdx+1)
		p3 := sqlPlaceholder(dialectName, argIdx+2)
		argIdx += 3
		values = append(values, fmt.Sprintf("(%s, %s, %s)", p1, p2, p3))
		args = append(args, apiKeyID, gid, priority)
	}
	insertQuery := "INSERT INTO api_key_groups (api_key_id, group_id, priority) VALUES " + strings.Join(values, ", ") +
		" ON CONFLICT (api_key_id, group_id) DO UPDATE SET priority = EXCLUDED.priority"
	_, err := client.ExecContext(ctx, insertQuery, args...)
	return err
}

func (r *apiKeyRepository) attachBoundGroups(ctx context.Context, client *dbent.Client, keys []*service.APIKey, authProjection bool) error {
	if len(keys) == 0 || client == nil {
		return nil
	}
	keyIDs := make([]int64, 0, len(keys))
	groupByID := make(map[int64]*service.Group, len(keys))
	for _, k := range keys {
		if k == nil {
			continue
		}
		if k.ID > 0 {
			keyIDs = append(keyIDs, k.ID)
		}
		if k.Group != nil && k.Group.ID > 0 {
			groupByID[k.Group.ID] = k.Group
		}
	}
	if len(keyIDs) == 0 {
		return nil
	}

	dialectName := client.Driver().Dialect()
	args := make([]any, 0, len(keyIDs))
	for _, id := range keyIDs {
		args = append(args, id)
	}
	query := fmt.Sprintf(
		"SELECT api_key_id, group_id FROM api_key_groups WHERE api_key_id IN (%s) ORDER BY api_key_id ASC, priority ASC, group_id ASC",
		sqlInPlaceholders(dialectName, 1, len(keyIDs)),
	)
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingAPIKeyGroupsTable(err, dialectName) {
			for _, k := range keys {
				applySingleGroupFallback(k)
			}
			return nil
		}
		return err
	}

	orderedByKey := make(map[int64][]int64, len(keyIDs))
	missingMap := make(map[int64]struct{})
	var missingIDs []int64
	for rows.Next() {
		var keyID, gid int64
		if scanErr := rows.Scan(&keyID, &gid); scanErr != nil {
			_ = rows.Close()
			return scanErr
		}
		orderedByKey[keyID] = append(orderedByKey[keyID], gid)
		if _, loaded := groupByID[gid]; !loaded {
			if _, seen := missingMap[gid]; !seen {
				missingMap[gid] = struct{}{}
				missingIDs = append(missingIDs, gid)
			}
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return rowsErr
	}
	if closeErr := rows.Close(); closeErr != nil {
		return closeErr
	}

	if len(missingIDs) > 0 {
		gq := client.Group.Query().Where(group.IDIn(missingIDs...))
		if authProjection {
			selectAuthGroupFields(gq)
		}
		loadedGroups, err := gq.All(ctx)
		if err != nil {
			return err
		}
		for _, g := range loadedGroups {
			if svcGroup := groupEntityToService(g); svcGroup != nil {
				groupByID[svcGroup.ID] = svcGroup
			}
		}
	}

	for _, k := range keys {
		if k == nil {
			continue
		}
		ids := orderedByKey[k.ID]
		if len(ids) == 0 {
			applySingleGroupFallback(k)
			continue
		}
		k.GroupIDs = ids
		k.Groups = make([]*service.Group, 0, len(ids))
		for _, gid := range ids {
			if g, ok := groupByID[gid]; ok && g != nil {
				k.Groups = append(k.Groups, g)
			}
		}
		k.GroupID = &ids[0]
		k.Group = groupByID[ids[0]]
	}
	return nil
}

func applySingleGroupFallback(k *service.APIKey) {
	if k == nil {
		return
	}
	if len(k.GroupIDs) == 0 && k.GroupID != nil && *k.GroupID > 0 {
		k.GroupIDs = []int64{*k.GroupID}
	}
	if len(k.Groups) == 0 && k.Group != nil {
		k.Groups = []*service.Group{k.Group}
	}
}

func (r *apiKeyRepository) listAPIKeyIDsByGroupID(ctx context.Context, client *dbent.Client, groupID int64) ([]int64, error) {
	if client == nil || groupID <= 0 {
		return nil, nil
	}
	dialectName := client.Driver().Dialect()
	query := fmt.Sprintf("SELECT api_key_id FROM api_key_groups WHERE group_id = %s", sqlPlaceholder(dialectName, 1))
	rows, err := client.QueryContext(ctx, query, groupID)
	if err != nil {
		if isMissingAPIKeyGroupsTable(err, dialectName) {
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

type apiKeyGroupBinding struct {
	id      int64
	primary sql.NullInt64
}

func lockAPIKeysBoundToGroup(ctx context.Context, client *dbent.Client, groupID int64, userID *int64) ([]apiKeyGroupBinding, error) {
	dialectName := client.Driver().Dialect()
	groupArg := sqlPlaceholder(dialectName, 1)
	query := fmt.Sprintf(`
		SELECT ak.id, ak.group_id
		FROM api_keys ak
		WHERE ak.deleted_at IS NULL
		  AND (ak.group_id = %[1]s OR EXISTS (
			SELECT 1 FROM api_key_groups akg WHERE akg.api_key_id = ak.id AND akg.group_id = %[1]s
		  ))`, groupArg)
	args := []any{groupID}
	if dialectName != dialect.Postgres {
		args = append(args, groupID)
	}
	if userID != nil {
		query += " AND ak.user_id = " + sqlPlaceholder(dialectName, len(args)+1)
		args = append(args, *userID)
	}
	query += " ORDER BY ak.id"
	if dialectName == dialect.Postgres {
		query += " FOR UPDATE OF ak"
	}
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var bindings []apiKeyGroupBinding
	for rows.Next() {
		var binding apiKeyGroupBinding
		if err := rows.Scan(&binding.id, &binding.primary); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func currentAPIKeyGroupIDs(ctx context.Context, client *dbent.Client, apiKeyID int64) ([]int64, error) {
	dialectName := client.Driver().Dialect()
	query := fmt.Sprintf(`SELECT akg.group_id FROM api_key_groups akg
		JOIN groups g ON g.id = akg.group_id AND g.deleted_at IS NULL
		WHERE akg.api_key_id = %s ORDER BY akg.priority, akg.group_id`, sqlPlaceholder(dialectName, 1))
	rows, err := client.QueryContext(ctx, query, apiKeyID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var groupIDs []int64
	for rows.Next() {
		var groupID int64
		if err := rows.Scan(&groupID); err != nil {
			return nil, err
		}
		groupIDs = append(groupIDs, groupID)
	}
	return groupIDs, rows.Err()
}

func writeAPIKeyGroupBindings(ctx context.Context, client *dbent.Client, apiKeyID int64, groupIDs []int64, emptyPrimary *int64) error {
	if err := syncAPIKeyGroups(ctx, client, apiKeyID, groupIDs); err != nil {
		return err
	}
	primary := emptyPrimary
	if len(groupIDs) > 0 {
		primary = &groupIDs[0]
	}
	update := client.APIKey.Update().Where(apikey.IDEQ(apiKeyID), apikey.DeletedAtIsNil())
	if primary == nil {
		update.ClearGroupID()
	} else {
		update.SetGroupID(*primary)
	}
	_, err := update.Save(ctx)
	return err
}

func clearAPIKeyGroupsByGroupID(ctx context.Context, client *dbent.Client, groupID int64, keepDeletedPrimary bool) (int64, error) {
	dialectName := client.Driver().Dialect()
	if dialectName == dialect.Postgres {
		var lockedID int64
		if err := scanSingleRow(ctx, client, "SELECT id FROM groups WHERE id = $1 FOR UPDATE", []any{groupID}, &lockedID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	bindings, err := lockAPIKeysBoundToGroup(ctx, client, groupID, nil)
	if isMissingAPIKeyGroupsTable(err, dialectName) {
		if keepDeletedPrimary {
			return 0, nil
		}
		affected, err := client.APIKey.Update().Where(apikey.GroupIDEQ(groupID), apikey.DeletedAtIsNil()).ClearGroupID().Save(ctx)
		return int64(affected), err
	}
	if err != nil {
		return 0, err
	}
	for _, binding := range bindings {
		groupIDs, err := currentAPIKeyGroupIDs(ctx, client, binding.id)
		if err != nil {
			return 0, err
		}
		groupIDs = slices.DeleteFunc(groupIDs, func(boundID int64) bool { return boundID == groupID })
		var emptyPrimary *int64
		if keepDeletedPrimary {
			emptyPrimary = &groupID
		}
		if err := writeAPIKeyGroupBindings(ctx, client, binding.id, groupIDs, emptyPrimary); err != nil {
			return 0, err
		}
	}
	_, err = client.ExecContext(ctx, "DELETE FROM api_key_groups WHERE group_id = "+sqlPlaceholder(dialectName, 1), groupID)
	if err != nil {
		return 0, err
	}
	return int64(len(bindings)), nil
}

func migrateAPIKeyGroupsByUserAndGroup(ctx context.Context, client *dbent.Client, userID, oldGroupID, newGroupID int64) (int64, error) {
	bindings, err := lockAPIKeysBoundToGroup(ctx, client, oldGroupID, &userID)
	if isMissingAPIKeyGroupsTable(err, client.Driver().Dialect()) {
		affected, err := client.APIKey.Update().Where(apikey.UserIDEQ(userID), apikey.GroupIDEQ(oldGroupID), apikey.DeletedAtIsNil()).SetGroupID(newGroupID).Save(ctx)
		return int64(affected), err
	}
	if err != nil {
		return 0, err
	}
	for _, binding := range bindings {
		groupIDs, err := currentAPIKeyGroupIDs(ctx, client, binding.id)
		if err != nil {
			return 0, err
		}
		if binding.primary.Valid && binding.primary.Int64 == oldGroupID && !slices.Contains(groupIDs, oldGroupID) {
			groupIDs = append([]int64{oldGroupID}, groupIDs...)
		}
		for idx, groupID := range groupIDs {
			if groupID == oldGroupID {
				groupIDs[idx] = newGroupID
			}
		}
		if err := writeAPIKeyGroupBindings(ctx, client, binding.id, deduplicatePositiveGroupIDs(groupIDs), nil); err != nil {
			return 0, err
		}
	}
	return int64(len(bindings)), nil
}
