//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelStatusProjectionExcludesPricesVolumesAndHiddenGroups(t *testing.T) {
	visible := []service.Group{{ID: 1, Name: "internal-0.25", Platform: "openai"}, {ID: 3, Name: "DeepSeek", Platform: "deepseek"}}
	statuses := map[int64]*service.ChannelMonitorV3GroupStatus{1: {Status: "unknown", RequestSampleState: "insufficient", DisplayName: "GPT Standard", ProbeModel: "private-check-model"}, 3: {Status: "down"}}
	out := toModelStatusGroups(visible, statuses, "zh-CN")
	require.Len(t, out, 2)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.Equal(t, "GPT Standard", out[0].Name)
	for _, forbidden := range []string{"internal-0.25", "private-check-model", "probe_model", "exclusive-a", "rate_multiplier", "pricing", "models", "account_id", "success_requests", "input_tokens"} {
		require.NotContains(t, string(raw), forbidden)
	}
	var objects []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &objects))
	require.Len(t, objects[0], 4)
	require.Contains(t, objects[0], "availability")
}

func TestModelStatusHandlerMissingDependenciesFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/model-status", nil)
	(&ModelPlazaHandler{}).GetStatus(c)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}
