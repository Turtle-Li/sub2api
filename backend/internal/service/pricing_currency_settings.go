package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	PricingSettlementCurrencyUSD = "USD"
	PricingSettlementCurrencyCNY = "CNY"

	pricingCurrencySettingsCacheTTL  = 15 * time.Second
	pricingCurrencySettingsDBTimeout = 5 * time.Second
	pricingCurrencySettingsSFKey     = "pricing_currency_settings"
)

// ErrPricingCurrencyMigrationRequired prevents an ordinary settings write from
// relabeling wallet balances. Settlement currency changes are only valid as
// part of the owner-controlled wallet and quota migration transaction.
var ErrPricingCurrencyMigrationRequired = errors.New("settlement currency changes require the wallet migration transaction")

// PricingCurrencySettings defines the internal settlement unit used for new
// wallet and usage values. USDToCNYRate records the approved one-time
// migration rate; it is not a live exchange-rate feed.
type PricingCurrencySettings struct {
	SettlementCurrency string  `json:"settlement_currency"`
	USDToCNYRate       float64 `json:"usd_to_cny_rate"`
}

type cachedPricingCurrencySettings struct {
	settings  PricingCurrencySettings
	err       error
	hasGood   bool
	expiresAt int64
}

// DefaultPricingCurrencySettings keeps existing installations on their legacy
// USD settlement unit until an owner-approved migration explicitly changes it.
func DefaultPricingCurrencySettings() PricingCurrencySettings {
	return PricingCurrencySettings{
		SettlementCurrency: PricingSettlementCurrencyUSD,
		USDToCNYRate:       6.75,
	}
}

func normalizePricingCurrencySettings(settings PricingCurrencySettings) (PricingCurrencySettings, error) {
	settings.SettlementCurrency = strings.ToUpper(strings.TrimSpace(settings.SettlementCurrency))
	switch settings.SettlementCurrency {
	case PricingSettlementCurrencyUSD, PricingSettlementCurrencyCNY:
	default:
		return PricingCurrencySettings{}, fmt.Errorf("settlement currency must be USD or CNY")
	}
	if math.IsNaN(settings.USDToCNYRate) || math.IsInf(settings.USDToCNYRate, 0) || settings.USDToCNYRate <= 0 {
		return PricingCurrencySettings{}, fmt.Errorf("USD to CNY rate must be a finite positive number")
	}
	return settings, nil
}

func (s *SettingService) readPricingCurrencySettings(ctx context.Context) (PricingCurrencySettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyPricingCurrencySettings)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && strings.TrimSpace(value) == "") {
		return DefaultPricingCurrencySettings(), nil
	}
	if err != nil {
		return PricingCurrencySettings{}, fmt.Errorf("get pricing currency settings: %w", err)
	}

	var stored PricingCurrencySettings
	if err := json.Unmarshal([]byte(value), &stored); err != nil {
		return PricingCurrencySettings{}, fmt.Errorf("parse pricing currency settings: %w", err)
	}
	settings, err := normalizePricingCurrencySettings(stored)
	if err != nil {
		return PricingCurrencySettings{}, fmt.Errorf("invalid pricing currency settings: %w", err)
	}
	return settings, nil
}

// GetPricingCurrencySettings returns the active settlement-currency settings.
// A failed refresh is cached for at most 15 seconds. If this process has
// already observed a valid CNY configuration, a later database failure returns
// that last known-good configuration with the error instead of silently
// reverting the caller to USD.
func (s *SettingService) GetPricingCurrencySettings(ctx context.Context) (PricingCurrencySettings, error) {
	if s == nil || s.settingRepo == nil {
		return PricingCurrencySettings{}, fmt.Errorf("pricing currency settings service is unavailable")
	}
	if cached, ok := s.pricingCurrencySettingsCache.Load().(*cachedPricingCurrencySettings); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cached.settings, cached.err
	}

	result, _, _ := s.pricingCurrencySettingsSF.Do(pricingCurrencySettingsSFKey, func() (any, error) {
		if cached, ok := s.pricingCurrencySettingsCache.Load().(*cachedPricingCurrencySettings); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
			return cached, nil
		}
		if ctx == nil {
			ctx = context.Background()
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pricingCurrencySettingsDBTimeout)
		defer cancel()

		settings, err := s.readPricingCurrencySettings(dbCtx)
		if err != nil {
			fallback := DefaultPricingCurrencySettings()
			hasGood := false
			if prior, ok := s.pricingCurrencySettingsCache.Load().(*cachedPricingCurrencySettings); ok && prior != nil && prior.hasGood {
				fallback = prior.settings
				hasGood = true
			}
			entry := &cachedPricingCurrencySettings{
				settings:  fallback,
				err:       err,
				hasGood:   hasGood,
				expiresAt: time.Now().Add(pricingCurrencySettingsCacheTTL).UnixNano(),
			}
			s.pricingCurrencySettingsCache.Store(entry)
			return entry, nil
		}

		entry := &cachedPricingCurrencySettings{
			settings:  settings,
			hasGood:   true,
			expiresAt: time.Now().Add(pricingCurrencySettingsCacheTTL).UnixNano(),
		}
		s.pricingCurrencySettingsCache.Store(entry)
		return entry, nil
	})
	if entry, ok := result.(*cachedPricingCurrencySettings); ok && entry != nil {
		return entry.settings, entry.err
	}
	return PricingCurrencySettings{}, fmt.Errorf("load pricing currency settings: unavailable result")
}

// UpdatePricingCurrencySettings validates and stores settings for the current
// settlement currency. It deliberately refuses a currency transition: changing
// USD/CNY outside the wallet migration transaction would relabel existing
// balances without converting them. The transaction that migrates balances and
// quotas writes the new currency configuration itself.
func (s *SettingService) UpdatePricingCurrencySettings(ctx context.Context, settings PricingCurrencySettings) error {
	if s == nil || s.settingRepo == nil {
		return fmt.Errorf("pricing currency settings service is unavailable")
	}
	normalized, err := normalizePricingCurrencySettings(settings)
	if err != nil {
		return err
	}
	current, err := s.readPricingCurrencySettings(ctx)
	if err != nil {
		return fmt.Errorf("read current pricing currency settings: %w", err)
	}
	if normalized.SettlementCurrency != current.SettlementCurrency {
		return fmt.Errorf(
			"%w: %s to %s",
			ErrPricingCurrencyMigrationRequired,
			current.SettlementCurrency,
			normalized.SettlementCurrency,
		)
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal pricing currency settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyPricingCurrencySettings, string(data)); err != nil {
		return fmt.Errorf("set pricing currency settings: %w", err)
	}

	s.pricingCurrencySettingsSF.Forget(pricingCurrencySettingsSFKey)
	s.pricingCurrencySettingsCache.Store(&cachedPricingCurrencySettings{
		settings:  normalized,
		hasGood:   true,
		expiresAt: time.Now().Add(pricingCurrencySettingsCacheTTL).UnixNano(),
	})
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return nil
}
