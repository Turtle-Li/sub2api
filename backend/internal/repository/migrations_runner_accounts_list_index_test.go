package repository

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestApplyMigrationsFS_NonTransactionalMigration_AccountsListDefaultIndexDropsInvalidIndexBeforeRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
		WithArgs(accountsListDefaultIndexMigration).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT EXISTS \\(").
		WithArgs(accountsListDefaultIndex).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("DROP INDEX CONCURRENTLY IF EXISTS idx_accounts_unpooled_name_id").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_unpooled_name_id").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO schema_migrations \\(filename, checksum\\) VALUES \\(\\$1, \\$2\\)").
		WithArgs(accountsListDefaultIndexMigration, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fsys := fstest.MapFS{
		accountsListDefaultIndexMigration: &fstest.MapFile{
			Data: []byte(`
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_unpooled_name_id
    ON accounts (name, id)
    WHERE deleted_at IS NULL AND pool_id IS NULL;
`),
		},
	}

	err = applyMigrationsFS(context.Background(), db, fsys)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
