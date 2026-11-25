package attachment_gateway

import (
	"container/list"
	"crypto/sha256"
	"os"
	"reflect"
	"sync"
)

const (
	warmCacheMaxEntries = 2048
	warmCacheMaxBytes   = 32 << 20
)

// A warm entry owns only already-verified optimized bytes, never request
// strings or decoded source data. Keeping the bytes under a separate hard cap
// also guarantees that storage failure, expiry or a generation flip can use
// the exact inline candidate even if disk cleanup runs during publication.
type warmImageCache struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]*list.Element
	lru     list.List
	bytes   int64
}

type warmCacheKey struct {
	digest  [sha256.Size]byte
	enabled bool
}

type warmImageEntry struct {
	tokenHash  [sha256.Size]byte
	image      optimizedImage
	sourceSize int
	files      cacheFilePair
}

type cacheFilePair struct {
	image, metadata         os.FileInfo
	imagePath, metadataPath string
}

func (c *imageCache) warmFiles(hash string) cacheFilePair {
	imagePath, metaPath := c.paths(hash)
	return (cacheFilePair{imagePath: imagePath, metadataPath: metaPath}).stat()
}

func (p cacheFilePair) stat() cacheFilePair {
	p.image, _ = os.Stat(p.imagePath)
	p.metadata, _ = os.Stat(p.metadataPath)
	return p
}

func (p cacheFilePair) matches(other cacheFilePair) bool {
	return sameCacheFile(p.image, other.image) && sameCacheFile(p.metadata, other.metadata)
}

// os.Stat returns one concrete Sys type on each platform. Resolve the field
// once, not for every request and file.
var statCtimeField struct {
	once  sync.Once
	index int
}

func sameCacheFile(expected, actual os.FileInfo) bool {
	if expected == nil || actual == nil || !actual.Mode().IsRegular() ||
		expected.Mode() != actual.Mode() || expected.Size() != actual.Size() ||
		!expected.ModTime().Equal(actual.ModTime()) || !os.SameFile(expected, actual) {
		return false
	}
	// A restored mtime must not hide an in-place rewrite or chmod. Use
	// the platform's ctime when exposed by os.Stat; otherwise use the
	// ordinary verified disk read rather than weakening the check.
	old, next := reflectedStat(expected), reflectedStat(actual)
	if !old.IsValid() || !next.IsValid() || old.Type() != next.Type() {
		return false
	}
	statCtimeField.once.Do(func() {
		statCtimeField.index = -1
		for _, name := range [...]string{"Ctim", "Ctimespec"} {
			if field, ok := old.Type().FieldByName(name); ok && field.Type.Comparable() && len(field.Index) == 1 {
				statCtimeField.index = field.Index[0]
				break
			}
		}
	})
	field := statCtimeField.index
	return field >= 0 && old.Field(field).Equal(next.Field(field))
}

func reflectedStat(info os.FileInfo) reflect.Value {
	stat := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if stat.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	return stat
}

func (c *imageCache) warmImage(key [sha256.Size]byte) (warmImageEntry, bool) {
	w := &c.warm
	w.mu.Lock()
	elem := w.entries[key]
	if elem == nil {
		w.mu.Unlock()
		return warmImageEntry{}, false
	}
	entry, ok := elem.Value.(warmImageEntry)
	if !ok {
		w.lru.Remove(elem)
		delete(w.entries, key)
		w.mu.Unlock()
		return warmImageEntry{}, false
	}
	w.lru.MoveToFront(elem)
	w.mu.Unlock()

	c.filesMu.RLock()
	meta := entry.image.Metadata
	valid := meta.Policy == c.policy && meta.Optimizer == c.optimizer &&
		meta.ExpiresAt.After(c.now().UTC()) && entry.files.matches(entry.files.stat())
	c.filesMu.RUnlock()
	if !valid {
		w.mu.Lock()
		if w.entries[key] == elem {
			w.remove(elem)
		}
		w.mu.Unlock()
		return warmImageEntry{}, false
	}
	return entry, true
}

func (c *imageCache) rememberWarmImage(entry warmImageEntry) {
	limit := min(int64(warmCacheMaxBytes), c.maxBytes)
	size := int64(cap(entry.image.Bytes))
	if entry.files.image == nil || size == 0 || size > limit {
		return
	}
	w := &c.warm
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.entries == nil {
		w.entries = make(map[[sha256.Size]byte]*list.Element)
	}
	if existing := w.entries[entry.tokenHash]; existing != nil {
		w.remove(existing)
	}
	for w.lru.Len() >= warmCacheMaxEntries || w.bytes+size > limit {
		w.remove(w.lru.Back())
	}
	w.entries[entry.tokenHash] = w.lru.PushFront(entry)
	w.bytes += size
}

func (w *warmImageCache) remove(elem *list.Element) {
	if entry, ok := elem.Value.(warmImageEntry); ok {
		delete(w.entries, entry.tokenHash)
		w.bytes -= int64(cap(entry.image.Bytes))
	}
	w.lru.Remove(elem)
}

// Only a persisted cache hit verified by the legacy hash check can populate
// the warm index. Stat both files on either side of that read so external
// replacement cannot associate verified bytes with the wrong file generation.
func (c *imageCache) loadForWarmIndex(hash string) (optimizedImage, cacheFilePair, bool) {
	c.filesMu.RLock()
	defer c.filesMu.RUnlock()
	files := c.warmFiles(hash)
	image, hit := c.loadLocked(hash)
	if !hit || !files.matches(files.stat()) {
		files = cacheFilePair{}
	}
	return image, files, hit
}
