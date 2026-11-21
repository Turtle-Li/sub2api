//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAliyunHostCredentialIsScopedToItsFixedSocketAndReference(t *testing.T) {
	require.NotNil(t, newImageUpscaleVaultLoader(config.ImageUpscaleAliyunHostAgentSocket, aliyunUpscaleVaultRef))
	require.Nil(t, newImageUpscaleVaultLoader(config.ImageUpscaleAliyunHostAgentSocket, "vault://secret/data/unrelated#api_key"))
	require.Nil(t, newImageUpscaleVaultLoader("/tmp/arbitrary.sock", aliyunUpscaleVaultRef))
	require.NotNil(t, newImageUpscaleVaultLoader(imageUpscaleVaultSocket, aliyunUpscaleVaultRef))
}

type aliyunTestTransport func(*http.Request) (*http.Response, error)

func (f aliyunTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAliyunUpscaleSignedLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		scale   int
		failure string
	}{{"native_2x", 2, ""}, {"native_4x", 4, ""}, {"lost_paid_response", 2, "PROCESS"}, {"invalid_upload_region", 2, "UPLOAD_POLICY"}, {"redirect", 2, "DOWNLOAD"}, {"wrong_size", 4, "RESULT_DIMENSIONS"}, {"oversize_result", 2, "DOWNLOAD"}} {
		t.Run(tc.name, func(t *testing.T) {
			const access = `{"access_key_id":"fixture-id","access_key_secret":"fixture-secret"}`
			const bucket = "temporary-input.oss-cn-shanghai.aliyuncs.com"
			const outputs = "ivpd-cn-shanghai.oss-cn-shanghai.aliyuncs.com"
			const key = "tmp/fixture one"
			input := imageUpscaleTestPNG(t, 56, 32)
			size := tc.scale
			if tc.failure == "RESULT_DIMENSIONS" {
				size = 1
			}
			final := imageUpscaleTestPNG(t, 56*size, 32*size)
			calls := map[string]int{}
			tr := aliyunTestTransport(func(r *http.Request) (*http.Response, error) {
				// net/http canonicalizes headers on the receiving server. The SDK emits
				// lowercase keys directly, so reproduce that wire boundary in this transport.
				hdr := make(http.Header)
				for k, v := range r.Header {
					for _, x := range v {
						hdr.Add(k, x)
					}
				}
				r.Header = hdr
				require.Equal(t, "https", r.URL.Scheme)
				calls[r.URL.Host]++
				status := 200
				body := []byte(nil)
				header := http.Header{"Content-Type": []string{"application/json"}}
				switch r.URL.Host {
				case "openplatform.aliyuncs.com":
					require.Equal(t, "AuthorizeFileUpload", r.Header.Get("x-acs-action"))
					require.NotEmpty(t, r.Header.Get("Authorization"))
					require.Equal(t, "imageenhan", r.URL.Query().Get("Product"))
					require.Equal(t, "cn-shanghai", r.URL.Query().Get("RegionId"))
					region := aliyunOSSRegion
					if tc.failure == "UPLOAD_POLICY" {
						region = "untrusted.invalid"
					}
					body = fmt.Appendf(nil, `{"Bucket":"temporary-input","Endpoint":%q,"ObjectKey":%q,"AccessKeyId":"staging","EncodedPolicy":"policy","Signature":"signed"}`, region, key)
				case bucket:
					require.Empty(t, r.Header.Get("Authorization"))
					require.NoError(t, r.ParseMultipartForm(5<<20))
					require.Equal(t, key, r.FormValue("key"))
					require.Equal(t, "201", r.FormValue("success_action_status"))
					require.Equal(t, "policy", r.FormValue("policy"))
					file, _, err := r.FormFile("file")
					require.NoError(t, err)
					data, err := io.ReadAll(file)
					require.NoError(t, err)
					require.NoError(t, file.Close())
					require.Equal(t, input, data)
					status = 201
				case "imageenhan.cn-shanghai.aliyuncs.com":
					require.Equal(t, "MakeSuperResolutionImage", r.Header.Get("x-acs-action"))
					require.NotEmpty(t, r.Header.Get("Authorization"))
					require.NoError(t, r.ParseForm())
					require.Equal(t, fmt.Sprint(tc.scale), r.Form.Get("UpscaleFactor"))
					require.Equal(t, "png", r.Form.Get("OutputFormat"))
					u, err := url.Parse(r.Form.Get("Url"))
					require.NoError(t, err)
					require.Equal(t, "https", u.Scheme)
					require.Equal(t, bucket, u.Host)
					require.Equal(t, "/"+key, u.Path)
					if tc.failure == "PROCESS" {
						return nil, errors.New("ambiguous transport failure")
					}
					body = []byte(`{"RequestId":"ok","Data":{"Url":"http://` + outputs + `/out/img?Signature=fixture"}}`)
				case outputs:
					require.Empty(t, r.Header.Get("Authorization"))
					require.Equal(t, "fixture", r.URL.Query().Get("Signature"))
					body = final
					if tc.failure == "DOWNLOAD" && tc.name == "redirect" {
						status = 302
						header.Set("Location", "https://unexpected.invalid/file")
					}
				default:
					t.Fatalf("unexpected destination %s", r.URL.Host)
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
			})
			s := newAliyunTestUpscaler(tr, access)
			if tc.name == "oversize_result" {
				s.maxBytes = 4
			}
			got, err := s.upscale(t.Context(), input, tc.scale)
			if tc.failure != "" {
				requireImageUpscaleError(t, err, "ALIYUN_"+tc.failure)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, final, got.Data)
				require.Equal(t, 56*tc.scale, got.Width)
				require.Empty(t, got.URL)
			}
			if tc.failure == "UPLOAD_POLICY" {
				require.Len(t, calls, 1)
			} else {
				require.Equal(t, 1, calls["imageenhan.cn-shanghai.aliyuncs.com"], "never replay a paid POST")
			}
		})
	}
}
func newAliyunTestUpscaler(tr http.RoundTripper, cred string) *aliyunUpscaler {
	return &aliyunUpscaler{http: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, load: func(context.Context) ([]byte, error) { return []byte(cred), nil }, maxBytes: 1 << 20}
}

func TestAliyunSourceLimitsAndGeometry(t *testing.T) {
	for _, tc := range []struct{ w, h, nw, nh int }{{192, 144, 192, 144}, {2048, 768, 1920, 720}, {1270, 1200, 1143, 1080}, {16, 64, 32, 64}} {
		src := imageUpscaleTestPNG(t, tc.w, tc.h)
		in, err := prepareAliyunInput(src)
		require.NoError(t, err)
		require.Equal(t, tc.nw, in.canvas.X)
		require.Equal(t, tc.nh, in.canvas.Y)
		output, err := in.result(imageUpscaleTestPNG(t, tc.nw*2, tc.nh*2), 2, 16<<20)
		require.NoError(t, err)
		w, h, _, err := decodeUpscaleImageConfig(output.Data)
		require.NoError(t, err)
		require.Equal(t, tc.w*2, w)
		require.Equal(t, tc.h*2, h)
	}
}
func TestAliyunAcceptsActualJPEGWithoutMislabeling(t *testing.T) {
	in, err := prepareAliyunInput(imageUpscaleTestPNG(t, 48, 32))
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, 96, 64)), nil))
	result, err := in.result(out.Bytes(), 2, 1<<20)
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", result.MimeType)
	require.Equal(t, out.Bytes(), result.Data)
}

func TestAliyunRejectsUnsafeOutputURLs(t *testing.T) {
	for _, host := range []string{"viapi-test.", "vibktprfx-prod-prod-aic-vc-cn-shanghai.", "ivpd-cn-shanghai."} {
		safe := "https://" + host + aliyunOSSRegion + "/o"
		canonical, ok := aliyunImageResultURL(safe)
		require.True(t, ok)
		require.Equal(t, safe, canonical)
	}
	_, ok := aliyunImageResultURL("http://viapi-test." + aliyunOSSRegion + "/o")
	require.False(t, ok)
	for _, url := range []string{"https://127.0.0.1/a", "https://other.oss-cn-shanghai.aliyuncs.com/o", "https://ivpd-cn-shanghai.oss-cn-shanghai.aliyuncs.com.evil.invalid/out", "https://user@ivpd-cn-shanghai.oss-cn-shanghai.aliyuncs.com/out", "http://ivpd-cn-shanghai.oss-cn-shanghai.aliyuncs.com:80/out", "https://ivpd-cn-shanghai.oss-cn-shanghai.aliyuncs.com/out#x"} {
		_, ok := aliyunImageResultURL(url)
		require.False(t, ok, url)
	}
}
func TestImageUpscaleFallbackSkipsAndFailsTruthfully(t *testing.T) {
	var loads, cloud int
	cfg := imageUpscaleTestConfig("https://mini.invalid")
	s := newImageUpscaleTestService(cfg, &http.Client{}, func(context.Context) ([]byte, error) { loads++; return nil, errors.New("mini credential unavailable") })
	s.cloudSlots = make(chan struct{}, 1)
	s.cloudUpscale = func(context.Context, []byte, int, func() error) (*ImageUpscaleResult, error) {
		cloud++
		return nil, aliFailure("PROCESS")
	}
	source := imageUpscaleTestPNG(t, 2048, 512)
	out, err := s.upscaleToRequestedSizeWithSourceLoader(t.Context(), "2K", imageUpscaleWorkInteractive, func(context.Context) ([]byte, error) { return source, nil })
	require.NoError(t, err)
	require.Equal(t, 1, out.Scale)
	require.NotZero(t, source[0])
	orig := []byte(`{"data":[]}`)
	outBody, err := (&OpenAIGatewayService{imageUpscaler: s}).upscaleOpenAIImagesResponse(t.Context(), nil, &OpenAIImagesRequest{Size: "1K"}, orig)
	require.NoError(t, err)
	require.Equal(t, orig, outBody)
	_, err = s.Upscale(t.Context(), []byte("invalid image"), 2)
	requireImageUpscaleError(t, err, "INVALID_SOURCE_IMAGE")
	require.Zero(t, loads)
	require.Zero(t, cloud)
	small := imageUpscaleTestPNG(t, 64, 64)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.Upscale(ctx, small, 2)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, cloud)
	task := WithAsyncImageTaskStorage(t.Context(), "task-with-paid-fallback")
	out, err = s.Upscale(task, small, 2)
	require.Nil(t, out)
	requireImageUpscaleError(t, err, "ALIYUN_PROCESS")
	require.Equal(t, 1, loads)
	require.Equal(t, 1, cloud)
	require.True(t, AsyncImageTaskReplayPrevented(task))
	require.False(t, AsyncImageTaskStorageComplete(task))
	require.False(t, AsyncImageTaskReplayPrevented(t.Context()))
	s.cloudSlots <- struct{}{}
	s.cfg.MaxQueue = 0
	require.NoError(t, s.acquire(t.Context()))
	defer s.release()
	ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = s.upscaleBase64FromLoader(ctx, 2, func(context.Context) (string, error) {
		t.Fatal("must not load source outside either admission budget")
		return "", nil
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, cloud)
	<-s.cloudSlots
}
func TestAliyunInvalidSourceOrCredentialDoesNotSend(t *testing.T) {
	s := newAliyunTestUpscaler(aliyunTestTransport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected network call"); return nil, nil }), `{"access_key_id":"missing secret"}`)
	source := imageUpscaleTestPNG(t, 32, 32)
	_, err := s.upscale(t.Context(), source, 2)
	requireImageUpscaleError(t, err, "ALIYUN_CREDENTIAL_UNAVAILABLE")
	_, err = s.upscale(t.Context(), source[:len(source)/2], 2)
	requireImageUpscaleError(t, err, "INVALID_SOURCE_IMAGE")
	require.NotContains(t, strings.ToLower(err.Error()), "fixture-secret")
}
