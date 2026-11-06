//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchImageAdminModelsUsesPublicCatalogAndAllowlist(t *testing.T) {
	svc, _, queue, _, _ := newTestBatchImagePublicService(true)
	svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{testBatchImageMappedAccount(101, AccountTypeAPIKey, map[string]any{
		"gemini-2.5-flash-image": "gemini-2.5-flash-image", "gemini-3.1-flash-image": "gemini-3.1-flash-image",
	})}}
	group := &Group{ID: 7, Status: StatusActive, Platform: PlatformGemini, AllowImageGeneration: true, AllowBatchImageGeneration: true,
		ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gemini-2.5-flash-image", "text-only"}}}
	svc.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{7: group}}
	public, err := svc.ListModels(t.Context(), BatchImageOwner{GroupID: &group.ID})
	require.NoError(t, err)
	got, err := svc.AdminModels(t.Context(), group.ID)
	require.NoError(t, err)
	require.Nil(t, got.UnavailableReason)
	expected := make([]BatchImagePublicModel, 0)
	for _, model := range public.Data {
		if group.ModelAllowlist.Allows(model.ID) {
			expected = append(expected, model)
		}
	}
	require.NotEmpty(t, expected)
	require.Equal(t, expected, got.Models)
	require.Greater(t, len(got.Candidates), len(got.Models))
	for _, model := range got.Candidates {
		require.NotEqual(t, "text-only", model.ID)
		require.NotEqual(t, "gpt-image-2", model.ID)
	}
	require.Empty(t, queue.enqueued)
	require.Empty(t, svc.BillingRepo.(*fakeBatchImageBillingRepo).reserves)
}

func TestBatchImageAdminModelsDisabledStatesKeepCandidates(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		change       func(*BatchImagePublicService, *Group)
	}{
		{"inactive", "group_inactive", func(_ *BatchImagePublicService, g *Group) { g.Status = "inactive" }},
		{"images", "image_generation_disabled", func(_ *BatchImagePublicService, g *Group) { g.AllowImageGeneration = false }},
		{"batch", "batch_image_disabled", func(_ *BatchImagePublicService, g *Group) { g.AllowBatchImageGeneration = false }},
		{"runtime", "service_disabled", func(s *BatchImagePublicService, _ *Group) { s.Config.BatchImage.Enabled = false }},
		{"pricing", "pricing_unavailable", func(s *BatchImagePublicService, _ *Group) { s.Pricing = nil }},
		{"allowlist", "no_available_models", func(_ *BatchImagePublicService, g *Group) {
			g.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"text-only"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _, _, _ := newTestBatchImagePublicService(true)
			group := &Group{ID: 7, Status: StatusActive, Platform: PlatformGemini, AllowImageGeneration: true, AllowBatchImageGeneration: true}
			svc.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{7: group}}
			tc.change(svc, group)
			got, err := svc.AdminModels(t.Context(), group.ID)
			require.NoError(t, err)
			require.Equal(t, tc.reason, *got.UnavailableReason)
			require.Empty(t, got.Models)
			require.NotEmpty(t, got.Candidates)
		})
	}
}

type unexpectedAdminUserRate struct{ called bool }

func (r *unexpectedAdminUserRate) GetByUserAndGroup(context.Context, int64, int64) (*float64, error) {
	r.called = true
	return nil, ErrBatchImageSettlementPricingMissing
}

func TestBatchImageAdminModelsNativeCandidatesDoNotAttestReadiness(t *testing.T) {
	svc, _, _, _, _ := newTestBatchImagePublicService(true)
	group := &Group{ID: 7, Status: StatusActive, Platform: PlatformOpenAI, AllowImageGeneration: true, AllowBatchImageGeneration: true}
	svc.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{7: group}}
	account := workBuddyTestAccount("http://unused", "cn:hunyuan-image-alpha")
	account.Status, account.Schedulable = StatusActive, true
	account.Extra = nil
	svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{*account}}
	gateway := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(&museSlotCache{}), httpUpstream: &museTestUpstream{}}
	svc.ProviderRegistry = NewBatchImageProviderRegistry(newWorkBuddyImagesProvider(newFakeOpenAIImageBatchStore(), testOpenAIImageBatchObjectPrefix, gateway))
	rates := &unexpectedAdminUserRate{}
	svc.UserGroupRateRepo = rates
	got, err := svc.AdminModels(t.Context(), group.ID)
	require.NoError(t, err)
	require.Empty(t, got.Models)
	var found bool
	for _, candidate := range got.Candidates {
		if candidate.ID == "cn:hunyuan-image-alpha" {
			found = true
			require.Equal(t, BatchImageProviderWorkBuddy, candidate.Provider)
			require.NotNil(t, candidate.Capabilities)
		}
	}
	require.True(t, found)
	account.Extra = WorkBuddyImageNativeRetryPolicy(true)
	svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{*account}}
	got, err = svc.AdminModels(t.Context(), group.ID)
	require.NoError(t, err)
	require.Len(t, got.Models, 1)
	require.False(t, rates.called)
	require.Same(t, rates, svc.UserGroupRateRepo)
}

func TestBatchImageAdminModelsMissingAndUnsupportedGroup(t *testing.T) {
	svc, _, _, _, _ := newTestBatchImagePublicService(true)
	svc.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{7: {ID: 7, Platform: PlatformAnthropic}}}
	_, err := svc.AdminModels(t.Context(), 8)
	require.ErrorIs(t, err, ErrGroupNotFound)
	got, err := svc.AdminModels(t.Context(), 7)
	require.NoError(t, err)
	require.Empty(t, got.Models)
	require.Empty(t, got.Candidates)
	require.Equal(t, "platform_unsupported", *got.UnavailableReason)
}
