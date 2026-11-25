package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyGroupsRepositoryLifecycle(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()

	user := mustCreateAPIKeyRepoUser(t, ctx, client, "multi-group-repo@test.com")
	createGroup := func(name, platform string, rate float64) int64 {
		g, err := client.Group.Create().
			SetName(name).
			SetPlatform(platform).
			SetStatus(service.StatusActive).
			SetSubscriptionType(service.SubscriptionTypeStandard).
			SetRateMultiplier(rate).
			Save(ctx)
		require.NoError(t, err)
		return g.ID
	}

	gA := createGroup("Group-Alpha", service.PlatformAnthropic, 1.0)
	gB := createGroup("Group-Beta", service.PlatformOpenAI, 1.5)
	gC := createGroup("Group-Gamma", service.PlatformGemini, 2.0)
	_, err := client.Group.UpdateOneID(gB).SetHourlyLimitUsd(0.25).SetHourlyWindowHours(2).Save(ctx)
	require.NoError(t, err)

	// Intentionally store reverse-ID order: [gC, gA, gB] -> priority 0, 1, 2
	key := &service.APIKey{
		UserID:   user.ID,
		Key:      "sk-multi-group-repo-1",
		Name:     "Multi Group Key",
		GroupID:  &gC,
		GroupIDs: []int64{gC, gA, gB},
		Status:   service.StatusActive,
	}
	require.NoError(t, repo.Create(ctx, key))

	got, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{gC, gA, gB}, got.GroupIDs)
	require.Len(t, got.Groups, 3)
	require.Equal(t, []int64{gC, gA, gB}, []int64{got.Groups[0].ID, got.Groups[1].ID, got.Groups[2].ID})
	require.NotNil(t, got.GroupID)
	require.Equal(t, gC, *got.GroupID)

	authKey, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.Equal(t, []int64{gC, gA, gB}, authKey.GroupIDs)
	require.Len(t, authKey.Groups, 3)
	require.InDelta(t, 1.5, authKey.Groups[2].RateMultiplier, 1e-9)
	require.NotNil(t, authKey.Groups[2].HourlyLimitUSD)
	require.InDelta(t, 0.25, *authKey.Groups[2].HourlyLimitUSD, 1e-9)
	require.Equal(t, 2, authKey.Groups[2].HourlyWindowHours)

	// Filter by secondary group (gB) returns this key
	list, page, err := repo.ListByUserID(ctx, user.ID, pagination.PaginationParams{Page: 1, PageSize: 10}, service.APIKeyListFilters{
		GroupID: &gB,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.Len(t, list, 1)
	require.Equal(t, []int64{gC, gA, gB}, list[0].GroupIDs)

	// Group membership lookups include secondary/tertiary bindings (for cache invalidation & counts)
	countB, err := repo.CountByGroupID(ctx, gB)
	require.NoError(t, err)
	require.Equal(t, int64(1), countB)

	keysInB, err := repo.ListKeysByGroupID(ctx, gB)
	require.NoError(t, err)
	require.Equal(t, []string{key.Key}, keysInB)

	byGroupB, pageB, err := repo.ListByGroupID(ctx, gB, pagination.PaginationParams{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.EqualValues(t, 1, pageB.Total)
	require.Equal(t, []int64{gC, gA, gB}, byGroupB[0].GroupIDs)

	// Clear primary group gC -> gA is promoted to primary group_id, order becomes [gA, gB]
	affected, err := repo.ClearGroupIDByGroupID(ctx, gC)
	require.NoError(t, err)
	require.Equal(t, int64(1), affected)

	afterClearC, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.NotNil(t, afterClearC.GroupID)
	require.Equal(t, gA, *afterClearC.GroupID)
	require.Equal(t, []int64{gA, gB}, afterClearC.GroupIDs)

	// Migrate user's gA -> gB when gB is ALREADY bound on the same key: must deduplicate cleanly without PK error
	migrated, err := repo.UpdateGroupIDByUserAndGroup(ctx, user.ID, gA, gB)
	require.NoError(t, err)
	require.Equal(t, int64(1), migrated)

	afterMigrate, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.NotNil(t, afterMigrate.GroupID)
	require.Equal(t, gB, *afterMigrate.GroupID)
	require.Equal(t, []int64{gB}, afterMigrate.GroupIDs)

	// Update reorders to [gC, gA]
	afterMigrate.GroupID = &gC
	afterMigrate.GroupIDs = []int64{gC, gA}
	require.NoError(t, repo.Update(ctx, afterMigrate, service.APIKeyUpdateFields{GroupID: true}))

	finalGot, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{gC, gA}, finalGot.GroupIDs)
	require.NotNil(t, finalGot.GroupID)
	require.Equal(t, gC, *finalGot.GroupID)

	finalGot.GroupID = &gA
	finalGot.GroupIDs = []int64{gA, gB, gC}
	require.NoError(t, repo.Update(ctx, finalGot, service.APIKeyUpdateFields{GroupID: true}))
	_, err = repo.UpdateGroupIDByUserAndGroup(ctx, user.ID, gA, gC)
	require.NoError(t, err)
	replaced, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{gC, gB}, replaced.GroupIDs)
	require.Equal(t, gC, *replaced.GroupID)
	var maxPriority int
	require.NoError(t, scanSingleRow(ctx, repo.sql, "SELECT MAX(priority) FROM api_key_groups WHERE api_key_id = ?", []any{key.ID}, &maxPriority))
	require.Equal(t, 1, maxPriority)

	_, err = repo.sql.ExecContext(ctx, `CREATE TRIGGER reject_api_key_secondary_binding BEFORE INSERT ON api_key_groups
		WHEN NEW.priority = 1 BEGIN SELECT RAISE(ABORT, 'test binding write failure'); END`)
	require.NoError(t, err)
	replaced.GroupID = &gA
	replaced.GroupIDs = []int64{gA, gB}
	require.Error(t, repo.Update(ctx, replaced, service.APIKeyUpdateFields{GroupID: true}))
	failedCreate := &service.APIKey{UserID: user.ID, Key: "test-rollback-key", Name: "rollback", GroupIDs: []int64{gA, gB}, Status: service.StatusActive}
	require.Error(t, repo.Create(ctx, failedCreate))
	require.Zero(t, failedCreate.ID)
	_, err = repo.GetByKey(ctx, failedCreate.Key)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
	unchanged, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, gC, *unchanged.GroupID)
	require.Equal(t, []int64{gC, gB}, unchanged.GroupIDs)
}
