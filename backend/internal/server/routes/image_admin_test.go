//go:build unit

package routes

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImageAdminRoutesRejectAnonymousAndAllScopedPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := service.NewSettingService(nil, nil)
	settings.SetScopedAdminKeyRepository(&scopedRouteKeys{})
	permissions, globalPermissions := []string{}, []string{}
	for _, permission := range adminaccess.PermissionCatalog() {
		if permission.Scope == "global" {
			globalPermissions = append(globalPermissions, permission.ID)
		} else {
			permissions = append(permissions, permission.ID)
		}
	}
	policy := adminaccess.Policy{GlobalPermissions: globalPermissions, Rules: []adminaccess.Rule{{
		Permissions: permissions, GroupIDs: []int64{2}, AccountTypes: []string{"apikey"}, Platforms: []string{"openai"},
	}}}
	key, _, err := settings.CreateScopedAdminKey(context.Background(), service.ScopedAdminKeyInput{Name: "image management denial", Enabled: true, Policy: policy}, 9)
	require.NoError(t, err)
	users := service.NewUserService(&scopedRouteUsers{}, nil, nil, nil)
	auth := middleware.NewAdminAuthMiddleware(nil, users, settings, nil)
	router := gin.New()
	RegisterAdminRoutes(router.Group("/api/v1"), &handler.Handlers{Admin: &handler.AdminHandlers{}}, auth,
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() }), nil, nil)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/admin/groups/2/image-models", ""},
		{"PATCH", "/api/v1/admin/accounts/2/workbuddy-image-policy", `{"native_retry_accepted":true}`},
	} {
		for _, credential := range []string{"", key} {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if credential != "" {
				req.Header.Set("x-api-key", credential)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			want := 401
			if credential != "" {
				want = 403
			}
			require.Equal(t, want, w.Code, tc.path+": "+w.Body.String())
		}
	}
}
