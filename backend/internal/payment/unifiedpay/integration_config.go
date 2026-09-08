package unifiedpay

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type IntegrationConfig struct {
	SchemaVersion      string                       `json:"schema_version"`
	Environment        Environment                  `json:"environment"`
	OrganizationID     string                       `json:"organization_id"`
	ProductID          string                       `json:"product_id"`
	AppID              string                       `json:"app_id"`
	RequestKeyID       string                       `json:"request_key_id"`
	PaymentMethods     []string                     `json:"payment_methods"`
	ReturnURLs         []string                     `json:"return_urls"`
	WebhookEndpoints   []IntegrationWebhookEndpoint `json:"webhook_endpoints"`
	WebhookSigningKeys []IntegrationWebhookKey      `json:"webhook_signing_keys"`
}

type IntegrationWebhookEndpoint struct {
	URL          string `json:"url"`
	SigningKeyID string `json:"signing_key_id"`
}

type IntegrationWebhookKey struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"public_key"`
	Status    string `json:"status"`
}

// GetIntegrationConfig verifies the current product scope through pay-v1.
func (c *client) GetIntegrationConfig(ctx context.Context) (*IntegrationConfig, error) {
	body, err := c.do(ctx, http.MethodGet, "/v1/integration-config", nil, "", http.StatusOK)
	if err != nil {
		return nil, err
	}
	var result IntegrationConfig
	if strictUnmarshalObject(body, &result, true) != nil || result.Validate(c.appID, c.keyID, c.organizationID, c.productID, c.environment) != nil {
		return nil, ErrInvalidResponse
	}
	return &result, nil
}

func (c IntegrationConfig) Validate(app, key, org, product string, env Environment) error {
	if c.SchemaVersion != "payment-integration.v1" || c.AppID != app || c.RequestKeyID != key || c.OrganizationID != org || c.ProductID != product || c.Environment != env || !validUUID(org) || !validUUID(product) || !validIdentifier(app, 8, 80) || !validIdentifier(key, 8, 80) || (env != EnvironmentSandbox && env != EnvironmentLive) || c.PaymentMethods == nil || c.ReturnURLs == nil || c.WebhookEndpoints == nil || c.WebhookSigningKeys == nil {
		return ErrInvalidResponse
	}
	methods := map[string]bool{}
	for _, m := range c.PaymentMethods {
		if methods[m] || (m != "alipay" && m != "wechat_pay" && m != "mock_sandbox") || (env == EnvironmentLive && m == "mock_sandbox") {
			return ErrInvalidResponse
		}
		methods[m] = true
	}
	keys := map[string]IntegrationWebhookKey{}
	for _, k := range c.WebhookSigningKeys {
		raw, err := base64.StdEncoding.Strict().DecodeString(k.PublicKey)
		_, duplicate := keys[k.KeyID]
		if duplicate || !validIdentifier(k.KeyID, 8, 80) || err != nil || len(raw) != 32 || k.Algorithm != "Ed25519" || (k.Status != "active" && k.Status != "retiring") {
			return ErrInvalidResponse
		}
		keys[k.KeyID] = k
	}
	urls := map[string]bool{}
	for _, u := range c.ReturnURLs {
		if urls[u] || !validIntegrationURL(u) {
			return ErrInvalidResponse
		}
		urls[u] = true
	}
	urls = map[string]bool{}
	for _, e := range c.WebhookEndpoints {
		k, ok := keys[e.SigningKeyID]
		if urls[e.URL] || !validIntegrationURL(e.URL) || !ok || k.Status != "active" {
			return ErrInvalidResponse
		}
		urls[e.URL] = true
	}
	return nil
}
func validIntegrationURL(raw string) bool {
	if raw == "" || strings.TrimSpace(raw) != raw || len(raw) > 500 || strings.Contains(raw, "#") {
		return false
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	return validIntegrationPublicDNSName(strings.ToLower(u.Hostname()))
}

// validIntegrationPublicDNSName mirrors the payment service's public webhook
// destination guard. Bindings are public callback targets, never loopback,
// private-network literals, or local development hostnames.
func validIntegrationPublicDNSName(host string) bool {
	if host == "" || len(host) > 253 || net.ParseIP(host) != nil || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || !strings.Contains(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

// StoredIntegration contains public data only; private key refs remain in the
// immutable deployment configuration and cannot be supplied by the browser.
type StoredIntegration struct {
	Version int               `json:"version"`
	BaseURL string            `json:"base_url"`
	Config  IntegrationConfig `json:"config"`
	SavedAt time.Time         `json:"saved_at"`
}

func ExpectedWebhookURL(raw config.UnifiedPaymentConfig) (string, error) {
	u, err := url.Parse(raw.ReturnURL)
	if err != nil || !validIntegrationURL(raw.ReturnURL) {
		return "", ErrInvalidConfiguration
	}
	if raw.WebhookURL != "" {
		if !validIntegrationURL(raw.WebhookURL) {
			return "", ErrInvalidConfiguration
		}
		return raw.WebhookURL, nil
	}
	return u.Scheme + "://" + u.Host + "/api/v1/payment/webhook/unified", nil
}

// Apply verifies all pinned identity fields and destinations before activation.
// Enabled remains an explicit deployment switch, including for live systems.
func (s StoredIntegration) Apply(raw config.UnifiedPaymentConfig) (config.UnifiedPaymentConfig, error) {
	if s.Version != 1 || s.Config.Validate(raw.AppID, raw.RequestKeyID, raw.OrganizationID, raw.ProductID, Environment(raw.Environment)) != nil || validateBindingOrigin(s.BaseURL, raw) != nil {
		return raw, ErrInvalidConfiguration
	}
	expected, err := ExpectedWebhookURL(raw)
	if err != nil {
		return raw, err
	}
	found := false
	for _, u := range s.Config.ReturnURLs {
		if u == raw.ReturnURL {
			found = true
		}
	}
	if !found || len(s.Config.WebhookEndpoints) != 1 || s.Config.WebhookEndpoints[0].URL != expected {
		return raw, ErrInvalidConfiguration
	}
	keys := map[string]string{}
	for _, k := range s.Config.WebhookSigningKeys {
		keys[k.KeyID] = k.PublicKey
	}
	encoded, _ := json.Marshal(keys)
	methods := []string{}
	for _, m := range s.Config.PaymentMethods {
		if m == "alipay" || m == "wechat_pay" {
			methods = append(methods, m)
		}
	}
	if len(methods) == 0 {
		return raw, ErrInvalidConfiguration
	}
	raw.BaseURL = s.BaseURL
	raw.WebhookPublicKeysJSON = string(encoded)
	raw.PaymentMethods = strings.Join(methods, ",")
	return raw, nil
}

// SynchronizeIntegration operates even before the gateway is enabled. The
// signing key still must have been generated and injected by the product owner.
func SynchronizeIntegration(ctx context.Context, raw config.UnifiedPaymentConfig, baseURL, code, idem string) (StoredIntegration, error) {
	if validateBindingOrigin(baseURL, raw) != nil {
		return StoredIntegration{}, ErrInvalidConfiguration
	}
	private, err := loadVaultEd25519PrivateKey(ctx, raw.VaultAgentSocket, raw.RequestPrivateKeyVaultRef, nil)
	if err != nil {
		return StoredIntegration{}, ErrInvalidConfiguration
	}
	defer clear(private)
	c, err := newClient(Config{BaseURL: baseURL, Environment: Environment(raw.Environment), AppID: raw.AppID, RequestKeyID: raw.RequestKeyID, OrganizationID: raw.OrganizationID, ProductID: raw.ProductID, RequestPrivateKey: ed25519.PrivateKey(private), HTTPClient: bindingHTTPClient(raw)})
	if err != nil {
		return StoredIntegration{}, err
	}
	defer clear(c.privateKey)
	if code != "" {
		webhook, err := ExpectedWebhookURL(raw)
		if err != nil {
			return StoredIntegration{}, err
		}
		if err := c.bindProduct(ctx, idem, code, raw.ReturnURL, webhook); err != nil {
			return StoredIntegration{}, err
		}
	}
	received, err := c.GetIntegrationConfig(ctx)
	if err != nil {
		return StoredIntegration{}, err
	}
	stored := StoredIntegration{Version: 1, BaseURL: baseURL, Config: *received, SavedAt: time.Now().UTC()}
	if _, err := stored.Apply(raw); err != nil {
		return StoredIntegration{}, err
	}
	return stored, nil
}
