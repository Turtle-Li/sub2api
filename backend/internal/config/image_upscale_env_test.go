//go:build unit

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadImageUpscaleFromEnv(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("IMAGE_UPSCALE_ENABLED", "true")
	t.Setenv("IMAGE_UPSCALE_BASE_URL", "https://upscale.example.com")
	t.Setenv("IMAGE_UPSCALE_API_KEY_VAULT_REF", "vault://secret/data/infrastructure/upscale#api_key")
	t.Setenv("IMAGE_UPSCALE_VAULT_AGENT_SOCKET", "/run/sub2api-upscale-vault/public.sock")
	t.Setenv("IMAGE_UPSCALE_MAX_CONCURRENT", "1")
	t.Setenv("IMAGE_UPSCALE_MAX_QUEUE", "8")

	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.ImageUpscale.Active())
	require.Equal(t, "https://upscale.example.com", cfg.ImageUpscale.BaseURL)
	require.Equal(t, "vault://secret/data/infrastructure/upscale#api_key", cfg.ImageUpscale.APIKeyVaultRef)
	require.Equal(t, 1, cfg.ImageUpscale.MaxConcurrent)
	require.Equal(t, 8, cfg.ImageUpscale.MaxQueue)
}

func TestImageUpscaleRejectsRawOrUnboundedConfiguration(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	cfg.ImageUpscale = ImageUpscaleConfig{
		Enabled:               true,
		BaseURL:               "http://upscale.example.com",
		APIKeyVaultRef:        "vault://secret/data/infrastructure/upscale#api_key",
		VaultAgentSocket:      "/run/sub2api-upscale-vault/public.sock",
		RequestTimeoutSeconds: 30,
		JobTimeoutSeconds:     900,
		PollIntervalMillis:    500,
		RetryMax:              3,
		MaxConcurrent:         1,
		MaxQueue:              8,
		MaxResultBytes:        128 * 1024 * 1024,
	}
	require.ErrorContains(t, cfg.Validate(), "HTTPS origin")

	cfg.ImageUpscale.BaseURL = "https://upscale.example.com"
	cfg.ImageUpscale.MaxConcurrent = 3
	require.ErrorContains(t, cfg.Validate(), "between 1 and 2")
}
