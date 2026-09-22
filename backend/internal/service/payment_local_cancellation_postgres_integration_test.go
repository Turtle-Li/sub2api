//go:build integration

package service

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const localCancellationPostgresTestDatabase = "sub2api_local_cancellation_test"

type localCancellationPostgresOrderInput struct {
	PayAmount        float64
	Status           string
	OrderType        string
	ProviderKey      string
	ProviderSnapshot map[string]any
	ProductSnapshot  map[string]any
	PaymentTradeNo   string
	PaidAt           *time.Time
}

func newLocalCancellationPostgresFixture(t *testing.T) (*dbent.Client, *sql.DB, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase(localCancellationPostgresTestDatabase),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Logf("local cancellation PostgreSQL container=%s database=%s image=postgres:18-alpine", container.GetContainerID(), localCancellationPostgresTestDatabase)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, applyAllMigrationsForRefundBenefitPGTest(ctx, db))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return client, db, ctx
}

func createLocalCancellationPostgresUser(t *testing.T, ctx context.Context, client *dbent.Client, suffix string) *dbent.User {
	t.Helper()
	identity := fmt.Sprintf("local-cancel-pg-%s-%s", suffix, uuid.NewString())
	user, err := client.User.Create().
		SetEmail(identity + "@example.test").
		SetPasswordHash("test-only").
		SetUsername(identity).
		SetBalance(17).
		Save(ctx)
	require.NoError(t, err)
	return user
}

func createLocalCancellationPostgresOrder(t *testing.T, ctx context.Context, client *dbent.Client, user *dbent.User, input localCancellationPostgresOrderInput) *dbent.PaymentOrder {
	t.Helper()
	identity := uuid.NewString()
	if input.PayAmount <= 0 {
		input.PayAmount = 1
	}
	if input.Status == "" {
		input.Status = OrderStatusPending
	}
	if input.OrderType == "" {
		input.OrderType = payment.OrderTypeBalance
	}
	builder := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(input.PayAmount).
		SetFeeRate(0).
		SetRechargeCode("LCPG-" + identity[:18]).
		SetOutTradeNo("sub2_local_cancel_pg_" + identity[:18]).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo(input.PaymentTradeNo).
		SetOrderType(input.OrderType).
		SetStatus(input.Status).
		SetExpiresAt(time.Now().UTC().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("local-cancellation.integration.test")
	if input.ProviderKey != "" {
		builder.SetProviderKey(input.ProviderKey)
	}
	if input.ProviderSnapshot != nil {
		builder.SetProviderSnapshot(input.ProviderSnapshot)
	}
	if input.ProductSnapshot != nil {
		builder.SetProductSnapshot(input.ProductSnapshot)
	}
	if input.PaidAt != nil {
		builder.SetPaidAt(*input.PaidAt)
	}
	order, err := builder.Save(ctx)
	require.NoError(t, err)
	return order
}

func localCancellationUnifiedProviderSnapshot() map[string]any {
	return map[string]any{
		"schema_version":   2,
		"provider_key":     payment.TypeUnifiedPay,
		"payment_order_id": uuid.NewString(),
		"environment":      "sandbox",
		"organization_id":  "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"product_id":       "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"app_id":           "app.sub2.sandbox",
		"currency":         payment.DefaultPaymentCurrency,
	}
}

type localCancellationUnifiedRefundRequest struct {
	PaymentOrderID  string  `json:"payment_order_id"`
	ProductRefundNo string  `json:"product_refund_no"`
	AmountFen       int64   `json:"amount_fen"`
	ReasonCode      string  `json:"reason_code"`
	ReasonSummary   *string `json:"reason_summary"`
}

// localCancellationWebhookInbox exercises the real signature and webhook
// processing path while making the duplicate-delivery boundary explicit in
// this focused service integration test. PostgreSQL owns all order and refund
// persistence below; dedicated repository tests cover inbox storage itself.
type localCancellationWebhookInbox struct {
	mu        sync.Mutex
	seen      map[string]struct{}
	claims    int
	processed int
}

func (i *localCancellationWebhookInbox) Claim(_ context.Context, record UnifiedWebhookInboxRecord, _ time.Duration) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.claims++
	if i.seen == nil {
		i.seen = make(map[string]struct{})
	}
	if _, exists := i.seen[record.EventID]; exists {
		return UnifiedWebhookClaimDuplicate, nil
	}
	i.seen[record.EventID] = struct{}{}
	return UnifiedWebhookClaimNew, nil
}

func (i *localCancellationWebhookInbox) MarkProcessed(context.Context, string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.processed++
	return nil
}

func (*localCancellationWebhookInbox) MarkRetryableFailure(context.Context, string, string) error {
	return nil
}
func (*localCancellationWebhookInbox) MarkRejected(context.Context, string, string) error { return nil }

func (i *localCancellationWebhookInbox) snapshot() (claims, processed int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.claims, i.processed
}

func localCancellationSignedWebhook(t *testing.T, event unifiedpay.WebhookEvent) (http.Header, []byte) {
	t.Helper()
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	timestamp := time.Now().UTC().Unix()
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set(unifiedpay.HeaderWebhookKeyID, "sub2.webhook.sandbox.v1")
	headers.Set(unifiedpay.HeaderWebhookTimestamp, strconv.FormatInt(timestamp, 10))
	headers.Set(unifiedpay.HeaderWebhookEventID, event.EventID)
	headers.Set(unifiedpay.HeaderWebhookSignature, base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(
		unifiedpay.WebhookSignaturePayload("sub2.webhook.sandbox.v1", timestamp, event.EventID, raw),
	))))
	return headers, raw
}

func reserveLocalCancellationCoupon(t *testing.T, ctx context.Context, client *dbent.Client, svc *PaymentService, user *dbent.User, order *dbent.PaymentOrder) {
	t.Helper()
	perUserMaxUses := 1
	code, err := svc.SavePaymentDiscountCode(ctx, user.ID, 0, PaymentDiscountCodeInput{
		Code:           fmt.Sprintf("LCPG%08d", order.ID),
		DiscountType:   paymentDiscountTypeFixed,
		DiscountValue:  "20.00",
		Currency:       payment.DefaultPaymentCurrency,
		MaxUses:        1,
		PerUserMaxUses: &perUserMaxUses,
		StartsAt:       time.Now().UTC().Add(-time.Hour),
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
		Enabled:        true,
	}, 0)
	require.NoError(t, err)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	txCtx := dbent.NewTxContext(ctx, tx)
	quote, err := quotePaymentDiscount(txCtx, tx.Client(), user.ID, code.Code, decimal.NewFromInt(100), payment.DefaultPaymentCurrency, payment.OrderTypeBalance, 0, "local-cancellation-postgres", true)
	require.NoError(t, err)
	require.Equal(t, "80.00", quote.PayAmount)
	require.NoError(t, reservePaymentDiscount(txCtx, tx.Client(), order.ID, user.ID, quote, fmt.Sprintf("%064x", order.ID), fmt.Sprintf("%064x", order.ID+1)))
	require.NoError(t, tx.Commit())
}

func installLocalCancellationPauseTrigger(t *testing.T, ctx context.Context, db *sql.DB, status string, advisoryKey int64) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		CREATE OR REPLACE FUNCTION local_cancellation_test_pause()
		RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.status = TG_ARGV[0] AND OLD.status = 'PENDING' THEN
				PERFORM pg_advisory_xact_lock(TG_ARGV[1]::BIGINT);
			END IF;
			RETURN NEW;
		END;
		$$`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `DROP TRIGGER IF EXISTS local_cancellation_test_pause ON payment_orders`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TRIGGER local_cancellation_test_pause
		BEFORE UPDATE OF status ON payment_orders
		FOR EACH ROW EXECUTE FUNCTION local_cancellation_test_pause('%s', '%d')`, status, advisoryKey))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS local_cancellation_test_pause ON payment_orders`)
		_, _ = db.ExecContext(context.Background(), `DROP FUNCTION IF EXISTS local_cancellation_test_pause()`)
	})
}

func holdLocalCancellationAdvisoryLock(t *testing.T, ctx context.Context, db *sql.DB, advisoryKey int64) func() {
	t.Helper()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey)
	require.NoError(t, err)
	var once sync.Once
	release := func() {
		once.Do(func() {
			_, releaseErr := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey)
			require.NoError(t, releaseErr)
			require.NoError(t, conn.Close())
		})
	}
	t.Cleanup(release)
	return release
}

func waitForLocalCancellationLockWaiters(t *testing.T, ctx context.Context, db *sql.DB, wantAtLeast int) {
	t.Helper()
	require.Eventually(t, func() bool {
		var waiting int
		err := db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting)
		return err == nil && waiting >= wantAtLeast
	}, 5*time.Second, 20*time.Millisecond)
}

func TestLocalCancellationPostgresRowLockLifecycle(t *testing.T) {
	client, db, ctx := newLocalCancellationPostgresFixture(t)

	t.Run("cancel first releases coupon and queues exact late unified refund", func(t *testing.T) {
		var gatewayCalls atomic.Int32
		const unifiedRefundRequestID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
		var dispatchMu sync.Mutex
		var dispatch localCancellationUnifiedRefundRequest
		var dispatchIdempotencyKey string
		gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gatewayCalls.Add(1)
			if r.Method != http.MethodPost || r.URL.Path != "/v1/refund-requests" {
				http.NotFound(w, r)
				return
			}
			var received localCancellationUnifiedRefundRequest
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				http.Error(w, "invalid refund request", http.StatusBadRequest)
				return
			}
			dispatchMu.Lock()
			dispatch = received
			dispatchIdempotencyKey = r.Header.Get(unifiedpay.HeaderIdempotencyKey)
			dispatchMu.Unlock()
			now := time.Now().UTC().Truncate(time.Second)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"environment":           "sandbox",
				"organization_id":       "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
				"product_id":            "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
				"refund_request_id":     unifiedRefundRequestID,
				"payment_order_id":      received.PaymentOrderID,
				"product_refund_no":     received.ProductRefundNo,
				"channel_out_refund_no": "sandbox_late_refund_8000",
				"amount_fen":            received.AmountFen,
				"currency":              payment.DefaultPaymentCurrency,
				"payment_method":        unifiedpay.PaymentMethodAlipay,
				"status":                unifiedpay.RefundStatusApproved,
				"needs_manual_review":   false,
				"created_at":            now,
				"updated_at":            now,
			}))
		}))
		defer gateway.Close()

		user := createLocalCancellationPostgresUser(t, ctx, client, "cancel-first")
		order := createLocalCancellationPostgresOrder(t, ctx, client, user, localCancellationPostgresOrderInput{
			PayAmount:        80,
			ProviderKey:      payment.TypeUnifiedPay,
			ProviderSnapshot: localCancellationUnifiedProviderSnapshot(),
			ProductSnapshot:  map[string]any{"payment_discount": map[string]any{"test": true}},
		})
		inbox := &localCancellationWebhookInbox{}
		svc := &PaymentService{entClient: client}
		svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, gateway.URL), inbox)
		reserveLocalCancellationCoupon(t, ctx, client, svc, user, order)

		const advisoryKey int64 = 8_745_201
		installLocalCancellationPauseTrigger(t, ctx, db, OrderStatusCancelled, advisoryKey)
		releasePause := holdLocalCancellationAdvisoryLock(t, ctx, db, advisoryKey)
		type cancelResult struct {
			result string
			err    error
		}
		cancelDone := make(chan cancelResult, 1)
		go func() {
			result, err := svc.CancelOrder(ctx, order.ID, user.ID)
			cancelDone <- cancelResult{result: result, err: err}
		}()
		waitForLocalCancellationLockWaiters(t, ctx, db, 1)

		paymentDone := make(chan error, 1)
		go func() {
			paymentDone <- svc.HandlePaymentNotification(ctx, &payment.PaymentNotification{
				TradeNo: "late-unified-payment", OrderID: order.OutTradeNo, Amount: 80, Status: payment.NotificationStatusSuccess,
			}, payment.TypeUnifiedPay)
		}()
		waitForLocalCancellationLockWaiters(t, ctx, db, 2)
		releasePause()

		cancelled := <-cancelDone
		require.NoError(t, cancelled.err)
		require.Equal(t, checkPaidResultCancelled, cancelled.result)
		require.NoError(t, <-paymentDone)
		require.Zero(t, gatewayCalls.Load(), "local cancellation and durable late-refund reservation must not call the provider")

		stored, err := client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusCancelled, stored.Status)
		require.NotNil(t, stored.PaidAt)
		require.Equal(t, "late-unified-payment", stored.PaymentTradeNo)
		require.Equal(t, 80.0, stored.PayAmount)

		var couponStatus string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM payment_discount_uses WHERE order_id=$1`, order.ID).Scan(&couponStatus))
		require.Equal(t, paymentDiscountUseReleased, couponStatus)
		admission, err := client.Tx(ctx)
		require.NoError(t, err)
		require.NoError(t, svc.checkSinglePendingOrder(ctx, admission, user.ID), "local cancellation commits before provider work and frees pending-order admission")
		require.NoError(t, admission.Rollback())

		var closeJobs int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_local_cancellation_work WHERE order_id=$1 AND work_kind='CLOSE'`, order.ID).Scan(&closeJobs))
		require.Equal(t, 1, closeJobs)
		result, err := svc.CancelOrder(ctx, order.ID, user.ID)
		require.NoError(t, err)
		require.Equal(t, checkPaidResultCancelled, result)
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_local_cancellation_work WHERE order_id=$1 AND work_kind='CLOSE'`, order.ID).Scan(&closeJobs))
		require.Equal(t, 1, closeJobs, "repeated cancellation must reuse the durable close job")

		attempt, err := loadUnifiedRefundAttempt(ctx, client, order.ID, "sub2_cancel_"+fmt.Sprint(order.ID))
		require.NoError(t, err)
		require.Equal(t, cancelLatePaymentRefundKind, attempt.RefundKind)
		require.Equal(t, int64(8000), attempt.AmountFen)
		require.Zero(t, attempt.BalanceAmountMinor)
		require.False(t, attempt.DeductBalance)
		require.False(t, attempt.EntitlementReserved)

		reservation, err := svc.advanceUnifiedRefund(ctx, attempt)
		require.NoError(t, err)
		require.False(t, reservation.Success, "an approved remote request remains pending until the signed terminal result")
		require.Equal(t, int32(1), gatewayCalls.Load())
		dispatchMu.Lock()
		capturedDispatch, capturedIdempotencyKey := dispatch, dispatchIdempotencyKey
		dispatchMu.Unlock()
		require.Equal(t, attempt.PaymentOrderID, capturedDispatch.PaymentOrderID)
		require.Equal(t, attempt.ProductRefundNo, capturedDispatch.ProductRefundNo)
		require.Equal(t, int64(8000), capturedDispatch.AmountFen)
		require.Equal(t, "service_not_delivered", capturedDispatch.ReasonCode)
		require.NotNil(t, capturedDispatch.ReasonSummary)
		require.Equal(t, "payment accepted after local cancellation", *capturedDispatch.ReasonSummary)
		require.Equal(t, attempt.IdempotencyKey, capturedIdempotencyKey)

		remoteReserved, err := loadUnifiedRefundAttempt(ctx, client, order.ID, attempt.ProductRefundNo)
		require.NoError(t, err)
		require.Equal(t, unifiedRefundPending, remoteReserved.Status)
		require.Equal(t, unifiedRefundRequestID, remoteReserved.RefundRequestID)
		require.Equal(t, "sandbox_late_refund_8000", remoteReserved.ChannelOutRefundNo)

		completedAt := time.Now().UTC().Truncate(time.Second)
		providerRefundID, providerStatus := "mock-provider-refund-8000", "SUCCESS"
		successEvent := unifiedpay.WebhookEvent{
			SchemaVersion:   "payment-webhook.v1",
			EventID:         uuid.NewString(),
			EventType:       unifiedpay.EventRefundSucceeded,
			OccurredAt:      completedAt,
			Environment:     unifiedpay.EnvironmentSandbox,
			OrganizationID:  remoteReserved.OrganizationID,
			ProductID:       remoteReserved.ProductID,
			ProductCode:     "sub2",
			AppID:           remoteReserved.AppID,
			Sequence:        2,
			OriginRequestID: "origin-refund-" + uuid.NewString(),
			Resource: unifiedpay.PaymentOrderResource{
				PaymentOrderID:          remoteReserved.PaymentOrderID,
				ProductOrderNo:          order.OutTradeNo,
				OrderType:               order.OrderType,
				AmountFen:               8000,
				PaidAmountFen:           8000,
				RefundedAmountFen:       8000,
				ReservedRefundAmountFen: 0,
				RefundableAmountFen:     0,
				Currency:                payment.DefaultPaymentCurrency,
				PaymentMethod:           remoteReserved.PaymentMethod,
				Status:                  unifiedpay.StatusRefunded,
				ChannelOutTradeNo:       "sandbox_late_trade_8000",
			},
			Refund: &unifiedpay.WebhookRefundResource{
				RefundRequestID:    unifiedRefundRequestID,
				ProductRefundNo:    remoteReserved.ProductRefundNo,
				ChannelOutRefundNo: remoteReserved.ChannelOutRefundNo,
				AmountFen:          8000,
				PaymentMethod:      remoteReserved.PaymentMethod,
				Status:             unifiedpay.RefundStatusSucceeded,
				ProviderRefundID:   &providerRefundID,
				ProviderStatus:     &providerStatus,
				CompletedAt:        &completedAt,
			},
		}
		headers, rawWebhook := localCancellationSignedWebhook(t, successEvent)
		require.NoError(t, svc.HandleUnifiedPaymentWebhook(ctx, headers, rawWebhook))
		require.NoError(t, svc.HandleUnifiedPaymentWebhook(ctx, headers, rawWebhook), "the same terminal event must be a durable no-op on replay")
		require.Equal(t, int32(1), gatewayCalls.Load(), "webhook settlement and replay must never create another refund request")
		claims, processed := inbox.snapshot()
		require.Equal(t, 2, claims)
		require.Equal(t, 1, processed)

		settled, err := loadUnifiedRefundAttempt(ctx, client, order.ID, attempt.ProductRefundNo)
		require.NoError(t, err)
		require.Equal(t, unifiedpay.RefundStatusSucceeded, settled.Status)
		require.Equal(t, unifiedRefundRequestID, settled.RefundRequestID)
		require.Equal(t, providerRefundID, settled.ProviderRefundID)
		require.Equal(t, providerStatus, settled.ProviderStatus)
		require.Zero(t, settled.BalanceAmountMinor)
		require.False(t, settled.DeductBalance)
		require.False(t, settled.EntitlementReserved)

		stored, err = client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusCancelled, stored.Status, "late-refund settlement must not recreate a payable or fulfilled order")
		require.NotNil(t, stored.PaidAt)
		storedUser, err := client.User.Get(ctx, user.ID)
		require.NoError(t, err)
		require.Equal(t, 17.0, storedUser.Balance)

		var attempts, successfulAttempts, walletFundings, benefitSources, successfulAudits int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM unified_payment_refund_attempts WHERE order_id=$1`, order.ID).Scan(&attempts))
		require.Equal(t, 1, attempts)
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM unified_payment_refund_attempts WHERE order_id=$1 AND status='SUCCEEDED'`, order.ID).Scan(&successfulAttempts))
		require.Equal(t, 1, successfulAttempts)
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_wallet_fundings WHERE payment_order_id=$1`, order.ID).Scan(&walletFundings))
		require.Zero(t, walletFundings, "a cancelled late payment must not acquire product balance benefits")
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_refund_benefit_sources WHERE payment_order_id=$1`, order.ID).Scan(&benefitSources))
		require.Zero(t, benefitSources)
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM unified_payment_refund_events WHERE order_id=$1 AND action='UNIFIED_CANCEL_LATE_REFUND_SUCCEEDED'`, order.ID).Scan(&successfulAudits))
		require.Equal(t, 1, successfulAudits)
	})

	t.Run("paid first rejects cancellation after the same row lock", func(t *testing.T) {
		user := createLocalCancellationPostgresUser(t, ctx, client, "paid-first")
		order := createLocalCancellationPostgresOrder(t, ctx, client, user, localCancellationPostgresOrderInput{
			PayAmount: 19,
			OrderType: "test_only",
		})
		svc := &PaymentService{entClient: client}

		const advisoryKey int64 = 8_745_202
		installLocalCancellationPauseTrigger(t, ctx, db, OrderStatusPaid, advisoryKey)
		releasePause := holdLocalCancellationAdvisoryLock(t, ctx, db, advisoryKey)
		paidDone := make(chan error, 1)
		go func() {
			paidDone <- svc.toPaid(ctx, order, "paid-first-payment", 19, payment.TypeAlipay)
		}()
		waitForLocalCancellationLockWaiters(t, ctx, db, 1)
		cancelDone := make(chan struct {
			result string
			err    error
		}, 1)
		go func() {
			result, err := svc.CancelOrder(ctx, order.ID, user.ID)
			cancelDone <- struct {
				result string
				err    error
			}{result: result, err: err}
		}()
		waitForLocalCancellationLockWaiters(t, ctx, db, 2)
		releasePause()

		require.Error(t, <-paidDone, "the deliberately unfulfillable test order still commits the trusted paid claim before fulfillment is attempted")
		cancelled := <-cancelDone
		require.NoError(t, cancelled.err)
		require.Equal(t, checkPaidResultAlreadyPaid, cancelled.result)
		stored, err := client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusPaid, stored.Status)
		require.NotNil(t, stored.PaidAt)
		var closeJobs int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_local_cancellation_work WHERE order_id=$1`, order.ID).Scan(&closeJobs))
		require.Zero(t, closeJobs)
	})
}

type localCancellationPostgresRefundProvider struct {
	mu                 sync.Mutex
	refunds            []payment.RefundRequest
	queries            []payment.RefundQueryRequest
	firstRefundStarted chan struct{}
	releaseFirstRefund chan struct{}
}

func (p *localCancellationPostgresRefundProvider) Name() string {
	return "local-cancellation-postgres-refund"
}

func (p *localCancellationPostgresRefundProvider) ProviderKey() string { return payment.TypeAlipay }

func (p *localCancellationPostgresRefundProvider) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeAlipay}
}

func (*localCancellationPostgresRefundProvider) CreatePayment(context.Context, payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	return nil, errors.New("unexpected provider create")
}

func (*localCancellationPostgresRefundProvider) QueryOrder(context.Context, string) (*payment.QueryOrderResponse, error) {
	return nil, errors.New("unexpected provider order query")
}

func (*localCancellationPostgresRefundProvider) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	return nil, errors.New("unexpected provider notification verification")
}

func (p *localCancellationPostgresRefundProvider) QueryRefund(_ context.Context, request payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	p.mu.Lock()
	p.queries = append(p.queries, request)
	p.mu.Unlock()
	return nil, nil
}

func (p *localCancellationPostgresRefundProvider) Refund(ctx context.Context, request payment.RefundRequest) (*payment.RefundResponse, error) {
	p.mu.Lock()
	p.refunds = append(p.refunds, request)
	call := len(p.refunds)
	p.mu.Unlock()
	if call == 1 {
		select {
		case p.firstRefundStarted <- struct{}{}:
		default:
		}
		select {
		case <-p.releaseFirstRefund:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &payment.RefundResponse{RefundID: fmt.Sprintf("local-cancel-refund-%d", call), Status: payment.ProviderStatusSuccess}, nil
}

func (p *localCancellationPostgresRefundProvider) refundRequests() []payment.RefundRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	copyOf := make([]payment.RefundRequest, len(p.refunds))
	copy(copyOf, p.refunds)
	return copyOf
}

func TestLocalCancellationPostgresWorkerReclaimKeepsStableDirectRefund(t *testing.T) {
	client, db, ctx := newLocalCancellationPostgresFixture(t)
	t.Setenv(runtimegate.StateFileEnv, "")
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })

	user := createLocalCancellationPostgresUser(t, ctx, client, "worker")
	paidAt := time.Now().UTC().Add(-time.Minute)
	order := createLocalCancellationPostgresOrder(t, ctx, client, user, localCancellationPostgresOrderInput{
		PayAmount:      12.34,
		Status:         OrderStatusCancelled,
		PaymentTradeNo: "late-direct-payment",
		PaidAt:         &paidAt,
	})
	_, err := db.ExecContext(ctx, `
		INSERT INTO payment_local_cancellation_work
		(order_id, work_kind, status, provider_key, payment_trade_no, amount_fen, currency, idempotency_key)
		VALUES ($1, 'DIRECT_REFUND', 'PENDING', $2, $3, $4, 'CNY', $5)`,
		order.ID, payment.TypeAlipay, order.PaymentTradeNo, 1234, localCancellationDirectRefundIdempotencyKey(order.ID))
	require.NoError(t, err)

	provider := &localCancellationPostgresRefundProvider{
		firstRefundStarted: make(chan struct{}, 1),
		releaseFirstRefund: make(chan struct{}),
	}
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := &PaymentService{entClient: client, registry: registry, providersLoaded: true}

	type workerResult struct {
		processed int
		err       error
	}
	const workerID = "local-cancellation-worker"
	firstDone := make(chan workerResult, 1)
	go func() {
		processed, workerErr := svc.ReconcileLocalCancellationWork(ctx, workerID)
		firstDone <- workerResult{processed: processed, err: workerErr}
	}()
	select {
	case <-provider.firstRefundStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first worker did not reach the direct refund mock")
	}

	_, err = db.ExecContext(ctx, `
		UPDATE payment_local_cancellation_work
		SET claimed_at = NOW() - INTERVAL '2 minutes'
		WHERE order_id = $1 AND work_kind = 'DIRECT_REFUND'`, order.ID)
	require.NoError(t, err)
	processed, err := svc.ReconcileLocalCancellationWork(ctx, workerID)
	require.NoError(t, err)
	require.Equal(t, 1, processed, "the same worker identity must reclaim the stale lease with a new claimed_at value")
	close(provider.releaseFirstRefund)
	first := <-firstDone
	require.Zero(t, first.processed)
	require.ErrorContains(t, first.err, "lease lost", "the stale same-worker claimed_at value must not complete the reclaimed work")

	requests := provider.refundRequests()
	require.Len(t, requests, 2)
	for _, request := range requests {
		require.Equal(t, localCancellationDirectRefundReference(order.ID), request.RefundReference)
		require.Equal(t, "12.34", request.Amount)
		require.Equal(t, order.PaymentTradeNo, request.TradeNo)
	}
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCancelled, stored.Status)
	require.NotNil(t, stored.PaidAt)
	var status, providerRefundID string
	var attempts, walletFundings int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT status, provider_refund_id
		FROM payment_local_cancellation_work
		WHERE order_id=$1 AND work_kind='DIRECT_REFUND'`, order.ID).Scan(&status, &providerRefundID))
	require.Equal(t, localCancellationWorkCompleted, status)
	require.Equal(t, "local-cancel-refund-2", providerRefundID)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM unified_payment_refund_attempts WHERE order_id=$1`, order.ID).Scan(&attempts))
	require.Zero(t, attempts, "direct recovery must not create a unified refund attempt")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_wallet_fundings WHERE payment_order_id=$1`, order.ID).Scan(&walletFundings))
	require.Zero(t, walletFundings, "retrying a late direct refund must not create payment benefits")
}
