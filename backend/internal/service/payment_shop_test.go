//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestShopCheckoutUsesServerPriceAndIdempotentGatewayRecovery(t *testing.T) {
	for _, lost := range []bool{false, true} {
		name := "normal"
		if lost {
			name = "lost provider response"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := newOwnerTestOrderFixture(t)
			s := NewShopService(f.client, shopTestEncryptor{}, &config.Config{Totp: config.TotpConfig{EncryptionKeyConfigured: true}}, nil, nil)
			f.service.shopService = s
			f.settings.values[SettingPaymentEnabled] = "true"
			f.settings.values[SettingRechargeFeeRate] = "10"
			f.settings.values[SettingBalancePayDisabled] = "true"
			f.settings.values[SettingPaymentVisibleMethodAlipaySource] = VisibleMethodSourceUnifiedAlipay
			f.central.loseFirstResponse = lost
			p, err := s.SaveProduct(ctx, 0, ShopProduct{Name: "独立商城商品", Price: 12.34, Enabled: true, FulfillmentMode: ShopAccountDelivery})
			require.NoError(t, err)
			req := CreateOrderRequest{UserID: f.userID, PaymentType: payment.TypeAlipay, IdempotencyKey: "shop-checkout-test-123456", SrcHost: "www.turtleligpt.com", SrcURL: ownerTestReturnURL}
			first, err := f.service.CreateShopOrder(ctx, p.ID, p.Revision, req)
			if lost {
				require.ErrorIs(t, err, unifiedpay.ErrCreateStateUnconfirmed)
			} else {
				require.NoError(t, err)
				require.NotNil(t, first)
			}
			replayed, err := f.service.CreateShopOrder(ctx, p.ID, p.Revision, req)
			require.NoError(t, err)
			require.Equal(t, 12.34, replayed.PayAmount)
			require.Zero(t, replayed.Amount)
			require.Zero(t, replayed.FeeRate)
			require.NotEmpty(t, replayed.PayURL)
			require.Equal(t, 1, f.client.PaymentOrder.Query().CountX(ctx))
			require.Equal(t, 1, f.client.ShopFulfillment.Query().CountX(ctx))
			f.central.mu.Lock()
			posts := append([]string{}, f.central.posts...)
			count := len(f.central.orders)
			f.central.mu.Unlock()
			require.Equal(t, 1, count)
			for _, body := range posts {
				var command struct {
					AmountFen int64  `json:"amount_fen"`
					OrderType string `json:"order_type"`
				}
				require.NoError(t, json.Unmarshal([]byte(body), &command))
				require.Equal(t, int64(1234), command.AmountFen)
				require.Equal(t, payment.OrderTypeManualShop, command.OrderType)
			}
			o := f.client.PaymentOrder.GetX(ctx, replayed.OrderID)
			require.Zero(t, o.BonusAmount)
			require.Equal(t, payment.OrderTypeManualShop, o.OrderType)
			d, err := s.Order(ctx, o.ID, f.userID, false)
			require.NoError(t, err)
			require.Equal(t, ShopWaitingPayment, d.FulfillmentStatus)
			// Changing the live catalog must not break a retry of the original attempt.
			p.Name = "改价后的商品"
			p.Price = 99
			_, err = s.SaveProduct(ctx, p.ID, *p)
			require.NoError(t, err)
			again, err := f.service.CreateShopOrder(ctx, p.ID, p.Revision, req)
			require.NoError(t, err)
			require.Equal(t, replayed.OrderID, again.OrderID)
			require.Equal(t, 12.34, again.PayAmount)
			_, err = f.service.CreateShopOrder(ctx, p.ID, p.Revision+1, req)
			require.ErrorIs(t, err, ErrIdempotencyKeyConflict)
			_, err = f.service.CreateOrder(ctx, CreateOrderRequest{UserID: f.userID, OrderType: payment.OrderTypeManualShop, Amount: 0.01})
			require.Equal(t, "INVALID_ORDER_TYPE", infraerrors.Reason(err))
		})
	}
}

func TestShopCheckoutSKUPricesSnapshotsAndReplay(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sku      ShopSKUSelection
		expected ShopSKUSelection
		price    float64
	}{
		{"legacy", ShopSKUSelection{}, ShopSKUSelection{Concurrency: 2, DurationDays: 1}, 6},
		{"day-three", ShopSKUSelection{Concurrency: 3, DurationDays: 1}, ShopSKUSelection{Concurrency: 3, DurationDays: 1}, 8},
		{"week-two", ShopSKUSelection{DurationDays: 7}, ShopSKUSelection{Concurrency: 2, DurationDays: 7}, 30},
		{"week-three", ShopSKUSelection{Concurrency: 3, DurationDays: 7}, ShopSKUSelection{Concurrency: 3, DurationDays: 7}, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := newOwnerTestOrderFixture(t)
			createInvoiceUnitRefundFenceTables(t, ctx, f.client)
			s := NewShopService(f.client, shopTestEncryptor{}, &config.Config{Totp: config.TotpConfig{EncryptionKeyConfigured: true}}, nil, nil)
			f.service.shopService = s
			f.settings.values[SettingPaymentEnabled] = "true"
			f.settings.values[SettingPaymentVisibleMethodAlipaySource] = VisibleMethodSourceUnifiedAlipay
			p, err := s.SaveProduct(ctx, 0, ShopProduct{Name: "DeepSeek API 不限量订阅卡", Price: 6, DurationValue: 1, DurationUnit: "day", Enabled: true, FulfillmentMode: ShopAccountDelivery, PaymentActions: daypassTestActions(s)})
			require.NoError(t, err)
			s.daypassCfg = config.ShopDaypassConfig{Enabled: true, ProductID: p.ID, WeeklyEnabled: true}
			gateway := &daypassTestGateway{}
			s.daypass = gateway
			catalog, err := s.Product(ctx, p.ID, false)
			require.NoError(t, err)
			require.Equal(t, []ShopConcurrencyOption{{Concurrency: 2, Price: 6, WeeklyPrice: 30}, {Concurrency: 3, Price: 8, WeeklyPrice: 40}}, catalog.ConcurrencyOptions)
			require.Empty(t, catalog.PaymentActions)
			req := CreateOrderRequest{UserID: f.userID, PaymentType: payment.TypeAlipay, IdempotencyKey: "shop-sku-checkout-123456", ShopSKU: tc.sku, SrcHost: "www.turtleligpt.com", SrcURL: ownerTestReturnURL}
			first, err := f.service.CreateShopOrder(ctx, p.ID, p.Revision, req)
			require.NoError(t, err)
			require.Equal(t, tc.price, first.PayAmount)
			row := f.client.PaymentOrder.GetX(ctx, first.OrderID)
			require.EqualValues(t, tc.expected.Concurrency, row.ProductSnapshot["concurrency"])
			require.EqualValues(t, tc.expected.DurationDays, row.ProductSnapshot["duration_value"])
			snapshot, err := shopSnapshot(f.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(row.ID)).OnlyX(ctx))
			require.NoError(t, err)
			require.Equal(t, tc.expected, shopProductSKU(snapshot).selection())
			require.Equal(t, tc.price, snapshot.Price)
			require.Equal(t, tc.expected.DurationDays, snapshot.DurationValue)
			require.Equal(t, "day", snapshot.DurationUnit)
			s.daypassCfg.WeeklyEnabled = false
			fresh := req
			fresh.IdempotencyKey = "shop-new-disabled-123456"
			fresh.ShopSKU = ShopSKUSelection{DurationDays: 7}
			_, err = f.service.CreateShopOrder(ctx, p.ID, p.Revision, fresh)
			require.Equal(t, "SHOP_DURATION_INVALID", infraerrors.Reason(err))
			s.daypassCfg.MaxConcurrency = 2
			fresh.ShopSKU = ShopSKUSelection{Concurrency: 3}
			_, err = f.service.CreateShopOrder(ctx, p.ID, p.Revision, fresh)
			require.Equal(t, "SHOP_CONCURRENCY_INVALID", infraerrors.Reason(err))
			catalog, err = s.Product(ctx, p.ID, false)
			require.NoError(t, err)
			require.Equal(t, []ShopConcurrencyOption{{Concurrency: 2, Price: 6}}, catalog.ConcurrencyOptions)
			// A sales switch cannot retroactively change an already issued checkout.
			changed := req
			changed.ShopSKU.Concurrency = 3
			if tc.expected.Concurrency == 3 {
				changed.ShopSKU.Concurrency = 2
			}
			_, err = f.service.CreateShopOrder(ctx, p.ID, p.Revision, changed)
			require.ErrorIs(t, err, ErrIdempotencyKeyConflict)
			changed = req
			changed.ShopSKU.DurationDays = 7
			if tc.expected.DurationDays == 7 {
				changed.ShopSKU.DurationDays = 0
			}
			_, err = f.service.CreateShopOrder(ctx, p.ID, p.Revision, changed)
			require.ErrorIs(t, err, ErrIdempotencyKeyConflict)
			p.Price = 99
			p.PaymentActions = nil
			_, err = s.SaveProduct(ctx, p.ID, *p)
			require.NoError(t, err)
			replay, err := f.service.CreateShopOrder(ctx, p.ID, p.Revision, req)
			require.NoError(t, err)
			require.Equal(t, first.OrderID, replay.OrderID)
			require.Equal(t, tc.price, replay.PayAmount)
			// Local fixture only: exercise trusted paid-outbox delivery after a catalog edit.
			row = f.client.PaymentOrder.UpdateOne(row).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).SaveX(ctx)
			require.NoError(t, markShopPaidTx(ctx, f.client, row))
			claim, err := s.claimShopAction(ctx, row.ID, time.Now())
			require.NoError(t, err)
			require.NotNil(t, claim)
			s.daypassCfg.MaxConcurrency = 2
			require.NoError(t, s.executeShopAction(ctx, claim))
			buyer, err := s.Order(ctx, row.ID, f.userID, false)
			require.NoError(t, err)
			require.Equal(t, tc.expected.Concurrency, buyer.Product.Concurrency)
			require.Equal(t, tc.expected.DurationDays, buyer.Product.DurationValue)
			require.Equal(t, row.PaidAt.Unix()+int64(tc.expected.DurationDays)*86400, gateway.grant.ExpiresAt)
			require.Contains(t, buyer.DeliveryContent, fmt.Sprintf("%d并发", tc.expected.Concurrency))
			require.Contains(t, buyer.DeliveryContent, fmt.Sprintf("有效期：%d 天", tc.expected.DurationDays))
		})
	}
}
