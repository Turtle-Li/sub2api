//go:build unit || integration

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
	"strings"
	"time"
)

// Keep the original dedicated-worker scenarios running against the new task
// executor while their fixture names remain useful as regression provenance.
type shopDaypassClaim = shopOrderActionClaim

const shopDaypassLease = shopActionLease

var errShopDaypassLeaseLost = errShopActionLeaseLost

func (s *ShopService) claimDaypass(ctx context.Context, id int64, now time.Time) (*shopDaypassClaim, error) {
	return s.claimShopAction(ctx, id, now)
}
func (s *ShopService) deliverDaypass(ctx context.Context, c *shopDaypassClaim) error {
	return s.executeShopAction(ctx, c)
}
func (s *ShopService) RecoverDaypassDeliveries(ctx context.Context) error {
	return s.RecoverShopActions(ctx)
}
func (s *ShopService) requireDaypassCheckout(ctx context.Context, p ShopProduct) error {
	return s.requireActionsCheckout(ctx, p)
}
func daypassTestActions(s *ShopService) []shopaction.Binding {
	i := shopaction.Integration{ID: "newapi-domestic", Version: 1, Adapter: shopaction.Native, Endpoint: "http://100.64.0.1:17867", Enabled: true}
	s.actionsCfg = shopaction.Config{Enabled: true, Integrations: []shopaction.Integration{i}}
	return []shopaction.Binding{{BindingKey: "primary-delivery", Adapter: shopaction.Native, IntegrationRef: i.ID, IntegrationVersion: 1}}
}

type daypassTestGateway struct {
	grant                   *shopDaypassGrant
	lost                    bool
	provisions, revocations int
	afterProvision          func()
}

func (*daypassTestGateway) Ready(context.Context, int) error { return nil }
func (g *daypassTestGateway) Provision(_ context.Context, b shopDaypassBinding) (*shopDaypassGrant, error) {
	g.provisions++
	if g.grant == nil {
		duration := int64(86400)
		if b.PriceFen == 3000 || b.PriceFen == 4000 {
			duration *= 7
		}
		g.grant = &shopDaypassGrant{Instance: b.Instance, OrderID: b.OrderID, ProductID: b.ProductID, PaidAt: b.PaidAt, ExpiresAt: b.PaidAt + duration, TokenID: 71, Status: "active", Key: "sk-" + strings.Repeat("d", 48)}
	}
	if g.afterProvision != nil {
		g.afterProvision()
	}
	if g.lost {
		g.lost = false
		return nil, errShopDaypassUnavailable
	}
	return g.grant, nil
}
func (g *daypassTestGateway) Revoke(context.Context, shopDaypassBinding) error {
	g.revocations++
	return nil
}
