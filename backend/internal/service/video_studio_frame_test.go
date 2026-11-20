//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVideoStudioFrameUsesBoundedSingleRead(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required for codec test")
	}
	path := filepath.Join(t.TempDir(), "fixture.mp4")
	require.NoError(t, exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=coral:s=640x360:d=0.2", "-c:v", "mpeg4", path).Run())
	src, err := os.ReadFile(path)
	require.NoError(t, err)
	moov := bytes.Index(src, []byte("moov")) - 4
	require.Positive(t, moov)
	free := make([]byte, studioFramePrefix+128)
	binary.BigEndian.PutUint32(free[:4], uint32(len(free)))
	copy(free[4:8], "free")
	for _, tc := range []struct {
		name      string
		b         []byte
		length    int64
		bytesRead int
		ok        bool
	}{{"initial frame without full download", bytes.Join([][]byte{src, free}, nil), -1, studioFramePrefix, true}, {"late index same response", bytes.Join([][]byte{src[:moov], free, src[moov:]}, nil), int64(len(src) + len(free)), len(src) + len(free), true}, {"oversized missing index cannot trigger another download", bytes.Join([][]byte{src[:moov], free}, nil), studioFrameMaxSource + 10, studioFramePrefix, false}, {"non-media has no thumbnail", []byte("definitely not a video"), -1, 22, false}} {
		t.Run(tc.name, func(t *testing.T) {
			r := bytes.NewReader(tc.b)
			calls := 0
			frame, err := decodeStudioVideoFrame(t.Context(), func(context.Context) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(r), ContentLength: tc.length}, nil
			})
			if tc.ok {
				require.NoError(t, err)
				require.True(t, validStudioJPEG(frame.jpeg))
				require.EqualValues(t, 200, frame.durationMS)
			} else {
				require.Error(t, err)
				require.Nil(t, frame)
			}
			require.Equal(t, 1, calls)
			require.Equal(t, tc.bytesRead, len(tc.b)-r.Len())
		})
	}
}
func TestVideoStudioFrameLimitsAndLifetime(t *testing.T) {
	b := &studioFrameBuffer{}
	_, err := b.Write(make([]byte, StudioPosterMaxBytes+1))
	require.Error(t, err)
	require.Empty(t, b.Bytes())
	j := VideoStudioJob{ID: "local", UserID: 1, KeyID: 3, GroupID: 5, Receipt: "bound", Status: "completed", CreatedAt: time.Now()}
	require.Positive(t, j.posterTTL(j.CreatedAt))
	require.Zero(t, j.posterTTL(j.CreatedAt.Add(24*time.Hour)))
	j.Status = "failed"
	require.Zero(t, j.posterTTL(j.CreatedAt))
}
