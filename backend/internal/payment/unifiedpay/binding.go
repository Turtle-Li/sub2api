package unifiedpay

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"net"
	"net/http"
	"strings"
	"time"
)

type bindingRequest struct {
	AppID       string      `json:"app_id"`
	Environment Environment `json:"environment"`
	BindingCode string      `json:"binding_code"`
	KeyID       string      `json:"key_id"`
	PublicKey   string      `json:"public_key"`
	ReturnURL   string      `json:"return_url"`
	WebhookURL  string      `json:"webhook_url"`
	Signature   string      `json:"signature"`
}

func bindingCanonical(r bindingRequest, idem string) []byte {
	hash := sha256.Sum256([]byte(r.BindingCode))
	return []byte(strings.Join([]string{"payment-binding.v1", "POST", "/v1/product-bindings", string(r.Environment), r.AppID, r.KeyID, r.PublicKey, r.ReturnURL, r.WebhookURL, hex.EncodeToString(hash[:]), idem}, "\n"))
}
func (c *client) bindProduct(ctx context.Context, idem, code, returnURL, webhookURL string) error {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(code)
	if err != nil || len(decoded) != 32 || !validIdentifier(idem, 16, 80) {
		return ErrInvalidRequest
	}
	publicKey, ok := c.privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return ErrInvalidConfiguration
	}
	r := bindingRequest{AppID: c.appID, Environment: c.environment, BindingCode: code, KeyID: c.keyID, PublicKey: base64.StdEncoding.EncodeToString(publicKey), ReturnURL: returnURL, WebhookURL: webhookURL}
	r.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(c.privateKey, bindingCanonical(r, idem)))
	body, _ := json.Marshal(r)
	response, err := c.do(ctx, http.MethodPost, "/v1/product-bindings", body, idem, http.StatusOK)
	if err != nil {
		return err
	}
	var receipt struct {
		SchemaVersion  string      `json:"schema_version"`
		Status         string      `json:"status"`
		AppID          string      `json:"app_id"`
		Environment    Environment `json:"environment"`
		OrganizationID string      `json:"organization_id"`
		ProductID      string      `json:"product_id"`
		RequestKeyID   string      `json:"request_key_id"`
	}
	if strictUnmarshalObject(response, &receipt, true) != nil || receipt.SchemaVersion != "payment-binding.v1" || receipt.Status != "bound" || receipt.AppID != c.appID || receipt.Environment != c.environment || receipt.OrganizationID != c.organizationID || receipt.ProductID != c.productID || receipt.RequestKeyID != c.keyID {
		return ErrInvalidResponse
	}
	return nil
}

func validateBindingOrigin(base string, raw config.UnifiedPaymentConfig) error {
	expected := raw.BaseURL
	if expected == "" {
		expected = "https://pay.totools.cn"
	}
	parsed, err := parseBaseURL(base, Environment(raw.Environment))
	if err != nil {
		return ErrInvalidConfiguration
	}
	approved, err := parseBaseURL(expected, Environment(raw.Environment))
	if err != nil || parsed.String() != approved.String() {
		return ErrInvalidConfiguration
	}
	ip := net.ParseIP(parsed.Hostname())
	local := raw.Environment == "sandbox" && ip != nil && ip.IsLoopback()
	if !local && (parsed.Scheme != "https" || (parsed.Port() != "" && parsed.Port() != "443") || ip != nil || !strings.Contains(parsed.Hostname(), ".")) {
		return ErrInvalidConfiguration
	}
	return nil
}

func bindingHTTPClient(raw config.UnifiedPaymentConfig) *http.Client {
	approved, _ := parseBaseURL(raw.BaseURL, Environment(raw.Environment))
	local := approved != nil && raw.Environment == "sandbox" && net.ParseIP(approved.Hostname()) != nil && net.ParseIP(approved.Hostname()).IsLoopback()
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, DisableKeepAlives: true}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrRequestFailed
		}
		dialer := net.Dialer{Timeout: 5 * time.Second}
		if local && approved != nil && host == approved.Hostname() {
			return dialer.DialContext(ctx, network, address)
		}
		if port != "443" {
			return nil, ErrRequestFailed
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, ErrRequestFailed
		}
		for _, candidate := range ips {
			if !bindingPublicIP(candidate.IP) {
				continue
			}
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			if err == nil {
				return connection, nil
			}
		}
		return nil, ErrRequestFailed
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func bindingPublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	if v := ip.To4(); v != nil {
		if v[0] == 0 || v[0] >= 224 {
			return false
		}
		if v[0] == 100 && v[1] >= 64 && v[1] <= 127 {
			return false
		}
		if v[0] == 192 && v[1] == 0 {
			return false
		}
		if v[0] == 198 && (v[1] == 18 || v[1] == 19 || v[1] == 51) {
			return false
		}
		if v[0] == 203 && v[1] == 0 && v[2] == 113 {
			return false
		}
		return true
	}
	v := ip.To16()
	if v == nil || v[0]&0xe0 != 0x20 {
		return false
	}
	if v[0] == 0x20 && v[1] == 0x01 && v[2] == 0x0d && v[3] == 0xb8 {
		return false
	}
	return true
}
