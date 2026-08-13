package attachment_gateway

import (
	"context"
	"errors"
	"fmt"
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
	errObjectStorageConfigChanged   = errors.New("attachment gateway: object storage config changed during upload")
	errObjectStorageURLUnsafe       = errors.New("attachment gateway: signed object URL is too close to expiry")
	errObjectStorageWriteSuppressed = errors.New("attachment gateway: object storage write suppressed")
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
	if c.MaxImageBytes <= 0 || c.MaxConcurrentUploads <= 0 {
		return errors.New("attachment gateway: URL rewrite limits must be positive")
	}
	if c.MaxImagesPerRequest <= 0 || c.MaxImagesPerRequest > maxImagesPerRequest {
		return fmt.Errorf("attachment gateway: URL rewrite max images per request must be between 1 and %d", maxImagesPerRequest)
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
	WriteSuppressed     bool
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

type imageURLPublishJob struct {
	index   int
	image   dataURLImage
	release func()
}

type imageURLPublishOutcome struct {
	index    int
	url      string
	cacheHit bool
	shared   bool
	uploaded bool
	err      error
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
	parseSlots   chan struct{}
}

func NewURLExternalizer(config URLConfig, store ObjectStorage) (*URLExternalizer, error) {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &URLExternalizer{
		config:     config,
		store:      store,
		now:        time.Now,
		published:  make(map[string]publishedURL),
		slots:      make(chan struct{}, config.MaxConcurrentUploads),
		parseSlots: make(chan struct{}, config.MaxConcurrentUploads),
	}, nil
}

func (e *URLExternalizer) Enabled() bool {
	return e != nil && e.config.Enabled && e.store != nil
}

func (e *URLExternalizer) Externalize(ctx context.Context, body []byte) (result URLResult) {
	return e.externalize(ctx, body, nil, nil)
}

// ExternalizeWithWriteGuard applies normal URL externalization while checking a
// live write guard immediately before every physical object-storage write. A
// nil guard permits writes. It is used by the handler's normal and background
// paths so an emergency rollout change also covers a request queued for an
// upload slot.
func (e *URLExternalizer) ExternalizeWithWriteGuard(
	ctx context.Context,
	body []byte,
	canWrite func() bool,
) (result URLResult) {
	return e.externalize(ctx, body, nil, canWrite)
}

// ExternalizeSelected is the cancellation-recovery variant of Externalize.
// It only publishes the retained image-token positions, which prevents a
// partial cache rehydrate from uploading untouched source data URLs.
func (e *URLExternalizer) ExternalizeSelected(ctx context.Context, body []byte, selectedIndexes []int) (result URLResult) {
	return e.externalizeSelected(ctx, body, selectedIndexes, nil)
}

// ExternalizeSelectedWithWriteGuard is the cancellation-recovery variant for
// a live rollout. canWrite is re-evaluated immediately after an upload slot is
// acquired, before object storage is touched. A nil guard permits writes.
func (e *URLExternalizer) ExternalizeSelectedWithWriteGuard(
	ctx context.Context,
	body []byte,
	selectedIndexes []int,
	canWrite func() bool,
) (result URLResult) {
	return e.externalizeSelected(ctx, body, selectedIndexes, canWrite)
}

func (e *URLExternalizer) externalizeSelected(
	ctx context.Context,
	body []byte,
	selectedIndexes []int,
	canWrite func() bool,
) (result URLResult) {
	if len(selectedIndexes) == 0 {
		return URLResult{Body: body, Metrics: URLMetrics{
			Enabled:            e.Enabled(),
			OriginalBodyBytes:  len(body),
			RewrittenBodyBytes: len(body),
		}}
	}
	selected := make(map[int]struct{}, len(selectedIndexes))
	for _, index := range selectedIndexes {
		if index < 0 {
			return URLResult{Body: body, Metrics: URLMetrics{
				Enabled:            e.Enabled(),
				OriginalBodyBytes:  len(body),
				RewrittenBodyBytes: len(body),
				Errors:             1,
			}}
		}
		selected[index] = struct{}{}
	}
	return e.externalize(ctx, body, selected, canWrite)
}

func (e *URLExternalizer) externalize(
	ctx context.Context,
	body []byte,
	selected map[int]struct{},
	canWrite func() bool,
) (result URLResult) {
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

	tokens, _, err := collectImageDataURLTokens(body, e.config.MaxImagesPerRequest)
	if err != nil {
		result.Metrics.Errors++
		return result
	}
	if len(tokens) == 0 {
		return result
	}
	if selected != nil {
		for index := range selected {
			if index >= len(tokens) {
				result.Metrics.Errors++
				return result
			}
		}
	}

	selectedCount := len(tokens)
	if selected != nil {
		selectedCount = len(selected)
		if selectedCount > len(tokens) {
			result.Metrics.Errors++
			return result
		}
	}
	if selectedCount == 0 {
		return result
	}
	workerCount := e.config.MaxConcurrentUploads
	if workerCount > selectedCount {
		workerCount = selectedCount
	}
	jobs := make(chan imageURLPublishJob)
	outcomes := make(chan imageURLPublishOutcome, len(tokens))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range jobs {
				func() {
					defer job.release()
					url, cacheHit, shared, uploaded, publishErr := e.publishSafely(ctx, job.image, storageVersion, canWrite)
					outcomes <- imageURLPublishOutcome{
						index:    job.index,
						url:      url,
						cacheHit: cacheHit,
						shared:   shared,
						uploaded: uploaded,
						err:      publishErr,
					}
				}()
			}
		}()
	}

	dispatchStopped := false
	for index, token := range tokens {
		if selected != nil {
			if _, wanted := selected[index]; !wanted {
				continue
			}
		}
		if ctx.Err() != nil {
			result.Metrics.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
			dispatchStopped = true
			break
		}
		rawURL, tokenErr := imageURLTokenValue(body, token)
		if tokenErr != nil {
			result.Metrics.Errors++
			dispatchStopped = true
			break
		}
		result.Metrics.ImageCount++
		parsed, release, parseErr := e.parseImageDataURLWithSlot(ctx, rawURL)
		if parseErr != nil {
			if !errors.Is(parseErr, errUnsupportedMediaType) {
				result.Metrics.Errors++
			}
			release()
			continue
		}
		select {
		case jobs <- imageURLPublishJob{index: index, image: parsed, release: release}:
		case <-ctx.Done():
			result.Metrics.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
			release()
			dispatchStopped = true
		}
		if dispatchStopped {
			break
		}
	}
	close(jobs)
	workers.Wait()
	close(outcomes)

	rewritten := make([]imageURLRewrite, len(tokens))
	for outcome := range outcomes {
		if outcome.err != nil {
			if errors.Is(outcome.err, context.DeadlineExceeded) {
				result.Metrics.TimedOut = true
			} else if errors.Is(outcome.err, errObjectStorageWriteSuppressed) {
				result.Metrics.WriteSuppressed = true
			} else if !errors.Is(outcome.err, context.Canceled) {
				result.Metrics.Errors++
			}
			continue
		}
		if outcome.cacheHit {
			result.Metrics.CacheHits++
		} else if outcome.uploaded && !outcome.shared {
			result.Metrics.UploadCount++
		}
		if outcome.shared {
			result.Metrics.CacheShared++
		}
		result.Metrics.ExternalizedCount++
		rewritten[outcome.index] = imageURLRewrite{value: outcome.url, changed: true}
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
	rewrittenBody, changed, rewriteErr := rewriteImageURLTokens(body, tokens, rewritten)
	if rewriteErr != nil {
		result.Metrics.Errors++
		return result
	}
	if changed {
		result.Body = rewrittenBody
		result.Metrics.RewrittenBodyBytes = len(rewrittenBody)
	}
	return result
}

func (e *URLExternalizer) parseImageDataURLWithSlot(ctx context.Context, rawURL string) (dataURLImage, func(), error) {
	select {
	case e.parseSlots <- struct{}{}:
	case <-ctx.Done():
		return dataURLImage{}, func() {}, ctx.Err()
	}
	released := false
	release := func() {
		if !released {
			released = true
			<-e.parseSlots
		}
	}
	parsed, _, err := parseImageDataURL(rawURL, e.config.MaxImageBytes)
	if err != nil {
		release()
		return parsed, func() {}, err
	}
	return parsed, release, nil
}

func (e *URLExternalizer) publishSafely(
	ctx context.Context,
	image dataURLImage,
	storageVersion uint64,
	canWrite func() bool,
) (url string, cacheHit bool, shared bool, uploaded bool, err error) {
	defer func() {
		if recover() != nil {
			url = ""
			cacheHit = false
			shared = false
			uploaded = false
			err = errors.New("attachment gateway: image upload panicked")
		}
	}()
	return e.publish(ctx, image, storageVersion, canWrite)
}

func (e *URLExternalizer) publish(
	ctx context.Context,
	image dataURLImage,
	storageVersion uint64,
	canWrite func() bool,
) (url string, cacheHit bool, shared bool, uploaded bool, err error) {
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
	channel := e.group.DoChan(flightKey, func() (value any, resultErr error) {
		workCtx, cancelWork := detachedWorkContext(ctx)
		defer cancelWork()
		defer func() {
			if recover() != nil {
				value = nil
				resultErr = errors.New("attachment gateway: object storage upload panicked")
			}
		}()
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
		// The handler has already checked its live rollout control before this
		// background operation started. Check once more after any queueing for the
		// bounded upload slot and immediately before storage I/O, so an emergency
		// rollback cannot be bypassed by an in-flight wait.
		if canWrite != nil && !canWrite() {
			return "", errObjectStorageWriteSuppressed
		}
		key := e.objectKey(hash, image.MIMEType)
		published := ""
		didUpload := true
		var saveErr error
		if contentStore, ok := e.store.(contentAddressedObjectStorage); ok {
			published, didUpload, saveErr = contentStore.Ensure(workCtx, key, image.MIMEType, image.Bytes)
		} else {
			published, saveErr = e.store.Save(workCtx, key, image.MIMEType, image.Bytes)
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
