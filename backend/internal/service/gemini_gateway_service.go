package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sub2api/internal/config"
	"sub2api/internal/model"
	"sub2api/internal/pkg/gemini"
	"sub2api/internal/service/ports"

	"github.com/gin-gonic/gin"
)

const (
	geminiStickySessionTTL = time.Hour // 粘性会话TTL
)

// Gemini allowed headers whitelist
var geminiAllowedHeaders = map[string]bool{
	"accept-language": true,
	"content-type":    true,
	"user-agent":      true,
}

// GeminiUsage represents Gemini API response usage
type GeminiUsage struct {
	PromptTokenCount     int `json:"prompt_token_count"`
	CandidatesTokenCount int `json:"candidates_token_count"`
	TotalTokenCount      int `json:"total_token_count"`
	CachedTokenCount     int `json:"cached_content_token_count"`
}

// GeminiForwardResult represents the result of forwarding
type GeminiForwardResult struct {
	RequestID    string
	Usage        GeminiUsage
	Model        string
	Stream       bool
	Duration     time.Duration
	FirstTokenMs *int
}

// GeminiGatewayService handles Gemini API gateway operations
type GeminiGatewayService struct {
	accountRepo         ports.AccountRepository
	usageLogRepo        ports.UsageLogRepository
	userRepo            ports.UserRepository
	userSubRepo         ports.UserSubscriptionRepository
	cache               ports.GatewayCache
	cfg                 *config.Config
	billingService      *BillingService
	rateLimitService    *RateLimitService
	billingCacheService *BillingCacheService
	httpUpstream        ports.HTTPUpstream
}

// NewGeminiGatewayService creates a new GeminiGatewayService
func NewGeminiGatewayService(
	accountRepo ports.AccountRepository,
	usageLogRepo ports.UsageLogRepository,
	userRepo ports.UserRepository,
	userSubRepo ports.UserSubscriptionRepository,
	cache ports.GatewayCache,
	cfg *config.Config,
	billingService *BillingService,
	rateLimitService *RateLimitService,
	billingCacheService *BillingCacheService,
	httpUpstream ports.HTTPUpstream,
) *GeminiGatewayService {
	return &GeminiGatewayService{
		accountRepo:         accountRepo,
		usageLogRepo:        usageLogRepo,
		userRepo:            userRepo,
		userSubRepo:         userSubRepo,
		cache:               cache,
		cfg:                 cfg,
		billingService:      billingService,
		rateLimitService:    rateLimitService,
		billingCacheService: billingCacheService,
		httpUpstream:        httpUpstream,
	}
}

// GenerateSessionHash generates session hash from Gemini request body
func (s *GeminiGatewayService) GenerateSessionHash(body []byte) string {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}

	// 1. 从 systemInstruction 提取内容
	if sysInst, ok := req["systemInstruction"].(map[string]any); ok {
		if parts, ok := sysInst["parts"].([]any); ok && len(parts) > 0 {
			if part, ok := parts[0].(map[string]any); ok {
				if text, ok := part["text"].(string); ok && text != "" {
					return s.hashContent(text)
				}
			}
		}
	}

	// 2. Fallback: 使用第一条 contents 消息
	if contents, ok := req["contents"].([]any); ok && len(contents) > 0 {
		if content, ok := contents[0].(map[string]any); ok {
			if parts, ok := content["parts"].([]any); ok && len(parts) > 0 {
				if part, ok := parts[0].(map[string]any); ok {
					if text, ok := part["text"].(string); ok {
						return s.hashContent(text)
					}
				}
			}
		}
	}

	return ""
}

func (s *GeminiGatewayService) hashContent(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// SelectAccount selects a Gemini account with sticky session support
func (s *GeminiGatewayService) SelectAccount(ctx context.Context, groupID *int64, sessionHash string) (*model.Account, error) {
	return s.SelectAccountForModel(ctx, groupID, sessionHash, "")
}

// SelectAccountForModel selects an account supporting the requested model
func (s *GeminiGatewayService) SelectAccountForModel(ctx context.Context, groupID *int64, sessionHash string, requestedModel string) (*model.Account, error) {
	// 1. Check sticky session
	if sessionHash != "" {
		accountID, err := s.cache.GetSessionAccountID(ctx, "gemini:"+sessionHash)
		if err == nil && accountID > 0 {
			account, err := s.accountRepo.GetByID(ctx, accountID)
			if err == nil && account.IsSchedulable() && account.IsGemini() && (requestedModel == "" || account.IsModelSupported(requestedModel)) {
				// Refresh sticky session TTL
				_ = s.cache.RefreshSessionTTL(ctx, "gemini:"+sessionHash, geminiStickySessionTTL)
				return account, nil
			}
		}
	}

	// 2. Get schedulable Gemini accounts
	var accounts []model.Account
	var err error
	if groupID != nil {
		accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, model.PlatformGemini)
	} else {
		accounts, err = s.accountRepo.ListSchedulableByPlatform(ctx, model.PlatformGemini)
	}
	if err != nil {
		return nil, fmt.Errorf("query accounts failed: %w", err)
	}

	// 3. Select by priority + LRU
	var selected *model.Account
	for i := range accounts {
		acc := &accounts[i]
		// Check model support
		if requestedModel != "" && !acc.IsModelSupported(requestedModel) {
			continue
		}
		if selected == nil {
			selected = acc
			continue
		}
		// Lower priority value means higher priority
		if acc.Priority < selected.Priority {
			selected = acc
		} else if acc.Priority == selected.Priority {
			// Same priority, select least recently used
			if acc.LastUsedAt == nil || (selected.LastUsedAt != nil && acc.LastUsedAt.Before(*selected.LastUsedAt)) {
				selected = acc
			}
		}
	}

	if selected == nil {
		if requestedModel != "" {
			return nil, fmt.Errorf("no available Gemini accounts supporting model: %s", requestedModel)
		}
		return nil, errors.New("no available Gemini accounts")
	}

	// 4. Set sticky session
	if sessionHash != "" {
		_ = s.cache.SetSessionAccountID(ctx, "gemini:"+sessionHash, selected.ID, geminiStickySessionTTL)
	}

	return selected, nil
}

// GetAccessToken gets the access token for a Gemini account
func (s *GeminiGatewayService) GetAccessToken(ctx context.Context, account *model.Account) (string, string, error) {
	switch account.Type {
	case model.AccountTypeOAuth:
		accessToken := account.GetGeminiAccessToken()
		if accessToken == "" {
			return "", "", errors.New("access_token not found in credentials")
		}
		return accessToken, "oauth", nil
	case model.AccountTypeApiKey:
		apiKey := account.GetGeminiApiKey()
		if apiKey == "" {
			return "", "", errors.New("api_key not found in credentials")
		}
		return apiKey, "apikey", nil
	default:
		return "", "", fmt.Errorf("unsupported account type: %s", account.Type)
	}
}

// Forward forwards request to Gemini API
func (s *GeminiGatewayService) Forward(ctx context.Context, c *gin.Context, account *model.Account, body []byte, modelName string, isStream bool) (*GeminiForwardResult, error) {
	startTime := time.Now()

	// Apply model mapping
	originalModel := modelName
	mappedModel := account.GetMappedModel(modelName)
	if mappedModel != modelName {
		modelName = mappedModel
	}

	// Get access token
	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}

	// Build upstream request
	upstreamReq, err := s.buildUpstreamRequest(ctx, c, account, body, token, tokenType, modelName, isStream)
	if err != nil {
		return nil, err
	}

	// Get proxy URL
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// Send request
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Handle error response
	if resp.StatusCode >= 400 {
		return s.handleErrorResponse(ctx, resp, c, account)
	}

	// Handle normal response
	var usage *GeminiUsage
	var firstTokenMs *int
	if isStream {
		streamResult, err := s.handleStreamingResponse(ctx, resp, c, account, startTime, originalModel, mappedModel)
		if err != nil {
			return nil, err
		}
		usage = streamResult.usage
		firstTokenMs = streamResult.firstTokenMs
	} else {
		usage, err = s.handleNonStreamingResponse(ctx, resp, c, account, originalModel, mappedModel)
		if err != nil {
			return nil, err
		}
	}

	return &GeminiForwardResult{
		RequestID:    resp.Header.Get("x-goog-request-id"),
		Usage:        *usage,
		Model:        originalModel,
		Stream:       isStream,
		Duration:     time.Since(startTime),
		FirstTokenMs: firstTokenMs,
	}, nil
}

func (s *GeminiGatewayService) buildUpstreamRequest(ctx context.Context, c *gin.Context, account *model.Account, body []byte, token, tokenType, modelName string, isStream bool) (*http.Request, error) {
	// Determine target URL
	var targetURL string
	if account.IsVertexAI() {
		// Vertex AI endpoint
		region := account.GetVertexRegion()
		projectID := account.GetVertexProjectID()
		targetURL = gemini.BuildVertexAIURL(region, projectID, modelName, isStream)
	} else {
		// Google AI Studio endpoint
		baseURL := account.GetGeminiBaseURL()
		targetURL = gemini.BuildGenerateContentURL(baseURL, modelName, isStream)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Set authentication
	if tokenType == "oauth" {
		// OAuth: Bearer token in Authorization header
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		// API Key: append to URL query parameter
		q := req.URL.Query()
		q.Set("key", token)
		req.URL.RawQuery = q.Encode()
	}

	// Set required headers
	req.Header.Set("Content-Type", "application/json")

	// For streaming, add alt=sse parameter
	if isStream {
		q := req.URL.Query()
		q.Set("alt", "sse")
		req.URL.RawQuery = q.Encode()
	}

	// Whitelist passthrough headers
	for key, values := range c.Request.Header {
		lowerKey := strings.ToLower(key)
		if geminiAllowedHeaders[lowerKey] {
			for _, v := range values {
				req.Header.Add(key, v)
			}
		}
	}

	return req, nil
}

func (s *GeminiGatewayService) handleErrorResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *model.Account) (*GeminiForwardResult, error) {
	body, _ := io.ReadAll(resp.Body)

	// Check custom error codes
	if !account.ShouldHandleErrorCode(resp.StatusCode) {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    500,
				"message": "Upstream gateway error",
				"status":  "INTERNAL",
			},
		})
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
	}

	// Handle upstream error (mark account status)
	s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, body)

	// Return appropriate error response
	var errCode int
	var errMsg, errStatus string

	switch resp.StatusCode {
	case 401:
		errCode = 401
		errMsg = "Upstream authentication failed, please contact administrator"
		errStatus = "UNAUTHENTICATED"
	case 403:
		errCode = 403
		errMsg = "Upstream access forbidden, please contact administrator"
		errStatus = "PERMISSION_DENIED"
	case 429:
		errCode = 429
		errMsg = "Upstream rate limit exceeded, please retry later"
		errStatus = "RESOURCE_EXHAUSTED"
	default:
		errCode = 502
		errMsg = "Upstream request failed"
		errStatus = "UNAVAILABLE"
	}

	c.JSON(errCode, gin.H{
		"error": gin.H{
			"code":    errCode,
			"message": errMsg,
			"status":  errStatus,
		},
	})

	return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
}

// geminiStreamingResult streaming response result
type geminiStreamingResult struct {
	usage        *GeminiUsage
	firstTokenMs *int
}

func (s *GeminiGatewayService) handleStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *model.Account, startTime time.Time, originalModel, mappedModel string) (*geminiStreamingResult, error) {
	// Set SSE response headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// Pass through request ID
	if v := resp.Header.Get("x-goog-request-id"); v != "" {
		c.Header("x-goog-request-id", v)
	}

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	usage := &GeminiUsage{}
	var firstTokenMs *int
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Forward line
		if _, err := fmt.Fprintf(w, "%s\n", line); err != nil {
			return &geminiStreamingResult{usage: usage, firstTokenMs: firstTokenMs}, err
		}
		flusher.Flush()

		// Parse usage data from SSE
		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			// Record first token time
			if firstTokenMs == nil && data != "" && data != "[DONE]" {
				ms := int(time.Since(startTime).Milliseconds())
				firstTokenMs = &ms
			}
			s.parseSSEUsage(data, usage)
		}
	}

	if err := scanner.Err(); err != nil {
		return &geminiStreamingResult{usage: usage, firstTokenMs: firstTokenMs}, fmt.Errorf("stream read error: %w", err)
	}

	return &geminiStreamingResult{usage: usage, firstTokenMs: firstTokenMs}, nil
}

func (s *GeminiGatewayService) parseSSEUsage(data string, usage *GeminiUsage) {
	if data == "" || data == "[DONE]" {
		return
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return
	}

	// Parse usageMetadata
	if usageMetadata, ok := resp["usageMetadata"].(map[string]any); ok {
		if v, ok := usageMetadata["promptTokenCount"].(float64); ok {
			usage.PromptTokenCount = int(v)
		}
		if v, ok := usageMetadata["candidatesTokenCount"].(float64); ok {
			usage.CandidatesTokenCount = int(v)
		}
		if v, ok := usageMetadata["totalTokenCount"].(float64); ok {
			usage.TotalTokenCount = int(v)
		}
		if v, ok := usageMetadata["cachedContentTokenCount"].(float64); ok {
			usage.CachedTokenCount = int(v)
		}
	}
}

func (s *GeminiGatewayService) handleNonStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *model.Account, originalModel, mappedModel string) (*GeminiUsage, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	// Parse usage from response
	usage := &GeminiUsage{}
	var respBody map[string]any
	if err := json.Unmarshal(body, &respBody); err == nil {
		if usageMetadata, ok := respBody["usageMetadata"].(map[string]any); ok {
			if v, ok := usageMetadata["promptTokenCount"].(float64); ok {
				usage.PromptTokenCount = int(v)
			}
			if v, ok := usageMetadata["candidatesTokenCount"].(float64); ok {
				usage.CandidatesTokenCount = int(v)
			}
			if v, ok := usageMetadata["totalTokenCount"].(float64); ok {
				usage.TotalTokenCount = int(v)
			}
			if v, ok := usageMetadata["cachedContentTokenCount"].(float64); ok {
				usage.CachedTokenCount = int(v)
			}
		}
	}

	// Forward response
	for key, values := range resp.Header {
		for _, value := range values {
			c.Header(key, value)
		}
	}
	c.Data(resp.StatusCode, "application/json", body)

	return usage, nil
}

// ConvertOpenAIToGemini converts OpenAI format request to Gemini format
func (s *GeminiGatewayService) ConvertOpenAIToGemini(openaiReq map[string]any) ([]byte, string, bool, error) {
	geminiReq := make(map[string]any)

	// Extract model
	modelName, _ := openaiReq["model"].(string)

	// Extract stream
	isStream, _ := openaiReq["stream"].(bool)

	// Convert messages -> contents
	messages, ok := openaiReq["messages"].([]any)
	if !ok {
		return nil, "", false, errors.New("messages field required")
	}

	var contents []map[string]any
	var systemInstruction map[string]any

	for _, msg := range messages {
		m, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)

		if role == "system" {
			// Gemini uses systemInstruction
			systemInstruction = map[string]any{
				"parts": []map[string]any{{"text": content}},
			}
			continue
		}

		// Role mapping: assistant -> model
		geminiRole := role
		if role == "assistant" {
			geminiRole = "model"
		}

		contents = append(contents, map[string]any{
			"role":  geminiRole,
			"parts": []map[string]any{{"text": content}},
		})
	}

	geminiReq["contents"] = contents
	if systemInstruction != nil {
		geminiReq["systemInstruction"] = systemInstruction
	}

	// Convert generation config
	genConfig := make(map[string]any)
	if temp, ok := openaiReq["temperature"].(float64); ok {
		genConfig["temperature"] = temp
	}
	if maxTokens, ok := openaiReq["max_tokens"].(float64); ok {
		genConfig["maxOutputTokens"] = int(maxTokens)
	}
	if topP, ok := openaiReq["top_p"].(float64); ok {
		genConfig["topP"] = topP
	}
	if len(genConfig) > 0 {
		geminiReq["generationConfig"] = genConfig
	}

	body, err := json.Marshal(geminiReq)
	return body, modelName, isStream, err
}

// ConvertGeminiToOpenAI converts Gemini response to OpenAI format
func (s *GeminiGatewayService) ConvertGeminiToOpenAI(geminiResp map[string]any, modelName string, isStream bool) map[string]any {
	openaiResp := map[string]any{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   modelName,
	}

	if isStream {
		openaiResp["object"] = "chat.completion.chunk"
	}

	var choices []map[string]any
	if candidates, ok := geminiResp["candidates"].([]any); ok && len(candidates) > 0 {
		for i, cand := range candidates {
			c, ok := cand.(map[string]any)
			if !ok {
				continue
			}

			var text string
			if content, ok := c["content"].(map[string]any); ok {
				if parts, ok := content["parts"].([]any); ok && len(parts) > 0 {
					if part, ok := parts[0].(map[string]any); ok {
						text, _ = part["text"].(string)
					}
				}
			}

			finishReason := "stop"
			if reason, ok := c["finishReason"].(string); ok {
				finishReason = strings.ToLower(reason)
				if finishReason == "max_tokens" {
					finishReason = "length"
				}
			}

			choice := map[string]any{
				"index":         i,
				"finish_reason": finishReason,
			}

			if isStream {
				choice["delta"] = map[string]any{
					"role":    "assistant",
					"content": text,
				}
			} else {
				choice["message"] = map[string]any{
					"role":    "assistant",
					"content": text,
				}
			}

			choices = append(choices, choice)
		}
	}
	openaiResp["choices"] = choices

	// Convert usage
	if usageData, ok := geminiResp["usageMetadata"].(map[string]any); ok {
		openaiResp["usage"] = map[string]any{
			"prompt_tokens":     usageData["promptTokenCount"],
			"completion_tokens": usageData["candidatesTokenCount"],
			"total_tokens":      usageData["totalTokenCount"],
		}
	}

	return openaiResp
}

// ForwardOpenAICompatible forwards OpenAI compatible request to Gemini API
func (s *GeminiGatewayService) ForwardOpenAICompatible(ctx context.Context, c *gin.Context, account *model.Account, body []byte) (*GeminiForwardResult, error) {
	// Parse OpenAI request
	var openaiReq map[string]any
	if err := json.Unmarshal(body, &openaiReq); err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}

	// Convert to Gemini format
	geminiBody, modelName, isStream, err := s.ConvertOpenAIToGemini(openaiReq)
	if err != nil {
		return nil, err
	}

	// Forward to Gemini
	startTime := time.Now()
	originalModel := modelName
	mappedModel := account.GetMappedModel(modelName)
	if mappedModel != modelName {
		modelName = mappedModel
	}

	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}

	upstreamReq, err := s.buildUpstreamRequest(ctx, c, account, geminiBody, token, tokenType, modelName, isStream)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return s.handleOpenAICompatibleError(ctx, resp, c, account)
	}

	// Handle response with OpenAI format conversion
	var usage *GeminiUsage
	var firstTokenMs *int
	if isStream {
		streamResult, err := s.handleOpenAICompatibleStreamingResponse(ctx, resp, c, account, startTime, originalModel)
		if err != nil {
			return nil, err
		}
		usage = streamResult.usage
		firstTokenMs = streamResult.firstTokenMs
	} else {
		usage, err = s.handleOpenAICompatibleNonStreamingResponse(ctx, resp, c, originalModel)
		if err != nil {
			return nil, err
		}
	}

	return &GeminiForwardResult{
		RequestID:    resp.Header.Get("x-goog-request-id"),
		Usage:        *usage,
		Model:        originalModel,
		Stream:       isStream,
		Duration:     time.Since(startTime),
		FirstTokenMs: firstTokenMs,
	}, nil
}

func (s *GeminiGatewayService) handleOpenAICompatibleError(ctx context.Context, resp *http.Response, c *gin.Context, account *model.Account) (*GeminiForwardResult, error) {
	body, _ := io.ReadAll(resp.Body)

	if !account.ShouldHandleErrorCode(resp.StatusCode) {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "upstream_error",
				"message": "Upstream gateway error",
			},
		})
		return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
	}

	s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, body)

	var errType, errMsg string
	var statusCode int

	switch resp.StatusCode {
	case 401:
		statusCode = http.StatusBadGateway
		errType = "authentication_error"
		errMsg = "Upstream authentication failed"
	case 403:
		statusCode = http.StatusBadGateway
		errType = "permission_error"
		errMsg = "Upstream access forbidden"
	case 429:
		statusCode = http.StatusTooManyRequests
		errType = "rate_limit_error"
		errMsg = "Rate limit exceeded"
	default:
		statusCode = http.StatusBadGateway
		errType = "api_error"
		errMsg = "Upstream request failed"
	}

	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": errMsg,
		},
	})

	return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
}

func (s *GeminiGatewayService) handleOpenAICompatibleStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *model.Account, startTime time.Time, modelName string) (*geminiStreamingResult, error) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	usage := &GeminiUsage{}
	var firstTokenMs *int
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			if data == "[DONE]" {
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				continue
			}

			// Record first token time
			if firstTokenMs == nil && data != "" {
				ms := int(time.Since(startTime).Milliseconds())
				firstTokenMs = &ms
			}

			// Parse Gemini response
			var geminiResp map[string]any
			if err := json.Unmarshal([]byte(data), &geminiResp); err != nil {
				continue
			}

			// Extract usage
			s.parseSSEUsage(data, usage)

			// Convert to OpenAI format
			openaiResp := s.ConvertGeminiToOpenAI(geminiResp, modelName, true)
			openaiData, err := json.Marshal(openaiResp)
			if err != nil {
				continue
			}

			fmt.Fprintf(w, "data: %s\n\n", openaiData)
			flusher.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		return &geminiStreamingResult{usage: usage, firstTokenMs: firstTokenMs}, fmt.Errorf("stream read error: %w", err)
	}

	return &geminiStreamingResult{usage: usage, firstTokenMs: firstTokenMs}, nil
}

func (s *GeminiGatewayService) handleOpenAICompatibleNonStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, modelName string) (*GeminiUsage, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	// Parse Gemini response
	var geminiResp map[string]any
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// Extract usage
	usage := &GeminiUsage{}
	if usageMetadata, ok := geminiResp["usageMetadata"].(map[string]any); ok {
		if v, ok := usageMetadata["promptTokenCount"].(float64); ok {
			usage.PromptTokenCount = int(v)
		}
		if v, ok := usageMetadata["candidatesTokenCount"].(float64); ok {
			usage.CandidatesTokenCount = int(v)
		}
		if v, ok := usageMetadata["totalTokenCount"].(float64); ok {
			usage.TotalTokenCount = int(v)
		}
		if v, ok := usageMetadata["cachedContentTokenCount"].(float64); ok {
			usage.CachedTokenCount = int(v)
		}
	}

	// Convert to OpenAI format
	openaiResp := s.ConvertGeminiToOpenAI(geminiResp, modelName, false)
	openaiBody, err := json.Marshal(openaiResp)
	if err != nil {
		return nil, fmt.Errorf("serialize response: %w", err)
	}

	c.Data(http.StatusOK, "application/json", openaiBody)

	return usage, nil
}

// GeminiRecordUsageInput input for recording usage
type GeminiRecordUsageInput struct {
	Result       *GeminiForwardResult
	ApiKey       *model.ApiKey
	User         *model.User
	Account      *model.Account
	Subscription *model.UserSubscription
}

// RecordUsage records usage and deducts balance
func (s *GeminiGatewayService) RecordUsage(ctx context.Context, input *GeminiRecordUsageInput) error {
	result := input.Result
	apiKey := input.ApiKey
	user := input.User
	account := input.Account
	subscription := input.Subscription

	// Calculate actual input tokens (subtract cached tokens)
	actualInputTokens := result.Usage.PromptTokenCount - result.Usage.CachedTokenCount
	if actualInputTokens < 0 {
		actualInputTokens = 0
	}

	// Calculate cost
	tokens := UsageTokens{
		InputTokens:     actualInputTokens,
		OutputTokens:    result.Usage.CandidatesTokenCount,
		CacheReadTokens: result.Usage.CachedTokenCount,
	}

	// Get rate multiplier
	multiplier := s.cfg.Default.RateMultiplier
	if apiKey.GroupID != nil && apiKey.Group != nil {
		multiplier = apiKey.Group.RateMultiplier
	}

	cost, err := s.billingService.CalculateCost(result.Model, tokens, multiplier)
	if err != nil {
		cost = &CostBreakdown{ActualCost: 0}
	}

	// Determine billing type
	isSubscriptionBilling := subscription != nil && apiKey.Group != nil && apiKey.Group.IsSubscriptionType()
	billingType := model.BillingTypeBalance
	if isSubscriptionBilling {
		billingType = model.BillingTypeSubscription
	}

	// Create usage log
	durationMs := int(result.Duration.Milliseconds())
	usageLog := &model.UsageLog{
		UserID:          user.ID,
		ApiKeyID:        apiKey.ID,
		AccountID:       account.ID,
		RequestID:       result.RequestID,
		Model:           result.Model,
		InputTokens:     actualInputTokens,
		OutputTokens:    result.Usage.CandidatesTokenCount,
		CacheReadTokens: result.Usage.CachedTokenCount,
		InputCost:       cost.InputCost,
		OutputCost:      cost.OutputCost,
		CacheReadCost:   cost.CacheReadCost,
		TotalCost:       cost.TotalCost,
		ActualCost:      cost.ActualCost,
		RateMultiplier:  multiplier,
		BillingType:     billingType,
		Stream:          result.Stream,
		DurationMs:      &durationMs,
		FirstTokenMs:    result.FirstTokenMs,
		CreatedAt:       time.Now(),
	}

	if apiKey.GroupID != nil {
		usageLog.GroupID = apiKey.GroupID
	}
	if subscription != nil {
		usageLog.SubscriptionID = &subscription.ID
	}

	_ = s.usageLogRepo.Create(ctx, usageLog)

	// Deduct based on billing type
	if isSubscriptionBilling {
		if cost.TotalCost > 0 {
			_ = s.userSubRepo.IncrementUsage(ctx, subscription.ID, cost.TotalCost)
			go func() {
				cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = s.billingCacheService.UpdateSubscriptionUsage(cacheCtx, user.ID, *apiKey.GroupID, cost.TotalCost)
			}()
		}
	} else {
		if cost.ActualCost > 0 {
			_ = s.userRepo.DeductBalance(ctx, user.ID, cost.ActualCost)
			go func() {
				cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = s.billingCacheService.DeductBalanceCache(cacheCtx, user.ID, cost.ActualCost)
			}()
		}
	}

	// Update account last used
	_ = s.accountRepo.UpdateLastUsed(ctx, account.ID)

	return nil
}

// GetModels returns list of Gemini models
func (s *GeminiGatewayService) GetModels() []gemini.Model {
	return gemini.DefaultModels
}
