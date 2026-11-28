package service

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// dynamicProviderProfiles 是自定义渠道商的 ProviderProfile 叠加层。内置 profile
// 是只读单一数据来源，动态快照只负责追加，绝不允许覆盖内置平台。
var dynamicProviderProfiles atomic.Pointer[map[string]*ProviderProfile]

func lookupDynamicProviderProfile(platform string) *ProviderProfile {
	profiles := dynamicProviderProfiles.Load()
	if profiles == nil {
		return nil
	}
	return (*profiles)[platform]
}

// SetDynamicProviderProfiles 整体替换动态 profile 叠加层。
func SetDynamicProviderProfiles(profiles map[string]*ProviderProfile) error {
	next := make(map[string]*ProviderProfile, len(profiles))
	for platform, profile := range profiles {
		id := strings.TrimSpace(platform)
		if id == "" || profile == nil {
			return fmt.Errorf("dynamic provider profile %q is invalid", platform)
		}
		if _, ok := providerProfiles[id]; ok {
			return fmt.Errorf("provider profile %q is builtin and cannot be overridden", id)
		}
		if profile.Platform == "" {
			profile.Platform = id
		}
		next[id] = profile
	}
	dynamicProviderProfiles.Store(&next)
	return nil
}

// ResetDynamicProviderProfiles 清空动态 profile（测试与进程退出时使用）。
func ResetDynamicProviderProfiles() {
	dynamicProviderProfiles.Store(nil)
}

// DynamicProviderProfiles 返回动态 profile 的副本（按平台 ID）。
func DynamicProviderProfiles() map[string]*ProviderProfile {
	profiles := dynamicProviderProfiles.Load()
	if profiles == nil {
		return map[string]*ProviderProfile{}
	}
	out := make(map[string]*ProviderProfile, len(*profiles))
	for platform, profile := range *profiles {
		out[platform] = profile
	}
	return out
}
