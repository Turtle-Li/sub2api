//go:build unit

package admin

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type workBuddyPolicyAdmin struct {
	service.AdminService
	account *service.Account
	writes  []map[string]any
}

func (s *workBuddyPolicyAdmin) GetAccount(context.Context, int64) (*service.Account, error) {
	return s.account, nil
}

func (s *workBuddyPolicyAdmin) UpdateAccountExtra(_ context.Context, _ int64, updates map[string]any) error {
	s.writes = append(s.writes, updates)
	for key, value := range updates {
		s.account.Extra[key] = value
	}
	return nil
}

func TestWorkBuddyImagePolicyStrictNarrowUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"native_retry_accepted":true}`, 200},
		{`{"native_retry_accepted":false}`, 200},
		{`{}`, 400}, {`null`, 400}, {`{"native_retry_accepted":null}`, 400},
		{`{"native_retry_accepted":"true"}`, 400}, {`{"native_retry_accepted":1}`, 400},
		{`{"native_retry_accepted":true,"single_attempt_verified":true}`, 400},
		{`{"native_retry_accepted":true} {}`, 400},
	} {
		t.Run(tc.body, func(t *testing.T) {
			api := &workBuddyPolicyAdmin{account: &service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Credentials: map[string]any{"model_mapping": map[string]any{"cn:hunyuan-image-alpha": "cn:hunyuan-image-alpha"}},
				Extra:       map[string]any{"workbuddy_image_single_attempt_verified": true, "unrelated": "kept"}}}
			h := &AccountHandler{adminService: api}
			router := gin.New()
			router.PATCH("/:id", h.UpdateWorkBuddyImagePolicy)
			req := httptest.NewRequest("PATCH", "/1", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, tc.code, w.Code, w.Body.String())
			require.Equal(t, true, api.account.Extra["workbuddy_image_single_attempt_verified"])
			require.Equal(t, "kept", api.account.Extra["unrelated"])
			if tc.code == 200 {
				require.Len(t, api.writes, 1)
				require.Len(t, api.writes[0], 1)
				require.Contains(t, api.writes[0], "workbuddy_image_native_retry_accepted")
			} else {
				require.Empty(t, api.writes)
			}
		})
	}
}

func TestWorkBuddyImagePolicyRejectsWrongAccountsAndScopedKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, platform, accountType, target string
		scoped                              bool
	}{
		{"text_workbuddy", "workbuddy", service.AccountTypeAPIKey, "cn:hunyuan-image-alpha", false},
		{"oauth", service.PlatformOpenAI, service.AccountTypeOAuth, "cn:hunyuan-image-alpha", false},
		{"alias", service.PlatformOpenAI, service.AccountTypeAPIKey, "cn:hunyuan-image-alpha-edit", false},
		{"scoped", service.PlatformOpenAI, service.AccountTypeAPIKey, "cn:hunyuan-image-alpha", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &workBuddyPolicyAdmin{account: &service.Account{Platform: tc.platform, Type: tc.accountType,
				Credentials: map[string]any{"model_mapping": map[string]any{"cn:hunyuan-image-alpha": tc.target}}}}
			h := &AccountHandler{adminService: api}
			router := gin.New()
			router.PATCH("/:id", h.UpdateWorkBuddyImagePolicy)
			req := httptest.NewRequest("PATCH", "/1", strings.NewReader(`{"native_retry_accepted":true}`))
			want := 400
			if tc.scoped {
				req = req.WithContext(adminaccess.WithPrincipal(req.Context(), &adminaccess.Principal{}))
				want = 403
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, want, w.Code)
			require.Empty(t, api.writes)
		})
	}
}
