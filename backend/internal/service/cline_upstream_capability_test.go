//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const clineCapabilityTestModel = "cline-pass/deepseek-v4.1-flash-1017"

func TestApplyClineUpstreamPin_CapabilityFilter(t *testing.T) {
	for _, pipeline := range []string{"", ClinePipelinePlanner, ClinePipelineDirect} {
		t.Run("pipeline="+pipeline, func(t *testing.T) {
			account := clineUpstreamTestAccount(
				map[string]any{clineCapabilityTestModel: clinePinExtra(ClinePinModeStrict, []string{"deepseek"}, nil, "")},
				map[string]any{clineCapabilityTestModel: clineMetaExtra(pipeline, nil)},
			)
			body := []byte(`{"model":"` + clineCapabilityTestModel + `","messages":[],"stream":true,"providerOptions":{"gateway":{"has":["reasoning","vision"],"custom":"keep"}}}`)

			out := ApplyClineUpstreamPin(account, body)

			require.Equal(t, []any{"deepseek"}, gjson.GetBytes(out, "providerOptions.gateway.only").Value())
			require.ElementsMatch(t, []any{"vision", "implicit-caching", "reasoning"}, gjson.GetBytes(out, "providerOptions.gateway.has").Value())
			require.Equal(t, "keep", gjson.GetBytes(out, "providerOptions.gateway.custom").String())
			require.Equal(t, clineCapabilityTestModel, gjson.GetBytes(out, "model").String())
			require.True(t, gjson.GetBytes(out, "stream").Bool())
		})
	}
}

func TestApplyClineUpstreamPin_CapabilityFilterScope(t *testing.T) {
	for _, model := range []string{clineCapabilityTestModel, "cline-pass/deepseek-v4-pro", "cline-pass/deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash-1017", "cline-pass/kimi-k3"} {
		t.Run(model, func(t *testing.T) {
			body := []byte(`{"model":"` + model + `","messages":[]}`)
			account := clineUpstreamTestAccount(
				map[string]any{model: clinePinExtra(ClinePinModeStrict, []string{"deepseek"}, nil, "")},
				map[string]any{model: clineMetaExtra(ClinePipelinePlanner, nil)},
			)

			out := ApplyClineUpstreamPin(account, body)

			require.Equal(t, model == clineCapabilityTestModel, gjson.GetBytes(out, "providerOptions.gateway.has").Exists())
		})
	}
}

func TestApplyClineUpstreamPin_CapabilityFilterRemainsOptIn(t *testing.T) {
	for _, pin := range []map[string]any{nil, clinePinExtra(ClinePinModeOff, []string{"deepseek"}, nil, ""), clinePinExtra(ClinePinModeOff, nil, nil, ClineSortCost)} {
		account := clineUpstreamTestAccount(map[string]any{clineCapabilityTestModel: pin}, nil)
		body := []byte(`{"model":"` + clineCapabilityTestModel + `","messages":[]}`)

		out := ApplyClineUpstreamPin(account, body)

		require.False(t, gjson.GetBytes(out, "providerOptions.gateway.has").Exists())
		if pin == nil || pin["sort"] == "" {
			require.Equal(t, body, out)
		}
	}
}

func TestHarvestAvailableProviders_UsesCapabilityFilter(t *testing.T) {
	upstream := &clineHarvestUpstream{responses: []*http.Response{
		clineHarvestResponse(http.StatusBadRequest, `{"error":"No available providers match the 'only' filter. Available providers are: deepseek, novita."}`),
	}}
	account := clineUpstreamTestAccount(nil, nil)
	account.Credentials["base_url"] = "https://api.cline.bot/api/v1"
	svc := NewClineUpstreamService(nil, nil, upstream, &config.Config{})

	harvest := svc.harvestAvailableProviders(context.Background(), account, clineCapabilityTestModel, ClinePipelinePlanner)

	require.True(t, harvest.Selectable)
	require.Equal(t, []string{"deepseek", "novita"}, harvest.Providers)
	raw, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, []any{"__probe__"}, gjson.GetBytes(raw, "providerOptions.gateway.only").Value())
	require.True(t, gjson.GetBytes(raw, "providerOptions.gateway.has").IsArray())
	require.ElementsMatch(t, []any{"vision", "implicit-caching"}, gjson.GetBytes(raw, "providerOptions.gateway.has").Value())
}

func TestValidateUpstreams_UsesCapabilityFilter(t *testing.T) {
	upstream := &clineHarvestUpstream{responses: []*http.Response{
		clineHarvestResponse(http.StatusOK, `{"choices":[{"message":{"content":"OK"}}],"provider_metadata":{"gateway":{"routing":{"finalProvider":"deepseek"}}}}`),
	}}
	account := clineUpstreamTestAccount(nil, nil)
	account.Credentials["base_url"] = "https://api.cline.bot/api/v1"
	svc := NewClineUpstreamService(nil, nil, upstream, &config.Config{})

	statuses := svc.validateUpstreams(context.Background(), account, clineCapabilityTestModel, ClineUpstreamMeta{Pipeline: ClinePipelinePlanner, Upstreams: []string{"deepseek"}})

	require.Equal(t, ClineUpstreamStatusOK, statuses["deepseek"].Status)
	raw, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, []any{"deepseek"}, gjson.GetBytes(raw, "providerOptions.gateway.only").Value())
	require.True(t, gjson.GetBytes(raw, "providerOptions.gateway.has").IsArray())
	require.ElementsMatch(t, []any{"vision", "implicit-caching"}, gjson.GetBytes(raw, "providerOptions.gateway.has").Value())
}

func TestClineUpstreamService_TestRejectsEmbeddedErrors(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"No available providers match the only filter","type":"invalid_request_error"}}`,
		"event: error\r\ndata: {\"error\":{\"message\":\"No available providers match the only filter\",\"type\":\"invalid_request_error\"}}\r\n\r\ndata: [DONE]\r\n\r\n",
	} {
		upstream := &clineHarvestUpstream{responses: []*http.Response{clineHarvestResponse(http.StatusOK, body)}}
		account := clineUpstreamTestAccount(nil, nil)
		account.Credentials["base_url"] = "https://api.cline.bot/api/v1"
		svc := NewClineUpstreamService(&clineAccountRepo{accounts: []Account{*account}}, nil, upstream, &config.Config{})

		result, err := svc.Test(context.Background(), account.ID, clineCapabilityTestModel, ClineUpstreamPin{Mode: ClinePinModeStrict, Upstreams: []string{"not-a-real-provider"}})

		require.NoError(t, err)
		require.False(t, result.OK)
		require.Contains(t, result.Error, "No available providers")
		require.Empty(t, result.ActualProvider)
	}
}

func TestClineUpstreamService_ProbeRejectsEmbeddedError(t *testing.T) {
	upstream := &clineHarvestUpstream{responses: []*http.Response{
		clineHarvestResponse(http.StatusOK, `{"error":"No available providers match the only filter"}`),
	}}
	svc := NewClineUpstreamService(nil, nil, upstream, &config.Config{})
	account := clineUpstreamTestAccount(nil, nil)
	account.Credentials["base_url"] = "https://api.cline.bot/api/v1"

	result := svc.probeModel(context.Background(), account, clineCapabilityTestModel)

	require.False(t, result.OK)
	require.Contains(t, result.Error, "No available providers")
	require.Len(t, upstream.requests, 1)
}

func TestValidateUpstreams_ClassifiesEmbeddedError(t *testing.T) {
	upstream := &clineHarvestUpstream{responses: []*http.Response{
		clineHarvestResponse(http.StatusOK, "data: {\"error\":{\"message\":\"No available providers match the only filter\",\"type\":\"invalid_request_error\"}}\n\ndata: [DONE]\n\n"),
	}}
	svc := NewClineUpstreamService(nil, nil, upstream, &config.Config{})
	account := clineUpstreamTestAccount(nil, nil)
	account.Credentials["base_url"] = "https://api.cline.bot/api/v1"

	statuses := svc.validateUpstreams(context.Background(), account, clineCapabilityTestModel, ClineUpstreamMeta{Upstreams: []string{"deepseek"}})

	require.Equal(t, ClineUpstreamStatusBad, statuses["deepseek"].Status)
	require.True(t, strings.Contains(statuses["deepseek"].Note, "No available providers"))
}

func TestClineUpstreamService_TestRequiresProviderEvidence(t *testing.T) {
	upstream := &clineHarvestUpstream{responses: []*http.Response{
		clineHarvestResponse(http.StatusOK, `{"choices":[{"message":{"content":"OK"}}]}`),
	}}
	account := clineUpstreamTestAccount(nil, nil)
	account.Credentials["base_url"] = "https://api.cline.bot/api/v1"
	svc := NewClineUpstreamService(&clineAccountRepo{accounts: []Account{*account}}, nil, upstream, &config.Config{})

	result, err := svc.Test(context.Background(), account.ID, clineCapabilityTestModel, ClineUpstreamPin{Mode: ClinePinModeStrict, Upstreams: []string{"deepseek"}})

	require.NoError(t, err)
	require.False(t, result.OK)
	require.Equal(t, string(ClineAdherenceUnresolved), result.Adherence)
}

func TestClineUpstreamService_ProbeRejectsEmptyHTTPError(t *testing.T) {
	upstream := &clineHarvestUpstream{responses: []*http.Response{clineHarvestResponse(http.StatusBadGateway, "")}}
	svc := NewClineUpstreamService(nil, nil, upstream, &config.Config{})
	account := clineUpstreamTestAccount(nil, nil)
	account.Credentials["base_url"] = "https://api.cline.bot/api/v1"

	result := svc.probeModel(context.Background(), account, clineCapabilityTestModel)

	require.False(t, result.OK)
	require.Contains(t, result.Error, "502")
	require.Len(t, upstream.requests, 1)
}

func TestForwardAsRawChatCompletions_CapabilityFilterAfterModelMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-flash-1017","messages":[{"role":"user","content":"hi"}]}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: clineHarvestResponse(http.StatusOK, `{"choices":[{"message":{"content":"OK","provider_metadata":{"gateway":{"routing":{"finalProvider":"deepseek"}}}}}],"model":"`+clineCapabilityTestModel+`"}`)}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := clineUpstreamTestAccount(
		map[string]any{clineCapabilityTestModel: clinePinExtra(ClinePinModeStrict, []string{"deepseek"}, nil, "")},
		map[string]any{clineCapabilityTestModel: clineMetaExtra(ClinePipelinePlanner, nil)},
	)
	account.Credentials["model_mapping"] = map[string]any{"deepseek-flash-1017": clineCapabilityTestModel}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "deepseek", result.UpstreamProvider)
	require.Equal(t, clineCapabilityTestModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, []any{"deepseek"}, gjson.GetBytes(upstream.lastBody, "providerOptions.gateway.only").Value())
	require.True(t, gjson.GetBytes(upstream.lastBody, "providerOptions.gateway.has").IsArray())
	require.ElementsMatch(t, []any{"vision", "implicit-caching"}, gjson.GetBytes(upstream.lastBody, "providerOptions.gateway.has").Value())
}
