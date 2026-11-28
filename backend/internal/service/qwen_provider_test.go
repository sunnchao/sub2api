//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestQwenProviderModesAndRouting(t *testing.T) {
	cases := []struct {
		mode              string
		chat              string
		anthropic         string
		wantAnthropicProt string
	}{
		{"token_cn", DefaultQwenTokenPlanCNBaseURL, DefaultQwenTokenPlanCNAnthropicBaseURL, APIProtocolAnthropic},
		{"token_sg", DefaultQwenTokenPlanSGBaseURL, DefaultQwenTokenPlanSGAnthropicBaseURL, APIProtocolAnthropic},
		{"coding_cn", DefaultQwenCodingPlanCNBaseURL, DefaultQwenCodingPlanCNAnthropicBaseURL, APIProtocolAnthropic},
		{"coding_sg", DefaultQwenCodingPlanSGBaseURL, DefaultQwenCodingPlanSGAnthropicBaseURL, APIProtocolAnthropic},
		{"payg_cn", DefaultQwenPayGCNBaseURL, DefaultQwenPayGCNAnthropicBaseURL, APIProtocolAnthropic},
		{"payg_sg", DefaultQwenPayGSGBaseURL, DefaultQwenPayGSGAnthropicBaseURL, APIProtocolAnthropic},
		{"qwencloud_token_cn", DefaultQwenCloudTokenPlanCNBaseURL, "", APIProtocolChatCompletions},
		{"qwencloud_token_sg", DefaultQwenCloudTokenPlanSGBaseURL, "", APIProtocolChatCompletions},
		{"qwencloud_payg_cn", DefaultQwenCloudPayGCNBaseURL, "", APIProtocolChatCompletions},
		{"qwencloud_payg_sg", DefaultQwenCloudPayGSGBaseURL, "", APIProtocolChatCompletions},
	}

	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			account := &Account{
				Platform: PlatformQwen,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"account_mode": tc.mode,
					"api_protocol": APIProtocolAdaptive,
				},
			}
			require.Equal(t, tc.chat, account.GetCNProtocolBaseURL(APIProtocolChatCompletions))
			require.Equal(t, tc.anthropic, account.GetCNProtocolBaseURL(APIProtocolAnthropic))
			require.Equal(t, APIProtocolChatCompletions, resolveUpstreamProtocol(account, APIProtocolResponses, "qwen3.6-plus", nil))
			require.Equal(t, tc.wantAnthropicProt, resolveUpstreamProtocol(account, APIProtocolAnthropic, "qwen3.6-plus", nil))
			require.False(t, account.SupportsNativeCNResponses())
		})
	}
}

func TestQwenCustomEndpointsAndResponsesConversion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"qwen3.6-plus","input":"hello","max_output_tokens":32,"stream":false}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := adaptiveProtocolTestAccount(PlatformQwen, map[string]any{
		APIProtocolChatCompletions: "http://custom.example/v1",
		APIProtocolAnthropic:       "http://custom-anthropic.example",
	})
	account.Credentials["account_mode"] = "token_cn"

	_, err := svc.Forward(context.Background(), adaptiveProtocolTestContext("/v1/responses", body), account, body)
	require.Error(t, err)
	require.Equal(t, "http://custom.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "messages").IsArray())
	require.Equal(t, "http://custom-anthropic.example", account.GetCNProtocolBaseURL(APIProtocolAnthropic))
}

func TestQwenNativeStreamingRoutesAndAnthropicThinkingConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := adaptiveProtocolTestAccount(PlatformQwen, map[string]any{
		APIProtocolChatCompletions: "http://custom.example/v1",
		APIProtocolAnthropic:       "http://custom-anthropic.example/apps/anthropic",
	})
	account.Credentials["account_mode"] = "token_sg"

	t.Run("chat completions", func(t *testing.T) {
		body := []byte(`{"model":"qwen3.6-plus","messages":[{"role":"user","content":"hello"}],"stream":true}`)
		upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
		svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
		_, err := svc.ForwardAsChatCompletions(context.Background(), adaptiveProtocolTestContext("/v1/chat/completions", body), account, body, "", "")
		require.Error(t, err)
		require.Equal(t, "http://custom.example/v1/chat/completions", upstream.lastReq.URL.String())
		require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
		require.Equal(t, "qwen3.6-plus", gjson.GetBytes(upstream.lastBody, "model").String())
	})

	t.Run("anthropic strips conflicting output_config.effort when thinking budget is set", func(t *testing.T) {
		body := []byte(`{"model":"qwen3.8-max","max_tokens":64,"thinking":{"type":"enabled","budget_tokens":1024},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hello"}],"stream":true}`)
		upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
		svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
		_, err := svc.ForwardAsAnthropic(context.Background(), adaptiveProtocolTestContext("/v1/messages", body), account, body, "", "")
		require.Error(t, err)
		require.Equal(t, "http://custom-anthropic.example/apps/anthropic/v1/messages", upstream.lastReq.URL.String())
		require.True(t, gjson.GetBytes(upstream.lastBody, "thinking.budget_tokens").Exists())
		require.False(t, gjson.GetBytes(upstream.lastBody, "output_config").Exists())
	})

	t.Run("anthropic preserves output_config.effort for adaptive thinking without budget_tokens", func(t *testing.T) {
		body := []byte(`{"model":"qwen3.8-max","max_tokens":64,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hello"}],"stream":true}`)
		upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
		svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
		_, err := svc.ForwardAsAnthropic(context.Background(), adaptiveProtocolTestContext("/v1/messages", body), account, body, "", "")
		require.Error(t, err)
		require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "output_config.effort").String())
	})
}

type qwenRateLimitRepoStub struct {
	rateLimitAccountRepoStub
	setSchedulableCalls int
	lastSchedulable     bool
}

func (r *qwenRateLimitRepoStub) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	r.setSchedulableCalls++
	r.lastSchedulable = schedulable
	return nil
}

func TestQwenRateLimitAndQuotaExhaustion(t *testing.T) {
	newAccount := func(mode string) *Account {
		return &Account{
			ID:          901,
			Platform:    PlatformQwen,
			Type:        AccountTypeAPIKey,
			Schedulable: true,
			Credentials: map[string]any{"account_mode": mode},
		}
	}

	t.Run("402 payg sets recoverable temp unschedulable instead of permanent error", func(t *testing.T) {
		repo := &qwenRateLimitRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		disable := svc.HandleUpstreamError(context.Background(), newAccount("payg_cn"), http.StatusPaymentRequired, http.Header{}, []byte(`{"error":{"message":"insufficient balance"}}`))
		require.False(t, disable)
		require.Equal(t, 0, repo.setErrorCalls)
		require.Equal(t, 1, repo.tempCalls)
	})

	t.Run("429 transient rate limit on token plan uses short cooldown without pausing", func(t *testing.T) {
		repo := &qwenRateLimitRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		disable := svc.HandleUpstreamError(context.Background(), newAccount("token_cn"), http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"Requests rate limit exceeded, please try again later"}}`))
		require.False(t, disable)
		require.Equal(t, 0, repo.setSchedulableCalls)
		require.Equal(t, 1, repo.rateLimitedCalls)
	})

	t.Run("429 token plan exhaustion with future reset time cools down until reset", func(t *testing.T) {
		repo := &qwenRateLimitRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		disable := svc.HandleUpstreamError(context.Background(), newAccount("token_cn"), http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"Your token-plan 1-week quota has been exhausted. The quota will reset at 2099-09-14 00:00:00"}}`))
		require.False(t, disable)
		require.Equal(t, 0, repo.setSchedulableCalls)
		require.Equal(t, 1, repo.rateLimitedCalls)
		wantReset := time.Date(2099, 9, 13, 16, 0, 0, 0, time.UTC)
		require.Equal(t, wantReset, repo.lastRateLimitedAt.UTC())
	})

	t.Run("429 one-time token plan exhaustion without reset time permanently disables scheduling", func(t *testing.T) {
		repo := &qwenRateLimitRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		disable := svc.HandleUpstreamError(context.Background(), newAccount("token_cn"), http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"code":"insufficient_quota","message":"Allocated quota exceeded"}}`))
		require.True(t, disable)
		require.Equal(t, 1, repo.setSchedulableCalls)
		require.False(t, repo.lastSchedulable)
		require.Equal(t, 1, repo.updateExtraCalls)
		require.Equal(t, true, repo.lastExtraUpdates[qwenTokenPlanExhaustedExtraKey])
	})

	t.Run("429 coding plan exhaustion never permanently disables scheduling", func(t *testing.T) {
		repo := &qwenRateLimitRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		disable := svc.HandleUpstreamError(context.Background(), newAccount("coding_cn"), http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"code":"insufficient_quota","message":"Allocated quota exceeded"}}`))
		require.False(t, disable)
		require.Equal(t, 0, repo.setSchedulableCalls)
		require.Equal(t, 1, repo.rateLimitedCalls)
	})

	t.Run("429 payg arrearage sets recoverable temp unschedulable", func(t *testing.T) {
		repo := &qwenRateLimitRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		disable := svc.HandleUpstreamError(context.Background(), newAccount("payg_cn"), http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"code":"Arrearage","message":"账户已欠费"}}`))
		require.False(t, disable)
		require.Equal(t, 1, repo.tempCalls)
		require.Equal(t, 0, repo.setErrorCalls)
	})
}
