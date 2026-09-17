package service

import (
	"encoding/json"
	"testing"
)

// Verifies the exact JSONB payload the runtime harvester writes straight to
// accounts.extra is understood by the gateway-side parser.
func TestPinnedPayloadWrittenByHarvesterIsParsed(t *testing.T) {
	const raw = `{"gpt-6-astra":{"state":"AAAAAA","state_len":292,"expires_at":"2126-09-17T13:44:30+00:00","updated_at":"2026-09-17T12:44:31+00:00"}}`
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	acct := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{PinnedCodexTurnStatesExtraKey: decoded},
	}
	if !acct.IsOpenAIOAuthLike() {
		t.Fatalf("test account is not recognised as OpenAI OAuth")
	}
	entries := acct.GetPinnedCodexTurnStates()
	e, ok := entries["gpt-6-astra"]
	if !ok {
		t.Fatalf("entry missing, got %#v", entries)
	}
	if e.StateLen != 292 {
		t.Fatalf("state_len = %d, want 292", e.StateLen)
	}
	if e.ExpiresAt == nil || e.ExpiresAt.Year() != 2126 {
		t.Fatalf("expires_at not parsed: %#v", e.ExpiresAt)
	}
	if e.UpdatedAt == nil {
		t.Fatalf("updated_at not parsed")
	}
	if got := acct.GetPinnedCodexTurnState("gpt-6-astra"); got != "AAAAAA" {
		t.Fatalf("GetPinnedCodexTurnState = %q, want AAAAAA", got)
	}
	// snapshot alias must still hit, cross-model must not
	if got := acct.GetPinnedCodexTurnState("gpt-6-astra-20260301"); got != "AAAAAA" {
		t.Fatalf("snapshot alias = %q, want AAAAAA", got)
	}
	for _, m := range []string{"gpt-6", "gpt-6-astra-mini", "gpt-5-codex"} {
		if got := acct.GetPinnedCodexTurnState(m); got != "" {
			t.Fatalf("model %q leaked pinned state %q", m, got)
		}
	}
}
