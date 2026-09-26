package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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

func TestApplyBPSCodexCompactHintToSSELine(t *testing.T) {
	gin.SetMode(gin.TestMode)
	completed := func(input, output int64) string {
		return fmt.Sprintf(`data: {"type":"response.completed","response":{"id":"r","usage":{"input_tokens":%d,"input_tokens_details":{"cached_tokens":1000},"output_tokens":%d,"total_tokens":%d}}}`, input, output, input+output)
	}
	unmarked, _ := gin.CreateTestContext(httptest.NewRecorder())
	line := completed(210_000, 500)
	require.Equal(t, line, applyBPSCodexCompactHintToSSELine(unmarked, &Account{ID: 69}, line, "response.completed"), "non-BPS requests are untouched")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	markBPSCodexCompactHint(c, 69)
	bps := &Account{ID: 69}
	require.Equal(t, line, applyBPSCodexCompactHintToSSELine(c, bps, line, "response.output_text.delta"))
	small := completed(150_000, 500)
	require.Equal(t, small, applyBPSCodexCompactHintToSSELine(c, bps, small, "response.completed"), "below the BPS limit the usage is real")

	require.Equal(t, line, applyBPSCodexCompactHintToSSELine(c, &Account{ID: 70}, line, "response.completed"), "failover to another account is untouched")

	patched := applyBPSCodexCompactHintToSSELine(c, bps, line, "response.completed")
	data := strings.TrimPrefix(patched, "data: ")
	require.EqualValues(t, bpsCodexCompactHintTokens, gjson.Get(data, "response.usage.input_tokens").Int())
	require.EqualValues(t, bpsCodexCompactHintTokens+500, gjson.Get(data, "response.usage.total_tokens").Int())
	require.EqualValues(t, 500, gjson.Get(data, "response.usage.output_tokens").Int())
	require.EqualValues(t, 1000, gjson.Get(data, "response.usage.input_tokens_details.cached_tokens").Int())
	require.Equal(t, "r", gjson.Get(data, "response.id").String())
}

func TestOpenAIBPSAttemptMarksCompactHint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bpsResetMonitor(t)
	t.Cleanup(func() { refreshOpenAIBPSUpstreamConfigCache(OpenAIBPSUpstreamConfig{}) })
	svc := &OpenAIGatewayService{settingService: &SettingService{settingRepo: &bpsSettingRepoStub{values: map[string]string{}}}}
	body := []byte(`{"model":"gpt-6-astra","input":"hi"}`)
	account := bpsTestAccount()
	account.ID = 90_011
	line := func(c *gin.Context) string {
		if !strings.Contains(applyBPSCodexCompactHintToSSELine(c, account, `data: {"type":"response.completed","response":{"usage":{"input_tokens":300000,"total_tokens":300000}}}`, "response.completed"), "1050000") {
			return ""
		}
		return "hinted"
	}
	marked := func(model string, compact bool) bool {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
		svc.openAIBPSAttemptFor(context.Background(), c, account, body, model, "high", compact, false, false)
		id, ok := c.Get(bpsCodexCompactHintContextKey)
		return ok && id == account.ID
	}

	require.False(t, marked("gpt-6-astra", false), "BPS disabled")
	refreshOpenAIBPSUpstreamConfigCache(OpenAIBPSUpstreamConfig{Enabled: true, AccountIDs: []int64{account.ID}})
	require.True(t, marked("gpt-6-astra", false))
	require.False(t, marked("gpt-6-sol", false))
	require.False(t, marked("gpt-6-astra", true))
	retry, _ := gin.CreateTestContext(httptest.NewRecorder())
	retry.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	svc.openAIBPSAttemptFor(context.Background(), retry, account, body, "gpt-6-astra", "high", false, false, false)
	svc.openAIBPSAttemptFor(context.Background(), retry, account, body, "gpt-6-sol", "high", false, false, false)
	require.Equal(t, line(retry), "", "a retry with a non-BPS model clears the earlier mark")
	other := bpsTestAccount()
	other.ID = 90_012
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	svc.openAIBPSAttemptFor(context.Background(), c, other, body, "gpt-6-astra", "high", false, false, false)
	_, ok := c.Get(bpsCodexCompactHintContextKey)
	require.False(t, ok, "accounts outside the BPS list are untouched")
}
