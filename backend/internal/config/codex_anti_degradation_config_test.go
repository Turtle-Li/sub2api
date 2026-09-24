package config

import "testing"

func TestCodexAntiDegradationDefaultsDisabled(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Gateway.CodexAntiDegradationEnabled {
		t.Fatal("codex anti-degradation must default to disabled")
	}
}
