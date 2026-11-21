package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

// NewAPIClientOptions 控制与 New API 站点通信时的客户端选项。
type NewAPIClientOptions struct {
	ValidateResolvedIP bool
	AllowPrivateHosts  bool
	Timeout            time.Duration
}

// NewAPIStatus 表示 New API GET /api/status 返回的站点元信息。
type NewAPIStatus struct {
	SystemName        string  `json:"system_name"`
	Version           string  `json:"version"`
	QuotaPerUnit      float64 `json:"quota_per_unit"`
	DisplayInCurrency bool    `json:"display_in_currency"`
}

// NewAPIUserInfo 表示从 New API 获取的用户与余额快照。
type NewAPIUserInfo struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	Email        string `json:"email"`
	Role         int    `json:"role"`
	Status       int    `json:"status"`
	TotalQuota   int64  `json:"total_quota"`
	RegularQuota int64  `json:"regular_quota"`
	GiftQuota    int64  `json:"gift_quota"`
	UsedQuota    int64  `json:"used_quota"`
	Group        string `json:"group"`
}

// NewAPIUpstreamError 封装 New API 上游返回的错误，区分明确拒绝与不确定的传输中断。
type NewAPIUpstreamError struct {
	StatusCode int
	Code       string
	Message    string
	Ambiguous  bool // true 表示请求可能已被上游接收但在读取响应前超时/断连
	Cause      error
}

func (e *NewAPIUpstreamError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		if e.Code != "" {
			return fmt.Sprintf("new-api error (%d/%s): %s", e.StatusCode, e.Code, e.Message)
		}
		if e.StatusCode > 0 {
			return fmt.Sprintf("new-api error (%d): %s", e.StatusCode, e.Message)
		}
		return e.Message
	}
	if e.Cause != nil {
		return fmt.Sprintf("new-api request failed: %v", e.Cause)
	}
	return fmt.Sprintf("new-api error (%d)", e.StatusCode)
}

func (e *NewAPIUpstreamError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsNewAPIAmbiguousError 判断错误是否为不确定结果的网络超时/读断连。
func IsNewAPIAmbiguousError(err error) bool {
	var upstreamErr *NewAPIUpstreamError
	return errors.As(err, &upstreamErr) && upstreamErr.Ambiguous
}

// NewAPIClient 定义与 New API 站点交互的抽象客户端接口。
type NewAPIClient interface {
	GetStatus(ctx context.Context, baseURL string, opts NewAPIClientOptions) (*NewAPIStatus, error)
	GetSelf(ctx context.Context, baseURL, userAccessToken string, opts NewAPIClientOptions) (*NewAPIUserInfo, error)
	GetUserByAdmin(ctx context.Context, baseURL, adminAccessToken string, newAPIUserID int64, opts NewAPIClientOptions) (*NewAPIUserInfo, error)
	CheckAdminUserRead(ctx context.Context, baseURL, adminAccessToken string, opts NewAPIClientOptions) error
	AdjustUserQuota(ctx context.Context, baseURL, adminAccessToken string, newAPIUserID int64, mode string, quota int64, opts NewAPIClientOptions) error
}

const (
	defaultNewAPIHTTPTimeout = 15 * time.Second
	maxNewAPIResponseBytes   = 2 << 20 // 2 MiB
)

type newAPIEnvelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Code    string          `json:"code"`
	Data    json.RawMessage `json:"data"`
}

// NewAPIHTTPClient 是基于共享连接池的 New API HTTP 客户端实现。
type NewAPIHTTPClient struct {
	httpClient *http.Client // 测试可注入自定义 *http.Client
}

// NewNewAPIHTTPClient 创建默认 New API HTTP 客户端。
func NewNewAPIHTTPClient() *NewAPIHTTPClient {
	return &NewAPIHTTPClient{}
}

func (c *NewAPIHTTPClient) client(opts NewAPIClientOptions) (*http.Client, error) {
	if c != nil && c.httpClient != nil {
		return c.httpClient, nil
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultNewAPIHTTPTimeout
	}
	return httpclient.GetClient(httpclient.Options{
		Timeout:            timeout,
		ValidateResolvedIP: opts.ValidateResolvedIP,
		AllowPrivateHosts:  opts.AllowPrivateHosts,
	})
}

func normalizeNewAPIBearerToken(raw string) string {
	token := strings.TrimSpace(raw)
	if len(token) >= 7 && strings.EqualFold(token[:7], "Bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	return token
}

func (c *NewAPIHTTPClient) doJSON(
	ctx context.Context,
	method string,
	endpoint string,
	accessToken string,
	body any,
	opts NewAPIClientOptions,
) (*newAPIEnvelope, error) {
	httpClient, err := c.client(opts)
	if err != nil {
		return nil, &NewAPIUpstreamError{Message: "failed to create http client", Cause: err}
	}

	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return nil, &NewAPIUpstreamError{Message: "invalid request url", Cause: err}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := normalizeNewAPIBearerToken(accessToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, classifyNewAPITransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	rawBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxNewAPIResponseBytes))
	if readErr != nil {
		return nil, &NewAPIUpstreamError{
			StatusCode: resp.StatusCode,
			Message:    "failed to read response from New API",
			Ambiguous:  true,
			Cause:      readErr,
		}
	}

	var env newAPIEnvelope
	if len(rawBytes) > 0 {
		if unmarshalErr := json.Unmarshal(rawBytes, &env); unmarshalErr != nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil, &NewAPIUpstreamError{
					StatusCode: resp.StatusCode,
					Message:    "unexpected non-JSON response from New API",
					Cause:      unmarshalErr,
				}
			}
			return nil, &NewAPIUpstreamError{
				StatusCode: resp.StatusCode,
				Message:    fmt.Sprintf("New API returned HTTP %d", resp.StatusCode),
				Cause:      unmarshalErr,
			}
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !env.Success {
		msg := strings.TrimSpace(env.Message)
		if msg == "" {
			msg = fmt.Sprintf("New API returned HTTP %d", resp.StatusCode)
		}
		return nil, &NewAPIUpstreamError{
			StatusCode: resp.StatusCode,
			Code:       strings.TrimSpace(env.Code),
			Message:    msg,
		}
	}

	return &env, nil
}

// classifyNewAPITransportError 将连接前失败（dial/DNS）标记为明确失败，将发送后的超时/EOF 标记为结果不确定。
func classifyNewAPITransportError(err error) *NewAPIUpstreamError {
	// 连接阶段（net.OpError with Op == "dial" 或 DNS 错误）说明请求未发往上游，结果确定未生效。
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return &NewAPIUpstreamError{
			Message:   "failed to connect to New API site",
			Ambiguous: false,
			Cause:     err,
		}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &NewAPIUpstreamError{
			Message:   "failed to resolve New API host",
			Ambiguous: false,
			Cause:     err,
		}
	}
	return &NewAPIUpstreamError{
		Message:   "communication with New API interrupted or timed out",
		Ambiguous: true,
		Cause:     err,
	}
}

func (c *NewAPIHTTPClient) GetStatus(ctx context.Context, baseURL string, opts NewAPIClientOptions) (*NewAPIStatus, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/api/status"
	env, err := c.doJSON(ctx, http.MethodGet, endpoint, "", nil, opts)
	if err != nil {
		return nil, err
	}
	var payload struct {
		SystemName        string  `json:"system_name"`
		Version           string  `json:"version"`
		QuotaPerUnit      float64 `json:"quota_per_unit"`
		DisplayInCurrency bool    `json:"display_in_currency"`
	}
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &payload); err != nil {
			return nil, &NewAPIUpstreamError{Message: "invalid /api/status response payload", Cause: err}
		}
	}
	if payload.QuotaPerUnit <= 0 {
		payload.QuotaPerUnit = DefaultNewAPIQuotaPerUnit
	}
	return &NewAPIStatus{
		SystemName:        strings.TrimSpace(payload.SystemName),
		Version:           strings.TrimSpace(payload.Version),
		QuotaPerUnit:      payload.QuotaPerUnit,
		DisplayInCurrency: payload.DisplayInCurrency,
	}, nil
}

func (c *NewAPIHTTPClient) GetSelf(ctx context.Context, baseURL, userAccessToken string, opts NewAPIClientOptions) (*NewAPIUserInfo, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/api/user/self"
	env, err := c.doJSON(ctx, http.MethodGet, endpoint, userAccessToken, nil, opts)
	if err != nil {
		return nil, err
	}
	var payload struct {
		ID           int64  `json:"id"`
		Username     string `json:"username"`
		DisplayName  string `json:"display_name"`
		Email        string `json:"email"`
		Role         int    `json:"role"`
		Status       int    `json:"status"`
		Quota        int64  `json:"quota"`
		RegularQuota *int64 `json:"regular_quota"`
		GiftQuota    int64  `json:"gift_quota"`
		UsedQuota    int64  `json:"used_quota"`
		Group        string `json:"group"`
	}
	if err := json.Unmarshal(env.Data, &payload); err != nil || payload.ID <= 0 {
		return nil, &NewAPIUpstreamError{Message: "invalid /api/user/self response payload", Cause: err}
	}
	regularQuota := payload.Quota
	if payload.RegularQuota != nil {
		regularQuota = *payload.RegularQuota
	}
	return &NewAPIUserInfo{
		ID:           payload.ID,
		Username:     strings.TrimSpace(payload.Username),
		DisplayName:  strings.TrimSpace(payload.DisplayName),
		Email:        strings.TrimSpace(payload.Email),
		Role:         payload.Role,
		Status:       payload.Status,
		TotalQuota:   payload.Quota,
		RegularQuota: regularQuota,
		GiftQuota:    payload.GiftQuota,
		UsedQuota:    payload.UsedQuota,
		Group:        strings.TrimSpace(payload.Group),
	}, nil
}

func (c *NewAPIHTTPClient) GetUserByAdmin(ctx context.Context, baseURL, adminAccessToken string, newAPIUserID int64, opts NewAPIClientOptions) (*NewAPIUserInfo, error) {
	endpoint := fmt.Sprintf("%s/api/user/%d", strings.TrimRight(strings.TrimSpace(baseURL), "/"), newAPIUserID)
	env, err := c.doJSON(ctx, http.MethodGet, endpoint, adminAccessToken, nil, opts)
	if err != nil {
		return nil, err
	}
	var payload struct {
		ID          int64  `json:"id"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Role        int    `json:"role"`
		Status      int    `json:"status"`
		Quota       int64  `json:"quota"`
		UsedQuota   int64  `json:"used_quota"`
		Group       string `json:"group"`
	}
	if err := json.Unmarshal(env.Data, &payload); err != nil || payload.ID <= 0 {
		return nil, &NewAPIUpstreamError{Message: "invalid /api/user/:id response payload", Cause: err}
	}
	return &NewAPIUserInfo{
		ID:           payload.ID,
		Username:     strings.TrimSpace(payload.Username),
		DisplayName:  strings.TrimSpace(payload.DisplayName),
		Email:        strings.TrimSpace(payload.Email),
		Role:         payload.Role,
		Status:       payload.Status,
		TotalQuota:   payload.Quota,
		RegularQuota: payload.Quota, // GET /api/user/:id 直接返回 users.quota（常规余额）
		UsedQuota:    payload.UsedQuota,
		Group:        strings.TrimSpace(payload.Group),
	}, nil
}

func (c *NewAPIHTTPClient) CheckAdminUserRead(ctx context.Context, baseURL, adminAccessToken string, opts NewAPIClientOptions) error {
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/api/user/?p=0&page_size=1"
	_, err := c.doJSON(ctx, http.MethodGet, endpoint, adminAccessToken, nil, opts)
	return err
}

// AdjustUserQuota 调用 New API 管理端原子余额调整接口 POST /api/user/manage (action="add_quota")。
// mode 支持 "subtract"（从 New API 迁出扣减）和 "add"（回滚补偿或未来反向迁入 New API）。
func (c *NewAPIHTTPClient) AdjustUserQuota(ctx context.Context, baseURL, adminAccessToken string, newAPIUserID int64, mode string, quota int64, opts NewAPIClientOptions) error {
	if newAPIUserID <= 0 || quota <= 0 {
		return &NewAPIUpstreamError{Message: "invalid user id or quota"}
	}
	if mode != "subtract" && mode != "add" {
		return &NewAPIUpstreamError{Message: fmt.Sprintf("unsupported quota adjustment mode: %q", mode)}
	}
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/api/user/manage"
	payload := struct {
		ID     int64  `json:"id"`
		Action string `json:"action"`
		Mode   string `json:"mode"`
		Value  int64  `json:"value"`
	}{
		ID:     newAPIUserID,
		Action: "add_quota",
		Mode:   mode,
		Value:  quota,
	}
	_, err := c.doJSON(ctx, http.MethodPost, endpoint, adminAccessToken, payload, opts)
	return err
}
