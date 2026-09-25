package service

import (
	"sort"
	"sync"
	"time"
)

// BPS 执行结果。
const (
	BPSOutcomeSuccess          = "success"            // BPS 完成本轮响应
	BPSOutcomeFallback         = "fallback"           // 输出前失败，已回退原路径
	BPSOutcomeSkipped          = "skipped"            // 请求形态不适用 BPS，直接走原路径
	BPSOutcomeErrorAfterOutput = "error_after_output" // 已向客户端输出后中断，无法回退
)

const (
	bpsMonitorMaxEvents   = 200
	bpsMonitorMaxFailures = 100 // 回退/输出后中断单独保留，避免被高频成功事件挤掉
	bpsMonitorDetailLimit = 500
)

// BPSEvent 是一次 BPS 执行记录，不含任何请求内容。
type BPSEvent struct {
	Time            time.Time `json:"time"`
	AccountID       int64     `json:"account_id"`
	Model           string    `json:"model,omitempty"`
	Outcome         string    `json:"outcome"`
	Reason          string    `json:"reason,omitempty"`
	Detail          string    `json:"detail,omitempty"`
	StatusCode      int       `json:"status_code,omitempty"`
	RequestedEffort string    `json:"requested_effort,omitempty"`
	AppliedEffort   string    `json:"applied_effort,omitempty"`
	DurationMs      int64     `json:"duration_ms,omitempty"`
}

// BPSAccountStats 是单个账号自进程启动以来的 BPS 统计。
type BPSAccountStats struct {
	AccountID         int64            `json:"account_id"`
	Successes         int64            `json:"successes"`
	Fallbacks         int64            `json:"fallbacks"`
	ErrorsAfterOutput int64            `json:"errors_after_output"`
	Skipped           map[string]int64 `json:"skipped"`
	LastSuccessAt     *time.Time       `json:"last_success_at,omitempty"`
	LastFailureAt     *time.Time       `json:"last_failure_at,omitempty"`
	LastFailureReason string           `json:"last_failure_reason,omitempty"`
	LastStatusCode    int              `json:"last_status_code,omitempty"`
	BreakerOpenUntil  *time.Time       `json:"breaker_open_until,omitempty"`
	BreakerFailures   int              `json:"breaker_failures"`
}

// BPSMonitorSnapshot 是监控快照。数据只保存在当前进程内，重启或切换蓝绿实例后清零。
type BPSMonitorSnapshot struct {
	StartedAt time.Time         `json:"started_at"`
	Accounts  []BPSAccountStats `json:"accounts"`
	Events    []BPSEvent        `json:"events"`
	Failures  []BPSEvent        `json:"failures"`
}

type bpsMonitorStore struct {
	mu        sync.Mutex
	startedAt time.Time
	accounts  map[int64]*BPSAccountStats
	events    []BPSEvent
	next      int
	failures  []BPSEvent
	failNext  int
	now       func() time.Time
}

var bpsMonitor = newBPSMonitorStore()

func newBPSMonitorStore() *bpsMonitorStore {
	return &bpsMonitorStore{startedAt: time.Now(), accounts: map[int64]*BPSAccountStats{}, now: time.Now}
}

func (m *bpsMonitorStore) statsLocked(accountID int64) *BPSAccountStats {
	stats := m.accounts[accountID]
	if stats == nil {
		stats = &BPSAccountStats{AccountID: accountID, Skipped: map[string]int64{}}
		m.accounts[accountID] = stats
	}
	return stats
}

// record 记录一次执行结果。"unsupported_model" 这类高频跳过只计数、不进事件列表。
func (m *bpsMonitorStore) record(event BPSEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if event.Time.IsZero() {
		event.Time = m.now()
	}
	event.Detail = truncateString(sanitizeUpstreamErrorMessage(event.Detail), bpsMonitorDetailLimit)
	stats := m.statsLocked(event.AccountID)
	at := event.Time
	switch event.Outcome {
	case BPSOutcomeSuccess:
		stats.Successes++
		stats.LastSuccessAt = &at
	case BPSOutcomeFallback, BPSOutcomeErrorAfterOutput:
		if event.Outcome == BPSOutcomeFallback {
			stats.Fallbacks++
		} else {
			stats.ErrorsAfterOutput++
		}
		stats.LastFailureAt = &at
		stats.LastFailureReason = event.Reason
		if event.Detail != "" {
			stats.LastFailureReason += ": " + event.Detail
		}
		stats.LastStatusCode = event.StatusCode
		m.failures, m.failNext = appendBPSEventRing(m.failures, m.failNext, event, bpsMonitorMaxFailures)
	case BPSOutcomeSkipped:
		stats.Skipped[event.Reason]++
		if event.Reason == bpsSkipUnsupportedModel {
			return
		}
	}
	m.events, m.next = appendBPSEventRing(m.events, m.next, event, bpsMonitorMaxEvents)
}

func appendBPSEventRing(ring []BPSEvent, next int, event BPSEvent, limit int) ([]BPSEvent, int) {
	if len(ring) < limit {
		return append(ring, event), next
	}
	ring[next] = event
	return ring, (next + 1) % limit
}

// newestBPSEvents 按时间倒序复制环形缓冲。
func newestBPSEvents(ring []BPSEvent, next int) []BPSEvent {
	result := make([]BPSEvent, 0, len(ring))
	for i := range ring {
		result = append(result, ring[(next+len(ring)-1-i)%len(ring)])
	}
	return result
}

// snapshot 返回统计副本，事件按时间倒序。
func (m *bpsMonitorStore) snapshot(breaker *bpsCircuitBreaker) BPSMonitorSnapshot {
	m.mu.Lock()
	result := BPSMonitorSnapshot{StartedAt: m.startedAt, Accounts: make([]BPSAccountStats, 0, len(m.accounts))}
	for _, stats := range m.accounts {
		copied := *stats
		copied.Skipped = make(map[string]int64, len(stats.Skipped))
		for reason, count := range stats.Skipped {
			copied.Skipped[reason] = count
		}
		result.Accounts = append(result.Accounts, copied)
	}
	result.Events = newestBPSEvents(m.events, m.next)
	result.Failures = newestBPSEvents(m.failures, m.failNext)
	m.mu.Unlock()

	for i := range result.Accounts {
		failures, openUntil := breaker.state(result.Accounts[i].AccountID)
		result.Accounts[i].BreakerFailures = failures
		result.Accounts[i].BreakerOpenUntil = openUntil
	}
	sort.Slice(result.Accounts, func(i, j int) bool { return result.Accounts[i].AccountID < result.Accounts[j].AccountID })
	return result
}

// BPSUpstreamMonitorSnapshot 返回当前进程的 BPS 执行统计，附带熔断状态。
func BPSUpstreamMonitorSnapshot() BPSMonitorSnapshot {
	return bpsMonitor.snapshot(bpsBreaker)
}

// ResetBPSUpstreamBreaker 手动解除账号熔断。
func ResetBPSUpstreamBreaker(accountID int64) {
	bpsBreaker.recordSuccess(accountID)
}

// BPSUpstreamPolicy 描述面板需要展示的静态路由策略。
type BPSUpstreamPolicy struct {
	Models                 []string `json:"models"`
	BreakerThreshold       int      `json:"breaker_threshold"`
	BreakerOpenSeconds     int64    `json:"breaker_open_seconds"`
	ImmediateBreakerStatus []int    `json:"immediate_breaker_status"`
}

func BPSUpstreamPolicyInfo() BPSUpstreamPolicy {
	models := make([]string, 0, len(bpsUpstreamModels))
	for model := range bpsUpstreamModels {
		models = append(models, model)
	}
	sort.Strings(models)
	return BPSUpstreamPolicy{
		Models:                 models,
		BreakerThreshold:       bpsBreakerThreshold,
		BreakerOpenSeconds:     int64(bpsBreakerOpenDuration / time.Second),
		ImmediateBreakerStatus: []int{401, 403, 429},
	}
}
