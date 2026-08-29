package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCommonRoutesUseCanonicalSiteRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	guard := servermiddleware.NewSiteDomainGuard()
	require.NoError(t, guard.Set("ai.example.com"))

	router := gin.New()
	RegisterCommonRoutes(router, guard.RedirectHost())

	healthRequest := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRequest.Host = "other.example.com"
	healthResponse := httptest.NewRecorder()
	router.ServeHTTP(healthResponse, healthRequest)
	require.Equal(t, http.StatusOK, healthResponse.Code)
	require.Empty(t, healthResponse.Header().Get("Location"))

	setupRequest := httptest.NewRequest(http.MethodGet, "/setup/status", nil)
	setupRequest.Host = "other.example.com"
	setupResponse := httptest.NewRecorder()
	router.ServeHTTP(setupResponse, setupRequest)
	require.Equal(t, http.StatusTemporaryRedirect, setupResponse.Code)
	require.Equal(t, "//ai.example.com/setup/status", setupResponse.Header().Get("Location"))

	request := httptest.NewRequest(http.MethodPost, "/api/event_logging/batch", nil)
	request.Host = "other.example.com"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
}
