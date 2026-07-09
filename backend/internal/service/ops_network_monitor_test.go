package service

import "testing"

func TestApplyOpsNetworkBandwidthSettingsToSummaryRefreshesLimits(t *testing.T) {
	oldRX := 10.0
	oldTX := 5.0
	newRX := 20.0
	newTX := 10.0
	summary := &OpsNetworkBandwidthSummary{
		Enabled:               true,
		LimitSource:           opsNetworkLimitSourceManual,
		RXLimitMbps:           &oldRX,
		TXLimitMbps:           &oldTX,
		RXMbps:                9,
		TXMbps:                4.5,
		Status:                "warning",
		RXUtilizationPercent:  &oldRX,
		TXUtilizationPercent:  &oldTX,
		MaxUtilizationPercent: &oldRX,
	}
	cfg := &OpsNetworkBandwidthSettings{
		Enabled:         true,
		RXLimitMbps:     &newRX,
		TXLimitMbps:     &newTX,
		LimitSource:     opsNetworkLimitSourceManual,
		WarnPercent:     80,
		CriticalPercent: 95,
	}

	updated := applyOpsNetworkBandwidthSettingsToSummary(summary, cfg)

	if updated.RXLimitMbps == nil || *updated.RXLimitMbps != newRX {
		t.Fatalf("expected rx limit to refresh to %v, got %#v", newRX, updated.RXLimitMbps)
	}
	if updated.TXLimitMbps == nil || *updated.TXLimitMbps != newTX {
		t.Fatalf("expected tx limit to refresh to %v, got %#v", newTX, updated.TXLimitMbps)
	}
	if updated.RXUtilizationPercent == nil || *updated.RXUtilizationPercent != 45 {
		t.Fatalf("expected rx utilization to be recalculated to 45, got %#v", updated.RXUtilizationPercent)
	}
	if updated.TXUtilizationPercent == nil || *updated.TXUtilizationPercent != 45 {
		t.Fatalf("expected tx utilization to be recalculated to 45, got %#v", updated.TXUtilizationPercent)
	}
	if updated.Status != "ok" {
		t.Fatalf("expected status to be recalculated to ok, got %q", updated.Status)
	}
}
