//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type catalogueHTTPGroups struct{ group *service.Group }
type catalogueHTTPRepo struct{ service.BatchImageRepository }

func (g *catalogueHTTPGroups) GetByIDLite(context.Context, int64) (*service.Group, error) {
	return g.group, nil
}

type catalogueHTTPAccounts struct {
	service.BatchImageAccountSelectionRepository
}

func (*catalogueHTTPAccounts) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]service.Account, error) {
	return []service.Account{{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2", "gpt-image-2.5-flare": "gpt-image-2.5-flare"}}}}, nil
}

type catalogueHTTPPricing struct{}

func (*catalogueHTTPPricing) BatchImageUnitPrice(context.Context, *service.BatchImageJob) (float64, error) {
	return 1, nil
}

type catalogueHTTPProvider struct{ service.BatchImageProvider }

func (*catalogueHTTPProvider) Name() string                          { return service.BatchImageProviderOpenAI }
func (*catalogueHTTPProvider) SupportsAccount(*service.Account) bool { return true }
func (*catalogueHTTPProvider) SubmissionAvailable() bool             { return true }

func TestImageStudioCatalogueConditionalReadRechecksAdmissionAndQuotes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := &service.Group{ID: 7, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true, AllowBatchImageGeneration: true, RateMultiplier: 1, BatchImageDiscountMultiplier: 1, BatchImageHoldMultiplier: 1}
	h := NewBatchImageHandler(&service.BatchImagePublicService{
		Config:    &config.Config{BatchImage: config.BatchImageConfig{Enabled: true}},
		Repo:      &catalogueHTTPRepo{},
		GroupRepo: &catalogueHTTPGroups{group}, AccountRepo: &catalogueHTTPAccounts{}, Pricing: &catalogueHTTPPricing{},
		ProviderRegistry: service.NewBatchImageProviderRegistry(&catalogueHTTPProvider{}),
	}, nil, nil)
	router := gin.New()
	router.GET("/models", func(c *gin.Context) {
		if c.GetHeader("Authorization") == "Bearer owner" {
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 20, UserID: 1, GroupID: &group.ID, Group: group})
		}
		h.Models(c)
	})
	request := func(auth, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/models", nil)
		req.Header.Set("Authorization", auth)
		req.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	first := request("Bearer owner", "")
	require.Equal(t, 200, first.Code, first.Body.String())
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)
	require.Equal(t, "private, no-cache", first.Header().Get("Cache-Control"))
	require.Contains(t, first.Header().Values("Vary"), "Authorization, X-API-Key-ID")
	second := request("Bearer owner", "W/"+etag)
	require.Equal(t, 304, second.Code)
	require.Empty(t, second.Body.String())
	denied := request("", etag)
	require.Equal(t, 401, denied.Code)
	require.Empty(t, denied.Header().Get("ETag"))
	group.RateMultiplier = 2
	repriced := request("Bearer owner", etag)
	require.Equal(t, 200, repriced.Code)
	require.NotEqual(t, etag, repriced.Header().Get("ETag"))
	group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-image-2"}}
	filtered := request("Bearer owner", repriced.Header().Get("ETag"))
	require.Equal(t, 200, filtered.Code)
	require.NotContains(t, filtered.Body.String(), "gpt-image-2.5-flare")
	group.AllowBatchImageGeneration = false
	revoked := request("Bearer owner", filtered.Header().Get("ETag"))
	require.Equal(t, 403, revoked.Code)
	require.Empty(t, revoked.Header().Get("ETag"))
}
