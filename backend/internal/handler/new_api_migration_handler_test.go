package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type handlerTestSettingRepo struct {
	data map[string]string
}

func (r *handlerTestSettingRepo) Get(_ context.Context, key string) (*service.Setting, error) {
	v, ok := r.data[key]
	if !ok {
		return nil, service.ErrSettingNotFound
	}
	return &service.Setting{Key: key, Value: v}, nil
}

func (r *handlerTestSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	v, ok := r.data[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return v, nil
}

func (r *handlerTestSettingRepo) Set(_ context.Context, key, value string) error {
	r.data[key] = value
	return nil
}

func (r *handlerTestSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := r.data[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (r *handlerTestSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	for k, v := range settings {
		r.data[k] = v
	}
	return nil
}

func (r *handlerTestSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	return r.data, nil
}

func (r *handlerTestSettingRepo) Delete(_ context.Context, key string) error {
	delete(r.data, key)
	return nil
}

type handlerTestMigrationRepo struct {
	service.NewAPIMigrationRepository
}

func (handlerTestMigrationRepo) ListByUser(_ context.Context, _ int64, params pagination.PaginationParams) ([]service.NewAPIMigrationRecord, *pagination.PaginationResult, error) {
	return []service.NewAPIMigrationRecord{
		{ID: 1, Direction: service.NewAPIMigrationDirectionFrom, Status: service.NewAPIMigrationStatusCompleted, ClientIP: "203.0.113.10"},
	}, &pagination.PaginationResult{Total: 1, Page: params.Page, PageSize: params.PageSize, Pages: 1}, nil
}

func TestNewAPIMigrationHandler_GetInfoAndListRecordsSanitizesClientIP(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := service.NewNewAPIMigrationService(
		handlerTestMigrationRepo{},
		&handlerTestSettingRepo{data: map[string]string{}},
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewNewAPIMigrationHandler(svc)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7, Concurrency: 5})
		c.Next()
	})
	router.GET("/api/v1/new-api-migration/info", h.GetInfo)
	router.POST("/api/v1/new-api-migration/preview", h.Preview)
	router.GET("/api/v1/new-api-migration/records", h.ListRecords)

	// GET /info
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/new-api-migration/info", nil)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var infoResp struct {
		Code int                            `json:"code"`
		Data service.NewAPIMigrationUserInfo `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &infoResp))
	require.Equal(t, 0, infoResp.Code)
	require.False(t, infoResp.Data.Enabled)
	require.False(t, infoResp.Data.MigrateToEnabled)

	// POST /preview when disabled -> 403
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/new-api-migration/preview", bytes.NewBufferString(`{"access_token":"nap_123"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)

	// GET /records strips ClientIP
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/new-api-migration/records?page=1&page_size=20", nil)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), "203.0.113.10")
}
