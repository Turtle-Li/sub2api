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
	all := plazaGroups()
	all[0].Models = []service.PlazaModel{{Name: "private-price-model"}}
	statuses := map[int64]*service.ChannelMonitorV3GroupStatus{1: {Status: "unknown", RequestSampleState: "insufficient"}, 2: {Status: "down"}}
	visible := filterPlazaVisibleGroups(all, nil, false)
	out := toModelStatusGroups(visible, statuses, "zh-CN")
	require.Len(t, out, 2)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	for _, forbidden := range []string{"private-price-model", "exclusive-a", "rate_multiplier", "pricing", "models", "account_id", "success_requests", "input_tokens"} {
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
