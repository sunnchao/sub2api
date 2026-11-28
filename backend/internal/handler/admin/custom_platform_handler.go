package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// CustomPlatformHandler 处理自定义渠道平台的管理端接口。
type CustomPlatformHandler struct {
	service        *service.CustomPlatformService
	pricingService *service.PricingService
}

// NewCustomPlatformHandler 创建自定义渠道平台处理器。
func NewCustomPlatformHandler(svc *service.CustomPlatformService, pricingService *service.PricingService) *CustomPlatformHandler {
	return &CustomPlatformHandler{
		service:        svc,
		pricingService: pricingService,
	}
}

// customPlatformRequest 是创建/更新请求体。
type customPlatformRequest struct {
	ID                 string                        `json:"id"`
	DisplayName        string                        `json:"display_name" binding:"required"`
	Enabled            *bool                         `json:"enabled"`
	SortOrder          int                           `json:"sort_order"`
	Icon               domain.PlatformIconSpec       `json:"icon"`
	ColorPreset        string                        `json:"color_preset"`
	LiteLLMProvider    string                        `json:"litellm_provider"`
	DefaultTestModel   string                        `json:"default_test_model"`
	SupportsEmbeddings bool                          `json:"supports_embeddings"`
	SupportsImages     bool                          `json:"supports_images"`
	MultiProtocol      service.CustomPlatformProfile `json:"multi_protocol"`
}

func (r *customPlatformRequest) toPlatform() *service.CustomPlatform {
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return &service.CustomPlatform{
		ID:                 strings.TrimSpace(r.ID),
		DisplayName:        r.DisplayName,
		Enabled:            enabled,
		SortOrder:          r.SortOrder,
		Icon:               r.Icon,
		ColorPreset:        r.ColorPreset,
		LiteLLMProvider:    r.LiteLLMProvider,
		DefaultTestModel:   r.DefaultTestModel,
		SupportsEmbeddings: r.SupportsEmbeddings,
		SupportsImages:     r.SupportsImages,
		Profile:            r.MultiProtocol,
	}
}

// List 返回内置（只读）与自定义平台清单。
// GET /api/v1/admin/custom-platforms
func (h *CustomPlatformHandler) List(c *gin.Context) {
	custom, err := h.service.List(c.Request.Context())
	if err != nil {
		writeCustomPlatformError(c, err)
		return
	}
	builtin := make([]domain.PlatformSpec, 0)
	for _, entry := range service.BuildPlatformCatalog().Platforms {
		if entry.Custom {
			continue
		}
		builtin = append(builtin, entry.PlatformSpec)
	}
	if custom == nil {
		custom = []*service.CustomPlatform{}
	}
	response.Success(c, gin.H{
		"builtin_platforms": builtin,
		"custom_platforms":  custom,
	})
}

// Create 创建自定义平台。
// POST /api/v1/admin/custom-platforms
func (h *CustomPlatformHandler) Create(c *gin.Context) {
	var req customPlatformRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	created, err := h.service.Create(c.Request.Context(), req.toPlatform())
	if err != nil {
		writeCustomPlatformError(c, err)
		return
	}
	response.Created(c, created)
}

// Update 更新自定义平台（ID 不可修改）。
// PUT /api/v1/admin/custom-platforms/:id
func (h *CustomPlatformHandler) Update(c *gin.Context) {
	var req customPlatformRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	updated, err := h.service.Update(c.Request.Context(), c.Param("id"), req.toPlatform())
	if err != nil {
		writeCustomPlatformError(c, err)
		return
	}
	response.Success(c, updated)
}

// Delete 删除自定义平台；仍有引用时返回 409 与引用计数。
// DELETE /api/v1/admin/custom-platforms/:id
func (h *CustomPlatformHandler) Delete(c *gin.Context) {
	err := h.service.Delete(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeCustomPlatformError(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ListLiteLLMProviders 返回定价目录中可用的 LiteLLM provider 列表（合并已登记平台，去重排序）。
// GET /api/v1/admin/custom-platforms/litellm-providers
func (h *CustomPlatformHandler) ListLiteLLMProviders(c *gin.Context) {
	seen := map[string]struct{}{}
	if h.pricingService != nil {
		for _, provider := range h.pricingService.ListProviders() {
			if p := strings.TrimSpace(provider); p != "" {
				seen[p] = struct{}{}
			}
		}
	}
	for _, entry := range service.BuildPlatformCatalog().Platforms {
		if provider := strings.TrimSpace(entry.LiteLLMProvider); provider != "" {
			seen[provider] = struct{}{}
		}
	}
	providers := make([]string, 0, len(seen))
	for provider := range seen {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	response.Success(c, gin.H{"providers": providers})
}

func writeCustomPlatformError(c *gin.Context, err error) {
	var validationErr *service.CustomPlatformValidationError
	if errors.As(err, &validationErr) {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    http.StatusBadRequest,
			"message": validationErr.Error(),
			"reason":  "INVALID_PLATFORM",
			"data":    gin.H{"field": validationErr.Field},
		})
		return
	}
	var inUse *service.CustomPlatformInUseError
	if errors.As(err, &inUse) {
		metadata := map[string]string{}
		if raw, marshalErr := json.Marshal(inUse.Usage); marshalErr == nil {
			metadata["usage_counts"] = string(raw)
		}
		c.JSON(http.StatusConflict, gin.H{
			"code":     http.StatusConflict,
			"message":  inUse.Error(),
			"reason":   "PLATFORM_IN_USE",
			"metadata": metadata,
			"data":     gin.H{"usage_counts": inUse.Usage},
		})
		return
	}
	if errors.Is(err, service.ErrCustomPlatformNotFound) {
		response.NotFound(c, "Custom platform not found")
		return
	}
	logger.L().Error("custom_platform.internal_error", zap.Error(err))
	response.InternalError(c, "Internal server error")
}
