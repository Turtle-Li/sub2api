package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type walletRouteSettings struct {
	service.SettingRepository
	value string
}

func (r *walletRouteSettings) GetValue(context.Context, string) (string, error) { return r.value, nil }

type walletRouteGroups struct{ group *service.Group }

func (r walletRouteGroups) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	if id != r.group.ID {
		return nil, service.ErrGroupNotFound
	}
	return r.group, nil
}

type walletRouteKeys struct{ service.APIKeyRepository }

func (walletRouteKeys) ListImageStudioKeys(context.Context, int64, pagination.PaginationParams) ([]service.ImageStudioKey, *pagination.PaginationResult, error) {
	return []service.ImageStudioKey{}, &pagination.PaginationResult{}, nil
}

type walletRoutePricing struct{}

func (walletRoutePricing) BatchImageUnitPrice(context.Context, *service.BatchImageJob) (float64, error) {
	return 0, service.ErrBatchImageSettlementPricingMissing
}

type walletRouteAccounts struct {
	service.BatchImageAccountSelectionRepository
}

func (walletRouteAccounts) GetByIDs(context.Context, []int64) ([]*service.Account, error) {
	return nil, nil
}

func TestImageStudioWalletRoutesRequireLoginAndGroupButNotAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{BatchImage: config.BatchImageConfig{Enabled: true}}
	cfg.Gateway.MaxBodySize = 1024 * 1024
	repo := &walletRouteSettings{value: `{"group_id":45,"account_ids":[249]}`}
	settings := service.NewSettingService(repo, cfg)
	group := &service.Group{ID: 45, Status: service.StatusActive, Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeStandard, AllowImageGeneration: true, IsExclusive: true}
	keys := service.NewAPIKeyService(walletRouteKeys{}, nil, nil, nil, nil, nil, cfg)
	jobs := &studioJobsRepo{}
	batch := handler.NewBatchImageHandler(&service.BatchImagePublicService{
		Config: cfg, Repo: jobs, GroupRepo: walletRouteGroups{group}, AccountRepo: walletRouteAccounts{},
		Pricing: walletRoutePricing{}, ProviderRegistry: service.NewBatchImageProviderRegistry(),
	}, nil, nil)
	router := gin.New()
	authenticated := router.Group("/api/v1", func(c *gin.Context) {
		subject := middleware.AuthSubject{UserID: 7}
		switch c.GetHeader("Authorization") {
		case "Bearer authorized":
			subject.AllowedGroups = []int64{45}
		case "Bearer unassigned":
		default:
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set(string(middleware.ContextKeyUser), subject)
	})
	registerImageStudioRoutes(authenticated, &handler.Handlers{APIKey: handler.NewAPIKeyHandler(keys), BatchImage: batch}, keys, nil, cfg, settings)
	get := func(path, auth string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/image-studio"+path, nil)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		router.ServeHTTP(w, req)
		return w
	}
	keysResponse := get("/keys", "authorized")
	require.Equal(t, http.StatusOK, keysResponse.Code)
	require.Equal(t, "on", keysResponse.Header().Get("X-Image-Studio-Wallet"))
	require.Contains(t, keysResponse.Body.String(), `"items":[]`)
	require.Equal(t, "off", get("/keys", "unassigned").Header().Get("X-Image-Studio-Wallet"))
	require.Equal(t, http.StatusUnauthorized, get("/wallet/batches/models", "").Code)
	require.Equal(t, http.StatusForbidden, get("/wallet/batches/models", "unassigned").Code)
	require.Equal(t, http.StatusOK, get("/wallet/batches/models", "authorized").Code)
	require.Equal(t, http.StatusBadRequest, get("/batches/models", "authorized").Code, "legacy route still requires a key")
	repo.value = `{"group_id":0,"account_ids":[]}`
	require.Equal(t, http.StatusForbidden, get("/wallet/batches/models", "authorized").Code)
	require.Equal(t, http.StatusOK, get("/wallet/batches/gallery", "authorized").Code)
	require.Equal(t, []int64{7, service.ImageStudioWalletOwnerSlot}, jobs.owner)
}
