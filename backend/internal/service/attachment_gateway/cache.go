package attachment_gateway

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type cacheLookup struct {
	Image optimizedImage
	Hit   bool
}

type imageCache struct {
	dir             string
	ttl             time.Duration
	maxBytes        int64
	cleanupInterval time.Duration
	policy          string
	optimizer       string
	now             func() time.Time
	flight          singleflight.Group
	filesMu         sync.RWMutex
	cleanupStateMu  sync.Mutex
	cleanupRunning  bool
	lastCleanup     time.Time
}

type cacheEntry struct {
	hash         string
	imagePath    string
	metadataPath string
	size         int64
	createdAt    time.Time
}

func newImageCache(dir string, ttl time.Duration, maxBytes int64, cleanupInterval time.Duration, policy, optimizer string) *imageCache {
	return &imageCache{
		dir:             filepath.Clean(dir),
		ttl:             ttl,
		maxBytes:        maxBytes,
		cleanupInterval: cleanupInterval,
		policy:          policy,
		optimizer:       optimizer,
		now:             time.Now,
	}
}

func (c *imageCache) getOrCreate(
	ctx context.Context,
	hash string,
	create func() (optimizedImage, error),
) (optimizedImage, bool, bool, error) {
	if cached, ok := c.load(hash); ok {
		return cached, true, false, nil
	}

	resultChannel := c.flight.DoChan(hash+":"+c.policy, func() (any, error) {
		if cached, ok := c.load(hash); ok {
			return cacheLookup{Image: cached, Hit: true}, nil
		}
		created, err := create()
		if err != nil {
			return cacheLookup{}, err
		}
		if err := c.store(hash, created); err != nil {
			return cacheLookup{}, err
		}
		return cacheLookup{Image: created, Hit: false}, nil
	})

	select {
	case <-ctx.Done():
		return optimizedImage{}, false, false, ctx.Err()
	case result := <-resultChannel:
		if result.Err != nil {
			return optimizedImage{}, false, result.Shared, result.Err
		}
		lookup, ok := result.Val.(cacheLookup)
		if !ok {
			return optimizedImage{}, false, result.Shared, errors.New("attachment gateway: invalid cache singleflight result")
		}
		// A shared in-flight encode is tracked separately from a persisted-cache
		// hit. Treating Shared as Hit would overstate cache effectiveness during a
		// cold stampede and hide the encode that still occurred.
		return lookup.Image, lookup.Hit, result.Shared, nil
	}
}

func (c *imageCache) load(hash string) (optimizedImage, bool) {
	c.filesMu.RLock()
	defer c.filesMu.RUnlock()
	return c.loadLocked(hash)
}

func (c *imageCache) loadLocked(hash string) (optimizedImage, bool) {
	imagePath, metadataPath := c.paths(hash)
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		return optimizedImage{}, false
	}
	var metadata Metadata
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		return optimizedImage{}, false
	}
	if metadata.OriginalHash != hash ||
		metadata.Policy != c.policy ||
		metadata.Optimizer != c.optimizer ||
		!metadata.ExpiresAt.After(c.now().UTC()) {
		return optimizedImage{}, false
	}
	encoded, err := os.ReadFile(imagePath)
	if err != nil || len(encoded) != metadata.OptimizedSize {
		return optimizedImage{}, false
	}
	if optimizedHash(encoded) != metadata.OptimizedHash {
		return optimizedImage{}, false
	}
	return optimizedImage{Bytes: encoded, Metadata: metadata}, true
}

func (c *imageCache) store(hash string, image optimizedImage) error {
	c.filesMu.Lock()
	err := c.storeLocked(hash, image)
	c.filesMu.Unlock()
	if err == nil {
		c.triggerCleanup()
	}
	return err
}

func (c *imageCache) storeLocked(hash string, image optimizedImage) error {
	if image.Metadata.OriginalHash != hash {
		return errors.New("attachment gateway: cache metadata hash mismatch")
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return fmt.Errorf("attachment gateway: create cache directory: %w", err)
	}
	if err := os.Chmod(c.dir, 0o700); err != nil {
		return fmt.Errorf("attachment gateway: secure cache directory: %w", err)
	}
	imagePath, metadataPath := c.paths(hash)
	if err := atomicWrite(imagePath, image.Bytes); err != nil {
		return err
	}
	metadataBytes, err := json.MarshalIndent(image.Metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("attachment gateway: encode cache metadata: %w", err)
	}
	if err := atomicWrite(metadataPath, metadataBytes); err != nil {
		return err
	}
	return nil
}

// triggerCleanup starts at most one best-effort cache cleanup per interval.
// Request processing never waits for the directory scan or eviction work.
func (c *imageCache) triggerCleanup() {
	now := c.now().UTC()
	c.cleanupStateMu.Lock()
	if c.cleanupRunning || (!c.lastCleanup.IsZero() && now.Before(c.lastCleanup.Add(c.cleanupInterval))) {
		c.cleanupStateMu.Unlock()
		return
	}
	c.cleanupRunning = true
	c.lastCleanup = now
	c.cleanupStateMu.Unlock()

	go func() {
		_ = c.cleanup()
		c.cleanupStateMu.Lock()
		c.cleanupRunning = false
		c.cleanupStateMu.Unlock()
	}()
}

// cleanup removes expired or malformed cache pairs, then evicts the oldest
// remaining pairs until the configured byte budget is met. Unknown files,
// temporary files, directories and non-cache names are deliberately ignored.
func (c *imageCache) cleanup() error {
	c.filesMu.Lock()
	defer c.filesMu.Unlock()

	directoryEntries, err := os.ReadDir(c.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("attachment gateway: read cache directory: %w", err)
	}

	pairs := make(map[string]map[string]string)
	for _, directoryEntry := range directoryEntries {
		if directoryEntry.IsDir() {
			continue
		}
		entryInfo, infoErr := directoryEntry.Info()
		if infoErr != nil || !entryInfo.Mode().IsRegular() {
			continue
		}
		hash, extension, ok := parseCacheFilename(directoryEntry.Name())
		if !ok {
			continue
		}
		if pairs[hash] == nil {
			pairs[hash] = make(map[string]string, 2)
		}
		pairs[hash][extension] = filepath.Join(c.dir, directoryEntry.Name())
	}

	now := c.now().UTC()
	validEntries := make([]cacheEntry, 0, len(pairs))
	var totalBytes int64
	var cleanupErr error
	for hash, pair := range pairs {
		imagePath, hasImage := pair[".webp"]
		metadataPath, hasMetadata := pair[".json"]
		if !hasImage || !hasMetadata {
			continue
		}

		metadataBytes, readErr := os.ReadFile(metadataPath)
		var metadata Metadata
		metadataErr := json.Unmarshal(metadataBytes, &metadata)
		if readErr != nil || metadataErr != nil || metadata.OriginalHash != hash || metadata.ExpiresAt.IsZero() {
			cleanupErr = errors.Join(cleanupErr, removeCachePair(imagePath, metadataPath))
			continue
		}
		if !metadata.ExpiresAt.After(now) {
			cleanupErr = errors.Join(cleanupErr, removeCachePair(imagePath, metadataPath))
			continue
		}

		imageInfo, imageInfoErr := os.Stat(imagePath)
		metadataInfo, metadataInfoErr := os.Stat(metadataPath)
		if imageInfoErr != nil || metadataInfoErr != nil {
			if imageInfoErr != nil && !errors.Is(imageInfoErr, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, imageInfoErr)
			}
			if metadataInfoErr != nil && !errors.Is(metadataInfoErr, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, metadataInfoErr)
			}
			continue
		}
		createdAt := metadata.CreatedAt
		if createdAt.IsZero() {
			createdAt = metadataInfo.ModTime()
		}
		entrySize := imageInfo.Size() + metadataInfo.Size()
		validEntries = append(validEntries, cacheEntry{
			hash:         hash,
			imagePath:    imagePath,
			metadataPath: metadataPath,
			size:         entrySize,
			createdAt:    createdAt,
		})
		totalBytes += entrySize
	}

	if totalBytes <= c.maxBytes {
		return cleanupErr
	}
	sort.Slice(validEntries, func(left, right int) bool {
		if validEntries[left].createdAt.Equal(validEntries[right].createdAt) {
			return validEntries[left].hash < validEntries[right].hash
		}
		return validEntries[left].createdAt.Before(validEntries[right].createdAt)
	})
	for _, entry := range validEntries {
		if totalBytes <= c.maxBytes {
			break
		}
		if removeErr := removeCachePair(entry.imagePath, entry.metadataPath); removeErr != nil {
			cleanupErr = errors.Join(cleanupErr, removeErr)
			continue
		}
		totalBytes -= entry.size
	}
	return cleanupErr
}

func parseCacheFilename(name string) (string, string, bool) {
	extension := filepath.Ext(name)
	if extension != ".json" && extension != ".webp" {
		return "", "", false
	}
	hash := strings.TrimSuffix(name, extension)
	if len(hash) != sha256HexLength || strings.ToLower(hash) != hash {
		return "", "", false
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", "", false
	}
	return hash, extension, true
}

func removeCachePair(imagePath, metadataPath string) error {
	var removeErr error
	for _, path := range []string{imagePath, metadataPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErr = errors.Join(removeErr, fmt.Errorf("attachment gateway: remove cache file: %w", err))
		}
	}
	return removeErr
}

func (c *imageCache) paths(hash string) (string, string) {
	return filepath.Join(c.dir, hash+".webp"), filepath.Join(c.dir, hash+".json")
}

func atomicWrite(path string, content []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".attachment-gateway-*")
	if err != nil {
		return fmt.Errorf("attachment gateway: create temporary cache file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("attachment gateway: secure temporary cache file: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("attachment gateway: write temporary cache file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("attachment gateway: sync temporary cache file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("attachment gateway: close temporary cache file: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("attachment gateway: publish cache file: %w", err)
	}
	return nil
}
