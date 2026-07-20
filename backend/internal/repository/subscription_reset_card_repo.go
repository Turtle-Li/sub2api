package repository

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type subscriptionResetCardRepository struct {
	client *dbent.Client
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

	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, `
		INSERT INTO subscription_reset_grants (
			subscription_id, user_id, group_id, quantity, used_count,
			expires_at, issued_by, created_at, updated_at
		)
		SELECT
			us.id, us.user_id, us.group_id, $2, 0,
			$3, NULLIF($4, 0), $5, $5
		FROM user_subscriptions us
		JOIN groups g ON g.id = us.group_id AND g.deleted_at IS NULL
		WHERE us.group_id = ANY($1)
			AND us.deleted_at IS NULL
			AND us.status = 'active'
			AND us.expires_at > $5
			AND g.status = 'active'
			AND g.subscription_type = 'subscription'
		RETURNING group_id
	`, pq.Array(groupIDs), quantity, expiresAt, issuedBy, now)
	if err != nil {
		return nil, fmt.Errorf("grant subscription reset cards: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var groupID int64
		if err := rows.Scan(&groupID); err != nil {
			return nil, fmt.Errorf("scan granted reset card group: %w", err)
		}
		result[groupID]++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate granted reset cards: %w", err)
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

	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, `
		INSERT INTO subscription_reset_grants (
			subscription_id, user_id, group_id, quantity, used_count,
			expires_at, issued_by, created_at, updated_at
		)
		SELECT
			us.id, us.user_id, us.group_id, $2, 0,
			$3, NULLIF($4, 0), $5, $5
		FROM user_subscriptions us
		JOIN groups g ON g.id = us.group_id AND g.deleted_at IS NULL
		WHERE us.id = ANY($1)
			AND us.deleted_at IS NULL
			AND us.status = 'active'
			AND us.expires_at > $5
			AND g.status = 'active'
			AND g.subscription_type = 'subscription'
		RETURNING subscription_id
	`, pq.Array(subscriptionIDs), quantity, expiresAt, issuedBy, now)
	if err != nil {
		return nil, fmt.Errorf("grant reset cards to subscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var subscriptionID int64
		if err := rows.Scan(&subscriptionID); err != nil {
			return nil, fmt.Errorf("scan granted reset card subscription: %w", err)
		}
		result[subscriptionID]++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate granted reset card subscriptions: %w", err)
	}
	return result, nil
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
	rows, err := client.QueryContext(ctx, `
		SELECT rg.subscription_id, rg.expires_at, SUM(rg.quantity - rg.used_count)::BIGINT AS remaining
		FROM subscription_reset_grants rg
		JOIN user_subscriptions us ON us.id = rg.subscription_id
		WHERE rg.subscription_id = ANY($1)
			AND rg.expires_at > $2
			AND rg.used_count < rg.quantity
			AND us.deleted_at IS NULL
			AND us.status = 'active'
			AND us.expires_at > $2
		GROUP BY rg.subscription_id, rg.expires_at
		ORDER BY rg.subscription_id ASC, rg.expires_at ASC
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
		rows, err := client.QueryContext(txCtx, `
			SELECT group_id, status, expires_at
			FROM user_subscriptions
			WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
			FOR UPDATE
		`, subscriptionID, userID)
		if err != nil {
			return fmt.Errorf("lock subscription for reset card use: %w", err)
		}
		var (
			status    string
			expiresAt time.Time
			found     bool
		)
		if rows.Next() {
			found = true
			if err := rows.Scan(&groupID, &status, &expiresAt); err != nil {
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

		grantRows, err := client.QueryContext(txCtx, `
			SELECT id
			FROM subscription_reset_grants
			WHERE subscription_id = $1
				AND user_id = $2
				AND expires_at > $3
				AND used_count < quantity
			ORDER BY expires_at ASC, id ASC
			LIMIT 1
			FOR UPDATE
		`, subscriptionID, userID, now)
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
