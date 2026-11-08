//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type studioCountedGroup struct {
	BatchImageGroupPricingRepository
	calls int
}

func (r *studioCountedGroup) GetByIDLite(ctx context.Context, id int64) (*Group, error) {
	r.calls++
	return r.BatchImageGroupPricingRepository.GetByIDLite(ctx, id)
}

type studioCountedRate struct {
	BatchImageUserGroupRateRepository
	calls int
}

func (r *studioCountedRate) GetByUserAndGroup(ctx context.Context, user, group int64) (*float64, error) {
	r.calls++
	return r.BatchImageUserGroupRateRepository.GetByUserAndGroup(ctx, user, group)
}

type studioCountedAccounts struct {
	BatchImageAccountSelectionRepository
	calls int
}

func (r *studioCountedAccounts) ListSchedulableByGroupIDAndPlatform(ctx context.Context, group int64, platform string) ([]Account, error) {
	r.calls++
	return r.BatchImageAccountSelectionRepository.ListSchedulableByGroupIDAndPlatform(ctx, group, platform)
}

func TestImageStudioCatalogueReusesReadsWithinRequestOnly(t *testing.T) {
	// Given several models/resolutions and two eligible Gemini providers.
	svc, _, _, _, _ := newTestBatchImagePublicService(true)
	for index := range svc.AccountRepo.(*publicBatchImageAccountRepo).accounts {
		svc.AccountRepo.(*publicBatchImageAccountRepo).accounts[index].Credentials["model_mapping"] = map[string]any{
			"gemini-3-pro-image":     "gemini-3-pro-image",
			"gemini-3.1-flash-image": "gemini-3.1-flash-image",
		}
	}
	group := &Group{ID: 7, Platform: PlatformGemini, AllowBatchImageGeneration: true, RateMultiplier: 1, BatchImageDiscountMultiplier: .5, BatchImageHoldMultiplier: 1}
	groups := &studioCountedGroup{BatchImageGroupPricingRepository: &publicBatchImageGroupRepo{groups: map[int64]*Group{7: group}}}
	rates := &studioCountedRate{BatchImageUserGroupRateRepository: &publicBatchImageUserGroupRateRepo{}}
	accounts := &studioCountedAccounts{BatchImageAccountSelectionRepository: svc.AccountRepo}
	svc.GroupRepo, svc.UserGroupRateRepo, svc.AccountRepo = groups, rates, accounts
	owner := testBatchImageOwner()
	owner.GroupID = &group.ID
	// When the catalogue is requested.
	first, err := svc.ListModels(t.Context(), owner)
	// Then each shared data source is read once, despite many quote calculations.
	require.NoError(t, err)
	require.NotEmpty(t, first.Data)
	require.Equal(t, 1, groups.calls)
	require.Equal(t, 1, rates.calls)
	require.Equal(t, 1, accounts.calls)
	// A later request must read the changed tariff rather than a process cache.
	group.RateMultiplier = 2
	second, err := svc.ListModels(t.Context(), owner)
	require.NoError(t, err)
	require.Equal(t, 2, groups.calls)
	require.Equal(t, 2, rates.calls)
	require.Equal(t, 2, accounts.calls)
	require.Equal(t, first.Data[0].Prices["1K"].Min*2, second.Data[0].Prices["1K"].Min)
}
