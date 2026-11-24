//go:build unit

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadFeishuPaymentAlertsFromExactEnvironment(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		cfg, err := Load()
		require.NoError(t, err)
		require.False(t, cfg.FeishuPaymentAlerts.Enabled)
	})
	t.Run("enabled by SUB2API_FEISHU_ENABLED", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		t.Setenv("SUB2API_FEISHU_ENABLED", "true")
		cfg, err := Load()
		require.NoError(t, err)
		require.True(t, cfg.FeishuPaymentAlerts.Enabled)
	})
}

func TestLoadOpsFeishuAlertSwitchIsIndependentAndDefaultOff(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("SUB2API_FEISHU_ENABLED", "true")
	t.Setenv("SUB2API_OPS_FEISHU_ALERTS_ENABLED", "")
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.FeishuPaymentAlerts.Enabled)
	require.False(t, cfg.Ops.FeishuAlertsEnabled)

	resetViperWithJWTSecret(t)
	t.Setenv("SUB2API_FEISHU_ENABLED", "false")
	t.Setenv("SUB2API_OPS_FEISHU_ALERTS_ENABLED", "true")
	cfg, err = Load()
	require.NoError(t, err)
	require.True(t, cfg.Ops.FeishuAlertsEnabled)
	require.False(t, cfg.FeishuPaymentAlerts.Enabled)
}
