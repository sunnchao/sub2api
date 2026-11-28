//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDynamicProviderProfileDrivesCatalogAndAccountEndpoints(t *testing.T) {
	t.Cleanup(ResetDynamicProviderProfiles)
	t.Cleanup(domain.ResetDynamicPlatforms)

	require.NoError(t, domain.SetDynamicPlatforms([]domain.PlatformSpec{{
		ID:                 "siliconflow",
		DisplayName:        "SiliconFlow",
		Gateway:            domain.PlatformGatewayOpenAI,
		Custom:             true,
		SortOrder:          5,
		LiteLLMProvider:    "siliconflow",
		DefaultTestModel:   "Qwen/Qwen2.5-7B-Instruct",
		SupportsEmbeddings: true,
		SupportsImages:     true,
		ColorPreset:        "violet",
		Icon:               &domain.PlatformIconSpec{Type: domain.PlatformIconTypeLetter, Value: "S"},
	}}))

	require.NoError(t, SetDynamicProviderProfiles(map[string]*ProviderProfile{
		"siliconflow": {
			Platform:         "siliconflow",
			DefaultMode:      "default",
			Routing:          ProviderRoutingByInbound,
			DefaultTestModel: "Qwen/Qwen2.5-7B-Instruct",
			Modes: map[string]ProviderEndpoints{
				"default": {
					Label: "默认端点",
					BaseURLs: map[string]string{
						APIProtocolChatCompletions: "https://api.siliconflow.cn/v1",
						APIProtocolAnthropic:       "https://api.siliconflow.cn/anthropic",
					},
					EmbeddingsBaseURL: "https://api.siliconflow.cn/v1",
					ImagesBaseURL:     "https://api.siliconflow.cn/v1",
				},
			},
		},
	}))

	profile := LookupProviderProfile("siliconflow")
	require.NotNil(t, profile)
	require.True(t, IsMultiProtocolAPIKeyProvider("siliconflow"))
	require.Equal(t, "https://api.siliconflow.cn/anthropic", profile.DefaultBaseURL("default", APIProtocolAnthropic))
	require.Equal(t, "https://api.siliconflow.cn/v1", profile.DefaultEmbeddingsBaseURL("default"))
	require.Equal(t, "https://api.siliconflow.cn/v1", profile.DefaultImagesBaseURL("default"))
	require.Empty(t, profile.DefaultBaseURL("default", APIProtocolResponses))

	account := &Account{
		Platform: "siliconflow",
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_protocol": APIProtocolAdaptive,
		},
	}
	require.Equal(t, "https://api.siliconflow.cn/v1", account.GetOpenAIBaseURL())
	require.Equal(t, "https://api.siliconflow.cn/anthropic", account.GetCNProtocolBaseURL(APIProtocolAnthropic))
	require.Equal(t, "https://api.siliconflow.cn/v1", account.GetProtocolBaseURL(APIProtocolEmbeddings))
	require.Equal(t, "https://api.siliconflow.cn/v1", account.GetProtocolBaseURL(APIProtocolImages))

	// Credential overrides win over the dynamic defaults.
	account.Credentials["base_url"] = "https://private-mirror.example.com/v1"
	require.Equal(t, "https://private-mirror.example.com/v1", account.GetProtocolBaseURL(APIProtocolEmbeddings))
	require.Equal(t, "https://private-mirror.example.com/v1", account.GetProtocolBaseURL(APIProtocolImages))

	// Explicit per-protocol override wins over base_url.
	account.Credentials["api_base_urls"] = map[string]any{
		APIProtocolEmbeddings: "https://embed.example.com/v1",
	}
	require.Equal(t, "https://embed.example.com/v1", account.GetProtocolBaseURL(APIProtocolEmbeddings))

	// Anthropic-protocol accounts must not leak their /anthropic base_url into OpenAI embeddings/images.
	anthropicOnlyAcc := &Account{
		Platform: "siliconflow",
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_protocol": APIProtocolAnthropic,
			"base_url":     "https://mirror.example.com/anthropic",
		},
	}
	require.Equal(t, "https://api.siliconflow.cn/v1", anthropicOnlyAcc.GetProtocolBaseURL(APIProtocolEmbeddings))

	catalog := BuildPlatformCatalog()
	var entry *PlatformCatalogEntry
	for i := range catalog.Platforms {
		if catalog.Platforms[i].ID == "siliconflow" {
			entry = &catalog.Platforms[i]
			break
		}
	}
	require.NotNil(t, entry)
	require.True(t, entry.Custom)
	require.Equal(t, "SiliconFlow", entry.DisplayName)
	require.Equal(t, "siliconflow", entry.LiteLLMProvider)
	require.Equal(t, "Qwen/Qwen2.5-7B-Instruct", entry.DefaultTestModel)
	require.True(t, entry.SupportsEmbeddings)
	require.True(t, entry.SupportsImages)
	require.Equal(t, "violet", entry.ColorPreset)
	require.NotNil(t, entry.Icon)
	require.Equal(t, domain.PlatformIconTypeLetter, entry.Icon.Type)
	require.NotNil(t, entry.MultiProtocol)
	require.Equal(t, "默认端点", entry.MultiProtocol.Modes[0].Label)
	require.Equal(t, "https://api.siliconflow.cn/v1", entry.MultiProtocol.Modes[0].BaseURLs[APIProtocolChatCompletions])
	require.Equal(t, "https://api.siliconflow.cn/v1", entry.MultiProtocol.Modes[0].BaseURLs[APIProtocolEmbeddings])
}

func TestDynamicProviderProfilesCannotOverrideBuiltins(t *testing.T) {
	t.Cleanup(ResetDynamicProviderProfiles)
	require.Error(t, SetDynamicProviderProfiles(map[string]*ProviderProfile{
		PlatformKimi: {Platform: PlatformKimi, DefaultMode: AccountModePayG},
	}))
	require.NotNil(t, LookupProviderProfile(PlatformKimi))
	require.Equal(t, AccountModePayG, LookupProviderProfile(PlatformKimi).DefaultMode)
}

func TestCustomPlatformMissingBaseURLNeverFallsBackToOpenAI(t *testing.T) {
	t.Cleanup(ResetDynamicProviderProfiles)
	t.Cleanup(domain.ResetDynamicPlatforms)

	require.NoError(t, domain.SetDynamicPlatforms([]domain.PlatformSpec{{
		ID:                 "anthropic_only_custom",
		DisplayName:        "Anthropic Only Custom",
		Gateway:            domain.PlatformGatewayOpenAI,
		Custom:             true,
		SupportsEmbeddings: true,
		SupportsImages:     true,
	}}))
	require.NoError(t, SetDynamicProviderProfiles(map[string]*ProviderProfile{
		"anthropic_only_custom": {
			Platform:    "anthropic_only_custom",
			DefaultMode: "default",
			Modes: map[string]ProviderEndpoints{
				"default": {
					BaseURLs: map[string]string{
						APIProtocolAnthropic: "https://custom.example.com/anthropic",
					},
				},
			},
		},
	}))

	account := &Account{
		ID:       99,
		Platform: "anthropic_only_custom",
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":      "sk-third-party-secret",
			"api_protocol": APIProtocolAnthropic,
		},
	}
	require.Empty(t, account.GetProtocolBaseURL(APIProtocolEmbeddings))
	require.Empty(t, account.GetProtocolBaseURL(APIProtocolImages))
	require.True(t, DefaultAllowImageGenerationForPlatform("anthropic_only_custom"))

	gw := &OpenAIGatewayService{cfg: &config.Config{}}
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil)

	_, err := gw.ForwardEmbeddings(context.Background(), c, account, []byte(`{"model":"bge-m3","input":"hi"}`), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing embeddings base_url")

	_, err = gw.buildOpenAIImagesRequest(context.Background(), c, account, []byte(`{"model":"flux-schnell","prompt":"cat"}`), "application/json", "sk-third-party-secret", openAIImagesGenerationsEndpoint)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing images base_url")
}

func TestCustomPlatformImagesBuildRequestAndModelValidation(t *testing.T) {
	t.Cleanup(ResetDynamicProviderProfiles)
	t.Cleanup(domain.ResetDynamicPlatforms)

	var seenPath string
	var seenAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"AA=="}]}`))
	}))
	defer upstream.Close()

	require.NoError(t, domain.SetDynamicPlatforms([]domain.PlatformSpec{{
		ID:                 "img_custom",
		DisplayName:        "Img Custom",
		Gateway:            domain.PlatformGatewayOpenAI,
		Custom:             true,
		SupportsEmbeddings: false,
		SupportsImages:     true,
	}}))
	require.NoError(t, SetDynamicProviderProfiles(map[string]*ProviderProfile{
		"img_custom": {
			Platform:    "img_custom",
			DefaultMode: "default",
			Modes: map[string]ProviderEndpoints{
				"default": {
					BaseURLs:      map[string]string{APIProtocolChatCompletions: upstream.URL + "/v1"},
					ImagesBaseURL: upstream.URL + "/v1",
				},
			},
		},
	}))

	account := &Account{
		ID:          101,
		Platform:    "img_custom",
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-custom-img"},
	}
	require.NoError(t, validateImagesModelForAccount("flux-schnell", account))
	require.Error(t, validateImagesModelForAccount("", account))
	require.True(t, DefaultAllowImageGenerationForPlatform("img_custom"))

	gw := &OpenAIGatewayService{
		cfg: &config.Config{
			Security: config.SecurityConfig{
				URLAllowlist: config.URLAllowlistConfig{
					Enabled:           false,
					AllowInsecureHTTP: true,
					AllowPrivateHosts: true,
				},
			},
		},
	}
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	req, err := gw.buildOpenAIImagesRequest(
		context.Background(), c, account,
		[]byte(`{"model":"flux-schnell","prompt":"sunset"}`),
		"application/json", "sk-custom-img", openAIImagesGenerationsEndpoint,
	)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, "/v1/images/generations", seenPath)
	require.Equal(t, "Bearer sk-custom-img", seenAuth)
}

func TestPricingServiceListProviders(t *testing.T) {
	ps := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"openrouter/auto":       {LiteLLMProvider: "openrouter"},
			"groq/llama-3":          {LiteLLMProvider: "groq"},
			"together/qwen":         {LiteLLMProvider: "together_ai"},
			"openrouter/claude-3.5": {LiteLLMProvider: "openrouter"},
			"empty":                 {LiteLLMProvider: "  "},
		},
	}
	require.Equal(t, []string{"groq", "openrouter", "together_ai"}, ps.ListProviders())
}
