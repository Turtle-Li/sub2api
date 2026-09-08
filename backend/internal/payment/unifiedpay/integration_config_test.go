package unifiedpay

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestIntegrationConfigAuthenticatesAndRejectsForeignScope(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		private := testPrivateKey()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			verifySignedRequest(t, r, body, private.Public().(ed25519.PublicKey))
			require.Equal(t, "/v1/integration-config", r.RequestURI)
			result := IntegrationConfig{SchemaVersion: "payment-integration.v1", Environment: EnvironmentSandbox, OrganizationID: testOrganizationID, ProductID: testProductID, AppID: testAppID, RequestKeyID: testRequestKeyID, PaymentMethods: []string{"alipay"}, ReturnURLs: []string{"https://sub2.example/result"}, WebhookEndpoints: []IntegrationWebhookEndpoint{{URL: "https://sub2.example/api/v1/payment/webhook/unified", SigningKeyID: testWebhookKeyID}}, WebhookSigningKeys: []IntegrationWebhookKey{{KeyID: testWebhookKeyID, Algorithm: "Ed25519", PublicKey: base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey)), Status: "active"}}}
			if foreign {
				result.ProductID = "33333333-3333-4333-8333-333333333333"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
		}))
		client, err := newClient(testConfig(private, server.URL))
		require.NoError(t, err)
		result, err := client.GetIntegrationConfig(context.Background())
		if foreign {
			require.Error(t, err)
			require.Nil(t, result)
		} else {
			require.NoError(t, err)
			require.Equal(t, testProductID, result.ProductID)
		}
		server.Close()
	}
}
func TestBindingOriginAndDNSDestinations(t *testing.T) {
	raw := config.UnifiedPaymentConfig{BaseURL: "https://pay.example.com", Environment: "live"}
	require.NoError(t, validateBindingOrigin(raw.BaseURL, raw))
	for _, target := range []string{"https://evil.example.com", "https://pay.example.com@127.0.0.1", "https://pay.example.com/v1", "http://pay.example.com", "https://pay.example.com?next=http://127.0.0.1"} {
		require.Error(t, validateBindingOrigin(target, raw))
	}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1", "2001:db8::1", "64:ff9b::7f00:1"} {
		require.False(t, bindingPublicIP(net.ParseIP(address)), address)
	}
	require.True(t, bindingPublicIP(net.ParseIP("1.1.1.1")))
	raw = config.UnifiedPaymentConfig{BaseURL: "http://127.0.0.1:4567", Environment: "sandbox"}
	require.NoError(t, validateBindingOrigin(raw.BaseURL, raw))
	raw.Environment = "live"
	require.Error(t, validateBindingOrigin(raw.BaseURL, raw))
}

func TestIntegrationWebhookURLRequiresPublicDNSHTTPS(t *testing.T) {
	for _, test := range []struct {
		name  string
		raw   string
		valid bool
	}{
		{name: "approved webhook", raw: "https://api.turtleligpt.com/api/v1/payment/webhook/unified", valid: true},
		{name: "explicit https port", raw: "https://receiver.example.com:443/hook", valid: true},
		{name: "ipv4 literal", raw: "https://127.0.0.1/hook"},
		{name: "ipv6 literal", raw: "https://[::1]/hook"},
		{name: "localhost", raw: "https://localhost/hook"},
		{name: "localhost suffix", raw: "https://receiver.localhost/hook"},
		{name: "local suffix", raw: "https://receiver.local/hook"},
		{name: "leading hyphen", raw: "https://-receiver.example.com/hook"},
		{name: "trailing hyphen", raw: "https://receiver-.example.com/hook"},
		{name: "bad label", raw: "https://receiver_name.example.com/hook"},
		{name: "empty label", raw: "https://receiver..example.com/hook"},
		{name: "trailing dot", raw: "https://receiver.example.com./hook"},
		{name: "nonstandard port", raw: "https://receiver.example.com:444/hook"},
		{name: "userinfo", raw: "https://user@receiver.example.com/hook"},
		{name: "fragment", raw: "https://receiver.example.com/hook#fragment"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.valid, validIntegrationURL(test.raw))
		})
	}
}

func TestExpectedWebhookURLUsesConfiguredPublicDestination(t *testing.T) {
	raw := config.UnifiedPaymentConfig{
		ReturnURL:  "https://www.turtleligpt.com/payment/result",
		WebhookURL: "https://api.turtleligpt.com/api/v1/payment/webhook/unified",
	}
	actual, err := ExpectedWebhookURL(raw)
	require.NoError(t, err)
	require.Equal(t, raw.WebhookURL, actual)

	raw.WebhookURL = "https://receiver.local/api/v1/payment/webhook/unified"
	_, err = ExpectedWebhookURL(raw)
	require.ErrorIs(t, err, ErrInvalidConfiguration)
}
