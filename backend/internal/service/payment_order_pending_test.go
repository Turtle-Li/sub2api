//go:build unit

package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestCheckSinglePendingOrderReturnsExistingOrderMetadata(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	actor, err := client.User.Create().SetEmail("pending-order@example.test").SetPasswordHash("test").SetUsername("pending-order").Save(ctx)
	require.NoError(t, err)
	existing, err := client.PaymentOrder.Create().
		SetUserID(actor.ID).
		SetUserEmail(actor.Email).
		SetUserName(actor.Username).
		SetAmount(20).
		SetPayAmount(20).
		SetFeeRate(0).
		SetRechargeCode("PENDING-ORDER-TEST").
		SetOutTradeNo("sub2_pending_order_test").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("localhost").
		Save(ctx)
	require.NoError(t, err)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	svc := &PaymentService{entClient: client}
	err = svc.checkSinglePendingOrder(ctx, tx, actor.ID)
	require.Equal(t, "TOO_MANY_PENDING", infraerrors.Reason(err))
	status := infraerrors.FromError(err)
	require.Equal(t, "1", status.Metadata["max"])
	require.Equal(t, strconv.FormatInt(existing.ID, 10), status.Metadata["order_id"])
}
