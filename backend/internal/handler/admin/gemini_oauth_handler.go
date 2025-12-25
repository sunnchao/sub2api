package admin

import (
	"strconv"

	"sub2api/internal/pkg/response"
	"sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// GeminiOAuthHandler handles Gemini OAuth-related operations
type GeminiOAuthHandler struct {
	geminiOAuthService *service.GeminiOAuthService
	adminService       service.AdminService
}

// NewGeminiOAuthHandler creates a new Gemini OAuth handler
func NewGeminiOAuthHandler(geminiOAuthService *service.GeminiOAuthService, adminService service.AdminService) *GeminiOAuthHandler {
	return &GeminiOAuthHandler{
		geminiOAuthService: geminiOAuthService,
		adminService:       adminService,
	}
}

// GeminiGenerateAuthURLRequest represents the request for generating Gemini auth URL
type GeminiGenerateAuthURLRequest struct {
	ClientID     string `json:"client_id" binding:"required"`
	ClientSecret string `json:"client_secret" binding:"required"`
	RedirectURI  string `json:"redirect_uri" binding:"required"`
	ProxyID      *int64 `json:"proxy_id"`
}

// GenerateAuthURL generates Gemini OAuth authorization URL
// POST /api/v1/admin/gemini/generate-auth-url
func (h *GeminiOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req GeminiGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	result, err := h.geminiOAuthService.GenerateAuthURL(c.Request.Context(), &service.GeminiAuthURLInput{
		ClientID:     req.ClientID,
		ClientSecret: req.ClientSecret,
		RedirectURI:  req.RedirectURI,
		ProxyID:      req.ProxyID,
	})
	if err != nil {
		response.InternalError(c, "Failed to generate auth URL: "+err.Error())
		return
	}

	response.Success(c, result)
}

// GeminiExchangeCodeRequest represents the request for exchanging Gemini auth code
type GeminiExchangeCodeRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	Code      string `json:"code" binding:"required"`
}

// ExchangeCode exchanges Gemini authorization code for tokens
// POST /api/v1/admin/gemini/exchange-code
func (h *GeminiOAuthHandler) ExchangeCode(c *gin.Context) {
	var req GeminiExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.geminiOAuthService.ExchangeCode(c.Request.Context(), &service.GeminiExchangeCodeInput{
		SessionID: req.SessionID,
		Code:      req.Code,
	})
	if err != nil {
		response.BadRequest(c, "Failed to exchange code: "+err.Error())
		return
	}

	response.Success(c, tokenInfo)
}

// GeminiRefreshTokenRequest represents the request for refreshing Gemini token
type GeminiRefreshTokenRequest struct {
	ClientID     string `json:"client_id" binding:"required"`
	ClientSecret string `json:"client_secret" binding:"required"`
	RefreshToken string `json:"refresh_token" binding:"required"`
	ProxyID      *int64 `json:"proxy_id"`
}

// RefreshToken refreshes a Gemini OAuth token
// POST /api/v1/admin/gemini/refresh-token
func (h *GeminiOAuthHandler) RefreshToken(c *gin.Context) {
	var req GeminiRefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	var proxyURL string
	if req.ProxyID != nil {
		proxy, err := h.adminService.GetProxy(c.Request.Context(), *req.ProxyID)
		if err == nil && proxy != nil {
			proxyURL = proxy.URL()
		}
	}

	tokenInfo, err := h.geminiOAuthService.RefreshToken(c.Request.Context(), req.ClientID, req.ClientSecret, req.RefreshToken, proxyURL)
	if err != nil {
		response.BadRequest(c, "Failed to refresh token: "+err.Error())
		return
	}

	response.Success(c, tokenInfo)
}

// RefreshAccountToken refreshes token for an existing account
// POST /api/v1/admin/gemini/accounts/:id/refresh
func (h *GeminiOAuthHandler) RefreshAccountToken(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	account, err := h.adminService.GetAccount(c.Request.Context(), id)
	if err != nil {
		response.NotFound(c, "Account not found")
		return
	}

	if !account.IsGeminiOAuth() {
		response.BadRequest(c, "Account is not a Gemini OAuth account")
		return
	}

	tokenInfo, err := h.geminiOAuthService.RefreshAccountToken(c.Request.Context(), account)
	if err != nil {
		response.InternalError(c, "Failed to refresh token: "+err.Error())
		return
	}

	// Update account credentials
	clientID := account.GetCredential("client_id")
	clientSecret := account.GetCredential("client_secret")
	creds := h.geminiOAuthService.BuildAccountCredentials(tokenInfo, clientID, clientSecret)
	if account.GetGeminiRefreshToken() != "" && tokenInfo.RefreshToken == "" {
		// Keep existing refresh token if not returned
		creds["refresh_token"] = account.GetGeminiRefreshToken()
	}

	// Preserve non-token settings from existing credentials
	for k, v := range account.Credentials {
		if _, exists := creds[k]; !exists {
			creds[k] = v
		}
	}

	_, err = h.adminService.UpdateAccount(c.Request.Context(), id, &service.UpdateAccountInput{
		Credentials: creds,
	})
	if err != nil {
		response.InternalError(c, "Failed to update account: "+err.Error())
		return
	}

	response.Success(c, tokenInfo)
}

// GeminiCreateAccountRequest represents the request for creating account from OAuth
type GeminiCreateAccountRequest struct {
	Name         string    `json:"name" binding:"required"`
	ClientID     string    `json:"client_id" binding:"required"`
	ClientSecret string    `json:"client_secret" binding:"required"`
	TokenInfo    TokenInfo `json:"token_info" binding:"required"`
	ProxyID      *int64    `json:"proxy_id"`
	GroupIDs     []int64   `json:"group_ids"`
	Priority     int       `json:"priority"`
	Concurrency  int       `json:"concurrency"`
}

type TokenInfo struct {
	AccessToken  string `json:"access_token" binding:"required"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

// CreateAccountFromOAuth creates a new Gemini account from OAuth credentials
// POST /api/v1/admin/gemini/create-from-oauth
func (h *GeminiOAuthHandler) CreateAccountFromOAuth(c *gin.Context) {
	var req GeminiCreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// Build credentials
	creds := h.geminiOAuthService.BuildAccountCredentials(&service.GeminiTokenInfo{
		AccessToken:  req.TokenInfo.AccessToken,
		RefreshToken: req.TokenInfo.RefreshToken,
		ExpiresAt:    req.TokenInfo.ExpiresAt,
	}, req.ClientID, req.ClientSecret)

	// Create account using service input
	account, err := h.adminService.CreateAccount(c.Request.Context(), &service.CreateAccountInput{
		Name:        req.Name,
		Platform:    "gemini",
		Type:        "oauth",
		Credentials: creds,
		ProxyID:     req.ProxyID,
		Concurrency: req.Concurrency,
		Priority:    req.Priority,
		GroupIDs:    req.GroupIDs,
	})
	if err != nil {
		response.InternalError(c, "Failed to create account: "+err.Error())
		return
	}

	response.Success(c, account)
}
