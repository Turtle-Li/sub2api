package service

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
)

// loadReviewedSubscriptionRefundState interprets the one provable legacy
// datetime-local truncation shape without rewriting immutable provenance.
// Backfill/replay continues to use the raw loader and its original audit values.
func loadReviewedSubscriptionRefundState(ctx context.Context, client *dbent.Client, orderID int64, lock bool) (*paymentSubscriptionGrant, *dbent.UserSubscription, error) {
	grant, sub, err := loadPaymentSubscriptionRefundState(ctx, client, orderID, lock)
	if err != nil || !legacyWholeSecondFirstTerm(grant, sub) {
		return grant, sub, err
	}
	entry, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
		paymentauditlog.ActionEQ(refundProvenanceBackfillAuditAction),
	).Only(ctx)
	if dbent.IsNotFound(err) {
		return grant, sub, nil
	}
	if err != nil {
		return nil, nil, err
	}
	precise := auditedSubscriptionPrecision(grant, sub, entry.Detail, false)
	if precise != grant {
		return precise, sub, nil
	}
	// A refunded successor can restore a more precise boundary than the one
	// visible during backfill (older refunds truncated a held future term).
	// Its durable, fully reclaimed grant independently proves that exact anchor.
	exactEnd := grant.OriginalEnd.Add(sub.StartsAt.Sub(grant.TermStart))
	rows, err := client.QueryContext(ctx, `SELECT term_start_at, original_term_end_at, refunded_seconds
		FROM payment_subscription_grants WHERE subscription_id=$1 AND payment_order_id<>$2
		AND term_start_at=$3 AND current_term_end_at=term_start_at AND reserved_seconds=0
		AND refunded_seconds>0`, grant.SubscriptionID, orderID, exactEnd)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	proven := false
	for rows.Next() {
		var start, end time.Time
		var seconds int64
		if err := rows.Scan(&start, &end, &seconds); err != nil {
			return nil, nil, err
		}
		if end.After(start) && int64(end.Sub(start)/time.Second) == seconds {
			proven = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return auditedSubscriptionPrecision(grant, sub, entry.Detail, proven), sub, nil
}

func legacyWholeSecondFirstTerm(grant *paymentSubscriptionGrant, sub *dbent.UserSubscription) bool {
	return grant != nil && sub != nil && grant.UserID == sub.UserID && grant.GroupID == sub.GroupID &&
		grant.SubscriptionID == sub.ID && grant.TermStart.Nanosecond() == 0 && grant.OriginalEnd.Nanosecond() == 0 &&
		sub.StartsAt.Nanosecond() != 0 && timeEqualToSecond(grant.TermStart, sub.StartsAt) &&
		grant.OriginalEnd.After(grant.TermStart)
}

func auditedSubscriptionPrecision(grant *paymentSubscriptionGrant, sub *dbent.UserSubscription, detail string, successorProven bool) *paymentSubscriptionGrant {
	if !legacyWholeSecondFirstTerm(grant, sub) {
		return grant
	}
	var audit struct {
		Kind   string `json:"kind"`
		Before struct {
			Subscription struct {
				ID        int64     `json:"id"`
				UserID    int64     `json:"user_id"`
				GroupID   int64     `json:"group_id"`
				StartsAt  time.Time `json:"starts_at"`
				ExpiresAt time.Time `json:"expires_at"`
			} `json:"subscription"`
		} `json:"before"`
		After struct {
			OrderID        int64     `json:"payment_order_id"`
			SubscriptionID int64     `json:"subscription_id"`
			UserID         int64     `json:"user_id"`
			GroupID        int64     `json:"group_id"`
			TermStart      time.Time `json:"term_start_at"`
			OriginalEnd    time.Time `json:"original_term_end_at"`
			CurrentEnd     time.Time `json:"current_term_end_at"`
		} `json:"after"`
	}
	if json.Unmarshal([]byte(detail), &audit) != nil || audit.Kind != "subscription_grant_backfill" ||
		audit.Before.Subscription.ID != sub.ID || audit.Before.Subscription.UserID != sub.UserID ||
		audit.Before.Subscription.GroupID != sub.GroupID || !audit.Before.Subscription.StartsAt.Equal(sub.StartsAt) ||
		(!successorProven && audit.Before.Subscription.ExpiresAt.Nanosecond() != sub.StartsAt.Nanosecond()) ||
		audit.After.OrderID != grant.OrderID || audit.After.SubscriptionID != grant.SubscriptionID ||
		audit.After.UserID != grant.UserID || audit.After.GroupID != grant.GroupID ||
		!audit.After.CurrentEnd.Equal(grant.OriginalEnd) ||
		!audit.After.TermStart.Equal(grant.TermStart) || !audit.After.OriginalEnd.Equal(grant.OriginalEnd) {
		return grant
	}
	// The audit proves both the actual lifecycle anchor and the serialized term.
	// Shift both immutable boundaries equally in memory so purchased duration
	// remains unchanged. Never use a drifting current expiry to infer this offset.
	precise := *grant
	offset := sub.StartsAt.Sub(grant.TermStart)
	precise.TermStart = precise.TermStart.Add(offset)
	precise.OriginalEnd = precise.OriginalEnd.Add(offset)
	if grant.RefundedSeconds == 0 && grant.RefundedCashMinor == 0 && grant.CurrentEnd.Equal(grant.OriginalEnd) {
		precise.CurrentEnd = precise.CurrentEnd.Add(offset)
	}
	return &precise
}
