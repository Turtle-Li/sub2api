//go:build integration

package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const paymentCancelledOrderFenceConstraint = "payment_orders_cancelled_status_immutable"

func requirePaymentCancelledOrderFence(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)

	var pgErr *pq.Error
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, pq.ErrorCode("23514"), pgErr.Code)
	require.Equal(t, paymentCancelledOrderFenceConstraint, pgErr.Constraint)
}

func TestLocalCancellationPostgresCancelledOrderFence(t *testing.T) {
	client, db, ctx := newLocalCancellationPostgresFixture(t)
	user := createLocalCancellationPostgresUser(t, ctx, client, "cancelled-order-fence")

	t.Run("legacy cancelled transitions are rejected before fulfillment", func(t *testing.T) {
		for _, nextStatus := range []string{
			OrderStatusPaid,
			OrderStatusFailed,
			OrderStatusRecharging,
			OrderStatusCompleted,
		} {
			t.Run(nextStatus, func(t *testing.T) {
				order := createLocalCancellationPostgresOrder(t, ctx, client, user, localCancellationPostgresOrderInput{
					PayAmount: 13.45,
					Status:    OrderStatusCancelled,
				})

				_, err := db.ExecContext(ctx, `UPDATE public.payment_orders SET status = $2 WHERE id = $1`, order.ID, nextStatus)
				requirePaymentCancelledOrderFence(t, err)

				stored, err := client.PaymentOrder.Get(ctx, order.ID)
				require.NoError(t, err)
				require.Equal(t, OrderStatusCancelled, stored.Status)
				require.Nil(t, stored.PaidAt)

				storedUser, err := client.User.Get(ctx, user.ID)
				require.NoError(t, err)
				require.Equal(t, user.Balance, storedUser.Balance)
				require.Equal(t, user.TotalRecharged, storedUser.TotalRecharged)
			})
		}
	})

	t.Run("same cancelled state permits trusted payment evidence", func(t *testing.T) {
		order := createLocalCancellationPostgresOrder(t, ctx, client, user, localCancellationPostgresOrderInput{
			PayAmount: 14.56,
			Status:    OrderStatusCancelled,
		})
		paidAt := time.Now().UTC().Truncate(time.Microsecond)

		_, err := db.ExecContext(ctx, `
			UPDATE public.payment_orders
			SET status = $2, paid_at = $3, payment_trade_no = $4
			WHERE id = $1`, order.ID, OrderStatusCancelled, paidAt, "same-cancelled-evidence")
		require.NoError(t, err)

		stored, err := client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusCancelled, stored.Status)
		require.NotNil(t, stored.PaidAt)
		require.Equal(t, "same-cancelled-evidence", stored.PaymentTradeNo)
	})

	t.Run("late refund records evidence without changing cancellation or balance", func(t *testing.T) {
		order := createLocalCancellationPostgresOrder(t, ctx, client, user, localCancellationPostgresOrderInput{
			PayAmount: 15.67,
			Status:    OrderStatusCancelled,
		})
		svc := &PaymentService{entClient: client}

		require.NoError(t, svc.toPaid(ctx, order, "late-cancelled-fence-payment", order.PayAmount, payment.TypeAlipay))

		stored, err := client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusCancelled, stored.Status)
		require.NotNil(t, stored.PaidAt)
		require.Equal(t, "late-cancelled-fence-payment", stored.PaymentTradeNo)

		var workStatus string
		var amountFen int64
		require.NoError(t, db.QueryRowContext(ctx, `
			SELECT status, amount_fen
			FROM payment_local_cancellation_work
			WHERE order_id = $1 AND work_kind = $2`, order.ID, localCancellationWorkDirectRefund).Scan(&workStatus, &amountFen))
		require.Equal(t, localCancellationWorkPending, workStatus)
		require.EqualValues(t, 1567, amountFen)

		storedUser, err := client.User.Get(ctx, user.ID)
		require.NoError(t, err)
		require.Equal(t, user.Balance, storedUser.Balance)
		require.Equal(t, user.TotalRecharged, storedUser.TotalRecharged)
	})

	t.Run("normal pending payment lifecycle remains allowed", func(t *testing.T) {
		order := createLocalCancellationPostgresOrder(t, ctx, client, user, localCancellationPostgresOrderInput{
			PayAmount: 16.78,
			Status:    OrderStatusPending,
		})
		paidAt := time.Now().UTC().Truncate(time.Microsecond)
		completedAt := paidAt.Add(time.Second)

		_, err := db.ExecContext(ctx, `
			UPDATE public.payment_orders
			SET status = $2, paid_at = $3
			WHERE id = $1`, order.ID, OrderStatusPaid, paidAt)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `
			UPDATE public.payment_orders
			SET status = $2, completed_at = $3
			WHERE id = $1`, order.ID, OrderStatusCompleted, completedAt)
		require.NoError(t, err)

		stored, err := client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusCompleted, stored.Status)
		require.NotNil(t, stored.PaidAt)
		require.NotNil(t, stored.CompletedAt)
	})
}
