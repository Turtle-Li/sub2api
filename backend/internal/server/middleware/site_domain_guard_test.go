package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSiteDomainGuardRequireHost(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		siteDomain string
		host       string
		wantStatus int
	}{
		{name: "disabled keeps legacy behavior", host: "legacy.example.com", wantStatus: http.StatusOK},
		{name: "canonical host passes", siteDomain: "ai.example.com", host: "ai.example.com", wantStatus: http.StatusOK},
		{name: "host comparison ignores case", siteDomain: "AI.EXAMPLE.COM", host: "ai.example.com", wantStatus: http.StatusOK},
		{name: "explicit port matches", siteDomain: "ai.example.com:8443", host: "ai.example.com:8443", wantStatus: http.StatusOK},
		{name: "other host is forbidden", siteDomain: "ai.example.com", host: "other.example.com", wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard := NewSiteDomainGuard()
			require.NoError(t, guard.Set(tt.siteDomain))
			router := gin.New()
			router.Use(guard.RequireHost())
			panel := router.Group("/api/v1")
			panel.GET("/profile", func(c *gin.Context) { c.Status(http.StatusOK) })

			req := httptest.NewRequest(http.MethodGet, "/api/v1/profile", nil)
			req.Host = tt.host
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			require.Equal(t, tt.wantStatus, response.Code)
		})
	}
}

func TestSiteDomainGuardOnlyInterceptsPanelAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	guard := NewSiteDomainGuard()
	require.NoError(t, guard.Set("ai.example.com"))
	router := gin.New()
	router.Use(guard.RequireHost())
	router.POST("/chat/completions", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	gatewayRequest := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	gatewayRequest.Host = "other.example.com"
	gatewayResponse := httptest.NewRecorder()
	router.ServeHTTP(gatewayResponse, gatewayRequest)
	require.Equal(t, http.StatusNoContent, gatewayResponse.Code)

	panelRequest := httptest.NewRequest(http.MethodGet, "/api/v1/not-registered", nil)
	panelRequest.Host = "other.example.com"
	panelResponse := httptest.NewRecorder()
	router.ServeHTTP(panelResponse, panelRequest)
	require.Equal(t, http.StatusForbidden, panelResponse.Code)
}

func TestSiteDomainGuardRedirectPage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	guard := NewSiteDomainGuard()
	disabledContext, disabledResponse := testSiteDomainGuardContext("other.example.com", "/dashboard")
	require.False(t, guard.RedirectPage(disabledContext))
	require.Equal(t, http.StatusOK, disabledResponse.Code)

	require.NoError(t, guard.Set("ai.example.com"))

	context, response := testSiteDomainGuardContext("other.example.com", "/dashboard?tab=keys")
	require.True(t, guard.RedirectPage(context))
	require.Equal(t, http.StatusTemporaryRedirect, response.Code)
	require.Equal(t, "//ai.example.com/dashboard?tab=keys", response.Header().Get("Location"))

	context, response = testSiteDomainGuardContext("ai.example.com", "/dashboard")
	require.False(t, guard.RedirectPage(context))
	require.Equal(t, http.StatusOK, response.Code)
}

func TestSiteDomainGuardRefresh(t *testing.T) {
	guard := NewSiteDomainGuard()
	require.NoError(t, guard.Set("old.example.com"))
	require.NoError(t, guard.Set("new.example.com"))
	context, response := testSiteDomainGuardContext("old.example.com", "/")

	require.True(t, guard.RedirectPage(context))
	require.Equal(t, "//new.example.com/", response.Header().Get("Location"))
}

func testSiteDomainGuardContext(host, target string) (*gin.Context, *httptest.ResponseRecorder) {
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodGet, target, nil)
	context.Request.Host = host
	return context, response
}
