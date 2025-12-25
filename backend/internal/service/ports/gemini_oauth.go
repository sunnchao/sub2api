package ports

import (
	"context"

	"sub2api/internal/pkg/gemini"
)

// GeminiOAuthClient interface for Gemini OAuth operations
type GeminiOAuthClient interface {
	ExchangeCode(ctx context.Context, code, codeVerifier, clientID, clientSecret, redirectURI, proxyURL string) (*gemini.TokenResponse, error)
	RefreshToken(ctx context.Context, clientID, clientSecret, refreshToken, proxyURL string) (*gemini.TokenResponse, error)
}
