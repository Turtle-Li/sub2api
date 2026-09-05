package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func newFixedEgressGuardSQLClient(t *testing.T) (*dbent.Client, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return client, mock
}

func expectLockedOpenAIOAuthProxy(mock sqlmock.Sqlmock, proxyID int64) {
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT id, status, protocol, host, port") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(proxyID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "status", "protocol", "host", "port", "username", "password", "expires_at", "fallback_mode", "backup_proxy_id",
		}).AddRow(proxyID, service.StatusActive, "socks5h", "100.80.10.114", 1080, "", "", nil, service.FallbackModeNone, nil))
}

func TestLockFixedEgressProxyForOpenAIOAuthUsesSharedMutationLock(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT id, status, protocol, host, port") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "status", "protocol", "host", "port", "username", "password", "expires_at", "fallback_mode", "backup_proxy_id",
		}).AddRow(7, service.StatusActive, "socks5h", "100.80.10.114", 1080, "", "", nil, service.FallbackModeNone, nil))

	proxy, err := lockFixedEgressProxyForOpenAIOAuth(context.Background(), client, 7)

	require.NoError(t, err)
	require.Equal(t, int64(7), proxy.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnforceOpenAIOAuthParentUpdateRejectsDisabledParentDirectProxyRebind(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT id, status, protocol, host, port") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "status", "protocol", "host", "port", "username", "password", "expires_at", "fallback_mode", "backup_proxy_id",
		}).AddRow(8, service.StatusDisabled, "http", "proxy.example", 8080, "", "", nil, service.FallbackModeDirect, nil))
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, status, parent_account_id, proxy_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(41)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "parent_account_id", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeOAuth, service.StatusDisabled, nil, int64(7)))
	proxyID := int64(8)

	err := enforceOpenAIOAuthParentUpdate(context.Background(), client, &service.Account{
		ID:       41,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusDisabled,
		ProxyID:  &proxyID,
	})

	require.ErrorIs(t, err, service.ErrFixedEgressCASRequired)
	require.Equal(t, "FIXED_EGRESS_CAS_REQUIRED", errors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnforceOpenAIOAuthParentUpdateLocksCandidateProxyBeforeAccount(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT id, status, protocol, host, port") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "status", "protocol", "host", "port", "username", "password", "expires_at", "fallback_mode", "backup_proxy_id",
		}).AddRow(7, service.StatusActive, "socks5h", "100.80.10.114", 1080, "", "", nil, service.FallbackModeNone, nil))
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, status, parent_account_id, proxy_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(41)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "parent_account_id", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeOAuth, service.StatusDisabled, nil, int64(7)))
	proxyID := int64(7)

	err := enforceOpenAIOAuthParentUpdate(context.Background(), client, &service.Account{
		ID:       41,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusActive,
		ProxyID:  &proxyID,
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHasOpenAIOAuthParentProxyBindingDoesNotFilterStatus(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("SELECT EXISTS")+`.*`+regexp.QuoteMeta("parent_account_id IS NULL")).
		WithArgs(int64(7), service.PlatformOpenAI, service.AccountTypeOAuth, service.AccountTypeSetupToken).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	bound, err := hasOpenAIOAuthParentProxyBinding(context.Background(), client, 7)

	require.NoError(t, err)
	require.True(t, bound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLockBulkProxyMutationTargetsFailsClosedWithoutClient(t *testing.T) {
	err := lockBulkProxyMutationTargets(context.Background(), nil, []int64{41}, nil, true)

	require.ErrorIs(t, err, service.ErrFixedEgressGuardUnavailable)
	require.Equal(t, "FIXED_EGRESS_GUARD_UNAVAILABLE", errors.Reason(err))
}

func TestEnforceOpenAIOAuthShadowProxyUpdateRejectsParentMismatch(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	expectLockedOpenAIOAuthProxy(mock, 8)
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, status, parent_account_id, proxy_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(41)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "parent_account_id", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeOAuth, service.StatusActive, nil, int64(7)))
	shadowProxyID := int64(8)
	parentID := int64(41)

	err := enforceOpenAIOAuthShadowProxyRelation(context.Background(), client, &service.Account{
		ID:              42,
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		ParentAccountID: &parentID,
		ProxyID:         &shadowProxyID,
	})

	require.ErrorIs(t, err, service.ErrCredentialShadowProxyMismatch)
	require.Equal(t, "CREDENTIAL_SHADOW_PROXY_MISMATCH", errors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnforceOpenAIOAuthShadowProxyUpdateAcceptsParentMatch(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	expectLockedOpenAIOAuthProxy(mock, 7)
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, status, parent_account_id, proxy_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(41)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "parent_account_id", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeOAuth, service.StatusActive, nil, int64(7)))
	proxyID := int64(7)
	parentID := int64(41)

	err := enforceOpenAIOAuthShadowProxyRelation(context.Background(), client, &service.Account{
		ID:              42,
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		ParentAccountID: &parentID,
		ProxyID:         &proxyID,
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnforceOpenAISetupTokenShadowProxyUpdateRejectsParentMismatch(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	expectLockedOpenAIOAuthProxy(mock, 8)
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, status, parent_account_id, proxy_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(41)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "parent_account_id", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeSetupToken, service.StatusActive, nil, int64(7)))
	shadowProxyID := int64(8)
	parentID := int64(41)

	err := enforceOpenAIOAuthShadowProxyRelation(context.Background(), client, &service.Account{
		ID:              42,
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeSetupToken,
		ParentAccountID: &parentID,
		ProxyID:         &shadowProxyID,
	})

	require.ErrorIs(t, err, service.ErrCredentialShadowProxyMismatch)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRejectsOpenAIOAuthShadowProxyMismatch(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	mock.ExpectBegin()
	expectLockedOpenAIOAuthProxy(mock, 8)
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, status, parent_account_id, proxy_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(41)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "parent_account_id", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeOAuth, service.StatusActive, nil, int64(7)))
	mock.ExpectRollback()
	parentID := int64(41)
	shadowProxyID := int64(8)
	repo := newAccountRepositoryWithSQL(client, nil, nil)

	err := repo.Create(context.Background(), &service.Account{
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		ParentAccountID: &parentID,
		ProxyID:         &shadowProxyID,
	})

	require.ErrorIs(t, err, service.ErrCredentialShadowProxyMismatch)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateWithAccountGroupsRejectsOpenAISetupTokenShadowProxyMismatch(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	mock.ExpectBegin()
	expectLockedOpenAIOAuthProxy(mock, 8)
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, status, parent_account_id, proxy_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(41)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "parent_account_id", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeSetupToken, service.StatusActive, nil, int64(7)))
	mock.ExpectRollback()
	parentID := int64(41)
	shadowProxyID := int64(8)
	repo := newAccountRepositoryWithSQL(client, nil, nil)

	err := repo.CreateWithAccountGroups(context.Background(), &service.Account{
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeSetupToken,
		ParentAccountID: &parentID,
		ProxyID:         &shadowProxyID,
	}, nil)

	require.ErrorIs(t, err, service.ErrCredentialShadowProxyMismatch)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRejectsPersistedOpenAIOAuthParentReclassifiedAsShadow(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	mock.ExpectBegin()
	expectLockedOpenAIOAuthProxy(mock, 8)
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, status, parent_account_id, proxy_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(52)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "parent_account_id", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeSetupToken, service.StatusActive, nil, int64(8)))
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, parent_account_id, quota_dimension, proxy_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(41)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "parent_account_id", "quota_dimension", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeOAuth, nil, service.QuotaDimensionGlobal, int64(7)))
	mock.ExpectRollback()
	parentID := int64(52)
	proxyID := int64(8)
	repo := newAccountRepositoryWithSQL(client, nil, nil)

	err := repo.Update(context.Background(), &service.Account{
		ID:              41,
		Platform:        service.PlatformOpenAI,
		Type:            service.AccountTypeOAuth,
		Status:          service.StatusActive,
		ParentAccountID: &parentID,
		QuotaDimension:  service.QuotaDimensionSpark,
		ProxyID:         &proxyID,
	})

	require.ErrorIs(t, err, service.ErrFixedEgressCASRequired)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPersistedOpenAIFixedEgressRelationRejectsPlatformOrTypePivot(t *testing.T) {
	tests := []struct {
		name              string
		persistedType     string
		candidatePlatform string
		candidateType     string
	}{
		{name: "oauth-to-api-key", persistedType: service.AccountTypeOAuth, candidatePlatform: service.PlatformOpenAI, candidateType: service.AccountTypeAPIKey},
		{name: "setup-token-to-api-key", persistedType: service.AccountTypeSetupToken, candidatePlatform: service.PlatformOpenAI, candidateType: service.AccountTypeAPIKey},
		{name: "oauth-to-setup-token", persistedType: service.AccountTypeOAuth, candidatePlatform: service.PlatformOpenAI, candidateType: service.AccountTypeSetupToken},
		{name: "openai-to-anthropic", persistedType: service.AccountTypeOAuth, candidatePlatform: service.PlatformAnthropic, candidateType: service.AccountTypeOAuth},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, mock := newFixedEgressGuardSQLClient(t)
			proxyID := int64(7)
			mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, parent_account_id, quota_dimension, proxy_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
				WithArgs(int64(41)).
				WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "parent_account_id", "quota_dimension", "proxy_id"}).
					AddRow(service.PlatformOpenAI, tc.persistedType, nil, service.QuotaDimensionGlobal, proxyID))

			err := enforcePersistedOpenAIFixedEgressRelation(context.Background(), client, &service.Account{
				ID:             41,
				Platform:       tc.candidatePlatform,
				Type:           tc.candidateType,
				QuotaDimension: service.QuotaDimensionGlobal,
				ProxyID:        &proxyID,
			})

			require.ErrorIs(t, err, service.ErrFixedEgressCASRequired)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpdateRejectsPersistedOpenAISetupTokenShadowReclassifiedAsParent(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	mock.ExpectBegin()
	expectLockedOpenAIOAuthProxy(mock, 7)
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, status, parent_account_id, proxy_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "parent_account_id", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeSetupToken, service.StatusDisabled, int64(41), int64(7)))
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, parent_account_id, quota_dimension, proxy_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "parent_account_id", "quota_dimension", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeSetupToken, int64(41), service.QuotaDimensionSpark, int64(7)))
	mock.ExpectRollback()
	proxyID := int64(7)
	repo := newAccountRepositoryWithSQL(client, nil, nil)

	err := repo.Update(context.Background(), &service.Account{
		ID:             42,
		Platform:       service.PlatformOpenAI,
		Type:           service.AccountTypeSetupToken,
		Status:         service.StatusDisabled,
		QuotaDimension: service.QuotaDimensionGlobal,
		ProxyID:        &proxyID,
	})

	require.ErrorIs(t, err, service.ErrFixedEgressCASRequired)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkUpdateRejectsOpenAIOAuthLikeShadowProxyWrite(t *testing.T) {
	for _, accountType := range []string{service.AccountTypeOAuth, service.AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			client, mock := newFixedEgressGuardSQLClient(t)
			newProxyID := int64(8)
			mock.ExpectBegin()
			expectLockedProxyForUpdate(mock, newProxyID, "100.80.10.114", "", "")
			mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, status, parent_account_id, proxy_id") + `.*` + regexp.QuoteMeta("FOR UPDATE")).
				WithArgs(sqlmock.AnyArg()).
				WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "parent_account_id", "proxy_id"}).
					AddRow(service.PlatformOpenAI, accountType, service.StatusActive, int64(41), int64(7)))
			mock.ExpectRollback()
			repo := newAccountRepositoryWithSQL(client, nil, nil)

			_, err := repo.BulkUpdate(context.Background(), []int64{42}, service.AccountBulkUpdate{ProxyID: &newProxyID})

			require.ErrorIs(t, err, service.ErrCredentialShadowProxyMismatch)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCompareAndSwapOpenAIOAuthProxyValidatesPersistedProxyShape(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT id, status, protocol, host, port") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "status", "protocol", "host", "port", "username", "password", "expires_at", "fallback_mode", "backup_proxy_id",
		}).AddRow(7, service.StatusActive, "socks5h", "proxy.example", 1080, "", "", nil, service.FallbackModeDirect, nil))
	mock.ExpectRollback()

	repo := newAccountRepositoryWithSQL(client, nil, nil)
	_, err := repo.CompareAndSwapOpenAIOAuthProxy(context.Background(), []int64{41}, 0, &service.Proxy{ID: 7})

	require.ErrorIs(t, err, service.ErrFixedEgressProxyInvalid)
	require.Equal(t, "FIXED_EGRESS_PROXY_INVALID", errors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompareAndSwapOpenAIOAuthProxyCompatibilityModeRejectsBeforeMutation(t *testing.T) {
	t.Setenv(service.FixedEgressCompatibilityModeEnv, "true")
	client, mock := newFixedEgressGuardSQLClient(t)
	cache := &proxySchedulerCacheRecorder{}
	repo := newAccountRepositoryWithSQL(client, nil, cache)

	_, err := repo.CompareAndSwapOpenAIOAuthProxy(
		context.Background(),
		[]int64{41},
		0,
		&service.Proxy{ID: 7},
	)

	require.ErrorIs(t, err, service.ErrFixedEgressMigrationNotReady)
	require.Equal(t, "FIXED_EGRESS_MIGRATION_NOT_READY", errors.Reason(err))
	require.Empty(t, cache.deleteIDs, "compatibility guard must not mutate scheduler cache")
	require.NoError(t, mock.ExpectationsWereMet(), "compatibility guard must not open a transaction or enqueue outbox work")
}

func TestBulkUpdateLocksProxyBeforePersistedOAuthParentAndRejectsRebind(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	newProxyID := int64(8)
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT id, protocol, host, port") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(newProxyID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "protocol", "host", "port", "username", "password", "status", "expires_at", "fallback_mode", "backup_proxy_id",
		}).AddRow(newProxyID, "socks5h", "100.80.10.114", 1080, "", "", service.StatusActive, nil, service.FallbackModeNone, nil))
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, status, parent_account_id, proxy_id") + `.*` + regexp.QuoteMeta("FOR UPDATE")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "parent_account_id", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeOAuth, service.StatusDisabled, nil, int64(7)))
	mock.ExpectRollback()

	repo := newAccountRepositoryWithSQL(client, nil, nil)
	_, err := repo.BulkUpdate(context.Background(), []int64{41}, service.AccountBulkUpdate{ProxyID: &newProxyID})

	require.ErrorIs(t, err, service.ErrFixedEgressCASRequired)
	require.Equal(t, "FIXED_EGRESS_CAS_REQUIRED", errors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkUpdateRejectsProxiedOAuthParentReactivationUnderAccountLock(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	status := service.StatusActive
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, status, parent_account_id, proxy_id") + `.*` + regexp.QuoteMeta("FOR UPDATE")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "status", "parent_account_id", "proxy_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeOAuth, service.StatusDisabled, nil, int64(7)))
	mock.ExpectRollback()

	repo := newAccountRepositoryWithSQL(client, nil, nil)
	_, err := repo.BulkUpdate(context.Background(), []int64{41}, service.AccountBulkUpdate{Status: &status})

	require.ErrorIs(t, err, service.ErrFixedEgressActivationRequiresSingleUpdate)
	require.Equal(t, "FIXED_EGRESS_ACTIVATION_REQUIRES_SINGLE_UPDATE", errors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRevertProxyFallbackRejectsOpenAIOAuthBeforeRawMutation(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT platform, type, proxy_fallback_origin_id") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(41)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "proxy_fallback_origin_id"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeOAuth, int64(7)))
	mock.ExpectRollback()

	repo := newAccountRepositoryWithSQL(client, nil, nil)
	err := repo.RevertProxyFallback(context.Background(), 41)

	require.ErrorIs(t, err, service.ErrFixedEgressCASRequired)
	require.Equal(t, "FIXED_EGRESS_CAS_REQUIRED", errors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxyDeleteLocksRelationAndRejectsConcurrentBoundAccount(t *testing.T) {
	client, mock := newFixedEgressGuardSQLClient(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT id, protocol, host, port") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "protocol", "host", "port", "username", "password", "status", "expires_at", "fallback_mode", "backup_proxy_id",
		}).AddRow(7, "socks5h", "100.80.10.114", 1080, "", "", service.StatusActive, nil, service.FallbackModeNone, nil))
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT EXISTS") + `.*` + regexp.QuoteMeta("proxy_id = $1")).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	repo := newProxyRepositoryWithSQL(client, nil)
	err := repo.Delete(context.Background(), 7)

	require.ErrorIs(t, err, service.ErrProxyInUse)
	require.Equal(t, "PROXY_IN_USE", errors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}
