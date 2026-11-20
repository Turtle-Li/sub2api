//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type goodPutReconcileStore struct {
	UsageBillingBatchRepository
	DurableVideoJobRepository
	job    *DurableVideoJob
	states []string
}

func (r *goodPutReconcileStore) ClaimVideoJob(context.Context) (*DurableVideoJob, error) {
	return r.job, nil
}

func (r *goodPutReconcileStore) FinishVideoJob(_ context.Context, id, state string) (bool, error) {
	if r.job == nil || r.job.ID != id {
		return false, nil
	}
	r.states = append(r.states, state)
	r.job = nil
	return true, nil
}

type goodPutReconcileTransport struct {
	HTTPUpstream
	call func(*http.Request, int64) (*http.Response, error)
}

func (u goodPutReconcileTransport) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	return u.call(req, accountID)
}

func TestGoodPutWorkerSettlesWithoutClientRead(t *testing.T) {
	for _, tc := range []struct {
		name, response, state string
	}{
		{"success", `{"id":"video_test","status":"completed","usage":{"duration_seconds":5}}`, "completed"},
		{"failure", `{"id":"video_test","status":"failed"}`, "failed"},
		{"unexpected duration", `{"id":"video_test","status":"completed","usage":{"duration_seconds":6}}`, "review"},
		{"still pending", `{"id":"video_test","status":"in_progress"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := &DurableVideoJob{ID: "test-job", AccountID: 42, NativeID: "video_test", Seconds: 5, CreatedAt: time.Now()}
			store := &goodPutReconcileStore{job: job}
			lanes := NewUsageBillingLaneCombiner(store)
			t.Cleanup(lanes.Stop)
			account := &Account{ID: job.AccountID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{
				"api_key": "test", "base_url": goodPutVideoOrigin + "/v1", "model_mapping": map[string]any{GoodPutH3Model: GoodPutH3Model},
			}}
			calls := 0
			svc := &OpenAIGatewayService{usageBillingRepo: ProvideUsageBillingRepository(lanes), cfg: &config.Config{},
				accountRepo:        &openAIRecordUsageAccountRepoStub{account: account},
				concurrencyService: NewConcurrencyService(&stubConcurrencyCacheForTest{acquireResult: true}),
				httpUpstream: goodPutReconcileTransport{call: func(req *http.Request, accountID int64) (*http.Response, error) {
					calls++
					require.Equal(t, http.MethodGet, req.Method)
					require.Equal(t, "/v1/videos/video_test", req.URL.Path)
					require.Equal(t, job.AccountID, accountID)
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.response))}, nil
				}},
			}
			require.Same(t, store, svc.goodPutVideoJobs(), "the production billing provider must preserve the durable repository")
			svc.reconcileGoodPutVideo(t.Context())
			require.Equal(t, 1, calls)
			if tc.state == "" {
				require.Empty(t, store.states)
				return
			}
			svc.reconcileGoodPutVideo(t.Context())
			require.Equal(t, []string{tc.state}, store.states)
			require.Equal(t, 1, calls)
		})
	}
}
