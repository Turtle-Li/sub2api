package attachment_gateway

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	defaultURLRewriteMinBodyBytes = 512 * 1024
	defaultURLCacheTTL            = 15 * time.Minute
	defaultURLObjectPrefix        = "attachments/"
	defaultMaxConcurrentUploads   = 2
)

var (
	errObjectStorageConfigChanged = errors.New("attachment gateway: object storage config changed during upload")
	errObjectStorageURLUnsafe     = errors.New("attachment gateway: signed object URL is too close to expiry")
)

// ObjectStorage is the narrow storage contract needed by URL externalization.
// service.ImageStorage and the existing S3/R2 implementation satisfy it.
type ObjectStorage interface {
	Save(ctx context.Context, key, contentType string, data []byte) (string, error)
}

// contentAddressedObjectStorage is an optional capability implemented by the
// S3/R2 adapter. It avoids re-uploading a deterministic hash key after process
// restarts while still returning a fresh signed URL.
type contentAddressedObjectStorage interface {
	Ensure(ctx context.Context, key, contentType string, data []byte) (url string, uploaded bool, err error)
}

// readyObjectStorage is implemented by the dynamic Attachment R2 provider so
// a disabled or incomplete configuration can skip URL rewriting once per
// request instead of failing once per image.
type readyObjectStorage interface {
	Ready(ctx context.Context) bool
}

// versionedObjectStorage lets a hot config update invalidate cached presigned
// URLs that point at the previous bucket or credentials.
type versionedObjectStorage interface {
	CacheVersion() uint64
}

// URLConfig controls the optional second-stage conversion from inline data
// URLs to object-storage URLs. The zero value is disabled.
type URLConfig struct {
	Enabled              bool
	MinBodyBytes         int
	ObjectPrefix         string
	URLCacheTTL          time.Duration
	MaxImageBytes        int
	MaxImagesPerRequest  int
	MaxConcurrentUploads int
}

func (c URLConfig) withDefaults() URLConfig {
	if c.MinBodyBytes == 0 {
		c.MinBodyBytes = defaultURLRewriteMinBodyBytes
	}
	if strings.TrimSpace(c.ObjectPrefix) == "" {
		c.ObjectPrefix = defaultURLObjectPrefix
	}
	if c.URLCacheTTL == 0 {
		c.URLCacheTTL = defaultURLCacheTTL
	}
	if c.MaxImageBytes == 0 {
		c.MaxImageBytes = defaultMaxImageBytes
	}
	if c.MaxImagesPerRequest == 0 {
		c.MaxImagesPerRequest = defaultMaxImagesPerRequest
	}
	if c.MaxConcurrentUploads == 0 {
		c.MaxConcurrentUploads = defaultMaxConcurrentUploads
	}
	return c
}

func (c URLConfig) validate() error {
	if !c.Enabled {
		return nil
	}
	if c.MinBodyBytes <= 0 {
		return errors.New("attachment gateway: URL rewrite minimum body bytes must be positive")
	}
	if strings.TrimSpace(c.ObjectPrefix) == "" {
		return errors.New("attachment gateway: URL object prefix must not be empty")
	}
	if c.URLCacheTTL <= 0 {
		return errors.New("attachment gateway: URL cache TTL must be positive")
	}
	if c.MaxImageBytes <= 0 || c.MaxImagesPerRequest <= 0 || c.MaxConcurrentUploads <= 0 {
		return errors.New("attachment gateway: URL rewrite limits must be positive")
	}
	return nil
}

// URLMetrics is safe to log. It deliberately excludes object keys, URLs,
// hashes, image bytes and request content.
type URLMetrics struct {
	Enabled             bool
	StorageReady        bool
	StorageUnavailable  bool
	OriginalBodyBytes   int
	RewrittenBodyBytes  int
	ImageCount          int
	ExternalizedCount   int
	UploadCount         int
	CacheHits           int
	CacheShared         int
	SkippedBelowTrigger bool
	TimedOut            bool
	Errors              int
	DurationMS          float64
}

type URLResult struct {
	Body    []byte
	Metrics URLMetrics
}

type publishedURL struct {
	URL       string
	ExpiresAt time.Time
}

type publishResult struct {
	URL      string
	CacheHit bool
	Uploaded bool
}

// URLExternalizer uploads inline image bytes under deterministic content-hash
// keys and rewrites the request to HTTPS URLs. Failures are per-image fail-open.
type URLExternalizer struct {
	config URLConfig
	store  ObjectStorage
	now    func() time.Time

	mu           sync.Mutex
	published    map[string]publishedURL
	storeVersion uint64
	group        singleflight.Group
	slots        chan struct{}
}

func NewURLExternalizer(config URLConfig, store ObjectStorage) (*URLExternalizer, error) {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &URLExternalizer{
		config:    config,
		store:     store,
		now:       time.Now,
		published: make(map[string]publishedURL),
		slots:     make(chan struct{}, config.MaxConcurrentUploads),
	}, nil
}

func (e *URLExternalizer) Enabled() bool {
	return e != nil && e.config.Enabled && e.store != nil
}

func (e *URLExternalizer) Externalize(ctx context.Context, body []byte) (result URLResult) {
	started := time.Now()
	result = URLResult{Body: body, Metrics: URLMetrics{
		Enabled:            e.Enabled(),
		OriginalBodyBytes:  len(body),
		RewrittenBodyBytes: len(body),
	}}
	defer func() {
		result.Metrics.DurationMS = float64(time.Since(started)) / float64(time.Millisecond)
	}()
	defer func() {
		if recover() != nil {
			result.Body = body
			result.Metrics.RewrittenBodyBytes = len(body)
			result.Metrics.ExternalizedCount = 0
			result.Metrics.Errors++
		}
	}()
	if !e.Enabled() {
		return result
	}
	if readyStore, ok := e.store.(readyObjectStorage); ok && !readyStore.Ready(ctx) {
		result.Metrics.StorageUnavailable = true
		return result
	}
	result.Metrics.StorageReady = true
	storageVersion := e.syncStorageVersion()
	if len(body) < e.config.MinBodyBytes {
		result.Metrics.SkippedBelowTrigger = true
		return result
	}

	rewritten, changed, err := rewriteImageURLs(body, func(rawURL string) string {
		if ctx.Err() != nil {
			result.Metrics.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
			return rawURL
		}
		if !isImageDataURL(rawURL) {
			return rawURL
		}
		result.Metrics.ImageCount++
		if result.Metrics.ImageCount > e.config.MaxImagesPerRequest {
			return rawURL
		}
		parsed, _, parseErr := parseImageDataURL(rawURL, e.config.MaxImageBytes)
		if parseErr != nil {
			if !errors.Is(parseErr, errUnsupportedMediaType) {
				result.Metrics.Errors++
			}
			return rawURL
		}

		url, cacheHit, shared, uploaded, uploadErr := e.publish(ctx, parsed, storageVersion)
		if uploadErr != nil {
			if errors.Is(uploadErr, context.DeadlineExceeded) {
				result.Metrics.TimedOut = true
			} else if !errors.Is(uploadErr, context.Canceled) {
				result.Metrics.Errors++
			}
			return rawURL
		}
		if cacheHit {
			result.Metrics.CacheHits++
		} else if uploaded && !shared {
			result.Metrics.UploadCount++
		}
		if shared {
			result.Metrics.CacheShared++
		}
		result.Metrics.ExternalizedCount++
		return url
	})
	if err != nil {
		result.Metrics.Errors++
		return result
	}
	// A destination or credential switch during this request must not leave a
	// mixture of URLs from the previous storage generation in the forwarded
	// payload. Keep the optimized inline data URLs and let the next request use
	// the new generation instead.
	if !e.storageVersionCurrent(storageVersion) {
		result.Metrics.Errors++
		return result
	}
	if ctx.Err() != nil {
		result.Metrics.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	}
	if changed {
		result.Body = rewritten
		result.Metrics.RewrittenBodyBytes = len(rewritten)
	}
	return result
}

func (e *URLExternalizer) publish(ctx context.Context, image dataURLImage, storageVersion uint64) (url string, cacheHit bool, shared bool, uploaded bool, err error) {
	hash := optimizedHash(image.Bytes)
	if !e.storageVersionCurrent(storageVersion) {
		return "", false, false, false, errObjectStorageConfigChanged
	}
	if cached, ok := e.cached(hash, storageVersion); ok {
		return cached, true, false, false, nil
	}

	flightKey := hash
	if _, ok := e.store.(versionedObjectStorage); ok {
		flightKey = strconv.FormatUint(storageVersion, 10) + ":" + hash
	}
	channel := e.group.DoChan(flightKey, func() (any, error) {
		if !e.storageVersionCurrent(storageVersion) {
			return "", errObjectStorageConfigChanged
		}
		if cached, ok := e.cached(hash, storageVersion); ok {
			return publishResult{URL: cached, CacheHit: true}, nil
		}
		select {
		case e.slots <- struct{}{}:
			defer func() { <-e.slots }()
		case <-ctx.Done():
			return "", ctx.Err()
		}
		key := e.objectKey(hash, image.MIMEType)
		published := ""
		didUpload := true
		var saveErr error
		if contentStore, ok := e.store.(contentAddressedObjectStorage); ok {
			published, didUpload, saveErr = contentStore.Ensure(ctx, key, image.MIMEType, image.Bytes)
		} else {
			published, saveErr = e.store.Save(ctx, key, image.MIMEType, image.Bytes)
		}
		if saveErr != nil {
			return "", saveErr
		}
		if !isFetchableAttachmentURL(published) {
			return "", errors.New("attachment gateway: object storage returned a non-HTTPS URL")
		}
		if rememberErr := e.remember(hash, published, storageVersion); rememberErr != nil {
			return "", rememberErr
		}
		return publishResult{URL: published, CacheHit: !didUpload, Uploaded: didUpload}, nil
	})

	select {
	case <-ctx.Done():
		return "", false, false, false, ctx.Err()
	case outcome := <-channel:
		if outcome.Err != nil {
			return "", false, outcome.Shared, false, outcome.Err
		}
		published, ok := outcome.Val.(publishResult)
		if !ok || published.URL == "" {
			return "", false, outcome.Shared, false, errors.New("attachment gateway: invalid published URL result")
		}
		return published.URL, published.CacheHit, outcome.Shared, published.Uploaded, nil
	}
}

func (e *URLExternalizer) remember(hash, published string, storageVersion uint64) error {
	if !e.storageVersionCurrent(storageVersion) {
		return errObjectStorageConfigChanged
	}
	now := e.now()
	expiresAt := now.Add(e.config.URLCacheTTL)
	if signedExpiry, ok := presignedURLExpiry(published); ok {
		// Never hand out a URL from the process cache near its signing expiry.
		// One minute is intentionally conservative relative to OpenAI fetch time.
		safeExpiry := signedExpiry.Add(-time.Minute)
		if safeExpiry.Before(expiresAt) {
			expiresAt = safeExpiry
		}
	}
	if !expiresAt.After(now) {
		return errObjectStorageURLUnsafe
	}
	e.mu.Lock()
	if _, ok := e.store.(versionedObjectStorage); ok && e.storeVersion != storageVersion {
		e.mu.Unlock()
		return errObjectStorageConfigChanged
	}
	e.published[hash] = publishedURL{URL: published, ExpiresAt: expiresAt}
	e.mu.Unlock()
	if !e.storageVersionCurrent(storageVersion) {
		return errObjectStorageConfigChanged
	}
	return nil
}

func (e *URLExternalizer) syncStorageVersion() uint64 {
	versioned, ok := e.store.(versionedObjectStorage)
	if !ok {
		return 0
	}
	version := versioned.CacheVersion()
	e.mu.Lock()
	defer e.mu.Unlock()
	if version == e.storeVersion {
		return version
	}
	e.published = make(map[string]publishedURL)
	e.storeVersion = version
	return version
}

func (e *URLExternalizer) storageVersionCurrent(expected uint64) bool {
	versioned, ok := e.store.(versionedObjectStorage)
	return !ok || versioned.CacheVersion() == expected
}

func presignedURLExpiry(raw string) (time.Time, bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return time.Time{}, false
	}
	query := parsed.Query()
	dateValue := query.Get("X-Amz-Date")
	expiresValue := query.Get("X-Amz-Expires")
	if dateValue == "" || expiresValue == "" {
		return time.Time{}, false
	}
	signedAt, err := time.Parse("20060102T150405Z", dateValue)
	if err != nil {
		return time.Time{}, false
	}
	expiresSeconds, err := strconv.ParseInt(expiresValue, 10, 64)
	if err != nil || expiresSeconds <= 0 {
		return time.Time{}, false
	}
	return signedAt.Add(time.Duration(expiresSeconds) * time.Second), true
}

func (e *URLExternalizer) cached(hash string, storageVersion uint64) (string, bool) {
	if !e.storageVersionCurrent(storageVersion) {
		return "", false
	}
	now := e.now()
	e.mu.Lock()
	if _, ok := e.store.(versionedObjectStorage); ok && e.storeVersion != storageVersion {
		e.mu.Unlock()
		return "", false
	}
	entry, ok := e.published[hash]
	if !ok {
		e.mu.Unlock()
		return "", false
	}
	if !now.Before(entry.ExpiresAt) {
		delete(e.published, hash)
		e.mu.Unlock()
		return "", false
	}
	url := entry.URL
	e.mu.Unlock()
	if !e.storageVersionCurrent(storageVersion) {
		return "", false
	}
	return url, true
}

func (e *URLExternalizer) objectKey(hash, mimeType string) string {
	extension := ".bin"
	switch mimeType {
	case "image/png":
		extension = ".png"
	case "image/jpeg":
		extension = ".jpg"
	case "image/webp":
		extension = ".webp"
	}
	prefix := strings.Trim(strings.TrimSpace(e.config.ObjectPrefix), "/")
	return path.Join(prefix, hash[:2], hash+extension)
}

func isFetchableAttachmentURL(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "https://")
}
