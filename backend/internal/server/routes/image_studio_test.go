package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type studioKeysRepo struct {
	service.APIKeyRepository
	key        *service.APIKey
	listedUser int64
	page       pagination.PaginationParams
}

func (r *studioKeysRepo) GetByID(context.Context, int64) (*service.APIKey, error) { return r.key, nil }
func (r *studioKeysRepo) UpdateLastUsed(context.Context, int64, time.Time) error  { return nil }
func (r *studioKeysRepo) ListByUserID(_ context.Context, id int64, p pagination.PaginationParams, _ service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	r.listedUser = id
	r.page = p
	return []service.APIKey{*r.key}, &pagination.PaginationResult{Total: 1}, nil
}

type studioJobsRepo struct {
	service.BatchImageRepository
	owner []int64
}

func (r *studioJobsRepo) GetBatchImageJobByBatchIDForOwner(_ context.Context, user, key int64, id string) (*service.BatchImageJob, error) {
	r.owner = []int64{user, key}
	return &service.BatchImageJob{BatchID: id, Status: service.BatchImageJobStatusCompleted}, nil
}

func (r *studioJobsRepo) ListBatchImageGalleryForOwner(_ context.Context, user, key int64, _ service.BatchImageGalleryQuery) ([]service.BatchImageGalleryRecord, error) {
	r.owner = []int64{user, key}
	return []service.BatchImageGalleryRecord{}, nil
}

type studioAccountRepo struct {
	service.BatchImageAccountSelectionRepository
}

type studioGroupsRepo struct {
	calls int
	id    int64
}

func (r *studioGroupsRepo) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	r.calls++
	r.id = id
	return &service.Group{ID: id, AllowBatchImageGeneration: false}, nil
}

func TestImageStudioRoutesReuseBatchServiceAndSafeDirectory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{BatchImage: config.BatchImageConfig{Enabled: true}}
	cfg.Gateway.MaxBodySize = 1024 * 1024
	group := &service.Group{ID: 9, Status: service.StatusActive, Hydrated: true}
	repo := &studioKeysRepo{key: &service.APIKey{ID: 3, UserID: 1, Key: "SECRET_CANARY", Name: "studio", Status: service.StatusActive, GroupID: &group.ID, Group: group, User: &service.User{ID: 1, Status: service.StatusActive, Balance: 10}}}
	keys := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	jobs := &studioJobsRepo{}
	groups := &studioGroupsRepo{}
	batch := handler.NewBatchImageHandler(&service.BatchImagePublicService{Config: cfg, Repo: jobs, GroupRepo: groups, AccountRepo: &studioAccountRepo{}}, nil, nil)
	h := &handler.Handlers{APIKey: handler.NewAPIKeyHandler(keys), BatchImage: batch}
	r := gin.New()
	authenticated := r.Group("/api/v1")
	authenticated.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer test-session" {
			c.AbortWithStatus(401)
			return
		}
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
	})
	registerImageStudioRoutes(authenticated, h, keys, nil, cfg, nil)
	request := func(method, path, body string, login bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1/image-studio"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key-ID", "3")
		if login {
			req.Header.Set("Authorization", "Bearer test-session")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	t.Run("unauthenticated", func(t *testing.T) { w := request("GET", "/keys", "", false); require.Equal(t, 401, w.Code) })
	t.Run("directory", func(t *testing.T) {
		w := request("GET", "/keys?page=2&page_size=5", "", true)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.EqualValues(t, 1, repo.listedUser)
		require.Equal(t, 2, repo.page.Page)
		require.Equal(t, 5, repo.page.PageSize)
		require.Contains(t, w.Body.String(), `"name":"studio"`)
		require.NotContains(t, w.Body.String(), "SECRET_CANARY")
		require.NotContains(t, w.Body.String(), `"key":`)
	})
	t.Run("owned read", func(t *testing.T) {
		w := request("GET", "/batches/batch-test", "", true)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "batch-test")
		require.Equal(t, []int64{1, 3}, jobs.owner)
	})
	t.Run("gallery uses session and owned key", func(t *testing.T) {
		require.Equal(t, http.StatusUnauthorized, request("GET", "/batches/gallery", "", false).Code)
		w := request("GET", "/batches/gallery?limit=24", "", true)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.JSONEq(t, `{"data":[]}`, w.Body.String())
		require.Equal(t, []int64{1, 3}, jobs.owner)
	})
	t.Run("thumbnail requires session", func(t *testing.T) {
		require.Equal(t, http.StatusUnauthorized, request("GET", "/batches/batch-test/items/one/thumbnail", "", false).Code)
	})
	t.Run("submit reaches original service group gate", func(t *testing.T) {
		w := request("POST", "/batches", `{"model":"gemini-2.5-flash-image","items":[{"custom_id":"one","prompt":"cat"}]}`, true)
		require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "BATCH_IMAGE_GROUP_DISABLED")
		require.Equal(t, 1, groups.calls)
		require.EqualValues(t, 9, groups.id)
	})
	t.Run("GET retains batch billing admission", func(t *testing.T) {
		repo.key.User.Balance = 0
		w := request("GET", "/batches/batch-test", "", true)
		require.Equal(t, 403, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "INSUFFICIENT_BALANCE")
	})
}
