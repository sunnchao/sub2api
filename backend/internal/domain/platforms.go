package domain

// 平台清单：平台的单一数据来源。"所有平台"类列表（分组平台、用户额度、组合
// 路由目标、调度快照、定价匹配等）与"某网关族"类列表都从这里派生；新增平台
// 登记一条即可在这些位置生效，无需逐处修改或新增数据库约束迁移。
//
// 依赖具体实现的能力（OAuth、渠道监控、余额 / Coding Plan 探测、用量窗口、
// Codex 模型目录等）仍由各自的代码显式列出，不在此处声明。

// PlatformGateway 标识平台请求由哪个网关服务处理。
type PlatformGateway string

const (
	// PlatformGatewayAnthropic 由 GatewayService 处理（Claude Messages 协议，以及 TypeSafe 的 /v1/systemone）。
	PlatformGatewayAnthropic PlatformGateway = "anthropic"
	// PlatformGatewayOpenAI 由 OpenAIGatewayService 处理（OpenAI、Grok 与多协议 API Key 供应商）。
	PlatformGatewayOpenAI PlatformGateway = "openai"
	// PlatformGatewayGemini 由 Gemini 兼容网关处理。
	PlatformGatewayGemini PlatformGateway = "gemini"
	// PlatformGatewayAntigravity 由 Antigravity 网关处理。
	PlatformGatewayAntigravity PlatformGateway = "antigravity"
)

// PlatformSpec 描述一个具体（非 composite）平台。
type PlatformSpec struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"display_name"`
	Gateway     PlatformGateway `json:"gateway"`
	// CNProvider 标识国产单一厂商（Kimi / 智谱 / DeepSeek / MiniMax），共享国产
	// 供应商的余额 / Coding Plan 等能力。
	CNProvider bool `json:"cn_provider"`
	// LiteLLMProvider 是定价目录中的 provider 名；空表示不支持从定价目录同步模型。
	LiteLLMProvider string `json:"litellm_provider,omitempty"`
	// Custom 标识后台动态登记的自定义渠道商；内置平台恒为 false。
	Custom bool `json:"custom,omitempty"`
	// Disabled 仅对自定义平台有意义：禁用后不再出现在新建账号/分组选项中，
	// 但既有账号、分组与历史统计仍按原样解析。
	Disabled bool `json:"disabled,omitempty"`
	// SortOrder 是自定义平台的展示顺序（内置平台忽略该字段）。
	SortOrder int `json:"sort_order,omitempty"`
	// SupportsEmbeddings / SupportsImages 是 /v1/embeddings 与 /v1/images/*
	// 网关门禁的能力开关。
	SupportsEmbeddings bool `json:"supports_embeddings,omitempty"`
	SupportsImages     bool `json:"supports_images,omitempty"`
	// Icon 与 ColorPreset 驱动全站平台徽章/图标展示。
	Icon        *PlatformIconSpec `json:"icon,omitempty"`
	ColorPreset string            `json:"color_preset,omitempty"`
	// DefaultTestModel 是账号连接测试未指定模型时的默认模型。
	DefaultTestModel string `json:"default_test_model,omitempty"`
}

// platformList 的顺序即平台的展示顺序。
var platformList = []PlatformSpec{
	{ID: PlatformAnthropic, DisplayName: "Anthropic", Gateway: PlatformGatewayAnthropic, LiteLLMProvider: "anthropic"},
	{ID: PlatformOpenAI, DisplayName: "OpenAI", Gateway: PlatformGatewayOpenAI, LiteLLMProvider: "openai", SupportsEmbeddings: true, SupportsImages: true},
	{ID: PlatformGemini, DisplayName: "Gemini", Gateway: PlatformGatewayGemini, LiteLLMProvider: "gemini"},
	{ID: PlatformAntigravity, DisplayName: "Antigravity", Gateway: PlatformGatewayAntigravity, LiteLLMProvider: "anthropic"},
	{ID: PlatformGrok, DisplayName: "Grok", Gateway: PlatformGatewayOpenAI, LiteLLMProvider: "xai", SupportsImages: true},
	{ID: PlatformKimi, DisplayName: "Kimi", Gateway: PlatformGatewayOpenAI, CNProvider: true, LiteLLMProvider: "moonshot"},
	{ID: PlatformZhipu, DisplayName: "Zhipu GLM", Gateway: PlatformGatewayOpenAI, CNProvider: true, LiteLLMProvider: "zhipu"},
	{ID: PlatformDeepseek, DisplayName: "DeepSeek", Gateway: PlatformGatewayOpenAI, CNProvider: true, LiteLLMProvider: "deepseek"},
	{ID: PlatformMiniMax, DisplayName: "MiniMax", Gateway: PlatformGatewayOpenAI, CNProvider: true, LiteLLMProvider: "minimax"},
	{ID: PlatformOpenCodeGo, DisplayName: "OpenCode", Gateway: PlatformGatewayOpenAI, LiteLLMProvider: "opencode-go"},
	{ID: PlatformTypeSafe, DisplayName: "TypeSafe / Jev", Gateway: PlatformGatewayAnthropic, LiteLLMProvider: "typesafe"},
	{ID: PlatformCommandCode, DisplayName: "Command Code", Gateway: PlatformGatewayOpenAI},
	{ID: PlatformCline, DisplayName: "Cline", Gateway: PlatformGatewayOpenAI},
	{ID: PlatformQwen, DisplayName: "Qwen", Gateway: PlatformGatewayOpenAI, LiteLLMProvider: "dashscope"},
}

var platformIndex = func() map[string]int {
	index := make(map[string]int, len(platformList))
	for i, spec := range platformList {
		index[spec.ID] = i
	}
	return index
}()

// Platforms 返回全部具体平台（副本），按展示顺序。
func Platforms() []PlatformSpec {
	dynamic := dynamicPlatformSpecs()
	out := make([]PlatformSpec, 0, len(platformList)+len(dynamic))
	out = append(out, platformList...)
	out = append(out, dynamic...)
	return out
}

// LookupPlatform 返回具体平台的登记信息；composite 与未知值返回 false。
func LookupPlatform(id string) (PlatformSpec, bool) {
	i, ok := platformIndex[id]
	if !ok {
		return lookupDynamicPlatform(id)
	}
	return platformList[i], true
}

// IsConcretePlatform 报告 id 是否为已登记的具体平台（不含 composite）。
func IsConcretePlatform(id string) bool {
	if _, ok := platformIndex[id]; ok {
		return true
	}
	_, ok := lookupDynamicPlatform(id)
	return ok
}

// IsGroupPlatform 报告 id 是否可作为分组平台（具体平台或 composite）。
func IsGroupPlatform(id string) bool {
	return id == PlatformComposite || IsConcretePlatform(id)
}

// ConcretePlatformIDs 返回全部具体平台标识，按展示顺序。
func ConcretePlatformIDs() []string {
	return PlatformIDsWhere(func(PlatformSpec) bool { return true })
}

// PlatformIDsWhere 返回满足条件的具体平台标识，按展示顺序。
func PlatformIDsWhere(match func(PlatformSpec) bool) []string {
	out := make([]string, 0, len(platformList))
	for _, spec := range Platforms() {
		if match(spec) {
			out = append(out, spec.ID)
		}
	}
	return out
}

// compositePrecedenceHead 是组合分组查找顺序的固定前缀（历史顺序：Gemini 先于 OpenAI）。
var compositePrecedenceHead = []string{PlatformAnthropic, PlatformGemini, PlatformOpenAI}

// CompositePrecedencePlatformIDs 返回按平台依次回退时的顺序：组合分组在请求目标
// 解析前逐平台查找定价 / 模型映射（先命中者生效）、拼接默认模型列表，以及调度快照
// 遍历。沿用历史顺序 anthropic、gemini、openai，其余按展示顺序，新登记的平台追加在末尾。
func CompositePrecedencePlatformIDs() []string {
	out := make([]string, 0, len(platformList))
	seen := make(map[string]struct{}, len(platformList))
	for _, id := range compositePrecedenceHead {
		if IsConcretePlatform(id) {
			out = append(out, id)
			seen[id] = struct{}{}
		}
	}
	for _, spec := range platformList {
		if _, ok := seen[spec.ID]; !ok {
			out = append(out, spec.ID)
		}
	}
	for _, spec := range dynamicPlatformSpecs() {
		if _, ok := seen[spec.ID]; !ok {
			out = append(out, spec.ID)
			seen[spec.ID] = struct{}{}
		}
	}
	return out
}

// UsesOpenAIGateway 报告平台是否由 OpenAI 网关处理。
func UsesOpenAIGateway(id string) bool {
	spec, ok := LookupPlatform(id)
	return ok && spec.Gateway == PlatformGatewayOpenAI
}

// IsCNProviderPlatform 报告平台是否为国产单一厂商。
func IsCNProviderPlatform(id string) bool {
	spec, ok := LookupPlatform(id)
	return ok && spec.CNProvider
}
