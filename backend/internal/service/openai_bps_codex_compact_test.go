package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func bpsCompactTestManifest() *OpenAIModelsResponse {
	body := []byte(`{"models":[` +
		`{"slug":"gpt-5.6-sol","context_window":272000,"auto_compact_token_limit":null,"extra":{"keep":true}},` +
		`{"slug":"gpt-6-astra","context_window":1050000,"auto_compact_token_limit":900000},` +
		`{"slug":"gpt-5.6-terra","auto_compact_token_limit":150000},` +
		`{"slug":"gpt-6-sol","context_window":272000}],"etag_hint":"x"}`)
	return &OpenAIModelsResponse{Body: body, ETag: codexModelsManifestBodyETag(body)}
}

func bpsCompactLimits(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var root struct {
		Models []map[string]any `json:"models"`
	}
	require.NoError(t, json.Unmarshal(body, &root))
	limits := map[string]any{}
	for _, model := range root.Models {
		limits[model["slug"].(string)] = model["auto_compact_token_limit"]
	}
	return limits
}

func bpsCompactTestService(t *testing.T, groupIDs []int64) *OpenAIGatewayService {
	t.Helper()
	t.Cleanup(func() { refreshOpenAIBPSUpstreamConfigCache(OpenAIBPSUpstreamConfig{}) })
	account := bpsTestAccount()
	account.Status = StatusActive
	account.GroupIDs = groupIDs
	refreshOpenAIBPSUpstreamConfigCache(OpenAIBPSUpstreamConfig{Enabled: true, AccountIDs: []int64{account.ID}})
	return &OpenAIGatewayService{
		settingService: &SettingService{settingRepo: &bpsSettingRepoStub{values: map[string]string{}}},
		accountRepo:    stubOpenAIAccountRepo{accounts: []Account{*account}},
	}
}

func TestFinalizeCodexModelsManifestCapsBPSGroupModels(t *testing.T) {
	svc := bpsCompactTestService(t, []int64{16})
	manifest := bpsCompactTestManifest()
	staleETag := manifest.ETag

	require.NoError(t, svc.FinalizeCodexModelsManifest(context.Background(), &Group{ID: 16}, manifest, staleETag))
	require.False(t, manifest.NotModified, "a pre-cap ETag must not keep the client on the uncapped catalog")
	require.NotEqual(t, staleETag, manifest.ETag)

	limits := bpsCompactLimits(t, manifest.Body)
	require.EqualValues(t, bpsCodexAutoCompactTokenLimit, limits["gpt-5.6-sol"])
	require.EqualValues(t, bpsCodexAutoCompactTokenLimit, limits["gpt-6-astra"])
	require.EqualValues(t, 150000, limits["gpt-5.6-terra"], "lower limits are kept")
	require.Nil(t, limits["gpt-6-sol"], "models BPS never serves are untouched")
	require.Contains(t, string(manifest.Body), `"extra":{"keep":true}`)
	require.Contains(t, string(manifest.Body), `"etag_hint":"x"`)

	again := bpsCompactTestManifest()
	require.NoError(t, svc.FinalizeCodexModelsManifest(context.Background(), &Group{ID: 16}, again, manifest.ETag))
	require.True(t, again.NotModified)
}

func TestFinalizeCodexModelsManifestLeavesOtherGroupsUntouched(t *testing.T) {
	svc := bpsCompactTestService(t, []int64{16})
	manifest := bpsCompactTestManifest()
	original := string(manifest.Body)

	require.NoError(t, svc.FinalizeCodexModelsManifest(context.Background(), &Group{ID: 7}, manifest, ""))
	require.Equal(t, original, string(manifest.Body))

	refreshOpenAIBPSUpstreamConfigCache(OpenAIBPSUpstreamConfig{Enabled: false, AccountIDs: []int64{69}})
	manifest = bpsCompactTestManifest()
	require.NoError(t, svc.FinalizeCodexModelsManifest(context.Background(), &Group{ID: 16}, manifest, ""))
	require.Equal(t, original, string(manifest.Body), "BPS disabled leaves the catalog as is")
}
