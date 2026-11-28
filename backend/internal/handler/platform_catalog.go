package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// PlatformCatalogHandler 提供前端首屏使用的公开平台目录。
type PlatformCatalogHandler struct{}

// NewPlatformCatalogHandler 创建公开平台目录处理器。
func NewPlatformCatalogHandler() *PlatformCatalogHandler {
	return &PlatformCatalogHandler{}
}

// Get 返回内置平台 + 自定义平台的完整目录。
// GET /api/v1/platform-catalog
func (h *PlatformCatalogHandler) Get(c *gin.Context) {
	response.Success(c, service.BuildPlatformCatalog())
}
