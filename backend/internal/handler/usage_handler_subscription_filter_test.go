package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseUserUsageFilters_SubscriptionID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &UsageHandler{}

	t.Run("parses valid subscription_id and preserves user_id scope", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/usage?subscription_id=42&billing_type=1", nil)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 9})

		parsed, ok := h.parseUserUsageFilters(c, false)
		require.True(t, ok)
		require.NotNil(t, parsed)
		require.Equal(t, int64(9), parsed.Filters.UserID)
		require.Equal(t, int64(42), parsed.Filters.SubscriptionID)
		require.NotNil(t, parsed.Filters.BillingType)
		require.Equal(t, int8(1), *parsed.Filters.BillingType)
	})

	t.Run("rejects invalid or negative subscription_id", func(t *testing.T) {
		for _, query := range []string{"subscription_id=abc", "subscription_id=-5"} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/usage?"+query, nil)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 9})

			parsed, ok := h.parseUserUsageFilters(c, false)
			require.False(t, ok)
			require.Nil(t, parsed)
			require.Equal(t, http.StatusBadRequest, rec.Code)
		}
	})
}
