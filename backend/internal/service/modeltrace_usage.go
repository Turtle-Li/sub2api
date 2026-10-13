package service

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/tidwall/gjson"
)

type modelTraceUsageKey struct{}
type modelTraceUsageCollector struct {
	tokens       UsageTokens
	model        string
	serviceTier  string
	seen         bool
	startedAt    time.Time
	endpoint     string
	firstTokenMs *int
}

// Capture the path from the constructed request rather than guessing from the
// account platform: accounts can use custom URLs and compatible protocols.
func newAccountTestRequest(ctx context.Context, method, target string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err == nil {
		if collector, ok := ctx.Value(modelTraceUsageKey{}).(*modelTraceUsageCollector); ok && collector != nil {
			collector.endpoint = req.URL.Path
		}
	}
	return req, err
}

// Usage events are cumulative snapshots (Anthropic splits input and output),
// so repeated terminal events must not count the same tokens twice.
func observeModelTraceUsage(ctx context.Context, payload string) {
	collector, ok := ctx.Value(modelTraceUsageKey{}).(*modelTraceUsageCollector)
	if !ok {
		return
	}
	root := gjson.Parse(payload)
	response := root.Get("response")
	if !response.Exists() {
		response = root
	}
	if model := response.Get("model").String(); model != "" {
		collector.model = model
	}
	if tier := response.Get("service_tier").String(); tier != "" {
		collector.serviceTier = tier
	}
	if usage, ok := extractOpenAIUsageFromJSONBytes([]byte(payload)); ok {
		collector.seen = true
		collector.tokens.InputTokens = max(collector.tokens.InputTokens, max(usage.InputTokens-usage.CacheReadInputTokens-usage.CacheCreationInputTokens, 0))
		collector.tokens.OutputTokens = max(collector.tokens.OutputTokens, usage.OutputTokens)
		collector.tokens.CacheReadTokens = max(collector.tokens.CacheReadTokens, usage.CacheReadInputTokens)
		collector.tokens.CacheCreationTokens = max(collector.tokens.CacheCreationTokens, usage.CacheCreationInputTokens)
	}
	// Anthropic input_tokens already excludes cache tokens.
	for _, path := range []string{"message.usage", "usage"} {
		usage := root.Get(path)
		if root.Get("type").String() == "message_start" || root.Get("type").String() == "message_delta" {
			if usage.Exists() {
				collector.seen = true
				collector.tokens.InputTokens = max(collector.tokens.InputTokens, int(usage.Get("input_tokens").Int()))
				collector.tokens.OutputTokens = max(collector.tokens.OutputTokens, int(usage.Get("output_tokens").Int()))
				collector.tokens.CacheReadTokens = max(collector.tokens.CacheReadTokens, int(usage.Get("cache_read_input_tokens").Int()))
				collector.tokens.CacheCreationTokens = max(collector.tokens.CacheCreationTokens, int(usage.Get("cache_creation_input_tokens").Int()))
			}
		}
	}
	if usage := response.Get("usageMetadata"); usage.Exists() {
		collector.seen = true
		cached := int(usage.Get("cachedContentTokenCount").Int())
		collector.tokens.InputTokens = max(collector.tokens.InputTokens, max(int(usage.Get("promptTokenCount").Int())-cached, 0))
		collector.tokens.CacheReadTokens = max(collector.tokens.CacheReadTokens, cached)
		collector.tokens.OutputTokens = max(collector.tokens.OutputTokens, int(usage.Get("candidatesTokenCount").Int()+usage.Get("thoughtsTokenCount").Int()))
	}
}
