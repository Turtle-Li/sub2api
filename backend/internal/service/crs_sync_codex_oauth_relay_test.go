//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type crsCodexRelayAccountRepo struct {
	*crsLongContextAccountRepo
	createCalls int
	updateCalls int
}

func (r *crsCodexRelayAccountRepo) Create(ctx context.Context, account *Account) error {
	r.createCalls++
	return r.crsLongContextAccountRepo.Create(ctx, account)
}

func (r *crsCodexRelayAccountRepo) Update(ctx context.Context, account *Account) error {
	r.updateCalls++
	return r.crsLongContextAccountRepo.Update(ctx, account)
}

func TestCRSSyncCodexOAuthRelayDeclaration(t *testing.T) {
	targets := []struct {
		collection  string
		platform    string
		accountType string
		credentials map[string]any
	}{
		{"openaiResponsesAccounts", PlatformOpenAI, AccountTypeAPIKey, map[string]any{"api_key": "sk-test"}},
		{"openaiOAuthAccounts", PlatformOpenAI, AccountTypeOAuth, map[string]any{"access_token": "oauth-token"}},
		{"claudeAccounts", PlatformAnthropic, AccountTypeOAuth, map[string]any{"access_token": "oauth-token"}},
		{"geminiOAuthAccounts", PlatformGemini, AccountTypeOAuth, map[string]any{"refresh_token": "refresh-token"}},
		{"geminiApiKeyAccounts", PlatformGemini, AccountTypeAPIKey, map[string]any{"api_key": "sk-test"}},
	}
	values := []struct {
		name    string
		present bool
		value   any
		invalid bool
	}{
		{name: "omitted"},
		{name: "enabled", present: true, value: true},
		{name: "disabled", present: true, value: false},
		{name: "string true", present: true, value: "true", invalid: true},
		{name: "string false", present: true, value: "false", invalid: true},
		{name: "number", present: true, value: 1, invalid: true},
		{name: "null", present: true, invalid: true},
		{name: "array", present: true, value: []any{true}, invalid: true},
		{name: "object", present: true, value: map[string]any{"enabled": true}, invalid: true},
	}
	for _, target := range targets {
		for _, update := range []bool{false, true} {
			mode := "create"
			if update {
				mode = "update"
			}
			for _, value := range values {
				t.Run(target.collection+"/"+mode+"/"+value.name, func(t *testing.T) {
					const crsID = "crs-openai-1"
					var existing *Account
					if update {
						existing = &Account{
							ID: 41, Name: "original", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
							Credentials: map[string]any{"api_key": "original"},
							Extra: map[string]any{
								"crs_account_id": crsID, "local_marker": "keep", "remote_marker": "old",
								OpenAICodexOAuthRelayExtraKey: true, openAILongContextBillingEnabledKey: true,
							},
						}
					}
					repo := &crsCodexRelayAccountRepo{crsLongContextAccountRepo: newCRSLongContextAccountRepo(existing)}
					extra := map[string]any{"remote_marker": "new"}
					if value.present {
						extra[OpenAICodexOAuthRelayExtraKey] = value.value
					}
					result := runCRSOpenAILongContextSync(t, repo, crsOpenAILongContextSource{
						collection: target.collection, credentials: target.credentials, extra: extra,
					})
					require.Len(t, result.Items, 1)
					eligible := target.platform == PlatformOpenAI && target.accountType == AccountTypeAPIKey
					unsupportedEnabled := !eligible && value.present && value.value == true
					if value.invalid || unsupportedEnabled {
						require.Equal(t, "failed", result.Items[0].Action)
						require.Equal(t, 1, result.Failed)
						if value.invalid {
							require.Contains(t, result.Items[0].Error, "openai_codex_oauth_relay must be a boolean")
						} else {
							require.Contains(t, result.Items[0].Error, "requires an OpenAI API-key account")
						}
						require.Zero(t, repo.createCalls)
						require.Zero(t, repo.updateCalls)
						if update {
							stored := repo.accounts[crsID]
							require.Equal(t, "original", stored.Name)
							require.Equal(t, PlatformOpenAI, stored.Platform)
							require.Equal(t, AccountTypeAPIKey, stored.Type)
							require.Equal(t, map[string]any{"api_key": "original"}, stored.Credentials)
							require.Equal(t, "old", stored.Extra["remote_marker"])
							require.Equal(t, true, stored.Extra[OpenAICodexOAuthRelayExtraKey])
						} else {
							require.Empty(t, repo.accounts)
						}
						return
					}
					wantAction := "created"
					if update {
						wantAction = "updated"
						require.Equal(t, 1, repo.updateCalls)
					} else {
						require.Equal(t, 1, repo.createCalls)
					}
					require.Equal(t, wantAction, result.Items[0].Action)
					require.Zero(t, result.Failed)
					stored := repo.accounts[crsID]
					require.Equal(t, target.platform, stored.Platform)
					require.Equal(t, target.accountType, stored.Type)
					require.Equal(t, "new", stored.Extra["remote_marker"])
					if update {
						require.Equal(t, "keep", stored.Extra["local_marker"])
					}
					if eligible && (value.present || update) {
						wantEnabled := value.value
						if !value.present {
							wantEnabled = true
						}
						require.Equal(t, wantEnabled, stored.Extra[OpenAICodexOAuthRelayExtraKey])
						require.Equal(t, wantEnabled, stored.IsOpenAICodexOAuthRelay())
					} else {
						require.NotContains(t, stored.Extra, OpenAICodexOAuthRelayExtraKey)
					}
					if target.platform == PlatformOpenAI {
						require.Equal(t, update, stored.Extra[openAILongContextBillingEnabledKey])
					}
				})
			}
		}
	}
}

func TestCRSSyncCodexOAuthRelayTypeRoundTripDoesNotRestoreDeclaration(t *testing.T) {
	for _, transition := range []crsOpenAILongContextSource{
		{collection: "openaiOAuthAccounts", credentials: map[string]any{"access_token": "oauth-token"}},
		{collection: "claudeAccounts", credentials: map[string]any{"access_token": "oauth-token"}},
		{collection: "claudeConsoleAccounts", credentials: map[string]any{"api_key": "sk-test"}},
		{collection: "geminiOAuthAccounts", credentials: map[string]any{"refresh_token": "refresh-token"}},
		{collection: "geminiApiKeyAccounts", credentials: map[string]any{"api_key": "sk-test"}},
	} {
		for _, enabled := range []bool{false, true} {
			name := transition.collection + "/disabled"
			if enabled {
				name = transition.collection + "/enabled"
			}
			t.Run(name, func(t *testing.T) {
				const crsID = "crs-openai-1"
				repo := &crsCodexRelayAccountRepo{crsLongContextAccountRepo: newCRSLongContextAccountRepo(&Account{
					ID: 41, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Extra: map[string]any{"crs_account_id": crsID, OpenAICodexOAuthRelayExtraKey: enabled},
				})}
				first := runCRSOpenAILongContextSync(t, repo, transition)
				require.Equal(t, "updated", first.Items[0].Action)
				require.NotContains(t, repo.accounts[crsID].Extra, OpenAICodexOAuthRelayExtraKey)
				second := runCRSOpenAILongContextSync(t, repo, crsOpenAILongContextSource{
					collection: "openaiResponsesAccounts", credentials: map[string]any{"api_key": "sk-test"},
				})
				require.Equal(t, "updated", second.Items[0].Action)
				require.Equal(t, 2, repo.updateCalls)
				stored := repo.accounts[crsID]
				require.NotContains(t, stored.Extra, OpenAICodexOAuthRelayExtraKey)
				require.False(t, stored.IsOpenAICodexOAuthRelay())
				require.Equal(t, "default", ResolveOpenAIServiceTierBilling(stored, "priority", "default").Billing)
			})
		}
	}
}

func TestCRSSyncCodexOAuthRelayDisabledStaysDisabledOnResync(t *testing.T) {
	repo := &crsCodexRelayAccountRepo{crsLongContextAccountRepo: newCRSLongContextAccountRepo()}
	source := crsOpenAILongContextSource{
		collection: "openaiResponsesAccounts", credentials: map[string]any{"api_key": "sk-test"},
		extra: map[string]any{OpenAICodexOAuthRelayExtraKey: true},
	}
	created := runCRSOpenAILongContextSync(t, repo, source)
	require.Equal(t, "created", created.Items[0].Action)
	require.True(t, repo.accounts["crs-openai-1"].IsOpenAICodexOAuthRelay())

	source.extra[OpenAICodexOAuthRelayExtraKey] = false
	disabled := runCRSOpenAILongContextSync(t, repo, source)
	require.Equal(t, "updated", disabled.Items[0].Action)
	require.Equal(t, false, repo.accounts["crs-openai-1"].Extra[OpenAICodexOAuthRelayExtraKey])

	source.extra = nil
	resynced := runCRSOpenAILongContextSync(t, repo, source)
	require.Equal(t, "updated", resynced.Items[0].Action)
	stored := repo.accounts["crs-openai-1"]
	require.Equal(t, false, stored.Extra[OpenAICodexOAuthRelayExtraKey])
	require.Equal(t, "default", ResolveOpenAIServiceTierBilling(stored, "priority", "default").Billing)
	require.Equal(t, 1, repo.createCalls)
	require.Equal(t, 2, repo.updateCalls)
}

func TestCRSSyncCodexOAuthRelayFailureDoesNotStopOtherAccounts(t *testing.T) {
	repo := &crsCodexRelayAccountRepo{crsLongContextAccountRepo: newCRSLongContextAccountRepo()}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/web/auth/login" {
			_, _ = response.Write([]byte(`{"success":true,"token":"admin-token"}`))
			return
		}
		require.Equal(t, "/admin/sync/export-accounts", request.URL.Path)
		require.NoError(t, json.NewEncoder(response).Encode(map[string]any{
			"success": true,
			"data": map[string]any{"openaiResponsesAccounts": []any{
				map[string]any{
					"id": "invalid", "credentials": map[string]any{"api_key": "sk-invalid"},
					"extra": map[string]any{OpenAICodexOAuthRelayExtraKey: "true"},
				},
				map[string]any{
					"id": "valid", "credentials": map[string]any{"api_key": "sk-valid"},
					"extra": map[string]any{
						OpenAICodexOAuthRelayExtraKey: true, openAILongContextBillingEnabledKey: true,
						UpstreamBillingProbeEnabledExtraKey: true, UpstreamBillingRateSyncEnabledExtraKey: true,
						UpstreamBillingProbeExtraKey: map[string]any{"status": "remote"},
					},
				},
			}},
		}))
	}))
	t.Cleanup(server.Close)
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	service := NewCRSSyncService(repo, nil, nil, nil, nil, cfg)
	result, err := service.SyncFromCRS(context.Background(), SyncFromCRSInput{
		BaseURL: server.URL, Username: "admin", Password: "password",
	})
	require.NoError(t, err)
	require.Len(t, result.Items, 2)
	require.Equal(t, "failed", result.Items[0].Action)
	require.Equal(t, "created", result.Items[1].Action)
	require.Equal(t, 1, result.Failed)
	require.Equal(t, 1, result.Created)
	require.Equal(t, 1, repo.createCalls)
	require.NotContains(t, repo.accounts, "invalid")
	stored := repo.accounts["valid"]
	require.True(t, stored.IsOpenAICodexOAuthRelay())
	require.Equal(t, true, stored.Extra[openAILongContextBillingEnabledKey])
	require.NotContains(t, stored.Extra, UpstreamBillingProbeEnabledExtraKey)
	require.NotContains(t, stored.Extra, UpstreamBillingRateSyncEnabledExtraKey)
	require.NotContains(t, stored.Extra, UpstreamBillingProbeExtraKey)
}
