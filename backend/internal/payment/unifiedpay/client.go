package unifiedpay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout       = 15 * time.Second
	maximumResponseBodyBytes = 1 << 20
)

type client struct {
	baseURL        *url.URL
	appID          string
	keyID          string
	organizationID string
	productID      string
	environment    Environment
	audience       string
	privateKey     ed25519.PrivateKey
	httpClient     *http.Client
	nonceSource    func() (string, error)
	now            func() time.Time
}

func newClient(config Config) (*client, error) {
	audience := ""
	switch config.Environment {
	case EnvironmentSandbox:
		audience = AudienceSandbox
	case EnvironmentLive:
		audience = AudienceLive
	default:
		return nil, ErrInvalidConfiguration
	}
	if !validIdentifier(config.AppID, 8, 80) || !validIdentifier(config.RequestKeyID, 8, 80) ||
		!validUUID(config.OrganizationID) || !validUUID(config.ProductID) || len(config.RequestPrivateKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalidConfiguration
	}
	baseURL, err := parseBaseURL(config.BaseURL, config.Environment)
	if err != nil {
		return nil, err
	}
	httpClient := http.Client{Timeout: defaultHTTPTimeout}
	if config.HTTPClient != nil {
		httpClient = *config.HTTPClient
		if httpClient.Timeout <= 0 {
			httpClient.Timeout = defaultHTTPTimeout
		}
	}
	httpClient.Jar = nil
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	nonceSource := config.NonceSource
	if nonceSource == nil {
		nonceSource = randomNonce
	}
	now := config.Clock
	if now == nil {
		now = time.Now
	}
	return &client{
		baseURL: baseURL, appID: config.AppID, keyID: config.RequestKeyID,
		organizationID: config.OrganizationID, productID: config.ProductID,
		environment: config.Environment, audience: audience,
		privateKey: bytes.Clone(config.RequestPrivateKey), httpClient: &httpClient,
		nonceSource: nonceSource, now: now,
	}, nil
}

func parseBaseURL(raw string, environment Environment) (*url.URL, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return nil, ErrInvalidConfiguration
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" {
		return nil, ErrInvalidConfiguration
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, ErrInvalidConfiguration
	}
	if environment == EnvironmentLive && parsed.Scheme != "https" {
		return nil, ErrInvalidConfiguration
	}
	parsed.Path = ""
	return parsed, nil
}

func randomNonce() (string, error) {
	random := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

// CanonicalPayload is the exact ten-line UTF-8 pay-v1 signature input.
func CanonicalPayload(method, audience, rawTarget, bodySHA256, appID, keyID, timestamp, nonce, idempotencyKey string) []byte {
	return []byte(strings.Join([]string{
		"pay-v1", method, audience, rawTarget, bodySHA256,
		appID, keyID, timestamp, nonce, idempotencyKey,
	}, "\n"))
}

func (c *client) createPaymentOrder(ctx context.Context, idempotencyKey string, input createPaymentOrderRequest) (*paymentOrderResponse, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	responseBody, err := c.do(ctx, http.MethodPost, "/v1/payment-orders", body, idempotencyKey, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	return c.decodePaymentOrder(responseBody)
}

func (c *client) getPaymentOrder(ctx context.Context, paymentOrderID string) (*paymentOrderResponse, error) {
	if !validUUID(paymentOrderID) {
		return nil, ErrInvalidRequest
	}
	body, err := c.do(ctx, http.MethodGet, "/v1/payment-orders/"+paymentOrderID, nil, "", http.StatusOK)
	if err != nil {
		return nil, err
	}
	result, err := c.decodePaymentOrder(body)
	if err != nil || !strings.EqualFold(result.PaymentOrderID, paymentOrderID) {
		return nil, ErrInvalidResponse
	}
	return result, nil
}

func (c *client) getPaymentOrderByProductOrderNo(ctx context.Context, productOrderNo string) (*paymentOrderResponse, error) {
	if !validIdentifier(productOrderNo, 6, 64) {
		return nil, ErrInvalidRequest
	}
	target := "/v1/payment-orders?product_order_no=" + url.QueryEscape(productOrderNo)
	body, err := c.do(ctx, http.MethodGet, target, nil, "", http.StatusOK)
	if err != nil {
		return nil, err
	}
	result, err := c.decodePaymentOrder(body)
	if err != nil || result.ProductOrderNo != productOrderNo {
		return nil, ErrInvalidResponse
	}
	return result, nil
}

func (c *client) closePaymentOrder(ctx context.Context, paymentOrderID, idempotencyKey string) (*paymentOrderResponse, error) {
	if !validUUID(paymentOrderID) {
		return nil, ErrInvalidRequest
	}
	body, _ := json.Marshal(closePaymentOrderRequest{ReasonCode: "customer_request"})
	target := "/v1/payment-orders/" + paymentOrderID + "/close"
	responseBody, err := c.do(ctx, http.MethodPost, target, body, idempotencyKey, http.StatusOK)
	if err != nil {
		return nil, err
	}
	result, err := c.decodePaymentOrder(responseBody)
	if err != nil || !strings.EqualFold(result.PaymentOrderID, paymentOrderID) {
		return nil, ErrInvalidResponse
	}
	return result, nil
}

func (c *client) decodePaymentOrder(body []byte) (*paymentOrderResponse, error) {
	var result paymentOrderResponse
	if err := strictUnmarshalObject(body, &result, false); err != nil {
		return nil, ErrInvalidResponse
	}
	if result.Environment != c.environment || result.OrganizationID != c.organizationID ||
		result.ProductID != c.productID || result.AppID != c.appID || !validPaymentOrderResponse(result) {
		return nil, ErrInvalidResponse
	}
	return &result, nil
}

func validPaymentOrderResponse(result paymentOrderResponse) bool {
	if !validUUID(result.PaymentOrderID) || !validIdentifier(result.ProductOrderNo, 6, 64) ||
		!validLowerIdentifier(result.OrderType, 3, 64) || result.AmountFen < 1 || result.PaidAmountFen < 0 ||
		result.RefundedAmountFen < 0 || result.ReservedRefundAmountFen < 0 || result.RefundableAmountFen < 0 ||
		result.Currency != "CNY" || result.PaymentMethod != PaymentMethodAlipay || result.CreatedAt.IsZero() || result.ExpiresAt.IsZero() {
		return false
	}
	if result.PaidAmountFen > result.AmountFen || result.RefundedAmountFen > result.PaidAmountFen ||
		result.ReservedRefundAmountFen > result.PaidAmountFen-result.RefundedAmountFen ||
		result.RefundableAmountFen != result.PaidAmountFen-result.RefundedAmountFen-result.ReservedRefundAmountFen {
		return false
	}
	hasChannelTransaction := result.ChannelTransactionID != nil && strings.TrimSpace(*result.ChannelTransactionID) != ""
	switch result.Status {
	case StatusCreated, StatusPendingPayment, StatusConfirmationPending, StatusClosed, StatusExpired:
		return result.PaidAmountFen == 0 && result.RefundedAmountFen == 0 && result.ReservedRefundAmountFen == 0
	case StatusPaid, StatusPaidAfterClose:
		return result.PaidAmountFen == result.AmountFen && hasChannelTransaction
	case StatusPartiallyRefunded:
		return result.PaidAmountFen == result.AmountFen && result.RefundedAmountFen > 0 &&
			result.RefundedAmountFen < result.PaidAmountFen && hasChannelTransaction
	case StatusRefunded:
		return result.PaidAmountFen == result.AmountFen && result.RefundedAmountFen == result.PaidAmountFen &&
			result.ReservedRefundAmountFen == 0 && hasChannelTransaction
	default:
		return false
	}
}

func (c *client) do(ctx context.Context, method, rawTarget string, body []byte, idempotencyKey string, expectedStatus ...int) ([]byte, error) {
	request, err := c.newSignedRequest(ctx, method, rawTarget, body, idempotencyKey)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil || response == nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, ErrRequestFailed
	}
	responseBody, readErr := readResponseBody(response)
	if readErr != nil {
		return nil, readErr
	}
	for _, status := range expectedStatus {
		if response.StatusCode == status {
			return responseBody, nil
		}
	}
	return nil, parseAPIError(response.StatusCode, responseBody)
}

func (c *client) newSignedRequest(ctx context.Context, method, rawTarget string, body []byte, idempotencyKey string) (*http.Request, error) {
	if c == nil || ctx == nil || c.baseURL == nil || c.httpClient == nil || c.now == nil || c.nonceSource == nil ||
		len(c.privateKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalidConfiguration
	}
	isWrite := method != http.MethodGet && method != http.MethodHead
	if (isWrite && !validIdentifier(idempotencyKey, 16, 128)) || (!isWrite && idempotencyKey != "") {
		return nil, ErrInvalidRequest
	}
	endpoint, err := c.endpointURL(rawTarget)
	if err != nil {
		return nil, err
	}
	nonce, err := c.nonceSource()
	if err != nil || !validIdentifier(nonce, 16, 128) {
		return nil, ErrInvalidRequest
	}
	now := c.now().UTC()
	if now.IsZero() || now.Unix() <= 0 {
		return nil, ErrInvalidConfiguration
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	bodyHash := sha256.Sum256(body)
	payload := CanonicalPayload(method, c.audience, rawTarget, hex.EncodeToString(bodyHash[:]), c.appID, c.keyID, timestamp, nonce, idempotencyKey)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(c.privateKey, payload))
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil || request.URL.RequestURI() != rawTarget {
		return nil, ErrInvalidRequest
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set(HeaderAppID, c.appID)
	request.Header.Set(HeaderKeyID, c.keyID)
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderNonce, nonce)
	request.Header.Set(HeaderAudience, c.audience)
	request.Header.Set(HeaderSignature, signature)
	if isWrite {
		request.Header.Set(HeaderIdempotencyKey, idempotencyKey)
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func (c *client) endpointURL(rawTarget string) (*url.URL, error) {
	if rawTarget == "" || !strings.HasPrefix(rawTarget, "/") || strings.HasPrefix(rawTarget, "//") ||
		strings.ContainsAny(rawTarget, "#\\\r\n\t ") {
		return nil, ErrInvalidRequest
	}
	target, err := url.ParseRequestURI(rawTarget)
	if err != nil || target == nil || target.IsAbs() || target.Host != "" || target.User != nil {
		return nil, ErrInvalidRequest
	}
	endpoint := *c.baseURL
	endpoint.Path, endpoint.RawPath = target.Path, target.RawPath
	endpoint.RawQuery, endpoint.ForceQuery = target.RawQuery, target.ForceQuery
	if endpoint.RequestURI() != rawTarget {
		return nil, ErrInvalidRequest
	}
	return &endpoint, nil
}

func readResponseBody(response *http.Response) ([]byte, error) {
	if response.Body == nil {
		return nil, ErrInvalidResponse
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBodyBytes+1))
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil {
		return nil, ErrInvalidResponse
	}
	if len(body) > maximumResponseBodyBytes {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

func parseAPIError(statusCode int, body []byte) error {
	result := &APIError{StatusCode: statusCode, Retryable: statusCode >= 500}
	var envelope struct {
		Code      string `json:"error"`
		Retryable bool   `json:"retryable"`
	}
	if err := strictUnmarshalObject(body, &envelope, false); err == nil {
		if validLowerIdentifier(envelope.Code, 3, 120) {
			result.Code = envelope.Code
		}
		result.Retryable = envelope.Retryable || result.Retryable
	}
	return result
}

func ambiguousCreateError(err error) bool {
	if errors.Is(err, ErrRequestFailed) || errors.Is(err, ErrInvalidResponse) || errors.Is(err, ErrResponseTooLarge) {
		return true
	}
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Retryable
}

func validIdentifier(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == ':' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func validLowerIdentifier(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			continue
		}
		return false
	}
	return true
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i := 0; i < len(value); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		c := value[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}
