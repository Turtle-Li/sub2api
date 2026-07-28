//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeDesktopPromotions(t *testing.T) {
	t.Parallel()

	items, err := NormalizeDesktopPromotions([]DesktopPromotion{
		{
			ID:        " turtle-chat ",
			Title:     " Turtle Chat ",
			Summary:   "网页聊天",
			TargetURL: "https://chat.example.com/start?from=desktop",
			Icon:      "CHAT",
			Surfaces:  []string{"overview", "discover", "overview"},
			Enabled:   true,
			SortOrder: 20,
		},
		{
			ID:        "agent-link",
			Title:     "AgentDescLink",
			TargetURL: "https://agent.example.com",
			Enabled:   true,
			SortOrder: 10,
		},
	})

	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "agent-link", items[0].ID)
	require.Equal(t, "link", items[0].Icon)
	require.Equal(t, []string{"discover"}, items[0].Surfaces)
	require.Equal(t, "打开", items[0].CTALabel)
	require.Equal(t, []string{"discover", "overview"}, items[1].Surfaces)
}

func TestNormalizeDesktopPromotionsRejectsUnsafeContent(t *testing.T) {
	t.Parallel()

	tests := []DesktopPromotion{
		{ID: "unsafe-url", Title: "Unsafe", TargetURL: "javascript:alert(1)", Enabled: true},
		{ID: "public-http", Title: "Unsafe", TargetURL: "http://example.com", Enabled: true},
		{ID: "remote-icon", Title: "Unsafe", TargetURL: "https://example.com", Icon: "https://example.com/icon.svg", Enabled: true},
		{ID: "bad-surface", Title: "Unsafe", TargetURL: "https://example.com", Surfaces: []string{"banner"}, Enabled: true},
	}
	for _, item := range tests {
		item := item
		t.Run(item.ID, func(t *testing.T) {
			t.Parallel()
			_, err := NormalizeDesktopPromotions([]DesktopPromotion{item})
			require.Error(t, err)
		})
	}
}

func TestActiveDesktopPromotionsFiltersStatusAndWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	raw := `[
		{"id":"active","title":"Active","summary":"","target_url":"https://example.com/active","cta_label":"打开","icon":"link","surfaces":["discover"],"enabled":true,"sort_order":1,"starts_at":"2026-07-28T11:00:00Z","ends_at":"2026-07-28T13:00:00Z"},
		{"id":"disabled","title":"Disabled","summary":"","target_url":"https://example.com/disabled","cta_label":"打开","icon":"link","surfaces":["discover"],"enabled":false,"sort_order":2},
		{"id":"future","title":"Future","summary":"","target_url":"https://example.com/future","cta_label":"打开","icon":"link","surfaces":["discover"],"enabled":true,"sort_order":3,"starts_at":"2026-07-28T13:00:00Z"},
		{"id":"expired","title":"Expired","summary":"","target_url":"https://example.com/expired","cta_label":"打开","icon":"link","surfaces":["discover"],"enabled":true,"sort_order":4,"ends_at":"2026-07-28T12:00:00Z"}
	]`

	active := ActiveDesktopPromotions(raw, now)
	require.Len(t, active, 1)
	require.Equal(t, "active", active[0].ID)
}

func TestNormalizeDesktopPromotionsAllowsLoopbackHTTP(t *testing.T) {
	t.Parallel()

	items, err := NormalizeDesktopPromotions([]DesktopPromotion{{
		ID:        "local",
		Title:     "Local",
		TargetURL: "http://127.0.0.1:1420/path",
		Enabled:   true,
	}})
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:1420/path", items[0].TargetURL)
}
