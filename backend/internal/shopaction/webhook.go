package shopaction

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type SecretLoader func(context.Context, string) ([]byte, error)

type Webhook struct {
	integration Integration
	load        SecretLoader
	client      *http.Client
}

func NewWebhook(i Integration, load SecretLoader) *Webhook {
	return &Webhook{integration: i, load: load, client: webhookHTTPClient()}
}

// Standard Webhooks v1, pinned source/tests and license are recorded in the design.
func signature(secret []byte, id string, timestamp int64, body []byte) (string, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(string(secret), "whsec_"))
	if err != nil || len(key) < 24 {
		return "", &Failure{Kind: "SIGNING_KEY_INVALID"}
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%s.%d.", id, timestamp)
	_, _ = mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (w *Webhook) request(ctx context.Context, method, endpoint, id string, body []byte) (*http.Response, error) {
	secret, err := w.load(ctx, w.integration.CredentialAlias)
	if err != nil {
		return nil, &Failure{Kind: "CREDENTIAL_UNAVAILABLE", Retryable: true}
	}
	defer clear(secret)
	ts := time.Now().Unix()
	sig, err := signature(secret, id, ts, body)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &Failure{Kind: "REQUEST_INVALID"}
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("webhook-id", id)
	r.Header.Set("webhook-timestamp", strconv.FormatInt(ts, 10))
	r.Header.Set("webhook-signature", sig)
	r.Header.Set("Idempotency-Key", id)
	res, err := w.client.Do(r)
	if err != nil {
		return nil, &Failure{Kind: "HTTP_TRANSPORT", Retryable: true}
	}
	return res, nil
}

func (w *Webhook) Ready(ctx context.Context) error {
	if ValidateIntegration(w.integration) != nil {
		return &Failure{Kind: "INTEGRATION_INVALID"}
	}
	defer w.client.CloseIdleConnections()
	if w.integration.ReadyEndpoint == "" {
		secret, err := w.load(ctx, w.integration.CredentialAlias)
		defer clear(secret)
		if err != nil {
			return &Failure{Kind: "CREDENTIAL_UNAVAILABLE", Retryable: true}
		}
		_, err = signature(secret, "readiness", time.Now().Unix(), nil)
		return err
	}
	res, err := w.request(ctx, http.MethodGet, w.integration.ReadyEndpoint, "readiness", nil)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return &Failure{Kind: "READINESS_FAILED", Retryable: true}
	}
	return nil
}

func (w *Webhook) Execute(ctx context.Context, input Request) (Result, error) {
	var result Result
	if ValidateIntegration(w.integration) != nil {
		return result, &Failure{Kind: "INTEGRATION_INVALID"}
	}
	body, err := json.Marshal(input)
	if err != nil {
		return result, &Failure{Kind: "REQUEST_INVALID"}
	}
	defer clear(body)
	defer w.client.CloseIdleConnections()
	res, err := w.request(ctx, http.MethodPost, w.integration.Endpoint, input.ActionID, body)
	if err != nil {
		return result, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		failure := &Failure{Kind: "HTTP_REJECTED", Retryable: res.StatusCode == 429 || res.StatusCode >= 500}
		if res.StatusCode == 202 {
			failure.Kind = "ASYNC_NOT_SUPPORTED"
		}
		if seconds, e := strconv.Atoi(res.Header.Get("Retry-After")); e == nil && seconds > 0 {
			failure.RetryAfter = time.Duration(min(seconds, 3600)) * time.Second
		}
		return result, failure
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32769))
	defer clear(raw)
	if err != nil {
		return result, &Failure{Kind: "HTTP_READ_FAILED", Retryable: true}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > 32768 || decoder.Decode(&result) != nil || decoder.Decode(new(json.RawMessage)) != io.EOF || !ValidResult(input, result) {
		return Result{}, &Failure{Kind: "RESULT_INVALID"}
	}
	return result, nil
}

func ValidResult(input Request, result Result) bool {
	if result.Version != 1 || result.ActionID != input.ActionID || result.OrderID != input.Order.ID || result.Status != "completed" || result.ReceiptID == "" || len(result.ReceiptID) > 128 {
		return false
	}
	for _, r := range result.ReceiptID {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("._:-", r) {
			return false
		}
	}
	switch result.Delivery.Kind {
	case "text":
		return strings.TrimSpace(result.Delivery.Content) != "" && len(result.Delivery.Content) <= 32768 && utf8.ValidString(result.Delivery.Content) && !strings.ContainsRune(result.Delivery.Content, 0)
	case "none":
		return result.Delivery.Content == ""
	default:
		return false
	}
}
