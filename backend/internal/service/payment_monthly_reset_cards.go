package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

const monthlyResetCardAnchorTimezone = "Asia/Shanghai"

// China no longer observes daylight saving time. A fixed location avoids a
// host tzdata dependency while retaining the business calendar explicitly.
var monthlyResetCardAnchorLocation = time.FixedZone(monthlyResetCardAnchorTimezone, 8*60*60)

type monthlyResetCardSchedule struct {
	ID                 int64
	PaymentOrderID     int64
	SubscriptionID     int64
	UserID             int64
	GroupID            int64
	SourcePlanID       int64
	CardFamilyKey      *string
	SourceTierRank     *int
	AnchorAt           time.Time
	AnchorTimezone     string
	AnchorDay          int
	TermEndAt          time.Time
	OccurrenceCount    int
	CardsPerOccurrence int
	CardValidityDays   int
	NextOccurrence     int
	NextDueAt          *time.Time
	Status             string
}

func monthlyResetCardDueAt(anchor time.Time, anchorDay, occurrence int) time.Time {
	if occurrence < 0 {
		return time.Time{}
	}
	local := anchor.In(monthlyResetCardAnchorLocation)
	monthOffset := int(local.Month()) - 1 + occurrence
	year := local.Year() + monthOffset/12
	month := time.Month(monthOffset%12 + 1)
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, monthlyResetCardAnchorLocation).Day()
	day := anchorDay
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), monthlyResetCardAnchorLocation).UTC()
}

func monthlyResetCardWindowEnd(schedule *monthlyResetCardSchedule, occurrence int, currentTermEnd time.Time) time.Time {
	if schedule == nil {
		return time.Time{}
	}
	end := schedule.TermEndAt.UTC()
	if occurrence+1 < schedule.OccurrenceCount {
		if due := monthlyResetCardDueAt(schedule.AnchorAt, schedule.AnchorDay, occurrence+1); due.Before(end) {
			end = due
		}
	}
	if !currentTermEnd.IsZero() && currentTermEnd.Before(end) {
		end = currentTermEnd.UTC()
	}
	return end
}

func nullableMonthlyTierValues(schedule *monthlyResetCardSchedule) (any, any) {
	if schedule == nil || schedule.CardFamilyKey == nil || schedule.SourceTierRank == nil {
		return nil, nil
	}
	return *schedule.CardFamilyKey, *schedule.SourceTierRank
}

func scanMonthlyResetCardSchedule(rows *sql.Rows) (*monthlyResetCardSchedule, error) {
	var schedule monthlyResetCardSchedule
	var family sql.NullString
	var rank sql.NullInt64
	var nextDue sql.NullTime
	if err := rows.Scan(
		&schedule.ID, &schedule.PaymentOrderID, &schedule.SubscriptionID,
		&schedule.UserID, &schedule.GroupID, &schedule.SourcePlanID,
		&family, &rank, &schedule.AnchorAt, &schedule.AnchorTimezone, &schedule.AnchorDay,
		&schedule.TermEndAt, &schedule.OccurrenceCount, &schedule.CardsPerOccurrence,
		&schedule.CardValidityDays, &schedule.NextOccurrence, &nextDue, &schedule.Status,
	); err != nil {
		return nil, err
	}
	if family.Valid {
		value := family.String
		schedule.CardFamilyKey = &value
	}
	if rank.Valid {
		value := int(rank.Int64)
		schedule.SourceTierRank = &value
	}
	if nextDue.Valid {
		value := nextDue.Time.UTC()
		schedule.NextDueAt = &value
	}
	schedule.AnchorAt = schedule.AnchorAt.UTC()
	schedule.TermEndAt = schedule.TermEndAt.UTC()
	return &schedule, nil
}

func loadMonthlyResetCardSchedule(ctx context.Context, client *dbent.Client, scheduleID int64, lock bool) (*monthlyResetCardSchedule, error) {
	if client == nil || scheduleID <= 0 {
		return nil, errors.New("monthly reset-card schedule input is invalid")
	}
	query := `SELECT id, payment_order_id, subscription_id, user_id, group_id,
		source_plan_id, card_family_key, source_tier_rank, anchor_at, anchor_timezone, anchor_day,
		term_end_at, occurrence_count, cards_per_occurrence, card_validity_days,
		next_occurrence, next_due_at, status
		FROM subscription_reset_card_schedules WHERE id = $1`
	if lock && paymentAuditDialect(client) == "postgres" {
		query += ` FOR UPDATE`
	}
	rows, err := client.QueryContext(ctx, query, scheduleID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	schedule, err := scanMonthlyResetCardSchedule(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return schedule, nil
}

func loadMonthlyResetCardScheduleByOrder(ctx context.Context, client *dbent.Client, orderID int64, lock bool) (*monthlyResetCardSchedule, error) {
	if client == nil || orderID <= 0 {
		return nil, errors.New("monthly reset-card order input is invalid")
	}
	query := `SELECT id, payment_order_id, subscription_id, user_id, group_id,
		source_plan_id, card_family_key, source_tier_rank, anchor_at, anchor_timezone, anchor_day,
		term_end_at, occurrence_count, cards_per_occurrence, card_validity_days,
		next_occurrence, next_due_at, status
		FROM subscription_reset_card_schedules WHERE payment_order_id = $1`
	if lock && paymentAuditDialect(client) == "postgres" {
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
		return nil, nil
	}
	schedule, err := scanMonthlyResetCardSchedule(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return schedule, nil
}

// ensureMonthlyResetCardSchedule records every schedule input from immutable
// order and exact payment-subscription-grant provenance. It intentionally does
// not derive a receiver from user_id/group_id because renewals can share those
// values while targeting a different term.
func ensureMonthlyResetCardSchedule(
	ctx context.Context,
	client *dbent.Client,
	order *dbent.PaymentOrder,
	grant *paymentSubscriptionGrant,
	entitlements PlanEntitlements,
) (*monthlyResetCardSchedule, error) {
	if client == nil || order == nil || grant == nil || order.PlanID == nil || *order.PlanID <= 0 {
		return nil, errors.New("monthly reset-card schedule provenance is incomplete")
	}
	if entitlements.ResetCardDeliveryMode != resetCardDeliveryModeMonthly || entitlements.ResetCardCount <= 0 {
		return nil, nil
	}
	if entitlements.ResetCardIssueCount < minMonthlyResetCardIssues || entitlements.ResetCardIssueCount > maxMonthlyResetCardIssues {
		return nil, errors.New("monthly reset-card issue count is invalid")
	}
	if grant.ResetCardCount != entitlements.ResetCardCount*entitlements.ResetCardIssueCount {
		return nil, errors.New("monthly reset-card commitment does not match payment subscription grant")
	}
	validityDays := entitlements.ResetCardValidityDays()
	if validityDays <= 0 {
		return nil, errors.New("monthly reset-card validity is invalid")
	}
	tierSnapshot, err := resetCardTierSnapshotForSubscriptionEntitlementOrder(order)
	if err != nil {
		return nil, err
	}
	familyKey, tierRank, _ := resetCardTierSnapshotValues(tierSnapshot)
	anchor := grant.TermStart.UTC()
	termEnd := grant.OriginalEnd.UTC()
	if !termEnd.After(anchor) {
		return nil, errors.New("monthly reset-card term is empty")
	}
	anchorDay := anchor.In(monthlyResetCardAnchorLocation).Day()
	firstDue := monthlyResetCardDueAt(anchor, anchorDay, 0)
	rows, err := client.QueryContext(ctx, `INSERT INTO subscription_reset_card_schedules (
		payment_order_id, subscription_id, user_id, group_id, source_plan_id,
		card_family_key, source_tier_rank, anchor_at, anchor_timezone, anchor_day,
		term_end_at, occurrence_count, cards_per_occurrence, card_validity_days,
		next_occurrence, next_due_at, status, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,0,$15,'active',$16,$16)
	ON CONFLICT (payment_order_id) DO NOTHING RETURNING id`,
		order.ID, grant.SubscriptionID, grant.UserID, grant.GroupID, *order.PlanID,
		familyKey, tierRank, anchor, monthlyResetCardAnchorTimezone, anchorDay,
		termEnd, entitlements.ResetCardIssueCount, entitlements.ResetCardCount,
		validityDays, firstDue, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("insert monthly reset-card schedule: %w", err)
	}
	inserted := false
	if rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		inserted = id > 0
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	schedule, err := loadMonthlyResetCardScheduleByOrder(ctx, client, order.ID, false)
	if err != nil {
		return nil, err
	}
	if schedule == nil {
		return nil, errors.New("monthly reset-card schedule was not persisted")
	}
	if !monthlyResetCardScheduleMatchesFrozenInput(schedule, order, grant, entitlements, tierSnapshot, anchor, termEnd, anchorDay, validityDays) {
		// ON CONFLICT is only an idempotency aid. It must never silently turn a
		// retried fulfillment with different frozen economic/calendar inputs
		// into acceptance of the original schedule.
		return nil, errors.New("monthly reset-card schedule provenance conflict")
	}
	_ = inserted
	return schedule, nil
}

// reconcileMonthlyResetCardScheduleForFulfillment is intentionally limited to
// the calendar ledger. It may run after SUBSCRIPTION_BENEFITS_GRANTED already
// exists, so it must never reapply balance, concurrency, or one-time benefits.
// It also repairs a schedule that an earlier worker incorrectly cancelled while
// a recoverable fulfillment was FAILED, provided its exact term can still
// cover the next occurrence.
func reconcileMonthlyResetCardScheduleForFulfillment(
	ctx context.Context,
	client *dbent.Client,
	order *dbent.PaymentOrder,
	grant *paymentSubscriptionGrant,
	sub *dbent.UserSubscription,
	entitlements PlanEntitlements,
	now time.Time,
) error {
	if entitlements.ResetCardDeliveryMode != resetCardDeliveryModeMonthly || entitlements.ResetCardCount <= 0 {
		return nil
	}
	if grant == nil || sub == nil {
		return errors.New("monthly reset-card entitlement is missing exact payment subscription grant")
	}
	schedule, err := ensureMonthlyResetCardSchedule(ctx, client, order, grant, entitlements)
	if err != nil {
		return fmt.Errorf("record monthly reset-card schedule: %w", err)
	}
	if err := resumeMonthlyResetCardSchedule(ctx, client, schedule, grant, sub, now); err != nil {
		return fmt.Errorf("resume monthly reset-card schedule: %w", err)
	}
	if !grant.TermStart.After(now) {
		if err := advanceMonthlyResetCardSchedule(ctx, client, schedule, grant, sub, now); err != nil {
			return fmt.Errorf("advance monthly reset-card schedule: %w", err)
		}
	}
	return nil
}

func monthlyResetCardScheduleMatchesFrozenInput(
	schedule *monthlyResetCardSchedule,
	order *dbent.PaymentOrder,
	grant *paymentSubscriptionGrant,
	entitlements PlanEntitlements,
	tierSnapshot *SubscriptionResetCardTierSnapshot,
	anchor, termEnd time.Time,
	anchorDay, validityDays int,
) bool {
	if schedule == nil || order == nil || grant == nil || order.PlanID == nil {
		return false
	}
	if schedule.PaymentOrderID != order.ID || schedule.SubscriptionID != grant.SubscriptionID ||
		schedule.UserID != grant.UserID || schedule.GroupID != grant.GroupID ||
		schedule.SourcePlanID != *order.PlanID || !schedule.AnchorAt.Equal(anchor.UTC()) ||
		schedule.AnchorTimezone != monthlyResetCardAnchorTimezone || schedule.AnchorDay != anchorDay ||
		!schedule.TermEndAt.Equal(termEnd.UTC()) || schedule.OccurrenceCount != entitlements.ResetCardIssueCount ||
		schedule.CardsPerOccurrence != entitlements.ResetCardCount || schedule.CardValidityDays != validityDays {
		return false
	}
	if tierSnapshot == nil {
		return schedule.CardFamilyKey == nil && schedule.SourceTierRank == nil
	}
	return schedule.CardFamilyKey != nil && schedule.SourceTierRank != nil &&
		*schedule.CardFamilyKey == tierSnapshot.FamilyKey && *schedule.SourceTierRank == tierSnapshot.TierRank
}

func completeMonthlyResetCardOccurrence(ctx context.Context, client *dbent.Client, schedule *monthlyResetCardSchedule, now time.Time) error {
	if schedule == nil {
		return errors.New("monthly reset-card schedule is missing")
	}
	nextOccurrence := schedule.NextOccurrence + 1
	status := "active"
	var nextDue any
	if nextOccurrence >= schedule.OccurrenceCount {
		status = "completed"
		nextDue = nil
	} else {
		due := monthlyResetCardDueAt(schedule.AnchorAt, schedule.AnchorDay, nextOccurrence)
		nextDue = due
	}
	_, err := client.ExecContext(ctx, `UPDATE subscription_reset_card_schedules
		SET next_occurrence = $1, next_due_at = $2, status = $3, updated_at = $4
		WHERE id = $5`, nextOccurrence, nextDue, status, now.UTC(), schedule.ID)
	if err != nil {
		return err
	}
	schedule.NextOccurrence = nextOccurrence
	schedule.Status = status
	if status == "completed" {
		schedule.NextDueAt = nil
	} else if due, ok := nextDue.(time.Time); ok {
		due = due.UTC()
		schedule.NextDueAt = &due
	}
	return nil
}

func cancelMonthlyResetCardSchedule(ctx context.Context, client *dbent.Client, schedule *monthlyResetCardSchedule, now time.Time) error {
	if schedule == nil || schedule.Status != "active" {
		return nil
	}
	_, err := client.ExecContext(ctx, `UPDATE subscription_reset_card_schedules
		SET status = 'cancelled', next_due_at = NULL, updated_at = $1 WHERE id = $2`, now.UTC(), schedule.ID)
	if err == nil {
		schedule.Status = "cancelled"
		schedule.NextDueAt = nil
	}
	return err
}

func resumeMonthlyResetCardSchedule(
	ctx context.Context,
	client *dbent.Client,
	schedule *monthlyResetCardSchedule,
	grant *paymentSubscriptionGrant,
	sub *dbent.UserSubscription,
	now time.Time,
) error {
	if schedule == nil || schedule.Status != "cancelled" {
		return nil
	}
	if grant == nil || sub == nil {
		return errors.New("monthly reset-card schedule recovery is missing subscription provenance")
	}
	if schedule.NextOccurrence < 0 || schedule.NextOccurrence >= schedule.OccurrenceCount {
		return nil
	}
	if schedule.SubscriptionID != grant.SubscriptionID || schedule.UserID != grant.UserID || schedule.GroupID != grant.GroupID ||
		!schedule.AnchorAt.Equal(grant.TermStart.UTC()) || !schedule.TermEndAt.Equal(grant.OriginalEnd.UTC()) {
		return errors.New("monthly reset-card schedule provenance mismatch")
	}
	// A refund reservation is reversible and an inactive/expired entitlement is
	// not. Only the latter remains terminal for monthly delivery.
	if grant.ReservedSeconds > 0 || sub.Status != SubscriptionStatusActive || !sub.ExpiresAt.After(now) {
		return nil
	}
	currentTermEnd := grant.CurrentEnd.UTC()
	if sub.ExpiresAt.Before(currentTermEnd) {
		currentTermEnd = sub.ExpiresAt.UTC()
	}
	dueAt := monthlyResetCardDueAt(schedule.AnchorAt, schedule.AnchorDay, schedule.NextOccurrence)
	if !currentTermEnd.After(dueAt) {
		return nil
	}
	_, err := client.ExecContext(ctx, `UPDATE subscription_reset_card_schedules
		SET status = 'active', next_due_at = $1, updated_at = $2 WHERE id = $3`, dueAt, now.UTC(), schedule.ID)
	if err != nil {
		return err
	}
	schedule.Status = "active"
	schedule.NextDueAt = &dueAt
	return nil
}

func insertMonthlyResetCardIssuance(ctx context.Context, client *dbent.Client, schedule *monthlyResetCardSchedule, dueAt, windowEnd time.Time, status, skipReason string, now time.Time) (int64, bool, error) {
	if schedule == nil {
		return 0, false, errors.New("monthly reset-card schedule is missing")
	}
	var reason any
	if skipReason != "" {
		reason = skipReason
	}
	rows, err := client.QueryContext(ctx, `INSERT INTO subscription_reset_card_issuances
		(schedule_id, occurrence_index, due_at, window_end_at, status, skip_reason, issued_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7)
		ON CONFLICT (schedule_id, occurrence_index) DO NOTHING RETURNING id`,
		schedule.ID, schedule.NextOccurrence, dueAt.UTC(), windowEnd.UTC(), status, reason, now.UTC())
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}
	var issuanceID int64
	if err := rows.Scan(&issuanceID); err != nil {
		return 0, false, err
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	return issuanceID, true, nil
}

func insertMonthlyResetCardGrant(ctx context.Context, client *dbent.Client, schedule *monthlyResetCardSchedule, issuanceID int64, expiresAt, now time.Time) error {
	if schedule == nil || issuanceID <= 0 {
		return errors.New("monthly reset-card issuance is missing")
	}
	familyKey, tierRank := nullableMonthlyTierValues(schedule)
	_, err := client.ExecContext(ctx, `INSERT INTO subscription_reset_grants (
		subscription_id, user_id, group_id, quantity, used_count, expires_at,
		issued_by, payment_order_id, schedule_issuance_id, card_family_key,
		source_tier_rank, source_plan_id, tier_snapshot_resolved, created_at, updated_at
	) VALUES ($1,$2,$3,$4,0,$5,NULL,NULL,$6,$7,$8,$9,TRUE,$10,$10)`,
		schedule.SubscriptionID, schedule.UserID, schedule.GroupID,
		schedule.CardsPerOccurrence, expiresAt.UTC(), issuanceID, familyKey,
		tierRank, schedule.SourcePlanID, now.UTC())
	return err
}

// advanceMonthlyResetCardSchedule processes ended windows as durable skips and
// at most one open window as an issued grant. It is called only while the
// caller holds payment order -> payment subscription grant -> subscription ->
// schedule locks in that order.
func advanceMonthlyResetCardSchedule(
	ctx context.Context,
	client *dbent.Client,
	schedule *monthlyResetCardSchedule,
	grant *paymentSubscriptionGrant,
	sub *dbent.UserSubscription,
	now time.Time,
) error {
	if schedule == nil || grant == nil || sub == nil || schedule.Status != "active" {
		return nil
	}
	if schedule.SubscriptionID != grant.SubscriptionID || schedule.UserID != grant.UserID || schedule.GroupID != grant.GroupID ||
		!schedule.AnchorAt.Equal(grant.TermStart.UTC()) || !schedule.TermEndAt.Equal(grant.OriginalEnd.UTC()) {
		return errors.New("monthly reset-card schedule provenance mismatch")
	}
	if grant.ReservedSeconds > 0 {
		return nil
	}
	if sub.Status != SubscriptionStatusActive || !sub.ExpiresAt.After(now) {
		return cancelMonthlyResetCardSchedule(ctx, client, schedule, now)
	}
	currentTermEnd := grant.CurrentEnd.UTC()
	if sub.ExpiresAt.Before(currentTermEnd) {
		currentTermEnd = sub.ExpiresAt.UTC()
	}
	for schedule.Status == "active" && schedule.NextOccurrence < schedule.OccurrenceCount {
		if schedule.NextDueAt == nil {
			return errors.New("active monthly reset-card schedule has no due time")
		}
		dueAt := schedule.NextDueAt.UTC()
		if dueAt.After(now) {
			return nil
		}
		if !currentTermEnd.After(dueAt) {
			return cancelMonthlyResetCardSchedule(ctx, client, schedule, now)
		}
		windowEnd := monthlyResetCardWindowEnd(schedule, schedule.NextOccurrence, currentTermEnd)
		if !windowEnd.After(dueAt) || !now.Before(windowEnd) {
			issuanceID, inserted, err := insertMonthlyResetCardIssuance(ctx, client, schedule, dueAt, windowEnd, "skipped", "window_elapsed", now)
			_ = issuanceID
			if err != nil {
				return fmt.Errorf("record skipped monthly reset-card issuance: %w", err)
			}
			if !inserted {
				return nil
			}
			if err := completeMonthlyResetCardOccurrence(ctx, client, schedule, now); err != nil {
				return fmt.Errorf("advance skipped monthly reset-card schedule: %w", err)
			}
			continue
		}

		issuanceID, inserted, err := insertMonthlyResetCardIssuance(ctx, client, schedule, dueAt, windowEnd, "issued", "", now)
		if err != nil {
			return fmt.Errorf("record monthly reset-card issuance: %w", err)
		}
		if !inserted {
			return nil
		}
		expiresAt := now.Add(time.Duration(schedule.CardValidityDays) * 24 * time.Hour)
		if windowEnd.Before(expiresAt) {
			expiresAt = windowEnd
		}
		if currentTermEnd.Before(expiresAt) {
			expiresAt = currentTermEnd
		}
		if err := insertMonthlyResetCardGrant(ctx, client, schedule, issuanceID, expiresAt, now); err != nil {
			return fmt.Errorf("insert monthly reset-card grant: %w", err)
		}
		if err := completeMonthlyResetCardOccurrence(ctx, client, schedule, now); err != nil {
			return fmt.Errorf("advance monthly reset-card schedule: %w", err)
		}
		return nil
	}
	return nil
}
