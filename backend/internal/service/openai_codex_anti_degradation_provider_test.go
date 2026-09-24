package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestProvideOpenAICodexAntiDegradationServiceDisabledByDefault(t *testing.T) {
	if svc := ProvideOpenAICodexAntiDegradationService(&config.Config{}, nil, nil, nil, nil); svc != nil {
		t.Fatal("anti-degradation service must stay disabled unless explicitly enabled")
	}
}

func TestProvideOpenAICodexAntiDegradationServiceCanBeExplicitlyEnabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexAntiDegradationEnabled = true

	svc := ProvideOpenAICodexAntiDegradationService(cfg, nil, nil, nil, nil)
	if svc == nil {
		t.Fatal("anti-degradation service should be constructed when explicitly enabled")
	}
	svc.Stop()
}
