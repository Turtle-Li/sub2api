//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func localCancelContractOrder(t *testing.T, client *dbent.Client) *dbent.PaymentOrder {
	t.Helper()
	ctx := context.Background()
	owner, err := client.User.Create().SetEmail("local-cancel-contract@example.test").SetUsername("local-cancel-contract").SetPasswordHash("test-only").Save(ctx)
	require.NoError(t, err)
	order, err := client.PaymentOrder.Create().SetUserID(owner.ID).SetUserEmail(owner.Email).SetUserName(owner.Username).
		SetAmount(100).SetPayAmount(100).SetFeeRate(0).SetRechargeCode("LOCAL-CANCEL-CONTRACT").SetOutTradeNo("sub2_local_cancel_contract").
		SetPaymentTradeNo("").SetPaymentType(payment.TypeAlipay).SetProviderKey(payment.TypeAlipay).SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).SetPayURL("https://openapi.alipay.com/gateway.do").SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").SetSrcHost("localhost").Save(ctx)
	require.NoError(t, err)
	return order
}

func TestLocalCancelContractCommitReleasesAdmissionWithoutProviderIO(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	order := localCancelContractOrder(t, client)
	provider := &paymentOrderLifecycleQueryProvider{
		queryErrors:  []error{errors.New("provider unavailable")},
		cancelErrors: []error{errors.New("provider unavailable")},
	}
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := &PaymentService{entClient: client, registry: registry, providersLoaded: true}

	before, err := client.Tx(ctx)
	require.NoError(t, err)
	require.Equal(t, "TOO_MANY_PENDING", infraerrors.Reason(svc.checkSinglePendingOrder(ctx, before, order.UserID)))
	require.NoError(t, before.Rollback())

	for range 2 {
		result, err := svc.CancelOrder(ctx, order.ID, order.UserID)
		require.NoError(t, err)
		require.Equal(t, "cancelled", result)
		stored, err := client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusCancelled, stored.Status)
		require.Nil(t, stored.PaidAt)
	}
	require.Zero(t, provider.queryCalls, "local cancellation must not wait for a provider query")
	require.Zero(t, provider.cancelCalls, "provider close belongs to the durable worker")
	rows, err := client.QueryContext(ctx, "SELECT COUNT(*) FROM payment_local_cancellation_work WHERE order_id = $1 AND work_kind = 'CLOSE'", order.ID)
	require.NoError(t, err, "a successful cancellation must persist its follow-up job")
	require.True(t, rows.Next())
	var jobs int
	require.NoError(t, rows.Scan(&jobs))
	require.NoError(t, rows.Close())
	require.Equal(t, 1, jobs, "repeated cancellation must reuse its durable close job")

	after, err := client.Tx(ctx)
	require.NoError(t, err)
	defer after.Rollback()
	require.NoError(t, svc.checkSinglePendingOrder(ctx, after, order.UserID), "new order admission must be free at successful cancel response")
}

func TestLocalCancelContractOwnershipCheckedBeforeMutation(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	order := localCancelContractOrder(t, client)
	svc := &PaymentService{entClient: client}
	_, err := svc.CancelOrder(ctx, order.ID, order.UserID+1)
	require.Equal(t, "FORBIDDEN", infraerrors.Reason(err))
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, stored.Status)
}

func TestLocalCancelContractPaidWinnerCannotBeOverwritten(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	order := localCancelContractOrder(t, client)
	paidAt := time.Now().UTC().Truncate(time.Second)
	_, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusPaid).SetPaidAt(paidAt).SetPaymentTradeNo("trusted-payment").Save(ctx)
	require.NoError(t, err)
	svc := &PaymentService{entClient: client}
	result, err := svc.CancelOrder(ctx, order.ID, order.UserID)
	require.NoError(t, err)
	require.Equal(t, "already_paid", result)
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPaid, stored.Status)
	require.Equal(t, "trusted-payment", stored.PaymentTradeNo)
	require.True(t, stored.PaidAt.Equal(paidAt))
}

func TestLocalCancelContractReplayStillCancelledAfterLatePayment(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	order := localCancelContractOrder(t, client)
	svc := &PaymentService{entClient: client}
	result, err := svc.CancelOrder(ctx, order.ID, order.UserID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", result)
	_, err = client.PaymentOrder.UpdateOneID(order.ID).SetPaidAt(time.Now()).SetPaymentTradeNo("late-payment-awaiting-refund").Save(ctx)
	require.NoError(t, err)
	result, err = svc.CancelOrder(ctx, order.ID, order.UserID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", result, "late cash evidence must not reverse the business cancellation result")
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCancelled, stored.Status)
	require.Equal(t, "late-payment-awaiting-refund", stored.PaymentTradeNo)
}
