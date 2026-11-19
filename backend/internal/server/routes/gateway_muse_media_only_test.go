package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGatewayRoutesMuseBlocksTextBeforeDispatch(t *testing.T) {
	router := newGatewayRoutesTestRouterWithGroup(allowlistGroup(service.PlatformOpenAI, true,
		"muse-spark", service.MuseImageModel, service.MuseVideoModel))
	for _, path := range []string{
		"/v1/chat/completions", "/chat/completions", "/v1/messages", "/v1/messages/count_tokens", "/messages/count_tokens",
		"/v1/responses", "/responses", "/v1/responses/compact", "/responses/compact",
		"/backend-api/codex/responses", "/backend-api/codex/responses/compact",
		"/v1/embeddings", "/embeddings", "/v1/alpha/search", "/v1/live", "/backend-api/codex/realtime/calls",
	} {
		t.Run(path, func(t *testing.T) {
			// An allowed media model is not permission to call a text endpoint.
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"muse-image","input":"sample"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "Muse is available only through the image and video APIs")
		})
	}
	for _, path := range []string{"/v1/responses", "/responses", "/backend-api/codex/responses"} {
		t.Run("websocket"+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "Muse is available only through the image and video APIs")
			require.Empty(t, w.Header().Get("Upgrade"))
		})
	}
	for _, path := range []string{"/v1/models?client_version=1", "/models?client_version=1", "/backend-api/codex/models"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNotFound, w.Code, "%s: %s", path, w.Body.String())
	}
}

func TestGatewayRoutesMuseKeepsMediaRoutes(t *testing.T) {
	router := newGatewayRoutesTestRouterWithGroup(allowlistGroup(service.PlatformOpenAI, true,
		service.MuseImageModel, service.MuseVideoModel))
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/v1/images/generations", `{"model":"muse-image","prompt":"clouds"}`},
		{http.MethodPost, "/images/edits", `{"model":"muse-image","prompt":"clouds"}`},
		{http.MethodPost, "/v1/images/generations/async", `{"model":"muse-image","prompt":"clouds"}`},
		{http.MethodPost, "/images/edits/async", `{"model":"muse-image","prompt":"clouds"}`},
		{http.MethodPost, "/v1/images/tasks", `{"model":"muse-image","prompt":"clouds"}`},
		{http.MethodPost, "/v1/videos", `{"model":"muse-video","prompt":"clouds"}`},
		{http.MethodPost, "/videos/generations", `{"model":"muse-video","prompt":"clouds"}`},
		{http.MethodGet, "/v1/images/tasks/muse_image_task_0123456789abcdef0123", ""},
		{http.MethodGet, "/images/tasks/muse_image_task_0123456789abcdef0123/content", ""},
		{http.MethodGet, "/v1/videos/muse_task_0123456789abcdef0123", ""},
		{http.MethodGet, "/videos/generations/muse_task_0123456789abcdef0123/content", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			// The fixture installs the key/group but not a user. This response is
			// from the native media handler, after the route admission check.
			require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "User context not found")
		})
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/images/batches"},
		{http.MethodGet, "/v1/images/batches/models"},
		{http.MethodGet, "/v1/images/batches/1/items/first/content"},
		{http.MethodGet, "/v1/images/batches/1/download"},
		{http.MethodPost, "/v1/images/batches/1/cancel"},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		require.Equal(t, http.StatusUnauthorized, w.Code, "%s: %s", tc.path, w.Body.String())
	}
}

func TestGatewayRoutesMuseRejectsRetiredModelEvenOnMediaRoutes(t *testing.T) {
	router := newGatewayRoutesTestRouterWithGroup(allowlistGroup(service.PlatformOpenAI, true,
		"muse-spark", service.MuseImageModel, service.MuseVideoModel))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"muse-spark","prompt":"clouds"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "model_not_found")
}

func TestGatewayRoutesMusePolicyDoesNotBlockUnrelatedText(t *testing.T) {
	for _, group := range []*service.Group{
		allowlistGroup(service.PlatformOpenAI, true, "gpt-test"),
		allowlistGroup(service.PlatformOpenAI, true, "muse-spark-1.3-contributor"),
		allowlistGroup(service.PlatformOpenAI, true, "gpt-test", service.MuseImageModel),
	} {
		router := newGatewayRoutesTestRouterWithGroup(group)
		w := httptest.NewRecorder()
		body := `{"model":"` + group.ModelAllowlist.Models[0] + `","messages":[{"role":"user","content":"hello"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "User context not found")
	}
}
