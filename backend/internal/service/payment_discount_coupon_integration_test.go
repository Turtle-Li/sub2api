//go:build integration

package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPaymentDiscountPostgresConcurrentReservationCapacity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:18-alpine", tcpostgres.WithDatabase("sub2api_coupon_test"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))
	migration, err := migrations.FS.ReadFile("253_payment_discount_coupons.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	scopeMigration, err := migrations.FS.ReadFile("254_payment_discount_scope.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(scopeMigration))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(scopeMigration))
	require.NoError(t, err, "scope migration remains safe when a runner retries before recording completion")

	admin := createPaymentDiscountPostgresTestUser(t, ctx, client, "admin")
	firstUser := createPaymentDiscountPostgresTestUser(t, ctx, client, "first")
	secondUser := createPaymentDiscountPostgresTestUser(t, ctx, client, "second")
	svc := &PaymentService{entClient: client}
	perUserUnlimited := 0
	coupon, err := svc.SavePaymentDiscountCode(ctx, admin.ID, 0, PaymentDiscountCodeInput{
		Code:           "POSTGRES26",
		DiscountType:   paymentDiscountTypeFixed,
		DiscountValue:  "20.00",
		Currency:       "CNY",
		MaxUses:        1,
		PerUserMaxUses: &perUserUnlimited,
		StartsAt:       time.Now().UTC().Add(-time.Minute),
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
		Enabled:        true,
	}, 0)
	require.NoError(t, err)
	orders := []*dbent.PaymentOrder{
		createPaymentDiscountPostgresTestOrder(t, ctx, client, firstUser, "first"),
		createPaymentDiscountPostgresTestOrder(t, ctx, client, secondUser, "second"),
	}
	users := []*dbent.User{firstUser, secondUser}

	start := make(chan struct{})
	ready := make(chan struct{}, len(orders))
	results := make(chan error, len(orders))
	var wg sync.WaitGroup
	for index := range orders {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			<-start
			tx, txErr := client.Tx(ctx)
			if txErr != nil {
				results <- txErr
				return
			}
			defer func() { _ = tx.Rollback() }()
			txCtx := dbent.NewTxContext(ctx, tx)
			quote, quoteErr := quotePaymentDiscount(txCtx, tx.Client(), users[index].ID, coupon.Code, decimal.NewFromInt(100), "CNY", payment.OrderTypeBalance, 0, fmt.Sprintf("postgres-reservation-%d", users[index].ID), true)
			if quoteErr != nil {
				results <- quoteErr
				return
			}
			requestHash := fmt.Sprintf("%064x", index+1)
			idempotencyHash := fmt.Sprintf("%064x", index+101)
			if reserveErr := reservePaymentDiscount(txCtx, tx.Client(), orders[index].ID, users[index].ID, quote, requestHash, idempotencyHash); reserveErr != nil {
				results <- reserveErr
				return
			}
			results <- tx.Commit()
		}()
	}
	for range orders {
		<-ready
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	invalid := 0
	for result := range results {
		if result == nil {
			successes++
			continue
		}
		if errors.Is(result, ErrPaymentDiscountInvalid) {
			invalid++
			continue
		}
		require.NoError(t, result)
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, invalid)
	var active int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_discount_uses WHERE code_id = $1 AND status = 'reserved'`, coupon.ID).Scan(&active))
	require.Equal(t, 1, active)
}

func createPaymentDiscountPostgresTestUser(t *testing.T, ctx context.Context, client *dbent.Client, suffix string) *dbent.User {
	t.Helper()
	identifier := fmt.Sprintf("coupon-pg-%s-%d", suffix, time.Now().UnixNano())
	user, err := client.User.Create().
		SetEmail(identifier + "@example.test").
		SetPasswordHash("test").
		SetUsername(identifier).
		Save(ctx)
	require.NoError(t, err)
	return user
}

func createPaymentDiscountPostgresTestOrder(t *testing.T, ctx context.Context, client *dbent.Client, user *dbent.User, suffix string) *dbent.PaymentOrder {
	t.Helper()
	identifier := fmt.Sprintf("coupon-pg-order-%s-%d", suffix, time.Now().UnixNano())
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(80).
		SetFeeRate(0).
		SetRechargeCode(identifier).
		SetOutTradeNo(identifier).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().UTC().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("coupon-postgres.test").
		Save(ctx)
	require.NoError(t, err)
	return order
}
