package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type multiGroupAPIKeyRepoStub struct {
	APIKeyRepository
	created *APIKey
	byID    map[int64]*APIKey
	updated *APIKey
}

func (s *multiGroupAPIKeyRepoStub) ExistsByKey(context.Context, string) (bool, error) {
	return false, nil
}

func (s *multiGroupAPIKeyRepoStub) Create(_ context.Context, key *APIKey) error {
	key.ID = 101
	cp := *key
	s.created = &cp
	if s.byID == nil {
		s.byID = make(map[int64]*APIKey)
	}
	s.byID[key.ID] = key
	return nil
}

func (s *multiGroupAPIKeyRepoStub) GetByID(_ context.Context, id int64) (*APIKey, error) {
	if k, ok := s.byID[id]; ok {
		cp := *k
		return &cp, nil
	}
	return nil, ErrAPIKeyNotFound
}

func (s *multiGroupAPIKeyRepoStub) Update(_ context.Context, key *APIKey, _ APIKeyUpdateFields) error {
	cp := *key
	s.updated = &cp
	s.byID[key.ID] = &cp
	return nil
}

type multiGroupUserRepoStub struct {
	UserRepository
	users map[int64]*User
}

func (s *multiGroupUserRepoStub) GetByID(_ context.Context, id int64) (*User, error) {
	if u, ok := s.users[id]; ok {
		return u, nil
	}
	return nil, ErrUserNotFound
}

type multiGroupGroupRepoStub struct {
	GroupRepository
	groups map[int64]*Group
}

func (s *multiGroupGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	if g, ok := s.groups[id]; ok {
		return g, nil
	}
	return nil, ErrGroupNotFound
}

func TestAPIKeyService_MultiGroupCreateAndUpdateOrder(t *testing.T) {
	ctx := context.Background()
	user := &User{ID: 7, Status: StatusActive, Role: RoleUser, AllowedGroups: []int64{10}}
	keyRepo := &multiGroupAPIKeyRepoStub{}
	userRepo := &multiGroupUserRepoStub{users: map[int64]*User{7: user}}
	groupRepo := &multiGroupGroupRepoStub{groups: map[int64]*Group{
		1:  {ID: 1, Name: "Claude", Platform: PlatformAnthropic, Status: StatusActive, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 1.0},
		2:  {ID: 2, Name: "GPT", Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 1.5},
		3:  {ID: 3, Name: "Disabled", Platform: PlatformOpenAI, Status: StatusDisabled, SubscriptionType: SubscriptionTypeStandard},
		10: {ID: 10, Name: "VIP-Allowed", Platform: PlatformGemini, Status: StatusActive, IsExclusive: true, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 0.8},
		20: {ID: 20, Name: "VIP-Forbidden", Platform: PlatformGrok, Status: StatusActive, IsExclusive: true, SubscriptionType: SubscriptionTypeStandard},
	}}

	svc := &APIKeyService{
		cfg:        &config.Config{},
		apiKeyRepo: keyRepo,
		userRepo:   userRepo,
		groupRepo:  groupRepo,
	}

	// Rejects if any group in the list is disallowed for user.
	_, err := svc.Create(ctx, 7, CreateAPIKeyRequest{
		Name:     "disallowed-secondary",
		GroupIDs: []int64{1, 20},
	})
	require.ErrorIs(t, err, ErrGroupNotAllowed)
	_, err = svc.Create(ctx, 7, CreateAPIKeyRequest{Name: "disabled-secondary", GroupIDs: []int64{1, 3}})
	require.Equal(t, "GROUP_NOT_ACTIVE", infraerrors.Reason(err))
	_, err = svc.Create(ctx, 7, CreateAPIKeyRequest{Name: "invalid-id", GroupIDs: []int64{1, 0}})
	require.Equal(t, "INVALID_GROUP_ID", infraerrors.Reason(err))

	// Deduplicates preserving exact first-seen order and sets primary GroupID = first.
	created, err := svc.Create(ctx, 7, CreateAPIKeyRequest{
		Name:     "ordered-key",
		GroupIDs: []int64{10, 1, 10, 2},
	})
	require.NoError(t, err)
	require.Equal(t, []int64{10, 1, 2}, created.GroupIDs)
	require.NotNil(t, created.GroupID)
	require.Equal(t, int64(10), *created.GroupID)
	require.Len(t, created.Groups, 3)
	require.Equal(t, int64(10), created.Groups[0].ID)
	require.Equal(t, int64(1), created.Groups[1].ID)
	require.Equal(t, int64(2), created.Groups[2].ID)

	// Update reorders and swaps primary group cleanly.
	reordered := []int64{2, 10}
	updated, err := svc.Update(ctx, created.ID, 7, UpdateAPIKeyRequest{
		GroupIDs: &reordered,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 10}, updated.GroupIDs)
	require.NotNil(t, updated.GroupID)
	require.Equal(t, int64(2), *updated.GroupID)
	require.Len(t, updated.Groups, 2)
	require.Equal(t, int64(2), updated.Groups[0].ID)
	disabledID := int64(3)
	_, err = svc.Update(ctx, created.ID, 7, UpdateAPIKeyRequest{GroupID: &disabledID})
	require.Equal(t, "GROUP_NOT_ACTIVE", infraerrors.Reason(err))

	// Empty GroupIDs unbinds all groups.
	empty := []int64{}
	cleared, err := svc.Update(ctx, created.ID, 7, UpdateAPIKeyRequest{
		GroupIDs: &empty,
	})
	require.NoError(t, err)
	require.Nil(t, cleared.GroupID)
	require.Nil(t, cleared.Group)
	require.Empty(t, cleared.GroupIDs)
	require.Empty(t, cleared.Groups)
}

func TestAPIKeyAuthSnapshotV30_MultiGroupRoundtrip(t *testing.T) {
	svc := &APIKeyService{}
	g1ID := int64(3)
	g1 := &Group{
		ID:                   3,
		Name:                 "G1-Anthropic",
		Platform:             PlatformAnthropic,
		Status:               StatusActive,
		Hydrated:             true,
		SubscriptionType:     SubscriptionTypeStandard,
		RateMultiplier:       0.9,
		ProfitControlEnabled: true,
		ProfitMinMargin:      0.15,
	}
	hourlyLimit := 0.25
	g2 := &Group{
		ID:                7,
		Name:              "G2-OpenAI",
		Platform:          PlatformOpenAI,
		Status:            StatusActive,
		Hydrated:          true,
		SubscriptionType:  SubscriptionTypeSubscription,
		RateMultiplier:    1.4,
		HourlyLimitUSD:    &hourlyLimit,
		HourlyWindowHours: 2,
	}
	apiKey := &APIKey{
		ID:       55,
		UserID:   9,
		Key:      "sk-snap-v29-test",
		Name:     "multi-snap",
		Status:   StatusActive,
		GroupID:  &g1ID,
		Group:    g1,
		GroupIDs: []int64{3, 7},
		Groups:   []*Group{g1, g2},
		User: &User{
			ID:          9,
			Email:       "snap@test.local",
			Status:      StatusActive,
			Role:        RoleUser,
			Balance:     10,
			Concurrency: 4,
		},
	}

	snap := svc.snapshotFromAPIKey(context.Background(), apiKey)
	require.NotNil(t, snap)
	require.Equal(t, 30, snap.Version)
	require.Equal(t, []int64{3, 7}, snap.GroupIDs)
	require.Len(t, snap.Groups, 2)

	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: snap})
	require.NoError(t, err)
	var entry APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &entry))

	restored, ok, err := svc.applyAuthCacheEntry(apiKey.Key, &entry)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []int64{3, 7}, restored.GroupIDs)
	require.Len(t, restored.Groups, 2)
	require.InDelta(t, 0.9, restored.Groups[0].RateMultiplier, 1e-9)
	require.True(t, restored.Groups[0].ProfitControlEnabled)
	require.InDelta(t, 1.4, restored.Groups[1].RateMultiplier, 1e-9)
	require.Equal(t, PlatformOpenAI, restored.Groups[1].Platform)
	require.Equal(t, &hourlyLimit, restored.Groups[1].HourlyLimitUSD)
	require.Equal(t, 2, restored.Groups[1].HourlyWindowHours)
	entry.Snapshot.Version = 29
	_, ok, err = svc.applyAuthCacheEntry(apiKey.Key, &entry)
	require.NoError(t, err)
	require.False(t, ok)
}
