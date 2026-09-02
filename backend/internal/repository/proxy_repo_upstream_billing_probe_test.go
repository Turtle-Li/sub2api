package repository

import (
	"context"
	"errors"
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

func TestProxyUpdateInvalidatesBoundProbeSnapshotsAndEnqueuesOutboxAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	expectLockedProxyForUpdate(mock, 9, "old.example", "user", "pass")
	mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("SELECT EXISTS")+`.*`+regexp.QuoteMeta("parent_account_id IS NULL")).
		WithArgs(int64(9), service.PlatformOpenAI, service.AccountTypeOAuth, service.AccountTypeSetupToken).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(`(?s)UPDATE "proxies" SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "proxies" SET "backup_proxy_id" = NULL WHERE "backup_proxy_id" = \$1`).
		WithArgs(int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectProxyUpdateReload(mock, 9, "new.example", "user", "pass")
	mock.ExpectQuery(`(?s)UPDATE accounts.*- 'upstream_billing_probe'.*- 'ollama_cloud_usage_snapshot'.*type = 'apikey'.*extra \? 'upstream_billing_probe'.*platform IN \('openai', 'anthropic', 'kimi', 'zhipu', 'deepseek'\).*extra \? 'ollama_cloud_usage_snapshot'.*RETURNING id`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(17)).AddRow(int64(18)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)")).
		WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: []int64{17, 18}}).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := newProxyRepositoryWithSQL(client, db)
	proxy := &service.Proxy{
		ID:       9,
		Name:     "proxy",
		Protocol: "http",
		Host:     "new.example",
		Port:     8080,
		Username: "user",
		Password: "pass",
		Status:   service.StatusActive,
	}

	err = repo.Update(context.Background(), proxy)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxyUpdateRollsBackWhenProbeInvalidationOutboxFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	expectLockedProxyForUpdate(mock, 9, "old.example", "", "")
	mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("SELECT EXISTS")+`.*`+regexp.QuoteMeta("parent_account_id IS NULL")).
		WithArgs(int64(9), service.PlatformOpenAI, service.AccountTypeOAuth, service.AccountTypeSetupToken).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(`(?s)UPDATE "proxies" SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "proxies" SET "backup_proxy_id" = NULL WHERE "backup_proxy_id" = \$1`).
		WithArgs(int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectProxyUpdateReload(mock, 9, "new.example", "", "")
	mock.ExpectQuery(`(?s)UPDATE accounts.*- 'upstream_billing_probe'.*- 'ollama_cloud_usage_snapshot'.*type = 'apikey'.*extra \? 'upstream_billing_probe'.*platform IN \('openai', 'anthropic', 'kimi', 'zhipu', 'deepseek'\).*extra \? 'ollama_cloud_usage_snapshot'.*RETURNING id`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(17)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)")).
		WillReturnError(errors.New("outbox failed"))
	mock.ExpectRollback()

	repo := newProxyRepositoryWithSQL(client, db)
	proxy := &service.Proxy{ID: 9, Name: "proxy", Protocol: "http", Host: "new.example", Port: 8080, Status: service.StatusActive}

	err = repo.Update(context.Background(), proxy)

	require.EqualError(t, err, "outbox failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxyUpdateSkipsProbeInvalidationForNonIdentityChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	expectLockedProxyForUpdate(mock, 9, "same.example", "", "")
	mock.ExpectExec(`(?s)UPDATE "proxies" SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "proxies" SET "backup_proxy_id" = NULL WHERE "backup_proxy_id" = \$1`).
		WithArgs(int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectProxyUpdateReload(mock, 9, "same.example", "", "")
	mock.ExpectCommit()

	repo := newProxyRepositoryWithSQL(client, db)
	proxy := &service.Proxy{ID: 9, Name: "renamed", Protocol: "http", Host: "same.example", Port: 8080, Status: service.StatusActive, FallbackMode: service.FallbackModeNone}

	err = repo.Update(context.Background(), proxy)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxyStatusChangeRefreshesEveryBoundAccount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	expectLockedProxyForUpdate(mock, 9, "same.example", "", "")
	mock.ExpectExec(`(?s)UPDATE "proxies" SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "proxies" SET "backup_proxy_id" = NULL WHERE "backup_proxy_id" = \$1`).
		WithArgs(int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectProxyUpdateReloadWithStatus(mock, 9, "same.example", "", "", service.StatusDisabled)
	mock.ExpectQuery(`(?s)UPDATE accounts.*- 'upstream_billing_probe'.*RETURNING id`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT id") + `.*` + regexp.QuoteMeta("WHERE proxy_id = $1 AND deleted_at IS NULL") + `.*` + regexp.QuoteMeta("ORDER BY id")).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(17)).AddRow(int64(18)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)")).
		WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: []int64{17, 18}}).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := newProxyRepositoryWithSQL(client, db)
	proxy := &service.Proxy{
		ID:           9,
		Name:         "proxy",
		Protocol:     "http",
		Host:         "same.example",
		Port:         8080,
		Status:       service.StatusDisabled,
		FallbackMode: service.FallbackModeNone,
	}

	err = repo.Update(context.Background(), proxy)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func expectLockedProxyForUpdate(mock sqlmock.Sqlmock, id int64, host, username, password string) {
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT id, protocol, host, port") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "protocol", "host", "port", "username", "password", "status", "expires_at", "fallback_mode", "backup_proxy_id",
		}).AddRow(id, "http", host, 8080, username, password, service.StatusActive, nil, service.FallbackModeNone, nil))
}

func TestProxyListAccountSummariesIncludesParentRelation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT id, name, platform, type, notes, parent_account_id") + `.*` + regexp.QuoteMeta("WHERE proxy_id = $1")).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "platform", "type", "notes", "parent_account_id"}).
			AddRow(int64(88), "parent", service.PlatformOpenAI, service.AccountTypeOAuth, nil, nil).
			AddRow(int64(89), "shadow", service.PlatformOpenAI, service.AccountTypeOAuth, nil, int64(88)))

	summaries, err := newProxyRepositoryWithSQL(client, db).ListAccountSummariesByProxyID(context.Background(), 9)

	require.NoError(t, err)
	require.Len(t, summaries, 2)
	require.Nil(t, summaries[0].ParentAccountID)
	require.NotNil(t, summaries[1].ParentAccountID)
	require.Equal(t, int64(88), *summaries[1].ParentAccountID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxyUpdateRejectsFixedEgressIdentityChangeAfterRowLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	expectLockedProxyForUpdate(mock, 9, "100.80.10.114", "", "")
	mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("SELECT EXISTS")+`.*`+regexp.QuoteMeta("parent_account_id IS NULL")).
		WithArgs(int64(9), service.PlatformOpenAI, service.AccountTypeOAuth, service.AccountTypeSetupToken).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	repo := newProxyRepositoryWithSQL(client, db)
	err = repo.Update(context.Background(), &service.Proxy{
		ID:           9,
		Name:         "proxy",
		Protocol:     "socks5h",
		Host:         "100.80.10.115",
		Port:         1080,
		Status:       service.StatusActive,
		FallbackMode: service.FallbackModeNone,
	})

	require.ErrorIs(t, err, service.ErrFixedEgressProxyIdentityImmutable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func expectProxyUpdateReload(mock sqlmock.Sqlmock, id int64, host, username, password string) {
	expectProxyUpdateReloadWithStatus(mock, id, host, username, password, service.StatusActive)
}

func expectProxyUpdateReloadWithStatus(mock sqlmock.Sqlmock, id int64, host, username, password, status string) {
	now := time.Now()
	mock.ExpectQuery(`(?s)SELECT .* FROM "proxies" WHERE "id" = \$1`).
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "deleted_at", "name", "protocol", "host", "port",
			"username", "password", "status", "expires_at", "fallback_mode", "backup_proxy_id", "expiry_warn_days",
		}).AddRow(
			id, now, now, nil, "proxy", "http", host, 8080,
			username, password, status, nil, service.FallbackModeNone, nil, 0,
		))
}

func TestEnqueueProxyAccountChangesChunksLargePayloads(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	accountIDs := make([]int64, 1001)
	for i := range accountIDs {
		accountIDs[i] = int64(i + 1)
	}
	for start := 0; start < len(accountIDs); start += proxyProbeOutboxAccountChunkSize {
		end := start + proxyProbeOutboxAccountChunkSize
		if end > len(accountIDs) {
			end = len(accountIDs)
		}
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)")).
			WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: accountIDs[start:end]}).
			WillReturnResult(sqlmock.NewResult(1, 1))
	}

	err = enqueueProxyProbeAccountChanges(context.Background(), db, accountIDs)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
