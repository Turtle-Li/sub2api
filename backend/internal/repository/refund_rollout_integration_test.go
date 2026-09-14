//go:build integration

package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/Wei-Shaw/sub2api/internal/server"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestReviewedRefundRolloutPostgresCASAndReservationFence(t *testing.T) {
	ctx := context.Background()
	key := service.SettingPaymentReviewedRefundsEnabled
	_, err := integrationDB.ExecContext(ctx, "DELETE FROM settings WHERE key=$1", key)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM settings WHERE key=$1", key)
	})
	dir := t.TempDir()
	traffic := filepath.Join(dir, "traffic")
	background := filepath.Join(dir, "background")
	require.NoError(t, os.WriteFile(traffic, []byte("accepting"), 0600))
	require.NoError(t, os.WriteFile(background, []byte("active"), 0600))
	t.Setenv("SUB2API_TRAFFIC_STATE_FILE", traffic)
	t.Setenv(runtimegate.StateFileEnv, background)
	health := server.ProvideHealthService(integrationDB, integrationRedis, nil)
	changed, err := health.CompareAndSetReviewedRefunds(ctx, "", true)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = health.CompareAndSetReviewedRefunds(ctx, "", true)
	require.NoError(t, err)
	require.False(t, changed)
	require.NoError(t, os.WriteFile(traffic, []byte("draining"), 0600))
	// A reservation already holding the shared lock must finish before disable
	// can inspect financial quiescence or commit the false setting.
	reservation, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = reservation.Rollback() }()
	_, err = reservation.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared($1)", service.ReviewedRefundRolloutLockID)
	require.NoError(t, err)
	bounded, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	changed, err = health.CompareAndSetReviewedRefunds(bounded, "true", false)
	require.Error(t, err)
	require.False(t, changed)
	var value string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=$1", key).Scan(&value))
	require.Equal(t, "true", value)
	require.NoError(t, reservation.Commit())
	changed, err = health.CompareAndSetReviewedRefunds(ctx, "true", false)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=$1", key).Scan(&value))
	require.Equal(t, "false", value)
}
