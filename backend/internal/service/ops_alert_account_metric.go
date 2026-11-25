package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

type opsAlertMetricResult struct {
	value                float64
	available            bool
	match                *OpsAccountUpstreamErrorMatch
	accountErrorIDs      []int64
	accountErrorNames    map[int64]string
	latestErrorAccountID int64
}

func (m opsAlertMetricResult) eventPlatform(scopePlatform string) string {
	if scopePlatform == "" && m.match != nil {
		return m.match.LatestPlatform
	}
	return scopePlatform
}

func (m opsAlertMetricResult) dimensions(platform string, groupID *int64, region *string) map[string]any {
	dimensions := buildOpsAlertDimensions(m.eventPlatform(platform), groupID)
	if dimensions == nil && (region != nil || m.match != nil || m.accountErrorIDs != nil) {
		dimensions = make(map[string]any)
	}
	if region != nil {
		dimensions["region"] = *region
	}
	if m.match != nil && m.match.Count > 0 {
		if m.match.LatestAccountID > 0 {
			dimensions["latest_account_id"] = m.match.LatestAccountID
		}
		dimensions["latest_upstream_status_code"] = m.match.LatestUpstreamStatusCode
		if IsValidProviderErrorCode(m.match.LatestProviderCode) {
			dimensions["latest_provider_code"] = m.match.LatestProviderCode
		}
		dimensions["matched_account_ids"] = m.match.MatchedAccountIDs
	}
	if m.accountErrorIDs != nil {
		dimensions["matched_account_ids"] = m.accountErrorIDs
		if id := m.latestErrorAccountID; id > 0 {
			dimensions["latest_account_id"] = id
			dimensions["latest_account_name"] = m.accountErrorNames[id]
		}
	}
	return dimensions
}

// accountErrorEscalation identifies the newest addition to a still-breached rule.
// Legacy count events have no account identities to compare, so only a higher
// count proves an addition. In that case use the highest currently matching ID.
func (m opsAlertMetricResult) accountErrorEscalation(metric string, active *OpsAlertEvent) (int64, bool) {
	if len(m.accountErrorIDs) == 0 {
		return 0, false
	}
	previous, ok := active.Dimensions["matched_account_ids"]
	if !ok {
		return m.latestErrorAccountID, metric == "account_error_count" && active.MetricValue != nil && m.value > *active.MetricValue
	}
	seen := make(map[int64]struct{})
	switch ids := previous.(type) {
	case []int64:
		for _, id := range ids {
			seen[id] = struct{}{}
		}
	case []int:
		for _, id := range ids {
			seen[int64(id)] = struct{}{}
		}
	case []any:
		for _, value := range ids {
			id := opsAlertDimensionInt(value)
			if id <= 0 {
				return 0, false
			}
			seen[id] = struct{}{}
		}
	default:
		return 0, false
	}
	for i := len(m.accountErrorIDs) - 1; i >= 0; i-- {
		id := m.accountErrorIDs[i]
		if _, matched := seen[id]; !matched {
			return id, true
		}
	}
	return 0, false
}

func opsAlertRequiresSystemMetrics(metric string) bool {
	switch metric {
	case "cpu_usage_percent", "memory_usage_percent", "concurrency_queue_depth":
		return true
	default:
		return false
	}
}

func (s *OpsAlertEvaluatorService) computeRuleMetric(
	ctx context.Context, rule *OpsAlertRule, systemMetrics *OpsSystemMetricsSnapshot,
	start, end time.Time, platform string, groupID *int64,
) (opsAlertMetricResult, error) {
	if rule == nil {
		return opsAlertMetricResult{}, nil
	}
	metric := strings.TrimSpace(rule.MetricType)
	if !opsAlertAccountScope(metric) {
		val, available, err := s.computeOtherRuleMetric(ctx, rule, systemMetrics, start, end, platform, groupID)
		return opsAlertMetricResult{value: val, available: available}, err
	}
	filter, err := parseOpsAccountAlertFilters(metric, rule.Filters)
	if err != nil {
		return opsAlertMetricResult{}, err
	}
	if metric == OpsAlertMetricAccountUpstreamError {
		if s.opsRepo == nil {
			return opsAlertMetricResult{}, fmt.Errorf("ops repository unavailable")
		}
		filter.Start, filter.End = start, end
		match, err := s.opsRepo.CountAccountUpstreamErrors(ctx, filter)
		if err != nil {
			return opsAlertMetricResult{}, err
		}
		return opsAlertMetricResult{value: float64(match.Count), available: true, match: &match}, nil
	}
	if s.opsService == nil {
		return opsAlertMetricResult{}, nil
	}
	availability, err := s.opsService.GetAccountAvailability(ctx, platform, groupID)
	if err != nil || availability == nil {
		return opsAlertMetricResult{}, err
	}
	return computeScopedAccountStatusMetric(metric, availability.Accounts, filter.AccountIDs, time.Now()), nil
}

func computeScopedAccountStatusMetric(metric string, accounts map[int64]*AccountAvailability, accountIDs []int64, now time.Time) opsAlertMetricResult {
	if len(accountIDs) > 0 {
		selected := make(map[int64]*AccountAvailability, len(accountIDs))
		for _, accountID := range accountIDs {
			if account := accounts[accountID]; account != nil {
				selected[accountID] = account
			}
		}
		accounts = selected
	}
	if metric == "account_error_count" || metric == "account_error_ratio" {
		result := opsAlertMetricResult{available: true, accountErrorIDs: []int64{}, accountErrorNames: make(map[int64]string)}
		total := 0
		for id, account := range accounts {
			if account == nil {
				continue
			}
			total++
			if account.HasError && account.TempUnschedulableUntil == nil {
				result.accountErrorIDs = append(result.accountErrorIDs, id)
				result.accountErrorNames[id] = account.AccountName
			}
		}
		slices.Sort(result.accountErrorIDs)
		if n := len(result.accountErrorIDs); n > 0 {
			result.latestErrorAccountID = result.accountErrorIDs[n-1]
		}
		result.value = float64(len(result.accountErrorIDs))
		if metric == "account_error_ratio" && total > 0 {
			result.value = result.value * 100 / float64(total)
		}
		return result
	}
	count := countAccountsByCondition(accounts, func(account *AccountAvailability) bool {
		switch metric {
		case "account_rate_limited_count":
			return account.IsRateLimited
		case "account_temp_unscheduled_count":
			return account.TempUnschedulableUntil != nil && now.Before(*account.TempUnschedulableUntil)
		case "overload_account_count":
			return account.IsOverloaded
		default:
			return false
		}
	})
	return opsAlertMetricResult{value: float64(count), available: true}
}
