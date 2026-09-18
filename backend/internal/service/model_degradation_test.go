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
		// Out-of-range but well-formed input is clamped, not rejected: an
		// over-eager caller should get data rather than an error.
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
