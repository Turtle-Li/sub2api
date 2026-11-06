//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/shopaction"
	"github.com/stretchr/testify/require"
)

func TestShopConcurrencyTierSnapshotAndLockedRevalidation(t *testing.T) {
	s, o, _ := newDaypassUnit(t)
	ctx := context.Background()
	d, err := s.Order(ctx, o.ID, o.UserID, true)
	require.NoError(t, err)
	base := d.Product
	base.Enabled = true
	saved, err := s.SaveProduct(ctx, base.ID, base)
	require.NoError(t, err)
	base = *saved
	base, err = s.resolveShopSKU(base, ShopSKUSelection{Concurrency: 3})
	require.NoError(t, err)
	require.Equal(t, 8.0, base.Price)
	require.Equal(t, 3, shopProductConcurrency(base))
	require.NoError(t, revalidateShopProductTx(ctx, s.client, &base))
	require.Equal(t, 8.0, base.Price)
	require.Equal(t, 3, shopProductConcurrency(base))
	public := publicShopProduct(base)
	require.Equal(t, 3, public.Concurrency)
	require.Len(t, public.ConcurrencyOptions, 2)
	require.Empty(t, public.PaymentActions)
	for _, concurrency := range []int{-1, 1, 4, 32} {
		_, err = s.resolveShopSKU(d.Product, ShopSKUSelection{Concurrency: concurrency})
		require.Error(t, err)
	}
	s.daypassCfg.MaxConcurrency = 2
	_, err = s.resolveShopSKU(d.Product, ShopSKUSelection{Concurrency: 3})
	require.Error(t, err)
	// A sales switch never invalidates a paid snapshot or its native action.
	require.True(t, validShopDaypassProduct(base))
	require.True(t, validateActionSnapshot(base.PaymentActions[0]))
	base.Price = 6
	require.False(t, validShopDaypassProduct(base))
	_, err = s.resolveShopSKU(ShopProduct{Price: 6}, ShopSKUSelection{Concurrency: 2})
	require.Error(t, err)
	_, err = s.resolveShopSKU(ShopProduct{Price: 6}, ShopSKUSelection{DurationDays: 7})
	require.Error(t, err)
	_, err = s.resolveShopSKU(d.Product, ShopSKUSelection{DurationDays: 5})
	require.Error(t, err)
}

func TestShopConcurrencyNativeActionRejectsMismatchedPrice(t *testing.T) {
	s, o, g := newDaypassUnit(t)
	ctx := context.Background()
	d, err := s.Order(ctx, o.ID, o.UserID, true)
	require.NoError(t, err)
	s.daypassCfg.WeeklyEnabled = true
	p, err := s.resolveShopSKU(d.Product, ShopSKUSelection{Concurrency: 3, DurationDays: 7})
	require.NoError(t, err)
	adapter := &shopDaypassAdapter{gateway: g, productID: p.ID}
	request := shopaction.Request{Source: shopDaypassInstance, Order: shopaction.Order{ID: o.ID, ProductID: p.ID, AmountFen: 4000, Currency: "CNY", PaidAt: *o.PaidAt}, Params: p.PaymentActions[0].Params}
	result, err := adapter.Execute(ctx, request)
	require.NoError(t, err)
	require.Contains(t, result.Delivery.Content, "3并发")
	require.Contains(t, result.Delivery.Content, "有效期：7 天")
	request.Order.AmountFen = 800
	_, err = adapter.Execute(ctx, request)
	require.Error(t, err)
	request.Order.AmountFen = 4000
	request.Params = json.RawMessage(`{"duration_seconds":604800,"default_concurrency":2}`)
	_, err = adapter.Execute(ctx, request)
	require.Error(t, err)
	require.Equal(t, 1, g.provisions)
}
