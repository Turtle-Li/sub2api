package handler

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const testStudioPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAEklEQVR4nGP4b8zAkPafAUIBACO3BS8QBf+HAAAAAElFTkSuQmCC"

func TestVideoStudioRequestSeparatesRolesAndUsesNativePayloads(t *testing.T) {
	m := service.VideoStudioModel{ID: service.GoodPutH3Model, MinSeconds: 4, MaxSeconds: 15, MaxReferences: 2, SupportsFrames: true, Sizes: []string{"1344x768"}}
	q := studioSubmission{Model: m.ID, Prompt: "camera moves", Seconds: 5, Size: m.Sizes[0], First: testStudioPNG, Last: testStudioPNG}
	b, err := q.providerBody(t.Context(), m)
	require.NoError(t, err)
	_, err = service.ParseGoodPutVideoRequest("application/json", b)
	require.NoError(t, err)
	require.Contains(t, string(b), "last_frame")
	q.First = ""
	_, err = q.providerBody(t.Context(), m)
	require.Error(t, err)
	q.First = testStudioPNG
	q.References = []string{testStudioPNG}
	_, err = q.providerBody(t.Context(), m)
	require.Error(t, err)
	m.ID = service.MuseVideoModel
	m.SupportsFrames = false
	q.Model = m.ID
	q.Seconds = service.MuseVideoDefaultSeconds
	_, err = q.providerBody(t.Context(), m)
	require.Error(t, err)
	q.First = ""
	q.Last = ""
	q.References = []string{testStudioPNG, testStudioPNG}
	b, err = q.providerBody(t.Context(), m)
	require.NoError(t, err)
	native, err := service.ParseMuseMediaRequest(service.MuseVideoCreate, "application/json", b)
	require.NoError(t, err)
	require.Equal(t, service.MuseVideoDefaultSeconds, native.Duration)
	require.NotContains(t, string(b), `"duration"`)
	require.NotContains(t, string(b), "first_frame")
	require.Contains(t, string(b), "images")
	q.References = []string{"https://media.example/image"}
	_, err = q.providerBody(t.Context(), m)
	require.Error(t, err)
	q.References = []string{"data:image/jpeg;base64," + strings.TrimPrefix(testStudioPNG, "data:image/png;base64,")}
	_, err = q.providerBody(t.Context(), m)
	require.Error(t, err)
}

func TestVideoStudioMuseAutomaticDurationWireContract(t *testing.T) {
	ctx := t.Context()
	m := service.VideoStudioModel{ID: service.MuseVideoModel, MinSeconds: 6, MaxSeconds: 6, DefaultSeconds: 6}
	const start = `{"request_id":"01636b55-d5ba-46a0-87d8-292de0c73810","quote_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","model":"muse-video","prompt":"pan"`
	parse := func(field string) (studioSubmission, error) {
		return decodeStudioSubmission([]byte(start + field + `}`))
	}
	a, err := parse("")
	require.NoError(t, err)
	require.Equal(t, service.MuseVideoDefaultSeconds, a.Seconds)
	require.Equal(t, service.VideoStudioDurationAutomatic, a.parameters().DurationMode)
	b, err := parse(`,"seconds":6`)
	require.NoError(t, err)
	require.Equal(t, a.fingerprint(), b.fingerprint(), "implicit and legacy canonical seconds are the same paid intent")
	old, err := parse(`,"seconds":5`)
	require.NoError(t, err, "preserve exact existing-request lookups before validating a new submission")
	require.NotEqual(t, a.fingerprint(), old.fingerprint())
	_, err = old.providerBody(ctx, m)
	require.ErrorContains(t, err, "不能自定义")
	for _, v := range []string{"0", "-1", "null", `"6"`, "6.5", "true"} {
		_, err = parse(`,"seconds":` + v)
		require.Error(t, err, v)
	}
	for _, id := range []string{service.GoodPutH3Model, service.GoodPutH3FastModel} {
		h3 := strings.Replace(start, service.MuseVideoModel, id, 1)
		_, err = decodeStudioSubmission([]byte(h3 + `}`))
		require.Error(t, err, "H3 still requires an explicit selectable duration")
		req, err := decodeStudioSubmission([]byte(h3 + `,"seconds":8}`))
		require.NoError(t, err)
		require.Equal(t, 8, req.Seconds)
		require.Equal(t, service.VideoStudioDurationSelectable, req.parameters().DurationMode)
	}
}
func TestVideoStudioRequestIdentityAndUnknownReceipt(t *testing.T) {
	q := studioSubmission{ID: "01636b55-d5ba-46a0-87d8-292de0c73810", Model: service.MuseVideoModel, Prompt: "orbit", Seconds: 6, Quote: strings.Repeat("a", 64)}
	b, _ := json.Marshal(q)
	parsed, err := decodeStudioSubmission(b)
	require.NoError(t, err)
	require.Equal(t, q.fingerprint(), parsed.fingerprint())
	for _, invalid := range []string{strings.TrimSuffix(string(b), "}") + `,"seconds":5}`, string(b) + "{}", strings.Replace(string(b), "request_id", "unrecognized", 1)} {
		_, err := decodeStudioSubmission([]byte(invalid))
		require.Error(t, err)
	}
	parsed.Seconds++
	require.NotEqual(t, q.fingerprint(), parsed.fingerprint())
	r := studioResult{status: 202}
	_, state, _ := studioCreateOutcome(q.Model, r)
	require.Equal(t, "uncertain", state)
	r.status = 502
	_, state, _ = studioCreateOutcome(q.Model, r)
	require.Equal(t, "uncertain", state)
	r.status = 202
	r.payload.ID = "muse_task_0123456789abcdef0123"
	r.payload.Status = "completed"
	_, state, _ = studioCreateOutcome(q.Model, r)
	require.Equal(t, "queued", state, "Muse must observe completion via original lookup to settle")
	state, _ = studioReceiptState(q.Model, "other", r)
	require.Empty(t, state)
}

func TestVideoStudioRejectsDamagedRasterUnknownArrayAndExhaustedBudget(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(testStudioPNG, "data:image/png;base64,"))
	require.NoError(t, err)
	// The MIME signature and dimensions survive a truncated IDAT. A paid task still must not be sent.
	require.Error(t, validateStudioImage(t.Context(), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(raw[:len(raw)-18])))
	ctx, release := httputil.WithRequestBodyMemoryBudget(t.Context(), httputil.NewBodyMemoryBudget(63))
	defer release()
	require.ErrorIs(t, validateStudioImage(ctx, testStudioPNG), httputil.ErrRequestBodyMemoryBudget)
	q := studioSubmission{ID: "01636b55-d5ba-46a0-87d8-292de0c73810", Model: service.MuseVideoModel, Prompt: "pan", Seconds: 6, Quote: strings.Repeat("a", 64)}
	b, err := json.Marshal(q)
	require.NoError(t, err)
	_, err = decodeStudioSubmission([]byte(strings.TrimSuffix(string(b), "}") + `,"references":["","",""]}`))
	require.Error(t, err)
}
