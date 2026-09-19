//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentinvoicedocument"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestInvoiceWorkflowOwnsEligibleOrdersAndStoresPDFPrivately(t *testing.T) {
	ctx := context.Background()
	svc, client := newInvoiceUnitService(t, ctx)
	createInvoiceUnitRefundFenceTables(t, ctx, client)
	owner := createInvoiceUnitUser(t, ctx, client, "invoice-owner@example.com")
	other := createInvoiceUnitUser(t, ctx, client, "invoice-other@example.com")
	order := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusCompleted, true, true)

	created, err := svc.CreateOrResubmitInvoiceRequest(ctx, order.ID, owner.ID, CreateInvoiceRequestInput{
		TitleType: InvoiceTitleTypeEnterprise, Title: "Example Technology Co., Ltd.",
		TaxIdentifier: "91310000MA12345678", RecipientEmail: "finance@example.com",
		RecipientPhone: "+86 138 0000 0000", Remark: "Electronic ordinary invoice",
	})
	require.NoError(t, err)
	require.Equal(t, InvoiceStatusPending, created.Status)
	require.Equal(t, order.PayAmount, created.Amount)
	require.Equal(t, "CNY", created.Currency)
	require.Equal(t, 1, created.Revision)

	_, err = svc.GetOrderInvoiceRequest(ctx, order.ID, other.ID)
	require.Error(t, err)
	require.Equal(t, "INVOICE_NOT_FOUND", infraerrors.Reason(err))
	_, err = svc.CreateOrResubmitInvoiceRequest(ctx, order.ID, other.ID, CreateInvoiceRequestInput{
		TitleType: InvoiceTitleTypePersonal, Title: "Other", RecipientEmail: "other@example.com",
	})
	require.Error(t, err)
	require.Equal(t, "INVOICE_ORDER_NOT_FOUND", infraerrors.Reason(err))

	_, err = svc.CreateOrResubmitInvoiceRequest(ctx, order.ID, owner.ID, CreateInvoiceRequestInput{
		TitleType: InvoiceTitleTypePersonal, Title: "Duplicate", RecipientEmail: "owner@example.com",
	})
	require.Error(t, err)
	require.Equal(t, "INVOICE_ALREADY_REQUESTED", infraerrors.Reason(err))

	processing, err := svc.AdminUpdateOrderInvoiceRequest(ctx, order.ID, 9001, AdminUpdateInvoiceInput{Status: InvoiceStatusProcessing})
	require.NoError(t, err)
	require.Equal(t, InvoiceStatusProcessing, processing.Status)

	pdf := &InvoicePDFInput{Filename: "official-invoice.pdf", Data: []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n%%EOF")}
	issued, err := svc.AdminUpdateOrderInvoiceRequest(ctx, order.ID, 9001, AdminUpdateInvoiceInput{
		Status: InvoiceStatusIssued, Provider: "manual", InvoiceItemName: "Information services",
		InvoiceNumber: "24612000000000000001", Document: pdf,
	})
	require.NoError(t, err)
	require.Equal(t, InvoiceStatusIssued, issued.Status)
	require.Equal(t, int64(len(pdf.Data)), *issued.DocumentSizeBytes)

	document, err := client.PaymentInvoiceDocument.Query().
		Where(paymentinvoicedocument.InvoiceRequestIDEQ(issued.ID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, pdf.Data, document.Data)

	encoded, err := json.Marshal(issued)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), string(pdf.Data), "invoice DTO must never contain PDF bytes")
}

func TestInvoiceWorkflowChecksUnifiedRefundReviewAtEveryEligibleTransition(t *testing.T) {
	ctx := context.Background()
	svc, client := newInvoiceUnitService(t, ctx)
	createInvoiceUnitRefundFenceTables(t, ctx, client)
	owner := createInvoiceUnitUser(t, ctx, client, "invoice-review@example.com")

	blockedCreate := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusCompleted, true, true)
	_, err := client.ExecContext(ctx,
		"INSERT INTO unified_payment_refund_attempts (order_id, needs_manual_review) VALUES (?, TRUE)", blockedCreate.ID)
	require.NoError(t, err)
	_, err = svc.CreateOrResubmitInvoiceRequest(ctx, blockedCreate.ID, owner.ID, invoiceUnitCreateInput("blocked-create@example.com"))
	require.Error(t, err)
	require.Equal(t, "INVOICE_ORDER_NOT_ELIGIBLE", infraerrors.Reason(err))

	blockedProcess := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusCompleted, true, true)
	_, err = svc.CreateOrResubmitInvoiceRequest(ctx, blockedProcess.ID, owner.ID, invoiceUnitCreateInput("blocked-process@example.com"))
	require.NoError(t, err)
	_, err = client.ExecContext(ctx,
		"INSERT INTO unified_payment_refund_events (order_id, action) VALUES (?, 'UNIFIED_REFUND_UNCORRELATED')", blockedProcess.ID)
	require.NoError(t, err)
	_, err = svc.AdminUpdateOrderInvoiceRequest(ctx, blockedProcess.ID, 9001, AdminUpdateInvoiceInput{Status: InvoiceStatusProcessing})
	require.Error(t, err)
	require.Equal(t, "INVOICE_ORDER_NOT_ELIGIBLE", infraerrors.Reason(err))

	blockedIssue := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusCompleted, true, true)
	_, err = svc.CreateOrResubmitInvoiceRequest(ctx, blockedIssue.ID, owner.ID, invoiceUnitCreateInput("blocked-issue@example.com"))
	require.NoError(t, err)
	_, err = svc.AdminUpdateOrderInvoiceRequest(ctx, blockedIssue.ID, 9001, AdminUpdateInvoiceInput{Status: InvoiceStatusProcessing})
	require.NoError(t, err)
	_, err = client.ExecContext(ctx,
		"INSERT INTO unified_payment_refund_attempts (order_id, needs_manual_review) VALUES (?, TRUE)", blockedIssue.ID)
	require.NoError(t, err)
	_, err = svc.AdminUpdateOrderInvoiceRequest(ctx, blockedIssue.ID, 9001, AdminUpdateInvoiceInput{
		Status: InvoiceStatusIssued, InvoiceItemName: "Information services", InvoiceNumber: "24612000000000000002",
		Document: &InvoicePDFInput{Filename: "blocked.pdf", Data: []byte("%PDF-1.7\n%%EOF")},
	})
	require.Error(t, err)
	require.Equal(t, "INVOICE_ORDER_NOT_ELIGIBLE", infraerrors.Reason(err))
}

func TestInvoiceFulfillmentFilterKeepsRefundReviewSeparateFromDelivery(t *testing.T) {
	ctx := context.Background()
	svc, client := newInvoiceUnitService(t, ctx)
	createInvoiceUnitRefundFenceTables(t, ctx, client)
	owner := createInvoiceUnitUser(t, ctx, client, "invoice-filter-owner@example.com")
	other := createInvoiceUnitUser(t, ctx, client, "invoice-filter-other@example.com")
	pending := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusPaid, true, false)
	unrelated := createInvoiceUnitOrder(t, ctx, client, other, OrderStatusPaid, true, false)

	_, err := client.ExecContext(ctx,
		"INSERT INTO unified_payment_refund_events (order_id, action) VALUES (?, 'UNIFIED_PAYMENT_EVENT_REJECTED')", unrelated.ID)
	require.NoError(t, err)

	pendingRows, pendingTotal, err := svc.GetUserOrders(ctx, owner.ID, OrderListParams{
		Page: 1, PageSize: 20, FulfillmentStatus: InvoiceFulfillmentStatusPending,
	})
	require.NoError(t, err)
	require.Equal(t, 1, pendingTotal)
	require.Len(t, pendingRows, 1)
	require.Equal(t, pending.ID, pendingRows[0].ID)

	byExactID, exactTotal, err := svc.AdminListOrders(ctx, 0, OrderListParams{
		Page: 1, PageSize: 20, Keyword: fmt.Sprintf("#%d", pending.ID),
	})
	require.NoError(t, err)
	require.Equal(t, 1, exactTotal)
	require.Len(t, byExactID, 1)
	require.Equal(t, pending.ID, byExactID[0].ID)

	_, err = client.ExecContext(ctx,
		"INSERT INTO unified_payment_refund_events (order_id, action) VALUES (?, 'UNIFIED_PAYMENT_EVENT_REJECTED')", pending.ID)
	require.NoError(t, err)
	pendingRows, pendingTotal, err = svc.GetUserOrders(ctx, owner.ID, OrderListParams{
		Page: 1, PageSize: 20, FulfillmentStatus: InvoiceFulfillmentStatusPending,
	})
	require.NoError(t, err)
	require.Equal(t, 1, pendingTotal)
	require.Len(t, pendingRows, 1)
	require.Equal(t, pending.ID, pendingRows[0].ID, "a refund review must not replace the delivery fact")

	manualRows, _, err := svc.GetUserOrders(ctx, owner.ID, OrderListParams{
		Page: 1, PageSize: 20, FulfillmentStatus: "MANUAL_REVIEW",
	})
	require.NoError(t, err)
	require.Len(t, manualRows, 1)
	require.Equal(t, pending.ID, manualRows[0].ID, "the legacy filter remains a refund-review compatibility alias")
}

func TestInvoiceSnapshotSanitizerAndFulfillmentPresentationAreTyped(t *testing.T) {
	order := &dbent.PaymentOrder{ProductSnapshot: map[string]any{
		"name":             "Pro plan",
		"description":      "Promised capacity",
		"features":         []any{"Priority queue", 7},
		"price":            "not-a-number",
		"daily_limit_usd":  "also-not-a-number",
		"weekly_limit_usd": float64(12.5),
		"entitlements": map[string]any{
			"balance_bonus":          float64(8),
			"concurrency":            "wrong",
			"reset_card_count":       float64(2),
			"reset_card_expiry_days": float64(30),
			"message":                "Two reset cards",
		},
	}}
	snapshot := SanitizedPaymentOrderProductSnapshot(order)
	require.Equal(t, "Pro plan", snapshot["name"])
	require.Equal(t, "Promised capacity", snapshot["description"])
	require.Equal(t, float64(12.5), snapshot["weekly_limit_usd"])
	_, hasPrice := snapshot["price"]
	require.False(t, hasPrice, "numeric strings must not reach frontend numeric rendering")
	_, hasDailyLimit := snapshot["daily_limit_usd"]
	require.False(t, hasDailyLimit, "numeric strings must not reach frontend numeric rendering")
	_, hasFeatures := snapshot["features"]
	require.False(t, hasFeatures, "mixed feature arrays are not a typed product snapshot")
	entitlements := snapshot["entitlements"].(map[string]any)
	require.Equal(t, float64(8), entitlements["balance_bonus"])
	require.Equal(t, float64(2), entitlements["reset_card_count"])
	require.Equal(t, float64(30), entitlements["reset_card_expiry_days"])
	require.Equal(t, "Two reset cards", entitlements["message"])
	_, hasConcurrency := entitlements["concurrency"]
	require.False(t, hasConcurrency)

	now := time.Now().UTC()
	paidFailed := &dbent.PaymentOrder{Status: OrderStatusFailed, PaidAt: &now}
	require.Equal(t, InvoiceFulfillmentStatusFailed, PaymentOrderFulfillmentStatus(paidFailed))
	completedWithReview := &dbent.PaymentOrder{Status: OrderStatusCompleted, PaidAt: &now, CompletedAt: &now}
	require.Equal(t, InvoiceFulfillmentStatusFulfilled, PaymentOrderFulfillmentStatus(completedWithReview))
	require.Equal(t, InvoiceFulfillmentStatusPending, PaymentOrderFulfillmentStatus(&dbent.PaymentOrder{Status: OrderStatusPaid, PaidAt: &now}))
	require.Equal(t, InvoiceFulfillmentStatusNotStarted, PaymentOrderFulfillmentStatus(&dbent.PaymentOrder{Status: OrderStatusFailed}))
}

func TestInvoiceOrderPresentationsProjectLatestRefundEntitlementStateInOnePage(t *testing.T) {
	ctx := context.Background()
	svc, client := newInvoiceUnitService(t, ctx)
	createInvoiceUnitRefundFenceTables(t, ctx, client)
	owner := createInvoiceUnitUser(t, ctx, client, "invoice-entitlement@example.com")

	reclaimed := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusCompleted, true, true)
	reclaiming := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusCompleted, true, true)
	restored := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusCompleted, true, true)
	manual := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusCompleted, true, true)
	balancePaused := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusRefundPending, true, true)
	historical := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusRefunded, true, true)
	notApplicable := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusCompleted, true, true)
	now := time.Now().UTC()

	// The older pending attempt must not win over the newest terminal result.
	insertInvoiceUnitRefundAttempt(t, ctx, client, reclaimed.ID, "attempt-reclaimed-old", unifiedRefundPending, "legacy_balance", false, true, false, now.Add(-time.Minute))
	insertInvoiceUnitRefundAttempt(t, ctx, client, reclaimed.ID, "attempt-reclaimed", "SUCCEEDED", "legacy_balance", true, false, false, now)
	insertInvoiceUnitRefundAttempt(t, ctx, client, reclaiming.ID, "attempt-reclaiming", unifiedRefundPending, refundReviewKindBalance, true, true, false, now)
	insertInvoiceUnitRefundAttempt(t, ctx, client, restored.ID, "attempt-restored", "FAILED", refundReviewKindBalance, true, false, false, now)
	insertInvoiceUnitRefundAttempt(t, ctx, client, manual.ID, "attempt-manual", "FAILED", refundReviewKindSubscription, false, true, false, now)
	insertInvoiceUnitRefundAttempt(t, ctx, client, balancePaused.ID, "attempt-balance-paused", unifiedRefundPending, refundReviewKindSubscription, false, true, true, now)
	_, err := client.ExecContext(ctx, `UPDATE unified_payment_refund_attempts SET
		provider_status=?, failure_code=?, refund_request_id=?, amount_fen=?, payment_method=?, provider_updated_at=? WHERE order_id=?`,
		unifiedRefundBalanceInsufficientProviderStatus, unifiedRefundProviderRejectedFailureCode,
		"cccccccc-cccc-4ccc-8ccc-cccccccccccc", 10, unifiedpay.PaymentMethodWechatPay, now, balancePaused.ID)
	require.NoError(t, err)

	presentations, err := svc.InvoiceOrderPresentations(ctx, []*dbent.PaymentOrder{
		reclaimed, reclaiming, restored, manual, balancePaused, historical, notApplicable,
	})
	require.NoError(t, err)
	require.Equal(t, RefundEntitlementStatusReclaimed, presentations[reclaimed.ID].RefundEntitlementStatus)
	require.Equal(t, InvoiceFulfillmentStatusFulfilled, presentations[reclaimed.ID].FulfillmentStatus, "fulfilled remains the historical delivery fact")
	require.Equal(t, RefundEntitlementStatusReclaiming, presentations[reclaiming.ID].RefundEntitlementStatus)
	require.Equal(t, RefundEntitlementStatusRestored, presentations[restored.ID].RefundEntitlementStatus)
	require.Equal(t, RefundEntitlementStatusManualReview, presentations[manual.ID].RefundEntitlementStatus)
	recovery := presentations[balancePaused.ID].RefundRecovery
	require.NotNil(t, recovery)
	require.Equal(t, "WAITING_PROVIDER_BALANCE", recovery.State)
	require.Equal(t, "WECHAT_MERCHANT_BALANCE_INSUFFICIENT", recovery.ReasonCode)
	require.True(t, recovery.CanRetry)
	require.True(t, recovery.CanConfirmExternal)
	require.Equal(t, RefundEntitlementStatusHistoricalUnverified, presentations[historical.ID].RefundEntitlementStatus)
	require.Equal(t, RefundEntitlementStatusNotApplicable, presentations[notApplicable.ID].RefundEntitlementStatus)
}

func TestPaymentProductSnapshotFreezesKnownPlanAndGroupEvidence(t *testing.T) {
	daily, weekly, monthly := 12.5, 70.0, 250.0
	plan := &dbent.SubscriptionPlan{
		ID:           41,
		GroupID:      9,
		Name:         "Pro",
		ProductName:  "Pro subscription",
		Description:  "Priority access and reset cards",
		Features:     `["Priority access","Higher concurrency"]`,
		Price:        199,
		Currency:     "CNY",
		ValidityDays: 30,
		ValidityUnit: "day",
		Entitlements: map[string]any{
			"balance_bonus":          3,
			"concurrency":            5,
			"reset_card_count":       2,
			"reset_card_expiry_days": 30,
			"message":                "Two reset cards included",
		},
	}
	snapshot := buildPaymentProductSnapshotWithGroup(plan, 199, 199, 30, &Group{
		ID: 9, Name: "OpenAI Pro", DailyLimitUSD: &daily, WeeklyLimitUSD: &weekly, MonthlyLimitUSD: &monthly,
	})
	require.Equal(t, "Priority access and reset cards", snapshot["description"])
	require.Equal(t, []string{"Priority access", "Higher concurrency"}, snapshot["features"])
	require.Equal(t, int64(9), snapshot["group_id"])
	require.Equal(t, "OpenAI Pro", snapshot["group_name"])
	require.Equal(t, daily, snapshot["daily_limit_usd"])
	require.Equal(t, weekly, snapshot["weekly_limit_usd"])
	require.Equal(t, monthly, snapshot["monthly_limit_usd"])
	entitlements := snapshot["entitlements"].(map[string]any)
	require.Equal(t, 2, entitlements["reset_card_count"])
	require.Equal(t, 30, entitlements["reset_card_expiry_days"])
	require.Equal(t, "Two reset cards included", entitlements["message"])
}

func TestCouponPaidReviewPresentationDoesNotChangeRefundStateOrWriteRetryAudit(t *testing.T) {
	ctx := context.Background()
	svc, client := newInvoiceUnitService(t, ctx)
	createInvoiceUnitRefundFenceTables(t, ctx, client)
	owner := createInvoiceUnitUser(t, ctx, client, "coupon-paid-review@example.com")
	order := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusFailed, true, false)
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetFailedAt(time.Now().UTC()).
		SetFailedReason(paymentDiscountManualReviewReason).
		SetProductSnapshot(map[string]any{"payment_discount": map[string]any{"code": "SAFE2026"}}).
		Save(ctx)
	require.NoError(t, err)

	presentations, err := svc.InvoiceOrderPresentations(ctx, []*dbent.PaymentOrder{order})
	require.NoError(t, err)
	presentation := presentations[order.ID]
	require.True(t, presentation.NeedsManualReview)
	require.Equal(t, RefundEntitlementStatusNotApplicable, presentation.RefundEntitlementStatus)

	err = svc.RetryFulfillment(ctx, order.ID)
	require.Error(t, err)
	require.Equal(t, "COUPON_MANUAL_REVIEW", infraerrors.Reason(err))
	retryAuditCount, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(fmt.Sprint(order.ID)),
		paymentauditlog.ActionEQ("RECHARGE_RETRY"),
	).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, retryAuditCount)
}

func TestInvoiceDeliveryReclaimsStaleClaimsAndKeepsFeishuPayloadPrivate(t *testing.T) {
	ctx := context.Background()
	svc, client := newInvoiceUnitService(t, ctx)
	createInvoiceUnitRefundFenceTables(t, ctx, client)
	owner := createInvoiceUnitUser(t, ctx, client, "invoice-delivery@example.com")
	order := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusCompleted, true, true)
	stale := time.Now().UTC().Add(-invoiceDeliveryClaimTTL - time.Minute)
	invoice, err := client.PaymentInvoiceRequest.Create().
		SetOrderID(order.ID).
		SetUserID(owner.ID).
		SetTitleType(InvoiceTitleTypeEnterprise).
		SetTitle("Private enterprise title").
		SetTaxIdentifier("91310000MA12345678").
		SetRecipientEmail("private-recipient@example.com").
		SetAmount(order.PayAmount).
		SetCurrency("CNY").
		SetStatus(InvoiceStatusIssued).
		SetRevision(1).
		SetProvider("manual").
		SetEmailDeliveryStatus(InvoiceEmailDeliverySending).
		SetEmailDeliveryClaimedAt(stale).
		SetFeishuNotificationStatus(InvoiceFeishuNotificationFailed).
		SetFeishuNotificationRevision(1).
		Save(ctx)
	require.NoError(t, err)

	claim, err := svc.claimInvoiceEmailDelivery(ctx, invoice.ID)
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.Equal(t, InvoiceEmailDeliverySending, claim.Invoice.EmailDeliveryStatus)
	require.Equal(t, 1, claim.Invoice.EmailDeliveryAttempts)
	require.NoError(t, svc.finishInvoiceEmailDelivery(ctx, claim, errors.New("smtp internal detail")))

	persisted, err := client.PaymentInvoiceRequest.Get(ctx, invoice.ID)
	require.NoError(t, err)
	require.Equal(t, InvoiceEmailDeliveryFailed, persisted.EmailDeliveryStatus)
	require.True(t, invoiceEmailRetryable(persisted, time.Now().UTC()))
	require.NotNil(t, persisted.EmailDeliveryNextAttemptAt)

	retried, err := svc.AdminRetryInvoiceEmail(ctx, order.ID, 9001)
	require.NoError(t, err)
	require.Equal(t, InvoiceEmailDeliveryPending, retried.EmailDeliveryStatus)
	require.True(t, retried.EmailRetryable)

	feishuRetried, err := svc.AdminRetryInvoiceFeishuNotification(ctx, order.ID, 9001)
	require.NoError(t, err)
	require.Equal(t, InvoiceFeishuNotificationPending, feishuRetried.FeishuNotificationStatus)
	require.True(t, feishuRetried.FeishuRetryable)

	message := invoiceFeishuRequestMessage(invoice)
	for _, forbidden := range []string{
		"91310000MA12345678", "private-recipient@example.com", "Private enterprise title", "invoice.pdf",
	} {
		require.NotContains(t, message, forbidden)
	}
	require.Contains(t, message, "https://www.turtleligpt.com/admin/orders/invoices?invoice_status=PENDING")
	require.Contains(t, message, fmt.Sprintf("订单：%d", order.ID))
}

func newInvoiceUnitService(t *testing.T, ctx context.Context) (*PaymentService, *dbent.Client) {
	t.Helper()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	return &PaymentService{entClient: client}, client
}

func createInvoiceUnitRefundFenceTables(t *testing.T, ctx context.Context, client *dbent.Client) {
	t.Helper()
	_, err := client.ExecContext(ctx, `CREATE TABLE unified_payment_refund_attempts (
		order_id INTEGER NOT NULL,
		product_refund_no TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'PENDING',
		refund_kind TEXT NOT NULL DEFAULT 'legacy_balance',
		deduct_balance BOOLEAN NOT NULL DEFAULT FALSE,
		entitlement_reserved BOOLEAN NOT NULL DEFAULT FALSE,
		needs_manual_review BOOLEAN NOT NULL DEFAULT FALSE,
		provider_status TEXT,
		failure_code TEXT,
		payment_method TEXT NOT NULL DEFAULT 'alipay',
		refund_request_id TEXT,
		provider_refund_id TEXT,
		amount_fen INTEGER NOT NULL DEFAULT 0,
		provider_updated_at TIMESTAMP,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `CREATE TABLE unified_payment_refund_events (
		order_id INTEGER NOT NULL, action TEXT NOT NULL
	)`)
	require.NoError(t, err)
}

func insertInvoiceUnitRefundAttempt(t *testing.T, ctx context.Context, client *dbent.Client, orderID int64, refundNo, status, kind string, deductBalance, entitlementReserved, needsReview bool, createdAt time.Time) {
	t.Helper()
	_, err := client.ExecContext(ctx, `INSERT INTO unified_payment_refund_attempts
		(order_id, product_refund_no, status, refund_kind, deduct_balance, entitlement_reserved, needs_manual_review, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		orderID, refundNo, status, kind, deductBalance, entitlementReserved, needsReview, createdAt)
	require.NoError(t, err)
}

func createInvoiceUnitUser(t *testing.T, ctx context.Context, client *dbent.Client, email string) *dbent.User {
	t.Helper()
	user, err := client.User.Create().
		SetEmail(email).
		SetPasswordHash("hash").
		SetUsername(strings.ReplaceAll(strings.Split(email, "@")[0], "+", "-")).
		Save(ctx)
	require.NoError(t, err)
	return user
}

func createInvoiceUnitOrder(t *testing.T, ctx context.Context, client *dbent.Client, user *dbent.User, status string, paid, completed bool) *dbent.PaymentOrder {
	t.Helper()
	serial := time.Now().UnixNano()
	builder := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(108).
		SetPayAmount(108).
		SetFeeRate(0).
		SetRechargeCode(fmt.Sprintf("INVOICE-%d", serial)).
		SetOutTradeNo(fmt.Sprintf("sub2_invoice_%d", serial)).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo(fmt.Sprintf("trade-%d", serial)).
		SetOrderType(payment.OrderTypeBalance).
		SetProviderSnapshot(map[string]any{"schema_version": 2, "currency": "CNY"}).
		SetStatus(status).
		SetExpiresAt(time.Now().UTC().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("invoice.example.com")
	if paid {
		builder.SetPaidAt(time.Now().UTC().Add(-time.Minute))
	}
	if completed {
		builder.SetCompletedAt(time.Now().UTC())
	}
	order, err := builder.Save(ctx)
	require.NoError(t, err)
	return order
}

func invoiceUnitCreateInput(email string) CreateInvoiceRequestInput {
	return CreateInvoiceRequestInput{TitleType: InvoiceTitleTypePersonal, Title: "Invoice User", RecipientEmail: email}
}
