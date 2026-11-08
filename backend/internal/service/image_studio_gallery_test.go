//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type studioGalleryTestRepo struct {
	BatchImageRepository
	owner []int64
	query BatchImageGalleryQuery
	rows  []BatchImageGalleryRecord
}

func (r *studioGalleryTestRepo) ListBatchImageGalleryForOwner(_ context.Context, user, key int64, q BatchImageGalleryQuery) ([]BatchImageGalleryRecord, error) {
	r.owner, r.query = []int64{user, key}, q
	return r.rows, nil
}

func TestImageStudioGalleryPageOwnsCursorAndPublicProjection(t *testing.T) {
	r := &studioGalleryTestRepo{}
	for id := int64(1); id <= 3; id++ {
		r.rows = append(r.rows, BatchImageGalleryRecord{
			Job:  &BatchImageJob{ID: 8, BatchID: "owned", Status: BatchImageJobStatusCompleted},
			Item: &BatchImageItem{ID: id, CustomID: "item", Status: BatchImageItemStatusSuccess, ImageCount: 1},
		})
	}
	s := &BatchImagePublicService{Repo: r}
	got, err := s.Gallery(t.Context(), BatchImageOwner{UserID: 10, APIKeyID: 20}, BatchImageGalleryRequest{Limit: 2, Cursor: "9:5", Status: "completed", Search: "flower"})
	require.NoError(t, err)
	require.Equal(t, []int64{10, 20}, r.owner)
	require.Equal(t, BatchImageGalleryQuery{Limit: 3, BeforeJob: 9, BeforeItem: 5, Status: "completed", Search: "flower"}, r.query)
	require.Len(t, got.Data, 2)
	require.Equal(t, "8:2", got.NextCursor)
	require.Equal(t, "succeeded", got.Data[0].Item.Status)
	require.Equal(t, "owned", got.Data[0].Job.ID)
}

func TestImageStudioGalleryRejectsMalformedCursor(t *testing.T) {
	for _, cursor := range []string{"bad", "1:2:3", "0:1", "1:-2", "99999999999999999999:1"} {
		t.Run(cursor, func(t *testing.T) {
			r := &studioGalleryTestRepo{}
			_, err := (&BatchImagePublicService{Repo: r}).Gallery(t.Context(), BatchImageOwner{}, BatchImageGalleryRequest{Limit: 24, Cursor: cursor, Status: "all"})
			require.ErrorIs(t, err, ErrBatchImageInvalidItems)
			require.Nil(t, r.owner)
		})
	}
}

func TestImageStudioCataloguePriceUsesSubmissionMultipliers(t *testing.T) {
	s, _, _, _, _ := newTestBatchImagePublicService(true)
	one, two, userRate, accountRate := 2.0, 3.0, 2.0, 1.25
	id := int64(7)
	s.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{7: {
		ID: 7, AllowBatchImageGeneration: true, RateMultiplier: 3,
		ImagePrice1K: &one, ImagePrice2K: &two, BatchImageDiscountMultiplier: 0.5, BatchImageHoldMultiplier: 0.8,
	}}}
	s.UserGroupRateRepo = &publicBatchImageUserGroupRateRepo{rates: map[int64]*float64{7: &userRate}}
	prices := s.cataloguePrices(t.Context(), BatchImageOwner{UserID: 10, GroupID: &id}, BatchImageProviderVertex, "gemini-2.5-flash-image", &Account{RateMultiplier: &accountRate})
	require.Equal(t, BatchImageModelPriceRange{Min: 2.5, Max: 2.5}, prices["1K"])
	require.Equal(t, BatchImageModelPriceRange{Min: 3.75, Max: 3.75}, prices["2K"])
	native := s.cataloguePrices(t.Context(), BatchImageOwner{UserID: 10, GroupID: &id}, BatchImageProviderMuse, "muse-image", &Account{RateMultiplier: &accountRate})
	require.Equal(t, BatchImageModelPriceRange{Min: 5, Max: 5}, native["1K"])
	require.NotContains(t, native, "2K")
}

func TestImageStudioCatalogueMergesEligibleAccountPriceRange(t *testing.T) {
	s, _, _, _, _ := newTestBatchImagePublicService(true)
	id, one, two, base := int64(7), 1.0, 2.0, 2.0
	s.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{7: {
		ID: 7, Platform: PlatformGemini, AllowImageGeneration: true, AllowBatchImageGeneration: true,
		RateMultiplier: 3, ImagePrice1K: &base, BatchImageDiscountMultiplier: 0.5, BatchImageHoldMultiplier: 0.8,
	}}}
	first := testBatchImageMappedAccount(303, AccountTypeAPIKey, map[string]any{"gemini-2.5-flash-image": "gemini-2.5-flash-image"})
	second := testBatchImageMappedAccount(304, AccountTypeAPIKey, map[string]any{"gemini-2.5-flash-image": "gemini-2.5-flash-image"})
	first.RateMultiplier, second.RateMultiplier = &one, &two
	s.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{first, second}}
	got, err := s.ListModels(t.Context(), BatchImageOwner{UserID: 10, GroupID: &id})
	require.NoError(t, err)
	require.NotEmpty(t, got.Data)
	for _, model := range got.Data {
		require.Equal(t, BatchImageModelPriceRange{Min: 3, Max: 6}, model.Prices["1K"])
	}
}

func TestImageStudioCatalogueUsesConfiguredOpenAIImagePrice(t *testing.T) {
	for _, configured := range []bool{true, false} {
		t.Run(map[bool]string{true: "configured group", false: "unpriced group"}[configured], func(t *testing.T) {
			s, _, _, _, _ := newTestBatchImagePublicService(true)
			id, unit := int64(7), 0.402
			group := &Group{ID: id, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeStandard,
				AllowImageGeneration: true, AllowBatchImageGeneration: true, RateMultiplier: 1,
				BatchImageDiscountMultiplier: 1, BatchImageHoldMultiplier: 1}
			if configured {
				group.ImagePrice1K = &unit
			}
			s.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{id: group}}
			account := testBatchImageMappedAccount(303, AccountTypeAPIKey, map[string]any{"gpt-image-2.5-flare": "gpt-image-2.5-flare"})
			account.Platform = PlatformOpenAI
			s.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{account}}
			s.ProviderRegistry = NewBatchImageProviderRegistry(&publicBatchImageProvider{name: BatchImageProviderOpenAI})
			billing := &BillingService{pricingService: &PricingService{pricingData: map[string]*LiteLLMModelPricing{
				"gpt-image-2.5-flare": {OutputCostPerImageToken: 30e-6},
			}}}
			s.Pricing = &BatchImageModelPricingResolver{Resolver: NewModelPricingResolver(nil, billing)}
			got, err := s.ListModels(t.Context(), BatchImageOwner{UserID: 10, GroupID: &id})
			require.NoError(t, err)
			if !configured {
				require.Empty(t, got.Data, "unknown token conversion must never invent a flat price")
				return
			}
			require.Len(t, got.Data, 1, "configured image quotes must bypass the global token-only precheck")
			require.Equal(t, "gpt-image-2.5-flare", got.Data[0].ID)
			require.Equal(t, BatchImageModelPriceRange{Min: unit, Max: unit}, got.Data[0].Prices["1K"])
		})
	}
}
