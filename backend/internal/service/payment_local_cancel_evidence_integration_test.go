//go:build integration

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/stretchr/testify/require"
)

// A claim revocation can prevent more work; it cannot revoke a cash fact that
// has already happened at the provider for the original immutable refund.
func TestLocalCancellationPostgresRecordsDispatchedSuccessAfterConflict(t *testing.T) {
	client, db, ctx := newLocalCancellationPostgresFixture(t)
	t.Setenv(runtimegate.StateFileEnv, "")
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	owner := createLocalCancellationPostgresUser(t, ctx, client, "post-dispatch-conflict")
	paidAt := time.Now().UTC().Add(-time.Minute)
	order := createLocalCancellationPostgresOrder(t, ctx, client, owner, localCancellationPostgresOrderInput{
		PayAmount: 12.34, Status: OrderStatusCancelled, PaymentTradeNo: "original-trusted-payment", PaidAt: &paidAt,
	})
	require.NoError(t, ensureLocalCancellationDirectRefundWorkTx(ctx, client, order, order.PayAmount))
	provider := &localCancellationPostgresRefundProvider{firstRefundStarted: make(chan struct{}, 1), releaseFirstRefund: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(provider.releaseFirstRefund) }) }
	defer release()
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := &PaymentService{entClient: client, registry: registry, providersLoaded: true}
	done := make(chan error, 1)
	go func() { _, err := svc.ReconcileLocalCancellationWork(ctx, "post-dispatch-test"); done <- err }()
	select {
	case <-provider.firstRefundStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("refund did not reach provider barrier")
	}

	require.NoError(t, svc.HandlePaymentNotification(ctx, &payment.PaymentNotification{
		OrderID: order.OutTradeNo, TradeNo: "conflicting-second-payment", Amount: order.PayAmount, Status: payment.NotificationStatusSuccess,
	}, payment.TypeAlipay))
	var status string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT status FROM payment_local_cancellation_work WHERE order_id=$1 AND work_kind='DIRECT_REFUND'", order.ID).Scan(&status))
	require.Equal(t, localCancellationWorkManualReview, status)
	release()
	select {
	case err := <-done:
		// The stale scheduling lease may report a conflict, but the trusted
		// provider success below must be durably represented independently.
		if err != nil {
			t.Logf("worker scheduling result: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider result was not processed")
	}

	var refundID, providerStatus string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT status, provider_refund_id, provider_status FROM payment_local_cancellation_work WHERE order_id=$1 AND work_kind='DIRECT_REFUND'", order.ID).Scan(&status, &refundID, &providerStatus))
	require.Equal(t, localCancellationWorkManualReview, status, "recording the original refund must not clear the second-payment conflict")
	require.Equal(t, "local-cancel-refund-1", refundID, "provider cash success must survive a revoked scheduling lease")
	require.Equal(t, payment.ProviderStatusSuccess, providerStatus)
	var successes int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM payment_audit_logs WHERE order_id=$1 AND action='LOCAL_CANCEL_DIRECT_REFUND_SUCCEEDED'", order.ID).Scan(&successes))
	require.Equal(t, 1, successes)
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCancelled, stored.Status)
	require.Equal(t, order.PaymentTradeNo, stored.PaymentTradeNo)
	unchanged, err := client.User.Get(ctx, owner.ID)
	require.NoError(t, err)
	require.Equal(t, owner.Balance, unchanged.Balance)
	_, err = svc.ReconcileLocalCancellationWork(context.Background(), "post-dispatch-replay")
	require.NoError(t, err)
	require.Len(t, provider.refundRequests(), 1, "the manual fence prevents a new refund submission")
}

type localCancellationTimeoutProvider struct {
	*localCancellationDirectCloseProvider
	blockedRef string
	started    chan struct{}
	once       sync.Once
}

func (p *localCancellationTimeoutProvider) QueryOrder(ctx context.Context, ref string) (*payment.QueryOrderResponse, error) {
	if ref == p.blockedRef {
		p.once.Do(func() { close(p.started) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.localCancellationDirectCloseProvider.QueryOrder(ctx, ref)
}

func TestLocalCancellationPostgresTimeoutDoesNotStarveLaterClose(t *testing.T) {
	client, db, ctx := newLocalCancellationPostgresFixture(t)
	t.Setenv(runtimegate.StateFileEnv, "")
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	owner := createLocalCancellationPostgresUser(t, ctx, client, "timeout-progress")
	blocked := createLocalCancellationPostgresOrder(t, ctx, client, owner, localCancellationPostgresOrderInput{})
	later := createLocalCancellationPostgresOrder(t, ctx, client, owner, localCancellationPostgresOrderInput{})
	provider := &localCancellationTimeoutProvider{localCancellationDirectCloseProvider: &localCancellationDirectCloseProvider{}, blockedRef: blocked.OutTradeNo, started: make(chan struct{})}
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := &PaymentService{entClient: client, registry: registry, providersLoaded: true}
	_, err := svc.CancelOrder(ctx, blocked.ID, owner.ID)
	require.NoError(t, err)
	_, err = svc.CancelOrder(ctx, later.ID, owner.ID)
	require.NoError(t, err)

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := svc.ReconcileLocalCancellationWork(workCtx, "timeout-progress"); done <- err }()
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first provider query did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled worker did not finish bounded cleanup")
	}
	var attempts int
	var availableAt time.Time
	var claimed bool
	require.NoError(t, db.QueryRowContext(ctx, "SELECT attempts, available_at, claimed_at IS NOT NULL FROM payment_local_cancellation_work WHERE order_id=$1 AND work_kind='CLOSE'", blocked.ID).Scan(&attempts, &availableAt, &claimed))
	require.Equal(t, 1, attempts, "provider failure retry must persist even after the work context is cancelled")
	require.True(t, availableAt.After(time.Now()), "a failed oldest item must yield priority through durable backoff")
	require.False(t, claimed)

	// Simulate the next recovery cycle after any unstarted claims have expired.
	_, err = db.ExecContext(ctx, "UPDATE payment_local_cancellation_work SET claimed_at=NOW()-INTERVAL '2 minutes' WHERE claimed_at IS NOT NULL")
	require.NoError(t, err)
	processed, err := svc.ReconcileLocalCancellationWork(ctx, "timeout-progress-next")
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	var status string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT status FROM payment_local_cancellation_work WHERE order_id=$1 AND work_kind='CLOSE'", later.ID).Scan(&status))
	require.Equal(t, localCancellationWorkCompleted, status)
	require.Equal(t, []string{later.OutTradeNo}, provider.closeRefs)
}
