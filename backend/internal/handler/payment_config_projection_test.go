package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestPublicPaymentConfigProjectionKeepsPaymentFields(t *testing.T) {
	cfg := &service.PaymentConfig{
		Enabled:      true,
		EntryEnabled: true,
	}

	projected := publicPaymentConfigProjection(cfg)
	if enabled, ok := projected["enabled"].(bool); !ok || !enabled {
		t.Fatalf("public payment config lost enabled field: %#v", projected["enabled"])
	}
	if entryEnabled, ok := projected["entry_enabled"].(bool); !ok || !entryEnabled {
		t.Fatalf("public payment config lost entry_enabled field: %#v", projected["entry_enabled"])
	}
	if _, exists := projected["monthly_reset_cards_enabled"]; exists {
		t.Fatalf("public payment config exposed removed monthly reset-card gate: %#v", projected)
	}
}
