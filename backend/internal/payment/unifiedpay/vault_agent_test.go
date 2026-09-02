package unifiedpay

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testRequestPrivateKeyVaultRef = "vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64"

type vaultAgentRoundTripFunc func(*http.Request) (*http.Response, error)

func (function vaultAgentRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestLoadVaultEd25519PrivateKeyUsesExactAgentRequest(t *testing.T) {
	privateKey := testPrivateKey()
	encoded := base64.StdEncoding.EncodeToString(privateKey)
	client := &http.Client{Transport: vaultAgentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, request.Method)
		require.Equal(t, "http://vault/v1/secret/data/sub2api/unified-payment/sandbox", request.URL.String())
		require.Equal(t, "application/json", request.Header.Get("Accept"))
		require.Empty(t, request.Header.Get("Authorization"))
		require.Empty(t, request.Header.Get("Cookie"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":{"data":{"request_private_key_base64":"` + encoded + `"}}}`)),
			Request:    request,
		}, nil
	})}

	loaded, err := loadVaultEd25519PrivateKey(context.Background(), vaultAgentSocketPath, testRequestPrivateKeyVaultRef, client)
	require.NoError(t, err)
	require.Equal(t, []byte(privateKey), []byte(loaded))
	clear(loaded)
}

func TestLoadVaultEd25519PrivateKeyFailsClosed(t *testing.T) {
	privateKey := testPrivateKey()
	valid := base64.StdEncoding.EncodeToString(privateKey)
	tests := []struct {
		name      string
		socket    string
		reference string
		status    int
		body      string
	}{
		{name: "wrong socket", socket: "/tmp/vault.sock", reference: testRequestPrivateKeyVaultRef, status: 200, body: `{"data":{"data":{"request_private_key_base64":"` + valid + `"}}}`},
		{name: "path traversal", socket: vaultAgentSocketPath, reference: "vault://secret/data/../sandbox#request_private_key_base64", status: 200, body: `{}`},
		{name: "agent locked", socket: vaultAgentSocketPath, reference: testRequestPrivateKeyVaultRef, status: 503, body: `{}`},
		{name: "extra field", socket: vaultAgentSocketPath, reference: testRequestPrivateKeyVaultRef, status: 200, body: `{"data":{"data":{"request_private_key_base64":"` + valid + `","other":"value"}}}`},
		{name: "duplicate field", socket: vaultAgentSocketPath, reference: testRequestPrivateKeyVaultRef, status: 200, body: `{"data":{"data":{"request_private_key_base64":"` + valid + `","request_private_key_base64":"` + valid + `"}}}`},
		{name: "malformed private key", socket: vaultAgentSocketPath, reference: testRequestPrivateKeyVaultRef, status: 200, body: `{"data":{"data":{"request_private_key_base64":"` + base64.StdEncoding.EncodeToString(make([]byte, ed25519.PrivateKeySize)) + `"}}}`},
		{name: "oversized", socket: vaultAgentSocketPath, reference: testRequestPrivateKeyVaultRef, status: 200, body: strings.Repeat("x", maximumVaultAgentBody+1)},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client := &http.Client{Transport: vaultAgentRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: testCase.status, Body: io.NopCloser(strings.NewReader(testCase.body)), Request: request}, nil
			})}
			key, err := loadVaultEd25519PrivateKey(context.Background(), testCase.socket, testCase.reference, client)
			require.ErrorIs(t, err, ErrInvalidConfiguration)
			require.Nil(t, key)
		})
	}
}

func TestVaultAgentSocketIsFixed(t *testing.T) {
	require.True(t, validVaultAgentSocket(vaultAgentSocketPath))
	for _, value := range []string{"", "/tmp/vault.sock", vaultAgentSocketPath + "/extra", "run/sub2api-payment-vault/public.sock"} {
		require.False(t, validVaultAgentSocket(value), value)
	}
}
