package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBuildGeminiAIStudioModelActionURL(t *testing.T) {
	const base = "https://generativelanguage.googleapis.com"

	got, err := buildGeminiAIStudioModelActionURL(base, "gemini-2.5-pro", "generateContent", false)
	require.NoError(t, err)
	require.Equal(t, base+"/v1beta/models/gemini-2.5-pro:generateContent", got)

	got, err = buildGeminiAIStudioModelActionURL(base+"/", " gemini-2.5-flash ", "streamGenerateContent", true)
	require.NoError(t, err)
	require.Equal(t, base+"/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse", got)

	got, err = buildGeminiAIStudioModelActionURL(base, "gemini-2.5-pro", "countTokens", false)
	require.NoError(t, err)
	require.Equal(t, base+"/v1beta/models/gemini-2.5-pro:countTokens", got)
}

// TestBuildGeminiAIStudioModelActionURLRejectsNonConformingModel 锁定不变式：
// 模型名来自客户端（native 路由的 URL 片段 / compat 路由的请求体），
// 只有合规的路径片段才允许拼进上游 URL。
func TestBuildGeminiAIStudioModelActionURLRejectsNonConformingModel(t *testing.T) {
	const base = "https://generativelanguage.googleapis.com"

	for _, model := range []string{
		"../../x/y",
		"..",
		".",
		"gemini-2.5-pro/../../x",
		`..\..\x`,
		"gemini-2.5-pro?a=b",
		"gemini-2.5-pro#frag",
		"gemini-2.5-pro%2f..",
		"gemini 2.5 pro",
		"gemini\x00pro",
		"gemini-2.5-pro@001",
		"gemini~pro",
		"models/gemini-2.5-pro",
		"...",
		"",
		"   ",
	} {
		t.Run("model_"+model, func(t *testing.T) {
			_, err := buildGeminiAIStudioModelActionURL(base, model, "generateContent", false)
			require.Error(t, err, "model %q must be rejected", model)
			require.False(t, IsSafeGeminiModelPathSegment(model))
		})
	}

	// action 只允许已知取值，避免未来把可变字符串拼进 path。
	_, err := buildGeminiAIStudioModelActionURL(base, "gemini-2.5-pro", "deleteModel", false)
	require.Error(t, err)

	_, err = buildGeminiAIStudioModelActionURL("", "gemini-2.5-pro", "generateContent", false)
	require.Error(t, err)
}

func TestBuildGeminiAIStudioMappedModelActionURL(t *testing.T) {
	const base = "https://bearlab.ai"

	t.Run("accepts admin-mapped relay models and escapes path segment", func(t *testing.T) {
		cases := []struct {
			model  string
			stream bool
			want   string
		}{
			{
				model: "[AIS]gemini-3.8-flash",
				want:  base + "/v1beta/models/%5BAIS%5Dgemini-3.8-flash:generateContent",
			},
			{
				model:  "[E]gemini-4-argon",
				stream: true,
				want:   base + "/v1beta/models/%5BE%5Dgemini-4-argon:streamGenerateContent?alt=sse",
			},
			{
				model: "[gcli转] gemini-3.1-pro-preview [假流]",
				want:  base + "/v1beta/models/%5Bgcli%E8%BD%AC%5D%20gemini-3.1-pro-preview%20%5B%E5%81%87%E6%B5%81%5D:generateContent",
			},
			{
				model: "【流式】gemini-3.8-flash(pro)+search",
				want:  base + "/v1beta/models/%E3%80%90%E6%B5%81%E5%BC%8F%E3%80%91gemini-3.8-flash%28pro%29+search:generateContent",
			},
		}
		for _, tc := range cases {
			action := "generateContent"
			if tc.stream {
				action = "streamGenerateContent"
			}
			got, err := buildGeminiAIStudioMappedModelActionURL(base, tc.model, action, tc.stream, true)
			require.NoError(t, err, "mapped model %q should be allowed", tc.model)
			require.Equal(t, tc.want, got)

			// Same model remains rejected when untrusted (unmapped client input).
			_, err = buildGeminiAIStudioMappedModelActionURL(base, tc.model, action, tc.stream, false)
			require.Error(t, err, "unmapped model %q must remain rejected", tc.model)
		}
	})

	t.Run("rejects characters outside closed allowlist even when mapped", func(t *testing.T) {
		for _, model := range []string{
			"",
			"   ",
			".",
			"..",
			"...",
			"../../x",
			"models/gemini-3.8-flash",
			"google/gemini-3.8-flash",
			`a\b`,
			"gemini-3.8-flash?alt=sse",
			"gemini-3.8-flash#frag",
			"gemini-3.8-flash%2f..",
			"gemini-3.8-flash:free",
			"gemini-*",
			"gemini;drop",
			"gemini@001",
			"gemini\"quote",
			"gemini\tflash",
			"gemini\u00a0flash",
			"gemini\u202eflash",
			"gemini∕flash",
			"gemini／flash",
			"gemini\x00flash",
			"gemini\nflash",
			strings.Repeat("a", maxUpstreamPathSegmentLen+1),
		} {
			_, err := buildGeminiAIStudioMappedModelActionURL(base, model, "generateContent", false, true)
			require.Error(t, err, "mapped model %q must be rejected", model)
		}
	})
}

func TestGeminiForwardGetModel_AccountMappedRelayModel(t *testing.T) {
	stub := &geminiMappedURLUpstreamStub{
		body: `{"name":"models/[AIS]gemini-3.8-flash"}`,
	}
	svc := &GeminiMessagesCompatService{
		httpUpstream: stub,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:       134,
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-key",
			"base_url": "https://bearlab.ai",
			"model_mapping": map[string]any{
				"gemini-3.8-flash": "[AIS]gemini-3.8-flash",
			},
		},
	}

	res, err := svc.ForwardGetModel(context.Background(), account, "gemini-3.8-flash")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NotNil(t, stub.lastReq)
	require.Equal(t, "/v1beta/models/%5BAIS%5Dgemini-3.8-flash", stub.lastReq.URL.EscapedPath())

	// Composite explicit rewrite without account mapping is also trusted.
	compositeCtx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched:        true,
		TargetPlatform: PlatformGemini,
		PublicModel:    "gemini-3.8-flash",
		UpstreamModel:  "[AIS]gemini-3.8-flash",
	})
	plainAccount := &Account{
		ID:       135,
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-key",
			"base_url": "https://bearlab.ai",
		},
	}
	res, err = svc.ForwardGetModel(compositeCtx, plainAccount, "[AIS]gemini-3.8-flash")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "/v1beta/models/%5BAIS%5Dgemini-3.8-flash", stub.lastReq.URL.EscapedPath())
}

type geminiMappedURLUpstreamStub struct {
	lastReq *http.Request
	body    string
}

func (s *geminiMappedURLUpstreamStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.lastReq = req
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(s.body)),
	}, nil
}

func (s *geminiMappedURLUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestGeminiForwardNative_AccountMappedRelayModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name              string
		baseURL           string
		requestModel      string
		mappedModel       string
		wantEscapedPath   string
		wantUpstreamModel string
	}{
		{
			name:              "bracket_prefix_ais",
			baseURL:           "https://bearlab.ai",
			requestModel:      "gemini-3.8-flash",
			mappedModel:       "[AIS]gemini-3.8-flash",
			wantEscapedPath:   "/v1beta/models/%5BAIS%5Dgemini-3.8-flash:generateContent",
			wantUpstreamModel: "[AIS]gemini-3.8-flash",
		},
		{
			name:              "chinese_bracket_and_space",
			baseURL:           "https://ckff.dev",
			requestModel:      "gemini-3.1-pro-preview",
			mappedModel:       "[gcli转] gemini-3.1-pro-preview [假流]",
			wantEscapedPath:   "/v1beta/models/%5Bgcli%E8%BD%AC%5D%20gemini-3.1-pro-preview%20%5B%E5%81%87%E6%B5%81%5D:generateContent",
			wantUpstreamModel: "[gcli转] gemini-3.1-pro-preview [假流]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &geminiMappedURLUpstreamStub{
				body: `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2}}`,
			}
			svc := &GeminiMessagesCompatService{
				httpUpstream: stub,
				cfg:          &config.Config{},
			}
			account := &Account{
				ID:       134,
				Platform: PlatformGemini,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"api_key":  "test-key",
					"base_url": tc.baseURL,
					"model_mapping": map[string]any{
						tc.requestModel: tc.mappedModel,
					},
				},
			}

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/"+tc.requestModel+":generateContent", nil)

			result, err := svc.ForwardNative(
				context.Background(),
				c,
				account,
				tc.requestModel,
				"generateContent",
				false,
				[]byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, tc.wantUpstreamModel, result.UpstreamModel)
			require.NotNil(t, stub.lastReq)
			require.Equal(t, tc.wantEscapedPath, stub.lastReq.URL.EscapedPath())
		})
	}
}
