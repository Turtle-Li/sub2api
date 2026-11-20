package service

import (
	"bytes"
	"context"
	"image/jpeg"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"
)

const (
	studioFramePrefix    = 1 << 20
	studioFrameMaxSource = 24 << 20
)

func validStudioJPEG(b []byte) bool {
	if len(b) <= 64 || len(b) > StudioPosterMaxBytes || !bytes.HasSuffix(b, []byte{0xff, 0xd9}) {
		return false
	}
	c, err := jpeg.DecodeConfig(bytes.NewReader(b))
	return err == nil && c.Width > 0 && c.Height > 0 && c.Width <= 512 && c.Height <= 512
}

// Most fast-start videos need only a prefix. For a tail-indexed MP4, continue the same GET
// within a strict size/time budget. The anonymous seekable file never enters an archive.
type studioVideoPreview struct {
	jpeg       []byte
	durationMS int64
}

func decodeStudioVideoFrame(ctx context.Context, open func(context.Context) (*http.Response, error)) (*studioVideoPreview, error) {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, err
	}
	res, err := open(ctx)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || (res.ContentLength >= 0 && res.ContentLength < 12) {
		return nil, errStudioFrameUnavailable
	}
	file, err := os.CreateTemp("", "sub2-video-frame-*")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err = os.Remove(file.Name()); err != nil {
		return nil, err
	}
	n, err := io.CopyN(file, res.Body, studioFramePrefix)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if n < 12 {
		return nil, errStudioFrameUnavailable
	}
	var header [12]byte
	if _, err = file.ReadAt(header[:], 0); err != nil {
		return nil, err
	}
	var format string
	switch sniffMuseMedia(header[:]) {
	case "video/mp4", "video/quicktime", "video/3gpp":
		format = "mov"
	case "video/webm":
		format = "matroska"
	default:
		return nil, errStudioFrameUnavailable
	}
	encode := func() ([]byte, error) {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, binary,
			"-hide_banner", "-loglevel", "error", "-nostdin", "-xerror",
			"-max_alloc", "67108864", "-cpucount", "1", "-threads", "1", "-filter_threads", "1", "-max_pixels", "16777216",
			"-protocol_whitelist", "fd", "-f", format, "-i", "fd:",
			"-map", "0:V:0", "-an", "-sn", "-dn", "-frames:v", "1",
			"-vf", "scale=w='min(512,iw)':h='min(512,ih)':force_original_aspect_ratio=decrease,setsar=1",
			"-threads", "1", "-c:v", "mjpeg", "-q:v", "5", "-f", "image2pipe", "pipe:1",
		)
		cmd.Env = []string{"LC_ALL=C"}
		cmd.Stdin = file
		out := &studioFrameBuffer{}
		cmd.Stdout = out
		if err := cmd.Run(); err != nil {
			return nil, err
		}
		if !validStudioJPEG(out.Bytes()) {
			return nil, errStudioFrameUnavailable
		}
		return out.Bytes(), nil
	}
	// Never let ffprobe estimate from a truncated fragment. A complete ordinary MOV header can stand alone.
	preview := func(jpeg []byte, complete bool) *studioVideoPreview {
		result := &studioVideoPreview{jpeg: jpeg}
		if complete {
			result.durationMS = studioFileDurationMS(ctx, file, format)
		} else if format == "mov" && n == studioFramePrefix {
			prefix := make([]byte, studioFramePrefix)
			if _, err := file.ReadAt(prefix, 0); err == nil {
				result.durationMS = studioMovieHeaderDuration(prefix)
			}
		}
		return result
	}
	frame, err := encode()
	if err == nil {
		return preview(frame, res.ContentLength == n || (res.ContentLength < 0 && n < studioFramePrefix)), nil
	}
	if ctx.Err() != nil || n < studioFramePrefix || res.ContentLength == n || res.ContentLength > studioFrameMaxSource {
		return nil, err
	}
	if _, err = file.Seek(n, io.SeekStart); err != nil {
		return nil, err
	}
	rest, err := io.Copy(file, io.LimitReader(res.Body, studioFrameMaxSource-n+1))
	if err != nil || rest <= 0 || n+rest > studioFrameMaxSource || (res.ContentLength >= 0 && n+rest != res.ContentLength) {
		return nil, errStudioFrameUnavailable
	}
	frame, err = encode()
	if err != nil {
		return nil, err
	}
	return preview(frame, true), nil
}

// The probe only sees the same complete, anonymous file used for the poster. A missing probe or
// unusable duration leaves the JPEG intact and never triggers another media request or charge.
func studioFileDurationMS(ctx context.Context, file *os.File, format string) int64 {
	binary, err := exec.LookPath("ffprobe")
	if err != nil {
		return 0
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-v", "error", "-max_alloc", "67108864", "-cpucount", "1", "-protocol_whitelist", "fd", "-f", format, "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", "-i", "fd:")
	cmd.Env = []string{"LC_ALL=C"}
	cmd.Stdin = file
	var out studioFrameBuffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0
	}
	seconds, err := strconv.ParseFloat(string(bytes.TrimSpace(out.Bytes())), 64)
	ms := math.Round(seconds * 1000)
	if err != nil || math.IsNaN(ms) || ms < 1 || ms > 1<<53-1 {
		return 0
	}
	return int64(ms)
}

type studioFrameBuffer struct{ data bytes.Buffer }

func (b *studioFrameBuffer) Bytes() []byte { return b.data.Bytes() }

func (b *studioFrameBuffer) Write(p []byte) (int, error) {
	if b.data.Len()+len(p) > StudioPosterMaxBytes {
		return 0, errStudioFrameUnavailable
	}
	return b.data.Write(p)
}
