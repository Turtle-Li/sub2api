//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/suite"
)

type ProxyExpirySuite struct {
	suite.Suite
	ctx  context.Context
	tx   *dbent.Tx
	repo *proxyRepository
}

func (s *ProxyExpirySuite) SetupTest() {
	s.ctx = context.Background()
	s.tx = testEntTx(s.T())
	s.repo = newProxyRepositoryWithSQL(s.tx.Client(), s.tx)
}
func TestProxyExpirySuite(t *testing.T) { suite.Run(t, new(ProxyExpirySuite)) }

func (s *ProxyExpirySuite) mkProxy(name, mode string, expiresAt *time.Time, backupID *int64) int64 {
	p := &service.Proxy{Name: name, Protocol: "http", Host: "127.0.0.1", Port: 8080,
		Status: service.StatusActive, FallbackMode: mode, ExpiryWarnDays: 7,
		ExpiresAt: expiresAt, BackupProxyID: backupID}
	s.Require().NoError(s.repo.Create(s.ctx, p))
	return p.ID
}

func (s *ProxyExpirySuite) mkAccountWithProxy(proxyID int64) int64 {
	var id int64
	err := scanSingleRow(s.ctx, s.tx, `
		INSERT INTO accounts (name, platform, type, credentials, extra, status, proxy_id, created_at, updated_at)
		VALUES ($1,'claude','api','{}','{}','active',$2,NOW(),NOW()) RETURNING id`,
		[]any{"acc-" + time.Now().Format("150405.000000"), proxyID}, &id)
	s.Require().NoError(err)
	return id
}

func (s *ProxyExpirySuite) mkOpenAIOAuthAccountWithProxy(proxyID int64, parentID *int64) int64 {
	return s.mkOpenAICodexAccountWithProxy(proxyID, parentID, service.AccountTypeOAuth)
}

func (s *ProxyExpirySuite) mkOpenAICodexAccountWithProxy(proxyID int64, parentID *int64, accountType string) int64 {
	quotaDimension := service.QuotaDimensionGlobal
	var parent any
	if parentID != nil {
		quotaDimension = service.QuotaDimensionSpark
		parent = *parentID
	}
	var id int64
	err := scanSingleRow(s.ctx, s.tx, `
		INSERT INTO accounts (
			name, platform, type, credentials, extra, status, proxy_id,
			parent_account_id, quota_dimension, created_at, updated_at
		)
		VALUES ($1,'openai',$2,'{}','{}','active',$3,$4,$5,NOW(),NOW())
		RETURNING id`,
		[]any{"codex-acc-" + time.Now().Format("150405.000000"), accountType, proxyID, parent, quotaDimension}, &id)
	s.Require().NoError(err)
	return id
}

func (s *ProxyExpirySuite) accountProxyID(id int64) *int64 {
	var pid *int64
	err := scanSingleRow(s.ctx, s.tx, `SELECT proxy_id FROM accounts WHERE id=$1`, []any{id}, &pid)
	s.Require().NoError(err)
	return pid
}

func (s *ProxyExpirySuite) TestSweep_DirectMode() {
	past := time.Now().Add(-time.Hour)
	pid := s.mkProxy("p-direct", service.FallbackModeDirect, &past, nil)
	aid := s.mkAccountWithProxy(pid)

	changed, err := s.repo.SweepExpiredProxies(s.ctx, time.Now())
	s.Require().NoError(err)
	s.Require().GreaterOrEqual(changed, int64(1))

	got, _ := s.repo.GetByID(s.ctx, pid)
	s.Require().Equal(service.StatusExpired, got.Status)
	s.Require().Nil(s.accountProxyID(aid))
	var origin *int64
	err = scanSingleRow(s.ctx, s.tx, `SELECT proxy_fallback_origin_id FROM accounts WHERE id=$1`, []any{aid}, &origin)
	s.Require().NoError(err)
	s.Require().NotNil(origin)
	s.Require().Equal(pid, *origin)
}

func (s *ProxyExpirySuite) TestSweep_EnqueuesChangedAccountIDsWithoutFullRebuild() {
	past := time.Now().Add(-time.Hour)
	firstProxyID := s.mkProxy("p-bulk-first", service.FallbackModeDirect, &past, nil)
	secondProxyID := s.mkProxy("p-bulk-second", service.FallbackModeDirect, &past, nil)
	firstAccountID := s.mkAccountWithProxy(firstProxyID)
	secondAccountID := s.mkAccountWithProxy(secondProxyID)

	changed, err := s.repo.SweepExpiredProxies(s.ctx, time.Now())
	s.Require().NoError(err)
	s.Require().EqualValues(2, changed)

	var payloadRaw []byte
	err = scanSingleRow(s.ctx, s.tx, `
		SELECT payload
		FROM scheduler_outbox
		WHERE event_type=$1
		ORDER BY id DESC
		LIMIT 1`, []any{service.SchedulerOutboxEventAccountBulkChanged}, &payloadRaw)
	s.Require().NoError(err)

	var payload struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	s.Require().NoError(json.Unmarshal(payloadRaw, &payload))
	s.Require().Equal([]int64{firstAccountID, secondAccountID}, payload.AccountIDs)

	var fullRebuildCount int
	err = scanSingleRow(s.ctx, s.tx, `
		SELECT COUNT(*)
		FROM scheduler_outbox
		WHERE event_type=$1`, []any{service.SchedulerOutboxEventFullRebuild}, &fullRebuildCount)
	s.Require().NoError(err)
	s.Require().Zero(fullRebuildCount)
}

func (s *ProxyExpirySuite) TestSweep_ProxyMode_Healthy() {
	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-time.Hour)
	backup := s.mkProxy("p-backup", service.FallbackModeNone, &future, nil)
	pid := s.mkProxy("p-main", service.FallbackModeProxy, &past, &backup)
	aid := s.mkAccountWithProxy(pid)

	_, err := s.repo.SweepExpiredProxies(s.ctx, time.Now())
	s.Require().NoError(err)
	s.Require().Equal(backup, *s.accountProxyID(aid))
	var origin *int64
	err = scanSingleRow(s.ctx, s.tx, `SELECT proxy_fallback_origin_id FROM accounts WHERE id=$1`, []any{aid}, &origin)
	s.Require().NoError(err)
	s.Require().NotNil(origin)
	s.Require().Equal(pid, *origin)
}

func (s *ProxyExpirySuite) TestSweep_NoneMode_KeepsAccount() {
	past := time.Now().Add(-time.Hour)
	pid := s.mkProxy("p-none", service.FallbackModeNone, &past, nil)
	aid := s.mkAccountWithProxy(pid)

	_, err := s.repo.SweepExpiredProxies(s.ctx, time.Now())
	s.Require().NoError(err)
	got, _ := s.repo.GetByID(s.ctx, pid)
	s.Require().Equal(service.StatusExpired, got.Status)
	s.Require().Equal(pid, *s.accountProxyID(aid))
	var origin *int64
	err = scanSingleRow(s.ctx, s.tx, `SELECT proxy_fallback_origin_id FROM accounts WHERE id=$1`, []any{aid}, &origin)
	s.Require().NoError(err)
	s.Require().Nil(origin)
}

func (s *ProxyExpirySuite) TestSweep_KeepsLegacyOpenAIOAuthParentAndShadowBoundFailClosed() {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	directProxyID := s.mkProxy("oauth-direct-expired", service.FallbackModeDirect, &past, nil)
	directParentID := s.mkOpenAIOAuthAccountWithProxy(directProxyID, nil)
	directShadowID := s.mkOpenAIOAuthAccountWithProxy(directProxyID, &directParentID)
	backupProxyID := s.mkProxy("oauth-backup", service.FallbackModeNone, &future, nil)
	proxyFallbackID := s.mkProxy("oauth-proxy-expired", service.FallbackModeProxy, &past, &backupProxyID)
	proxyFallbackParentID := s.mkOpenAIOAuthAccountWithProxy(proxyFallbackID, nil)
	proxyFallbackShadowID := s.mkOpenAIOAuthAccountWithProxy(proxyFallbackID, &proxyFallbackParentID)

	changed, err := s.repo.SweepExpiredProxies(s.ctx, time.Now())
	s.Require().NoError(err)
	s.Require().EqualValues(4, changed, "retained OAuth bindings must still be invalidated")

	for _, tc := range []struct {
		accountID int64
		proxyID   int64
	}{
		{directParentID, directProxyID},
		{directShadowID, directProxyID},
		{proxyFallbackParentID, proxyFallbackID},
		{proxyFallbackShadowID, proxyFallbackID},
	} {
		gotProxyID := s.accountProxyID(tc.accountID)
		s.Require().NotNil(gotProxyID)
		s.Require().Equal(tc.proxyID, *gotProxyID)
		var origin *int64
		err := scanSingleRow(s.ctx, s.tx, `SELECT proxy_fallback_origin_id FROM accounts WHERE id=$1`, []any{tc.accountID}, &origin)
		s.Require().NoError(err)
		s.Require().Nil(origin)
	}
	for _, proxyID := range []int64{directProxyID, proxyFallbackID} {
		got, err := s.repo.GetByID(s.ctx, proxyID)
		s.Require().NoError(err)
		s.Require().Equal(service.StatusExpired, got.Status)
	}

	_, err = service.ResolveAccountProxyURL(&service.Account{
		ID:       directParentID,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		ProxyID:  &directProxyID,
		Proxy: &service.Proxy{
			ID:        directProxyID,
			Status:    service.StatusExpired,
			ExpiresAt: &past,
		},
	})
	s.Require().ErrorIs(err, service.ErrAccountProxyUnavailable)
}

func (s *ProxyExpirySuite) TestSweep_KeepsOpenAISetupTokenBindingsBoundFailClosed() {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	directProxyID := s.mkProxy("setup-token-direct-expired", service.FallbackModeDirect, &past, nil)
	directAccountID := s.mkOpenAICodexAccountWithProxy(directProxyID, nil, service.AccountTypeSetupToken)
	backupProxyID := s.mkProxy("setup-token-backup", service.FallbackModeNone, &future, nil)
	proxyFallbackID := s.mkProxy("setup-token-proxy-expired", service.FallbackModeProxy, &past, &backupProxyID)
	proxyFallbackAccountID := s.mkOpenAICodexAccountWithProxy(proxyFallbackID, nil, service.AccountTypeSetupToken)

	changed, err := s.repo.SweepExpiredProxies(s.ctx, time.Now())
	s.Require().NoError(err)
	s.Require().EqualValues(2, changed, "retained setup-token bindings must still be invalidated")
	for _, tc := range []struct {
		accountID int64
		proxyID   int64
	}{
		{directAccountID, directProxyID},
		{proxyFallbackAccountID, proxyFallbackID},
	} {
		gotProxyID := s.accountProxyID(tc.accountID)
		s.Require().NotNil(gotProxyID)
		s.Require().Equal(tc.proxyID, *gotProxyID)
		var origin *int64
		err := scanSingleRow(s.ctx, s.tx, `SELECT proxy_fallback_origin_id FROM accounts WHERE id=$1`, []any{tc.accountID}, &origin)
		s.Require().NoError(err)
		s.Require().Nil(origin)
	}

	_, err = service.ResolveAccountProxyURL(&service.Account{
		ID:       directAccountID,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeSetupToken,
		ProxyID:  &directProxyID,
		Proxy: &service.Proxy{
			ID:        directProxyID,
			Status:    service.StatusExpired,
			ExpiresAt: &past,
		},
	})
	s.Require().ErrorIs(err, service.ErrAccountProxyUnavailable)
}

func (s *ProxyExpirySuite) TestSweep_StaleSnapshotDoesNotExpireRenewedProxy() {
	past := time.Now().Add(-time.Hour)
	proxyID := s.mkProxy("renewed-after-snapshot", service.FallbackModeDirect, &past, nil)
	accountID := s.mkAccountWithProxy(proxyID)
	future := time.Now().Add(24 * time.Hour)
	_, err := s.tx.ExecContext(s.ctx, `UPDATE proxies SET expires_at=$1 WHERE id=$2`, future, proxyID)
	s.Require().NoError(err)

	affectedIDs, err := s.repo.sweepOneExpiredProxy(s.ctx, proxyID, nil, true, time.Now())
	s.Require().NoError(err)
	s.Require().Empty(affectedIDs)
	proxy, err := s.repo.GetByID(s.ctx, proxyID)
	s.Require().NoError(err)
	s.Require().Equal(service.StatusActive, proxy.Status)
	s.Require().Equal(proxyID, *s.accountProxyID(accountID))
}

func (s *ProxyExpirySuite) TestSweepOne_OuterTransactionDoesNotEvictBeforeCommit() {
	past := time.Now().Add(-time.Hour)
	proxyID := s.mkProxy("outer-tx-no-early-cache-eviction", service.FallbackModeNone, &past, nil)
	accountID := s.mkOpenAICodexAccountWithProxy(proxyID, nil, service.AccountTypeOAuth)
	cache := &schedulerCacheRecorder{accounts: map[int64]*service.Account{
		accountID: {ID: accountID},
	}}
	s.repo.schedulerCache = cache

	affectedIDs, err := s.repo.sweepOneExpiredProxy(s.ctx, proxyID, nil, false, time.Now())

	s.Require().NoError(err)
	s.Require().Equal([]int64{accountID}, affectedIDs)
	s.Require().Empty(cache.deleteIDs, "an outer transaction has not committed yet")
	s.Require().Contains(cache.accounts, accountID)
}
