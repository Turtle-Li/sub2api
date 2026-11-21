//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func (b *grokMediaSlotBindings) ClaimStudioPoster(_ context.Context, k, l string, _ time.Duration) (bool, error) {
	if b.posters == nil || b.posters[k] != nil {
		return false, nil
	}
	b.posters[k] = []byte(l)
	return true, nil
}
func (b *grokMediaSlotBindings) SaveStudioPoster(_ context.Context, k, l string, p []byte, _ time.Duration) error {
	if string(b.posters[k]) == l {
		b.posters[k] = p
	}
	return nil
}
func (b *grokMediaSlotBindings) StudioPosterSizes(_ context.Context, keys []string) ([]int64, error) {
	v := make([]int64, len(keys))
	for i, k := range keys {
		v[i] = int64(len(b.posters[k]))
	}
	return v, nil
}
func (b *grokMediaSlotBindings) StudioPosterBytes(_ context.Context, k string) ([]byte, error) {
	return b.posters[k], nil
}

func TestVideoStudioGalleryNewAndLegacyParameterModes(t *testing.T) {
	zero := int64(0)
	for _, tc := range []struct {
		q    studioSubmission
		mode string
		refs int
	}{{studioSubmission{}, "text", 0}, {studioSubmission{References: []string{"a", "b"}}, "references", 2}, {studioSubmission{First: "a"}, "first_frame", 0}, {studioSubmission{First: "a", Last: "b", Seed: &zero}, "first_last_frame", 0}} {
		p := tc.q.parameters()
		require.Equal(t, tc.mode, p.MaterialMode)
		require.Equal(t, tc.refs, p.ReferenceCount)
		require.Equal(t, tc.q.Seed, p.Seed)
	}
	body, err := json.Marshal(service.VideoStudioJob{})
	require.NoError(t, err)
	require.NotContains(t, string(body), "parameters")
	require.NotContains(t, string(body), "has_poster")
}

func TestVideoStudioGalleryCompletedBeforeFirstPlay(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg runtime not installed")
	}
	clip, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=royalblue:s=128x80:d=1", "-c:v", "mpeg4", "-movflags", "frag_keyframe+empty_moov", "-f", "mp4", "pipe:1").Output()
	require.NoError(t, err)
	for _, tc := range []struct {
		name, model string
		immediate   bool
	}{
		{"Muse observed completion", service.MuseVideoModel, false}, {"H3 observed completion", service.GoodPutH3Model, false}, {"H3 immediate complete without verified meter", service.GoodPutH3FastModel, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := tc.model
			muse := model == service.MuseVideoModel
			h, slots, bindings, up := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, model)
			key, repo, user, run := studioJourney(t, h, model)
			bindings.posters = map[string][]byte{}
			if muse {
				pr := .15
				key.Group.VideoPrice720P = &pr
				key.Group.ModelPricing = nil
			}
			native, file := "video_studio", "0123456789abcdef0123456789abcdef.mp4"
			if muse {
				native = "task_0123456789abcdef0123"
			}
			state := "queued"
			posts, observations, mediaReads := 0, 0, 0
			up.call = func(req *http.Request, acc int64) (*http.Response, error) {
				require.Equal(t, int64(1), acc)
				var b []byte
				typ := "application/json"
				switch req.URL.Path {
				case "/api/v1/public/models":
					b = []byte(goodPutVideoTestCatalog(model, "0.09"))
				case "/v1/videos":
					posts++
					if tc.immediate {
						b = []byte(fmt.Sprintf(`{"id":%q,"status":"completed"}`, native))
					} else {
						b = []byte(fmt.Sprintf(`{"id":%q,"status":"queued"}`, native))
					}
				case "/v1/videos/" + native:
					observations++
					b = []byte(fmt.Sprintf(`{"id":%q,"status":%q,"result":{"filename":%q},"usage":{"duration_seconds":5}}`, native, state, file))
				case "/v1/videos/" + native + "/content", "/v1/media/" + file:
					mediaReads++
					require.Equal(t, "GET", req.Method)
					if muse {
						require.Empty(t, req.Header.Get("Authorization"))
					}
					b = clip
					typ = "video/mp4"
				default:
					t.Fatalf("unexpected request %s", req.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {typ}}, ContentLength: int64(len(b)), Body: io.NopCloser(bytes.NewReader(b))}, nil
			}
			var catalog struct {
				Data struct{ Items []service.VideoStudioModel }
			}
			require.NoError(t, json.Unmarshal(run("GET", "models", nil).Body.Bytes(), &catalog))
			require.Len(t, catalog.Data.Items, 1)
			id := uuid.NewString()
			b, err := json.Marshal(studioSubmission{ID: id, Model: model, Quote: catalog.Data.Items[0].QuoteID, Prompt: "private test prompt", Seconds: catalog.Data.Items[0].DefaultSeconds, References: []string{testStudioPNG}})
			require.NoError(t, err)
			w := run("POST", "jobs", b)
			require.Equal(t, 202, w.Code, w.Body.String())
			url := "jobs/" + id
			wantObservations := 0
			if !tc.immediate {
				require.Equal(t, 404, run("GET", url+"/poster", nil).Code)
				require.Zero(t, observations)
				require.Zero(t, mediaReads)
				state = "completed"
				w = run("GET", url, nil)
				require.Equal(t, 200, w.Code, w.Body.String())
				wantObservations = 1
			} else {
				require.Contains(t, w.Body.String(), `"billing_review":true`)
			}
			require.Contains(t, w.Body.String(), `"has_poster":true`)
			require.Contains(t, w.Body.String(), `"poster_expires_at"`)
			require.Contains(t, w.Body.String(), `"actual_duration_ms":1000`)
			require.NotContains(t, w.Body.String(), file)
			require.Equal(t, wantObservations, observations)
			require.Equal(t, 1, mediaReads)
			key.User.Balance = 0
			for range 3 {
				w = run("GET", "jobs", nil)
				require.Contains(t, w.Body.String(), `"has_poster":true`)
				require.Contains(t, w.Body.String(), `"poster_expires_at"`)
				require.Contains(t, w.Body.String(), `"actual_duration_ms":1000`)
				require.Contains(t, w.Body.String(), `"reference_count":1`)
				require.NotContains(t, w.Body.String(), "private test prompt")
				w = run("GET", url+"/poster", nil)
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
				require.Equal(t, "image/jpeg", w.Header().Get("Content-Type"))
				im, err := jpeg.Decode(w.Body)
				require.NoError(t, err)
				r, g, b, _ := im.At(30, 20).RGBA()
				require.Greater(t, b, r)
				require.Greater(t, b, g)
			}
			require.Equal(t, wantObservations, observations)
			require.Equal(t, 1, mediaReads)
			require.Equal(t, 1, posts)
			if muse {
				require.Equal(t, 1, bindings.museLedger.captures)
				bindings.museLedger.job.AccountID = 0
				require.Equal(t, 404, run("GET", url+"/poster", nil).Code)
				bindings.museLedger.job.AccountID = 1
			}
			*user++
			require.Equal(t, 404, run("GET", url+"/poster", nil).Code)
			*user--
			key.User.AllowedGroups = nil
			require.NotEqual(t, 200, run("GET", url+"/poster", nil).Code)
			key.User.AllowedGroups = []int64{key.Group.ID}
			row := repo.rows[id]
			row.CreatedAt = time.Now().Add(-25 * time.Hour)
			repo.rows[id] = row
			require.NotEqual(t, 200, run("GET", url+"/poster", nil).Code)
			require.NotContains(t, run("GET", "jobs", nil).Body.String(), `"has_poster":true`)
			require.Equal(t, wantObservations, observations)
			require.Equal(t, 1, mediaReads)
			slots.assertReleased(t)
		})
	}
}
