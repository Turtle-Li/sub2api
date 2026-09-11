//go:build integration

package repository

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type paymentFulfillmentRecoveryPostgresFixture struct {
	t             *testing.T
	client        *dbent.Client
	userIDs       []int64
	groupIDs      []int64
	orderIDs      []int64
	rechargeCodes []string
}

func newPaymentFulfillmentRecoveryPostgresFixture(t *testing.T) *paymentFulfillmentRecoveryPostgresFixture {
	t.Helper()
	// Share the harness driver's pool, but use a fresh Ent client configuration
	// so test-local hooks do not leak into sibling integration cases. Do not call
	// Close on it: the driver is owned by integrationEntClient/TestMain.
	client := dbent.NewClient(dbent.Driver(integrationEntClient.Driver()))

	f := &paymentFulfillmentRecoveryPostgresFixture{t: t, client: client}
	t.Cleanup(f.cleanup)
	return f
}

func (f *paymentFulfillmentRecoveryPostgresFixture) createUser(balance float64) *service.User {
	f.t.Helper()
	unique := uuid.NewString()
	user := mustCreateUser(f.t, f.client, &service.User{
		Email:        unique + "@payment-fulfillment.integration.test",
		PasswordHash: "test-only-password-hash",
		Username:     "fulfillment-" + unique[:8],
		Balance:      balance,
		Concurrency:  1,
	})
	f.userIDs = append(f.userIDs, user.ID)
	return user
}

func (f *paymentFulfillmentRecoveryPostgresFixture) createSubscriptionGroup() *service.Group {
	f.t.Helper()
	unique := uuid.NewString()
	group := mustCreateGroup(f.t, f.client, &service.Group{
		Name:             "fulfillment-subscription-" + unique[:8],
		Platform:         service.PlatformAnthropic,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	f.groupIDs = append(f.groupIDs, group.ID)
	return group
}

func (f *paymentFulfillmentRecoveryPostgresFixture) createPaidBalanceOrder(user *service.User, amount float64, status string, updatedAt time.Time) *dbent.PaymentOrder {
	f.t.Helper()
	unique := uuid.NewString()
	order, err := f.client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(amount).
		SetPayAmount(amount).
		SetFeeRate(0).
		SetRechargeCode("rec-" + unique[:24]).
		SetOutTradeNo("sub2_rec_" + unique[:24]).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-" + unique[:24]).
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(status).
		SetPaidAt(time.Now().UTC().Add(-time.Hour)).
		SetExpiresAt(time.Now().UTC().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("integration.test").
		SetUpdatedAt(updatedAt).
		Save(context.Background())
	require.NoError(f.t, err)
	f.orderIDs = append(f.orderIDs, order.ID)
	f.rechargeCodes = append(f.rechargeCodes, order.RechargeCode)
	return order
}

func (f *paymentFulfillmentRecoveryPostgresFixture) createPaidSubscriptionOrder(user *service.User, groupID int64, days int, status string, updatedAt time.Time) *dbent.PaymentOrder {
	f.t.Helper()
	unique := uuid.NewString()
	order, err := f.client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(10).
		SetPayAmount(10).
		SetFeeRate(0).
		SetRechargeCode("rec-sub-" + unique[:20]).
		SetOutTradeNo("sub2_rec_sub_" + unique[:20]).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-sub-" + unique[:20]).
		SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(1).
		SetSubscriptionGroupID(groupID).
		SetSubscriptionDays(days).
		SetStatus(status).
		SetPaidAt(time.Now().UTC().Add(-time.Hour)).
		SetExpiresAt(time.Now().UTC().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("integration.test").
		SetUpdatedAt(updatedAt).
		Save(context.Background())
	require.NoError(f.t, err)
	f.orderIDs = append(f.orderIDs, order.ID)
	f.rechargeCodes = append(f.rechargeCodes, order.RechargeCode)
	return order
}

func (f *paymentFulfillmentRecoveryPostgresFixture) paymentService() *service.PaymentService {
	f.t.Helper()
	userRepo := NewUserRepository(f.client, integrationDB)
	groupRepo := NewGroupRepository(f.client, integrationDB)
	subscriptionSvc := service.NewSubscriptionService(
		groupRepo,
		NewUserSubscriptionRepository(f.client),
		nil,
		f.client,
		nil,
	)
	redeemSvc := service.NewRedeemService(
		NewRedeemCodeRepository(f.client),
		userRepo,
		subscriptionSvc,
		nil,
		nil,
		f.client,
		nil,
		nil,
	)
	return service.NewPaymentService(f.client, nil, nil, redeemSvc, subscriptionSvc, nil, userRepo, groupRepo, nil)
}

func (f *paymentFulfillmentRecoveryPostgresFixture) cleanup() {
	ctx := context.Background()
	for _, orderID := range f.orderIDs {
		for _, query := range []string{
			`DELETE FROM unified_payment_refund_events WHERE order_id = $1`,
			`DELETE FROM unified_payment_refund_attempts WHERE order_id = $1`,
			`DELETE FROM payment_audit_logs WHERE order_id = $1`,
			`DELETE FROM subscription_reset_grants WHERE payment_order_id = $1`,
			`DELETE FROM payment_orders WHERE id = $1`,
		} {
			if _, err := integrationDB.ExecContext(ctx, query, orderID); err != nil {
				f.t.Errorf("payment fulfillment recovery cleanup order %d: %v", orderID, err)
			}
		}
	}
	for _, code := range f.rechargeCodes {
		if _, err := integrationDB.ExecContext(ctx, `DELETE FROM redeem_codes WHERE code = $1`, code); err != nil {
			f.t.Errorf("payment fulfillment recovery cleanup redeem code %q: %v", code, err)
		}
	}
	for _, userID := range f.userIDs {
		for _, query := range []string{
			`DELETE FROM subscription_reset_grants WHERE user_id = $1`,
			`DELETE FROM user_subscriptions WHERE user_id = $1`,
			`DELETE FROM user_allowed_groups WHERE user_id = $1`,
			`DELETE FROM users WHERE id = $1`,
		} {
			if _, err := integrationDB.ExecContext(ctx, query, userID); err != nil {
				f.t.Errorf("payment fulfillment recovery cleanup user %d: %v", userID, err)
			}
		}
	}
	for _, groupID := range f.groupIDs {
		if _, err := integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id = $1`, groupID); err != nil {
			f.t.Errorf("payment fulfillment recovery cleanup group %d: %v", groupID, err)
		}
	}
}

func TestPaymentFulfillmentRecoveryPostgresRestartedSweeperAndBalanceConcurrency(t *testing.T) {
	ctx := context.Background()

	t.Run("restarted sweeper fulfills paid order without callback", func(t *testing.T) {
		fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
		user := fixture.createUser(0)
		order := fixture.createPaidBalanceOrder(user, 7, service.OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))
		sweeper := service.NewPaymentOrderExpiryService(fixture.paymentService(), time.Hour)
		sweeper.Start()
		t.Cleanup(sweeper.Stop)

		require.Eventually(t, func() bool {
			current, err := fixture.client.PaymentOrder.Get(ctx, order.ID)
			return err == nil && current.Status == service.OrderStatusCompleted
		}, 5*time.Second, 25*time.Millisecond)
		currentUser, err := fixture.client.User.Get(ctx, user.ID)
		require.NoError(t, err)
		require.InDelta(t, 7, currentUser.Balance, 0.000001)
	})

	t.Run("two workers claim one balance order exactly once", func(t *testing.T) {
		fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
		user := fixture.createUser(0)
		order := fixture.createPaidBalanceOrder(user, 11, service.OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))
		svcA := fixture.paymentService()
		svcB := fixture.paymentService()

		arrived := make(chan struct{}, 2)
		release := make(chan struct{})
		var intercepts atomic.Int32
		fixture.client.PaymentOrder.Intercept(dbent.InterceptFunc(func(next dbent.Querier) dbent.Querier {
			return dbent.QuerierFunc(func(queryCtx context.Context, query dbent.Query) (dbent.Value, error) {
				if intercepts.Add(1) <= 2 {
					arrived <- struct{}{}
					<-release
				}
				return next.Query(queryCtx, query)
			})
		}))

		type result struct {
			recovered int
			err       error
		}
		results := make(chan result, 2)
		go func() {
			recovered, err := svcA.RecoverPendingPaymentOrderFulfillments(ctx)
			results <- result{recovered, err}
		}()
		go func() {
			recovered, err := svcB.RecoverPendingPaymentOrderFulfillments(ctx)
			results <- result{recovered, err}
		}()
		for i := 0; i < 2; i++ {
			select {
			case <-arrived:
			case <-time.After(5 * time.Second):
				t.Fatal("recovery workers did not both reach candidate selection")
			}
		}
		close(release)

		first, second := <-results, <-results
		require.NoError(t, first.err)
		require.NoError(t, second.err)

		current, err := fixture.client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, service.OrderStatusCompleted, current.Status)
		currentUser, err := fixture.client.User.Get(ctx, user.ID)
		require.NoError(t, err)
		require.InDelta(t, 11, currentUser.Balance, 0.000001)
		var successAudits int
		require.NoError(t, integrationDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM payment_audit_logs WHERE order_id = $1 AND action = 'RECHARGE_SUCCESS'`, strconv.FormatInt(order.ID, 10)).Scan(&successAudits))
		require.Equal(t, 1, successAudits)
	})

	t.Run("different balance orders retain both credits", func(t *testing.T) {
		fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
		user := fixture.createUser(0)
		first := fixture.createPaidBalanceOrder(user, 3, service.OrderStatusPaid, time.Now().UTC().Add(-3*time.Minute))
		second := fixture.createPaidBalanceOrder(user, 8, service.OrderStatusFailed, time.Now().UTC().Add(-2*time.Minute))

		recovered, err := fixture.paymentService().RecoverPendingPaymentOrderFulfillments(ctx)
		require.NoError(t, err)
		require.Equal(t, 2, recovered)
		for _, orderID := range []int64{first.ID, second.ID} {
			current, err := fixture.client.PaymentOrder.Get(ctx, orderID)
			require.NoError(t, err)
			require.Equal(t, service.OrderStatusCompleted, current.Status)
		}
		currentUser, err := fixture.client.User.Get(ctx, user.ID)
		require.NoError(t, err)
		require.InDelta(t, 11, currentUser.Balance, 0.000001)
	})

	t.Run("completion write failure after balance credit retries without duplicate grant", func(t *testing.T) {
		fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
		user := fixture.createUser(0)
		order := fixture.createPaidBalanceOrder(user, 6, service.OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))

		var failCompleted atomic.Bool
		fixture.client.PaymentOrder.Use(func(next dbent.Mutator) dbent.Mutator {
			return dbent.MutateFunc(func(mutateCtx context.Context, mutation dbent.Mutation) (dbent.Value, error) {
				if paymentMutation, ok := mutation.(*dbent.PaymentOrderMutation); ok && mutation.Op().Is(dbent.OpUpdate) {
					if status, exists := paymentMutation.Status(); exists && status == service.OrderStatusCompleted && failCompleted.CompareAndSwap(false, true) {
						return nil, errors.New("injected balance completion persistence failure")
					}
				}
				return next.Mutate(mutateCtx, mutation)
			})
		})

		recovered, err := fixture.paymentService().RecoverPendingPaymentOrderFulfillments(ctx)
		require.Error(t, err)
		require.Zero(t, recovered)
		require.True(t, failCompleted.Load())
		failed, err := fixture.client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, service.OrderStatusFailed, failed.Status)
		currentUser, err := fixture.client.User.Get(ctx, user.ID)
		require.NoError(t, err)
		require.InDelta(t, 6, currentUser.Balance, 0.000001)

		_, err = fixture.client.PaymentOrder.UpdateOneID(order.ID).SetUpdatedAt(time.Now().UTC().Add(-2 * time.Minute)).Save(ctx)
		require.NoError(t, err)
		recovered, err = fixture.paymentService().RecoverPendingPaymentOrderFulfillments(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, recovered)
		completed, err := fixture.client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, service.OrderStatusCompleted, completed.Status)
		currentUser, err = fixture.client.User.Get(ctx, user.ID)
		require.NoError(t, err)
		require.InDelta(t, 6, currentUser.Balance, 0.000001)
	})
}

func TestPaymentFulfillmentRecoveryPostgresSubscriptionConvergenceAndCommitBoundaries(t *testing.T) {
	ctx := context.Background()

	t.Run("simultaneous same group orders preserve both terms", func(t *testing.T) {
		fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
		user := fixture.createUser(0)
		group := fixture.createSubscriptionGroup()
		started := time.Now().UTC()
		first := fixture.createPaidSubscriptionOrder(user, group.ID, 30, service.OrderStatusPaid, started.Add(-3*time.Minute))
		second := fixture.createPaidSubscriptionOrder(user, group.ID, 30, service.OrderStatusPaid, started.Add(-2*time.Minute))

		createArrived := make(chan struct{}, 2)
		releaseCreates := make(chan struct{})
		var creates atomic.Int32
		fixture.client.UserSubscription.Use(func(next dbent.Mutator) dbent.Mutator {
			return dbent.MutateFunc(func(mutateCtx context.Context, mutation dbent.Mutation) (dbent.Value, error) {
				if mutation.Op().Is(dbent.OpCreate) && creates.Add(1) <= 2 {
					createArrived <- struct{}{}
					<-releaseCreates
				}
				return next.Mutate(mutateCtx, mutation)
			})
		})

		type result struct {
			recovered int
			err       error
		}
		resultCh := make(chan result, 1)
		go func() {
			recovered, err := fixture.paymentService().RecoverPendingPaymentOrderFulfillments(ctx)
			resultCh <- result{recovered, err}
		}()
		for i := 0; i < 2; i++ {
			select {
			case <-createArrived:
			case <-time.After(5 * time.Second):
				t.Fatal("simultaneous subscription workers did not both reach creation")
			}
		}
		close(releaseCreates)
		firstPass := <-resultCh
		// One concurrent insert may lose the active unique index. That failure is
		// durable and must be retried by the next scan, rather than dropping its
		// subscription term.
		require.Error(t, firstPass.err)

		failed, err := fixture.client.PaymentOrder.Query().Where(paymentorder.StatusEQ(service.OrderStatusFailed)).All(ctx)
		require.NoError(t, err)
		require.Len(t, failed, 1)
		_, err = fixture.client.PaymentOrder.UpdateOneID(failed[0].ID).
			SetUpdatedAt(time.Now().UTC().Add(-2 * time.Minute)).
			Save(ctx)
		require.NoError(t, err)

		recovered, err := fixture.paymentService().RecoverPendingPaymentOrderFulfillments(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, recovered)
		for _, orderID := range []int64{first.ID, second.ID} {
			current, err := fixture.client.PaymentOrder.Get(ctx, orderID)
			require.NoError(t, err)
			require.Equal(t, service.OrderStatusCompleted, current.Status)
		}
		subscription, err := fixture.client.UserSubscription.Query().
			Where(usersubscription.UserIDEQ(user.ID), usersubscription.GroupIDEQ(group.ID)).
			Only(ctx)
		require.NoError(t, err)
		require.True(t, subscription.ExpiresAt.After(started.Add(59*24*time.Hour)))
		require.True(t, subscription.ExpiresAt.Before(started.Add(61*24*time.Hour)), "each paid order must grant exactly one 30-day term")
	})

	t.Run("failure before entitlement commit remains retryable", func(t *testing.T) {
		fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
		user := fixture.createUser(0)
		group := fixture.createSubscriptionGroup()
		_, err := fixture.client.Group.UpdateOneID(group.ID).SetStatus(service.StatusDisabled).Save(ctx)
		require.NoError(t, err)
		order := fixture.createPaidSubscriptionOrder(user, group.ID, 30, service.OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))

		recovered, err := fixture.paymentService().RecoverPendingPaymentOrderFulfillments(ctx)
		require.Error(t, err)
		require.Zero(t, recovered)
		current, err := fixture.client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, service.OrderStatusFailed, current.Status)
		count, err := fixture.client.UserSubscription.Query().
			Where(usersubscription.UserIDEQ(user.ID), usersubscription.GroupIDEQ(group.ID)).
			Count(ctx)
		require.NoError(t, err)
		require.Zero(t, count)

		_, err = fixture.client.Group.UpdateOneID(group.ID).SetStatus(service.StatusActive).Save(ctx)
		require.NoError(t, err)
		_, err = fixture.client.PaymentOrder.UpdateOneID(order.ID).SetUpdatedAt(time.Now().UTC().Add(-2 * time.Minute)).Save(ctx)
		require.NoError(t, err)
		recovered, err = fixture.paymentService().RecoverPendingPaymentOrderFulfillments(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, recovered)
		current, err = fixture.client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, service.OrderStatusCompleted, current.Status)
	})

	t.Run("failure after entitlement commit does not grant twice", func(t *testing.T) {
		fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
		user := fixture.createUser(0)
		group := fixture.createSubscriptionGroup()
		order := fixture.createPaidSubscriptionOrder(user, group.ID, 30, service.OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))

		var failCompleted atomic.Bool
		fixture.client.PaymentOrder.Use(func(next dbent.Mutator) dbent.Mutator {
			return dbent.MutateFunc(func(mutateCtx context.Context, mutation dbent.Mutation) (dbent.Value, error) {
				if paymentMutation, ok := mutation.(*dbent.PaymentOrderMutation); ok && mutation.Op().Is(dbent.OpUpdate) {
					if status, exists := paymentMutation.Status(); exists && status == service.OrderStatusCompleted && failCompleted.CompareAndSwap(false, true) {
						return nil, errors.New("injected completion persistence failure")
					}
				}
				return next.Mutate(mutateCtx, mutation)
			})
		})

		recovered, err := fixture.paymentService().RecoverPendingPaymentOrderFulfillments(ctx)
		require.Error(t, err)
		require.Zero(t, recovered)
		require.True(t, failCompleted.Load())
		failed, err := fixture.client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, service.OrderStatusFailed, failed.Status)
		firstSubscription, err := fixture.client.UserSubscription.Query().
			Where(usersubscription.UserIDEQ(user.ID), usersubscription.GroupIDEQ(group.ID)).
			Only(ctx)
		require.NoError(t, err)
		firstExpiry := firstSubscription.ExpiresAt

		_, err = fixture.client.PaymentOrder.UpdateOneID(order.ID).SetUpdatedAt(time.Now().UTC().Add(-2 * time.Minute)).Save(ctx)
		require.NoError(t, err)
		recovered, err = fixture.paymentService().RecoverPendingPaymentOrderFulfillments(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, recovered)
		completed, err := fixture.client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, service.OrderStatusCompleted, completed.Status)
		secondSubscription, err := fixture.client.UserSubscription.Query().
			Where(usersubscription.UserIDEQ(user.ID), usersubscription.GroupIDEQ(group.ID)).
			Only(ctx)
		require.NoError(t, err)
		require.True(t, secondSubscription.ExpiresAt.Equal(firstExpiry))
	})
}

func TestPaymentFulfillmentRecoveryPostgresRefundFenceFairness(t *testing.T) {
	ctx := context.Background()
	fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
	user := fixture.createUser(0)
	for i := 0; i < 100; i++ {
		order := fixture.createPaidBalanceOrder(user, 1, service.OrderStatusPaid, time.Now().UTC().Add(-3*time.Minute-time.Duration(i)*time.Microsecond))
		_, err := integrationDB.ExecContext(ctx, `
			INSERT INTO unified_payment_refund_events (id, order_id, action, detail)
			VALUES ($1, $2, 'UNIFIED_REFUND_UNCORRELATED', '{}')`, uuid.NewString(), order.ID)
		require.NoError(t, err)
	}
	eligible := fixture.createPaidBalanceOrder(user, 9, service.OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))

	recovered, err := fixture.paymentService().RecoverPendingPaymentOrderFulfillments(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	current, err := fixture.client.PaymentOrder.Get(ctx, eligible.ID)
	require.NoError(t, err)
	require.Equal(t, service.OrderStatusCompleted, current.Status)
	currentUser, err := fixture.client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.InDelta(t, 9, currentUser.Balance, 0.000001)
}

func TestPaymentFulfillmentRecoveryPostgresRefundFenceClaimBoundary(t *testing.T) {
	t.Run("committed manual review fence before claim blocks automatic fulfillment", func(t *testing.T) {
		ctx := context.Background()
		fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
		user := fixture.createUser(0)
		order := fixture.createPaidBalanceOrder(user, 12, service.OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))
		originalUpdatedAt := order.UpdatedAt

		// Query 1 is the bounded candidate selection. Query 2 is the recovery
		// executor's first reload. Historically the nonlocking refund-fence read
		// had already run before query 2, while the lease claim still had not.
		// Hold that exact old race seam, commit a fence under the refund writer's
		// payment-order row lock, and then allow recovery to reach its claim.
		recoveryBeforeClaim := make(chan struct{})
		releaseRecovery := make(chan struct{})
		defer func() {
			select {
			case <-releaseRecovery:
			default:
				close(releaseRecovery)
			}
		}()
		var queries atomic.Int32
		fixture.client.PaymentOrder.Intercept(dbent.InterceptFunc(func(next dbent.Querier) dbent.Querier {
			return dbent.QuerierFunc(func(queryCtx context.Context, query dbent.Query) (dbent.Value, error) {
				if queries.Add(1) == 2 {
					close(recoveryBeforeClaim)
					<-releaseRecovery
				}
				return next.Query(queryCtx, query)
			})
		}))

		type recoveryResult struct {
			recovered int
			err       error
		}
		resultCh := make(chan recoveryResult, 1)
		go func() {
			recovered, err := fixture.paymentService().RecoverPendingPaymentOrderFulfillments(ctx)
			resultCh <- recoveryResult{recovered: recovered, err: err}
		}()
		select {
		case <-recoveryBeforeClaim:
		case <-time.After(5 * time.Second):
			t.Fatal("recovery did not reach the pre-claim race seam")
		}

		refundTx, err := integrationDB.BeginTx(ctx, nil)
		require.NoError(t, err)
		rows, err := refundTx.QueryContext(ctx, `SELECT id FROM payment_orders WHERE id = $1 FOR UPDATE`, order.ID)
		require.NoError(t, err)
		require.True(t, rows.Next())
		var lockedID int64
		require.NoError(t, rows.Scan(&lockedID))
		require.Equal(t, order.ID, lockedID)
		require.NoError(t, rows.Close())
		_, err = refundTx.ExecContext(ctx, `
			INSERT INTO unified_payment_refund_events (id, order_id, action, detail)
			VALUES ($1, $2, 'UNIFIED_REFUND_UNCORRELATED', '{}')`, uuid.NewString(), order.ID)
		require.NoError(t, err)
		require.NoError(t, refundTx.Commit())

		close(releaseRecovery)
		result := <-resultCh
		require.NoError(t, result.err)
		require.Zero(t, result.recovered)

		current, err := fixture.client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, service.OrderStatusPaid, current.Status)
		// PostgreSQL stores TIMESTAMPTZ at microsecond precision. Ent returns the
		// caller's full-precision value from Create, so normalize both sides before
		// asserting that the fenced recovery path did not mutate the row.
		require.Equal(t, originalUpdatedAt.UTC().Truncate(time.Microsecond), current.UpdatedAt.UTC().Truncate(time.Microsecond))
		require.Nil(t, current.CompletedAt)
		currentUser, err := fixture.client.User.Get(ctx, user.ID)
		require.NoError(t, err)
		require.InDelta(t, 0, currentUser.Balance, 0.000001)
		var fenceCount int
		require.NoError(t, integrationDB.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM unified_payment_refund_events
			WHERE order_id = $1 AND action = 'UNIFIED_REFUND_UNCORRELATED'`, order.ID).Scan(&fenceCount))
		require.Equal(t, 1, fenceCount)
	})

	t.Run("canceled recovery claim rolls back its locked transaction", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
		user := fixture.createUser(0)
		order := fixture.createPaidBalanceOrder(user, 5, service.OrderStatusPaid, time.Now().UTC().Add(-2*time.Minute))

		// The fourth payment-order query is lockUnifiedRefundOrder inside the
		// recovery claim transaction: selection, dispatch reload, executor
		// reload, then SELECT ... FOR UPDATE. Pause after the lock is acquired,
		// cancel, and verify a new pass can subsequently acquire and fulfill.
		claimLockHeld := make(chan struct{})
		releaseClaimLock := make(chan struct{})
		defer func() {
			select {
			case <-releaseClaimLock:
			default:
				close(releaseClaimLock)
			}
		}()
		var queries atomic.Int32
		fixture.client.PaymentOrder.Intercept(dbent.InterceptFunc(func(next dbent.Querier) dbent.Querier {
			return dbent.QuerierFunc(func(queryCtx context.Context, query dbent.Query) (dbent.Value, error) {
				value, err := next.Query(queryCtx, query)
				if queries.Add(1) == 4 && err == nil {
					close(claimLockHeld)
					<-releaseClaimLock
				}
				return value, err
			})
		}))

		type recoveryResult struct {
			recovered int
			err       error
		}
		resultCh := make(chan recoveryResult, 1)
		go func() {
			recovered, err := fixture.paymentService().RecoverPendingPaymentOrderFulfillments(ctx)
			resultCh <- recoveryResult{recovered: recovered, err: err}
		}()
		select {
		case <-claimLockHeld:
		case <-time.After(5 * time.Second):
			t.Fatal("recovery did not acquire the transactional order lock")
		}
		cancel()
		close(releaseClaimLock)
		result := <-resultCh
		require.ErrorIs(t, result.err, context.Canceled)
		require.Zero(t, result.recovered)

		// A fresh context can claim the same paid order. This proves the canceled
		// short transaction released its row lock and did not retain a lease.
		recovered, err := fixture.paymentService().RecoverPendingPaymentOrderFulfillments(context.Background())
		require.NoError(t, err)
		require.Equal(t, 1, recovered)
		current, err := fixture.client.PaymentOrder.Get(context.Background(), order.ID)
		require.NoError(t, err)
		require.Equal(t, service.OrderStatusCompleted, current.Status)
		currentUser, err := fixture.client.User.Get(context.Background(), user.ID)
		require.NoError(t, err)
		require.InDelta(t, 5, currentUser.Balance, 0.000001)
	})
}
