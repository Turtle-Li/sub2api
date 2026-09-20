package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

const userConcurrencyAuthorizationFenceMutationTimeout = 5 * time.Second

// UserConcurrencyAuthorizationFenceMutation fences a single user's new
// admissions from the instant a concurrency write starts until the committed
// durable projection has reached Redis. The marker is deliberately independent
// of API-key cache eviction: a request may already retain an old auth subject.
type UserConcurrencyAuthorizationFenceMutation struct {
	service *ConcurrencyService
	userID  int64
	token   string
}

// BeginUserConcurrencyAuthorizationFenceMutation starts a per-user strict
// marker after the caller already holds that user's PostgreSQL FOR UPDATE lock.
// That ordering lets a cache recovery obtain FOR SHARE and know whether the
// durable mutation has committed, rolled back, or is still in flight.
func (s *ConcurrencyService) BeginUserConcurrencyAuthorizationFenceMutation(
	ctx context.Context,
	userID int64,
) (*UserConcurrencyAuthorizationFenceMutation, error) {
	if s == nil || userID <= 0 || !s.userAuthorizationFenceEnabled.Load() {
		return nil, nil
	}
	cache, ok := s.cache.(UserConcurrencyAuthorizationFenceMutationCache)
	if !ok {
		return nil, errors.New("user concurrency authorization fence mutation cache is unsupported")
	}
	mutation := &UserConcurrencyAuthorizationFenceMutation{
		service: s,
		userID:  userID,
		token:   generateRequestID(),
	}
	// The Redis marker script derives the next projection revision atomically
	// when the caller does not have a durable revision in hand. Repository
	// writers hold users.id FOR UPDATE before reaching this point.
	if err := cache.BeginUserConcurrencyAuthorizationFenceMutation(ctx, userID, mutation.token, 0); err != nil {
		return nil, err
	}
	return mutation, nil
}

// Attach arranges cache publication only after the enclosing Ent transaction
// has committed. A rollback first reloads durable state, then removes its own
// marker, so normal rollback never leaves a false lower ceiling behind.
func (m *UserConcurrencyAuthorizationFenceMutation) Attach(tx *dbent.Tx) {
	if m == nil || tx == nil {
		return
	}
	tx.OnCommit(func(next dbent.Committer) dbent.Committer {
		return dbent.CommitFunc(func(commitCtx context.Context, committedTx *dbent.Tx) error {
			if err := next.Commit(commitCtx, committedTx); err != nil {
				return err
			}
			m.logCompletionFailure("commit", commitCtx, m.Committed)
			return nil
		})
	})
	tx.OnRollback(func(next dbent.Rollbacker) dbent.Rollbacker {
		return dbent.RollbackFunc(func(rollbackCtx context.Context, rolledBackTx *dbent.Tx) error {
			if err := next.Rollback(rollbackCtx, rolledBackTx); err != nil {
				return err
			}
			m.logCompletionFailure("rollback", rollbackCtx, m.RolledBack)
			return nil
		})
	})
}

func (m *UserConcurrencyAuthorizationFenceMutation) logCompletionFailure(
	phase string,
	ctx context.Context,
	complete func(context.Context) error,
) {
	if m == nil || complete == nil {
		return
	}
	if err := complete(ctx); err != nil {
		// The database outcome is already final. Keep the marker fail-closed and
		// surface the operational failure without returning an error that could
		// cause a non-idempotent caller to replay a committed DELTA.
		slog.Error("synchronize user concurrency authorization fence failed",
			"phase", phase,
			"user_id", m.userID,
			"error", err,
		)
	}
}

// Committed publishes the authoritative post-commit projection before lifting
// this marker. A stale asynchronous snapshot cannot widen the cap because the
// Redis projection is revisioned.
func (m *UserConcurrencyAuthorizationFenceMutation) Committed(ctx context.Context) error {
	return m.synchronizeAndFinish(ctx)
}

// RolledBack restores the durable pre-mutation projection before lifting this
// marker. It uses the same path as a successful completion because the database
// is already back to its authoritative state.
func (m *UserConcurrencyAuthorizationFenceMutation) RolledBack(ctx context.Context) error {
	return m.synchronizeAndFinish(ctx)
}

func (m *UserConcurrencyAuthorizationFenceMutation) synchronizeAndFinish(ctx context.Context) error {
	if m == nil || m.service == nil || m.userID <= 0 {
		return nil
	}
	cache, ok := m.service.cache.(UserConcurrencyAuthorizationFenceMutationCache)
	if !ok {
		return errors.New("user concurrency authorization fence mutation cache is unsupported")
	}
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	finishCtx, cancel := context.WithTimeout(base, userConcurrencyAuthorizationFenceMutationTimeout)
	defer cancel()
	// Publish from the durable FOR SHARE lookup, then finish only this
	// transaction's token. The lookup may overlap a newer writer installing a
	// replacement marker; token-aware Finish leaves that newer owner intact.
	if err := m.service.synchronizeUserConcurrencyAuthorizationFence(finishCtx, m.userID, false); err != nil {
		return err
	}
	_, err := cache.FinishUserConcurrencyAuthorizationFenceMutation(finishCtx, m.userID, m.token)
	return err
}

// BeginUserConcurrencyAuthorizationFenceMutationAfterUserLock installs the
// per-user Redis marker after the caller has acquired users.id FOR UPDATE in
// the transaction carried by ctx. It publishes the durable projection only
// after that transaction commits (and restores it after rollback).
//
// Payment fulfillment, refund lifecycle transitions, and first-bind grants
// use this narrow helper instead of duplicating cache timing. A nil result
// means the strict fence has not been configured for this process.
func BeginUserConcurrencyAuthorizationFenceMutationAfterUserLock(
	ctx context.Context,
	fence *ConcurrencyService,
	userID int64,
) (*UserConcurrencyAuthorizationFenceMutation, error) {
	if fence == nil || !fence.userAuthorizationFenceEnabled.Load() || userID <= 0 {
		return nil, nil
	}
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return nil, errors.New("user concurrency authorization fence mutation requires a transaction context")
	}
	mutation, err := fence.BeginUserConcurrencyAuthorizationFenceMutation(ctx, userID)
	if err != nil || mutation == nil {
		return mutation, err
	}
	mutation.Attach(tx)
	return mutation, nil
}

// BeginUserConcurrencyAuthorizationFenceMutationWithUserLock obtains the
// required users.id FOR UPDATE lock, then delegates to the transaction-bound
// marker helper. Callers that already hold the lock should use the AfterUserLock
// form to preserve their established lock order.
func BeginUserConcurrencyAuthorizationFenceMutationWithUserLock(
	ctx context.Context,
	client *dbent.Client,
	fence *ConcurrencyService,
	userID int64,
) (*UserConcurrencyAuthorizationFenceMutation, error) {
	if fence == nil || !fence.userAuthorizationFenceEnabled.Load() || userID <= 0 {
		return nil, nil
	}
	if client == nil {
		return nil, errors.New("user concurrency authorization fence client is unavailable")
	}
	rows, err := client.QueryContext(ctx, `SELECT id FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("user concurrency authorization fence user is unavailable")
	}
	var lockedUserID int64
	if err := rows.Scan(&lockedUserID); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if lockedUserID != userID {
		return nil, errors.New("user concurrency authorization fence lock mismatch")
	}
	return BeginUserConcurrencyAuthorizationFenceMutationAfterUserLock(ctx, fence, userID)
}
