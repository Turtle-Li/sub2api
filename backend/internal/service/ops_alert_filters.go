package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const OpsAlertMetricAccountUpstreamError = "account_upstream_error"

type OpsAccountUpstreamErrorFilter struct {
	Start, End          time.Time
	Platform            string
	GroupID             *int64
	AccountIDs          []int64  `json:"account_ids"`
	UpstreamStatusCodes []int    `json:"upstream_status_codes"`
	ProviderCodes       []string `json:"provider_codes"`
	ErrorKeywords       []string `json:"error_keywords"`
}

func (f OpsAccountUpstreamErrorFilter) Selective() bool {
	return len(f.AccountIDs) > 0 || (f.GroupID != nil && *f.GroupID > 0) || len(f.UpstreamStatusCodes) > 0 || len(f.ProviderCodes) > 0
}

type OpsAccountUpstreamErrorMatch struct {
	Count                    int64
	LatestAccountID          int64
	LatestUpstreamStatusCode int
	LatestProviderCode       string
	LatestPlatform           string
	MatchedAccountIDs        []int64
}

func opsAlertAccountScope(metric string) bool {
	switch metric {
	case OpsAlertMetricAccountUpstreamError, "account_error_count", "account_rate_limited_count",
		"account_temp_unscheduled_count", "overload_account_count", "account_error_ratio":
		return true
	default:
		return false
	}
}

// NormalizeOpsAlertRuleFilters keeps the legacy scope and normalizes the typed
// predicates. Empty lists clear filters; predicates for other metrics never
// leak into a subsequent metric switch.
func NormalizeOpsAlertRuleFilters(metric string, filters map[string]any) (map[string]any, error) {
	parsed, err := parseOpsAccountAlertFilters(metric, filters)
	if err != nil {
		return nil, err
	}
	normalized := make(map[string]any, len(filters))
	for key, value := range filters {
		switch key {
		case "account_ids", "upstream_status_codes", "provider_codes", "error_keywords":
		default:
			normalized[key] = value
		}
	}
	if len(parsed.AccountIDs) > 0 {
		normalized["account_ids"] = parsed.AccountIDs
	}
	if len(parsed.UpstreamStatusCodes) > 0 {
		normalized["upstream_status_codes"] = parsed.UpstreamStatusCodes
	}
	if len(parsed.ProviderCodes) > 0 {
		normalized["provider_codes"] = parsed.ProviderCodes
	}
	if len(parsed.ErrorKeywords) > 0 {
		normalized["error_keywords"] = parsed.ErrorKeywords
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

func parseOpsAccountAlertFilters(metric string, filters map[string]any) (OpsAccountUpstreamErrorFilter, error) {
	var f OpsAccountUpstreamErrorFilter
	f.Platform, f.GroupID, _ = parseOpsAlertRuleScope(filters)
	if !opsAlertAccountScope(metric) {
		return f, nil
	}
	if err := decodeOpsAlertFilter(filters, "account_ids", &f.AccountIDs, 50, func(id int64) bool { return id > 0 }); err != nil {
		return f, err
	}
	if metric != OpsAlertMetricAccountUpstreamError {
		return f, nil
	}
	if err := decodeOpsAlertFilter(filters, "upstream_status_codes", &f.UpstreamStatusCodes, 20, func(code int) bool { return code >= 400 && code <= 599 }); err != nil {
		return f, err
	}
	if err := decodeOpsAlertFilter(filters, "provider_codes", &f.ProviderCodes, 20, IsValidProviderErrorCode); err != nil {
		return f, err
	}
	if err := decodeOpsAlertFilter(filters, "error_keywords", &f.ErrorKeywords, 20, func(keyword string) bool {
		return utf8.RuneCountInString(keyword) <= 128 && strings.TrimSpace(keyword) != "" && !strings.ContainsFunc(keyword, unicode.IsControl)
	}); err != nil {
		return f, err
	}
	if !f.Selective() {
		return f, fmt.Errorf("account_upstream_error requires account_ids, group_id, upstream_status_codes or provider_codes")
	}
	return f, nil
}

func decodeOpsAlertFilter[T ~int | ~int64 | ~string](filters map[string]any, field string, out *[]T, limit int, valid func(T) bool) error {
	raw, present := filters[field]
	if !present || raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(encoded, out) != nil || len(*out) > limit {
		return fmt.Errorf("filters.%s must be an array with at most %d elements", field, limit)
	}
	for _, value := range *out {
		if !valid(value) {
			return fmt.Errorf("filters.%s contains an invalid value", field)
		}
	}
	slices.Sort(*out)
	*out = slices.Compact(*out)
	return nil
}
