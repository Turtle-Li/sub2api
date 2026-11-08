//go:build unit

package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountIsSchedulable_DailyRequestQuota(t *testing.T) {
	for _, test := range []struct {
		name  string
		limit int
		used  int
		start string
		want  bool
	}{
		{"below limit", 10, 9, "2999-01-01T00:00:00Z", true},
		{"at limit", 10, 10, "2999-01-01T00:00:00Z", false},
		{"over limit", 10, 12, "2999-01-01T00:00:00Z", false},
		{"expired period", 10, 10, "2000-01-01T00:00:00Z", true},
		{"unlimited", 0, 10, "2999-01-01T00:00:00Z", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := &Account{
				Status: StatusActive, Schedulable: true, Type: AccountTypeAPIKey,
				Extra: map[string]any{
					"quota_daily_request_limit": test.limit,
					"quota_daily_request_used":  test.used,
					"quota_daily_request_start": test.start,
				},
			}

			got := account.IsSchedulable()

			require.Equal(t, test.want, got)
		})
	}
}

func TestValidateQuotaResetConfig_DailyRequestLimit(t *testing.T) {
	for _, limit := range []any{-1, 1.5, "10", true, int64(math.MaxInt32) + 1, math.NaN(), math.Inf(1), json.Number("invalid")} {
		extra := map[string]any{"quota_daily_request_limit": limit}

		err := ValidateQuotaResetConfig(extra)

		require.Error(t, err, "invalid request limit: %v", limit)
	}
	for _, limit := range []any{nil, 0, 10.0, int64(math.MaxInt32), json.Number("10")} {
		require.NoError(t, ValidateQuotaResetConfig(map[string]any{"quota_daily_request_limit": limit}))
	}
}

func TestNormalizeFixedQuotaWindows_DailyRequestUsage(t *testing.T) {
	extra := map[string]any{
		"quota_daily_request_limit": 10,
		"quota_daily_request_used":  10,
		"quota_daily_request_start": "2000-01-01T00:00:00Z",
		"quota_daily_reset_mode":    "fixed",
		"quota_daily_reset_hour":    0,
		"quota_reset_timezone":      "Asia/Shanghai",
	}

	NormalizeFixedQuotaWindows(extra)

	require.Zero(t, extra["quota_daily_request_used"])
	start, err := time.Parse(time.RFC3339, extra["quota_daily_request_start"].(string))
	require.NoError(t, err)
	require.Greater(t, start.Year(), 2000)
}

func TestBulkUpdateAccounts_DailyRequestQuota(t *testing.T) {
	repo := &upstreamBillingProbeAccountRepo{}
	svc := &adminServiceImpl{accountRepo: repo}
	input := &BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Extra: map[string]any{
			"quota_daily_request_limit":    10,
			"quota_daily_request_used":     0,
			"quota_daily_request_start":    "2999-01-01T00:00:00Z",
			"quota_daily_request_reset_at": "2999-01-02T00:00:00Z",
		},
	}
	result, err := svc.BulkUpdateAccounts(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, 1, result.Success)
	require.Len(t, repo.bulkUpdates, 1)
	require.Equal(t, 10, repo.bulkUpdates[0].Extra["quota_daily_request_limit"])
	for _, key := range []string{"quota_daily_request_used", "quota_daily_request_start", "quota_daily_request_reset_at"} {
		require.NotContains(t, repo.bulkUpdates[0].Extra, key)
	}

	input.Extra["quota_daily_request_limit"] = 1.5
	_, err = svc.BulkUpdateAccounts(context.Background(), input)
	require.Error(t, err)
	require.Len(t, repo.bulkUpdates, 1, "invalid limit must be rejected before writing")
}

func TestBuildAccountForCreate_DailyRequestQuota(t *testing.T) {
	extra := map[string]any{
		"quota_daily_request_limit":    10,
		"quota_daily_request_used":     10,
		"quota_daily_request_start":    "2999-01-01T00:00:00Z",
		"quota_daily_request_reset_at": "2999-01-02T00:00:00Z",
	}
	account, err := buildAccountForCreate(&CreateAccountInput{
		Name: "count-limited account", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
	}, extra)
	require.NoError(t, err)
	require.Equal(t, 10, account.GetQuotaDailyRequestLimit())
	require.Zero(t, account.GetQuotaDailyRequestUsed())
	require.NotContains(t, account.Extra, "quota_daily_request_start")
	require.NotContains(t, account.Extra, "quota_daily_request_reset_at")
}
