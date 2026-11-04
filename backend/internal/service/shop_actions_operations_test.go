//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
	"github.com/stretchr/testify/require"
)

func TestShopActionPausedConfigurationRetainsCleanupCapability(t *testing.T) {
	i := shopaction.Integration{ID: "newapi-domestic", Version: 1, Adapter: shopaction.Native, Endpoint: "http://100.64.0.1:17867"}
	s := NewShopService(nil, nil, &config.Config{ShopDaypass: config.ShopDaypassConfig{ProductID: 9}, ShopActions: shopaction.Config{Integrations: []shopaction.Integration{i}}}, nil, nil)
	b := shopaction.Binding{BindingKey: "primary-delivery", Adapter: i.Adapter, IntegrationRef: i.ID, IntegrationVersion: 1, Integration: &i, Params: json.RawMessage(`{"duration_seconds":86400,"default_concurrency":2}`)}
	_, err := s.actionAdapter(b)
	require.Error(t, err)
	a, err := s.actionAdapterFor(b, true)
	require.NoError(t, err)
	require.IsType(t, &shopDaypassAdapter{}, a)
	require.NotNil(t, a.(*shopDaypassAdapter).gateway)
}

func TestShopActionRefundReviewWarnsUntilExactRevokeCompletes(t *testing.T) {
	s, o, _ := newDaypassUnit(t)
	ctx := context.Background()
	review, err := shopManualRefundReview(ctx, s.client, o, time.Now())
	require.NoError(t, err)
	require.False(t, review.CanRefund)
	require.True(t, review.RequiresManualReview)
	require.Equal(t, "SHOP_REFUND_REVOKE_REQUIRED", review.ReasonCode)
	require.Contains(t, review.Reason, "授权尚未确认撤销")
	task := s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).OnlyX(ctx)
	s.client.ShopOrderAction.UpdateOne(task).SetStatus(shopaction.Revoked).ExecX(ctx)
	review, err = shopManualRefundReview(ctx, s.client, o, time.Now())
	require.NoError(t, err)
	require.False(t, review.CanRefund)
	require.True(t, review.RequiresManualReview)
	require.Equal(t, "SHOP_REFUND_MANUAL_ONLY", review.ReasonCode)
}

func TestShopActionMissingBindingCannotFallBackToManualOrStarveRepair(t *testing.T) {
	s, o, _ := newDaypassUnit(t)
	ctx := context.Background()
	f := s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).OnlyX(ctx)
	f.ProductSnapshot["payment_actions"] = []any{}
	raw, err := json.Marshal(f.ProductSnapshot)
	require.NoError(t, err)
	_, err = s.client.ExecContext(ctx, "UPDATE shop_fulfillments SET product_snapshot=? WHERE id=?", string(raw), f.ID)
	require.NoError(t, err)
	s.client.ShopOrderAction.Delete().Where(shoporderaction.OrderIDEQ(o.ID)).ExecX(ctx)
	require.NoError(t, s.repairShopActionOutbox(ctx))
	task := s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).OnlyX(ctx)
	require.Equal(t, shopaction.Review, task.Status)
	_, err = s.Action(ctx, o.ID, o.UserID, ShopActionInput{Action: "complete", Revision: f.Revision, DeliveryContent: "must not duplicate delivery"})
	require.Equal(t, "SHOP_AUTOMATIC_DELIVERY", infraerrors.Reason(err))
	detail, err := s.Order(ctx, o.ID, o.UserID, true)
	require.NoError(t, err)
	require.False(t, detail.Automation.CanRetry)
	require.True(t, detail.Product.AutomaticFulfillment)
	require.Error(t, s.requireActionsCheckout(ctx, detail.Product))
}
