//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

type fakeCustomPlatformRepo struct {
	platforms map[string]*CustomPlatform
	usage     map[string]CustomPlatformUsageCounts
	createErr error
}

func newFakeCustomPlatformRepo() *fakeCustomPlatformRepo {
	return &fakeCustomPlatformRepo{
		platforms: map[string]*CustomPlatform{},
		usage:     map[string]CustomPlatformUsageCounts{},
	}
}

func (r *fakeCustomPlatformRepo) List(context.Context) ([]*CustomPlatform, error) {
	out := make([]*CustomPlatform, 0, len(r.platforms))
	for _, platform := range r.platforms {
		clone := *platform
		out = append(out, &clone)
	}
	return out, nil
}

func (r *fakeCustomPlatformRepo) Get(_ context.Context, id string) (*CustomPlatform, error) {
	platform, ok := r.platforms[id]
	if !ok {
		return nil, nil
	}
	clone := *platform
	return &clone, nil
}

func (r *fakeCustomPlatformRepo) Exists(_ context.Context, id string) (bool, error) {
	_, ok := r.platforms[id]
	return ok, nil
}

func (r *fakeCustomPlatformRepo) Create(_ context.Context, platform *CustomPlatform) (*CustomPlatform, error) {
	if r.createErr != nil {
		return nil, r.createErr
	}
	clone := *platform
	r.platforms[platform.ID] = &clone
	return &clone, nil
}

func (r *fakeCustomPlatformRepo) Update(_ context.Context, platform *CustomPlatform) (*CustomPlatform, error) {
	clone := *platform
	r.platforms[platform.ID] = &clone
	return &clone, nil
}

func (r *fakeCustomPlatformRepo) Delete(_ context.Context, id string) error {
	delete(r.platforms, id)
	return nil
}

func (r *fakeCustomPlatformRepo) CountUsage(_ context.Context, id string) (CustomPlatformUsageCounts, error) {
	return r.usage[id], nil
}

type fakeCustomPlatformCache struct {
	notified   int
	onUpdate   func()
	subCtx     context.Context
	subscribed bool
}

func (c *fakeCustomPlatformCache) NotifyUpdate(context.Context) error { c.notified++; return nil }
func (c *fakeCustomPlatformCache) SubscribeUpdates(ctx context.Context, fn func()) {
	c.subscribed = true
	c.subCtx = ctx
	c.onUpdate = fn
}

func newTestCustomPlatform(id, name string) *CustomPlatform {
	return &CustomPlatform{
		ID:                 id,
		DisplayName:        name,
		Enabled:            true,
		ColorPreset:        "violet",
		SupportsEmbeddings: true,
		Profile: CustomPlatformProfile{
			DefaultMode: "default",
			Routing:     ProviderRoutingByInbound,
			Modes: []CustomPlatformMode{{
				Mode:  "default",
				Label: "默认端点",
				BaseURLs: map[string]string{
					APIProtocolChatCompletions: "https://api.siliconflow.cn/v1",
					APIProtocolAnthropic:       "https://api.siliconflow.cn/anthropic",
				},
			}},
		},
	}
}

func TestCustomPlatformServiceCreateInstallsRegistry(t *testing.T) {
	t.Cleanup(domain.ResetDynamicPlatforms)
	t.Cleanup(ResetDynamicProviderProfiles)

	repo := newFakeCustomPlatformRepo()
	cache := &fakeCustomPlatformCache{}
	svc := NewCustomPlatformService(repo, cache, nil)

	created, err := svc.Create(context.Background(), newTestCustomPlatform("siliconflow", "SiliconFlow"))
	require.NoError(t, err)
	require.Equal(t, "siliconflow", created.ID)
	require.Equal(t, 1, cache.notified)

	require.True(t, domain.IsConcretePlatform("siliconflow"))
	require.True(t, domain.IsEnabledConcretePlatform("siliconflow"))
	require.True(t, domain.SupportsEmbeddings("siliconflow"))
	profile := LookupProviderProfile("siliconflow")
	require.NotNil(t, profile)
	require.Equal(t, "https://api.siliconflow.cn/v1", profile.DefaultEmbeddingsBaseURL("default"))
}

func TestCustomPlatformServiceValidation(t *testing.T) {
	t.Cleanup(domain.ResetDynamicPlatforms)
	t.Cleanup(ResetDynamicProviderProfiles)
	svc := NewCustomPlatformService(newFakeCustomPlatformRepo(), nil, nil)
	ctx := context.Background()

	cases := []struct {
		name   string
		mutate func(*CustomPlatform)
		field  string
	}{
		{"bad id", func(p *CustomPlatform) { p.ID = "SiliconFlow" }, "id"},
		{"builtin id", func(p *CustomPlatform) { p.ID = "openai" }, "id"},
		{"reserved id", func(p *CustomPlatform) { p.ID = "composite" }, "id"},
		{"empty name", func(p *CustomPlatform) { p.DisplayName = "  " }, "display_name"},
		{"bad color", func(p *CustomPlatform) { p.ColorPreset = "rainbow" }, "color_preset"},
		{"bad icon scheme", func(p *CustomPlatform) {
			p.Icon = domain.PlatformIconSpec{Type: domain.PlatformIconTypeURL, Value: "javascript:alert(1)"}
		}, "icon.value"},
		{"malformed https icon url", func(p *CustomPlatform) {
			p.Icon = domain.PlatformIconSpec{Type: domain.PlatformIconTypeURL, Value: "https://"}
		}, "icon.value"},
		{"unsafe svg", func(p *CustomPlatform) {
			p.Icon = domain.PlatformIconSpec{
				Type:  domain.PlatformIconTypeDataURL,
				Value: "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg><script>alert(1)</script></svg>")),
			}
		}, "icon.value"},
		{"unsafe svg slash onload bypass", func(p *CustomPlatform) {
			p.Icon = domain.PlatformIconSpec{
				Type:  domain.PlatformIconTypeDataURL,
				Value: "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg/onload=alert(1)></svg>")),
			}
		}, "icon.value"},
		{"no dialogue protocol", func(p *CustomPlatform) {
			p.Profile.Modes[0].BaseURLs = map[string]string{APIProtocolEmbeddings: "https://api.example.com/v1"}
		}, "multi_protocol.modes[0].base_urls"},
		{"extra mode missing dialogue protocol", func(p *CustomPlatform) {
			p.Profile.Modes = append(p.Profile.Modes, CustomPlatformMode{
				Mode:     "intl",
				Label:    "国际",
				BaseURLs: map[string]string{APIProtocolEmbeddings: "https://intl.example.com/v1"},
			})
		}, "multi_protocol.modes[1].base_urls"},
		{"bad routing", func(p *CustomPlatform) { p.Profile.Routing = "random" }, "multi_protocol.routing"},
		{"bad responses path", func(p *CustomPlatform) { p.Profile.ResponsesPath = "../responses" }, "multi_protocol.responses_path"},
		{"unknown protocol", func(p *CustomPlatform) {
			p.Profile.Modes[0].BaseURLs["gemini"] = "https://example.com"
		}, "multi_protocol.modes[0].base_urls"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			platform := newTestCustomPlatform("siliconflow", "SiliconFlow")
			tc.mutate(platform)
			_, err := svc.Create(ctx, platform)
			var validationErr *CustomPlatformValidationError
			require.ErrorAs(t, err, &validationErr)
			require.Equal(t, tc.field, validationErr.Field)
		})
	}
}

func TestCustomPlatformServiceDeleteProtection(t *testing.T) {
	t.Cleanup(domain.ResetDynamicPlatforms)
	t.Cleanup(ResetDynamicProviderProfiles)

	repo := newFakeCustomPlatformRepo()
	svc := NewCustomPlatformService(repo, nil, nil)
	ctx := context.Background()
	_, err := svc.Create(ctx, newTestCustomPlatform("siliconflow", "SiliconFlow"))
	require.NoError(t, err)

	repo.usage["siliconflow"] = CustomPlatformUsageCounts{Accounts: 2}
	err = svc.Delete(ctx, "siliconflow")
	var inUse *CustomPlatformInUseError
	require.ErrorAs(t, err, &inUse)
	require.Equal(t, int64(2), inUse.Usage.Accounts)
	require.True(t, domain.IsConcretePlatform("siliconflow"))

	repo.usage["siliconflow"] = CustomPlatformUsageCounts{}
	require.NoError(t, svc.Delete(ctx, "siliconflow"))
	require.False(t, domain.IsConcretePlatform("siliconflow"))
}

func TestCustomPlatformServiceUpdateRejectsIDChange(t *testing.T) {
	t.Cleanup(domain.ResetDynamicPlatforms)
	t.Cleanup(ResetDynamicProviderProfiles)

	svc := NewCustomPlatformService(newFakeCustomPlatformRepo(), nil, nil)
	ctx := context.Background()
	_, err := svc.Create(ctx, newTestCustomPlatform("siliconflow", "SiliconFlow"))
	require.NoError(t, err)

	update := newTestCustomPlatform("other", "Other")
	update.ID = "renamed"
	_, err = svc.Update(ctx, "siliconflow", update)
	var validationErr *CustomPlatformValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "id", validationErr.Field)
}

func TestCustomPlatformServiceNotFound(t *testing.T) {
	svc := NewCustomPlatformService(newFakeCustomPlatformRepo(), nil, nil)
	_, err := svc.Update(context.Background(), "missing", newTestCustomPlatform("missing", "Missing"))
	require.True(t, errors.Is(err, ErrCustomPlatformNotFound))
}

func TestCustomPlatformServiceEnforcesURLAllowlist(t *testing.T) {
	t.Cleanup(domain.ResetDynamicPlatforms)
	t.Cleanup(ResetDynamicProviderProfiles)
	validator := func(string) (string, error) {
		return "", errors.New("host is not in security.url_allowlist.upstream_hosts")
	}
	svc := NewCustomPlatformService(newFakeCustomPlatformRepo(), nil, validator)
	_, err := svc.Create(context.Background(), newTestCustomPlatform("siliconflow", "SiliconFlow"))
	var validationErr *CustomPlatformValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "multi_protocol.modes[0].base_urls.chat_completions", validationErr.Field)
	require.Contains(t, validationErr.Message, "url_allowlist")
}

func TestCustomPlatformServiceConcurrentReloadAndClose(t *testing.T) {
	t.Cleanup(domain.ResetDynamicPlatforms)
	t.Cleanup(ResetDynamicProviderProfiles)

	repo := newFakeCustomPlatformRepo()
	cache := &fakeCustomPlatformCache{}
	svc := NewCustomPlatformService(repo, cache, nil)
	require.True(t, cache.subscribed)
	require.NotNil(t, cache.subCtx)

	_, err := svc.Create(context.Background(), newTestCustomPlatform("siliconflow", "SiliconFlow"))
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, svc.Reload(context.Background()))
		}()
	}
	wg.Wait()

	require.True(t, domain.IsConcretePlatform("siliconflow"))
	require.NotNil(t, LookupProviderProfile("siliconflow"))

	svc.Close()
	require.ErrorIs(t, cache.subCtx.Err(), context.Canceled)
}
