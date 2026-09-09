//go:build unit

package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func createPaymentFulfillmentRecoveryFenceTables(t *testing.T, ctx context.Context, svc *PaymentService) {
	t.Helper()
	_, err := svc.entClient.ExecContext(ctx, `
		CREATE TABLE unified_payment_refund_attempts (
			order_id INTEGER NOT NULL,
			needs_manual_review BOOLEAN NOT NULL DEFAULT FALSE
		)`)
	require.NoError(t, err)
	_, err = svc.entClient.ExecContext(ctx, `
		CREATE TABLE unified_payment_refund_events (
			order_id INTEGER NOT NULL,
			action TEXT NOT NULL
		)`)
	require.NoError(t, err)
}

func TestRecoverPendingPaymentOrderFulfillmentsCompletesDurablePaidBalanceOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(payment.OrderTypeBalance).
		ClearPlanID().
		ClearSubscriptionGroupID().
		ClearSubscriptionDays().
		SetUpdatedAt(time.Now().UTC().Add(-2 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	credited := 0.0
	userRepo := &mockUserRepo{getByIDUser: &User{ID: order.UserID}}
	userRepo.updateBalanceFn = func(_ context.Context, userID int64, amount float64) error {
		require.Equal(t, order.UserID, userID)
		credited += amount
		return nil
	}
	redeemRepo := &paymentFulfillmentRedeemRepo{}
	svc := &PaymentService{
		entClient:     client,
		redeemService: NewRedeemService(redeemRepo, userRepo, nil, nil, nil, client, nil, nil),
		userRepo:      userRepo,
	}
	createPaymentFulfillmentRecoveryFenceTables(t, ctx, svc)

	recovered, err := svc.RecoverPendingPaymentOrderFulfillments(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	require.Equal(t, order.Amount, credited)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
	require.NotNil(t, reloaded.PaidAt)

	count, err := client.PaymentOrder.Query().Where(
		paymentorder.IDEQ(order.ID),
		paymentorder.StatusEQ(OrderStatusCompleted),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func createPaymentFulfillmentRecoveryBalanceOrder(
	t *testing.T,
	ctx context.Context,
	client *dbent.Client,
	status string,
	updatedAt time.Time,
) *dbent.PaymentOrder {
	t.Helper()
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, status, updatedAt)
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(payment.OrderTypeBalance).
		ClearPlanID().
		ClearSubscriptionGroupID().
		ClearSubscriptionDays().
		SetUpdatedAt(updatedAt).
		Save(ctx)
	require.NoError(t, err)
	return order
}

func newPaymentFulfillmentRecoveryBalanceService(
	t *testing.T,
	client *dbent.Client,
	order *dbent.PaymentOrder,
	credited *float64,
) *PaymentService {
	t.Helper()
	userRepo := &mockUserRepo{getByIDUser: &User{ID: order.UserID}}
	userRepo.updateBalanceFn = func(_ context.Context, userID int64, amount float64) error {
		require.Equal(t, order.UserID, userID)
		*credited += amount
		return nil
	}
	return &PaymentService{
		entClient:     client,
		redeemService: NewRedeemService(&paymentFulfillmentRedeemRepo{}, userRepo, nil, nil, nil, client, nil, nil),
		userRepo:      userRepo,
	}
}

func TestRecoverPendingPaymentOrderFulfillmentsClaimsStaleRechargingLease(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentRecoveryBalanceOrder(
		t,
		ctx,
		client,
		OrderStatusRecharging,
		time.Now().UTC().Add(-paymentFulfillmentLeaseDuration-time.Minute),
	)

	credited := 0.0
	svc := newPaymentFulfillmentRecoveryBalanceService(t, client, order, &credited)
	createPaymentFulfillmentRecoveryFenceTables(t, ctx, svc)

	recovered, err := svc.RecoverPendingPaymentOrderFulfillments(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	require.Equal(t, order.Amount, credited)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
}

func TestRecoverPendingPaymentOrderFulfillmentsSkipsRefundFencesBeforeLimit(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	// These 100 rows are older than the usable one. They must be filtered in
	// SQL before LIMIT, otherwise a one-minute scanner would never reach the
	// later paid order while the manual-review rows remain durable.
	for i := 0; i < paymentFulfillmentRecoveryLimit; i++ {
		fenced := createPaymentFulfillmentRecoveryBalanceOrder(
			t,
			ctx,
			client,
			OrderStatusPaid,
			time.Now().UTC().Add(-3*time.Minute-time.Duration(i)*time.Microsecond),
		)
		if i == 0 {
			createPaymentFulfillmentRecoveryFenceTables(t, ctx, &PaymentService{entClient: client})
		}
		_, err := client.ExecContext(ctx, `
			INSERT INTO unified_payment_refund_attempts (order_id, needs_manual_review) VALUES (?, TRUE)`, fenced.ID)
		require.NoError(t, err)
	}

	eligible := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))
	credited := 0.0
	svc := newPaymentFulfillmentRecoveryBalanceService(t, client, eligible, &credited)

	recovered, err := svc.RecoverPendingPaymentOrderFulfillments(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	require.Equal(t, eligible.Amount, credited)

	reloaded, err := client.PaymentOrder.Get(ctx, eligible.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
}

func TestRecoverPendingPaymentOrderFulfillmentsExcludesRefundFencesAndRefundStates(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name       string
		status     string
		fenceTable string
		fenceSQL   string
	}{
		{
			name:       "manual review attempt",
			status:     OrderStatusPaid,
			fenceTable: "unified_payment_refund_attempts",
			fenceSQL:   `INSERT INTO unified_payment_refund_attempts (order_id, needs_manual_review) VALUES (?, TRUE)`,
		},
		{
			name:       "uncorrelated refund event",
			status:     OrderStatusPaid,
			fenceTable: "unified_payment_refund_events",
			fenceSQL:   `INSERT INTO unified_payment_refund_events (order_id, action) VALUES (?, 'UNIFIED_REFUND_UNCORRELATED')`,
		},
		{
			name:   "refund requested",
			status: OrderStatusRefundRequested,
		},
		{
			name:   "refunding",
			status: OrderStatusRefunding,
		},
		{
			name:   "refund pending",
			status: OrderStatusRefundPending,
		},
		{
			name:   "partially refunded",
			status: OrderStatusPartiallyRefunded,
		},
		{
			name:   "refunded",
			status: OrderStatusRefunded,
		},
		{
			name:   "refund failed",
			status: OrderStatusRefundFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newPaymentConfigServiceTestClient(t)
			order := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, tc.status, time.Now().UTC().Add(-2*time.Minute))
			svc := &PaymentService{entClient: client}
			createPaymentFulfillmentRecoveryFenceTables(t, ctx, svc)
			if tc.fenceSQL != "" {
				_, err := client.ExecContext(ctx, tc.fenceSQL, order.ID)
				require.NoError(t, err)
			}

			recovered, err := svc.RecoverPendingPaymentOrderFulfillments(ctx)
			require.NoError(t, err)
			require.Zero(t, recovered)

			reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, tc.status, reloaded.Status)
		})
	}
}

func TestRecoverPendingPaymentOrderFulfillmentsFailsClosedWhenRefundFenceUnavailable(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))
	credited := 0.0
	svc := newPaymentFulfillmentRecoveryBalanceService(t, client, order, &credited)
	createPaymentFulfillmentRecoveryFenceTables(t, ctx, svc)
	_, err := client.ExecContext(ctx, `DROP TABLE unified_payment_refund_events`)
	require.NoError(t, err)

	recovered, err := svc.RecoverPendingPaymentOrderFulfillments(ctx)
	require.Error(t, err)
	require.Zero(t, recovered)
	require.Zero(t, credited)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPaid, reloaded.Status)
}

func TestRecoverPendingPaymentOrderFulfillmentsHonorsCanceledContextBeforeScanning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	recovered, err := (&PaymentService{}).RecoverPendingPaymentOrderFulfillments(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, recovered)
}

func TestRecoverPendingPaymentOrderFulfillmentsCanceledAfterSelectionDoesNotGrantEntitlement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))
	credited := 0.0
	svc := newPaymentFulfillmentRecoveryBalanceService(t, client, order, &credited)
	createPaymentFulfillmentRecoveryFenceTables(t, ctx, svc)

	// The first Ent query lists candidates. Block the executor's subsequent
	// order reload, then cancel the recovery context before it can acquire a
	// lease or invoke balance redemption.
	executorReloadReached := make(chan struct{})
	releaseExecutorReload := make(chan struct{})
	defer func() {
		select {
		case <-releaseExecutorReload:
		default:
			close(releaseExecutorReload)
		}
	}()
	var paymentOrderQueries atomic.Int32
	client.PaymentOrder.Intercept(dbent.InterceptFunc(func(next dbent.Querier) dbent.Querier {
		return dbent.QuerierFunc(func(queryCtx context.Context, query dbent.Query) (dbent.Value, error) {
			if paymentOrderQueries.Add(1) == 2 {
				close(executorReloadReached)
				<-releaseExecutorReload
			}
			return next.Query(queryCtx, query)
		})
	}))

	type recoveryResult struct {
		recovered int
		err       error
	}
	resultCh := make(chan recoveryResult, 1)
	go func() {
		recovered, err := svc.RecoverPendingPaymentOrderFulfillments(ctx)
		resultCh <- recoveryResult{recovered: recovered, err: err}
	}()
	select {
	case <-executorReloadReached:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not reach the executor reload")
	}
	cancel()
	close(releaseExecutorReload)

	result := <-resultCh
	require.ErrorIs(t, result.err, context.Canceled)
	require.Zero(t, result.recovered)
	require.Zero(t, credited)
	reloaded, err := client.PaymentOrder.Get(context.Background(), order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPaid, reloaded.Status)
}

func TestPaymentOrderFulfillmentLookupErrorsRemainRetryable(t *testing.T) {
	ctx := context.Background()
	dbUnavailable := errors.New("payment order database unavailable")

	for _, tc := range []struct {
		name string
		call func(*PaymentService) error
	}{
		{
			name: "confirm payment",
			call: func(svc *PaymentService) error {
				return svc.confirmPayment(ctx, 123, "trade", 1, payment.TypeAlipay, nil)
			},
		},
		{
			name: "already processed",
			call: func(svc *PaymentService) error {
				return svc.alreadyProcessed(ctx, &dbent.PaymentOrder{ID: 123})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newPaymentConfigServiceTestClient(t)
			client.PaymentOrder.Intercept(dbent.InterceptFunc(func(dbent.Querier) dbent.Querier {
				return dbent.QuerierFunc(func(context.Context, dbent.Query) (dbent.Value, error) {
					return nil, dbUnavailable
				})
			}))

			err := tc.call(&PaymentService{entClient: client})
			require.ErrorIs(t, err, dbUnavailable)
			require.NotErrorIs(t, err, ErrOrderNotFound)
		})
	}
}

func TestPaymentOrderFulfillmentMissingLookupRemainsTerminal(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentService{entClient: client}

	err := svc.confirmPayment(ctx, 123, "trade", 1, payment.TypeAlipay, nil)
	require.ErrorIs(t, err, ErrOrderNotFound)
	err = svc.alreadyProcessed(ctx, &dbent.PaymentOrder{ID: 123})
	require.ErrorIs(t, err, ErrOrderNotFound)
}

func TestAcquirePaymentFulfillmentLeaseRejectsTimestampReplacedBeforeReload(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))
	svc := &PaymentService{entClient: client}

	// Simulate a worker that pauses after its UPDATE. A second worker reclaims
	// the stale lease and advances updated_at before the first worker reloads.
	// The first worker must not adopt the second owner's version.
	newerOwnerVersion := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	var injected atomic.Bool
	client.PaymentOrder.Intercept(dbent.InterceptFunc(func(next dbent.Querier) dbent.Querier {
		return dbent.QuerierFunc(func(queryCtx context.Context, query dbent.Query) (dbent.Value, error) {
			if injected.CompareAndSwap(false, true) {
				if _, err := client.ExecContext(queryCtx, `UPDATE payment_orders SET updated_at = ? WHERE id = ?`, newerOwnerVersion, order.ID); err != nil {
					return nil, err
				}
			}
			return next.Query(queryCtx, query)
		})
	}))

	lease, err := svc.acquirePaymentFulfillmentLease(ctx, order)
	require.Error(t, err)
	require.Nil(t, lease)
	require.Equal(t, "CONFLICT", infraerrors.Reason(err))
	require.True(t, injected.Load())

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, reloaded.UpdatedAt.Equal(newerOwnerVersion))
}

func TestPaymentOrderExpiryServiceRunOnceRecoversPaidOrderWithoutCallback(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))
	credited := 0.0
	svc := newPaymentFulfillmentRecoveryBalanceService(t, client, order, &credited)
	createPaymentFulfillmentRecoveryFenceTables(t, ctx, svc)

	NewPaymentOrderExpiryService(svc, time.Hour).runOnce()
	require.Equal(t, order.Amount, credited)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
}
