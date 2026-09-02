package unifiedpay

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
)

// Gateway adapts the pay-v1 product API to Sub2's existing payment.Provider
// seam. The only user-visible method it claims is Alipay.
type Gateway struct {
	enabled        bool
	client         *client
	verifier       *webhookVerifier
	returnURL      string
	environment    Environment
	organizationID string
	productID      string
	appID          string
}

func NewFromAppConfig(appConfig *config.Config) (*Gateway, error) {
	if appConfig == nil || !appConfig.UnifiedPayment.Enabled {
		return &Gateway{}, nil
	}
	raw := appConfig.UnifiedPayment
	loadContext, cancel := context.WithTimeout(context.Background(), defaultVaultAgentTimeout)
	defer cancel()
	privateKeyBytes, err := loadVaultEd25519PrivateKey(
		loadContext,
		strings.TrimSpace(raw.VaultAgentSocket),
		strings.TrimSpace(raw.RequestPrivateKeyVaultRef),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("unified payment request private key: %w", ErrInvalidConfiguration)
	}
	defer clear(privateKeyBytes)
	publicKeys, err := decodePublicKeys(raw.WebhookPublicKeysJSON)
	if err != nil {
		return nil, err
	}
	return New(Config{
		Enabled: true, BaseURL: strings.TrimSpace(raw.BaseURL), Environment: Environment(strings.TrimSpace(raw.Environment)),
		OrganizationID: strings.TrimSpace(raw.OrganizationID), ProductID: strings.TrimSpace(raw.ProductID),
		AppID: strings.TrimSpace(raw.AppID), RequestKeyID: strings.TrimSpace(raw.RequestKeyID),
		RequestPrivateKey: ed25519.PrivateKey(privateKeyBytes), WebhookPublicKeys: publicKeys,
		ReturnURL: strings.TrimSpace(raw.ReturnURL),
	})
}

func decodePublicKeys(raw string) (map[string]ed25519.PublicKey, error) {
	var encoded map[string]string
	if err := strictUnmarshalObject([]byte(strings.TrimSpace(raw)), &encoded, false); err != nil || len(encoded) == 0 {
		return nil, fmt.Errorf("unified payment webhook public keys: %w", ErrInvalidConfiguration)
	}
	result := make(map[string]ed25519.PublicKey, len(encoded))
	for keyID, value := range encoded {
		decoded, err := base64.StdEncoding.Strict().DecodeString(value)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("unified payment webhook public key %q: %w", keyID, ErrInvalidConfiguration)
		}
		result[keyID] = ed25519.PublicKey(decoded)
	}
	return result, nil
}

func New(gatewayConfig Config) (*Gateway, error) {
	if !gatewayConfig.Enabled {
		return &Gateway{}, nil
	}
	returnURL, err := validateReturnURL(gatewayConfig.ReturnURL, gatewayConfig.Environment)
	if err != nil {
		return nil, err
	}
	apiClient, err := newClient(gatewayConfig)
	if err != nil {
		return nil, err
	}
	verifier, err := newWebhookVerifier(gatewayConfig)
	if err != nil {
		return nil, err
	}
	return &Gateway{
		enabled: true, client: apiClient, verifier: verifier, returnURL: returnURL,
		environment: gatewayConfig.Environment, organizationID: gatewayConfig.OrganizationID,
		productID: gatewayConfig.ProductID, appID: gatewayConfig.AppID,
	}, nil
}

func validateReturnURL(raw string, environment Environment) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return "", ErrInvalidConfiguration
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return "", ErrInvalidConfiguration
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "https" {
		loopback := environment == EnvironmentSandbox && parsed.Scheme == "http" &&
			(parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1")
		if !loopback {
			return "", ErrInvalidConfiguration
		}
	}
	return parsed.String(), nil
}

func (g *Gateway) Enabled() bool { return g != nil && g.enabled }

func (g *Gateway) ReturnURL() string {
	if g == nil {
		return ""
	}
	return g.returnURL
}

func (g *Gateway) ScopeMetadata() map[string]string {
	if !g.Enabled() {
		return nil
	}
	return map[string]string{
		"environment": g.environment.String(), "organization_id": g.organizationID,
		"product_id": g.productID, "app_id": g.appID,
	}
}

func (e Environment) String() string { return string(e) }

func (g *Gateway) Selection() *payment.InstanceSelection {
	if !g.Enabled() {
		return nil
	}
	return &payment.InstanceSelection{
		ProviderKey: payment.TypeUnifiedPay, SupportedTypes: payment.TypeAlipay,
		PaymentMode: "popup", Config: g.ScopeMetadata(),
	}
}

func (g *Gateway) Name() string             { return "Unified Payment" }
func (g *Gateway) ProviderKey() string      { return payment.TypeUnifiedPay }
func (g *Gateway) SupportedTypes() []string { return []string{payment.TypeAlipay} }

func (g *Gateway) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	if !g.Enabled() {
		return nil, ErrDisabled
	}
	if req.PaymentType != payment.TypeAlipay || req.ReturnURL != g.returnURL ||
		!validIdentifier(req.OrderID, 6, 64) || !validLowerIdentifier(strings.TrimSpace(req.OrderType), 3, 64) ||
		req.ExpiresInSeconds < 300 || req.ExpiresInSeconds > 7200 {
		return nil, ErrInvalidRequest
	}
	amountFen, err := payment.AmountToMinorUnit(req.Amount, payment.DefaultPaymentCurrency)
	if err != nil || amountFen < 1 {
		return nil, ErrInvalidRequest
	}
	returnURL := g.returnURL
	input := createPaymentOrderRequest{
		ProductOrderNo: req.OrderID, OrderType: strings.TrimSpace(req.OrderType), AmountFen: amountFen,
		Currency: payment.DefaultPaymentCurrency, Subject: strings.TrimSpace(req.Subject),
		PaymentMethod: PaymentMethodAlipay, ExpiresInSeconds: req.ExpiresInSeconds,
		ReturnURL: &returnURL, Metadata: map[string]string{"source": "sub2"},
	}
	if input.Subject == "" || !utf8.ValidString(input.Subject) || utf8.RuneCountInString(input.Subject) > 120 {
		return nil, ErrInvalidRequest
	}
	idempotencyKey := "sub2:create:" + req.OrderID
	result, createErr := g.client.createPaymentOrder(ctx, idempotencyKey, input)
	if createErr != nil && ambiguousCreateError(createErr) {
		// A transport failure may happen after the server committed. Reconcile by
		// the product order number before reporting failure and risking a second
		// business order.
		result, err = g.client.getPaymentOrderByProductOrderNo(ctx, req.OrderID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCreateStateUnconfirmed, createErr)
		}
	} else if createErr != nil {
		return nil, createErr
	}
	if err := validateCreatedOrder(result, input); err != nil || !g.client.validCheckoutURL(strings.TrimSpace(*result.CheckoutURL)) {
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalidResponse
	}
	return &payment.CreatePaymentResponse{
		TradeNo: result.PaymentOrderID, PayURL: strings.TrimSpace(*result.CheckoutURL),
		Currency: payment.DefaultPaymentCurrency, ResultType: payment.CreatePaymentResultOrderCreated,
	}, nil
}

func validateCreatedOrder(result *paymentOrderResponse, input createPaymentOrderRequest) error {
	if result == nil || result.ProductOrderNo != input.ProductOrderNo || result.OrderType != input.OrderType ||
		result.AmountFen != input.AmountFen || result.Currency != input.Currency || result.PaymentMethod != input.PaymentMethod ||
		result.CheckoutURL == nil || result.ExpiresAt.IsZero() || result.NeedsManualReview {
		return ErrInvalidResponse
	}
	switch result.Status {
	case StatusCreated, StatusPendingPayment:
		return nil
	default:
		return ErrInvalidResponse
	}
}

func (c *client) validCheckoutURL(raw string) bool {
	if c == nil || c.baseURL == nil {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawFragment != "" || parsed.Fragment != "" || parsed.RawPath != "" ||
		!strings.HasPrefix(parsed.EscapedPath(), "/checkout/") {
		return false
	}
	return strings.EqualFold(parsed.Scheme, c.baseURL.Scheme) && strings.EqualFold(parsed.Host, c.baseURL.Host)
}

func (g *Gateway) QueryOrder(ctx context.Context, paymentOrderID string) (*payment.QueryOrderResponse, error) {
	if !g.Enabled() {
		return nil, ErrDisabled
	}
	result, err := g.client.getPaymentOrder(ctx, paymentOrderID)
	if err != nil {
		return nil, err
	}
	status := payment.ProviderStatusPending
	switch result.Status {
	case StatusPaid:
		status = payment.ProviderStatusPaid
	case StatusPartiallyRefunded, StatusRefunded:
		status = payment.ProviderStatusRefunded
	case StatusClosed, StatusExpired, StatusPaidAfterClose:
		status = payment.ProviderStatusFailed
	}
	if result.NeedsManualReview {
		status = payment.ProviderStatusPending
	}
	tradeNo := result.PaymentOrderID
	if result.ChannelTransactionID != nil && strings.TrimSpace(*result.ChannelTransactionID) != "" {
		tradeNo = strings.TrimSpace(*result.ChannelTransactionID)
	}
	metadata := map[string]string{
		"payment_order_id":    result.PaymentOrderID,
		"status":              result.Status,
		"needs_manual_review": strconv.FormatBool(result.NeedsManualReview),
	}
	for key, value := range g.ScopeMetadata() {
		metadata[key] = value
	}
	if result.ChannelOutTradeNo != nil {
		metadata["channel_out_trade_no"] = strings.TrimSpace(*result.ChannelOutTradeNo)
	}
	paidAt := ""
	if result.PaidAt != nil {
		paidAt = result.PaidAt.UTC().Format(time.RFC3339)
	}
	return &payment.QueryOrderResponse{
		TradeNo: tradeNo, Status: status, Amount: payment.MinorUnitToAmount(result.PaidAmountFen, payment.DefaultPaymentCurrency),
		PaidAt: paidAt, Metadata: metadata,
	}, nil
}

func (g *Gateway) CancelPayment(ctx context.Context, paymentOrderID string) error {
	if !g.Enabled() {
		return ErrDisabled
	}
	result, err := g.client.closePaymentOrder(ctx, paymentOrderID, "sub2:close:"+paymentOrderID)
	if err != nil {
		return fmt.Errorf("%w: %v", payment.ErrUpstreamStateUnconfirmed, err)
	}
	if result.NeedsManualReview {
		return fmt.Errorf("%w: manual review", payment.ErrUpstreamStateUnconfirmed)
	}
	switch result.Status {
	case StatusClosed, StatusExpired:
		return nil
	default:
		return fmt.Errorf("%w: status=%s", payment.ErrUpstreamStateUnconfirmed, result.Status)
	}
}

func (g *Gateway) VerifyWebhook(headers http.Header, rawBody []byte) (VerifiedWebhook, error) {
	if !g.Enabled() {
		return VerifiedWebhook{}, ErrDisabled
	}
	return g.verifier.verify(headers, rawBody)
}

// VerifyNotification is deliberately unsupported: the unified Webhook needs
// the original multi-value HTTP headers and a durable inbox, which the legacy
// provider callback seam cannot supply.
func (g *Gateway) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	return nil, ErrInvalidRequest
}

func (g *Gateway) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, errors.New("unified payment refunds are not enabled for Sub2")
}

func (g *Gateway) MerchantIdentityMetadata() map[string]string { return g.ScopeMetadata() }

var _ payment.Provider = (*Gateway)(nil)
var _ payment.CancelableProvider = (*Gateway)(nil)

// MarshalJSON prevents accidental serialization of Gateway internals if it is
// ever attached to a diagnostic structure.
func (g *Gateway) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"enabled": g.Enabled(), "provider": payment.TypeUnifiedPay})
}
