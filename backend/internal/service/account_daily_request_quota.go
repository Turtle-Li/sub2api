package service

import (
	"encoding/json"
	"errors"
	"math"
	"time"
)

func (a *Account) GetQuotaDailyRequestLimit() int {
	return a.getExtraInt("quota_daily_request_limit")
}

func (a *Account) GetQuotaDailyRequestUsed() int {
	return a.getExtraInt("quota_daily_request_used")
}

func (a *Account) IsDailyRequestQuotaPeriodExpired() bool {
	start := a.getExtraTime("quota_daily_request_start")
	if a.GetQuotaDailyResetMode() == "fixed" {
		return a.isFixedDailyPeriodExpired(start)
	}
	return isPeriodExpired(start, 24*time.Hour)
}

func validateDailyRequestQuotaConfig(extra map[string]any) error {
	raw, exists := extra["quota_daily_request_limit"]
	if !exists || raw == nil {
		return nil
	}
	var limit float64
	switch value := raw.(type) {
	case int, int64, float32, float64:
		limit = parseExtraFloat64(value)
	case json.Number:
		var err error
		limit, err = value.Float64()
		if err != nil {
			return errors.New("quota_daily_request_limit must be a non-negative integer")
		}
	default:
		return errors.New("quota_daily_request_limit must be a non-negative integer")
	}
	if math.IsNaN(limit) || math.IsInf(limit, 0) || limit < 0 || limit > math.MaxInt32 || math.Trunc(limit) != limit {
		return errors.New("quota_daily_request_limit must be an integer between 0 and 2147483647")
	}
	return nil
}

func NormalizeFixedDailyRequestWindow(extra map[string]any) {
	account := &Account{Extra: extra}
	if account.GetQuotaDailyRequestLimit() <= 0 || account.GetQuotaDailyResetMode() != "fixed" {
		return
	}
	tz, err := time.LoadLocation(account.GetQuotaResetTimezone())
	if err != nil {
		tz = time.UTC
	}
	lastReset := lastFixedDailyReset(account.GetQuotaDailyResetHour(), tz, time.Now())
	if account.getExtraTime("quota_daily_request_start").Before(lastReset) {
		extra["quota_daily_request_used"] = 0
		extra["quota_daily_request_start"] = lastReset.UTC().Format(time.RFC3339)
	}
}
