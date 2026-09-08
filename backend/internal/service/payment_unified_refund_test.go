package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

type unifiedRefundTestUsers struct {
	UserRepository
	client      *dbent.Client
	afterDeduct func() error
}

func (r *unifiedRefundTestUsers) GetByID(ctx context.Context, id int64) (*User, error) {
	u, err := r.client.User.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &User{ID: u.ID, Balance: u.Balance}, nil
}

func (r *unifiedRefundTestUsers) DeductAvailableBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return 0, errors.New("refund balance deduction must share the transaction")
	}
	u, err := tx.User.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	deducted := math.Max(0, math.Min(amount, u.Balance))
	if _, err := tx.User.UpdateOneID(id).AddBalance(-deducted).Save(ctx); err != nil {
		return 0, err
	}
	if r.afterDeduct != nil {
		if err := r.afterDeduct(); err != nil {
			return 0, err
		}
	}
	return deducted, nil
}

func newUnifiedRefundSQLiteClient(t *testing.T) *dbent.Client {
	t.Helper()
	db, err := sql.Open("sqlite", "file:unified_refund_"+uuid.NewString()+"?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	installUnifiedRefundTestMigration(t, client, true)
	return client
}

func installUnifiedRefundTestMigration(t *testing.T, client *dbent.Client, sqlite bool) {
	t.Helper()
	content, err := migrations.FS.ReadFile("237_unified_payment_refund_attempts.sql")
	require.NoError(t, err)
	ddl := string(content)
	if sqlite {
		ddl = strings.Split(ddl, "COMMENT ON TABLE")[0]
	}
	_, err = client.ExecContext(context.Background(), ddl)
	require.NoError(t, err)
}

func newUnifiedRefundFixture(t *testing.T, client *dbent.Client, method string) (*PaymentService, *dbent.PaymentOrder, *RefundPlan) {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewString()
	u, err := client.User.Create().SetEmail(id + "@example.test").SetPasswordHash("test-only").SetUsername("refund-test").SetBalance(10).Save(ctx)
	require.NoError(t, err)
	o, err := client.PaymentOrder.Create().SetUserID(u.ID).SetUserEmail(u.Email).SetUserName(u.Username).
		SetAmount(10).SetPayAmount(10.23).SetFeeRate(2.3).SetRechargeCode(id).SetOutTradeNo("sub2_" + id).
		SetPaymentType(method).SetPaymentTradeNo("channel_transaction_test").SetProviderKey(payment.TypeUnifiedPay).
		SetProviderSnapshot(map[string]any{"schema_version": 2, "provider_key": payment.TypeUnifiedPay,
			"payment_order_id": uuid.NewString(), "environment": "sandbox", "organization_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			"product_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "app_id": "app.sub2.sandbox"}).
		SetOrderType(payment.OrderTypeBalance).SetStatus(OrderStatusCompleted).SetPaidAt(time.Now()).SetCompletedAt(time.Now()).
		SetExpiresAt(time.Now().Add(time.Hour)).SetClientIP("127.0.0.1").SetSrcHost("localhost").Save(ctx)
	require.NoError(t, err)
	svc := &PaymentService{entClient: client, userRepo: &unifiedRefundTestUsers{client: client}}
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, "https://pay.example.test"), nil)
	p, early, err := svc.PrepareRefund(ctx, o.ID, 10, "local test refund", false, true)
	require.NoError(t, err)
	require.Nil(t, early)
	return svc, o, p
}

func unifiedRefundFixtureResource(a *unifiedRefundAttempt, status string) *payment.UnifiedRefundResource {
	return &payment.UnifiedRefundResource{RefundRequestID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		PaymentOrderID: a.PaymentOrderID, ProductRefundNo: a.ProductRefundNo, ChannelOutRefundNo: "sandbox_sub2_refund_001",
		AmountFen: a.AmountFen, Currency: "CNY", PaymentMethod: a.PaymentMethod, Status: status}
}

func assertUnifiedRefundBalance(t *testing.T, svc *PaymentService, o *dbent.PaymentOrder, status string, balance float64) {
	t.Helper()
	persisted, err := svc.entClient.PaymentOrder.Get(context.Background(), o.ID)
	require.NoError(t, err)
	require.Equal(t, status, persisted.Status)
	u, err := svc.entClient.User.Get(context.Background(), o.UserID)
	require.NoError(t, err)
	require.Equal(t, balance, u.Balance)
}

func TestUnifiedRefundIntegerAmounts(t *testing.T) {
	for _, tc := range []struct {
		amount, paid, refund float64
		want                 int64
	}{
		{10, 10.23, 10, 1023}, {10, 10.23, 5, 512}, {.01, .01, .01, 1},
	} {
		minor, fen, err := unifiedRefundAmounts(&dbent.PaymentOrder{Amount: tc.amount, PayAmount: tc.paid}, tc.refund)
		require.NoError(t, err)
		require.Positive(t, minor)
		require.Equal(t, tc.want, fen)
	}
	for _, amount := range []float64{math.NaN(), math.Inf(1), .001, 0, -1, 11} {
		_, _, err := unifiedRefundAmounts(&dbent.PaymentOrder{Amount: 10, PayAmount: 10.23}, amount)
		require.Error(t, err)
	}
}

func TestUnifiedRefundRetriesPersistedRequestAfterLostResponse(t *testing.T) {
	svc, o, p := newUnifiedRefundFixture(t, newUnifiedRefundSQLiteClient(t), payment.TypeWxpay)
	var requests []string
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		requests = append(requests, string(body))
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		require.Equal(t, "/v1/refund-requests", r.URL.Path)
		a, err := loadUnifiedRefundAttempt(r.Context(), svc.entClient, o.ID, "")
		require.NoError(t, err, "request is durable before networking")
		assertUnifiedRefundBalance(t, svc, o, OrderStatusRefundPending, 10)
		if len(requests) == 1 {
			w.WriteHeader(503)
			return
		}
		response := map[string]any{"environment": "sandbox", "organization_id": a.OrganizationID, "product_id": a.ProductID,
			"refund_request_id": "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "payment_order_id": a.PaymentOrderID,
			"product_refund_no": a.ProductRefundNo, "channel_out_refund_no": "sandbox_sub2_refund_001", "amount_fen": a.AmountFen,
			"currency": "CNY", "payment_method": "wechat_pay", "status": "APPROVED", "created_at": time.Now(), "updated_at": time.Now()}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}))
	defer server.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
	result, err := svc.ExecuteRefund(context.Background(), p)
	require.NoError(t, err)
	require.False(t, result.Success)
	// A fresh service instance models restart; only database state recovers the request.
	restarted := &PaymentService{entClient: svc.entClient, userRepo: svc.userRepo}
	restarted.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)
	result, err = restarted.QueryAndFinalizeRefund(context.Background(), o.ID)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Len(t, requests, 2)
	require.Equal(t, requests[0], requests[1])
	require.Equal(t, keys[0], keys[1])
	a, err := loadUnifiedRefundAttempt(context.Background(), svc.entClient, o.ID, "")
	require.NoError(t, err)
	require.NotEmpty(t, a.RefundRequestID)
	assertUnifiedRefundBalance(t, svc, o, OrderStatusRefundPending, 10)
}

func TestUnifiedRefundTerminalProcessing(t *testing.T) {
	for _, method := range []string{payment.TypeAlipay, payment.TypeWxpay} {
		t.Run(method, func(t *testing.T) {
			svc, o, p := newUnifiedRefundFixture(t, newUnifiedRefundSQLiteClient(t), method)
			ctx := context.Background()
			a, err := svc.reserveUnifiedRefundAttempt(ctx, p)
			require.NoError(t, err)
			for _, status := range []string{"APPROVED", "PROCESSING", "UNKNOWN"} {
				result, err := svc.applyUnifiedRefundResource(ctx, o.ID, unifiedRefundFixtureResource(a, status), "query")
				require.NoError(t, err)
				require.False(t, result.Success)
				assertUnifiedRefundBalance(t, svc, o, OrderStatusRefundPending, 10)
			}
			for i := 0; i < 2; i++ {
				result, err := svc.applyUnifiedRefundResource(ctx, o.ID, unifiedRefundFixtureResource(a, "SUCCEEDED"), "webhook")
				require.NoError(t, err)
				require.True(t, result.Success)
				assertUnifiedRefundBalance(t, svc, o, OrderStatusRefunded, 0)
			}
			// A delayed create response cannot roll a terminal result back to pending.
			_, err = svc.applyUnifiedRefundResource(ctx, o.ID, unifiedRefundFixtureResource(a, "APPROVED"), "query")
			require.NoError(t, err)
			assertUnifiedRefundBalance(t, svc, o, OrderStatusRefunded, 0)
			count, err := svc.entClient.PaymentAuditLog.Query().Where(paymentauditlog.ActionEQ("REFUND_SUCCESS")).Count(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			_, err = svc.applyUnifiedRefundResource(ctx, o.ID, unifiedRefundFixtureResource(a, "FAILED"), "webhook")
			require.NoError(t, err)
			stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, o.ID, a.ProductRefundNo)
			require.NoError(t, err)
			require.Equal(t, "SUCCEEDED", stored.Status)
			require.True(t, stored.NeedsManualReview)
			assertUnifiedRefundBalance(t, svc, o, OrderStatusRefunded, 0)
		})
	}
}

func TestUnifiedRefundFailureHistoryAndLateConflict(t *testing.T) {
	svc, o, p := newUnifiedRefundFixture(t, newUnifiedRefundSQLiteClient(t), payment.TypeAlipay)
	ctx := context.Background()
	a, err := svc.reserveUnifiedRefundAttempt(ctx, p)
	require.NoError(t, err)
	_, err = svc.applyUnifiedRefundResource(ctx, o.ID, unifiedRefundFixtureResource(a, "FAILED"), "query")
	require.NoError(t, err)
	assertUnifiedRefundBalance(t, svc, o, OrderStatusRefundFailed, 10)
	b, err := svc.reserveUnifiedRefundAttempt(ctx, p)
	require.NoError(t, err)
	require.NotEqual(t, a.ProductRefundNo, b.ProductRefundNo)
	_, err = svc.applyUnifiedRefundResource(ctx, o.ID, unifiedRefundFixtureResource(a, "SUCCEEDED"), "webhook")
	require.NoError(t, err)
	assertUnifiedRefundBalance(t, svc, o, OrderStatusRefundPending, 10)
	_, err = svc.reserveUnifiedRefundAttempt(ctx, p)
	require.Error(t, err)
	result, err := svc.queryUnifiedRefund(ctx, o)
	require.NoError(t, err)
	require.Contains(t, result.Warning, "manual review")
}

func TestUnifiedRefundRejectsCorrelationAndHonorsReview(t *testing.T) {
	for _, name := range []string{"amount", "method", "identity", "manual"} {
		t.Run(name, func(t *testing.T) {
			svc, o, p := newUnifiedRefundFixture(t, newUnifiedRefundSQLiteClient(t), payment.TypeAlipay)
			ctx := context.Background()
			a, err := svc.reserveUnifiedRefundAttempt(ctx, p)
			require.NoError(t, err)
			_, err = svc.applyUnifiedRefundResource(ctx, o.ID, unifiedRefundFixtureResource(a, "APPROVED"), "query")
			require.NoError(t, err)
			r := unifiedRefundFixtureResource(a, "SUCCEEDED")
			switch name {
			case "amount":
				r.AmountFen++
			case "method":
				r.PaymentMethod = "wechat_pay"
			case "identity":
				r.RefundRequestID = uuid.NewString()
			case "manual":
				r.NeedsManualReview = true
			}
			result, err := svc.applyUnifiedRefundResource(ctx, o.ID, r, "webhook")
			require.NoError(t, err)
			require.False(t, result.Success)
			assertUnifiedRefundBalance(t, svc, o, OrderStatusRefundPending, 10)
			stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, o.ID, a.ProductRefundNo)
			require.NoError(t, err)
			require.True(t, stored.NeedsManualReview)
		})
	}
}

func TestUnifiedRefundFinalizationRollbackAndDeductionChoice(t *testing.T) {
	for _, name := range []string{"deduction failure", "no deduction", "shortfall"} {
		t.Run(name, func(t *testing.T) {
			svc, o, p := newUnifiedRefundFixture(t, newUnifiedRefundSQLiteClient(t), payment.TypeWxpay)
			ctx := context.Background()
			if name == "no deduction" {
				p.DeductBalance = false
			}
			a, err := svc.reserveUnifiedRefundAttempt(ctx, p)
			require.NoError(t, err)
			if name == "deduction failure" {
				svc.userRepo.(*unifiedRefundTestUsers).afterDeduct = func() error { return errors.New("injected transaction failure") }
			}
			if name == "shortfall" {
				_, err = svc.entClient.User.UpdateOneID(o.UserID).SetBalance(2).Save(ctx)
				require.NoError(t, err)
			}
			result, err := svc.applyUnifiedRefundResource(ctx, o.ID, unifiedRefundFixtureResource(a, "SUCCEEDED"), "query")
			if name == "deduction failure" {
				require.Error(t, err)
				assertUnifiedRefundBalance(t, svc, o, OrderStatusRefundPending, 10)
				stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, o.ID, a.ProductRefundNo)
				require.NoError(t, err)
				require.Equal(t, "PENDING", stored.Status)
			} else {
				require.NoError(t, err)
				require.True(t, result.Success)
				balance := float64(0)
				if name == "no deduction" {
					balance = 10
				} else {
					require.Contains(t, result.Warning, "manual review")
				}
				assertUnifiedRefundBalance(t, svc, o, OrderStatusRefunded, balance)
			}
		})
	}
}

func TestUnifiedRefundCannotUseLegacyNoTradeShortcut(t *testing.T) {
	svc, _, p := newUnifiedRefundFixture(t, newUnifiedRefundSQLiteClient(t), payment.TypeAlipay)
	p.Order.PaymentTradeNo = ""
	_, err := svc.gwRefund(context.Background(), p)
	require.Error(t, err)
	require.Error(t, svc.validateUnifiedRefundOrder(p.Order))
}

func TestUnifiedRefundUnknownAttemptFreezesOrder(t *testing.T) {
	svc, o, p := newUnifiedRefundFixture(t, newUnifiedRefundSQLiteClient(t), payment.TypeAlipay)
	ctx := context.Background()
	unknown := &unifiedRefundAttempt{ProductRefundNo: "sub2-refund-unknown", PaymentOrderID: psOrderProviderSnapshot(o).PaymentOrderID, AmountFen: 1023, PaymentMethod: "alipay"}
	_, err := svc.applyUnifiedRefundResource(ctx, o.ID, unifiedRefundFixtureResource(unknown, "SUCCEEDED"), "webhook")
	var permanent *unifiedWebhookPermanentError
	require.ErrorAs(t, err, &permanent)
	_, err = svc.reserveUnifiedRefundAttempt(ctx, p)
	require.Error(t, err)
	assertUnifiedRefundBalance(t, svc, o, OrderStatusCompleted, 10)
}

func TestUnifiedRefundRejectedEventPersistsReviewBeforeAck(t *testing.T) {
	for _, failAudit := range []bool{false, true} {
		t.Run(map[bool]string{false: "persisted", true: "audit unavailable"}[failAudit], func(t *testing.T) {
			svc, o, p := newUnifiedRefundFixture(t, newUnifiedRefundSQLiteClient(t), payment.TypeWxpay)
			ctx := context.Background()
			a, err := svc.reserveUnifiedRefundAttempt(ctx, p)
			require.NoError(t, err)
			if failAudit {
				_, err = svc.entClient.ExecContext(ctx, "DROP TABLE unified_payment_refund_events")
				require.NoError(t, err)
			}
			event := unifiedpay.WebhookEvent{EventID: uuid.NewString(), EventType: unifiedpay.EventRefundSucceeded, Sequence: 2,
				Refund: &unifiedpay.WebhookRefundResource{ProductRefundNo: a.ProductRefundNo}}
			err = svc.rejectUnifiedPaymentEvent(ctx, o, event, "amount_mismatch")
			var permanent *unifiedWebhookPermanentError
			if failAudit {
				require.Error(t, err)
				require.False(t, errors.As(err, &permanent))
			} else {
				require.ErrorAs(t, err, &permanent)
			}
			stored, readErr := loadUnifiedRefundAttempt(ctx, svc.entClient, o.ID, a.ProductRefundNo)
			require.NoError(t, readErr)
			require.Equal(t, !failAudit, stored.NeedsManualReview, "review update and evidence must commit together")
			assertUnifiedRefundBalance(t, svc, o, OrderStatusRefundPending, 10)
		})
	}
}

func TestUnifiedRefundEvidenceRetainsTrustedTerminalFields(t *testing.T) {
	for _, action := range []string{"UNIFIED_REFUND_RESULT", "UNIFIED_REFUND_UNCORRELATED", "UNIFIED_PAYMENT_EVENT_REJECTED"} {
		t.Run(action, func(t *testing.T) {
			svc, o, p := newUnifiedRefundFixture(t, newUnifiedRefundSQLiteClient(t), payment.TypeWxpay)
			ctx := context.Background()
			a, err := svc.reserveUnifiedRefundAttempt(ctx, p)
			require.NoError(t, err)
			completed := time.Now().UTC().Truncate(time.Second)
			providerID, providerStatus, failureCode := "refund-test-001", "FAILED", "channel_declined"
			event := unifiedpay.WebhookEvent{
				EventID: uuid.NewString(), EventType: unifiedpay.EventRefundFailed, Sequence: 3, OccurredAt: completed, OriginRequestID: uuid.NewString(),
				Environment: unifiedpay.EnvironmentSandbox, OrganizationID: a.OrganizationID, ProductID: a.ProductID, AppID: a.AppID,
				Resource: unifiedpay.PaymentOrderResource{PaymentOrderID: a.PaymentOrderID, ProductOrderNo: o.OutTradeNo, OrderType: payment.OrderTypeBalance,
					AmountFen: 1023, PaidAmountFen: 1023, Currency: "CNY", PaymentMethod: "wechat_pay", Status: unifiedpay.StatusPaid, ChannelOutTradeNo: "sandbox_sub2_payment_001"},
				Refund: &unifiedpay.WebhookRefundResource{RefundRequestID: uuid.NewString(), ProductRefundNo: a.ProductRefundNo, ChannelOutRefundNo: "sandbox_sub2_refund_001",
					AmountFen: 1023, PaymentMethod: "wechat_pay", Status: "FAILED", ProviderRefundID: &providerID, ProviderStatus: &providerStatus, FailureCode: &failureCode, CompletedAt: &completed},
			}
			if action == "UNIFIED_REFUND_UNCORRELATED" {
				event.Refund.ProductRefundNo = "sub2-refund-unknown"
			}
			if action == "UNIFIED_PAYMENT_EVENT_REJECTED" {
				event.Resource.AmountFen++
			}
			err = svc.processUnifiedPaymentEvent(ctx, unifiedpay.VerifiedWebhook{Event: event})
			if action == "UNIFIED_REFUND_RESULT" {
				require.NoError(t, err)
			} else {
				var permanent *unifiedWebhookPermanentError
				require.ErrorAs(t, err, &permanent)
			}
			rows, err := svc.entClient.QueryContext(ctx, "SELECT detail FROM unified_payment_refund_events WHERE order_id=$1 AND action=$2", o.ID, action)
			require.NoError(t, err)
			defer rows.Close()
			require.True(t, rows.Next())
			var raw string
			require.NoError(t, rows.Scan(&raw))
			var detail map[string]any
			require.NoError(t, json.Unmarshal([]byte(raw), &detail))
			for key, want := range map[string]any{
				"event_id": event.EventID, "source": "webhook", "payment_order_id": a.PaymentOrderID, "refund_request_id": event.Refund.RefundRequestID,
				"product_refund_no": event.Refund.ProductRefundNo, "channel_out_refund_no": event.Refund.ChannelOutRefundNo,
				"amount_fen": float64(1023), "currency": "CNY", "payment_method": "wechat_pay", "status": "FAILED",
				"provider_refund_id": providerID, "provider_status": providerStatus, "failure_code": failureCode,
				"completed_at": completed.Format(time.RFC3339), "occurred_at": completed.Format(time.RFC3339),
			} {
				require.Equal(t, want, detail[key], key)
			}
			require.NotContains(t, raw, "raw_body")
		})
	}
}
