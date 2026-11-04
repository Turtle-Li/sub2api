package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShopAdminGuardRejectsKeysAndDisablesCaching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, key, method string
		status            int
	}{
		{"header key", "synthetic-admin-key", "", http.StatusForbidden},
		{"resolved key", "", service.AuditAuthMethodAdminAPIKey, http.StatusForbidden},
		{"human auth continues to admin validation", "", "jwt", http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set("auth_method", tc.method); c.Next() })
			called := false
			router.POST("/shop-secret", shopNoStore, shopAdminJWT, func(c *gin.Context) {
				called = true
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/shop-secret", nil)
			if tc.key != "" {
				req.Header.Set("x-api-key", tc.key)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, tc.status, rec.Code)
			require.Equal(t, tc.status == http.StatusNoContent, called)
			require.Equal(t, "no-store, private", rec.Header().Get("Cache-Control"))
			require.Equal(t, "no-cache", rec.Header().Get("Pragma"))
			require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
			require.NotContains(t, rec.Body.String(), "synthetic-admin-key")
		})
	}
}
