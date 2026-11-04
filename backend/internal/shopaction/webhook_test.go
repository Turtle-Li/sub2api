package shopaction

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Public interoperability vector from the pinned Apache-2.0 reference's
// libraries/go/webhook_test.go:TestWebhookSign; this is not a runtime secret.
const vectorSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"

func TestStandardWebhookInteroperability(t *testing.T) {
	sig, err := signature([]byte(vectorSecret), "msg_p5jXN8AQM9LWM0D4loKWxJek", 1614265330, []byte(`{"test": 2432232314}`))
	require.NoError(t, err)
	require.Equal(t, "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=", sig)
	changed, err := signature([]byte(vectorSecret), "msg_p5jXN8AQM9LWM0D4loKWxJek", 1614265330, []byte(`{"test":2432232314}`))
	require.NoError(t, err)
	require.NotEqual(t, sig, changed)
	_, err = signature([]byte("invalid"), "id", 1, nil)
	require.Error(t, err)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebhookFinalResultAndStableSignedReplay(t *testing.T) {
	input := Request{Version: 1, Event: Paid, ActionID: "act_example", Source: "shop-test", Order: Order{ID: 42, ProductID: 3, AmountFen: 600, Currency: "CNY", PaidAt: time.Unix(1700000000, 0).UTC()}, Params: json.RawMessage(`{"sku":"example"}`)}
	valid := `{"version":1,"action_id":"act_example","order_id":42,"status":"completed","receipt_id":"receipt-example","delivery":{"kind":"none"}}`
	for _, tc := range []struct {
		name  string
		code  int
		body  string
		kind  string
		retry bool
	}{
		{"final none", 200, valid, "", false},
		{"final text", 200, strings.Replace(valid, `"kind":"none"`, `"kind":"text","content":"sk-test-secret-value"`, 1), "", false},
		{"accepted", 202, valid, "ASYNC_NOT_SUPPORTED", false},
		{"bare success", 200, "", "RESULT_INVALID", false},
		{"wrong order", 200, strings.Replace(valid, `"order_id":42`, `"order_id":99`, 1), "RESULT_INVALID", false},
		{"wrong action", 200, strings.Replace(valid, "act_example", "act_other", 1), "RESULT_INVALID", false},
		{"multiple json", 200, valid + valid, "RESULT_INVALID", false},
		{"too large", 200, strings.Repeat("x", 32769), "RESULT_INVALID", false},
		{"authentication", 403, "sk-test-secret-value", "HTTP_REJECTED", false},
		{"retry", 503, "sk-test-secret-value", "HTTP_REJECTED", true},
		{"throttle", 429, "", "HTTP_REJECTED", true},
		{"redirect", 302, "", "HTTP_REJECTED", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWebhook(Integration{ID: "test", Version: 1, Adapter: HTTP, Endpoint: "https://hooks.example.com/deliver", CredentialAlias: "test"}, func(context.Context, string) ([]byte, error) { return []byte(vectorSecret), nil })
			var first []byte
			calls := 0
			w.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				raw, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				if first == nil {
					first = append([]byte(nil), raw...)
				} else {
					require.Equal(t, first, raw)
				}
				require.Equal(t, input.ActionID, r.Header.Get("Idempotency-Key"))
				require.Equal(t, input.ActionID, r.Header.Get("webhook-id"))
				ts, err := strconv.ParseInt(r.Header.Get("webhook-timestamp"), 10, 64)
				require.NoError(t, err)
				require.WithinDuration(t, time.Now(), time.Unix(ts, 0), 2*time.Second)
				sig, err := signature([]byte(vectorSecret), input.ActionID, ts, raw)
				require.NoError(t, err)
				require.Equal(t, sig, r.Header.Get("webhook-signature"))
				return &http.Response{StatusCode: tc.code, Header: http.Header{"Location": []string{"https://other.example.com"}, "Retry-After": []string{"999999999999999"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})
			for range 2 {
				result, err := w.Execute(context.Background(), input)
				if tc.kind == "" {
					require.NoError(t, err)
					require.Equal(t, "completed", result.Status)
				} else {
					require.Error(t, err)
					failure := Classify(err)
					require.Equal(t, tc.kind, failure.Kind)
					require.Equal(t, tc.retry, failure.Retryable)
					require.NotContains(t, err.Error(), "sk-test-secret-value")
				}
			}
			require.Equal(t, 2, calls, "redirects must not be followed")
		})
	}
}

func TestWebhookReadyNeverExecutesFulfillment(t *testing.T) {
	i := Integration{ID: "test", Version: 1, Adapter: HTTP, Endpoint: "https://hooks.example.com/deliver", CredentialAlias: "test"}
	w := NewWebhook(i, func(context.Context, string) ([]byte, error) { return []byte(vectorSecret), nil })
	calls := 0
	w.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, http.MethodGet, r.Method)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	require.NoError(t, w.Ready(context.Background()))
	require.Zero(t, calls)
	w.integration.ReadyEndpoint = "https://hooks.example.com/ready"
	require.NoError(t, w.Ready(context.Background()))
	require.Equal(t, 1, calls)
}
