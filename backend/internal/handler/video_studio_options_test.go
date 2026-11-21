package handler

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestVideoStudioTypedMuseOptionsAndMixedReferenceOrder(t *testing.T) {
	m := service.VideoStudioModel{ID: service.MuseVideoModel, MinSeconds: 5, MaxSeconds: 15, MaxReferences: 10, ReferenceKinds: []string{"image", "video", "audio", "file"}, MuseOptions: &service.VideoStudioMuseOptions{ResolutionMaxBytes: 64, ExtraMaxBytes: 4096, TimeoutMax: 600, NativeSizeMaxBytes: 64}}
	inline := func(typ, data string) string {
		return "data:" + typ + ";base64," + base64.StdEncoding.EncodeToString([]byte(data))
	}
	timeout := 600
	q := studioSubmission{Model: m.ID, Prompt: "cup", Seconds: 6, Timeout: &timeout, Extra: "private instruction", Resolution: "720p", NativeSize: "1280x720", References: []string{inline("audio/mpeg", "\xff\xfb\x90\x64fixture"), inline("video/mp4", "\x00\x00\x00\x18ftypisom12345678"), testStudioPNG, inline("application/pdf", "%PDF-1.4\nfixture")}}
	body, err := q.providerBody(t.Context(), m)
	require.NoError(t, err)
	req, err := service.ParseMuseMediaRequest(service.MuseVideoCreate, "application/json", body)
	require.NoError(t, err)
	require.Equal(t, 600, req.Timeout)
	require.Equal(t, "720p", req.Resolution)
	require.Equal(t, q.Extra, req.Extra)
	require.Equal(t, q.NativeSize, req.Size)
	_, err = req.PrepareReferences(t.Context())
	require.NoError(t, err)
	require.Len(t, req.Images, 4)
	for i, ref := range q.References {
		require.Equal(t, ref, req.Images[i])
	}
	p := q.parameters()
	require.Equal(t, []string{"audio", "video", "image", "file"}, p.ReferenceKinds)
	data, err := json.Marshal(p)
	require.NoError(t, err)
	require.NotContains(t, string(data), q.Extra)
	require.NotContains(t, string(data), "data:")
	require.True(t, p.HasExtra)
	for _, f := range []func(*studioSubmission){func(s *studioSubmission) { v := 601; s.Timeout = &v }, func(s *studioSubmission) { v := 0; s.Timeout = &v }, func(s *studioSubmission) { s.Size = "16:9" }, func(s *studioSubmission) { s.Extra = strings.Repeat("x", 4097) }, func(s *studioSubmission) { s.Resolution = strings.Repeat("界", 22) }, func(s *studioSubmission) { s.References = []string{inline("video/mp4", "%PDF-1.4")} }, func(s *studioSubmission) { s.References = []string{"https://localhost/reference.png"} }} {
		bad := q
		f(&bad)
		_, err := bad.providerBody(t.Context(), m)
		require.Error(t, err)
	}
	h := m
	h.ID = service.GoodPutH3Model
	h.ReferenceKinds = []string{"image", "audio", "video"}
	h.MuseOptions = nil
	q.Model = h.ID
	_, err = q.providerBody(t.Context(), h)
	require.Error(t, err)
	q.Timeout = nil
	q.Extra = ""
	q.Resolution = ""
	q.NativeSize = ""
	_, err = q.providerBody(t.Context(), h)
	require.Error(t, err, "file refs rejected on H3 without contacting a provider")
	q.References = q.References[:3]
	body, err = q.providerBody(t.Context(), h)
	require.NoError(t, err)
	var native struct{ Media []struct{ Type string } }
	require.NoError(t, json.Unmarshal(body, &native))
	require.Equal(t, "reference_audio", native.Media[0].Type)
	require.Equal(t, "reference_video", native.Media[1].Type)
	require.Equal(t, "reference_image", native.Media[2].Type)
}
