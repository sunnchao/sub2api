package domain

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

// PlatformIconSpec 描述自定义平台的图标来源。前端只会以安全的方式渲染：
// builtin 走内置单色 SVG 白名单，url/data_url 走 <img>，letter 走首字母徽标。
type PlatformIconSpec struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

const (
	PlatformIconTypeBuiltin = "builtin"
	PlatformIconTypeURL     = "url"
	PlatformIconTypeDataURL = "data_url"
	PlatformIconTypeLetter  = "letter"
)

// ColorPresets 是自定义平台可选的徽章配色预设，前后端共用同一白名单。
var ColorPresets = []string{
	"blue", "violet", "emerald", "teal", "amber",
	"orange", "rose", "pink", "indigo", "slate",
}

// IsValidColorPreset 报告配色预设是否在白名单内。
func IsValidColorPreset(preset string) bool {
	for _, item := range ColorPresets {
		if item == preset {
			return true
		}
	}
	return false
}

// dynamicPlatformSnapshot 是不可变快照：替换整个指针即可热生效，读路径无需加锁。
type dynamicPlatformSnapshot struct {
	specs []PlatformSpec
	index map[string]int
}

var dynamicPlatforms atomic.Pointer[dynamicPlatformSnapshot]

func currentDynamicSnapshot() *dynamicPlatformSnapshot {
	if snapshot := dynamicPlatforms.Load(); snapshot != nil {
		return snapshot
	}
	return &dynamicPlatformSnapshot{index: map[string]int{}}
}

func dynamicPlatformSpecs() []PlatformSpec {
	return currentDynamicSnapshot().specs
}

func lookupDynamicPlatform(id string) (PlatformSpec, bool) {
	snapshot := currentDynamicSnapshot()
	i, ok := snapshot.index[id]
	if !ok {
		return PlatformSpec{}, false
	}
	return snapshot.specs[i], true
}

// SetDynamicPlatforms 用给定列表整体替换动态平台叠加层。内置平台不可被覆盖或重名，
// 重复 ID 与空 ID 会被拒绝；调用成功后所有读路径立即可见（原子指针替换）。
func SetDynamicPlatforms(specs []PlatformSpec) error {
	ordered := make([]PlatformSpec, 0, len(specs))
	index := make(map[string]int, len(specs))
	for _, spec := range specs {
		id := strings.TrimSpace(spec.ID)
		if id == "" {
			return fmt.Errorf("custom platform id is required")
		}
		if _, ok := platformIndex[id]; ok {
			return fmt.Errorf("platform %q is a builtin platform and cannot be overridden", id)
		}
		if _, ok := index[id]; ok {
			return fmt.Errorf("duplicate custom platform %q", id)
		}
		spec.ID = id
		spec.Custom = true
		if spec.Gateway == "" {
			spec.Gateway = PlatformGatewayOpenAI
		}
		if spec.ColorPreset == "" {
			spec.ColorPreset = "slate"
		}
		index[id] = len(ordered)
		ordered = append(ordered, spec)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].SortOrder != ordered[j].SortOrder {
			return ordered[i].SortOrder < ordered[j].SortOrder
		}
		return ordered[i].ID < ordered[j].ID
	})
	// index 需按排序后的顺序重建。
	for i := range ordered {
		index[ordered[i].ID] = i
	}
	dynamicPlatforms.Store(&dynamicPlatformSnapshot{specs: ordered, index: index})
	return nil
}

// ResetDynamicPlatforms 清空动态平台叠加层（测试与进程退出时使用）。
func ResetDynamicPlatforms() {
	dynamicPlatforms.Store(nil)
}

// DynamicPlatforms 返回自定义平台快照（副本）。
func DynamicPlatforms() []PlatformSpec {
	dynamic := dynamicPlatformSpecs()
	out := make([]PlatformSpec, len(dynamic))
	copy(out, dynamic)
	return out
}

// EnabledPlatforms 返回可用于新建账号/分组的平台（过滤已禁用的自定义平台）。
func EnabledPlatforms() []PlatformSpec {
	all := Platforms()
	out := make([]PlatformSpec, 0, len(all))
	for _, spec := range all {
		if !spec.Disabled {
			out = append(out, spec)
		}
	}
	return out
}

// IsEnabledConcretePlatform 报告平台是否为可新建账号的具体平台。
func IsEnabledConcretePlatform(id string) bool {
	spec, ok := LookupPlatform(id)
	return ok && !spec.Disabled
}

// IsEnabledGroupPlatform 报告平台是否可作为新建分组的平台。
func IsEnabledGroupPlatform(id string) bool {
	return id == PlatformComposite || IsEnabledConcretePlatform(id)
}

// IsBuiltinPlatform 报告 id 是否为代码内置平台（不可改删）。
func IsBuiltinPlatform(id string) bool {
	_, ok := platformIndex[id]
	return ok
}

// IsCustomPlatform 报告 id 是否为后台登记的自定义平台。
func IsCustomPlatform(id string) bool {
	return !IsBuiltinPlatform(id) && IsConcretePlatform(id)
}

// SupportsEmbeddings 报告平台是否允许 /v1/embeddings；已禁用的自定义平台仍允许，
// 以免中断既有账号。
func SupportsEmbeddings(id string) bool {
	spec, ok := LookupPlatform(id)
	return ok && spec.SupportsEmbeddings
}

// SupportsImages 报告平台是否允许 /v1/images/*。
func SupportsImages(id string) bool {
	spec, ok := LookupPlatform(id)
	return ok && spec.SupportsImages
}
