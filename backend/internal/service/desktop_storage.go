package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	DesktopStorageSchemaVersion = 1
	desktopStorageProvider      = "tencent_cos"
)

var (
	desktopStorageRegionPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	desktopStorageBucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}-[0-9]{5,}$`)
)

// DesktopStorageSettings is the persisted TT Switch Tencent COS contract.
// SecretKey is encrypted before persistence and is never returned by Get or
// Update. Empty SecretKey on update keeps the existing encrypted value.
type DesktopStorageSettings struct {
	SchemaVersion int    `json:"schema_version"`
	Enabled       bool   `json:"enabled"`
	Provider      string `json:"provider"`

	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	SecretID  string `json:"secret_id"`
	SecretKey string `json:"secret_key,omitempty"`

	PublicBaseURL    string `json:"public_base_url"`
	ReleasePrefix    string `json:"release_prefix"`
	ThemePrefix      string `json:"theme_prefix"`
	QuarantinePrefix string `json:"quarantine_prefix"`
}

// DesktopStorageSettingsView contains only safe-to-display values and derived
// endpoints. The encrypted secret is never included.
type DesktopStorageSettingsView struct {
	Config             DesktopStorageSettings `json:"config"`
	SecretConfigured   bool                   `json:"secret_configured"`
	Endpoint           string                 `json:"endpoint"`
	ReleaseManifestURL string                 `json:"release_manifest_url"`
	ThemeCatalogURL    string                 `json:"theme_catalog_url"`
}

// DesktopStorageProbeResult is returned after a real write/read/delete probe.
type DesktopStorageProbeResult struct {
	Endpoint  string `json:"endpoint"`
	ObjectKey string `json:"object_key"`
}

type desktopStorageProbe interface {
	Probe(ctx context.Context, key string) error
}

// DesktopStorageService owns the COS credentials and directory contract used
// by TT Switch releases and the future theme store.
type DesktopStorageService struct {
	settingRepo SettingRepository
	encryptor   SecretEncryptor
	backup      *BackupService
	factory     ImageStorageFactory
}

func NewDesktopStorageService(
	settingRepo SettingRepository,
	encryptor SecretEncryptor,
	backup *BackupService,
	factory ImageStorageFactory,
) *DesktopStorageService {
	return &DesktopStorageService{
		settingRepo: settingRepo,
		encryptor:   encryptor,
		backup:      backup,
		factory:     factory,
	}
}

func DefaultDesktopStorageSettings() DesktopStorageSettings {
	return DesktopStorageSettings{
		SchemaVersion:    DesktopStorageSchemaVersion,
		Provider:         desktopStorageProvider,
		ReleasePrefix:    "releases/",
		ThemePrefix:      "themes/",
		QuarantinePrefix: "theme-quarantine/",
	}
}

func (s *DesktopStorageService) Get(ctx context.Context) (*DesktopStorageSettingsView, error) {
	settings, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		defaults := DefaultDesktopStorageSettings()
		settings = &defaults
	}
	return desktopStorageView(*settings), nil
}

// Update encrypts a new SecretKey, or preserves the existing one when the
// submitted field is empty.
func (s *DesktopStorageService) Update(
	ctx context.Context,
	in DesktopStorageSettings,
) (*DesktopStorageSettingsView, error) {
	normalizeDesktopStorageSettings(&in)

	old, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if in.SecretKey == "" && old != nil {
		in.SecretKey = old.SecretKey
	} else if in.SecretKey != "" {
		if s.backup == nil || !s.backup.EncryptionKeyConfigured() {
			return nil, ErrSecretEncryptionKeyNotConfigured
		}
		encrypted, encryptErr := s.encryptor.Encrypt(in.SecretKey)
		if encryptErr != nil {
			return nil, fmt.Errorf("encrypt TT Switch COS secret key: %w", encryptErr)
		}
		in.SecretKey = encrypted
	}

	if err := validateDesktopStorageSettings(in); err != nil {
		return nil, infraerrors.BadRequest("INVALID_DESKTOP_STORAGE", err.Error())
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("encode TT Switch COS settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyDesktopStorageConfig, string(data)); err != nil {
		return nil, fmt.Errorf("save TT Switch COS settings: %w", err)
	}
	return desktopStorageView(in), nil
}

// TestConnection performs the exact permissions required for publishing:
// write one object, fetch it through PublicBaseURL, then delete it.
func (s *DesktopStorageService) TestConnection(
	ctx context.Context,
	in DesktopStorageSettings,
) (*DesktopStorageProbeResult, error) {
	normalizeDesktopStorageSettings(&in)
	old, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if in.SecretKey == "" && old != nil {
		in.SecretKey = old.SecretKey
	}
	// A connection probe always requires a complete, publishable
	// configuration even when the persisted enable switch is still off.
	in.Enabled = true
	if err := validateDesktopStorageSettings(in); err != nil {
		return nil, infraerrors.BadRequest("INVALID_DESKTOP_STORAGE", err.Error())
	}

	runtimeConfig, err := s.runtimeConfig(in)
	if err != nil {
		return nil, err
	}
	storage, err := s.factory(ctx, runtimeConfig)
	if err != nil {
		return nil, fmt.Errorf("create Tencent COS client: %w", err)
	}
	prober, ok := storage.(desktopStorageProbe)
	if !ok {
		return nil, errors.New("configured object storage does not support write/read/delete probing")
	}
	objectKey := path.Join(
		strings.TrimSuffix(in.ReleasePrefix, "/"),
		"_probes",
		fmt.Sprintf("desktop-console-%d.txt", time.Now().UnixNano()),
	)
	if err := prober.Probe(ctx, objectKey); err != nil {
		return nil, fmt.Errorf("tencent COS write/read/delete probe failed: %w", err)
	}
	return &DesktopStorageProbeResult{
		Endpoint:  desktopStorageEndpoint(in.Region),
		ObjectKey: objectKey,
	}, nil
}

func (s *DesktopStorageService) runtimeConfig(
	in DesktopStorageSettings,
) (*config.ImageStorageConfig, error) {
	secretKey := in.SecretKey
	if secretKey != "" {
		decrypted, err := s.encryptor.Decrypt(secretKey)
		if err == nil {
			secretKey = decrypted
		}
	}
	return &config.ImageStorageConfig{
		Enabled:         true,
		Endpoint:        desktopStorageEndpoint(in.Region),
		Region:          in.Region,
		Bucket:          in.Bucket,
		AccessKeyID:     in.SecretID,
		SecretAccessKey: secretKey,
		PublicBaseURL:   in.PublicBaseURL,
		PresignExpiry:   1,
		ForcePathStyle:  false,
	}, nil
}

func (s *DesktopStorageService) load(ctx context.Context) (*DesktopStorageSettings, error) {
	if s == nil || s.settingRepo == nil {
		return nil, nil //nolint:nilnil // an unconfigured service is a valid state
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDesktopStorageConfig)
	if err != nil {
		return nil, fmt.Errorf("load TT Switch COS settings: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil //nolint:nilnil // never configured is a valid state
	}
	var settings DesktopStorageSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return nil, fmt.Errorf("parse TT Switch COS settings: %w", err)
	}
	normalizeDesktopStorageSettings(&settings)
	return &settings, nil
}

func desktopStorageView(settings DesktopStorageSettings) *DesktopStorageSettingsView {
	secretConfigured := strings.TrimSpace(settings.SecretKey) != ""
	settings.SecretKey = ""
	return &DesktopStorageSettingsView{
		Config:             settings,
		SecretConfigured:   secretConfigured,
		Endpoint:           desktopStorageEndpoint(settings.Region),
		ReleaseManifestURL: desktopStoragePublicURL(settings.PublicBaseURL, settings.ReleasePrefix+"latest.json"),
		ThemeCatalogURL:    desktopStoragePublicURL(settings.PublicBaseURL, settings.ThemePrefix+"catalog.json"),
	}
}

func normalizeDesktopStorageSettings(settings *DesktopStorageSettings) {
	settings.SchemaVersion = DesktopStorageSchemaVersion
	settings.Provider = desktopStorageProvider
	settings.Region = strings.ToLower(strings.TrimSpace(settings.Region))
	settings.Bucket = strings.ToLower(strings.TrimSpace(settings.Bucket))
	settings.SecretID = strings.TrimSpace(settings.SecretID)
	settings.SecretKey = strings.TrimSpace(settings.SecretKey)
	settings.PublicBaseURL = strings.TrimRight(strings.TrimSpace(settings.PublicBaseURL), "/")
	settings.ReleasePrefix = normalizeDesktopStoragePrefix(settings.ReleasePrefix, "releases/")
	settings.ThemePrefix = normalizeDesktopStoragePrefix(settings.ThemePrefix, "themes/")
	settings.QuarantinePrefix = normalizeDesktopStoragePrefix(settings.QuarantinePrefix, "theme-quarantine/")
}

func normalizeDesktopStoragePrefix(value, fallback string) string {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if value == "" {
		return fallback
	}
	return value + "/"
}

func validateDesktopStorageSettings(settings DesktopStorageSettings) error {
	if settings.Region != "" && !desktopStorageRegionPattern.MatchString(settings.Region) {
		return errors.New("region must look like ap-guangzhou")
	}
	if settings.Bucket != "" && !desktopStorageBucketPattern.MatchString(settings.Bucket) {
		return errors.New("bucket must include the APPID suffix, for example tt-switch-1250000000")
	}
	if len(settings.SecretID) > 128 || strings.ContainsAny(settings.SecretID, " \t\r\n") {
		return errors.New("SecretId is invalid")
	}
	if settings.PublicBaseURL != "" {
		if err := validateDesktopStoragePublicURL(settings.PublicBaseURL); err != nil {
			return err
		}
	}
	for name, prefix := range map[string]string{
		"release_prefix":    settings.ReleasePrefix,
		"theme_prefix":      settings.ThemePrefix,
		"quarantine_prefix": settings.QuarantinePrefix,
	} {
		if err := validateDesktopStoragePrefix(name, prefix); err != nil {
			return err
		}
	}
	if desktopStoragePrefixesOverlap(settings.ReleasePrefix, settings.ThemePrefix) ||
		desktopStoragePrefixesOverlap(settings.ReleasePrefix, settings.QuarantinePrefix) ||
		desktopStoragePrefixesOverlap(settings.ThemePrefix, settings.QuarantinePrefix) {
		return errors.New("release, theme, and quarantine prefixes must not overlap")
	}
	if settings.Enabled {
		switch {
		case settings.Region == "":
			return errors.New("region is required when COS storage is enabled")
		case settings.Bucket == "":
			return errors.New("bucket is required when COS storage is enabled")
		case settings.SecretID == "":
			return errors.New("SecretId is required when COS storage is enabled")
		case settings.SecretKey == "":
			return errors.New("SecretKey is required when COS storage is enabled")
		case settings.PublicBaseURL == "":
			return errors.New("public download URL is required when COS storage is enabled")
		}
	}
	return nil
}

func validateDesktopStoragePublicURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("public download URL must be an HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("public download URL cannot contain credentials, query parameters, or a fragment")
	}
	if strings.Contains(parsed.EscapedPath(), "..") {
		return errors.New("public download URL path is invalid")
	}
	return nil
}

func validateDesktopStoragePrefix(name, prefix string) error {
	if len(prefix) > 128 || strings.Contains(prefix, "\\") || strings.Contains(prefix, "..") {
		return fmt.Errorf("%s is invalid", name)
	}
	trimmed := strings.TrimSuffix(prefix, "/")
	if trimmed == "" || path.Clean(trimmed) != trimmed {
		return fmt.Errorf("%s is invalid", name)
	}
	return nil
}

func desktopStoragePrefixesOverlap(a, b string) bool {
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func desktopStorageEndpoint(region string) string {
	if strings.TrimSpace(region) == "" {
		return ""
	}
	return "https://cos." + strings.TrimSpace(region) + ".myqcloud.com"
}

func desktopStoragePublicURL(baseURL, objectKey string) string {
	if strings.TrimSpace(baseURL) == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(objectKey, "/")
}
