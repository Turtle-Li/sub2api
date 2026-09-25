package service

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"golang.org/x/sync/singleflight"
)

// AccountListPoolNone scopes the admin account list to accounts outside any pool.
const AccountListPoolNone int64 = -1

const (
	accountPoolNameMaxLen         = 100
	accountPoolMemberBatchMax     = 5000
	accountPoolUsageCacheTTL      = 60 * time.Second
	accountPoolUsageRefreshBudget = 30 * time.Second
	accountPoolUsageChunkSize     = 1000
)

var (
	ErrAccountPoolNotFound         = infraerrors.NotFound("ACCOUNT_POOL_NOT_FOUND", "account pool not found")
	ErrAccountPoolInvalidName      = infraerrors.BadRequest("ACCOUNT_POOL_INVALID_NAME", "account pool name is required and must be at most 100 characters")
	ErrAccountPoolInvalidPlatform  = infraerrors.BadRequest("ACCOUNT_POOL_INVALID_PLATFORM", "account pool platform is required")
	ErrAccountPoolPlatformMismatch = infraerrors.BadRequest("ACCOUNT_POOL_PLATFORM_MISMATCH", "account platform does not match the account pool platform")
	ErrAccountPoolEmptyMembers     = infraerrors.BadRequest("ACCOUNT_POOL_EMPTY_MEMBERS", "account_ids is required")
	ErrAccountPoolInvalidFilter    = infraerrors.BadRequest("INVALID_POOL_FILTER", "invalid pool filter")
	ErrAccountPoolTooManyMembers   = infraerrors.BadRequest("ACCOUNT_POOL_TOO_MANY_MEMBERS", "too many account_ids in one request")
)

// AccountListFilter is the admin account list filter including pool scoping.
// PoolID: 0 = all accounts, AccountListPoolNone = only accounts without a pool, >0 = that pool.
type AccountListFilter struct {
	Platform    string
	Type        string
	Status      string
	Search      string
	GroupID     int64
	PrivacyMode string
	PoolID      int64
}

// AccountListPoolFilterNone is the query value selecting accounts outside any pool.
const AccountListPoolFilterNone = "none"

// ParseAccountListPoolFilter maps "" / "none" / "<id>" to the AccountListFilter.PoolID value.
func ParseAccountListPoolFilter(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "":
		return 0, nil
	case AccountListPoolFilterNone:
		return AccountListPoolNone, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrAccountPoolInvalidFilter
	}
	return id, nil
}

// accountFilteredListReader is implemented by the admin account repository.
// It is optional so existing test doubles keep compiling.
type accountFilteredListReader interface {
	ListWithAccountFilter(ctx context.Context, params pagination.PaginationParams, filter AccountListFilter) ([]Account, *pagination.PaginationResult, error)
}

// AccountPool is a display-only folder of accounts of a single platform.
// Scheduling and groups stay bound to the member accounts.
type AccountPool struct {
	ID        int64
	Name      string
	Platform  string
	Notes     *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AccountPoolStats mirrors the admin list status filters so a click-through
// from a pool stat lands on the same count.
type AccountPoolStats struct {
	Total             int64                  `json:"total"`
	Normal            int64                  `json:"normal"`
	RateLimited       int64                  `json:"rate_limited"`
	TempUnschedulable int64                  `json:"temp_unschedulable"`
	Unschedulable     int64                  `json:"unschedulable"`
	Error             int64                  `json:"error"`
	Inactive          int64                  `json:"inactive"`
	Expired           int64                  `json:"expired"`
	Codex             *AccountPoolCodexQuota `json:"codex,omitempty"`
}

// AccountPoolCodexQuota aggregates the Codex usage snapshots already stored in account extra.
type AccountPoolCodexQuota struct {
	Accounts         int64    `json:"accounts"`
	Avg5hUsedPercent *float64 `json:"avg_5h_used_percent,omitempty"`
	Avg7dUsedPercent *float64 `json:"avg_7d_used_percent,omitempty"`
	Exhausted        int64    `json:"exhausted"`
}

// AccountPoolUsage is the cached rolling usage of a pool.
type AccountPoolUsage struct {
	WindowHours int                       `json:"window_hours"`
	Requests    int64                     `json:"requests"`
	Tokens      int64                     `json:"tokens"`
	Cost        float64                   `json:"cost"`
	GrokFree    *AccountPoolGrokFreeQuota `json:"grok_free,omitempty"`
	UpdatedAt   time.Time                 `json:"updated_at"`
}

// AccountPoolGrokFreeQuota sums the local rolling token quota of explicit free Grok accounts.
type AccountPoolGrokFreeQuota struct {
	Accounts    int64 `json:"accounts"`
	UsedTokens  int64 `json:"used_tokens"`
	LimitTokens int64 `json:"limit_tokens"`
	NearLimit   int64 `json:"near_limit"`
}

// AccountPoolMemberRef is the minimal member projection used for usage aggregation.
type AccountPoolMemberRef struct {
	ID       int64
	PoolID   int64
	GrokFree bool
}

// AccountPoolSummary is a pool with its statistics for the admin list.
type AccountPoolSummary struct {
	Pool  AccountPool
	Stats AccountPoolStats
	Usage *AccountPoolUsage
}

type AccountPoolRepository interface {
	List(ctx context.Context, platform string) ([]AccountPool, error)
	GetByID(ctx context.Context, id int64) (*AccountPool, error)
	Create(ctx context.Context, pool *AccountPool) error
	Update(ctx context.Context, pool *AccountPool) error
	Delete(ctx context.Context, id int64) error
	// Stats counts members of the given pools by list-filter status.
	Stats(ctx context.Context, poolIDs []int64) (map[int64]*AccountPoolStats, error)
	ListMemberRefs(ctx context.Context, poolIDs []int64) ([]AccountPoolMemberRef, error)
	// AssignAccounts moves accounts into the pool; every account must match the pool platform.
	AssignAccounts(ctx context.Context, poolID int64, accountIDs []int64) (int64, error)
	ReleaseAccounts(ctx context.Context, accountIDs []int64) (int64, error)
	// ListAccountIDs returns member ids filtered like the admin list status filter.
	ListAccountIDs(ctx context.Context, poolID int64, status string) ([]int64, error)
}

type AccountPoolService struct {
	repo         AccountPoolRepository
	usageLogRepo UsageLogRepository
	cfg          *config.Config

	usageMu      sync.RWMutex
	usage        map[int64]*AccountPoolUsage
	usageAt      time.Time
	usageGroup   singleflight.Group
	usageTimeNow func() time.Time
}

func NewAccountPoolService(repo AccountPoolRepository, usageLogRepo UsageLogRepository, cfg *config.Config) *AccountPoolService {
	return &AccountPoolService{
		repo:         repo,
		usageLogRepo: usageLogRepo,
		cfg:          cfg,
		usageTimeNow: time.Now,
	}
}

func (s *AccountPoolService) List(ctx context.Context, platform string) ([]AccountPoolSummary, error) {
	pools, err := s.repo.List(ctx, strings.TrimSpace(platform))
	if err != nil {
		return nil, err
	}
	if len(pools) == 0 {
		return []AccountPoolSummary{}, nil
	}
	ids := make([]int64, len(pools))
	for i := range pools {
		ids[i] = pools[i].ID
	}
	stats, err := s.repo.Stats(ctx, ids)
	if err != nil {
		return nil, err
	}
	usage := s.cachedUsage()
	out := make([]AccountPoolSummary, len(pools))
	for i := range pools {
		out[i].Pool = pools[i]
		if st := stats[pools[i].ID]; st != nil {
			out[i].Stats = *st
		}
		if usage != nil {
			out[i].Usage = usage[pools[i].ID]
		}
	}
	return out, nil
}

func (s *AccountPoolService) Get(ctx context.Context, id int64) (*AccountPoolSummary, error) {
	pool, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	stats, err := s.repo.Stats(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	out := &AccountPoolSummary{Pool: *pool}
	if st := stats[id]; st != nil {
		out.Stats = *st
	}
	if usage := s.cachedUsage(); usage != nil {
		out.Usage = usage[id]
	}
	return out, nil
}

func (s *AccountPoolService) Create(ctx context.Context, name, platform string, notes *string) (*AccountPool, error) {
	pool := &AccountPool{Name: strings.TrimSpace(name), Platform: strings.TrimSpace(platform), Notes: normalizeAccountNotes(notes)}
	if err := validateAccountPool(pool); err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, pool); err != nil {
		return nil, err
	}
	return pool, nil
}

// Update edits name and notes; the platform is fixed at creation because members must match it.
func (s *AccountPoolService) Update(ctx context.Context, id int64, name *string, notes *string) (*AccountPool, error) {
	pool, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if name != nil {
		pool.Name = strings.TrimSpace(*name)
	}
	if notes != nil {
		pool.Notes = normalizeAccountNotes(notes)
	}
	if err := validateAccountPool(pool); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, pool); err != nil {
		return nil, err
	}
	return pool, nil
}

// Delete removes the pool; member accounts are released (FK ON DELETE SET NULL).
func (s *AccountPoolService) Delete(ctx context.Context, id int64) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.dropUsage(id)
	return nil
}

func (s *AccountPoolService) AssignAccounts(ctx context.Context, poolID int64, accountIDs []int64) (int64, error) {
	ids, err := normalizeAccountPoolMemberIDs(accountIDs)
	if err != nil {
		return 0, err
	}
	return s.repo.AssignAccounts(ctx, poolID, ids)
}

func (s *AccountPoolService) ReleaseAccounts(ctx context.Context, accountIDs []int64) (int64, error) {
	ids, err := normalizeAccountPoolMemberIDs(accountIDs)
	if err != nil {
		return 0, err
	}
	return s.repo.ReleaseAccounts(ctx, ids)
}

func (s *AccountPoolService) ListAccountIDs(ctx context.Context, poolID int64, status string) ([]int64, error) {
	if _, err := s.repo.GetByID(ctx, poolID); err != nil {
		return nil, err
	}
	return s.repo.ListAccountIDs(ctx, poolID, strings.TrimSpace(status))
}

func validateAccountPool(pool *AccountPool) error {
	if pool.Name == "" || len([]rune(pool.Name)) > accountPoolNameMaxLen {
		return ErrAccountPoolInvalidName
	}
	if pool.Platform == "" {
		return ErrAccountPoolInvalidPlatform
	}
	return nil
}

func normalizeAccountPoolMemberIDs(accountIDs []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(accountIDs))
	ids := make([]int64, 0, len(accountIDs))
	for _, id := range accountIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, ErrAccountPoolEmptyMembers
	}
	if len(ids) > accountPoolMemberBatchMax {
		return nil, ErrAccountPoolTooManyMembers
	}
	return ids, nil
}

// cachedUsage returns the last usage snapshot and refreshes it in the background
// when stale, so listing pools never waits on usage_logs aggregation.
func (s *AccountPoolService) cachedUsage() map[int64]*AccountPoolUsage {
	if s == nil || s.usageLogRepo == nil {
		return nil
	}
	s.usageMu.RLock()
	usage, at := s.usage, s.usageAt
	s.usageMu.RUnlock()
	if at.IsZero() || s.usageTimeNow().Sub(at) >= accountPoolUsageCacheTTL {
		go s.refreshUsage()
	}
	return usage
}

func (s *AccountPoolService) dropUsage(poolID int64) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if s.usage == nil {
		return
	}
	next := make(map[int64]*AccountPoolUsage, len(s.usage))
	for id, u := range s.usage {
		if id != poolID {
			next[id] = u
		}
	}
	s.usage = next
}

func (s *AccountPoolService) refreshUsage() {
	_, _, _ = s.usageGroup.Do("usage", func() (any, error) {
		s.usageMu.RLock()
		fresh := !s.usageAt.IsZero() && s.usageTimeNow().Sub(s.usageAt) < accountPoolUsageCacheTTL
		s.usageMu.RUnlock()
		if fresh {
			return nil, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), accountPoolUsageRefreshBudget)
		defer cancel()
		usage, err := s.computeUsage(ctx)
		if err != nil {
			logger.LegacyPrintf("service.account_pool", "refresh pool usage failed: %v", err)
			return nil, err
		}
		s.usageMu.Lock()
		s.usage = usage
		s.usageAt = s.usageTimeNow()
		s.usageMu.Unlock()
		return nil, nil
	})
}

func (s *AccountPoolService) computeUsage(ctx context.Context) (map[int64]*AccountPoolUsage, error) {
	now := s.usageTimeNow()
	pools, err := s.repo.List(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*AccountPoolUsage, len(pools))
	if len(pools) == 0 {
		return out, nil
	}
	poolIDs := make([]int64, len(pools))
	for i := range pools {
		poolIDs[i] = pools[i].ID
	}
	members, err := s.repo.ListMemberRefs(ctx, poolIDs)
	if err != nil {
		return nil, err
	}

	windowHours := 24
	var grokLimit, grokGate int64
	if s.cfg != nil {
		if s.cfg.Gateway.Grok.FreeQuotaWindowHours > 0 {
			windowHours = s.cfg.Gateway.Grok.FreeQuotaWindowHours
		}
		grokLimit = s.cfg.Gateway.Grok.FreeQuotaTokenLimit
		percent := s.cfg.Gateway.Grok.FreeQuotaSoftGatePercent
		if percent < 1 || percent > 100 {
			percent = 95
		}
		grokGate = calculateGrokFreeQuotaSoftGateTokens(grokLimit, percent)
	}
	start := now.Add(-time.Duration(windowHours) * time.Hour)

	for i := range pools {
		out[pools[i].ID] = &AccountPoolUsage{WindowHours: windowHours, UpdatedAt: now}
	}
	for start0 := 0; start0 < len(members); start0 += accountPoolUsageChunkSize {
		end := start0 + accountPoolUsageChunkSize
		if end > len(members) {
			end = len(members)
		}
		chunk := members[start0:end]
		ids := make([]int64, len(chunk))
		for i := range chunk {
			ids[i] = chunk[i].ID
		}
		stats, err := queryGrokFreeQuotaWindowStats(ctx, s.usageLogRepo, ids, start)
		if err != nil {
			return nil, err
		}
		for i := range chunk {
			u := out[chunk[i].PoolID]
			if u == nil {
				continue
			}
			st := stats[chunk[i].ID]
			var tokens int64
			if st != nil {
				u.Requests += st.Requests
				u.Tokens += st.Tokens
				u.Cost += st.Cost
				tokens = st.Tokens
			}
			if chunk[i].GrokFree && grokLimit > 0 {
				if u.GrokFree == nil {
					u.GrokFree = &AccountPoolGrokFreeQuota{}
				}
				u.GrokFree.Accounts++
				u.GrokFree.UsedTokens += tokens
				u.GrokFree.LimitTokens += grokLimit
				if grokGate > 0 && tokens >= grokGate {
					u.GrokFree.NearLimit++
				}
			}
		}
	}
	return out, nil
}
