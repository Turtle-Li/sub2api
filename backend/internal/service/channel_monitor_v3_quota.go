package service

import (
	"context"
	"time"
)

type channelMonitorV3QuotaReader interface {
	GroupQuotaAccounts(context.Context, []int64) (map[int64][]Account, error)
}

// Describe current explicit Claude quota windows, never general 429s or account
// errors. A remaining API-key route means partial restriction, not pool exhaustion.
func channelMonitorV3ApplyQuota(item *ChannelMonitorV3GroupStatus, accounts []Account, now time.Time) {
	eligible, blocked := 0, 0
	var earliest *time.Time
	for i := range accounts {
		a := &accounts[i]
		if a.Platform != PlatformAnthropic || a.Status != StatusActive || !a.Schedulable || (a.AutoPauseOnExpired && a.ExpiresAt != nil && !a.ExpiresAt.After(now)) {
			continue
		}
		eligible++
		if !a.IsOAuth() {
			continue
		}
		limited, known, current := false, true, false
		var latest *time.Time
		for _, window := range anthropicThresholdCandidates(a) {
			if window.usedPercent < 100 || (window.until != nil && !window.until.After(now)) {
				continue
			}
			limited = true
			if window.until == nil {
				known = false
				continue
			}
			current = true
			if latest == nil || window.until.After(*latest) {
				latest = window.until
			}
		}
		// An undated old usage snapshot alone cannot prove a current block.
		if !limited || (!current && (a.RateLimitResetAt == nil || !a.RateLimitResetAt.After(now))) {
			continue
		}
		blocked++
		if known && latest != nil && (earliest == nil || latest.Before(*earliest)) {
			at := latest.UTC()
			earliest = &at
		}
	}
	if blocked == 0 {
		return
	}
	item.PoolQuotaState = "limited"
	if eligible == blocked {
		item.PoolQuotaState = "exhausted"
	}
	at := now.UTC()
	item.PoolQuotaObservedAt, item.PoolQuotaResetsAt = &at, earliest
}

func channelMonitorV3FactsSince(facts *ChannelMonitorV3Facts, first time.Time) *ChannelMonitorV3Facts {
	out := &ChannelMonitorV3Facts{}
	if facts == nil {
		return out
	}
	for _, fact := range facts.Metrics {
		if !fact.Slot.Before(first) {
			out.Metrics = append(out.Metrics, fact)
		}
	}
	for _, fact := range facts.Errors {
		if !fact.Slot.Before(first) {
			out.Errors = append(out.Errors, fact)
		}
	}
	for _, fact := range facts.Latency {
		if !fact.Slot.Before(first) {
			out.Latency = append(out.Latency, fact)
		}
	}
	return out
}
