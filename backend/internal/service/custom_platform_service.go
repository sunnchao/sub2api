package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

// CustomPlatformRepository 是自定义平台的数据访问边界。
type CustomPlatformRepository interface {
	List(ctx context.Context) ([]*CustomPlatform, error)
	Get(ctx context.Context, id string) (*CustomPlatform, error)
	Exists(ctx context.Context, id string) (bool, error)
	Create(ctx context.Context, platform *CustomPlatform) (*CustomPlatform, error)
	Update(ctx context.Context, platform *CustomPlatform) (*CustomPlatform, error)
	Delete(ctx context.Context, id string) error
	CountUsage(ctx context.Context, id string) (CustomPlatformUsageCounts, error)
}

// customPlatformBatchUsageRepository 是可选的批量聚合计数扩展，避免列表 N+1 查询。
type customPlatformBatchUsageRepository interface {
	CountUsageBatch(ctx context.Context, ids []string) (map[string]CustomPlatformUsageCounts, error)
}

// customPlatformAtomicDeleteRepository 是可选的事务删除扩展，避免 CountUsage 与 Delete 之间的 TOCTOU。
type customPlatformAtomicDeleteRepository interface {
	DeleteIfUnused(ctx context.Context, id string) error
}

// CustomPlatformCache 负责多实例热生效广播。
type CustomPlatformCache interface {
	NotifyUpdate(ctx context.Context) error
	SubscribeUpdates(ctx context.Context, handler func())
}

// CustomPlatformService 管理自定义渠道平台，并把变更热同步到 domain / service
// 两层原子快照，使网关与列表接口无需重启即可生效。
type CustomPlatformService struct {
	repo            CustomPlatformRepository
	cache           CustomPlatformCache
	validateBaseURL func(string) (string, error)
	reloadMu        sync.Mutex
	subCancel       context.CancelFunc
}

// NewCustomPlatformURLValidator 构造与账号创建一致的全局 URL 白名单校验器。
func NewCustomPlatformURLValidator(cfg *config.Config) func(string) (string, error) {
	return func(raw string) (string, error) {
		if cfg == nil {
			return urlvalidator.ValidateURLFormat(raw, false)
		}
		allowlist := cfg.Security.URLAllowlist
		if !allowlist.Enabled {
			return urlvalidator.ValidateURLFormat(raw, allowlist.AllowInsecureHTTP)
		}
		return urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
			AllowedHosts:     allowlist.UpstreamHosts,
			RequireAllowlist: true,
			AllowPrivate:     allowlist.AllowPrivateHosts,
		})
	}
}

// NewCustomPlatformService 创建自定义平台服务，并从数据库加载初始快照。
func NewCustomPlatformService(
	repo CustomPlatformRepository,
	cache CustomPlatformCache,
	validateBaseURL func(string) (string, error),
) *CustomPlatformService {
	svc := &CustomPlatformService{repo: repo, cache: cache, validateBaseURL: validateBaseURL}
	if svc.validateBaseURL == nil {
		svc.validateBaseURL = func(raw string) (string, error) {
			return urlvalidator.ValidateURLFormat(raw, false)
		}
	}
	if repo == nil {
		return svc
	}
	subCtx, cancel := context.WithCancel(context.Background())
	svc.subCancel = cancel
	if err := svc.Reload(subCtx); err != nil {
		logger.LegacyPrintf("service.custom_platform", "[CustomPlatform] failed to load on startup: %v", err)
	}
	if cache != nil {
		cache.SubscribeUpdates(subCtx, func() {
			if err := svc.Reload(subCtx); err != nil && subCtx.Err() == nil {
				logger.LegacyPrintf("service.custom_platform", "[CustomPlatform] failed to reload: %v", err)
			}
		})
	}
	return svc
}

// Close 停止多实例更新订阅协程。
func (s *CustomPlatformService) Close() {
	if s != nil && s.subCancel != nil {
		s.subCancel()
	}
}

// Reload 从数据库重新加载并把快照安装到全局注册表。
// 加互斥锁保证并发 Reload（含本地 refresh 与 Pub/Sub 回调）不会使 domain / service 快照失步。
func (s *CustomPlatformService) Reload(ctx context.Context) error {
	if s == nil || s.repo == nil {
		return nil
	}
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	platforms, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	return installCustomPlatforms(platforms)
}

func installCustomPlatforms(platforms []*CustomPlatform) error {
	specs := make([]domain.PlatformSpec, 0, len(platforms))
	profiles := make(map[string]*ProviderProfile, len(platforms))
	for _, platform := range platforms {
		if platform == nil {
			continue
		}
		specs = append(specs, platform.ToPlatformSpec())
		profiles[platform.ID] = platform.ToProviderProfile()
	}
	prevSpecs := domain.DynamicPlatforms()
	if err := domain.SetDynamicPlatforms(specs); err != nil {
		return err
	}
	if err := SetDynamicProviderProfiles(profiles); err != nil {
		_ = domain.SetDynamicPlatforms(prevSpecs)
		return err
	}
	return nil
}

// List 返回全部自定义平台（含引用计数），按展示顺序。
func (s *CustomPlatformService) List(ctx context.Context) ([]*CustomPlatform, error) {
	platforms, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(platforms) == 0 {
		return platforms, nil
	}
	if batchRepo, ok := s.repo.(customPlatformBatchUsageRepository); ok {
		ids := make([]string, 0, len(platforms))
		for _, platform := range platforms {
			ids = append(ids, platform.ID)
		}
		countsByID, err := batchRepo.CountUsageBatch(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, platform := range platforms {
			platform.UsageCounts = countsByID[platform.ID]
		}
		return platforms, nil
	}
	for _, platform := range platforms {
		usage, err := s.repo.CountUsage(ctx, platform.ID)
		if err != nil {
			return nil, err
		}
		platform.UsageCounts = usage
	}
	return platforms, nil
}

// Get 返回单个自定义平台。
func (s *CustomPlatformService) Get(ctx context.Context, id string) (*CustomPlatform, error) {
	platform, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if platform == nil {
		return nil, ErrCustomPlatformNotFound
	}
	usage, err := s.repo.CountUsage(ctx, id)
	if err != nil {
		return nil, err
	}
	platform.UsageCounts = usage
	return platform, nil
}

// Create 校验并创建自定义平台，成功后立即热生效。
func (s *CustomPlatformService) Create(ctx context.Context, input *CustomPlatform) (*CustomPlatform, error) {
	if input == nil {
		return nil, customPlatformValidationError("", "platform is required")
	}
	normalized, err := s.normalize(input, true)
	if err != nil {
		return nil, err
	}
	exists, err := s.repo.Exists(ctx, normalized.ID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, customPlatformValidationError("id", "platform %q already exists", normalized.ID)
	}
	created, err := s.repo.Create(ctx, normalized)
	if err != nil {
		return nil, err
	}
	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	created.UsageCounts = CustomPlatformUsageCounts{}
	return created, nil
}

// Update 更新自定义平台；ID 取自路径且不可修改。成功后立即热生效。
func (s *CustomPlatformService) Update(ctx context.Context, id string, input *CustomPlatform) (*CustomPlatform, error) {
	if input == nil {
		return nil, customPlatformValidationError("", "platform is required")
	}
	if trimmed := strings.TrimSpace(input.ID); trimmed != "" && trimmed != id {
		return nil, customPlatformValidationError("id", "platform id is immutable")
	}
	existing, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrCustomPlatformNotFound
	}
	input.ID = id
	normalized, err := s.normalize(input, false)
	if err != nil {
		return nil, err
	}
	updated, err := s.repo.Update(ctx, normalized)
	if err != nil {
		return nil, err
	}
	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	usage, err := s.repo.CountUsage(ctx, id)
	if err == nil {
		updated.UsageCounts = usage
	}
	return updated, nil
}

// Delete 在无任何引用时物理删除自定义平台。
func (s *CustomPlatformService) Delete(ctx context.Context, id string) error {
	if atomicRepo, ok := s.repo.(customPlatformAtomicDeleteRepository); ok {
		if err := atomicRepo.DeleteIfUnused(ctx, id); err != nil {
			return err
		}
		return s.refresh(ctx)
	}
	existing, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrCustomPlatformNotFound
	}
	usage, err := s.repo.CountUsage(ctx, id)
	if err != nil {
		return err
	}
	if usage.Total() > 0 {
		return &CustomPlatformInUseError{Usage: usage}
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	return s.refresh(ctx)
}

func (s *CustomPlatformService) refresh(ctx context.Context) error {
	if err := s.Reload(ctx); err != nil {
		return err
	}
	if s.cache != nil {
		if err := s.cache.NotifyUpdate(ctx); err != nil {
			logger.LegacyPrintf("service.custom_platform", "[CustomPlatform] notify update: %v", err)
		}
	}
	return nil
}

var (
	customPlatformIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)
	customPlatformModePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	svgEventHandlerPattern    = regexp.MustCompile(`(?i)(?:^|[\s/])on[a-z]+\s*=`)
	builtinIconKeys           = map[string]struct{}{
		"cloud": {}, "cube": {}, "bolt": {}, "spark": {}, "server": {}, "globe": {}, "brain": {}, "leaf": {},
	}
	reservedCustomPlatformIDs = map[string]struct{}{
		"composite": {}, "all": {}, "default": {}, "none": {}, "other": {}, "__other__": {},
	}
)

func (s *CustomPlatformService) normalize(input *CustomPlatform, creating bool) (*CustomPlatform, error) {
	out := *input
	out.ID = strings.TrimSpace(out.ID)
	out.DisplayName = strings.TrimSpace(out.DisplayName)
	out.LiteLLMProvider = strings.TrimSpace(out.LiteLLMProvider)
	out.DefaultTestModel = strings.TrimSpace(out.DefaultTestModel)

	if creating {
		if !customPlatformIDPattern.MatchString(out.ID) {
			return nil, customPlatformValidationError("id", "id must match ^[a-z][a-z0-9_-]{1,31}$")
		}
		if _, reserved := reservedCustomPlatformIDs[out.ID]; reserved {
			return nil, customPlatformValidationError("id", "id %q is reserved", out.ID)
		}
		if domain.IsBuiltinPlatform(out.ID) {
			return nil, customPlatformValidationError("id", "id %q collides with a builtin platform", out.ID)
		}
	}
	if nameLen := utf8.RuneCountInString(out.DisplayName); nameLen == 0 || nameLen > 64 {
		return nil, customPlatformValidationError("display_name", "display_name must be 1-64 characters")
	}
	if out.ColorPreset == "" {
		out.ColorPreset = "slate"
	}
	if !domain.IsValidColorPreset(out.ColorPreset) {
		return nil, customPlatformValidationError("color_preset", "unsupported color preset %q", out.ColorPreset)
	}
	icon, err := normalizeCustomPlatformIcon(out.Icon)
	if err != nil {
		return nil, err
	}
	out.Icon = icon

	profile, err := s.normalizeProfile(out.Profile)
	if err != nil {
		return nil, err
	}
	out.Profile = *profile
	out.CreatedAt = input.CreatedAt
	out.UpdatedAt = input.UpdatedAt
	return &out, nil
}

func normalizeCustomPlatformIcon(icon domain.PlatformIconSpec) (domain.PlatformIconSpec, error) {
	icon.Type = strings.TrimSpace(icon.Type)
	icon.Value = strings.TrimSpace(icon.Value)
	if icon.Type == "" {
		icon.Type = domain.PlatformIconTypeLetter
	}
	switch icon.Type {
	case domain.PlatformIconTypeBuiltin:
		if _, ok := builtinIconKeys[icon.Value]; !ok {
			return icon, customPlatformValidationError("icon.value", "unknown builtin icon %q", icon.Value)
		}
	case domain.PlatformIconTypeURL:
		if len(icon.Value) > 2048 {
			return icon, customPlatformValidationError("icon.value", "icon url is too long")
		}
		lower := strings.ToLower(icon.Value)
		if strings.ContainsAny(icon.Value, " \t\r\n") || strings.Contains(lower, "javascript:") {
			return icon, customPlatformValidationError("icon.value", "icon url contains unsafe characters or scheme")
		}
		if strings.HasPrefix(lower, "https://") {
			parsed, err := url.Parse(icon.Value)
			if err != nil || strings.ToLower(parsed.Scheme) != "https" || strings.TrimSpace(parsed.Hostname()) == "" {
				return icon, customPlatformValidationError("icon.value", "icon url must be a valid https:// url")
			}
		} else if !(strings.HasPrefix(icon.Value, "/") && !strings.HasPrefix(icon.Value, "//")) {
			return icon, customPlatformValidationError("icon.value", "icon url must be https:// or an absolute site path")
		}
	case domain.PlatformIconTypeDataURL:
		if err := validateImageDataURL(icon.Value); err != nil {
			return icon, customPlatformValidationError("icon.value", "%s", err.Error())
		}
	case domain.PlatformIconTypeLetter:
		if utf8.RuneCountInString(icon.Value) > 2 {
			return icon, customPlatformValidationError("icon.value", "letter icon accepts at most 2 characters")
		}
	default:
		return icon, customPlatformValidationError("icon.type", "unsupported icon type %q", icon.Type)
	}
	return icon, nil
}

func validateImageDataURL(value string) error {
	const maxIconBytes = 64 * 1024
	lower := strings.ToLower(value)
	var payload string
	var svg bool
	switch {
	case strings.HasPrefix(lower, "data:image/png;base64,"),
		strings.HasPrefix(lower, "data:image/jpeg;base64,"),
		strings.HasPrefix(lower, "data:image/webp;base64,"):
		payload = value[strings.Index(value, ",")+1:]
	case strings.HasPrefix(lower, "data:image/svg+xml;base64,"):
		payload = value[strings.Index(value, ",")+1:]
		svg = true
	default:
		return fmt.Errorf("icon data url must be png/jpeg/webp/svg base64")
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return fmt.Errorf("icon data url is not valid base64")
	}
	if len(decoded) > maxIconBytes {
		return fmt.Errorf("icon must be smaller than 64KB")
	}
	if svg {
		text := strings.ToLower(string(decoded))
		for _, forbidden := range []string{"<script", "<foreignobject", "<iframe", "<object", "<embed", "javascript:"} {
			if strings.Contains(text, forbidden) {
				return fmt.Errorf("icon svg contains unsafe content")
			}
		}
		if svgEventHandlerPattern.MatchString(text) {
			return fmt.Errorf("icon svg contains unsafe event handlers")
		}
	}
	return nil
}

func (s *CustomPlatformService) normalizeProfile(profile CustomPlatformProfile) (*CustomPlatformProfile, error) {
	out := profile
	out.ResponsesPath = strings.TrimSpace(out.ResponsesPath)
	if out.ResponsesPath != "" {
		if !strings.HasPrefix(out.ResponsesPath, "/") ||
			strings.Contains(out.ResponsesPath, "..") ||
			strings.ContainsAny(out.ResponsesPath, "?#") ||
			strings.ContainsAny(out.ResponsesPath, " \t\r\n") {
			return nil, customPlatformValidationError("multi_protocol.responses_path", "responses_path must be a clean absolute path")
		}
	}
	switch out.Routing {
	case "":
		out.Routing = ProviderRoutingByInbound
	case ProviderRoutingByInbound, ProviderRoutingByModel:
	default:
		return nil, customPlatformValidationError("multi_protocol.routing", "routing must be by_inbound or by_model")
	}
	if len(out.Modes) == 0 {
		return nil, customPlatformValidationError("multi_protocol.modes", "at least one access mode is required")
	}
	seen := make(map[string]struct{}, len(out.Modes))
	normalizedModes := make([]CustomPlatformMode, 0, len(out.Modes))
	for i := range out.Modes {
		mode := out.Modes[i]
		mode.Mode = strings.TrimSpace(mode.Mode)
		mode.Label = strings.TrimSpace(mode.Label)
		if !customPlatformModePattern.MatchString(mode.Mode) {
			return nil, customPlatformValidationError(
				fmt.Sprintf("multi_protocol.modes[%d].mode", i),
				"mode must match ^[a-z0-9][a-z0-9_-]{0,31}$",
			)
		}
		if _, dup := seen[mode.Mode]; dup {
			return nil, customPlatformValidationError("multi_protocol.modes", "duplicate mode %q", mode.Mode)
		}
		seen[mode.Mode] = struct{}{}
		if utf8.RuneCountInString(mode.Label) > 64 {
			return nil, customPlatformValidationError(fmt.Sprintf("multi_protocol.modes[%d].label", i), "label is too long")
		}
		baseURLs := make(map[string]string, len(mode.BaseURLs))
		protocolKeys := make([]string, 0, len(mode.BaseURLs))
		for rawKey := range mode.BaseURLs {
			protocolKeys = append(protocolKeys, rawKey)
		}
		sort.Slice(protocolKeys, func(a, b int) bool {
			ra, rb := protocolSortRank(strings.TrimSpace(protocolKeys[a])), protocolSortRank(strings.TrimSpace(protocolKeys[b]))
			if ra != rb {
				return ra < rb
			}
			return protocolKeys[a] < protocolKeys[b]
		})
		for _, rawKey := range protocolKeys {
			baseURL := mode.BaseURLs[rawKey]
			protocol := strings.TrimSpace(rawKey)
			if !isCustomPlatformBaseURLProtocol(protocol) {
				return nil, customPlatformValidationError(
					fmt.Sprintf("multi_protocol.modes[%d].base_urls", i),
					"unsupported protocol %q",
					protocol,
				)
			}
			baseURL = strings.TrimSpace(baseURL)
			if baseURL == "" {
				continue
			}
			normalized, err := s.validateBaseURL(baseURL)
			if err != nil {
				return nil, customPlatformValidationError(
					fmt.Sprintf("multi_protocol.modes[%d].base_urls.%s", i, protocol),
					"%s",
					err.Error(),
				)
			}
			baseURLs[protocol] = normalized
		}
		for j := range mode.ProtocolRules {
			rule := &mode.ProtocolRules[j]
			rule.Pattern = strings.TrimSpace(rule.Pattern)
			if rule.Pattern == "" || len(rule.Pattern) > 200 {
				return nil, customPlatformValidationError(
					fmt.Sprintf("multi_protocol.modes[%d].protocol_rules[%d].pattern", i, j),
					"pattern must be 1-200 characters",
				)
			}
			if !isDialogueProtocol(rule.Protocol) || baseURLs[rule.Protocol] == "" {
				return nil, customPlatformValidationError(
					fmt.Sprintf("multi_protocol.modes[%d].protocol_rules[%d].protocol", i, j),
					"protocol %q must be enabled in this mode",
					rule.Protocol,
				)
			}
			for _, protocol := range rule.Protocols {
				if !isDialogueProtocol(protocol) || baseURLs[protocol] == "" {
					return nil, customPlatformValidationError(
						fmt.Sprintf("multi_protocol.modes[%d].protocol_rules[%d].protocols", i, j),
						"protocols must only contain protocols enabled in this mode",
					)
				}
			}
		}
		mode.BaseURLs = baseURLs
		if !modeHasDialogueProtocol(mode) {
			return nil, customPlatformValidationError(
				fmt.Sprintf("multi_protocol.modes[%d].base_urls", i),
				"mode %q must enable at least one of chat_completions / responses / anthropic",
				mode.Mode,
			)
		}
		normalizedModes = append(normalizedModes, mode)
	}
	out.Modes = normalizedModes
	if out.DefaultMode == "" {
		out.DefaultMode = out.Modes[0].Mode
	}
	if _, ok := seen[out.DefaultMode]; !ok {
		return nil, customPlatformValidationError("multi_protocol.default_mode", "default_mode %q is not configured", out.DefaultMode)
	}
	return &out, nil
}

func modeHasDialogueProtocol(mode CustomPlatformMode) bool {
	for _, protocol := range dialogueProtocols {
		if strings.TrimSpace(mode.BaseURLs[protocol]) != "" {
			return true
		}
	}
	return false
}

func protocolSortRank(protocol string) int {
	switch protocol {
	case APIProtocolChatCompletions:
		return 0
	case APIProtocolResponses:
		return 1
	case APIProtocolAnthropic:
		return 2
	case APIProtocolEmbeddings:
		return 3
	case APIProtocolImages:
		return 4
	default:
		return 5
	}
}
