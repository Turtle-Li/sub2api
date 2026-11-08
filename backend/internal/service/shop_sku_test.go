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
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
	"github.com/stretchr/testify/require"
)

func TestShopConfiguredSKUPriceSavesAndReloads(t *testing.T) {
	// Given a native product with an owner-authored price, independent of its guide.
	s, _, _ := newDaypassUnit(t)
	var p ShopProduct
	require.NoError(t, json.Unmarshal([]byte(`{"name":"DeepSeek 订阅卡","price":9.9,"duration_value":1,"duration_unit":"day","fulfillment_mode":"account_delivery","default_sku_id":"day-2","skus":[{"id":"day-2","attributes":{"周期":"日卡","并发":"2 并发"},"price":9.9,"duration_value":1,"duration_unit":"day","concurrency":2,"enabled":true},{"id":"week-3","attributes":{"周期":"周卡","并发":"3 并发"},"price":49.9,"duration_value":7,"duration_unit":"day","concurrency":3,"enabled":true}]}`), &p))
	p.PaymentActions = daypassTestActions(s)
	// When the catalog is saved and loaded through the real persistence boundary.
	saved, err := s.SaveProduct(context.Background(), 0, p)
	require.NoError(t, err)
	loaded, err := s.Product(context.Background(), saved.ID, true)
	// Then the authored price survives instead of being rejected or reset to CNY6.
	require.NoError(t, err)
	require.Equal(t, 9.9, loaded.Price)
	encoded, err := json.Marshal(loaded)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"price":49.9`)
}

func configuredShopProduct() ShopProduct {
	return ShopProduct{Name: "可配置商品", Enabled: true, FulfillmentMode: ShopAccountDelivery, DefaultSKUID: "day", SKUs: []ShopSKU{
		{ID: "day", Attributes: map[string]string{"周期": "日卡"}, Price: 9.9, DurationValue: 1, DurationUnit: "day", Enabled: true},
		{ID: "week", Attributes: map[string]string{"周期": "周卡"}, Price: 6, DurationValue: 7, DurationUnit: "day", Enabled: true},
	}}
}

func TestShopConfiguredSKUValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ShopProduct)
	}{
		{"duplicate-id", func(p *ShopProduct) { p.SKUs[1].ID = "day" }},
		{"duplicate-combination", func(p *ShopProduct) { p.SKUs[1].Attributes["周期"] = "日卡" }},
		{"duplicate-empty-combination", func(p *ShopProduct) { p.SKUs[0].Attributes = nil; p.SKUs[1].Attributes = map[string]string{} }},
		{"different-attributes", func(p *ShopProduct) { p.SKUs[1].Attributes = map[string]string{"版本": "Pro"} }},
		{"missing-default", func(p *ShopProduct) { p.DefaultSKUID = "unknown" }},
		{"disabled-default", func(p *ShopProduct) { p.SKUs[0].Enabled = false }},
		{"fractional-fen", func(p *ShopProduct) { p.SKUs[1].Price = 1.001 }},
		{"zero-on-sale", func(p *ShopProduct) { p.SKUs[1].Price = 0 }},
		{"invalid-duration", func(p *ShopProduct) { p.SKUs[1].DurationValue = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := configuredShopProduct()
			tc.change(&p)
			require.Error(t, normalizeShopSKUs(&p))
		})
	}
	p := configuredShopProduct()
	require.NoError(t, normalizeShopSKUs(&p))
	for _, selected := range []ShopSKUSelection{{}, {ID: "missing"}, {ID: "week", DurationDays: 1}} {
		_, err := selectShopSKU(p, selected)
		require.Error(t, err)
	}
	p.SKUs[1].Enabled = false
	_, err := selectShopSKU(p, ShopSKUSelection{ID: "week"})
	require.Equal(t, "SHOP_SKU_UNAVAILABLE", infraerrors.Reason(err))
}

func TestShopConfiguredSKUCheckoutFreezesPriceAndDelivery(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "native"}[native], func(t *testing.T) {
			ctx := context.Background()
			f := newOwnerTestOrderFixture(t)
			createInvoiceUnitRefundFenceTables(t, ctx, f.client)
			s := NewShopService(f.client, shopTestEncryptor{}, &config.Config{Totp: config.TotpConfig{EncryptionKeyConfigured: true}}, nil, nil)
			f.service.shopService = s
			f.settings.values[SettingPaymentEnabled] = "true"
			f.settings.values[SettingPaymentVisibleMethodAlipaySource] = VisibleMethodSourceUnifiedAlipay
			p := configuredShopProduct()
			gateway := &daypassTestGateway{}
			if native {
				p.PaymentActions = daypassTestActions(s)
				for i := range p.SKUs {
					p.SKUs[i].Concurrency = 3
				}
				s.daypass = gateway
			}
			catalog, err := s.SaveProduct(ctx, 0, p)
			require.NoError(t, err)
			s.daypassCfg = config.ShopDaypassConfig{Enabled: true, ProductID: catalog.ID, WeeklyEnabled: true}
			req := CreateOrderRequest{UserID: f.userID, PaymentType: payment.TypeAlipay, IdempotencyKey: "configured-sku-order-123456", ShopSKU: ShopSKUSelection{ID: "week"}, SrcHost: "www.turtleligpt.com", SrcURL: ownerTestReturnURL, Amount: 0.01}
			checkout, err := f.service.CreateShopOrder(ctx, catalog.ID, catalog.Revision, req)
			require.NoError(t, err)
			require.Equal(t, 6.0, checkout.PayAmount)
			fulfillment := f.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(checkout.OrderID)).OnlyX(ctx)
			frozen, err := shopSnapshot(fulfillment)
			require.NoError(t, err)
			require.Equal(t, "week", frozen.SKUID)
			require.Len(t, frozen.SKUs, 1)
			require.Equal(t, 7, frozen.DurationValue)
			selected, err := selectShopSKU(*catalog, req.ShopSKU)
			require.NoError(t, err)
			require.NoError(t, revalidateShopProductTx(ctx, f.client, &selected))
			catalog.SKUs[1].Price = 49.9
			_, err = s.SaveProduct(ctx, catalog.ID, *catalog)
			require.NoError(t, err)
			require.Error(t, revalidateShopProductTx(ctx, f.client, &selected))
			replay, err := f.service.CreateShopOrder(ctx, catalog.ID, catalog.Revision, req)
			require.NoError(t, err)
			require.Equal(t, checkout.OrderID, replay.OrderID)
			require.Equal(t, 6.0, replay.PayAmount)
			for _, id := range []string{"day", ""} {
				changed := req
				changed.ShopSKU.ID = id
				_, err = f.service.CreateShopOrder(ctx, catalog.ID, catalog.Revision, changed)
				require.ErrorIs(t, err, ErrIdempotencyKeyConflict)
			}
			if native {
				row := f.client.PaymentOrder.UpdateOneID(checkout.OrderID).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).SaveX(ctx)
				require.NoError(t, markShopPaidTx(ctx, f.client, row))
				action := f.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(row.ID)).OnlyX(ctx)
				var snapshot shopaction.Snapshot
				require.NoError(t, json.Unmarshal([]byte(action.Snapshot), &snapshot))
				require.Equal(t, "week", snapshot.Request.Order.SKU.ID)
				binding := daypassBinding(snapshot.Request)
				require.Equal(t, 2, binding.SKUVersion)
				require.EqualValues(t, 604800, binding.DurationSeconds)
				require.Equal(t, 3, binding.Concurrency)
				require.EqualValues(t, 600, binding.PriceFen)
				claim, err := s.claimShopAction(ctx, row.ID, time.Now())
				require.NoError(t, err)
				require.NotNil(t, claim)
				require.NoError(t, s.executeShopAction(ctx, claim))
				require.Equal(t, row.PaidAt.Unix()+604800, gateway.grant.ExpiresAt)
			}
		})
	}
}
