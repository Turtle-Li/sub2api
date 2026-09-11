//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountStatsModelPricingCurrencyPersistence(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &channelRepository{db: db}
	mock.ExpectQuery(`(?s)SELECT .*billing_mode, currency, input_price.*FROM channel_account_stats_model_pricing.*rule_id = ANY`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "rule_id", "platform", "models", "billing_mode", "currency", "input_price", "output_price",
			"cache_write_price", "cache_write_1h_price", "cache_read_price", "image_output_price", "per_request_price", "created_at", "updated_at",
		}).AddRow(
			int64(11), int64(7), "openai", `["gpt-5"]`, service.BillingModeToken, service.PricingCurrencyCNY,
			nil, nil, nil, nil, nil, nil, nil, time.Time{}, time.Time{},
		))
	mock.ExpectQuery(`SELECT id, pricing_id, min_tokens, max_tokens, tier_label`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	loaded, err := repo.batchLoadAccountStatsModelPricing(context.Background(), []int64{7})
	require.NoError(t, err)
	require.Len(t, loaded[7], 1)
	require.Equal(t, service.PricingCurrencyCNY, loaded[7][0].Currency)
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectBegin()
	tx, err := db.Begin()
	require.NoError(t, err)
	pricing := &service.ChannelModelPricing{
		Platform:    "openai",
		Models:      []string{"gpt-5"},
		Currency:    "cny",
		BillingMode: service.BillingModeToken,
	}
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, currency, input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price, image_output_price, per_request_price)")).
		WithArgs(
			int64(7), "openai", []byte(`["gpt-5"]`), service.BillingModeToken, service.PricingCurrencyCNY,
			nil, nil, nil, nil, nil, nil, nil,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(12), time.Time{}, time.Time{}))
	mock.ExpectCommit()

	require.NoError(t, createAccountStatsModelPricingTx(context.Background(), tx, 7, pricing))
	require.NoError(t, tx.Commit())
	require.Equal(t, service.PricingCurrencyCNY, pricing.Currency)
	require.NoError(t, mock.ExpectationsWereMet())
}
