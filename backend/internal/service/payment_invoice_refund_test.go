package service

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func createRefundInvoice(t *testing.T, ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, status string) {
	t.Helper()
	builder := client.PaymentInvoiceRequest.Create().
		SetOrderID(order.ID).
		SetUserID(order.UserID).
		SetTitleType(InvoiceTitleTypePersonal).
		SetTitle("Refund admission test").
		SetRecipientEmail("refund-invoice@example.test").
		SetAmount(order.PayAmount).
		SetCurrency(PaymentOrderCurrency(order)).
		SetStatus(status)
	if status == InvoiceStatusIssued {
		builder.SetIssuedAt(time.Now().UTC())
	}
	if status == InvoiceStatusRejected {
		builder.SetRejectionReason("test rejection")
	}
	_, err := builder.Save(ctx)
	require.NoError(t, err)
}
