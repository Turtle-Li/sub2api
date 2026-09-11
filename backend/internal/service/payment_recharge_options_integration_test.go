//go:build integration

package service

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestRechargeOptionsFirstWritePostgresSerializesBalanceCheckout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine", tcpostgres.WithDatabase("sub2api_recharge_options_test"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))

	user, err := client.User.Create().
		SetEmail("recharge-options-postgres@example.test").
		SetPasswordHash("hash").
		SetUsername("recharge-options-postgres").
		Save(ctx)
	require.NoError(t, err)

	svc := &PaymentService{entClient: client}
	cfg := &PaymentConfig{MaxPendingOrders: 100, OrderTimeoutMin: 30}
	request := CreateOrderRequest{
		UserID:      user.ID,
		PaymentType: payment.TypeAlipay,
		OrderType:   payment.OrderTypeBalance,
		ClientIP:    "127.0.0.1",
		SrcHost:     "recharge-options.integration.test",
	}
	createOrder := func() (*dbent.PaymentOrder, error) {
		return svc.createOrderInTx(ctx, request, &User{ID: user.ID, Email: user.Email, Username: user.Username}, nil, cfg, 88, 88, 0, 88, nil)
	}
	clearRechargeOptions := func() {
		_, deleteErr := client.Setting.Delete().Where(setting.KeyEQ(SettingRechargeOptions)).Exec(ctx)
		require.NoError(t, deleteErr)
	}
	restrictedOptions, err := encodeRechargeOptions([]RechargeOption{{
		Amount:  88,
		Enabled: true,
		PurchaseRules: &PurchaseRules{
			VisibleUserIDs: []int64{user.ID + 1},
		},
	}})
	require.NoError(t, err)

	t.Run("absent setting is the persistent custom-mode default", func(t *testing.T) {
		clearRechargeOptions()
		order, createErr := createOrder()
		require.NoError(t, createErr)
		require.NotNil(t, order)
		stored, readErr := client.Setting.Query().Where(setting.KeyEQ(SettingRechargeOptions)).Only(ctx)
		require.NoError(t, readErr)
		require.Equal(t, "[]", stored.Value)
	})

	t.Run("admin first restricted card rejects stale custom checkout", func(t *testing.T) {
		clearRechargeOptions()
		_, createErr := client.Setting.Create().SetKey(SettingRechargeOptions).SetValue(restrictedOptions).Save(ctx)
		require.NoError(t, createErr)
		before, countErr := client.PaymentOrder.Query().Count(ctx)
		require.NoError(t, countErr)
		_, createErr = createOrder()
		require.Error(t, createErr)
		require.Equal(t, "RECHARGE_OPTION_CHANGED", infraerrors.Reason(createErr))
		after, countErr := client.PaymentOrder.Query().Count(ctx)
		require.NoError(t, countErr)
		require.Equal(t, before, after)
	})

	t.Run("checkout first holds the unique key through the write", func(t *testing.T) {
		clearRechargeOptions()
		checkoutReachedSettingRead := make(chan struct{})
		releaseCheckout := make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseCheckout) }) }
		t.Cleanup(release)
		var paused atomic.Int32
		client.Setting.Intercept(dbent.InterceptFunc(func(next dbent.Querier) dbent.Querier {
			return dbent.QuerierFunc(func(queryCtx context.Context, query dbent.Query) (dbent.Value, error) {
				if paused.CompareAndSwap(0, 1) {
					close(checkoutReachedSettingRead)
					select {
					case <-releaseCheckout:
					case <-queryCtx.Done():
						return nil, queryCtx.Err()
					}
				}
				return next.Query(queryCtx, query)
			})
		}))

		type checkoutResult struct {
			order *dbent.PaymentOrder
			err   error
		}
		checkoutDone := make(chan checkoutResult, 1)
		go func() {
			order, createErr := createOrder()
			checkoutDone <- checkoutResult{order: order, err: createErr}
		}()
		select {
		case <-checkoutReachedSettingRead:
		case <-time.After(5 * time.Second):
			t.Fatal("checkout did not reach the recharge-options read")
		}

		adminDone := make(chan error, 1)
		go func() {
			_, upsertErr := db.ExecContext(ctx, `
				INSERT INTO settings (key, value, updated_at)
				VALUES ($1, $2, NOW())
				ON CONFLICT (key) DO UPDATE
				SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at
				/* recharge_options_first_admin_upsert */`, SettingRechargeOptions, restrictedOptions)
			adminDone <- upsertErr
		}()
		require.Eventually(t, func() bool {
			var waiting int
			err := db.QueryRowContext(ctx, `
				SELECT count(*)
				FROM pg_stat_activity
				WHERE pid <> pg_backend_pid()
					AND wait_event_type = 'Lock'
					AND query LIKE '%recharge_options_first_admin_upsert%'`).Scan(&waiting)
			return err == nil && waiting == 1
		}, 5*time.Second, 20*time.Millisecond, "the first admin upsert must wait for checkout's unique-key lock")

		release()
		select {
		case result := <-checkoutDone:
			require.NoError(t, result.err)
			require.NotNil(t, result.order)
		case <-time.After(5 * time.Second):
			t.Fatal("checkout did not complete after releasing the setting read")
		}
		select {
		case upsertErr := <-adminDone:
			require.NoError(t, upsertErr)
		case <-time.After(5 * time.Second):
			t.Fatal("admin recharge-option upsert did not complete after checkout committed")
		}

		stored, readErr := client.Setting.Query().Where(setting.KeyEQ(SettingRechargeOptions)).Only(ctx)
		require.NoError(t, readErr)
		require.Equal(t, restrictedOptions, stored.Value)
	})
}
