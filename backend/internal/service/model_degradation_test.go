package service

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseDegradedAccountsWindow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		raw    string
		want   time.Duration
		wantOK bool
	}{
		{"empty takes the default", "", 30 * time.Minute, true},
		{"whitespace takes the default", "   ", 30 * time.Minute, true},
		{"explicit default", "30m", 30 * time.Minute, true},
		{"compound", "1h30m", 90 * time.Minute, true},
		{"at the ceiling", "6h", 6 * time.Hour, true},
		{"below the floor clamps up", "5s", time.Minute, true},
		{"above the ceiling clamps down", "24h", 6 * time.Hour, true},
		{"not a duration", "abc", 0, false},
		{"bare number has no unit", "30", 0, false},
		{"negative", "-5m", 0, false},
		{"zero", "0", 0, false},
		{"zero with unit", "0s", 0, false},
		{"injection attempt", "30m; DROP TABLE usage_logs", 0, false},
		{"absurdly long", strings.Repeat("1h", 20), 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			window, ok := ParseDegradedAccountsWindow(tc.raw)
			require.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.Equal(t, tc.want, window)
			}
		})
	}
}

func TestModelDegradationSuspectedFiltersNamingVariants(t *testing.T) {
	for _, tc := range []struct {
		name          string
		sent          string
		response      string
		wantSuspected bool
	}{
		{"iso date suffix", "gpt-6-astra", "gpt-6-astra-2026-03-01", false},
		{"compact date suffix", "gpt-6-astra", "gpt-6-astra-20260301", false},
		{"latest suffix", "gpt-6-astra", "gpt-6-astra-latest", false},
		{"latest then date", "gpt-6-astra-latest", "gpt-6-astra-2026-03-01", false},
		{"two dated builds", "gpt-6-astra-2026-03-01", "gpt-6-astra-2026-04-01", false},
		{"case fold", "GPT-6-ASTRA", "gpt-6-astra", false},
		{"historical grok build alias", "grok-4.6", "grok-4.6-build", false},
		{"empty response", "gpt-6-astra", "", false},
		{"empty sent", "", "gpt-6-astra", false},
		{"compact suffix is positional", "gpt-4-12345678", "gpt-4", false},
		{"real degradation", "gpt-6-astra", "gpt-5.6-luna", true},
		{"alphabetic suffix is a different model", "gpt-6", "gpt-6-astra", true},
		{"alphabetic suffix reversed", "gpt-6-astra", "gpt-6", true},
		{"unpadded date is not stripped", "gpt-6-2026-3-1", "gpt-6", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantSuspected, ModelDegradationSuspected(tc.sent, tc.response))
		})
	}
}

func TestNormalizeModelVariantStripsSuffixesInOrder(t *testing.T) {
	require.Equal(t, "gpt-6-astra", NormalizeModelVariant("  GPT-6-Astra-Latest  "))
	require.Equal(t, "gpt-6-astra", NormalizeModelVariant("gpt-6-astra-2026-03-01"))
	require.Equal(t, "gpt-6-astra", NormalizeModelVariant("gpt-6-astra-20260301"))
	require.Equal(t, "gpt-6-astra", NormalizeModelVariant("gpt-6-astra-2026-03-01-latest"))
	require.Equal(t, "", NormalizeModelVariant("   "))
}

func TestModelDegradationDoesNotChangePersistedMismatch(t *testing.T) {
	variant := upstreamModelMismatch("gpt-6-astra", "gpt-6-astra-2026-03-01")
	require.NotNil(t, variant)
	require.True(t, *variant, "the raw audit field remains a mismatch")
	require.False(t, ModelDegradationSuspected("gpt-6-astra", "gpt-6-astra-2026-03-01"))

	grok := upstreamModelMismatch("grok-4.6", "grok-4.6-build")
	require.NotNil(t, grok)
	require.False(t, *grok)
	require.Nil(t, upstreamModelMismatch("gpt-6-astra", ""))
}
