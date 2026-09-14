package service

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestPaymentConfigService_ResetCardTierPoliciesAreExplicitSubscriptionPolicies(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}

	createGroup := func(name, subscriptionType string) int64 {
		candidate, err := client.Group.Create().
			SetName(name).
			SetPlatform(PlatformOpenAI).
			SetStatus(StatusActive).
			SetSubscriptionType(subscriptionType).
			Save(ctx)
		require.NoError(t, err)
		return candidate.ID
	}

	plusGroupID := createGroup("tier policy plus", SubscriptionTypeSubscription)
	fiveXGroupID := createGroup("tier policy 5x", SubscriptionTypeSubscription)
	standardGroupID := createGroup("tier policy standard", SubscriptionTypeStandard)

	plus, err := svc.UpsertResetCardTierPolicy(ctx, UpsertSubscriptionResetCardTierPolicyInput{
		GroupID: plusGroupID, FamilyKey: " GPT-5 ", TierRank: 1,
	})
	require.NoError(t, err)
	require.Equal(t, plusGroupID, plus.GroupID)
	require.Equal(t, "gpt-5", plus.FamilyKey)
	require.Equal(t, 1, plus.TierRank)
	require.False(t, plus.CreatedAt.IsZero())
	require.False(t, plus.UpdatedAt.IsZero())

	fiveX, err := svc.UpsertResetCardTierPolicy(ctx, UpsertSubscriptionResetCardTierPolicyInput{
		GroupID: fiveXGroupID, FamilyKey: "gpt-5", TierRank: 2,
	})
	require.NoError(t, err)
	require.Equal(t, "gpt-5", fiveX.FamilyKey)
	require.Equal(t, 2, fiveX.TierRank)

	policies, err := svc.ListResetCardTierPolicies(ctx)
	require.NoError(t, err)
	require.Len(t, policies, 2)
	require.Equal(t, []int{1, 2}, []int{policies[0].TierRank, policies[1].TierRank})

	byGroup, err := svc.ResetCardTierPoliciesByGroup(ctx, []int64{plusGroupID, standardGroupID})
	require.NoError(t, err)
	require.Equal(t, map[int64]SubscriptionResetCardTierPolicy{plusGroupID: *plus}, byGroup)

	_, err = svc.UpsertResetCardTierPolicy(ctx, UpsertSubscriptionResetCardTierPolicyInput{
		GroupID: standardGroupID, FamilyKey: "gpt-5", TierRank: 3,
	})
	require.ErrorIs(t, err, ErrResetCardTierPolicyInvalidGroup)

	_, err = svc.UpsertResetCardTierPolicy(ctx, UpsertSubscriptionResetCardTierPolicyInput{
		GroupID: plusGroupID, FamilyKey: "gpt-5", TierRank: 0,
	})
	require.ErrorIs(t, err, ErrResetCardTierPolicyInvalidRank)

	_, err = svc.UpsertResetCardTierPolicy(ctx, UpsertSubscriptionResetCardTierPolicyInput{
		GroupID: plusGroupID, FamilyKey: "five x", TierRank: 1,
	})
	require.ErrorIs(t, err, ErrResetCardTierPolicyInvalidFamily)

	_, err = svc.UpsertResetCardTierPolicy(ctx, UpsertSubscriptionResetCardTierPolicyInput{
		GroupID: fiveXGroupID, FamilyKey: "gpt-5", TierRank: 1,
	})
	require.ErrorIs(t, err, ErrResetCardTierPolicyConflict)
}

func TestResetCardTierQuoteRevisionRejectsStaleAndMissingTypedQuotes(t *testing.T) {
	policy := &SubscriptionResetCardTierPolicy{
		GroupID:   44,
		FamilyKey: "gpt",
		TierRank:  1,
		UpdatedAt: time.Date(2026, time.September, 15, 8, 30, 0, 0, time.UTC),
	}
	revision := resetCardTierPolicyRevision(policy)
	require.NotEmpty(t, revision)
	require.NoError(t, validateResetCardTierQuoteRevision(revision, policy))
	require.ErrorIs(t, validateResetCardTierQuoteRevision("", policy), ErrResetCardQuoteChanged)

	changed := *policy
	changed.FamilyKey = "gpt-5"
	changed.TierRank = 2
	changed.UpdatedAt = changed.UpdatedAt.Add(time.Nanosecond)
	require.NotEqual(t, revision, resetCardTierPolicyRevision(&changed))
	require.ErrorIs(t, validateResetCardTierQuoteRevision(revision, &changed), ErrResetCardQuoteChanged)

	// A pre-policy legacy quote stays exact-only, while an old typed quote
	// cannot become exact-only after a policy is removed or otherwise absent.
	require.NoError(t, validateResetCardTierQuoteRevision("", nil))
	require.ErrorIs(t, validateResetCardTierQuoteRevision(revision, nil), ErrResetCardQuoteChanged)
}

func TestPaymentConfigService_ResetCardTierPolicyFreezesAfterTierSnapshots(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}
	group, err := client.Group.Create().
		SetName("tier policy frozen").
		SetPlatform(PlatformOpenAI).
		SetStatus(StatusActive).
		SetSubscriptionType(SubscriptionTypeSubscription).
		Save(ctx)
	require.NoError(t, err)

	initial := UpsertSubscriptionResetCardTierPolicyInput{GroupID: group.ID, FamilyKey: "gpt", TierRank: 1}
	_, err = svc.UpsertResetCardTierPolicy(ctx, initial)
	require.NoError(t, err)

	_, err = client.ExecContext(ctx, `
		CREATE TABLE subscription_reset_grants (
			group_id INTEGER NOT NULL,
			card_family_key TEXT,
			source_tier_rank INTEGER,
			tier_snapshot_resolved BOOLEAN NOT NULL DEFAULT FALSE
		);
		CREATE TABLE subscription_reset_card_schedules (
			group_id INTEGER NOT NULL,
			card_family_key TEXT,
			source_tier_rank INTEGER
		)
	`)
	require.NoError(t, err)

	assertFrozen := func(label string) {
		t.Helper()
		_, updateErr := svc.UpsertResetCardTierPolicy(ctx, UpsertSubscriptionResetCardTierPolicyInput{
			GroupID: group.ID, FamilyKey: "gpt", TierRank: 2,
		})
		require.ErrorIs(t, updateErr, ErrResetCardTierPolicyFrozen, label)
		require.Equal(t, "RESET_CARD_TIER_POLICY_FROZEN", infraerrors.Reason(updateErr), label)
		// Replaying the frozen value itself remains idempotent.
		_, updateErr = svc.UpsertResetCardTierPolicy(ctx, initial)
		require.NoError(t, updateErr, label)
	}

	_, err = client.ExecContext(ctx, `INSERT INTO subscription_reset_grants (group_id, card_family_key, source_tier_rank, tier_snapshot_resolved) VALUES ($1, $2, $3, TRUE)`, group.ID, "gpt", 1)
	require.NoError(t, err)
	assertFrozen("issued grant")
	_, err = client.ExecContext(ctx, `DELETE FROM subscription_reset_grants`)
	require.NoError(t, err)

	_, err = client.ExecContext(ctx, `INSERT INTO subscription_reset_card_schedules (group_id, card_family_key, source_tier_rank) VALUES ($1, $2, $3)`, group.ID, "gpt", 1)
	require.NoError(t, err)
	assertFrozen("monthly schedule")
	_, err = client.ExecContext(ctx, `DELETE FROM subscription_reset_card_schedules`)
	require.NoError(t, err)

	user, err := client.User.Create().
		SetEmail("tier-policy@example.com").
		SetUsername("tier-policy").
		SetPasswordHash("hash").
		Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(1).
		SetPayAmount(1).
		SetRechargeCode("tier-policy").
		SetPaymentType("balance").
		SetPaymentTradeNo("tier-policy").
		SetSubscriptionGroupID(group.ID).
		SetExpiresAt(time.Now().UTC().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("test.example").
		SetProductSnapshot(map[string]any{"reset_card_tier": map[string]any{"family_key": "gpt", "tier_rank": 1}}).
		Save(ctx)
	require.NoError(t, err)
	assertFrozen("frozen payment-order snapshot")
}

func TestPaymentConfigService_ResetCardTierPolicyUpdateLocksRowAndChecksEvidenceInTransaction(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer func() { _ = client.Close() }()

	const groupID int64 = 23
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT .*"groups"\."id".*"groups"\."subscription_type".*FROM "groups".*WHERE .*"groups"\."id" = \$1.*"groups"\."deleted_at" IS NULL.*LIMIT 2`).
		WithArgs(groupID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "subscription_type"}).AddRow(groupID, SubscriptionTypeSubscription))
	mock.ExpectQuery(`(?s)SELECT group_id, family_key, tier_rank, created_at, updated_at.*FROM subscription_reset_card_tiers.*WHERE group_id = \$1.*FOR UPDATE`).
		WithArgs(groupID).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "family_key", "tier_rank", "created_at", "updated_at"}).AddRow(groupID, "gpt", 1, now, now))
	mock.ExpectQuery(`(?s)SELECT EXISTS\(SELECT 1 FROM subscription_reset_grants`).
		WithArgs(groupID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`(?s)SELECT EXISTS\(SELECT 1 FROM subscription_reset_card_schedules`).
		WithArgs(groupID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`(?s)SELECT .*FROM "payment_orders".*"payment_orders"\."subscription_group_id" = \$1`).
		WithArgs(groupID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`(?s)UPDATE subscription_reset_card_tiers.*SET family_key = \$1, tier_rank = \$2, updated_at = \$3.*WHERE group_id = \$4.*RETURNING`).
		WithArgs("gpt", 2, sqlmock.AnyArg(), groupID).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "family_key", "tier_rank", "created_at", "updated_at"}).AddRow(groupID, "gpt", 2, now, now))
	mock.ExpectCommit()

	policy, err := (&PaymentConfigService{entClient: client}).UpsertResetCardTierPolicy(context.Background(), UpsertSubscriptionResetCardTierPolicyInput{
		GroupID: groupID, FamilyKey: "gpt", TierRank: 2,
	})
	require.NoError(t, err)
	require.Equal(t, 2, policy.TierRank)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPaymentConfigService_ResetCardTierPolicyFirstWriteLocksParentBeforeEmptyTierRead(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer func() { _ = client.Close() }()

	const groupID int64 = 29
	now := time.Now().UTC()
	mock.ExpectBegin()
	// The parent group must be locked before the child query. A missing child
	// row has nothing to lock by itself, so this is the first-write race fence.
	mock.ExpectQuery(`(?s)SELECT .*"groups"\."id".*"groups"\."subscription_type".*FROM "groups".*WHERE .*"groups"\."id" = \$1.*"groups"\."deleted_at" IS NULL.*LIMIT 2.*FOR UPDATE`).
		WithArgs(groupID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "subscription_type"}).AddRow(groupID, SubscriptionTypeSubscription))
	mock.ExpectQuery(`(?s)SELECT group_id, family_key, tier_rank, created_at, updated_at.*FROM subscription_reset_card_tiers.*WHERE group_id = \$1.*FOR UPDATE`).
		WithArgs(groupID).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "family_key", "tier_rank", "created_at", "updated_at"}))
	mock.ExpectQuery(`(?s)INSERT INTO subscription_reset_card_tiers.*ON CONFLICT \(group_id\) DO NOTHING.*RETURNING group_id, family_key, tier_rank, created_at, updated_at`).
		WithArgs(groupID, "gpt", 1, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "family_key", "tier_rank", "created_at", "updated_at"}).AddRow(groupID, "gpt", 1, now, now))
	mock.ExpectCommit()

	policy, err := (&PaymentConfigService{entClient: client}).UpsertResetCardTierPolicy(context.Background(), UpsertSubscriptionResetCardTierPolicyInput{
		GroupID: groupID, FamilyKey: "gpt", TierRank: 1,
	})
	require.NoError(t, err)
	require.Equal(t, "gpt", policy.FamilyKey)
	require.Equal(t, 1, policy.TierRank)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResetCardTierSnapshotLocksParentBeforeReadingTierRow(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer func() { _ = client.Close() }()

	const groupID int64 = 31
	planID := int64(41)
	now := time.Now().UTC()
	mock.ExpectQuery(`(?s)SELECT id.*FROM groups.*WHERE id = ANY\(\$1\).*ORDER BY id ASC.*FOR SHARE`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(groupID))
	mock.ExpectQuery(`(?s)SELECT group_id, family_key, tier_rank, created_at, updated_at.*FROM subscription_reset_card_tiers.*WHERE group_id = \$1.*FOR SHARE`).
		WithArgs(groupID).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "family_key", "tier_rank", "created_at", "updated_at"}).AddRow(groupID, "gpt", 2, now, now))

	snapshot, err := resetCardTierSnapshotForGroup(context.Background(), client, groupID, &planID, true)
	require.NoError(t, err)
	require.Equal(t, &SubscriptionResetCardTierSnapshot{FamilyKey: "gpt", TierRank: 2, SourcePlanID: &planID}, snapshot)
	require.NoError(t, mock.ExpectationsWereMet())
}
