package openai

import "strings"

// CodexBillingErrorCodeHeader preserves the Sub2 business classification when
// the Codex-specific response body must be plain text for verbatim display.
const CodexBillingErrorCodeHeader = "X-Sub2-Error-Code"

// IsCodexBillingErrorClientByHeaders uses the strict official-client matcher
// for the narrow response-shape compatibility path. The broad matcher remains
// available to legacy passthrough behavior, where compound User-Agent strings
// are intentionally accepted.
func IsCodexBillingErrorClientByHeaders(userAgent, originator string) bool {
	return IsCodexOfficialClientRequestStrict(userAgent) || IsCodexOfficialClientOriginator(originator)
}

// NormalizeCodexBillingErrorCode reports whether code is one of the local
// billing states that should be shown directly to an official Codex client.
func NormalizeCodexBillingErrorCode(code string) (string, bool) {
	code = strings.TrimSpace(code)
	switch code {
	case "USAGE_LIMIT_EXCEEDED", "INSUFFICIENT_BALANCE":
		return code, true
	default:
		return "", false
	}
}
