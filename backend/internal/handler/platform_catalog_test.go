package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPlatformCatalogHandlerIncludesCustomPlatforms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(domain.ResetDynamicPlatforms)
	require.NoError(t, domain.SetDynamicPlatforms([]domain.PlatformSpec{{
		ID:                 "siliconflow",
		DisplayName:        "SiliconFlow",
		Gateway:            domain.PlatformGatewayOpenAI,
		Custom:             true,
		SupportsEmbeddings: true,
		Icon:               &domain.PlatformIconSpec{Type: domain.PlatformIconTypeLetter, Value: "S"},
	}}))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/platform-catalog", nil)
	NewPlatformCatalogHandler().Get(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Code int `json:"code"`
		Data struct {
			Platforms []domain.PlatformSpec `json:"platforms"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, 0, payload.Code)

	var custom *domain.PlatformSpec
	for i := range payload.Data.Platforms {
		if payload.Data.Platforms[i].ID == "siliconflow" {
			custom = &payload.Data.Platforms[i]
		}
	}
	require.NotNil(t, custom)
	require.True(t, custom.Custom)
	require.True(t, custom.SupportsEmbeddings)
	require.NotNil(t, custom.Icon)
	require.Equal(t, domain.PlatformIconTypeLetter, custom.Icon.Type)
}
