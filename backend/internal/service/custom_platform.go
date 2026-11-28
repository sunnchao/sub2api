package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

// CustomPlatform 是后台登记的自定义渠道平台。ID 一经创建不可修改，删除前必须
// 没有账号/分组/Composite 路由/用户平台限额引用。
type CustomPlatform struct {
	ID                 string                    `json:"id"`
	DisplayName        string                    `json:"display_name"`
	Enabled            bool                      `json:"enabled"`
	SortOrder          int                       `json:"sort_order"`
	Icon               domain.PlatformIconSpec   `json:"icon"`
	ColorPreset        string                    `json:"color_preset"`
	LiteLLMProvider    string                    `json:"litellm_provider"`
	DefaultTestModel   string                    `json:"default_test_model"`
	SupportsEmbeddings bool                      `json:"supports_embeddings"`
	SupportsImages     bool                      `json:"supports_images"`
	Profile            CustomPlatformProfile     `json:"multi_protocol"`
	UsageCounts        CustomPlatformUsageCounts `json:"usage_counts,omitempty"`
	CreatedAt          time.Time                 `json:"created_at,omitempty"`
	UpdatedAt          time.Time                 `json:"updated_at,omitempty"`
}

// CustomPlatformProfile 与 ProviderProfile 同构，是 profile_json 的持久化形状。
type CustomPlatformProfile struct {
	DefaultMode   string               `json:"default_mode"`
	Routing       ProviderRouting      `json:"routing"`
	ResponsesPath string               `json:"responses_path,omitempty"`
	ModelCatalog  bool                 `json:"model_catalog,omitempty"`
	Modes         []CustomPlatformMode `json:"modes"`
}

// CustomPlatformMode 是某一接入模式下的端点配置。BaseURLs 仅允许对话协议
// （chat_completions / responses / anthropic）与可选的 embeddings / images。
type CustomPlatformMode struct {
	Mode          string            `json:"mode"`
	Label         string            `json:"label,omitempty"`
	BaseURLs      map[string]string `json:"base_urls"`
	ProtocolRules []ProtocolRule    `json:"protocol_rules,omitempty"`
}

// CustomPlatformUsageCounts 是删除保护用的四类引用计数。
type CustomPlatformUsageCounts struct {
	Accounts        int64 `json:"accounts"`
	Groups          int64 `json:"groups"`
	CompositeRoutes int64 `json:"composite_routes"`
	UserQuotas      int64 `json:"user_quotas"`
}

// Total 返回引用总数。
func (c CustomPlatformUsageCounts) Total() int64 {
	return c.Accounts + c.Groups + c.CompositeRoutes + c.UserQuotas
}

// 自定义平台生命周期错误。
var (
	ErrCustomPlatformNotFound = errors.New("custom platform not found")
)

// CustomPlatformValidationError 表示表单校验失败；Field 用于前端定位输入项。
type CustomPlatformValidationError struct {
	Field   string
	Message string
}

func (e *CustomPlatformValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// CustomPlatformInUseError 表示平台仍被引用，不能物理删除。
type CustomPlatformInUseError struct {
	Usage CustomPlatformUsageCounts
}

func (e *CustomPlatformInUseError) Error() string {
	return fmt.Sprintf("custom platform is still referenced (accounts=%d groups=%d composite_routes=%d user_quotas=%d)",
		e.Usage.Accounts, e.Usage.Groups, e.Usage.CompositeRoutes, e.Usage.UserQuotas)
}

func customPlatformValidationError(field, format string, args ...any) error {
	return &CustomPlatformValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// dialogueProtocols 是自定义平台可原生支持的对话协议（顺序即管理端展示顺序）。
var dialogueProtocols = []string{APIProtocolChatCompletions, APIProtocolResponses, APIProtocolAnthropic}

// customPlatformBaseURLProtocols 是 BaseURLs 允许出现的全部键。
var customPlatformBaseURLProtocols = []string{
	APIProtocolChatCompletions, APIProtocolResponses, APIProtocolAnthropic,
	APIProtocolEmbeddings, APIProtocolImages,
}

func isDialogueProtocol(protocol string) bool {
	for _, item := range dialogueProtocols {
		if item == protocol {
			return true
		}
	}
	return false
}

func isCustomPlatformBaseURLProtocol(protocol string) bool {
	for _, item := range customPlatformBaseURLProtocols {
		if item == protocol {
			return true
		}
	}
	return false
}

// DefaultModeEndpoints 返回默认模式（缺失时回退第一个模式）的端点。
func (p CustomPlatformProfile) DefaultModeEndpoints() *CustomPlatformMode {
	for i := range p.Modes {
		if p.Modes[i].Mode == p.DefaultMode {
			return &p.Modes[i]
		}
	}
	if len(p.Modes) > 0 {
		return &p.Modes[0]
	}
	return nil
}

// ToPlatformSpec 生成 domain 平台清单条目（含展示与能力元数据）。
func (c *CustomPlatform) ToPlatformSpec() domain.PlatformSpec {
	return domain.PlatformSpec{
		ID:                 c.ID,
		DisplayName:        c.DisplayName,
		Gateway:            domain.PlatformGatewayOpenAI,
		Custom:             true,
		Disabled:           !c.Enabled,
		SortOrder:          c.SortOrder,
		LiteLLMProvider:    strings.TrimSpace(c.LiteLLMProvider),
		DefaultTestModel:   strings.TrimSpace(c.DefaultTestModel),
		SupportsEmbeddings: c.SupportsEmbeddings,
		SupportsImages:     c.SupportsImages,
		ColorPreset:        c.ColorPreset,
		Icon:               &domain.PlatformIconSpec{Type: c.Icon.Type, Value: c.Icon.Value},
	}
}

// ToProviderProfile 把可编辑的端点配置转换为运行时的 ProviderProfile。
func (c *CustomPlatform) ToProviderProfile() *ProviderProfile {
	profile := &ProviderProfile{
		Platform:         c.ID,
		DefaultMode:      c.Profile.DefaultMode,
		Routing:          c.Profile.Routing,
		ResponsesPath:    c.Profile.ResponsesPath,
		DefaultTestModel: strings.TrimSpace(c.DefaultTestModel),
		ModelCatalog:     c.Profile.ModelCatalog,
		Modes:            make(map[string]ProviderEndpoints, len(c.Profile.Modes)),
	}
	for _, mode := range c.Profile.Modes {
		endpoints := ProviderEndpoints{Label: mode.Label}
		if len(mode.BaseURLs) > 0 {
			endpoints.BaseURLs = make(map[string]string, len(mode.BaseURLs))
		}
		for protocol, baseURL := range mode.BaseURLs {
			trimmed := strings.TrimSpace(baseURL)
			if trimmed == "" {
				continue
			}
			switch protocol {
			case APIProtocolEmbeddings:
				endpoints.EmbeddingsBaseURL = trimmed
			case APIProtocolImages:
				endpoints.ImagesBaseURL = trimmed
			default:
				endpoints.BaseURLs[protocol] = trimmed
			}
		}
		if c.SupportsEmbeddings && endpoints.EmbeddingsBaseURL == "" {
			endpoints.EmbeddingsBaseURL = endpoints.BaseURLs[APIProtocolChatCompletions]
		}
		if c.SupportsImages && endpoints.ImagesBaseURL == "" {
			endpoints.ImagesBaseURL = endpoints.BaseURLs[APIProtocolChatCompletions]
		}
		endpoints.ProtocolRules = cloneProtocolRules(mode.ProtocolRules)
		profile.Modes[mode.Mode] = endpoints
	}
	return profile
}
