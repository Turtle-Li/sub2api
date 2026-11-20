//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
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

type modelStatusCatalogStub struct {
	service.GroupRepository
	groups []service.Group
}

func (s modelStatusCatalogStub) ListActive(context.Context) ([]service.Group, error) {
	return s.groups, nil
}

type modelStatusFaultRepo struct {
	monitorV3HandlerRepo
	through time.Time
}

func (r *modelStatusFaultRepo) GetConfig(ctx context.Context) (*service.ChannelMonitorV3Config, error) {
	cfg, err := r.monitorV3HandlerRepo.GetConfig(ctx)
	cfg.GroupProbe.Groups = []service.ChannelMonitorV3GroupProbeTarget{{GroupID: 1, Enabled: true}, {GroupID: 2, Enabled: true}, {GroupID: 3, Enabled: true}}
	return cfg, err
}

func (r *modelStatusFaultRepo) DataThrough(context.Context) (*time.Time, error) {
	return &r.through, nil
}

func TestModelStatusHandlerScoresAuthorizedGroupsByCatalogPlatform(t *testing.T) {
	now := time.Now().UTC()
	repo := &modelStatusFaultRepo{through: now}
	repo.publicEnabled = true
	groups := []service.Group{
		{ID: 1, Name: "按量", Platform: service.PlatformOpenAI, Status: service.StatusActive},
		{ID: 2, Name: "GPT display alias", Platform: service.PlatformAnthropic, Status: service.StatusActive},
		{ID: 3, Name: "private", Platform: service.PlatformOpenAI, Status: service.StatusActive, IsExclusive: true},
	}
	for _, g := range groups {
		repo.facts.Metrics = append(repo.facts.Metrics, service.ChannelMonitorV3Fact{GroupID: g.ID, Slot: now.Truncate(10 * time.Minute), Success: 20, Errors: 5, OpenAIFaults: 0})
	}
	settings := &settingHandlerPublicRepoStub{values: map[string]string{service.SettingKeyChannelMonitorEnabled: "true", service.SettingKeyChannelMonitorMode: service.ChannelMonitorModeV3}}
	h := NewModelPlazaHandler(nil, nil, service.NewSettingService(settings, &config.Config{}), service.NewChannelMonitorV3Service(repo, modelStatusCatalogStub{groups: groups}))
	router := gin.New()
	router.GET("/api/v1/model-status", h.GetStatus)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/model-status", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var body struct{ Data modelStatusResponse }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data.Groups, 2)
	rates := map[int64]float64{}
	for _, g := range body.Data.Groups {
		require.NotNil(t, g.Availability.RequestSuccessRate)
		rates[g.ID] = *g.Availability.RequestSuccessRate
	}
	require.Equal(t, map[int64]float64{1: 100, 2: 80}, rates)
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
