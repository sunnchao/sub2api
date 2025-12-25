package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sub2api/internal/pkg/gemini"
)

// GeminiOAuthClient implements Gemini OAuth HTTP operations
type GeminiOAuthClient struct {
	httpClient *http.Client
}

// NewGeminiOAuthClient creates a new GeminiOAuthClient
func NewGeminiOAuthClient() *GeminiOAuthClient {
	return &GeminiOAuthClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ExchangeCode exchanges authorization code for tokens
func (c *GeminiOAuthClient) ExchangeCode(ctx context.Context, code, codeVerifier, clientID, clientSecret, redirectURI, proxyURL string) (*gemini.TokenResponse, error) {
	req := gemini.BuildTokenRequest(clientID, clientSecret, code, redirectURI, codeVerifier)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", gemini.TokenURL, strings.NewReader(req.ToFormData()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := c.getHTTPClient(proxyURL)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %d - %s", resp.StatusCode, string(body))
	}

	var tokenResp gemini.TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &tokenResp, nil
}

// RefreshToken refreshes an access token
func (c *GeminiOAuthClient) RefreshToken(ctx context.Context, clientID, clientSecret, refreshToken, proxyURL string) (*gemini.TokenResponse, error) {
	req := gemini.BuildRefreshTokenRequest(clientID, clientSecret, refreshToken)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", gemini.TokenURL, strings.NewReader(req.ToFormData()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := c.getHTTPClient(proxyURL)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: %d - %s", resp.StatusCode, string(body))
	}

	var tokenResp gemini.TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &tokenResp, nil
}

// getHTTPClient returns an HTTP client with optional proxy
func (c *GeminiOAuthClient) getHTTPClient(proxyURL string) *http.Client {
	if proxyURL == "" {
		return c.httpClient
	}

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return c.httpClient
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(parsedURL),
	}

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
}
