//go:build unit

package service

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const hiAPIBothFramesRequest = `{"model":"seedance-2.0-mini","prompt":"A continuous transition from the red cube to the blue sphere.","resolution":"480p","duration":4,"aspect_ratio":"16:9","generate_audio":false,"first_frame_url":"https://assets.example/start.png","last_frame_url":"https://assets.example/end.png"}`

func TestHiAPIVideoRequestKeepsFrameRolesAndExactOptions(t *testing.T) {
	r, err := ParseHiAPIVideoRequest("application/json; charset=utf-8", []byte(hiAPIBothFramesRequest))
	require.NoError(t, err)
	info := r.Info()
	require.Equal(t, []string{"https://assets.example/start.png", "https://assets.example/end.png"}, info.InputImageURLs)
	require.Equal(t, 4, info.DurationSeconds)
	require.Equal(t, "480p", info.Resolution)
	encoded, err := r.marshal()
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"seedance-2.0-mini","input":{"prompt":"A continuous transition from the red cube to the blue sphere.","resolution":"480p","duration":4,"aspect_ratio":"16:9","generate_audio":false,"first_frame_url":"https://assets.example/start.png","last_frame_url":"https://assets.example/end.png"}}`, string(encoded))
	for _, tc := range []struct{ from, to string }{
		{`"first_frame_url":"https://assets.example/start.png",`, ""},
		{`"generate_audio":false,`, ""},
		{`"aspect_ratio":"16:9",`, ""},
		{`"resolution":"480p"`, `"resolution":"1080p"`},
		{`"duration":4`, `"duration":3`},
		{`"duration":4`, `"duration":16`},
		{`"seedance-2.0-mini"`, `"seedance-2.5-flash/image-to-video"`},
		{`https://assets.example/end.png`, `https://127.0.0.1/end.png`},
		{`"last_frame_url"`, `"reference_image_urls"`},
		{`"resolution":"480p"`, `"resolution":"480p","callback":{"url":"https://example.com"}`},
	} {
		_, err := ParseHiAPIVideoRequest("application/json", []byte(strings.Replace(hiAPIBothFramesRequest, tc.from, tc.to, 1)))
		require.Error(t, err, "must reject %s", tc.to)
	}
}

type hiAPIVideoReconcileStore struct{ *goodPutReconcileStore }

func (r hiAPIVideoReconcileStore) HiAPIVideoJobs() DurableVideoJobRepository {
	return r.goodPutReconcileStore
}

func TestHiAPIVideoWorkerSettlesThroughProductionBillingLanes(t *testing.T) {
	const nativeID = "tk-hiapi-01HZTQ8BX2N3GM3YFK4Z9D7VQR"
	for _, tc := range []struct{ name, detail, state string }{
		{"success", `"status":"success","output":[{"type":"video","url":"https://cdn.hiapi.ai/sample.mp4"}]`, "completed"},
		{"no result", `"status":"success","output":[{"type":"image","url":"https://cdn.hiapi.ai/missing.png"}]`, "review"},
		{"terminal failure", `"status":"fail"`, "failed"},
		{"in flight", `"status":"archiving"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := &DurableVideoJob{ID: "job", NativeID: nativeID, Model: HiAPISeedanceMiniModel, AccountID: 42, CreatedAt: time.Now()}
			store := &goodPutReconcileStore{job: job}
			lanes := NewUsageBillingLaneCombiner(hiAPIVideoReconcileStore{store})
			t.Cleanup(lanes.Stop)
			account := &Account{ID: job.AccountID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{
				"api_key": "test", "base_url": hiAPIVideoOrigin + "/v1", "model_mapping": map[string]any{HiAPISeedanceMiniModel: HiAPISeedanceMiniModel},
			}}
			calls := 0
			svc := &OpenAIGatewayService{usageBillingRepo: ProvideUsageBillingRepository(lanes), cfg: &config.Config{},
				accountRepo:        &openAIRecordUsageAccountRepoStub{account: account},
				concurrencyService: NewConcurrencyService(&stubConcurrencyCacheForTest{acquireResult: true}),
				httpUpstream: goodPutReconcileTransport{call: func(req *http.Request, accountID int64) (*http.Response, error) {
					calls++
					require.Equal(t, http.MethodGet, req.Method)
					require.Equal(t, hiAPIVideoTasksURL+"/"+nativeID, req.URL.String())
					require.Equal(t, job.AccountID, accountID)
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200,"data":{"taskId":"` + nativeID + `","model":"seedance-2.0-mini",` + tc.detail + `}}`))}, nil
				}},
			}
			require.Same(t, store, svc.hiAPIVideoJobs())
			svc.reconcileHiAPIVideo(t.Context())
			require.Equal(t, 1, calls)
			if tc.state == "" {
				require.Empty(t, store.states)
				return
			}
			svc.reconcileHiAPIVideo(t.Context())
			require.Equal(t, []string{tc.state}, store.states)
			require.Equal(t, 1, calls)
		})
	}
}

func TestHiAPIVideoTransportPostsOnceAndNeverDelegatesAccountToOtherCapabilities(t *testing.T) {
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{
		"api_key": "test", "base_url": hiAPIVideoOrigin, "model_mapping": map[string]any{HiAPISeedanceMiniModel: HiAPISeedanceMiniModel},
	}}
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityGoodPutVideo))
	count := 0
	svc := &OpenAIGatewayService{httpUpstream: goodPutReconcileTransport{call: func(req *http.Request, id int64) (*http.Response, error) {
		count++
		require.Equal(t, account.ID, id)
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, hiAPIVideoTasksURL, req.URL.String())
		require.Equal(t, "Bearer test", req.Header.Get("Authorization"))
		require.Equal(t, "test-reservation", req.Header.Get("Idempotency-Key"))
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		require.Nil(t, req.GetBody)
		return nil, io.ErrUnexpectedEOF
	}}}
	_, err := svc.sendHiAPIVideo(t.Context(), account, "", "test-reservation", []byte(`{}`))
	require.ErrorContains(t, err, "do not resubmit")
	require.Equal(t, 1, count)
}
