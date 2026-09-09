//go:build unit

package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOwnedOrderResponseIncludesInvoiceButPublicProjectionExcludesInvoicePII(t *testing.T) {
	now := time.Now().UTC()
	taxID := "91310000MA12345678"
	order := &dbent.PaymentOrder{
		ID: 51, UserID: 7, Amount: 108, PayAmount: 108, FeeRate: 0,
		OutTradeNo: "sub2_invoice_projection", PaymentType: "alipay", OrderType: "balance",
		Status: service.OrderStatusCompleted, PaidAt: &now, CompletedAt: &now,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		ProductSnapshot: map[string]any{
			"name": "Purchased plan", "price": float64(108), "description": "Immutable purchase evidence",
		},
		Edges: dbent.PaymentOrderEdges{InvoiceRequest: &dbent.PaymentInvoiceRequest{
			ID: 61, OrderID: 51, UserID: 7, TitleType: service.InvoiceTitleTypeEnterprise,
			Title: "Example Technology", TaxIdentifier: &taxID, RecipientEmail: "finance@example.com",
			Amount: 108, Currency: "CNY", Status: service.InvoiceStatusPending, Revision: 1,
			Provider: "manual", RequestedAt: now, CreatedAt: now, UpdatedAt: now,
		}},
	}

	owned := sanitizePaymentOrderForResponse(order)
	require.NotNil(t, owned.Invoice)
	require.Equal(t, taxID, *owned.Invoice.TaxIdentifier)
	require.Equal(t, service.InvoicePaymentStatusPaid, owned.PaymentStatus)
	require.Equal(t, service.InvoiceFulfillmentStatusFulfilled, owned.FulfillmentStatus)
	require.Equal(t, "Purchased plan", owned.ProductSnapshot["name"])

	publicBody, err := json.Marshal(buildPublicOrderResult(order))
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(publicBody)), `"invoice":`)
	require.NotContains(t, string(publicBody), taxID)
	require.NotContains(t, strings.ToLower(string(publicBody)), "product_snapshot")
}
