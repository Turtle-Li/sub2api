package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type crsProxyCreateCaptureRepo struct {
	ProxyRepository
	created *Proxy
}

func (r *crsProxyCreateCaptureRepo) Create(_ context.Context, proxy *Proxy) error {
	r.created = proxy
	proxy.ID = 99
	return nil
}

func TestMapOrCreateProxyNewProxyDefaultsToNoFallback(t *testing.T) {
	repo := &crsProxyCreateCaptureRepo{}
	service := &CRSSyncService{proxyRepo: repo}
	cached := []Proxy{}

	_, err := service.mapOrCreateProxy(context.Background(), true, &cached, &crsProxy{
		Protocol: "socks5h",
		Host:     "100.81.60.44",
		Port:     1080,
	}, "crs-openai")

	require.NoError(t, err)
	require.NotNil(t, repo.created)
	require.Equal(t, FallbackModeNone, repo.created.FallbackMode)
	require.Nil(t, repo.created.BackupProxyID)
	require.NoError(t, ValidateFixedEgressProxy(repo.created))
	require.Len(t, cached, 1)
	require.Equal(t, FallbackModeNone, cached[0].FallbackMode)
}
