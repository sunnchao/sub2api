package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

// PlazaOfficialPricing 模型广场展示用的官方参考价（USD per token），与计费同源：
// LiteLLM → 内置兜底价卡 → 模型策略。字段为 nil 表示该项缺失（0 视为未配置）。
type PlazaOfficialPricing struct {
	InputPrice         *float64
	OutputPrice        *float64
	CacheWritePrice    *float64 // 5m 缓存写入（= LiteLLM cache_creation）
	CacheWrite1hPrice  *float64 // 1h 缓存写入，仅计费会区分 5m/1h 时给出
	CacheWrite12hPrice *float64 // OpenAI 12h cache-write price
	CacheReadPrice     *float64
	// Intervals 官方长上下文阶梯（多档时给出），不受分组开关影响。
	Intervals []PricingInterval
}

// PlazaModel 模型广场中单个模型条目：按实收口径合成的展示定价 + 官方参考价。
type PlazaModel struct {
	Name            string
	Platform        string
	Pricing         *ChannelModelPricing
	OfficialPricing *PlazaOfficialPricing
	// LongContextBasis 多档时的计价基准（整单 / 仅超出部分），单档为空。
	LongContextBasis ContextPricingBasis
	// TimePricing 计费会生效的分时倍率时段；无分时为 nil。
	TimePricing *TimePricingSchedule
}

// PlazaGroup 模型广场中以分组为顶层的条目。
//
// 与 AvailableGroupRef 相比多了 Description 与 Models；Models 来自该分组关联渠道的
// 支持模型（普通分组按分组平台隔离，Composite 分组展开关联渠道已配置的
// 具体平台），与「可用渠道」页口径一致。
type PlazaGroup struct {
	ID                 int64
	Name               string
	Description        string
	I18n               GroupI18n
	Platform           string
	SubscriptionType   string
	RateMultiplier     float64
	PeakRateEnabled    bool
	PeakStart          string
	PeakEnd            string
	PeakRateMultiplier float64
	IsExclusive        bool
	ShowInModelPlaza   bool
	// 图片按次实付倍率：ImageRateIndependent 为 true 时，图片计费模型的实付
	// = 档位价 × ImageRateMultiplier，不乘分组/用户专属倍率（与计费口径一致）。
	ImageRateIndependent bool
	ImageRateMultiplier  float64
	// 视频独立倍率与图片独立倍率分别配置，开启时覆盖分组/用户专属倍率。
	VideoRateIndependent bool
	VideoRateMultiplier  float64
	// LongContextPricingEnabled 分组是否按上下文长度应用阶梯价；关闭时模型展示的是最低档。
	LongContextPricingEnabled bool
	Models                    []PlazaModel
}

// ModelPlazaService 聚合模型广场数据。
//
// 模型枚举来自渠道配置，并按分组的实际访问权限收敛（分组模型白名单 + 分组内活跃
// 可调度账号实际支持的平台与模型；当分组暂无活跃可调度账号时回退为渠道配置 +
// 分组模型白名单）；token 模型的展示单价与阶梯由 BillingService 的阶梯表查询给出
// （与扣费走同一条解析链与计费函数），图片/按次模型沿用渠道/分组档位价。
type ModelPlazaService struct {
	channelRepo        ChannelRepository
	groupRepo          GroupRepository
	accountRepo        AccountRepository
	compositeRouteRepo CompositeModelRouteRepository
	pricingService     *PricingService
	billingService     *BillingService
	resolver           *ModelPricingResolver
}

// NewModelPlazaService 创建模型广场服务。
func NewModelPlazaService(
	channelRepo ChannelRepository,
	groupRepo GroupRepository,
	accountRepo AccountRepository,
	compositeRouteRepo CompositeModelRouteRepository,
	pricingService *PricingService,
	billingService *BillingService,
	resolver *ModelPricingResolver,
) *ModelPlazaService {
	return &ModelPlazaService{
		channelRepo:        channelRepo,
		groupRepo:          groupRepo,
		accountRepo:        accountRepo,
		compositeRouteRepo: compositeRouteRepo,
		pricingService:     pricingService,
		billingService:     billingService,
		resolver:           resolver,
	}
}

// ListGroups 返回模型广场数据：每个活跃分组附带其有权限访问的可用模型与定价。
//
// 模型枚举在渠道配置（Active 渠道、SupportedModels ∪ 全局定价回落、平台隔离）基础上，
// 按分组权限收敛：
//   - 若分组启用了模型白名单（ModelAllowlist），仅保留命中白名单的模型；
//   - 若分组绑定了活跃且持久可调度的账号，仅保留能被至少一个合格账号服务的平台与模型
//     （Composite 分组结合显式路由与实际账号平台；anthropic/gemini 兼容混合调度 Antigravity）；
//   - 若分组暂无活跃可调度账号，回退为渠道配置 + 分组模型白名单展示（保留价格橱窗）；
//   - 渠道按 lower(name) 排序后遍历，保证同名模型去重结果确定；
//   - 同分组同名模型「先见者胜」，仅当已存条目无定价而新条目有定价时升级替换；
//   - token 模型的单价与阶梯按实收口径合成（见 ResolveContextPricingSchedule），
//     图片计费模型的档位价按实收口径合成（见 plazaImageDisplayPricing）；
//   - 每个模型附带官方参考价（查不到为 nil）；
//   - 只返回 Models 非空的分组；分组按 RateMultiplier 升序（同倍率按名称），
//     组内模型按名称排序。
//
// 可见性过滤（专属分组）不在此层做，由 handler 按登录态裁剪。
func (s *ModelPlazaService) ListGroups(ctx context.Context) ([]PlazaGroup, error) {
	channels, err := s.channelRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	sort.SliceStable(channels, func(i, j int) bool {
		return strings.ToLower(channels[i].Name) < strings.ToLower(channels[j].Name)
	})

	byGroup := make(map[int64]*PlazaGroup, len(groups))
	groupEnt := make(map[int64]*Group, len(groups))
	order := make([]int64, 0, len(groups))
	for i := range groups {
		g := &groups[i]
		byGroup[g.ID] = &PlazaGroup{
			ID:                        g.ID,
			Name:                      g.Name,
			Description:               g.Description,
			I18n:                      g.I18n,
			Platform:                  g.Platform,
			SubscriptionType:          g.SubscriptionType,
			RateMultiplier:            g.RateMultiplier,
			PeakRateEnabled:           g.PeakRateEnabled,
			PeakStart:                 g.PeakStart,
			PeakEnd:                   g.PeakEnd,
			PeakRateMultiplier:        g.PeakRateMultiplier,
			IsExclusive:               g.IsExclusive,
			ShowInModelPlaza:          g.ShowInModelPlaza,
			ImageRateIndependent:      g.ImageRateIndependent,
			ImageRateMultiplier:       g.ImageRateMultiplier,
			VideoRateIndependent:      g.VideoRateIndependent,
			VideoRateMultiplier:       g.VideoRateMultiplier,
			LongContextPricingEnabled: g.LongContextPricingEnabled,
		}
		groupEnt[g.ID] = g
		order = append(order, g.ID)
	}

	activeChannelGroups := make(map[int64]*Group, len(groups))
	for i := range channels {
		if channels[i].Status != StatusActive {
			continue
		}
		for _, gid := range channels[i].GroupIDs {
			if g, ok := groupEnt[gid]; ok {
				activeChannelGroups[gid] = g
			}
		}
	}
	if len(activeChannelGroups) == 0 {
		return []PlazaGroup{}, nil
	}

	accountsByGroup, err := s.loadGroupAvailabilityAccounts(ctx, activeChannelGroups)
	if err != nil {
		return nil, err
	}
	compositeRoutesByGroup, err := s.loadCompositeRoutesByGroup(ctx, order, activeChannelGroups, accountsByGroup)
	if err != nil {
		return nil, err
	}

	type modelKey struct {
		platform string
		name     string
	}
	// modelIdx[groupID][platform+modelName] = index into byGroup[groupID].Models
	modelIdx := make(map[int64]map[modelKey]int, len(groups))
	for i := range channels {
		ch := &channels[i]
		if ch.Status != StatusActive {
			continue
		}
		ch.normalizeBillingModelSource()
		supported := ch.SupportedModels()
		fillGlobalPricingFallback(s.pricingService, supported)

		for _, gid := range ch.GroupIDs {
			pg, ok := byGroup[gid]
			if !ok {
				continue
			}
			g := groupEnt[gid]
			idx := modelIdx[gid]
			if idx == nil {
				idx = make(map[modelKey]int, len(supported))
				modelIdx[gid] = idx
			}
			for j := range supported {
				m := supported[j]
				if pg.Platform == PlatformComposite {
					if !isConcreteRequestPlatform(m.Platform) {
						continue
					}
				} else if m.Platform != pg.Platform {
					continue
				}
				if !isModelAccessibleForGroup(g, ch, m, accountsByGroup[gid], compositeRoutesByGroup[gid]) {
					continue
				}
				key := modelKey{platform: m.Platform, name: m.Name}
				if at, seen := idx[key]; seen {
					// 先见者胜；仅当已存条目无定价而新条目有定价时升级。
					if pg.Models[at].Pricing == nil && m.Pricing != nil {
						pg.Models[at].Pricing = m.Pricing
					}
					continue
				}
				idx[key] = len(pg.Models)
				pg.Models = append(pg.Models, PlazaModel{
					Name:     m.Name,
					Platform: m.Platform,
					Pricing:  m.Pricing,
				})
			}
		}
	}

	officialMemo := make(map[string]*PlazaOfficialPricing)
	out := make([]PlazaGroup, 0, len(order))
	for _, gid := range order {
		pg := byGroup[gid]
		if len(pg.Models) == 0 {
			continue
		}
		sort.SliceStable(pg.Models, func(i, j int) bool {
			if pg.Models[i].Name != pg.Models[j].Name {
				return pg.Models[i].Name < pg.Models[j].Name
			}
			return pg.Models[i].Platform < pg.Models[j].Platform
		})
		g := groupEnt[gid]
		for j := range pg.Models {
			s.fillDisplayPricing(ctx, &pg.Models[j], g)
			pg.Models[j].OfficialPricing = s.lookupOfficialPricing(ctx, pg.Models[j].Name, officialMemo)
		}
		out = append(out, *pg)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RateMultiplier != out[j].RateMultiplier {
			return out[i].RateMultiplier < out[j].RateMultiplier
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// fillDisplayPricing 把模型的展示定价换成实收口径：
// token 模型取计费阶梯表（单价与档位均由真实计费函数得出），
// 图片/按次模型（或阶梯表不可用时）沿用渠道定价与分组图片档位价。
func (s *ModelPlazaService) fillDisplayPricing(ctx context.Context, m *PlazaModel, g *Group) {
	if groupPricing := matchGroupModelPricing(g, m.Name); groupPricing != nil {
		m.Pricing = groupPricing
		// 分组模型级固定价就是实收价，不能再被分组图片兜底档位覆盖。
		// 否则模型广场会显示 image_price_1k/2k/4k，而实际按 model_pricing 扣费。
		if groupPricing.BillingMode == BillingModeImage || groupPricing.BillingMode == BillingModePerRequest {
			return
		}
	}
	if s.billingService != nil && s.resolver != nil {
		sched, err := s.billingService.ResolveContextPricingSchedule(ctx, s.resolver, ContextPricingScheduleInput{
			Model:    m.Name,
			Group:    g,
			Platform: m.Platform,
		})
		if err == nil && sched != nil && len(sched.Tiers) > 0 {
			m.Pricing = plazaPricingFromSchedule(m.Pricing, sched)
			if len(sched.Tiers) > 1 {
				m.LongContextBasis = sched.Basis
			}
			m.TimePricing = sched.TimePricing
			return
		}
	}
	m.Pricing = plazaImageDisplayPricing(m.Pricing, g)
}

// plazaPricingFromSchedule 把阶梯表压成展示用的 ChannelModelPricing：
// 平价取首档单价，多档时 Intervals 逐档给出绝对单价；图片/按次字段沿用原始定价。
func plazaPricingFromSchedule(raw *ChannelModelPricing, sched *ContextPricingSchedule) *ChannelModelPricing {
	out := &ChannelModelPricing{BillingMode: BillingModeToken}
	if raw != nil {
		out.ImageInputPrice = raw.ImageInputPrice
		out.ImageOutputPrice = raw.ImageOutputPrice
		out.PerRequestPrice = raw.PerRequestPrice
		out.ReasoningEffortMultipliers = reasoningEffortMultipliersFromPricing(raw)
	}
	first := sched.Tiers[0]
	out.InputPrice = first.Input
	out.OutputPrice = first.Output
	out.CacheWritePrice = first.CacheWrite
	out.CacheWrite1hPrice = first.CacheWrite1h
	out.CacheWrite12hPrice = first.CacheWrite12h
	out.CacheReadPrice = first.CacheRead
	if len(sched.Tiers) > 1 {
		out.Intervals = plazaIntervalsFromTiers(sched.Tiers)
	}
	return out
}

func plazaIntervalsFromTiers(tiers []ContextPricingTier) []PricingInterval {
	intervals := make([]PricingInterval, 0, len(tiers))
	for i, t := range tiers {
		intervals = append(intervals, PricingInterval{
			MinTokens:          t.MinTokens,
			MaxTokens:          t.MaxTokens,
			TierLabel:          t.Label,
			InputPrice:         t.Input,
			OutputPrice:        t.Output,
			CacheWritePrice:    t.CacheWrite,
			CacheWrite1hPrice:  t.CacheWrite1h,
			CacheWrite12hPrice: t.CacheWrite12h,
			CacheReadPrice:     t.CacheRead,
			SortOrder:          i,
		})
	}
	return intervals
}

// plazaImageDisplayPricing 为图片计费模型合成展示定价，使档位价与实收口径一致：
// 每档（1K/2K/4K）单价 = 分组图片价 > 渠道同档位价 > 渠道默认按次价，无价的档不展示。
// 分组未配任何图片价、或定价非图片模式时原样返回。返回克隆，不修改入参
// （渠道定价指针指向缓存共享数据）。
func plazaImageDisplayPricing(p *ChannelModelPricing, g *Group) *ChannelModelPricing {
	if p == nil || g == nil || p.BillingMode != BillingModeImage {
		return p
	}
	if g.ImagePrice1K == nil && g.ImagePrice2K == nil && g.ImagePrice4K == nil {
		return p
	}
	channelTierPrice := func(label string) *float64 {
		for i := range p.Intervals {
			if p.Intervals[i].TierLabel == label && p.Intervals[i].PerRequestPrice != nil {
				return p.Intervals[i].PerRequestPrice
			}
		}
		return p.PerRequestPrice
	}
	tiers := []struct {
		label      string
		groupPrice *float64
	}{
		{"1K", g.ImagePrice1K},
		{"2K", g.ImagePrice2K},
		{"4K", g.ImagePrice4K},
	}
	clone := *p
	clone.Intervals = make([]PricingInterval, 0, len(tiers))
	for i, t := range tiers {
		price := t.groupPrice
		if price == nil {
			price = channelTierPrice(t.label)
		}
		if price == nil {
			continue
		}
		v := *price
		clone.Intervals = append(clone.Intervals, PricingInterval{
			TierLabel:       t.label,
			PerRequestPrice: &v,
			SortOrder:       i,
		})
	}
	return &clone
}

// lookupOfficialPricing 查询模型的官方参考价（与计费同源：LiteLLM → 内置兜底 → 模型策略），
// 带 memo 避免同名模型重复解析。官方阶梯按无分组、无渠道的口径查阶梯表。
// billingService 为 nil（测试场景）或查不到时返回 nil。
func (s *ModelPlazaService) lookupOfficialPricing(ctx context.Context, modelName string, memo map[string]*PlazaOfficialPricing) *PlazaOfficialPricing {
	if s.billingService == nil {
		return nil
	}
	if cached, ok := memo[modelName]; ok {
		return cached
	}
	var result *PlazaOfficialPricing
	if mp, err := s.billingService.GetModelPricing(modelName); err == nil && mp != nil {
		result = &PlazaOfficialPricing{
			InputPrice:      nonZeroPtr(mp.InputPricePerToken),
			OutputPrice:     nonZeroPtr(mp.OutputPricePerToken),
			CacheWritePrice: nonZeroPtr(mp.CacheCreationPricePerToken),
			CacheReadPrice:  nonZeroPtr(mp.CacheReadPricePerToken),
		}
		// 计费只在支持 5m/1h 分档时使用 1h 价，其余情况 1h 价对用户无意义。
		if mp.SupportsCacheBreakdown {
			result.CacheWrite1hPrice = nonZeroPtr(mp.CacheCreation1hPrice)
		}
		result.CacheWrite12hPrice = mp.CacheCreation12hPricePerToken
		if s.resolver != nil {
			sched, schedErr := s.billingService.ResolveContextPricingSchedule(ctx, s.resolver, ContextPricingScheduleInput{Model: modelName})
			if schedErr == nil && sched != nil && len(sched.Tiers) > 1 {
				result.Intervals = plazaIntervalsFromTiers(sched.Tiers)
			}
		}
		if result.InputPrice == nil && result.OutputPrice == nil && result.CacheWritePrice == nil &&
			result.CacheWrite1hPrice == nil && result.CacheWrite12hPrice == nil && result.CacheReadPrice == nil && len(result.Intervals) == 0 {
			result = nil
		}
	}
	memo[modelName] = result
	return result
}

// loadGroupAvailabilityAccounts 一次性拉取所有活跃且持久可调度的账号，并按分组归类。
// 复用 ListModelAvailabilityCandidates：只看持久化配置（status=active, schedulable=true），
// 忽略限流/过载/临时不可调度等瞬态，避免账号短暂 429 导致模型广场展示抖动。
// accountRepo 为 nil（单测未注入账号仓储）时返回 nil，调用方跳过账号维度过滤。
func (s *ModelPlazaService) loadGroupAvailabilityAccounts(
	ctx context.Context,
	groups map[int64]*Group,
) (map[int64][]Account, error) {
	if s.accountRepo == nil || len(groups) == 0 {
		return nil, nil
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(ctx, nil, domain.ConcretePlatformIDs(), true)
	if err != nil {
		return nil, fmt.Errorf("list availability accounts: %w", err)
	}
	byGroup := make(map[int64][]Account, len(groups))
	for i := range accounts {
		acc := accounts[i]
		if !acc.IsActive() || !acc.Schedulable {
			continue
		}
		for _, gid := range accountAssignedGroupIDs(&acc) {
			g, ok := groups[gid]
			if !ok {
				continue
			}
			if !plazaAccountBelongsToGroup(g, &acc) {
				continue
			}
			byGroup[gid] = append(byGroup[gid], acc)
		}
	}
	return byGroup, nil
}

// loadCompositeRoutesByGroup 加载参与模型广场展示且存在活跃可调度账号的 Composite 分组已启用显式路由。
// compositeRouteRepo 为 nil 或分组无活跃可调度账号（回退模式）时跳过查询。
func (s *ModelPlazaService) loadCompositeRoutesByGroup(
	ctx context.Context,
	order []int64,
	activeChannelGroups map[int64]*Group,
	accountsByGroup map[int64][]Account,
) (map[int64][]CompositeModelRoute, error) {
	if s.compositeRouteRepo == nil || len(activeChannelGroups) == 0 {
		return nil, nil
	}
	var out map[int64][]CompositeModelRoute
	for _, gid := range order {
		g, ok := activeChannelGroups[gid]
		if !ok || g.Platform != PlatformComposite || len(accountsByGroup[gid]) == 0 {
			continue
		}
		routes, err := s.compositeRouteRepo.ListByGroup(ctx, gid, false)
		if err != nil {
			return nil, fmt.Errorf("list composite routes for group %d: %w", gid, err)
		}
		if len(routes) == 0 {
			continue
		}
		if out == nil {
			out = make(map[int64][]CompositeModelRoute)
		}
		out[gid] = routes
	}
	return out, nil
}

func accountAssignedGroupIDs(acc *Account) []int64 {
	if acc == nil {
		return nil
	}
	if len(acc.GroupIDs) > 0 {
		return acc.GroupIDs
	}
	if len(acc.AccountGroups) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(acc.AccountGroups))
	for i := range acc.AccountGroups {
		ids = append(ids, acc.AccountGroups[i].GroupID)
	}
	return ids
}

func plazaAccountEligibleForPlatform(acc *Account, targetPlatform string) bool {
	if acc == nil || targetPlatform == "" {
		return false
	}
	if acc.Platform == targetPlatform {
		return true
	}
	useMixed := targetPlatform == PlatformAnthropic || targetPlatform == PlatformGemini
	return useMixed && acc.Platform == PlatformAntigravity && acc.IsMixedSchedulingEnabled()
}

func plazaAccountBelongsToGroup(g *Group, acc *Account) bool {
	if g == nil || acc == nil {
		return false
	}
	if g.Platform == PlatformComposite {
		return isConcreteRequestPlatform(acc.Platform)
	}
	return plazaAccountEligibleForPlatform(acc, g.Platform)
}

// isModelAccessibleForGroup 判断渠道中的候选模型 m 是否对分组 g 有权访问：
//  1. 分组模型白名单：开启时必须命中 g.ModelAllowlist.Allows(m.Name)；
//  2. 分组账号池：当分组存在活跃且持久可调度的合格账号时，仅保留能被至少一个账号
//     服务的平台与模型；当分组暂无活跃可调度账号（或未注入 accountRepo）时，回退为
//     按渠道配置 + 分组模型白名单展示（保留价格橱窗）。
func isModelAccessibleForGroup(
	g *Group,
	ch *Channel,
	m SupportedModel,
	groupAccounts []Account,
	compositeRoutes []CompositeModelRoute,
) bool {
	if g == nil {
		return false
	}
	if g.ModelAllowlistEnabled() && !g.ModelAllowlist.Allows(m.Name) {
		return false
	}
	if len(groupAccounts) == 0 {
		return true
	}

	candidates := []string{m.Name}
	if g.Platform == PlatformComposite && len(compositeRoutes) > 0 {
		candidates = compositePlazaCandidateModels(compositeRoutes, m.Name, m.Platform)
		if len(candidates) == 0 {
			return false
		}
	}

	for _, candidate := range candidates {
		if plazaAccountsSupportModel(groupAccounts, ch, m.Platform, candidate) {
			return true
		}
	}
	return false
}

// compositePlazaCandidateModels 计算 Composite 分组中模型 model 经显式路由后
// 能落到 targetPlatform 的候选上游模型名集合。
//   - 若存在任意 endpoint 的显式路由命中且目标为 targetPlatform，收集对应的上游模型名；
//   - 若无 any 端点路由拦截（未配置显式路由的端点仍走默认解析），保留 model 本身；
//   - 若 any 端点路由把 model 重定向到其他平台且无特定端点路由指向 targetPlatform，返回空切片。
func compositePlazaCandidateModels(routes []CompositeModelRoute, model, targetPlatform string) []string {
	enabledRoutes := make([]CompositeModelRoute, 0, len(routes))
	endpoints := make([]string, 0, len(routes))
	seenEndpoint := make(map[string]struct{}, len(routes))
	for _, r := range routes {
		if !r.Enabled {
			continue
		}
		enabledRoutes = append(enabledRoutes, r)
		ep := normalizeCompositeRouteEndpoint(r.Endpoint)
		if ep != CompositeRouteEndpointAny {
			if _, ok := seenEndpoint[ep]; !ok {
				seenEndpoint[ep] = struct{}{}
				endpoints = append(endpoints, ep)
			}
		}
	}
	if len(enabledRoutes) == 0 {
		return []string{model}
	}

	seenModel := make(map[string]struct{}, 2)
	candidates := make([]string, 0, 2)
	addCandidate := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seenModel[name]; ok {
			return
		}
		seenModel[name] = struct{}{}
		candidates = append(candidates, name)
	}

	if route, matched := matchCompositeRoute(enabledRoutes, model, CompositeRouteEndpointAny); matched {
		if route.TargetPlatform == targetPlatform {
			upstream := strings.TrimSpace(route.UpstreamModel)
			if upstream == "" {
				upstream = model
			}
			addCandidate(upstream)
		}
	} else {
		addCandidate(model)
	}

	for _, ep := range endpoints {
		route, matched := matchCompositeRoute(enabledRoutes, model, ep)
		if !matched || route.TargetPlatform != targetPlatform {
			continue
		}
		upstream := strings.TrimSpace(route.UpstreamModel)
		if upstream == "" {
			upstream = model
		}
		addCandidate(upstream)
	}
	return candidates
}

// plazaAccountsSupportModel 判断 accounts 中是否有至少一个合格账号能够服务 (targetPlatform, model)。
// 与运行时网关保持一致：
//   - OpenAI 网关族平台在选号前已应用渠道映射，按映射后的模型名校验；
//   - Gemini 原生端点按渠道映射后的模型名选号，Messages 兼容端点按原始模型名选号，两者任一命中即可；
//   - Anthropic / Antigravity / TypeSafe 在选号阶段按原始模型名校验。
func plazaAccountsSupportModel(accounts []Account, ch *Channel, targetPlatform, model string) bool {
	mappedModel, mapped := resolveChannelMappedModelForPlatform(ch, targetPlatform, model)
	usesOpenAIGateway := domain.UsesOpenAIGateway(targetPlatform)

	for i := range accounts {
		acc := &accounts[i]
		if !plazaAccountEligibleForPlatform(acc, targetPlatform) {
			continue
		}
		if usesOpenAIGateway {
			if acc.IsModelSupported(mappedModel) {
				return true
			}
			continue
		}
		if targetPlatform == PlatformGemini {
			if isAccountModelSupported(acc, mappedModel) || (mapped && mappedModel != model && isAccountModelSupported(acc, model)) {
				return true
			}
			continue
		}
		if isAccountModelSupported(acc, model) {
			return true
		}
	}
	return false
}

// resolveChannelMappedModelForPlatform 按指定平台解析渠道级模型映射（精确优先、最长前缀次之）。
// 目标为空或仍带尾部通配符时视作未改写，原样返回。
func resolveChannelMappedModelForPlatform(ch *Channel, platform, model string) (string, bool) {
	if ch == nil || len(ch.ModelMapping) == 0 {
		return model, false
	}
	mapping := ch.ModelMapping[platform]
	if len(mapping) == 0 {
		return model, false
	}
	modelLower := strings.ToLower(model)

	var exactSrc, exactDst string
	for src, dst := range mapping {
		if strings.HasSuffix(src, wildcardSuffix) {
			continue
		}
		if strings.ToLower(src) == modelLower {
			if exactSrc == "" || src < exactSrc {
				exactSrc = src
				exactDst = dst
			}
		}
	}
	if exactSrc != "" {
		if exactDst != "" && !strings.HasSuffix(exactDst, wildcardSuffix) {
			return exactDst, true
		}
		return model, false
	}

	bestLen := -1
	bestSrc := ""
	bestDst := ""
	for src, dst := range mapping {
		prefix, isWild := splitWildcardSuffix(src)
		if !isWild || dst == "" || strings.HasSuffix(dst, wildcardSuffix) {
			continue
		}
		prefixLower := strings.ToLower(prefix)
		if !strings.HasPrefix(modelLower, prefixLower) {
			continue
		}
		if len(prefixLower) > bestLen || (len(prefixLower) == bestLen && src < bestSrc) {
			bestLen = len(prefixLower)
			bestSrc = src
			bestDst = dst
		}
	}
	if bestLen >= 0 {
		return bestDst, true
	}
	return model, false
}
