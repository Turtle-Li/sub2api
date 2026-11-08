package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type channelMonitorV3GatewayProbe struct {
	gateway     *GatewayService
	openai      *OpenAIGatewayService
	concurrency *ConcurrencyService
}

type channelMonitorV3ProbeGroupContextKey struct{}

// Only the operator-owned runner installs this private context value. A probe
// checks its exact group, including client-restricted groups, instead of using
// the ordinary client's fallback group. It does not certify client admission.
func channelMonitorV3ProbeTargetsGroup(ctx context.Context, groupID int64) bool {
	id, ok := ctx.Value(channelMonitorV3ProbeGroupContextKey{}).(int64)
	return ok && id == groupID
}

// Check uses the normal scheduler and provider forwarder, without creating a
// customer usage record. It deliberately makes only one selected-account attempt.
func (p *channelMonitorV3GatewayProbe) Check(ctx context.Context, group *Group, model string) (out ChannelMonitorV3GroupProbeResult) {
	started := time.Now()
	out.Outcome = "failure"
	out.Category = "upstream_error"
	defer func() {
		out.LatencyMs = time.Since(started).Milliseconds()
		if recover() != nil {
			out.Outcome = "failure"
			out.Category = "probe_internal"
		}
	}()
	ctx = context.WithValue(ctx, channelMonitorV3ProbeGroupContextKey{}, group.ID)
	var selection *AccountSelectionResult
	var err error
	if domain.UsesOpenAIGateway(group.Platform) {
		selection, _, err = p.openai.SelectAccountWithSchedulerForCapability(ctx, &group.ID, "", "", model, nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true, group.Platform)
	} else {
		selection, err = p.gateway.SelectAccountWithLoadAwareness(ctx, &group.ID, "", model, nil, "", 0)
	}
	if err != nil || selection == nil || selection.Account == nil {
		out.Category = "no_available_account"
		return
	}
	out.AccountID = selection.Account.ID
	if selection.Acquired {
		if selection.ReleaseFunc != nil {
			defer selection.ReleaseFunc()
		}
	} else {
		if p.concurrency == nil || selection.WaitPlan == nil {
			out.Category = "capacity"
			return
		}
		acquired, err := p.concurrency.AcquireAccountSlot(ctx, selection.Account.ID, selection.WaitPlan.MaxConcurrency)
		if err != nil || acquired == nil || !acquired.Acquired {
			out.Category = "capacity"
			return
		}
		if acquired.ReleaseFunc != nil {
			defer acquired.ReleaseFunc()
		}
	}
	return p.checkAccount(ctx, group, model, selection.Account)
}

func (p *channelMonitorV3GatewayProbe) checkAccount(ctx context.Context, group *Group, model string, account *Account) (out ChannelMonitorV3GroupProbeResult) {
	out = ChannelMonitorV3GroupProbeResult{Outcome: "failure", Category: "upstream_error", AccountID: account.ID}
	if channelMonitorV3ProbeMediaModel(account.GetMappedModel(model)) {
		out.Category = "unsupported_model"
		return
	}
	var err error
	var firstTokenMs *int
	var cacheReadTokens int64
	var nativeCacheReceipt bool
	body := channelMonitorV3ProbeBody(group.Platform, model)
	writer := &channelMonitorV3ProbeWriter{header: make(http.Header)}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "Sub2API-Availability-Probe")
	c.Set("api_key", &APIKey{GroupID: &group.ID, Group: group})
	if domain.UsesOpenAIGateway(group.Platform) {
		c.Request.URL.Path = "/v1/chat/completions"
		result, forwardErr := p.openai.ForwardAsChatCompletions(ctx, c, account, body, "", model)
		err = forwardErr
		if result != nil {
			out.InputTokens = int64(result.Usage.InputTokens)
			out.OutputTokens = int64(result.Usage.OutputTokens)
			firstTokenMs = result.FirstTokenMs
			cacheReadTokens = int64(result.Usage.CacheReadInputTokens)
			nativeCacheReceipt = result.UpstreamEndpoint == grokChatRawEndpoint
		}
	} else {
		parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
		if parseErr != nil {
			out.Category = "probe_internal"
			return
		}
		parsed.GroupID = &group.ID
		result, forwardErr := p.gateway.Forward(ctx, c, account, parsed)
		err = forwardErr
		if result != nil {
			// Anthropic reports disjoint input buckets; unlike OpenAI's total,
			// input_tokens alone omits the cached consumption of this check.
			out.InputTokens = int64(result.Usage.InputTokens) + int64(result.Usage.CacheCreationInputTokens) + int64(result.Usage.CacheReadInputTokens)
			out.OutputTokens = int64(result.Usage.OutputTokens)
			firstTokenMs = result.FirstTokenMs
			cacheReadTokens = int64(result.Usage.CacheReadInputTokens)
			nativeCacheReceipt = result.OpenAIResult == nil
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		out.Outcome = "cancelled"
		out.Category = ""
		return
	}
	if ctx.Err() != nil {
		out.Category = "timeout"
		return
	}
	if err != nil || writer.status >= 400 || writer.overflow || !channelMonitorV3ProbeResponseOK(writer.body.Bytes()) {
		return
	}
	if firstTokenMs != nil && *firstTokenMs >= 0 {
		ms := int64(*firstTokenMs)
		out.FirstTokenMs = &ms
	}
	// Compatibility converters do not preserve explicit zero versus missing
	// cache details. Use only native receipts, never their synthesized fields.
	if nativeCacheReceipt {
		if cached := channelMonitorV3ProbeCacheReadTokens(writer.body.Bytes(), out.InputTokens, cacheReadTokens); cached != nil {
			input := out.InputTokens
			out.CacheInputTokens, out.CacheReadTokens = &input, cached
		}
	}
	out.Outcome = "success"
	out.Category = ""
	return
}

// Use the native normalized buckets, but require an explicit numeric cache
// declaration in the captured receipt. Absent usage must not become a cold hit.
func channelMonitorV3ProbeCacheReadTokens(body []byte, input, cached int64) *int64 {
	if input <= 0 || cached < 0 || cached > input {
		return nil
	}
	reported, invalid := false, false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if !gjson.Valid(raw) {
			continue
		}
		v := gjson.Parse(raw)
		for _, root := range []string{"usage", "response.usage", "message.usage"} {
			usage := v.Get(root)
			for _, key := range []string{"input_tokens_details.cached_tokens", "prompt_tokens_details.cached_tokens", "cache_read_input_tokens", "cache_read_tokens", "cached_tokens", "prompt_cache_hit_tokens"} {
				value := usage.Get(key)
				if !value.Exists() {
					continue
				}
				n := value.Int()
				if value.Type != gjson.Number || n < 0 || n > input || value.Float() != float64(n) {
					invalid = true
				} else if n == cached {
					reported = true
				}
			}
		}
	}
	if !reported || invalid {
		return nil
	}
	return &cached
}

func channelMonitorV3ProbeBody(platform, model string) []byte {
	payload := map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "Reply with 1."}}, "stream": true, "max_tokens": 1}
	if domain.UsesOpenAIGateway(platform) {
		delete(payload, "max_tokens")
		payload["max_completion_tokens"] = 16
		payload["reasoning_effort"] = "none"
	}
	body, _ := json.Marshal(payload)
	return body
}

// Require a completed response. The intentional tiny output budget may end a
// reasoning-only answer at length; this proves connectivity without requiring
// a larger paid budget. Empty streams and provider errors still fail.
func channelMonitorV3ProbeResponseOK(body []byte) bool {
	text, terminal, bad := false, false, false
	reasoning, budgetEnd := false, false
	inspect := func(raw string) {
		if !gjson.Valid(raw) {
			return
		}
		v := gjson.Parse(raw)
		kind := v.Get("type").String()
		if v.Get("error").Exists() || kind == "error" || kind == "response.failed" || kind == "response.incomplete" {
			bad = true
		}
		if v.Get("choices.0.delta.content").String() != "" || v.Get("delta.text").String() != "" || v.Get("delta").String() != "" && kind == "response.output_text.delta" {
			text = true
		}
		if v.Get("choices.0.delta.reasoning_content").String() != "" {
			reasoning = true
		}
		if v.Get("choices.0.finish_reason").String() == "length" {
			budgetEnd = true
		}
		if kind == "message_stop" || kind == "response.completed" || v.Get("choices.0.finish_reason").String() != "" {
			terminal = true
		}
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if raw == "[DONE]" {
				terminal = true
			} else {
				inspect(raw)
			}
		}
	}
	return (text || reasoning && budgetEnd) && terminal && !bad
}

type channelMonitorV3ProbeWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *channelMonitorV3ProbeWriter) Header() http.Header { return w.header }
func (w *channelMonitorV3ProbeWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *channelMonitorV3ProbeWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len()+len(b) <= 64*1024 {
		w.body.Write(b)
	} else {
		w.overflow = true
	}
	return len(b), nil
}
func (w *channelMonitorV3ProbeWriter) Flush() {}
