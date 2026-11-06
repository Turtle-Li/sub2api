//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkBuddyReadinessModes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]any
		ready bool
	}{
		{"missing", nil, false},
		{"false", map[string]any{"workbuddy_image_native_retry_accepted": false}, false},
		{"string", map[string]any{"workbuddy_image_native_retry_accepted": "true"}, false},
		{"number", map[string]any{"workbuddy_image_native_retry_accepted": 1}, false},
		{"verified_wrong_type", map[string]any{workBuddyImageReadyKey: "true"}, false},
		{"native", map[string]any{"workbuddy_image_native_retry_accepted": true}, true},
		{"native_verified_false", map[string]any{"workbuddy_image_native_retry_accepted": true, workBuddyImageReadyKey: false}, true},
		{"verified", map[string]any{workBuddyImageReadyKey: true}, true},
		{"verified_native_false", map[string]any{workBuddyImageReadyKey: true, "workbuddy_image_native_retry_accepted": false}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given an otherwise eligible account with an explicit readiness mode.
			svc, _, _, _, _ := newTestBatchImagePublicService(true)
			a := workBuddyTestAccount("http://unused", "cn:hunyuan-image-alpha")
			a.Status, a.Schedulable, a.Extra = StatusActive, true, tc.extra
			svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{*a}}
			gateway := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(&museSlotCache{}), httpUpstream: &museTestUpstream{}}
			svc.ProviderRegistry = NewBatchImageProviderRegistry(newWorkBuddyImagesProvider(newFakeOpenAIImageBatchStore(), testOpenAIImageBatchObjectPrefix, gateway))

			// When the public catalog resolves usable models.
			list, err := svc.ListModels(t.Context(), testBatchImageOwner())

			// Then only attested modes expose the exact model.
			require.NoError(t, err)
			if tc.ready {
				require.Len(t, list.Data, 1)
				require.Equal(t, "cn:hunyuan-image-alpha", list.Data[0].ID)
			} else {
				require.Empty(t, list.Data)
			}
		})
	}
}

func TestWorkBuddyCatalogReadinessAndPreReserve(t *testing.T) {
	svc, _, _, _, _ := newTestBatchImagePublicService(true)
	a := workBuddyTestAccount("http://unused", "cn:hunyuan-image-alpha-edit")
	a.Status = StatusActive
	a.Schedulable = true
	svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{*a}}
	gateway := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(&museSlotCache{}), httpUpstream: &museTestUpstream{}}
	svc.ProviderRegistry = NewBatchImageProviderRegistry(newWorkBuddyImagesProvider(newFakeOpenAIImageBatchStore(), testOpenAIImageBatchObjectPrefix, gateway))
	list, err := svc.ListModels(t.Context(), testBatchImageOwner())
	require.NoError(t, err)
	require.Len(t, list.Data, 1)
	require.Equal(t, "cn:hunyuan-image-alpha-edit", list.Data[0].ID)
	a.Extra[workBuddyImageReadyKey] = false
	list, err = svc.ListModels(t.Context(), testBatchImageOwner())
	require.NoError(t, err)
	require.Empty(t, list.Data)
	a.Extra[workBuddyImageReadyKey] = true
	_, err = svc.Submit(t.Context(), testBatchImageOwner(), BatchImageSubmitRequest{Model: "cn:hunyuan-image-alpha-edit", Items: []BatchImageSubmitItem{{Prompt: "image"}}}, "missing-edit-ref")
	require.Error(t, err)
	require.Empty(t, svc.BillingRepo.(*fakeBatchImageBillingRepo).reserves)
	a.Credentials["model_mapping"] = map[string]any{"cn:hunyuan-image-alpha": "cn:hunyuan-image-alpha-edit"}
	require.False(t, batchImageAccountSupportsProviderModel(BatchImageProviderWorkBuddy, a, "cn:hunyuan-image-alpha"))
	list, err = svc.ListModels(t.Context(), testBatchImageOwner())
	require.NoError(t, err)
	require.Empty(t, list.Data)
}
