package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestValidateProxyPoolAssignment(t *testing.T) {
	now := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	poolID := int64(7)
	activeUntil := now.Add(time.Hour)
	base := service.Proxy{
		ID:        11,
		Protocol:  "http",
		Host:      "127.0.0.1",
		Port:      8080,
		Username:  "user",
		Password:  "pass",
		Status:    service.StatusActive,
		ExpiresAt: &activeUntil,
		PoolID:    &poolID,
	}

	tests := []struct {
		name   string
		mutate func(*service.Proxy)
	}{
		{name: "proxy moved to another pool", mutate: func(proxy *service.Proxy) { other := int64(8); proxy.PoolID = &other }},
		{name: "pool cleared", mutate: func(proxy *service.Proxy) { proxy.PoolID = nil }},
		{name: "proxy disabled", mutate: func(proxy *service.Proxy) { proxy.Status = service.StatusDisabled }},
		{name: "proxy expired", mutate: func(proxy *service.Proxy) { expired := now; proxy.ExpiresAt = &expired }},
		{name: "protocol changed", mutate: func(proxy *service.Proxy) { proxy.Protocol = "socks5" }},
		{name: "host changed", mutate: func(proxy *service.Proxy) { proxy.Host = "127.0.0.2" }},
		{name: "port changed", mutate: func(proxy *service.Proxy) { proxy.Port++ }},
		{name: "username changed", mutate: func(proxy *service.Proxy) { proxy.Username = "other" }},
		{name: "password changed", mutate: func(proxy *service.Proxy) { proxy.Password = "other" }},
		{name: "identity changed", mutate: func(proxy *service.Proxy) { proxy.ID++ }},
	}

	require.NoError(t, validateProxyPoolAssignment(&base, &base, poolID, now))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := base
			tt.mutate(&current)
			err := validateProxyPoolAssignment(&current, &base, poolID, now)
			require.ErrorIs(t, err, service.ErrProxyPoolBindingChanged)
		})
	}
}

func TestLockProxyPoolAssignmentLocksAndLoadsSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	expiresAt := time.Date(2026, time.September, 27, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT id, protocol, host, port, COALESCE(username, ''), COALESCE(password, ''), status,") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(11)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "protocol", "host", "port", "username", "password", "status", "expires_at", "pool_id",
		}).AddRow(int64(11), "http", "127.0.0.1", 8080, "user", "pass", service.StatusActive, expiresAt, int64(7)))

	proxy, err := lockProxyPoolAssignment(context.Background(), client, 11)
	require.NoError(t, err)
	require.Equal(t, int64(11), proxy.ID)
	require.Equal(t, "http://user:pass@127.0.0.1:8080", proxy.URL())
	require.Equal(t, int64(7), *proxy.PoolID)
	require.Equal(t, expiresAt, *proxy.ExpiresAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLockProxyPoolAssignmentRejectsMissingProxy(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("FROM proxies") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(11)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "protocol", "host", "port", "username", "password", "status", "expires_at", "pool_id",
		}))

	_, err = lockProxyPoolAssignment(context.Background(), client, 11)
	require.ErrorIs(t, err, service.ErrProxyPoolBindingChanged)
	require.NoError(t, mock.ExpectationsWereMet())
}
