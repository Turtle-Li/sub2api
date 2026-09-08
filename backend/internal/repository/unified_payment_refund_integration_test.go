//go:build integration

package repository

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	unifiedRefundIntegrationOrganizationID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	unifiedRefundIntegrationProductID      = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	unifiedRefundIntegrationAppID          = "app.sub2.sandbox"
	unifiedRefundIntegrationRequestKeyID   = "request.key.test"
	unifiedRefundIntegrationWebhookKeyID   = "webhook.key.test"
)

// These tests exercise the product-side durable refund seam against PostgreSQL.
// The fake pay-v1 endpoint uses only in-memory Ed25519 keys and httptest; it
// never talks to a payment channel or reads application configuration.
func TestUnifiedRefundPostgres(t *testing.T) {
	for _, paymentType := range []string{payment.TypeAlipay, payment.TypeWxpay} {
		paymentType := paymentType
		t.Run(paymentType, func(t *testing.T) {
			t.Run("eight administrators retain one pending attempt", func(t *testing.T) {
				testUnifiedRefundConcurrentPendingPostgres(t, paymentType)
			})
			t.Run("query and signed webhook finalize once", func(t *testing.T) {
				testUnifiedRefundQueryWebhookRacePostgres(t, paymentType)
			})
			t.Run("early signed webhook wins over delayed post response", func(t *testing.T) {
				testUnifiedRefundEarlyWebhookPostgres(t, paymentType)
			})
		})
	}
}

func testUnifiedRefundConcurrentPendingPostgres(t *testing.T, paymentType string) {
	t.Helper()
	requestPrivate, webhookPrivate := newUnifiedRefundIntegrationKeys(t)
	var (
		mu          sync.Mutex
		requests    []unifiedRefundHTTPRecord
		handlerErrs []error
		fixture     *unifiedRefundPostgresFixture
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err == nil {
			err = verifyUnifiedRefundSignedRequest(r, body, requestPrivate.Public().(ed25519.PublicKey))
		}
		if err != nil {
			mu.Lock()
			handlerErrs = append(handlerErrs, err)
			mu.Unlock()
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		current := fixture
		mu.Unlock()
		if current == nil {
			http.Error(w, "fixture unavailable", http.StatusInternalServerError)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/refund-requests":
			record, decodeErr := decodeUnifiedRefundHTTPRecord(body, r.Header.Get(unifiedpay.HeaderIdempotencyKey))
			if decodeErr != nil {
				mu.Lock()
				handlerErrs = append(handlerErrs, decodeErr)
				mu.Unlock()
				http.Error(w, decodeErr.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			requests = append(requests, record)
			mu.Unlock()
			writeUnifiedRefundHTTPResponse(w, http.StatusAccepted, current.refundResponse(record.ProductRefundNo, unifiedpay.RefundStatusApproved))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/refund-requests/"+current.remoteRefundID:
			mu.Lock()
			productRefundNo := ""
			if len(requests) > 0 {
				productRefundNo = requests[0].ProductRefundNo
			}
			mu.Unlock()
			if productRefundNo == "" {
				err := fmt.Errorf("refund GET arrived before durable refund request capture")
				mu.Lock()
				handlerErrs = append(handlerErrs, err)
				mu.Unlock()
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			writeUnifiedRefundHTTPResponse(w, http.StatusOK, current.refundResponse(productRefundNo, unifiedpay.RefundStatusApproved))
		default:
			err := fmt.Errorf("unexpected endpoint %s %s", r.Method, r.URL.Path)
			mu.Lock()
			handlerErrs = append(handlerErrs, err)
			mu.Unlock()
			http.Error(w, err.Error(), http.StatusNotFound)
		}
	}))
	defer server.Close()

	fixture = newUnifiedRefundPostgresFixture(t, paymentType, server.URL, requestPrivate, webhookPrivate, time.Now().UTC().Truncate(time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const administrators = 8
	start := make(chan struct{})
	errs := make(chan error, administrators)
	var wg sync.WaitGroup
	for i := 0; i < administrators; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			plan, early, err := fixture.paymentService.PrepareRefund(ctx, fixture.orderID, 10, "administrator refund", false, true)
			if err != nil {
				errs <- err
				return
			}
			if early != nil {
				errs <- fmt.Errorf("unexpected early refund result: %+v", early)
				return
			}
			if _, err := fixture.paymentService.ExecuteRefund(ctx, plan); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	mu.Lock()
	captured := append([]unifiedRefundHTTPRecord(nil), requests...)
	serverErrors := append([]error(nil), handlerErrs...)
	mu.Unlock()
	for _, err := range serverErrors {
		require.NoError(t, err)
	}
	require.NotEmpty(t, captured, "the durable pending attempt must be submitted to pay-v1")

	attemptCount := unifiedRefundAttemptCount(t, fixture.orderID)
	require.Equal(t, 1, attemptCount)
	attempt := loadUnifiedRefundPostgresAttempt(t, fixture.orderID)
	require.Equal(t, "PENDING", attempt.Status)
	require.NotEmpty(t, attempt.RefundRequestID)
	for _, request := range captured {
		require.Equal(t, fixture.paymentOrderID, request.PaymentOrderID)
		require.Equal(t, attempt.ProductRefundNo, request.ProductRefundNo)
		require.Equal(t, attempt.IdempotencyKey, request.IdempotencyKey)
		require.Equal(t, int64(1000), request.AmountFen)
		require.Equal(t, "other", request.ReasonCode)
	}
	assertUnifiedRefundPostgresState(t, fixture, service.OrderStatusRefundPending, 10)
}

func testUnifiedRefundQueryWebhookRacePostgres(t *testing.T, paymentType string) {
	t.Helper()
	requestPrivate, webhookPrivate := newUnifiedRefundIntegrationKeys(t)
	var (
		mu          sync.Mutex
		handlerErrs []error
		fixture     *unifiedRefundPostgresFixture
		productNo   string
	)
	getStarted := make(chan struct{}, 1)
	releaseGet := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseGet) }) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err == nil {
			err = verifyUnifiedRefundSignedRequest(r, body, requestPrivate.Public().(ed25519.PublicKey))
		}
		if err != nil {
			mu.Lock()
			handlerErrs = append(handlerErrs, err)
			mu.Unlock()
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		current := fixture
		storedProductNo := productNo
		mu.Unlock()
		if current == nil {
			http.Error(w, "fixture unavailable", http.StatusInternalServerError)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/refund-requests":
			record, decodeErr := decodeUnifiedRefundHTTPRecord(body, r.Header.Get(unifiedpay.HeaderIdempotencyKey))
			if decodeErr != nil {
				http.Error(w, decodeErr.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			productNo = record.ProductRefundNo
			mu.Unlock()
			writeUnifiedRefundHTTPResponse(w, http.StatusAccepted, current.refundResponse(record.ProductRefundNo, unifiedpay.RefundStatusApproved))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/refund-requests/"+current.remoteRefundID:
			getStarted <- struct{}{}
			<-releaseGet
			if storedProductNo == "" {
				mu.Lock()
				storedProductNo = productNo
				mu.Unlock()
			}
			writeUnifiedRefundHTTPResponse(w, http.StatusOK, current.refundResponse(storedProductNo, unifiedpay.RefundStatusSucceeded))
		default:
			http.Error(w, "unexpected pay-v1 request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	fixture = newUnifiedRefundPostgresFixture(t, paymentType, server.URL, requestPrivate, webhookPrivate, time.Now().UTC().Truncate(time.Second))
	plan := prepareUnifiedRefundPostgres(t, fixture)
	pending, err := fixture.paymentService.ExecuteRefund(context.Background(), plan)
	require.NoError(t, err)
	require.False(t, pending.Success)
	attempt := loadUnifiedRefundPostgresAttempt(t, fixture.orderID)
	require.NotEmpty(t, attempt.RefundRequestID)
	require.Equal(t, fixture.remoteRefundID, attempt.RefundRequestID)

	queryDone := make(chan error, 1)
	go func() {
		_, err := fixture.paymentService.QueryAndFinalizeRefund(context.Background(), fixture.orderID)
		queryDone <- err
	}()
	select {
	case <-getStarted:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for refund status query")
	}

	rawEvent, webhookHeaders := fixture.signedRefundSucceededWebhook(t, attempt.ProductRefundNo, 20)
	webhookDone := make(chan error, 1)
	go func() {
		webhookDone <- fixture.paymentService.HandleUnifiedPaymentWebhook(context.Background(), webhookHeaders, rawEvent)
	}()
	releaseOnce.Do(func() { close(releaseGet) })
	require.NoError(t, awaitUnifiedRefundResult(t, webhookDone, "signed refund webhook"))
	require.NoError(t, awaitUnifiedRefundResult(t, queryDone, "refund status query"))
	// Same signed event must ACK as an inbox duplicate and cannot deduct again.
	require.NoError(t, fixture.paymentService.HandleUnifiedPaymentWebhook(context.Background(), webhookHeaders, rawEvent))
	require.NoError(t, fixture.paymentService.HandleUnifiedPaymentWebhook(context.Background(), webhookHeaders, rawEvent))

	mu.Lock()
	serverErrors := append([]error(nil), handlerErrs...)
	mu.Unlock()
	for _, handlerErr := range serverErrors {
		require.NoError(t, handlerErr)
	}
	attempt = loadUnifiedRefundPostgresAttempt(t, fixture.orderID)
	require.Equal(t, unifiedpay.RefundStatusSucceeded, attempt.Status)
	require.False(t, attempt.NeedsManualReview)
	assertUnifiedRefundPostgresState(t, fixture, service.OrderStatusRefunded, 0)
	require.Equal(t, 1, unifiedRefundSuccessAuditCount(t, fixture.orderID))
}

func testUnifiedRefundEarlyWebhookPostgres(t *testing.T, paymentType string) {
	t.Helper()
	requestPrivate, webhookPrivate := newUnifiedRefundIntegrationKeys(t)
	var (
		mu          sync.Mutex
		handlerErrs []error
		fixture     *unifiedRefundPostgresFixture
	)
	postArrived := make(chan unifiedRefundHTTPRecord, 1)
	releasePost := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releasePost) }) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err == nil {
			err = verifyUnifiedRefundSignedRequest(r, body, requestPrivate.Public().(ed25519.PublicKey))
		}
		if err == nil && (r.Method != http.MethodPost || r.URL.Path != "/v1/refund-requests") {
			err = fmt.Errorf("unexpected endpoint %s %s", r.Method, r.URL.Path)
		}
		var record unifiedRefundHTTPRecord
		if err == nil {
			record, err = decodeUnifiedRefundHTTPRecord(body, r.Header.Get(unifiedpay.HeaderIdempotencyKey))
		}
		if err != nil {
			mu.Lock()
			handlerErrs = append(handlerErrs, err)
			mu.Unlock()
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		postArrived <- record
		<-releasePost
		mu.Lock()
		current := fixture
		mu.Unlock()
		if current == nil {
			http.Error(w, "fixture unavailable", http.StatusInternalServerError)
			return
		}
		writeUnifiedRefundHTTPResponse(w, http.StatusAccepted, current.refundResponse(record.ProductRefundNo, unifiedpay.RefundStatusApproved))
	}))
	defer server.Close()

	fixture = newUnifiedRefundPostgresFixture(t, paymentType, server.URL, requestPrivate, webhookPrivate, time.Now().UTC().Truncate(time.Second))
	executeDone := make(chan unifiedRefundExecutionOutcome, 1)
	go func() {
		plan, early, err := fixture.paymentService.PrepareRefund(context.Background(), fixture.orderID, 10, "administrator refund", false, true)
		if err == nil && early != nil {
			err = fmt.Errorf("unexpected early refund result: %+v", early)
		}
		if err != nil {
			executeDone <- unifiedRefundExecutionOutcome{err: err}
			return
		}
		result, err := fixture.paymentService.ExecuteRefund(context.Background(), plan)
		executeDone <- unifiedRefundExecutionOutcome{result: result, err: err}
	}()

	var post unifiedRefundHTTPRecord
	select {
	case post = <-postArrived:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for durable refund POST")
	}
	attempt := loadUnifiedRefundPostgresAttempt(t, fixture.orderID)
	require.Equal(t, post.ProductRefundNo, attempt.ProductRefundNo)
	require.Equal(t, post.IdempotencyKey, attempt.IdempotencyKey)

	rawEvent, webhookHeaders := fixture.signedRefundSucceededWebhook(t, attempt.ProductRefundNo, 30)
	require.NoError(t, fixture.paymentService.HandleUnifiedPaymentWebhook(context.Background(), webhookHeaders, rawEvent))
	releaseOnce.Do(func() { close(releasePost) })
	outcome := awaitUnifiedRefundOutcome(t, executeDone, "delayed create response")
	require.NoError(t, outcome.err)
	require.NotNil(t, outcome.result)
	require.True(t, outcome.result.Success)

	mu.Lock()
	serverErrors := append([]error(nil), handlerErrs...)
	mu.Unlock()
	for _, handlerErr := range serverErrors {
		require.NoError(t, handlerErr)
	}
	attempt = loadUnifiedRefundPostgresAttempt(t, fixture.orderID)
	require.Equal(t, unifiedpay.RefundStatusSucceeded, attempt.Status)
	assertUnifiedRefundPostgresState(t, fixture, service.OrderStatusRefunded, 0)
	require.Equal(t, 1, unifiedRefundSuccessAuditCount(t, fixture.orderID))
}

type unifiedRefundPostgresFixture struct {
	paymentService     *service.PaymentService
	userID, orderID    int64
	paymentOrderID     string
	outTradeNo         string
	gatewayMethod      string
	organizationID     string
	productID          string
	appID              string
	remoteRefundID     string
	channelOutRefundNo string
	now                time.Time
	webhookPrivateKey  ed25519.PrivateKey
}

type unifiedRefundExecutionOutcome struct {
	result *service.RefundResult
	err    error
}

func newUnifiedRefundPostgresFixture(t *testing.T, paymentType, baseURL string, requestPrivate, webhookPrivate ed25519.PrivateKey, now time.Time) *unifiedRefundPostgresFixture {
	t.Helper()
	method, ok := unifiedpay.PaymentMethodForPaymentType(paymentType)
	require.True(t, ok, "supported integration payment type")
	gateway := newUnifiedRefundIntegrationGateway(t, baseURL, paymentType, requestPrivate, webhookPrivate, now)
	unique := uuid.NewString()
	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email:        unique + "@refund.integration.test",
		PasswordHash: "test-only-password-hash",
		Username:     "refund-" + unique[:8],
		Balance:      10,
	})
	remotePaymentOrderID := uuid.NewString()
	outTradeNo := "sub2_" + unique
	order, err := integrationEntClient.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(10).
		SetPayAmount(10).
		SetFeeRate(0).
		SetRechargeCode(unique).
		SetOutTradeNo(outTradeNo).
		SetPaymentType(paymentType).
		SetPaymentTradeNo("channel_transaction_" + unique).
		SetProviderKey(payment.TypeUnifiedPay).
		SetProviderSnapshot(map[string]any{
			"schema_version":   2,
			"provider_key":     payment.TypeUnifiedPay,
			"payment_order_id": remotePaymentOrderID,
			"environment":      unifiedpay.EnvironmentSandbox.String(),
			"organization_id":  unifiedRefundIntegrationOrganizationID,
			"product_id":       unifiedRefundIntegrationProductID,
			"app_id":           unifiedRefundIntegrationAppID,
			"currency":         payment.DefaultPaymentCurrency,
		}).
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(service.OrderStatusCompleted).
		SetPaidAt(now).
		SetCompletedAt(now).
		SetExpiresAt(now.Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("localhost").
		Save(context.Background())
	require.NoError(t, err)

	fixture := &unifiedRefundPostgresFixture{
		userID: user.ID, orderID: order.ID, paymentOrderID: remotePaymentOrderID, outTradeNo: outTradeNo,
		gatewayMethod: method, organizationID: unifiedRefundIntegrationOrganizationID,
		productID: unifiedRefundIntegrationProductID, appID: unifiedRefundIntegrationAppID,
		remoteRefundID: uuid.NewString(), channelOutRefundNo: "sandbox_refund_" + uuid.NewString(),
		now: now, webhookPrivateKey: webhookPrivate,
	}
	fixture.paymentService = service.NewPaymentService(
		integrationEntClient, nil, nil, nil, nil, nil,
		NewUserRepository(integrationEntClient, integrationDB), nil, nil,
	)
	fixture.paymentService.SetUnifiedPayment(gateway, NewUnifiedPaymentWebhookInboxStore(integrationDB))
	t.Cleanup(func() { fixture.cleanup(t) })
	return fixture
}

func (f *unifiedRefundPostgresFixture) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	queries := []struct {
		name  string
		query string
		args  []any
	}{
		{"refund event history", `DELETE FROM unified_payment_refund_events WHERE order_id = $1`, []any{f.orderID}},
		{"refund attempt", `DELETE FROM unified_payment_refund_attempts WHERE order_id = $1`, []any{f.orderID}},
		{"legacy audit", `DELETE FROM payment_audit_logs WHERE order_id = $1`, []any{strconv.FormatInt(f.orderID, 10)}},
		{"payment order", `DELETE FROM payment_orders WHERE id = $1`, []any{f.orderID}},
		{"user", `DELETE FROM users WHERE id = $1`, []any{f.userID}},
		{"webhook inbox", `DELETE FROM unified_payment_webhook_inbox WHERE payment_order_id = $1::uuid`, []any{f.paymentOrderID}},
		{"webhook cursor", `DELETE FROM unified_payment_webhook_cursor WHERE payment_order_id = $1::uuid`, []any{f.paymentOrderID}},
	}
	for _, item := range queries {
		if _, err := integrationDB.ExecContext(ctx, item.query, item.args...); err != nil {
			t.Errorf("cleanup %s: %v", item.name, err)
		}
	}
}

func (f *unifiedRefundPostgresFixture) refundResponse(productRefundNo, status string) map[string]any {
	var completedAt any
	if status == unifiedpay.RefundStatusSucceeded || status == unifiedpay.RefundStatusFailed {
		completedAt = f.now
	}
	return map[string]any{
		"environment":           unifiedpay.EnvironmentSandbox,
		"organization_id":       f.organizationID,
		"product_id":            f.productID,
		"refund_request_id":     f.remoteRefundID,
		"payment_order_id":      f.paymentOrderID,
		"product_refund_no":     productRefundNo,
		"channel_out_refund_no": f.channelOutRefundNo,
		"amount_fen":            int64(1000),
		"currency":              payment.DefaultPaymentCurrency,
		"payment_method":        f.gatewayMethod,
		"status":                status,
		"needs_manual_review":   false,
		"created_at":            f.now,
		"updated_at":            f.now,
		"completed_at":          completedAt,
	}
}

func (f *unifiedRefundPostgresFixture) signedRefundSucceededWebhook(t *testing.T, productRefundNo string, sequence int64) ([]byte, http.Header) {
	t.Helper()
	eventID := uuid.NewString()
	paidAt := f.now
	completedAt := f.now
	transactionID := "channel_transaction_" + uuid.NewString()
	event := unifiedpay.WebhookEvent{
		SchemaVersion:   "payment-webhook.v1",
		EventID:         eventID,
		EventType:       unifiedpay.EventRefundSucceeded,
		OccurredAt:      f.now,
		Environment:     unifiedpay.EnvironmentSandbox,
		OrganizationID:  f.organizationID,
		ProductID:       f.productID,
		ProductCode:     "sub2",
		AppID:           f.appID,
		Sequence:        sequence,
		OriginRequestID: "origin_refund_" + uuid.NewString(),
		Resource: unifiedpay.PaymentOrderResource{
			PaymentOrderID:          f.paymentOrderID,
			ProductOrderNo:          f.outTradeNo,
			OrderType:               payment.OrderTypeBalance,
			AmountFen:               1000,
			PaidAmountFen:           1000,
			RefundedAmountFen:       1000,
			ReservedRefundAmountFen: 0,
			RefundableAmountFen:     0,
			Currency:                payment.DefaultPaymentCurrency,
			PaymentMethod:           f.gatewayMethod,
			Status:                  unifiedpay.StatusRefunded,
			ChannelOutTradeNo:       "sandbox_trade_" + uuid.NewString(),
			ChannelTransactionID:    &transactionID,
			PaidAt:                  &paidAt,
		},
		Refund: &unifiedpay.WebhookRefundResource{
			RefundRequestID:    f.remoteRefundID,
			ProductRefundNo:    productRefundNo,
			ChannelOutRefundNo: f.channelOutRefundNo,
			AmountFen:          1000,
			PaymentMethod:      f.gatewayMethod,
			Status:             unifiedpay.RefundStatusSucceeded,
			CompletedAt:        &completedAt,
		},
	}
	rawBody, err := json.Marshal(event)
	require.NoError(t, err)
	signature := ed25519.Sign(f.webhookPrivateKey, []byte(unifiedpay.WebhookSignaturePayload(
		unifiedRefundIntegrationWebhookKeyID, f.now.Unix(), eventID, rawBody,
	)))
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set(unifiedpay.HeaderWebhookKeyID, unifiedRefundIntegrationWebhookKeyID)
	headers.Set(unifiedpay.HeaderWebhookTimestamp, strconv.FormatInt(f.now.Unix(), 10))
	headers.Set(unifiedpay.HeaderWebhookEventID, eventID)
	headers.Set(unifiedpay.HeaderWebhookSignature, base64.StdEncoding.EncodeToString(signature))
	return rawBody, headers
}

func prepareUnifiedRefundPostgres(t *testing.T, fixture *unifiedRefundPostgresFixture) *service.RefundPlan {
	t.Helper()
	plan, early, err := fixture.paymentService.PrepareRefund(context.Background(), fixture.orderID, 10, "administrator refund", false, true)
	require.NoError(t, err)
	require.Nil(t, early)
	return plan
}

func assertUnifiedRefundPostgresState(t *testing.T, fixture *unifiedRefundPostgresFixture, wantStatus string, wantBalance float64) {
	t.Helper()
	order, err := integrationEntClient.PaymentOrder.Get(context.Background(), fixture.orderID)
	require.NoError(t, err)
	require.Equal(t, wantStatus, order.Status)
	user, err := integrationEntClient.User.Get(context.Background(), fixture.userID)
	require.NoError(t, err)
	require.Equal(t, wantBalance, user.Balance)
}

type unifiedRefundPostgresAttempt struct {
	ProductRefundNo   string
	IdempotencyKey    string
	RefundRequestID   string
	Status            string
	NeedsManualReview bool
}

func loadUnifiedRefundPostgresAttempt(t *testing.T, orderID int64) unifiedRefundPostgresAttempt {
	t.Helper()
	var attempt unifiedRefundPostgresAttempt
	err := integrationDB.QueryRowContext(context.Background(), `
		SELECT product_refund_no, idempotency_key, COALESCE(refund_request_id::text, ''), status, needs_manual_review
		FROM unified_payment_refund_attempts
		WHERE order_id = $1
		ORDER BY created_at DESC, product_refund_no DESC
		LIMIT 1
	`, orderID).Scan(&attempt.ProductRefundNo, &attempt.IdempotencyKey, &attempt.RefundRequestID, &attempt.Status, &attempt.NeedsManualReview)
	require.NoError(t, err)
	return attempt
}

func unifiedRefundAttemptCount(t *testing.T, orderID int64) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM unified_payment_refund_attempts WHERE order_id = $1
	`, orderID).Scan(&count))
	return count
}

func unifiedRefundSuccessAuditCount(t *testing.T, orderID int64) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM payment_audit_logs WHERE order_id = $1 AND action = 'REFUND_SUCCESS'
	`, strconv.FormatInt(orderID, 10)).Scan(&count))
	return count
}

type unifiedRefundHTTPRecord struct {
	PaymentOrderID  string
	ProductRefundNo string
	IdempotencyKey  string
	AmountFen       int64
	ReasonCode      string
}

func decodeUnifiedRefundHTTPRecord(body []byte, idempotencyKey string) (unifiedRefundHTTPRecord, error) {
	var payload struct {
		PaymentOrderID  string `json:"payment_order_id"`
		ProductRefundNo string `json:"product_refund_no"`
		AmountFen       int64  `json:"amount_fen"`
		ReasonCode      string `json:"reason_code"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return unifiedRefundHTTPRecord{}, fmt.Errorf("decode refund request: %w", err)
	}
	if payload.PaymentOrderID == "" || payload.ProductRefundNo == "" || idempotencyKey == "" {
		return unifiedRefundHTTPRecord{}, fmt.Errorf("refund request missing durable correlation field")
	}
	return unifiedRefundHTTPRecord{
		PaymentOrderID: payload.PaymentOrderID, ProductRefundNo: payload.ProductRefundNo,
		IdempotencyKey: idempotencyKey, AmountFen: payload.AmountFen, ReasonCode: payload.ReasonCode,
	}, nil
}

func verifyUnifiedRefundSignedRequest(r *http.Request, body []byte, requestPublicKey ed25519.PublicKey) error {
	if r == nil {
		return fmt.Errorf("request is nil")
	}
	if r.Header.Get(unifiedpay.HeaderAppID) != unifiedRefundIntegrationAppID ||
		r.Header.Get(unifiedpay.HeaderKeyID) != unifiedRefundIntegrationRequestKeyID ||
		r.Header.Get(unifiedpay.HeaderAudience) != unifiedpay.AudienceSandbox {
		return fmt.Errorf("request scope headers are invalid")
	}
	bodyHash := sha256.Sum256(body)
	payload := unifiedpay.CanonicalPayload(
		r.Method,
		r.Header.Get(unifiedpay.HeaderAudience),
		r.URL.RequestURI(),
		hex.EncodeToString(bodyHash[:]),
		r.Header.Get(unifiedpay.HeaderAppID),
		r.Header.Get(unifiedpay.HeaderKeyID),
		r.Header.Get(unifiedpay.HeaderTimestamp),
		r.Header.Get(unifiedpay.HeaderNonce),
		r.Header.Get(unifiedpay.HeaderIdempotencyKey),
	)
	signature, err := base64.StdEncoding.Strict().DecodeString(r.Header.Get(unifiedpay.HeaderSignature))
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(requestPublicKey, payload, signature) {
		return fmt.Errorf("request signature is invalid")
	}
	return nil
}

func writeUnifiedRefundHTTPResponse(w http.ResponseWriter, status int, response map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func newUnifiedRefundIntegrationKeys(t *testing.T) (ed25519.PrivateKey, ed25519.PrivateKey) {
	t.Helper()
	_, requestPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_, webhookPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return requestPrivate, webhookPrivate
}

func newUnifiedRefundIntegrationGateway(t *testing.T, baseURL, paymentType string, requestPrivate, webhookPrivate ed25519.PrivateKey, now time.Time) *unifiedpay.Gateway {
	t.Helper()
	var nonceSequence uint64
	gateway, err := unifiedpay.New(unifiedpay.Config{
		Enabled:           true,
		BaseURL:           baseURL,
		Environment:       unifiedpay.EnvironmentSandbox,
		OrganizationID:    unifiedRefundIntegrationOrganizationID,
		ProductID:         unifiedRefundIntegrationProductID,
		AppID:             unifiedRefundIntegrationAppID,
		RequestKeyID:      unifiedRefundIntegrationRequestKeyID,
		RequestPrivateKey: requestPrivate,
		WebhookPublicKeys: map[string]ed25519.PublicKey{
			unifiedRefundIntegrationWebhookKeyID: webhookPrivate.Public().(ed25519.PublicKey),
		},
		SupportedMethods: []string{paymentType},
		ReturnURL:        "https://refund.integration.test/result",
		HTTPClient:       &http.Client{Timeout: 15 * time.Second},
		Clock:            func() time.Time { return now },
		NonceSource: func() (string, error) {
			return fmt.Sprintf("nonce.refund.integration.%020d", atomic.AddUint64(&nonceSequence, 1)), nil
		},
	})
	require.NoError(t, err)
	return gateway
}

func awaitUnifiedRefundResult(t *testing.T, done <-chan error, operation string) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
		return nil
	}
}

func awaitUnifiedRefundOutcome(t *testing.T, done <-chan unifiedRefundExecutionOutcome, operation string) unifiedRefundExecutionOutcome {
	t.Helper()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
		return unifiedRefundExecutionOutcome{}
	}
}
