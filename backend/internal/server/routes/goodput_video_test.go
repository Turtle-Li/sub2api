package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGoodPutVideoDispatchIsExclusive(t *testing.T) {
	for _, tc := range []struct {
		name   string
		group  *service.Group
		status int
	}{
		{"exclusive H3", allowlistGroup(service.PlatformOpenAI, true, service.GoodPutH3FastModel), http.StatusBadRequest},
		{"nonexclusive H3", allowlistGroup(service.PlatformOpenAI, true, service.GoodPutH3FastModel), http.StatusNotFound},
		{"mixed group", allowlistGroup(service.PlatformOpenAI, true, service.GoodPutH3FastModel, service.MuseVideoModel), http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.group.IsExclusive = tc.name != "nonexclusive H3"
			router := newGatewayRoutesTestRouterWithGroup(tc.group)
			for _, path := range []string{"/v1/videos", "/videos"} {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"minimax/h3-fast","prompt":""}`))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				require.Equal(t, tc.status, w.Code, w.Body.String())
			}
		})
	}
}
