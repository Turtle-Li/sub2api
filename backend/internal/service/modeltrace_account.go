package service

import (
	"context"
	"io"
	"regexp"
	"strings"
	"time"
)

// Matches the account-test error formats ("API returned 429: ...", "(HTTP 429)")
// without mistaking ports such as ":443" in transport errors for a status.
var modelTraceHTTPStatus = regexp.MustCompile(`(?:returned|HTTP) ([45][0-9]{2})\b`)

func (c *ModelTraceAccountConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// modelTraceTerminalStatus reports upstream rejections that further attempts in
// the same probe cannot fix: bad request/parameters, auth, missing model, or
// rate limiting. Retrying them only burns quota and can extend upstream cooldowns.
func modelTraceTerminalStatus(err error) bool {
	if err == nil {
		return false
	}
	status := modelTraceHTTPStatus.FindStringSubmatch(err.Error())
	if len(status) < 2 {
		return false
	}
	switch status[1] {
	case "400", "401", "402", "403", "404", "422", "429":
		return true
	}
	return false
}

// modelTraceCoolingDown reports provider-observed cooldown windows. Scheduled
// probes skip these accounts so monitoring never adds load to a limited account.
func modelTraceCoolingDown(account *Account) bool {
	if account == nil {
		return false
	}
	now := time.Now()
	return (account.RateLimitResetAt != nil && now.Before(*account.RateLimitResetAt)) ||
		(account.TempUnschedulableUntil != nil && now.Before(*account.TempUnschedulableUntil)) ||
		(account.OverloadUntil != nil && now.Before(*account.OverloadUntil))
}

// modelTraceReasoningEffortFor drops the reasoning parameter for GPT-4-era
// chat models, which reject it with HTTP 400.
func modelTraceReasoningEffortFor(account *Account, requestedModel, effort string) string {
	mapped := strings.ToLower(strings.TrimSpace(requestedModel))
	if account != nil {
		mapped = strings.ToLower(strings.TrimSpace(account.GetMappedModel(requestedModel)))
	}
	for _, prefix := range []string{"gpt-4", "gpt-3.5", "chatgpt-4o"} {
		if strings.HasPrefix(mapped, prefix) {
			return ""
		}
	}
	return effort
}

func modelTraceAccountSupported(account *Account) bool {
	return account != nil && account.IsOpenAI() &&
		(account.Type == AccountTypeOAuth || account.Type == AccountTypeAPIKey) &&
		!account.IsShadow() && !account.IsOpenAIAgentIdentity() && !account.IsSyntheticUITest()
}

func modelTraceModelSupported(account *Account, requestedModel string) bool {
	if account == nil || !account.IsModelSupported(requestedModel) {
		return false
	}
	mapped := strings.ToLower(strings.TrimSpace(account.GetMappedModel(requestedModel)))
	if mapped == "" || isOpenAIImageModel(mapped) {
		return false
	}
	// ModelTrace's calibration bank covers GPT/Codex-style text models. Keep
	// obvious mappings to other provider families out of the monitor even for
	// OpenAI-compatible API-key accounts.
	for _, prefix := range []string{"deepseek", "glm-", "claude", "gemini", "grok", "qwen", "llama", "mistral", "moonshot", "kimi", "doubao"} {
		if strings.HasPrefix(mapped, prefix) {
			return false
		}
	}
	return true
}

func modelTraceBoundedBody(ctx context.Context, body io.Reader) io.Reader {
	if !accountTestMayChangeAvailability(ctx) {
		return io.LimitReader(body, 2<<20)
	}
	return body
}
