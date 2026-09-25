package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	attachmentgateway "github.com/Wei-Shaw/sub2api/internal/service/attachment_gateway"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func bpsTestSSE(events ...string) io.ReadCloser {
	var builder strings.Builder
	for _, event := range events {
		builder.WriteString("event: ")
		builder.WriteString(gjson.Get(event, "type").String())
		builder.WriteString("\ndata: ")
		builder.WriteString(event)
		builder.WriteString("\n\n")
	}
	return io.NopCloser(strings.NewReader(builder.String()))
}

func bpsTestSSEResponse(events ...string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       bpsTestSSE(events...),
	}
}

func bpsTestAccount() *Account {
	return &Account{
		ID:          69,
		Name:        "bps-test",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"chatgpt_account_id": "acct-test"},
	}
}

func bpsTestShellBody() []byte {
	return []byte(`{
		"model": "gpt-6-astra",
		"instructions": "be helpful",
		"service_tier": "priority",
		"reasoning": {"effort": "max"},
		"tools": [{"type": "function", "name": "shell", "parameters": {"type": "object", "properties": {"command": {"type": "string"}}, "required": ["command"]}}],
		"input": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "list files"}]}]
	}`)
}

func TestPrepareBPSRequestBody(t *testing.T) {
	attempt := &openAIBPSAttempt{upstreamModel: "gpt-6-astra", effort: "high", scope: "account:69/key:1/thread:"}
	body, bridge, err := prepareBPSRequestBody(bpsTestShellBody(), attempt)
	require.NoError(t, err)
	require.Equal(t, "xhigh", bridge.Effort)
	require.Equal(t, "gpt-6-astra", gjson.GetBytes(body, "model").String())
	require.Equal(t, "explicit", gjson.GetBytes(body, "model_selection").String())
	require.Equal(t, "xhigh", gjson.GetBytes(body, "reasoning_effort").String())
	require.False(t, gjson.GetBytes(body, "service_tier").Exists())
	require.False(t, gjson.GetBytes(body, "tools").Exists())
	require.Contains(t, gjson.GetBytes(body, "input").Raw, "shell")
	require.NotEmpty(t, gjson.GetBytes(body, "metadata.task_id").String())

	// 客户端未声明 effort 时使用转发链解析出的档位；上游模型名覆盖客户端别名。
	aliased := []byte(`{"model":"gpt-6","input":"hi"}`)
	body, _, err = prepareBPSRequestBody(aliased, attempt)
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", gjson.GetBytes(body, "model").String())
	require.Equal(t, "high", gjson.GetBytes(body, "reasoning_effort").String())

	// 结构化输出与 item_reference 不受支持：Prepare 报错，调用方走原路径。
	for _, raw := range []string{
		`{"model":"gpt-6-astra","input":"hi","text":{"format":{"type":"json_object"}}}`,
		`{"model":"gpt-6-astra","input":[{"type":"item_reference","id":"x"}]}`,
		`{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`,
	} {
		_, _, err = prepareBPSRequestBody([]byte(raw), attempt)
		require.Error(t, err, raw)
	}
}

func TestBPSStripEncryptedReasoning(t *testing.T) {
	payload := []byte(`{"input":[{"type":"reasoning","encrypted_content":"enc"},{"type":"message","role":"user","content":[]}],"big":12345678901234567890}`)
	stripped, ok := bpsStripEncryptedReasoning(payload)
	require.True(t, ok)
	require.Len(t, gjson.GetBytes(stripped, "input").Array(), 1)
	require.Equal(t, "12345678901234567890", gjson.GetBytes(stripped, "big").Raw)
	_, ok = bpsStripEncryptedReasoning(stripped)
	require.False(t, ok)
}

func bpsTestPrimedStream(t *testing.T, events ...string) *bpsPrimedBody {
	t.Helper()
	_, bridge, err := prepareBPSRequestBody(bpsTestShellBody(), &openAIBPSAttempt{upstreamModel: "gpt-6-astra", scope: "test:" + t.Name()})
	require.NoError(t, err)
	return newBPSPrimedBody(bridge.Stream(bpsTestSSE(events...)))
}

func TestBPSPrimedStreamConvertsTransportCall(t *testing.T) {
	native := map[string]any{
		"type": "function_call", "id": "fc_native", "call_id": "call_bps_relay_1", "name": "run_officejs", "status": "completed",
		"arguments": bpsTestJSON(t, map[string]any{
			"summary": "list files", "extended_summary": "list files", "destructive": false, "references": []any{},
			"code": `{"name":"shell","arguments":{"command":"ls"}}`,
		}),
	}
	stream := bpsTestPrimedStream(t,
		`{"type":"response.created","response":{"id":"resp_1","output":[]}}`,
		bpsTestJSON(t, map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "function_call", "call_id": "call_bps_relay_1", "name": "run_officejs"}}),
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"code\""}`,
		bpsTestJSON(t, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": native}),
		bpsTestJSON(t, map[string]any{"type": "response.completed", "response": map[string]any{
			"id": "resp_1", "output": []any{native},
			"usage": map[string]any{"input_tokens": 22000, "output_tokens": 10},
		}}),
	)
	ok, reason := stream.primeUntilOutput()
	require.True(t, ok, reason)
	out, err := io.ReadAll(stream)
	require.NoError(t, err)
	text := string(out)
	require.NotContains(t, text, "run_officejs")
	require.Contains(t, text, "response.function_call_arguments.done")

	var completed string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"response.completed"`) {
			completed = strings.TrimPrefix(line, "data: ")
		}
	}
	require.NotEmpty(t, completed)
	call := gjson.Get(completed, "response.output.0")
	require.Equal(t, "shell", call.Get("name").String())
	require.JSONEq(t, `{"command":"ls"}`, call.Get("arguments").String())
	require.Equal(t, int64(22000), gjson.Get(completed, "response.usage.input_tokens").Int())
}

func TestBPSPrimedStreamFailsBeforeOutput(t *testing.T) {
	failed := bpsTestPrimedStream(t,
		`{"type":"response.created","response":{"id":"resp_1","output":[]}}`,
		`{"type":"response.failed","response":{"id":"resp_1","output":[],"error":{"code":"server_error","message":"boom"}}}`,
	)
	ok, reason := failed.primeUntilOutput()
	require.False(t, ok)
	require.Contains(t, reason, "boom")

	truncated := bpsTestPrimedStream(t, `{"type":"response.created","response":{"id":"resp_1","output":[]}}`)
	ok, _ = truncated.primeUntilOutput()
	require.False(t, ok)

	// 只有无法还原的调用：basispoints 发出 basispoints_protocol_error，同样在输出前回退。
	bogus := map[string]any{"type": "function_call", "id": "fc_1", "call_id": "c1", "name": "run_officejs", "arguments": `{"code":"Excel.run()"}`}
	invalid := bpsTestPrimedStream(t,
		bpsTestJSON(t, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": bogus}),
		bpsTestJSON(t, map[string]any{"type": "response.completed", "response": map[string]any{"output": []any{bogus}}}),
	)
	ok, reason = invalid.primeUntilOutput()
	require.False(t, ok)
	require.Contains(t, reason, "response.failed")

	text := bpsTestPrimedStream(t,
		`{"type":"response.output_text.delta","output_index":0,"delta":"pong"}`,
		`{"type":"response.completed","response":{"output":[]}}`,
	)
	ok, _ = text.primeUntilOutput()
	require.True(t, ok)
	out, err := io.ReadAll(text)
	require.NoError(t, err)
	require.Contains(t, string(out), "pong")
	require.Contains(t, string(out), "response.completed")
}

func bpsTestJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return string(raw)
}

func TestBPSCircuitBreaker(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	breaker := &bpsCircuitBreaker{states: map[int64]*bpsBreakerState{}, now: func() time.Time { return now }}
	require.True(t, breaker.allow(1))
	require.False(t, breaker.recordFailure(1, 0))
	require.False(t, breaker.recordFailure(1, 500))
	breaker.recordSuccess(1)
	require.False(t, breaker.recordFailure(1, 0))
	require.False(t, breaker.recordFailure(1, 0))
	require.True(t, breaker.recordFailure(1, 0))
	require.False(t, breaker.allow(1))
	now = now.Add(bpsBreakerOpenDuration)
	require.True(t, breaker.allow(1))

	require.True(t, breaker.recordFailure(2, http.StatusForbidden))
	require.False(t, breaker.allow(2))
	require.True(t, breaker.allow(3))
}

type bpsSettingRepoStub struct {
	SettingRepository
	values map[string]string
}

func (r *bpsSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func (r *bpsSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func TestOpenAIBPSUpstreamConfig(t *testing.T) {
	t.Cleanup(func() { refreshOpenAIBPSUpstreamConfigCache(OpenAIBPSUpstreamConfig{}) })
	repo := &bpsSettingRepoStub{values: map[string]string{}}
	svc := &SettingService{settingRepo: repo}

	saved, err := svc.UpdateOpenAIBPSUpstreamConfig(context.Background(), OpenAIBPSUpstreamConfig{Enabled: true, AccountIDs: []int64{90, 69, 69, -1}})
	require.NoError(t, err)
	require.Equal(t, OpenAIBPSUpstreamConfig{Enabled: true, AccountIDs: []int64{69, 90}}, saved)
	require.Equal(t, "[69,90]", repo.values[SettingKeyOpenAIBPSUpstreamAccountIDs])
	require.Equal(t, "true", repo.values[SettingKeyOpenAIBPSUpstreamEnabled])

	// 缓存过期后从 settings 重新读取。
	bpsUpstreamConfigCache.Store(&cachedBPSUpstreamConfig{})
	enabled, listed, liveSearch := svc.isOpenAIBPSUpstreamAccount(context.Background(), 90)
	require.True(t, enabled)
	require.True(t, listed)
	require.False(t, liveSearch)
	_, listed, _ = svc.isOpenAIBPSUpstreamAccount(context.Background(), 70)
	require.False(t, listed)

	saved, err = svc.UpdateOpenAIBPSUpstreamConfig(context.Background(), OpenAIBPSUpstreamConfig{Enabled: true, AccountIDs: []int64{69}, LiveSearch: true})
	require.NoError(t, err)
	require.True(t, saved.LiveSearch)
	require.Equal(t, "true", repo.values[SettingKeyOpenAIBPSUpstreamLiveSearch])
	bpsUpstreamConfigCache.Store(&cachedBPSUpstreamConfig{})
	_, _, liveSearch = svc.isOpenAIBPSUpstreamAccount(context.Background(), 69)
	require.True(t, liveSearch)
}

func bpsResetMonitor(t *testing.T) {
	t.Helper()
	previous := bpsMonitor
	bpsMonitor = newBPSMonitorStore()
	t.Cleanup(func() { bpsMonitor = previous })
}

func TestOpenAIBPSAttemptGate(t *testing.T) {
	bpsResetMonitor(t)
	t.Cleanup(func() { refreshOpenAIBPSUpstreamConfigCache(OpenAIBPSUpstreamConfig{}) })
	svc := &OpenAIGatewayService{settingService: &SettingService{settingRepo: &bpsSettingRepoStub{values: map[string]string{}}}}
	body := []byte(`{"model":"gpt-6-astra","input":"hi"}`)
	account := bpsTestAccount()
	account.ID = 90_001

	refreshOpenAIBPSUpstreamConfigCache(OpenAIBPSUpstreamConfig{Enabled: true, AccountIDs: []int64{account.ID}})
	attempt := svc.openAIBPSAttemptFor(context.Background(), nil, account, body, "gpt-6-astra", "high", false, false, false)
	require.NotNil(t, attempt)
	require.Equal(t, account.ID, attempt.accountID)
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna", "gpt-5.5"} {
		require.Nil(t, svc.openAIBPSAttemptFor(context.Background(), nil, account, body, model, "high", false, false, false), model)
	}
	require.Nil(t, svc.openAIBPSAttemptFor(context.Background(), nil, account, body, "gpt-5.6-sol", "high", true, false, false))
	require.Nil(t, svc.openAIBPSAttemptFor(context.Background(), nil, account, body, "gpt-5.6-sol", "high", false, true, false))
	imageTool := []byte(`{"model":"gpt-6-astra","tools":[{"type":"image_generation"}]}`)
	require.Nil(t, svc.openAIBPSAttemptFor(context.Background(), nil, account, imageTool, "gpt-6-astra", "high", false, false, false))
	liveSearchTool := []byte(`{"model":"gpt-5.6-terra","tools":[{"type":"web_search","external_web_access":true}]}`)
	require.Nil(t, svc.openAIBPSAttemptFor(context.Background(), nil, account, liveSearchTool, "gpt-5.6-terra", "high", false, false, false))
	refreshOpenAIBPSUpstreamConfigCache(OpenAIBPSUpstreamConfig{Enabled: true, AccountIDs: []int64{account.ID}, LiveSearch: true})
	require.NotNil(t, svc.openAIBPSAttemptFor(context.Background(), nil, account, liveSearchTool, "gpt-5.6-terra", "high", false, false, false))

	snapshot := BPSUpstreamMonitorSnapshot()
	require.Len(t, snapshot.Accounts, 1)
	require.Equal(t, int64(3), snapshot.Accounts[0].Skipped[bpsSkipUnsupportedModel])
	require.Equal(t, int64(1), snapshot.Accounts[0].Skipped[bpsSkipCompact])
	require.Equal(t, int64(2), snapshot.Accounts[0].Skipped[bpsSkipImageGeneration])
	// 不支持的模型只计数，不进事件列表。
	require.Equal(t, int64(1), snapshot.Accounts[0].Skipped[bpsSkipNativeTool])
	require.Len(t, snapshot.Events, 4)

	unlisted := bpsTestAccount()
	unlisted.ID = 90_002
	require.Nil(t, svc.openAIBPSAttemptFor(context.Background(), nil, unlisted, body, "gpt-6-astra", "high", false, false, false))

	refreshOpenAIBPSUpstreamConfigCache(OpenAIBPSUpstreamConfig{Enabled: false, AccountIDs: []int64{account.ID}})
	require.Nil(t, svc.openAIBPSAttemptFor(context.Background(), nil, account, body, "gpt-6-astra", "high", false, false, false))
	require.Len(t, BPSUpstreamMonitorSnapshot().Accounts, 1)
}

func TestBPSMonitorRingAndBreakerState(t *testing.T) {
	store := newBPSMonitorStore()
	for i := 0; i < bpsMonitorMaxEvents+5; i++ {
		store.record(BPSEvent{AccountID: 1, Outcome: BPSOutcomeSuccess, DurationMs: int64(i)})
	}
	store.record(BPSEvent{AccountID: 1, Outcome: BPSOutcomeFallback, Reason: bpsFailureHTTPStatus, Detail: "denied", StatusCode: 403})
	breaker := &bpsCircuitBreaker{states: map[int64]*bpsBreakerState{}, now: time.Now}
	breaker.recordFailure(1, http.StatusForbidden)
	snapshot := store.snapshot(breaker)
	require.Len(t, snapshot.Events, bpsMonitorMaxEvents)
	require.Equal(t, BPSOutcomeFallback, snapshot.Events[0].Outcome)
	require.Equal(t, int64(bpsMonitorMaxEvents+4), snapshot.Events[1].DurationMs)
	stats := snapshot.Accounts[0]
	require.Equal(t, int64(bpsMonitorMaxEvents+5), stats.Successes)
	require.Equal(t, int64(1), stats.Fallbacks)
	require.Equal(t, "http_status: denied", stats.LastFailureReason)
	require.NotNil(t, stats.BreakerOpenUntil)
}

type bpsExternalizerStub struct {
	body []byte
}

func (s bpsExternalizerStub) Externalize(context.Context, []byte) attachmentgateway.URLResult {
	return attachmentgateway.URLResult{Body: s.body}
}

func TestDoOpenAIUpstreamPreferBPS(t *testing.T) {
	bpsResetMonitor(t)
	newReq := func() *http.Request {
		req, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader("{}"))
		return req
	}
	originalResp := func() *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("original"))}
	}
	newAttempt := func(account *Account) *openAIBPSAttempt {
		return &openAIBPSAttempt{accountID: account.ID, body: []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"max"},"input":"hi"}`), upstreamModel: "gpt-6-astra", effort: "max", scope: "test"}
	}

	t.Run("success uses bps", func(t *testing.T) {
		account := bpsTestAccount()
		account.ID = 90_101
		upstream := &httpUpstreamRecorder{responses: []*http.Response{bpsTestSSEResponse(
			`{"type":"response.output_text.delta","output_index":0,"delta":"pong"}`,
			`{"type":"response.completed","response":{"output":[]}}`,
		)}}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		resp, bpsRun, err := svc.doOpenAIUpstreamPreferBPS(newReq(), "", account, "tok", newAttempt(account))
		require.NoError(t, err)
		require.NotNil(t, bpsRun)
		require.Equal(t, "xhigh", bpsRun.appliedEffort)
		require.Len(t, upstream.requests, 1)
		sent := upstream.requests[0]
		require.Equal(t, basispoints.ResponsesURL, sent.URL.String())
		require.Equal(t, "Bearer tok", sent.Header.Get("Authorization"))
		require.Equal(t, "acct-test", sent.Header.Get("X-OpenAI-Account-ID"))
		require.Equal(t, "chatgpt", sent.Header.Get("X-Basispoints-Auth-Mode"))
		require.Equal(t, "xhigh", gjson.GetBytes(upstream.bodies[0], "reasoning_effort").String())
		require.False(t, gjson.GetBytes(upstream.bodies[0], "service_tier").Exists())
		out, _ := io.ReadAll(resp.Body)
		require.Contains(t, string(out), "pong")
	})

	t.Run("error falls back and opens breaker on 403", func(t *testing.T) {
		account := bpsTestAccount()
		account.ID = 90_102
		upstream := &httpUpstreamRecorder{responses: []*http.Response{
			{StatusCode: http.StatusForbidden, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"denied"}}`))},
			originalResp(),
		}}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		resp, bpsRun, err := svc.doOpenAIUpstreamPreferBPS(newReq(), "", account, "tok", newAttempt(account))
		require.NoError(t, err)
		require.Nil(t, bpsRun)
		require.Len(t, upstream.requests, 2)
		out, _ := io.ReadAll(resp.Body)
		require.Equal(t, "original", string(out))
		require.False(t, bpsBreaker.allow(account.ID))
	})

	t.Run("failed stream before output falls back", func(t *testing.T) {
		account := bpsTestAccount()
		account.ID = 90_103
		upstream := &httpUpstreamRecorder{responses: []*http.Response{
			bpsTestSSEResponse(`{"type":"response.failed","response":{"error":{"code":"server_error"}}}`),
			originalResp(),
		}}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		_, bpsRun, err := svc.doOpenAIUpstreamPreferBPS(newReq(), "", account, "tok", newAttempt(account))
		require.NoError(t, err)
		require.Nil(t, bpsRun)
		require.Len(t, upstream.requests, 2)
		require.True(t, bpsBreaker.allow(account.ID))
	})

	t.Run("inline image without externalizer skips bps", func(t *testing.T) {
		account := bpsTestAccount()
		account.ID = 90_104
		upstream := &httpUpstreamRecorder{responses: []*http.Response{originalResp()}}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		imageAttempt := &openAIBPSAttempt{body: []byte(`{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`), upstreamModel: "gpt-6-astra", scope: "test", accountID: account.ID}
		_, bpsRun, err := svc.doOpenAIUpstreamPreferBPS(newReq(), "", account, "tok", imageAttempt)
		require.NoError(t, err)
		require.Nil(t, bpsRun)
		require.Len(t, upstream.requests, 1)
		require.Equal(t, "chatgpt.com", upstream.requests[0].URL.Host)

		upstream = &httpUpstreamRecorder{responses: []*http.Response{bpsTestSSEResponse(`{"type":"response.output_text.delta","delta":"ok"}`, `{"type":"response.completed","response":{"output":[]}}`)}}
		svc = &OpenAIGatewayService{httpUpstream: upstream}
		svc.SetBPSImageExternalizer(bpsExternalizerStub{body: []byte(`{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://r2.example/x.png"}]}]}`)})
		_, bpsRun, err = svc.doOpenAIUpstreamPreferBPS(newReq(), "", account, "tok", imageAttempt)
		require.NoError(t, err)
		require.NotNil(t, bpsRun)
		require.Contains(t, string(upstream.bodies[0]), "https://r2.example/x.png")
	})
}
