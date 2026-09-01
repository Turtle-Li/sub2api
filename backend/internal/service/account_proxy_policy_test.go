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
