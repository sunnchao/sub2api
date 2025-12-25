package service

import (
	"context"
	"fmt"
	"time"

	"sub2api/internal/model"
	"sub2api/internal/pkg/gemini"
	"sub2api/internal/service/ports"
)

// GeminiOAuthService handles Gemini OAuth authentication flows
type GeminiOAuthService struct {
	sessionStore *gemini.SessionStore
	proxyRepo    ports.ProxyRepository
	accountRepo  ports.AccountRepository
	oauthClient  ports.GeminiOAuthClient
}

// NewGeminiOAuthService creates a new GeminiOAuthService
func NewGeminiOAuthService(
	proxyRepo ports.ProxyRepository,
	accountRepo ports.AccountRepository,
	oauthClient ports.GeminiOAuthClient,
) *GeminiOAuthService {
	return &GeminiOAuthService{
		sessionStore: gemini.NewSessionStore(),
		proxyRepo:    proxyRepo,
		accountRepo:  accountRepo,
		oauthClient:  oauthClient,
	}
}

// GeminiAuthURLInput input for generating auth URL
type GeminiAuthURLInput struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	ProxyID      *int64
}

// GeminiAuthURLResult result of auth URL generation
type GeminiAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
}

// GenerateAuthURL generates a Gemini OAuth authorization URL
func (s *GeminiOAuthService) GenerateAuthURL(ctx context.Context, input *GeminiAuthURLInput) (*GeminiAuthURLResult, error) {
	// Generate PKCE values
	state, err := gemini.GenerateState()
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}

	codeVerifier, err := gemini.GenerateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("generate code verifier: %w", err)
	}

	codeChallenge := gemini.GenerateCodeChallenge(codeVerifier)

	sessionID, err := gemini.GenerateSessionID()
	if err != nil {
		return nil, fmt.Errorf("generate session ID: %w", err)
	}

	// Get proxy URL if specified
	var proxyURL string
	if input.ProxyID != nil {
		proxy, err := s.proxyRepo.GetByID(ctx, *input.ProxyID)
		if err == nil && proxy != nil {
			proxyURL = proxy.URL()
		}
	}

	// Store session
	session := &gemini.OAuthSession{
		State:        state,
		CodeVerifier: codeVerifier,
		ClientID:     input.ClientID,
		ClientSecret: input.ClientSecret,
		RedirectURI:  input.RedirectURI,
		ProxyURL:     proxyURL,
		CreatedAt:    time.Now(),
	}
	s.sessionStore.Set(sessionID, session)

	// Build authorization URL
	authURL := gemini.BuildAuthorizationURL(input.ClientID, input.RedirectURI, state, codeChallenge)

	return &GeminiAuthURLResult{
		AuthURL:   authURL,
		SessionID: sessionID,
	}, nil
}

// GeminiExchangeCodeInput input for code exchange
type GeminiExchangeCodeInput struct {
	SessionID string
	Code      string
}

// GeminiTokenInfo token information
type GeminiTokenInfo struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int64  `json:"expires_in"`
	ExpiresAt    int64  `json:"expires_at"`
}

// ExchangeCode exchanges authorization code for tokens
func (s *GeminiOAuthService) ExchangeCode(ctx context.Context, input *GeminiExchangeCodeInput) (*GeminiTokenInfo, error) {
	session, ok := s.sessionStore.Get(input.SessionID)
	if !ok {
		return nil, fmt.Errorf("session not found or expired")
	}

	// Exchange code for tokens
	tokenResp, err := s.oauthClient.ExchangeCode(ctx, input.Code, session.CodeVerifier, session.ClientID, session.ClientSecret, session.RedirectURI, session.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	// Delete used session
	s.sessionStore.Delete(input.SessionID)

	return &GeminiTokenInfo{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresIn:    tokenResp.ExpiresIn,
		ExpiresAt:    time.Now().Unix() + tokenResp.ExpiresIn,
	}, nil
}

// RefreshToken refreshes an access token
func (s *GeminiOAuthService) RefreshToken(ctx context.Context, clientID, clientSecret, refreshToken, proxyURL string) (*GeminiTokenInfo, error) {
	tokenResp, err := s.oauthClient.RefreshToken(ctx, clientID, clientSecret, refreshToken, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}

	result := &GeminiTokenInfo{
		AccessToken: tokenResp.AccessToken,
		ExpiresIn:   tokenResp.ExpiresIn,
		ExpiresAt:   time.Now().Unix() + tokenResp.ExpiresIn,
	}

	// Google may return a new refresh token
	if tokenResp.RefreshToken != "" {
		result.RefreshToken = tokenResp.RefreshToken
	}

	return result, nil
}

// RefreshAccountToken refreshes token for an account
func (s *GeminiOAuthService) RefreshAccountToken(ctx context.Context, account *model.Account) (*GeminiTokenInfo, error) {
	if !account.IsGeminiOAuth() {
		return nil, fmt.Errorf("account is not a Gemini OAuth account")
	}

	clientID := account.GetCredential("client_id")
	clientSecret := account.GetCredential("client_secret")
	refreshToken := account.GetGeminiRefreshToken()

	if clientID == "" || clientSecret == "" || refreshToken == "" {
		return nil, fmt.Errorf("missing OAuth credentials")
	}

	// Get proxy URL
	var proxyURL string
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	return s.RefreshToken(ctx, clientID, clientSecret, refreshToken, proxyURL)
}

// BuildAccountCredentials builds credentials map from token info
func (s *GeminiOAuthService) BuildAccountCredentials(tokenInfo *GeminiTokenInfo, clientID, clientSecret string) model.JSONB {
	creds := model.JSONB{
		"access_token":  tokenInfo.AccessToken,
		"expires_at":    time.Unix(tokenInfo.ExpiresAt, 0).Format(time.RFC3339),
		"client_id":     clientID,
		"client_secret": clientSecret,
	}

	if tokenInfo.RefreshToken != "" {
		creds["refresh_token"] = tokenInfo.RefreshToken
	}

	return creds
}

// NeedsRefresh checks if account token needs refresh
func (s *GeminiOAuthService) NeedsRefresh(account *model.Account, refreshBeforeHours int) bool {
	if !account.IsGeminiOAuth() {
		return false
	}

	expiresAt := account.GetGeminiTokenExpiresAt()
	if expiresAt == nil {
		return false
	}

	// Check if token expires within refreshBeforeHours
	refreshThreshold := time.Duration(refreshBeforeHours) * time.Hour
	return time.Until(*expiresAt) < refreshThreshold
}
