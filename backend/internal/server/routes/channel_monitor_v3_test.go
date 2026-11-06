package routes

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

type monitorV3RouteSettings struct {
	channelMonitorRouteSettingRepoStub
}

func (r *monitorV3RouteSettings) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *monitorV3RouteSettings) SetMultiple(_ context.Context, values map[string]string) error {
	for k, v := range values {
		r.values[k] = v
	}
	return nil
}
func (r *monitorV3RouteSettings) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}

type monitorV3RouteRepo struct {
	service.ChannelMonitorV3Repository
}

func (monitorV3RouteRepo) GetConfig(context.Context) (*service.ChannelMonitorV3Config, error) {
	return nil, service.ErrChannelMonitorV3NotFound
}

func TestChannelMonitorV3PublicRouteGuards(t *testing.T) {
	for _, tc := range []struct {
		name, mode       string
		enabled, backend bool
		want             int
	}{
		{"disabled", "v3", false, false, 403}, {"v2", "v2", true, false, 403},
		{"backend", "v3", true, true, 403}, {"active reaches opt-in", "v3", true, false, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &monitorV3RouteSettings{channelMonitorRouteSettingRepoStub{values: map[string]string{}}}
			settings := service.NewSettingService(repo, &config.Config{})
			require.NoError(t, settings.UpdateSettings(context.Background(), &service.SystemSettings{ChannelMonitorEnabled: tc.enabled, ChannelMonitorMode: tc.mode, BackendModeEnabled: tc.backend}))
			h := &handler.Handlers{ChannelMonitorV3: handler.NewChannelMonitorV3Handler(service.NewChannelMonitorV3Service(monitorV3RouteRepo{}, nil), nil, settings)}
			r := gin.New()
			registerChannelMonitorV3PublicRoutes(r.Group("/api/v1"), h, settings, nil)
			for _, path := range []string{"/status", "/incidents"} {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/public/channel-monitor-v3"+path, nil))
				require.Equal(t, tc.want, w.Code)
			}
		})
	}
}

func TestChannelMonitorV3PublicRouteRateLimit(t *testing.T) {
	repo := &monitorV3RouteSettings{channelMonitorRouteSettingRepoStub{values: map[string]string{}}}
	settings := service.NewSettingService(repo, &config.Config{})
	require.NoError(t, settings.UpdateSettings(context.Background(), &service.SystemSettings{ChannelMonitorEnabled: true, ChannelMonitorMode: "v3"}))
	require.NoError(t, settings.SetPanelRateLimitSettings(context.Background(), &service.PanelRateLimitSettings{Enabled: true, UserRPM: 60, HeavyRPM: 20, PublicIPRPM: 1}))
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	h := &handler.Handlers{ChannelMonitorV3: handler.NewChannelMonitorV3Handler(service.NewChannelMonitorV3Service(monitorV3RouteRepo{}, nil), nil, settings)}
	r := gin.New()
	registerChannelMonitorV3PublicRoutes(r.Group("/api/v1"), h, settings, middleware.NewPanelRateLimiter(client, settings))
	registerChannelMonitorV3UserRoutes(r.Group("/api/v1"), h, settings, nil)
	unauthorized := httptest.NewRecorder()
	r.ServeHTTP(unauthorized, httptest.NewRequest("GET", "/api/v1/channel-monitor-v3/status", nil))
	require.Equal(t, 401, unauthorized.Code)
	for _, want := range []int{404, 429} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/public/channel-monitor-v3/status", nil)
		req.RemoteAddr = "8.8.8.8:1234"
		r.ServeHTTP(w, req)
		require.Equal(t, want, w.Code)
	}
}
