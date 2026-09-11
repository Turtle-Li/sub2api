package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func newPricingCurrencySettingsTestService(repo SettingRepository) *SettingService {
	return NewSettingService(repo, &config.Config{})
}

func TestGetPricingCurrencySettingsDefaultsAndCaches(t *testing.T) {
	repo := &panelRateLimitSettingRepo{}
	svc := newPricingCurrencySettingsTestService(repo)

	for range 2 {
		settings, err := svc.GetPricingCurrencySettings(context.Background())
		require.NoError(t, err)
		require.Equal(t, DefaultPricingCurrencySettings(), settings)
	}

	repo.mu.Lock()
	calls := repo.getValueCalls
	repo.mu.Unlock()
	require.Equal(t, 1, calls, "a cached pricing currency setting should not re-read the database")
}

func TestUpdatePricingCurrencySettingsRoundTripAndValidation(t *testing.T) {
	repo := &panelRateLimitSettingRepo{}
	svc := newPricingCurrencySettingsTestService(repo)

	invalid := []PricingCurrencySettings{
		{SettlementCurrency: "EUR", USDToCNYRate: 6.75},
		{SettlementCurrency: PricingSettlementCurrencyCNY, USDToCNYRate: 0},
		{SettlementCurrency: PricingSettlementCurrencyCNY, USDToCNYRate: math.NaN()},
		{SettlementCurrency: PricingSettlementCurrencyCNY, USDToCNYRate: math.Inf(1)},
	}
	for _, settings := range invalid {
		require.Error(t, svc.UpdatePricingCurrencySettings(context.Background(), settings))
	}

	want := PricingCurrencySettings{
		SettlementCurrency: PricingSettlementCurrencyCNY,
		USDToCNYRate:       6.75,
	}
	require.NoError(t, svc.UpdatePricingCurrencySettings(context.Background(), want))

	got, err := svc.GetPricingCurrencySettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, got)

	repo.mu.Lock()
	stored := repo.values[SettingKeyPricingCurrencySettings]
	repo.mu.Unlock()
	require.JSONEq(t, `{"settlement_currency":"CNY","usd_to_cny_rate":6.75}`, stored)
}

func TestGetPricingCurrencySettingsPreservesLastGoodConfigOnDBFailure(t *testing.T) {
	repo := &panelRateLimitSettingRepo{values: map[string]string{
		SettingKeyPricingCurrencySettings: `{"settlement_currency":"CNY","usd_to_cny_rate":6.75}`,
	}}
	svc := newPricingCurrencySettingsTestService(repo)

	good, err := svc.GetPricingCurrencySettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, PricingSettlementCurrencyCNY, good.SettlementCurrency)

	entry, ok := svc.pricingCurrencySettingsCache.Load().(*cachedPricingCurrencySettings)
	require.True(t, ok)
	entry.expiresAt = time.Now().Add(-time.Second).UnixNano()

	repo.mu.Lock()
	repo.getValueErr = errors.New("database unavailable")
	repo.mu.Unlock()

	stale, err := svc.GetPricingCurrencySettings(context.Background())
	require.Error(t, err)
	require.Equal(t, good, stale, "a failed refresh must keep the previous CNY setting")

	_, err = svc.GetPricingCurrencySettings(context.Background())
	require.Error(t, err)
	repo.mu.Lock()
	calls := repo.getValueCalls
	repo.mu.Unlock()
	require.Equal(t, 2, calls, "a refresh error should also be cached for the bounded failure TTL")
}
