package server

import (
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
)

// TrafficAdmission uses the existing operator-owned traffic-state file. A
// draining node rejects new work while requests already admitted can finish.
// This is an HTTP fence only: operators must also stop/drain background writers
// before a database denomination change. Health probes remain reachable.
func (s *HealthService) TrafficAdmission() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s == nil || c.Request.URL.Path == "/health" || c.Request.URL.Path == "/internal/livez" || c.Request.URL.Path == "/internal/readyz" {
			c.Next()
			return
		}
		// Count before checking admission: a request that raced with draining
		// cannot be admitted after an operator observes zero in-flight work.
		s.inFlightRequests.Add(1)
		defer s.inFlightRequests.Add(-1)
		if !s.acceptingTraffic() && !s.authorizedLocalCanary(c.Request) {
			c.Header("Retry-After", "30")
			c.Header("Cache-Control", "no-store")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "temporarily_unavailable", "message": "Service maintenance in progress. Please retry shortly."}})
			return
		}
		c.Next()
	}
}

func (s *HealthService) InFlightRequests() int64 {
	if s == nil {
		return 0
	}
	return s.inFlightRequests.Load()
}

// A monitor-authenticated loopback request can exercise the normal API/auth and
// billing handlers before public admission reopens. Forwarded IP headers are
// deliberately ignored; a proxy connection is not a local canary.
func (s *HealthService) authorizedLocalCanary(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return false
	}
	return s.Authorized(r.Header.Get("X-Monitor-Token"))
}
