package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestS3ImageStorageEnsureReusesExistingContentAddressedObject(t *testing.T) {
	var mu sync.Mutex
	exists := false
	headCalls := 0
	putCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodHead:
			headCalls++
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodPut:
			putCalls++
			_, _ = io.Copy(io.Discard, r.Body)
			exists = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	store, err := NewS3ImageStorage(context.Background(), &config.ImageStorageConfig{
		Endpoint:        server.URL,
		Region:          "auto",
		Bucket:          "private-bucket",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
		ForcePathStyle:  true,
		PublicBaseURL:   "https://objects.example.test",
	})
	require.NoError(t, err)

	firstURL, firstUploaded, err := store.Ensure(context.Background(), "attachments/ab/hash.webp", "image/webp", []byte("optimized"))
	require.NoError(t, err)
	require.True(t, firstUploaded)
	require.Equal(t, "https://objects.example.test/attachments/ab/hash.webp", firstURL)

	secondURL, secondUploaded, err := store.Ensure(context.Background(), "attachments/ab/hash.webp", "image/webp", []byte("optimized"))
	require.NoError(t, err)
	require.False(t, secondUploaded)
	require.Equal(t, firstURL, secondURL)

	mu.Lock()
	require.Equal(t, 2, headCalls)
	require.Equal(t, 1, putCalls)
	mu.Unlock()
}
