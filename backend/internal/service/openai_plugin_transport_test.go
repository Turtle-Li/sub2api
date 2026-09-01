package service

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type openAIPluginTransportUpstream struct {
	proxyURLs []string
}

func (u *openAIPluginTransportUpstream) Do(_ *http.Request, proxyURL string, _ int64, _ int) (*http.Response, error) {
	u.proxyURLs = append(u.proxyURLs, proxyURL)
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func (u *openAIPluginTransportUpstream) DoWithTLS(
	request *http.Request,
	proxyURL string,
	accountID int64,
	concurrency int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	return u.Do(request, proxyURL, accountID, concurrency)
}

func TestOpenAIPluginTransportUsesResolvedAccountProxy(t *testing.T) {
	proxyID := int64(7)
	account := &Account{
		ID:          1,
		ProxyID:     &proxyID,
		Concurrency: 1,
		Proxy: &Proxy{
			ID:       proxyID,
			Status:   StatusActive,
			Protocol: "socks5",
			Host:     "100.64.0.7",
			Port:     1080,
		},
	}
	request, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	require.NoError(t, err)

	t.Run("gateway", func(t *testing.T) {
		upstream := &openAIPluginTransportUpstream{}
		service := &OpenAIGatewayService{httpUpstream: upstream}

		response, err := service.doOpenAIUpstream(request, "http://stale-proxy:8080", account)

		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, []string{"socks5h://100.64.0.7:1080"}, upstream.proxyURLs)
	})

	t.Run("account test", func(t *testing.T) {
		upstream := &openAIPluginTransportUpstream{}
		service := &AccountTestService{httpUpstream: upstream}

		response, err := service.doOpenAIAccountTestUpstream(request, "http://stale-proxy:8080", account, false)

		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, []string{"socks5h://100.64.0.7:1080"}, upstream.proxyURLs)
	})

	t.Run("account test TLS fallback", func(t *testing.T) {
		upstream := &openAIPluginTransportUpstream{}
		service := &AccountTestService{httpUpstream: upstream}

		response, err := service.doOpenAIAccountTestUpstream(request, "http://stale-proxy:8080", account, true)

		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, []string{"socks5h://100.64.0.7:1080"}, upstream.proxyURLs)
	})
}
