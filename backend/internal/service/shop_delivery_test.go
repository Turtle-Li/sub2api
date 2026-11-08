//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestShopDeliveryConfigPersistsAndFreezesWithPurchase(t *testing.T) {
	// Given an existing purchase without the optional account-copy setting.
	ctx := context.Background()
	s, _, oldOrder, user := newShopUnit(t, ShopAccountDelivery, false)
	old, err := s.Order(ctx, oldOrder.ID, user.ID, false)
	require.NoError(t, err)
	p, err := s.Product(ctx, old.Product.ID, true)
	require.NoError(t, err)
	p.DeliveryConfig = &ShopDeliveryConfig{IndividualCopy: true}
	saved, err := s.SaveProduct(ctx, p.ID, *p)
	require.NoError(t, err)
	loaded, err := s.Product(ctx, saved.ID, false)
	require.NoError(t, err)
	require.Equal(t, &ShopDeliveryConfig{IndividualCopy: true}, loaded.DeliveryConfig)
	newOrder := createInvoiceUnitOrder(t, ctx, s.client, user, OrderStatusPending, false, false)
	require.NoError(t, createShopFulfillmentTx(ctx, s.client, newOrder, *saved))
	// When an administrator subsequently disables the catalog option.
	saved.DeliveryConfig = &ShopDeliveryConfig{}
	disabled, err := s.SaveProduct(ctx, saved.ID, *saved)
	require.NoError(t, err)
	// Then only the purchase made with opt-in retains individual copying.
	require.Nil(t, disabled.DeliveryConfig)
	old, err = s.Order(ctx, oldOrder.ID, user.ID, false)
	require.NoError(t, err)
	require.Nil(t, old.Product.DeliveryConfig)
	frozen, err := shopSnapshot(s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(newOrder.ID)).OnlyX(ctx))
	require.NoError(t, err)
	require.Equal(t, &ShopDeliveryConfig{IndividualCopy: true}, frozen.DeliveryConfig)
	legacy, err := shopProductMap(old.Product)
	require.NoError(t, err)
	require.NotContains(t, legacy, "delivery_config", "absent historical fields must stay absent")
}

func TestShopDeliveryConfigRejectsTopupAndAutomaticProducts(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual topup", true: "automatic key"}[automatic], func(t *testing.T) {
			// Given a real top-up or native automatic product.
			ctx := context.Background()
			var s *ShopService
			var o *dbent.PaymentOrder
			if automatic {
				s, o, _ = newDaypassUnit(t)
			} else {
				s, _, o, _ = newShopUnit(t, ShopSessionTopup, false)
			}
			detail, err := s.Order(ctx, o.ID, o.UserID, true)
			require.NoError(t, err)
			p, err := s.Product(ctx, detail.Product.ID, true)
			require.NoError(t, err)
			p.DeliveryConfig = &ShopDeliveryConfig{IndividualCopy: true}
			// When an API client tries to enable account-only copying.
			_, err = s.SaveProduct(ctx, p.ID, *p)
			// Then the server rejects the incompatible setting.
			require.Equal(t, "SHOP_DELIVERY_CONFIG_INVALID", infraerrors.Reason(err))
		})
	}
}

func TestShopLegacyDeliveryPresentationKeepsStoredCiphertext(t *testing.T) {
	// Given a completed native purchase with the legacy sentence in its encrypted content.
	ctx := context.Background()
	s, o, _ := newDaypassUnit(t)
	claim, err := s.claimDaypass(ctx, o.ID, time.Now())
	require.NoError(t, err)
	require.NoError(t, s.deliverDaypass(ctx, claim))
	detail, err := s.Order(ctx, o.ID, o.UserID, false)
	require.NoError(t, err)
	original := detail.DeliveryContent + "\n无需注册或登录New API。"
	ciphertext, err := s.encrypt(o.ID, "delivery", 1, original)
	require.NoError(t, err)
	f := s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).OnlyX(ctx)
	f = s.client.ShopFulfillment.UpdateOne(f).SetDeliveryCiphertext(ciphertext).SaveX(ctx)
	// When the buyer reads the old delivery and its pending email is sent.
	visible, err := s.Order(ctx, o.ID, o.UserID, false)
	require.NoError(t, err)
	require.NoError(t, s.RecoverNotifications(ctx))
	// Then both presentations omit only that sentence without rewriting delivery history.
	require.Equal(t, detail.DeliveryContent, visible.DeliveryContent)
	stored := s.client.ShopFulfillment.GetX(ctx, f.ID)
	require.Equal(t, ciphertext, stored.DeliveryCiphertext)
	decrypted, err := s.decrypt(o.ID, "delivery", 1, stored.DeliveryCiphertext)
	require.NoError(t, err)
	require.Equal(t, original, decrypted)
	mail := s.email.(*shopTestMail)
	require.Len(t, mail.bodies, 1)
	require.NotContains(t, mail.bodies[0], "无需注册或登录New API。")
	require.Contains(t, mail.bodies[0], "#usage-guide")
	require.Equal(t, original, shopDeliveryContent(ShopProduct{FulfillmentMode: ShopAccountDelivery}, original), "manual delivery text is not rewritten")
}
