package dto

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestAccountFromServiceShallow_DailyRequestQuota(t *testing.T) {
	for _, mode := range []string{"rolling", "fixed"} {
		t.Run(mode, func(t *testing.T) {
			account := &service.Account{
				ID: 1, Type: service.AccountTypeAPIKey,
				Extra: map[string]any{
					"quota_daily_request_limit": 10,
					"quota_daily_request_used":  7,
					"quota_daily_request_start": "2999-01-01T00:00:00Z",
					"quota_daily_reset_mode":    mode,
				},
			}
			out := AccountFromServiceShallow(account)
			require.Equal(t, 10, *out.QuotaDailyRequestLimit)
			require.Equal(t, 7, *out.QuotaDailyRequestUsed)

			account.Extra["quota_daily_request_start"] = "2000-01-01T00:00:00Z"
			out = AccountFromServiceShallow(account)
			require.Zero(t, *out.QuotaDailyRequestUsed)
			require.Equal(t, 7, account.GetQuotaDailyRequestUsed(), "mapping must not mutate stored usage")

			account.Type = service.AccountTypeOAuth
			out = AccountFromServiceShallow(account)
			require.Nil(t, out.QuotaDailyRequestLimit)
			require.Nil(t, out.QuotaDailyRequestUsed)
		})
	}
}
