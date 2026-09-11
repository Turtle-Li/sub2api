package unifiedpay

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

// Invoked by the central repository with ephemeral keys on an anonymous pipe.
// Never set SUB2_JOINT_STDIN during an ordinary test suite.
func TestJointCentralProtocolClient(t *testing.T) {
	if os.Getenv("SUB2_JOINT_STDIN") != "1" {
		t.Skip("central joint-test helper only")
	}
	var input struct {
		Phase          string   `json:"phase"`
		BaseURL        string   `json:"base_url"`
		AppID          string   `json:"app_id"`
		KeyID          string   `json:"key_id"`
		OrganizationID string   `json:"organization_id"`
		ProductID      string   `json:"product_id"`
		ReturnURL      string   `json:"return_url"`
		PrivateKey     []byte   `json:"private_key"`
		WebhookKeyID   string   `json:"webhook_key_id"`
		Orders         []string `json:"orders"`
		RefundID       string   `json:"refund_id"`
		Webhooks       []struct {
			Headers map[string]string `json:"headers"`
			Body    []byte            `json:"body"`
		} `json:"webhooks"`
	}
	require.NoError(t, json.NewDecoder(os.Stdin).Decode(&input))
	defer clear(input.PrivateKey)
	if len(input.PrivateKey) != 64 {
		t.Fatal("invalid ephemeral test key size")
	}
	private := ed25519.PrivateKey(input.PrivateKey)
	publicKey, ok := private.Public().(ed25519.PublicKey)
	require.True(t, ok)
	g, err := New(Config{Enabled: true, BaseURL: input.BaseURL, Environment: EnvironmentSandbox, OrganizationID: input.OrganizationID, ProductID: input.ProductID, AppID: input.AppID, RequestKeyID: input.KeyID, RequestPrivateKey: private, WebhookPublicKeys: map[string]ed25519.PublicKey{input.WebhookKeyID: publicKey}, ReturnURL: input.ReturnURL, SupportedMethods: []string{"alipay"}})
	require.NoError(t, err)
	ctx := context.Background()
	config, err := g.client.GetIntegrationConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, input.ProductID, config.ProductID)
	require.Contains(t, config.PaymentMethods, "alipay")
	result := struct {
		Orders   []string `json:"orders"`
		RefundID string   `json:"refund_id"`
	}{Orders: input.Orders, RefundID: input.RefundID}
	for _, event := range input.Webhooks {
		headers := http.Header{}
		for k, v := range event.Headers {
			headers.Set(k, v)
		}
		verified, err := g.VerifyWebhook(headers, event.Body)
		require.NoError(t, err)
		require.NotEmpty(t, verified.EventID)
		// Verifying a duplicate preserves the same event identity; durable inbox
		// deduplication is exercised by the separate PostgreSQL inbox suite.
		duplicate, err := g.VerifyWebhook(headers, event.Body)
		require.NoError(t, err)
		require.Equal(t, verified.EventID, duplicate.EventID)
	}
	switch input.Phase {
	case "create":
		for i, amount := range []string{"0.01", "0.02"} {
			request := payment.CreatePaymentRequest{OrderID: fmt.Sprintf("sub2_joint_%s_%d", input.ProductID[:8], i), Amount: amount, PaymentType: payment.TypeAlipay, OrderType: "balance", Subject: "local joint test", ReturnURL: input.ReturnURL, ExpiresInSeconds: 300}
			created, err := g.CreatePayment(ctx, request)
			require.NoError(t, err)
			require.NotEmpty(t, created.PayURL)
			replay, err := g.CreatePayment(ctx, request)
			require.NoError(t, err)
			require.Equal(t, created.TradeNo, replay.TradeNo)
			queried, err := g.QueryOrder(ctx, created.TradeNo)
			require.NoError(t, err)
			require.Equal(t, payment.ProviderStatusPending, queried.Status)
			result.Orders = append(result.Orders, created.TradeNo)
		}
	case "refund":
		require.Len(t, input.Orders, 2)
		paid, err := g.QueryOrder(ctx, input.Orders[0])
		require.NoError(t, err)
		require.Equal(t, payment.ProviderStatusPaid, paid.Status)
		refund, err := g.CreateUnifiedRefund(ctx, payment.UnifiedRefundRequest{PaymentOrderID: input.Orders[0], ProductRefundNo: "joint_refund_" + input.ProductID[:8], IdempotencyKey: "joint-refund-request-" + input.ProductID, AmountFen: 1, ReasonCode: "customer_request"})
		require.NoError(t, err)
		require.Equal(t, "APPROVED", refund.Status)
		result.RefundID = refund.RefundRequestID
		require.ErrorIs(t, g.CancelPayment(ctx, input.Orders[1]), payment.ErrUpstreamStateUnconfirmed)
	case "verify":
		refunded, err := g.QueryOrder(ctx, input.Orders[0])
		require.NoError(t, err)
		require.Equal(t, payment.ProviderStatusRefunded, refunded.Status)
		refund, err := g.GetUnifiedRefund(ctx, input.RefundID, payment.UnifiedRefundExpectation{PaymentOrderID: input.Orders[0], ProductRefundNo: "joint_refund_" + input.ProductID[:8], AmountFen: 1})
		require.NoError(t, err)
		require.Equal(t, "SUCCEEDED", refund.Status)
		closed, err := g.QueryOrder(ctx, input.Orders[1])
		require.NoError(t, err)
		require.Equal(t, payment.ProviderStatusFailed, closed.Status)
		require.GreaterOrEqual(t, len(input.Webhooks), 3)
	default:
		t.Fatal("unknown helper phase")
	}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	fmt.Printf("JOINT_RESULT %s\n", encoded)
}
