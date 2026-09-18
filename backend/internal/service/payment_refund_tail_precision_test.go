//go:build unit

package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	"github.com/stretchr/testify/require"
)

func TestReviewedRefundAuditedWholeSecondTail(t *testing.T) {
	for _, successor := range []bool{false, true} {
		for _, outcome := range []string{unifiedpay.RefundStatusSucceeded, unifiedpay.RefundStatusFailed} {
			t.Run(outcome+"/successor="+strconv.FormatBool(successor), func(t *testing.T) {
				ctx := context.Background()
				svc, order, sub, start, end := newReviewedSubscriptionRefundFixture(t)
				offset := 41622 * time.Microsecond
				sub, err := svc.entClient.UserSubscription.UpdateOneID(sub.ID).
					SetStartsAt(start.Add(offset)).SetExpiresAt(end.Add(offset)).Save(ctx)
				require.NoError(t, err)
				auditSub := *sub
				if successor {
					// The online failure: a previous refund held the next term at an
					// old whole-second expiry during backfill; capture later restored
					// the next grant's exact start. That completed grant proves it.
					auditSub.ExpiresAt = end
					_, err = svc.entClient.ExecContext(ctx, `INSERT INTO payment_subscription_grants
					(payment_order_id, subscription_id, user_id, group_id, term_start_at,
					 original_term_end_at, current_term_end_at, refunded_seconds)
					VALUES ($1,$2,$3,$4,$5,$6,$5,$7)`, order.ID+1000, sub.ID, order.UserID, sub.GroupID,
						end.Add(offset), end.Add(offset).Add(30*24*time.Hour), int64(30*24*60*60))
					require.NoError(t, err)
				}
				detail, err := subscriptionGrantBackfillAuditDetail(order, &auditSub, SubscriptionGrantBackfillInput{
					TermStartAt: start, TermEndAt: end, OperatorID: 1,
					EvidenceSource: refundBackfillEvidencePaymentAuditAndSubscription,
				}, 0, 0, 0)
				require.NoError(t, err)
				_, err = svc.entClient.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(order.ID, 10)).
					SetAction(refundProvenanceBackfillAuditAction).SetDetail(detail).Save(ctx)
				require.NoError(t, err)
				review, err := svc.ReviewRefund(ctx, order.ID)
				require.NoError(t, err)
				require.True(t, review.CanRefund, review.ReasonCode)
				require.Equal(t, end.Add(offset), review.Subscription.CurrentExpiresAt)
				plan, err := svc.PrepareReviewedRefund(ctx, order.ID, review.QuoteRevision, "audited precision regression")
				require.NoError(t, err)
				attempt, err := svc.reserveUnifiedRefundAttempt(ctx, plan)
				require.NoError(t, err)
				_, err = svc.applyUnifiedRefundResource(ctx, order.ID, unifiedRefundFixtureResource(attempt, outcome), "test")
				require.NoError(t, err)
				actual, err := svc.entClient.UserSubscription.Get(ctx, sub.ID)
				require.NoError(t, err)
				if outcome == unifiedpay.RefundStatusFailed {
					require.Equal(t, end.Add(offset), actual.ExpiresAt)
				} else {
					require.Equal(t, review.Subscription.NewExpiresAt, actual.ExpiresAt)
				}
				// An interpretation must never rewrite the administrator's evidence.
				raw, _, err := loadPaymentSubscriptionRefundState(ctx, svc.entClient, order.ID, false)
				require.NoError(t, err)
				require.Equal(t, start, raw.TermStart)
				require.Equal(t, end, raw.OriginalEnd)
			})
		}
	}
}

func TestReviewedRefundTailPrecisionDoesNotRelaxIntegrity(t *testing.T) {
	for _, scenario := range []string{"no audit", "extension", "different anchor", "malformed audit", "new precise grant"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			svc, order, sub, start, end := newReviewedSubscriptionRefundFixture(t)
			offset := 41622 * time.Microsecond
			sub, err := svc.entClient.UserSubscription.UpdateOneID(sub.ID).
				SetStartsAt(start.Add(offset)).SetExpiresAt(end.Add(offset)).Save(ctx)
			require.NoError(t, err)
			detail, err := subscriptionGrantBackfillAuditDetail(order, sub, SubscriptionGrantBackfillInput{
				TermStartAt: start, TermEndAt: end, OperatorID: 1,
			}, 0, 0, 0)
			require.NoError(t, err)
			switch scenario {
			case "extension":
				_, err = sub.Update().SetExpiresAt(end.Add(time.Second + offset)).Save(ctx)
			case "different anchor":
				_, err = sub.Update().SetStartsAt(start.Add(time.Second + offset)).Save(ctx)
			case "malformed audit":
				detail = "{}"
			case "new precise grant":
				_, err = svc.entClient.ExecContext(ctx, `UPDATE payment_subscription_grants SET current_term_end_at=$2 WHERE payment_order_id=$1`, order.ID, end.Add(-time.Microsecond))
			}
			require.NoError(t, err)
			if scenario != "no audit" {
				_, err = svc.entClient.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(order.ID, 10)).
					SetAction(refundProvenanceBackfillAuditAction).SetDetail(detail).Save(ctx)
				require.NoError(t, err)
			}
			review, err := svc.ReviewRefund(ctx, order.ID)
			require.NoError(t, err)
			require.False(t, review.CanRefund)
			require.Equal(t, "SUBSCRIPTION_NOT_TAIL", review.ReasonCode)
		})
	}
}
