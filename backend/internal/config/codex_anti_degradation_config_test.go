package config

import (
	"os"
	"testing"
)

func TestCodexAntiDegradationDefaultsDisabled(t *testing.T) {
	const envName = "GATEWAY_CODEX_ANTI_DEGRADATION_ENABLED"
	original, existed := os.LookupEnv(envName)
	if err := os.Unsetenv(envName); err != nil {
		t.Fatalf("unset %s: %v", envName, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(envName, original)
			return
		}
		_ = os.Unsetenv(envName)
	})

	resetViperWithJWTSecret(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Gateway.CodexAntiDegradationEnabled {
		t.Fatal("codex anti-degradation must default to disabled")
	}
}

func TestCodexAntiDegradationCanBeEnabledByEnvironment(t *testing.T) {
	t.Setenv("GATEWAY_CODEX_ANTI_DEGRADATION_ENABLED", "true")
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.Gateway.CodexAntiDegradationEnabled {
		t.Fatal("codex anti-degradation environment override was ignored")
	}
}
