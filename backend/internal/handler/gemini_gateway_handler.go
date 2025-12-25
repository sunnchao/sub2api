package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"sub2api/internal/middleware"
	"sub2api/internal/pkg/gemini"
	"sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// GeminiGatewayHandler handles Gemini API gateway requests
type GeminiGatewayHandler struct {
	gatewayService      *service.GeminiGatewayService
	billingCacheService *service.BillingCacheService
	concurrencyHelper   *ConcurrencyHelper
}

// NewGeminiGatewayHandler creates a new GeminiGatewayHandler
func NewGeminiGatewayHandler(
	gatewayService *service.GeminiGatewayService,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
) *GeminiGatewayHandler {
	return &GeminiGatewayHandler{
		gatewayService:      gatewayService,
		billingCacheService: billingCacheService,
		concurrencyHelper:   NewConcurrencyHelper(concurrencyService, SSEPingFormatNone),
	}
}

// HandleModelAction handles Gemini model action requests
// POST /gemini/v1beta/models/*modelAction
// Parses paths like /gemini-2.5-flash:generateContent or /gemini-2.5-flash:streamGenerateContent
func (h *GeminiGatewayHandler) HandleModelAction(c *gin.Context) {
	// Get the modelAction parameter (includes leading slash)
	modelAction := c.Param("modelAction")
	modelAction = strings.TrimPrefix(modelAction, "/")

	// Parse model and action (format: model:action)
	parts := strings.SplitN(modelAction, ":", 2)
	if len(parts) != 2 {
		h.errorResponse(c, http.StatusBadRequest, 400, "INVALID_ARGUMENT", "Invalid path format, expected models/{model}:{action}")
		return
	}

	modelName := parts[0]
	action := parts[1]

	// Determine if streaming based on action
	var isStream bool
	switch action {
	case "generateContent":
		isStream = false
	case "streamGenerateContent":
		isStream = true
	default:
		h.errorResponse(c, http.StatusBadRequest, 400, "INVALID_ARGUMENT", "Unknown action: "+action)
		return
	}

	h.handleGeminiRequest(c, modelName, isStream)
}

// GenerateContent handles Gemini generateContent API endpoint (legacy)
func (h *GeminiGatewayHandler) GenerateContent(c *gin.Context) {
	modelName := c.Param("model")
	h.handleGeminiRequest(c, modelName, false)
}

// StreamGenerateContent handles Gemini streamGenerateContent API endpoint (legacy)
func (h *GeminiGatewayHandler) StreamGenerateContent(c *gin.Context) {
	modelName := c.Param("model")
	h.handleGeminiRequest(c, modelName, true)
}

// handleGeminiRequest handles both streaming and non-streaming Gemini requests
func (h *GeminiGatewayHandler) handleGeminiRequest(c *gin.Context, modelName string, isStream bool) {
	// Get apiKey and user from context (set by ApiKeyAuth middleware)
	apiKey, ok := middleware.GetApiKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, 401, "UNAUTHENTICATED", "Invalid API key")
		return
	}

	user, ok := middleware.GetUserFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, 500, "INTERNAL", "User context not found")
		return
	}

	// Validate model name
	if modelName == "" {
		h.errorResponse(c, http.StatusBadRequest, 400, "INVALID_ARGUMENT", "Model name is required")
		return
	}

	// Read request body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, 400, "INVALID_ARGUMENT", "Failed to read request body")
		return
	}

	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, 400, "INVALID_ARGUMENT", "Request body is empty")
		return
	}

	// Track if we've started streaming (for error handling)
	streamStarted := false

	// Get subscription info (may be nil)
	subscription, _ := middleware.GetSubscriptionFromContext(c)

	// 0. Check if wait queue is full
	maxWait := service.CalculateMaxWait(user.Concurrency)
	canWait, err := h.concurrencyHelper.IncrementWaitCount(c.Request.Context(), user.ID, maxWait)
	if err != nil {
		log.Printf("Increment wait count failed: %v", err)
	} else if !canWait {
		h.errorResponse(c, http.StatusTooManyRequests, 429, "RESOURCE_EXHAUSTED", "Too many pending requests, please retry later")
		return
	}
	defer h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), user.ID)

	// 1. First acquire user concurrency slot
	userReleaseFunc, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, user, isStream, &streamStarted)
	if err != nil {
		log.Printf("User concurrency acquire failed: %v", err)
		h.handleConcurrencyError(c, err, "user", streamStarted)
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// 2. Re-check billing eligibility after wait
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), user, apiKey, apiKey.Group, subscription); err != nil {
		log.Printf("Billing eligibility check failed after wait: %v", err)
		h.handleStreamingAwareError(c, http.StatusForbidden, 403, "PERMISSION_DENIED", err.Error(), streamStarted)
		return
	}

	// Generate session hash
	sessionHash := h.gatewayService.GenerateSessionHash(body)

	// Select account supporting the requested model
	log.Printf("[Gemini Handler] Selecting account: groupID=%v model=%s", apiKey.GroupID, modelName)
	account, err := h.gatewayService.SelectAccountForModel(c.Request.Context(), apiKey.GroupID, sessionHash, modelName)
	if err != nil {
		log.Printf("[Gemini Handler] SelectAccount failed: %v", err)
		h.handleStreamingAwareError(c, http.StatusServiceUnavailable, 503, "UNAVAILABLE", "No available accounts: "+err.Error(), streamStarted)
		return
	}
	log.Printf("[Gemini Handler] Selected account: id=%d name=%s", account.ID, account.Name)

	// 3. Acquire account concurrency slot
	accountReleaseFunc, err := h.concurrencyHelper.AcquireAccountSlotWithWait(c, account, isStream, &streamStarted)
	if err != nil {
		log.Printf("Account concurrency acquire failed: %v", err)
		h.handleConcurrencyError(c, err, "account", streamStarted)
		return
	}
	if accountReleaseFunc != nil {
		defer accountReleaseFunc()
	}

	// Forward request
	result, err := h.gatewayService.Forward(c.Request.Context(), c, account, body, modelName, isStream)
	if err != nil {
		log.Printf("Forward request failed: %v", err)
		return
	}

	// Async record usage
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.gatewayService.RecordUsage(ctx, &service.GeminiRecordUsageInput{
			Result:       result,
			ApiKey:       apiKey,
			User:         user,
			Account:      account,
			Subscription: subscription,
		}); err != nil {
			log.Printf("Record usage failed: %v", err)
		}
	}()
}

// ChatCompletions handles OpenAI compatible chat completions endpoint
// POST /gemini/v1/chat/completions
func (h *GeminiGatewayHandler) ChatCompletions(c *gin.Context) {
	// Get apiKey and user from context
	apiKey, ok := middleware.GetApiKeyFromContext(c)
	if !ok {
		h.openaiErrorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	user, ok := middleware.GetUserFromContext(c)
	if !ok {
		h.openaiErrorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}

	// Read request body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.openaiErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	if len(body) == 0 {
		h.openaiErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	// Parse request to get model and stream
	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		h.openaiErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}

	reqModel, _ := reqBody["model"].(string)
	reqStream, _ := reqBody["stream"].(bool)

	// Track if we've started streaming
	streamStarted := false

	// Get subscription info
	subscription, _ := middleware.GetSubscriptionFromContext(c)

	// 0. Check wait queue
	maxWait := service.CalculateMaxWait(user.Concurrency)
	canWait, err := h.concurrencyHelper.IncrementWaitCount(c.Request.Context(), user.ID, maxWait)
	if err != nil {
		log.Printf("Increment wait count failed: %v", err)
	} else if !canWait {
		h.openaiErrorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests")
		return
	}
	defer h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), user.ID)

	// 1. Acquire user concurrency slot
	userReleaseFunc, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, user, reqStream, &streamStarted)
	if err != nil {
		h.handleOpenAIConcurrencyError(c, err, "user", streamStarted)
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// 2. Re-check billing eligibility
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), user, apiKey, apiKey.Group, subscription); err != nil {
		h.handleOpenAIStreamingAwareError(c, http.StatusForbidden, "billing_error", err.Error(), streamStarted)
		return
	}

	// Generate session hash
	sessionHash := h.gatewayService.GenerateSessionHash(body)

	// Select account
	log.Printf("[Gemini ChatCompletions] Selecting account: groupID=%v model=%s", apiKey.GroupID, reqModel)
	account, err := h.gatewayService.SelectAccountForModel(c.Request.Context(), apiKey.GroupID, sessionHash, reqModel)
	if err != nil {
		log.Printf("[Gemini ChatCompletions] SelectAccount failed: %v", err)
		h.handleOpenAIStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts: "+err.Error(), streamStarted)
		return
	}
	log.Printf("[Gemini ChatCompletions] Selected account: id=%d name=%s", account.ID, account.Name)

	// 3. Acquire account concurrency slot
	accountReleaseFunc, err := h.concurrencyHelper.AcquireAccountSlotWithWait(c, account, reqStream, &streamStarted)
	if err != nil {
		h.handleOpenAIConcurrencyError(c, err, "account", streamStarted)
		return
	}
	if accountReleaseFunc != nil {
		defer accountReleaseFunc()
	}

	// Forward request with OpenAI format conversion
	result, err := h.gatewayService.ForwardOpenAICompatible(c.Request.Context(), c, account, body)
	if err != nil {
		log.Printf("Forward request failed: %v", err)
		return
	}

	// Async record usage
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.gatewayService.RecordUsage(ctx, &service.GeminiRecordUsageInput{
			Result:       result,
			ApiKey:       apiKey,
			User:         user,
			Account:      account,
			Subscription: subscription,
		}); err != nil {
			log.Printf("Record usage failed: %v", err)
		}
	}()
}

// ListModels returns list of available Gemini models
// GET /gemini/v1beta/models
func (h *GeminiGatewayHandler) ListModels(c *gin.Context) {
	models := h.gatewayService.GetModels()

	// Convert to Gemini API format
	response := gin.H{
		"models": models,
	}

	c.JSON(http.StatusOK, response)
}

// Models returns list of models in OpenAI format
// GET /gemini/v1/models
func (h *GeminiGatewayHandler) Models(c *gin.Context) {
	models := h.gatewayService.GetModels()

	// Convert to OpenAI format
	var data []gin.H
	for _, m := range models {
		data = append(data, gin.H{
			"id":       m.ID,
			"object":   "model",
			"created":  m.Created,
			"owned_by": m.OwnedBy,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

// GetModel returns a specific model
// GET /gemini/v1beta/models/*model
func (h *GeminiGatewayHandler) GetModel(c *gin.Context) {
	modelName := c.Param("model")
	// Remove leading slash from wildcard parameter
	modelName = strings.TrimPrefix(modelName, "/")

	models := h.gatewayService.GetModels()

	for _, m := range models {
		if m.ID == modelName {
			c.JSON(http.StatusOK, gin.H{
				"name":        "models/" + m.ID,
				"displayName": m.DisplayName,
				"description": "Gemini model",
			})
			return
		}
	}

	h.errorResponse(c, http.StatusNotFound, 404, "NOT_FOUND", "Model not found: "+modelName)
}

// handleConcurrencyError handles concurrency-related errors
func (h *GeminiGatewayHandler) handleConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool) {
	h.handleStreamingAwareError(c, http.StatusTooManyRequests, 429, "RESOURCE_EXHAUSTED",
		fmt.Sprintf("Concurrency limit exceeded for %s, please retry later", slotType), streamStarted)
}

// handleStreamingAwareError handles errors that may occur after streaming has started
func (h *GeminiGatewayHandler) handleStreamingAwareError(c *gin.Context, status, code int, errStatus, message string, streamStarted bool) {
	if streamStarted {
		flusher, ok := c.Writer.(http.Flusher)
		if ok {
			errorEvent := fmt.Sprintf(`data: {"error": {"code": %d, "message": "%s", "status": "%s"}}`+"\n\n", code, message, errStatus)
			if _, err := fmt.Fprint(c.Writer, errorEvent); err != nil {
				_ = c.Error(err)
			}
			flusher.Flush()
		}
		return
	}

	h.errorResponse(c, status, code, errStatus, message)
}

// errorResponse returns Gemini API format error response
func (h *GeminiGatewayHandler) errorResponse(c *gin.Context, status, code int, errStatus, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
			"status":  errStatus,
		},
	})
}

// handleOpenAIConcurrencyError handles concurrency errors in OpenAI format
func (h *GeminiGatewayHandler) handleOpenAIConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool) {
	h.handleOpenAIStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error",
		fmt.Sprintf("Concurrency limit exceeded for %s", slotType), streamStarted)
}

// handleOpenAIStreamingAwareError handles errors in OpenAI format
func (h *GeminiGatewayHandler) handleOpenAIStreamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool) {
	if streamStarted {
		flusher, ok := c.Writer.(http.Flusher)
		if ok {
			errorEvent := fmt.Sprintf(`event: error`+"\n"+`data: {"error": {"type": "%s", "message": "%s"}}`+"\n\n", errType, message)
			if _, err := fmt.Fprint(c.Writer, errorEvent); err != nil {
				_ = c.Error(err)
			}
			flusher.Flush()
		}
		return
	}

	h.openaiErrorResponse(c, status, errType, message)
}

// openaiErrorResponse returns OpenAI API format error response
func (h *GeminiGatewayHandler) openaiErrorResponse(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// Ensure gemini package is imported for model types
var _ = gemini.DefaultModels
