package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fakeCodexDegradationProbeStore struct {
	created   []service.CodexDegradationProbeRequest
	createErr error
	deleted   []int64
}

func (f *fakeCodexDegradationProbeStore) Create(_ context.Context, req service.CodexDegradationProbeRequest) ([]service.CodexDegradationProbeResult, error) {
	f.created = append(f.created, req)
	if f.createErr != nil {
		return nil, f.createErr
	}
	return []service.CodexDegradationProbeResult{{ID: 7, AccountID: req.AccountIDs[0], Path: service.CodexDegradationProbePathTurnState, Status: service.CodexDegradationProbeStatusQueued}}, nil
}

func (f *fakeCodexDegradationProbeStore) List(context.Context) ([]service.CodexDegradationProbeResult, error) {
	return []service.CodexDegradationProbeResult{{ID: 7, Path: service.CodexDegradationProbePathTurnState}}, nil
}

func (f *fakeCodexDegradationProbeStore) Get(_ context.Context, id int64) (*service.CodexDegradationProbeResult, error) {
	if id != 7 {
		return nil, service.ErrCodexDegradationProbeNotFound
	}
	return &service.CodexDegradationProbeResult{ID: 7, Content: "full"}, nil
}

func (f *fakeCodexDegradationProbeStore) Delete(_ context.Context, id int64) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeCodexDegradationProbeStore) DeleteAll(context.Context) (int64, error) {
	return 3, nil
}

func newCodexDegradationProbeTestRouter(store codexDegradationProbeStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &CodexDegradationProbeHandler{probes: store}
	r := gin.New()
	g := r.Group("/codex-degradation-probes")
	g.GET("", h.List)
	g.POST("", h.Create)
	g.DELETE("", h.DeleteAll)
	g.GET("/:id", h.Get)
	g.DELETE("/:id", h.Delete)
	return r
}

type codexDegradationProbeEnvelope struct {
	Code   int             `json:"code"`
	Reason string          `json:"reason"`
	Data   json.RawMessage `json:"data"`
}

func doCodexDegradationProbeRequest(t *testing.T, r *gin.Engine, method, path, body string) (int, codexDegradationProbeEnvelope) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var env codexDegradationProbeEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
	return rec.Code, env
}

func TestCodexDegradationProbeHandler(t *testing.T) {
	store := &fakeCodexDegradationProbeStore{}
	r := newCodexDegradationProbeTestRouter(store)

	status, env := doCodexDegradationProbeRequest(t, r, http.MethodPost, "/codex-degradation-probes",
		`{"account_ids":[5],"model":"gpt-5.6-sol","effort":"high","prompt":"hi"}`)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, store.created, 1)
	require.Equal(t, service.CodexDegradationProbeRequest{AccountIDs: []int64{5}, Model: "gpt-5.6-sol", Effort: "high", Prompt: "hi"}, store.created[0])
	var created []map[string]any
	require.NoError(t, json.Unmarshal(env.Data, &created))
	require.Equal(t, "turn_state", created[0]["path"])

	status, _ = doCodexDegradationProbeRequest(t, r, http.MethodPost, "/codex-degradation-probes", `{"account_ids":"x"}`)
	require.Equal(t, http.StatusBadRequest, status)

	store.createErr = infraerrors.BadRequest("CODEX_DEGRADATION_PROBE_NO_PINNED_STATE", "account 5 has no active pinned turn-state")
	status, env = doCodexDegradationProbeRequest(t, r, http.MethodPost, "/codex-degradation-probes",
		`{"account_ids":[5],"model":"gpt-5.6-sol","prompt":"hi"}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "CODEX_DEGRADATION_PROBE_NO_PINNED_STATE", env.Reason)

	status, _ = doCodexDegradationProbeRequest(t, r, http.MethodGet, "/codex-degradation-probes", "")
	require.Equal(t, http.StatusOK, status)

	status, env = doCodexDegradationProbeRequest(t, r, http.MethodGet, "/codex-degradation-probes/7", "")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, string(env.Data), `"content":"full"`)

	status, env = doCodexDegradationProbeRequest(t, r, http.MethodGet, "/codex-degradation-probes/8", "")
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "CODEX_DEGRADATION_PROBE_NOT_FOUND", env.Reason)

	status, _ = doCodexDegradationProbeRequest(t, r, http.MethodGet, "/codex-degradation-probes/0", "")
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = doCodexDegradationProbeRequest(t, r, http.MethodDelete, "/codex-degradation-probes/7", "")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []int64{7}, store.deleted)

	status, env = doCodexDegradationProbeRequest(t, r, http.MethodDelete, "/codex-degradation-probes", "")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"deleted":3}`, string(env.Data))
}
