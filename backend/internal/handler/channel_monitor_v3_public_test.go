package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChannelMonitorV3AnonymousRoutesFailClosedAndPublishOnlyPublic(t *testing.T) {
	cat := int64(1)
	repo := &monitorV3HandlerRepo{components: []service.ChannelMonitorV3Component{
		{ID: 1, CategoryID: &cat, Name: "Published", Enabled: true, GroupID: 4, GroupStatus: service.StatusActive, Visibility: service.ChannelMonitorV3VisibilityPublic, ShowMultiplier: true, GroupRateMultiplier: 0.3},
		{ID: 2, CategoryID: &cat, Name: "PrivateName", Enabled: true, GroupID: 9, GroupStatus: service.StatusActive, Visibility: service.ChannelMonitorV3VisibilityGroup},
	}}
	h := &ChannelMonitorV3Handler{service: service.NewChannelMonitorV3Service(repo, nil)}
	r := gin.New()
	r.GET("/status", h.PublicStatus)
	r.GET("/incidents", h.PublicIncidents)
	for _, path := range []string{"/status", "/incidents"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, 404, w.Code)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
	repo.publicEnabled = true
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "Published")
	for _, value := range []string{"PrivateName", "\"group_id\"", "\"multiplier\"", "\"requests\"", "\"errors\""} {
		require.NotContains(t, w.Body.String(), value)
	}
}
