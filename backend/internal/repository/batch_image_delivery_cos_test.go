//go:build unit

package repository

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
)

const testBatchImageCOSVaultPath = "secret/data/sub2api/test-batch-image-cos"

func testBatchImageCOSConfig(endpoint string) *config.Config {
	return &config.Config{BatchImage: config.BatchImageConfig{
		Enabled:                            true,
		DeliveryCOSEndpoint:                endpoint,
		DeliveryCOSRegion:                  "auto",
		DeliveryCOSBucket:                  "private-bucket",
		DeliveryCOSAccessKeyVaultRef:       "vault://" + testBatchImageCOSVaultPath + "#access_key_id",
		DeliveryCOSSecretAccessKeyVaultRef: "vault://" + testBatchImageCOSVaultPath + "#secret_access_key",
		DeliveryCOSVaultAgentSocket:        batchImageCOSVaultAgentSocket,
		DeliveryCOSForcePathStyle:          true,
	}}
}

func testBatchImageCOSVaultClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: newInProcessTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/"+testBatchImageCOSVaultPath, r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"data":{"access_key_id":"test-access","secret_access_key":"test-secret"}}}`)
	}), nil)}
}

func testBatchImageCOSStore(t *testing.T, endpoint string) *batchImageCOSDeliveryStore {
	t.Helper()
	client, err := newS3Client(context.Background(), s3ClientParams{
		Endpoint:        endpoint,
		Region:          "auto",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
		ForcePathStyle:  true,
	})
	require.NoError(t, err)
	return &batchImageCOSDeliveryStore{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  "private-bucket",
	}
}

func TestBatchImageCOSPresignUsesPrivateBucketVirtualHost(t *testing.T) {
	client, err := newS3Client(context.Background(), s3ClientParams{
		Endpoint:        "https://cos.ap-shanghai.myqcloud.com",
		Region:          "ap-shanghai",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret-only",
	})
	require.NoError(t, err)
	store := &batchImageCOSDeliveryStore{client: client, presign: s3.NewPresignClient(client), bucket: "image-1309919944"}
	signed, err := store.PresignPut(
		context.Background(),
		"sub2-batch-image/prod/imgbatch_0123456789abcdef0123456789abcdef/hash/0",
		time.Hour,
	)
	require.NoError(t, err)
	parsed, err := url.Parse(signed)
	require.NoError(t, err)
	require.Equal(t, "https", parsed.Scheme)
	require.Equal(t, "image-1309919944.cos.ap-shanghai.myqcloud.com", parsed.Host)
	require.Contains(t, parsed.Path, "/sub2-batch-image/prod/")
	require.NotEmpty(t, parsed.Query().Get("X-Amz-Signature"))
	require.NotContains(t, signed, "test-secret-only")
}

func TestBatchImageCOSStoreRemainsAvailableWhenUpscaleIsDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.True(t, r.URL.Query().Has("versioning"))
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"/>`)
	}))
	t.Cleanup(server.Close)
	cfg := testBatchImageCOSConfig(server.URL)
	cfg.BatchImage.DeliveryCOSRegion = "ap-shanghai"
	cfg.BatchImage.DeliveryCOSBucket = "image-1309919944"
	cfg.ImageUpscale = config.ImageUpscaleConfig{Enabled: false}

	store, err := provideBatchImageDeliveryObjectStore(cfg, testBatchImageCOSVaultClient(t))
	require.NoError(t, err)
	require.NotNil(t, store)
	cfg.BatchImage.DeliveryCOSSecretAccessKeyVaultRef = ""
	store, err = provideBatchImageDeliveryObjectStore(cfg, testBatchImageCOSVaultClient(t))
	require.NoError(t, err)
	require.Nil(t, store)
}

func TestBatchImageCOSPutIfAbsentUsesAtomicPrecondition(t *testing.T) {
	exists := false
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Has("versioning") {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"/>`)
			return
		}
		requestCount++
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "true", r.Header.Get("x-cos-forbid-overwrite"))
		require.Empty(t, r.Header.Get("If-None-Match"))
		_, err := io.Copy(io.Discard, r.Body)
		require.NoError(t, err)
		if exists {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `<Error><Code>FileAlreadyExists</Code><Message>already exists</Message></Error>`)
			return
		}
		exists = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	providerStore := testBatchImageCOSStore(t, server.URL)

	created, err := providerStore.PutIfAbsent(context.Background(), "provider/openai/attempt", "text/plain", bytes.NewReader([]byte("owner-a")), 7)
	require.NoError(t, err)
	require.True(t, created)
	created, err = providerStore.PutIfAbsent(context.Background(), "provider/openai/attempt", "text/plain", bytes.NewReader([]byte("owner-b")), 7)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, 2, requestCount)
}

func TestBatchImageCOSPutIfAbsentFailsClosedForVersionedBucket(t *testing.T) {
	for _, status := range []string{"Enabled", "Suspended"} {
		t.Run(status, func(t *testing.T) {
			putCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Query().Has("versioning") {
					w.Header().Set("Content-Type", "application/xml")
					_, _ = io.WriteString(w, `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>`+status+`</Status></VersioningConfiguration>`)
					return
				}
				putCount++
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(server.Close)

			providerStore := testBatchImageCOSStore(t, server.URL)

			created, err := providerStore.PutIfAbsent(context.Background(), "provider/openai/attempt", "text/plain", bytes.NewReader([]byte("owner-a")), 7)
			require.Error(t, err)
			require.False(t, created)
			require.Equal(t, 0, putCount)
		})
	}
}

func TestBatchImageCOSPutIfAbsentFailsClosedWhenVersioningStateCannotBeRead(t *testing.T) {
	putCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Has("versioning") {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
			return
		}
		putCount++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	providerStore := testBatchImageCOSStore(t, server.URL)

	created, err := providerStore.PutIfAbsent(context.Background(), "provider/openai/attempt", "text/plain", bytes.NewReader([]byte("owner-a")), 7)
	require.Error(t, err)
	require.False(t, created)
	require.Equal(t, 0, putCount)
}

func TestBatchImageCOSProviderRejectsVersionedOrUnreadableBucketAtStartup(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "enabled", status: http.StatusOK, body: `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>Enabled</Status></VersioningConfiguration>`},
		{name: "suspended", status: http.StatusOK, body: `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>Suspended</Status></VersioningConfiguration>`},
		{name: "forbidden", status: http.StatusForbidden, body: `<Error><Code>AccessDenied</Code></Error>`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.True(t, r.URL.Query().Has("versioning"))
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			t.Cleanup(server.Close)
			cfg := testBatchImageCOSConfig(server.URL)

			store, err := provideBatchImageDeliveryObjectStore(cfg, testBatchImageCOSVaultClient(t))
			require.Error(t, err)
			require.Nil(t, store)
		})
	}
}

func TestBatchImageCOSVaultCredentialsFailClosed(t *testing.T) {
	cfg := testBatchImageCOSConfig("https://cos.ap-shanghai.myqcloud.com")
	tests := map[string]func(*config.Config){
		"wrong access field": func(cfg *config.Config) {
			cfg.BatchImage.DeliveryCOSAccessKeyVaultRef = "vault://" + testBatchImageCOSVaultPath + "#wrong"
		},
		"different paths": func(cfg *config.Config) {
			cfg.BatchImage.DeliveryCOSSecretAccessKeyVaultRef = "vault://secret/data/sub2api/other#secret_access_key"
		},
		"wrong socket": func(cfg *config.Config) {
			cfg.BatchImage.DeliveryCOSVaultAgentSocket = "/tmp/public.sock"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := *cfg
			candidate.BatchImage = cfg.BatchImage
			mutate(&candidate)
			store, err := provideBatchImageDeliveryObjectStore(&candidate, testBatchImageCOSVaultClient(t))
			require.Error(t, err)
			require.Nil(t, store)
		})
	}
}

func TestBatchImageCOSVaultCredentialsRejectIncompletePayload(t *testing.T) {
	cfg := testBatchImageCOSConfig("https://cos.ap-shanghai.myqcloud.com")
	client := &http.Client{Transport: newInProcessTransport(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"data":{"access_key_id":"test-access"}}}`)
	}), nil)}

	store, err := provideBatchImageDeliveryObjectStore(cfg, client)
	require.Error(t, err)
	require.Nil(t, store)
}
