package unifiedpay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

const (
	testRefundRequestID = "66666666-7777-4888-8999-aaaaaaaaaaaa"
	testProductRefundNo = "sub2.refund.000001"
)

func TestGatewayCreateUnifiedRefundSignsAcceptedAlipayRequest(t *testing.T) {
	privateKey := testPrivateKey()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "/v1/refund-requests", request.RequestURI)
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		verifySignedRequest(t, request, body, testPublicKey(t, privateKey))
		require.Equal(t, "sub2:refund:attempt-000001", request.Header.Get(HeaderIdempotencyKey))

		var input createRefundRequest
		require.NoError(t, json.Unmarshal(body, &input))
		require.Equal(t, testPaymentOrderID, input.PaymentOrderID)
		require.Equal(t, testProductRefundNo, input.ProductRefundNo)
		require.Equal(t, int64(1234), input.AmountFen)
		require.Equal(t, "other", input.ReasonCode)
		require.NotNil(t, input.ReasonSummary)
		require.Equal(t, "Sub2 administrator refund", *input.ReasonSummary)

		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		require.NoError(t, json.NewEncoder(writer).Encode(refundTestResponse(PaymentMethodAlipay, RefundStatusApproved, false)))
	}))
	defer server.Close()

	gateway, err := New(testConfig(privateKey, server.URL))
	require.NoError(t, err)
	summary := "Sub2 administrator refund"
	result, err := gateway.CreateUnifiedRefund(context.Background(), payment.UnifiedRefundRequest{
		PaymentOrderID: testPaymentOrderID, ProductRefundNo: testProductRefundNo,
		IdempotencyKey: "sub2:refund:attempt-000001", AmountFen: 1234,
		ReasonCode: "other", ReasonSummary: &summary,
	})
	require.NoError(t, err)
	require.Equal(t, testRefundRequestID, result.RefundRequestID)
	require.Equal(t, PaymentMethodAlipay, result.PaymentMethod)
	require.Equal(t, RefundStatusApproved, result.Status)
	require.False(t, result.NeedsManualReview)
}

func TestGatewayGetUnifiedRefundSignsAndCorrelatesWechatResource(t *testing.T) {
	privateKey := testPrivateKey()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodGet, request.Method)
		require.Equal(t, "/v1/refund-requests/"+testRefundRequestID, request.RequestURI)
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.Empty(t, body)
		require.Empty(t, request.Header.Get(HeaderIdempotencyKey))
		verifySignedRequest(t, request, body, testPublicKey(t, privateKey))

		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode(refundTestResponse(PaymentMethodWechatPay, RefundStatusUnknown, true)))
	}))
	defer server.Close()

	gateway, err := New(testConfig(privateKey, server.URL))
	require.NoError(t, err)
	result, err := gateway.GetUnifiedRefund(context.Background(), testRefundRequestID, payment.UnifiedRefundExpectation{
		PaymentOrderID: testPaymentOrderID, ProductRefundNo: testProductRefundNo, AmountFen: 1234,
	})
	require.NoError(t, err)
	require.Equal(t, PaymentMethodWechatPay, result.PaymentMethod)
	require.Equal(t, RefundStatusUnknown, result.Status)
	require.True(t, result.NeedsManualReview)
}

func TestGatewayCreateUnifiedRefundRetainsUnconfirmedStateForInvalidResponses(t *testing.T) {
	testCases := []struct {
		name   string
		mutate func(*refundResponse)
		raw    string
	}{
		{
			name: "foreign scope",
			mutate: func(result *refundResponse) {
				result.OrganizationID = "dddddddd-eeee-4eee-8eee-ffffffffffff"
			},
		},
		{
			name: "wrong amount",
			mutate: func(result *refundResponse) {
				result.AmountFen = 1233
			},
		},
		{
			name: "wrong payment order id",
			mutate: func(result *refundResponse) {
				result.PaymentOrderID = "99999999-8888-4888-8999-777777777777"
			},
		},
		{
			name: "invalid refund request id",
			mutate: func(result *refundResponse) {
				result.RefundRequestID = "not-a-refund-id"
			},
		},
		{
			name: "unsupported status",
			mutate: func(result *refundResponse) {
				result.Status = "CANCELLED"
			},
		},
		{name: "malformed JSON", raw: `{"environment":`},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			privateKey := testPrivateKey()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				body, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				verifySignedRequest(t, request, body, testPublicKey(t, privateKey))
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusAccepted)
				if testCase.raw != "" {
					_, _ = writer.Write([]byte(testCase.raw))
					return
				}
				result := refundTestResponse(PaymentMethodAlipay, RefundStatusApproved, false)
				testCase.mutate(&result)
				require.NoError(t, json.NewEncoder(writer).Encode(result))
			}))
			defer server.Close()

			gateway, err := New(testConfig(privateKey, server.URL))
			require.NoError(t, err)
			_, err = gateway.CreateUnifiedRefund(context.Background(), refundTestRequest())
			require.ErrorIs(t, err, ErrRefundStateUnconfirmed)
		})
	}
}

func TestGatewayGetUnifiedRefundRejectsMismatchedExpectation(t *testing.T) {
	privateKey := testPrivateKey()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		verifySignedRequest(t, request, body, testPublicKey(t, privateKey))
		result := refundTestResponse(PaymentMethodWechatPay, RefundStatusProcessing, false)
		result.ProductRefundNo = "sub2.refund.000002"
		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode(result))
	}))
	defer server.Close()

	gateway, err := New(testConfig(privateKey, server.URL))
	require.NoError(t, err)
	_, err = gateway.GetUnifiedRefund(context.Background(), testRefundRequestID, payment.UnifiedRefundExpectation{
		PaymentOrderID: testPaymentOrderID, ProductRefundNo: testProductRefundNo, AmountFen: 1234,
	})
	require.ErrorIs(t, err, ErrRefundStateUnconfirmed)
}

func TestGatewayCreateUnifiedRefundTreatsRemoteErrorsAsUnconfirmed(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			privateKey := testPrivateKey()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				body, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				verifySignedRequest(t, request, body, testPublicKey(t, privateKey))
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(status)
				_, _ = writer.Write([]byte(`{"error":"refund_request_rejected","retryable":false}`))
			}))
			defer server.Close()

			gateway, err := New(testConfig(privateKey, server.URL))
			require.NoError(t, err)
			_, err = gateway.CreateUnifiedRefund(context.Background(), refundTestRequest())
			require.ErrorIs(t, err, ErrRefundStateUnconfirmed)
		})
	}
}

func TestGatewayRejectsInvalidUnifiedRefundRequestBeforeNetwork(t *testing.T) {
	gateway, err := New(testConfig(testPrivateKey(), "https://pay.example.test"))
	require.NoError(t, err)
	request := refundTestRequest()
	request.ReasonCode = "admin_refund"
	_, err = gateway.CreateUnifiedRefund(context.Background(), request)
	require.ErrorIs(t, err, ErrInvalidRequest)

	request = refundTestRequest()
	request.AmountFen = 0
	_, err = gateway.CreateUnifiedRefund(context.Background(), request)
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestValidRefundResponseAcceptsAllContractStatuses(t *testing.T) {
	for _, status := range []string{
		RefundStatusApproved, RefundStatusProcessing, RefundStatusSucceeded, RefundStatusFailed, RefundStatusUnknown,
	} {
		t.Run(status, func(t *testing.T) {
			require.True(t, validRefundResponse(refundTestResponse(PaymentMethodAlipay, status, status == RefundStatusUnknown)))
		})
	}
}

func refundTestRequest() payment.UnifiedRefundRequest {
	summary := "Sub2 administrator refund"
	return payment.UnifiedRefundRequest{
		PaymentOrderID: testPaymentOrderID, ProductRefundNo: testProductRefundNo,
		IdempotencyKey: "sub2:refund:attempt-000001", AmountFen: 1234,
		ReasonCode: "other", ReasonSummary: &summary,
	}
}

func refundTestResponse(paymentMethod, status string, manualReview bool) refundResponse {
	now := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	result := refundResponse{
		Environment: EnvironmentSandbox, OrganizationID: testOrganizationID, ProductID: testProductID,
		RefundRequestID: testRefundRequestID, PaymentOrderID: testPaymentOrderID,
		ProductRefundNo: testProductRefundNo, ChannelOutRefundNo: "sub2_refund_channel_000001",
		AmountFen: 1234, Currency: payment.DefaultPaymentCurrency, PaymentMethod: paymentMethod,
		Status: status, NeedsManualReview: manualReview, CreatedAt: now, UpdatedAt: now.Add(time.Minute),
	}
	if status == RefundStatusSucceeded || status == RefundStatusFailed {
		completedAt := now.Add(2 * time.Minute)
		result.CompletedAt = &completedAt
	}
	return result
}
