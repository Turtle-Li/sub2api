//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3GroupProbeClaimsAndSeparateFacts(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	group := mustCreateGroup(t, client, &service.Group{Name: "probe-it-" + uuid.NewString(), Platform: service.PlatformOpenAI, RateMultiplier: 1})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group.ID)
		require.NoError(t, err)
	})
	base := NewChannelMonitorV3Repository(integrationDB)
	repo := base.(service.ChannelMonitorV3GroupProbeRepository)
	target := service.ChannelMonitorV3GroupProbeTarget{GroupID: group.ID, Model: "gpt-mini", Enabled: true}
	slot := time.Now().UTC().Truncate(service.ChannelMonitorV3ProbeInterval)
	var wg sync.WaitGroup
	ids := make(chan int64, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); id, err := repo.ClaimGroupProbe(ctx, target, slot); ids <- id; errs <- err }()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var claimed int64
	for id := range ids {
		if id > 0 {
			require.Zero(t, claimed, "concurrent generations must share one claim")
			claimed = id
		}
	}
	require.Positive(t, claimed)
	// Changing the selected model cannot spend again in the same group/slot.
	target.Model = "another-model"
	id, err := repo.ClaimGroupProbe(ctx, target, slot)
	require.NoError(t, err)
	require.Zero(t, id)
	finished := slot.Add(time.Minute)
	require.NoError(t, repo.FinishGroupProbe(ctx, service.ChannelMonitorV3GroupProbeResult{ID: claimed, FinishedAt: &finished, Outcome: "success", InputTokens: 8, OutputTokens: 1, AccountID: 123}))
	require.NoError(t, repo.FinishGroupProbe(ctx, service.ChannelMonitorV3GroupProbeResult{ID: claimed, FinishedAt: &finished, Outcome: "failure"}))
	rows, err := repo.GroupProbes(ctx, []int64{group.ID}, slot, slot.Add(10*time.Minute))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "success", rows[0].Outcome)
	require.Equal(t, int64(1), rows[0].OutputTokens)
	rows, err = repo.GroupProbes(ctx, []int64{group.ID + 999999}, slot, slot.Add(10*time.Minute))
	require.NoError(t, err)
	require.Empty(t, rows)
	facts, err := base.SlotFacts(ctx, []int64{group.ID}, slot, slot.Add(10*time.Minute), 10*time.Minute, false)
	require.NoError(t, err)
	require.Empty(t, facts.Metrics, "synthetic checks must never become user requests")
	require.NoError(t, repo.PruneGroupProbes(ctx, slot.Add(time.Minute)))
	rows, err = repo.GroupProbes(ctx, []int64{group.ID}, slot, slot.Add(10*time.Minute))
	require.NoError(t, err)
	require.Empty(t, rows)
}
