package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestReviewedRefundRolloutCAS(t *testing.T) {
	for _, tc := range []struct {
		name, expected    string
		enabled           bool
		pending, affected int64
	}{
		{"first enable", "", true, 0, 1}, {"enable false", "false", true, 0, 1},
		{"stale", "false", true, 0, 0}, {"pending enable", "false", true, 1, 0},
		{"disable", "true", false, 0, 1}, {"pending disable", "true", false, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			redisServer := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
			defer func() { _ = client.Close() }()
			dir := t.TempDir()
			traffic := filepath.Join(dir, "traffic")
			background := filepath.Join(dir, "background")
			state := "draining"
			if tc.enabled {
				state = "accepting"
			}
			require.NoError(t, os.WriteFile(traffic, []byte(state), 0600))
			backgroundState := "active"
			if tc.name == "disable" {
				backgroundState = "standby"
			}
			require.NoError(t, os.WriteFile(background, []byte(backgroundState), 0600))
			t.Setenv(runtimegate.StateFileEnv, background)
			health := newHealthService(db, client, "", traffic, time.Second)
			if tc.enabled {
				mock.ExpectPing()
			}
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(service.ReviewedRefundRolloutLockID).WillReturnResult(sqlmock.NewResult(0, 1))
			if tc.enabled {
				mock.ExpectPing()
			}
			mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tc.pending))
			if tc.pending == 0 {
				value := "false"
				if tc.enabled {
					value = "true"
				}
				if tc.expected == "" {
					mock.ExpectExec("INSERT INTO settings").WithArgs(service.SettingPaymentReviewedRefundsEnabled, value).WillReturnResult(sqlmock.NewResult(0, tc.affected))
				} else {
					mock.ExpectExec("UPDATE settings").WithArgs(service.SettingPaymentReviewedRefundsEnabled, tc.expected, value).WillReturnResult(sqlmock.NewResult(0, tc.affected))
				}
			}
			if tc.pending == 0 && tc.affected == 1 {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			changed, err := health.CompareAndSetReviewedRefunds(context.Background(), tc.expected, tc.enabled)
			if tc.pending != 0 {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.pending == 0 && tc.affected == 1, changed)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestReviewedRefundRolloutRejectsUnsafeRuntime(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dir := t.TempDir()
	traffic := filepath.Join(dir, "traffic")
	background := filepath.Join(dir, "background")
	require.NoError(t, os.WriteFile(traffic, []byte("accepting"), 0600))
	require.NoError(t, os.WriteFile(background, []byte("active"), 0600))
	t.Setenv(runtimegate.StateFileEnv, background)
	health := newHealthService(db, nil, "", traffic, time.Second)
	_, err = health.CompareAndSetReviewedRefunds(context.Background(), "true", false)
	require.Error(t, err)
	require.NoError(t, os.WriteFile(traffic, []byte("draining"), 0600))
	health.inFlightRequests.Store(1)
	_, err = health.CompareAndSetReviewedRefunds(context.Background(), "true", false)
	require.Error(t, err)
	health.inFlightRequests.Store(0)
	require.NoError(t, os.WriteFile(background, []byte("invalid"), 0600))
	_, err = health.CompareAndSetReviewedRefunds(context.Background(), "true", false)
	require.Error(t, err)
	_, err = health.CompareAndSetReviewedRefunds(context.Background(), "garbage", true)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
