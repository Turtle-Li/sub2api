package unifiedpay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGatewayLookupPaymentOrderByProductOrderNo(t *testing.T) {
	privateKey := testPrivateKey()
	const productOrderNo = "sub2_lookup_20260919"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodGet, request.Method)
		require.Equal(t, "/v1/payment-orders?product_order_no="+productOrderNo, request.RequestURI)
		verifySignedRequest(t, request, nil, testPublicKey(t, privateKey))
		require.Empty(t, request.Header.Get(HeaderIdempotencyKey))
		require.NoError(t, json.NewEncoder(writer).Encode(paymentOrderResponse{
			Environment: EnvironmentSandbox, OrganizationID: testOrganizationID, ProductID: testProductID,
			AppID: testAppID, PaymentOrderID: testPaymentOrderID, ProductOrderNo: productOrderNo,
			OrderType: "reset_card", AmountFen: 1234, Currency: "CNY", PaymentMethod: PaymentMethodAlipay,
			Status: StatusPendingPayment, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
		}))
	}))
	defer server.Close()

	gateway, err := New(testConfig(privateKey, server.URL))
	require.NoError(t, err)
	lookup, err := gateway.LookupPaymentOrderByProductOrderNo(context.Background(), productOrderNo)
	require.NoError(t, err)
	require.True(t, lookup.Found)
	require.Equal(t, testPaymentOrderID, lookup.PaymentOrderID)
	require.Equal(t, productOrderNo, lookup.ProductOrderNo)
	require.Equal(t, "reset_card", lookup.OrderType)
	require.Equal(t, int64(1234), lookup.AmountFen)
	require.Equal(t, PaymentMethodAlipay, lookup.PaymentMethod)
}

func TestGatewayLookupPaymentOrderByProductOrderNoRequiresVerifiedScopedAbsence(t *testing.T) {
	privateKey := testPrivateKey()
	for _, testCase := range []struct {
		name string
		body string
		want error
	}{
		{
			name: "canonical not found",
			body: `{"error":"not_found","request_id":"request.lookup.000001","retryable":false}`,
		},
		{
			name: "missing request identifier",
			body: `{"error":"not_found","retryable":false}`,
			want: ErrInvalidResponse,
		},
		{
			name: "different business error is not absence",
			body: `{"error":"invalid_request","request_id":"request.lookup.000001","retryable":false}`,
			want: ErrInvalidResponse,
		},
		{
			name: "missing retryability is not explicit absence",
			body: `{"error":"not_found","request_id":"request.lookup.000001"}`,
			want: ErrInvalidResponse,
		},
		{
			name: "retryable absence is unsafe",
			body: `{"error":"not_found","request_id":"request.lookup.000001","retryable":true}`,
			want: ErrInvalidResponse,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				require.Equal(t, http.MethodGet, request.Method)
				verifySignedRequest(t, request, nil, testPublicKey(t, privateKey))
				writer.WriteHeader(http.StatusNotFound)
				_, _ = writer.Write([]byte(testCase.body))
			}))
			defer server.Close()

			gateway, err := New(testConfig(privateKey, server.URL))
			require.NoError(t, err)
			lookup, err := gateway.LookupPaymentOrderByProductOrderNo(context.Background(), "sub2_lookup_missing")
			if testCase.want != nil {
				require.ErrorIs(t, err, testCase.want)
				require.Nil(t, lookup)
				return
			}
			require.NoError(t, err)
			require.False(t, lookup.Found)
		})
	}
}

func TestGatewayLookupPaymentOrderByProductOrderNoRejectsForeignScope(t *testing.T) {
	privateKey := testPrivateKey()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.NoError(t, json.NewEncoder(writer).Encode(paymentOrderResponse{
			Environment: EnvironmentSandbox, OrganizationID: testOrganizationID, ProductID: testProductID,
			AppID: "app.foreign.sandbox", PaymentOrderID: testPaymentOrderID, ProductOrderNo: "sub2_lookup_foreign",
			OrderType: "reset_card", AmountFen: 1234, Currency: "CNY", PaymentMethod: PaymentMethodAlipay,
			Status: StatusPendingPayment, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
		}))
	}))
	defer server.Close()

	gateway, err := New(testConfig(privateKey, server.URL))
	require.NoError(t, err)
	lookup, err := gateway.LookupPaymentOrderByProductOrderNo(context.Background(), "sub2_lookup_foreign")
	require.ErrorIs(t, err, ErrInvalidResponse)
	require.Nil(t, lookup)
}
