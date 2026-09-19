//go:build integration

package service

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

type invoiceLockRefundProvider struct {
	started chan struct{}
	release chan struct{}
}

type invoiceLockRefundLoadBalancer struct{}

func (invoiceLockRefundLoadBalancer) GetInstanceConfig(context.Context, int64) (map[string]string, error) {
	return map[string]string{}, nil
}

func (invoiceLockRefundLoadBalancer) SelectInstance(context.Context, string, payment.PaymentType, payment.Strategy, float64) (*payment.InstanceSelection, error) {
	return nil, nil
}

func (p *invoiceLockRefundProvider) Name() string { return "invoice-lock-refund" }

func (p *invoiceLockRefundProvider) ProviderKey() string { return payment.TypeAlipay }

func (p *invoiceLockRefundProvider) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeAlipay}
}

func (p *invoiceLockRefundProvider) CreatePayment(context.Context, payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	return nil, nil
}

func (p *invoiceLockRefundProvider) QueryOrder(context.Context, string) (*payment.QueryOrderResponse, error) {
	return nil, nil
}

func (p *invoiceLockRefundProvider) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	return nil, nil
}

func (p *invoiceLockRefundProvider) Refund(ctx context.Context, _ payment.RefundRequest) (*payment.RefundResponse, error) {
	select {
	case p.started <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
		return &payment.RefundResponse{RefundID: "invoice-lock-refund", Status: payment.ProviderStatusPending}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestPaymentInvoicePostgresLockingAndDeliveryClaims(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:18-alpine", tcpostgres.WithDatabase("sub2api_invoice_test"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))
	_, err = db.ExecContext(ctx, `CREATE TABLE unified_payment_refund_attempts(order_id BIGINT,needs_manual_review BOOLEAN NOT NULL DEFAULT FALSE); CREATE TABLE unified_payment_refund_events(order_id BIGINT,action TEXT NOT NULL)`)
	require.NoError(t, err)
	user := createPaymentInvoicePostgresUser(t, ctx, client)
	svc := &PaymentService{entClient: client}
	input := CreateInvoiceRequestInput{TitleType: InvoiceTitleTypePersonal, Title: "PG invoice", RecipientEmail: "invoice@example.test"}

	t.Run("invoice holds the order lock until its outer transaction commits", func(t *testing.T) {
		order := createPaymentInvoicePostgresOrder(t, ctx, client, user, "outer-lock")
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()
		invoice, err := svc.CreateOrResubmitInvoiceRequest(dbent.NewTxContext(ctx, tx), order.ID, user.ID, input)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() {
			_, e := db.ExecContext(ctx, `UPDATE payment_orders SET status='REFUND_REQUESTED' WHERE id=$1 /* invoice_test_refund_lock */`, order.ID)
			done <- e
		}()
		require.Eventually(t, func() bool {
			var n int
			e := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%invoice_test_refund_lock%'`).Scan(&n)
			return e == nil && n == 1
		}, 5*time.Second, 20*time.Millisecond)
		require.NoError(t, tx.Commit())
		require.NoError(t, <-done)
		_, err = svc.AdminUpdateOrderInvoiceRequest(ctx, order.ID, 9001, AdminUpdateInvoiceInput{Status: InvoiceStatusProcessing})
		require.Error(t, err, "refund start must prevent invoice processing")
		persisted, err := client.PaymentInvoiceRequest.Get(ctx, invoice.ID)
		require.NoError(t, err)
		require.Equal(t, InvoiceStatusPending, persisted.Status)
	})

	t.Run("refund committed ahead of issuance is rechecked after lock acquisition", func(t *testing.T) {
		order := createPaymentInvoicePostgresOrder(t, ctx, client, user, "refund-first")
		invoice, err := svc.CreateOrResubmitInvoiceRequest(ctx, order.ID, user.ID, input)
		require.NoError(t, err)
		_, err = svc.AdminUpdateOrderInvoiceRequest(ctx, order.ID, 9001, AdminUpdateInvoiceInput{Status: InvoiceStatusProcessing})
		require.NoError(t, err)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, `UPDATE payment_orders SET status='REFUNDING' WHERE id=$1`, order.ID)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() {
			_, e := svc.AdminUpdateOrderInvoiceRequest(ctx, order.ID, 9001, AdminUpdateInvoiceInput{Status: InvoiceStatusIssued, InvoiceItemName: "Services", InvoiceNumber: "202609100001", Document: &InvoicePDFInput{Filename: "official.pdf", Data: []byte("%PDF-1.7\n%%EOF")}})
			done <- e
		}()
		require.Eventually(t, func() bool {
			var n int
			e := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%payment_orders%'`).Scan(&n)
			return e == nil && n > 0
		}, 5*time.Second, 20*time.Millisecond)
		require.NoError(t, tx.Commit())
		require.Error(t, <-done)
		persisted, err := client.PaymentInvoiceRequest.Get(ctx, invoice.ID)
		require.NoError(t, err)
		require.Equal(t, InvoiceStatusProcessing, persisted.Status)
		count, err := client.PaymentInvoiceDocument.Query().Count(ctx)
		require.NoError(t, err)
		require.Zero(t, count, "blocked issuance must not persist a PDF")
	})

	t.Run("issued invoice wins when the legacy refund claim waits on its order lock", func(t *testing.T) {
		order := createPaymentInvoicePostgresOrder(t, ctx, client, user, "issued-first")
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()
		txCtx := dbent.NewTxContext(ctx, tx)
		_, err = svc.CreateOrResubmitInvoiceRequest(txCtx, order.ID, user.ID, input)
		require.NoError(t, err)
		_, err = svc.AdminUpdateOrderInvoiceRequest(txCtx, order.ID, 9001, AdminUpdateInvoiceInput{Status: InvoiceStatusProcessing})
		require.NoError(t, err)
		_, err = svc.AdminUpdateOrderInvoiceRequest(txCtx, order.ID, 9001, AdminUpdateInvoiceInput{
			Status: InvoiceStatusIssued, InvoiceItemName: "Services", InvoiceNumber: "202609100002",
			Document: &InvoicePDFInput{Filename: "issued-first.pdf", Data: []byte("%PDF-1.7\n%%EOF")},
		})
		require.NoError(t, err)

		claimDone := make(chan error, 1)
		go func() {
			_, claimErr := (&PaymentService{entClient: client}).ExecuteRefund(ctx, &RefundPlan{
				OrderID: order.ID, Order: order, RefundAmount: order.Amount, Reason: "invoice lock test",
			})
			claimDone <- claimErr
		}()
		require.Eventually(t, func() bool {
			var n int
			err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%payment_orders%'`).Scan(&n)
			return err == nil && n > 0
		}, 5*time.Second, 20*time.Millisecond)
		require.NoError(t, tx.Commit())
		err = <-claimDone
		require.Equal(t, "REFUND_INVOICED_ORDER", infraerrors.Reason(err))

		persisted, err := client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusCompleted, persisted.Status)
		require.Zero(t, persisted.RefundRequestedAmount)
	})

	t.Run("legacy refund claim prevents concurrent invoice issuance", func(t *testing.T) {
		order := createPaymentInvoicePostgresOrder(t, ctx, client, user, "refund-first-service")
		invoice, err := svc.CreateOrResubmitInvoiceRequest(ctx, order.ID, user.ID, input)
		require.NoError(t, err)
		_, err = svc.AdminUpdateOrderInvoiceRequest(ctx, order.ID, 9001, AdminUpdateInvoiceInput{Status: InvoiceStatusProcessing})
		require.NoError(t, err)
		instance, err := client.PaymentProviderInstance.Create().
			SetProviderKey(payment.TypeAlipay).
			SetName("invoice-lock-provider").
			SetConfig("{}").
			SetSupportedTypes("alipay").
			SetEnabled(true).
			SetRefundEnabled(true).
			Save(ctx)
		require.NoError(t, err)
		order, err = client.PaymentOrder.UpdateOneID(order.ID).
			SetProviderInstanceID(fmt.Sprintf("%d", instance.ID)).
			Save(ctx)
		require.NoError(t, err)

		provider := &invoiceLockRefundProvider{started: make(chan struct{}, 1), release: make(chan struct{})}
		originalFactory := createPaymentProviderFromInstance
		createPaymentProviderFromInstance = func(string, string, map[string]string) (payment.Provider, error) {
			return provider, nil
		}
		t.Cleanup(func() { createPaymentProviderFromInstance = originalFactory })
		releaseProvider := false
		defer func() {
			if !releaseProvider {
				close(provider.release)
			}
		}()

		refundDone := make(chan error, 1)
		refundService := &PaymentService{entClient: client, loadBalancer: invoiceLockRefundLoadBalancer{}}
		go func() {
			_, refundErr := refundService.ExecuteRefund(ctx, &RefundPlan{
				OrderID: order.ID, Order: order, RefundAmount: order.Amount, Reason: "refund lock test",
			})
			refundDone <- refundErr
		}()
		select {
		case <-provider.started:
		case <-time.After(5 * time.Second):
			t.Fatal("legacy refund did not reach the provider after claiming the order")
		}

		_, err = svc.AdminUpdateOrderInvoiceRequest(ctx, order.ID, 9001, AdminUpdateInvoiceInput{
			Status: InvoiceStatusIssued, InvoiceItemName: "Services", InvoiceNumber: "202609100003",
			Document: &InvoicePDFInput{Filename: "refund-first.pdf", Data: []byte("%PDF-1.7\n%%EOF")},
		})
		require.Equal(t, "INVOICE_ORDER_NOT_ELIGIBLE", infraerrors.Reason(err))
		persistedInvoice, err := client.PaymentInvoiceRequest.Get(ctx, invoice.ID)
		require.NoError(t, err)
		require.Equal(t, InvoiceStatusProcessing, persistedInvoice.Status)

		close(provider.release)
		releaseProvider = true
		require.NoError(t, <-refundDone)
		persistedOrder, err := client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusRefundPending, persistedOrder.Status)
	})

	t.Run("concurrent delivery claims and stale finish use database CAS", func(t *testing.T) {
		order := createPaymentInvoicePostgresOrder(t, ctx, client, user, "claims")
		row, err := client.PaymentInvoiceRequest.Create().SetOrderID(order.ID).SetUserID(user.ID).SetTitleType(InvoiceTitleTypePersonal).SetTitle("Claim test").SetRecipientEmail("claim@example.test").SetAmount(order.PayAmount).SetCurrency("CNY").SetStatus(InvoiceStatusIssued).SetEmailDeliveryStatus(InvoiceEmailDeliveryPending).Save(ctx)
		require.NoError(t, err)
		var wg sync.WaitGroup
		claims := make(chan *invoiceEmailDeliveryClaim, 12)
		failures := make(chan error, 12)
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				instance := &PaymentService{entClient: client}
				claim, e := instance.claimInvoiceEmailDelivery(ctx, row.ID)
				if e != nil {
					failures <- e
				}
				if claim != nil {
					claims <- claim
				}
			}()
		}
		wg.Wait()
		close(claims)
		close(failures)
		for e := range failures {
			require.NoError(t, e)
		}
		require.Len(t, claims, 1)
		original := <-claims
		_, err = client.PaymentInvoiceRequest.UpdateOneID(row.ID).SetEmailDeliveryClaimedAt(time.Now().UTC().Add(-10 * time.Minute)).Save(ctx)
		require.NoError(t, err)
		replacement, err := svc.claimInvoiceEmailDelivery(ctx, row.ID)
		require.NoError(t, err)
		require.NotNil(t, replacement)
		require.NotEqual(t, original.Token, replacement.Token)
		require.NoError(t, svc.finishInvoiceEmailDelivery(ctx, original, nil))
		persisted, err := client.PaymentInvoiceRequest.Get(ctx, row.ID)
		require.NoError(t, err)
		require.Equal(t, InvoiceEmailDeliverySending, persisted.EmailDeliveryStatus)
		require.NoError(t, svc.finishInvoiceEmailDelivery(ctx, replacement, nil))
		persisted, err = client.PaymentInvoiceRequest.Get(ctx, row.ID)
		require.NoError(t, err)
		require.Equal(t, InvoiceEmailDeliverySent, persisted.EmailDeliveryStatus)
		require.Equal(t, 2, persisted.EmailDeliveryAttempts)
	})
}

func createPaymentInvoicePostgresUser(t *testing.T, ctx context.Context, client *dbent.Client) *dbent.User {
	t.Helper()
	user, err := client.User.Create().
		SetEmail("invoice-postgres@example.com").
		SetPasswordHash("hash").
		SetUsername("invoice-postgres").
		Save(ctx)
	require.NoError(t, err)
	return user
}

func createPaymentInvoicePostgresOrder(t *testing.T, ctx context.Context, client *dbent.Client, user *dbent.User, suffix string) *dbent.PaymentOrder {
	t.Helper()
	serial := time.Now().UnixNano()
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(108).
		SetPayAmount(108).
		SetFeeRate(0).
		SetRechargeCode(fmt.Sprintf("INVOICE-PG-%s-%d", suffix, serial)).
		SetOutTradeNo(fmt.Sprintf("sub2_invoice_pg_%s_%d", suffix, serial)).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo(fmt.Sprintf("trade-%s-%d", suffix, serial)).
		SetOrderType(payment.OrderTypeBalance).
		SetProviderSnapshot(map[string]any{"schema_version": 2, "currency": "CNY"}).
		SetStatus(OrderStatusCompleted).
		SetPaidAt(time.Now().UTC()).
		SetCompletedAt(time.Now().UTC()).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("invoice.example.com").
		Save(ctx)
	require.NoError(t, err)
	return order
}
