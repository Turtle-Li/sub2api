//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type desktopStorageProbeStub struct {
	probedKeys []string
}

func (s *desktopStorageProbeStub) Save(
	context.Context,
	string,
	string,
	[]byte,
) (string, error) {
	return "", nil
}

func (s *desktopStorageProbeStub) Probe(_ context.Context, key string) error {
	s.probedKeys = append(s.probedKeys, key)
	return nil
}

func newDesktopStorageFixture(
	t *testing.T,
	encryptionKeyConfigured bool,
) (*DesktopStorageService, *stubSettingRepo, *[]config.ImageStorageConfig, *desktopStorageProbeStub) {
	t.Helper()
	repo := newStubSettingRepo()
	encryptor := reversibleEncryptor{}
	backup := NewBackupService(repo, &config.Config{
		Totp: config.TotpConfig{EncryptionKeyConfigured: encryptionKeyConfigured},
	}, encryptor, nil, nil)
	var built []config.ImageStorageConfig
	probe := &desktopStorageProbeStub{}
	factory := func(_ context.Context, cfg *config.ImageStorageConfig) (ImageStorage, error) {
		built = append(built, *cfg)
		return probe, nil
	}
	return NewDesktopStorageService(repo, encryptor, backup, factory), repo, &built, probe
}

func validDesktopStorageSettings() DesktopStorageSettings {
	return DesktopStorageSettings{
		Enabled:          true,
		Region:           "ap-guangzhou",
		Bucket:           "tt-switch-1250000000",
		SecretID:         "AKIDEXAMPLE",
		SecretKey:        "secret-value",
		PublicBaseURL:    "https://download.example.com/tt-switch",
		ReleasePrefix:    "releases",
		ThemePrefix:      "themes",
		QuarantinePrefix: "theme-quarantine",
	}
}

func TestDesktopStorageEncryptsMasksAndPreservesSecret(t *testing.T) {
	svc, repo, _, _ := newDesktopStorageFixture(t, true)
	ctx := context.Background()

	saved, err := svc.Update(ctx, validDesktopStorageSettings())
	require.NoError(t, err)
	require.True(t, saved.SecretConfigured)
	require.Empty(t, saved.Config.SecretKey)
	require.Equal(t, "https://cos.ap-guangzhou.myqcloud.com", saved.Endpoint)
	require.Equal(
		t,
		"https://download.example.com/tt-switch/releases/latest.json",
		saved.ReleaseManifestURL,
	)

	var stored DesktopStorageSettings
	require.NoError(t, json.Unmarshal(
		[]byte(repo.values[SettingKeyDesktopStorageConfig]),
		&stored,
	))
	require.Equal(t, "enc:secret-value", stored.SecretKey)
	require.Equal(t, "releases/", stored.ReleasePrefix)

	update := validDesktopStorageSettings()
	update.SecretKey = ""
	update.PublicBaseURL = "https://cdn.example.com"
	_, err = svc.Update(ctx, update)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(
		[]byte(repo.values[SettingKeyDesktopStorageConfig]),
		&stored,
	))
	require.Equal(t, "enc:secret-value", stored.SecretKey)

	fetched, err := svc.Get(ctx)
	require.NoError(t, err)
	require.True(t, fetched.SecretConfigured)
	require.Empty(t, fetched.Config.SecretKey)
	require.Equal(t, "https://cdn.example.com/themes/catalog.json", fetched.ThemeCatalogURL)
}

func TestDesktopStorageRejectsEphemeralEncryptionKey(t *testing.T) {
	svc, repo, _, _ := newDesktopStorageFixture(t, false)
	_, err := svc.Update(context.Background(), validDesktopStorageSettings())
	require.ErrorIs(t, err, ErrSecretEncryptionKeyNotConfigured)
	require.Empty(t, repo.values[SettingKeyDesktopStorageConfig])
}

func TestDesktopStorageRejectsInvalidOrOverlappingPaths(t *testing.T) {
	svc, _, _, _ := newDesktopStorageFixture(t, true)

	invalidBucket := validDesktopStorageSettings()
	invalidBucket.Bucket = "missing-appid"
	_, err := svc.Update(context.Background(), invalidBucket)
	require.ErrorContains(t, err, "APPID")

	overlap := validDesktopStorageSettings()
	overlap.QuarantinePrefix = "themes/quarantine"
	_, err = svc.Update(context.Background(), overlap)
	require.ErrorContains(t, err, "must not overlap")
}

func TestDesktopStorageProbeUsesCOSRuntimeAndReleasePrefix(t *testing.T) {
	svc, _, built, probe := newDesktopStorageFixture(t, true)
	ctx := context.Background()
	_, err := svc.Update(ctx, validDesktopStorageSettings())
	require.NoError(t, err)

	input := validDesktopStorageSettings()
	input.SecretKey = ""
	result, err := svc.TestConnection(ctx, input)
	require.NoError(t, err)
	require.Equal(t, "https://cos.ap-guangzhou.myqcloud.com", result.Endpoint)
	require.Len(t, *built, 1)
	require.Equal(t, "https://cos.ap-guangzhou.myqcloud.com", (*built)[0].Endpoint)
	require.Equal(t, "secret-value", (*built)[0].SecretAccessKey)
	require.Len(t, probe.probedKeys, 1)
	require.Contains(t, probe.probedKeys[0], "releases/_probes/")
}
