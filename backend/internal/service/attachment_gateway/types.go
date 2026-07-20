package attachment_gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"time"
)

const (
	sha256HexLength                      = sha256.Size * 2
	defaultThresholdBytes                = 512 * 1024
	defaultMaxImageBytes                 = 64 * 1024 * 1024
	defaultMaxTotalImageBytes            = 64 * 1024 * 1024
	defaultMaxPixels               int64 = 50_000_000
	defaultQuality                       = 85
	defaultSpecialQuality                = 90
	defaultMinSavingsRatio               = 0.05
	defaultCacheTTL                      = 7 * 24 * time.Hour
	defaultCacheMaxBytes           int64 = 512 * 1024 * 1024
	defaultCacheCleanupInterval          = 10 * time.Minute
	defaultNegativeCacheTTL              = 24 * time.Hour
	defaultNegativeCacheMaxEntries       = 10_000
	defaultMaxImagesPerRequest           = 20
	defaultMaxConcurrentEncode           = 2
	defaultAggregateTriggerBytes         = 4 * 1024 * 1024
	defaultAggregateTriggerCount         = 8
	defaultAggregateThresholdBytes       = 128 * 1024
	defaultCacheDir                      = "data/attachment_cache"
	policyVersion                        = "phase1-v1"
)

var (
	errUnsupportedMediaType = errors.New("attachment gateway: unsupported image media type")
	errImageTooLarge        = errors.New("attachment gateway: decoded image exceeds configured limit")
	errTooManyPixels        = errors.New("attachment gateway: image dimensions exceed configured limit")
	errAnimatedImage        = errors.New("attachment gateway: animated images are not supported")
	errNotSmaller           = errors.New("attachment gateway: optimized image does not meet minimum savings")
)

// Config controls the Phase 1 attachment optimizer. The zero-value is disabled.
// New applies conservative defaults to the remaining fields.
type Config struct {
	Enabled                           bool
	RequestBudgetEnabled              bool
	ThresholdBytes                    int
	AggregateSmallImageEnabled        bool
	AggregateSmallImageTriggerBytes   int
	AggregateSmallImageTriggerCount   int
	AggregateSmallImageThresholdBytes int
	MaxImageBytes                     int
	MaxTotalImageBytes                int
	MaxPixels                         int64
	Quality                           int
	SpecialQuality                    int
	MinSavingsRatio                   float64
	CacheDir                          string
	CacheTTL                          time.Duration
	CacheMaxBytes                     int64
	CacheCleanupInterval              time.Duration
	NegativeCacheTTL                  time.Duration
	NegativeCacheMaxEntries           int
	MaxImagesPerRequest               int
	MaxConcurrentEncode               int
}

// DefaultConfig returns the complete Phase 1 policy with the experiment off.
func DefaultConfig() Config {
	return Config{
		Enabled:                           false,
		RequestBudgetEnabled:              false,
		ThresholdBytes:                    defaultThresholdBytes,
		AggregateSmallImageEnabled:        false,
		AggregateSmallImageTriggerBytes:   defaultAggregateTriggerBytes,
		AggregateSmallImageTriggerCount:   defaultAggregateTriggerCount,
		AggregateSmallImageThresholdBytes: defaultAggregateThresholdBytes,
		MaxImageBytes:                     defaultMaxImageBytes,
		MaxTotalImageBytes:                defaultMaxTotalImageBytes,
		MaxPixels:                         defaultMaxPixels,
		Quality:                           defaultQuality,
		SpecialQuality:                    defaultSpecialQuality,
		MinSavingsRatio:                   defaultMinSavingsRatio,
		CacheDir:                          defaultCacheDir,
		CacheTTL:                          defaultCacheTTL,
		CacheMaxBytes:                     defaultCacheMaxBytes,
		CacheCleanupInterval:              defaultCacheCleanupInterval,
		NegativeCacheTTL:                  defaultNegativeCacheTTL,
		NegativeCacheMaxEntries:           defaultNegativeCacheMaxEntries,
		MaxImagesPerRequest:               defaultMaxImagesPerRequest,
		MaxConcurrentEncode:               defaultMaxConcurrentEncode,
	}
}

func (c Config) withDefaults() Config {
	defaults := DefaultConfig()
	if c.ThresholdBytes == 0 {
		c.ThresholdBytes = defaults.ThresholdBytes
	}
	if c.AggregateSmallImageTriggerBytes == 0 {
		c.AggregateSmallImageTriggerBytes = defaults.AggregateSmallImageTriggerBytes
	}
	if c.AggregateSmallImageTriggerCount == 0 {
		c.AggregateSmallImageTriggerCount = defaults.AggregateSmallImageTriggerCount
	}
	if c.AggregateSmallImageThresholdBytes == 0 {
		c.AggregateSmallImageThresholdBytes = defaults.AggregateSmallImageThresholdBytes
	}
	if c.MaxImageBytes == 0 {
		c.MaxImageBytes = defaults.MaxImageBytes
	}
	if c.MaxTotalImageBytes == 0 {
		c.MaxTotalImageBytes = defaults.MaxTotalImageBytes
	}
	if c.MaxPixels == 0 {
		c.MaxPixels = defaults.MaxPixels
	}
	if c.Quality == 0 {
		c.Quality = defaults.Quality
	}
	if c.SpecialQuality == 0 {
		c.SpecialQuality = defaults.SpecialQuality
	}
	if c.MinSavingsRatio == 0 {
		c.MinSavingsRatio = defaults.MinSavingsRatio
	}
	if c.CacheDir == "" {
		c.CacheDir = defaults.CacheDir
	}
	if c.CacheTTL == 0 {
		c.CacheTTL = defaults.CacheTTL
	}
	if c.CacheMaxBytes == 0 {
		c.CacheMaxBytes = defaults.CacheMaxBytes
	}
	if c.CacheCleanupInterval == 0 {
		c.CacheCleanupInterval = defaults.CacheCleanupInterval
	}
	if c.NegativeCacheTTL == 0 {
		c.NegativeCacheTTL = defaults.NegativeCacheTTL
	}
	if c.NegativeCacheMaxEntries == 0 {
		c.NegativeCacheMaxEntries = defaults.NegativeCacheMaxEntries
	}
	if c.MaxImagesPerRequest == 0 {
		c.MaxImagesPerRequest = defaults.MaxImagesPerRequest
	}
	if c.MaxConcurrentEncode == 0 {
		c.MaxConcurrentEncode = defaults.MaxConcurrentEncode
	}
	return c
}

func (c Config) validate() error {
	if c.ThresholdBytes < 0 {
		return errors.New("attachment gateway: threshold bytes must be non-negative")
	}
	if c.AggregateSmallImageEnabled {
		if c.AggregateSmallImageTriggerBytes <= 0 {
			return errors.New("attachment gateway: aggregate image trigger bytes must be positive")
		}
		if c.AggregateSmallImageTriggerCount <= 0 {
			return errors.New("attachment gateway: aggregate image trigger count must be positive")
		}
		if c.AggregateSmallImageThresholdBytes <= 0 || c.AggregateSmallImageThresholdBytes > c.ThresholdBytes {
			return errors.New("attachment gateway: aggregate image threshold must be positive and no greater than the normal threshold")
		}
	}
	if c.MaxImageBytes <= 0 {
		return errors.New("attachment gateway: max image bytes must be positive")
	}
	if c.MaxTotalImageBytes <= 0 {
		return errors.New("attachment gateway: max total image bytes must be positive")
	}
	if c.MaxPixels <= 0 {
		return errors.New("attachment gateway: max pixels must be positive")
	}
	if c.Quality < 1 || c.Quality > 100 || c.SpecialQuality < 1 || c.SpecialQuality > 100 {
		return errors.New("attachment gateway: WebP quality must be between 1 and 100")
	}
	if c.MinSavingsRatio < 0 || c.MinSavingsRatio >= 1 {
		return errors.New("attachment gateway: minimum savings ratio must be in [0,1)")
	}
	if c.CacheDir == "" {
		return errors.New("attachment gateway: cache directory must not be empty")
	}
	if c.CacheTTL <= 0 {
		return errors.New("attachment gateway: cache TTL must be positive")
	}
	if c.CacheMaxBytes <= 0 {
		return errors.New("attachment gateway: cache max bytes must be positive")
	}
	if c.CacheCleanupInterval <= 0 {
		return errors.New("attachment gateway: cache cleanup interval must be positive")
	}
	if c.NegativeCacheTTL <= 0 {
		return errors.New("attachment gateway: negative cache TTL must be positive")
	}
	if c.NegativeCacheMaxEntries <= 0 {
		return errors.New("attachment gateway: negative cache max entries must be positive")
	}
	if c.MaxImagesPerRequest <= 0 {
		return errors.New("attachment gateway: max images per request must be positive")
	}
	if c.MaxConcurrentEncode <= 0 {
		return errors.New("attachment gateway: max concurrent encodes must be positive")
	}
	return nil
}

// Metrics is safe to log: it contains only counts, byte sizes, durations and
// cache outcomes, never image bytes, base64 strings, prompts or hashes.
type Metrics struct {
	Enabled                             bool
	OriginalBodyBytes                   int
	OptimizedBodyBytes                  int
	ImageCount                          int
	OptimizedImageCount                 int
	OriginalImageBytes                  int
	OptimizedImageBytes                 int
	CacheHit                            bool
	CacheHits                           int
	CacheShared                         int
	NegativeCacheHit                    bool
	NegativeCacheHits                   int
	NegativeCacheShared                 int
	TimedOut                            bool
	SkippedBelowThreshold               int
	SkippedUnsupported                  int
	SkippedNotSmaller                   int
	SkippedRequestImageLimit            int
	SkippedTotalImageBytes              int
	AggregatePressure                   bool
	EffectiveThresholdBytes             int
	OriginalInlineAttachmentCount       int
	OriginalInlineAttachmentBytes       int
	OriginalUnsupportedAttachmentCount  int
	CandidateInlineAttachmentCount      int
	CandidateInlineAttachmentBytes      int
	CandidateUnsupportedAttachmentCount int
	Errors                              int
	OptimizeDurationMS                  float64
}

// Result contains the body to forward and privacy-safe request metrics.
type Result struct {
	Body    []byte
	Metrics Metrics
}

// Metadata is persisted next to each optimized image. Hashes identify decoded
// image bytes and optimized bytes; they are never emitted to request logs.
type Metadata struct {
	OriginalHash     string    `json:"original_hash"`
	OptimizedHash    string    `json:"optimized_hash"`
	OriginalSize     int       `json:"original_size"`
	OptimizedSize    int       `json:"optimized_size"`
	OriginalMIMEType string    `json:"original_mime_type"`
	MIMEType         string    `json:"mime_type"`
	Width            int       `json:"width"`
	Height           int       `json:"height"`
	Quality          int       `json:"quality"`
	Lossless         bool      `json:"lossless"`
	Policy           string    `json:"policy"`
	Optimizer        string    `json:"optimizer"`
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at"`
}

// NegativeMetadata records a deterministic "not smaller" optimization
// outcome. It contains no image bytes, base64, prompt, URL or user data. The
// policy and optimizer fingerprints ensure an encoder/policy change forces a
// fresh attempt instead of reusing an obsolete decision.
type NegativeMetadata struct {
	OriginalHash     string    `json:"original_hash"`
	OriginalSize     int       `json:"original_size"`
	OriginalMIMEType string    `json:"original_mime_type"`
	CandidateSize    int       `json:"candidate_size"`
	Width            int       `json:"width"`
	Height           int       `json:"height"`
	Quality          int       `json:"quality"`
	Lossless         bool      `json:"lossless"`
	Reason           string    `json:"reason"`
	Policy           string    `json:"policy"`
	Optimizer        string    `json:"optimizer"`
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type imagePolicy struct {
	Quality  int
	Lossless bool
	Reason   string
}

type optimizedImage struct {
	Bytes    []byte
	Metadata Metadata
}

type encodeOptions struct {
	Quality  int
	Lossless bool
}

type imageEncoder interface {
	Encode(image.Image, encodeOptions) ([]byte, error)
	ID() string
}

func sourceHash(source []byte) string {
	sum := sha256.Sum256(source)
	return hex.EncodeToString(sum[:])
}

func optimizedHash(encoded []byte) string {
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func policyFingerprint(cfg Config, encoderID string) string {
	value := fmt.Sprintf(
		"%s|encoder=%s|q=%d|special_q=%d|min_savings=%.6f|max_pixels=%d",
		policyVersion,
		encoderID,
		cfg.Quality,
		cfg.SpecialQuality,
		cfg.MinSavingsRatio,
		cfg.MaxPixels,
	)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
