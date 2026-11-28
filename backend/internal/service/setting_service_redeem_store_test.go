//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSettingService_GetPublicSettings_ExposesRedeemStoreConfig(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{
		values: map[string]string{
			SettingKeyRedeemStoreURL:      "  https://shop.example.com/cards  ",
			SettingKeyRedeemStoreOpenMode: "iframe",
			SettingKeyRedeemStoreLabel:    "  官方商城  ",
		},
	}, &config.Config{})

	public, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, "https://shop.example.com/cards", public.RedeemStoreURL)
	require.Equal(t, RedeemStoreOpenModeIframe, public.RedeemStoreOpenMode)
	require.Equal(t, "官方商城", public.RedeemStoreLabel)

	injectedAny, err := svc.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)
	injected, ok := injectedAny.(*PublicSettingsInjectionPayload)
	require.True(t, ok)
	require.Equal(t, "https://shop.example.com/cards", injected.RedeemStoreURL)
	require.Equal(t, RedeemStoreOpenModeIframe, injected.RedeemStoreOpenMode)
	require.Equal(t, "官方商城", injected.RedeemStoreLabel)

	// Missing or invalid open mode normalizes to new_tab
	emptySvc := NewSettingService(&settingPublicRepoStub{
		values: map[string]string{
			SettingKeyRedeemStoreOpenMode: "invalid",
		},
	}, &config.Config{})
	emptyPublic, err := emptySvc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, "", emptyPublic.RedeemStoreURL)
	require.Equal(t, RedeemStoreOpenModeNewTab, emptyPublic.RedeemStoreOpenMode)
	require.Equal(t, "", emptyPublic.RedeemStoreLabel)
}

func TestSettingService_GetFrameSrcOrigins_IncludesRedeemStoreOnlyInIframeMode(t *testing.T) {
	iframeSvc := NewSettingService(&settingPublicRepoStub{
		values: map[string]string{
			SettingKeyRedeemStoreURL:      "https://cards.example.com/buy?id=1",
			SettingKeyRedeemStoreOpenMode: RedeemStoreOpenModeIframe,
		},
	}, &config.Config{})

	origins, err := iframeSvc.GetFrameSrcOrigins(context.Background())
	require.NoError(t, err)
	require.Contains(t, origins, "https://cards.example.com")

	newTabSvc := NewSettingService(&settingPublicRepoStub{
		values: map[string]string{
			SettingKeyRedeemStoreURL:      "https://cards.example.com/buy?id=1",
			SettingKeyRedeemStoreOpenMode: RedeemStoreOpenModeNewTab,
		},
	}, &config.Config{})

	newTabOrigins, err := newTabSvc.GetFrameSrcOrigins(context.Background())
	require.NoError(t, err)
	require.NotContains(t, newTabOrigins, "https://cards.example.com")

	emptyURLSvc := NewSettingService(&settingPublicRepoStub{
		values: map[string]string{
			SettingKeyRedeemStoreURL:      "",
			SettingKeyRedeemStoreOpenMode: RedeemStoreOpenModeIframe,
		},
	}, &config.Config{})

	emptyOrigins, err := emptyURLSvc.GetFrameSrcOrigins(context.Background())
	require.NoError(t, err)
	require.Empty(t, emptyOrigins)
}
