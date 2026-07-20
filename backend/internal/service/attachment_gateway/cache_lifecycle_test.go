package attachment_gateway

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCacheCleanupRemovesExpiredPairsAndProtectsUnknownFiles(t *testing.T) {
	cacheDir := t.TempDir()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	cache := newImageCache(cacheDir, time.Hour, 1<<30, time.Minute, "test-policy", "test-optimizer")
	cache.now = func() time.Time { return now }

	expiredHash := sourceHash([]byte("expired"))
	liveHash := sourceHash([]byte("live"))
	writeCachePairForCleanupTest(t, cache, expiredHash, now.Add(-2*time.Hour), now.Add(-time.Minute), 100)
	writeCachePairForCleanupTest(t, cache, liveHash, now.Add(-time.Hour), now.Add(time.Hour), 100)
	unknownPath := filepath.Join(cacheDir, "do-not-delete.txt")
	temporaryPath := filepath.Join(cacheDir, ".attachment-gateway-do-not-delete")
	orphanHash := sourceHash([]byte("orphan"))
	orphanPath := filepath.Join(cacheDir, orphanHash+".webp")
	require.NoError(t, os.WriteFile(unknownPath, []byte("owner data"), 0o600))
	require.NoError(t, os.WriteFile(temporaryPath, []byte("temporary"), 0o600))
	require.NoError(t, os.WriteFile(orphanPath, []byte("orphan"), 0o600))

	require.NoError(t, cache.cleanup())

	assertPathMissing(t, filepath.Join(cacheDir, expiredHash+".webp"))
	assertPathMissing(t, filepath.Join(cacheDir, expiredHash+".json"))
	assertPathExists(t, filepath.Join(cacheDir, liveHash+".webp"))
	assertPathExists(t, filepath.Join(cacheDir, liveHash+".json"))
	assertPathExists(t, unknownPath)
	assertPathExists(t, temporaryPath)
	assertPathExists(t, orphanPath)
}

func TestCacheCleanupEvictsOldestPairsToByteBudget(t *testing.T) {
	cacheDir := t.TempDir()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	cache := newImageCache(cacheDir, time.Hour, 1<<30, time.Minute, "test-policy", "test-optimizer")
	cache.now = func() time.Time { return now }

	hashes := []string{
		sourceHash([]byte("oldest")),
		sourceHash([]byte("middle")),
		sourceHash([]byte("newest")),
	}
	sizes := make([]int64, 0, len(hashes))
	for index, hash := range hashes {
		sizes = append(sizes, writeCachePairForCleanupTest(
			t, cache, hash, now.Add(time.Duration(index-3)*time.Hour), now.Add(time.Hour), 200,
		))
	}
	cache.maxBytes = sizes[1] + sizes[2]

	require.NoError(t, cache.cleanup())

	assertPathMissing(t, filepath.Join(cacheDir, hashes[0]+".webp"))
	assertPathMissing(t, filepath.Join(cacheDir, hashes[0]+".json"))
	for _, hash := range hashes[1:] {
		assertPathExists(t, filepath.Join(cacheDir, hash+".webp"))
		assertPathExists(t, filepath.Join(cacheDir, hash+".json"))
	}
}

func writeCachePairForCleanupTest(
	t *testing.T,
	cache *imageCache,
	hash string,
	createdAt time.Time,
	expiresAt time.Time,
	imageSize int,
) int64 {
	t.Helper()
	require.NoError(t, os.MkdirAll(cache.dir, 0o700))
	imageBytes := bytes.Repeat([]byte{0x42}, imageSize)
	metadata := Metadata{
		OriginalHash:  hash,
		OptimizedHash: optimizedHash(imageBytes),
		OriginalSize:  imageSize * 2,
		OptimizedSize: imageSize,
		MIMEType:      "image/webp",
		Policy:        cache.policy,
		Optimizer:     cache.optimizer,
		CreatedAt:     createdAt,
		ExpiresAt:     expiresAt,
	}
	metadataBytes, err := json.Marshal(metadata)
	require.NoError(t, err)
	imagePath, metadataPath := cache.paths(hash)
	require.NoError(t, atomicWrite(imagePath, imageBytes))
	require.NoError(t, atomicWrite(metadataPath, metadataBytes))
	imageInfo, err := os.Stat(imagePath)
	require.NoError(t, err)
	metadataInfo, err := os.Stat(metadataPath)
	require.NoError(t, err)
	return imageInfo.Size() + metadataInfo.Size()
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	require.NoError(t, err)
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}
