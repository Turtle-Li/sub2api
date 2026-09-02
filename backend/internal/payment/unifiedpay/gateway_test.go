package unifiedpay

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

const (
	testOrganizationID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testProductID      = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	testPaymentOrderID = "11111111-2222-4333-8444-555555555555"
	testEventID        = "cccccccc-dddd-4eee-8fff-000000000001"
	testAppID          = "app.sub2.sandbox"
	testRequestKeyID   = "request.key.sandbox"
	testWebhookKeyID   = "webhook.key.sandbox"
)

func TestCanonicalPayloadSignatureVector(t *testing.T) {
	seed, err := hex.DecodeString("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
	require.NoError(t, err)
	privateKey := ed25519.NewKeyFromSeed(seed)
	apiClient, err := newClient(testConfig(privateKey, "https://pay.example.test"))
	require.NoError(t, err)
	apiClient.now = func() time.Time { return time.Unix(1788120000, 0).UTC() }
	apiClient.nonceSource = func() (string, error) { return "nonce.sdk.vector.0001", nil }
	body := []byte(`{"amount_fen":1200}`)
	request, err := apiClient.newSignedRequest(context.Background(), http.MethodPost, "/v1/payment-orders?source=backend", body, "idem.sdk.request.0001")
	require.NoError(t, err)
	bodyHash := sha256.Sum256(body)
	payload := CanonicalPayload(http.MethodPost, AudienceSandbox, "/v1/payment-orders?source=backend", hex.EncodeToString(bodyHash[:]), testAppID, testRequestKeyID, "1788120000", "nonce.sdk.vector.0001", "idem.sdk.request.0001")
	require.True(t, ed25519.Verify(privateKey.Public().(ed25519.PublicKey), payload, mustBase64(t, request.Header.Get(HeaderSignature))))
}

func TestGatewayCreatesScopedAlipayOrderAndRejectsRedirects(t *testing.T) {
	privateKey := testPrivateKey()
	foreignCheckout := false
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.Equal(t, "/v1/payment-orders", request.RequestURI)
		verifySignedRequest(t, request, body, privateKey.Public().(ed25519.PublicKey))
		var input createPaymentOrderRequest
		require.NoError(t, json.Unmarshal(body, &input))
		require.Equal(t, "sub2:create:"+input.ProductOrderNo, request.Header.Get(HeaderIdempotencyKey))
		require.Equal(t, int64(1234), input.AmountFen)
		require.Equal(t, "balance", input.OrderType)
		checkout := server.URL + "/checkout/token"
		if foreignCheckout {
			checkout = "https://checkout.attacker.example/checkout/token"
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(writer).Encode(paymentOrderResponse{
			Environment: EnvironmentSandbox, OrganizationID: testOrganizationID, ProductID: testProductID,
			AppID: testAppID, PaymentOrderID: testPaymentOrderID, ProductOrderNo: input.ProductOrderNo,
			OrderType: input.OrderType, AmountFen: input.AmountFen, Currency: "CNY", PaymentMethod: PaymentMethodAlipay,
			Status: StatusPendingPayment, CheckoutURL: &checkout, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(30 * time.Minute),
		})
	}))
	defer server.Close()
	gateway, err := New(testConfig(privateKey, server.URL))
	require.NoError(t, err)
	result, err := gateway.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID: "sub2_20260902abcd1234", Amount: "12.34", PaymentType: payment.TypeAlipay,
		OrderType: "balance", Subject: "Sub2API 12.34 CNY", ReturnURL: gateway.ReturnURL(), ExpiresInSeconds: 1800,
	})
	require.NoError(t, err)
	require.Equal(t, testPaymentOrderID, result.TradeNo)
	require.Contains(t, result.PayURL, "/checkout/token")

	foreignCheckout = true
	_, err = gateway.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID: "sub2_20260902wxyz5678", Amount: "12.34", PaymentType: payment.TypeAlipay,
		OrderType: "balance", Subject: "Sub2API 12.34 CNY", ReturnURL: gateway.ReturnURL(), ExpiresInSeconds: 1800,
	})
	require.ErrorIs(t, err, ErrInvalidResponse)
}

func TestGatewayDoesNotFulfillManualReviewOrderFromActiveQuery(t *testing.T) {
	privateKey := testPrivateKey()
	transactionID := "alipay_trade_001"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/v1/payment-orders/"+testPaymentOrderID, request.RequestURI)
		verifySignedRequest(t, request, nil, privateKey.Public().(ed25519.PublicKey))
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(paymentOrderResponse{
			Environment: EnvironmentSandbox, OrganizationID: testOrganizationID, ProductID: testProductID,
			AppID: testAppID, PaymentOrderID: testPaymentOrderID, ProductOrderNo: "sub2_20260902abcd1234",
			OrderType: "balance", AmountFen: 1234, PaidAmountFen: 1234, RefundableAmountFen: 1234,
			Currency: "CNY", PaymentMethod: PaymentMethodAlipay, Status: StatusPaid,
			ChannelTransactionID: &transactionID, NeedsManualReview: true,
			CreatedAt: time.Now(), ExpiresAt: time.Now().Add(30 * time.Minute),
		})
	}))
	defer server.Close()
	gateway, err := New(testConfig(privateKey, server.URL))
	require.NoError(t, err)

	result, err := gateway.QueryOrder(context.Background(), testPaymentOrderID)
	require.NoError(t, err)
	require.Equal(t, payment.ProviderStatusPending, result.Status)
	require.Equal(t, "true", result.Metadata["needs_manual_review"])
}

func TestGatewayClassifiesAmbiguousCreateWhenReconciliationAlsoFails(t *testing.T) {
	privateKey := testPrivateKey()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(`{"error":"temporarily_unavailable","retryable":true}`))
	}))
	defer server.Close()
	gateway, err := New(testConfig(privateKey, server.URL))
	require.NoError(t, err)

	_, err = gateway.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID: "sub2_20260902uncertain", Amount: "12.34", PaymentType: payment.TypeAlipay,
		OrderType: "balance", Subject: "Sub2API 12.34 CNY", ReturnURL: gateway.ReturnURL(), ExpiresInSeconds: 1800,
	})
	require.ErrorIs(t, err, ErrCreateStateUnconfirmed)
}

func TestPaymentOrderResponseRejectsPaidStateWithoutFundsEvidence(t *testing.T) {
	result := paymentOrderResponse{
		PaymentOrderID: testPaymentOrderID, ProductOrderNo: "sub2_20260902abcd1234",
		OrderType: "balance", AmountFen: 1234, PaidAmountFen: 1234, RefundableAmountFen: 1234,
		Currency: "CNY", PaymentMethod: PaymentMethodAlipay, Status: StatusPaid,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(30 * time.Minute),
	}
	require.False(t, validPaymentOrderResponse(result))

	transactionID := "alipay_trade_001"
	result.ChannelTransactionID = &transactionID
	require.True(t, validPaymentOrderResponse(result))
}

func TestWebhookVerifierUsesExactBodyAndRejectsDuplicateHeader(t *testing.T) {
	privateKey := testPrivateKey()
	now := time.Unix(1788120000, 0).UTC()
	config := testConfig(privateKey, "https://pay.example.test")
	config.Clock = func() time.Time { return now }
	gateway, err := New(config)
	require.NoError(t, err)
	body := []byte(`{"schema_version":"payment-webhook.v1","event_id":"` + testEventID + `","event_type":"payment.order.paid","occurred_at":"2026-08-31T00:00:00Z","environment":"sandbox","organization_id":"` + testOrganizationID + `","product_id":"` + testProductID + `","product_code":"sub2","app_id":"` + testAppID + `","sequence":7,"origin_request_id":"request.sdk.000001","resource":{"payment_order_id":"` + testPaymentOrderID + `","product_order_no":"sub2_20260902abcd1234","order_type":"balance","amount_fen":1200,"paid_amount_fen":1200,"refunded_amount_fen":0,"reserved_refund_amount_fen":0,"refundable_amount_fen":1200,"currency":"CNY","payment_method":"alipay","status":"PAID","channel_out_trade_no":"sub2_channel_0001","channel_transaction_id":"txn_001","paid_at":"2026-08-31T00:00:00Z","closed_at":null}}`)
	headers := http.Header{"Content-Type": []string{"application/json"}}
	headers.Set(HeaderWebhookKeyID, testWebhookKeyID)
	headers.Set(HeaderWebhookTimestamp, strconv.FormatInt(now.Unix(), 10))
	headers.Set(HeaderWebhookEventID, testEventID)
	headers.Set(HeaderWebhookSignature, base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(WebhookSignaturePayload(testWebhookKeyID, now.Unix(), testEventID, body)))))
	verified, err := gateway.VerifyWebhook(headers, body)
	require.NoError(t, err)
	require.Equal(t, int64(7), verified.Sequence)

	headers["x-pay-webhook-key-id"] = []string{testWebhookKeyID}
	_, err = gateway.VerifyWebhook(headers, body)
	require.ErrorIs(t, err, ErrDuplicateWebhookHeader)
}

func testPrivateKey() ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func testConfig(privateKey ed25519.PrivateKey, baseURL string) Config {
	return Config{
		Enabled: true, BaseURL: baseURL, Environment: EnvironmentSandbox,
		OrganizationID: testOrganizationID, ProductID: testProductID, AppID: testAppID,
		RequestKeyID: testRequestKeyID, RequestPrivateKey: privateKey,
		WebhookPublicKeys: map[string]ed25519.PublicKey{testWebhookKeyID: privateKey.Public().(ed25519.PublicKey)},
		ReturnURL:         "http://127.0.0.1:3000/payment/result",
	}
}

func verifySignedRequest(t *testing.T, request *http.Request, body []byte, publicKey ed25519.PublicKey) {
	t.Helper()
	bodyHash := sha256.Sum256(body)
	payload := CanonicalPayload(request.Method, request.Header.Get(HeaderAudience), request.RequestURI,
		hex.EncodeToString(bodyHash[:]), request.Header.Get(HeaderAppID), request.Header.Get(HeaderKeyID),
		request.Header.Get(HeaderTimestamp), request.Header.Get(HeaderNonce), request.Header.Get(HeaderIdempotencyKey))
	require.True(t, ed25519.Verify(publicKey, payload, mustBase64(t, request.Header.Get(HeaderSignature))))
}

func mustBase64(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	require.NoError(t, err)
	return decoded
}
