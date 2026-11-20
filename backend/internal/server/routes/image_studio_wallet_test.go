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
	account *service.Account
}

func (r walletRouteAccounts) GetByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	if len(ids) == 1 && ids[0] == r.account.ID {
		return []*service.Account{r.account}, nil
	}
	return nil, nil
}

type walletRouteProvider struct{ service.BatchImageProvider }

func (walletRouteProvider) Name() string                          { return service.BatchImageProviderOpenAI }
func (walletRouteProvider) SupportsAccount(*service.Account) bool { return true }

type walletRouteJobs struct{ studioJobsRepo }

func (r *walletRouteJobs) GetBatchImageJobByBatchIDForOwner(ctx context.Context, user, key int64, id string) (*service.BatchImageJob, error) {
	if user != 7 || key != service.ImageStudioWalletOwnerSlot {
		return nil, service.ErrBatchImageJobNotFound
	}
	return r.studioJobsRepo.GetBatchImageJobByBatchIDForOwner(ctx, user, key, id)
}

func (*walletRouteJobs) GetBatchImageJobByIdempotencyKey(context.Context, int64, int64, string) (*service.BatchImageJob, error) {
	return nil, service.ErrBatchImageJobNotFound
}

func (*walletRouteJobs) CreateBatchImageJob(_ context.Context, p service.CreateBatchImageJobParams) (*service.BatchImageJob, error) {
	return &service.BatchImageJob{BatchID: p.BatchID, UserID: p.UserID, APIKeyID: p.APIKeyID,
		StudioGroupID: p.StudioGroupID, HoldAmount: p.HoldAmount}, nil
}

func (*walletRouteJobs) RecordBatchImageJobSubmitFailure(context.Context, string, string, string, bool) error {
	return nil
}

func (*walletRouteJobs) MarkBatchImageJobUserDeleted(context.Context, int64, int64, string, time.Time) error {
	return nil
}

type walletRouteBilling struct {
	service.UsageBillingRepository
	reservations []service.BatchImageBalanceHoldCommand
}

func (r *walletRouteBilling) ReserveBatchImageBalance(_ context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	r.reservations = append(r.reservations, *cmd)
	return nil, service.ErrBatchImageInsufficientBalance
}

func TestImageStudioWalletRoutesRequireLoginButNotAPIKeyOrGroupMembership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{BatchImage: config.BatchImageConfig{Enabled: true}}
	cfg.Gateway.MaxBodySize = 1024 * 1024
	repo := &walletRouteSettings{value: `{"group_id":45,"account_ids":[249]}`}
	settings := service.NewSettingService(repo, cfg)
	const model = "gpt-image-2.6-flare"
	price := 0.20
	group := &service.Group{ID: 45, Status: service.StatusActive, Platform: service.PlatformOpenAI,
		SubscriptionType: service.SubscriptionTypeStandard, AllowImageGeneration: true, IsExclusive: true,
		RateMultiplier: 1, BatchImageHoldMultiplier: 1, ModelPricing: []service.ChannelModelPricing{{
			Models: []string{model}, BillingMode: service.BillingModeImage, Currency: service.PricingCurrencyCNY,
			Intervals: []service.PricingInterval{{TierLabel: "1K", PerRequestPrice: &price}},
		}},
	}
	account := &service.Account{ID: 249, Status: service.StatusActive, Schedulable: true, Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{model: model}},
	}
	keys := service.NewAPIKeyService(walletRouteKeys{}, nil, nil, nil, nil, nil, cfg)
	jobs := &walletRouteJobs{}
	billing := &walletRouteBilling{}
	batch := handler.NewBatchImageHandler(&service.BatchImagePublicService{
		Config: cfg, Repo: jobs, GroupRepo: walletRouteGroups{group}, AccountRepo: walletRouteAccounts{account: account},
		BillingRepo: billing, Pricing: walletRoutePricing{}, ProviderRegistry: service.NewBatchImageProviderRegistry(walletRouteProvider{}),
	}, nil, nil)
	router := gin.New()
	authenticated := router.Group("/api/v1", func(c *gin.Context) {
		subject := middleware.AuthSubject{UserID: 7, RestrictPublicGroups: true}
		switch c.GetHeader("Authorization") {
		case "Bearer unassigned":
		case "Bearer other-user":
			subject.UserID = 8
		default:
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set(string(middleware.ContextKeyUser), subject)
	})
	registerImageStudioRoutes(authenticated, &handler.Handlers{APIKey: handler.NewAPIKeyHandler(keys), BatchImage: batch}, keys, nil, cfg, settings)
	request := func(method, path, auth, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/v1/image-studio"+path, strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "one-keyless-image")
		}
		router.ServeHTTP(w, req)
		return w
	}
	get := func(path, auth string) *httptest.ResponseRecorder { return request(http.MethodGet, path, auth, "") }
	const payload = `{"model":"gpt-image-2.6-flare","image_size":"1K","items":[{"custom_id":"one","prompt":"a pear"}]}`
	t.Run("keyless catalogue", func(t *testing.T) {
		directory := get("/keys", "unassigned")
		require.Equal(t, http.StatusOK, directory.Code)
		require.Equal(t, "on", directory.Header().Get("X-Image-Studio-Wallet"))
		require.Contains(t, directory.Body.String(), `"items":[]`)
		models := get("/wallet/batches/models", "unassigned")
		require.Equal(t, http.StatusOK, models.Code, models.Body.String())
		require.Contains(t, models.Body.String(), model)
	})
	t.Run("keyless submit reaches the user's balance before the upstream", func(t *testing.T) {
		w := request(http.MethodPost, "/wallet/batches", "unassigned", payload)
		require.Equal(t, http.StatusPaymentRequired, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "BATCH_IMAGE_INSUFFICIENT_BALANCE")
		require.Len(t, billing.reservations, 1)
		require.EqualValues(t, 7, billing.reservations[0].UserID)
		require.Equal(t, service.ImageStudioWalletOwnerSlot, billing.reservations[0].APIKeyID)
		require.Equal(t, price, billing.reservations[0].HoldAmount)
	})
	t.Run("login and legacy key boundaries", func(t *testing.T) {
		require.Equal(t, http.StatusUnauthorized, get("/wallet/batches/models", "").Code)
		require.Equal(t, http.StatusUnauthorized, request(http.MethodPost, "/wallet/batches", "", payload).Code)
		require.Equal(t, http.StatusBadRequest, get("/batches/models", "unassigned").Code, "legacy route still requires a key")
		require.Equal(t, http.StatusNotFound, get("/wallet/batches/batch-owned", "other-user").Code)
	})
	t.Run("missing configuration group fails closed", func(t *testing.T) {
		repo.value = `{"group_id":46,"account_ids":[249]}`
		require.Equal(t, "off", get("/keys", "unassigned").Header().Get("X-Image-Studio-Wallet"))
		require.Equal(t, http.StatusForbidden, get("/wallet/batches/models", "unassigned").Code)
		require.Equal(t, http.StatusForbidden, request(http.MethodPost, "/wallet/batches", "unassigned", payload).Code)
	})
	t.Run("pause blocks new work but leaves history", func(t *testing.T) {
		repo.value = `{"group_id":0,"account_ids":[]}`
		require.Equal(t, "off", get("/keys", "unassigned").Header().Get("X-Image-Studio-Wallet"))
		require.Equal(t, http.StatusForbidden, get("/wallet/batches/models", "unassigned").Code)
		require.Equal(t, http.StatusForbidden, request(http.MethodPost, "/wallet/batches", "unassigned", payload).Code)
		require.Equal(t, http.StatusOK, get("/wallet/batches/gallery", "unassigned").Code)
		require.Equal(t, http.StatusOK, get("/wallet/batches/batch-owned", "unassigned").Code)
		require.Equal(t, []int64{7, service.ImageStudioWalletOwnerSlot}, jobs.owner)
	})
}
