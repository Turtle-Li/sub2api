package service

import (
	"regexp"
	"strings"
	"time"
)

// Window bounds for the model-degradation audit feed. The default matches the
// operational question the manager asks ("who degraded since my last pass");
// the ceiling keeps one request from aggregating an unbounded usage_logs slice.
const (
	DegradedAccountsDefaultWindow = 30 * time.Minute
	DegradedAccountsMinWindow     = time.Minute
	DegradedAccountsMaxWindow     = 6 * time.Hour

	// Untrusted query input: "1h30m" is 5 bytes, so anything past this is
	// either a mistake or an attempt to make ParseDuration work hard.
	degradedAccountsWindowMaxLength = 16
)

// DegradedAccount is one (account, model pair) group that answered with a
// different model than the one sent, after naming variants are filtered out.
type DegradedAccount struct {
	AccountID      int64     `json:"account_id"`
	AccountName    string    `json:"account_name"`
	RequestedModel string    `json:"requested_model"`
	SentModel      string    `json:"sent_model"`
	ResponseModel  string    `json:"response_model"`
	Count          int64     `json:"count"`
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
	TTFTAvgMs      *float64  `json:"ttft_avg_ms"`
}

var (
	modelVariantLatestSuffix      = regexp.MustCompile(`-latest$`)
	modelVariantISODateSuffix     = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)
	modelVariantCompactDateSuffix = regexp.MustCompile(`-\d{8}$`)
)

// ParseDegradedAccountsWindow reads the ?window= query value. Garbage and
// non-positive input are rejected; a valid duration is clamped into the
// allowed range rather than rejected.
func ParseDegradedAccountsWindow(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DegradedAccountsDefaultWindow, true
	}
	if len(raw) > degradedAccountsWindowMaxLength {
		return 0, false
	}
	window, err := time.ParseDuration(raw)
	if err != nil || window <= 0 {
		return 0, false
	}
	return ClampDegradedAccountsWindow(window), true
}

// ClampDegradedAccountsWindow bounds an already-parsed window. The query path
// calls it again rather than trusting its HTTP caller.
func ClampDegradedAccountsWindow(window time.Duration) time.Duration {
	if window < DegradedAccountsMinWindow {
		return DegradedAccountsMinWindow
	}
	if window > DegradedAccountsMaxWindow {
		return DegradedAccountsMaxWindow
	}
	return window
}

// NormalizeModelVariant strips the naming-variant suffixes that make one model
// look like two: a date stamp or "-latest". It is used for query-time reporting
// only and must not change upstream_model_mismatch audit semantics.
func NormalizeModelVariant(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	// Chained, not exclusive: "gpt-6-astra-latest" must lose "-latest" before
	// the date patterns get a chance to look at what is left.
	normalized = modelVariantLatestSuffix.ReplaceAllString(normalized, "")
	normalized = modelVariantISODateSuffix.ReplaceAllString(normalized, "")
	normalized = modelVariantCompactDateSuffix.ReplaceAllString(normalized, "")
	return normalized
}

// ModelDegradationSuspected reports whether a persisted mismatch is a genuine
// model change rather than a naming variant of the sent model.
func ModelDegradationSuspected(sentModel, responseModel string) bool {
	sentModel = strings.TrimSpace(sentModel)
	responseModel = strings.TrimSpace(responseModel)
	if sentModel == "" || responseModel == "" {
		return false
	}
	// Clear historical Grok runtime-build aliases using the existing raw-audit
	// comparison without altering how that audit column is written.
	if upstreamModelsMatchForAudit(sentModel, responseModel) {
		return false
	}
	return NormalizeModelVariant(sentModel) != NormalizeModelVariant(responseModel)
}
