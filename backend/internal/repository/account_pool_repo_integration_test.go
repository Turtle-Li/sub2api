//go:build integration

package repository

import (
	"context"
	"time"

	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (s *AccountRepoSuite) newPoolRepo() *accountPoolRepository {
	tx := testEntTx(s.T())
	s.client = tx.Client()
	s.repo = newAccountRepositoryWithSQL(s.client, tx, nil)
	return &accountPoolRepository{client: s.client, sql: tx}
}

func (s *AccountRepoSuite) TestAccountPool_StatsMirrorListFilters() {
	poolRepo := s.newPoolRepo()
	ctx := context.Background()
	pool := &service.AccountPool{Name: "grok-free", Platform: service.PlatformGrok}
	s.Require().NoError(poolRepo.Create(ctx, pool))

	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	normal := mustCreateAccount(s.T(), s.client, &service.Account{Name: "normal", Platform: service.PlatformGrok,
		Credentials: map[string]any{"plan_type": "Free"}})
	limited := mustCreateAccount(s.T(), s.client, &service.Account{Name: "limited", Platform: service.PlatformGrok, RateLimitResetAt: &future})
	errored := mustCreateAccount(s.T(), s.client, &service.Account{Name: "errored", Platform: service.PlatformGrok, Status: service.StatusError})
	unsched := mustCreateAccount(s.T(), s.client, &service.Account{Name: "unsched", Platform: service.PlatformGrok})
	s.Require().NoError(s.client.Account.UpdateOneID(unsched.ID).SetSchedulable(false).Exec(ctx))
	temp := mustCreateAccount(s.T(), s.client, &service.Account{Name: "temp", Platform: service.PlatformGrok})
	s.Require().NoError(s.client.Account.UpdateOneID(temp.ID).SetTempUnschedulableUntil(future).SetExpiresAt(past).Exec(ctx))
	outside := mustCreateAccount(s.T(), s.client, &service.Account{Name: "outside", Platform: service.PlatformGrok})
	other := mustCreateAccount(s.T(), s.client, &service.Account{Name: "other-platform", Platform: service.PlatformOpenAI})

	_, err := poolRepo.AssignAccounts(ctx, pool.ID, []int64{normal.ID, other.ID})
	s.Require().ErrorIs(err, service.ErrAccountPoolPlatformMismatch)

	affected, err := poolRepo.AssignAccounts(ctx, pool.ID, []int64{normal.ID, limited.ID, errored.ID, unsched.ID, temp.ID})
	s.Require().NoError(err)
	s.Require().Equal(int64(5), affected)

	stats, err := poolRepo.Stats(ctx, []int64{pool.ID})
	s.Require().NoError(err)
	st := stats[pool.ID]
	s.Require().NotNil(st)
	s.Require().Equal(int64(5), st.Total)
	s.Require().Equal(int64(1), st.Normal)
	s.Require().Equal(int64(1), st.RateLimited)
	s.Require().Equal(int64(1), st.TempUnschedulable)
	s.Require().Equal(int64(1), st.Unschedulable)
	s.Require().Equal(int64(1), st.Error)
	s.Require().Equal(int64(1), st.Expired)
	s.Require().Nil(st.Codex)

	for status, want := range map[string]int64{"": 5, service.StatusActive: 1, "rate_limited": 1, "temp_unschedulable": 1, "unschedulable": 1, service.StatusError: 1} {
		ids, err := poolRepo.ListAccountIDs(ctx, pool.ID, status)
		s.Require().NoError(err)
		s.Require().Len(ids, int(want), "status=%q", status)
	}

	refs, err := poolRepo.ListMemberRefs(ctx, []int64{pool.ID})
	s.Require().NoError(err)
	s.Require().Len(refs, 5)
	for _, ref := range refs {
		s.Require().Equal(ref.ID == normal.ID, ref.GrokFree, "account %d", ref.ID)
	}

	// Main list defaults to accounts outside any pool.
	accounts, _, err := s.repo.ListWithAccountFilter(ctx, pagination.PaginationParams{Page: 1, PageSize: 50},
		service.AccountListFilter{PoolID: service.AccountListPoolNone})
	s.Require().NoError(err)
	names := make([]string, 0, len(accounts))
	for _, a := range accounts {
		names = append(names, a.Name)
	}
	s.Require().ElementsMatch([]string{outside.Name, other.Name}, names)

	inPool, total, err := s.repo.ListWithAccountFilter(ctx, pagination.PaginationParams{Page: 1, PageSize: 50},
		service.AccountListFilter{PoolID: pool.ID, Status: "rate_limited"})
	s.Require().NoError(err)
	s.Require().Equal(int64(1), total.Total)
	s.Require().Equal(limited.ID, inPool[0].ID)
	s.Require().NotNil(inPool[0].PoolID)
	s.Require().Equal(pool.ID, *inPool[0].PoolID)

	released, err := poolRepo.ReleaseAccounts(ctx, []int64{errored.ID, outside.ID})
	s.Require().NoError(err)
	s.Require().Equal(int64(1), released)

	s.Require().NoError(poolRepo.Delete(ctx, pool.ID))
	_, err = poolRepo.GetByID(ctx, pool.ID)
	s.Require().ErrorIs(err, service.ErrAccountPoolNotFound)
	member, err := s.client.Account.Get(ctx, normal.ID)
	s.Require().NoError(err)
	s.Require().Nil(member.PoolID, "deleting a pool releases its members")
}

func (s *AccountRepoSuite) TestAccountPool_CodexQuotaIgnoresResetSnapshots() {
	poolRepo := s.newPoolRepo()
	ctx := context.Background()
	pool := &service.AccountPool{Name: "gpt-free", Platform: service.PlatformOpenAI}
	s.Require().NoError(poolRepo.Create(ctx, pool))

	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	a := mustCreateAccount(s.T(), s.client, &service.Account{Name: "a", Platform: service.PlatformOpenAI, Extra: map[string]any{
		"codex_5h_used_percent": 100, "codex_5h_reset_at": future,
		"codex_7d_used_percent": 40, "codex_7d_reset_at": future,
	}})
	b := mustCreateAccount(s.T(), s.client, &service.Account{Name: "b", Platform: service.PlatformOpenAI, Extra: map[string]any{
		"codex_5h_used_percent": 100, "codex_5h_reset_at": past,
		"codex_7d_used_percent": 20, "codex_7d_reset_at": "not-a-time",
	}})
	c := mustCreateAccount(s.T(), s.client, &service.Account{Name: "c", Platform: service.PlatformOpenAI, Extra: map[string]any{
		"codex_5h_used_percent": "bad",
	}})
	_, err := poolRepo.AssignAccounts(ctx, pool.ID, []int64{a.ID, b.ID, c.ID})
	s.Require().NoError(err)

	stats, err := poolRepo.Stats(ctx, []int64{pool.ID})
	s.Require().NoError(err)
	codex := stats[pool.ID].Codex
	s.Require().NotNil(codex)
	s.Require().Equal(int64(2), codex.Accounts)
	s.Require().Equal(int64(1), codex.Exhausted)
	s.Require().InDelta(50, *codex.Avg5hUsedPercent, 0.001)
	s.Require().InDelta(30, *codex.Avg7dUsedPercent, 0.001)
}

func (s *AccountRepoSuite) TestAccountPool_CreateAccountValidatesPoolPlatform() {
	poolRepo := s.newPoolRepo()
	ctx := context.Background()
	pool := &service.AccountPool{Name: "grok", Platform: service.PlatformGrok}
	s.Require().NoError(poolRepo.Create(ctx, pool))

	mismatch := &service.Account{Name: "x", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Status: service.StatusActive, Credentials: map[string]any{}, Extra: map[string]any{}, Concurrency: 1, PoolID: &pool.ID}
	s.Require().ErrorIs(createAccountRecord(ctx, s.client, mismatch), service.ErrAccountPoolPlatformMismatch)

	ok := &service.Account{Name: "y", Platform: service.PlatformGrok, Type: service.AccountTypeOAuth,
		Status: service.StatusActive, Credentials: map[string]any{}, Extra: map[string]any{}, Concurrency: 1, PoolID: &pool.ID}
	s.Require().NoError(createAccountRecord(ctx, s.client, ok))
	stored, err := s.client.Account.Query().Where(dbaccount.IDEQ(ok.ID)).Only(ctx)
	s.Require().NoError(err)
	s.Require().NotNil(stored.PoolID)
	s.Require().Equal(pool.ID, *stored.PoolID)
}
