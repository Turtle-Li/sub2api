package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestPublicPaymentConfigProjectionOmitsMonthlyResetCardRolloutFlag(t *testing.T) {
	cfg := &service.PaymentConfig{
		Enabled:                  true,
		EntryEnabled:             true,
		MonthlyResetCardsEnabled: true,
	}

	projected := publicPaymentConfigProjection(cfg)
	if _, exposed := projected["monthly_reset_cards_enabled"]; exposed {
		t.Fatal("public payment config exposed the private monthly reset-card rollout flag")
	}
	if enabled, ok := projected["enabled"].(bool); !ok || !enabled {
		t.Fatalf("public payment config lost enabled field: %#v", projected["enabled"])
	}
	if entryEnabled, ok := projected["entry_enabled"].(bool); !ok || !entryEnabled {
		t.Fatalf("public payment config lost entry_enabled field: %#v", projected["entry_enabled"])
	}
}
