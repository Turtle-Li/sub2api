package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type subscriptionCacheInvalidationOutboxRepository struct {
	db *sql.DB
}

func NewSubscriptionCacheInvalidationOutboxRepository(db *sql.DB) service.SubscriptionCacheInvalidationOutboxRepository {
	return &subscriptionCacheInvalidationOutboxRepository{db: db}
}

func (r *subscriptionCacheInvalidationOutboxRepository) Claim(
	ctx context.Context,
	workerID string,
	limit int,
	lease time.Duration,
) ([]service.SubscriptionCacheInvalidationEvent, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("nil subscription cache invalidation outbox database")
	}
	if limit <= 0 {
		limit = 100
	}
	leaseSeconds := int64(lease / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 30
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH candidates AS (
			SELECT id
			FROM subscription_cache_invalidation_outbox
			WHERE available_at <= NOW()
			  AND (claimed_at IS NULL OR claimed_at < NOW() - ($3 * INTERVAL '1 second'))
			ORDER BY id ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE subscription_cache_invalidation_outbox AS o
		SET claimed_at = NOW(), claimed_by = $1
		FROM candidates AS c
		WHERE o.id = c.id
		RETURNING o.id, o.user_id, o.group_id, o.attempts, o.delivery_stage, o.created_at
	`, workerID, limit, leaseSeconds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	events := make([]service.SubscriptionCacheInvalidationEvent, 0, limit)
	for rows.Next() {
		var event service.SubscriptionCacheInvalidationEvent
		if err := rows.Scan(
			&event.ID,
			&event.UserID,
			&event.GroupID,
			&event.Attempts,
			&event.Stage,
			&event.CreatedAt,
		); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *subscriptionCacheInvalidationOutboxRepository) ScheduleSecondPass(
	ctx context.Context,
	id int64,
	workerID string,
	availableAt time.Time,
) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE subscription_cache_invalidation_outbox
		SET delivery_stage = 1,
			available_at = $3,
			last_error = NULL,
			claimed_at = NULL,
			claimed_by = NULL
		WHERE id = $1 AND claimed_by = $2 AND delivery_stage = 0
	`, id, workerID, availableAt)
	if err != nil {
		return err
	}
	return requireSubscriptionInvalidationClaim(result, id, "schedule second pass")
}

func (r *subscriptionCacheInvalidationOutboxRepository) DeleteClaimed(ctx context.Context, id int64, workerID string) error {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM subscription_cache_invalidation_outbox
		WHERE id = $1 AND claimed_by = $2
	`, id, workerID)
	if err != nil {
		return err
	}
	return requireSubscriptionInvalidationClaim(result, id, "delete")
}

func (r *subscriptionCacheInvalidationOutboxRepository) RetryClaimed(
	ctx context.Context,
	id int64,
	workerID string,
	availableAt time.Time,
	lastError string,
) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE subscription_cache_invalidation_outbox
		SET attempts = attempts + 1,
			available_at = $3,
			last_error = $4,
			claimed_at = NULL,
			claimed_by = NULL
		WHERE id = $1 AND claimed_by = $2
	`, id, workerID, availableAt, lastError)
	if err != nil {
		return err
	}
	return requireSubscriptionInvalidationClaim(result, id, "retry")
}

func requireSubscriptionInvalidationClaim(result sql.Result, id int64, action string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("subscription cache invalidation claim %d cannot %s", id, action)
	}
	return nil
}

func (r *subscriptionCacheInvalidationOutboxRepository) Stats(ctx context.Context) (service.AuthCacheInvalidationOutboxStats, error) {
	var (
		stats     service.AuthCacheInvalidationOutboxStats
		oldest    sql.NullTime
		lastError sql.NullString
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(created_at), COALESCE(MAX(attempts), 0),
			(SELECT last_error
			 FROM subscription_cache_invalidation_outbox
			 WHERE last_error IS NOT NULL
			 ORDER BY available_at DESC, id DESC
			 LIMIT 1)
		FROM subscription_cache_invalidation_outbox
	`).Scan(&stats.Pending, &oldest, &stats.MaxAttempts, &lastError)
	if err != nil {
		return stats, err
	}
	if oldest.Valid {
		value := oldest.Time
		stats.OldestCreatedAt = &value
	}
	if lastError.Valid {
		stats.LastError = lastError.String
	}
	return stats, nil
}
