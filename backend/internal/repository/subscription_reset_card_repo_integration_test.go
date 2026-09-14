//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionResetCardRepository_GrantConsumeAndOwnership(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewSubscriptionResetCardRepository(client)
	now := time.Now().UTC().Truncate(time.Microsecond)

	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("reset-card-%d@example.com", time.Now().UnixNano())})
	other := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("reset-card-other-%d@example.com", time.Now().UnixNano())})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             fmt.Sprintf("reset-card-group-%d", time.Now().UnixNano()),
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	sub := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:          user.ID,
		GroupID:         group.ID,
		Status:          service.SubscriptionStatusActive,
		ExpiresAt:       now.Add(24 * time.Hour),
		DailyUsageUSD:   3,
		WeeklyUsageUSD:  4,
		MonthlyUsageUSD: 5,
	})

	laterExpiry := now.Add(2 * time.Hour)
	earlierExpiry := now.Add(time.Hour)
	byGroup, err := repo.GrantToGroups(ctx, []int64{group.ID}, 2, laterExpiry, 0, now)
	require.NoError(t, err)
	require.Equal(t, int64(1), byGroup[group.ID])
	byGroup, err = repo.GrantToGroups(ctx, []int64{group.ID}, 1, earlierExpiry, 0, now)
	require.NoError(t, err)
	require.Equal(t, int64(1), byGroup[group.ID])

	summaries, err := repo.ListAvailable(ctx, []int64{sub.ID}, now)
	require.NoError(t, err)
	require.Equal(t, 3, summaries[sub.ID].AvailableCount)
	require.Equal(t, []service.SubscriptionResetCardBatch{
		{Remaining: 1, ExpiresAt: earlierExpiry},
		{Remaining: 2, ExpiresAt: laterExpiry},
	}, summaries[sub.ID].Batches)

	_, err = repo.ConsumeAndReset(ctx, other.ID, sub.ID, now, now)
	require.ErrorIs(t, err, service.ErrSubscriptionNotFound)
	summaries, err = repo.ListAvailable(ctx, []int64{sub.ID}, now)
	require.NoError(t, err)
	require.Equal(t, 3, summaries[sub.ID].AvailableCount, "ownership failure must not consume a card")

	groupID, err := repo.ConsumeAndReset(ctx, user.ID, sub.ID, now, now)
	require.NoError(t, err)
	require.Equal(t, group.ID, groupID)

	refreshed, err := client.UserSubscription.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.Zero(t, refreshed.DailyUsageUsd)
	require.Zero(t, refreshed.WeeklyUsageUsd)
	require.Zero(t, refreshed.MonthlyUsageUsd)
	summaries, err = repo.ListAvailable(ctx, []int64{sub.ID}, now)
	require.NoError(t, err)
	require.Equal(t, 2, summaries[sub.ID].AvailableCount)

	var earlierUsed, laterUsed int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT used_count
		FROM subscription_reset_grants
		WHERE subscription_id = $1 AND expires_at = $2
	`, sub.ID, earlierExpiry).Scan(&earlierUsed))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT used_count
		FROM subscription_reset_grants
		WHERE subscription_id = $1 AND expires_at = $2
	`, sub.ID, laterExpiry).Scan(&laterUsed))
	require.Equal(t, 1, earlierUsed, "the earliest-expiring batch must be consumed first")
	require.Zero(t, laterUsed)

	expiredSummaries, err := repo.ListAvailable(ctx, []int64{sub.ID}, laterExpiry.Add(time.Microsecond))
	require.NoError(t, err)
	require.NotContains(t, expiredSummaries, sub.ID, "expired batches must not be exposed as available")
}

func TestSubscriptionResetCardRepository_GrantTargetsOnlySelectedSubscription(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewSubscriptionResetCardRepository(client)
	now := time.Now().UTC().Truncate(time.Microsecond)

	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("reset-card-direct-%d@example.com", time.Now().UnixNano())})
	other := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("reset-card-direct-other-%d@example.com", time.Now().UnixNano())})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             fmt.Sprintf("reset-card-direct-group-%d", time.Now().UnixNano()),
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	target := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:    user.ID,
		GroupID:   group.ID,
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: now.Add(24 * time.Hour),
	})
	nonTarget := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:    other.ID,
		GroupID:   group.ID,
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: now.Add(24 * time.Hour),
	})

	granted, err := repo.GrantToSubscriptions(ctx, []int64{target.ID}, 2, now.Add(time.Hour), 0, now)
	require.NoError(t, err)
	require.Equal(t, map[int64]int64{target.ID: 1}, granted)

	summaries, err := repo.ListAvailable(ctx, []int64{target.ID, nonTarget.ID}, now)
	require.NoError(t, err)
	require.Equal(t, 2, summaries[target.ID].AvailableCount)
	require.NotContains(t, summaries, nonTarget.ID)
}

func TestSubscriptionResetCardRepository_ConcurrentSingleCardUse(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewSubscriptionResetCardRepository(client)
	now := time.Now().UTC().Truncate(time.Microsecond)

	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("reset-card-race-%d@example.com", time.Now().UnixNano())})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             fmt.Sprintf("reset-card-race-group-%d", time.Now().UnixNano()),
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	sub := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:    user.ID,
		GroupID:   group.ID,
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: now.Add(24 * time.Hour),
	})
	_, err := repo.GrantToGroups(ctx, []int64{group.ID}, 1, now.Add(time.Hour), 0, now)
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, consumeErr := repo.ConsumeAndReset(ctx, user.ID, sub.ID, now, now)
			errs <- consumeErr
		}()
	}
	wg.Wait()
	close(errs)

	var success, unavailable int
	for consumeErr := range errs {
		switch {
		case consumeErr == nil:
			success++
		case errors.Is(consumeErr, service.ErrResetCardUnavailable):
			unavailable++
		default:
			t.Fatalf("unexpected consume error: %v", consumeErr)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, unavailable)
}

func TestSubscriptionResetCardRepository_LegacyGrantSQLWaitsForFirstTierPolicyWriteAndGetsTypedSnapshot(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	policyService := service.NewPaymentConfigService(client, nil, nil)
	stamp := time.Now().UnixNano()
	now := time.Now().UTC().Truncate(time.Microsecond)
	familyKey := fmt.Sprintf("race%d", stamp)

	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("reset-tier-race-%d@example.com", stamp)})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             fmt.Sprintf("reset-tier-race-group-%d", stamp),
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:    user.ID,
		GroupID:   group.ID,
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: now.Add(24 * time.Hour),
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM subscription_reset_grants WHERE user_id = $1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM subscription_reset_card_tiers WHERE group_id = $1", group.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM user_subscriptions WHERE id = $1", subscription.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", group.ID)
	})

	// Hold the actual first-write after Upsert has taken its parent group lock
	// but before it can create the tier child row. This makes the grant race
	// deterministic without introducing a production-only test hook.
	gateKey := fmt.Sprintf("reset-card-tier-first-write-test:%d", stamp)
	functionName := fmt.Sprintf("block_reset_card_tier_insert_%d", stamp)
	triggerName := fmt.Sprintf("block_reset_card_tier_insert_trigger_%d", stamp)
	_, err := integrationDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.group_id = %d THEN
				PERFORM pg_advisory_xact_lock(hashtextextended('%s', 0));
			END IF;
			RETURN NEW;
		END;
		$body$
	`, functionName, group.ID, gateKey))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s
		BEFORE INSERT ON subscription_reset_card_tiers
		FOR EACH ROW EXECUTE FUNCTION %s()
	`, triggerName, functionName))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON subscription_reset_card_tiers", triggerName))
		_, _ = integrationDB.ExecContext(ctx, fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
	})

	gateConn, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	var releaseOnce sync.Once
	releaseGate := func() error {
		var releaseErr error
		releaseOnce.Do(func() {
			_, releaseErr = gateConn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, gateKey)
		})
		return releaseErr
	}
	t.Cleanup(func() {
		_ = releaseGate()
		_ = gateConn.Close()
	})
	_, err = gateConn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, gateKey)
	require.NoError(t, err)

	upsertResult := make(chan error, 1)
	go func() {
		_, upsertErr := policyService.UpsertResetCardTierPolicy(ctx, service.UpsertSubscriptionResetCardTierPolicyInput{
			GroupID: group.ID, FamilyKey: familyKey, TierRank: 1,
		})
		upsertResult <- upsertErr
	}()

	// A NOWAIT shared lock fails only after Upsert owns the parent group FOR
	// UPDATE; the trigger keeps that transaction at its first child INSERT.
	require.Eventually(t, func() bool {
		probe, probeErr := integrationDB.BeginTx(ctx, nil)
		if probeErr != nil {
			return false
		}
		defer func() { _ = probe.Rollback() }()
		_, probeErr = probe.ExecContext(ctx, `SELECT id FROM groups WHERE id = $1 FOR SHARE NOWAIT`, group.ID)
		return probeErr != nil
	}, 2*time.Second, 10*time.Millisecond)

	type grantResult struct {
		grantID int64
		err     error
	}
	grantStarted := make(chan struct{})
	grantDone := make(chan grantResult, 1)
	go func() {
		close(grantStarted)
		grantID, grantErr := insertLegacySubscriptionResetCardGrant(
			ctx, subscription.ID, user.ID, group.ID, now.Add(time.Hour), now,
		)
		grantDone <- grantResult{grantID: grantID, err: grantErr}
	}()
	<-grantStarted
	select {
	case result := <-grantDone:
		t.Fatalf("legacy grant completed while the first tier write held the group lock: grant_id=%d err=%v", result.grantID, result.err)
	case <-time.After(150 * time.Millisecond):
	}

	require.NoError(t, releaseGate())
	require.NoError(t, <-upsertResult)
	result := <-grantDone
	require.NoError(t, result.err)
	require.NotZero(t, result.grantID)

	var (
		actualFamily string
		actualRank   int
		resolved     bool
	)
	err = integrationDB.QueryRowContext(ctx, `
		SELECT card_family_key, source_tier_rank, tier_snapshot_resolved
		FROM subscription_reset_grants
		WHERE id = $1
	`, result.grantID).Scan(&actualFamily, &actualRank, &resolved)
	require.NoError(t, err)
	require.Equal(t, familyKey, actualFamily)
	require.Equal(t, 1, actualRank)
	require.True(t, resolved)
}

func TestSubscriptionResetCardRepository_LegacyGrantSQLSnapshotTrigger(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	policyService := service.NewPaymentConfigService(client, nil, nil)
	stamp := time.Now().UnixNano()
	now := time.Now().UTC().Truncate(time.Microsecond)
	familyKey := fmt.Sprintf("legacy%d", stamp)

	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("reset-tier-legacy-%d@example.com", stamp)})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             fmt.Sprintf("reset-tier-legacy-group-%d", stamp),
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:    user.ID,
		GroupID:   group.ID,
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: now.Add(24 * time.Hour),
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM subscription_reset_grants WHERE user_id = $1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM subscription_reset_card_tiers WHERE group_id = $1", group.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM user_subscriptions WHERE id = $1", subscription.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", group.ID)
	})

	// This raw statement is intentionally the column list emitted by a binary
	// from before migration 248: it neither has typed snapshot columns nor
	// takes the parent group lock itself.
	legacyGrantID, err := insertLegacySubscriptionResetCardGrant(
		ctx, subscription.ID, user.ID, group.ID, now.Add(2*time.Hour), now,
	)
	require.NoError(t, err)

	var familyNull, rankNull, resolved bool
	err = integrationDB.QueryRowContext(ctx, `
		SELECT card_family_key IS NULL, source_tier_rank IS NULL, tier_snapshot_resolved
		FROM subscription_reset_grants
		WHERE id = $1
	`, legacyGrantID).Scan(&familyNull, &rankNull, &resolved)
	require.NoError(t, err)
	require.True(t, familyNull)
	require.True(t, rankNull, "without a policy, legacy SQL must remain exact-only")
	require.True(t, resolved, "the old SQL sentinel must be resolved before persistence")
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO subscription_reset_grants (
			subscription_id, user_id, group_id, quantity, used_count,
			expires_at, issued_by, card_family_key, source_tier_rank, tier_snapshot_resolved, created_at, updated_at
		) VALUES ($1, $2, $3, 1, 0, $4, NULL, $5, $6, TRUE, $7, $7)
	`, subscription.ID, user.ID, group.ID, now.Add(90*time.Minute), familyKey, 1, now.Add(time.Second))
	require.ErrorContains(t, err, "tier snapshot must match the group policy")

	_, err = policyService.UpsertResetCardTierPolicy(ctx, service.UpsertSubscriptionResetCardTierPolicyInput{
		GroupID: group.ID, FamilyKey: familyKey, TierRank: 1,
	})
	require.NoError(t, err)

	// Creating the policy must not rewrite an older exact-only card, including
	// when that card later receives its normal usage-counter update.
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE subscription_reset_grants
		SET used_count = used_count + 1, updated_at = $2
		WHERE id = $1
	`, legacyGrantID, now.Add(time.Minute))
	require.NoError(t, err)
	err = integrationDB.QueryRowContext(ctx, `
		SELECT card_family_key IS NULL, source_tier_rank IS NULL, tier_snapshot_resolved
		FROM subscription_reset_grants
		WHERE id = $1
	`, legacyGrantID).Scan(&familyNull, &rankNull, &resolved)
	require.NoError(t, err)
	require.True(t, familyNull)
	require.True(t, rankNull, "historical legacy cards must never be backfilled")
	require.True(t, resolved)

	typedGrantID, err := insertLegacySubscriptionResetCardGrant(
		ctx, subscription.ID, user.ID, group.ID, now.Add(3*time.Hour), now.Add(time.Second),
	)
	require.NoError(t, err)
	var (
		actualFamily     string
		actualRank       int
		sourcePlanIsNull bool
		actualResolved   bool
	)
	err = integrationDB.QueryRowContext(ctx, `
		SELECT card_family_key, source_tier_rank, source_plan_id IS NULL, tier_snapshot_resolved
		FROM subscription_reset_grants
		WHERE id = $1
	`, typedGrantID).Scan(&actualFamily, &actualRank, &sourcePlanIsNull, &actualResolved)
	require.NoError(t, err)
	require.Equal(t, familyKey, actualFamily)
	require.Equal(t, 1, actualRank)
	require.True(t, sourcePlanIsNull, "the trigger preserves legacy NULL source-plan provenance")
	require.True(t, actualResolved)

	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO subscription_reset_grants (
			subscription_id, user_id, group_id, quantity, used_count,
			expires_at, issued_by, card_family_key, source_tier_rank, tier_snapshot_resolved, created_at, updated_at
		) VALUES ($1, $2, $3, 1, 0, $4, NULL, $5, $6, TRUE, $7, $7)
	`, subscription.ID, user.ID, group.ID, now.Add(4*time.Hour), familyKey, 1, now.Add(2*time.Second))
	require.NoError(t, err, "a current writer may repeat the matching typed snapshot")

	var frozenGrantID int64
	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO subscription_reset_grants (
			subscription_id, user_id, group_id, quantity, used_count,
			expires_at, issued_by, card_family_key, source_tier_rank, source_plan_id,
			tier_snapshot_resolved, created_at, updated_at
		) VALUES ($1, $2, $3, 1, 0, $4, NULL, NULL, NULL, $5, TRUE, $6, $6)
		RETURNING id
	`, subscription.ID, user.ID, group.ID, now.Add(5*time.Hour), int64(4242), now.Add(3*time.Second)).Scan(&frozenGrantID)
	require.NoError(t, err, "an explicit frozen NULL/NULL snapshot must remain exact-only after policy opens")
	var frozenFamilyNull, frozenRankNull, frozenResolved bool
	var frozenSourcePlan int64
	err = integrationDB.QueryRowContext(ctx, `
		SELECT card_family_key IS NULL, source_tier_rank IS NULL, source_plan_id, tier_snapshot_resolved
		FROM subscription_reset_grants
		WHERE id = $1
	`, frozenGrantID).Scan(&frozenFamilyNull, &frozenRankNull, &frozenSourcePlan, &frozenResolved)
	require.NoError(t, err)
	require.True(t, frozenFamilyNull)
	require.True(t, frozenRankNull)
	require.Equal(t, int64(4242), frozenSourcePlan)
	require.True(t, frozenResolved)

	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO subscription_reset_grants (
			subscription_id, user_id, group_id, quantity, used_count,
			expires_at, issued_by, card_family_key, tier_snapshot_resolved, created_at, updated_at
		) VALUES ($1, $2, $3, 1, 0, $4, NULL, $5, TRUE, $6, $6)
	`, subscription.ID, user.ID, group.ID, now.Add(6*time.Hour), familyKey, now.Add(4*time.Second))
	require.ErrorContains(t, err, "tier snapshot must include both family and rank")

	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO subscription_reset_grants (
			subscription_id, user_id, group_id, quantity, used_count,
			expires_at, issued_by, card_family_key, source_tier_rank, tier_snapshot_resolved, created_at, updated_at
		) VALUES ($1, $2, $3, 1, 0, $4, NULL, $5, $6, TRUE, $7, $7)
	`, subscription.ID, user.ID, group.ID, now.Add(7*time.Hour), familyKey, 2, now.Add(5*time.Second))
	require.ErrorContains(t, err, "tier snapshot must match the group policy")

	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO subscription_reset_grants (
			subscription_id, user_id, group_id, quantity, used_count,
			expires_at, issued_by, card_family_key, source_tier_rank, tier_snapshot_resolved, created_at, updated_at
		) VALUES ($1, $2, $3, 1, 0, $4, NULL, $5, $6, FALSE, $7, $7)
	`, subscription.ID, user.ID, group.ID, now.Add(8*time.Hour), familyKey, 1, now.Add(6*time.Second))
	require.ErrorContains(t, err, "typed snapshot requires explicit resolution")
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO subscription_reset_grants (
			subscription_id, user_id, group_id, quantity, used_count,
			expires_at, issued_by, tier_snapshot_resolved, created_at, updated_at
		) VALUES ($1, $2, $3, 1, 0, $4, NULL, NULL, $5, $5)
	`, subscription.ID, user.ID, group.ID, now.Add(9*time.Hour), now.Add(7*time.Second))
	require.ErrorContains(t, err, "tier snapshot resolution is required")

	_, err = integrationDB.ExecContext(ctx, `
		UPDATE subscription_reset_grants
		SET card_family_key = 'wrong'
		WHERE id = $1
	`, typedGrantID)
	require.ErrorContains(t, err, "tier provenance is immutable")
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE subscription_reset_grants
		SET tier_snapshot_resolved = FALSE
		WHERE id = $1
	`, typedGrantID)
	require.ErrorContains(t, err, "tier provenance is immutable")
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE subscription_reset_grants
		SET source_plan_id = $2
		WHERE id = $1
	`, frozenGrantID, int64(4343))
	require.ErrorContains(t, err, "tier provenance is immutable")
}

// insertLegacySubscriptionResetCardGrant models the reset-card INSERT emitted
// by binaries deployed before migration 248. It deliberately omits every
// snapshot column, leaving PostgreSQL to provide mixed-version compatibility.
func insertLegacySubscriptionResetCardGrant(
	ctx context.Context,
	subscriptionID, userID, groupID int64,
	expiresAt, now time.Time,
) (int64, error) {
	var grantID int64
	err := integrationDB.QueryRowContext(ctx, `
		INSERT INTO subscription_reset_grants (
			subscription_id, user_id, group_id, quantity, used_count,
			expires_at, issued_by, created_at, updated_at
		) VALUES ($1, $2, $3, 1, 0, $4, NULL, $5, $5)
		RETURNING id
	`, subscriptionID, userID, groupID, expiresAt, now).Scan(&grantID)
	return grantID, err
}
