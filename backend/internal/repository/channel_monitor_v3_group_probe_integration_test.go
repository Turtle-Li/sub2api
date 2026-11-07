//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3ProbeReportDoesNotInventCancelledAttempts(t *testing.T) {
	// Given a legacy completion, a withdrawn new claim, and a recovered check.
	ctx := context.Background()
	client := testEntClient(t)
	group := mustCreateGroup(t, client, &service.Group{Name: "probe-report-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group.ID) })
	start := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO channel_monitor_v3_group_probes
 (group_id,model,slot_start,finished_at,outcome,attempt_count,missing_usage_attempts,input_tokens,output_tokens,status_hidden)
 VALUES ($1,'report-model',$2,$2,'cancelled',NULL,0,0,0,TRUE),
 ($1,'report-model',$2::timestamptz+INTERVAL '10 minutes',$2,'cancelled',0,0,0,0,FALSE),
 ($1,'report-model',$2::timestamptz+INTERVAL '20 minutes',$2,'success',2,1,20,1,FALSE)`, group.ID, start)
	require.NoError(t, err)
	data, err := os.ReadFile("../../../deploy/model-status-probe-consumption.sql")
	require.NoError(t, err)
	script := string(data)
	query := script[strings.Index(script, "WITH bounds AS ("):strings.Index(script, "\nROLLBACK;")]
	query = strings.NewReplacer(":'trial_start'", "'"+start.Format(time.RFC3339)+"'", ":'trial_end'", "'"+start.Add(time.Hour).Format(time.RFC3339)+"'").Replace(query)
	// When the actual operator report runs against PostgreSQL.
	var raw []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, query).Scan(&raw))
	var report struct {
		Groups []struct {
			GroupID  int64 `json:"group_id"`
			Checks   int   `json:"checks"`
			Attempts int   `json:"attempts"`
			Missing  int   `json:"attempts_without_reported_usage"`
			Cleared  int   `json:"checks_cleared_from_status"`
			Input    int   `json:"reported_input_tokens"`
		} `json:"groups"`
	}
	require.NoError(t, json.Unmarshal(raw, &report))
	// Then pre-execution cancellation costs no invented attempt, and hidden usage remains.
	for _, row := range report.Groups {
		if row.GroupID == group.ID {
			require.Equal(t, 3, row.Checks)
			require.Equal(t, 3, row.Attempts)
			require.Equal(t, 2, row.Missing)
			require.Equal(t, 1, row.Cleared)
			require.Equal(t, 20, row.Input)
			return
		}
	}
	t.Fatal("report omitted the test group")
}

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
	progress := service.ChannelMonitorV3GroupProbeResult{ID: claimed, AttemptCount: 1, InputTokens: 8, OutputTokens: 1}
	require.NoError(t, repo.RecordGroupProbeAttempt(ctx, progress))
	require.NoError(t, repo.RecordGroupProbeAttempt(ctx, progress), "progress writes are idempotent")
	progress.AttemptCount, progress.InputTokens = 2, 20
	progress.MissingUsageAttempts = 1
	require.NoError(t, repo.RecordGroupProbeAttempt(ctx, progress))
	var attempts, input int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT attempt_count,input_tokens FROM channel_monitor_v3_group_probes WHERE id=$1 AND outcome='pending'`, claimed).Scan(&attempts, &input))
	require.EqualValues(t, 2, attempts)
	require.EqualValues(t, 20, input, "a crash before finalization must not lose earlier attempt consumption")
	progress.FinishedAt, progress.Outcome, progress.AccountID = &finished, "success", 123
	require.NoError(t, repo.FinishGroupProbe(ctx, progress))
	require.NoError(t, repo.FinishGroupProbe(ctx, service.ChannelMonitorV3GroupProbeResult{ID: claimed, FinishedAt: &finished, Outcome: "failure"}))
	rows, err := repo.GroupProbes(ctx, []int64{group.ID}, slot, slot.Add(10*time.Minute))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "success", rows[0].Outcome)
	require.Equal(t, int64(1), rows[0].OutputTokens)
	require.Equal(t, 2, rows[0].AttemptCount)
	require.Equal(t, 1, rows[0].MissingUsageAttempts)
	require.Error(t, repo.RecordGroupProbeAttempt(ctx, progress), "a finished claim cannot fund more attempts")
	_, err = integrationDB.ExecContext(ctx, `UPDATE channel_monitor_v3_group_probes SET status_hidden=TRUE WHERE id=$1`, claimed)
	require.NoError(t, err)
	rows, err = repo.GroupProbes(ctx, []int64{group.ID}, slot, slot.Add(10*time.Minute))
	require.NoError(t, err)
	require.Empty(t, rows, "cleared monitoring history is not displayed")
	id, err = repo.ClaimGroupProbe(ctx, target, slot)
	require.NoError(t, err)
	require.Zero(t, id, "clearing display history must not erase the paid claim")
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT input_tokens FROM channel_monitor_v3_group_probes WHERE id=$1`, claimed).Scan(&input))
	require.EqualValues(t, 20, input, "private consumption survives display cleanup")
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
