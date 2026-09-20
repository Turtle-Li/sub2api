package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
)

const (
	refundBenefitStateActive                  = "ACTIVE"
	refundBenefitStateReserved                = "RESERVED"
	refundBenefitStateRevoked                 = "REVOKED"
	refundBenefitConcurrencyEventSet          = "SET"
	refundBenefitConcurrencyEventDelta        = "DELTA"
	refundBenefitConcurrencyEventPaymentMax   = "PAYMENT_MAX"
	refundBenefitConcurrencyEventUnattributed = "UNATTRIBUTED"
	maxUserConcurrency                        = math.MaxInt32
)

var errRefundBenefitProvenanceMissing = errors.New("refund benefit provenance is missing")

type paymentRefundBenefitSource struct {
	ID                       int64
	PaymentOrderID           int64
	SubscriptionGrantOrderID int64
	UserID                   int64
	SubscriptionID           int64
	GroupID                  int64
	SourceOrigin             string
	ResetCardsCommitted      int
	ResetCardDeliveryMode    string
	ConcurrencyBefore        *int
	ConcurrencyTarget        int
	ConcurrencyAfterGrant    *int
	State                    string
	ReservedProductRefundNo  string
	ReservationProofDigest   string
	ReservedAt               *time.Time
	RevokedAt                *time.Time
	CreatedAt                time.Time
}

type paymentRefundBenefitResetCardGrant struct {
	ID             int64
	SubscriptionID int64
	UserID         int64
	GroupID        int64
	Quantity       int
	UsedCount      int
	ExpiresAt      time.Time
}

// paymentRefundBenefitReviewState is kept only inside the trusted refund
// service. The public RefundBenefitsReview deliberately exposes the result,
// while this value retains the exact immutable evidence that must be checked
// again after the unified-refund attempt row has been inserted.
type paymentRefundBenefitReviewState struct {
	Source             *paymentRefundBenefitSource
	Cards              []paymentRefundBenefitResetCardGrant
	Schedule           *monthlyResetCardSchedule
	ConcurrencyCurrent int
	ConcurrencyAfter   int
	ProofDigest        string
	Legacy             bool
}

type paymentRefundConcurrencyEvent struct {
	Sequence       int64
	Kind           string
	RequestedDelta *int
	Target         *int
	SourceID       int64
	SourceState    string
	Before         int
	After          int
}

type paymentRefundBenefitDeniedError struct {
	Code   string
	Reason string
}

func (e *paymentRefundBenefitDeniedError) Error() string {
	if e == nil {
		return "payment refund benefit review denied"
	}
	return e.Reason
}

func denyPaymentRefundBenefit(code, reason string) error {
	return &paymentRefundBenefitDeniedError{Code: code, Reason: reason}
}

func paymentRefundBenefitDenial(err error) (code, reason string, ok bool) {
	var denied *paymentRefundBenefitDeniedError
	if errors.As(err, &denied) {
		return denied.Code, denied.Reason, true
	}
	if errors.Is(err, errRefundBenefitProvenanceMissing) {
		return "BENEFIT_PROVENANCE_MISSING", "payment benefit provenance is incomplete", true
	}
	return "", "", false
}

func paymentRefundBenefitSourceRequired(order *dbent.PaymentOrder) (bool, PlanEntitlements, error) {
	entitlements, err := paymentOrderEntitlementsStrict(order)
	if err != nil {
		return false, PlanEntitlements{}, err
	}
	commitment, err := entitlements.ResetCardTotalCommitment()
	if err != nil {
		return false, PlanEntitlements{}, err
	}
	return commitment > 0 || entitlements.Concurrency > 0, entitlements, nil
}

func normalizedPaymentConcurrencyTarget(value int) (int, error) {
	if value < 0 || value > maxUserConcurrency {
		return 0, fmt.Errorf("payment concurrency target is out of range")
	}
	return value, nil
}

// createPaymentRefundBenefitSource records a post-P19 benefit snapshot before
// an entitlement changes users.concurrency or creates card rows. The caller
// already holds order -> term-grant -> subscription locks; this function adds
// the user lock before any source card rows can be inserted.
func createPaymentRefundBenefitSource(
	ctx context.Context,
	client *dbent.Client,
	order *dbent.PaymentOrder,
	entitlements PlanEntitlements,
	grant *paymentSubscriptionGrant,
	subscription *dbent.UserSubscription,
) (*paymentRefundBenefitSource, error) {
	if client == nil || order == nil || paymentAuditDialect(client) != dialect.Postgres {
		return nil, nil
	}
	commitment, err := entitlements.ResetCardTotalCommitment()
	if err != nil {
		return nil, err
	}
	if commitment <= 0 && entitlements.Concurrency <= 0 {
		return nil, nil
	}
	// The normal subscription fulfillment path always has the exact term grant
	// and subscription lock. The nil path is retained only for historical
	// recovery callers; it must not manufacture a post-P19 source snapshot.
	if order.OrderType == payment.OrderTypeSubscription && (grant == nil || subscription == nil) {
		return nil, nil
	}
	if commitment > math.MaxInt32 {
		return nil, errors.New("payment reset-card commitment is out of range")
	}
	target, err := normalizedPaymentConcurrencyTarget(entitlements.Concurrency)
	if err != nil {
		return nil, err
	}

	// This lock is intentionally taken after subscription and before reset-card
	// rows. It serializes the source snapshot with every concurrency event.
	rows, err := client.QueryContext(ctx, `SELECT concurrency FROM users
		WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, order.UserID)
	if err != nil {
		return nil, fmt.Errorf("lock payment benefit user: %w", err)
	}
	var current int
	found := rows.Next()
	if found {
		err = rows.Scan(&current)
	}
	if closeErr := rows.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errRefundBenefitProvenanceMissing
	}
	if current < 0 {
		return nil, errors.New("payment benefit user concurrency is invalid")
	}

	mode := "none"
	if commitment > 0 {
		mode = strings.TrimSpace(entitlements.ResetCardDeliveryMode)
		if mode == "" {
			mode = resetCardDeliveryModeImmediate
		}
	}
	var grantOrderID, subscriptionID, groupID any
	if grant != nil {
		grantOrderID = grant.OrderID
	}
	if subscription != nil {
		subscriptionID = subscription.ID
		groupID = subscription.GroupID
	}
	var concurrencyBefore, concurrencyAfter any
	if target > 0 {
		before := current
		after := current
		if target > after {
			after = target
		}
		concurrencyBefore, concurrencyAfter = before, after
	}
	now := time.Now().UTC()
	rows, err = client.QueryContext(ctx, `INSERT INTO payment_refund_benefit_sources (
		payment_order_id, subscription_grant_order_id, user_id, subscription_id, group_id,
		reset_cards_committed, reset_card_delivery_mode, concurrency_before,
		concurrency_target, concurrency_after_grant, state, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'ACTIVE',$11,$11)
	ON CONFLICT (payment_order_id) DO NOTHING
	RETURNING id`, order.ID, grantOrderID, order.UserID, subscriptionID, groupID,
		commitment, mode, concurrencyBefore, target, concurrencyAfter, now)
	if err != nil {
		return nil, fmt.Errorf("insert payment refund benefit source: %w", err)
	}
	var sourceID int64
	inserted := rows.Next()
	if inserted {
		err = rows.Scan(&sourceID)
	}
	if closeErr := rows.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	source, err := loadPaymentRefundBenefitSource(ctx, client, order.ID, false)
	if err != nil {
		return nil, err
	}
	if source.ID == 0 || (inserted && source.ID != sourceID) ||
		source.UserID != order.UserID || source.ResetCardsCommitted != commitment ||
		source.ResetCardDeliveryMode != mode || source.ConcurrencyTarget != target ||
		!sameOptionalInt(source.ConcurrencyBefore, intPointerOrNil(target, current)) ||
		!sameOptionalInt(source.ConcurrencyAfterGrant, intPointerOrNil(target, max(current, target))) ||
		!sameNullableInt64(source.SubscriptionGrantOrderID, nullableInt64(grant)) ||
		!sameNullableInt64(source.SubscriptionID, nullableSubscriptionID(subscription)) ||
		!sameNullableInt64(source.GroupID, nullableSubscriptionGroupID(subscription)) {
		return nil, errors.New("payment refund benefit source idempotency conflict")
	}
	return source, nil
}

func intPointerOrNil(target, value int) *int {
	if target <= 0 {
		return nil
	}
	result := value
	return &result
}

func sameOptionalInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func nullableInt64(grant *paymentSubscriptionGrant) int64 {
	if grant == nil {
		return 0
	}
	return grant.OrderID
}

func nullableSubscriptionID(subscription *dbent.UserSubscription) int64 {
	if subscription == nil {
		return 0
	}
	return subscription.ID
}

func nullableSubscriptionGroupID(subscription *dbent.UserSubscription) int64 {
	if subscription == nil {
		return 0
	}
	return subscription.GroupID
}

func sameNullableInt64(actual, expected int64) bool {
	return actual == expected
}

func loadPaymentRefundBenefitSource(ctx context.Context, client *dbent.Client, orderID int64, lock bool) (*paymentRefundBenefitSource, error) {
	if client == nil || orderID <= 0 {
		return nil, errRefundBenefitProvenanceMissing
	}
	query := `SELECT id, payment_order_id, COALESCE(subscription_grant_order_id, 0), user_id,
		COALESCE(subscription_id, 0), COALESCE(group_id, 0), source_origin, reset_cards_committed,
		reset_card_delivery_mode, concurrency_before, concurrency_target,
		concurrency_after_grant, state, COALESCE(reserved_product_refund_no, ''),
		COALESCE(reservation_proof_digest, ''), reserved_at, revoked_at, created_at
		FROM payment_refund_benefit_sources WHERE payment_order_id = $1`
	if lock && paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, orderID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, errRefundBenefitProvenanceMissing
	}
	source := &paymentRefundBenefitSource{}
	var before, after sql.NullInt64
	var reservedAt, revokedAt sql.NullTime
	if err := rows.Scan(&source.ID, &source.PaymentOrderID, &source.SubscriptionGrantOrderID,
		&source.UserID, &source.SubscriptionID, &source.GroupID, &source.SourceOrigin, &source.ResetCardsCommitted,
		&source.ResetCardDeliveryMode, &before, &source.ConcurrencyTarget, &after,
		&source.State, &source.ReservedProductRefundNo, &source.ReservationProofDigest,
		&reservedAt, &revokedAt, &source.CreatedAt); err != nil {
		return nil, err
	}
	if before.Valid {
		value := int(before.Int64)
		source.ConcurrencyBefore = &value
	}
	if after.Valid {
		value := int(after.Int64)
		source.ConcurrencyAfterGrant = &value
	}
	if reservedAt.Valid {
		value := reservedAt.Time.UTC()
		source.ReservedAt = &value
	}
	if revokedAt.Valid {
		value := revokedAt.Time.UTC()
		source.RevokedAt = &value
	}
	if (source.SourceOrigin != "fulfillment" && source.SourceOrigin != "legacy_card_fk") ||
		source.ResetCardsCommitted < 0 || source.ConcurrencyTarget < 0 ||
		source.ConcurrencyTarget > maxUserConcurrency ||
		(source.ConcurrencyTarget > 0 && (source.ConcurrencyBefore == nil || source.ConcurrencyAfterGrant == nil)) {
		return nil, errors.New("payment refund benefit source is invalid")
	}
	return source, rows.Err()
}

func loadPaymentRefundBenefitResetCards(ctx context.Context, client *dbent.Client, sourceID int64, lock bool) ([]paymentRefundBenefitResetCardGrant, error) {
	if client == nil || sourceID <= 0 {
		return nil, errRefundBenefitProvenanceMissing
	}
	query := `SELECT card_grant.id, card_grant.subscription_id, card_grant.user_id, card_grant.group_id,
		card_grant.quantity, card_grant.used_count, card_grant.expires_at
		FROM payment_refund_benefit_reset_card_grants link
		JOIN subscription_reset_grants card_grant ON card_grant.id = link.reset_card_grant_id
		WHERE link.source_id = $1
		ORDER BY card_grant.id ASC`
	if lock && paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE OF card_grant`
	}
	rows, err := client.QueryContext(ctx, query, sourceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	grants := make([]paymentRefundBenefitResetCardGrant, 0)
	for rows.Next() {
		var grant paymentRefundBenefitResetCardGrant
		if err := rows.Scan(&grant.ID, &grant.SubscriptionID, &grant.UserID, &grant.GroupID,
			&grant.Quantity, &grant.UsedCount, &grant.ExpiresAt); err != nil {
			return nil, err
		}
		grant.ExpiresAt = grant.ExpiresAt.UTC()
		if grant.Quantity <= 0 || grant.UsedCount < 0 || grant.UsedCount > grant.Quantity {
			return nil, errors.New("payment reset-card grant is invalid")
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

func applyPaymentRefundBenefitConcurrencyMax(ctx context.Context, client *dbent.Client, userID, target int64, sourceID int64) (int, int, error) {
	if client == nil || target <= 0 {
		return 0, 0, nil
	}
	if target > maxUserConcurrency {
		return 0, 0, errors.New("payment concurrency target is out of range")
	}
	rows, err := client.QueryContext(ctx,
		`SELECT before_concurrency, after_concurrency
		 FROM sub2api_apply_payment_concurrency_max($1, $2, $3)`, userID, target, sourceID)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return 0, 0, errors.New("payment concurrency grant did not return a result")
	}
	var before, after int
	if err := rows.Scan(&before, &after); err != nil {
		return 0, 0, err
	}
	return before, after, rows.Err()
}

func applyPaymentRefundBenefitConcurrencyDelta(ctx context.Context, client *dbent.Client, userID int64, delta int) (int, int, error) {
	if client == nil {
		return 0, 0, errors.New("payment concurrency client is unavailable")
	}
	rows, err := client.QueryContext(ctx,
		`SELECT before_concurrency, after_concurrency
		 FROM sub2api_apply_user_concurrency_delta($1, $2)`, userID, delta)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return 0, 0, errors.New("payment concurrency delta did not return a result")
	}
	var before, after int
	if err := rows.Scan(&before, &after); err != nil {
		return 0, 0, err
	}
	return before, after, rows.Err()
}

func recomputePaymentRefundBenefitConcurrency(ctx context.Context, client *dbent.Client, userID int64) (int, error) {
	rows, err := client.QueryContext(ctx, `SELECT sub2api_recompute_user_concurrency($1)`, userID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return 0, errors.New("payment concurrency replay did not return a result")
	}
	var value int
	if err := rows.Scan(&value); err != nil {
		return 0, err
	}
	return value, rows.Err()
}

func paymentRefundBenefitCutoverAt(ctx context.Context, client *dbent.Client) (time.Time, error) {
	rows, err := client.QueryContext(ctx, `SELECT cutover_at FROM payment_refund_benefit_rollout WHERE singleton = TRUE`)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return time.Time{}, errRefundBenefitProvenanceMissing
	}
	var value time.Time
	if err := rows.Scan(&value); err != nil {
		return time.Time{}, err
	}
	return value.UTC(), rows.Err()
}

func paymentOrderRequiresNewBenefitProvenance(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder) bool {
	if order == nil || client == nil || paymentAuditDialect(client) != dialect.Postgres {
		return false
	}
	cutover, err := paymentRefundBenefitCutoverAt(ctx, client)
	return err == nil && !order.CreatedAt.Before(cutover)
}

func paymentRefundBenefitSourceAuditDetail(source *paymentRefundBenefitSource, cards []paymentRefundBenefitResetCardGrant, before, after int) map[string]any {
	var beforePtr, afterPtr *int
	if before != 0 || after != 0 {
		beforeValue, afterValue := before, after
		beforePtr, afterPtr = &beforeValue, &afterValue
	}
	return paymentRefundBenefitSourceAuditDetailWithPresence(source, cards, beforePtr, afterPtr)
}

// paymentRefundBenefitSourceAuditDetailWithPresence keeps a legitimate zero
// concurrency value distinct from an omitted value. Fulfillment's historical
// call site still uses the compatibility helper above; reserve/capture/release
// pass pointers so their audit reflects the actual post-lock observation.
func paymentRefundBenefitSourceAuditDetailWithPresence(source *paymentRefundBenefitSource, cards []paymentRefundBenefitResetCardGrant, before, after *int) map[string]any {
	cardIDs := make([]int64, 0, len(cards))
	for _, card := range cards {
		cardIDs = append(cardIDs, card.ID)
	}
	detail := map[string]any{
		"benefit_source_id":           source.ID,
		"benefit_source_origin":       source.SourceOrigin,
		"benefit_source_state":        source.State,
		"payment_order_id":            source.PaymentOrderID,
		"subscription_grant_order_id": source.SubscriptionGrantOrderID,
		"subscription_id":             source.SubscriptionID,
		"group_id":                    source.GroupID,
		"reset_card_grant_ids":        cardIDs,
		"reset_cards_committed":       source.ResetCardsCommitted,
		"reset_card_delivery_mode":    source.ResetCardDeliveryMode,
		"concurrency_before":          source.ConcurrencyBefore,
		"concurrency_target":          source.ConcurrencyTarget,
		"concurrency_after_grant":     source.ConcurrencyAfterGrant,
	}
	if source.ConcurrencyTarget > 0 {
		current := source.ConcurrencyBefore
		postAction := source.ConcurrencyAfterGrant
		if before != nil {
			value := *before
			current = &value
		}
		if after != nil {
			value := *after
			postAction = &value
		}
		if current != nil {
			detail["concurrency_current"] = *current
		}
		if postAction != nil {
			detail["concurrency_after"] = *postAction
		}
	}
	return detail
}

func paymentRefundBenefitSourceReference(source *paymentRefundBenefitSource) string {
	if source == nil {
		return ""
	}
	return strconv.FormatInt(source.ID, 10)
}

func paymentRefundBenefitHasOnlyAutomaticExtras(order *dbent.PaymentOrder) bool {
	entitlements, err := paymentOrderEntitlementsStrict(order)
	if err != nil {
		return false
	}
	if entitlements.BalanceBonus > 0 {
		// Balance bonuses are folded into the immutable wallet funding split and
		// are reclaimed by the ordinary balance ledger. Subscription bonuses
		// remain outside that ledger and therefore stay manual-only.
		if order == nil || order.OrderType != payment.OrderTypeBalance || order.ProductSnapshot == nil {
			return false
		}
		kind, _ := order.ProductSnapshot["kind"].(string)
		if kind != paymentSnapshotKindBalance {
			return false
		}
		funding, fundingErr := PaymentWalletFundingForOrder(order)
		if fundingErr != nil || math.Abs(funding.GiftCredit-entitlements.BalanceBonus) > paymentAmountZeroTolerance(PaymentOrderCurrency(order)) {
			return false
		}
	}
	commitment, err := entitlements.ResetCardTotalCommitment()
	return err == nil && (commitment > 0 || entitlements.Concurrency > 0)
}

func paymentRefundBenefitOrderTypeSupported(order *dbent.PaymentOrder) bool {
	return order != nil && (order.OrderType == payment.OrderTypeBalance || order.OrderType == payment.OrderTypeSubscription)
}

func loadPaymentRefundBenefitUserConcurrency(ctx context.Context, client *dbent.Client, userID int64, lock bool) (int, error) {
	if client == nil || userID <= 0 {
		return 0, errRefundBenefitProvenanceMissing
	}
	query := `SELECT concurrency FROM users WHERE id = $1 AND deleted_at IS NULL`
	if lock && paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, userID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, errRefundBenefitProvenanceMissing
	}
	var current int
	if err := rows.Scan(&current); err != nil {
		return 0, err
	}
	if current < 0 || current > maxUserConcurrency {
		return 0, errors.New("user concurrency is outside the supported range")
	}
	return current, rows.Err()
}

func loadPaymentRefundConcurrencyHistory(ctx context.Context, client *dbent.Client, userID int64) (int, []paymentRefundConcurrencyEvent, error) {
	rows, err := client.QueryContext(ctx, `SELECT baseline_concurrency
		FROM payment_refund_concurrency_baselines WHERE user_id = $1`, userID)
	if err != nil {
		return 0, nil, err
	}
	var baseline int
	if !rows.Next() {
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return 0, nil, err
		}
		return 0, nil, errRefundBenefitProvenanceMissing
	}
	if err := rows.Scan(&baseline); err != nil {
		_ = rows.Close()
		return 0, nil, err
	}
	if err := rows.Close(); err != nil {
		return 0, nil, err
	}
	if baseline < 0 || baseline > maxUserConcurrency {
		return 0, nil, errors.New("concurrency baseline is invalid")
	}

	rows, err = client.QueryContext(ctx, `SELECT event.event_seq, event.event_kind,
		event.requested_delta, event.target_concurrency,
		COALESCE(event.benefit_source_id, 0), COALESCE(source.state, ''),
		event.before_concurrency, event.after_concurrency
		FROM payment_refund_concurrency_events event
		LEFT JOIN payment_refund_benefit_sources source ON source.id = event.benefit_source_id
		WHERE event.user_id = $1
		ORDER BY event.event_seq ASC, event.id ASC`, userID)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = rows.Close() }()
	events := make([]paymentRefundConcurrencyEvent, 0)
	for rows.Next() {
		var event paymentRefundConcurrencyEvent
		var delta, target sql.NullInt64
		if err := rows.Scan(&event.Sequence, &event.Kind, &delta, &target, &event.SourceID, &event.SourceState, &event.Before, &event.After); err != nil {
			return 0, nil, err
		}
		if event.Before < 0 || event.Before > maxUserConcurrency || event.After < 0 || event.After > maxUserConcurrency {
			return 0, nil, errors.New("concurrency event observed value is out of range")
		}
		if delta.Valid {
			value := int(delta.Int64)
			event.RequestedDelta = &value
		}
		if target.Valid {
			value := int(target.Int64)
			event.Target = &value
		}
		switch event.Kind {
		case refundBenefitConcurrencyEventSet:
			if event.Target == nil || event.RequestedDelta != nil || event.SourceID != 0 {
				return 0, nil, errors.New("SET concurrency event is invalid")
			}
		case refundBenefitConcurrencyEventDelta:
			if event.RequestedDelta == nil || event.Target != nil || event.SourceID != 0 {
				return 0, nil, errors.New("DELTA concurrency event is invalid")
			}
		case refundBenefitConcurrencyEventPaymentMax:
			if event.Target == nil || event.SourceID <= 0 || event.SourceState == "" {
				return 0, nil, errors.New("PAYMENT_MAX concurrency event is invalid")
			}
		case refundBenefitConcurrencyEventUnattributed:
			if event.Target == nil || event.RequestedDelta != nil || event.SourceID != 0 || event.SourceState != "" {
				return 0, nil, errors.New("UNATTRIBUTED concurrency event is invalid")
			}
		default:
			return 0, nil, errors.New("unknown concurrency event kind")
		}
		if event.Target != nil && (*event.Target < 0 || *event.Target > maxUserConcurrency) {
			return 0, nil, errors.New("concurrency event target is out of range")
		}
		events = append(events, event)
	}
	return baseline, events, rows.Err()
}

func replayPaymentRefundConcurrency(baseline int, events []paymentRefundConcurrencyEvent, excludedSourceID int64) (int, error) {
	return replayPaymentRefundConcurrencyFrom(baseline, events, excludedSourceID)
}

func replayPaymentRefundConcurrencyFrom(baseline int, events []paymentRefundConcurrencyEvent, excludedSourceID int64) (int, error) {
	value := int64(baseline)
	for _, event := range events {
		switch event.Kind {
		case refundBenefitConcurrencyEventSet:
			value = int64(*event.Target)
		case refundBenefitConcurrencyEventDelta:
			value += int64(*event.RequestedDelta)
			if value < 0 {
				value = 0
			}
			if value > int64(maxUserConcurrency) {
				value = int64(maxUserConcurrency)
			}
		case refundBenefitConcurrencyEventPaymentMax:
			if event.SourceID != excludedSourceID && event.SourceState == refundBenefitStateActive {
				if target := int64(*event.Target); target > value {
					value = target
				}
			}
		case refundBenefitConcurrencyEventUnattributed:
			// This is an observed raw-writer result. It is a replay frontier,
			// not evidence of a trusted absolute administrator override.
			value = int64(event.After)
		default:
			return 0, errors.New("unknown concurrency event kind")
		}
	}
	return int(value), nil
}

// replayPaymentRefundConcurrencyAfterSourceRemoval removes one source while
// preserving the actual value at the latest unattributed barrier before that
// source. A source created before an unattributed event cannot be refunded
// automatically because the later raw write could have retained its cap.
// A source created after the barrier may use the observed value as its new
// baseline; later active payment sources remain replayable by sequence.
func replayPaymentRefundConcurrencyAfterSourceRemoval(
	baseline int,
	events []paymentRefundConcurrencyEvent,
	sourceID int64,
	target int,
) (int, error) {
	sourceIndex := -1
	for index, event := range events {
		if event.Kind == refundBenefitConcurrencyEventPaymentMax && event.SourceID == sourceID &&
			event.Target != nil && *event.Target == target {
			if sourceIndex >= 0 {
				return 0, errRefundBenefitProvenanceMissing
			}
			sourceIndex = index
		}
	}
	if sourceIndex < 0 {
		return 0, errRefundBenefitProvenanceMissing
	}
	for _, event := range events[sourceIndex+1:] {
		if event.Kind == refundBenefitConcurrencyEventUnattributed {
			return 0, errRefundBenefitProvenanceMissing
		}
	}
	start := 0
	resetBaseline := baseline
	for index, event := range events[:sourceIndex] {
		if event.Kind == refundBenefitConcurrencyEventUnattributed {
			start = index + 1
			resetBaseline = event.After
		}
	}
	return replayPaymentRefundConcurrencyFrom(resetBaseline, events[start:], sourceID)
}

func loadLegacyPaymentRefundBenefitResetCards(ctx context.Context, client *dbent.Client, orderID int64, lock bool) ([]paymentRefundBenefitResetCardGrant, error) {
	query := `SELECT id, subscription_id, user_id, group_id, quantity, used_count, expires_at
		FROM subscription_reset_grants WHERE payment_order_id = $1 ORDER BY id ASC`
	if lock && paymentAuditDialect(client) == dialect.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, orderID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cards := make([]paymentRefundBenefitResetCardGrant, 0)
	for rows.Next() {
		var card paymentRefundBenefitResetCardGrant
		if err := rows.Scan(&card.ID, &card.SubscriptionID, &card.UserID, &card.GroupID,
			&card.Quantity, &card.UsedCount, &card.ExpiresAt); err != nil {
			return nil, err
		}
		card.ExpiresAt = card.ExpiresAt.UTC()
		if card.Quantity <= 0 || card.UsedCount < 0 || card.UsedCount > card.Quantity {
			return nil, errors.New("legacy reset-card grant is invalid")
		}
		cards = append(cards, card)
	}
	return cards, rows.Err()
}

func paymentRefundBenefitCardsTotal(cards []paymentRefundBenefitResetCardGrant) int {
	total := 0
	for _, card := range cards {
		total += card.Quantity
	}
	return total
}

func validatePaymentRefundBenefitCards(source *paymentRefundBenefitSource, cards []paymentRefundBenefitResetCardGrant) error {
	if source == nil {
		return errRefundBenefitProvenanceMissing
	}
	total := 0
	for _, card := range cards {
		if card.UsedCount > 0 {
			return denyPaymentRefundBenefit("RESET_CARD_BENEFIT_USED", "an issued reset-card benefit has already been used")
		}
		if card.UserID != source.UserID || (source.SubscriptionID > 0 && card.SubscriptionID != source.SubscriptionID) ||
			(source.GroupID > 0 && card.GroupID != source.GroupID) {
			return errRefundBenefitProvenanceMissing
		}
		total += card.Quantity
	}
	switch source.ResetCardDeliveryMode {
	case "none":
		if source.ResetCardsCommitted != 0 || total != 0 {
			return errRefundBenefitProvenanceMissing
		}
	case resetCardDeliveryModeImmediate:
		if total != source.ResetCardsCommitted {
			return errRefundBenefitProvenanceMissing
		}
	case resetCardDeliveryModeMonthly:
		if total > source.ResetCardsCommitted {
			return errRefundBenefitProvenanceMissing
		}
	default:
		return errRefundBenefitProvenanceMissing
	}
	return nil
}

// validatePaymentRefundBenefitSchedule makes the recurring promise part of the
// same source proof as already-issued card IDs. A monthly source must retain
// the exact schedule that can append future grants; an immediate source must
// not acquire a schedule later and silently broaden its reclaimable set.
func validatePaymentRefundBenefitSchedule(
	source *paymentRefundBenefitSource,
	schedule *monthlyResetCardSchedule,
) error {
	if source == nil {
		return errRefundBenefitProvenanceMissing
	}
	switch source.ResetCardDeliveryMode {
	case resetCardDeliveryModeMonthly:
		if schedule == nil || schedule.ID <= 0 || schedule.PaymentOrderID != source.PaymentOrderID ||
			schedule.SubscriptionID != source.SubscriptionID || schedule.UserID != source.UserID ||
			schedule.GroupID != source.GroupID || schedule.OccurrenceCount <= 0 ||
			schedule.CardsPerOccurrence <= 0 || schedule.CardValidityDays <= 0 ||
			schedule.NextOccurrence < 0 || schedule.NextOccurrence > schedule.OccurrenceCount ||
			schedule.AnchorTimezone != monthlyResetCardAnchorTimezone || schedule.AnchorDay <= 0 ||
			!schedule.TermEndAt.After(schedule.AnchorAt) {
			return errRefundBenefitProvenanceMissing
		}
		committed := int64(schedule.OccurrenceCount) * int64(schedule.CardsPerOccurrence)
		if committed < 0 || committed > math.MaxInt32 || int(committed) != source.ResetCardsCommitted {
			return errRefundBenefitProvenanceMissing
		}
		switch schedule.Status {
		case "active":
			if schedule.NextOccurrence >= schedule.OccurrenceCount || schedule.NextDueAt == nil {
				return errRefundBenefitProvenanceMissing
			}
		case "completed":
			if schedule.NextOccurrence != schedule.OccurrenceCount || schedule.NextDueAt != nil {
				return errRefundBenefitProvenanceMissing
			}
		case "cancelled":
			if schedule.NextDueAt != nil {
				return errRefundBenefitProvenanceMissing
			}
		default:
			return errRefundBenefitProvenanceMissing
		}
	case "none", resetCardDeliveryModeImmediate:
		if schedule != nil {
			return errRefundBenefitProvenanceMissing
		}
	default:
		return errRefundBenefitProvenanceMissing
	}
	return nil
}

type paymentRefundBenefitProofSource struct {
	ID, PaymentOrderID, SubscriptionGrantOrderID, UserID, SubscriptionID, GroupID int64
	Origin, DeliveryMode, State                                                   string
	ResetCardsCommitted, ConcurrencyTarget                                        int
	ConcurrencyBefore, ConcurrencyAfterGrant                                      *int
}

type paymentRefundBenefitProofCard struct {
	ID, SubscriptionID, UserID, GroupID int64
	Quantity, UsedCount                 int
	ExpiresAt                           time.Time
}

type paymentRefundBenefitProofSchedule struct {
	ID, PaymentOrderID, SubscriptionID, UserID, GroupID, SourcePlanID int64
	CardFamilyKey                                                     *string
	SourceTierRank                                                    *int
	AnchorAt                                                          time.Time
	AnchorTimezone                                                    string
	AnchorDay                                                         int
	TermEndAt                                                         time.Time
	OccurrenceCount, CardsPerOccurrence, CardValidityDays             int
	NextOccurrence                                                    int
	NextDueAt                                                         *time.Time
	Status                                                            string
}

type paymentRefundBenefitProof struct {
	Legacy             bool
	Source             paymentRefundBenefitProofSource
	Cards              []paymentRefundBenefitProofCard
	Schedule           *paymentRefundBenefitProofSchedule
	Baseline           int
	Events             []paymentRefundConcurrencyEvent
	ConcurrencyCurrent int
	ConcurrencyAfter   int
}

func paymentRefundBenefitDigest(state *paymentRefundBenefitReviewState, baseline int, events []paymentRefundConcurrencyEvent) (string, error) {
	if state == nil || state.Source == nil {
		return "", errRefundBenefitProvenanceMissing
	}
	source := state.Source
	proof := paymentRefundBenefitProof{
		Legacy: state.Legacy,
		Source: paymentRefundBenefitProofSource{
			ID: source.ID, PaymentOrderID: source.PaymentOrderID, SubscriptionGrantOrderID: source.SubscriptionGrantOrderID,
			UserID: source.UserID, SubscriptionID: source.SubscriptionID, GroupID: source.GroupID,
			Origin: source.SourceOrigin, DeliveryMode: source.ResetCardDeliveryMode, State: source.State,
			ResetCardsCommitted: source.ResetCardsCommitted, ConcurrencyTarget: source.ConcurrencyTarget,
			ConcurrencyBefore: source.ConcurrencyBefore, ConcurrencyAfterGrant: source.ConcurrencyAfterGrant,
		},
		Baseline: baseline, Events: events, ConcurrencyCurrent: state.ConcurrencyCurrent, ConcurrencyAfter: state.ConcurrencyAfter,
		Cards: make([]paymentRefundBenefitProofCard, 0, len(state.Cards)),
	}
	for _, card := range state.Cards {
		proof.Cards = append(proof.Cards, paymentRefundBenefitProofCard{
			ID: card.ID, SubscriptionID: card.SubscriptionID, UserID: card.UserID, GroupID: card.GroupID,
			Quantity: card.Quantity, UsedCount: card.UsedCount, ExpiresAt: card.ExpiresAt.UTC(),
		})
	}
	if state.Schedule != nil {
		schedule := state.Schedule
		proof.Schedule = &paymentRefundBenefitProofSchedule{
			ID: schedule.ID, PaymentOrderID: schedule.PaymentOrderID, SubscriptionID: schedule.SubscriptionID,
			UserID: schedule.UserID, GroupID: schedule.GroupID, SourcePlanID: schedule.SourcePlanID,
			CardFamilyKey: schedule.CardFamilyKey, SourceTierRank: schedule.SourceTierRank,
			AnchorAt: schedule.AnchorAt.UTC(), AnchorTimezone: schedule.AnchorTimezone, AnchorDay: schedule.AnchorDay,
			TermEndAt: schedule.TermEndAt.UTC(), OccurrenceCount: schedule.OccurrenceCount,
			CardsPerOccurrence: schedule.CardsPerOccurrence, CardValidityDays: schedule.CardValidityDays,
			NextOccurrence: schedule.NextOccurrence, NextDueAt: schedule.NextDueAt, Status: schedule.Status,
		}
	}
	encoded, err := json.Marshal(proof)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func paymentRefundBenefitsDTO(state *paymentRefundBenefitReviewState) *RefundBenefitsReview {
	if state == nil || state.Source == nil {
		return nil
	}
	ids := make([]int64, 0, len(state.Cards))
	for _, card := range state.Cards {
		ids = append(ids, card.ID)
	}
	var before *int
	if !state.Legacy && state.Source.ConcurrencyBefore != nil {
		value := *state.Source.ConcurrencyBefore
		before = &value
	}
	return &RefundBenefitsReview{
		ResetCardGrantIDs: ids, ResetCardsToReclaim: paymentRefundBenefitCardsTotal(state.Cards),
		ConcurrencyBefore: before, ConcurrencyCurrent: state.ConcurrencyCurrent, ConcurrencyAfterRefund: state.ConcurrencyAfter,
	}
}

func verifyPaymentRefundBenefitSource(
	source *paymentRefundBenefitSource,
	order *dbent.PaymentOrder,
	entitlements PlanEntitlements,
	grant *paymentSubscriptionGrant,
	subscription *dbent.UserSubscription,
) error {
	if source == nil || order == nil || source.PaymentOrderID != order.ID || source.UserID != order.UserID {
		return errRefundBenefitProvenanceMissing
	}
	commitment, err := entitlements.ResetCardTotalCommitment()
	if err != nil {
		return err
	}
	mode := "none"
	if commitment > 0 {
		mode = strings.TrimSpace(entitlements.ResetCardDeliveryMode)
		if mode == "" {
			mode = resetCardDeliveryModeImmediate
		}
	}
	if source.ResetCardsCommitted != commitment || source.ResetCardDeliveryMode != mode ||
		source.ConcurrencyTarget != entitlements.Concurrency {
		return errRefundBenefitProvenanceMissing
	}
	if source.State == refundBenefitStateReserved {
		return denyPaymentRefundBenefit("BENEFIT_REFUND_IN_FLIGHT", "payment benefits are already reserved by another refund attempt")
	}
	if source.State != refundBenefitStateActive {
		return errRefundBenefitProvenanceMissing
	}
	if order.OrderType == payment.OrderTypeSubscription {
		if grant == nil || subscription == nil || source.SubscriptionGrantOrderID != order.ID ||
			source.SubscriptionID != grant.SubscriptionID || source.GroupID != grant.GroupID ||
			source.SubscriptionID != subscription.ID || source.GroupID != subscription.GroupID {
			return errRefundBenefitProvenanceMissing
		}
	} else if source.SubscriptionGrantOrderID != 0 || source.SubscriptionID != 0 || source.GroupID != 0 {
		return errRefundBenefitProvenanceMissing
	}
	if source.ConcurrencyTarget > 0 && (source.ConcurrencyBefore == nil || source.ConcurrencyAfterGrant == nil ||
		*source.ConcurrencyAfterGrant != max(*source.ConcurrencyBefore, source.ConcurrencyTarget)) {
		return errRefundBenefitProvenanceMissing
	}
	return nil
}

func materializePaymentRefundBenefitReview(
	ctx context.Context,
	client *dbent.Client,
	order *dbent.PaymentOrder,
	source *paymentRefundBenefitSource,
	cards []paymentRefundBenefitResetCardGrant,
	schedule *monthlyResetCardSchedule,
	legacy bool,
	lock bool,
) (*paymentRefundBenefitReviewState, error) {
	if err := validatePaymentRefundBenefitCards(source, cards); err != nil {
		return nil, err
	}
	if !legacy {
		if err := validatePaymentRefundBenefitSchedule(source, schedule); err != nil {
			return nil, err
		}
	} else if schedule != nil {
		return nil, errRefundBenefitProvenanceMissing
	}
	current, err := loadPaymentRefundBenefitUserConcurrency(ctx, client, order.UserID, lock)
	if err != nil {
		return nil, err
	}
	state := &paymentRefundBenefitReviewState{
		Source: source, Cards: cards, Schedule: schedule,
		ConcurrencyCurrent: current, ConcurrencyAfter: current, Legacy: legacy,
	}
	baseline, events, err := loadPaymentRefundConcurrencyHistory(ctx, client, order.UserID)
	if err != nil {
		return nil, err
	}
	if source.ConcurrencyTarget > 0 {
		actual, err := replayPaymentRefundConcurrency(baseline, events, 0)
		if err != nil {
			return nil, err
		}
		if actual != current {
			return nil, errRefundBenefitProvenanceMissing
		}
		after, err := replayPaymentRefundConcurrencyAfterSourceRemoval(
			baseline, events, source.ID, source.ConcurrencyTarget,
		)
		if err != nil {
			return nil, err
		}
		state.ConcurrencyAfter = after
	} else if legacy {
		// A finite historical order may prove exact unused card IDs, but it has
		// no trusted pre-grant concurrency baseline. Preserve an already higher
		// current cap rather than inventing an entitlement reduction.
		entitlements, err := paymentOrderEntitlementsStrict(order)
		if err != nil {
			return nil, err
		}
		if entitlements.Concurrency > 0 && current <= entitlements.Concurrency {
			return nil, errRefundBenefitProvenanceMissing
		}
	}
	digest, err := paymentRefundBenefitDigest(state, baseline, events)
	if err != nil {
		return nil, err
	}
	state.ProofDigest = digest
	return state, nil
}

func reviewLegacyPaymentRefundBenefits(
	ctx context.Context,
	client *dbent.Client,
	order *dbent.PaymentOrder,
	entitlements PlanEntitlements,
	grant *paymentSubscriptionGrant,
	subscription *dbent.UserSubscription,
	lock bool,
) (*paymentRefundBenefitReviewState, error) {
	if order == nil || paymentOrderRequiresNewBenefitProvenance(ctx, client, order) {
		return nil, errRefundBenefitProvenanceMissing
	}
	commitment, err := entitlements.ResetCardTotalCommitment()
	if err != nil {
		return nil, err
	}
	if commitment > 0 && entitlements.ResetCardDeliveryMode == resetCardDeliveryModeMonthly {
		// Historical schedule issuances do not prove the absent future grants.
		return nil, errRefundBenefitProvenanceMissing
	}
	if order.OrderType == payment.OrderTypeBalance {
		// Historical balance orders may use the narrow compatibility path when
		// their immutable wallet funding row proves the cash/credit ledger. The
		// concurrency before value is intentionally unknown: materialization
		// below only accepts a current value strictly above the old target and
		// leaves that higher value untouched.
		if _, _, err := loadPaymentWalletRefundState(ctx, client, order.ID, order.UserID, lock); err != nil {
			return nil, err
		}
		if _, err := loadPaymentRefundBenefitUserConcurrency(ctx, client, order.UserID, lock); err != nil {
			return nil, err
		}
		cards, err := loadLegacyPaymentRefundBenefitResetCards(ctx, client, order.ID, lock)
		if err != nil {
			return nil, err
		}
		mode := "none"
		if commitment > 0 {
			mode = resetCardDeliveryModeImmediate
		}
		source := &paymentRefundBenefitSource{
			PaymentOrderID: order.ID, UserID: order.UserID, SourceOrigin: "legacy_card_fk",
			ResetCardsCommitted: commitment, ResetCardDeliveryMode: mode, State: refundBenefitStateActive,
		}
		return materializePaymentRefundBenefitReview(ctx, client, order, source, cards, nil, true, lock)
	}
	if order.OrderType != payment.OrderTypeSubscription || grant == nil || subscription == nil {
		return nil, errRefundBenefitProvenanceMissing
	}
	if _, err := loadPaymentRefundBenefitUserConcurrency(ctx, client, order.UserID, lock); err != nil {
		return nil, err
	}
	cards, err := loadLegacyPaymentRefundBenefitResetCards(ctx, client, order.ID, lock)
	if err != nil {
		return nil, err
	}
	mode := "none"
	if commitment > 0 {
		mode = resetCardDeliveryModeImmediate
	}
	source := &paymentRefundBenefitSource{
		PaymentOrderID: order.ID, SubscriptionGrantOrderID: order.ID, UserID: order.UserID,
		SubscriptionID: grant.SubscriptionID, GroupID: grant.GroupID, SourceOrigin: "legacy_card_fk",
		ResetCardsCommitted: commitment, ResetCardDeliveryMode: mode, State: refundBenefitStateActive,
	}
	return materializePaymentRefundBenefitReview(ctx, client, order, source, cards, nil, true, lock)
}

// reviewPaymentRefundBenefits reads immutable source evidence and computes the
// exact effect of removing this source from the user's event history. In a
// lock-holding reservation it deliberately locks user, cards, then source;
// the caller already holds order -> term grant -> subscription.
func reviewPaymentRefundBenefits(
	ctx context.Context,
	client *dbent.Client,
	order *dbent.PaymentOrder,
	grant *paymentSubscriptionGrant,
	subscription *dbent.UserSubscription,
	lock bool,
) (*paymentRefundBenefitReviewState, error) {
	if client == nil || order == nil {
		return nil, errRefundBenefitProvenanceMissing
	}
	// The provenance tables and replay functions are PostgreSQL-only. Keep
	// SQLite/unit fixtures on the pre-P19 accounting path; production
	// PostgreSQL still fails closed when required source evidence is absent.
	if paymentAuditDialect(client) != dialect.Postgres {
		return nil, nil
	}
	required, entitlements, err := paymentRefundBenefitSourceRequired(order)
	if err != nil {
		return nil, err
	}
	if !required {
		return nil, nil
	}

	// Read without the source row lock first so cards can be locked before the
	// source snapshot. The source is re-read FOR UPDATE after the card locks.
	source, err := loadPaymentRefundBenefitSource(ctx, client, order.ID, false)
	if errors.Is(err, errRefundBenefitProvenanceMissing) {
		return reviewLegacyPaymentRefundBenefits(ctx, client, order, entitlements, grant, subscription, lock)
	}
	if err != nil {
		return nil, err
	}
	if err := verifyPaymentRefundBenefitSource(source, order, entitlements, grant, subscription); err != nil {
		return nil, err
	}
	// The monthly schedule is locked after the existing subscription lock and
	// before the user/card/source suffix. The delivery worker starts with the
	// same payment-order lock, so it cannot append a source card while a held
	// refund is calculating this proof.
	// Read every order's schedule so immediate/no-card sources reject an
	// impossible later schedule too; a legitimate non-monthly order returns nil.
	schedule, err := loadMonthlyResetCardScheduleByOrder(ctx, client, order.ID, lock)
	if err != nil {
		return nil, err
	}
	// User first, then cards, then source snapshot. This is the accepted P19
	// suffix after the pre-existing order/grant/subscription locks.
	if _, err := loadPaymentRefundBenefitUserConcurrency(ctx, client, order.UserID, lock); err != nil {
		return nil, err
	}
	cards, err := loadPaymentRefundBenefitResetCards(ctx, client, source.ID, lock)
	if err != nil {
		return nil, err
	}
	if lock {
		source, err = loadPaymentRefundBenefitSource(ctx, client, order.ID, true)
		if err != nil {
			return nil, err
		}
		if err := verifyPaymentRefundBenefitSource(source, order, entitlements, grant, subscription); err != nil {
			return nil, err
		}
	}
	return materializePaymentRefundBenefitReview(ctx, client, order, source, cards, schedule, false, lock)
}

func loadPaymentRefundBenefitGrantContext(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, lock bool) (*paymentSubscriptionGrant, *dbent.UserSubscription, error) {
	if order == nil || order.OrderType != payment.OrderTypeSubscription {
		return nil, nil, nil
	}
	grant, subscription, err := loadPaymentSubscriptionRefundState(ctx, client, order.ID, lock)
	if err != nil {
		return nil, nil, err
	}
	return grant, subscription, nil
}

func createLegacyPaymentRefundBenefitSource(
	ctx context.Context,
	client *dbent.Client,
	state *paymentRefundBenefitReviewState,
) (*paymentRefundBenefitSource, error) {
	if state == nil || state.Source == nil || !state.Legacy || state.Source.SourceOrigin != "legacy_card_fk" {
		return nil, errRefundBenefitProvenanceMissing
	}
	snapshot := state.Source
	rows, err := client.QueryContext(ctx, `INSERT INTO payment_refund_benefit_sources (
		payment_order_id, subscription_grant_order_id, user_id, subscription_id, group_id,
		source_origin, reset_cards_committed, reset_card_delivery_mode,
		concurrency_before, concurrency_target, concurrency_after_grant, state, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,'legacy_card_fk',$6,$7,NULL,0,NULL,'ACTIVE',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
	ON CONFLICT (payment_order_id) DO NOTHING RETURNING id`,
		snapshot.PaymentOrderID, nullablePositiveInt64(snapshot.SubscriptionGrantOrderID), snapshot.UserID,
		nullablePositiveInt64(snapshot.SubscriptionID), nullablePositiveInt64(snapshot.GroupID),
		snapshot.ResetCardsCommitted, snapshot.ResetCardDeliveryMode)
	if err != nil {
		return nil, err
	}
	inserted := rows.Next()
	if inserted {
		var ignored int64
		err = rows.Scan(&ignored)
	}
	if closeErr := rows.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if !inserted {
		return nil, errRefundBenefitProvenanceMissing
	}
	source, err := loadPaymentRefundBenefitSource(ctx, client, snapshot.PaymentOrderID, true)
	if err != nil {
		return nil, err
	}
	if source.SourceOrigin != "legacy_card_fk" || source.UserID != snapshot.UserID ||
		source.ResetCardsCommitted != snapshot.ResetCardsCommitted || source.ResetCardDeliveryMode != snapshot.ResetCardDeliveryMode ||
		source.ConcurrencyTarget != 0 || source.State != refundBenefitStateActive {
		return nil, errRefundBenefitProvenanceMissing
	}
	for _, card := range state.Cards {
		res, err := client.ExecContext(ctx, `INSERT INTO payment_refund_benefit_reset_card_grants (source_id, reset_card_grant_id)
			VALUES ($1,$2) ON CONFLICT (reset_card_grant_id) DO NOTHING`, source.ID, card.ID)
		if err != nil {
			return nil, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			return nil, errRefundBenefitProvenanceMissing
		}
	}
	return source, nil
}

func nullablePositiveInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func updateUnifiedRefundBenefitProof(ctx context.Context, client *dbent.Client, attempt *unifiedRefundAttempt, digest string) error {
	if attempt == nil || strings.TrimSpace(digest) == "" {
		return errRefundBenefitProvenanceMissing
	}
	res, err := client.ExecContext(ctx, `UPDATE unified_payment_refund_attempts
		SET benefit_proof_digest = $2, updated_at = CURRENT_TIMESTAMP
		WHERE product_refund_no = $1 AND benefit_proof_digest = ''`, attempt.ProductRefundNo, digest)
	if err != nil {
		return err
	}
	if err := requireSingleAffected(res, "bind refund benefit proof"); err != nil {
		return err
	}
	attempt.BenefitProofDigest = digest
	return nil
}

// reserveReviewedRefundBenefits is intentionally called only after the
// attempt row exists. The source lifecycle can therefore reference the same
// provider idempotency record that will own capture or release.
func reserveReviewedRefundBenefits(
	ctx context.Context,
	client *dbent.Client,
	fence *ConcurrencyService,
	order *dbent.PaymentOrder,
	review *RefundReview,
	attempt *unifiedRefundAttempt,
) error {
	if review == nil || review.benefitState == nil {
		return nil
	}
	grant, subscription, err := loadPaymentRefundBenefitGrantContext(ctx, client, order, true)
	if err != nil {
		return err
	}
	fresh, err := reviewPaymentRefundBenefits(ctx, client, order, grant, subscription, true)
	if err != nil {
		return err
	}
	if fresh == nil || fresh.ProofDigest != review.benefitState.ProofDigest {
		return errRefundQuoteStale
	}
	source := fresh.Source
	if fresh.Legacy {
		source, err = createLegacyPaymentRefundBenefitSource(ctx, client, fresh)
		if err != nil {
			return err
		}
	}
	if source == nil || source.State != refundBenefitStateActive {
		return denyPaymentRefundBenefit("BENEFIT_REFUND_IN_FLIGHT", "payment benefits are already reserved by another refund attempt")
	}
	if source.ConcurrencyTarget > 0 {
		if _, err := BeginUserConcurrencyAuthorizationFenceMutationAfterUserLock(ctx, fence, source.UserID); err != nil {
			return fmt.Errorf("begin refund benefit reservation concurrency fence: %w", err)
		}
	}
	if err := updateUnifiedRefundBenefitProof(ctx, client, attempt, fresh.ProofDigest); err != nil {
		return err
	}
	now := time.Now().UTC()
	res, err := client.ExecContext(ctx, `UPDATE payment_refund_benefit_sources
		SET state = 'RESERVED', reserved_product_refund_no = $2,
			reservation_proof_digest = $3, reserved_at = $4, updated_at = $4
		WHERE id = $1 AND state = 'ACTIVE'`, source.ID, attempt.ProductRefundNo, fresh.ProofDigest, now)
	if err != nil {
		return err
	}
	if err := requireSingleAffected(res, "reserve payment refund benefits"); err != nil {
		return denyPaymentRefundBenefit("BENEFIT_REFUND_IN_FLIGHT", "payment benefits are already reserved by another refund attempt")
	}
	source.State = refundBenefitStateReserved
	after := fresh.ConcurrencyCurrent
	if source.ConcurrencyTarget > 0 {
		after, err = recomputePaymentRefundBenefitConcurrency(ctx, client, source.UserID)
		if err != nil {
			return err
		}
		if after != fresh.ConcurrencyAfter {
			return errRefundBenefitProvenanceMissing
		}
	}
	beforeValue, afterValue := fresh.ConcurrencyCurrent, after
	detail := paymentRefundBenefitSourceAuditDetailWithPresence(source, fresh.Cards, &beforeValue, &afterValue)
	detail["proof_digest"] = fresh.ProofDigest
	detail["legacy_no_historical_concurrency_before"] = fresh.Legacy && source.ConcurrencyTarget == 0
	return writeUnifiedRefundAudit(ctx, client, order.ID, "UNIFIED_REFUND_BENEFITS_RESERVED", detail)
}

func lockReservedPaymentRefundBenefits(
	ctx context.Context,
	client *dbent.Client,
	order *dbent.PaymentOrder,
	attempt *unifiedRefundAttempt,
) (*paymentRefundBenefitSource, []paymentRefundBenefitResetCardGrant, int, []paymentRefundConcurrencyEvent, int, error) {
	if attempt == nil || strings.TrimSpace(attempt.BenefitProofDigest) == "" {
		return nil, nil, 0, nil, 0, nil
	}
	// Read the source only to obtain the id; the mutable snapshot is locked
	// last after the established order/grant/subscription/user/card sequence.
	source, err := loadPaymentRefundBenefitSource(ctx, client, order.ID, false)
	if err != nil {
		return nil, nil, 0, nil, 0, err
	}
	grant, subscription, err := loadPaymentRefundBenefitGrantContext(ctx, client, order, true)
	if err != nil {
		return nil, nil, 0, nil, 0, err
	}
	if order.OrderType == payment.OrderTypeSubscription && (grant == nil || subscription == nil ||
		source.SubscriptionGrantOrderID != order.ID || source.SubscriptionID != grant.SubscriptionID || source.GroupID != grant.GroupID) {
		return nil, nil, 0, nil, 0, errRefundBenefitProvenanceMissing
	}
	// The worker follows the same order -> grant -> subscription -> schedule
	// prefix. Lock this mutable recurring ledger before user/card/source so a
	// terminal capture/release cannot validate a different future-gift promise.
	schedule, err := loadMonthlyResetCardScheduleByOrder(ctx, client, order.ID, true)
	if err != nil {
		return nil, nil, 0, nil, 0, err
	}
	current, err := loadPaymentRefundBenefitUserConcurrency(ctx, client, source.UserID, true)
	if err != nil {
		return nil, nil, 0, nil, 0, err
	}
	cards, err := loadPaymentRefundBenefitResetCards(ctx, client, source.ID, true)
	if err != nil {
		return nil, nil, 0, nil, 0, err
	}
	source, err = loadPaymentRefundBenefitSource(ctx, client, order.ID, true)
	if err != nil {
		return nil, nil, 0, nil, 0, err
	}
	if source.UserID != order.UserID {
		return nil, nil, 0, nil, 0, errRefundBenefitProvenanceMissing
	}
	// A release retry after its original transaction already restored ACTIVE is
	// idempotent. Every other state must remain bound to this exact provider
	// attempt and proof before its lifecycle can change.
	if !(source.State == refundBenefitStateActive && source.ReservedProductRefundNo == "") &&
		(source.ReservedProductRefundNo != attempt.ProductRefundNo || source.ReservationProofDigest != attempt.BenefitProofDigest) {
		return nil, nil, 0, nil, 0, errRefundBenefitProvenanceMissing
	}
	if err := validatePaymentRefundBenefitSchedule(source, schedule); err != nil {
		return nil, nil, 0, nil, 0, err
	}
	if err := validatePaymentRefundBenefitCards(source, cards); err != nil {
		return nil, nil, 0, nil, 0, err
	}
	baseline, events, err := loadPaymentRefundConcurrencyHistory(ctx, client, source.UserID)
	if err != nil {
		return nil, nil, 0, nil, 0, err
	}
	expected, err := replayPaymentRefundConcurrency(baseline, events, 0)
	if err != nil {
		return nil, nil, 0, nil, 0, err
	}
	if expected != current {
		return nil, nil, 0, nil, 0, errRefundBenefitProvenanceMissing
	}
	if source.ConcurrencyTarget > 0 {
		if _, err := replayPaymentRefundConcurrencyAfterSourceRemoval(
			baseline, events, source.ID, source.ConcurrencyTarget,
		); err != nil {
			return nil, nil, 0, nil, 0, err
		}
	}
	return source, cards, current, events, baseline, nil
}

func captureReviewedRefundBenefits(ctx context.Context, client *dbent.Client, fence *ConcurrencyService, order *dbent.PaymentOrder, attempt *unifiedRefundAttempt) error {
	if attempt == nil || strings.TrimSpace(attempt.BenefitProofDigest) == "" {
		return nil
	}
	source, cards, current, _, _, err := lockReservedPaymentRefundBenefits(ctx, client, order, attempt)
	if err != nil {
		return err
	}
	if source.State == refundBenefitStateRevoked && source.ReservedProductRefundNo == attempt.ProductRefundNo {
		return nil
	}
	if source.State != refundBenefitStateReserved {
		return errRefundBenefitProvenanceMissing
	}
	if source.ConcurrencyTarget > 0 {
		if _, err := BeginUserConcurrencyAuthorizationFenceMutationAfterUserLock(ctx, fence, source.UserID); err != nil {
			return fmt.Errorf("begin refund benefit capture concurrency fence: %w", err)
		}
	}
	now := time.Now().UTC()
	res, err := client.ExecContext(ctx, `UPDATE payment_refund_benefit_sources
		SET state = 'REVOKED', revoked_at = $2, updated_at = $2
		WHERE id = $1 AND state = 'RESERVED' AND reserved_product_refund_no = $3`, source.ID, now, attempt.ProductRefundNo)
	if err != nil {
		return err
	}
	if err := requireSingleAffected(res, "capture payment refund benefits"); err != nil {
		return errRefundBenefitProvenanceMissing
	}
	source.State = refundBenefitStateRevoked
	after := current
	if source.ConcurrencyTarget > 0 {
		after, err = recomputePaymentRefundBenefitConcurrency(ctx, client, source.UserID)
		if err != nil {
			return err
		}
	}
	beforeValue, afterValue := current, after
	detail := paymentRefundBenefitSourceAuditDetailWithPresence(source, cards, &beforeValue, &afterValue)
	detail["proof_digest"] = attempt.BenefitProofDigest
	return writeUnifiedRefundAudit(ctx, client, order.ID, "UNIFIED_REFUND_BENEFITS_CAPTURED", detail)
}

func releaseReviewedRefundBenefits(ctx context.Context, client *dbent.Client, fence *ConcurrencyService, order *dbent.PaymentOrder, attempt *unifiedRefundAttempt) error {
	if attempt == nil || strings.TrimSpace(attempt.BenefitProofDigest) == "" {
		return nil
	}
	source, cards, current, events, baseline, err := lockReservedPaymentRefundBenefits(ctx, client, order, attempt)
	if err != nil {
		return err
	}
	if source.State == refundBenefitStateActive && source.ReservedProductRefundNo == "" {
		return nil
	}
	if source.State != refundBenefitStateReserved {
		return errRefundBenefitProvenanceMissing
	}
	if source.ConcurrencyTarget > 0 {
		if _, err := BeginUserConcurrencyAuthorizationFenceMutationAfterUserLock(ctx, fence, source.UserID); err != nil {
			return fmt.Errorf("begin refund benefit release concurrency fence: %w", err)
		}
	}
	now := time.Now().UTC()
	res, err := client.ExecContext(ctx, `UPDATE payment_refund_benefit_sources
		SET state = 'ACTIVE', reserved_product_refund_no = NULL, reservation_proof_digest = NULL,
			reserved_at = NULL, revoked_at = NULL, updated_at = $2
		WHERE id = $1 AND state = 'RESERVED' AND reserved_product_refund_no = $3`, source.ID, now, attempt.ProductRefundNo)
	if err != nil {
		return err
	}
	if err := requireSingleAffected(res, "release payment refund benefits"); err != nil {
		return errRefundBenefitProvenanceMissing
	}
	source.State = refundBenefitStateActive
	after := current
	if source.ConcurrencyTarget > 0 {
		after, err = recomputePaymentRefundBenefitConcurrency(ctx, client, source.UserID)
		if err != nil {
			return err
		}
		// The source transition is what makes its PAYMENT_MAX event live again.
		// Reload event state after the ACTIVE transition; the rows locked before
		// the update still describe the source as RESERVED and would otherwise
		// incorrectly predict the held cap.
		baseline, events, err = loadPaymentRefundConcurrencyHistory(ctx, client, source.UserID)
		if err != nil {
			return err
		}
		expected, replayErr := replayPaymentRefundConcurrency(baseline, events, 0)
		if replayErr != nil || after != expected {
			return errRefundBenefitProvenanceMissing
		}
	}
	beforeValue, afterValue := current, after
	detail := paymentRefundBenefitSourceAuditDetailWithPresence(source, cards, &beforeValue, &afterValue)
	detail["proof_digest"] = attempt.BenefitProofDigest
	return writeUnifiedRefundAudit(ctx, client, order.ID, "UNIFIED_REFUND_BENEFITS_RELEASED", detail)
}

// paymentRefundBenefitConcurrencyCeilingSnapshot is the durable, revisioned
// projection used after Redis loses its ready marker. It deliberately includes
// ACTIVE sources too: their zero ceiling is the durable tombstone that prevents
// a stale held snapshot from recreating a positive restriction.
func (s *PaymentService) paymentRefundBenefitConcurrencyCeilingSnapshot(ctx context.Context) (map[int64]UserConcurrencyAuthorizationFenceProjection, error) {
	if s == nil || s.entClient == nil {
		return nil, errors.New("payment refund benefit source storage is unavailable")
	}
	// Select identities first, then resolve each user under FOR SHARE. A
	// concurrent SET/DELTA/PAYMENT_MAX or source lifecycle change takes the
	// same user FOR UPDATE lock before it installs a Redis mutation marker. This
	// prevents a Redis-restart reconcile from publishing a pre-commit cap.
	rows, err := s.entClient.QueryContext(ctx, `SELECT DISTINCT source.user_id
		FROM payment_refund_benefit_sources source
		JOIN users ON users.id = source.user_id AND users.deleted_at IS NULL
		ORDER BY source.user_id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	userIDs := make([]int64, 0)
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		if userID <= 0 {
			return nil, errors.New("stored user concurrency fence projection is invalid")
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	projections := make(map[int64]UserConcurrencyAuthorizationFenceProjection, len(userIDs))
	for _, userID := range userIDs {
		projection, tracked, err := s.paymentRefundBenefitConcurrencyCeilingForUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		if tracked {
			projections[userID] = projection
		}
	}
	return projections, nil
}

// paymentRefundBenefitConcurrencyCeilingForUser resolves one tracked user for
// fresh API-key auth snapshots and for the provider-bound reservation. It uses
// the same revision as the full projection, so these paths can race safely.
func (s *PaymentService) paymentRefundBenefitConcurrencyCeilingForUser(
	ctx context.Context,
	userID int64,
) (UserConcurrencyAuthorizationFenceProjection, bool, error) {
	if s == nil || s.entClient == nil || userID <= 0 {
		return UserConcurrencyAuthorizationFenceProjection{}, false, nil
	}
	rows, err := s.entClient.QueryContext(ctx, `SELECT users.concurrency,
		baseline.fence_revision,
		EXISTS (
			SELECT 1 FROM payment_refund_benefit_sources source
			WHERE source.user_id = users.id
		) AS tracked,
		EXISTS (
			SELECT 1 FROM payment_refund_benefit_sources source
			WHERE source.user_id = users.id AND source.state IN ('RESERVED', 'REVOKED')
		) AS held
		FROM users
		JOIN payment_refund_concurrency_baselines baseline ON baseline.user_id = users.id
		WHERE users.id = $1 AND users.deleted_at IS NULL
		FOR SHARE OF users`, userID)
	if err != nil {
		return UserConcurrencyAuthorizationFenceProjection{}, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return UserConcurrencyAuthorizationFenceProjection{}, false, err
		}
		return UserConcurrencyAuthorizationFenceProjection{}, false, nil
	}
	var current int
	var revision int64
	var tracked, held bool
	if err := rows.Scan(&current, &revision, &tracked, &held); err != nil {
		return UserConcurrencyAuthorizationFenceProjection{}, false, err
	}
	if current < 0 || current > maxUserConcurrency || revision < 0 {
		return UserConcurrencyAuthorizationFenceProjection{}, false, errors.New("stored user concurrency fence projection is invalid")
	}
	if err := rows.Err(); err != nil {
		return UserConcurrencyAuthorizationFenceProjection{}, false, err
	}
	ceiling := 0
	if held && current > 0 {
		ceiling = current
	}
	return UserConcurrencyAuthorizationFenceProjection{Ceiling: ceiling, Revision: revision}, tracked, nil
}

func (s *PaymentService) ensureReviewedRefundBenefitConcurrencyAuthorizationFence(ctx context.Context, attempt *unifiedRefundAttempt) error {
	if attempt == nil || strings.TrimSpace(attempt.BenefitProofDigest) == "" {
		return nil
	}
	if s == nil || s.entClient == nil || s.concurrencyAuthorizationFence == nil {
		return errors.New("refund concurrency authorization fence is unavailable")
	}
	source, err := loadPaymentRefundBenefitSource(ctx, s.entClient, attempt.OrderID, false)
	if err != nil {
		return err
	}
	if source.ConcurrencyTarget <= 0 {
		return nil
	}
	if source.State != refundBenefitStateReserved || source.ReservedProductRefundNo != attempt.ProductRefundNo ||
		source.ReservationProofDigest != attempt.BenefitProofDigest {
		return errRefundBenefitProvenanceMissing
	}
	projection, tracked, err := s.paymentRefundBenefitConcurrencyCeilingForUser(ctx, source.UserID)
	if err != nil {
		return err
	}
	if !tracked {
		return errRefundBenefitProvenanceMissing
	}
	return s.concurrencyAuthorizationFence.EnsureUserConcurrencyAuthorizationCeiling(ctx, source.UserID, projection)
}

// syncPaymentRefundBenefitConcurrencyAuthorizationFence is best-effort only
// after a terminal transaction commits. Reservation remains strict before the
// provider; a delayed release/capture sync can at worst retain a lower ceiling
// until the durable reconcile reruns.
func (s *PaymentService) syncPaymentRefundBenefitConcurrencyAuthorizationFence(ctx context.Context, attempt *unifiedRefundAttempt) error {
	if attempt == nil || strings.TrimSpace(attempt.BenefitProofDigest) == "" || s == nil ||
		s.entClient == nil || s.concurrencyAuthorizationFence == nil {
		return nil
	}
	source, err := loadPaymentRefundBenefitSource(ctx, s.entClient, attempt.OrderID, false)
	if err != nil {
		return err
	}
	if source.ConcurrencyTarget <= 0 {
		return nil
	}
	projection, tracked, err := s.paymentRefundBenefitConcurrencyCeilingForUser(ctx, source.UserID)
	if err != nil {
		return err
	}
	if !tracked {
		return errRefundBenefitProvenanceMissing
	}
	return s.concurrencyAuthorizationFence.EnsureUserConcurrencyAuthorizationCeiling(ctx, source.UserID, projection)
}
