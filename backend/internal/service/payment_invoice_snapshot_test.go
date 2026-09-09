package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestInvoiceProductSnapshotWorksWithoutOptionalGroupRepository(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().SetEmail("snapshot-invoice@example.test").SetPasswordHash("hash").SetUsername("snapshot-buyer").Save(ctx)
	require.NoError(t, err)
	group, err := client.Group.Create().SetName("Original group").SetPlatform("openai").Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetGroupID(group.ID).SetName("Purchased plan").SetPrice(50).SetCurrency("USD").SetDescription("Original description").SetFeatures(`["Original feature"]`).SetValidityDays(30).SetValidityUnit("day").Save(ctx)
	require.NoError(t, err)
	svc := &PaymentService{entClient: client}
	order, err := svc.createOrderInTx(ctx, CreateOrderRequest{UserID: user.ID, PaymentType: payment.TypeAlipay, OrderType: payment.OrderTypeSubscription}, &User{ID: user.ID, Email: user.Email, Username: user.Username}, plan, &PaymentConfig{MaxPendingOrders: 3, OrderTimeoutMin: 30}, 50, 50, 0, 50, nil)
	require.NoError(t, err)
	_, err = client.SubscriptionPlan.UpdateOneID(plan.ID).SetName("Renamed plan").SetPrice(99).SetDescription("New description").Save(ctx)
	require.NoError(t, err)
	persisted, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	snapshot := SanitizedPaymentOrderProductSnapshot(persisted)
	require.Equal(t, "Purchased plan", snapshot["name"])
	require.Equal(t, float64(50), snapshot["price"])
	require.Equal(t, "Original description", snapshot["description"])
	require.Equal(t, []string{"Original feature"}, snapshot["features"])
	require.NotContains(t, snapshot, "group_name", "missing optional repository must not invent historical group evidence")
}
