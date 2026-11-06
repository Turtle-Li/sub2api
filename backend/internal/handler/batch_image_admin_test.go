//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imageAdminGroupRepository struct{}

func (*imageAdminGroupRepository) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	if id != 7 {
		return nil, service.ErrGroupNotFound
	}
	return &service.Group{ID: id, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true}, nil
}

type imageAdminAccountRepository struct {
	service.BatchImageAccountSelectionRepository
}

func (*imageAdminAccountRepository) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]service.Account, error) {
	return []service.Account{}, nil
}

func TestImageAdminModelsHandlerEnvelopeAndAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewBatchImageHandler(&service.BatchImagePublicService{GroupRepo: &imageAdminGroupRepository{}, AccountRepo: &imageAdminAccountRepository{}}, nil, nil)
	router := gin.New()
	router.GET("/:id", h.AdminModels)
	for _, tc := range []struct {
		path   string
		code   int
		scoped bool
	}{
		{"/7", 200, false}, {"/8", 404, false}, {"/0", 400, false}, {"/invalid", 400, false}, {"/7", 403, true},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		if tc.scoped {
			req = req.WithContext(adminaccess.WithPrincipal(req.Context(), &adminaccess.Principal{}))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, tc.code, w.Code, w.Body.String())
		if tc.code == 200 {
			var body struct {
				Code int                           `json:"code"`
				Data service.BatchImageAdminModels `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Equal(t, 0, body.Code)
			require.Equal(t, "batch_image_disabled", *body.Data.UnavailableReason)
			require.Empty(t, body.Data.Models)
			require.NotNil(t, body.Data.Models)
			require.NotEmpty(t, body.Data.Candidates)
		}
	}
}
