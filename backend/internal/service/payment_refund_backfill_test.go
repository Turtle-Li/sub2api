//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBackfillSubscriptionGrantCreatesImmutableProvenanceAndRefreshesReview(t *testing.T) {
	ctx := context.Background()
	svc, order, subscription, termStart, termEnd := newLegacySubscriptionGrantBackfillFixture(t, true)

	before, err := svc.ReviewRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, before.RequiresManualReview)
	require.Equal(t, "LEGACY_SUBSCRIPTION_UNATTRIBUTED", before.ReasonCode)
	require.NotNil(t, before.SubscriptionBackfill)
	require.Equal(t, 30, before.SubscriptionBackfill.PurchasedDays)
	require.Len(t, before.SubscriptionBackfill.Candidates, 1)
	require.Equal(t, subscription.ID, before.SubscriptionBackfill.Candidates[0].SubscriptionID)
	require.Equal(t, subscription.ID, before.SubscriptionBackfill.SuggestedSubscriptionID)
	require.True(t, before.SubscriptionBackfill.SuggestedTermStartAt.Equal(termStart))
	require.True(t, before.SubscriptionBackfill.SuggestedTermEndAt.Equal(termEnd))
	require.Equal(t, refundBackfillEvidencePaymentAuditAndSubscription, before.SubscriptionBackfill.EvidenceSource)

	input := SubscriptionGrantBackfillInput{
		AuditRevision:  before.SubscriptionBackfill.AuditRevision,
		SubscriptionID: subscription.ID,
		TermStartAt:    termStart,
		TermEndAt:      termEnd,
		OperatorID:     71,
		// Empty evidence_source deliberately selects the trusted default.
	}
	after, err := svc.BackfillSubscriptionGrant(ctx, order.ID, input)
	require.NoError(t, err)
	require.True(t, after.CanRefund)
	require.False(t, after.RequiresManualReview)
	require.NotNil(t, after.Subscription)
	require.Equal(t, subscription.ID, after.Subscription.SubscriptionID)
	require.True(t, after.Subscription.TermStartAt.Equal(termStart))
	require.True(t, after.Subscription.TermEndAt.Equal(termEnd))

	grant, grantedSubscription, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
	require.NoError(t, err)
	require.Equal(t, subscription.ID, grant.SubscriptionID)
	require.Equal(t, subscription.ID, grantedSubscription.ID)
	require.True(t, grant.TermStart.Equal(termStart))
	require.True(t, grant.OriginalEnd.Equal(termEnd))
	require.True(t, grant.CurrentEnd.Equal(termEnd))

	audit, err := svc.entClient.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
		paymentauditlog.ActionEQ(refundProvenanceBackfillAuditAction),
	).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "admin:71", audit.Operator)
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(audit.Detail), &detail))
	require.Equal(t, "subscription_grant_backfill", detail["kind"])
	evidence, ok := detail["evidence"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, refundBackfillEvidencePaymentAuditAndSubscription, evidence["source"])
	operator, ok := detail["operator"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(71), operator["id"])
	require.Equal(t, "admin:71", operator["label"])
	require.NotNil(t, detail["before"])
	require.NotNil(t, detail["after"])

	// A retry of the same audited assertion is a replay, not a duplicate grant
	// or a second audit entry.
	replayed, err := svc.BackfillSubscriptionGrant(ctx, order.ID, input)
	require.NoError(t, err)
	require.True(t, replayed.CanRefund)
	count, err := svc.entClient.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
		paymentauditlog.ActionEQ(refundProvenanceBackfillAuditAction),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestBackfillSubscriptionGrantRejectsUnprovenOrStaleAssertions(t *testing.T) {
	ctx := context.Background()

	t.Run("stale audit revision", func(t *testing.T) {
		svc, order, subscription, termStart, termEnd := newLegacySubscriptionGrantBackfillFixture(t, true)
		_, err := svc.BackfillSubscriptionGrant(ctx, order.ID, SubscriptionGrantBackfillInput{
			AuditRevision: "stale", SubscriptionID: subscription.ID, TermStartAt: termStart, TermEndAt: termEnd, OperatorID: 71,
		})
		require.Error(t, err)
		require.Equal(t, "SUBSCRIPTION_BACKFILL_AUDIT_STALE", infraerrors.Reason(err))
		requireNoSubscriptionGrant(t, ctx, svc, order.ID)
	})

	t.Run("term must equal immutable snapshot duration", func(t *testing.T) {
		svc, order, subscription, termStart, termEnd := newLegacySubscriptionGrantBackfillFixture(t, true)
		review, err := svc.ReviewRefund(ctx, order.ID)
		require.NoError(t, err)
		_, err = svc.BackfillSubscriptionGrant(ctx, order.ID, SubscriptionGrantBackfillInput{
			AuditRevision: review.SubscriptionBackfill.AuditRevision, SubscriptionID: subscription.ID,
			TermStartAt: termStart, TermEndAt: termEnd.AddDate(0, 0, -1), OperatorID: 71,
		})
		require.Error(t, err)
		require.Equal(t, "SUBSCRIPTION_BACKFILL_TERM_DURATION_MISMATCH", infraerrors.Reason(err))
		requireNoSubscriptionGrant(t, ctx, svc, order.ID)
	})

	t.Run("default evidence requires assignment audit", func(t *testing.T) {
		svc, order, subscription, termStart, termEnd := newLegacySubscriptionGrantBackfillFixture(t, false)
		review, err := svc.ReviewRefund(ctx, order.ID)
		require.NoError(t, err)
		_, err = svc.BackfillSubscriptionGrant(ctx, order.ID, SubscriptionGrantBackfillInput{
			AuditRevision: review.SubscriptionBackfill.AuditRevision, SubscriptionID: subscription.ID,
			TermStartAt: termStart, TermEndAt: termEnd, OperatorID: 71,
		})
		require.Error(t, err)
		require.Equal(t, "SUBSCRIPTION_BACKFILL_AUDIT_EVIDENCE_MISSING", infraerrors.Reason(err))
		requireNoSubscriptionGrant(t, ctx, svc, order.ID)
	})

	t.Run("external evidence has a required detail", func(t *testing.T) {
		svc, order, subscription, termStart, termEnd := newLegacySubscriptionGrantBackfillFixture(t, true)
		review, err := svc.ReviewRefund(ctx, order.ID)
		require.NoError(t, err)
		_, err = svc.BackfillSubscriptionGrant(ctx, order.ID, SubscriptionGrantBackfillInput{
			AuditRevision: review.SubscriptionBackfill.AuditRevision, SubscriptionID: subscription.ID,
			TermStartAt: termStart, TermEndAt: termEnd, EvidenceSource: refundBackfillEvidenceProviderReceipt, OperatorID: 71,
		})
		require.Error(t, err)
		require.Equal(t, "SUBSCRIPTION_BACKFILL_EVIDENCE_DETAIL_REQUIRED", infraerrors.Reason(err))
		requireNoSubscriptionGrant(t, ctx, svc, order.ID)
	})

	t.Run("pending refund keeps provenance immutable", func(t *testing.T) {
		svc, order, subscription, termStart, termEnd := newLegacySubscriptionGrantBackfillFixture(t, true)
		review, err := svc.ReviewRefund(ctx, order.ID)
		require.NoError(t, err)
		insertPendingSubscriptionBackfillRefundAttempt(t, ctx, svc, order)
		_, err = svc.BackfillSubscriptionGrant(ctx, order.ID, SubscriptionGrantBackfillInput{
			AuditRevision: review.SubscriptionBackfill.AuditRevision, SubscriptionID: subscription.ID,
			TermStartAt: termStart, TermEndAt: termEnd, OperatorID: 71,
		})
		require.Error(t, err)
		require.Equal(t, "SUBSCRIPTION_BACKFILL_REFUND_IN_FLIGHT", infraerrors.Reason(err))
		requireNoSubscriptionGrant(t, ctx, svc, order.ID)
	})

	t.Run("another order term cannot overlap", func(t *testing.T) {
		svc, order, subscription, termStart, termEnd := newLegacySubscriptionGrantBackfillFixture(t, true)
		review, err := svc.ReviewRefund(ctx, order.ID)
		require.NoError(t, err)
		_, otherOrder, _ := newUnifiedRefundFixture(t, svc.entClient, payment.TypeWxpay)
		_, err = svc.entClient.ExecContext(ctx, `INSERT INTO payment_subscription_grants
			(payment_order_id, subscription_id, user_id, group_id, term_start_at, original_term_end_at, current_term_end_at)
			VALUES ($1,$2,$3,$4,$5,$6,$6)`,
			otherOrder.ID, subscription.ID, subscription.UserID, subscription.GroupID,
			termStart.AddDate(0, 0, -5), termEnd.AddDate(0, 0, -5))
		require.NoError(t, err)
		_, err = svc.BackfillSubscriptionGrant(ctx, order.ID, SubscriptionGrantBackfillInput{
			AuditRevision: review.SubscriptionBackfill.AuditRevision, SubscriptionID: subscription.ID,
			TermStartAt: termStart, TermEndAt: termEnd, OperatorID: 71,
		})
		require.Error(t, err)
		require.Equal(t, "SUBSCRIPTION_BACKFILL_TERM_OVERLAP", infraerrors.Reason(err))
		requireNoSubscriptionGrant(t, ctx, svc, order.ID)
	})
}

func newLegacySubscriptionGrantBackfillFixture(t *testing.T, includeAssignmentAudit bool) (*PaymentService, *dbent.PaymentOrder, *dbent.UserSubscription, time.Time, time.Time) {
	t.Helper()
	ctx := context.Background()
	client := newUnifiedRefundSQLiteClient(t)
	svc, order, _ := newUnifiedRefundFixture(t, client, payment.TypeWxpay)
	fixed := time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC)
	termStart := fixed.AddDate(0, 0, -10)
	termEnd := termStart.AddDate(0, 0, 30)

	group, err := client.Group.Create().SetName("subscription-backfill-group").Save(ctx)
	require.NoError(t, err)
	subscription, err := client.UserSubscription.Create().
		SetUserID(order.UserID).
		SetGroupID(group.ID).
		SetStartsAt(fixed.AddDate(0, 0, -40)).
		SetExpiresAt(termEnd).
		SetStatus(SubscriptionStatusActive).
		Save(ctx)
	require.NoError(t, err)

	// Leave SubscriptionDays unset to model the historical rows this endpoint
	// is for. The immutable product snapshot remains the duration authority.
	order, err = client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(payment.OrderTypeSubscription).
		SetSubscriptionGroupID(group.ID).
		SetProductSnapshot(map[string]any{
			"kind":              "subscription",
			"group_id":          group.ID,
			"subscription_days": 30,
			"entitlements":      map[string]any{},
		}).
		Save(ctx)
	require.NoError(t, err)
	svc.refundReviewNow = func() time.Time { return fixed }

	if includeAssignmentAudit {
		detail, marshalErr := json.Marshal(map[string]any{
			"groupID":      group.ID,
			"validityDays": 30,
		})
		require.NoError(t, marshalErr)
		_, err = client.PaymentAuditLog.Create().
			SetOrderID(strconv.FormatInt(order.ID, 10)).
			SetAction("SUBSCRIPTION_ASSIGNED").
			SetDetail(string(detail)).
			SetOperator("system").
			Save(ctx)
		require.NoError(t, err)
	}
	return svc, order, subscription, termStart, termEnd
}

func requireNoSubscriptionGrant(t *testing.T, ctx context.Context, svc *PaymentService, orderID int64) {
	t.Helper()
	_, _, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, orderID, false)
	require.ErrorIs(t, err, errRefundAccountingMissing)
}

func insertPendingSubscriptionBackfillRefundAttempt(t *testing.T, ctx context.Context, svc *PaymentService, order *dbent.PaymentOrder) {
	t.Helper()
	snapshot := psOrderProviderSnapshot(order)
	require.NotNil(t, snapshot)
	_, err := svc.entClient.ExecContext(ctx, `INSERT INTO unified_payment_refund_attempts
		(product_refund_no, order_id, payment_order_id, idempotency_key, environment,
		 organization_id, product_id, app_id, payment_method, amount_fen, balance_amount_minor,
		 deduct_balance, force_refund, reason_summary, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		"backfill-"+uuid.NewString(), order.ID, snapshot.PaymentOrderID, "backfill:"+uuid.NewString(),
		snapshot.Environment, snapshot.OrganizationID, snapshot.ProductID, snapshot.AppID, "wechat_pay",
		int64(1), int64(1), false, false, "subscription backfill test", unifiedRefundPending)
	require.NoError(t, err)
}
