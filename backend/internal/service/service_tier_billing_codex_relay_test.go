package service

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveOpenAIServiceTierBilling_CodexRelayBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name, requested, observed, want string
		platform, accountType           string
		declaration                     any
	}{
		{"enabled default echo", "priority", "default", "priority", PlatformOpenAI, AccountTypeAPIKey, true},
		{"disabled public downgrade", "priority", "default", "default", PlatformOpenAI, AccountTypeAPIKey, false},
		{"missing public downgrade", "priority", "default", "default", PlatformOpenAI, AccountTypeAPIKey, nil},
		{"malformed declaration ignored", "priority", "default", "default", PlatformOpenAI, AccountTypeAPIKey, "true"},
		{"other platform ignored", "priority", "default", "default", PlatformGrok, AccountTypeAPIKey, true},
		{"other credential ignored", "priority", "default", "default", PlatformOpenAI, AccountTypeUpstream, true},
		{"explicit flex downgrade", "priority", "flex", "flex", PlatformOpenAI, AccountTypeAPIKey, true},
		{"standard response authoritative", "priority", "standard", "standard", PlatformOpenAI, AccountTypeAPIKey, true},
		{"untiered response cannot upgrade", "", "priority", "", PlatformOpenAI, AccountTypeAPIKey, true},
		{"default response cannot upgrade", "default", "priority", "default", PlatformOpenAI, AccountTypeAPIKey, true},
		{"missing observation retained", "priority", "", "priority", PlatformOpenAI, AccountTypeAPIKey, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Given a declaration and independent final outbound/observed tiers.
			account := &Account{Platform: tt.platform, Type: tt.accountType, Extra: map[string]any{OpenAICodexOAuthRelayExtraKey: tt.declaration}}

			// When settling accounting.
			got := ResolveOpenAIServiceTierBilling(account, tt.requested, tt.observed)

			// Then only an opted-in default echo bypasses the generic downgrade.
			require.Equal(t, tt.want, got.Billing)
		})
	}
}

func TestServiceTierBillingDecisionAuditsCodexRelayRetention(t *testing.T) {
	// Given a declared relay and a logger capturing structured output.
	var output bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	account := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "private-test-value"}, Extra: map[string]any{OpenAICodexOAuthRelayExtraKey: true}}
	resolution := ResolveOpenAIServiceTierBilling(account, "priority", "default")

	// When the usage boundary records its decision.
	logServiceTierBillingDecision("service.openai_gateway", account, "req_relay_audit", resolution)

	// Then the event separates all three tiers and explains the exception.
	var event map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &event))
	require.Equal(t, "billing.service_tier_retained", event["msg"])
	require.Equal(t, "req_relay_audit", event["request_id"])
	require.Equal(t, float64(12), event["account_id"])
	require.Equal(t, "priority", event["requested_tier"])
	require.Equal(t, "default", event["response_tier"])
	require.Equal(t, "priority", event["billed_tier"])
	require.Equal(t, "codex_oauth_relay_default_echo", event["policy_reason"])
	require.NotContains(t, output.String(), "private-test-value")
}
