package service

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestNormalizePlanEntitlementsMonthlyResetCardDelivery(t *testing.T) {
	_, monthly, err := normalizePlanEntitlements(map[string]any{
		"reset_card_count":         2,
		"reset_card_delivery_mode": " MONTHLY ",
		"reset_card_issue_count":   3,
		"reset_card_expiry_days":   14,
	})
	require.NoError(t, err)
	require.Equal(t, resetCardDeliveryModeMonthly, monthly.ResetCardDeliveryMode)
	require.Equal(t, 3, monthly.ResetCardIssueCount)
	require.Equal(t, 6, mustResetCardCommitment(t, monthly))

	_, immediate, err := normalizePlanEntitlements(map[string]any{
		"reset_card_count":         2,
		"reset_card_delivery_mode": "immediate",
		"reset_card_issue_count":   99,
		"reset_card_expiry_days":   14,
	})
	require.NoError(t, err)
	require.Equal(t, resetCardDeliveryModeImmediate, immediate.ResetCardDeliveryMode)
	require.Equal(t, 1, immediate.ResetCardIssueCount)

	_, noCards, err := normalizePlanEntitlements(map[string]any{
		"reset_card_delivery_mode": "monthly",
		"reset_card_issue_count":   3,
	})
	require.NoError(t, err)
	require.Equal(t, resetCardDeliveryModeImmediate, noCards.ResetCardDeliveryMode)
	require.Zero(t, noCards.ResetCardIssueCount)

	_, _, err = normalizePlanEntitlements(map[string]any{
		"reset_card_count":         1,
		"reset_card_delivery_mode": "weekly",
		"reset_card_expiry_days":   14,
	})
	require.ErrorContains(t, err, "reset_card_delivery_mode")
}

func mustResetCardCommitment(t *testing.T, entitlements PlanEntitlements) int {
	t.Helper()
	value, err := entitlements.ResetCardTotalCommitment()
	require.NoError(t, err)
	return value
}

func TestMonthlyResetCardPlanTermValidationUsesCalendarUnits(t *testing.T) {
	for _, tc := range []struct {
		name         string
		validityDays int
		validityUnit string
		issues       int
	}{
		{name: "months", validityDays: 2, validityUnit: "months", issues: 2},
		{name: "quarters", validityDays: 1, validityUnit: "quarter", issues: 3},
		{name: "years", validityDays: 1, validityUnit: "years", issues: 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePlanResetCardDelivery(PlanEntitlements{
				ResetCardCount:        1,
				ResetCardDeliveryMode: resetCardDeliveryModeMonthly,
				ResetCardIssueCount:   tc.issues,
			}, tc.validityDays, tc.validityUnit)
			require.NoError(t, err)
		})
	}

	err := validatePlanResetCardDelivery(PlanEntitlements{
		ResetCardCount:        1,
		ResetCardDeliveryMode: resetCardDeliveryModeMonthly,
		ResetCardIssueCount:   3,
	}, 90, "days")
	require.ErrorContains(t, err, "monthly reset cards require")

	err = validatePlanResetCardDelivery(PlanEntitlements{
		ResetCardCount:        1,
		ResetCardDeliveryMode: resetCardDeliveryModeMonthly,
		ResetCardIssueCount:   2,
	}, 1, "quarter")
	require.ErrorContains(t, err, "reset_card_issue_count")
}

func TestMonthlyResetCardCalendarAnchorsMonthEndAndLeapYear(t *testing.T) {
	anchor := time.Date(2024, time.January, 31, 9, 15, 0, 0, monthlyResetCardAnchorLocation)
	require.Equal(t, "2024-01-31", monthlyResetCardDueAt(anchor, 31, 0).In(monthlyResetCardAnchorLocation).Format("2006-01-02"))
	require.Equal(t, "2024-02-29", monthlyResetCardDueAt(anchor, 31, 1).In(monthlyResetCardAnchorLocation).Format("2006-01-02"))
	require.Equal(t, "2024-03-31", monthlyResetCardDueAt(anchor, 31, 2).In(monthlyResetCardAnchorLocation).Format("2006-01-02"))

	nonLeap := time.Date(2025, time.January, 31, 9, 15, 0, 0, monthlyResetCardAnchorLocation)
	require.Equal(t, "2025-02-28", monthlyResetCardDueAt(nonLeap, 31, 1).In(monthlyResetCardAnchorLocation).Format("2006-01-02"))
	require.Equal(t, "2025-03-31", monthlyResetCardDueAt(nonLeap, 31, 2).In(monthlyResetCardAnchorLocation).Format("2006-01-02"))
}

func TestPaymentConfigMonthlyResetCardPlanGuardFailsClosed(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	repo := &paymentConfigSettingRepoStub{values: map[string]string{}}
	svc := &PaymentConfigService{entClient: client, settingRepo: repo}
	group, err := client.Group.Create().
		SetName("monthly-plan-guard").
		SetPlatform(PlatformOpenAI).
		SetStatus(StatusActive).
		SetSubscriptionType(SubscriptionTypeSubscription).
		Save(ctx)
	require.NoError(t, err)
	request := CreatePlanRequest{
		GroupID:      group.ID,
		Name:         "quarterly cards",
		Price:        10,
		ValidityDays: 1,
		ValidityUnit: "quarter",
		Entitlements: map[string]any{
			"reset_card_count":         2,
			"reset_card_delivery_mode": "monthly",
			"reset_card_issue_count":   3,
			"reset_card_expiry_days":   14,
		},
	}
	_, err = svc.CreatePlan(ctx, request)
	require.Equal(t, "MONTHLY_RESET_CARDS_DISABLED", infraerrors.Reason(err))

	repo.values[SettingPaymentMonthlyResetCardsEnabled] = "true"
	plan, err := svc.CreatePlan(ctx, request)
	require.NoError(t, err)

	repo.values[SettingPaymentMonthlyResetCardsEnabled] = "false"
	name := "quarterly cards renamed"
	_, err = svc.UpdatePlan(ctx, plan.ID, UpdatePlanRequest{Name: &name})
	require.Equal(t, "MONTHLY_RESET_CARDS_DISABLED", infraerrors.Reason(err), "an unrelated patch cannot keep a monthly plan enabled")
	forSale := false
	updated, err := svc.UpdatePlan(ctx, plan.ID, UpdatePlanRequest{ForSale: &forSale})
	require.NoError(t, err, "operators must be able to take a monthly plan off sale while the worker is disabled")
	require.False(t, updated.ForSale)
}

func TestMonthlyResetCardDisabledHidesCatalogAndRejectsCheckoutAtBothBoundaries(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	settings := &paymentConfigSettingRepoStub{values: map[string]string{SettingPaymentMonthlyResetCardsEnabled: "true"}}
	configService := &PaymentConfigService{entClient: client, settingRepo: settings}
	user, err := client.User.Create().
		SetEmail("monthly-admission@example.com").
		SetUsername("monthly-admission").
		SetPasswordHash("hash").
		Save(ctx)
	require.NoError(t, err)
	group, err := client.Group.Create().
		SetName("monthly-admission").
		SetPlatform(PlatformOpenAI).
		SetStatus(StatusActive).
		SetSubscriptionType(SubscriptionTypeSubscription).
		Save(ctx)
	require.NoError(t, err)
	plan, err := configService.CreatePlan(ctx, CreatePlanRequest{
		GroupID: group.ID, Name: "monthly admission", Price: 10,
		ValidityDays: 1, ValidityUnit: "quarter", ForSale: true,
		Entitlements: map[string]any{
			"reset_card_count":         1,
			"reset_card_delivery_mode": resetCardDeliveryModeMonthly,
			"reset_card_issue_count":   3,
			"reset_card_expiry_days":   14,
		},
	})
	require.NoError(t, err)

	settings.values[SettingPaymentMonthlyResetCardsEnabled] = "false"
	catalog, err := configService.CustomerPaymentCatalogForUser(ctx, user.ID, nil)
	require.NoError(t, err)
	require.Empty(t, catalog.Plans, "the private rollout state must not be exposed as a purchasable card")

	paymentService := &PaymentService{
		entClient:     client,
		configService: configService,
		groupRepo: monthlyResetCardCheckoutGroupRepo{group: &Group{
			ID: group.ID, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription,
		}},
	}
	request := CreateOrderRequest{UserID: user.ID, PlanID: plan.ID, OrderType: payment.OrderTypeSubscription}
	_, err = paymentService.validateSubOrder(ctx, request)
	require.ErrorIs(t, err, ErrPurchaseNotAllowed)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	txCtx := dbent.NewTxContext(ctx, tx)
	_, _, err = paymentService.revalidateSubscriptionOrderInTx(txCtx, tx, request, plan)
	require.ErrorIs(t, err, ErrPurchaseNotAllowed, "a switch change after catalog validation must still prevent a durable order")
	require.NoError(t, tx.Rollback())
}

type monthlyResetCardCheckoutGroupRepo struct {
	GroupRepository
	group *Group
}

func (r monthlyResetCardCheckoutGroupRepo) GetByID(context.Context, int64) (*Group, error) {
	return r.group, nil
}

type monthlyResetCardContextSettingRepo struct {
	paymentConfigSettingRepoStub
	seen context.Context
}

func (r *monthlyResetCardContextSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	r.seen = ctx
	return r.paymentConfigSettingRepoStub.GetValue(ctx, key)
}

func TestPaymentMonthlyResetCardDeliveryRunOnceNormalizesNilContextAndStopCancels(t *testing.T) {
	client := newPaymentConfigServiceTestClient(t)
	installMonthlyResetCardSQLiteTables(t, client)
	repo := &monthlyResetCardContextSettingRepo{paymentConfigSettingRepoStub: paymentConfigSettingRepoStub{
		values: map[string]string{SettingPaymentMonthlyResetCardsEnabled: "false"},
	}}
	worker := NewPaymentMonthlyResetCardDeliveryService(client, &PaymentConfigService{settingRepo: repo}, time.Hour)
	require.NoError(t, worker.RunOnce(nil))
	require.Nil(t, repo.seen, "the delivery worker must not consult the new-order admission gate")

	worker.Start()
	worker.Start()
	worker.Stop()
	worker.Stop()
	select {
	case <-worker.ctx.Done():
	default:
		t.Fatal("Stop must cancel the worker context before waiting")
	}
}

func TestPaymentMonthlyResetCardDeliveryContinuesPaidScheduleWhenAdmissionGateIsDisabled(t *testing.T) {
	anchor := time.Date(2025, time.January, 15, 9, 0, 0, 0, monthlyResetCardAnchorLocation)
	now := anchor.Add(2 * time.Hour).UTC()
	fixture := newMonthlyResetCardFixture(t, anchor, monthlyResetCardDueAt(anchor, 15, 3), monthlyResetCardDueAt(anchor, 15, 3), 3)
	fixture.ensureSchedule(t)

	settings := &paymentConfigSettingRepoStub{values: map[string]string{
		SettingPaymentMonthlyResetCardsEnabled: "false",
	}}
	worker := NewPaymentMonthlyResetCardDeliveryService(
		fixture.client,
		&PaymentConfigService{settingRepo: settings},
		time.Hour,
	)
	worker.now = func() time.Time { return now }

	require.NoError(t, worker.RunOnce(fixture.ctx))
	assertMonthlyResetCardCounts(t, fixture, 1, 1)
}

func TestMonthlyResetCardScheduleIssuesExactlyOnceWithFrozenProvenance(t *testing.T) {
	anchor := time.Date(2025, time.January, 31, 9, 0, 0, 0, monthlyResetCardAnchorLocation)
	now := anchor.Add(2 * time.Hour).UTC()
	fixture := newMonthlyResetCardFixture(t, anchor, monthlyResetCardDueAt(anchor, 31, 3), monthlyResetCardDueAt(anchor, 31, 3), 3)

	var committed int
	scanMonthlyResetCardRow(t, fixture.client, fixture.ctx, `SELECT reset_card_count FROM payment_subscription_grants WHERE payment_order_id = $1`, []any{fixture.order.ID}, &committed)
	require.Equal(t, 6, committed, "refund accounting must fence all three deliveries of two cards")

	schedule := fixture.ensureSchedule(t)
	require.Equal(t, fixture.sub.ID, schedule.SubscriptionID, "the schedule must use the exact payment subscription grant")
	require.Equal(t, "gpt", *schedule.CardFamilyKey)
	require.Equal(t, 2, *schedule.SourceTierRank)

	worker := &PaymentMonthlyResetCardDeliveryService{entClient: fixture.client}
	candidate := paymentMonthlyResetCardDeliveryCandidate{ScheduleID: schedule.ID, PaymentOrderID: fixture.order.ID}
	require.NoError(t, worker.processCandidate(fixture.ctx, candidate, now))
	require.NoError(t, worker.processCandidate(fixture.ctx, candidate, now), "retrying the same occurrence must not duplicate a grant")

	var (
		issuanceCount int
		grantCount    int
		paymentOrder  sql.NullInt64
		family        sql.NullString
		rank          sql.NullInt64
		sourcePlan    int64
		resolved      bool
	)
	scanMonthlyResetCardRow(t, fixture.client, fixture.ctx, `SELECT COUNT(*) FROM subscription_reset_card_issuances WHERE schedule_id = $1 AND occurrence_index = 0`, []any{schedule.ID}, &issuanceCount)
	require.Equal(t, 1, issuanceCount)
	scanMonthlyResetCardRow(t, fixture.client, fixture.ctx, `
		SELECT payment_order_id, card_family_key, source_tier_rank, source_plan_id, tier_snapshot_resolved
		FROM subscription_reset_grants WHERE schedule_issuance_id IS NOT NULL`, nil,
		&paymentOrder, &family, &rank, &sourcePlan, &resolved)
	require.False(t, paymentOrder.Valid, "scheduled grants deliberately leave payment_order_id NULL")
	require.Equal(t, "gpt", family.String)
	require.Equal(t, int64(2), rank.Int64)
	require.Equal(t, int64(88), sourcePlan)
	require.True(t, resolved)
	scanMonthlyResetCardRow(t, fixture.client, fixture.ctx, `SELECT COUNT(*) FROM subscription_reset_grants WHERE schedule_issuance_id IS NOT NULL`, nil, &grantCount)
	require.Equal(t, 1, grantCount)
}

func TestMonthlyResetCardFutureScheduleKeepsFrozenExactOnlyTierAfterPolicyOpens(t *testing.T) {
	anchor := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond)
	anchorDay := anchor.In(monthlyResetCardAnchorLocation).Day()
	termEnd := monthlyResetCardDueAt(anchor, anchorDay, 3)
	fixture := newMonthlyResetCardFixture(t, anchor, termEnd, termEnd, 3)

	// This order predates tier policy. Its future schedule freezes a deliberate
	// NULL/NULL snapshot before an administrator later opens a policy.
	snapshot := make(map[string]any, len(fixture.order.ProductSnapshot))
	for key, value := range fixture.order.ProductSnapshot {
		snapshot[key] = value
	}
	delete(snapshot, "reset_card_tier")
	order, err := fixture.client.PaymentOrder.UpdateOneID(fixture.order.ID).
		SetProductSnapshot(snapshot).
		Save(fixture.ctx)
	require.NoError(t, err)
	fixture.order = order

	schedule := fixture.ensureSchedule(t)
	require.Nil(t, schedule.CardFamilyKey)
	require.Nil(t, schedule.SourceTierRank)

	_, err = (&PaymentConfigService{entClient: fixture.client}).UpsertResetCardTierPolicy(fixture.ctx, UpsertSubscriptionResetCardTierPolicyInput{
		GroupID: fixture.grant.GroupID, FamilyKey: "frozen-monthly", TierRank: 1,
	})
	require.NoError(t, err)

	due := monthlyResetCardDueAt(anchor, anchorDay, 0)
	require.NoError(t, workerProcessMonthlyResetCardCandidate(fixture, schedule, due.Add(time.Hour)))

	var familyNull, rankNull, resolved bool
	scanMonthlyResetCardRow(t, fixture.client, fixture.ctx, `
		SELECT card_family_key IS NULL, source_tier_rank IS NULL, tier_snapshot_resolved
		FROM subscription_reset_grants
		WHERE schedule_issuance_id IS NOT NULL
	`, nil, &familyNull, &rankNull, &resolved)
	require.True(t, familyNull)
	require.True(t, rankNull)
	require.True(t, resolved, "the service must explicitly mark its frozen NULL/NULL schedule grant resolved")
}

func TestMonthlyResetCardFulfillmentIssuesStartedFirstOccurrenceIndependentOfFutureWorkerGate(t *testing.T) {
	anchor := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	anchorDay := anchor.In(monthlyResetCardAnchorLocation).Day()
	termEnd := monthlyResetCardDueAt(anchor, anchorDay, 3)
	fixture := newMonthlyResetCardFixture(t, anchor, termEnd, termEnd, 3)

	// This function is invoked only after an immutable order is paid. It must
	// never inspect the rollout switch used by catalog/checkout and the future
	// worker, or a buyer who checked out just before a disable could lose card 0.
	require.NoError(t, grantPaymentProductEntitlementsForSubscriptionGrant(
		fixture.ctx, fixture.client, fixture.order, fixture.grant.GroupID, fixture.grant, fixture.sub,
	))
	assertMonthlyResetCardCounts(t, fixture, 1, 1)
}

func TestMonthlyResetCardScheduleSkipsClosedWindowsAndIssuesOnlyCurrentWindow(t *testing.T) {
	anchor := time.Date(2025, time.January, 31, 9, 0, 0, 0, monthlyResetCardAnchorLocation)
	now := time.Date(2025, time.April, 15, 9, 0, 0, 0, monthlyResetCardAnchorLocation).UTC()
	fixture := newMonthlyResetCardFixture(t, anchor, monthlyResetCardDueAt(anchor, 31, 3), monthlyResetCardDueAt(anchor, 31, 3), 3)
	schedule := fixture.ensureSchedule(t)

	worker := &PaymentMonthlyResetCardDeliveryService{entClient: fixture.client}
	require.NoError(t, worker.processCandidate(fixture.ctx, paymentMonthlyResetCardDeliveryCandidate{ScheduleID: schedule.ID, PaymentOrderID: fixture.order.ID}, now))

	var skipped, issued, grants int
	scanMonthlyResetCardRow(t, fixture.client, fixture.ctx, `SELECT COUNT(*) FROM subscription_reset_card_issuances WHERE schedule_id = $1 AND status = 'skipped' AND skip_reason = 'window_elapsed'`, []any{schedule.ID}, &skipped)
	require.Equal(t, 2, skipped)
	scanMonthlyResetCardRow(t, fixture.client, fixture.ctx, `SELECT COUNT(*) FROM subscription_reset_card_issuances WHERE schedule_id = $1 AND status = 'issued'`, []any{schedule.ID}, &issued)
	require.Equal(t, 1, issued)
	scanMonthlyResetCardRow(t, fixture.client, fixture.ctx, `SELECT COUNT(*) FROM subscription_reset_grants WHERE schedule_issuance_id IS NOT NULL`, nil, &grants)
	require.Equal(t, 1, grants, "missed windows must become ledger skips instead of backlogged cards")
}

func TestMonthlyResetCardScheduleDoesNotIssueFutureRenewalAndCancelsShortenedTerm(t *testing.T) {
	now := time.Date(2025, time.January, 15, 9, 0, 0, 0, monthlyResetCardAnchorLocation).UTC()
	futureAnchor := now.Add(48 * time.Hour)
	future := newMonthlyResetCardFixture(t, futureAnchor, monthlyResetCardDueAt(futureAnchor, futureAnchor.In(monthlyResetCardAnchorLocation).Day(), 3), monthlyResetCardDueAt(futureAnchor, futureAnchor.In(monthlyResetCardAnchorLocation).Day(), 3), 3)
	futureSchedule := future.ensureSchedule(t)
	worker := &PaymentMonthlyResetCardDeliveryService{entClient: future.client}
	require.NoError(t, worker.processCandidate(future.ctx, paymentMonthlyResetCardDeliveryCandidate{ScheduleID: futureSchedule.ID, PaymentOrderID: future.order.ID}, now))
	assertMonthlyResetCardCounts(t, future, 0, 0)

	anchor := time.Date(2025, time.January, 31, 9, 0, 0, 0, monthlyResetCardAnchorLocation)
	shortened := newMonthlyResetCardFixture(t, anchor, monthlyResetCardDueAt(anchor, 31, 3), anchor.Add(-time.Hour), 3)
	shortenedSchedule := shortened.ensureSchedule(t)
	require.NoError(t, workerProcessMonthlyResetCardCandidate(shortened, shortenedSchedule, anchor.Add(time.Hour).UTC()))
	var status string
	scanMonthlyResetCardRow(t, shortened.client, shortened.ctx, `SELECT status FROM subscription_reset_card_schedules WHERE id = $1`, []any{shortenedSchedule.ID}, &status)
	require.Equal(t, "cancelled", status, "a shortened term that no longer reaches due must stop delivery")
	assertMonthlyResetCardCounts(t, shortened, 0, 0)
}

func TestMonthlyResetCardWorkerPausesForRefundAndResumesAfterFailureRelease(t *testing.T) {
	anchor := time.Date(2025, time.January, 31, 9, 0, 0, 0, monthlyResetCardAnchorLocation)
	now := anchor.Add(time.Hour).UTC()
	fixture := newMonthlyResetCardFixture(t, anchor, monthlyResetCardDueAt(anchor, 31, 3), monthlyResetCardDueAt(anchor, 31, 3), 3)
	schedule := fixture.ensureSchedule(t)
	candidate := paymentMonthlyResetCardDeliveryCandidate{ScheduleID: schedule.ID, PaymentOrderID: fixture.order.ID}
	worker := &PaymentMonthlyResetCardDeliveryService{entClient: fixture.client}

	_, err := fixture.client.PaymentOrder.UpdateOneID(fixture.order.ID).SetStatus(OrderStatusRefundPending).Save(fixture.ctx)
	require.NoError(t, err)
	require.NoError(t, worker.processCandidate(fixture.ctx, candidate, now))
	assertMonthlyResetCardCounts(t, fixture, 0, 0)

	_, err = fixture.client.PaymentOrder.UpdateOneID(fixture.order.ID).SetStatus(OrderStatusRefundFailed).Save(fixture.ctx)
	require.NoError(t, err)
	_, err = fixture.client.ExecContext(fixture.ctx, `INSERT INTO unified_payment_refund_attempts (order_id, entitlement_reserved) VALUES ($1, TRUE)`, fixture.order.ID)
	require.NoError(t, err)
	require.NoError(t, worker.processCandidate(fixture.ctx, candidate, now))
	assertMonthlyResetCardCounts(t, fixture, 0, 0)

	_, err = fixture.client.ExecContext(fixture.ctx, `UPDATE unified_payment_refund_attempts SET entitlement_reserved = FALSE WHERE order_id = $1`, fixture.order.ID)
	require.NoError(t, err)
	require.NoError(t, worker.processCandidate(fixture.ctx, candidate, now))
	assertMonthlyResetCardCounts(t, fixture, 1, 1)

	finalRefund := newMonthlyResetCardFixture(t, anchor.AddDate(1, 0, 0), monthlyResetCardDueAt(anchor.AddDate(1, 0, 0), 31, 3), monthlyResetCardDueAt(anchor.AddDate(1, 0, 0), 31, 3), 3)
	finalSchedule := finalRefund.ensureSchedule(t)
	_, err = finalRefund.client.PaymentOrder.UpdateOneID(finalRefund.order.ID).SetStatus(OrderStatusRefunded).Save(finalRefund.ctx)
	require.NoError(t, err)
	require.NoError(t, workerProcessMonthlyResetCardCandidate(finalRefund, finalSchedule, anchor.AddDate(1, 0, 0).Add(time.Hour).UTC()))
	var finalStatus string
	scanMonthlyResetCardRow(t, finalRefund.client, finalRefund.ctx, `SELECT status FROM subscription_reset_card_schedules WHERE id = $1`, []any{finalSchedule.ID}, &finalStatus)
	require.Equal(t, "cancelled", finalStatus, "a final refund must terminate future monthly deliveries")
	assertMonthlyResetCardCounts(t, finalRefund, 0, 0)
}

func TestMonthlyResetCardScheduleConflictChecksEveryFrozenField(t *testing.T) {
	anchor := time.Date(2025, time.January, 31, 9, 0, 0, 0, monthlyResetCardAnchorLocation).UTC()
	termEnd := monthlyResetCardDueAt(anchor, 31, 3)
	planID := int64(88)
	family := "gpt"
	rank := 2
	entitlements := PlanEntitlements{
		ResetCardCount: 2, ResetCardDeliveryMode: resetCardDeliveryModeMonthly,
		ResetCardIssueCount: 3, ResetCardExpiryDays: 14,
	}
	order := &dbent.PaymentOrder{ID: 9, PlanID: &planID}
	grant := &paymentSubscriptionGrant{
		SubscriptionID: 7, UserID: 6, GroupID: 5, TermStart: anchor, OriginalEnd: termEnd,
	}
	schedule := &monthlyResetCardSchedule{
		PaymentOrderID: order.ID, SubscriptionID: grant.SubscriptionID, UserID: grant.UserID, GroupID: grant.GroupID,
		SourcePlanID: planID, CardFamilyKey: &family, SourceTierRank: &rank,
		AnchorAt: anchor, AnchorTimezone: monthlyResetCardAnchorTimezone, AnchorDay: 31, TermEndAt: termEnd,
		OccurrenceCount: 3, CardsPerOccurrence: 2, CardValidityDays: 14,
	}
	tier := &SubscriptionResetCardTierSnapshot{FamilyKey: family, TierRank: rank, SourcePlanID: &planID}
	require.True(t, monthlyResetCardScheduleMatchesFrozenInput(schedule, order, grant, entitlements, tier, anchor, termEnd, 31, 14))

	for _, mutate := range []func(*monthlyResetCardSchedule){
		func(value *monthlyResetCardSchedule) { value.CardsPerOccurrence++ },
		func(value *monthlyResetCardSchedule) { value.TermEndAt = value.TermEndAt.Add(time.Hour) },
		func(value *monthlyResetCardSchedule) { value.AnchorDay = 30 },
		func(value *monthlyResetCardSchedule) { next := *value.SourceTierRank + 1; value.SourceTierRank = &next },
	} {
		copy := *schedule
		if schedule.SourceTierRank != nil {
			rankCopy := *schedule.SourceTierRank
			copy.SourceTierRank = &rankCopy
		}
		mutate(&copy)
		require.False(t, monthlyResetCardScheduleMatchesFrozenInput(&copy, order, grant, entitlements, tier, anchor, termEnd, 31, 14))
	}
}

type monthlyResetCardFixture struct {
	ctx          context.Context
	client       *dbent.Client
	order        *dbent.PaymentOrder
	grant        *paymentSubscriptionGrant
	sub          *dbent.UserSubscription
	entitlements PlanEntitlements
}

func newMonthlyResetCardFixture(t *testing.T, anchor, termEnd, currentEnd time.Time, issueCount int) *monthlyResetCardFixture {
	t.Helper()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	installMonthlyResetCardSQLiteTables(t, client)
	anchor = anchor.UTC().Truncate(time.Microsecond)
	termEnd = termEnd.UTC().Truncate(time.Microsecond)
	currentEnd = currentEnd.UTC().Truncate(time.Microsecond)
	require.True(t, termEnd.After(anchor), "fixture term must be non-empty")
	fixtureID := fmt.Sprintf("%d", anchor.UnixNano())

	user, err := client.User.Create().
		SetEmail("monthly-reset-card-" + fixtureID + "@example.com").
		SetUsername("monthly-reset-card-" + fixtureID).
		SetPasswordHash("hash").
		Save(ctx)
	require.NoError(t, err)
	group, err := client.Group.Create().
		SetName("monthly-reset-card-" + fixtureID).
		SetPlatform(PlatformOpenAI).
		SetStatus(StatusActive).
		SetSubscriptionType(SubscriptionTypeSubscription).
		Save(ctx)
	require.NoError(t, err)
	sub, err := client.UserSubscription.Create().
		SetUserID(user.ID).
		SetGroupID(group.ID).
		SetStartsAt(anchor).
		SetExpiresAt(termEnd).
		SetStatus(SubscriptionStatusActive).
		Save(ctx)
	require.NoError(t, err)

	normalized, entitlements, err := normalizePlanEntitlements(map[string]any{
		"reset_card_count":         2,
		"reset_card_delivery_mode": resetCardDeliveryModeMonthly,
		"reset_card_issue_count":   issueCount,
		"reset_card_expiry_days":   14,
	})
	require.NoError(t, err)
	const planID int64 = 88
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(20).
		SetPayAmount(20).
		SetFeeRate(0).
		SetRechargeCode("MONTHLY-RESET-CARD-" + fixtureID).
		SetOutTradeNo("monthly-reset-card-order-" + fixtureID).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("monthly-reset-card-trade-" + fixtureID).
		SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(planID).
		SetSubscriptionGroupID(group.ID).
		SetSubscriptionDays(90).
		SetStatus(OrderStatusCompleted).
		SetPaidAt(anchor).
		SetExpiresAt(termEnd).
		SetClientIP("127.0.0.1").
		SetSrcHost("monthly-reset-card.test").
		SetProductSnapshot(map[string]any{
			"plan_id":      planID,
			"entitlements": normalized,
			"reset_card_tier": map[string]any{
				"family_key":     "gpt",
				"tier_rank":      2,
				"source_plan_id": planID,
			},
		}).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, insertPaymentSubscriptionGrant(ctx, client, order, &UserSubscription{
		ID: sub.ID, UserID: sub.UserID, GroupID: sub.GroupID, ExpiresAt: termEnd,
	}, anchor))
	if !currentEnd.Equal(termEnd) {
		_, err = client.ExecContext(ctx, `UPDATE payment_subscription_grants SET current_term_end_at = $1 WHERE payment_order_id = $2`, currentEnd, order.ID)
		require.NoError(t, err)
	}
	grant, exactSubscription, err := loadPaymentSubscriptionRefundState(ctx, client, order.ID, false)
	require.NoError(t, err)
	return &monthlyResetCardFixture{
		ctx: ctx, client: client, order: order, grant: grant, sub: exactSubscription, entitlements: entitlements,
	}
}

func (f *monthlyResetCardFixture) ensureSchedule(t *testing.T) *monthlyResetCardSchedule {
	t.Helper()
	schedule, err := ensureMonthlyResetCardSchedule(f.ctx, f.client, f.order, f.grant, f.entitlements)
	require.NoError(t, err)
	require.NotNil(t, schedule)
	return schedule
}

func workerProcessMonthlyResetCardCandidate(f *monthlyResetCardFixture, schedule *monthlyResetCardSchedule, now time.Time) error {
	return (&PaymentMonthlyResetCardDeliveryService{entClient: f.client}).processCandidate(
		f.ctx,
		paymentMonthlyResetCardDeliveryCandidate{ScheduleID: schedule.ID, PaymentOrderID: f.order.ID},
		now,
	)
}

func assertMonthlyResetCardCounts(t *testing.T, fixture *monthlyResetCardFixture, wantIssuances, wantGrants int) {
	t.Helper()
	var issuances, grants int
	scanMonthlyResetCardRow(t, fixture.client, fixture.ctx, `
		SELECT COUNT(*)
		FROM subscription_reset_card_issuances issuance
		JOIN subscription_reset_card_schedules schedule ON schedule.id = issuance.schedule_id
		WHERE schedule.payment_order_id = $1`, []any{fixture.order.ID}, &issuances)
	scanMonthlyResetCardRow(t, fixture.client, fixture.ctx, `
		SELECT COUNT(*)
		FROM subscription_reset_grants grant
		JOIN subscription_reset_card_issuances issuance ON issuance.id = grant.schedule_issuance_id
		JOIN subscription_reset_card_schedules schedule ON schedule.id = issuance.schedule_id
		WHERE schedule.payment_order_id = $1`, []any{fixture.order.ID}, &grants)
	require.Equal(t, wantIssuances, issuances)
	require.Equal(t, wantGrants, grants)
}

func installMonthlyResetCardSQLiteTables(t *testing.T, client *dbent.Client) {
	t.Helper()
	_, err := client.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS subscription_reset_card_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			payment_order_id INTEGER NOT NULL UNIQUE,
			subscription_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			group_id INTEGER NOT NULL,
			source_plan_id INTEGER NOT NULL,
			card_family_key TEXT,
			source_tier_rank INTEGER,
			anchor_at DATETIME NOT NULL,
			anchor_timezone TEXT NOT NULL,
			anchor_day INTEGER NOT NULL,
			term_end_at DATETIME NOT NULL,
			occurrence_count INTEGER NOT NULL,
			cards_per_occurrence INTEGER NOT NULL,
			card_validity_days INTEGER NOT NULL,
			next_occurrence INTEGER NOT NULL,
			next_due_at DATETIME,
			status TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		CREATE TABLE IF NOT EXISTS subscription_reset_card_issuances (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			schedule_id INTEGER NOT NULL,
			occurrence_index INTEGER NOT NULL,
			due_at DATETIME NOT NULL,
			window_end_at DATETIME NOT NULL,
			status TEXT NOT NULL,
			skip_reason TEXT,
			issued_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL,
			UNIQUE (schedule_id, occurrence_index)
		);
		CREATE TABLE IF NOT EXISTS subscription_reset_grants (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			subscription_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			group_id INTEGER NOT NULL,
			quantity INTEGER NOT NULL,
			used_count INTEGER NOT NULL DEFAULT 0,
			expires_at DATETIME NOT NULL,
			issued_by INTEGER,
			payment_order_id INTEGER,
			schedule_issuance_id INTEGER UNIQUE,
			card_family_key TEXT,
			source_tier_rank INTEGER,
			source_plan_id INTEGER,
			tier_snapshot_resolved BOOLEAN NOT NULL DEFAULT FALSE,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		CREATE TABLE IF NOT EXISTS unified_payment_refund_attempts (
			order_id INTEGER NOT NULL,
			entitlement_reserved BOOLEAN NOT NULL DEFAULT FALSE
		)
	`)
	require.NoError(t, err)
}

func scanMonthlyResetCardRow(t *testing.T, client *dbent.Client, ctx context.Context, query string, args []any, dest ...any) {
	t.Helper()
	rows, err := client.QueryContext(ctx, query, args...)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	require.True(t, rows.Next(), "expected a row for query: %s", query)
	require.NoError(t, rows.Scan(dest...))
	require.NoError(t, rows.Err())
}
