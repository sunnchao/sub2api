package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewAPIHTTPClient_Endpoints(t *testing.T) {
	var lastAuth string
	var lastManageBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "",
				"data": map[string]any{
					"system_name":         "Demo New API",
					"version":             "v1.0.0",
					"quota_per_unit":      500000,
					"display_in_currency": true,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/self":
			if lastAuth != "Bearer nap_user_token" {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": false,
					"code":    "ACCESS_TOKEN_EXPIRED",
					"message": "Unauthorized, the access token has expired",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "",
				"data": map[string]any{
					"id":            42,
					"username":      "alice",
					"display_name":  "Alice",
					"email":         "alice@example.com",
					"role":          1,
					"status":        1,
					"quota":         6000000,
					"regular_quota": 5000000,
					"gift_quota":    1000000,
					"used_quota":    250000,
					"group":         "default",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/42":
			if lastAuth != "Bearer nap_admin_token" {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": false,
					"code":    "ACCESS_TOKEN_SCOPE_DENIED",
					"message": "The access token lacks the required permission: user:read",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "",
				"data": map[string]any{
					"id":           42,
					"username":     "alice",
					"display_name": "Alice",
					"email":        "alice@example.com",
					"role":         1,
					"status":       1,
					"quota":        5000000,
					"used_quota":   250000,
					"group":        "default",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/":
			if lastAuth != "Bearer nap_admin_token" {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": false,
					"code":    "ACCESS_TOKEN_SCOPE_DENIED",
					"message": "missing user:read",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "",
				"data":    map[string]any{"items": []any{}, "total": 0},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/user/manage":
			_ = json.NewDecoder(r.Body).Decode(&lastManageBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := &NewAPIHTTPClient{httpClient: srv.Client()}
	ctx := context.Background()
	opts := NewAPIClientOptions{Timeout: 5 * time.Second}

	status, err := client.GetStatus(ctx, srv.URL, opts)
	require.NoError(t, err)
	require.Equal(t, "Demo New API", status.SystemName)
	require.Equal(t, "v1.0.0", status.Version)
	require.Equal(t, 500000.0, status.QuotaPerUnit)
	require.True(t, status.DisplayInCurrency)

	// Supports optional "Bearer " prefix in user-pasted token
	selfUser, err := client.GetSelf(ctx, srv.URL, "Bearer nap_user_token", opts)
	require.NoError(t, err)
	require.Equal(t, int64(42), selfUser.ID)
	require.Equal(t, int64(5000000), selfUser.RegularQuota)
	require.Equal(t, int64(1000000), selfUser.GiftQuota)
	require.Equal(t, int64(6000000), selfUser.TotalQuota)

	// Expired token error mapping
	_, err = client.GetSelf(ctx, srv.URL, "nap_bad", opts)
	require.Error(t, err)
	var upErr *NewAPIUpstreamError
	require.ErrorAs(t, err, &upErr)
	require.Equal(t, http.StatusUnauthorized, upErr.StatusCode)
	require.Equal(t, "ACCESS_TOKEN_EXPIRED", upErr.Code)
	require.False(t, upErr.Ambiguous)

	// Admin lookup
	adminUser, err := client.GetUserByAdmin(ctx, srv.URL, "nap_admin_token", 42, opts)
	require.NoError(t, err)
	require.Equal(t, int64(5000000), adminUser.RegularQuota)

	// Admin scope check
	require.NoError(t, client.CheckAdminUserRead(ctx, srv.URL, "nap_admin_token", opts))

	// Atomic quota subtract via POST /api/user/manage
	err = client.AdjustUserQuota(ctx, srv.URL, "nap_admin_token", 42, "subtract", 5000000, opts)
	require.NoError(t, err)
	require.Equal(t, float64(42), lastManageBody["id"])
	require.Equal(t, "add_quota", lastManageBody["action"])
	require.Equal(t, "subtract", lastManageBody["mode"])
	require.Equal(t, float64(5000000), lastManageBody["value"])
}

func TestNewAPIHTTPClient_LegacySelfWithoutRegularQuotaField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"message": "",
			"data": map[string]any{
				"id":       7,
				"username": "legacy",
				"status":   1,
				"role":     1,
				"quota":    2500000,
			},
		})
	}))
	defer srv.Close()

	client := &NewAPIHTTPClient{httpClient: srv.Client()}
	selfUser, err := client.GetSelf(context.Background(), srv.URL, "legacy_token", NewAPIClientOptions{})
	require.NoError(t, err)
	require.Equal(t, int64(2500000), selfUser.RegularQuota)
	require.Equal(t, int64(2500000), selfUser.TotalQuota)
	require.Equal(t, int64(0), selfUser.GiftQuota)
}
