package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// proxySchedulerSnapshotGuardRecorder embeds the broad cache interface so the
// test only has to define the mutation methods exercised by the repository.
// Implementing the optional retirer interface lets the test distinguish an
// ID-only fence from an unsafe partial Account payload.
type proxySchedulerSnapshotGuardRecorder struct {
	service.SchedulerCache
	setAccounts    []*service.Account
	retireAccounts []*service.Account
	deleteIDs      []int64
}

func (c *proxySchedulerSnapshotGuardRecorder) SetAccount(_ context.Context, account *service.Account) error {
	c.setAccounts = append(c.setAccounts, account)
	return nil
}

func (c *proxySchedulerSnapshotGuardRecorder) RetireAccountSnapshot(_ context.Context, account *service.Account) error {
	c.retireAccounts = append(c.retireAccounts, account)
	return nil
}

func (c *proxySchedulerSnapshotGuardRecorder) RetireDeletedAccountSnapshot(_ context.Context, accountID int64) error {
	return nil
}

func (c *proxySchedulerSnapshotGuardRecorder) DeleteAccount(_ context.Context, accountID int64) error {
	c.deleteIDs = append(c.deleteIDs, accountID)
	return nil
}

func TestDeleteSchedulerAccountSnapshotsUsesIDOnlyRetirementWhenMetadataEnrichmentFails(t *testing.T) {
	t.Setenv(service.FixedEgressCompatibilityModeEnv, "false")
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	// GetByIDs performs the post-commit enrichment read. Simulate a transient
	// database failure after fixed-egress classification has already succeeded.
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta(`SELECT`) + `.*` + regexp.QuoteMeta(`FROM "accounts"`)).
		WithArgs(int64(41)).
		WillReturnError(errors.New("metadata enrichment unavailable"))

	cache := &proxySchedulerSnapshotGuardRecorder{}
	repo := newProxyRepositoryWithSQL(client, db, cache)
	partial := &service.Account{
		ID:       41,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
	}

	repo.deleteSchedulerAccountSnapshots(context.Background(), []int64{partial.ID}, map[int64]*service.Account{
		partial.ID: partial,
	})

	require.Empty(t, cache.setAccounts, "metadata read failure must not publish a partial account")
	require.Empty(t, cache.deleteIDs, "fixed-egress IDs must use the retirement fence")
	require.Len(t, cache.retireAccounts, 1)
	require.Equal(t, &service.Account{ID: partial.ID}, cache.retireAccounts[0], "retirement must be ID-only when enrichment fails")
	require.NoError(t, mock.ExpectationsWereMet())
}
