package admin

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestUsageStatsCacheKey_StableAndDistinct(t *testing.T) {
	start := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	base := usagestats.UsageLogFilters{StartTime: &start, EndTime: &end, Model: "claude-3"}

	k1 := usageStatsCacheKey(base)
	k2 := usageStatsCacheKey(base)
	require.NotEmpty(t, k1)
	require.Equal(t, k1, k2, "same filters must produce same key")

	other := base
	other.Model = "gpt-4o"
	require.NotEqual(t, k1, usageStatsCacheKey(other), "different model must change key")

	withUser := base
	withUser.UserID = 7
	require.NotEqual(t, k1, usageStatsCacheKey(withUser), "different user must change key")
}

func TestPaidFallbackReadCacheScopesAreDistinct(t *testing.T) {
	paid, ordinary := true, false
	usageKeys := map[string]bool{}
	trendKeys := map[string]bool{}
	modelGroupKeys := map[string]bool{}
	snapshotKeys := map[string]bool{}
	for _, value := range []*bool{nil, &paid, &ordinary} {
		usageKeys[usageStatsCacheKey(usagestats.UsageLogFilters{PaidFallback: value})] = true
		trendKeys[mustMarshalDashboardCacheKey(dashboardTrendCacheKey{PaidFallback: value})] = true
		modelGroupKeys[mustMarshalDashboardCacheKey(dashboardModelGroupCacheKey{PaidFallback: value})] = true
		snapshotKeys[mustMarshalDashboardCacheKey(dashboardSnapshotV2CacheKey{PaidFallback: value})] = true
	}
	require.Len(t, usageKeys, 3)
	require.Len(t, trendKeys, 3)
	require.Len(t, modelGroupKeys, 3)
	require.Len(t, snapshotKeys, 3)
}
