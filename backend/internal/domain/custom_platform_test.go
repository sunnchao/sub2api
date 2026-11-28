package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDynamicPlatformOverlay(t *testing.T) {
	t.Cleanup(ResetDynamicPlatforms)

	err := SetDynamicPlatforms([]PlatformSpec{
		{
			ID:                 "siliconflow",
			DisplayName:        "SiliconFlow",
			Gateway:            PlatformGatewayOpenAI,
			Custom:             true,
			SortOrder:          10,
			SupportsEmbeddings: true,
			ColorPreset:        "violet",
		},
		{
			ID:          "disabled_platform",
			DisplayName: "Disabled",
			Gateway:     PlatformGatewayOpenAI,
			Custom:      true,
			Disabled:    true,
			SortOrder:   20,
		},
	})
	require.NoError(t, err)

	// Dynamic platforms join the full list but only enabled ones are selectable.
	require.True(t, IsConcretePlatform("siliconflow"))
	require.True(t, IsGroupPlatform("siliconflow"))
	require.True(t, IsCustomPlatform("siliconflow"))
	require.False(t, IsBuiltinPlatform("siliconflow"))
	require.ElementsMatch(t, []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok,
		PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo, PlatformTypeSafe,
		PlatformCommandCode, PlatformCline, PlatformQwen, "siliconflow"},
		EnabledPlatformIDsForTest())
	require.Contains(t, ConcretePlatformIDs(), "disabled_platform",
		"disabled platforms stay resolvable for existing accounts")
	require.NotContains(t, EnabledPlatformIDsForTest(), "disabled_platform")
	require.False(t, IsEnabledConcretePlatform("disabled_platform"))
	require.False(t, IsEnabledGroupPlatform("disabled_platform"))
	require.True(t, IsEnabledConcretePlatform("siliconflow"))

	require.True(t, UsesOpenAIGateway("siliconflow"))
	require.True(t, SupportsEmbeddings("siliconflow"))
	require.False(t, SupportsImages("siliconflow"))
	require.Contains(t, CompositePrecedencePlatformIDs(), "siliconflow")
}

func TestDynamicPlatformsCannotOverrideBuiltins(t *testing.T) {
	t.Cleanup(ResetDynamicPlatforms)

	err := SetDynamicPlatforms([]PlatformSpec{{
		ID:          PlatformOpenAI,
		DisplayName: "Hijacked",
		Gateway:     PlatformGatewayOpenAI,
		Custom:      true,
	}})
	require.Error(t, err)
	require.Equal(t, "OpenAI", Platforms()[1].DisplayName)

	require.Error(t, SetDynamicPlatforms([]PlatformSpec{
		{ID: "dup", DisplayName: "A", Gateway: PlatformGatewayOpenAI, Custom: true},
		{ID: "dup", DisplayName: "B", Gateway: PlatformGatewayOpenAI, Custom: true},
	}))
}

func TestBuiltinPlatformCapabilities(t *testing.T) {
	openaiSpec, ok := LookupPlatform(PlatformOpenAI)
	require.True(t, ok)
	require.True(t, openaiSpec.SupportsEmbeddings)
	require.True(t, openaiSpec.SupportsImages)
	grokSpec, ok := LookupPlatform(PlatformGrok)
	require.True(t, ok)
	require.False(t, grokSpec.SupportsEmbeddings)
	require.True(t, grokSpec.SupportsImages)

	require.True(t, SupportsEmbeddings(PlatformOpenAI))
	require.True(t, SupportsImages(PlatformOpenAI))
	require.True(t, SupportsImages(PlatformGrok))
	require.False(t, SupportsEmbeddings(PlatformAnthropic))
	require.False(t, SupportsImages(PlatformKimi))
	require.True(t, IsBuiltinPlatform(PlatformOpenAI))
	require.False(t, IsCustomPlatform(PlatformOpenAI))
}

func EnabledPlatformIDsForTest() []string {
	out := make([]string, 0)
	for _, spec := range EnabledPlatforms() {
		out = append(out, spec.ID)
	}
	return out
}
