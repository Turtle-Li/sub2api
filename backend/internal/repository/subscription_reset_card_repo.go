package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type subscriptionResetCardRepository struct {
	client *dbent.Client
}

// resetCardTierEligibilityPredicateSQL is intentionally shared by listing and
// consumption. NULL family/rank denotes an exact-subscription grant even when
// source_plan_id is retained as historical provenance; typed grants follow
// only the explicit group-tier policy and never consult a source
// subscription's current status.
const resetCardTierEligibilityPredicateSQL = `
			AND (
				(
					rg.card_family_key IS NULL
					AND rg.source_tier_rank IS NULL
					AND rg.subscription_id = target.subscription_id
				)
				OR (
					rg.card_family_key IS NOT NULL
					AND rg.source_tier_rank IS NOT NULL
					AND target.family_key IS NOT NULL
					AND target.tier_rank IS NOT NULL
					AND rg.card_family_key = target.family_key
					AND rg.source_tier_rank >= target.tier_rank
				)
		)
`

// P19 sources stay append-only. A card row remains for audit after its source
// is reserved or revoked, but it must not appear available to either summary
// or selection queries. The database trigger is the final guard for older
// writers; this predicate prevents a held/revoked card from shaping normal UI
// availability or a tier-insufficient result.
func resetCardRefundBenefitAvailabilityPredicate(client *dbent.Client) string {
	if client == nil || client.Driver().Dialect() != dialect.Postgres {
		return ""
	}
	return `
			AND NOT EXISTS (
				SELECT 1
				FROM payment_refund_benefit_reset_card_grants benefit_link
				JOIN payment_refund_benefit_sources benefit_source ON benefit_source.id = benefit_link.source_id
				WHERE benefit_link.reset_card_grant_id = rg.id
					AND benefit_source.state <> 'ACTIVE'
			)`
}

func NewSubscriptionResetCardRepository(client *dbent.Client) service.SubscriptionResetCardRepository {
	return &subscriptionResetCardRepository{client: client}
}

func (r *subscriptionResetCardRepository) GrantToGroups(
	ctx context.Context,
	groupIDs []int64,
	quantity int,
	expiresAt time.Time,
	issuedBy int64,
	now time.Time,
) (map[int64]int64, error) {
	result := make(map[int64]int64, len(groupIDs))
	if len(groupIDs) == 0 {
		return result, nil
	}

	err := r.withTx(ctx, func(txCtx context.Context, client *dbent.Client) error {
		// Lock the stable parent group before the optional tier child. The
		// policy service takes the same parent FOR UPDATE before a first write;
		// the existing tier FOR SHARE below preserves rolling-binary safety.
		if err := lockResetCardTierPoliciesForGroupGrant(txCtx, client, groupIDs); err != nil {
			return err
		}
		rows, err := client.QueryContext(txCtx, `
			INSERT INTO subscription_reset_grants (
				subscription_id, user_id, group_id, quantity, used_count,
				expires_at, issued_by, card_family_key, source_tier_rank,
				source_plan_id, tier_snapshot_resolved, created_at, updated_at
			)
			SELECT
				us.id, us.user_id, us.group_id, $2, 0,
				$3, NULLIF($4, 0), tier.family_key, tier.tier_rank, NULL, TRUE, $5, $5
			FROM user_subscriptions us
			JOIN groups g ON g.id = us.group_id AND g.deleted_at IS NULL
			LEFT JOIN subscription_reset_card_tiers tier ON tier.group_id = us.group_id
			WHERE us.group_id = ANY($1)
				AND us.deleted_at IS NULL
				AND us.status = 'active'
				AND us.expires_at > $5
				AND g.status = 'active'
				AND g.subscription_type = 'subscription'
			RETURNING group_id
		`, pq.Array(groupIDs), quantity, expiresAt, issuedBy, now)
		if err != nil {
			return fmt.Errorf("grant subscription reset cards: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var groupID int64
			if err := rows.Scan(&groupID); err != nil {
				return fmt.Errorf("scan granted reset card group: %w", err)
			}
			result[groupID]++
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate granted reset cards: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *subscriptionResetCardRepository) GrantToSubscriptions(
	ctx context.Context,
	subscriptionIDs []int64,
	quantity int,
	expiresAt time.Time,
	issuedBy int64,
	now time.Time,
) (map[int64]int64, error) {
	result := make(map[int64]int64, len(subscriptionIDs))
	if len(subscriptionIDs) == 0 {
		return result, nil
	}

	err := r.withTx(ctx, func(txCtx context.Context, client *dbent.Client) error {
		if err := lockResetCardTierPoliciesForSubscriptionGrant(txCtx, client, subscriptionIDs); err != nil {
			return err
		}
		rows, err := client.QueryContext(txCtx, `
			INSERT INTO subscription_reset_grants (
				subscription_id, user_id, group_id, quantity, used_count,
				expires_at, issued_by, card_family_key, source_tier_rank,
				source_plan_id, tier_snapshot_resolved, created_at, updated_at
			)
			SELECT
				us.id, us.user_id, us.group_id, $2, 0,
				$3, NULLIF($4, 0), tier.family_key, tier.tier_rank, NULL, TRUE, $5, $5
			FROM user_subscriptions us
			JOIN groups g ON g.id = us.group_id AND g.deleted_at IS NULL
			LEFT JOIN subscription_reset_card_tiers tier ON tier.group_id = us.group_id
			WHERE us.id = ANY($1)
				AND us.deleted_at IS NULL
				AND us.status = 'active'
				AND us.expires_at > $5
				AND g.status = 'active'
				AND g.subscription_type = 'subscription'
			RETURNING subscription_id
		`, pq.Array(subscriptionIDs), quantity, expiresAt, issuedBy, now)
		if err != nil {
			return fmt.Errorf("grant reset cards to subscriptions: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var subscriptionID int64
			if err := rows.Scan(&subscriptionID); err != nil {
				return fmt.Errorf("scan granted reset card subscription: %w", err)
			}
			result[subscriptionID]++
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate granted reset card subscriptions: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// The parent group lock covers an absent policy row, while the child lock keeps
// rolling deployments safe when an older policy writer only locks the tier
// row. Both locks remain held through the grant INSERT.
func lockResetCardTierPoliciesForGroupGrant(ctx context.Context, client *dbent.Client, groupIDs []int64) error {
	if client == nil || len(groupIDs) == 0 {
		return nil
	}
	if err := service.LockSubscriptionResetCardTierSnapshotGroups(ctx, client, groupIDs); err != nil {
		return err
	}
	if client.Driver().Dialect() != dialect.Postgres {
		return nil
	}
	rows, err := client.QueryContext(ctx, `
		SELECT group_id
		FROM subscription_reset_card_tiers
		WHERE group_id = ANY($1)
		FOR SHARE
	`, pq.Array(groupIDs))
	if err != nil {
		return fmt.Errorf("lock reset card tier policies for group grant: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var groupID int64
		if err := rows.Scan(&groupID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func lockResetCardTierPoliciesForSubscriptionGrant(ctx context.Context, client *dbent.Client, subscriptionIDs []int64) error {
	if client == nil || len(subscriptionIDs) == 0 {
		return nil
	}
	if err := service.LockSubscriptionResetCardTierSnapshotGroupsForSubscriptions(ctx, client, subscriptionIDs); err != nil {
		return err
	}
	if client.Driver().Dialect() != dialect.Postgres {
		return nil
	}
	rows, err := client.QueryContext(ctx, `
		SELECT tier.group_id
		FROM user_subscriptions us
		JOIN subscription_reset_card_tiers tier ON tier.group_id = us.group_id
		WHERE us.id = ANY($1)
		FOR SHARE OF tier
	`, pq.Array(subscriptionIDs))
	if err != nil {
		return fmt.Errorf("lock reset card tier policies for subscription grant: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var groupID int64
		if err := rows.Scan(&groupID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (r *subscriptionResetCardRepository) ListAvailable(
	ctx context.Context,
	subscriptionIDs []int64,
	now time.Time,
) (map[int64]service.SubscriptionResetCardSummary, error) {
	result := make(map[int64]service.SubscriptionResetCardSummary, len(subscriptionIDs))
	if len(subscriptionIDs) == 0 {
		return result, nil
	}

	client := clientFromContext(ctx, r.client)
	benefitAvailability := resetCardRefundBenefitAvailabilityPredicate(client)
	rows, err := client.QueryContext(ctx, `
		WITH target AS (
			SELECT us.id AS subscription_id, us.user_id, tier.family_key, tier.tier_rank
			FROM user_subscriptions us
			LEFT JOIN subscription_reset_card_tiers tier ON tier.group_id = us.group_id
			WHERE us.id = ANY($1)
				AND us.deleted_at IS NULL
				AND us.status = 'active'
				AND us.expires_at > $2
		)
		SELECT target.subscription_id, rg.expires_at, SUM(rg.quantity - rg.used_count)::BIGINT AS remaining
		FROM target
		JOIN subscription_reset_grants rg ON rg.user_id = target.user_id
		WHERE rg.expires_at > $2
			AND rg.used_count < rg.quantity
`+benefitAvailability+`
`+resetCardTierEligibilityPredicateSQL+`
		GROUP BY target.subscription_id, rg.expires_at
		ORDER BY target.subscription_id ASC, rg.expires_at ASC
	`, pq.Array(subscriptionIDs), now)
	if err != nil {
		return nil, fmt.Errorf("list available subscription reset cards: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			subscriptionID int64
			expiresAt      time.Time
			remaining      int64
		)
		if err := rows.Scan(&subscriptionID, &expiresAt, &remaining); err != nil {
			return nil, fmt.Errorf("scan subscription reset card summary: %w", err)
		}
		summary := result[subscriptionID]
		summary.AvailableCount += int(remaining)
		summary.Batches = append(summary.Batches, service.SubscriptionResetCardBatch{
			Remaining: int(remaining),
			ExpiresAt: expiresAt,
		})
		result[subscriptionID] = summary
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscription reset card summaries: %w", err)
	}
	return result, nil
}

func (r *subscriptionResetCardRepository) ConsumeAndReset(
	ctx context.Context,
	userID, subscriptionID int64,
	now, windowStart time.Time,
) (int64, error) {
	var groupID int64
	err := r.withTx(ctx, func(txCtx context.Context, client *dbent.Client) error {
		benefitAvailability := resetCardRefundBenefitAvailabilityPredicate(client)
		rows, err := client.QueryContext(txCtx, `
			SELECT us.group_id, us.status, us.expires_at, tier.family_key, tier.tier_rank
			FROM user_subscriptions us
			LEFT JOIN subscription_reset_card_tiers tier ON tier.group_id = us.group_id
			WHERE us.id = $1 AND us.user_id = $2 AND us.deleted_at IS NULL
			FOR UPDATE OF us
		`, subscriptionID, userID)
		if err != nil {
			return fmt.Errorf("lock subscription for reset card use: %w", err)
		}
		var (
			status         string
			expiresAt      time.Time
			targetFamily   sql.NullString
			targetTierRank sql.NullInt64
			found          bool
		)
		if rows.Next() {
			found = true
			if err := rows.Scan(&groupID, &status, &expiresAt, &targetFamily, &targetTierRank); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan subscription for reset card use: %w", err)
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if !found {
			return service.ErrSubscriptionNotFound
		}
		if !expiresAt.After(now) || status == service.SubscriptionStatusExpired {
			return service.ErrSubscriptionExpired
		}
		if status != service.SubscriptionStatusActive {
			return service.ErrSubscriptionSuspended
		}

		var (
			familyValue any
			rankValue   any
		)
		if targetFamily.Valid {
			familyValue = targetFamily.String
		}
		if targetTierRank.Valid {
			rankValue = targetTierRank.Int64
		}
		grantRows, err := client.QueryContext(txCtx, `
			WITH target AS (
				SELECT $1::BIGINT AS subscription_id, $2::BIGINT AS user_id,
					$3::TEXT AS family_key, $4::INTEGER AS tier_rank
			)
			SELECT rg.id
			FROM subscription_reset_grants rg
			CROSS JOIN target
			WHERE rg.user_id = target.user_id
				AND rg.expires_at > $5
				AND rg.used_count < rg.quantity
`+benefitAvailability+`
`+resetCardTierEligibilityPredicateSQL+`
			ORDER BY rg.expires_at ASC, rg.source_tier_rank ASC NULLS FIRST, rg.id ASC
			LIMIT 1
			FOR UPDATE
		`, subscriptionID, userID, familyValue, rankValue, now)
		if err != nil {
			return fmt.Errorf("lock subscription reset card: %w", err)
		}
		var grantID int64
		if grantRows.Next() {
			if err := grantRows.Scan(&grantID); err != nil {
				_ = grantRows.Close()
				return fmt.Errorf("scan subscription reset card: %w", err)
			}
		}
		if err := grantRows.Close(); err != nil {
			return err
		}
		if err := grantRows.Err(); err != nil {
			return err
		}
		if grantID == 0 {
			if targetFamily.Valid && targetTierRank.Valid {
				lowerRows, lowerErr := client.QueryContext(txCtx, `
					WITH target AS (
						SELECT $1::BIGINT AS user_id, $2::TEXT AS family_key, $3::INTEGER AS tier_rank
					)
					SELECT EXISTS (
						SELECT 1
						FROM subscription_reset_grants rg
						CROSS JOIN target
						WHERE rg.user_id = target.user_id
							AND rg.expires_at > $4
							AND rg.used_count < rg.quantity
						`+benefitAvailability+`
							AND rg.card_family_key = target.family_key
							AND rg.source_tier_rank IS NOT NULL
							AND rg.source_tier_rank < target.tier_rank
					)
				`, userID, targetFamily.String, targetTierRank.Int64, now)
				if lowerErr != nil {
					return fmt.Errorf("check insufficient reset card tier: %w", lowerErr)
				}
				var hasInsufficient bool
				if lowerRows.Next() {
					if err := lowerRows.Scan(&hasInsufficient); err != nil {
						_ = lowerRows.Close()
						return fmt.Errorf("scan insufficient reset card tier: %w", err)
					}
				}
				if err := lowerRows.Err(); err != nil {
					_ = lowerRows.Close()
					return err
				}
				if err := lowerRows.Close(); err != nil {
					return err
				}
				if hasInsufficient {
					return service.ErrResetCardTierInsufficient
				}
			}
			return service.ErrResetCardUnavailable
		}

		res, err := client.ExecContext(txCtx, `
			UPDATE subscription_reset_grants
			SET used_count = used_count + 1, updated_at = $2
			WHERE id = $1 AND used_count < quantity AND expires_at > $2
		`, grantID, now)
		if err != nil {
			return fmt.Errorf("consume subscription reset card: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("read consumed reset card count: %w", err)
		}
		if affected != 1 {
			return service.ErrResetCardUnavailable
		}

		res, err = client.ExecContext(txCtx, `
			UPDATE user_subscriptions
			SET daily_usage_usd = 0,
				weekly_usage_usd = 0,
				monthly_usage_usd = 0,
				daily_window_start = $2,
				weekly_window_start = $2,
				monthly_window_start = $2,
				updated_at = $3
			WHERE id = $1 AND user_id = $4 AND deleted_at IS NULL
		`, subscriptionID, windowStart, now, userID)
		if err != nil {
			return fmt.Errorf("reset subscription usage with reset card: %w", err)
		}
		affected, err = res.RowsAffected()
		if err != nil {
			return fmt.Errorf("read reset subscription count: %w", err)
		}
		if affected != 1 {
			return service.ErrSubscriptionNotFound
		}
		return nil
	})
	return groupID, err
}

func (r *subscriptionResetCardRepository) withTx(
	ctx context.Context,
	fn func(context.Context, *dbent.Client) error,
) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(ctx, tx.Client())
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin subscription reset card transaction: %w", err)
	}
	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx, tx.Client()); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit subscription reset card transaction: %w", err)
	}
	return nil
}
