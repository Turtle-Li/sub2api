//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3SharedProbePoolsRecheckPublicBindings(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	a := mustCreateAccount(t, client, &service.Account{Name: "shared-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey})
	source := mustCreateGroup(t, client, &service.Group{Name: "source-" + uuid.NewString(), Platform: service.PlatformDeepseek})
	follower := mustCreateGroup(t, client, &service.Group{Name: "follower-" + uuid.NewString(), Platform: service.PlatformKimi})
	private := mustCreateGroup(t, client, &service.Group{Name: "private-" + uuid.NewString(), Platform: service.PlatformKimi, IsExclusive: true})
	for _, id := range []int64{source.ID, follower.ID, private.ID} {
		mustBindAccountToGroup(t, client, a.ID, id, 1)
	}
	repo := NewChannelMonitorV3Repository(integrationDB).(*channelMonitorV3Repository)
	ids := []int64{source.ID, follower.ID, private.ID}
	pools, err := repo.GroupProbePools(ctx, ids)
	require.NoError(t, err)
	require.Equal(t, []int64{a.ID}, pools[source.ID])
	require.Equal(t, pools[source.ID], pools[follower.ID])
	require.Empty(t, pools[private.ID])
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM account_groups WHERE group_id=$1`, follower.ID)
	require.NoError(t, err)
	pools, err = repo.GroupProbePools(ctx, ids)
	require.NoError(t, err)
	require.Empty(t, pools[follower.ID], "revoked bindings must not remain shareable")
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET deleted_at=NOW() WHERE id=$1`, a.ID)
	require.NoError(t, err)
	pools, err = repo.GroupProbePools(ctx, ids)
	require.NoError(t, err)
	require.Empty(t, pools[source.ID])
}
