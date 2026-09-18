package service

import (
	"strings"
	"time"
)

// Window bounds for the model-degradation audit feed. The default matches the
// operational question the probe daemon asks ("who degraded since my last
// pass"); the ceiling keeps one probe from aggregating an unbounded slice of
// usage_logs.
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

// ParseDegradedAccountsWindow reads the ?window= query value. Garbage and
// non-positive input are rejected (ok == false, meaning HTTP 400); a valid
// duration is clamped into [DegradedAccountsMinWindow, DegradedAccountsMaxWindow]
// rather than rejected, so an over-eager caller gets data instead of an error.
func ParseDegradedAccountsWindow(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DegradedAccountsDefaultWindow, true
	}
	if len(raw) > degradedAccountsWindowMaxLength {
		return 0, false
	}
	// ParseDuration already rejects "30", "abc" and "30m; DROP TABLE ...".
	window, err := time.ParseDuration(raw)
	if err != nil {
		return 0, false
	}
	if window <= 0 {
		return 0, false
	}
	return ClampDegradedAccountsWindow(window), true
}

// ClampDegradedAccountsWindow bounds an already-parsed window. Exported so the
// query layer can re-clamp defensively without trusting its caller.
func ClampDegradedAccountsWindow(window time.Duration) time.Duration {
	if window < DegradedAccountsMinWindow {
		return DegradedAccountsMinWindow
	}
	if window > DegradedAccountsMaxWindow {
		return DegradedAccountsMaxWindow
	}
	return window
}
