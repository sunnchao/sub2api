//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// newPlazaService 构造 ListGroups 测试用的 ModelPlazaService（不接计费服务：展示定价原样透传）。
func newPlazaService(channels []Channel, groups []Group, pricing *PricingService) *ModelPlazaService {
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return channels, nil },
	}
	return NewModelPlazaService(repo, &stubGroupRepoForAvailable{activeGroups: groups}, nil, nil, pricing, nil, nil)
}

func plazaPricedChannel(id int64, name string, groupIDs []int64, platform string, models ...string) Channel {
	return Channel{
		ID:       id,
		Name:     name,
		Status:   StatusActive,
		GroupIDs: groupIDs,
		ModelPricing: []ChannelModelPricing{{
			Platform:    platform,
			Models:      models,
			BillingMode: BillingModeToken,
			InputPrice:  testPtrFloat64(3e-6),
			OutputPrice: testPtrFloat64(1.5e-5),
		}},
	}
}

func TestListPlazaGroups_GroupCentricAggregation(t *testing.T) {
	// 两个渠道挂同一分组:模型并入同一 PlazaGroup;无模型的分组不返回。
	channels := []Channel{
		plazaPricedChannel(1, "chA", []int64{10}, "anthropic", "claude-sonnet"),
		plazaPricedChannel(2, "chB", []int64{10}, "anthropic", "claude-opus"),
	}
	groups := []Group{
		{ID: 10, Name: "g-main", Description: "desc", Platform: "anthropic", RateMultiplier: 1},
		{ID: 20, Name: "g-empty", Platform: "anthropic", RateMultiplier: 0.5},
	}
	svc := newPlazaService(channels, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1, "无模型的分组不应返回")
	require.Equal(t, int64(10), out[0].ID)
	require.Equal(t, "desc", out[0].Description)
	require.Len(t, out[0].Models, 2)
	// 组内模型按名称排序
	require.Equal(t, "claude-opus", out[0].Models[0].Name)
	require.Equal(t, "claude-sonnet", out[0].Models[1].Name)
}

func TestListPlazaGroups_Fable51HasNoImplicitReasoningMultiplier(t *testing.T) {
	ch := plazaPricedChannel(1, "ch", []int64{10}, "anthropic", "claude-fable-5-1")
	svc := newPlazaService([]Channel{ch}, []Group{{ID: 10, Platform: "anthropic"}}, nil)
	groups, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Len(t, groups[0].Models, 1)
	require.Empty(t, groups[0].Models[0].Pricing.ReasoningEffortMultipliers)
}

func TestListPlazaGroups_DedupFirstWinsWithPricingUpgrade(t *testing.T) {
	// 同名模型:先见者胜;仅当已存条目无定价而新条目有定价时升级替换。
	unpriced := Channel{
		ID: 1, Name: "alpha", Status: StatusActive, GroupIDs: []int64{10},
		// mapping-only → SupportedModels 产出无定价条目
		ModelMapping: map[string]map[string]string{
			"anthropic": {"claude-sonnet": "claude-sonnet"},
		},
	}
	priced := plazaPricedChannel(2, "beta", []int64{10}, "anthropic", "claude-sonnet")
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}

	// alpha(无价)按名称序先于 beta(有价):先见者无价,应被有价条目升级。
	svc := newPlazaService([]Channel{priced, unpriced}, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 1)
	require.NotNil(t, out[0].Models[0].Pricing, "无价条目应被有价条目升级")
	require.NotNil(t, out[0].Models[0].Pricing.InputPrice)
}

func TestListPlazaGroups_PlatformIsolation(t *testing.T) {
	// 渠道同时有 anthropic/openai 定价,anthropic 分组只应看到 anthropic 模型。
	ch := Channel{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{
			{Platform: "anthropic", Models: []string{"claude-sonnet"}, InputPrice: testPtrFloat64(3e-6)},
			{Platform: "openai", Models: []string{"gpt-5"}, InputPrice: testPtrFloat64(2e-6)},
		},
	}
	groups := []Group{
		{ID: 10, Name: "g-claude", Platform: "anthropic", RateMultiplier: 1},
		{ID: 20, Name: "g-gpt", Platform: "openai", RateMultiplier: 1},
	}
	svc := newPlazaService([]Channel{ch}, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	byName := map[string][]PlazaModel{}
	for _, g := range out {
		byName[g.Name] = g.Models
	}
	require.Len(t, byName["g-claude"], 1)
	require.Equal(t, "claude-sonnet", byName["g-claude"][0].Name)
	require.Len(t, byName["g-gpt"], 1)
	require.Equal(t, "gpt-5", byName["g-gpt"][0].Name)
}

func TestListPlazaGroups_CompositeIncludesConfiguredConcretePlatforms(t *testing.T) {
	anthropicPrice := 3e-6
	openAIPrice := 2e-6
	ch := Channel{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformAnthropic, Models: []string{"shared-model"}, InputPrice: &anthropicPrice},
			{Platform: PlatformOpenAI, Models: []string{"shared-model"}, InputPrice: &openAIPrice},
			{Platform: "", Models: []string{"empty-platform"}},
			{Platform: PlatformComposite, Models: []string{"nested-composite"}},
			{Platform: "unknown-platform", Models: []string{"unknown-platform"}},
		},
	}
	groups := []Group{{ID: 10, Name: "composite", Platform: PlatformComposite, RateMultiplier: 1}}

	out, err := newPlazaService([]Channel{ch}, groups, nil).ListGroups(context.Background())

	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 2, "only concrete platforms are included and same-named models remain distinct")
	require.Equal(t, PlatformAnthropic, out[0].Models[0].Platform)
	require.Equal(t, PlatformOpenAI, out[0].Models[1].Platform)
	require.InDelta(t, anthropicPrice, *out[0].Models[0].Pricing.InputPrice, 1e-12)
	require.InDelta(t, openAIPrice, *out[0].Models[1].Pricing.InputPrice, 1e-12)
}

func TestListPlazaGroups_CompositeAndOrdinaryGroupsDoNotLeakPlatforms(t *testing.T) {
	ch := Channel{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformAnthropic, Models: []string{"claude-sonnet"}, InputPrice: testPtrFloat64(3e-6)},
			{Platform: PlatformOpenAI, Models: []string{"gpt-5"}, InputPrice: testPtrFloat64(2e-6)},
		},
	}
	groups := []Group{
		{ID: 10, Name: "anthropic-only", Platform: PlatformAnthropic, RateMultiplier: 1},
		{ID: 20, Name: "composite", Platform: PlatformComposite, RateMultiplier: 1},
	}

	out, err := newPlazaService([]Channel{ch}, groups, nil).ListGroups(context.Background())

	require.NoError(t, err)
	require.Len(t, out, 2)
	byName := map[string]PlazaGroup{}
	for _, group := range out {
		byName[group.Name] = group
	}
	require.Len(t, byName["anthropic-only"].Models, 1)
	require.Equal(t, []PlazaModel{{
		Name: "claude-sonnet", Platform: PlatformAnthropic, Pricing: byName["anthropic-only"].Models[0].Pricing,
	}}, byName["anthropic-only"].Models)
	require.Len(t, byName["composite"].Models, 2)
	require.Equal(t, []string{"claude-sonnet", "gpt-5"}, []string{
		byName["composite"].Models[0].Name,
		byName["composite"].Models[1].Name,
	})
	require.Equal(t, []string{PlatformAnthropic, PlatformOpenAI}, []string{
		byName["composite"].Models[0].Platform,
		byName["composite"].Models[1].Platform,
	})
}

func TestListPlazaGroups_InactiveChannelSkipped(t *testing.T) {
	inactive := plazaPricedChannel(1, "off", []int64{10}, "anthropic", "claude-sonnet")
	inactive.Status = "inactive"
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}
	svc := newPlazaService([]Channel{inactive}, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestListPlazaGroups_SortedByRateMultiplierAsc(t *testing.T) {
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10, 20, 30}, "anthropic", "claude-sonnet"),
	}
	groups := []Group{
		{ID: 10, Name: "b-standard", Platform: "anthropic", RateMultiplier: 1},
		{ID: 20, Name: "a-standard", Platform: "anthropic", RateMultiplier: 1},
		{ID: 30, Name: "cheap", Platform: "anthropic", RateMultiplier: 0.5},
	}
	svc := newPlazaService(channels, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 3)
	require.Equal(t, "cheap", out[0].Name, "倍率低者在前")
	require.Equal(t, "a-standard", out[1].Name, "同倍率按名称")
	require.Equal(t, "b-standard", out[2].Name)
}

func TestListPlazaGroups_OfficialPricingFill(t *testing.T) {
	pricingSvc := newStubPricingServiceFromMap(map[string]*LiteLLMModelPricing{
		"claude-sonnet": {
			Mode:                                "chat",
			InputCostPerToken:                   3e-6,
			OutputCostPerToken:                  1.5e-5,
			CacheCreationInputTokenCost:         3.75e-6,
			CacheCreationInputTokenCostAbove1hr: 6e-6,
			CacheReadInputTokenCost:             3e-7,
		},
		"token-absent": {Mode: "image_generation", TokenPricingAbsent: true, OutputCostPerImage: 0.04},
	})
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10}, "anthropic", "claude-sonnet", "unknown-model", "token-absent"),
	}
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}
	svc := newPlazaService(channels, groups, pricingSvc)
	// 官方价与计费同源：需要计费服务与解析器（官方参考不查渠道，解析器无需渠道服务）。
	svc.billingService = NewBillingService(&config.Config{}, pricingSvc)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 3)

	byName := map[string]PlazaModel{}
	for _, m := range out[0].Models {
		byName[m.Name] = m
	}
	// 命中:填充完整官方价(含 1h 缓存写入)
	official := byName["claude-sonnet"].OfficialPricing
	require.NotNil(t, official)
	require.InDelta(t, 3e-6, *official.InputPrice, 1e-12)
	require.InDelta(t, 6e-6, *official.CacheWrite1hPrice, 1e-12)
	require.InDelta(t, 3e-7, *official.CacheReadPrice, 1e-12)
	// 未命中:nil(GetModelPricing 的 claude 系列模糊匹配对非 claude 名不生效)
	require.Nil(t, byName["unknown-model"].OfficialPricing)
	// TokenPricingAbsent 条目不作为官方 token 价展示
	require.Nil(t, byName["token-absent"].OfficialPricing)
}

func TestListPlazaGroups_GroupImagePriceOverridesChannelPricing(t *testing.T) {
	// 图片计费模型:档位价按实收口径合成(分组图片价 > 渠道档位价 > 渠道默认按次价),
	// 分组独立倍率字段透传;未配图片价的分组保持渠道定价原样。
	perReq := 0.2
	tier4K := 0.3
	imgPrice := 0.02
	channels := []Channel{{
		ID: 1, Name: "img-ch", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{{
			Platform:        "openai",
			Models:          []string{"gpt-image-2"},
			BillingMode:     BillingModeImage,
			PerRequestPrice: &perReq,
			Intervals:       []PricingInterval{{TierLabel: "4K", PerRequestPrice: &tier4K}},
		}},
	}}
	groups := []Group{
		{ID: 10, Name: "g-media", Platform: "openai", RateMultiplier: 1,
			ImagePrice1K: &imgPrice, ImageRateIndependent: true, ImageRateMultiplier: 1,
			VideoRateIndependent: true, VideoRateMultiplier: 0.7},
		{ID: 20, Name: "g-plain", Platform: "openai", RateMultiplier: 0.1},
	}
	svc := newPlazaService(channels, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	byName := map[string]PlazaGroup{}
	for _, g := range out {
		byName[g.Name] = g
	}

	media := byName["g-media"]
	require.True(t, media.ImageRateIndependent)
	require.True(t, media.VideoRateIndependent)
	require.InDelta(t, 0.7, media.VideoRateMultiplier, 1e-9)
	require.InDelta(t, 1.0, media.ImageRateMultiplier, 1e-9)
	require.Len(t, media.Models, 1)
	p := media.Models[0].Pricing
	require.NotNil(t, p)
	require.Len(t, p.Intervals, 3)
	tierPrices := map[string]float64{}
	for _, iv := range p.Intervals {
		require.NotNil(t, iv.PerRequestPrice)
		tierPrices[iv.TierLabel] = *iv.PerRequestPrice
	}
	require.InDelta(t, 0.02, tierPrices["1K"], 1e-9, "1K 用分组图片价")
	require.InDelta(t, 0.2, tierPrices["2K"], 1e-9, "2K 分组未配,回落渠道默认按次价")
	require.InDelta(t, 0.3, tierPrices["4K"], 1e-9, "4K 分组未配,回落渠道档位价")

	plain := byName["g-plain"]
	require.False(t, plain.ImageRateIndependent)
	require.False(t, plain.VideoRateIndependent)
	require.Len(t, plain.Models, 1)
	pp := plain.Models[0].Pricing
	require.NotNil(t, pp)
	require.Len(t, pp.Intervals, 1, "未配分组图片价:渠道定价原样")
	require.InDelta(t, 0.2, *pp.PerRequestPrice, 1e-9)

	// 合成为克隆,渠道原始定价不被修改
	require.Len(t, channels[0].ModelPricing[0].Intervals, 1)
}

func TestListPlazaGroups_GroupModelImagePricingWinsOverGroupImageTiers(t *testing.T) {
	groupImage1K, groupImage2K, groupImage4K := 0.05, 0.055, 0.06
	model1K, model2K, model4K := 0.05, 0.075, 0.1
	channels := []Channel{{
		ID: 1, Name: "img-ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{
			Platform:        "openai",
			Models:          []string{"yeahzi-image"},
			BillingMode:     BillingModeImage,
			PerRequestPrice: testPtrFloat64(0.2),
		}},
	}}
	groups := []Group{{
		ID: 10, Name: "g", Platform: "openai", RateMultiplier: 1,
		ImagePrice1K: &groupImage1K, ImagePrice2K: &groupImage2K, ImagePrice4K: &groupImage4K,
		ModelPricing: []ChannelModelPricing{{
			Platform:        "openai",
			Models:          []string{"yeahzi-image"},
			BillingMode:     BillingModeImage,
			PerRequestPrice: testPtrFloat64(0.1),
			Intervals: []PricingInterval{
				{TierLabel: "1K", PerRequestPrice: &model1K},
				{TierLabel: "2K", PerRequestPrice: &model2K},
				{TierLabel: "4K", PerRequestPrice: &model4K},
			},
		}},
	}}

	out, err := newPlazaService(channels, groups, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 1)
	p := out[0].Models[0].Pricing
	require.Equal(t, BillingModeImage, p.BillingMode)
	require.Len(t, p.Intervals, 3)
	tierPrices := map[string]float64{}
	for _, iv := range p.Intervals {
		require.NotNil(t, iv.PerRequestPrice)
		tierPrices[iv.TierLabel] = *iv.PerRequestPrice
	}
	require.InDelta(t, 0.05, tierPrices["1K"], 1e-9)
	require.InDelta(t, 0.075, tierPrices["2K"], 1e-9)
	require.InDelta(t, 0.1, tierPrices["4K"], 1e-9)
}

func TestListPlazaGroups_GroupModelFlatImagePriceWinsOverGroupImageTiers(t *testing.T) {
	groupPrice := 0.2
	channels := []Channel{{
		ID: 1, Name: "img-ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{
			Platform: "openai", Models: []string{"gemini-3.1-flash-lite-image"},
			BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(0.2),
		}},
	}}
	groups := []Group{{
		ID: 10, Name: "g", Platform: "openai", RateMultiplier: 1,
		ImagePrice1K: &groupPrice,
		ModelPricing: []ChannelModelPricing{{
			Platform: "openai", Models: []string{"gemini-3.1-flash-lite-image"},
			BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(0.1),
		}},
	}}

	out, err := newPlazaService(channels, groups, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	p := out[0].Models[0].Pricing
	require.Equal(t, BillingModeImage, p.BillingMode)
	require.NotNil(t, p.PerRequestPrice)
	require.InDelta(t, 0.1, *p.PerRequestPrice, 1e-9)
	require.Empty(t, p.Intervals, "模型级固定价不应被分组图片档位覆盖")
}

func TestListPlazaGroups_GroupImagePriceIgnoredForNonImageModes(t *testing.T) {
	// token 模式定价不受分组图片价影响。
	imgPrice := 0.02
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, "openai", "gpt-5")}
	groups := []Group{{ID: 10, Name: "g", Platform: "openai", RateMultiplier: 1, ImagePrice1K: &imgPrice}}
	svc := newPlazaService(channels, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	p := out[0].Models[0].Pricing
	require.NotNil(t, p)
	require.Empty(t, p.Intervals)
	require.NotNil(t, p.InputPrice)
	require.Nil(t, p.PerRequestPrice)
}

func TestListPlazaGroups_RepoErrorsPropagate(t *testing.T) {
	sentinel := errors.New("boom")
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return nil, sentinel },
	}
	svc := NewModelPlazaService(repo, &stubGroupRepoForAvailable{}, nil, nil, nil, nil, nil)
	out, err := svc.ListGroups(context.Background())
	require.Nil(t, out)
	require.ErrorIs(t, err, sentinel)

	svc2 := NewModelPlazaService(
		&mockChannelRepository{listAllFn: func(ctx context.Context) ([]Channel, error) { return nil, nil }},
		&stubGroupRepoForAvailable{listActiveErr: sentinel},
		nil, nil, nil, nil, nil,
	)
	out2, err2 := svc2.ListGroups(context.Background())
	require.Nil(t, out2)
	require.ErrorIs(t, err2, sentinel)

	ch := plazaPricedChannel(1, "ch", []int64{10}, PlatformAnthropic, "claude-sonnet")
	grp := []Group{{ID: 10, Name: "g", Platform: PlatformAnthropic, RateMultiplier: 1}}
	svc3 := NewModelPlazaService(
		&mockChannelRepository{listAllFn: func(ctx context.Context) ([]Channel, error) { return []Channel{ch}, nil }},
		&stubGroupRepoForAvailable{activeGroups: grp},
		&plazaAccountRepoStub{err: sentinel},
		nil, nil, nil, nil,
	)
	out3, err3 := svc3.ListGroups(context.Background())
	require.Nil(t, out3)
	require.ErrorIs(t, err3, sentinel)

	compGrp := []Group{{ID: 10, Name: "comp", Platform: PlatformComposite, RateMultiplier: 1}}
	compAcc := Account{ID: 1, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, GroupIDs: []int64{10}}
	svc4 := NewModelPlazaService(
		&mockChannelRepository{listAllFn: func(ctx context.Context) ([]Channel, error) { return []Channel{ch}, nil }},
		&stubGroupRepoForAvailable{activeGroups: compGrp},
		&plazaAccountRepoStub{accounts: []Account{compAcc}},
		&plazaCompositeRouteRepoStub{err: sentinel},
		nil, nil, nil,
	)
	out4, err4 := svc4.ListGroups(context.Background())
	require.Nil(t, out4)
	require.ErrorIs(t, err4, sentinel)
}

// newPlazaServiceWithBilling 构造接入计费服务与解析器的广场服务：解析器的渠道服务与广场共用同一份渠道数据。
func newPlazaServiceWithBilling(channels []Channel, groups []Group, groupPlatforms map[int64]string, catalog *PricingService) *ModelPlazaService {
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return channels, nil },
		getGroupPlatformsFn: func(ctx context.Context, _ []int64) (map[int64]string, error) {
			return groupPlatforms, nil
		},
	}
	cs := NewChannelService(repo, nil, nil, nil, nil)
	bs := NewBillingService(&config.Config{}, catalog)
	return NewModelPlazaService(repo, &stubGroupRepoForAvailable{activeGroups: groups}, nil, nil, catalog, bs, NewModelPricingResolver(cs, bs))
}

func plazaModelsByName(models []PlazaModel) map[string]PlazaModel {
	out := make(map[string]PlazaModel, len(models))
	for _, m := range models {
		out[m.Name] = m
	}
	return out
}

func TestListGroups_TokenLadderFollowsGroupToggle(t *testing.T) {
	// 同一渠道挂开启/关闭阶梯的两个分组：实付档位随分组开关，官方阶梯不受影响。
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-5.4"}, BillingMode: BillingModeToken}},
	}}
	groups := []Group{
		{ID: 10, Name: "on", Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: true},
		{ID: 20, Name: "off", Platform: PlatformOpenAI, RateMultiplier: 2, LongContextPricingEnabled: false},
	}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformOpenAI, 20: PlatformOpenAI},
		newStubPricingServiceFromJSON(t, openAILadderCatalogJSON))
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)

	on, off := out[0], out[1]
	require.True(t, on.LongContextPricingEnabled)
	require.False(t, off.LongContextPricingEnabled)

	onModel := on.Models[0]
	require.Equal(t, ContextPricingBasisWholeRequest, onModel.LongContextBasis)
	require.Len(t, onModel.Pricing.Intervals, 2)
	require.Equal(t, "≤272K", onModel.Pricing.Intervals[0].TierLabel)
	require.Equal(t, ">272K", onModel.Pricing.Intervals[1].TierLabel)
	require.InDelta(t, 2.5e-6, *onModel.Pricing.InputPrice, 1e-15)
	require.InDelta(t, 5e-6, *onModel.Pricing.Intervals[1].InputPrice, 1e-15)
	require.InDelta(t, 22.5e-6, *onModel.Pricing.Intervals[1].OutputPrice, 1e-15)
	require.InDelta(t, 5e-6, *onModel.Pricing.Intervals[1].CacheWritePrice, 1e-15)
	require.InDelta(t, 0.5e-6, *onModel.Pricing.Intervals[1].CacheReadPrice, 1e-15)

	offModel := off.Models[0]
	require.Empty(t, offModel.LongContextBasis)
	require.Empty(t, offModel.Pricing.Intervals)
	require.InDelta(t, 2.5e-6, *offModel.Pricing.InputPrice, 1e-15)

	for _, m := range []PlazaModel{onModel, offModel} {
		require.NotNil(t, m.OfficialPricing)
		require.Len(t, m.OfficialPricing.Intervals, 2, "官方阶梯不受分组开关影响")
		require.InDelta(t, 5e-6, *m.OfficialPricing.Intervals[1].InputPrice, 1e-15)
		require.InDelta(t, 2.5e-6, *m.OfficialPricing.InputPrice, 1e-15)
	}
}

func TestListGroups_GeminiCatalogLadderShownWholeRequest(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelMapping: map[string]map[string]string{PlatformGemini: {"gemini-2.5-pro": "gemini-2.5-pro"}},
	}}
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformGemini, RateMultiplier: 1, LongContextPricingEnabled: true}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformGemini},
		newStubPricingServiceFromJSON(t, geminiLadderCatalogJSON))
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	m := out[0].Models[0]
	require.Equal(t, ContextPricingBasisWholeRequest, m.LongContextBasis)
	require.Len(t, m.Pricing.Intervals, 2)
	require.Equal(t, "≤200K", m.Pricing.Intervals[0].TierLabel)
	require.Equal(t, ">200K", m.Pricing.Intervals[1].TierLabel)
	require.InDelta(t, 2.5e-6, *m.Pricing.Intervals[1].InputPrice, 1e-15)
	require.InDelta(t, 15e-6, *m.Pricing.Intervals[1].OutputPrice, 1e-15)
	// 官方参考价与实付同源：都来自目录数据的阶梯字段
	require.NotNil(t, m.OfficialPricing)
	require.Len(t, m.OfficialPricing.Intervals, 2)
}

func TestListGroups_GroupTokenCardOverridesChannelPricing(t *testing.T) {
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformAnthropic, "claude-sonnet-4")}
	groups := []Group{{
		ID: 10, Name: "g", Platform: PlatformAnthropic, RateMultiplier: 1, LongContextPricingEnabled: true,
		ModelPricing: []ChannelModelPricing{{Models: []string{"claude-sonnet-*"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(1e-6)}},
	}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformAnthropic}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]
	require.InDelta(t, 1e-6, *m.Pricing.InputPrice, 1e-15, "分组价卡优先于渠道平价")
	require.InDelta(t, 15e-6, *m.Pricing.OutputPrice, 1e-15, "卡未配置的项回落目录价")
	require.Empty(t, m.Pricing.Intervals)
}

func TestListGroups_ImageModelKeepsTierSynthesisWithBilling(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{
			Platform: PlatformOpenAI, Models: []string{"gpt-image-2"}, BillingMode: BillingModeImage,
			PerRequestPrice: testPtrFloat64(0.04),
		}},
	}}
	groups := []Group{{
		ID: 10, Name: "g", Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: true,
		ImagePrice1K: testPtrFloat64(0.02),
	}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformOpenAI}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]
	require.Equal(t, BillingModeImage, m.Pricing.BillingMode)
	require.Empty(t, m.LongContextBasis)
	require.Len(t, m.Pricing.Intervals, 3)
	require.InDelta(t, 0.02, *m.Pricing.Intervals[0].PerRequestPrice, 1e-12)
	require.InDelta(t, 0.04, *m.Pricing.Intervals[1].PerRequestPrice, 1e-12)
}

func TestListGroups_CatalogMissingStillShowsChannelFlatPricing(t *testing.T) {
	// 目录查不到的模型：计费按渠道平价（未配置项 $0），广场单档展示渠道平价，官方价为空。
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformAnthropic, "unknown-model-xyz")}
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformAnthropic, RateMultiplier: 1, LongContextPricingEnabled: true}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformAnthropic}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]
	require.NotNil(t, m.Pricing)
	require.InDelta(t, 3e-6, *m.Pricing.InputPrice, 1e-15)
	require.Empty(t, m.Pricing.Intervals)
	require.Nil(t, m.Pricing.CacheWritePrice, "目录无价且渠道未配置 → 无价")
	require.Nil(t, m.OfficialPricing)
}

func TestListGroups_TimePricingPassthrough(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{
			Platform: PlatformDeepseek, Models: []string{"deepseek-chat"}, BillingMode: BillingModeToken,
			InputPrice: testPtrFloat64(0.28e-6), OutputPrice: testPtrFloat64(0.42e-6),
			TimePricing: &ChannelTimePricing{Timezone: "Asia/Shanghai", Periods: []ChannelTimePricingPeriod{
				{StartTime: "00:30", EndTime: "08:30", Multiplier: 0.5},
			}},
		}},
	}}
	groups := []Group{{ID: 10, Name: "cn", Platform: PlatformDeepseek, RateMultiplier: 1, LongContextPricingEnabled: true}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformDeepseek}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]
	require.NotNil(t, m.TimePricing)
	require.Equal(t, "Asia/Shanghai", m.TimePricing.Timezone)
	require.Len(t, m.TimePricing.Periods, 1)
	require.InDelta(t, 0.5, m.TimePricing.Periods[0].Multiplier, 1e-12)
	// 展示单价为标准时段价
	require.InDelta(t, 0.28e-6, *m.Pricing.InputPrice, 1e-15)
}

type plazaAccountRepoStub struct {
	AccountRepository
	accounts []Account
	err      error
}

func (s *plazaAccountRepoStub) ListModelAvailabilityCandidates(
	_ context.Context,
	_ *int64,
	_ []string,
	_ bool,
) ([]Account, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.accounts, nil
}

type plazaCompositeRouteRepoStub struct {
	CompositeModelRouteRepository
	byGroup map[int64][]CompositeModelRoute
	err     error
}

func (s *plazaCompositeRouteRepoStub) ListByGroup(
	_ context.Context,
	groupID int64,
	includeDisabled bool,
) ([]CompositeModelRoute, error) {
	if s.err != nil {
		return nil, s.err
	}
	routes := s.byGroup[groupID]
	if includeDisabled {
		return routes, nil
	}
	out := make([]CompositeModelRoute, 0, len(routes))
	for _, r := range routes {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out, nil
}

func newPlazaServiceWithAccess(
	channels []Channel,
	groups []Group,
	accounts []Account,
	routes map[int64][]CompositeModelRoute,
) *ModelPlazaService {
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return channels, nil },
	}
	var routeRepo CompositeModelRouteRepository
	if routes != nil {
		routeRepo = &plazaCompositeRouteRepoStub{byGroup: routes}
	}
	return NewModelPlazaService(
		repo,
		&stubGroupRepoForAvailable{activeGroups: groups},
		&plazaAccountRepoStub{accounts: accounts},
		routeRepo,
		nil, nil, nil,
	)
}

func plazaModelNames(models []PlazaModel) []string {
	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.Name)
	}
	return names
}

func TestListPlazaGroups_GroupModelAllowlistFiltersModels(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10, 20, 30},
		ModelPricing: []ChannelModelPricing{
			{
				Platform: PlatformOpenAI,
				Models:   []string{"gpt-5.4", "gpt-5.3-codex", "gpt-5.2-codex", "o3"},
			},
		},
	}}
	groups := []Group{
		{
			ID: 10, Name: "allowlisted", Platform: PlatformOpenAI, RateMultiplier: 1,
			ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-*-codex", "o3"}},
		},
		{
			ID: 20, Name: "disabled-with-stale-list", Platform: PlatformOpenAI, RateMultiplier: 2,
			ModelAllowlist: GroupModelAllowlist{Enabled: false, Models: []string{"o3"}},
		},
		{
			ID: 30, Name: "enabled-empty", Platform: PlatformOpenAI, RateMultiplier: 3,
			ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: nil},
		},
	}

	out, err := newPlazaService(channels, groups, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2, "启用空白名单的分组过滤后无模型，应被省略")
	require.Equal(t, "allowlisted", out[0].Name)
	require.Equal(t, []string{"gpt-5.2-codex", "gpt-5.3-codex", "o3"}, plazaModelNames(out[0].Models))
	require.Equal(t, "disabled-with-stale-list", out[1].Name)
	require.Equal(t, []string{"gpt-5.2-codex", "gpt-5.3-codex", "gpt-5.4", "o3"}, plazaModelNames(out[1].Models))
}

func TestListPlazaGroups_FiltersByGroupAccountSupportedModelsAndAllowlistIntersection(t *testing.T) {
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10, 20}, PlatformAnthropic, "claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"),
	}
	groups := []Group{
		{
			ID: 10, Name: "g-accounts-only", Platform: PlatformAnthropic, RateMultiplier: 1,
		},
		{
			ID: 20, Name: "g-allowlist-and-accounts", Platform: PlatformAnthropic, RateMultiplier: 2,
			ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"claude-sonnet-4-6", "claude-opus-4-6"}},
		},
	}
	accounts := []Account{
		{
			ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			GroupIDs:    []int64{10, 20},
			Credentials: map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-4-6"}},
		},
		{
			ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			GroupIDs:    []int64{10},
			Credentials: map[string]any{"model_mapping": map[string]any{"claude-haiku-4-5": "claude-haiku-4-5"}},
		},
	}

	out, err := newPlazaServiceWithAccess(channels, groups, accounts, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	// 分组 10：账号 1 + 账号 2 并集，排除 claude-opus-4-6
	require.Equal(t, []string{"claude-haiku-4-5", "claude-sonnet-4-6"}, plazaModelNames(out[0].Models))
	// 分组 20：白名单允许 sonnet/opus，但账号 1 只支持 sonnet，交集仅剩 sonnet
	require.Equal(t, []string{"claude-sonnet-4-6"}, plazaModelNames(out[1].Models))
}

func TestListPlazaGroups_FallsBackToChannelAndAllowlistWhenNoActiveSchedulableAccounts(t *testing.T) {
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10, 20}, PlatformOpenAI, "gpt-5.4", "gpt-5.3"),
	}
	groups := []Group{
		{
			ID: 10, Name: "no-accounts-plain", Platform: PlatformOpenAI, RateMultiplier: 1,
		},
		{
			ID: 20, Name: "paused-account-with-allowlist", Platform: PlatformOpenAI, RateMultiplier: 2,
			ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-5.4"}},
		},
	}
	// 仅存在已停用或不可调度的账号，应视为无活跃可调度账号并回退
	accounts := []Account{
		{
			ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusDisabled, Schedulable: true,
			GroupIDs:    []int64{10, 20},
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.3": "gpt-5.3"}},
		},
		{
			ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: false,
			GroupIDs:    []int64{10, 20},
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.3": "gpt-5.3"}},
		},
	}

	out, err := newPlazaServiceWithAccess(channels, groups, accounts, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Equal(t, []string{"gpt-5.3", "gpt-5.4"}, plazaModelNames(out[0].Models), "无活跃可调度账号时回退保留渠道全量模型")
	require.Equal(t, []string{"gpt-5.4"}, plazaModelNames(out[1].Models), "无活跃可调度账号回退时仍应遵循分组白名单")
}

func TestListPlazaGroups_TransientRateLimitedAccountStillParticipatesInAvailability(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10}, PlatformAnthropic, "claude-opus-4-6", "claude-sonnet-4-6"),
	}
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformAnthropic, RateMultiplier: 1}}
	accounts := []Account{{
		ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		RateLimitResetAt: &future,
		GroupIDs:         []int64{10},
		Credentials:      map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-4-6"}},
	}}

	out, err := newPlazaServiceWithAccess(channels, groups, accounts, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, []string{"claude-sonnet-4-6"}, plazaModelNames(out[0].Models), "短暂限流不应触发回退把不支持的模型放出来")
}

func TestListPlazaGroups_CompositeGroupFiltersByAccountPlatformsAndModels(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4-6", "claude-opus-4-6"}, InputPrice: testPtrFloat64(3e-6)},
			{Platform: PlatformOpenAI, Models: []string{"gpt-5.4", "gpt-5.3"}, InputPrice: testPtrFloat64(2e-6)},
			{Platform: PlatformGemini, Models: []string{"gemini-2.5-pro"}, InputPrice: testPtrFloat64(1.25e-6)},
			{Platform: PlatformGrok, Models: []string{"grok-4"}, InputPrice: testPtrFloat64(3e-6)},
		},
	}}
	groups := []Group{
		{ID: 10, Name: "comp-partial", Platform: PlatformComposite, RateMultiplier: 1},
		{
			ID: 20, Name: "comp-empty-fallback", Platform: PlatformComposite, RateMultiplier: 2,
			ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-5.4", "gemini-2.5-pro"}},
		},
	}
	accounts := []Account{
		{
			ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			GroupIDs:    []int64{10},
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}},
		},
		{
			ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			GroupIDs: []int64{10}, // 空 mapping = 允许全部 anthropic 模型
		},
	}

	out, err := newPlazaServiceWithAccess(channels, groups, accounts, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)

	// comp-partial 只绑定了 openai(仅 gpt-5.4) 和 anthropic(全量)，不展示 gemini/grok 与 gpt-5.3
	require.Equal(t, "comp-partial", out[0].Name)
	require.Equal(t, []string{"claude-opus-4-6", "claude-sonnet-4-6", "gpt-5.4"}, plazaModelNames(out[0].Models))

	// comp-empty-fallback 无活跃可调度账号：回退为渠道配置 + 分组白名单
	require.Equal(t, "comp-empty-fallback", out[1].Name)
	require.Equal(t, []string{"gemini-2.5-pro", "gpt-5.4"}, plazaModelNames(out[1].Models))
}

func TestListPlazaGroups_CompositeRoutesHonorTargetPlatformAndUpstreamModel(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformOpenAI, Models: []string{"my-fast", "gpt-5.4"}, InputPrice: testPtrFloat64(2e-6)},
			{Platform: PlatformAnthropic, Models: []string{"my-fast", "claude-sonnet-4-6"}, InputPrice: testPtrFloat64(3e-6)},
		},
	}}
	groups := []Group{{ID: 10, Name: "comp", Platform: PlatformComposite, RateMultiplier: 1}}
	accounts := []Account{
		{
			ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			GroupIDs:    []int64{10},
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}},
		},
		{
			ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			GroupIDs: []int64{10},
		},
	}
	routes := map[int64][]CompositeModelRoute{
		10: {
			{
				ID: 1, GroupID: 10, PublicModel: "my-fast", MatchType: CompositeRouteMatchExact,
				TargetPlatform: PlatformOpenAI, UpstreamModel: "gpt-5.4", Endpoint: CompositeRouteEndpointAny, Enabled: true,
			},
			{
				ID: 2, GroupID: 10, PublicModel: "claude-sonnet-4-6", MatchType: CompositeRouteMatchExact,
				TargetPlatform: PlatformOpenAI, UpstreamModel: "gpt-5.3", Endpoint: CompositeRouteEndpointAny, Enabled: false,
			},
		},
	}

	out, err := newPlazaServiceWithAccess(channels, groups, accounts, routes).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)

	got := make([]string, 0, len(out[0].Models))
	for _, m := range out[0].Models {
		got = append(got, m.Platform+":"+m.Name)
	}
	// my-fast 被 any 端点路由固定指向 openai(gpt-5.4)，应仅以 openai 展示，不再保留不可达的 anthropic:my-fast；
	// 禁用的 route #2 不应生效，anthropic:claude-sonnet-4-6 正常保留。
	require.Equal(t, []string{
		"anthropic:claude-sonnet-4-6",
		"openai:gpt-5.4",
		"openai:my-fast",
	}, got)
}

func TestListPlazaGroups_MixedAntigravityAccountServesAnthropicAndGemini(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4-6", "claude-opus-4-6"}, InputPrice: testPtrFloat64(3e-6)},
			{Platform: PlatformGemini, Models: []string{"gemini-2.5-pro", "gemini-2.5-flash"}, InputPrice: testPtrFloat64(1.25e-6)},
		},
	}}
	groups := []Group{
		{ID: 10, Name: "g-anthropic", Platform: PlatformAnthropic, RateMultiplier: 1},
		{ID: 20, Name: "g-gemini-unmixed", Platform: PlatformGemini, RateMultiplier: 2},
	}
	accounts := []Account{
		{
			ID: 1, Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
			GroupIDs:    []int64{10},
			Extra:       map[string]any{"mixed_scheduling": true},
			Credentials: map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-4-6"}},
		},
		{
			ID: 2, Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
			GroupIDs:    []int64{20},
			Extra:       map[string]any{"mixed_scheduling": false},
			Credentials: map[string]any{"model_mapping": map[string]any{"gemini-2.5-pro": "gemini-2.5-pro"}},
		},
	}

	out, err := newPlazaServiceWithAccess(channels, groups, accounts, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	// 分组 10 启用了混合调度的 Antigravity 账号，按该账号支持的模型收敛
	require.Equal(t, []string{"claude-sonnet-4-6"}, plazaModelNames(out[0].Models))
	// 分组 20 的 Antigravity 未启用混合调度，不构成有效 gemini 账号 → 回退为渠道全量模型
	require.Equal(t, []string{"gemini-2.5-flash", "gemini-2.5-pro"}, plazaModelNames(out[1].Models))
}

func TestListPlazaGroups_ChannelMappingCheckedBeforeOpenAIAccountSupport(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelMapping: map[string]map[string]string{
			PlatformOpenAI: {
				"alias-ok":   "gpt-5.4",
				"alias-miss": "gpt-5.3",
			},
		},
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformOpenAI, Models: []string{"gpt-5.4", "gpt-5.3"}, InputPrice: testPtrFloat64(2e-6)},
		},
	}}
	groups := []Group{{ID: 10, Name: "g-openai", Platform: PlatformOpenAI, RateMultiplier: 1}}
	accounts := []Account{{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		GroupIDs:    []int64{10},
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}},
	}}

	out, err := newPlazaServiceWithAccess(channels, groups, accounts, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, []string{"alias-ok", "gpt-5.4"}, plazaModelNames(out[0].Models))
}

func TestListPlazaGroups_OpenAIOAuthRetiredAndPassthroughFilter(t *testing.T) {
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10, 20, 30}, PlatformOpenAI, "gpt-5.4", "gpt-5.5", "deepseek-chat"),
	}
	groups := []Group{
		{ID: 10, Name: "g-oauth", Platform: PlatformOpenAI, RateMultiplier: 1},
		{ID: 20, Name: "g-passthrough-unfiltered", Platform: PlatformOpenAI, RateMultiplier: 2},
		{ID: 30, Name: "g-passthrough-filtered", Platform: PlatformOpenAI, RateMultiplier: 3},
	}
	accounts := []Account{
		{
			ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
			GroupIDs: []int64{10},
		},
		{
			ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			GroupIDs:    []int64{20},
			Extra:       map[string]any{"openai_passthrough": true},
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}},
		},
		{
			ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			GroupIDs:    []int64{30},
			Extra:       map[string]any{"openai_passthrough": true, "openai_passthrough_model_filter": true},
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}},
		},
	}

	out, err := newPlazaServiceWithAccess(channels, groups, accounts, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 3)
	// OpenAI OAuth 排除已下线的 gpt-5.5 与非 OpenAI 家族模型 deepseek-chat
	require.Equal(t, []string{"gpt-5.4"}, plazaModelNames(out[0].Models))
	// 透传未开过滤忽略残留 model_mapping
	require.Equal(t, []string{"deepseek-chat", "gpt-5.4", "gpt-5.5"}, plazaModelNames(out[1].Models))
	// 透传开启过滤时按 model_mapping 源模型筛选
	require.Equal(t, []string{"gpt-5.4"}, plazaModelNames(out[2].Models))
}

func TestListPlazaGroups_AnthropicBedrockFiltersUnsupportedModels(t *testing.T) {
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10}, PlatformAnthropic, "claude-sonnet-4-5", "claude-3-5-sonnet-20241022"),
	}
	groups := []Group{{ID: 10, Name: "g-bedrock", Platform: PlatformAnthropic, RateMultiplier: 1}}
	accounts := []Account{{
		ID: 1, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Status: StatusActive, Schedulable: true,
		GroupIDs:    []int64{10},
		Credentials: map[string]any{"aws_region": "us-east-1"},
	}}

	out, err := newPlazaServiceWithAccess(channels, groups, accounts, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, []string{"claude-sonnet-4-5"}, plazaModelNames(out[0].Models))
}
