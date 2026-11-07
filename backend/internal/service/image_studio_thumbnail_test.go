//go:build unit

package service

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageStudioThumbnailShrinksAndCachesAuthorizedSource(t *testing.T) {
	svc, repo, _, _, _ := persistentThumbnailFixture(t)
	limiter := svc.Limiter.(*fakeBatchImageDownloadLimiter)
	original := image.NewRGBA(image.Rect(0, 0, 2048, 1024))
	for y := 0; y < 1024; y++ {
		for x := 0; x < 2048; x++ {
			original.SetRGBA(x, y, color.RGBA{R: uint8(x % 251), G: uint8(y % 241), B: uint8(x * y % 239), A: 255})
		}
	}
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, original))
	provider := &publicBatchImageProvider{name: BatchImageProviderGeminiAPI, result: fmt.Sprintf(`{"key":"cover/../001","response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"%s"}}]}}]}}`, base64.StdEncoding.EncodeToString(encoded.Bytes()))}
	svc.ProviderRegistry = NewBatchImageProviderRegistry(provider)
	thumbnail, err := svc.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "cover/../001")
	require.NoError(t, err)
	config, format, err := image.DecodeConfig(bytes.NewReader(thumbnail))
	require.NoError(t, err)
	require.Equal(t, "jpeg", format)
	require.Equal(t, 480, config.Width)
	require.Equal(t, 240, config.Height)
	require.Less(t, len(thumbnail), encoded.Len()/4)
	t.Logf("2048x1024 source: %d bytes; 480x240 JPEG preview: %d bytes", encoded.Len(), len(thumbnail))
	require.Equal(t, limiter.acquireCount, limiter.releaseCount)
	acquires := limiter.acquireCount
	_, err = svc.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "cover/../001")
	require.NoError(t, err)
	require.Equal(t, acquires, limiter.acquireCount)
	wrong := testBatchImageOwner()
	wrong.UserID++
	_, err = svc.ItemThumbnail(t.Context(), wrong, "imgbatch_download", "cover/../001")
	require.ErrorIs(t, err, ErrBatchImageJobNotFound)
	repo.jobs["imgbatch_download"].Status = BatchImageJobStatusOutputDeleted
	_, err = svc.ItemThumbnail(t.Context(), testBatchImageOwner(), "imgbatch_download", "cover/../001")
	require.Error(t, err)
}

func TestImageStudioThumbnailBoundsCacheAndRejectsInvalidImage(t *testing.T) {
	_, err := encodeStudioThumbnail([]byte("invalid image"))
	require.Error(t, err)
	cache := &batchImageThumbnailCache{}
	for i := 0; i < studioThumbnailCacheEntries+1; i++ {
		cache.put(fmt.Sprint(i), []byte{1})
	}
	require.Nil(t, cache.get("0"))
	require.Equal(t, []byte{1}, cache.get("256"))
	cache.put("large", make([]byte, studioThumbnailCacheBytes))
	require.Len(t, cache.entries, 1)
	require.Equal(t, studioThumbnailCacheBytes, cache.size)
}
