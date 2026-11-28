package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/ent/compositemodelroute"
	"github.com/Wei-Shaw/sub2api/ent/customplatform"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/userplatformquota"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type customPlatformRepository struct {
	client *ent.Client
}

// NewCustomPlatformRepository 创建自定义渠道平台仓库。
func NewCustomPlatformRepository(client *ent.Client) service.CustomPlatformRepository {
	return &customPlatformRepository{client: client}
}

func (r *customPlatformRepository) List(ctx context.Context) ([]*service.CustomPlatform, error) {
	rows, err := r.client.CustomPlatform.Query().
		Order(ent.Asc(customplatform.FieldSortOrder), ent.Asc(customplatform.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*service.CustomPlatform, 0, len(rows))
	for _, row := range rows {
		platform, err := customPlatformFromEnt(row)
		if err != nil {
			return nil, err
		}
		out = append(out, platform)
	}
	return out, nil
}

func (r *customPlatformRepository) Get(ctx context.Context, id string) (*service.CustomPlatform, error) {
	row, err := r.client.CustomPlatform.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return customPlatformFromEnt(row)
}

func (r *customPlatformRepository) Exists(ctx context.Context, id string) (bool, error) {
	return r.client.CustomPlatform.Query().
		Where(customplatform.ID(id)).
		Exist(ctx)
}

func (r *customPlatformRepository) Create(ctx context.Context, platform *service.CustomPlatform) (*service.CustomPlatform, error) {
	profile, err := profileJSONMap(platform.Profile)
	if err != nil {
		return nil, err
	}
	row, err := r.client.CustomPlatform.Create().
		SetID(platform.ID).
		SetDisplayName(platform.DisplayName).
		SetEnabled(platform.Enabled).
		SetSortOrder(platform.SortOrder).
		SetIconType(platform.Icon.Type).
		SetIconValue(platform.Icon.Value).
		SetColorPreset(platform.ColorPreset).
		SetLitellmProvider(platform.LiteLLMProvider).
		SetDefaultTestModel(platform.DefaultTestModel).
		SetSupportsEmbeddings(platform.SupportsEmbeddings).
		SetSupportsImages(platform.SupportsImages).
		SetProfileJSON(profile).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return customPlatformFromEnt(row)
}

func (r *customPlatformRepository) Update(ctx context.Context, platform *service.CustomPlatform) (*service.CustomPlatform, error) {
	profile, err := profileJSONMap(platform.Profile)
	if err != nil {
		return nil, err
	}
	row, err := r.client.CustomPlatform.UpdateOneID(platform.ID).
		SetDisplayName(platform.DisplayName).
		SetEnabled(platform.Enabled).
		SetSortOrder(platform.SortOrder).
		SetIconType(platform.Icon.Type).
		SetIconValue(platform.Icon.Value).
		SetColorPreset(platform.ColorPreset).
		SetLitellmProvider(platform.LiteLLMProvider).
		SetDefaultTestModel(platform.DefaultTestModel).
		SetSupportsEmbeddings(platform.SupportsEmbeddings).
		SetSupportsImages(platform.SupportsImages).
		SetProfileJSON(profile).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, service.ErrCustomPlatformNotFound
		}
		return nil, err
	}
	return customPlatformFromEnt(row)
}

func (r *customPlatformRepository) Delete(ctx context.Context, id string) error {
	if err := r.client.CustomPlatform.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return service.ErrCustomPlatformNotFound
		}
		return err
	}
	return nil
}

// DeleteIfUnused 在单个数据库事务内校验引用计数并删除，避免 CountUsage 与 Delete 间的 TOCTOU。
func (r *customPlatformRepository) DeleteIfUnused(ctx context.Context, id string) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.CustomPlatform.Get(ctx, id); err != nil {
		if ent.IsNotFound(err) {
			return service.ErrCustomPlatformNotFound
		}
		return err
	}
	usage, err := countUsageWithClient(ctx, tx.Client(), id)
	if err != nil {
		return err
	}
	if usage.Total() > 0 {
		return &service.CustomPlatformInUseError{Usage: usage}
	}
	if err := tx.CustomPlatform.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return service.ErrCustomPlatformNotFound
		}
		return err
	}
	return tx.Commit()
}

func (r *customPlatformRepository) CountUsage(ctx context.Context, id string) (service.CustomPlatformUsageCounts, error) {
	return countUsageWithClient(ctx, r.client, id)
}

func countUsageWithClient(ctx context.Context, client *ent.Client, id string) (service.CustomPlatformUsageCounts, error) {
	var counts service.CustomPlatformUsageCounts
	accounts, err := client.Account.Query().Where(account.PlatformEQ(id)).Count(ctx)
	if err != nil {
		return counts, err
	}
	groups, err := client.Group.Query().Where(group.PlatformEQ(id)).Count(ctx)
	if err != nil {
		return counts, err
	}
	routes, err := client.CompositeModelRoute.Query().Where(compositemodelroute.TargetPlatformEQ(id)).Count(ctx)
	if err != nil {
		return counts, err
	}
	quotas, err := client.UserPlatformQuota.Query().Where(userplatformquota.PlatformEQ(id)).Count(ctx)
	if err != nil {
		return counts, err
	}
	counts.Accounts = int64(accounts)
	counts.Groups = int64(groups)
	counts.CompositeRoutes = int64(routes)
	counts.UserQuotas = int64(quotas)
	return counts, nil
}

// CountUsageBatch 以 4 次 GROUP BY 聚合查询批量统计多个平台的引用数，避免列表 4N+1。
func (r *customPlatformRepository) CountUsageBatch(ctx context.Context, ids []string) (map[string]service.CustomPlatformUsageCounts, error) {
	out := make(map[string]service.CustomPlatformUsageCounts, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	type platformCountRow struct {
		Platform string `json:"platform"`
		Count    int64  `json:"count"`
	}
	type routeCountRow struct {
		TargetPlatform string `json:"target_platform"`
		Count          int64  `json:"count"`
	}

	var accountRows []platformCountRow
	if err := r.client.Account.Query().
		Where(account.PlatformIn(ids...)).
		GroupBy(account.FieldPlatform).
		Aggregate(ent.Count()).
		Scan(ctx, &accountRows); err != nil {
		return nil, err
	}
	for _, row := range accountRows {
		c := out[row.Platform]
		c.Accounts = row.Count
		out[row.Platform] = c
	}

	var groupRows []platformCountRow
	if err := r.client.Group.Query().
		Where(group.PlatformIn(ids...)).
		GroupBy(group.FieldPlatform).
		Aggregate(ent.Count()).
		Scan(ctx, &groupRows); err != nil {
		return nil, err
	}
	for _, row := range groupRows {
		c := out[row.Platform]
		c.Groups = row.Count
		out[row.Platform] = c
	}

	var routeRows []routeCountRow
	if err := r.client.CompositeModelRoute.Query().
		Where(compositemodelroute.TargetPlatformIn(ids...)).
		GroupBy(compositemodelroute.FieldTargetPlatform).
		Aggregate(ent.Count()).
		Scan(ctx, &routeRows); err != nil {
		return nil, err
	}
	for _, row := range routeRows {
		c := out[row.TargetPlatform]
		c.CompositeRoutes = row.Count
		out[row.TargetPlatform] = c
	}

	var quotaRows []platformCountRow
	if err := r.client.UserPlatformQuota.Query().
		Where(userplatformquota.PlatformIn(ids...)).
		GroupBy(userplatformquota.FieldPlatform).
		Aggregate(ent.Count()).
		Scan(ctx, &quotaRows); err != nil {
		return nil, err
	}
	for _, row := range quotaRows {
		c := out[row.Platform]
		c.UserQuotas = row.Count
		out[row.Platform] = c
	}

	return out, nil
}

func profileJSONMap(profile service.CustomPlatformProfile) (map[string]any, error) {
	raw, err := json.Marshal(profile)
	if err != nil {
		return nil, fmt.Errorf("marshal custom platform profile: %w", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unmarshal custom platform profile: %w", err)
	}
	return out, nil
}

func customPlatformFromEnt(row *ent.CustomPlatform) (*service.CustomPlatform, error) {
	profile := service.CustomPlatformProfile{}
	if len(row.ProfileJSON) > 0 {
		raw, err := json.Marshal(row.ProfileJSON)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &profile); err != nil {
			return nil, fmt.Errorf("decode custom platform %s profile: %w", row.ID, err)
		}
	}
	return &service.CustomPlatform{
		ID:                 row.ID,
		DisplayName:        row.DisplayName,
		Enabled:            row.Enabled,
		SortOrder:          row.SortOrder,
		Icon:               domain.PlatformIconSpec{Type: row.IconType, Value: row.IconValue},
		ColorPreset:        row.ColorPreset,
		LiteLLMProvider:    row.LitellmProvider,
		DefaultTestModel:   row.DefaultTestModel,
		SupportsEmbeddings: row.SupportsEmbeddings,
		SupportsImages:     row.SupportsImages,
		Profile:            profile,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}, nil
}
