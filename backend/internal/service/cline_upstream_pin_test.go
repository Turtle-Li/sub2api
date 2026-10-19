//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func clineUpstreamTestAccount(pins, metas map[string]any) *Account {
	extra := map[string]any{}
	if pins != nil {
		extra[ClineUpstreamPinsExtraKey] = pins
	}
	if metas != nil {
		extra[ClineUpstreamMetaExtraKey] = metas
	}
	return &Account{
		ID:          501,
		Name:        "cline-test",
		Platform:    PlatformCline,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-cline-test",
			"base_url": "http://upstream.example",
		},
		Extra: extra,
	}
}

func clinePinExtra(mode string, upstreams, exclude []string, sort string) map[string]any {
	toAny := func(values []string) []any {
		out := make([]any, 0, len(values))
		for _, value := range values {
			out = append(out, value)
		}
		return out
	}
	return map[string]any{
		"mode":      mode,
		"upstreams": toAny(upstreams),
		"exclude":   toAny(exclude),
		"sort":      sort,
	}
}

func clineMetaExtra(pipeline string, upstreams []string) map[string]any {
	toAny := func(values []string) []any {
		out := make([]any, 0, len(values))
		for _, value := range values {
			out = append(out, value)
		}
		return out
	}
	return map[string]any{
		"pipeline":  pipeline,
		"upstreams": toAny(upstreams),
		"pinnable":  pipeline != "",
	}
}

const clineTestModel = "cline-pass/deepseek-v4-flash"

func clineTestBody() []byte {
	return []byte(`{"model":"` + clineTestModel + `","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`)
}

func TestApplyClineUpstreamPin_NoopWithoutConfig(t *testing.T) {
	body := clineTestBody()

	// 非 Cline 账号
	openaiAccount := clineUpstreamTestAccount(nil, nil)
	openaiAccount.Platform = PlatformOpenAI
	require.Equal(t, body, ApplyClineUpstreamPin(openaiAccount, body))

	// Cline 账号但没有配置
	require.Equal(t, body, ApplyClineUpstreamPin(clineUpstreamTestAccount(nil, nil), body))

	// 配置了其它模型
	other := clineUpstreamTestAccount(map[string]any{"cline-pass/other": clinePinExtra(ClinePinModeStrict, []string{"alibaba"}, nil, "")}, nil)
	require.Equal(t, body, ApplyClineUpstreamPin(other, body))
}

func TestApplyClineUpstreamPin_StrictPlannerPipeline(t *testing.T) {
	account := clineUpstreamTestAccount(
		map[string]any{clineTestModel: clinePinExtra(ClinePinModeStrict, []string{"alibaba"}, nil, "")},
		map[string]any{clineTestModel: clineMetaExtra(ClinePipelinePlanner, []string{"alibaba", "baseten"})},
	)
	out := ApplyClineUpstreamPin(account, clineTestBody())

	require.Equal(t, []any{"alibaba"}, gjson.GetBytes(out, "providerOptions.gateway.only").Value())
	require.False(t, gjson.GetBytes(out, "provider").Exists(), "planner pipeline must not receive a top-level provider block")
	// 请求体其余字段保持原样
	require.Equal(t, clineTestModel, gjson.GetBytes(out, "model").String())
	require.Equal(t, "hi", gjson.GetBytes(out, "messages.0.content").String())
}

func TestApplyClineUpstreamPin_PreferredDirectPipeline(t *testing.T) {
	account := clineUpstreamTestAccount(
		map[string]any{clineTestModel: clinePinExtra(ClinePinModePreferred, []string{"deepseek", "baseten"}, nil, "")},
		map[string]any{clineTestModel: clineMetaExtra(ClinePipelineDirect, []string{"deepseek", "baseten", "gmicloud"})},
	)
	out := ApplyClineUpstreamPin(account, clineTestBody())

	require.Equal(t, []any{"deepseek", "baseten"}, gjson.GetBytes(out, "provider.order").Value())
	require.False(t, gjson.GetBytes(out, "providerOptions").Exists(), "direct pipeline must not receive a gateway block")
}

func TestApplyClineUpstreamPin_UnknownPipelineInjectsBothForms(t *testing.T) {
	account := clineUpstreamTestAccount(
		map[string]any{clineTestModel: clinePinExtra(ClinePinModeStrict, []string{"alibaba"}, nil, "")},
		nil, // 尚未探测：管道未知
	)
	out := ApplyClineUpstreamPin(account, clineTestBody())

	require.Equal(t, []any{"alibaba"}, gjson.GetBytes(out, "providerOptions.gateway.only").Value())
	require.Equal(t, []any{"alibaba"}, gjson.GetBytes(out, "provider.only").Value())
}

func TestApplyClineUpstreamPin_ExcludeBecomesAllowList(t *testing.T) {
	account := clineUpstreamTestAccount(
		map[string]any{clineTestModel: clinePinExtra(ClinePinModeOff, nil, []string{"baseten"}, "")},
		map[string]any{clineTestModel: clineMetaExtra(ClinePipelinePlanner, []string{"alibaba", "baseten", "gmicloud"})},
	)
	out := ApplyClineUpstreamPin(account, clineTestBody())

	require.Equal(t, []any{"alibaba", "gmicloud"}, gjson.GetBytes(out, "providerOptions.gateway.only").Value())
}

func TestApplyClineUpstreamPin_ExcludedUpstreamDroppedFromSelection(t *testing.T) {
	account := clineUpstreamTestAccount(
		map[string]any{clineTestModel: clinePinExtra(ClinePinModeStrict, []string{"baseten", "alibaba"}, []string{"baseten"}, "")},
		map[string]any{clineTestModel: clineMetaExtra(ClinePipelinePlanner, []string{"alibaba", "baseten"})},
	)
	out := ApplyClineUpstreamPin(account, clineTestBody())

	require.Equal(t, []any{"alibaba"}, gjson.GetBytes(out, "providerOptions.gateway.only").Value())
}

func TestApplyClineUpstreamPin_SortMapping(t *testing.T) {
	planner := clineUpstreamTestAccount(
		map[string]any{clineTestModel: clinePinExtra(ClinePinModeOff, nil, nil, ClineSortCost)},
		map[string]any{clineTestModel: clineMetaExtra(ClinePipelinePlanner, nil)},
	)
	require.Equal(t, "cost", gjson.GetBytes(ApplyClineUpstreamPin(planner, clineTestBody()), "providerOptions.gateway.sort").String())

	direct := clineUpstreamTestAccount(
		map[string]any{clineTestModel: clinePinExtra(ClinePinModeOff, nil, nil, ClineSortTTFT)},
		map[string]any{clineTestModel: clineMetaExtra(ClinePipelineDirect, nil)},
	)
	require.Equal(t, "latency", gjson.GetBytes(ApplyClineUpstreamPin(direct, clineTestBody()), "provider.sort").String())
}

func TestApplyClineUpstreamPin_OffModeIgnoresSelectedUpstreams(t *testing.T) {
	account := clineUpstreamTestAccount(
		map[string]any{clineTestModel: clinePinExtra(ClinePinModeOff, []string{"alibaba"}, nil, "")},
		map[string]any{clineTestModel: clineMetaExtra(ClinePipelinePlanner, []string{"alibaba"})},
	)
	require.Equal(t, clineTestBody(), ApplyClineUpstreamPin(account, clineTestBody()))
}

func TestApplyClineUpstreamPin_KeepsClientProviderFields(t *testing.T) {
	account := clineUpstreamTestAccount(
		map[string]any{clineTestModel: clinePinExtra(ClinePinModeStrict, []string{"alibaba"}, nil, "")},
		map[string]any{clineTestModel: clineMetaExtra(ClinePipelineDirect, []string{"alibaba"})},
	)
	body := []byte(`{"model":"` + clineTestModel + `","messages":[],"provider":{"allow_fallbacks":false}}`)
	out := ApplyClineUpstreamPin(account, body)

	require.Equal(t, []any{"alibaba"}, gjson.GetBytes(out, "provider.only").Value())
	require.False(t, gjson.GetBytes(out, "provider.allow_fallbacks").Bool(), "unrelated provider fields must be preserved")
}

func TestParseClineUpstreamRouting_PlannerEnvelope(t *testing.T) {
	body := []byte(`{"data":{"choices":[{"message":{"content":"OK","provider_metadata":{"gateway":{"routing":{"canonicalSlug":"deepseek/deepseek-v4-flash","finalProvider":"alibaba","fallbacksAvailable":["baseten"],"planningReasoning":"alibaba won tier 0 over baseten"}}}}}]}}`)
	routing := parseClineUpstreamRouting(body)

	require.Equal(t, ClinePipelinePlanner, routing.pipeline)
	require.Equal(t, "alibaba", routing.finalProvider)
	require.Equal(t, "deepseek/deepseek-v4-flash", routing.canonicalSlug)
	require.Equal(t, []string{"baseten"}, routing.fallbacks)
	require.Equal(t, "OK", routing.content)
}

func TestParseClineUpstreamRouting_DirectTopLevel(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"OK"}}],"provider":"GMICloud","model":"z-ai/glm-5.3-flash"}`)
	routing := parseClineUpstreamRouting(body)

	require.Equal(t, ClinePipelineDirect, routing.pipeline)
	require.Equal(t, "gmicloud", routing.finalProvider)
	require.Equal(t, "GMICloud", routing.finalProviderName)
	require.Equal(t, "z-ai/glm-5.3-flash", routing.canonicalSlug)
}

func TestParseClineAvailableProviders(t *testing.T) {
	planner := `invalid request: Provider set restricted to: __probe__. Available providers are: alibaba, baseten, gmicloud.`
	require.Equal(t, []string{"alibaba", "baseten", "gmicloud"}, parseClineAvailableProviders(planner, ClinePipelinePlanner))

	// 错误文本里混入 JSON 片段时必须按 slug 形状过滤
	noisy := `Available providers are: alibaba, ","type":"invalid_request_error"`
	require.Equal(t, []string{"alibaba"}, parseClineAvailableProviders(noisy, ClinePipelinePlanner))

	direct := `no allowed providers, {"error":{"message":"no allowed providers","metadata":{"available_providers":["deepseek","baseten"]}}}`
	require.Equal(t, []string{"deepseek", "baseten"}, parseClineAvailableProviders(direct, ClinePipelineDirect))
}

func TestParseClineAvailableProvidersHandlesJSONOnEitherPipeline(t *testing.T) {
	// 直连管道的 JSON 错误体：以前只在 pipeline==direct 时才解析，导致规划器管道
	// 误判时渠道清单为空、只剩 fallbacksAvailable 一条。
	jsonBody := `no allowed providers, {"error":{"message":"no allowed providers","metadata":{"available_providers":["deepseek","alibaba"]}}}`
	require.Equal(t, []string{"deepseek", "alibaba"}, parseClineAvailableProviders(jsonBody, ClinePipelinePlanner))
	require.Equal(t, []string{"deepseek", "alibaba"}, parseClineAvailableProviders(jsonBody, ClinePipelineDirect))

	// 规划器文本 + 引号/方括号包裹
	require.Equal(t, []string{"alibaba", "baseten"}, parseClineAvailableProviders(
		`Available providers are: ["alibaba","baseten"].`, ClinePipelinePlanner))
	require.Equal(t, []string{"alibaba"}, parseClineAvailableProviders(
		"Available providers: 'alibaba'", ClinePipelinePlanner))
	// 大小写与缺少 are 的写法
	require.Equal(t, []string{"novita"}, parseClineAvailableProviders(
		"available providers are: novita", ClinePipelinePlanner))
	// 真正拿不到清单时必须是空，而不是编造
	require.Empty(t, parseClineAvailableProviders("model not found", ClinePipelinePlanner))
}

func TestClassifyClineUpstreamError(t *testing.T) {
	cases := []struct {
		message string
		want    string
	}{
		{"empty response content", ClineUpstreamStatusOK},
		{"429 temporarily rate limited", ClineUpstreamStatusLimited},
		{"invalid_request_error: no allowed providers", ClineUpstreamStatusBad},
		{"Unauthorized: re-authenticate", ClineUpstreamStatusAuth},
		{"something unexpected", ClineUpstreamStatusUnknown},
		{"", ClineUpstreamStatusUnknown},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, classifyClineUpstreamError(tc.message), tc.message)
	}
}

func TestMergeClineMetaExtraPreservesOtherModels(t *testing.T) {
	extra := map[string]any{
		ClineUpstreamMetaExtraKey: map[string]any{
			"cline-pass/other": map[string]any{"pipeline": ClinePipelineDirect, "upstreams": []any{"deepseek"}},
		},
	}
	merged := mergeClineMetaExtra(extra, map[string]any{
		clineTestModel: map[string]any{"pipeline": ClinePipelinePlanner, "upstreams": []any{"alibaba"}},
	})

	require.Len(t, merged, 2)
	require.Equal(t, ClinePipelineDirect, merged["cline-pass/other"].(map[string]any)["pipeline"])
	require.Equal(t, ClinePipelinePlanner, merged[clineTestModel].(map[string]any)["pipeline"])
}

func TestClineUpstreamService_UpdatePinsNormalizesAndDropsEmpty(t *testing.T) {
	account := clineUpstreamTestAccount(nil, nil)
	repo := &clineAccountRepo{accounts: []Account{*account}}
	svc := NewClineUpstreamService(repo, nil, nil, nil)

	err := svc.UpdatePins(context.Background(), account.ID, map[string]ClineUpstreamPin{
		clineTestModel:     {Mode: "STRICT", Upstreams: []string{" alibaba ", "alibaba", ""}, Sort: "cost"},
		"cline-pass/empty": {Mode: ClinePinModeOff},
	})
	require.NoError(t, err)
	require.Len(t, repo.extraWrites[account.ID], 1)

	pins, ok := repo.extraWrites[account.ID][0][ClineUpstreamPinsExtraKey].(map[string]any)
	require.True(t, ok)
	require.Len(t, pins, 1, "empty pin configs must not be persisted")
	entry := pins[clineTestModel].(map[string]any)
	require.Equal(t, ClinePinModeStrict, entry["mode"])
	require.Equal(t, []string{"alibaba"}, entry["upstreams"])
	require.Equal(t, ClineSortCost, entry["sort"])
}

func TestClineUpstreamService_GetStateRejectsNonClineAccount(t *testing.T) {
	account := clineUpstreamTestAccount(nil, nil)
	account.Platform = PlatformOpenAI
	repo := &clineAccountRepo{accounts: []Account{*account}}
	svc := NewClineUpstreamService(repo, nil, nil, nil)

	_, err := svc.GetState(context.Background(), account.ID)
	require.Error(t, err)
}

// TestForwardAsRawChatCompletions_InjectsClineUpstreamPin 验证数据面出站点确实注入钉住字段。
func TestForwardAsRawChatCompletions_InjectsClineUpstreamPin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := clineTestBody()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_1","object":"chat.completion","model":"` + clineTestModel + `","choices":[{"index":0,"message":{"role":"assistant","content":"OK"}}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
		)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	account := clineUpstreamTestAccount(
		map[string]any{clineTestModel: clinePinExtra(ClinePinModeStrict, []string{"deepseek"}, nil, ClineSortCost)},
		map[string]any{clineTestModel: clineMetaExtra(ClinePipelinePlanner, []string{"deepseek", "baseten"})},
	)

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, []any{"deepseek"}, gjson.GetBytes(upstream.lastBody, "providerOptions.gateway.only").Value())
	require.Equal(t, "cost", gjson.GetBytes(upstream.lastBody, "providerOptions.gateway.sort").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "provider").Exists())
}

// TestForwardAsRawChatCompletions_NoPinWithoutClineConfig 确认未配置的 Cline 账号请求体不变。
func TestForwardAsRawChatCompletions_NoPinWithoutClineConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := clineTestBody()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_2","object":"chat.completion","model":"` + clineTestModel + `","choices":[{"index":0,"message":{"role":"assistant","content":"OK"}}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
		)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	_, err := svc.forwardAsRawChatCompletions(context.Background(), c, clineUpstreamTestAccount(nil, nil), body, "")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(upstream.lastBody, "providerOptions").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "provider").Exists())
}

// TestClineUpstreamStateJSONNeverContainsNullLists 固定管理端 JSON 契约：Go 的 nil slice
// 会编码成 null，前端按数组使用时会抛 "Cannot read properties of null (reading 'length')"。
// 该 bug 曾在生产页面复现，因此这里直接断言序列化结果。
func TestClineUpstreamStateJSONNeverContainsNullLists(t *testing.T) {
	account := clineUpstreamTestAccount(nil, nil)
	repo := &clineAccountRepo{accounts: []Account{*account}}
	svc := NewClineUpstreamService(repo, nil, nil, nil)

	state, err := svc.GetState(context.Background(), account.ID)
	require.NoError(t, err)
	require.NotNil(t, state)

	// 无探测快照、无配置的账号：models 可能为空，但字段本身绝不能是 null。
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"models":[]`)
	require.NotContains(t, string(raw), `"models":null`)

	// 构造一个「有模型但无快照」的状态，覆盖 candidates / upstreams / statuses。
	pin, meta := ClineUpstreamPin{}, ClineUpstreamMeta{}
	candidates, _ := ClineUpstreamCandidate(pin, meta)
	modelState := ClineUpstreamModelState{
		Model:      clineTestModel,
		Source:     "subscription",
		Pin:        normalizeClinePinForJSON(pin),
		Meta:       normalizeClineMetaForJSON(meta),
		Candidates: normalizeClineListForJSON(candidates),
	}
	raw, err = json.Marshal(modelState)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"candidates":[]`)
	require.Contains(t, string(raw), `"upstreams":[]`)
	require.Contains(t, string(raw), `"exclude":[]`)
	require.Contains(t, string(raw), `"statuses":{}`)
	require.NotContains(t, string(raw), `null`)
}

// clineHarvestUpstream 是渠道清单采集用的最小 HTTPUpstream 假实现。
type clineHarvestUpstream struct {
	responses []*http.Response
	requests  []*http.Request
	idx       int
}

func (u *clineHarvestUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.requests = append(u.requests, req)
	resp := u.responses[u.idx]
	if u.idx < len(u.responses)-1 {
		u.idx++
	}
	return resp, nil
}

func (u *clineHarvestUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func clineHarvestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestHarvestAvailableProviders_SingleUpstreamModel 覆盖 private/* 这类单一上游模型：
// 网关直接忽略占位渠道限制并正常应答，此时不能声称「可钉住」，而要把实际上游回读出来。
func TestHarvestAvailableProviders_SingleUpstreamModel(t *testing.T) {
	upstream := &clineHarvestUpstream{responses: []*http.Response{
		clineHarvestResponse(http.StatusOK, `{"choices":[{"message":{"content":"hi"}}],"provider_metadata":{"gateway":{"routing":{"canonicalSlug":"private/deepseek-v4p1-flash-contributor","finalProvider":"deepseek"}}}}`),
	}}
	account := clineUpstreamTestAccount(nil, nil)
	account.Credentials["base_url"] = "https://upstream.example"
	svc := NewClineUpstreamService(&clineAccountRepo{accounts: []Account{*account}}, nil, upstream, &config.Config{})

	harvest := svc.harvestAvailableProviders(context.Background(), account, "cline-pass/deepseek-v4.1-flash", ClinePipelinePlanner)
	require.False(t, harvest.Selectable, "a gateway that ignores the restriction means there is no selectable pool")
	require.Equal(t, []string{"deepseek"}, harvest.Providers)
	require.Contains(t, harvest.Note, "single upstream")
	require.Contains(t, harvest.Note, "deepseek")
}

// TestHarvestAvailableProviders_RestrictedPool 覆盖正常情况：网关按 only 过滤并报错，
// 渠道清单可解析且标记为可选。
func TestHarvestAvailableProviders_RestrictedPool(t *testing.T) {
	upstream := &clineHarvestUpstream{responses: []*http.Response{
		clineHarvestResponse(http.StatusBadRequest, `invalid request: Provider set restricted to: __probe__. Available providers are: alibaba, baseten, deepseek.`),
	}}
	account := clineUpstreamTestAccount(nil, nil)
	account.Credentials["base_url"] = "https://upstream.example"
	svc := NewClineUpstreamService(&clineAccountRepo{accounts: []Account{*account}}, nil, upstream, &config.Config{})

	harvest := svc.harvestAvailableProviders(context.Background(), account, clineTestModel, ClinePipelinePlanner)
	require.True(t, harvest.Selectable)
	require.Equal(t, []string{"alibaba", "baseten", "deepseek"}, harvest.Providers)
	require.Empty(t, harvest.Note)
}

// TestHarvestAvailableProviders_JSONErrorOnPlannerPipeline 是这次线上问题的回归：
// 规划器管道回 JSON 错误体时，以前完全解析不到清单。
func TestHarvestAvailableProviders_JSONErrorOnPlannerPipeline(t *testing.T) {
	upstream := &clineHarvestUpstream{responses: []*http.Response{
		clineHarvestResponse(http.StatusBadRequest, `no allowed providers, {"error":{"message":"no allowed providers","metadata":{"available_providers":["deepseek","novita"]}}}`),
	}}
	account := clineUpstreamTestAccount(nil, nil)
	account.Credentials["base_url"] = "https://upstream.example"
	svc := NewClineUpstreamService(&clineAccountRepo{accounts: []Account{*account}}, nil, upstream, &config.Config{})

	harvest := svc.harvestAvailableProviders(context.Background(), account, clineTestModel, ClinePipelinePlanner)
	require.True(t, harvest.Selectable)
	require.Equal(t, []string{"deepseek", "novita"}, harvest.Providers)
}

// TestParseClineUpstreamRoutingCapturesRawEvidence 覆盖「私有/单一上游」模型的取证：
// 只有路由元数据原文能说明它到底被谁服务。
func TestParseClineUpstreamRoutingCapturesRawEvidence(t *testing.T) {
	body := []byte(`{"model":"deepseek/deepseek-v4.1-flash","choices":[{"message":{"content":"OK"}}],"provider_metadata":{"gateway":{"routing":{"canonicalSlug":"private/deepseek-v4p1-flash-contributor","finalProvider":"openai-compatible-private","planningReasoning":"single private provider"}}}}`)
	routing := parseClineUpstreamRouting(body)

	require.Equal(t, "openai-compatible-private", routing.finalProvider)
	require.Equal(t, "deepseek/deepseek-v4.1-flash", routing.responseModel)
	require.Equal(t, "private/deepseek-v4p1-flash-contributor", routing.canonicalSlug)
	require.Contains(t, routing.routingRaw, "openai-compatible-private")
	require.Contains(t, routing.routingRaw, "single private provider")
}

// TestEvaluateClinePinAdherence 覆盖「200 不代表钉住生效」的判定。
func TestEvaluateClinePinAdherence(t *testing.T) {
	strict := ClineUpstreamPin{Mode: ClinePinModeStrict, Upstreams: []string{"deepseek"}}
	preferred := ClineUpstreamPin{Mode: ClinePinModePreferred, Upstreams: []string{"deepseek", "baseten"}}
	excluded := ClineUpstreamPin{Mode: ClinePinModeStrict, Upstreams: []string{"deepseek"}, Exclude: []string{"novita"}}
	auto := ClineUpstreamPin{Mode: ClinePinModeOff}

	require.Equal(t, ClineAdherenceAdopted, EvaluateClinePinAdherence(strict, "deepseek"))
	// 两条管道拼写不同也必须判为采纳
	require.Equal(t, ClineAdherenceAdopted, EvaluateClinePinAdherence(
		ClineUpstreamPin{Mode: ClinePinModeStrict, Upstreams: []string{"zai"}}, "z-ai"))
	require.Equal(t, ClineAdherenceFallback, EvaluateClinePinAdherence(preferred, "baseten"))
	// 私有路由忽略 only：请求 200 但由别的渠道服务
	require.Equal(t, ClineAdherenceNotAdopted, EvaluateClinePinAdherence(strict, "openai-compatible-private"))
	require.Equal(t, ClineAdherenceViolated, EvaluateClinePinAdherence(excluded, "novita"))
	require.Equal(t, ClineAdherenceUnresolved, EvaluateClinePinAdherence(strict, ""))
	require.Equal(t, ClineAdherenceUnresolved, EvaluateClinePinAdherence(auto, "deepseek"))

	require.True(t, ClinePinAdherenceOK(ClineAdherenceAdopted))
	require.True(t, ClinePinAdherenceOK(ClineAdherenceFallback))
	require.False(t, ClinePinAdherenceOK(ClineAdherenceNotAdopted))
	require.False(t, ClinePinAdherenceOK(ClineAdherenceViolated))
}

// TestInjectClineUpstreamPrefsUsesPipelineSpelling 覆盖两条管道的渠道名拼写差异：
// 直连管道把 only 透传给 OpenRouter，未知渠道会被拒绝而不是忽略。
func TestInjectClineUpstreamPrefsUsesPipelineSpelling(t *testing.T) {
	pin := ClineUpstreamPin{Mode: ClinePinModeStrict, Upstreams: []string{"zai"}}
	body := clineTestBody()

	planner := injectClineUpstreamPrefs(body, pin, ClineUpstreamMeta{Pipeline: ClinePipelinePlanner})
	require.Equal(t, []any{"zai"}, gjson.GetBytes(planner, "providerOptions.gateway.only").Value())

	direct := injectClineUpstreamPrefs(body, pin, ClineUpstreamMeta{Pipeline: ClinePipelineDirect})
	require.Equal(t, []any{"z-ai"}, gjson.GetBytes(direct, "provider.only").Value())
}
