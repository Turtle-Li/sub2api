package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/lib/pq"

	"entgo.io/ent/dialect"
)

const maxResetCardTierFamilyKeyLength = 64

var resetCardTierFamilyKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

var (
	ErrResetCardTierPolicyUnavailable   = infraerrors.ServiceUnavailable("RESET_CARD_TIER_POLICY_UNAVAILABLE", "reset card tier policy is unavailable")
	ErrResetCardTierPolicyInvalidGroup  = infraerrors.BadRequest("RESET_CARD_TIER_POLICY_GROUP_INVALID", "reset card tier policy requires a subscription group")
	ErrResetCardTierPolicyInvalidFamily = infraerrors.BadRequest("RESET_CARD_TIER_POLICY_FAMILY_INVALID", "reset card family_key must be a normalized lowercase stable key")
	ErrResetCardTierPolicyInvalidRank   = infraerrors.BadRequest("RESET_CARD_TIER_POLICY_RANK_INVALID", "reset card tier_rank must be a positive integer")
	ErrResetCardTierPolicyConflict      = infraerrors.Conflict("RESET_CARD_TIER_POLICY_CONFLICT", "another group already uses this reset card family and tier rank")
	ErrResetCardTierPolicyFrozen        = infraerrors.Conflict("RESET_CARD_TIER_POLICY_FROZEN", "reset card tier policy is frozen by issued or scheduled reset cards")
)

// SubscriptionResetCardTierPolicy maps one subscription group to one stable,
// comparable reset-card tier. It contains no purchase rules or other private
// catalogue policy, so the same read-only value can safely be projected to a
// customer plan or quote.
type SubscriptionResetCardTierPolicy struct {
	GroupID   int64     `json:"group_id"`
	FamilyKey string    `json:"family_key"`
	TierRank  int       `json:"tier_rank"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SubscriptionResetCardTierSnapshot is copied to a grant at issuance time.
// SourcePlanID is deliberately scalar and nullable: admin grants have a
// policy-derived tier but no purchased plan to reference.
type SubscriptionResetCardTierSnapshot struct {
	FamilyKey    string
	TierRank     int
	SourcePlanID *int64
}

// resetCardTierPolicyRevision is a quote binding, not a secret. It makes the
// exact policy observed by a buyer explicit so a policy rewrite between quote
// and checkout cannot silently change the card they purchase.
func resetCardTierPolicyRevision(policy *SubscriptionResetCardTierPolicy) string {
	if policy == nil {
		return ""
	}
	return fmt.Sprintf("v1:%d:%s:%d:%d", policy.GroupID, policy.FamilyKey, policy.TierRank, policy.UpdatedAt.UTC().UnixNano())
}

// validateResetCardTierQuoteRevision keeps a legacy no-policy quote
// exact-only, while requiring every quote issued under an explicit policy to
// repeat that policy revision at checkout. A typed quote cannot be silently
// downgraded if the policy disappears either.
func validateResetCardTierQuoteRevision(expected string, policy *SubscriptionResetCardTierPolicy) error {
	expected = strings.TrimSpace(expected)
	if policy == nil {
		if expected == "" {
			return nil
		}
		return ErrResetCardQuoteChanged
	}
	if expected == "" || expected != resetCardTierPolicyRevision(policy) {
		return ErrResetCardQuoteChanged
	}
	return nil
}

type UpsertSubscriptionResetCardTierPolicyInput struct {
	GroupID   int64  `json:"group_id"`
	FamilyKey string `json:"family_key"`
	TierRank  int    `json:"tier_rank"`
}

// ListResetCardTierPolicies returns the full administrative policy list.
func (s *PaymentConfigService) ListResetCardTierPolicies(ctx context.Context) ([]SubscriptionResetCardTierPolicy, error) {
	if s == nil || s.entClient == nil {
		return nil, ErrResetCardTierPolicyUnavailable
	}
	return listSubscriptionResetCardTierPolicies(ctx, s.entClient)
}

// ResetCardTierPoliciesByGroup is the read-only projection used by plan DTOs.
func (s *PaymentConfigService) ResetCardTierPoliciesByGroup(ctx context.Context, groupIDs []int64) (map[int64]SubscriptionResetCardTierPolicy, error) {
	if s == nil || s.entClient == nil {
		return nil, ErrResetCardTierPolicyUnavailable
	}
	return subscriptionResetCardTierPoliciesByGroup(ctx, s.entClient, groupIDs)
}

// UpsertResetCardTierPolicy validates that the configured group is a
// subscription group before persisting its explicit tier definition.
func (s *PaymentConfigService) UpsertResetCardTierPolicy(ctx context.Context, input UpsertSubscriptionResetCardTierPolicyInput) (*SubscriptionResetCardTierPolicy, error) {
	if s == nil || s.entClient == nil {
		return nil, ErrResetCardTierPolicyUnavailable
	}
	if input.GroupID <= 0 {
		return nil, ErrResetCardTierPolicyInvalidGroup
	}
	familyKey, err := normalizeResetCardTierFamilyKey(input.FamilyKey)
	if err != nil {
		return nil, err
	}
	if input.TierRank <= 0 {
		return nil, ErrResetCardTierPolicyInvalidRank
	}

	// Updating a policy is intentionally serialized with every checkout that
	// snapshots it.  Checkout reads an existing policy FOR SHARE; this FOR
	// UPDATE lock therefore ensures an administrator cannot check the frozen
	// evidence, then rewrite the policy while a paid order is still recording
	// the old snapshot.
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin reset card tier policy transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	groupQuery := client.Group.Query().
		Where(group.IDEQ(input.GroupID), group.DeletedAtIsNil()).
		Select(group.FieldID, group.FieldSubscriptionType)
	if paymentAuditDialect(client) == dialect.Postgres {
		// This parent row exists before the optional tier row. Taking it first
		// serializes a first policy insert with every tier snapshot or card grant,
		// including the otherwise-unlockable no-tier case.
		groupQuery.ForUpdate()
	}
	candidate, err := groupQuery.Only(txCtx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, ErrResetCardTierPolicyInvalidGroup
		}
		return nil, fmt.Errorf("load reset card tier group: %w", err)
	}
	if candidate.SubscriptionType != SubscriptionTypeSubscription {
		return nil, ErrResetCardTierPolicyInvalidGroup
	}

	current, err := loadSubscriptionResetCardTierPolicyForUpdate(txCtx, client, input.GroupID)
	if err != nil {
		return nil, err
	}
	if current == nil {
		// Do not use ON CONFLICT DO UPDATE here. Two first-time administrators
		// must not silently overwrite each other before either has observed the
		// row. The losing insert re-locks the row below and becomes a normal,
		// serialized update attempt.
		policy, inserted, insertErr := insertSubscriptionResetCardTierPolicyIfAbsent(txCtx, client, input.GroupID, familyKey, input.TierRank)
		if insertErr != nil {
			return nil, insertErr
		}
		if inserted {
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("commit reset card tier policy insert: %w", err)
			}
			return policy, nil
		}
		current, err = loadSubscriptionResetCardTierPolicyForUpdate(txCtx, client, input.GroupID)
		if err != nil {
			return nil, err
		}
		if current == nil {
			return nil, errors.New("reset card tier policy disappeared after insert conflict")
		}
	}
	if current != nil {
		if current.FamilyKey == familyKey && current.TierRank == input.TierRank {
			// A repeat administrative request must not turn a stable policy into
			// a conflict merely because cards have since been issued from it.
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("commit reset card tier policy read: %w", err)
			}
			return current, nil
		}
		frozen, err := resetCardTierPolicyHasFrozenSnapshot(txCtx, client, input.GroupID)
		if err != nil {
			return nil, fmt.Errorf("check reset card tier policy stability: %w", err)
		}
		if frozen {
			return nil, ErrResetCardTierPolicyFrozen
		}
	}

	now := time.Now().UTC()
	rows, err := client.QueryContext(txCtx, `
		UPDATE subscription_reset_card_tiers
		SET family_key = $1, tier_rank = $2, updated_at = $3
		WHERE group_id = $4
		RETURNING group_id, family_key, tier_rank, created_at, updated_at
	`, familyKey, input.TierRank, now, input.GroupID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrResetCardTierPolicyConflict
		}
		return nil, fmt.Errorf("upsert reset card tier policy: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("upsert reset card tier policy returned no row")
	}
	policy, err := scanSubscriptionResetCardTierPolicy(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit reset card tier policy update: %w", err)
	}
	return policy, nil
}

// insertSubscriptionResetCardTierPolicyIfAbsent is the first-write half of a
// policy upsert. It avoids an unconditional ON CONFLICT UPDATE so the caller
// can lock and revalidate a concurrently created row before changing it.
func insertSubscriptionResetCardTierPolicyIfAbsent(
	ctx context.Context,
	client *dbent.Client,
	groupID int64,
	familyKey string,
	tierRank int,
) (*SubscriptionResetCardTierPolicy, bool, error) {
	if client == nil {
		return nil, false, ErrResetCardTierPolicyUnavailable
	}
	now := time.Now().UTC()
	rows, err := client.QueryContext(ctx, `
		INSERT INTO subscription_reset_card_tiers (
			group_id, family_key, tier_rank, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (group_id) DO NOTHING
		RETURNING group_id, family_key, tier_rank, created_at, updated_at
	`, groupID, familyKey, tierRank, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, false, ErrResetCardTierPolicyConflict
		}
		return nil, false, fmt.Errorf("insert reset card tier policy: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	policy, err := scanSubscriptionResetCardTierPolicy(rows)
	if err != nil {
		return nil, false, err
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return policy, true, nil
}

// resetCardTierPolicyHasFrozenSnapshot prevents a mutable policy from
// changing the interpretation of existing typed cards. A group can safely be
// changed only before any grant, order snapshot, or monthly schedule has
// frozen the old family/rank pair.
func resetCardTierPolicyHasFrozenSnapshot(ctx context.Context, client *dbent.Client, groupID int64) (bool, error) {
	if client == nil || groupID <= 0 {
		return false, errors.New("reset card tier policy stability input is invalid")
	}
	for _, query := range []string{
		`SELECT EXISTS(SELECT 1 FROM subscription_reset_grants
			WHERE group_id = $1 AND card_family_key IS NOT NULL AND source_tier_rank IS NOT NULL)`,
		`SELECT EXISTS(SELECT 1 FROM subscription_reset_card_schedules
			WHERE group_id = $1 AND card_family_key IS NOT NULL AND source_tier_rank IS NOT NULL)`,
	} {
		rows, err := client.QueryContext(ctx, query, groupID)
		if err != nil {
			// Unit fixtures that exercise the tier table independently of later
			// migrations have no grant/schedule table. Production PostgreSQL has
			// every migration applied before this service starts, and any query
			// failure there remains fail-closed.
			if paymentAuditDialect(client) != "postgres" && strings.Contains(strings.ToLower(err.Error()), "no such table") {
				continue
			}
			return false, err
		}
		if !rows.Next() {
			err = rows.Err()
			_ = rows.Close()
			if err != nil {
				return false, err
			}
			return false, errors.New("reset card tier policy stability query returned no row")
		}
		var found bool
		if err := rows.Scan(&found); err != nil {
			_ = rows.Close()
			return false, err
		}
		if err := rows.Close(); err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}

	// Product snapshots are decoded in Go rather than with a database-specific
	// JSON expression. This covers both PostgreSQL and the SQLite fixtures and
	// treats malformed frozen data as a safety fence instead of silently
	// permitting a policy rewrite.
	orders, err := client.PaymentOrder.Query().
		Where(paymentorder.SubscriptionGroupIDEQ(groupID)).
		All(ctx)
	if err != nil {
		return false, err
	}
	for _, order := range orders {
		if order.ProductSnapshot == nil {
			continue
		}
		if _, found := order.ProductSnapshot["reset_card_tier"]; found {
			return true, nil
		}
	}
	return false, nil
}

func normalizeResetCardTierFamilyKey(raw string) (string, error) {
	familyKey := strings.ToLower(strings.TrimSpace(raw))
	if len(familyKey) == 0 || len(familyKey) > maxResetCardTierFamilyKeyLength || !resetCardTierFamilyKeyPattern.MatchString(familyKey) {
		return "", ErrResetCardTierPolicyInvalidFamily
	}
	return familyKey, nil
}

func listSubscriptionResetCardTierPolicies(ctx context.Context, client *dbent.Client) ([]SubscriptionResetCardTierPolicy, error) {
	if client == nil {
		return nil, ErrResetCardTierPolicyUnavailable
	}
	rows, err := client.QueryContext(ctx, `
		SELECT group_id, family_key, tier_rank, created_at, updated_at
		FROM subscription_reset_card_tiers
		ORDER BY family_key ASC, tier_rank ASC, group_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list reset card tier policies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	policies := make([]SubscriptionResetCardTierPolicy, 0)
	for rows.Next() {
		policy, err := scanSubscriptionResetCardTierPolicy(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, *policy)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reset card tier policies: %w", err)
	}
	return policies, nil
}

func subscriptionResetCardTierPoliciesByGroup(ctx context.Context, client *dbent.Client, groupIDs []int64) (map[int64]SubscriptionResetCardTierPolicy, error) {
	policies, err := listSubscriptionResetCardTierPolicies(ctx, client)
	if err != nil {
		return nil, err
	}
	wanted := make(map[int64]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID > 0 {
			wanted[groupID] = struct{}{}
		}
	}
	result := make(map[int64]SubscriptionResetCardTierPolicy, len(wanted))
	for _, policy := range policies {
		if _, ok := wanted[policy.GroupID]; ok {
			result[policy.GroupID] = policy
		}
	}
	return result, nil
}

// LockSubscriptionResetCardTierSnapshotGroups holds the stable parent group
// rows through a transaction that snapshots tier policy. Writers take the same
// rows FOR UPDATE before inspecting or inserting the optional child policy, so
// a first policy write cannot race an empty child-row read. PostgreSQL acquires
// the selected parent rows in ascending ID order; callers that snapshot several
// groups must use this helper rather than taking tier rows directly.
func LockSubscriptionResetCardTierSnapshotGroups(ctx context.Context, client *dbent.Client, groupIDs []int64) error {
	if client == nil || len(groupIDs) == 0 || paymentAuditDialect(client) != dialect.Postgres {
		return nil
	}
	return lockSubscriptionResetCardTierSnapshotGroupRows(ctx, client, `
		SELECT id
		FROM groups
		WHERE id = ANY($1)
		ORDER BY id ASC
		FOR SHARE
	`, pq.Array(groupIDs))
}

// LockSubscriptionResetCardTierSnapshotGroupsForSubscriptions follows the
// same parent-group lock protocol for a targeted administrative grant. The
// query intentionally locks only groups, not subscription rows: the grant
// INSERT remains responsible for deciding which active subscriptions receive a
// card, while policy first-writes and snapshots share one lock order.
func LockSubscriptionResetCardTierSnapshotGroupsForSubscriptions(ctx context.Context, client *dbent.Client, subscriptionIDs []int64) error {
	if client == nil || len(subscriptionIDs) == 0 || paymentAuditDialect(client) != dialect.Postgres {
		return nil
	}
	return lockSubscriptionResetCardTierSnapshotGroupRows(ctx, client, `
		SELECT g.id
		FROM groups AS g
		JOIN user_subscriptions AS us ON us.group_id = g.id
		WHERE us.id = ANY($1)
		ORDER BY g.id ASC
		FOR SHARE OF g
	`, pq.Array(subscriptionIDs))
}

func lockSubscriptionResetCardTierSnapshotGroupRows(ctx context.Context, client *dbent.Client, query string, args ...any) error {
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("lock reset card tier snapshot groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var groupID int64
		if err := rows.Scan(&groupID); err != nil {
			return fmt.Errorf("scan locked reset card tier snapshot group: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate locked reset card tier snapshot groups: %w", err)
	}
	return nil
}

// loadSubscriptionResetCardTierPolicy locks the parent group before an
// existing policy row when a purchase transaction needs to freeze it. If no
// row exists, the parent lock makes an overlapping first policy write wait; a
// caller can then either observe its complete policy or retain exact-only
// behavior from a transaction that was ordered before the write.
func loadSubscriptionResetCardTierPolicy(ctx context.Context, client *dbent.Client, groupID int64, lock bool) (*SubscriptionResetCardTierPolicy, error) {
	if client == nil || groupID <= 0 {
		return nil, nil
	}
	if lock {
		if err := LockSubscriptionResetCardTierSnapshotGroups(ctx, client, []int64{groupID}); err != nil {
			return nil, err
		}
	}
	query := `
		SELECT group_id, family_key, tier_rank, created_at, updated_at
		FROM subscription_reset_card_tiers
		WHERE group_id = $1
	`
	if lock && client.Driver().Dialect() == dialect.Postgres {
		query += " FOR SHARE"
	}
	rows, err := client.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("load reset card tier policy: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	policy, err := scanSubscriptionResetCardTierPolicy(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return policy, nil
}

// loadSubscriptionResetCardTierPolicyForUpdate is reserved for the policy
// administration path. It conflicts with checkout's FOR SHARE snapshot read,
// so policy stability evidence and the eventual rewrite share one transaction
// boundary.
func loadSubscriptionResetCardTierPolicyForUpdate(ctx context.Context, client *dbent.Client, groupID int64) (*SubscriptionResetCardTierPolicy, error) {
	if client == nil || groupID <= 0 {
		return nil, nil
	}
	query := `
		SELECT group_id, family_key, tier_rank, created_at, updated_at
		FROM subscription_reset_card_tiers
		WHERE group_id = $1
	`
	if client.Driver().Dialect() == dialect.Postgres {
		query += " FOR UPDATE"
	}
	rows, err := client.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("lock reset card tier policy: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	policy, err := scanSubscriptionResetCardTierPolicy(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return policy, nil
}

func resetCardTierSnapshotForGroup(ctx context.Context, client *dbent.Client, groupID int64, sourcePlanID *int64, lock bool) (*SubscriptionResetCardTierSnapshot, error) {
	policy, err := loadSubscriptionResetCardTierPolicy(ctx, client, groupID, lock)
	if err != nil || policy == nil {
		return nil, err
	}
	return resetCardTierSnapshotFromPolicy(policy, sourcePlanID), nil
}

func resetCardTierSnapshotFromPolicy(policy *SubscriptionResetCardTierPolicy, sourcePlanID *int64) *SubscriptionResetCardTierSnapshot {
	if policy == nil {
		return nil
	}
	planID := sourcePlanID
	if planID != nil {
		copyID := *planID
		planID = &copyID
	}
	return &SubscriptionResetCardTierSnapshot{
		FamilyKey:    policy.FamilyKey,
		TierRank:     policy.TierRank,
		SourcePlanID: planID,
	}
}

// resetCardTierSnapshotValues returns database-safe scalar values for a grant
// insert. A missing policy is deliberately stored as three NULLs so the grant
// retains legacy exact-subscription semantics.
func resetCardTierSnapshotValues(snapshot *SubscriptionResetCardTierSnapshot) (any, any, any) {
	if snapshot == nil {
		return nil, nil, nil
	}
	var sourcePlanID any
	if snapshot.SourcePlanID != nil {
		sourcePlanID = *snapshot.SourcePlanID
	}
	return snapshot.FamilyKey, snapshot.TierRank, sourcePlanID
}

// resetCardTierSnapshotForProductSnapshot serializes the immutable source
// evidence stored with an order. Only issued grants consume this shape; the
// read-only plan DTO exposes the policy separately.
func resetCardTierSnapshotForProductSnapshot(snapshot *SubscriptionResetCardTierSnapshot) map[string]any {
	if snapshot == nil || snapshot.SourcePlanID == nil {
		return nil
	}
	return map[string]any{
		"family_key":     snapshot.FamilyKey,
		"tier_rank":      snapshot.TierRank,
		"source_plan_id": *snapshot.SourcePlanID,
	}
}

// resetCardTierSnapshotFromProductSnapshot reads a frozen order snapshot. A
// missing value is deliberately accepted for orders created before tier policy
// existed; their resulting grants remain exact-only. A present value must be
// complete and match the order's immutable plan identity.
func resetCardTierSnapshotFromProductSnapshot(product map[string]any, expectedPlanID int64) (*SubscriptionResetCardTierSnapshot, error) {
	if product == nil {
		return nil, errors.New("reset card tier snapshot is missing product snapshot")
	}
	raw, found := product["reset_card_tier"]
	if !found || raw == nil {
		return nil, nil
	}
	productPlanID, productPlanOK := paymentSnapshotInt64(product["plan_id"])
	if !productPlanOK || productPlanID != expectedPlanID {
		return nil, errors.New("reset card tier snapshot does not match the product plan")
	}
	tier, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("reset card tier snapshot has an invalid shape")
	}
	familyRaw, familyOK := tier["family_key"].(string)
	familyKey, err := normalizeResetCardTierFamilyKey(familyRaw)
	if !familyOK || err != nil || familyKey != familyRaw {
		return nil, errors.New("reset card tier snapshot has an invalid family")
	}
	rank64, rankOK := paymentSnapshotInt64(tier["tier_rank"])
	if !rankOK || rank64 <= 0 || rank64 > int64(^uint(0)>>1) {
		return nil, errors.New("reset card tier snapshot has an invalid rank")
	}
	planID, planOK := paymentSnapshotInt64(tier["source_plan_id"])
	if !planOK || planID <= 0 || planID != expectedPlanID {
		return nil, errors.New("reset card tier snapshot has an invalid source plan")
	}
	return &SubscriptionResetCardTierSnapshot{
		FamilyKey:    familyKey,
		TierRank:     int(rank64),
		SourcePlanID: &planID,
	}, nil
}

func resetCardTierSnapshotsEqual(left, right *SubscriptionResetCardTierSnapshot) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.FamilyKey != right.FamilyKey || left.TierRank != right.TierRank {
		return false
	}
	if left.SourcePlanID == nil || right.SourcePlanID == nil {
		return left.SourcePlanID == right.SourcePlanID
	}
	return *left.SourcePlanID == *right.SourcePlanID
}

type resetCardTierPolicyScanner interface {
	Scan(dest ...any) error
}

func scanSubscriptionResetCardTierPolicy(scanner resetCardTierPolicyScanner) (*SubscriptionResetCardTierPolicy, error) {
	var policy SubscriptionResetCardTierPolicy
	if err := scanner.Scan(&policy.GroupID, &policy.FamilyKey, &policy.TierRank, &policy.CreatedAt, &policy.UpdatedAt); err != nil {
		return nil, fmt.Errorf("scan reset card tier policy: %w", err)
	}
	return &policy, nil
}
