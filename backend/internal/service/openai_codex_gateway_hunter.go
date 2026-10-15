package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 满血网关监控：Codex 上游按网关（unified-N，由 __cflb/__oailb Cookie 指定）间歇性地给降智账号
// 满血窗口（实测 ≤240s，常见 40–200s），窗口通常跨模型、跨账号共享。本服务：
//   - 侦察：用 free 账号（scout 分组）经轮换出口请求，收集各网关的路由 Cookie。free 账号对目标模型
//     返回 400 但仍下发 Cookie，不消耗额度。每个账号只映射到固定的少量网关，账号越多网关池越大。
//   - 搜索：hunter 账号（降智账号）带不同网关 Cookie 问固定题，按回答判档（高市=满血）。
//   - 跟踪：网关一旦满血，tracker 账号每 10 秒复测，连续两轮无满血即判窗口结束。
//   - 对照：按比例插入不带 Cookie 的裸请求，满血判断必须与同期对照比较。
// 只在持有单例租约的实例上运行；状态全部在内存里，重启后重新侦察。

const (
	SettingKeyCodexGatewayHunterConfig = "codex_gateway_hunter_config"

	codexGatewayHunterLockKey      = "codex-gateway-hunter"
	codexGatewayHunterLockTTL      = time.Minute
	codexGatewayHunterSupervisorTk = 15 * time.Second
	codexGatewayHunterTrackEvery   = 10 * time.Second
	codexGatewayHunterReloadEvery  = time.Minute
	codexGatewayHunterProbeTimeout = 60 * time.Second
	codexGatewayHunterScoutTimeout = 20 * time.Second
	codexGatewayHunterBareRatio    = 0.08
	codexGatewayHunterColdMisses   = 2
	codexGatewayHunterMaxProbes    = 200
	codexGatewayHunterMaxWindows   = 100
	codexGatewayHunterErrorBody    = 2048

	// 题目与判档沿用此前所有实测：高市=满血，石破=剪枝，岸田=降智。
	codexGatewayHunterQuestion = "不要联网，不要调用工具，直接回答，你记忆中的日本首相是谁？只回答名字。"
)

const (
	GatewayTierFull     = "full"
	GatewayTierPruned   = "pruned"
	GatewayTierDegraded = "degraded"
	GatewayTierUnknown  = "unknown"
	GatewayTierError    = "error"
)

var codexGatewayNamePattern = regexp.MustCompile(`unified-\d+`)

// CodexGatewayHunterConfig 持久化在设置表；Enabled 由启动 / 停止接口维护。
type CodexGatewayHunterConfig struct {
	Enabled                 bool    `json:"enabled"`
	Model                   string  `json:"model"`
	HunterAccountIDs        []int64 `json:"hunter_account_ids"`
	TrackerAccountIDs       []int64 `json:"tracker_account_ids"`
	ScoutGroupID            int64   `json:"scout_group_id"`
	ScoutProxyID            int64   `json:"scout_proxy_id"`
	ProbeProxyID            int64   `json:"probe_proxy_id"`
	Workers                 int     `json:"workers"`
	MinProbeIntervalSeconds int     `json:"min_probe_interval_seconds"`
	ScoutIntervalSeconds    int     `json:"scout_interval_seconds"`
	TrackerDailyBudget      int     `json:"tracker_daily_budget"`
}

func defaultCodexGatewayHunterConfig() CodexGatewayHunterConfig {
	return CodexGatewayHunterConfig{
		Model:                   "gpt-6-astra",
		Workers:                 6,
		MinProbeIntervalSeconds: 45,
		ScoutIntervalSeconds:    3,
		TrackerDailyBudget:      150,
	}
}

func normalizeCodexGatewayHunterConfig(cfg CodexGatewayHunterConfig) (CodexGatewayHunterConfig, error) {
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Model == "" {
		cfg.Model = defaultCodexGatewayHunterConfig().Model
	}
	cfg.HunterAccountIDs = uniquePositiveIDs(cfg.HunterAccountIDs)
	cfg.TrackerAccountIDs = uniquePositiveIDs(cfg.TrackerAccountIDs)
	switch {
	case cfg.Workers < 1 || cfg.Workers > 16:
		return cfg, errors.New("workers must be between 1 and 16")
	case cfg.MinProbeIntervalSeconds < 10 || cfg.MinProbeIntervalSeconds > 600:
		return cfg, errors.New("min_probe_interval_seconds must be between 10 and 600")
	case cfg.ScoutIntervalSeconds < 1 || cfg.ScoutIntervalSeconds > 300:
		return cfg, errors.New("scout_interval_seconds must be between 1 and 300")
	case cfg.TrackerDailyBudget < 0 || cfg.TrackerDailyBudget > 100000:
		return cfg, errors.New("tracker_daily_budget must be between 0 and 100000")
	case cfg.ScoutGroupID < 0 || cfg.ScoutProxyID < 0 || cfg.ProbeProxyID < 0:
		return cfg, errors.New("ids must not be negative")
	}
	return cfg, nil
}

func uniquePositiveIDs(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

type CodexGatewayHunterAccount struct {
	ID                   int64    `json:"id"`
	Name                 string   `json:"name"`
	Role                 string   `json:"role"`
	PrimaryUsedPercent   *float64 `json:"primary_used_percent"`
	SecondaryUsedPercent *float64 `json:"secondary_used_percent"`
	ProbesToday          int      `json:"probes_today"`
	Retired              bool     `json:"retired"`
	RetiredReason        string   `json:"retired_reason"`
	LastError            string   `json:"last_error"`
}

type CodexGatewayHunterGateway struct {
	Gateway        string            `json:"gateway"`
	FirstSeen      int64             `json:"first_seen"`
	ScoutedAt      int64             `json:"scouted_at"`
	ScoutAccountID int64             `json:"scout_account_id"`
	Probes         int               `json:"probes"`
	Hits           int               `json:"hits"`
	LastTier       string            `json:"last_tier"`
	LastProbeAt    int64             `json:"last_probe_at"`
	HotSince       *int64            `json:"hot_since"`
	HotAccounts    map[string]string `json:"hot_accounts"`
}

type CodexGatewayHunterWindow struct {
	Gateway         string  `json:"gateway"`
	Start           int64   `json:"start"`
	End             *int64  `json:"end"`
	DurationSeconds int64   `json:"duration_seconds"`
	FoundBy         int64   `json:"found_by"`
	ConfirmedBy     []int64 `json:"confirmed_by"`
}

type CodexGatewayHunterProbe struct {
	At              int64  `json:"at"`
	Phase           string `json:"phase"`
	AccountID       int64  `json:"account_id"`
	Gateway         string `json:"gateway"`
	HTTPStatus      int    `json:"http_status"`
	Tier            string `json:"tier"`
	Answer          string `json:"answer"`
	TTFBMs          int64  `json:"ttfb_ms"`
	TotalMs         int64  `json:"total_ms"`
	ReasoningTokens *int64 `json:"reasoning_tokens"`
}

type CodexGatewayHunterCounters struct {
	Scouts         int   `json:"scouts"`
	ScoutErrors    int   `json:"scout_errors"`
	Hunts          int   `json:"hunts"`
	HuntHits       int   `json:"hunt_hits"`
	BareProbes     int   `json:"bare_probes"`
	BareHits       int   `json:"bare_hits"`
	Tracks         int   `json:"tracks"`
	TrackHits      int   `json:"track_hits"`
	Windows        int   `json:"windows"`
	CoveredSeconds int64 `json:"covered_seconds"`
	RunningSeconds int64 `json:"running_seconds"`
}

type CodexGatewayHunterState struct {
	GeneratedAt int64                       `json:"generated_at"`
	Running     bool                        `json:"running"`
	Leader      bool                        `json:"leader"`
	StartedAt   *int64                      `json:"started_at"`
	LastError   string                      `json:"last_error"`
	Config      CodexGatewayHunterConfig    `json:"config"`
	Accounts    []CodexGatewayHunterAccount `json:"accounts"`
	Gateways    []CodexGatewayHunterGateway `json:"gateways"`
	Windows     []CodexGatewayHunterWindow  `json:"windows"`
	Probes      []CodexGatewayHunterProbe   `json:"probes"`
	Counters    CodexGatewayHunterCounters  `json:"counters"`
}

// CodexHotGateway 是当前处于满血窗口的网关及其路由 Cookie，供后续把真实请求路由过去。
type CodexHotGateway struct {
	Gateway string
	Cookie  string
	Since   time.Time
}

type gatewayHunterEntry struct {
	cookie      string
	firstSeen   time.Time
	scoutedAt   time.Time
	scoutAcc    int64
	probes      int
	hits        int
	lastTier    string
	lastProbeAt time.Time
}

type gatewayHunterHot struct {
	start    time.Time
	misses   int
	accounts map[int64]string
}

type gatewayHunterAccountState struct {
	account       *Account
	role          string
	primary       *float64
	secondary     *float64
	day           string
	probesToday   int
	cooldownUntil time.Time
	retired       string
	lastError     string
}

type gatewayProbeResult struct {
	status    int
	gateway   string
	cookie    string
	answer    string
	tier      string
	ttfb      time.Duration
	total     time.Duration
	reasoning *int64
	primary   *float64
	secondary *float64
	err       string
}

type CodexGatewayHunterService struct {
	settingRepo  SettingRepository
	accountRepo  AccountRepository
	proxyRepo    ProxyRepository
	httpUpstream HTTPUpstream
	tlsFP        *TLSFingerprintProfileService
	leaderLock   *singletonJobLock
	trackEvery   time.Duration

	// call 发出一次上游请求；测试替换。
	call func(ctx context.Context, account *Account, proxyURL, prompt, cookie string, timeout time.Duration) gatewayProbeResult

	mu        sync.Mutex
	cfg       CodexGatewayHunterConfig
	running   bool
	cancelRun context.CancelFunc
	runDone   chan struct{}
	startedAt time.Time
	lastError string
	scoutURL  string
	probeURL  string
	pool      map[string]*gatewayHunterEntry
	busy      map[string]bool
	hot       map[string]*gatewayHunterHot
	accounts  map[int64]*gatewayHunterAccountState
	windows   []CodexGatewayHunterWindow
	probes    []CodexGatewayHunterProbe
	counters  CodexGatewayHunterCounters

	wake     chan struct{}
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewCodexGatewayHunterService(
	settingRepo SettingRepository,
	accountRepo AccountRepository,
	proxyRepo ProxyRepository,
	httpUpstream HTTPUpstream,
	tlsFP *TLSFingerprintProfileService,
) *CodexGatewayHunterService {
	s := &CodexGatewayHunterService{
		settingRepo:  settingRepo,
		accountRepo:  accountRepo,
		proxyRepo:    proxyRepo,
		httpUpstream: httpUpstream,
		tlsFP:        tlsFP,
		cfg:          defaultCodexGatewayHunterConfig(),
		trackEvery:   codexGatewayHunterTrackEvery,
		wake:         make(chan struct{}, 1),
		stopCh:       make(chan struct{}),
	}
	s.call = s.callUpstream
	s.resetRunStateLocked()
	return s
}

func (s *CodexGatewayHunterService) resetRunStateLocked() {
	s.pool = map[string]*gatewayHunterEntry{}
	s.busy = map[string]bool{}
	s.hot = map[string]*gatewayHunterHot{}
	s.accounts = map[int64]*gatewayHunterAccountState{}
	s.windows = nil
	s.probes = nil
	s.counters = CodexGatewayHunterCounters{}
	s.lastError = ""
}

// Start 启动监督循环：按设置决定是否抢租约运行。
func (s *CodexGatewayHunterService) Start() {
	if s == nil || s.settingRepo == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(codexGatewayHunterSupervisorTk)
		defer ticker.Stop()
		for {
			s.supervise()
			select {
			case <-ticker.C:
			case <-s.wake:
			case <-s.stopCh:
				s.stopRun()
				return
			}
		}
	}()
}

func (s *CodexGatewayHunterService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *CodexGatewayHunterService) supervise() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cfg, err := s.loadConfig(ctx)
	cancel()
	if err != nil {
		slog.Warn("codex gateway hunter: load config failed", "error", err)
		return
	}
	s.mu.Lock()
	s.cfg = cfg
	running := s.running
	s.mu.Unlock()
	if !cfg.Enabled {
		if running {
			s.stopRun()
		}
		return
	}
	if running {
		return
	}
	leaseCtx, release, acquired := s.leaderLock.try(context.Background())
	if !acquired {
		return
	}
	runCtx, cancelRun := context.WithCancel(leaseCtx)
	done := make(chan struct{})
	s.mu.Lock()
	s.resetRunStateLocked()
	s.running = true
	s.cancelRun = cancelRun
	s.runDone = done
	s.startedAt = time.Now()
	s.mu.Unlock()
	go func() {
		defer close(done)
		defer release()
		err := s.run(runCtx, cfg)
		cancelRun()
		s.mu.Lock()
		s.running = false
		if err != nil {
			s.lastError = err.Error()
		}
		s.mu.Unlock()
	}()
}

func (s *CodexGatewayHunterService) stopRun() {
	s.mu.Lock()
	cancel, done := s.cancelRun, s.runDone
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (s *CodexGatewayHunterService) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *CodexGatewayHunterService) loadConfig(ctx context.Context) (CodexGatewayHunterConfig, error) {
	cfg := defaultCodexGatewayHunterConfig()
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyCodexGatewayHunterConfig)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return cfg, nil
		}
		return cfg, err
	}
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return defaultCodexGatewayHunterConfig(), fmt.Errorf("decode config: %w", err)
	}
	return normalizeCodexGatewayHunterConfig(cfg)
}

func (s *CodexGatewayHunterService) saveConfig(ctx context.Context, cfg CodexGatewayHunterConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyCodexGatewayHunterConfig, string(b))
}

// UpdateConfig 保存配置（保留当前启用状态）；运行中则按新配置重启。
func (s *CodexGatewayHunterService) UpdateConfig(ctx context.Context, cfg CodexGatewayHunterConfig) (CodexGatewayHunterConfig, error) {
	current, err := s.loadConfig(ctx)
	if err != nil {
		return cfg, err
	}
	cfg.Enabled = current.Enabled
	cfg, err = normalizeCodexGatewayHunterConfig(cfg)
	if err != nil {
		return cfg, infraerrors.BadRequest("CODEX_GATEWAY_HUNTER_INVALID", err.Error())
	}
	if err := s.saveConfig(ctx, cfg); err != nil {
		return cfg, err
	}
	s.stopRun()
	s.nudge()
	return cfg, nil
}

func (s *CodexGatewayHunterService) SetEnabled(ctx context.Context, enabled bool) (*CodexGatewayHunterState, error) {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if enabled && len(cfg.HunterAccountIDs) == 0 {
		return nil, infraerrors.BadRequest("CODEX_GATEWAY_HUNTER_INVALID", "configure at least one hunter account first")
	}
	cfg.Enabled = enabled
	if err := s.saveConfig(ctx, cfg); err != nil {
		return nil, err
	}
	if !enabled {
		s.stopRun()
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	s.nudge()
	if enabled {
		// 给监督循环一点时间抢租约，返回的状态尽量反映"已运行"。
		time.Sleep(300 * time.Millisecond)
	}
	return s.State(ctx)
}

// HotGateways 返回当前满血窗口中的网关（按开始时间早→晚）。
func (s *CodexGatewayHunterService) HotGateways() []CodexHotGateway {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CodexHotGateway, 0, len(s.hot))
	for gw, h := range s.hot {
		if e := s.pool[gw]; e != nil {
			out = append(out, CodexHotGateway{Gateway: gw, Cookie: e.cookie, Since: h.start})
		}
	}
	slices.SortFunc(out, func(a, b CodexHotGateway) int { return a.Since.Compare(b.Since) })
	return out
}

func (s *CodexGatewayHunterService) State(ctx context.Context) (*CodexGatewayHunterState, error) {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	st := &CodexGatewayHunterState{
		GeneratedAt: now.Unix(),
		Running:     s.running,
		Leader:      s.running,
		LastError:   s.lastError,
		Config:      cfg,
		Counters:    s.counters,
	}
	if s.running {
		started := s.startedAt.Unix()
		st.StartedAt = &started
		st.Counters.RunningSeconds = int64(now.Sub(s.startedAt).Seconds())
	}
	for id, a := range s.accounts {
		item := CodexGatewayHunterAccount{ID: id, Role: a.role, PrimaryUsedPercent: a.primary, SecondaryUsedPercent: a.secondary,
			ProbesToday: a.probesToday, LastError: a.lastError}
		if a.account != nil {
			item.Name = a.account.Name
		}
		if reason := a.unavailableReason(now, cfg, s.isHunter(id, cfg)); reason != "" {
			item.Retired, item.RetiredReason = true, reason
		}
		st.Accounts = append(st.Accounts, item)
	}
	roleOrder := map[string]int{"hunter": 0, "tracker": 1, "scout": 2}
	slices.SortFunc(st.Accounts, func(a, b CodexGatewayHunterAccount) int {
		if d := roleOrder[a.Role] - roleOrder[b.Role]; d != 0 {
			return d
		}
		return int(a.ID - b.ID)
	})
	for gw, e := range s.pool {
		item := CodexGatewayHunterGateway{Gateway: gw, FirstSeen: e.firstSeen.Unix(), ScoutedAt: e.scoutedAt.Unix(),
			ScoutAccountID: e.scoutAcc, Probes: e.probes, Hits: e.hits, LastTier: e.lastTier, HotAccounts: map[string]string{}}
		if !e.lastProbeAt.IsZero() {
			item.LastProbeAt = e.lastProbeAt.Unix()
		}
		if h := s.hot[gw]; h != nil {
			since := h.start.Unix()
			item.HotSince = &since
			for id, tier := range h.accounts {
				item.HotAccounts[strconv.FormatInt(id, 10)] = tier
			}
		}
		st.Gateways = append(st.Gateways, item)
	}
	slices.SortFunc(st.Gateways, func(a, b CodexGatewayHunterGateway) int {
		if (a.HotSince != nil) != (b.HotSince != nil) {
			if a.HotSince != nil {
				return -1
			}
			return 1
		}
		if a.Hits != b.Hits {
			return b.Hits - a.Hits
		}
		return gatewayNumber(a.Gateway) - gatewayNumber(b.Gateway)
	})
	st.Windows = slices.Clone(s.windows)
	for i := range st.Windows {
		if st.Windows[i].End == nil {
			st.Windows[i].DurationSeconds = now.Unix() - st.Windows[i].Start
		}
	}
	slices.Reverse(st.Windows)
	st.Probes = slices.Clone(s.probes)
	slices.Reverse(st.Probes)
	return st, nil
}

func gatewayNumber(gw string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(gw, "unified-"))
	return n
}

func (s *CodexGatewayHunterService) isHunter(id int64, cfg CodexGatewayHunterConfig) bool {
	return slices.Contains(cfg.HunterAccountIDs, id)
}

// unavailableReason：冷却中、额度将尽、或纯 tracker 账号超出当日预算时返回原因。
func (a *gatewayHunterAccountState) unavailableReason(now time.Time, cfg CodexGatewayHunterConfig, hunter bool) string {
	if a.role == "scout" {
		return ""
	}
	if a.retired != "" {
		return a.retired
	}
	if now.Before(a.cooldownUntil) {
		return "cooldown"
	}
	if a.secondary != nil && *a.secondary >= 99 {
		return "weekly_quota"
	}
	if a.primary != nil && *a.primary >= 95 {
		return "5h_quota"
	}
	if !hunter && cfg.TrackerDailyBudget > 0 && a.day == now.Format("2006-01-02") && a.probesToday >= cfg.TrackerDailyBudget {
		return "daily_budget"
	}
	return ""
}

// ---- run loop ----

func (s *CodexGatewayHunterService) run(ctx context.Context, cfg CodexGatewayHunterConfig) error {
	if err := s.reload(ctx, cfg); err != nil {
		return err
	}
	var wg sync.WaitGroup
	spawn := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}
	spawn(func() { s.scoutLoop(ctx, cfg) })
	spawn(func() {
		ticker := time.NewTicker(codexGatewayHunterReloadEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.reload(ctx, cfg); err != nil {
					s.setError(err.Error())
				}
			}
		}
	})
	spawn(func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				if len(s.hot) > 0 {
					s.counters.CoveredSeconds++
				}
				s.mu.Unlock()
			}
		}
	})
	spawn(func() { s.trackLoop(ctx, cfg) })
	for i := 0; i < cfg.Workers; i++ {
		spawn(func() { s.huntLoop(ctx, cfg, i) })
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

func (s *CodexGatewayHunterService) setError(msg string) {
	s.mu.Lock()
	s.lastError = msg
	s.mu.Unlock()
}

// reload 重新读取账号（令牌会轮换）与出口代理。
func (s *CodexGatewayHunterService) reload(ctx context.Context, cfg CodexGatewayHunterConfig) error {
	roles := map[int64]string{}
	if cfg.ScoutGroupID > 0 {
		members, err := s.accountRepo.ListByGroup(ctx, cfg.ScoutGroupID)
		if err != nil {
			return fmt.Errorf("load scout group: %w", err)
		}
		for _, m := range members {
			roles[m.ID] = "scout"
		}
	}
	for _, id := range cfg.TrackerAccountIDs {
		roles[id] = "tracker"
	}
	for _, id := range cfg.HunterAccountIDs {
		roles[id] = "hunter"
	}
	ids := make([]int64, 0, len(roles))
	for id := range roles {
		ids = append(ids, id)
	}
	loaded, err := s.accountRepo.GetByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("load accounts: %w", err)
	}
	scoutURL, probeURL := "", ""
	if cfg.ScoutProxyID > 0 {
		p, err := s.proxyRepo.GetByID(ctx, cfg.ScoutProxyID)
		if err != nil {
			return fmt.Errorf("load scout proxy: %w", err)
		}
		scoutURL = p.URL()
	}
	if cfg.ProbeProxyID > 0 {
		p, err := s.proxyRepo.GetByID(ctx, cfg.ProbeProxyID)
		if err != nil {
			return fmt.Errorf("load probe proxy: %w", err)
		}
		probeURL = p.URL()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scoutURL, s.probeURL = scoutURL, probeURL
	seen := map[int64]bool{}
	for _, a := range loaded {
		if a == nil {
			continue
		}
		seen[a.ID] = true
		st := s.accounts[a.ID]
		if st == nil {
			st = &gatewayHunterAccountState{}
			s.accounts[a.ID] = st
		}
		st.account, st.role, st.retired = a, roles[a.ID], ""
		switch {
		case !a.IsOpenAIOAuthLike() || a.GetOpenAIAccessToken() == "" || a.GetChatGPTAccountID() == "":
			st.retired = "no_credentials"
		case a.Status != StatusActive:
			st.retired = "inactive"
		}
	}
	for id := range s.accounts {
		if !seen[id] {
			delete(s.accounts, id)
		}
	}
	return nil
}

func (s *CodexGatewayHunterService) pickAccount(role string, cfg CodexGatewayHunterConfig, index int) *Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var pool []*Account
	for id, a := range s.accounts {
		if a.account == nil || a.role != role || a.unavailableReason(now, cfg, s.isHunter(id, cfg)) != "" {
			continue
		}
		pool = append(pool, a.account)
	}
	if len(pool) == 0 {
		return nil
	}
	slices.SortFunc(pool, func(a, b *Account) int { return int(a.ID - b.ID) })
	if index < 0 {
		return pool[rand.IntN(len(pool))]
	}
	return pool[index%len(pool)]
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (s *CodexGatewayHunterService) scoutLoop(ctx context.Context, cfg CodexGatewayHunterConfig) {
	for {
		if acc := s.pickAccount("scout", cfg, -1); acc != nil {
			s.mu.Lock()
			proxyURL := s.scoutURL
			s.mu.Unlock()
			r := s.call(ctx, acc, proxyURL, "hi", "", codexGatewayHunterScoutTimeout)
			s.mu.Lock()
			s.counters.Scouts++
			if r.gateway != "" && r.cookie != "" {
				now := time.Now()
				e := s.pool[r.gateway]
				if e == nil {
					e = &gatewayHunterEntry{firstSeen: now}
					s.pool[r.gateway] = e
				}
				e.cookie, e.scoutedAt, e.scoutAcc = r.cookie, now, acc.ID
			} else {
				s.counters.ScoutErrors++
				if st := s.accounts[acc.ID]; st != nil {
					st.lastError = r.errorSummary()
				}
			}
			s.mu.Unlock()
		}
		if !sleepCtx(ctx, time.Duration(cfg.ScoutIntervalSeconds)*time.Second) {
			return
		}
	}
}

// pickGateway 选最久未测、非忙、非满血且超过最小间隔的网关；按比例返回 "" 表示裸对照。
func (s *CodexGatewayHunterService) pickGateway(cfg CodexGatewayHunterConfig) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	// 按累计计数控制对照比例；网关都在冷却时工作线程每秒重试，按概率抽会让对照偏多。
	bareDue := float64(s.counters.BareProbes) < codexGatewayHunterBareRatio*float64(s.counters.Hunts+s.counters.BareProbes+1)
	if bareDue && !s.busy[""] {
		s.busy[""] = true
		return "", true
	}
	minGap := time.Duration(cfg.MinProbeIntervalSeconds) * time.Second
	best := ""
	var bestAt time.Time
	for gw, e := range s.pool {
		if s.busy[gw] || s.hot[gw] != nil || now.Sub(e.lastProbeAt) < minGap {
			continue
		}
		if best == "" || e.lastProbeAt.Before(bestAt) {
			best, bestAt = gw, e.lastProbeAt
		}
	}
	if best == "" {
		return "", false
	}
	s.busy[best] = true
	s.pool[best].lastProbeAt = now
	return best, true
}

func (s *CodexGatewayHunterService) huntLoop(ctx context.Context, cfg CodexGatewayHunterConfig, index int) {
	for ctx.Err() == nil {
		acc := s.pickAccount("hunter", cfg, index)
		if acc == nil {
			if !sleepCtx(ctx, 30*time.Second) {
				return
			}
			continue
		}
		gw, ok := s.pickGateway(cfg)
		if !ok {
			if !sleepCtx(ctx, time.Second) {
				return
			}
			continue
		}
		phase := "hunt"
		if gw == "" {
			phase = "bare"
		}
		r := s.probe(ctx, acc, gw, phase)
		s.mu.Lock()
		delete(s.busy, gw)
		if gw != "" && r.tier == GatewayTierFull && s.hot[gw] == nil {
			now := time.Now()
			s.hot[gw] = &gatewayHunterHot{start: now, accounts: map[int64]string{acc.ID: r.tier}}
			s.counters.Windows++
			s.windows = append(s.windows, CodexGatewayHunterWindow{Gateway: gw, Start: now.Unix(), FoundBy: acc.ID, ConfirmedBy: []int64{acc.ID}})
			if len(s.windows) > codexGatewayHunterMaxWindows {
				s.windows = s.windows[len(s.windows)-codexGatewayHunterMaxWindows:]
			}
		}
		s.mu.Unlock()
	}
}

func (s *CodexGatewayHunterService) trackLoop(ctx context.Context, cfg CodexGatewayHunterConfig) {
	for sleepCtx(ctx, s.trackEvery) {
		s.mu.Lock()
		gws := make([]string, 0, len(s.hot))
		for gw := range s.hot {
			gws = append(gws, gw)
		}
		var trackers []*Account
		now := time.Now()
		for _, id := range cfg.TrackerAccountIDs {
			if a := s.accounts[id]; a != nil && a.account != nil && a.unavailableReason(now, cfg, s.isHunter(id, cfg)) == "" {
				trackers = append(trackers, a.account)
			}
		}
		s.mu.Unlock()
		for _, gw := range gws {
			results := make([]gatewayProbeResult, len(trackers))
			var wg sync.WaitGroup
			for i, acc := range trackers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					results[i] = s.probe(ctx, acc, gw, "track")
				}()
			}
			wg.Wait()
			if ctx.Err() != nil {
				return
			}
			s.mu.Lock()
			h := s.hot[gw]
			if h == nil {
				s.mu.Unlock()
				continue
			}
			full := false
			for i, r := range results {
				h.accounts[trackers[i].ID] = r.tier
				if r.tier == GatewayTierFull {
					full = true
					s.confirmWindowLocked(gw, h.start, trackers[i].ID)
				}
			}
			if full {
				h.misses = 0
			} else {
				h.misses++
			}
			if h.misses >= codexGatewayHunterColdMisses || len(trackers) == 0 {
				s.closeWindowLocked(gw, h.start, time.Now())
				delete(s.hot, gw)
				if e := s.pool[gw]; e != nil {
					e.lastProbeAt = time.Now()
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *CodexGatewayHunterService) findWindowLocked(gw string, start time.Time) *CodexGatewayHunterWindow {
	for i := len(s.windows) - 1; i >= 0; i-- {
		if s.windows[i].Gateway == gw && s.windows[i].Start == start.Unix() {
			return &s.windows[i]
		}
	}
	return nil
}

func (s *CodexGatewayHunterService) confirmWindowLocked(gw string, start time.Time, accountID int64) {
	if w := s.findWindowLocked(gw, start); w != nil && !slices.Contains(w.ConfirmedBy, accountID) {
		w.ConfirmedBy = append(w.ConfirmedBy, accountID)
	}
}

func (s *CodexGatewayHunterService) closeWindowLocked(gw string, start, end time.Time) {
	if w := s.findWindowLocked(gw, start); w != nil {
		e := end.Unix()
		w.End = &e
		w.DurationSeconds = e - w.Start
	}
}

// probe 用降智账号问题目；gw 为空表示不带 Cookie 的裸对照。
func (s *CodexGatewayHunterService) probe(ctx context.Context, acc *Account, gw, phase string) gatewayProbeResult {
	s.mu.Lock()
	cookie := ""
	if e := s.pool[gw]; gw != "" && e != nil {
		cookie = e.cookie
	}
	proxyURL := s.probeURL
	s.mu.Unlock()

	r := s.call(ctx, acc, proxyURL, codexGatewayHunterQuestion, cookie, codexGatewayHunterProbeTimeout)
	if ctx.Err() != nil {
		return r
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.accounts[acc.ID]; st != nil {
		day := now.Format("2006-01-02")
		if st.day != day {
			st.day, st.probesToday = day, 0
		}
		st.probesToday++
		if r.primary != nil {
			st.primary = r.primary
		}
		if r.secondary != nil {
			st.secondary = r.secondary
		}
		st.lastError = r.errorSummary()
		if r.status == http.StatusTooManyRequests {
			st.cooldownUntil = now.Add(15 * time.Minute)
		}
	}
	switch phase {
	case "hunt":
		s.counters.Hunts++
		if r.tier == GatewayTierFull {
			s.counters.HuntHits++
		}
	case "bare":
		s.counters.BareProbes++
		if r.tier == GatewayTierFull {
			s.counters.BareHits++
		}
	case "track":
		s.counters.Tracks++
		if r.tier == GatewayTierFull {
			s.counters.TrackHits++
		}
	}
	if e := s.pool[gw]; gw != "" && e != nil {
		e.probes++
		e.lastTier = r.tier
		e.lastProbeAt = now
		if r.tier == GatewayTierFull {
			e.hits++
		}
	}
	label := gw
	if label == "" {
		label = "bare"
	}
	s.probes = append(s.probes, CodexGatewayHunterProbe{At: now.Unix(), Phase: phase, AccountID: acc.ID, Gateway: label,
		HTTPStatus: r.status, Tier: r.tier, Answer: r.answer, TTFBMs: r.ttfb.Milliseconds(), TotalMs: r.total.Milliseconds(),
		ReasoningTokens: r.reasoning})
	if len(s.probes) > codexGatewayHunterMaxProbes {
		s.probes = s.probes[len(s.probes)-codexGatewayHunterMaxProbes:]
	}
	return r
}

func (r gatewayProbeResult) errorSummary() string {
	if r.err != "" {
		return r.err
	}
	if r.status != 0 && r.status != http.StatusOK && r.status != http.StatusBadRequest {
		return "HTTP " + strconv.Itoa(r.status)
	}
	return ""
}

// ---- upstream ----

func (s *CodexGatewayHunterService) callUpstream(ctx context.Context, account *Account, proxyURL, prompt, cookie string, timeout time.Duration) gatewayProbeResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s.mu.Lock()
	model := s.cfg.Model
	s.mu.Unlock()
	payload := map[string]any{
		"model":  model,
		"stream": true,
		"store":  false,
		// 随机前缀避免命中提示缓存（缓存命中会掩盖网关差异）。
		"instructions": "[" + uuid.NewString() + "] You are a coding assistant.",
		"input": []map[string]any{{
			"role":    "user",
			"content": []map[string]any{{"type": "input_text", "text": prompt}},
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return gatewayProbeResult{tier: GatewayTierError, err: err.Error()}
	}
	req, err := http.NewRequestWithContext(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileOpenAI), http.MethodPost, chatgptCodexAPIURL, bytes.NewReader(body))
	if err != nil {
		return gatewayProbeResult{tier: GatewayTierError, err: err.Error()}
	}
	req.Host = "chatgpt.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+account.GetOpenAIAccessToken())
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	canonical := resolveCodexOutboundIdentity("")
	req.Header.Set("Originator", canonical.originator)
	req.Header.Set("User-Agent", canonical.userAgent)
	req.Header.Set("Session-Id", uuid.NewString())
	setOpenAIChatGPTAccountHeaders(req.Header, account)
	enforceCodexIdentityHeadersWithUA(req.Header, "")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	started := time.Now()
	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.tlsFP.ResolveTLSProfile(account))
	if err != nil {
		return gatewayProbeResult{tier: GatewayTierError, err: sanitizeGatewayHunterError(err.Error()), total: time.Since(started)}
	}
	defer func() { _ = resp.Body.Close() }()
	r := gatewayProbeResult{status: resp.StatusCode, ttfb: time.Since(started)}
	r.gateway, r.cookie = gatewayCookieFromHeader(resp.Header)
	r.primary = parsePercentHeader(resp.Header.Get("x-codex-primary-used-percent"))
	r.secondary = parsePercentHeader(resp.Header.Get("x-codex-secondary-used-percent"))
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, codexGatewayHunterErrorBody))
		r.tier = GatewayTierError
		if resp.StatusCode != http.StatusBadRequest {
			r.err = sanitizeGatewayHunterError(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, extractUpstreamErrorMessage(errBody)))
		}
		r.total = time.Since(started)
		return r
	}
	out, streamErr := parseCodexDegradationProbeStream(resp.Body)
	r.total = time.Since(started)
	if out != nil {
		r.answer = truncateRunes(strings.TrimSpace(out.Content), 32)
		if out.ReasoningTokens > 0 || streamErr == nil {
			rt := int64(out.ReasoningTokens)
			r.reasoning = &rt
		}
	}
	r.tier = classifyGatewayHunterAnswer(r.answer)
	if streamErr != nil && r.answer == "" {
		r.tier = GatewayTierError
		r.err = sanitizeGatewayHunterError(streamErr.Error())
	}
	return r
}

func classifyGatewayHunterAnswer(answer string) string {
	switch {
	case strings.Contains(answer, "高市"):
		return GatewayTierFull
	case strings.Contains(answer, "石破"):
		return GatewayTierPruned
	case strings.Contains(answer, "岸田"):
		return GatewayTierDegraded
	}
	return GatewayTierUnknown
}

// gatewayCookieFromHeader 取出 __cflb/__oailb 并从 __oailb JWT 的载荷里解析 unified-N。
func gatewayCookieFromHeader(h http.Header) (string, string) {
	values := map[string]string{}
	for _, line := range h.Values("Set-Cookie") {
		pair, _, _ := strings.Cut(line, ";")
		name, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok && (name == "__cflb" || name == "__oailb") && value != "" {
			values[name] = value
		}
	}
	gw := gatewayFromOAILB(values["__oailb"])
	if gw == "" {
		return "", ""
	}
	parts := make([]string, 0, 2)
	for _, name := range []string{"__cflb", "__oailb"} {
		if v := values[name]; v != "" {
			parts = append(parts, name+"="+v)
		}
	}
	return gw, strings.Join(parts, "; ")
}

func gatewayFromOAILB(token string) string {
	segments := strings.Split(token, ".")
	if len(segments) < 2 {
		return ""
	}
	claims, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(segments[1], "="))
	if err != nil {
		return ""
	}
	return codexGatewayNamePattern.FindString(string(claims))
}

func parsePercentHeader(v string) *float64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil
	}
	return &f
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// 错误信息可能回显代理地址（含凭据），只保留首行并去掉 URL。
var gatewayHunterURLPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://\S+`)

func sanitizeGatewayHunterError(msg string) string {
	msg, _, _ = strings.Cut(msg, "\n")
	msg = gatewayHunterURLPattern.ReplaceAllString(msg, "<url>")
	return truncateRunes(msg, 160)
}
