package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	settingKeyAttachmentGatewayR2Config = "attachment_gateway_r2_config"
	defaultAttachmentR2Region           = "auto"
	defaultAttachmentR2PresignMinutes   = 60
	attachmentR2ConfigReloadInterval    = 30 * time.Second
)

var (
	ErrAttachmentR2NotConfigured = infraerrors.BadRequest(
		"ATTACHMENT_R2_NOT_CONFIGURED",
		"attachment gateway R2 storage is disabled or not configured",
	)
	ErrAttachmentR2ConfigCorrupt = infraerrors.InternalServer(
		"ATTACHMENT_R2_CONFIG_CORRUPT",
		"attachment gateway R2 config data is corrupted",
	)
)

// AttachmentR2Config is the independently persisted object-storage config for
// Attachment Gateway. It deliberately does not reuse backup or async-image
// credentials. SecretAccessKey is accepted on writes but is always blank on
// reads; SecretConfigured is the only secret-related value returned to admins.
type AttachmentR2Config struct {
	Enabled              bool   `json:"enabled"`
	Endpoint             string `json:"endpoint"`
	Region               string `json:"region"`
	Bucket               string `json:"bucket"`
	AccessKeyID          string `json:"access_key_id"`
	SecretAccessKey      string `json:"secret_access_key,omitempty"` //nolint:revive // AWS-compatible field name
	Prefix               string `json:"prefix"`
	ForcePathStyle       bool   `json:"force_path_style"`
	PresignExpiryMinutes int    `json:"presign_expiry_minutes"`
	SecretConfigured     bool   `json:"secret_configured,omitempty"`
	Configured           bool   `json:"configured,omitempty"`
}

func (c AttachmentR2Config) isConfigured() bool {
	return strings.TrimSpace(c.Endpoint) != "" &&
		strings.TrimSpace(c.Bucket) != "" &&
		strings.TrimSpace(c.AccessKeyID) != "" &&
		strings.TrimSpace(c.SecretAccessKey) != ""
}

func (c AttachmentR2Config) hasConnectionValues() bool {
	return strings.TrimSpace(c.Endpoint) != "" ||
		strings.TrimSpace(c.Bucket) != "" ||
		strings.TrimSpace(c.AccessKeyID) != "" ||
		strings.TrimSpace(c.SecretAccessKey) != ""
}

func (c AttachmentR2Config) runtimeEqual(other AttachmentR2Config) bool {
	c.SecretConfigured = false
	c.Configured = false
	other.SecretConfigured = false
	other.Configured = false
	return c == other
}

// AttachmentR2ObjectStore is the infrastructure contract used by the dynamic
// provider. Probe must write, read and remove one tiny temporary object so the
// admin connection test verifies Object Read & Write permissions, not merely
// bucket visibility.
type AttachmentR2ObjectStore interface {
	ImageStorage
	Ensure(ctx context.Context, key, contentType string, data []byte) (url string, uploaded bool, err error)
	Probe(ctx context.Context, key string) error
}

type AttachmentR2StoreFactory func(ctx context.Context, cfg *AttachmentR2Config) (AttachmentR2ObjectStore, error)

// AttachmentR2Service owns encrypted settings plus a short-lived runtime
// client cache. It also implements the storage contract consumed by the URL
// externalizer, allowing config changes to take effect without restarting the
// gateway.
type AttachmentR2Service struct {
	settingRepo SettingRepository
	encryptor   SecretEncryptor
	factory     AttachmentR2StoreFactory

	mu             sync.Mutex
	store          AttachmentR2ObjectStore
	cachedConfig   *AttachmentR2Config
	runtimeErr     error
	lastReload     time.Time
	reloadInterval time.Duration
	now            func() time.Time
	generation     atomic.Uint64
}

func NewAttachmentR2Service(
	settingRepo SettingRepository,
	encryptor SecretEncryptor,
	factory AttachmentR2StoreFactory,
) *AttachmentR2Service {
	return &AttachmentR2Service{
		settingRepo:    settingRepo,
		encryptor:      encryptor,
		factory:        factory,
		reloadInterval: attachmentR2ConfigReloadInterval,
		now:            time.Now,
	}
}

func (s *AttachmentR2Service) GetConfig(ctx context.Context) (*AttachmentR2Config, error) {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = &AttachmentR2Config{
			Region:               defaultAttachmentR2Region,
			PresignExpiryMinutes: defaultAttachmentR2PresignMinutes,
		}
	}
	return publicAttachmentR2Config(*cfg), nil
}

func (s *AttachmentR2Service) UpdateConfig(ctx context.Context, input AttachmentR2Config) (*AttachmentR2Config, error) {
	input.SecretConfigured = false
	input.Configured = false

	// An empty secret means "keep the current value", matching the existing
	// backup configuration UX without ever returning the saved secret.
	if strings.TrimSpace(input.SecretAccessKey) == "" {
		old, err := s.loadConfig(ctx)
		if err != nil {
			return nil, err
		}
		if old != nil {
			input.SecretAccessKey = old.SecretAccessKey
		}
	}

	cfg, err := normalizeAttachmentR2Config(input)
	if err != nil {
		return nil, err
	}
	if err := validateAttachmentR2Config(cfg, false); err != nil {
		return nil, err
	}

	stored := cfg
	if stored.SecretAccessKey != "" {
		if s.encryptor == nil {
			return nil, fmt.Errorf("attachment R2 secret encryptor is unavailable")
		}
		encrypted, encryptErr := s.encryptor.Encrypt(stored.SecretAccessKey)
		if encryptErr != nil {
			return nil, fmt.Errorf("encrypt attachment R2 secret: %w", encryptErr)
		}
		stored.SecretAccessKey = encrypted
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("marshal attachment R2 config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, settingKeyAttachmentGatewayR2Config, string(data)); err != nil {
		return nil, fmt.Errorf("save attachment R2 config: %w", err)
	}

	s.invalidateRuntime()
	return publicAttachmentR2Config(cfg), nil
}

// TestConnection validates the submitted config without persisting it. When
// the secret field is empty, the currently saved secret is used. The probe
// creates and removes a tiny object under the configured prefix.
func (s *AttachmentR2Service) TestConnection(ctx context.Context, input AttachmentR2Config) error {
	if strings.TrimSpace(input.SecretAccessKey) == "" {
		old, err := s.loadConfig(ctx)
		if err != nil {
			return err
		}
		if old != nil {
			input.SecretAccessKey = old.SecretAccessKey
		}
	}
	input.SecretConfigured = false
	input.Configured = false
	cfg, err := normalizeAttachmentR2Config(input)
	if err != nil {
		return err
	}
	if err := validateAttachmentR2Config(cfg, true); err != nil {
		return err
	}
	if s.factory == nil {
		return fmt.Errorf("attachment R2 store factory is unavailable")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, err := s.factory(probeCtx, &cfg)
	if err != nil {
		return fmt.Errorf("create attachment R2 client: %w", err)
	}
	probeID := make([]byte, 16)
	if _, err := rand.Read(probeID); err != nil {
		return fmt.Errorf("create attachment R2 probe id: %w", err)
	}
	probeKey := prefixAttachmentR2Key(cfg.Prefix, path.Join(".sub2api-connection-test", hex.EncodeToString(probeID)+".txt"))
	if err := store.Probe(probeCtx, probeKey); err != nil {
		return fmt.Errorf("attachment R2 read/write probe failed: %w", err)
	}
	return nil
}

// Ready is an optional capability consumed by URLExternalizer. It avoids one
// failed upload per image while storage is disabled or incomplete.
func (s *AttachmentR2Service) Ready(ctx context.Context) bool {
	_, _, err := s.runtimeStore(ctx)
	return err == nil
}

// CacheVersion lets URLExternalizer discard signed URLs when the bucket or
// credentials change at runtime.
func (s *AttachmentR2Service) CacheVersion() uint64 {
	if s == nil {
		return 0
	}
	return s.generation.Load()
}

func (s *AttachmentR2Service) Save(ctx context.Context, key, contentType string, data []byte) (string, error) {
	store, cfg, err := s.runtimeStore(ctx)
	if err != nil {
		return "", err
	}
	return store.Save(ctx, prefixAttachmentR2Key(cfg.Prefix, key), contentType, data)
}

func (s *AttachmentR2Service) Ensure(ctx context.Context, key, contentType string, data []byte) (string, bool, error) {
	store, cfg, err := s.runtimeStore(ctx)
	if err != nil {
		return "", false, err
	}
	return store.Ensure(ctx, prefixAttachmentR2Key(cfg.Prefix, key), contentType, data)
}

func (s *AttachmentR2Service) runtimeStore(ctx context.Context) (AttachmentR2ObjectStore, AttachmentR2Config, error) {
	if s == nil {
		return nil, AttachmentR2Config{}, ErrAttachmentR2NotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if !s.lastReload.IsZero() && now.Sub(s.lastReload) < s.reloadInterval {
		if s.store != nil && s.cachedConfig != nil && s.runtimeErr == nil {
			return s.store, *s.cachedConfig, nil
		}
		if s.runtimeErr != nil {
			return nil, AttachmentR2Config{}, s.runtimeErr
		}
		return nil, AttachmentR2Config{}, ErrAttachmentR2NotConfigured
	}

	previous := s.cachedConfig
	cfg, err := s.loadConfig(ctx)
	s.lastReload = now
	if err != nil {
		s.store = nil
		s.runtimeErr = err
		return nil, AttachmentR2Config{}, err
	}
	if cfg == nil || !cfg.Enabled || !cfg.isConfigured() {
		if previous != nil && (cfg == nil || !previous.runtimeEqual(*cfg)) {
			s.generation.Add(1)
		}
		s.cachedConfig = cfg
		s.store = nil
		s.runtimeErr = ErrAttachmentR2NotConfigured
		return nil, AttachmentR2Config{}, s.runtimeErr
	}
	if err := validateAttachmentR2Config(*cfg, true); err != nil {
		s.cachedConfig = cfg
		s.store = nil
		s.runtimeErr = err
		return nil, AttachmentR2Config{}, err
	}
	if previous != nil && previous.runtimeEqual(*cfg) && s.store != nil && s.runtimeErr == nil {
		s.cachedConfig = cfg
		return s.store, *cfg, nil
	}
	if previous == nil || !previous.runtimeEqual(*cfg) {
		s.generation.Add(1)
	}
	if s.factory == nil {
		s.cachedConfig = cfg
		s.store = nil
		s.runtimeErr = errors.New("attachment R2 store factory is unavailable")
		return nil, AttachmentR2Config{}, s.runtimeErr
	}
	store, err := s.factory(ctx, cfg)
	if err != nil {
		s.cachedConfig = cfg
		s.store = nil
		s.runtimeErr = fmt.Errorf("create attachment R2 client: %w", err)
		return nil, AttachmentR2Config{}, s.runtimeErr
	}
	s.cachedConfig = cfg
	s.store = store
	s.runtimeErr = nil
	return store, *cfg, nil
}

func (s *AttachmentR2Service) loadConfig(ctx context.Context) (*AttachmentR2Config, error) {
	if s == nil || s.settingRepo == nil {
		return nil, nil //nolint:nilnil // absent config is a valid disabled state
	}
	raw, err := s.settingRepo.GetValue(ctx, settingKeyAttachmentGatewayR2Config)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return nil, nil //nolint:nilnil // absent config is a valid disabled state
		}
		return nil, fmt.Errorf("load attachment R2 config: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil //nolint:nilnil // absent config is a valid disabled state
	}
	var cfg AttachmentR2Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, ErrAttachmentR2ConfigCorrupt
	}
	if cfg.SecretAccessKey != "" {
		if s.encryptor == nil {
			return nil, ErrAttachmentR2ConfigCorrupt
		}
		decrypted, decryptErr := s.encryptor.Decrypt(cfg.SecretAccessKey)
		if decryptErr != nil {
			return nil, ErrAttachmentR2ConfigCorrupt
		}
		cfg.SecretAccessKey = decrypted
	}
	normalized, err := normalizeAttachmentR2Config(cfg)
	if err != nil {
		return nil, ErrAttachmentR2ConfigCorrupt
	}
	return &normalized, nil
}

func (s *AttachmentR2Service) invalidateRuntime() {
	s.mu.Lock()
	s.store = nil
	s.cachedConfig = nil
	s.runtimeErr = nil
	s.lastReload = time.Time{}
	s.mu.Unlock()
	s.generation.Add(1)
}

func normalizeAttachmentR2Config(input AttachmentR2Config) (AttachmentR2Config, error) {
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	input.Region = strings.TrimSpace(input.Region)
	if input.Region == "" {
		input.Region = defaultAttachmentR2Region
	}
	input.Bucket = strings.TrimSpace(input.Bucket)
	input.AccessKeyID = strings.TrimSpace(input.AccessKeyID)
	input.SecretAccessKey = strings.TrimSpace(input.SecretAccessKey)
	prefix := strings.Trim(strings.TrimSpace(input.Prefix), "/")
	if prefix != "" {
		for _, segment := range strings.Split(prefix, "/") {
			if segment == "." || segment == ".." {
				return AttachmentR2Config{}, infraerrors.BadRequest("ATTACHMENT_R2_INVALID_PREFIX", "R2 prefix must not contain dot path segments")
			}
		}
		prefix += "/"
	}
	input.Prefix = prefix
	if input.PresignExpiryMinutes == 0 {
		input.PresignExpiryMinutes = defaultAttachmentR2PresignMinutes
	}
	input.SecretConfigured = false
	input.Configured = false
	return input, nil
}

func validateAttachmentR2Config(cfg AttachmentR2Config, requireConfigured bool) error {
	if !requireConfigured && !cfg.Enabled && !cfg.hasConnectionValues() {
		return nil
	}
	if !cfg.isConfigured() {
		return infraerrors.BadRequest(
			"ATTACHMENT_R2_INCOMPLETE_CONFIG",
			"endpoint, bucket, access_key_id and secret_access_key are required",
		)
	}
	parsed, err := url.Parse(cfg.Endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return infraerrors.BadRequest(
			"ATTACHMENT_R2_INVALID_ENDPOINT",
			"endpoint must be an absolute HTTPS URL without credentials, query or fragment",
		)
	}
	if strings.ContainsAny(cfg.Bucket, "/\\\t\r\n ") {
		return infraerrors.BadRequest("ATTACHMENT_R2_INVALID_BUCKET", "bucket must be a bucket name, not a path")
	}
	if cfg.PresignExpiryMinutes < 5 || cfg.PresignExpiryMinutes > 7*24*60 {
		return infraerrors.BadRequest(
			"ATTACHMENT_R2_INVALID_PRESIGN_EXPIRY",
			"presign_expiry_minutes must be between 5 and 10080",
		)
	}
	return nil
}

func publicAttachmentR2Config(cfg AttachmentR2Config) *AttachmentR2Config {
	configured := cfg.isConfigured()
	cfg.SecretConfigured = strings.TrimSpace(cfg.SecretAccessKey) != ""
	cfg.Configured = configured
	cfg.SecretAccessKey = ""
	return &cfg
}

func prefixAttachmentR2Key(prefix, key string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	key = strings.TrimLeft(strings.TrimSpace(key), "/")
	if prefix == "" {
		return key
	}
	return path.Join(prefix, key)
}
