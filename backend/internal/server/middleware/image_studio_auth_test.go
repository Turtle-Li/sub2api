package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type studioTestKeys struct {
	key      *service.APIKey
	touched  int
	lookedUp int
}

func (s *studioTestKeys) GetByID(_ context.Context, id int64) (*service.APIKey, error) {
	s.lookedUp++
	if id != s.key.ID {
		return nil, service.ErrAPIKeyNotFound
	}
	return s.key, nil
}
func (s *studioTestKeys) TouchLastUsed(context.Context, int64) error { s.touched++; return nil }

func TestImageStudioKeyAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		mutate func(*service.APIKey)
		login  bool
		id     string
		model  string
		status int
	}{
		{name: "owned key", login: true, id: "3", model: "allowed", status: 200},
		{name: "no session", id: "3", model: "allowed", status: 401},
		{name: "missing selection", login: true, model: "allowed", status: 400},
		{name: "unknown key", login: true, id: "4", model: "allowed", status: 404},
		{name: "other owner", login: true, id: "3", model: "allowed", status: 404, mutate: func(k *service.APIKey) { k.UserID = 2 }},
		{name: "disabled", login: true, id: "3", model: "allowed", status: 401, mutate: func(k *service.APIKey) { k.Status = service.StatusAPIKeyDisabled }},
		{name: "inactive user", login: true, id: "3", model: "allowed", status: 401, mutate: func(k *service.APIKey) { k.User.Status = "disabled" }},
		{name: "disabled group", login: true, id: "3", model: "allowed", status: 403, mutate: func(k *service.APIKey) { k.Group.Status = "disabled" }},
		{name: "exclusive group denied", login: true, id: "3", model: "allowed", status: 403, mutate: func(k *service.APIKey) { k.Group.IsExclusive = true }},
		{name: "exclusive group allowed", login: true, id: "3", model: "allowed", status: 200, mutate: func(k *service.APIKey) { k.Group.IsExclusive = true; k.User.AllowedGroups = []int64{9} }},
		{name: "model denied", login: true, id: "3", model: "denied", status: 404},
		{name: "quota exhausted", login: true, id: "3", model: "allowed", status: 429, mutate: func(k *service.APIKey) { k.Status = service.StatusAPIKeyQuotaExhausted }},
		{name: "expired", login: true, id: "3", model: "allowed", status: 403, mutate: func(k *service.APIKey) { k.Status = service.StatusAPIKeyExpired }},
		{name: "empty balance", login: true, id: "3", model: "allowed", status: 403, mutate: func(k *service.APIKey) { k.User.Balance = 0 }},
		{name: "IP denied", login: true, id: "3", model: "allowed", status: 403, mutate: func(k *service.APIKey) {
			k.IPWhitelist = []string{"127.0.0.1"}
			k.CompiledIPWhitelist = ip.CompileIPRules(k.IPWhitelist)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := &service.Group{ID: 9, Status: service.StatusActive, Hydrated: true, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"allowed"}}}
			key := &service.APIKey{ID: 3, UserID: 1, Key: "SECRET_CANARY", Status: service.StatusActive, GroupID: &group.ID, Group: group, User: &service.User{ID: 1, Status: service.StatusActive, Balance: 10}}
			if tc.mutate != nil {
				tc.mutate(key)
			}
			keys := &studioTestKeys{key: key}
			router := gin.New()
			if tc.login {
				router.Use(func(c *gin.Context) { c.Set(string(ContextKeyUser), AuthSubject{UserID: 1}); c.Next() })
			}
			router.Use(ImageStudioKeyAuth(keys, nil, &config.Config{}), GroupModelAllowlist())
			called := false
			router.POST("/api/v1/image-studio/batches", func(c *gin.Context) {
				called = true
				got, ok := GetAPIKeyFromContext(c)
				require.True(t, ok)
				require.Same(t, key, got)
				require.Equal(t, "Bearer JWT_CANARY", c.GetHeader("Authorization"))
				c.Status(200)
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/image-studio/batches", strings.NewReader(`{"model":"`+tc.model+`"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key-ID", tc.id)
			req.Header.Set("Authorization", "Bearer JWT_CANARY")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Equal(t, tc.status == 200, called)
			require.NotContains(t, w.Body.String(), "SECRET_CANARY")
			if !tc.login {
				require.Zero(t, keys.lookedUp)
			}
		})
	}
}
