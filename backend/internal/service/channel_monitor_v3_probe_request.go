package service

import (
	"encoding/json"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

// Restricted API-key relays keep the native account-test profile. OAuth keeps
// the regular forwarding/authentication path; its runner-owned context selects
// the minimal system blocks instead of the general agent expansion.
func channelMonitorV3ProbeRequest(group *Group, account *Account, model string) ([]byte, http.Header, error) {
	headers := http.Header{"Content-Type": {"application/json"}, "User-Agent": {"Sub2API-Availability-Probe"}}
	claudeCodeProfile := group.Platform == PlatformAnthropic && group.ClaudeCodeOnly && account.Type == AccountTypeAPIKey
	if !claudeCodeProfile {
		return channelMonitorV3ProbeBody(group.Platform, model), headers, nil
	}
	payload, err := createTestPayload(model)
	if err != nil {
		return nil, nil, err
	}
	payload["max_tokens"] = 1
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	for key, value := range claude.DefaultHeaders() {
		headers.Set(key, value)
	}
	return body, headers, nil
}

func channelMonitorV3ProbeBody(platform, model string) []byte {
	prompt := "Reply with 1."
	if platform == PlatformAnthropic {
		prompt = "Hi"
	}
	payload := map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": prompt}}, "stream": true, "max_tokens": 1}
	if domain.UsesOpenAIGateway(platform) {
		delete(payload, "max_tokens")
		payload["max_completion_tokens"] = 16
		payload["reasoning_effort"] = "none"
	}
	body, _ := json.Marshal(payload)
	return body
}
