//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type refundAuthCacheInvalidatorSpy struct {
	userIDs []int64
}

func (s *refundAuthCacheInvalidatorSpy) InvalidateAuthCacheByUserID(_ context.Context, userID int64) {
	s.userIDs = append(s.userIDs, userID)
}

func (s *refundAuthCacheInvalidatorSpy) InvalidateAuthCacheByKey(context.Context, string) {}

func (s *refundAuthCacheInvalidatorSpy) InvalidateAuthCacheByGroupID(context.Context, int64) {}

type refundBalanceAuthorizationCacheSpy struct {
	err     error
	userIDs []int64
}

func TestNormalizeRefundReasonUsesGatewayContractAndLegacyCompatibility(t *testing.T) {
	tests := []struct {
		name        string
		input       RefundReasonInput
		wantCode    string
		wantSummary string
		wantAudit   string
		wantReason  string
	}{
		{
			name: "customer request has a stable default summary", input: RefundReasonInput{Code: refundReasonCodeCustomerRequest},
			wantCode: refundReasonCodeCustomerRequest, wantSummary: "Customer requested a refund", wantAudit: "customer_request: Customer requested a refund",
		},
		{
			name: "preset with default summary", input: RefundReasonInput{Code: refundReasonCodeDuplicateCharge},
			wantCode: refundReasonCodeDuplicateCharge, wantSummary: "Duplicate charge", wantAudit: "duplicate_charge: Duplicate charge",
		},
		{
			name: "preset folds supplemental whitespace", input: RefundReasonInput{Code: refundReasonCodeServiceError, Detail: "  upstream\n gateway\t timeout  "},
			wantCode: refundReasonCodeServiceError, wantSummary: "upstream gateway timeout", wantAudit: "service_error: upstream gateway timeout",
		},
		{
			name: "legacy reason becomes other", input: RefundReasonInput{LegacyReason: "  customer\nrequest  "},
			wantCode: refundReasonCodeOther, wantSummary: "customer request", wantAudit: "other: customer request",
		},
		{
			name: "other requires detail", input: RefundReasonInput{Code: refundReasonCodeOther}, wantReason: "REFUND_REASON_DETAIL_REQUIRED",
		},
		{
			name: "rejects unknown code", input: RefundReasonInput{Code: "operator_override", Detail: "manual"}, wantReason: "INVALID_REFUND_REASON_CODE",
		},
		{
			name: "rejects summary over gateway boundary", input: RefundReasonInput{Code: refundReasonCodeOther, Detail: strings.Repeat("x", refundReasonSummaryMaximumRunes+1)}, wantReason: "INVALID_REFUND_REASON_DETAIL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason, err := normalizeRefundReason(tc.input)
			if tc.wantReason != "" {
				require.Error(t, err)
				require.Equal(t, tc.wantReason, infraerrors.Reason(err))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantCode, reason.Code)
			require.Equal(t, tc.wantSummary, reason.Summary)
			require.Equal(t, tc.wantAudit, reason.AuditText)
		})
	}
}

func TestPrepareReviewedRefundRequestCarriesStructuredReasonIntoDurableAttempt(t *testing.T) {
	ctx := context.Background()
	svc, order := newReviewedBalanceRefundFixture(t, 100, 0.1)
	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)

	plan, err := svc.PrepareReviewedRefundRequest(ctx, order.ID, review.QuoteRevision, RefundReasonInput{
		Code:   refundReasonCodeServiceNotDelivered,
		Detail: "  account was never\nprovisioned ",
	})
	require.NoError(t, err)
	require.Equal(t, refundReasonCodeServiceNotDelivered, plan.ReasonCode)
	require.Equal(t, "account was never provisioned", plan.ReasonSummary)
	require.Equal(t, "service_not_delivered: account was never provisioned", plan.Reason)

	attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, refundReasonCodeServiceNotDelivered, attempt.ReasonCode)
	require.Equal(t, "account was never provisioned", attempt.ReasonSummary)
	persistedOrder, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, persistedOrder.RefundReason)
	require.Equal(t, "service_not_delivered: account was never provisioned", *persistedOrder.RefundReason)
}

func (s *refundBalanceAuthorizationCacheSpy) EnsureBalanceAuthorizationCacheInvalidated(_ context.Context, userID int64) error {
	s.userIDs = append(s.userIDs, userID)
	return s.err
}

func TestCalculateWalletRefundQuoteSeparatesPrincipalAndGift(t *testing.T) {
	tests := []struct {
		name              string
		availableBalance  string
		availablePaid     string
		wantPrincipal     string
		wantGiftReclaimed string
		wantCashMinor     int64
	}{
		{
			name:             "fully unused owner test top-up",
			availableBalance: "100.00", availablePaid: "0.10",
			wantPrincipal: "0.10", wantGiftReclaimed: "99.90", wantCashMinor: 10,
		},
		{
			name:             "paid principal was consumed before gift",
			availableBalance: "99.90", availablePaid: "0",
			wantPrincipal: "0", wantGiftReclaimed: "0", wantCashMinor: 0,
		},
		{
			name:             "half of paid principal remains",
			availableBalance: "99.95", availablePaid: "0.05",
			wantPrincipal: "0.05", wantGiftReclaimed: "49.95", wantCashMinor: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quote := calculateWalletRefundQuote(walletRefundInputs{
				OriginalPaid:     decimal.RequireFromString("0.10"),
				OriginalGift:     decimal.RequireFromString("99.90"),
				CashPaidMinor:    10,
				AvailableBalance: decimal.RequireFromString(tt.availableBalance),
				AvailablePaid:    decimal.RequireFromString(tt.availablePaid),
			})

			require.True(t, quote.Principal.Equal(decimal.RequireFromString(tt.wantPrincipal)))
			require.True(t, quote.GiftReclaimed.Equal(decimal.RequireFromString(tt.wantGiftReclaimed)))
			require.Equal(t, tt.wantCashMinor, quote.CashMinor)
		})
	}
}

func TestCalculateSubscriptionRefundQuoteUsesRemainingSeconds(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	now := start.Add(15 * 24 * time.Hour)

	quote := calculateSubscriptionRefundQuote(subscriptionRefundInputs{
		OrderAmount: decimal.RequireFromString("120.00"),
		PayAmount:   decimal.RequireFromString("120.00"),
		TermStart:   start,
		OriginalEnd: end,
		CurrentEnd:  end,
		ValuationAt: now,
	})

	require.Equal(t, int64(15*24*time.Hour/time.Second), quote.Seconds)
	require.True(t, quote.ProductAmount.Equal(decimal.RequireFromString("60.00")))
	require.Equal(t, int64(6000), quote.CashMinor)
	require.Equal(t, now, quote.NewExpiry)
}

func TestCalculateSubscriptionRefundQuoteFloorsToFen(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(3 * time.Second)
	quote := calculateSubscriptionRefundQuote(subscriptionRefundInputs{
		OrderAmount: decimal.RequireFromString("0.10"),
		PayAmount:   decimal.RequireFromString("0.10"),
		TermStart:   start,
		OriginalEnd: end,
		CurrentEnd:  end,
		ValuationAt: start.Add(time.Second),
	})

	require.Equal(t, int64(2), quote.Seconds)
	require.True(t, quote.ProductAmount.Equal(decimal.RequireFromString("0.06")))
	require.Equal(t, int64(6), quote.CashMinor)
}

func newReviewedBalanceRefundFixture(t *testing.T, balance, availablePaid float64) (*PaymentService, *dbent.PaymentOrder) {
	t.Helper()
	ctx := context.Background()
	client := newUnifiedRefundSQLiteClient(t)
	svc, order, _ := newUnifiedRefundFixture(t, client, payment.TypeAlipay)

	_, err := client.User.UpdateOneID(order.UserID).
		SetBalance(balance).
		SetFrozenBalance(0).
		SetWalletAvailablePaid(availablePaid).
		SetWalletFrozenPaid(0).
		Save(ctx)
	require.NoError(t, err)
	order, err = client.PaymentOrder.UpdateOneID(order.ID).
		SetAmount(100).
		SetPayAmount(0.1).
		SetProductSnapshot(map[string]any{
			"kind":               paymentSnapshotKindBalance,
			"credited_amount":    100.0,
			"paid_credit_amount": 0.1,
			"gift_credit_amount": 99.9,
		}).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `INSERT INTO payment_wallet_fundings
		(payment_order_id, user_id, paid_credit_amount, gift_credit_amount, cash_paid_minor, currency)
		VALUES ($1,$2,$3,$4,$5,$6)`, order.ID, order.UserID, "0.10", "99.90", 10, payment.DefaultPaymentCurrency)
	require.NoError(t, err)
	svc.SetBalanceAuthorizationCacheInvalidator(&refundBalanceAuthorizationCacheSpy{})
	return svc, order
}

func loadReviewedBalanceRefundState(t *testing.T, svc *PaymentService, order *dbent.PaymentOrder) (*paymentWalletFunding, *dbent.User) {
	t.Helper()
	funding, user, err := loadPaymentWalletRefundState(context.Background(), svc.entClient, order.ID, order.UserID, false)
	require.NoError(t, err)
	return funding, user
}

func newReviewedSubscriptionRefundFixture(t *testing.T) (*PaymentService, *dbent.PaymentOrder, *dbent.UserSubscription, time.Time, time.Time) {
	t.Helper()
	ctx := context.Background()
	client := newUnifiedRefundSQLiteClient(t)
	svc, order, _ := newUnifiedRefundFixture(t, client, payment.TypeWxpay)
	start := time.Now().UTC().Truncate(time.Second).Add(-45 * 24 * time.Hour)
	end := start.Add(120 * 24 * time.Hour)
	valuationAt := start.Add(45*24*time.Hour + 5*time.Minute).UTC().Truncate(time.Minute)
	svc.refundReviewNow = func() time.Time { return valuationAt }
	group, err := client.Group.Create().SetName("refund-accounting-subscription-group").Save(ctx)
	require.NoError(t, err)
	subscription, err := client.UserSubscription.Create().
		SetUserID(order.UserID).
		SetGroupID(group.ID).
		SetStartsAt(start).
		SetExpiresAt(end).
		SetStatus(SubscriptionStatusActive).
		Save(ctx)
	require.NoError(t, err)
	order, err = client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(payment.OrderTypeSubscription).
		SetAmount(120).
		SetPayAmount(120).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `INSERT INTO payment_subscription_grants
		(payment_order_id, subscription_id, user_id, group_id, term_start_at, original_term_end_at, current_term_end_at)
		VALUES ($1,$2,$3,$4,$5,$6,$6)`, order.ID, subscription.ID, order.UserID, group.ID, start, end)
	require.NoError(t, err)
	// Reviewed subscription refunds have a strict pre-provider cache fence.
	// Model the Redis fence and invalidation Pub/Sub contract in every fixture
	// so accounting tests exercise the same boundary as production.
	svc.subscriptionSvc = &SubscriptionService{
		billingCacheService: &BillingCacheService{cache: &fencedSubscriptionCacheStub{}},
	}
	return svc, order, subscription, start, end
}

func prepareBalancePausedSubscriptionRefund(t *testing.T) (*PaymentService, *dbent.PaymentOrder, *dbent.UserSubscription, *unifiedRefundAttempt) {
	t.Helper()
	ctx := context.Background()
	svc, order, subscription, _, _ := newReviewedSubscriptionRefundFixture(t)
	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "provider balance recovery test")
	require.NoError(t, err)
	attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.NoError(t, err)
	attempt.RefundRequestID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	attempt.ChannelOutRefundNo = "sandbox_sub2_refund_recovery_001"
	attempt.ProviderStatus = unifiedRefundBalanceInsufficientProviderStatus
	attempt.FailureCode = unifiedRefundProviderRejectedFailureCode
	providerUpdatedAt := time.Date(2026, time.September, 16, 3, 9, 0, 123_000_000, time.UTC)
	attempt.ProviderUpdatedAt = &providerUpdatedAt
	attempt.NeedsManualReview = true
	require.NoError(t, saveUnifiedRefundAttempt(ctx, svc.entClient, attempt))
	return svc, order, subscription, attempt
}

func TestUnifiedRefundRecoveryIdempotencyKeysRotateOnlyForANewPauseGeneration(t *testing.T) {
	firstGeneration := time.Date(2026, time.September, 16, 3, 9, 0, 123_000_000, time.UTC)
	secondGeneration := firstGeneration.Add(time.Second)
	attempt := &unifiedRefundAttempt{
		ProductRefundNo:   "sub2-refund-80d5f249-1a39-4b87-b86e-95fb3d46a90e",
		ProviderUpdatedAt: &firstGeneration,
	}

	resumeFirst := unifiedRefundRecoveryIdempotencyKey("resume", attempt)
	confirmation := ExternalRefundConfirmationInput{
		MethodCode:        "wechat_transfer",
		ExternalReference: "wx-transfer_20260916:01",
		RefundedAt:        firstGeneration,
		EvidenceDetail:    "已核对收款人和退款金额",
	}
	externalFirst := unifiedRefundExternalConfirmationIdempotencyKey(attempt, confirmation)
	require.Equal(t, resumeFirst, unifiedRefundRecoveryIdempotencyKey("resume", attempt))
	require.Equal(t, externalFirst, unifiedRefundExternalConfirmationIdempotencyKey(attempt, confirmation))
	attempt.ProviderUpdatedAt = &secondGeneration
	require.NotEqual(t, resumeFirst, unifiedRefundRecoveryIdempotencyKey("resume", attempt))
	require.NotEqual(t, externalFirst, unifiedRefundExternalConfirmationIdempotencyKey(attempt, confirmation))

	// The central authority durably caches a deterministic 409. Correcting the
	// attested time or evidence must not replay that stale response, while an
	// exact transport retry must retain the same key.
	attempt.ProviderUpdatedAt = &firstGeneration
	corrected := confirmation
	corrected.RefundedAt = corrected.RefundedAt.Add(time.Second)
	require.NotEqual(t, externalFirst, unifiedRefundExternalConfirmationIdempotencyKey(attempt, corrected))
	corrected = confirmation
	corrected.EvidenceDetail = "已再次核对收款人和退款金额"
	require.NotEqual(t, externalFirst, unifiedRefundExternalConfirmationIdempotencyKey(attempt, corrected))
	require.LessOrEqual(t, len(externalFirst), 128)
}

func TestNormalizeExternalRefundConfirmationRejectsEvidenceThatCentralCannotPersist(t *testing.T) {
	base := ExternalRefundConfirmationInput{
		MethodCode:        "wechat_transfer",
		ExternalReference: "wx-transfer_20260916:01",
		RefundedAt:        time.Now().UTC(),
		EvidenceDetail:    "已核对收款人和退款金额",
	}
	_, err := normalizeExternalRefundConfirmation(base)
	require.NoError(t, err)

	for name, mutate := range map[string]func(*ExternalRefundConfirmationInput){
		"reference whitespace": func(input *ExternalRefundConfirmationInput) { input.ExternalReference = "wx transfer 1" },
		"json punctuation":     func(input *ExternalRefundConfirmationInput) { input.EvidenceDetail = `核对记录{"raw":"value"}` },
		"authorization":        func(input *ExternalRefundConfirmationInput) { input.EvidenceDetail = "Authorization bearer credential" },
		"phone number":         func(input *ExternalRefundConfirmationInput) { input.EvidenceDetail = "已核对收款人 13800138000" },
		"phone or id run":      func(input *ExternalRefundConfirmationInput) { input.EvidenceDetail = "已核对 138001380001234" },
		"implausibly old time": func(input *ExternalRefundConfirmationInput) { input.RefundedAt = time.Unix(0, 0).UTC() },
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			mutate(&input)
			_, err := normalizeExternalRefundConfirmation(input)
			require.Error(t, err)
		})
	}
}

func writeRefundRecoveryResponse(t *testing.T, writer http.ResponseWriter, attempt *unifiedRefundAttempt, status string, manual bool, providerStatus, failureCode string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	response := map[string]any{
		"environment": attempt.Environment, "organization_id": attempt.OrganizationID, "product_id": attempt.ProductID,
		"refund_request_id": attempt.RefundRequestID, "payment_order_id": attempt.PaymentOrderID,
		"product_refund_no": attempt.ProductRefundNo, "channel_out_refund_no": attempt.ChannelOutRefundNo,
		"amount_fen": attempt.AmountFen, "currency": payment.DefaultPaymentCurrency, "payment_method": attempt.PaymentMethod,
		"status": status, "needs_manual_review": manual, "created_at": now.Add(-time.Minute), "updated_at": now,
	}
	if providerStatus != "" {
		response["provider_status"] = providerStatus
	}
	if failureCode != "" {
		response["failure_code"] = failureCode
	}
	if status == unifiedpay.RefundStatusSucceeded || status == unifiedpay.RefundStatusFailed {
		response["completed_at"] = now
	}
	writer.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(writer).Encode(response))
}

func TestResumeUnifiedRefundClearsOnlyProvenBalancePauseAndKeepsReservation(t *testing.T) {
	runtimegate.SetProcessActive(true)
	ctx := context.Background()
	svc, order, _, attempt := prepareBalancePausedSubscriptionRefund(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "/v1/refund-requests/"+attempt.RefundRequestID+"/resume", request.URL.Path)
		writeRefundRecoveryResponse(t, writer, attempt, unifiedpay.RefundStatusUnknown, false,
			unifiedRefundBalanceInsufficientProviderStatus, unifiedRefundProviderRejectedFailureCode)
	}))
	defer server.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)

	result, err := svc.ResumeUnifiedRefund(ctx, order.ID, 71)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, 1, requests)
	stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.False(t, stored.NeedsManualReview)
	require.True(t, stored.EntitlementReserved)
	require.Equal(t, unifiedRefundPending, stored.Status)
	persisted, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefundPending, persisted.Status)
}

func TestConfirmExternalUnifiedRefundRecordsCentralSuccessBeforeCapturingEntitlement(t *testing.T) {
	runtimegate.SetProcessActive(true)
	ctx := context.Background()
	svc, order, subscription, attempt := prepareBalancePausedSubscriptionRefund(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "/v1/refund-requests/"+attempt.RefundRequestID+"/confirm-external", request.URL.Path)
		writeRefundRecoveryResponse(t, writer, attempt, unifiedpay.RefundStatusSucceeded, false,
			unifiedRefundManualExternalConfirmedStatus, "")
	}))
	defer server.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)

	result, err := svc.ConfirmExternalUnifiedRefund(ctx, order.ID, 71, ExternalRefundConfirmationInput{
		MethodCode: "wechat_transfer", ExternalReference: "wx-transfer-20260916-1",
		RefundedAt: time.Now().UTC().Add(-time.Minute), EvidenceDetail: "verified recipient and exact transfer amount",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedpay.RefundStatusSucceeded, stored.Status)
	require.False(t, stored.NeedsManualReview)
	require.False(t, stored.EntitlementReserved)
	persisted, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPartiallyRefunded, persisted.Status)
	grant, _, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
	require.NoError(t, err)
	require.Zero(t, grant.ReservedSeconds)
	require.Positive(t, grant.RefundedSeconds)
	currentSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
	require.NoError(t, err)
	require.True(t, currentSubscription.ExpiresAt.Before(grant.OriginalEnd))
}

func TestConfirmExternalUnifiedRefundRepairsLegacyWholeSecondFutureTermReservation(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	ctx := context.Background()
	svc, order, subscription, _, _ := newReviewedSubscriptionRefundFixture(t)
	termStart := time.Date(2026, time.October, 12, 15, 13, 43, 41_622_000, time.UTC)
	termEnd := termStart.AddDate(0, 0, 30)
	svc.refundReviewNow = func() time.Time { return termStart.Add(-24 * time.Hour) }

	_, err := svc.entClient.ExecContext(ctx, `UPDATE payment_subscription_grants SET
		term_start_at=$2, original_term_end_at=$3, current_term_end_at=$3
		WHERE payment_order_id=$1`, order.ID, termStart, termEnd)
	require.NoError(t, err)
	_, err = svc.entClient.UserSubscription.UpdateOneID(subscription.ID).
		SetStartsAt(termStart.AddDate(0, 0, -30)).
		SetExpiresAt(termEnd).
		Save(ctx)
	require.NoError(t, err)

	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, review.CanRefund)
	require.NotNil(t, review.Subscription)
	require.Equal(t, termStart, review.Subscription.NewExpiresAt)
	plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "legacy whole-second reservation")
	require.NoError(t, err)
	attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.NoError(t, err)

	// e98 preserves the sub-second boundary for new reservations. This models
	// the sole older shape we can prove: an already-held future term that was
	// truncated to the same whole second before its exact grant start.
	legacyExpiry := termStart.Truncate(time.Second)
	require.True(t, legacyExpiry.Before(termStart))
	require.True(t, timeEqualToSecond(legacyExpiry, termStart))
	_, err = svc.entClient.UserSubscription.UpdateOneID(subscription.ID).SetExpiresAt(legacyExpiry).Save(ctx)
	require.NoError(t, err)
	attempt.ValuationAt = &legacyExpiry
	attempt.RefundRequestID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	attempt.ChannelOutRefundNo = "sandbox_sub2_refund_recovery_legacy_boundary"
	attempt.ProviderStatus = unifiedRefundBalanceInsufficientProviderStatus
	attempt.FailureCode = unifiedRefundProviderRejectedFailureCode
	providerUpdatedAt := time.Date(2026, time.September, 16, 3, 9, 0, 123_000_000, time.UTC)
	attempt.ProviderUpdatedAt = &providerUpdatedAt
	attempt.NeedsManualReview = true
	require.NoError(t, saveUnifiedRefundAttempt(ctx, svc.entClient, attempt))
	_, err = svc.entClient.ExecContext(ctx, `UPDATE unified_payment_refund_attempts
		SET valuation_at=$2 WHERE product_refund_no=$1`, attempt.ProductRefundNo, legacyExpiry)
	require.NoError(t, err)
	installSubscriptionGrantCurrentEndGuard(t, svc.entClient)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "/v1/refund-requests/"+attempt.RefundRequestID+"/confirm-external", request.URL.Path)
		writeRefundRecoveryResponse(t, writer, attempt, unifiedpay.RefundStatusSucceeded, false,
			unifiedRefundManualExternalConfirmedStatus, "")
	}))
	defer server.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)

	result, err := svc.ConfirmExternalUnifiedRefund(ctx, order.ID, 71, ExternalRefundConfirmationInput{
		MethodCode: "wechat_transfer", ExternalReference: "wx-transfer-20260916-legacy-boundary",
		RefundedAt: time.Now().UTC().Add(-time.Minute), EvidenceDetail: "verified recipient and exact transfer amount",
	})
	require.NoError(t, err)
	require.True(t, result.Success)

	grant, _, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
	require.NoError(t, err)
	require.Equal(t, termStart, grant.CurrentEnd)
	require.Zero(t, grant.ReservedSeconds)
	require.Positive(t, grant.RefundedSeconds)
	finalSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
	require.NoError(t, err)
	require.Equal(t, termStart, finalSubscription.ExpiresAt)
	stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedpay.RefundStatusSucceeded, stored.Status)
	require.False(t, stored.EntitlementReserved)
}

func TestResumeUnifiedRefundRecoversCentralSuccessAfterLostResponse(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	ctx := context.Background()
	svc, order, subscription, attempt := prepareBalancePausedSubscriptionRefund(t)
	originalExpiry := subscription.ExpiresAt
	postCalls, getCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/refund-requests/"+attempt.RefundRequestID+"/resume":
			// The central command has committed, but its response was lost before
			// Sub2 could apply it or receive a terminal webhook.
			postCalls++
			writer.WriteHeader(http.StatusServiceUnavailable)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/refund-requests/"+attempt.RefundRequestID:
			getCalls++
			writeRefundRecoveryResponse(t, writer, attempt, unifiedpay.RefundStatusSucceeded, false, "SUCCESS", "")
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)

	result, err := svc.ResumeUnifiedRefund(ctx, order.ID, 71)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, postCalls)
	require.Equal(t, 1, getCalls)

	stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedpay.RefundStatusSucceeded, stored.Status)
	require.False(t, stored.NeedsManualReview)
	require.False(t, stored.EntitlementReserved)
	grant, _, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
	require.NoError(t, err)
	require.Zero(t, grant.ReservedSeconds)
	require.Positive(t, grant.RefundedSeconds)
	updatedSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
	require.NoError(t, err)
	require.True(t, updatedSubscription.ExpiresAt.Before(originalExpiry))
	successes, err := svc.entClient.PaymentAuditLog.Query().Where(paymentauditlog.ActionEQ("REFUND_SUCCESS")).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, successes)
}

func TestResumeUnifiedRefundConvergesAfterDifferentAdminIdempotencyConflict(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	ctx := context.Background()
	svc, order, subscription, attempt := prepareBalancePausedSubscriptionRefund(t)
	reservedSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
	require.NoError(t, err)
	postCalls, getCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/refund-requests/"+attempt.RefundRequestID+"/resume":
			// A different administrator reached the central command first. The
			// current request must read the authoritative resource, not create a
			// second refund or leave the local manual fence forever.
			postCalls++
			writer.WriteHeader(http.StatusConflict)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/refund-requests/"+attempt.RefundRequestID:
			getCalls++
			writeRefundRecoveryResponse(t, writer, attempt, unifiedpay.RefundStatusProcessing, false, "PROCESSING", "")
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)

	result, err := svc.ResumeUnifiedRefund(ctx, order.ID, 72)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, 1, postCalls)
	require.Equal(t, 1, getCalls)
	stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedRefundPending, stored.Status)
	require.False(t, stored.NeedsManualReview)
	require.True(t, stored.EntitlementReserved)
	persisted, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefundPending, persisted.Status)
	grant, _, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
	require.NoError(t, err)
	require.Positive(t, grant.ReservedSeconds)
	require.Zero(t, grant.RefundedSeconds)
	currentSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
	require.NoError(t, err)
	require.Equal(t, reservedSubscription.ExpiresAt, currentSubscription.ExpiresAt)
}

func TestResumeUnifiedRefundKeepsBalanceFenceWhenCentralIsStillManual(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	ctx := context.Background()
	svc, order, subscription, attempt := prepareBalancePausedSubscriptionRefund(t)
	reservedSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/refund-requests/"+attempt.RefundRequestID+"/resume":
			writer.WriteHeader(http.StatusServiceUnavailable)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/refund-requests/"+attempt.RefundRequestID:
			writeRefundRecoveryResponse(t, writer, attempt, unifiedpay.RefundStatusUnknown, true,
				unifiedRefundBalanceInsufficientProviderStatus, unifiedRefundProviderRejectedFailureCode)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)

	result, err := svc.ResumeUnifiedRefund(ctx, order.ID, 71)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Warning, "manual review")
	stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedRefundPending, stored.Status)
	require.True(t, stored.NeedsManualReview)
	require.True(t, stored.EntitlementReserved)
	grant, _, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
	require.NoError(t, err)
	require.Positive(t, grant.ReservedSeconds)
	require.Zero(t, grant.RefundedSeconds)
	currentSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
	require.NoError(t, err)
	require.Equal(t, reservedSubscription.ExpiresAt, currentSubscription.ExpiresAt)
}

func TestResumeUnifiedRefundKeepsBalanceFenceWhenCentralCommandWasNotCommitted(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	ctx := context.Background()
	svc, order, subscription, attempt := prepareBalancePausedSubscriptionRefund(t)
	reservedSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
	require.NoError(t, err)
	postCalls, getCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/refund-requests/"+attempt.RefundRequestID+"/resume":
			postCalls++
			writer.WriteHeader(http.StatusServiceUnavailable)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/refund-requests/"+attempt.RefundRequestID:
			getCalls++
			// The request is unavailable at central, so no trusted resource
			// proves that the local provider-balance fence may be released.
			writer.WriteHeader(http.StatusNotFound)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)

	result, err := svc.ResumeUnifiedRefund(ctx, order.ID, 71)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Warning, "unconfirmed")
	require.Equal(t, 1, postCalls)
	require.Equal(t, 1, getCalls)
	stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.True(t, stored.NeedsManualReview)
	require.True(t, stored.EntitlementReserved)
	grant, _, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
	require.NoError(t, err)
	require.Positive(t, grant.ReservedSeconds)
	require.Zero(t, grant.RefundedSeconds)
	currentSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
	require.NoError(t, err)
	require.Equal(t, reservedSubscription.ExpiresAt, currentSubscription.ExpiresAt)
}

func TestRefundRecoveryDoesNotBypassExistingTerminalConflictFence(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	ctx := context.Background()
	svc, order, _, attempt := prepareBalancePausedSubscriptionRefund(t)
	require.NoError(t, writeUnifiedRefundAudit(ctx, svc.entClient, order.ID, "UNIFIED_REFUND_CONFLICT", map[string]any{
		"product_refund_no": attempt.ProductRefundNo,
		"reason":            "refund_terminal_conflict",
	}))
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)

	_, err := svc.ResumeUnifiedRefund(ctx, order.ID, 71)
	require.Error(t, err)
	require.Equal(t, "REFUND_RECOVERY_CONFLICT", infraerrors.Reason(err))
	require.Zero(t, calls)
	stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.True(t, stored.NeedsManualReview)
	require.True(t, stored.EntitlementReserved)

	providerStatus := unifiedRefundManualExternalConfirmedStatus
	resource := unifiedRefundFixtureResource(attempt, unifiedpay.RefundStatusSucceeded)
	resource.ChannelOutRefundNo = attempt.ChannelOutRefundNo
	resource.ProviderStatus = &providerStatus
	result, err := svc.applyUnifiedRefundResource(ctx, order.ID, resource, "webhook")
	require.NoError(t, err)
	require.False(t, result.Success)
	stored, err = loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.True(t, stored.NeedsManualReview)
	require.True(t, stored.EntitlementReserved)
}

func TestConfirmExternalUnifiedRefundRecoversLostSuccessResponseExactlyOnce(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	ctx := context.Background()
	svc, order, subscription, attempt := prepareBalancePausedSubscriptionRefund(t)
	postCalls, getCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/refund-requests/"+attempt.RefundRequestID+"/confirm-external":
			postCalls++
			writer.WriteHeader(http.StatusServiceUnavailable)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/refund-requests/"+attempt.RefundRequestID:
			getCalls++
			writeRefundRecoveryResponse(t, writer, attempt, unifiedpay.RefundStatusSucceeded, false,
				unifiedRefundManualExternalConfirmedStatus, "")
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, server.URL), nil)

	result, err := svc.ConfirmExternalUnifiedRefund(ctx, order.ID, 71, ExternalRefundConfirmationInput{
		MethodCode: "wechat_transfer", ExternalReference: "wx-transfer-20260916-2",
		RefundedAt: time.Now().UTC().Add(-time.Minute), EvidenceDetail: "verified recipient and exact transfer amount",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, postCalls)
	require.Equal(t, 1, getCalls)
	stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedpay.RefundStatusSucceeded, stored.Status)
	require.False(t, stored.EntitlementReserved)
	grant, _, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
	require.NoError(t, err)
	require.Zero(t, grant.ReservedSeconds)
	require.Positive(t, grant.RefundedSeconds)
	firstRefundedSeconds := grant.RefundedSeconds

	providerStatus := unifiedRefundManualExternalConfirmedStatus
	replay := unifiedRefundFixtureResource(attempt, unifiedpay.RefundStatusSucceeded)
	replay.ChannelOutRefundNo = attempt.ChannelOutRefundNo
	replay.ProviderStatus = &providerStatus
	result, err = svc.applyUnifiedRefundResource(ctx, order.ID, replay, "webhook")
	require.NoError(t, err)
	require.True(t, result.Success)
	grant, _, err = loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
	require.NoError(t, err)
	require.Equal(t, firstRefundedSeconds, grant.RefundedSeconds)
	successes, err := svc.entClient.PaymentAuditLog.Query().Where(paymentauditlog.ActionEQ("REFUND_SUCCESS")).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, successes)
	currentSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
	require.NoError(t, err)
	require.True(t, currentSubscription.ExpiresAt.Before(grant.OriginalEnd))
}

func TestReviewedBalanceRefundReviewSeparatesPaidAndGift(t *testing.T) {
	svc, order := newReviewedBalanceRefundFixture(t, 100, 0.1)
	review, err := svc.ReviewRefund(context.Background(), order.ID)
	require.NoError(t, err)
	require.True(t, review.CanRefund)
	require.False(t, review.RequiresManualReview)
	require.InDelta(t, 0.1, review.DefaultRefundAmount, 1e-9)
	require.InDelta(t, 100, review.EntitlementAmount, 1e-9)
	require.NotNil(t, review.Balance)
	require.InDelta(t, 0.1, review.Balance.OriginalPaidCredit, 1e-9)
	require.InDelta(t, 99.9, review.Balance.OriginalGiftCredit, 1e-9)
	require.InDelta(t, 0.1, review.Balance.PaidCreditToReclaim, 1e-9)
	require.InDelta(t, 99.9, review.Balance.GiftCreditToReclaim, 1e-9)
}

func TestReviewedRefundRolloutGatesFailBeforeReservationOrProvider(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enabled    bool
		state      string
		wantReason string
	}{
		{name: "disabled by default", enabled: false, state: runtimegate.StateActive, wantReason: "REVIEWED_REFUNDS_DISABLED"},
		{name: "draining generation", enabled: true, state: runtimegate.StateStandby, wantReason: "REFUND_ADMISSION_DRAINING"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtimegate.SetProcessActive(true)
			t.Cleanup(func() { runtimegate.SetProcessActive(true) })
			stateFile := t.TempDir() + "/background-state"
			require.NoError(t, os.WriteFile(stateFile, []byte(tc.state+"\n"), 0o600))
			t.Setenv(runtimegate.StateFileEnv, stateFile)

			ctx := context.Background()
			svc, order, subscription, _, originalEnd := newReviewedSubscriptionRefundFixture(t)
			if !tc.enabled {
				svc.configService = nil
			}
			review, err := svc.ReviewRefund(ctx, order.ID)
			require.NoError(t, err)
			plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "rollout gate test")
			require.NoError(t, err)

			providerCalls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				providerCalls++
			}))
			defer provider.Close()
			svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, provider.URL), nil)

			result, err := svc.ExecuteRefund(ctx, plan)
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, tc.wantReason, infraerrors.Reason(err))
			require.Zero(t, providerCalls)

			persistedOrder, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusCompleted, persistedOrder.Status)
			persistedSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
			require.NoError(t, err)
			require.True(t, persistedSubscription.ExpiresAt.Equal(originalEnd))
			rows, err := svc.entClient.QueryContext(ctx, `SELECT COUNT(*) FROM unified_payment_refund_attempts WHERE order_id=$1`, order.ID)
			require.NoError(t, err)
			require.True(t, rows.Next())
			var attempts int
			require.NoError(t, rows.Scan(&attempts))
			require.NoError(t, rows.Close())
			require.Zero(t, attempts)
		})
	}
}

func TestReviewedRefundProviderCreateHonorsRolloutGateButKnownRemoteQueryConverges(t *testing.T) {
	runtimegate.SetProcessActive(true)
	t.Cleanup(func() { runtimegate.SetProcessActive(true) })
	t.Setenv(runtimegate.StateFileEnv, "")

	ctx := context.Background()
	svc, order := newReviewedBalanceRefundFixture(t, 100, 0.1)
	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "provider rollout gate test")
	require.NoError(t, err)
	attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.NoError(t, err)

	var methods []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		status := unifiedpay.RefundStatusUnknown
		if r.Method == http.MethodGet {
			status = unifiedpay.RefundStatusSucceeded
		}
		writePaymentRefundReconciliationResponse(t, w, r, attempt, status, false)
	}))
	defer provider.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, provider.URL), nil)

	enabledConfig := svc.configService
	svc.configService = nil
	result, err := svc.advanceUnifiedRefund(ctx, attempt)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, "REVIEWED_REFUNDS_DISABLED", infraerrors.Reason(err))
	require.Empty(t, methods, "a disabled rollout must not create a provider refund")
	pending, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedRefundPending, pending.Status)
	require.Empty(t, pending.RefundRequestID)
	require.True(t, pending.EntitlementReserved)

	// Explicit activation may create the durable remote request. An UNKNOWN
	// response persists its ID, after which disabling creation must still allow
	// GET-only convergence for money that may already have moved.
	svc.configService = enabledConfig
	result, err = svc.advanceUnifiedRefund(ctx, pending)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, []string{http.MethodPost}, methods)
	pending, err = loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.NotEmpty(t, pending.RefundRequestID)

	svc.configService = nil
	result, err = svc.advanceUnifiedRefund(ctx, pending)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, []string{http.MethodPost, http.MethodGet}, methods)
}

func TestReviewedSubscriptionRefundInvalidatesAuthCacheAfterReserveAndRelease(t *testing.T) {
	ctx := context.Background()
	svc, order, _, _, _ := newReviewedSubscriptionRefundFixture(t)
	invalidator := &refundAuthCacheInvalidatorSpy{}
	svc.SetAuthCacheInvalidator(invalidator)
	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "cache invalidation")
	require.NoError(t, err)
	attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, []int64{order.UserID}, invalidator.userIDs)

	_, err = svc.applyUnifiedRefundResource(ctx, order.ID, unifiedRefundFixtureResource(attempt, unifiedpay.RefundStatusFailed), "test")
	require.NoError(t, err)
	require.Equal(t, []int64{order.UserID, order.UserID}, invalidator.userIDs)
}

func TestReviewedSubscriptionRefundWaitsForAuthorizationCacheInvalidationBeforeProvider(t *testing.T) {
	ctx := context.Background()
	svc, order, _, _, _ := newReviewedSubscriptionRefundFixture(t)
	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "strict cache boundary")
	require.NoError(t, err)
	attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.NoError(t, err)

	cache, ok := svc.subscriptionSvc.billingCacheService.cache.(*fencedSubscriptionCacheStub)
	require.True(t, ok)
	cache.mu.Lock()
	cache.publishErr = errors.New("redis publish unavailable")
	cache.mu.Unlock()

	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls++
		writePaymentRefundReconciliationResponse(t, w, r, attempt, unifiedpay.RefundStatusApproved, false)
	}))
	defer provider.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, provider.URL), nil)

	result, err := svc.advanceUnifiedRefund(ctx, attempt)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Zero(t, providerCalls, "money must not move until subscription authorization caches are fenced")
	pending, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedRefundPending, pending.Status)
	require.True(t, pending.EntitlementReserved)
	rows, err := svc.entClient.QueryContext(ctx, `SELECT COUNT(*) FROM unified_payment_refund_events
		WHERE order_id=$1 AND action='UNIFIED_REFUND_CACHE_INVALIDATION_PENDING'`, order.ID)
	require.NoError(t, err)
	require.True(t, rows.Next())
	var blockedAudits int
	require.NoError(t, rows.Scan(&blockedAudits))
	require.NoError(t, rows.Close())
	require.Equal(t, 1, blockedAudits)

	cache.mu.Lock()
	cache.publishErr = nil
	cache.mu.Unlock()
	result, err = svc.advanceUnifiedRefund(ctx, attempt)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, 1, providerCalls, "the same durable attempt may proceed after cache convergence")
}

func TestReviewedBalanceRefundWaitsForAuthorizationCacheInvalidationBeforeProvider(t *testing.T) {
	ctx := context.Background()
	svc, order := newReviewedBalanceRefundFixture(t, 100, 0.1)
	cache := &refundBalanceAuthorizationCacheSpy{}
	svc.SetBalanceAuthorizationCacheInvalidator(cache)
	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "strict balance cache boundary")
	require.NoError(t, err)
	attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, []int64{order.UserID}, cache.userIDs, "reserve should promptly invalidate the previous balance authorization")

	cache.err = errors.New("redis balance fence unavailable")
	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls++
		writePaymentRefundReconciliationResponse(t, w, r, attempt, unifiedpay.RefundStatusApproved, false)
	}))
	defer provider.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, provider.URL), nil)

	result, err := svc.advanceUnifiedRefund(ctx, attempt)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Zero(t, providerCalls, "money must not move until the balance authorization cache fence advances")
	pending, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedRefundPending, pending.Status)
	require.True(t, pending.EntitlementReserved)

	cache.err = nil
	result, err = svc.advanceUnifiedRefund(ctx, attempt)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, 1, providerCalls, "the same durable attempt may retry after balance cache convergence")
	require.Equal(t, []int64{order.UserID, order.UserID, order.UserID, order.UserID}, cache.userIDs,
		"reserve, each provider boundary, and the persisted provider observation advance the balance fence")
}

func TestReviewedBalanceRefundInvalidatesAuthorizationAfterProviderFailure(t *testing.T) {
	ctx := context.Background()
	svc, order := newReviewedBalanceRefundFixture(t, 100, 0.1)
	cache := &refundBalanceAuthorizationCacheSpy{}
	svc.SetBalanceAuthorizationCacheInvalidator(cache)
	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "release balance cache boundary")
	require.NoError(t, err)
	attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.NoError(t, err)

	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls++
		writePaymentRefundReconciliationResponse(t, w, r, attempt, unifiedpay.RefundStatusFailed, false)
	}))
	defer provider.Close()
	svc.SetUnifiedPayment(newUnifiedServiceTestGateway(t, provider.URL), nil)

	result, err := svc.advanceUnifiedRefund(ctx, attempt)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, 1, providerCalls)
	pending, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	require.Equal(t, unifiedpay.RefundStatusFailed, pending.Status)
	require.False(t, pending.EntitlementReserved)
	_, user := loadReviewedBalanceRefundState(t, svc, order)
	require.InDelta(t, 100, user.Balance, 1e-9)
	require.InDelta(t, 0, user.FrozenBalance, 1e-9)
	require.Equal(t, []int64{order.UserID, order.UserID, order.UserID}, cache.userIDs,
		"reserve, provider boundary, and terminal release must each invalidate balance authorization")
}

func TestReviewedSubscriptionRefundRejectsElapsedReviewWindowWhenCashIsUnchanged(t *testing.T) {
	ctx := context.Background()
	svc, order, _, start, _ := newReviewedSubscriptionRefundFixture(t)
	current := start.Add(45*24*time.Hour + 5*time.Minute).UTC().Truncate(time.Minute)
	svc.refundReviewNow = func() time.Time { return current }

	first, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, first.CanRefund)
	require.NotNil(t, first.Subscription)

	current = current.Add(refundReviewValuationWindow)
	second, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, first.DefaultRefundAmount, second.DefaultRefundAmount,
		"the regression must stay inside the same cash-rounding interval")
	require.Equal(t, first.EntitlementAmount, second.EntitlementAmount,
		"the regression must stay inside the same product-rounding interval")
	require.NotEqual(t, first.Subscription.RemainingSeconds, second.Subscription.RemainingSeconds)
	require.NotEqual(t, first.Subscription.NewExpiresAt, second.Subscription.NewExpiresAt)
	require.NotEqual(t, first.QuoteRevision, second.QuoteRevision)

	_, err = svc.PrepareReviewedRefund(ctx, order.ID, first.QuoteRevision, "elapsed review window")
	require.Error(t, err)
	require.Equal(t, "REFUND_QUOTE_STALE", infraerrors.Reason(err))

	persisted, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, persisted.Status)
}

func TestReviewedBalanceRefundReviewReturnsZeroAfterPaidPrincipalIsConsumed(t *testing.T) {
	svc, order := newReviewedBalanceRefundFixture(t, 99.9, 0)
	review, err := svc.ReviewRefund(context.Background(), order.ID)
	require.NoError(t, err)
	require.False(t, review.CanRefund)
	require.False(t, review.RequiresManualReview)
	require.Equal(t, "PAID_BALANCE_CONSUMED", review.ReasonCode)
	require.Zero(t, review.DefaultRefundAmount)
	require.Zero(t, review.EntitlementAmount)
	require.NotNil(t, review.Balance)
	require.Zero(t, review.Balance.PaidCreditToReclaim)
	require.Zero(t, review.Balance.GiftCreditToReclaim)
}

func TestReviewedRefundReservationRejectsExpiredQuoteWithoutMovingBalance(t *testing.T) {
	ctx := context.Background()
	svc, order := newReviewedBalanceRefundFixture(t, 100, 0.1)
	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "quote expiry test")
	require.NoError(t, err)
	_, err = svc.entClient.ExecContext(ctx, `UPDATE payment_wallet_fundings SET version = version + 1 WHERE payment_order_id = $1`, order.ID)
	require.NoError(t, err)

	attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.Error(t, err)
	require.Nil(t, attempt)
	require.Equal(t, "REFUND_QUOTE_STALE", infraerrors.Reason(err))
	funding, user := loadReviewedBalanceRefundState(t, svc, order)
	require.InDelta(t, 100, user.Balance, 1e-9)
	require.Zero(t, user.FrozenBalance)
	require.InDelta(t, 0.1, user.WalletAvailablePaid, 1e-9)
	require.Zero(t, user.WalletFrozenPaid)
	require.True(t, funding.ReservedPaid.IsZero())
	require.True(t, funding.ReservedGift.IsZero())
	require.Zero(t, funding.ReservedCashMinor)
	persisted, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, persisted.Status)
}

func TestReviewedBalanceRefundReservationIsAtomicAndTerminalPathsSettleOrRelease(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status          string
		wantOrderStatus string
		wantSuccess     bool
	}{
		{name: "success captures frozen entitlement", status: unifiedpay.RefundStatusSucceeded, wantOrderStatus: OrderStatusRefunded, wantSuccess: true},
		{name: "failure releases frozen entitlement", status: unifiedpay.RefundStatusFailed, wantOrderStatus: OrderStatusRefundFailed, wantSuccess: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc, order := newReviewedBalanceRefundFixture(t, 100, 0.1)
			review, err := svc.ReviewRefund(ctx, order.ID)
			require.NoError(t, err)
			plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "reviewed balance refund")
			require.NoError(t, err)
			attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
			require.NoError(t, err)
			require.True(t, attempt.EntitlementReserved)
			require.Equal(t, refundReviewKindBalance, attempt.RefundKind)
			funding, user := loadReviewedBalanceRefundState(t, svc, order)
			require.Zero(t, user.Balance)
			require.InDelta(t, 100, user.FrozenBalance, 1e-9)
			require.Zero(t, user.WalletAvailablePaid)
			require.InDelta(t, 0.1, user.WalletFrozenPaid, 1e-9)
			require.True(t, funding.ReservedPaid.Equal(decimal.RequireFromString("0.10")))
			require.True(t, funding.ReservedGift.Equal(decimal.RequireFromString("99.90")))
			require.Equal(t, int64(10), funding.ReservedCashMinor)

			result, err := svc.applyUnifiedRefundResource(ctx, order.ID, unifiedRefundFixtureResource(attempt, tc.status), "test")
			require.NoError(t, err)
			require.Equal(t, tc.wantSuccess, result.Success)
			funding, user = loadReviewedBalanceRefundState(t, svc, order)
			require.True(t, funding.ReservedPaid.IsZero())
			require.True(t, funding.ReservedGift.IsZero())
			require.Zero(t, funding.ReservedCashMinor)
			stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
			require.NoError(t, err)
			require.False(t, stored.EntitlementReserved)
			persisted, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, tc.wantOrderStatus, persisted.Status)
			if tc.wantSuccess {
				require.Zero(t, user.Balance)
				require.Zero(t, user.FrozenBalance)
				require.Zero(t, user.WalletAvailablePaid)
				require.Zero(t, user.WalletFrozenPaid)
				require.True(t, funding.RefundedPaid.Equal(decimal.RequireFromString("0.10")))
				require.True(t, funding.ReclaimedGift.Equal(decimal.RequireFromString("99.90")))
				require.Equal(t, int64(10), funding.RefundedCashMinor)
			} else {
				require.InDelta(t, 100, user.Balance, 1e-9)
				require.Zero(t, user.FrozenBalance)
				require.InDelta(t, 0.1, user.WalletAvailablePaid, 1e-9)
				require.Zero(t, user.WalletFrozenPaid)
				require.True(t, funding.RefundedPaid.IsZero())
				require.True(t, funding.ReclaimedGift.IsZero())
				require.Zero(t, funding.RefundedCashMinor)
			}
		})
	}
}

func TestReviewedBalanceRefundReservationRollsBackAsOneTransaction(t *testing.T) {
	ctx := context.Background()
	svc, order := newReviewedBalanceRefundFixture(t, 100, 0.1)
	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "atomic rollback test")
	require.NoError(t, err)
	_, err = svc.entClient.ExecContext(ctx, `DROP TABLE unified_payment_refund_events`)
	require.NoError(t, err)

	_, err = svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.Error(t, err)
	funding, user := loadReviewedBalanceRefundState(t, svc, order)
	require.InDelta(t, 100, user.Balance, 1e-9)
	require.Zero(t, user.FrozenBalance)
	require.InDelta(t, 0.1, user.WalletAvailablePaid, 1e-9)
	require.Zero(t, user.WalletFrozenPaid)
	require.True(t, funding.ReservedPaid.IsZero())
	require.True(t, funding.ReservedGift.IsZero())
	require.Zero(t, funding.ReservedCashMinor)
}

func TestReviewedSubscriptionRefundUsesSecondsAndRestoresFailedReservation(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status          string
		wantOrderStatus string
		wantSuccess     bool
	}{
		{name: "success captures shortened term", status: unifiedpay.RefundStatusSucceeded, wantOrderStatus: OrderStatusPartiallyRefunded, wantSuccess: true},
		{name: "failure restores original expiry", status: unifiedpay.RefundStatusFailed, wantOrderStatus: OrderStatusRefundFailed, wantSuccess: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc, order, subscription, start, end := newReviewedSubscriptionRefundFixture(t)
			review, err := svc.ReviewRefund(ctx, order.ID)
			require.NoError(t, err)
			require.True(t, review.CanRefund)
			require.NotNil(t, review.Subscription)
			totalSeconds := int64(end.Sub(start) / time.Second)
			expectedSeconds := int64(end.Sub(review.Subscription.NewExpiresAt) / time.Second)
			expectedAmount := decimal.NewFromInt(120).
				Mul(decimal.NewFromInt(expectedSeconds)).
				Div(decimal.NewFromInt(totalSeconds)).
				Truncate(2).
				InexactFloat64()
			require.Equal(t, expectedSeconds, review.Subscription.RemainingSeconds)
			require.InDelta(t, expectedAmount, review.EntitlementAmount, 1e-9)
			require.InDelta(t, expectedAmount, review.DefaultRefundAmount, 1e-9)

			plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "reviewed subscription refund")
			require.NoError(t, err)
			attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
			require.NoError(t, err)
			require.True(t, attempt.EntitlementReserved)
			require.Equal(t, refundReviewKindSubscription, attempt.RefundKind)
			require.Positive(t, attempt.SubscriptionSeconds)
			require.LessOrEqual(t, attempt.SubscriptionSeconds, totalSeconds)
			require.NotNil(t, attempt.ValuationAt)
			reservedSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
			require.NoError(t, err)
			require.True(t, timeEqualToSecond(reservedSubscription.ExpiresAt, *attempt.ValuationAt))
			require.True(t, reservedSubscription.ExpiresAt.Before(end))
			grant, _, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
			require.NoError(t, err)
			require.Equal(t, attempt.SubscriptionSeconds, grant.ReservedSeconds)
			require.True(t, timeEqualToSecond(grant.CurrentEnd, end))

			result, err := svc.applyUnifiedRefundResource(ctx, order.ID, unifiedRefundFixtureResource(attempt, tc.status), "test")
			require.NoError(t, err)
			require.Equal(t, tc.wantSuccess, result.Success)
			stored, err := loadUnifiedRefundAttempt(ctx, svc.entClient, order.ID, attempt.ProductRefundNo)
			require.NoError(t, err)
			require.False(t, stored.EntitlementReserved)
			finalSubscription, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
			require.NoError(t, err)
			grant, _, err = loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
			require.NoError(t, err)
			require.Zero(t, grant.ReservedSeconds)
			persisted, err := svc.entClient.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, tc.wantOrderStatus, persisted.Status)
			if tc.wantSuccess {
				require.True(t, timeEqualToSecond(finalSubscription.ExpiresAt, *attempt.ValuationAt))
				require.True(t, timeEqualToSecond(grant.CurrentEnd, *attempt.ValuationAt))
				require.Equal(t, attempt.SubscriptionSeconds, grant.RefundedSeconds)
				require.Equal(t, attempt.AmountFen, grant.RefundedCashMinor)
			} else {
				require.True(t, timeEqualToSecond(finalSubscription.ExpiresAt, end))
				require.True(t, timeEqualToSecond(grant.CurrentEnd, end))
				require.Zero(t, grant.RefundedSeconds)
				require.Zero(t, grant.RefundedCashMinor)
			}
		})
	}
}

func TestReviewedFutureSubscriptionRefundPreservesSubsecondGrantBoundary(t *testing.T) {
	ctx := context.Background()
	svc, order, subscription, _, _ := newReviewedSubscriptionRefundFixture(t)
	termStart := time.Date(2026, time.October, 12, 15, 13, 43, 41_622_000, time.UTC)
	termEnd := termStart.AddDate(0, 0, 30)
	svc.refundReviewNow = func() time.Time { return termStart.Add(-24 * time.Hour) }

	_, err := svc.entClient.ExecContext(ctx, `UPDATE payment_subscription_grants SET
		term_start_at=$2, original_term_end_at=$3, current_term_end_at=$3
		WHERE payment_order_id=$1`, order.ID, termStart, termEnd)
	require.NoError(t, err)
	_, err = svc.entClient.UserSubscription.UpdateOneID(subscription.ID).
		SetStartsAt(termStart.AddDate(0, 0, -30)).
		SetExpiresAt(termEnd).
		Save(ctx)
	require.NoError(t, err)

	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, review.CanRefund)
	require.NotNil(t, review.Subscription)
	require.True(t, review.Subscription.NewExpiresAt.Equal(termStart))

	plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "future term refund")
	require.NoError(t, err)
	attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
	require.NoError(t, err)
	require.NotNil(t, attempt.ValuationAt)
	require.True(t, attempt.ValuationAt.Equal(termStart))

	reserved, err := svc.entClient.UserSubscription.Get(ctx, subscription.ID)
	require.NoError(t, err)
	require.True(t, reserved.ExpiresAt.Equal(termStart))
}

func TestReviewedRefundRequiresManualReviewWhenHistoricalProvenanceIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		newFixture func(*testing.T) (*PaymentService, *dbent.PaymentOrder)
		remove     func(context.Context, *PaymentService, *dbent.PaymentOrder) error
		wantReason string
	}{
		{
			name: "balance",
			newFixture: func(t *testing.T) (*PaymentService, *dbent.PaymentOrder) {
				client := newUnifiedRefundSQLiteClient(t)
				svc, order, _ := newUnifiedRefundFixture(t, client, payment.TypeAlipay)
				return svc, order
			},
			remove:     func(context.Context, *PaymentService, *dbent.PaymentOrder) error { return nil },
			wantReason: "LEGACY_BALANCE_UNATTRIBUTED",
		},
		{
			name: "subscription",
			newFixture: func(t *testing.T) (*PaymentService, *dbent.PaymentOrder) {
				svc, order, _, _, _ := newReviewedSubscriptionRefundFixture(t)
				return svc, order
			},
			remove: func(ctx context.Context, svc *PaymentService, order *dbent.PaymentOrder) error {
				_, err := svc.entClient.ExecContext(ctx, `DELETE FROM payment_subscription_grants WHERE payment_order_id = $1`, order.ID)
				return err
			},
			wantReason: "LEGACY_SUBSCRIPTION_UNATTRIBUTED",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc, order := tc.newFixture(t)
			require.NoError(t, tc.remove(ctx, svc, order))
			review, err := svc.ReviewRefund(ctx, order.ID)
			require.NoError(t, err)
			require.True(t, review.RequiresManualReview)
			require.False(t, review.CanRefund)
			require.Equal(t, tc.wantReason, review.ReasonCode)
			_, err = svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "legacy order")
			require.Error(t, err)
			require.Equal(t, "REFUND_REQUIRES_MANUAL_REVIEW", infraerrors.Reason(err))
		})
	}
}

func TestPaymentWalletFundingForOrderUsesImmutablePaidGiftSplit(t *testing.T) {
	order := &dbent.PaymentOrder{
		ID: 31, UserID: 41, OrderType: payment.OrderTypeBalance, Amount: 100, PayAmount: 0.1,
		ProductSnapshot: map[string]any{
			"schema_version": 2, "kind": paymentSnapshotKindBalance,
			"credited_amount": 100.0, "paid_credit_amount": 0.1, "gift_credit_amount": 99.9,
		},
	}
	input, err := PaymentWalletFundingForOrder(order)
	require.NoError(t, err)
	require.Equal(t, order.ID, input.OrderID)
	require.Equal(t, order.UserID, input.UserID)
	require.InDelta(t, 0.1, input.PaidCredit, 1e-9)
	require.InDelta(t, 99.9, input.GiftCredit, 1e-9)
	require.Equal(t, int64(10), input.CashPaidMinor)
	require.True(t, paymentWalletFundingRequired(order))

	legacy := &dbent.PaymentOrder{ProductSnapshot: map[string]any{}}
	require.False(t, paymentWalletFundingRequired(legacy))
	require.True(t, paymentWalletFundingRequired(&dbent.PaymentOrder{ProductSnapshot: map[string]any{"schema_version": 2}}))
}

func TestPaymentWalletFundingForOrderRejectsMalformedOrIncompleteVersionedSplit(t *testing.T) {
	base := func() *dbent.PaymentOrder {
		return &dbent.PaymentOrder{
			ID: 11, UserID: 22, OrderType: payment.OrderTypeBalance,
			Amount: 100, PayAmount: 0.1,
			ProductSnapshot: map[string]any{
				"schema_version":     2,
				"credited_amount":    100.0,
				"paid_credit_amount": 0.1,
				"gift_credit_amount": 99.9,
			},
		}
	}

	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "malformed version", mutate: func(snapshot map[string]any) { snapshot["schema_version"] = "two" }},
		{name: "missing paid", mutate: func(snapshot map[string]any) { delete(snapshot, "paid_credit_amount") }},
		{name: "malformed paid", mutate: func(snapshot map[string]any) { snapshot["paid_credit_amount"] = "not-a-number" }},
		{name: "missing gift", mutate: func(snapshot map[string]any) { delete(snapshot, "gift_credit_amount") }},
		{name: "malformed gift", mutate: func(snapshot map[string]any) { snapshot["gift_credit_amount"] = "not-a-number" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			order := base()
			tc.mutate(order.ProductSnapshot)
			require.True(t, paymentWalletFundingRequired(order))
			_, err := PaymentWalletFundingForOrder(order)
			require.Error(t, err)
		})
	}
}

func TestReviewedRefundRequiresManualAffiliateRebateRecovery(t *testing.T) {
	ctx := context.Background()
	svc, order := newReviewedBalanceRefundFixture(t, 100, 0.1)
	_, err := svc.entClient.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("AFFILIATE_REBATE_APPLIED").
		SetDetail(`{"rebateAmount":1}`).
		SetOperator("system").
		Save(ctx)
	require.NoError(t, err)

	review, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, review.RequiresManualReview)
	require.False(t, review.CanRefund)
	require.Equal(t, "NON_REVERSIBLE_AFFILIATE_REBATE", review.ReasonCode)
}

func TestUnifiedSubscriptionRefundRequiresReviewedQuote(t *testing.T) {
	ctx := context.Background()
	svc, order, _, _, _ := newReviewedSubscriptionRefundFixture(t)
	_, err := svc.reserveUnifiedRefundAttempt(ctx, &RefundPlan{OrderID: order.ID, RefundAmount: order.Amount, Reason: "unreviewed"})
	require.Error(t, err)
	require.Equal(t, "REFUND_REVIEW_REQUIRED", infraerrors.Reason(err))
}
