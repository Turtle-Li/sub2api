//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type fixedEgressUpdateProxyRepoStub struct {
	*proxyRepoStub
	proxy        *Proxy
	summaries    []ProxyAccountSummary
	summaryErr   error
	summaryCalls int
	updateCalls  int
}

type fixedEgressCreateAccountRepoStub struct {
	accountRepoStub
	created     *Account
	createCalls int
}

type fixedEgressAccountUpdateRepoStub struct {
	accountRepoStub
	account     *Account
	updateCalls int
}

func (s *fixedEgressAccountUpdateRepoStub) GetByID(context.Context, int64) (*Account, error) {
	copy := *s.account
	return &copy, nil
}

func (s *fixedEgressAccountUpdateRepoStub) Update(context.Context, *Account) error {
	s.updateCalls++
	return nil
}

func (s *fixedEgressCreateAccountRepoStub) Create(_ context.Context, account *Account) error {
	s.createCalls++
	copy := *account
	copy.ID = 123
	s.created = &copy
	account.ID = copy.ID
	return nil
}

func (s *fixedEgressUpdateProxyRepoStub) GetByID(context.Context, int64) (*Proxy, error) {
	copy := *s.proxy
	return &copy, nil
}

func (s *fixedEgressUpdateProxyRepoStub) Update(_ context.Context, proxy *Proxy) error {
	s.updateCalls++
	copy := *proxy
	s.proxy = &copy
	return nil
}

func (s *fixedEgressUpdateProxyRepoStub) ListAccountSummariesByProxyID(context.Context, int64) ([]ProxyAccountSummary, error) {
	s.summaryCalls++
	if s.summaryErr != nil {
		return nil, s.summaryErr
	}
	return append([]ProxyAccountSummary(nil), s.summaries...), nil
}

func fixedEgressOAuthParentSummary(id int64) ProxyAccountSummary {
	return ProxyAccountSummary{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
}

func fixedEgressSetupTokenParentSummary(id int64) ProxyAccountSummary {
	return ProxyAccountSummary{ID: id, Platform: PlatformOpenAI, Type: AccountTypeSetupToken}
}

func newFixedEgressUpdateProxy(id int64) *Proxy {
	return &Proxy{
		ID:             id,
		Name:           "fixed-egress",
		Protocol:       "socks5h",
		Host:           "100.80.10.114",
		Port:           1080,
		Status:         StatusActive,
		FallbackMode:   FallbackModeNone,
		ExpiryWarnDays: 7,
	}
}

func newFixedEgressUpdateInput() *UpdateProxyInput {
	return &UpdateProxyInput{
		Protocol:       "socks5h",
		Host:           "100.80.10.114",
		Port:           1080,
		Status:         StatusActive,
		FallbackMode:   FallbackModeNone,
		ExpiryWarnDays: 7,
	}
}

func TestUpdateProxy_RejectsProtectedChangesForActiveOpenAIOAuthParentBinding(t *testing.T) {
	proxyID := int64(7)
	backupProxyID := int64(8)
	expiresAt := time.Now().Add(time.Hour)

	tests := []struct {
		name   string
		mutate func(*UpdateProxyInput)
	}{
		{name: "host", mutate: func(input *UpdateProxyInput) { input.Host = "100.80.10.115" }},
		{name: "protocol", mutate: func(input *UpdateProxyInput) { input.Protocol = "http" }},
		{name: "port", mutate: func(input *UpdateProxyInput) { input.Port = 1081 }},
		{name: "username", mutate: func(input *UpdateProxyInput) { input.Username = "operator" }},
		{name: "password", mutate: func(input *UpdateProxyInput) { input.Password = "secret" }},
		{name: "expiry", mutate: func(input *UpdateProxyInput) { input.ExpiresAt = &expiresAt }},
		{name: "fallback mode", mutate: func(input *UpdateProxyInput) { input.FallbackMode = FallbackModeDirect }},
		{name: "backup proxy", mutate: func(input *UpdateProxyInput) {
			input.FallbackMode = FallbackModeProxy
			input.BackupProxyID = &backupProxyID
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fixedEgressUpdateProxyRepoStub{
				proxyRepoStub: &proxyRepoStub{},
				proxy:         newFixedEgressUpdateProxy(proxyID),
				summaries:     []ProxyAccountSummary{fixedEgressOAuthParentSummary(88)},
			}
			svc := &adminServiceImpl{proxyRepo: repo}
			input := newFixedEgressUpdateInput()
			tt.mutate(input)

			_, err := svc.UpdateProxy(context.Background(), proxyID, input)

			require.Error(t, err)
			require.Equal(t, "FIXED_EGRESS_PROXY_IDENTITY_IMMUTABLE", infraerrors.Reason(err))
			require.Zero(t, repo.updateCalls)
			require.Equal(t, 1, repo.summaryCalls)
		})
	}
}

func TestUpdateProxy_AllowsProtectedChangeWhenNoOpenAIOAuthParentIsBound(t *testing.T) {
	proxyID := int64(7)
	repo := &fixedEgressUpdateProxyRepoStub{
		proxyRepoStub: &proxyRepoStub{},
		proxy:         newFixedEgressUpdateProxy(proxyID),
		summaries: []ProxyAccountSummary{{
			ID:              88,
			Platform:        PlatformOpenAI,
			Type:            AccountTypeOAuth,
			ParentAccountID: int64Ptr(77),
		}},
	}
	svc := &adminServiceImpl{proxyRepo: repo}
	input := newFixedEgressUpdateInput()
	input.Host = "proxy.example"

	updated, err := svc.UpdateProxy(context.Background(), proxyID, input)

	require.NoError(t, err)
	require.Equal(t, "proxy.example", updated.Host)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, 1, repo.summaryCalls)
}

func TestUpdateProxy_IgnoresOAuthShadowOnlyBinding(t *testing.T) {
	proxyID := int64(7)
	parentID := int64(88)
	repo := &fixedEgressUpdateProxyRepoStub{
		proxyRepoStub: &proxyRepoStub{},
		proxy:         newFixedEgressUpdateProxy(proxyID),
		summaries: []ProxyAccountSummary{{
			ID:              89,
			Platform:        PlatformOpenAI,
			Type:            AccountTypeOAuth,
			ParentAccountID: &parentID,
		}},
	}
	svc := &adminServiceImpl{proxyRepo: repo}
	input := newFixedEgressUpdateInput()
	input.Host = "proxy.example"

	updated, err := svc.UpdateProxy(context.Background(), proxyID, input)

	require.NoError(t, err)
	require.Equal(t, "proxy.example", updated.Host)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, 1, repo.summaryCalls)
}

func TestUpdateProxy_AllowsBoundParentDisplayAndMonitoringChanges(t *testing.T) {
	proxyID := int64(7)
	repo := &fixedEgressUpdateProxyRepoStub{
		proxyRepoStub: &proxyRepoStub{},
		proxy:         newFixedEgressUpdateProxy(proxyID),
		summaries:     []ProxyAccountSummary{fixedEgressOAuthParentSummary(88)},
	}
	svc := &adminServiceImpl{proxyRepo: repo}
	input := newFixedEgressUpdateInput()
	input.Name = "fixed-egress-renamed"
	input.ExpiryWarnDays = 14

	updated, err := svc.UpdateProxy(context.Background(), proxyID, input)

	require.NoError(t, err)
	require.Equal(t, "fixed-egress-renamed", updated.Name)
	require.Equal(t, 14, updated.ExpiryWarnDays)
	require.Equal(t, 1, repo.updateCalls)
	require.Zero(t, repo.summaryCalls, "non-identity updates must not need the binding lookup")
}

func TestUpdateProxy_FailsClosedWhenOpenAIOAuthParentLookupFails(t *testing.T) {
	proxyID := int64(7)
	repo := &fixedEgressUpdateProxyRepoStub{
		proxyRepoStub: &proxyRepoStub{},
		proxy:         newFixedEgressUpdateProxy(proxyID),
		summaryErr:    errors.New("database unavailable"),
	}
	svc := &adminServiceImpl{proxyRepo: repo}
	input := newFixedEgressUpdateInput()
	input.Host = "100.80.10.115"

	_, err := svc.UpdateProxy(context.Background(), proxyID, input)

	require.Error(t, err)
	require.Zero(t, repo.updateCalls)
	require.Equal(t, 1, repo.summaryCalls)
}

func TestProxyService_RejectsProtectedChangesForActiveOpenAIOAuthParentBinding(t *testing.T) {
	proxyID := int64(7)
	repo := &fixedEgressUpdateProxyRepoStub{
		proxyRepoStub: &proxyRepoStub{},
		proxy:         newFixedEgressUpdateProxy(proxyID),
		summaries:     []ProxyAccountSummary{fixedEgressOAuthParentSummary(88)},
	}
	svc := NewProxyService(repo)
	host := "100.80.10.115"

	_, err := svc.Update(context.Background(), proxyID, UpdateProxyRequest{Host: &host})

	require.Error(t, err)
	require.Equal(t, "FIXED_EGRESS_PROXY_IDENTITY_IMMUTABLE", infraerrors.Reason(err))
	require.Zero(t, repo.updateCalls)
	require.Equal(t, 1, repo.summaryCalls)
}

func TestProxyService_FailsClosedWhenBindingLookupFails(t *testing.T) {
	proxyID := int64(7)
	repo := &fixedEgressUpdateProxyRepoStub{
		proxyRepoStub: &proxyRepoStub{},
		proxy:         newFixedEgressUpdateProxy(proxyID),
		summaryErr:    errors.New("database unavailable"),
	}
	svc := NewProxyService(repo)
	host := "100.80.10.115"

	_, err := svc.Update(context.Background(), proxyID, UpdateProxyRequest{Host: &host})

	require.Error(t, err)
	require.Zero(t, repo.updateCalls)
	require.Equal(t, 1, repo.summaryCalls)
}

func TestUpdateProxy_RejectsProtectedChangesForDisabledOrErrorOpenAIOAuthParentBinding(t *testing.T) {
	proxyID := int64(7)
	// Proxy account summaries deliberately omit status: a disabled/error
	// parent remains a binding and must keep this proxy's identity immutable.
	for _, status := range []string{StatusDisabled, StatusError} {
		t.Run(status, func(t *testing.T) {
			repo := &fixedEgressUpdateProxyRepoStub{
				proxyRepoStub: &proxyRepoStub{},
				proxy:         newFixedEgressUpdateProxy(proxyID),
				summaries:     []ProxyAccountSummary{fixedEgressOAuthParentSummary(88)},
			}
			svc := &adminServiceImpl{proxyRepo: repo}
			input := newFixedEgressUpdateInput()
			input.Host = "100.80.10.115"

			_, err := svc.UpdateProxy(context.Background(), proxyID, input)

			require.Error(t, err)
			require.Equal(t, "FIXED_EGRESS_PROXY_IDENTITY_IMMUTABLE", infraerrors.Reason(err))
			require.Zero(t, repo.updateCalls)
			require.Equal(t, 1, repo.summaryCalls)
		})
	}
}

func TestUpdateProxy_RejectsProtectedChangeForOpenAISetupTokenParentBinding(t *testing.T) {
	proxyID := int64(7)
	repo := &fixedEgressUpdateProxyRepoStub{
		proxyRepoStub: &proxyRepoStub{},
		proxy:         newFixedEgressUpdateProxy(proxyID),
		summaries:     []ProxyAccountSummary{fixedEgressSetupTokenParentSummary(88)},
	}
	svc := &adminServiceImpl{proxyRepo: repo}
	input := newFixedEgressUpdateInput()
	input.Host = "100.80.10.115"

	_, err := svc.UpdateProxy(context.Background(), proxyID, input)

	require.ErrorIs(t, err, ErrFixedEgressProxyIdentityImmutable)
	require.Zero(t, repo.updateCalls)
	require.Equal(t, 1, repo.summaryCalls)
}

func TestAdminServiceCreateAccount_RequiresFixedEgressProxyForOpenAICodexParent(t *testing.T) {
	proxyID := int64(7)
	validProxy := newFixedEgressUpdateProxy(proxyID)

	tests := []struct {
		name      string
		proxy     *Proxy
		wantError string
	}{
		{name: "accepts authenticated hostname proxy", proxy: &Proxy{ID: proxyID, Status: StatusActive, Protocol: "http", Host: "proxy.example", Port: 8080, Username: "operator", Password: "secret", FallbackMode: FallbackModeNone}},
		{name: "accepts IPv6 proxy", proxy: &Proxy{ID: proxyID, Status: StatusActive, Protocol: "socks5h", Host: "2001:db8::10", Port: 1080, FallbackMode: FallbackModeNone}},
		{name: "rejects unsupported protocol", proxy: &Proxy{ID: proxyID, Status: StatusActive, Protocol: "ftp", Host: "proxy.example", Port: 21, FallbackMode: FallbackModeNone}, wantError: "FIXED_EGRESS_PROXY_INVALID"},
		{name: "accepts valid proxy", proxy: validProxy},
	}

	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					accounts := &fixedEgressCreateAccountRepoStub{}
					proxies := &fixedEgressUpdateProxyRepoStub{proxyRepoStub: &proxyRepoStub{}, proxy: tt.proxy}
					svc := &adminServiceImpl{accountRepo: accounts, proxyRepo: proxies}

					created, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
						Name:                 "codex-parent",
						Platform:             PlatformOpenAI,
						Type:                 accountType,
						ProxyID:              &proxyID,
						SkipDefaultGroupBind: true,
					})

					if tt.wantError != "" {
						require.Error(t, err)
						require.Equal(t, tt.wantError, infraerrors.Reason(err))
						require.Nil(t, created)
						require.Zero(t, accounts.createCalls)
						return
					}
					require.NoError(t, err)
					require.NotNil(t, created)
					require.Equal(t, proxyID, *accounts.created.ProxyID)
					require.Equal(t, 1, accounts.createCalls)
				})
			}
		})
	}
}

func TestOpenAIOAuthServiceResolveProxyURL_RequiresFixedEgress(t *testing.T) {
	proxyID := int64(7)
	valid := newFixedEgressUpdateProxy(proxyID)
	invalid := *valid
	invalid.Protocol = "ftp"

	for _, tt := range []struct {
		name      string
		proxy     *Proxy
		wantError bool
	}{
		{name: "valid", proxy: valid},
		{name: "invalid", proxy: &invalid, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewOpenAIOAuthService(&proxyRepoStub{getByID: map[int64]*Proxy{proxyID: tt.proxy}}, nil)
			defer svc.Stop()

			proxyURL, err := svc.ResolveProxyURL(context.Background(), &proxyID)

			if tt.wantError {
				require.Error(t, err)
				require.Empty(t, proxyURL)
				require.Equal(t, AccountProxyUnavailableReason, infraerrors.Reason(err))
				return
			}
			require.NoError(t, err)
			require.Equal(t, "socks5h://100.80.10.114:1080", proxyURL)
		})
	}
}

func TestAccountServiceUpdate_RequiresCASForActiveOpenAIOAuthParentProxyChange(t *testing.T) {
	currentProxyID := int64(7)
	newProxyID := int64(8)
	repo := &fixedEgressAccountUpdateRepoStub{account: &Account{
		ID:       41,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		ProxyID:  &currentProxyID,
	}}

	_, err := (&AccountService{accountRepo: repo}).Update(context.Background(), 41, UpdateAccountRequest{ProxyID: &newProxyID})

	require.Error(t, err)
	require.Equal(t, "FIXED_EGRESS_CAS_REQUIRED", infraerrors.Reason(err))
	require.Zero(t, repo.updateCalls)
}

func TestAccountServiceUpdate_RequiresCASForActiveOpenAISetupTokenParentProxyChange(t *testing.T) {
	currentProxyID := int64(7)
	newProxyID := int64(8)
	repo := &fixedEgressAccountUpdateRepoStub{account: &Account{
		ID:       41,
		Platform: PlatformOpenAI,
		Type:     AccountTypeSetupToken,
		Status:   StatusActive,
		ProxyID:  &currentProxyID,
	}}

	_, err := (&AccountService{accountRepo: repo}).Update(context.Background(), 41, UpdateAccountRequest{ProxyID: &newProxyID})

	require.ErrorIs(t, err, ErrFixedEgressCASRequired)
	require.Zero(t, repo.updateCalls)
}
