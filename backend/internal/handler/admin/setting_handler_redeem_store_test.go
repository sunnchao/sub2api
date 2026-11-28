package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingHandler_RedeemStoreRoundTripAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("defaults and round trip", func(t *testing.T) {
		repo := &settingHandlerRepoStub{}
		svc := service.NewSettingService(repo, &config.Config{})
		handler := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewBufferString(`{
			"redeem_store_url": "  https://shop.example.com/cards?from=site  ",
			"redeem_store_open_mode": "iframe",
			"redeem_store_label": "  官方发卡商城  "
		}`))
		c.Request.Header.Set("Content-Type", "application/json")
		handler.UpdateSettings(c)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		require.Equal(t, "https://shop.example.com/cards?from=site", repo.values[service.SettingKeyRedeemStoreURL])
		require.Equal(t, service.RedeemStoreOpenModeIframe, repo.values[service.SettingKeyRedeemStoreOpenMode])
		require.Equal(t, "官方发卡商城", repo.values[service.SettingKeyRedeemStoreLabel])

		var putResp struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &putResp))
		require.Equal(t, "https://shop.example.com/cards?from=site", putResp.Data["redeem_store_url"])
		require.Equal(t, "iframe", putResp.Data["redeem_store_open_mode"])
		require.Equal(t, "官方发卡商城", putResp.Data["redeem_store_label"])

		// Partial payload omitting redeem store fields preserves previous values
		rec2 := httptest.NewRecorder()
		c2, _ := gin.CreateTestContext(rec2)
		c2.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewBufferString(`{"site_name":"Sub2API"}`))
		c2.Request.Header.Set("Content-Type", "application/json")
		handler.UpdateSettings(c2)
		require.Equal(t, http.StatusOK, rec2.Code, rec2.Body.String())
		require.Equal(t, "https://shop.example.com/cards?from=site", repo.values[service.SettingKeyRedeemStoreURL])
		require.Equal(t, "iframe", repo.values[service.SettingKeyRedeemStoreOpenMode])
		require.Equal(t, "官方发卡商城", repo.values[service.SettingKeyRedeemStoreLabel])

		// Clearing URL succeeds
		rec3 := httptest.NewRecorder()
		c3, _ := gin.CreateTestContext(rec3)
		c3.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewBufferString(`{"redeem_store_url":"","redeem_store_open_mode":""}`))
		c3.Request.Header.Set("Content-Type", "application/json")
		handler.UpdateSettings(c3)
		require.Equal(t, http.StatusOK, rec3.Code, rec3.Body.String())
		require.Equal(t, "", repo.values[service.SettingKeyRedeemStoreURL])
		require.Equal(t, service.RedeemStoreOpenModeNewTab, repo.values[service.SettingKeyRedeemStoreOpenMode])
	})

	invalidCases := []struct {
		name string
		body string
	}{
		{"javascript scheme rejected", `{"redeem_store_url":"javascript:alert(1)"}`},
		{"ftp scheme rejected", `{"redeem_store_url":"ftp://shop.example.com"}`},
		{"relative path rejected", `{"redeem_store_url":"/shop"}`},
		{"missing scheme rejected", `{"redeem_store_url":"shop.example.com"}`},
		{"url too long rejected", `{"redeem_store_url":"https://shop.example.com/` + strings.Repeat("a", service.MaxRedeemStoreURLLen) + `"}`},
		{"unknown open mode rejected", `{"redeem_store_open_mode":"popup"}`},
		{"label over 50 runes rejected", `{"redeem_store_label":"` + strings.Repeat("购", service.MaxRedeemStoreLabelLen+1) + `"}`},
	}

	for _, tt := range invalidCases {
		t.Run(tt.name, func(t *testing.T) {
			repo := &settingHandlerRepoStub{}
			svc := service.NewSettingService(repo, &config.Config{})
			handler := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewBufferString(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")
			handler.UpdateSettings(c)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Empty(t, repo.lastUpdates)
		})
	}
}

func TestSettingsAuditTracksRedeemStoreFields(t *testing.T) {
	before := &service.SystemSettings{
		RedeemStoreURL:      "",
		RedeemStoreOpenMode: service.RedeemStoreOpenModeNewTab,
		RedeemStoreLabel:    "",
	}
	after := &service.SystemSettings{
		RedeemStoreURL:      "https://shop.example.com",
		RedeemStoreOpenMode: service.RedeemStoreOpenModeIframe,
		RedeemStoreLabel:    "购买兑换码",
	}
	changed := diffSettings(before, after, nil, nil, UpdateSettingsRequest{})
	require.Contains(t, changed, "redeem_store_url")
	require.Contains(t, changed, "redeem_store_open_mode")
	require.Contains(t, changed, "redeem_store_label")
}
