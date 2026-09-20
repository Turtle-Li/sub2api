//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestUserConcurrencyAuthorizationFencePublishesCommittedMutationBeforeOldAuthAdmission
// executes the repository's real Postgres SET path inside an outer Ent
// transaction. It proves that an already-read auth max of 8 cannot keep
// admitting above a committed lower cap, and that rollback leaves no false
// lower projection. The same service methods are used by HTTP/WS and Live.
func TestUserConcurrencyAuthorizationFencePublishesCommittedMutationBeforeOldAuthAdmission(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	unique := uuid.NewString()
	user := mustCreateUser(t, client, &service.User{
		Email:       "p19-fence-" + unique + "@integration.test",
		Username:    "p19-fence-" + unique[:8],
		Concurrency: 8,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", user.ID)
	})

	cache := NewConcurrencyCache(testRedis(t), 15, 900).(*concurrencyCache)
	fence := service.NewConcurrencyService(cache)
	projection := func(queryCtx context.Context) (service.UserConcurrencyAuthorizationFenceProjection, bool, error) {
		rows, err := client.QueryContext(queryCtx, `SELECT users.concurrency, baseline.fence_revision
			FROM users
			JOIN payment_refund_concurrency_baselines baseline ON baseline.user_id = users.id
			WHERE users.id = $1 AND users.deleted_at IS NULL
			FOR SHARE OF users`, user.ID)
		if err != nil {
			return service.UserConcurrencyAuthorizationFenceProjection{}, false, err
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return service.UserConcurrencyAuthorizationFenceProjection{}, false, err
			}
			return service.UserConcurrencyAuthorizationFenceProjection{}, false, nil
		}
		var concurrency int
		var revision int64
		if err := rows.Scan(&concurrency, &revision); err != nil {
			return service.UserConcurrencyAuthorizationFenceProjection{}, false, err
		}
		if err := rows.Err(); err != nil {
			return service.UserConcurrencyAuthorizationFenceProjection{}, false, err
		}
		return service.UserConcurrencyAuthorizationFenceProjection{Ceiling: concurrency, Revision: revision}, true, nil
	}
	require.NoError(t, fence.ConfigureUserConcurrencyAuthorizationFence(
		func(queryCtx context.Context) (map[int64]service.UserConcurrencyAuthorizationFenceProjection, error) {
			value, tracked, err := projection(queryCtx)
			if err != nil {
				return nil, err
			}
			if !tracked {
				return map[int64]service.UserConcurrencyAuthorizationFenceProjection{}, nil
			}
			return map[int64]service.UserConcurrencyAuthorizationFenceProjection{user.ID: value}, nil
		},
		func(queryCtx context.Context, requestedUserID int64) (service.UserConcurrencyAuthorizationFenceProjection, bool, error) {
			if requestedUserID != user.ID {
				return service.UserConcurrencyAuthorizationFenceProjection{}, false, nil
			}
			return projection(queryCtx)
		},
	))

	repo := newUserRepositoryWithSQL(client, integrationDB)
	repo.concurrencyAuthorizationFence = fence

	commitSet := func(target int) {
		t.Helper()
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		txCtx := dbent.NewTxContext(ctx, tx)
		require.NoError(t, repo.Update(txCtx, &service.User{ID: user.ID, Concurrency: target}, service.UserUpdateFields{Concurrency: true}))

		// The marker is visible before outer commit. A direct Lua call carrying
		// the old auth max must fail closed rather than consume a fourth-old-cap
		// admission while this write is still invisible.
		_, err = cache.AcquireUserSlot(ctx, user.ID, 8, fmt.Sprintf("precommit-%d", target))
		require.ErrorIs(t, err, service.ErrUserConcurrencyAuthorizationFenceMutationInFlight)
		require.NoError(t, tx.Commit())
	}

	commitSet(3)
	regular := make([]*service.AcquireResult, 0, 3)
	for i := 0; i < 3; i++ {
		result, err := fence.AcquireUserSlot(ctx, user.ID, 8) // stale auth subject says eight
		require.NoError(t, err)
		require.True(t, result.Acquired)
		regular = append(regular, result)
	}
	blocked, err := fence.AcquireUserSlot(ctx, user.ID, 8)
	require.NoError(t, err)
	require.False(t, blocked.Acquired)
	for _, result := range regular {
		result.ReleaseFunc()
	}

	commitSet(1)
	live, err := fence.AcquireLiveLeaseWithAuthorizationFence(ctx, 1, 0, user.ID, 8, 5001, "p19-live-one", false)
	require.NoError(t, err)
	require.True(t, live)
	live, err = fence.AcquireLiveLeaseWithAuthorizationFence(ctx, 1, 0, user.ID, 8, 5002, "p19-live-two", false)
	require.NoError(t, err)
	require.False(t, live)
	require.NoError(t, cache.ReleaseLiveLease(ctx, 1, user.ID, 5001, "p19-live-one"))

	// An outer rollback cannot leave a provisional lowering marker or cap.
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	txCtx := dbent.NewTxContext(ctx, tx)
	require.NoError(t, repo.Update(txCtx, &service.User{ID: user.ID, Concurrency: 0}, service.UserUpdateFields{Concurrency: true}))
	_, err = cache.AcquireUserSlot(ctx, user.ID, 8, "rollback-precommit")
	require.ErrorIs(t, err, service.ErrUserConcurrencyAuthorizationFenceMutationInFlight)
	require.NoError(t, tx.Rollback())

	value, tracked, err := projection(ctx)
	require.NoError(t, err)
	require.True(t, tracked)
	require.Equal(t, 1, value.Ceiling, "rollback must preserve the prior committed cap")
	result, err := fence.AcquireUserSlot(ctx, user.ID, 8)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	result.ReleaseFunc()

	marker, err := cache.rdb.Exists(ctx, userAuthorizationFenceMutationKey(user.ID)).Result()
	require.NoError(t, err)
	require.Zero(t, marker)
}
