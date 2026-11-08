//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestDailyRequestQuota_SettlementDeduplicatesAndResets(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)
	for _, mode := range []string{"rolling", "fixed"} {
		t.Run(mode, func(t *testing.T) {
			account := mustCreateAccount(t, client, &service.Account{
				Name: "request-quota-" + uuid.NewString(), Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true,
				Extra: map[string]any{
					"quota_daily_request_limit": json.Number("2.0"), "quota_daily_reset_mode": mode,
					"quota_reset_timezone": "Asia/Shanghai", "quota_daily_used": 7,
				},
			})
			accountRepo := newAccountRepositoryWithSQL(client, integrationDB, nil)
			stale, err := accountRepo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			var initialEvents int
			require.NoError(t, integrationDB.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND account_id = $2",
				service.SchedulerOutboxEventAccountChanged, account.ID).Scan(&initialEvents))
			cmd := &service.UsageBillingCommand{
				RequestID: uuid.NewString(), APIKeyID: account.ID, AccountID: account.ID,
				AccountType: service.AccountTypeAPIKey, AccountRequestCount: 1,
			}

			for index := 0; index < 2; index++ {
				copy := *cmd
				copy.RequestID = fmt.Sprintf("%s-%d", cmd.RequestID, index)
				first, err := repo.Apply(ctx, &copy)
				require.NoError(t, err)
				require.True(t, first.Applied)
				duplicate, err := repo.Apply(ctx, &copy)
				require.NoError(t, err)
				require.False(t, duplicate.Applied)
			}

			var used, moneyUsed, events int
			require.NoError(t, integrationDB.QueryRowContext(ctx,
				"SELECT (extra->>'quota_daily_request_used')::int, (extra->>'quota_daily_used')::int FROM accounts WHERE id = $1",
				account.ID).Scan(&used, &moneyUsed))
			require.Equal(t, 2, used)
			require.Equal(t, 7, moneyUsed)
			require.NoError(t, integrationDB.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND account_id = $2",
				service.SchedulerOutboxEventAccountChanged, account.ID).Scan(&events))
			require.Equal(t, initialEvents+1, events)
			loaded, err := accountRepo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			require.False(t, loaded.IsSchedulable())

			stale.Name = "edited account"
			require.NoError(t, accountRepo.Update(ctx, stale))
			loaded, err = accountRepo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			require.Equal(t, 2, loaded.GetQuotaDailyRequestUsed())

			_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra = extra ||
				'{"quota_daily_request_start":"2000-01-01T00:00:00Z","quota_daily_request_reset_at":"2000-01-02T00:00:00Z"}'::jsonb WHERE id=$1`, account.ID)
			require.NoError(t, err)
			cmd.RequestID = uuid.NewString()
			_, err = repo.Apply(ctx, cmd)
			require.NoError(t, err)
			loaded, err = accountRepo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			require.Equal(t, 1, loaded.GetQuotaDailyRequestUsed())
			require.True(t, loaded.IsSchedulable())
			require.NoError(t, accountRepo.ResetQuotaUsedAndClearRateLimitCooldown(ctx, account.ID))
			loaded, err = accountRepo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			require.Zero(t, loaded.GetQuotaDailyRequestUsed())
		})
	}
}

func TestDailyRequestQuota_FreeBatchCaptureCountsSuccessfulItemsOnce(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{
		Name: "batch-request-quota-" + uuid.NewString(), Type: service.AccountTypeAPIKey,
		Extra: map[string]any{"quota_daily_request_limit": 10},
	})
	repo := NewUsageBillingRepository(client, integrationDB)
	cmd := &service.BatchImageBalanceHoldCommand{
		RequestID: uuid.NewString(), BatchID: uuid.NewString(), APIKeyID: account.ID,
		AccountID: account.ID, AccountRequestCount: 3,
	}

	_, err := repo.CaptureBatchImageBalance(ctx, cmd)
	require.NoError(t, err)
	duplicate, err := repo.CaptureBatchImageBalance(ctx, cmd)
	require.NoError(t, err)

	require.False(t, duplicate.Applied)
	var lastUsed time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT last_used_at FROM accounts WHERE id=$1", account.ID).Scan(&lastUsed))
	require.WithinDuration(t, time.Now(), lastUsed, time.Minute, "successful batch items are ordinary account activity")
	var used int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT (extra->>'quota_daily_request_used')::int FROM accounts WHERE id=$1", account.ID).Scan(&used))
	require.Equal(t, 3, used)
	var started time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT (extra->>'quota_daily_request_start')::timestamptz FROM accounts WHERE id=$1", account.ID).Scan(&started))
	require.False(t, started.IsZero())
}
