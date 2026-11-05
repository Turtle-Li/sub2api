package service

import infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

// OpenAICodexOAuthRelayExtraKey declares the response-tier contract of an
// API-key relay backed by Codex OAuth. It never changes authentication or requests.
const OpenAICodexOAuthRelayExtraKey = "openai_codex_oauth_relay"

func (a *Account) IsOpenAICodexOAuthRelay() bool {
	if a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey {
		return false
	}
	enabled, ok := a.Extra[OpenAICodexOAuthRelayExtraKey].(bool)
	return ok && enabled
}

// ValidateOpenAICodexOAuthRelayExtra is shared by all administrative writes,
// including imported accounts. Enabling requires an OpenAI API-key credential;
// an explicit false also permits clearing an obsolete declaration.
func ValidateOpenAICodexOAuthRelayExtra(platform, accountType string, extra map[string]any) error {
	raw, present := extra[OpenAICodexOAuthRelayExtraKey]
	if !present {
		return nil
	}
	enabled, ok := raw.(bool)
	if !ok {
		return infraerrors.BadRequest("OPENAI_CODEX_OAUTH_RELAY_INVALID", "openai_codex_oauth_relay must be a boolean")
	}
	if enabled && (platform != PlatformOpenAI || accountType != AccountTypeAPIKey) {
		return infraerrors.BadRequest("OPENAI_CODEX_OAUTH_RELAY_ACCOUNT_INVALID", "Codex OAuth relay compatibility requires an OpenAI API-key account")
	}
	return nil
}
