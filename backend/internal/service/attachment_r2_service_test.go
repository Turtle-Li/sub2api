package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type attachmentR2SettingRepo struct {
	mu   sync.Mutex
	data map[string]string
}

func newAttachmentR2SettingRepo() *attachmentR2SettingRepo {
	return &attachmentR2SettingRepo{data: make(map[string]string)}
}

func (r *attachmentR2SettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.data[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *attachmentR2SettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	setting, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (r *attachmentR2SettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[key] = value
	return nil
}

func (r *attachmentR2SettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, err := r.GetValue(ctx, key); err == nil {
			result[key] = value
		}
	}
	return result, nil
}

func (r *attachmentR2SettingRepo) SetMultiple(ctx context.Context, values map[string]string) error {
	for key, value := range values {
		if err := r.Set(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}

func (r *attachmentR2SettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]string, len(r.data))
	for key, value := range r.data {
		result[key] = value
	}
	return result, nil
}

func (r *attachmentR2SettingRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, key)
	return nil
}

type attachmentR2Encryptor struct{}

func (attachmentR2Encryptor) Encrypt(value string) (string, error) { return "ENC:" + value, nil }
func (attachmentR2Encryptor) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, "ENC:") {
		return "", errors.New("not encrypted")
	}
	return strings.TrimPrefix(value, "ENC:"), nil
}

type attachmentR2FakeStore struct {
	mu        sync.Mutex
	keys      []string
	probeKeys []string
}

func (s *attachmentR2FakeStore) Save(_ context.Context, key, _ string, _ []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, key)
	return "https://objects.example.test/" + key, nil
}

func (s *attachmentR2FakeStore) Ensure(ctx context.Context, key, contentType string, data []byte) (string, bool, error) {
	url, err := s.Save(ctx, key, contentType, data)
	return url, true, err
}

func (s *attachmentR2FakeStore) Probe(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probeKeys = append(s.probeKeys, key)
	return nil
}

func validAttachmentR2Config() AttachmentR2Config {
	return AttachmentR2Config{
		Enabled:              true,
		Endpoint:             "https://account.r2.cloudflarestorage.com",
		Region:               "auto",
		Bucket:               "private-attachments",
		AccessKeyID:          "access-id",
		SecretAccessKey:      "secret-value",
		Prefix:               "sub2api/",
		PresignExpiryMinutes: 60,
	}
}

func TestAttachmentR2ConfigEncryptedRedactedAndEmptySecretPreserved(t *testing.T) {
	repo := newAttachmentR2SettingRepo()
	service := NewAttachmentR2Service(repo, attachmentR2Encryptor{}, func(context.Context, *AttachmentR2Config) (AttachmentR2ObjectStore, error) {
		return &attachmentR2FakeStore{}, nil
	})

	saved, err := service.UpdateConfig(context.Background(), validAttachmentR2Config())
	require.NoError(t, err)
	require.Empty(t, saved.SecretAccessKey)
	require.True(t, saved.SecretConfigured)
	require.True(t, saved.Configured)

	raw, err := repo.GetValue(context.Background(), settingKeyAttachmentGatewayR2Config)
	require.NoError(t, err)
	var stored AttachmentR2Config
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.Equal(t, "ENC:secret-value", stored.SecretAccessKey)
	require.NotContains(t, raw, `"secret_access_key":"secret-value"`)

	update := validAttachmentR2Config()
	update.AccessKeyID = "new-access-id"
	update.SecretAccessKey = ""
	_, err = service.UpdateConfig(context.Background(), update)
	require.NoError(t, err)

	raw, err = repo.GetValue(context.Background(), settingKeyAttachmentGatewayR2Config)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.Equal(t, "ENC:secret-value", stored.SecretAccessKey)

	got, err := service.GetConfig(context.Background())
	require.NoError(t, err)
	require.Empty(t, got.SecretAccessKey)
	require.True(t, got.SecretConfigured)
	require.Equal(t, "new-access-id", got.AccessKeyID)
}

func TestAttachmentR2ConnectionProbeUsesSavedSecretWithoutPersistingInput(t *testing.T) {
	repo := newAttachmentR2SettingRepo()
	store := &attachmentR2FakeStore{}
	var factoryConfig AttachmentR2Config
	service := NewAttachmentR2Service(repo, attachmentR2Encryptor{}, func(_ context.Context, cfg *AttachmentR2Config) (AttachmentR2ObjectStore, error) {
		factoryConfig = *cfg
		return store, nil
	})
	_, err := service.UpdateConfig(context.Background(), validAttachmentR2Config())
	require.NoError(t, err)
	rawBefore, err := repo.GetValue(context.Background(), settingKeyAttachmentGatewayR2Config)
	require.NoError(t, err)

	testConfig := validAttachmentR2Config()
	testConfig.SecretAccessKey = ""
	testConfig.Prefix = "canary"
	require.NoError(t, service.TestConnection(context.Background(), testConfig))
	require.Equal(t, "secret-value", factoryConfig.SecretAccessKey)
	require.Equal(t, "canary/", factoryConfig.Prefix)

	store.mu.Lock()
	require.Len(t, store.probeKeys, 1)
	require.True(t, strings.HasPrefix(store.probeKeys[0], "canary/.sub2api-connection-test/"))
	store.mu.Unlock()
	rawAfter, err := repo.GetValue(context.Background(), settingKeyAttachmentGatewayR2Config)
	require.NoError(t, err)
	require.Equal(t, rawBefore, rawAfter)
}

func TestAttachmentR2RuntimeHotUpdateRebuildsStoreAndPrefixesKeys(t *testing.T) {
	repo := newAttachmentR2SettingRepo()
	stores := make([]*attachmentR2FakeStore, 0, 2)
	service := NewAttachmentR2Service(repo, attachmentR2Encryptor{}, func(_ context.Context, _ *AttachmentR2Config) (AttachmentR2ObjectStore, error) {
		store := &attachmentR2FakeStore{}
		stores = append(stores, store)
		return store, nil
	})
	_, err := service.UpdateConfig(context.Background(), validAttachmentR2Config())
	require.NoError(t, err)
	versionBefore := service.CacheVersion()

	_, _, err = service.Ensure(context.Background(), "attachments/aa/hash.webp", "image/webp", []byte("one"))
	require.NoError(t, err)
	require.Len(t, stores, 1)
	stores[0].mu.Lock()
	require.Equal(t, []string{"sub2api/attachments/aa/hash.webp"}, stores[0].keys)
	stores[0].mu.Unlock()

	updated := validAttachmentR2Config()
	updated.Bucket = "second-private-bucket"
	updated.Prefix = "next/"
	updated.SecretAccessKey = ""
	_, err = service.UpdateConfig(context.Background(), updated)
	require.NoError(t, err)
	require.Greater(t, service.CacheVersion(), versionBefore)

	_, _, err = service.Ensure(context.Background(), "attachments/bb/hash.webp", "image/webp", []byte("two"))
	require.NoError(t, err)
	require.Len(t, stores, 2)
	stores[1].mu.Lock()
	require.Equal(t, []string{"next/attachments/bb/hash.webp"}, stores[1].keys)
	stores[1].mu.Unlock()
}

func TestAttachmentR2RuntimeReloadsExternalSettingChanges(t *testing.T) {
	repo := newAttachmentR2SettingRepo()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	factoryCalls := 0
	service := NewAttachmentR2Service(repo, attachmentR2Encryptor{}, func(_ context.Context, _ *AttachmentR2Config) (AttachmentR2ObjectStore, error) {
		factoryCalls++
		return &attachmentR2FakeStore{}, nil
	})
	service.now = func() time.Time { return now }
	_, err := service.UpdateConfig(context.Background(), validAttachmentR2Config())
	require.NoError(t, err)
	require.True(t, service.Ready(context.Background()))
	require.Equal(t, 1, factoryCalls)

	external := validAttachmentR2Config()
	external.Bucket = "externally-updated"
	external.SecretAccessKey = "ENC:secret-value"
	raw, err := json.Marshal(external)
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), settingKeyAttachmentGatewayR2Config, string(raw)))

	require.True(t, service.Ready(context.Background()))
	require.Equal(t, 1, factoryCalls)
	now = now.Add(attachmentR2ConfigReloadInterval + time.Second)
	require.True(t, service.Ready(context.Background()))
	require.Equal(t, 2, factoryCalls)
}

func TestAttachmentR2DisabledAndInvalidConfigsFailClosed(t *testing.T) {
	repo := newAttachmentR2SettingRepo()
	service := NewAttachmentR2Service(repo, attachmentR2Encryptor{}, func(context.Context, *AttachmentR2Config) (AttachmentR2ObjectStore, error) {
		return &attachmentR2FakeStore{}, nil
	})
	require.False(t, service.Ready(context.Background()))
	_, err := service.Save(context.Background(), "key", "image/webp", []byte("data"))
	require.ErrorIs(t, err, ErrAttachmentR2NotConfigured)

	invalid := validAttachmentR2Config()
	invalid.Endpoint = "http://127.0.0.1:9000"
	_, err = service.UpdateConfig(context.Background(), invalid)
	require.ErrorContains(t, err, "absolute HTTPS URL")

	incomplete := validAttachmentR2Config()
	incomplete.SecretAccessKey = ""
	_, err = NewAttachmentR2Service(newAttachmentR2SettingRepo(), attachmentR2Encryptor{}, nil).
		UpdateConfig(context.Background(), incomplete)
	require.ErrorContains(t, err, "secret_access_key")
}

func TestAttachmentR2CorruptCiphertextIsNeverReturnedOrUsed(t *testing.T) {
	repo := newAttachmentR2SettingRepo()
	cfg := validAttachmentR2Config()
	cfg.SecretAccessKey = "plaintext-or-corrupt"
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), settingKeyAttachmentGatewayR2Config, string(raw)))
	service := NewAttachmentR2Service(repo, attachmentR2Encryptor{}, nil)

	got, err := service.GetConfig(context.Background())
	require.Nil(t, got)
	require.ErrorIs(t, err, ErrAttachmentR2ConfigCorrupt)
}
