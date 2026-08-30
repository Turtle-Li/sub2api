package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestHealthServiceAuthorizedUsesProtectedTokenFile(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "monitor-token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("test-monitor-token\n"), 0o600))
	service := newHealthService(nil, nil, tokenPath, "", time.Second)

	require.True(t, service.Authorized("test-monitor-token"))
	require.False(t, service.Authorized("wrong-token"))
	require.False(t, service.Authorized(""))

	require.NoError(t, os.Chmod(tokenPath, 0o640))
	require.False(t, service.Authorized("test-monitor-token"), "group-readable token must fail closed")
	require.NoError(t, os.Chmod(tokenPath, 0o400))
	require.False(t, service.Authorized("test-monitor-token"), "token mode must be exactly 0600")
}

func TestHealthServiceAuthorizedRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	targetPath := filepath.Join(directory, "target")
	linkPath := filepath.Join(directory, "token")
	require.NoError(t, os.WriteFile(targetPath, []byte("test-monitor-token"), 0o600))
	require.NoError(t, os.Symlink(targetPath, linkPath))
	service := newHealthService(nil, nil, linkPath, "", time.Second)

	require.False(t, service.Authorized("test-monitor-token"))
}

func TestHealthServiceReadyRequiresDependenciesAndTrafficState(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	statePath := filepath.Join(t.TempDir(), "traffic-state")
	require.NoError(t, os.WriteFile(statePath, []byte("accepting\n"), 0o600))
	service := newHealthService(db, redisClient, "", statePath, time.Second)

	mock.ExpectPing()
	require.True(t, service.Ready(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())

	require.NoError(t, os.WriteFile(statePath, []byte("draining\n"), 0o600))
	require.False(t, service.Ready(context.Background()))
	require.NoError(t, os.Remove(statePath))
	require.False(t, service.Ready(context.Background()), "configured missing state must fail closed")

	service.SetAccepting(false)
	require.False(t, service.Ready(context.Background()))
}

func TestHealthServiceReadyFailsWhenPostgresFails(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectPing().WillReturnError(errors.New("postgres unavailable"))
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	service := newHealthService(db, redisClient, "", "", time.Second)

	require.False(t, service.Ready(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthServiceReadyFailsWhenRedisFails(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectPing()
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 10 * time.Millisecond})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	service := newHealthService(db, redisClient, "", "", 50*time.Millisecond)

	require.False(t, service.Ready(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}
