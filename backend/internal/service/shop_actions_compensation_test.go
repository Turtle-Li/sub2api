//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
	"github.com/stretchr/testify/require"
)

func TestShopActionPersistedCompensationCannotFlipBackToProvision(t *testing.T) {
	s, o, g := newDaypassUnit(t)
	ctx := context.Background()
	claim, err := s.claimShopAction(ctx, o.ID, time.Now())
	require.NoError(t, err)
	adapter, err := s.actionAdapter(claim.snapshot.Binding)
	require.NoError(t, err)
	result, err := adapter.Execute(ctx, claim.snapshot.Request)
	require.NoError(t, err)
	s.client.PaymentOrder.UpdateOne(o).SetStatus(OrderStatusRefunding).ExecX(ctx)
	require.ErrorIs(t, s.completeShopAction(ctx, claim, result), errShopActionCompensate)
	row := s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).OnlyX(ctx)
	require.Equal(t, shopaction.Compensating, row.Status)
	// Simulate a crash after intent commit and a later financial review release.
	s.client.PaymentOrder.UpdateOne(o).SetStatus(OrderStatusPaid).ExecX(ctx)
	expireDaypassLease(t, s, o.ID)
	recovered, err := s.claimShopAction(ctx, o.ID, time.Now())
	require.NoError(t, err)
	require.Equal(t, shopaction.Compensating, recovered.row.Status)
	require.NoError(t, s.executeShopAction(ctx, recovered))
	require.Equal(t, 1, g.provisions)
	require.Equal(t, 1, g.revocations)
	require.Equal(t, shopaction.Revoked, s.client.ShopOrderAction.GetX(ctx, row.ID).Status)
	require.Equal(t, OrderStatusPaid, s.client.PaymentOrder.GetX(ctx, o.ID).Status)
}

func TestShopActionAdminRevokesOnlyOriginalGrantWithoutRefunding(t *testing.T) {
	s, o, g := newDaypassUnit(t)
	ctx := context.Background()
	claim, err := s.claimShopAction(ctx, o.ID, time.Now())
	require.NoError(t, err)
	require.NoError(t, s.executeShopAction(ctx, claim))
	admin, err := s.Order(ctx, o.ID, o.UserID, true)
	require.NoError(t, err)
	require.True(t, admin.Automation.CanRevoke)
	_, err = s.ManageAutomation(ctx, o.ID, o.UserID, ShopAutomationInput{Action: "revoke", ClaimRevision: 99})
	require.Error(t, err)
	queued, err := s.ManageAutomation(ctx, o.ID, o.UserID, ShopAutomationInput{Action: "revoke", ClaimRevision: admin.Automation.ClaimRevision})
	require.NoError(t, err)
	require.Equal(t, shopaction.Compensating, queued.Automation.Status)
	buyer, err := s.Order(ctx, o.ID, o.UserID, false)
	require.NoError(t, err)
	require.Empty(t, buyer.DeliveryContent)
	require.NoError(t, s.RecoverNotifications(ctx))
	require.Empty(t, s.email.(*shopTestMail).bodies, "a queued completion email must not send a revoked credential")
	require.NoError(t, s.RecoverShopActions(ctx))
	row := s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).OnlyX(ctx)
	require.Equal(t, shopaction.Revoked, row.Status)
	require.Equal(t, claim.row.ActionKey, row.ActionKey)
	require.Equal(t, 1, g.provisions)
	require.Equal(t, 1, g.revocations)
	require.Equal(t, OrderStatusCompleted, s.client.PaymentOrder.GetX(ctx, o.ID).Status)
}

func TestShopActionManualRetryKeepsOriginalKeyAndExpiry(t *testing.T) {
	s, o, g := newDaypassUnit(t)
	ctx := context.Background()
	g.lost = true
	claim, err := s.claimShopAction(ctx, o.ID, time.Now())
	require.NoError(t, err)
	require.Error(t, s.executeShopAction(ctx, claim))
	key, expiry := g.grant.Key, g.grant.ExpiresAt
	row := s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).OnlyX(ctx)
	_, err = s.ManageAutomation(ctx, o.ID, o.UserID, ShopAutomationInput{Action: "retry", ClaimRevision: row.ClaimRevision})
	require.NoError(t, err)
	require.NoError(t, s.RecoverShopActions(ctx))
	buyer, err := s.Order(ctx, o.ID, o.UserID, false)
	require.NoError(t, err)
	require.Contains(t, buyer.DeliveryContent, key)
	require.Equal(t, expiry, g.grant.ExpiresAt)
	require.Equal(t, row.ActionKey, s.client.ShopOrderAction.GetX(ctx, row.ID).ActionKey)
}
