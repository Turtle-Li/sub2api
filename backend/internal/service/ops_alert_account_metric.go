package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type opsAlertMetricResult struct {
	value     float64
	available bool
	match     *OpsAccountUpstreamErrorMatch
}

func (m opsAlertMetricResult) eventPlatform(scopePlatform string) string {
	if scopePlatform == "" && m.match != nil {
		return m.match.LatestPlatform
	}
	return scopePlatform
}

func (m opsAlertMetricResult) dimensions(platform string, groupID *int64, region *string) map[string]any {
	dimensions := buildOpsAlertDimensions(m.eventPlatform(platform), groupID)
	if dimensions == nil && (region != nil || m.match != nil) {
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
	return dimensions
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
	count := countAccountsByCondition(accounts, func(account *AccountAvailability) bool {
		switch metric {
		case "account_error_count", "account_error_ratio":
			return account.HasError && account.TempUnschedulableUntil == nil
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
	value := float64(count)
	if metric == "account_error_ratio" {
		if total := countAccountsByCondition(accounts, func(*AccountAvailability) bool { return true }); total > 0 {
			value = value * 100 / float64(total)
		}
	}
	return opsAlertMetricResult{value: value, available: true}
}
