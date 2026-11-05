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
	base, err = s.resolveShopConcurrency(base, 3)
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
		_, err = s.resolveShopConcurrency(d.Product, concurrency)
		require.Error(t, err)
	}
	s.daypassCfg.MaxConcurrency = 2
	_, err = s.resolveShopConcurrency(d.Product, 3)
	require.Error(t, err)
	// A sales switch never invalidates a paid snapshot or its native action.
	require.True(t, validShopDaypassProduct(base))
	require.True(t, validateActionSnapshot(base.PaymentActions[0]))
	base.Price = 6
	require.False(t, validShopDaypassProduct(base))
	_, err = s.resolveShopConcurrency(ShopProduct{Price: 6}, 2)
	require.Error(t, err)
}

func TestShopConcurrencyNativeActionRejectsMismatchedPrice(t *testing.T) {
	s, o, g := newDaypassUnit(t)
	ctx := context.Background()
	d, err := s.Order(ctx, o.ID, o.UserID, true)
	require.NoError(t, err)
	p, err := s.resolveShopConcurrency(d.Product, 3)
	require.NoError(t, err)
	adapter := &shopDaypassAdapter{gateway: g, productID: p.ID}
	request := shopaction.Request{Source: shopDaypassInstance, Order: shopaction.Order{ID: o.ID, ProductID: p.ID, AmountFen: 800, Currency: "CNY", PaidAt: *o.PaidAt}, Params: p.PaymentActions[0].Params}
	result, err := adapter.Execute(ctx, request)
	require.NoError(t, err)
	require.Contains(t, result.Delivery.Content, "3并发")
	request.Order.AmountFen = 600
	_, err = adapter.Execute(ctx, request)
	require.Error(t, err)
	request.Order.AmountFen = 800
	request.Params = json.RawMessage(`{"duration_seconds":86400,"default_concurrency":4}`)
	_, err = adapter.Execute(ctx, request)
	require.Error(t, err)
}
