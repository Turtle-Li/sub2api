//go:build integration

package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/ent/shopnotification"
	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

type shopPGCipher struct{}

func (shopPGCipher) Encrypt(s string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func (shopPGCipher) Decrypt(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}

type shopPGMail struct {
	mu    sync.Mutex
	calls int
}

func (m *shopPGMail) SendEmail(context.Context, string, string, string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	return nil
}

type shopPGFeishu struct{}

func (shopPGFeishu) SendText(context.Context, string) error { return nil }

func TestShopPostgresAtomicDeliveryAndRefundFences(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine", tcpostgres.WithDatabase("shop_test"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))
	// Exercise the authored migration rather than Ent's unconstrained test schema.
	_, err = db.ExecContext(ctx, "DROP TABLE shop_order_actions,shop_notifications,shop_fulfillments,shop_products")
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("265_manual_shop.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	actionsMigration, err := migrations.FS.ReadFile("266_shop_order_actions.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(actionsMigration))
	require.NoError(t, err)
	skuMigration, err := migrations.FS.ReadFile("274_shop_sku_config.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = db.ExecContext(ctx, string(skuMigration))
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, "CREATE TABLE unified_payment_refund_attempts(order_id BIGINT,needs_manual_review BOOLEAN NOT NULL DEFAULT FALSE); CREATE TABLE unified_payment_refund_events(order_id BIGINT,action TEXT NOT NULL)")
	require.NoError(t, err)
	u := createPaymentInvoicePostgresUser(t, ctx, client)
	s := NewShopService(client, shopPGCipher{}, &config.Config{Totp: config.TotpConfig{EncryptionKeyConfigured: true}}, nil, nil)
	mail := &shopPGMail{}
	s.email = mail
	s.feishu = shopPGFeishu{}
	t.Run("configured SKU JSONB persistence snapshot and revision lock", func(t *testing.T) {
		p, err := s.SaveProduct(ctx, 0, ShopProduct{Name: "PG SKU", Enabled: true, FulfillmentMode: ShopAccountDelivery, DefaultSKUID: "week", SKUs: []ShopSKU{
			{ID: "day", Attributes: map[string]string{"周期": "日卡"}, Price: 9.9, DurationValue: 1, DurationUnit: "day", Enabled: true},
			{ID: "week", Attributes: map[string]string{"周期": "周卡"}, Price: 49.9, DurationValue: 7, DurationUnit: "day", Enabled: true},
		}})
		require.NoError(t, err)
		loaded, err := s.Product(ctx, p.ID, true)
		require.NoError(t, err)
		require.Equal(t, p.SKUs, loaded.SKUs)
		selected, err := selectShopSKU(*loaded, ShopSKUSelection{ID: "week"})
		require.NoError(t, err)
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		require.NoError(t, revalidateShopProductTx(ctx, tx.Client(), &selected))
		require.NoError(t, tx.Commit())
		o := createPaymentInvoicePostgresOrder(t, ctx, client, u, fmt.Sprintf("sku-%d", time.Now().UnixNano()))
		o = client.PaymentOrder.UpdateOne(o).SetOrderType(payment.OrderTypeManualShop).SetAmount(0).SetPayAmount(49.9).SaveX(ctx)
		require.NoError(t, createShopFulfillmentTx(ctx, client, o, selected))
		frozen, err := shopSnapshot(client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).OnlyX(ctx))
		require.NoError(t, err)
		require.Equal(t, "week", frozen.SKUID)
		require.Len(t, frozen.SKUs, 1)
		require.Equal(t, 49.9, frozen.Price)
		loaded.SKUs[1].Price = 59.9
		_, err = s.SaveProduct(ctx, loaded.ID, *loaded)
		require.NoError(t, err)
		require.Error(t, revalidateShopProductTx(ctx, client, &selected))
		require.Equal(t, 49.9, frozen.Price)
	})
	makeOrder := func(mode string) (*dbent.PaymentOrder, *ShopOrderDetail) {
		p, err := s.SaveProduct(ctx, 0, ShopProduct{Name: "PG 商品", Price: 108, Enabled: true, FulfillmentMode: mode})
		require.NoError(t, err)
		o := createPaymentInvoicePostgresOrder(t, ctx, client, u, fmt.Sprintf("shop-%d", time.Now().UnixNano()))
		o, err = client.PaymentOrder.UpdateOne(o).SetOrderType(payment.OrderTypeManualShop).SetStatus(OrderStatusPaid).SetAmount(0).ClearCompletedAt().Save(ctx)
		require.NoError(t, err)
		require.NoError(t, createShopFulfillmentTx(ctx, client, o, *p))
		require.NoError(t, markShopPaidTx(ctx, client, o))
		d, err := s.Order(ctx, o.ID, u.ID, true)
		require.NoError(t, err)
		return o, d
	}

	t.Run("competing completions ship once and notification claims send once", func(t *testing.T) {
		o, d := makeOrder(ShopAccountDelivery)
		var wg sync.WaitGroup
		results := make(chan error, 12)
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := s.Action(ctx, o.ID, u.ID, ShopActionInput{Action: "complete", Revision: d.Revision, DeliveryContent: "账号：synthetic@example.test\n密码：synthetic"})
				results <- e
			}()
		}
		wg.Wait()
		close(results)
		success := 0
		for e := range results {
			if e == nil {
				success++
			}
		}
		require.Equal(t, 1, success)
		require.Equal(t, 1, client.ShopNotification.Query().Where(shopnotification.OrderIDEQ(o.ID), shopnotification.ChannelEQ("email")).CountX(ctx))
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); resultsErr := s.RecoverNotifications(ctx); require.NoError(t, resultsErr) }()
		}
		wg.Wait()
		mail.mu.Lock()
		calls := mail.calls
		mail.mu.Unlock()
		require.Equal(t, 1, calls)
		require.Equal(t, OrderStatusCompleted, client.PaymentOrder.GetX(ctx, o.ID).Status)
	})
	t.Run("refund row lock wins before manual action", func(t *testing.T) {
		o, d := makeOrder(ShopAccountDelivery)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, "SELECT id FROM payment_orders WHERE id=$1 FOR UPDATE", o.ID)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() {
			_, e := s.Action(ctx, o.ID, u.ID, ShopActionInput{Action: "complete", Revision: d.Revision, DeliveryContent: "should never ship"})
			done <- e
		}()
		require.Eventually(t, func() bool {
			var n int
			e := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%payment_orders%' AND pid<>pg_backend_pid()").Scan(&n)
			return e == nil && n > 0
		}, 5*time.Second, 10*time.Millisecond)
		_, err = tx.ExecContext(ctx, "UPDATE payment_orders SET status='REFUND_PENDING' WHERE id=$1", o.ID)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
		require.Equal(t, "SHOP_PAYMENT_NOT_ACTIONABLE", infraerrors.Reason(<-done))
		f := client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).OnlyX(ctx)
		require.Equal(t, ShopQueued, f.Status)
		require.Empty(t, f.DeliveryCiphertext)
		require.Zero(t, client.ShopNotification.Query().Where(shopnotification.OrderIDEQ(o.ID), shopnotification.ChannelEQ("email")).CountX(ctx))
	})
	for _, sku := range []ShopSKUSelection{
		{Concurrency: 2, DurationDays: 1}, {Concurrency: 3, DurationDays: 1},
		{Concurrency: 2, DurationDays: 7}, {Concurrency: 3, DurationDays: 7},
	} {
		t.Run(fmt.Sprintf("DeepSeek PostgreSQL claims fence competing workers and lost replies/%dd-%d", sku.DurationDays, sku.Concurrency), func(t *testing.T) {
			p, err := s.SaveProduct(ctx, 0, ShopProduct{Name: "PG DeepSeek", Price: 6, DurationValue: 1, DurationUnit: "day", FulfillmentMode: ShopAccountDelivery, GuideKey: ShopDaypassGuide, PaymentActions: daypassTestActions(s)})
			require.NoError(t, err)
			s.daypassCfg = config.ShopDaypassConfig{Enabled: true, ProductID: p.ID, WeeklyEnabled: true}
			selected, err := s.resolveShopSKU(*p, sku)
			require.NoError(t, err)
			p = &selected
			gateway := &daypassTestGateway{lost: true}
			s.daypass = gateway
			o := createPaymentInvoicePostgresOrder(t, ctx, client, u, fmt.Sprintf("daypass-%dd-%d", sku.DurationDays, sku.Concurrency))
			o = client.PaymentOrder.UpdateOne(o).SetOrderType(payment.OrderTypeManualShop).SetStatus(OrderStatusPaid).SetAmount(0).SetPayAmount(p.Price).ClearCompletedAt().SaveX(ctx)
			require.NoError(t, createShopFulfillmentTx(ctx, client, o, *p))
			require.NoError(t, markShopPaidTx(ctx, client, o))
			claims := make(chan *shopDaypassClaim, 8)
			failures := make(chan error, 8)
			var wg sync.WaitGroup
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					claim, e := s.claimDaypass(ctx, o.ID, time.Now())
					claims <- claim
					failures <- e
				}()
			}
			wg.Wait()
			close(claims)
			close(failures)
			for e := range failures {
				require.NoError(t, e)
			}
			var first *shopDaypassClaim
			for claim := range claims {
				if claim != nil {
					require.Nil(t, first)
					first = claim
				}
			}
			require.NotNil(t, first)
			require.ErrorIs(t, s.deliverDaypass(ctx, first), errShopDaypassUnavailable)
			client.ShopOrderAction.Update().Where(shoporderaction.OrderIDEQ(o.ID)).SetLeaseUntil(time.Now().Add(-time.Second)).SetNextAttemptAt(time.Now().Add(-time.Second)).ExecX(ctx)
			second, err := s.claimDaypass(ctx, o.ID, time.Now())
			require.NoError(t, err)
			require.NotNil(t, second)
			require.ErrorIs(t, s.deliverDaypass(ctx, first), errShopDaypassLeaseLost)
			require.Zero(t, gateway.revocations)
			require.NoError(t, s.deliverDaypass(ctx, second))
			require.Equal(t, OrderStatusCompleted, client.PaymentOrder.GetX(ctx, o.ID).Status)
			require.Equal(t, 1, client.ShopNotification.Query().Where(shopnotification.OrderIDEQ(o.ID), shopnotification.ChannelEQ("email")).CountX(ctx))
			require.Equal(t, o.PaidAt.Unix()+int64(sku.DurationDays)*86400, gateway.grant.ExpiresAt)
			detail, err := s.Order(ctx, o.ID, u.ID, false)
			require.NoError(t, err)
			require.Equal(t, sku.Concurrency, detail.Product.Concurrency)
			require.Equal(t, sku.DurationDays, detail.Product.DurationValue)
		})
	}

	t.Run("concurrent customer submissions preserve one complete version", func(t *testing.T) {
		o, d := makeOrder(ShopSessionTopup)
		var wg sync.WaitGroup
		results := make(chan error, 10)
		content := `{"accessToken":"eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjQwNzA5MDg4MDB9.c3ludGhldGljLXNpZ25hdHVyZQ","user":{"email":"test@example.test"}}`
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, e := s.SubmitInput(ctx, o.ID, u.ID, d.Revision, content); results <- e }()
		}
		wg.Wait()
		close(results)
		success := 0
		for e := range results {
			if e == nil {
				success++
			}
		}
		require.Equal(t, 1, success)
		d, err = s.Order(ctx, o.ID, u.ID, true)
		require.NoError(t, err)
		raw, version, err := s.RevealInput(ctx, o.ID, u.ID, d.Revision)
		require.NoError(t, err)
		require.Equal(t, content, raw)
		require.Equal(t, 1, version)
		require.Equal(t, 1, client.ShopNotification.Query().Where(shopnotification.OrderIDEQ(o.ID), shopnotification.EventEQ("input_received")).CountX(ctx))
	})
}
