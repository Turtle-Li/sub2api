package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestHiAPIVideoDispatchRequiresExactExclusiveGroup(t *testing.T) {
	for _, exclusive := range []bool{true, false} {
		group := allowlistGroup(service.PlatformOpenAI, true, service.HiAPISeedanceMiniModel)
		group.IsExclusive = exclusive
		router := newGatewayRoutesTestRouterWithGroup(group)
		for _, path := range []string{"/v1/videos", "/v1/videos/generations"} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"seedance-2.0-mini","prompt":"missing required input fields"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if exclusive {
				require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), "duration must be")
			} else {
				require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), "hiapi_video_group_not_enabled")
			}
		}
	}
}
