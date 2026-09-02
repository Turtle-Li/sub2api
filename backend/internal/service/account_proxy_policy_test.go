package service

import (
	"context"
	"errors"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type accountProxyLookupStub struct {
	proxy *Proxy
	err   error
}

func (s accountProxyLookupStub) GetByID(context.Context, int64) (*Proxy, error) {
	return s.proxy, s.err
}

func TestResolveAccountProxyURL_DirectOnlyWithoutProxyID(t *testing.T) {
	proxyURL, err := ResolveAccountProxyURL(&Account{ID: 1})
	require.NoError(t, err)
	require.Empty(t, proxyURL)
}

func TestResolveAccountProxyURL_NilAccountFailsClosed(t *testing.T) {
	proxyURL, err := ResolveAccountProxyURL(nil)
	require.Error(t, err)
	require.Empty(t, proxyURL)
	require.Equal(t, AccountProxyUnavailableReason, infraerrors.Reason(err))
}

func TestResolveAccountProxyURL_FailsClosedForBrokenBindings(t *testing.T) {
	now := time.Now()
	expiredAt := now.Add(-time.Minute)
	proxyID := int64(7)

	tests := []struct {
		name    string
		account *Account
	}{
		{name: "missing relation", account: &Account{ID: 1, ProxyID: &proxyID}},
		{name: "mismatched relation", account: &Account{ID: 1, ProxyID: &proxyID, Proxy: &Proxy{ID: 8, Status: StatusActive}}},
		{name: "inactive proxy", account: &Account{ID: 1, ProxyID: &proxyID, Proxy: &Proxy{ID: proxyID, Status: "disabled"}}},
		{name: "expired proxy", account: &Account{ID: 1, ProxyID: &proxyID, Proxy: &Proxy{ID: proxyID, Status: StatusActive, ExpiresAt: &expiredAt}}},
		{name: "invalid URL", account: &Account{ID: 1, ProxyID: &proxyID, Proxy: &Proxy{ID: proxyID, Status: StatusActive, Protocol: "ftp", Host: "proxy.test", Port: 21}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxyURL, err := resolveAccountProxyURLAt(tt.account, nil, now)
			require.Error(t, err)
			require.Empty(t, proxyURL)
			require.Equal(t, AccountProxyUnavailableReason, infraerrors.Reason(err))
		})
	}
}

func TestResolveAccountProxyURL_NormalizesValidSOCKSProxy(t *testing.T) {
	proxyID := int64(7)
	proxyURL, err := ResolveAccountProxyURL(&Account{
		ID:      1,
		ProxyID: &proxyID,
		Proxy: &Proxy{
			ID:       proxyID,
			Status:   StatusActive,
			Protocol: "socks5",
			Host:     "100.64.0.7",
			Port:     1080,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "socks5h://100.64.0.7:1080", proxyURL)
}

func TestResolveAccountProxyURLWithLookup_FailsClosedOnLookupError(t *testing.T) {
	proxyID := int64(7)
	proxyURL, err := ResolveAccountProxyURLWithLookup(
		context.Background(),
		&Account{ID: 1, ProxyID: &proxyID},
		accountProxyLookupStub{err: errors.New("database unavailable")},
	)
	require.Error(t, err)
	require.Empty(t, proxyURL)
	require.Equal(t, AccountProxyUnavailableReason, infraerrors.Reason(err))
}

func TestResolveAccountProxyURLWithLookup_FailsClosedOnMissingOrMismatchedResult(t *testing.T) {
	proxyID := int64(7)
	for name, proxy := range map[string]*Proxy{
		"missing":    nil,
		"mismatched": {ID: 8, Status: StatusActive, Protocol: "http", Host: "proxy.test", Port: 8080},
	} {
		t.Run(name, func(t *testing.T) {
			proxyURL, err := ResolveAccountProxyURLWithLookup(
				context.Background(),
				&Account{ID: 1, ProxyID: &proxyID},
				accountProxyLookupStub{proxy: proxy},
			)
			require.Error(t, err)
			require.Empty(t, proxyURL)
			require.Equal(t, AccountProxyUnavailableReason, infraerrors.Reason(err))
		})
	}
}

func TestResolveAccountProxyURLWithLookup_UsesHydratedProxy(t *testing.T) {
	proxyID := int64(7)
	proxyURL, err := ResolveAccountProxyURLWithLookup(
		context.Background(),
		&Account{ID: 1, ProxyID: &proxyID},
		accountProxyLookupStub{proxy: &Proxy{
			ID:       proxyID,
			Status:   StatusActive,
			Protocol: "http",
			Host:     "proxy.test",
			Port:     8080,
		}},
	)
	require.NoError(t, err)
	require.Equal(t, "http://proxy.test:8080", proxyURL)
}

func TestResolveAccountProxyURL_OpenAIOAuthRequiresFixedEgress(t *testing.T) {
	now := time.Now()
	proxyID := int64(7)
	parentID := int64(1)

	newValidProxy := func() *Proxy {
		return &Proxy{
			ID:           proxyID,
			Status:       StatusActive,
			Protocol:     "socks5h",
			Host:         "100.80.10.114",
			Port:         1080,
			FallbackMode: FallbackModeNone,
		}
	}
	newOAuthAccount := func(shadow bool) *Account {
		account := &Account{
			ID:       9,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			ProxyID:  &proxyID,
		}
		if shadow {
			account.ParentAccountID = &parentID
		}
		return account
	}

	for _, shadow := range []bool{false, true} {
		name := "parent"
		if shadow {
			name = "shadow"
		}
		t.Run("valid "+name+" binding", func(t *testing.T) {
			proxyURL, err := resolveAccountProxyURLAt(newOAuthAccount(shadow), newValidProxy(), now)
			require.NoError(t, err)
			require.Equal(t, "socks5h://100.80.10.114:1080", proxyURL)
		})
	}

	backupProxyID := int64(8)
	expiresAt := now.Add(time.Hour)
	tests := []struct {
		name   string
		mutate func(*Proxy)
	}{
		{name: "inactive", mutate: func(proxy *Proxy) { proxy.Status = "inactive" }},
		{name: "non tailnet host", mutate: func(proxy *Proxy) { proxy.Host = "127.0.0.1" }},
		{name: "non socks5h protocol", mutate: func(proxy *Proxy) { proxy.Protocol = "socks5" }},
		{name: "wrong port", mutate: func(proxy *Proxy) { proxy.Port = 1081 }},
		{name: "authenticated", mutate: func(proxy *Proxy) { proxy.Username = "operator" }},
		{name: "expiry", mutate: func(proxy *Proxy) { proxy.ExpiresAt = &expiresAt }},
		{name: "fallback", mutate: func(proxy *Proxy) { proxy.FallbackMode = FallbackModeDirect }},
		{name: "backup", mutate: func(proxy *Proxy) { proxy.BackupProxyID = &backupProxyID }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := newValidProxy()
			tt.mutate(proxy)

			proxyURL, err := resolveAccountProxyURLAt(newOAuthAccount(false), proxy, now)
			require.Error(t, err)
			require.Empty(t, proxyURL)
			require.Equal(t, AccountProxyUnavailableReason, infraerrors.Reason(err))
		})
	}
}

func TestResolveAccountProxyURLWithLookup_OpenAIOAuthFailsClosedForInvalidFixedEgress(t *testing.T) {
	proxyID := int64(7)
	proxyURL, err := ResolveAccountProxyURLWithLookup(
		context.Background(),
		&Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, ProxyID: &proxyID},
		accountProxyLookupStub{proxy: &Proxy{
			ID:           proxyID,
			Status:       StatusActive,
			Protocol:     "socks5h",
			Host:         "proxy.internal",
			Port:         1080,
			FallbackMode: FallbackModeNone,
		}},
	)
	require.Error(t, err)
	require.Empty(t, proxyURL)
	require.Equal(t, AccountProxyUnavailableReason, infraerrors.Reason(err))
}

func TestResolveAccountProxyURL_OpenAISetupTokenSharesFixedEgressInvariant(t *testing.T) {
	proxyID := int64(7)
	account := &Account{
		ID:       2,
		Platform: PlatformOpenAI,
		Type:     AccountTypeSetupToken,
		Status:   StatusActive,
		ProxyID:  &proxyID,
	}
	valid := &Proxy{
		ID:           proxyID,
		Status:       StatusActive,
		Protocol:     "socks5h",
		Host:         "100.81.60.44",
		Port:         1080,
		FallbackMode: FallbackModeNone,
	}

	proxyURL, err := resolveAccountProxyURLAt(account, valid, time.Now())
	require.NoError(t, err)
	require.Equal(t, "socks5h://100.81.60.44:1080", proxyURL)

	invalid := *valid
	invalid.Host = "203.0.113.10"
	proxyURL, err = resolveAccountProxyURLAt(account, &invalid, time.Now())
	require.ErrorIs(t, err, ErrAccountProxyUnavailable)
	require.Empty(t, proxyURL)
	require.Contains(t, err.Error(), "outside Tailnet")
}
