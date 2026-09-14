package openai

import (
	"regexp"
	"strings"
)

// CodexBillingErrorCodeHeader preserves the Sub2 business classification when
// the Codex-specific response body must be plain text for verbatim display.
const CodexBillingErrorCodeHeader = "X-Sub2-Error-Code"

const codexBillingErrorClaudianOriginator = "claudian"

var codexBillingErrorClientVersionPattern = regexp.MustCompile(
	`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9a-z]+(?:[.-][0-9a-z]+)*)?(?:\+[0-9a-z]+(?:[.-][0-9a-z]+)*)?$`,
)

// IsCodexBillingErrorClientByHeaders uses the strict official-client matcher
// plus exact, production-observed Codex-engine clients for this narrow
// response-shape compatibility path. Claudian uses Codex's retry handling but
// identifies itself as claudian, so an ordinary billing 429 hides the Sub2
// message. Keep this exception local to billing responses: it must not grant
// official-client identity or change OAuth/passthrough admission.
func IsCodexBillingErrorClientByHeaders(userAgent, originator string) bool {
	if strings.HasPrefix(normalizeCodexClientHeader(userAgent), codexBillingErrorClaudianOriginator+"/") {
		return isClaudianBillingErrorClient(userAgent, originator)
	}
	if IsCodexOfficialClientRequestStrict(userAgent) || IsCodexOfficialClientOriginator(originator) {
		return true
	}
	return false
}

func isClaudianBillingErrorClient(userAgent, originator string) bool {
	ua := normalizeCodexClientHeader(userAgent)
	if !codexBillingErrorClientVersionPattern.MatchString(CodexUserAgentVersion(ua)) {
		return false
	}

	trailerOpen := strings.LastIndexByte(ua, '(')
	if trailerOpen < 0 {
		return false
	}
	trailerRest := ua[trailerOpen+1:]
	trailerClose := strings.IndexByte(trailerRest, ')')
	if trailerClose < 0 || strings.TrimSpace(trailerRest[trailerClose+1:]) != "" {
		return false
	}
	trailerParts := strings.SplitN(trailerRest[:trailerClose], ";", 2)
	if len(trailerParts) != 2 || strings.TrimSpace(trailerParts[0]) != codexBillingErrorClaudianOriginator ||
		!codexBillingErrorClientVersionPattern.MatchString(strings.TrimSpace(trailerParts[1])) {
		return false
	}

	normalizedOriginator := normalizeCodexClientHeader(originator)
	return normalizedOriginator == "" || normalizedOriginator == codexBillingErrorClaudianOriginator
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
