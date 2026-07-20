package attachment_gateway

import (
	"context"
	"errors"
	"path"
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

	mu        sync.Mutex
	published map[string]publishedURL
	group     singleflight.Group
	slots     chan struct{}
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

		url, cacheHit, shared, uploaded, uploadErr := e.publish(ctx, parsed)
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
	if ctx.Err() != nil {
		result.Metrics.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	}
	if changed {
		result.Body = rewritten
		result.Metrics.RewrittenBodyBytes = len(rewritten)
	}
	return result
}

func (e *URLExternalizer) publish(ctx context.Context, image dataURLImage) (url string, cacheHit bool, shared bool, uploaded bool, err error) {
	hash := optimizedHash(image.Bytes)
	if cached, ok := e.cached(hash); ok {
		return cached, true, false, false, nil
	}

	channel := e.group.DoChan(hash, func() (any, error) {
		if cached, ok := e.cached(hash); ok {
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
		e.mu.Lock()
		e.published[hash] = publishedURL{URL: published, ExpiresAt: e.now().Add(e.config.URLCacheTTL)}
		e.mu.Unlock()
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

func (e *URLExternalizer) cached(hash string) (string, bool) {
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	entry, ok := e.published[hash]
	if !ok {
		return "", false
	}
	if !now.Before(entry.ExpiresAt) {
		delete(e.published, hash)
		return "", false
	}
	return entry.URL, true
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
