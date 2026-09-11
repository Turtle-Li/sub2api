//go:build embed

package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHTMLCacheExpiresInjectedSettingsFromPeerUpdates(t *testing.T) {
	cache := NewHTMLCache()
	cache.SetBaseHTML([]byte("app"))
	cache.Set([]byte("checkout disabled"), []byte(`{"payment_enabled":false}`))
	old := cache.Get()
	require.NotNil(t, old)
	// Advance the stored deadline without a wall-clock sleep or local callback.
	cache.expiresAt = time.Now().Add(-time.Second)
	require.Nil(t, cache.Get())
	cache.Set([]byte("checkout enabled"), []byte(`{"payment_enabled":true}`))
	fresh := cache.Get()
	require.NotNil(t, fresh)
	require.Equal(t, "checkout enabled", string(fresh.Content))
	require.NotEqual(t, old.ETag, fresh.ETag)
}
