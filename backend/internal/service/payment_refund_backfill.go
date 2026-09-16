package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	refundBackfillEvidencePaymentAuditAndSubscription = "payment_audit_and_subscription"
	refundBackfillEvidenceProviderReceipt             = "provider_receipt"
	refundBackfillEvidenceDatabaseBackup              = "database_backup"
	refundBackfillEvidenceOther                       = "other"
	refundBackfillEvidenceDetailMaxRunes              = 2000
	refundProvenanceBackfillAuditAction               = "REFUND_PROVENANCE_BACKFILL"
	// Historical subscription expiry timestamps were persisted at whole-second
	// precision while starts could retain sub-second precision. Keep this
	// tolerance narrow: it only repairs that serialization boundary and never
	// changes the immutable purchased duration or overlap checks.
	refundBackfillLifecyclePrecisionTolerance = time.Second
)

// SubscriptionGrantBackfillHint is supplied only for a historical subscription
// order whose paid term cannot yet be proven. It gives the admin enough
// immutable purchase evidence to perform a separate audit; it never contains a
// cash amount because the service calculates that only after the provenance is
// written.
type SubscriptionGrantBackfillHint struct {
	AuditRevision           string                               `json:"audit_revision"`
	SuggestedSubscriptionID int64                                `json:"suggested_subscription_id"`
	SuggestedTermStartAt    time.Time                            `json:"suggested_term_start_at"`
	SuggestedTermEndAt      time.Time                            `json:"suggested_term_end_at"`
	EvidenceSource          string                               `json:"evidence_source"`
	SubscriptionGroupID     int64                                `json:"subscription_group_id"`
	PurchasedDays           int                                  `json:"purchased_days"`
	Candidates              []SubscriptionGrantBackfillCandidate `json:"candidates"`
}

// SubscriptionGrantBackfillCandidate is deliberately constrained to an
// existing subscription for the order's user and group. The administrator must
// still supply the audited term; the candidate lifetime is not treated as an
// inferred purchase term.
type SubscriptionGrantBackfillCandidate struct {
	SubscriptionID int64     `json:"subscription_id"`
	StartsAt       time.Time `json:"starts_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	Status         string    `json:"status"`
}

// subscriptionGrantBackfillOccupiedTerm is existing immutable provenance on a
// candidate subscription. It is kept private because the administrator only
// needs the resulting free-term suggestion, while the complete set still
// participates in the audit revision.
type subscriptionGrantBackfillOccupiedTerm struct {
	PaymentOrderID int64
	SubscriptionID int64
	TermStartAt    time.Time
	TermEndAt      time.Time
}

// SubscriptionGrantBackfillInput is an audit assertion, not a refund request.
// Amounts and entitlement effects are intentionally absent so a client cannot
// choose a cash value or overwrite the server-calculated refund quote.
type SubscriptionGrantBackfillInput struct {
	AuditRevision  string
	SubscriptionID int64
	TermStartAt    time.Time
	TermEndAt      time.Time
	EvidenceSource string
	EvidenceDetail string
	OperatorID     int64
}

type subscriptionGrantBackfillSnapshot struct {
	GroupID       int64
	PurchasedDays int
}

// paymentSubscriptionBackfillSnapshot treats the immutable product snapshot
// as historical truth. Newer rows duplicate identity and days in columns,
// which are cross-checked when present, but pre-column legacy rows can still
// be recovered from an intact immutable snapshot.
func paymentSubscriptionBackfillSnapshot(order *dbent.PaymentOrder) (subscriptionGrantBackfillSnapshot, bool) {
	if order == nil || order.ProductSnapshot == nil {
		return subscriptionGrantBackfillSnapshot{}, false
	}
	rawDays, ok := order.ProductSnapshot["subscription_days"]
	if !ok {
		return subscriptionGrantBackfillSnapshot{}, false
	}
	days, ok := paymentSnapshotInt64(rawDays)
	if !ok || days <= 0 || days > int64(MaxValidityDays) {
		return subscriptionGrantBackfillSnapshot{}, false
	}
	if order.SubscriptionDays != nil && (*order.SubscriptionDays <= 0 || int64(*order.SubscriptionDays) != days) {
		return subscriptionGrantBackfillSnapshot{}, false
	}

	groupID := int64(0)
	if order.SubscriptionGroupID != nil && *order.SubscriptionGroupID > 0 {
		groupID = *order.SubscriptionGroupID
	}
	if rawGroupID, exists := order.ProductSnapshot["group_id"]; exists {
		snapshotGroupID, groupOK := paymentSnapshotInt64(rawGroupID)
		if !groupOK || snapshotGroupID <= 0 || (groupID > 0 && groupID != snapshotGroupID) {
			return subscriptionGrantBackfillSnapshot{}, false
		}
		groupID = snapshotGroupID
	}
	if groupID <= 0 {
		return subscriptionGrantBackfillSnapshot{}, false
	}
	return subscriptionGrantBackfillSnapshot{GroupID: groupID, PurchasedDays: int(days)}, true
}

func subscriptionGrantBackfillAuditRevision(order *dbent.PaymentOrder, snapshot subscriptionGrantBackfillSnapshot, candidates []SubscriptionGrantBackfillCandidate, occupied []subscriptionGrantBackfillOccupiedTerm) string {
	if order == nil || snapshot.GroupID <= 0 || snapshot.PurchasedDays <= 0 {
		return ""
	}
	completedAt := ""
	if order.CompletedAt != nil {
		completedAt = order.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	paidAt := ""
	if order.PaidAt != nil {
		paidAt = order.PaidAt.UTC().Format(time.RFC3339Nano)
	}
	parts := []string{
		"subscription-grant-backfill",
		strconv.FormatInt(order.ID, 10),
		strconv.FormatInt(order.UserID, 10),
		strconv.FormatInt(snapshot.GroupID, 10),
		strconv.Itoa(snapshot.PurchasedDays),
		order.OrderType,
		order.Status,
		decimalString(order.RefundAmount),
		decimalString(order.RefundRequestedAmount),
		paidAt,
		completedAt,
		order.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	for _, candidate := range candidates {
		parts = append(parts,
			strconv.FormatInt(candidate.SubscriptionID, 10),
			candidate.StartsAt.UTC().Format(time.RFC3339Nano),
			candidate.ExpiresAt.UTC().Format(time.RFC3339Nano),
			candidate.Status,
		)
	}
	for _, term := range occupied {
		parts = append(parts,
			strconv.FormatInt(term.PaymentOrderID, 10),
			strconv.FormatInt(term.SubscriptionID, 10),
			term.TermStartAt.UTC().Format(time.RFC3339Nano),
			term.TermEndAt.UTC().Format(time.RFC3339Nano),
		)
	}
	return refundReviewRevision(parts...)
}

func (s *PaymentService) subscriptionGrantBackfillHint(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder) (*SubscriptionGrantBackfillHint, error) {
	snapshot, ok := paymentSubscriptionBackfillSnapshot(order)
	if !ok || client == nil {
		return nil, nil
	}
	candidates, err := client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(order.UserID),
			usersubscription.GroupIDEQ(snapshot.GroupID),
			usersubscription.DeletedAtIsNil(),
			usersubscription.StatusEQ(SubscriptionStatusActive),
		).
		Order(usersubscription.ByID()).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("load subscription backfill candidates: %w", err)
	}
	hint := &SubscriptionGrantBackfillHint{
		SubscriptionGroupID: snapshot.GroupID,
		PurchasedDays:       snapshot.PurchasedDays,
		Candidates:          make([]SubscriptionGrantBackfillCandidate, 0, len(candidates)),
	}
	for _, candidate := range candidates {
		hint.Candidates = append(hint.Candidates, SubscriptionGrantBackfillCandidate{
			SubscriptionID: candidate.ID,
			StartsAt:       candidate.StartsAt,
			ExpiresAt:      candidate.ExpiresAt,
			Status:         candidate.Status,
		})
	}
	occupied, err := subscriptionGrantBackfillOccupiedTerms(ctx, client, order.ID, hint.Candidates)
	if err != nil {
		return nil, fmt.Errorf("load occupied subscription grant terms: %w", err)
	}
	suggested, termStart, termEnd, found := subscriptionGrantBackfillSuggestion(snapshot.PurchasedDays, hint.Candidates, occupied)
	if !found {
		return nil, nil
	}
	hint.SuggestedSubscriptionID = suggested.SubscriptionID
	hint.SuggestedTermStartAt = termStart
	hint.SuggestedTermEndAt = termEnd
	hint.EvidenceSource = refundBackfillEvidencePaymentAuditAndSubscription
	hint.AuditRevision = subscriptionGrantBackfillAuditRevision(order, snapshot, hint.Candidates, occupied)
	return hint, nil
}

func subscriptionGrantBackfillOccupiedTerms(ctx context.Context, client *dbent.Client, orderID int64, candidates []SubscriptionGrantBackfillCandidate) ([]subscriptionGrantBackfillOccupiedTerm, error) {
	if client == nil || len(candidates) == 0 {
		return nil, nil
	}
	args := []any{orderID}
	placeholders := make([]string, 0, len(candidates))
	seen := make(map[int64]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.SubscriptionID <= 0 {
			continue
		}
		if _, exists := seen[candidate.SubscriptionID]; exists {
			continue
		}
		seen[candidate.SubscriptionID] = struct{}{}
		args = append(args, candidate.SubscriptionID)
		placeholders = append(placeholders, "$"+strconv.Itoa(len(args)))
	}
	if len(placeholders) == 0 {
		return nil, nil
	}
	rows, err := client.QueryContext(ctx, `SELECT payment_order_id, subscription_id, term_start_at, original_term_end_at
		FROM payment_subscription_grants
		WHERE payment_order_id <> $1 AND subscription_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY subscription_id ASC, term_start_at ASC, original_term_end_at ASC, payment_order_id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	occupied := make([]subscriptionGrantBackfillOccupiedTerm, 0)
	for rows.Next() {
		var term subscriptionGrantBackfillOccupiedTerm
		if err := rows.Scan(&term.PaymentOrderID, &term.SubscriptionID, &term.TermStartAt, &term.TermEndAt); err != nil {
			return nil, err
		}
		occupied = append(occupied, term)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return occupied, nil
}

// subscriptionGrantBackfillSuggestion pre-fills only a term that fits in a
// candidate lifecycle and does not intersect another order's immutable grant.
// It is not provenance: the administrator still submits the asserted dates and
// evidence, then the transaction locks and rechecks them before insertion.
func subscriptionGrantBackfillSuggestion(purchasedDays int, candidates []SubscriptionGrantBackfillCandidate, occupied []subscriptionGrantBackfillOccupiedTerm) (SubscriptionGrantBackfillCandidate, time.Time, time.Time, bool) {
	if purchasedDays <= 0 {
		return SubscriptionGrantBackfillCandidate{}, time.Time{}, time.Time{}, false
	}
	var selected SubscriptionGrantBackfillCandidate
	var selectedStart time.Time
	var selectedEnd time.Time
	for _, candidate := range candidates {
		lifecycleStart := candidate.StartsAt.UTC()
		lifecycleEnd := candidate.ExpiresAt.UTC()
		if !lifecycleEnd.After(lifecycleStart) {
			continue
		}
		terms := make([]subscriptionGrantBackfillOccupiedTerm, 0)
		for _, term := range occupied {
			if term.SubscriptionID != candidate.SubscriptionID || !term.TermEndAt.After(lifecycleStart) || !term.TermStartAt.Before(lifecycleEnd) {
				continue
			}
			term.TermStartAt = maxBackfillTime(term.TermStartAt.UTC(), lifecycleStart)
			term.TermEndAt = minBackfillTime(term.TermEndAt.UTC(), lifecycleEnd)
			if term.TermEndAt.After(term.TermStartAt) {
				terms = append(terms, term)
			}
		}
		sort.Slice(terms, func(i, j int) bool {
			if terms[i].TermStartAt.Equal(terms[j].TermStartAt) {
				return terms[i].TermEndAt.Before(terms[j].TermEndAt)
			}
			return terms[i].TermStartAt.Before(terms[j].TermStartAt)
		})

		// An exact purchased-day term may begin a few milliseconds before the
		// stored lifecycle when the historical expiry lost fractional seconds.
		// Search only within the bounded compatibility window; the transaction
		// repeats the same lifecycle check after locking the subscription.
		cursor := lifecycleStart.Add(-refundBackfillLifecyclePrecisionTolerance)
		for _, term := range append(terms, subscriptionGrantBackfillOccupiedTerm{TermStartAt: lifecycleEnd, TermEndAt: lifecycleEnd}) {
			if term.TermStartAt.After(cursor) {
				end := term.TermStartAt
				start := end.AddDate(0, 0, -purchasedDays)
				if !start.Before(cursor) && betterSubscriptionGrantBackfillSuggestion(candidate, start, end, selected, selectedStart, selectedEnd) {
					selected, selectedStart, selectedEnd = candidate, start, end
				}
			}
			if term.TermEndAt.After(cursor) {
				cursor = term.TermEndAt
			}
		}
	}
	if selected.SubscriptionID == 0 {
		return SubscriptionGrantBackfillCandidate{}, time.Time{}, time.Time{}, false
	}
	return selected, selectedStart, selectedEnd, true
}

func betterSubscriptionGrantBackfillSuggestion(candidate SubscriptionGrantBackfillCandidate, start, end time.Time, selected SubscriptionGrantBackfillCandidate, selectedStart, selectedEnd time.Time) bool {
	if selected.SubscriptionID == 0 {
		return true
	}
	// Prefer the most recent unoccupied term, preserving the prior behavior
	// when several active subscription records are available. Within one
	// lifecycle, the latest complete gap preserves the historical tail-based
	// suggestion while still stopping at the next proven grant boundary.
	if candidate.ExpiresAt.After(selected.ExpiresAt) {
		return true
	}
	if candidate.ExpiresAt.Before(selected.ExpiresAt) {
		return false
	}
	if candidate.SubscriptionID != selected.SubscriptionID {
		return candidate.SubscriptionID > selected.SubscriptionID
	}
	if start.Equal(selectedStart) {
		return end.Before(selectedEnd)
	}
	return start.After(selectedStart)
}

func maxBackfillTime(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}

func minBackfillTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func (s *PaymentService) manualSubscriptionGrantBackfillReview(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, now time.Time) (*RefundReview, error) {
	review := manualRefundReview(order, now, "LEGACY_SUBSCRIPTION_UNATTRIBUTED", "this historical order has no provable subscription term")
	hint, err := s.subscriptionGrantBackfillHint(ctx, client, order)
	if err != nil {
		return nil, err
	}
	review.SubscriptionBackfill = hint
	return review, nil
}

func normalizeSubscriptionGrantBackfillInput(input SubscriptionGrantBackfillInput) (SubscriptionGrantBackfillInput, error) {
	input.AuditRevision = strings.TrimSpace(input.AuditRevision)
	if input.AuditRevision == "" {
		return input, infraerrors.BadRequest("SUBSCRIPTION_BACKFILL_AUDIT_REVISION_REQUIRED", "audit revision is required")
	}
	if input.SubscriptionID <= 0 {
		return input, infraerrors.BadRequest("SUBSCRIPTION_BACKFILL_SUBSCRIPTION_REQUIRED", "subscription id is required")
	}
	if input.OperatorID <= 0 {
		return input, infraerrors.Forbidden("SUBSCRIPTION_BACKFILL_OPERATOR_REQUIRED", "a human administrator is required")
	}
	if input.TermStartAt.IsZero() || input.TermEndAt.IsZero() || !input.TermEndAt.After(input.TermStartAt) {
		return input, infraerrors.BadRequest("SUBSCRIPTION_BACKFILL_INVALID_TERM", "term start and end must form a non-empty interval")
	}
	input.TermStartAt = input.TermStartAt.UTC()
	input.TermEndAt = input.TermEndAt.UTC()
	input.EvidenceSource = strings.ToLower(strings.TrimSpace(input.EvidenceSource))
	if input.EvidenceSource == "" {
		input.EvidenceSource = refundBackfillEvidencePaymentAuditAndSubscription
	}
	switch input.EvidenceSource {
	case refundBackfillEvidencePaymentAuditAndSubscription,
		refundBackfillEvidenceProviderReceipt,
		refundBackfillEvidenceDatabaseBackup,
		refundBackfillEvidenceOther:
	default:
		return input, infraerrors.BadRequest("SUBSCRIPTION_BACKFILL_EVIDENCE_SOURCE_INVALID", "evidence source is invalid")
	}
	if !utf8.ValidString(input.EvidenceDetail) || strings.Contains(input.EvidenceDetail, "\x00") {
		return input, infraerrors.BadRequest("SUBSCRIPTION_BACKFILL_EVIDENCE_DETAIL_INVALID", "evidence detail is invalid")
	}
	input.EvidenceDetail = strings.Join(strings.Fields(input.EvidenceDetail), " ")
	if utf8.RuneCountInString(input.EvidenceDetail) > refundBackfillEvidenceDetailMaxRunes {
		return input, infraerrors.BadRequest("SUBSCRIPTION_BACKFILL_EVIDENCE_DETAIL_INVALID", "evidence detail is too long")
	}
	if input.EvidenceDetail == "" {
		return input, infraerrors.BadRequest("SUBSCRIPTION_BACKFILL_EVIDENCE_DETAIL_REQUIRED", "evidence detail is required")
	}
	return input, nil
}

// BackfillSubscriptionGrant records audited historical provenance only. It
// locks the financial order before the chosen subscription, matching the refund
// reservation lock order, then rechecks every mutable condition before writing
// a single immutable grant and audit row.
func (s *PaymentService) BackfillSubscriptionGrant(ctx context.Context, orderID int64, input SubscriptionGrantBackfillInput) (*RefundReview, error) {
	input, err := normalizeSubscriptionGrantBackfillInput(input)
	if err != nil {
		return nil, err
	}
	if s == nil || s.entClient == nil {
		return nil, infraerrors.InternalServer("SUBSCRIPTION_BACKFILL_UNAVAILABLE", "subscription backfill storage is unavailable")
	}
	if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
		if err := s.backfillSubscriptionGrantTx(ctx, existingTx.Client(), orderID, input); err != nil {
			return nil, err
		}
		return s.reviewRefundWithClient(ctx, existingTx.Client(), orderID, s.refundValuationTime(), false)
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin subscription grant backfill transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	txCtx := dbent.NewTxContext(ctx, tx)
	if err := s.backfillSubscriptionGrantTx(txCtx, tx.Client(), orderID, input); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit subscription grant backfill: %w", err)
	}
	committed = true
	return s.ReviewRefund(ctx, orderID)
}

func (s *PaymentService) backfillSubscriptionGrantTx(ctx context.Context, client *dbent.Client, orderID int64, input SubscriptionGrantBackfillInput) error {
	order, err := lockUnifiedRefundOrder(ctx, client, orderID)
	if err != nil {
		if dbent.IsNotFound(err) {
			return infraerrors.NotFound("NOT_FOUND", "order not found")
		}
		return fmt.Errorf("lock subscription backfill order: %w", err)
	}
	if order.OrderType != payment.OrderTypeSubscription {
		return infraerrors.BadRequest("SUBSCRIPTION_BACKFILL_ORDER_TYPE_INVALID", "only subscription orders can receive subscription provenance")
	}
	if subscriptionGrantBackfillRefundInFlight(order) {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_REFUND_IN_FLIGHT", "a refund is already in flight for this order")
	}
	if !refundReviewStateAllowed(order) {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_ORDER_STATUS_INVALID", "order status does not allow subscription provenance backfill")
	}
	snapshot, ok := paymentSubscriptionBackfillSnapshot(order)
	if !ok {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_SNAPSHOT_UNVERIFIED", "the immutable subscription duration snapshot is unavailable")
	}

	existingGrant, existingSubscription, grantErr := loadPaymentSubscriptionRefundState(ctx, client, order.ID, true)
	if grantErr == nil {
		return verifySubscriptionGrantBackfillReplay(ctx, client, order, existingGrant, existingSubscription, input)
	}
	if !errors.Is(grantErr, errRefundAccountingMissing) {
		return fmt.Errorf("load subscription backfill grant: %w", grantErr)
	}

	pending, err := paymentOrderHasPendingUnifiedRefundAttempt(ctx, client, order.ID)
	if err != nil {
		return fmt.Errorf("check subscription backfill refund attempt: %w", err)
	}
	if pending {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_REFUND_IN_FLIGHT", "a refund attempt is still in flight")
	}

	review, err := s.reviewRefundWithClient(ctx, client, order.ID, s.refundValuationTime(), true)
	if err != nil {
		return err
	}
	if review.ReasonCode != "LEGACY_SUBSCRIPTION_UNATTRIBUTED" || review.SubscriptionBackfill == nil {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_NOT_REQUIRED", "this order is not eligible for subscription provenance backfill")
	}
	if input.AuditRevision != review.SubscriptionBackfill.AuditRevision {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_AUDIT_STALE", "the backfill review changed; reload it before submitting")
	}
	if !input.TermStartAt.AddDate(0, 0, snapshot.PurchasedDays).Equal(input.TermEndAt) {
		return infraerrors.BadRequest("SUBSCRIPTION_BACKFILL_TERM_DURATION_MISMATCH", "term duration must exactly match the immutable purchased subscription days")
	}

	subscription, err := lockSubscriptionGrantBackfillCandidate(ctx, client, input.SubscriptionID)
	if err != nil {
		return err
	}
	if subscription.UserID != order.UserID || subscription.GroupID != snapshot.GroupID ||
		subscription.DeletedAt != nil || subscription.Status != SubscriptionStatusActive {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_SUBSCRIPTION_MISMATCH", "subscription does not match the order user and group")
	}
	if input.TermStartAt.Before(subscription.StartsAt.Add(-refundBackfillLifecyclePrecisionTolerance)) ||
		input.TermEndAt.After(subscription.ExpiresAt.Add(refundBackfillLifecyclePrecisionTolerance)) {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_TERM_OUTSIDE_LIFECYCLE", "term must stay within the selected subscription lifecycle")
	}
	if input.EvidenceSource == refundBackfillEvidencePaymentAuditAndSubscription {
		matched, evidenceErr := hasSubscriptionBackfillPaymentAudit(ctx, client, order.ID, snapshot.GroupID, snapshot.PurchasedDays, subscription.ID)
		if evidenceErr != nil {
			return fmt.Errorf("verify subscription backfill payment audit: %w", evidenceErr)
		}
		if !matched {
			return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_AUDIT_EVIDENCE_MISSING", "payment audit does not prove this subscription assignment")
		}
	}
	overlaps, err := subscriptionGrantBackfillOverlaps(ctx, client, subscription.ID, order.ID, input.TermStartAt, input.TermEndAt)
	if err != nil {
		return fmt.Errorf("check subscription backfill overlap: %w", err)
	}
	if overlaps {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_TERM_OVERLAP", "term overlaps another payment subscription grant")
	}
	if existingAudit, auditErr := hasSubscriptionBackfillAudit(ctx, client, order.ID); auditErr != nil {
		return fmt.Errorf("check subscription backfill audit: %w", auditErr)
	} else if existingAudit {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_AUDIT_CONFLICT", "a subscription provenance backfill audit already exists for this order")
	}

	entitlements, err := paymentOrderEntitlementsStrict(order)
	if err != nil {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_SNAPSHOT_UNVERIFIED", "the immutable entitlement snapshot is invalid")
	}
	resetCardCommitment, err := entitlements.ResetCardTotalCommitment()
	if err != nil {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_SNAPSHOT_UNVERIFIED", "the immutable reset-card entitlement snapshot is invalid")
	}
	inserted, err := client.ExecContext(ctx, `INSERT INTO payment_subscription_grants
		(payment_order_id, subscription_id, user_id, group_id, term_start_at,
		 original_term_end_at, current_term_end_at, balance_bonus,
		 reset_card_count, concurrency_target)
		VALUES ($1,$2,$3,$4,$5,$6,$6,$7,$8,$9)
		ON CONFLICT (payment_order_id) DO NOTHING`,
		order.ID, subscription.ID, order.UserID, subscription.GroupID, input.TermStartAt, input.TermEndAt,
		entitlements.BalanceBonus, resetCardCommitment, entitlements.Concurrency)
	if err != nil {
		return fmt.Errorf("insert subscription backfill grant: %w", err)
	}
	if err := requireSingleAffected(inserted, "insert subscription backfill grant"); err != nil {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_GRANT_CONFLICT", "subscription provenance changed while the backfill was being written")
	}

	detail, err := subscriptionGrantBackfillAuditDetail(order, subscription, input, entitlements.BalanceBonus, resetCardCommitment, entitlements.Concurrency)
	if err != nil {
		return err
	}
	if _, err := client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction(refundProvenanceBackfillAuditAction).
		SetDetail(detail).
		SetOperator(fmt.Sprintf("admin:%d", input.OperatorID)).
		Save(ctx); err != nil {
		if dbent.IsConstraintError(err) {
			return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_AUDIT_CONFLICT", "subscription provenance backfill was already recorded")
		}
		return fmt.Errorf("append subscription backfill audit: %w", err)
	}
	return nil
}

func subscriptionGrantBackfillRefundInFlight(order *dbent.PaymentOrder) bool {
	if order == nil {
		return false
	}
	return order.Status == OrderStatusRefundRequested || order.Status == OrderStatusRefunding || order.Status == OrderStatusRefundPending
}

func lockSubscriptionGrantBackfillCandidate(ctx context.Context, client *dbent.Client, subscriptionID int64) (*dbent.UserSubscription, error) {
	query := client.UserSubscription.Query().Where(usersubscription.IDEQ(subscriptionID))
	if paymentAuditDialect(client) == dialect.Postgres {
		query.ForUpdate()
	}
	subscription, err := query.Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, infraerrors.NotFound("SUBSCRIPTION_BACKFILL_SUBSCRIPTION_NOT_FOUND", "subscription not found")
		}
		return nil, fmt.Errorf("lock subscription backfill candidate: %w", err)
	}
	return subscription, nil
}

func paymentOrderHasPendingUnifiedRefundAttempt(ctx context.Context, client *dbent.Client, orderID int64) (bool, error) {
	rows, err := client.QueryContext(ctx, `SELECT COUNT(*) FROM unified_payment_refund_attempts WHERE order_id = $1 AND status = $2`, orderID, unifiedRefundPending)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false, rows.Err()
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, rows.Err()
}

func subscriptionGrantBackfillOverlaps(ctx context.Context, client *dbent.Client, subscriptionID, orderID int64, start, end time.Time) (bool, error) {
	query := `SELECT payment_order_id FROM payment_subscription_grants
		WHERE subscription_id = $1 AND payment_order_id <> $2
		  AND term_start_at < $3 AND original_term_end_at > $4
		LIMIT 1`
	if paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, subscriptionID, orderID, end, start)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false, rows.Err()
	}
	var existingOrderID int64
	if err := rows.Scan(&existingOrderID); err != nil {
		return false, err
	}
	return true, rows.Err()
}

func hasSubscriptionBackfillPaymentAudit(ctx context.Context, client *dbent.Client, orderID, groupID int64, days int, subscriptionID int64) (bool, error) {
	logs, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
		paymentauditlog.ActionIn("SUBSCRIPTION_ASSIGNED", "SUBSCRIPTION_SUCCESS"),
	).All(ctx)
	if err != nil {
		return false, err
	}
	for _, log := range logs {
		if subscriptionBackfillAuditDetailMatches(log.Detail, groupID, days, subscriptionID) {
			return true, nil
		}
	}
	return false, nil
}

func subscriptionBackfillAuditDetailMatches(raw string, groupID int64, days int, subscriptionID int64) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(raw), &detail); err != nil {
		return false
	}
	groupMatched := false
	daysMatched := false
	for _, check := range []struct {
		keys     []string
		want     int64
		required *bool
	}{
		{keys: []string{"groupID", "group_id"}, want: groupID, required: &groupMatched},
		{keys: []string{"validityDays", "subscription_days"}, want: int64(days), required: &daysMatched},
		{keys: []string{"subscriptionID", "subscription_id"}, want: subscriptionID},
	} {
		for _, key := range check.keys {
			rawValue, exists := detail[key]
			if !exists {
				continue
			}
			value, ok := paymentSnapshotInt64(rawValue)
			if !ok || value != check.want {
				return false
			}
			if check.required != nil {
				*check.required = true
			}
		}
	}
	return groupMatched && daysMatched
}

func subscriptionGrantBackfillAuditDetail(order *dbent.PaymentOrder, subscription *dbent.UserSubscription, input SubscriptionGrantBackfillInput, balanceBonus float64, resetCardCount, concurrencyTarget int) (string, error) {
	detail, err := json.Marshal(map[string]any{
		"kind": "subscription_grant_backfill",
		"operator": map[string]any{
			"id":    input.OperatorID,
			"label": fmt.Sprintf("admin:%d", input.OperatorID),
		},
		"evidence": map[string]any{
			"source": input.EvidenceSource,
			"detail": input.EvidenceDetail,
		},
		"before": map[string]any{
			"subscription_grant": nil,
			"subscription": map[string]any{
				"id": subscription.ID, "user_id": subscription.UserID, "group_id": subscription.GroupID,
				"starts_at": subscription.StartsAt, "expires_at": subscription.ExpiresAt, "status": subscription.Status,
			},
		},
		"after": map[string]any{
			"payment_order_id": order.ID, "subscription_id": subscription.ID, "user_id": order.UserID,
			"group_id": subscription.GroupID, "term_start_at": input.TermStartAt,
			"original_term_end_at": input.TermEndAt, "current_term_end_at": input.TermEndAt,
			"balance_bonus": balanceBonus, "reset_card_count": resetCardCount, "concurrency_target": concurrencyTarget,
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal subscription backfill audit: %w", err)
	}
	return string(detail), nil
}

func hasSubscriptionBackfillAudit(ctx context.Context, client *dbent.Client, orderID int64) (bool, error) {
	return client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
		paymentauditlog.ActionEQ(refundProvenanceBackfillAuditAction),
	).Exist(ctx)
}

func verifySubscriptionGrantBackfillReplay(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, grant *paymentSubscriptionGrant, subscription *dbent.UserSubscription, input SubscriptionGrantBackfillInput) error {
	if order == nil || grant == nil || subscription == nil || grant.SubscriptionID != input.SubscriptionID ||
		!grant.TermStart.Equal(input.TermStartAt) || !grant.OriginalEnd.Equal(input.TermEndAt) || !grant.CurrentEnd.Equal(input.TermEndAt) {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_GRANT_CONFLICT", "an immutable subscription grant already exists for this order")
	}
	log, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
		paymentauditlog.ActionEQ(refundProvenanceBackfillAuditAction),
	).Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_GRANT_CONFLICT", "an unverified subscription grant already exists for this order")
		}
		return err
	}
	if !subscriptionGrantBackfillAuditReplayMatches(log.Detail, input) {
		return infraerrors.Conflict("SUBSCRIPTION_BACKFILL_AUDIT_CONFLICT", "the existing provenance audit does not match this backfill request")
	}
	return nil
}

func subscriptionGrantBackfillAuditReplayMatches(raw string, input SubscriptionGrantBackfillInput) bool {
	var detail struct {
		Kind     string `json:"kind"`
		Evidence struct {
			Source string `json:"source"`
			Detail string `json:"detail"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(raw), &detail); err != nil {
		return false
	}
	return detail.Kind == "subscription_grant_backfill" && detail.Evidence.Source == input.EvidenceSource && detail.Evidence.Detail == input.EvidenceDetail
}
