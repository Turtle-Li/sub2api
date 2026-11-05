//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPaymentEntryVisibilityDefaultsAndPublicInjection(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want bool
	}{
		{"missing preserves navigation", "", true},
		{"explicit show", "true", true},
		{"link only", "false", false},
		{"invalid hides entry", "invalid", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]string{SettingPaymentEnabled: "true", SettingPaymentEntryEnabled: tt.raw}
			paymentSvc := &PaymentConfigService{settingRepo: &paymentConfigSettingRepoStub{values: values}}
			paymentCfg, err := paymentSvc.GetPaymentConfig(context.Background())
			require.NoError(t, err)
			require.True(t, paymentCfg.Enabled, "navigation visibility must not close payments")
			require.Equal(t, tt.want, paymentCfg.EntryEnabled)

			settingsSvc := NewSettingService(&settingPublicRepoStub{values: values}, &config.Config{})
			public, err := settingsSvc.GetPublicSettings(context.Background())
			require.NoError(t, err)
			require.True(t, public.PaymentEnabled)
			require.Equal(t, tt.want, public.PaymentEntryEnabled)
			injected, err := settingsSvc.GetPublicSettingsForInjection(context.Background())
			require.NoError(t, err)
			payload, ok := injected.(*PublicSettingsInjectionPayload)
			require.True(t, ok)
			require.Equal(t, public.PaymentEntryEnabled, payload.PaymentEntryEnabled)
		})
	}
}

func TestPaymentEntryVisibilityPartialSavePreservesPaymentConfiguration(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{
		SettingPaymentEnabled: "true", SettingPaymentEntryEnabled: "true",
		SettingRechargeOptions: `[{"amount":0.1,"balance_bonus":99.9,"enabled":true,"purchase_rules":{"visible_user_ids":[2]}}]`,
	}}
	svc := &PaymentConfigService{settingRepo: repo}
	hidden := false
	err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{EntryEnabled: &hidden})
	require.NoError(t, err)
	require.Equal(t, map[string]string{SettingPaymentEntryEnabled: "false"}, repo.updates)
	require.Equal(t, "true", repo.values[SettingPaymentEnabled])
	before := repo.values[SettingRechargeOptions]
	enabled := false
	require.NoError(t, svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{Enabled: &enabled}))
	require.Equal(t, "false", repo.values[SettingPaymentEntryEnabled], "omitted navigation setting must be preserved")
	require.Equal(t, before, repo.values[SettingRechargeOptions])
}

func TestShopEntryVisibilityDefaultsAndPublicInjection(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want bool
	}{
		{"missing preserves navigation", "", true},
		{"show", "true", true},
		{"hide", "false", false},
		{"invalid hides entry", "invalid", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]string{SettingPaymentEnabled: "true", SettingPaymentEntryEnabled: "false", SettingShopEntryEnabled: tt.raw}
			paymentSvc := &PaymentConfigService{settingRepo: &paymentConfigSettingRepoStub{values: values}}
			cfg, err := paymentSvc.GetPaymentConfig(context.Background())
			require.NoError(t, err)
			require.Equal(t, tt.want, cfg.ShopEntryEnabled)
			require.True(t, cfg.Enabled)
			require.False(t, cfg.EntryEnabled)

			settingsSvc := NewSettingService(&settingPublicRepoStub{values: values}, &config.Config{})
			public, err := settingsSvc.GetPublicSettings(context.Background())
			require.NoError(t, err)
			require.Equal(t, tt.want, public.ShopEntryEnabled)
			require.False(t, public.PaymentEntryEnabled)
			injected, err := settingsSvc.GetPublicSettingsForInjection(context.Background())
			require.NoError(t, err)
			require.Equal(t, tt.want, injected.(*PublicSettingsInjectionPayload).ShopEntryEnabled)
		})
	}
}

func TestShopEntryVisibilityPartialSavePreservesOtherConfiguration(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{
		SettingPaymentEnabled: "true", SettingPaymentEntryEnabled: "true", SettingShopEntryEnabled: "true",
		SettingRechargeOptions: `[{"amount":0.1,"enabled":true}]`,
	}}
	svc := &PaymentConfigService{settingRepo: repo}
	hidden := false
	require.NoError(t, svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{ShopEntryEnabled: &hidden}))
	require.Equal(t, map[string]string{SettingShopEntryEnabled: "false"}, repo.updates)
	require.Equal(t, "true", repo.values[SettingPaymentEnabled])
	require.Equal(t, "true", repo.values[SettingPaymentEntryEnabled])
	require.Equal(t, `[{"amount":0.1,"enabled":true}]`, repo.values[SettingRechargeOptions])
	require.NoError(t, svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{EntryEnabled: &hidden}))
	require.Equal(t, "false", repo.values[SettingShopEntryEnabled], "omitted visibility setting must be preserved")
}
