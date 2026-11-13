//go:build unit

package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/netip"
	"net/textproto"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMuseMediaForwardsNativeMultimodalOptionsInOrder(t *testing.T) {
	png := "data:image/png;base64," + museTestPNG(t)
	rawWebP := base64.StdEncoding.EncodeToString(museTestWebP(t))
	webp := "data:image/webp;base64," + rawWebP
	clip := museDataURL([]byte("\x00\x00\x00\x18ftypmp42sample"), "video/mp4")
	audio := museDataURL([]byte("RIFF\x10\x00\x00\x00WAVEfmt "), "audio/wav")
	doc := museDataURL([]byte("%PDF-1.7"), "application/pdf")
	raw, err := json.Marshal(map[string]any{
		"model": "muse-video", "prompt": "Use [图1] and [图2]", "duration": 10,
		"aspect_ratio": "9:16", "size": "720x1280", "resolution": "1080p", "timeout": 300, "extra": "slow motion",
		"images": []any{map[string]any{"image_url": map[string]string{"url": png}}, map[string]string{"b64_json": rawWebP}},
		"videos": []any{map[string]string{"video_url": clip}},
		"audios": []string{audio}, "files": []any{map[string]string{"file_url": doc}},
	})
	require.NoError(t, err)
	req, err := ParseMuseMediaRequest(MuseVideoCreate, "application/json", raw)
	require.NoError(t, err)
	audited, err := req.PrepareReferences(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{png, webp}, audited)
	require.Equal(t, []any{png, webp, clip, audio, doc}, req.Images)
	require.Equal(t, "9:16", req.AspectRatio)
	require.Equal(t, "720x1280", req.Size)
	require.Equal(t, "1080p", req.Resolution)
	require.Equal(t, 300, req.Timeout)
	require.Equal(t, "slow motion", req.Extra)
	require.Equal(t, 10, req.Info().DurationSeconds)
	require.Equal(t, VideoBillingResolution720P, req.Info().Resolution)
}

func TestMuseImageMultipartMixedFiles(t *testing.T) {
	png, err := base64.StdEncoding.DecodeString(museTestPNG(t))
	require.NoError(t, err)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range [][2]string{{"async", "true"}, {"aspect_ratio", "3:4"}, {"response_format", "b64_json"}, {"timeout", "60"}} {
		require.NoError(t, writer.WriteField(field[0], field[1]))
	}
	upload := func(name, filename, mediaType string, content []byte) {
		t.Helper()
		part, err := writer.CreatePart(textproto.MIMEHeader{
			"Content-Disposition": {`form-data; name="` + name + `"; filename="` + filename + `"`},
			"Content-Type":        {mediaType},
		})
		require.NoError(t, err)
		_, err = part.Write(content)
		require.NoError(t, err)
	}
	pdf := []byte("%PDF-1.7")
	upload("file", "reference.pdf", "application/octet-stream", pdf)
	upload("image[]", "reference.png", "image/png", png)
	require.NoError(t, writer.Close())
	req, err := ParseMuseMediaRequest(MuseImageEdit, writer.FormDataContentType(), body.Bytes())
	require.NoError(t, err)
	audited, err := req.PrepareReferences(t.Context())
	require.NoError(t, err)
	require.Equal(t, MuseImageModel, req.Model)
	require.Equal(t, "参考此图片并进行生图创作", req.Prompt)
	require.True(t, req.Async)
	require.Equal(t, "b64_json", req.ResponseFormat)
	require.Equal(t, "3:4", req.AspectRatio)
	require.Equal(t, 60, req.Timeout)
	require.Equal(t, []string{museDataURL(png, "image/png")}, audited)
	require.Equal(t, []any{audited[0], museDataURL(pdf, "application/pdf")}, req.Images)
}

func TestMuseMediaRejectsReferenceAmbiguityAndCount(t *testing.T) {
	_, err := ParseMuseMediaRequest(MuseVideoCreate, "application/json",
		[]byte(`{"prompt":"x","images":["abc"],"image_url":{"url":"abc"}}`))
	require.ErrorContains(t, err, "legacy")
	_, err = ParseMuseMediaRequest(MuseImageCreate, "application/json",
		[]byte(`{"prompt":"x","images":[`+strings.Repeat(`"abc",`, 9)+`"abc"],"files":["abc"]}`))
	require.ErrorContains(t, err, "at most 10")
}

func TestMuseReferenceURLRejectsNonPublicAddressesAndUnsafeRedirects(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1", "http://169.254.169.254", "https://metadata.google.internal",
		"https://[::ffff:10.0.0.1]", "http://[64:ff9b::a00:1]", "http://192.0.2.1",
		"http://192.168.1.1", "http://100.64.1.2", "http://198.18.0.1",
		"http://localhost.", "http://user:pw@93.184.216.34", "file:///etc/passwd",
	} {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		require.Error(t, validateMuseReferenceURL(u), raw)
		_, _, _, err = fetchMuseReference(t.Context(), raw)
		require.Error(t, err, raw)
	}
	require.True(t, isMusePublicAddress(netip.MustParseAddr("2606:4700:4700::1111")))
	u, err := url.Parse("https://93.184.216.34/image.png")
	require.NoError(t, err)
	require.NoError(t, validateMuseReferenceURL(u))
	privateRedirect, err := http.NewRequest(http.MethodGet, "http://169.254.169.254", nil)
	require.NoError(t, err)
	privateRedirect.Header.Set("Referer", "https://public.example/path?private-signature")
	require.Error(t, museReferenceHTTP.CheckRedirect(privateRedirect, nil))
	require.Empty(t, privateRedirect.Header.Get("Referer"))
}
