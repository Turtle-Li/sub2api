package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestProxyHandlerListPoolFilter(t *testing.T) {
	router, adminSvc := setupAdminRouter()

	cases := map[string]int64{"": 0, "&pool=none": service.ProxyListPoolNone, "&pool=5": 5}
	for query, want := range cases {
		adminSvc.lastListProxies.poolID = 999
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/proxies?page=1&page_size=20"+query, nil))
		require.Equal(t, http.StatusOK, rec.Code, query)
		require.Equal(t, want, adminSvc.lastListProxies.poolID, query)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/proxies?pool=abc", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
