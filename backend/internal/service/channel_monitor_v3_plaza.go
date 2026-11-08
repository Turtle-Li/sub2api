package service

import (
	"context"
	"errors"
	"sort"
	"time"
)

const (
	ChannelMonitorV3GroupWindowMinutes = 180
	ChannelMonitorV3GroupMinRequests   = 20
	ChannelMonitorV3GroupMinProbes     = 6
	channelMonitorV3GroupCells         = 72
	channelMonitorV3GroupInterval      = ChannelMonitorV3ProbeInterval
	channelMonitorV3CacheLookbackHours = 24
)

type ChannelMonitorV3GroupCell struct {
	Start  time.Time `json:"start"`
	Status string    `json:"status"`
	Source string    `json:"source"`
}

// Published metrics contain no traffic volumes. Real requests and inexpensive
// scheduled checks always retain independent denominators.
type ChannelMonitorV3GroupStatus struct {
	Status                            string     `json:"status"`
	Source                            string     `json:"source"`
	CheckedAt                         *time.Time `json:"checked_at"`
	ProbeEnabled                      bool       `json:"probe_enabled"`
	ProbeModel                        string     `json:"-"`
	DisplayName                       string     `json:"-"`
	RequestSuccessRate                *float64   `json:"request_success_rate"`
	ProbeSuccessRate                  *float64   `json:"probe_success_rate"`
	ProbeMetricsVersion               int        `json:"probe_metrics_version"`
	ProbeTTFTP50Ms                    *int64     `json:"probe_ttft_p50_ms"`
	ProbeLatencyP50Ms                 *int64     `json:"probe_latency_p50_ms"`
	ProbeCacheReadRate                *float64   `json:"probe_cache_read_rate"`
	ProbeCacheState                   string     `json:"probe_cache_sample_state"`
	ProbeCacheScope                   string     `json:"probe_cache_scope"`
	ProbeCacheStatistic               string     `json:"probe_cache_statistic"`
	ProbeCacheVerifiedAt              *time.Time `json:"probe_cache_verified_at"`
	ProbeCacheVerificationVersion     int        `json:"probe_cache_verification_version"`
	ProbeCacheVerificationWindowHours int        `json:"probe_cache_verification_window_hours"`
	PoolQuotaState                    string     `json:"pool_quota_state,omitempty"`
	PoolQuotaResetsAt                 *time.Time `json:"pool_quota_resets_at,omitempty"`
	PoolQuotaObservedAt               *time.Time `json:"pool_quota_observed_at,omitempty"`
	HistoryWindowMinutes              int        `json:"history_window_minutes"`
	// Retired rolling-rate field stays null so older clients cannot mislabel a peak.
	CacheReadRate      *float64                    `json:"cache_read_rate"`
	CachePeakHourRate  *float64                    `json:"cache_peak_hour_rate"`
	CacheSampleState   string                      `json:"cache_sample_state"`
	CacheScope         string                      `json:"cache_scope"`
	CacheStatistic     string                      `json:"cache_statistic"`
	CacheLookbackHours int                         `json:"cache_lookback_hours"`
	CacheHourStart     *time.Time                  `json:"cache_hour_start"`
	CacheHourEnd       *time.Time                  `json:"cache_hour_end"`
	TTFTP50Ms          *int64                      `json:"ttft_p50_ms"`
	RequestSampleState string                      `json:"request_sample_state"`
	ProbeSampleState   string                      `json:"probe_sample_state"`
	WindowMinutes      int                         `json:"window_minutes"`
	MinRequests        int                         `json:"min_requests"`
	Cells              []ChannelMonitorV3GroupCell `json:"cells"`
}

// Token buckets match the dashboard's usage_logs contract and are never public.
type ChannelMonitorV3RequestCache struct {
	HourStart                                                   time.Time
	Requests, InputTokens, CacheCreationTokens, CacheReadTokens int64
}

// PlazaStatuses accepts only IDs authorized for public card publication.
// Success/history stay group-scoped; cache has an explicit account-pool scope.
func (s *ChannelMonitorV3Service) PlazaStatuses(ctx context.Context, groupIDs []int64, anonymous bool) (map[int64]*ChannelMonitorV3GroupStatus, error) {
	out := map[int64]*ChannelMonitorV3GroupStatus{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if anonymous && !cfg.PublicEnabled {
		return out, nil
	}
	now := s.now().UTC()
	interval := channelMonitorV3GroupInterval
	last := now.Truncate(interval)
	first := last.Add(-time.Duration(channelMonitorV3GroupCells-1) * interval)
	metricsFirst := last.Add(-time.Duration(ChannelMonitorV3GroupWindowMinutes/int(interval/time.Minute)-1) * interval)
	facts, err := s.repo.SlotFacts(ctx, groupIDs, first, last.Add(interval), interval, true)
	if err != nil {
		return nil, err
	}
	metricFacts := channelMonitorV3FactsSince(facts, metricsFirst)
	through, err := s.repo.DataThrough(ctx)
	if err != nil {
		return nil, err
	}
	passiveFresh := through != nil && !through.After(now) && now.Sub(*through) <= 10*time.Minute
	cacheTo := now.Truncate(time.Hour)
	cacheFrom := cacheTo.Add(-channelMonitorV3CacheLookbackHours * time.Hour)
	requestCache, err := s.repo.GroupRequestCache(ctx, groupIDs, cacheFrom, cacheTo)
	if err != nil {
		return nil, err
	}
	var probes []ChannelMonitorV3GroupProbeResult
	if repo, ok := s.repo.(ChannelMonitorV3GroupProbeRepository); ok {
		probes, err = repo.GroupProbes(ctx, groupIDs, now.Add(-24*time.Hour), now.Add(time.Nanosecond))
		if err != nil {
			return nil, err
		}
	}
	targets := map[int64]ChannelMonitorV3GroupProbeTarget{}
	for _, target := range cfg.GroupProbe.Groups {
		targets[target.GroupID] = target
	}
	byGroup := map[int64][]ChannelMonitorV3GroupProbeResult{}
	for _, probe := range probes {
		byGroup[probe.GroupID] = append(byGroup[probe.GroupID], probe)
	}
	quotaAccounts := map[int64][]Account{}
	if repo, ok := s.repo.(channelMonitorV3QuotaReader); ok {
		// Quota context is supplementary; a failed snapshot must not erase history.
		if accounts, err := repo.GroupQuotaAccounts(ctx, groupIDs); err == nil {
			quotaAccounts = accounts
		}
	}
	for _, id := range groupIDs {
		target := targets[id]
		item := &ChannelMonitorV3GroupStatus{Status: ChannelMonitorV3StatusUnknown, Source: "none", ProbeEnabled: cfg.GroupProbe.Enabled && target.Enabled, ProbeModel: target.Model, ProbeMetricsVersion: 1,
			DisplayName: target.DisplayName, RequestSampleState: "none", ProbeSampleState: "none", ProbeCacheState: "none", ProbeCacheScope: "group_probe", ProbeCacheStatistic: "weighted_tokens", CacheSampleState: "none", CacheScope: "shared_account_pool", CacheStatistic: "best_complete_hour", CacheLookbackHours: channelMonitorV3CacheLookbackHours, WindowMinutes: ChannelMonitorV3GroupWindowMinutes, MinRequests: max(ChannelMonitorV3GroupMinRequests, cfg.MinRequests), Cells: make([]ChannelMonitorV3GroupCell, channelMonitorV3GroupCells)}
		item.HistoryWindowMinutes = int(time.Duration(channelMonitorV3GroupCells) * interval / time.Minute)
		item.ProbeCacheVerificationVersion, item.ProbeCacheVerificationWindowHours = 1, 24
		channelMonitorV3ApplyQuota(item, quotaAccounts[id], now)
		for _, cache := range requestCache[id] {
			start := cache.HourStart.UTC()
			if start.Before(cacheFrom) || !start.Before(cacheTo) || !start.Equal(start.Truncate(time.Hour)) || cache.Requests <= 0 || cache.InputTokens < 0 || cache.CacheCreationTokens < 0 || cache.CacheReadTokens < 0 {
				continue
			}
			input := float64(cache.InputTokens) + float64(cache.CacheCreationTokens) + float64(cache.CacheReadTokens)
			if input == 0 {
				continue
			}
			if item.CacheSampleState == "none" {
				item.CacheSampleState = "insufficient"
			}
			if cache.Requests < int64(item.MinRequests) {
				continue
			}
			rate := 100 * float64(cache.CacheReadTokens) / input
			if item.CachePeakHourRate == nil || rate > *item.CachePeakHourRate || (rate == *item.CachePeakHourRate && start.After(*item.CacheHourStart)) {
				end := start.Add(time.Hour)
				item.CacheSampleState, item.CachePeakHourRate = "ready", &rate
				item.CacheHourStart, item.CacheHourEnd = &start, &end
			}
		}
		component := ChannelMonitorV3Component{GroupID: id}
		total := channelMonitorV3Accumulate(*cfg, component, metricFacts, time.Time{}, 0)[0]
		if total.counted() > 0 {
			item.RequestSampleState = "insufficient"
		}
		if !passiveFresh {
			item.RequestSampleState = "stale"
		} else if total.counted() >= int64(item.MinRequests) {
			item.RequestSampleState = "ready"
			rate := 100 * float64(total.success) / float64(total.counted())
			item.RequestSuccessRate = &rate
			var latencySamples int64
			for _, n := range total.ttft {
				latencySamples += n
			}
			if latencySamples >= int64(item.MinRequests) {
				item.TTFTP50Ms = channelMonitorV3P50(total.ttft)
			}
		}
		cells := channelMonitorV3Cells(*cfg, component, facts, first, channelMonitorV3GroupCells, interval, false)
		observations := channelMonitorV3Accumulate(*cfg, component, facts, first, channelMonitorV3GroupCells, interval)
		for i := range item.Cells {
			item.Cells[i] = ChannelMonitorV3GroupCell{Start: first.Add(time.Duration(i) * interval), Status: ChannelMonitorV3StatusUnknown, Source: "none"}
			if cells[i] != nil {
				item.Cells[i].Status, item.Cells[i].Source = cells[i].Status, "requests"
				if cells[i].Status == ChannelMonitorV3StatusInsufficient {
					item.Cells[i].Status = "observed"
					if observations[i].errors > 0 {
						item.Cells[i].Status = "errors_observed"
					}
				}
			}
		}
		var successes, count, latencySamples, ttftSamples, cacheSamples int
		var probeCacheInput, probeCacheRead float64
		probeLatency, probeTTFT := map[int64]int64{}, map[int64]int64{}
		var latestProbeAt *time.Time
		for _, probe := range byGroup[id] {
			if probe.Model != target.Model || probe.Slot.Before(now.Add(-24*time.Hour)) || probe.Slot.After(now) || probe.FinishedAt == nil || probe.FinishedAt.Before(probe.Slot) || probe.FinishedAt.After(now) || (probe.Outcome != "success" && probe.Outcome != "failure") {
				continue
			}
			if item.ProbeEnabled && probe.Outcome == "success" && probe.CacheInputTokens != nil && probe.CacheReadTokens != nil && *probe.CacheInputTokens > 0 && *probe.CacheReadTokens > 0 && *probe.CacheReadTokens <= *probe.CacheInputTokens && (item.ProbeCacheVerifiedAt == nil || probe.FinishedAt.After(*item.ProbeCacheVerifiedAt)) {
				at := *probe.FinishedAt
				item.ProbeCacheVerifiedAt = &at
			}
			if !probe.Slot.Before(first) {
				index := int(probe.Slot.Sub(first) / interval)
				if index >= 0 && index < len(item.Cells) && item.Cells[index].Source != "requests" {
					state := ChannelMonitorV3StatusDown
					if probe.Outcome == "success" {
						state = ChannelMonitorV3StatusOperational
					}
					item.Cells[index].Status, item.Cells[index].Source = state, "probe"
				}
			}
			if probe.Slot.Before(metricsFirst) {
				continue
			}
			count++
			if latestProbeAt == nil || probe.FinishedAt.After(*latestProbeAt) {
				latestProbeAt = probe.FinishedAt
			}
			if probe.Outcome == "success" {
				successes++
				if probe.LatencyMs > 0 {
					probeLatency[probe.LatencyMs]++
					latencySamples++
				}
				if probe.FirstTokenMs != nil && *probe.FirstTokenMs >= 0 && *probe.FirstTokenMs <= probe.LatencyMs {
					probeTTFT[*probe.FirstTokenMs]++
					ttftSamples++
				}
				if probe.CacheInputTokens != nil && probe.CacheReadTokens != nil && *probe.CacheInputTokens > 0 && *probe.CacheReadTokens >= 0 && *probe.CacheReadTokens <= *probe.CacheInputTokens {
					cacheSamples++
					probeCacheInput += float64(*probe.CacheInputTokens)
					probeCacheRead += float64(*probe.CacheReadTokens)
				}
			}
		}
		if count > 0 {
			item.ProbeSampleState = "insufficient"
		}
		if !item.ProbeEnabled {
			item.ProbeSampleState = "disabled"
		} else if latestProbeAt != nil && now.Sub(*latestProbeAt) > 15*time.Minute {
			item.ProbeSampleState = "stale"
		} else if count >= ChannelMonitorV3GroupMinProbes {
			rate := 100 * float64(successes) / float64(count)
			item.ProbeSuccessRate, item.ProbeSampleState = &rate, "ready"
			if latencySamples >= ChannelMonitorV3GroupMinProbes {
				item.ProbeLatencyP50Ms = channelMonitorV3P50(probeLatency)
			}
			if ttftSamples >= ChannelMonitorV3GroupMinProbes {
				item.ProbeTTFTP50Ms = channelMonitorV3P50(probeTTFT)
			}
		}
		if cacheSamples > 0 {
			item.ProbeCacheState = "insufficient"
		}
		if item.ProbeSampleState == "disabled" || item.ProbeSampleState == "stale" {
			item.ProbeCacheState = item.ProbeSampleState
		} else if item.ProbeSampleState == "ready" && cacheSamples >= ChannelMonitorV3GroupMinProbes {
			rate := 100 * probeCacheRead / probeCacheInput
			item.ProbeCacheReadRate, item.ProbeCacheState = &rate, "ready"
		}
		for i := len(item.Cells) - 1; i >= 0; i-- {
			cell := item.Cells[i]
			if cell.Source == "none" || cell.Status == ChannelMonitorV3StatusInsufficient {
				continue
			}
			if now.Sub(cell.Start) > 15*time.Minute {
				break
			}
			if cell.Source == "requests" && !passiveFresh || cell.Source == "probe" && !item.ProbeEnabled {
				continue
			}
			item.Status, item.Source = cell.Status, cell.Source
			at := cell.Start
			item.CheckedAt = &at
			if cell.Source == "probe" {
				for _, probe := range byGroup[id] {
					if probe.Model == target.Model && probe.Slot.Equal(cell.Start) && probe.FinishedAt != nil && !probe.FinishedAt.After(now) {
						item.CheckedAt = probe.FinishedAt
					}
				}
			}
			break
		}
		// A fresh check remains useful when the real-traffic collector is stale.
		if item.Source == "none" && item.ProbeEnabled {
			for _, probe := range byGroup[id] {
				if probe.Model != target.Model || probe.FinishedAt == nil || now.Sub(*probe.FinishedAt) > 15*time.Minute || probe.FinishedAt.After(now) || (probe.Outcome != "success" && probe.Outcome != "failure") {
					continue
				}
				if item.CheckedAt != nil && !probe.FinishedAt.After(*item.CheckedAt) {
					continue
				}
				item.Source, item.CheckedAt = "probe", probe.FinishedAt
				item.Status = ChannelMonitorV3StatusDown
				if probe.Outcome == "success" {
					item.Status = ChannelMonitorV3StatusOperational
				}
			}
		}
		if item.Cells[len(item.Cells)-1].Source == "none" {
			item.Cells[len(item.Cells)-1].Status = "collecting"
		}
		out[id] = item
	}
	return out, nil
}

// Selection owns publication independently of the scheduled-check switch.
// Active public groups are the candidates, never an implicit publication list.
func (s *ChannelMonitorV3Service) PublicStatusGroups(ctx context.Context, anonymous bool) ([]Group, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if anonymous && !cfg.PublicEnabled {
		return nil, ErrChannelMonitorV3NotFound
	}
	selected := make(map[int64]bool, len(cfg.GroupProbe.Groups))
	for _, target := range cfg.GroupProbe.Groups {
		selected[target.GroupID] = target.IsListed()
	}
	catalog, ok := s.groups.(interface {
		ListActive(context.Context) ([]Group, error)
	})
	if !ok {
		return nil, errors.New("status group catalog unavailable")
	}
	groups, err := catalog.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	public := make([]Group, 0, len(groups))
	for _, g := range groups {
		if selected[g.ID] && g.Status == StatusActive && !g.IsExclusive {
			public = append(public, g)
		}
	}
	sort.SliceStable(public, func(i, j int) bool {
		if public[i].Platform != public[j].Platform {
			return public[i].Platform < public[j].Platform
		}
		return public[i].Name < public[j].Name
	})
	return public, nil
}
