package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShopSecretBodiesNeverEnterAuditStorage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditCaptureRepository{}
	audit := service.NewAuditLogService(repo, nil)
	audit.Start()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 77})
		c.Set(string(ContextKeyUserRole), "admin")
		c.Next()
	}, gin.HandlerFunc(NewAuditLogMiddleware(audit)))
	paths := []string{"/api/v1/shop/orders/:id/input", "/api/v1/shop/orders/:id/input/check", "/api/v1/admin/shop/orders/:id/input/reveal", "/api/v1/admin/shop/orders/:id/action"}
	for _, path := range paths {
		router.POST(path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
		req := httptest.NewRequest(http.MethodPost, strings.ReplaceAll(path, ":id", "1"), strings.NewReader(`{"content":"synthetic-session-secret","delivery_content":"synthetic-delivery-secret"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNoContent, rec.Code)
	}
	audit.Stop()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.logs, len(paths))
	for _, entry := range repo.logs {
		require.Equal(t, "<credential-bearing body omitted>", entry.RequestBody)
		require.NotContains(t, entry.RequestBody, "synthetic-")
	}
}
