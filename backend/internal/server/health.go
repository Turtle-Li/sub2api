package server

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	internalHealthTokenFileEnv = "SUB2API_INTERNAL_HEALTH_TOKEN_FILE"
	trafficStateFileEnv        = "SUB2API_TRAFFIC_STATE_FILE"
	defaultDependencyTimeout   = 2 * time.Second
	maxInternalHealthTokenSize = 4096
	trafficStateAccepting      = "accepting"
)

// HealthService owns process-local traffic admission and dependency probes for
// the authenticated internal health contract. It never calls model providers.
type HealthService struct {
	db                *sql.DB
	redis             *redis.Client
	tokenFile         string
	trafficStateFile  string
	dependencyTimeout time.Duration
	processAccepting  atomic.Bool
}

// ProvideHealthService wires the already-existing PostgreSQL and Redis pools
// into health checks. Token and traffic files are injected by deployment.
func ProvideHealthService(db *sql.DB, redisClient *redis.Client) *HealthService {
	service := newHealthService(
		db,
		redisClient,
		strings.TrimSpace(os.Getenv(internalHealthTokenFileEnv)),
		strings.TrimSpace(os.Getenv(trafficStateFileEnv)),
		defaultDependencyTimeout,
	)
	service.processAccepting.Store(true)
	return service
}

func newHealthService(
	db *sql.DB,
	redisClient *redis.Client,
	tokenFile string,
	trafficStateFile string,
	dependencyTimeout time.Duration,
) *HealthService {
	service := &HealthService{
		db:                db,
		redis:             redisClient,
		tokenFile:         tokenFile,
		trafficStateFile:  trafficStateFile,
		dependencyTimeout: dependencyTimeout,
	}
	service.processAccepting.Store(true)
	return service
}

// SetAccepting changes only this process generation. A configured deployment
// traffic-state file is an additional fail-closed gate.
func (s *HealthService) SetAccepting(accepting bool) {
	if s == nil {
		return
	}
	s.processAccepting.Store(accepting)
}

// Authorized validates the dedicated monitor header against a root-injected
// file. Missing, empty, symlinked, non-regular, or group/world-readable files
// fail closed. The token is read on each probe so rotation needs no app restart.
func (s *HealthService) Authorized(provided string) bool {
	if s == nil || provided == "" || s.tokenFile == "" {
		return false
	}
	info, err := os.Lstat(s.tokenFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > maxInternalHealthTokenSize {
		return false
	}
	data, err := os.ReadFile(s.tokenFile)
	if err != nil {
		return false
	}
	expected := strings.TrimSpace(string(data))
	if expected == "" || len(expected) > maxInternalHealthTokenSize || len(expected) != len(provided) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

// Live reports only process liveness. Draining and dependency failures do not
// make a running process dead.
func (s *HealthService) Live() bool {
	return s != nil
}

// Ready requires local traffic admission plus both shared dependencies. Each
// dependency uses the same bounded context and the result exposes no details.
func (s *HealthService) Ready(ctx context.Context) bool {
	if s == nil || !s.acceptingTraffic() || s.db == nil || s.redis == nil {
		return false
	}
	timeout := s.dependencyTimeout
	if timeout <= 0 {
		timeout = defaultDependencyTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	results := make(chan error, 2)
	go func() { results <- s.db.PingContext(probeCtx) }()
	go func() { results <- s.redis.Ping(probeCtx).Err() }()
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				return false
			}
		case <-probeCtx.Done():
			return false
		}
	}
	return true
}

func (s *HealthService) acceptingTraffic() bool {
	if !s.processAccepting.Load() {
		return false
	}
	if s.trafficStateFile == "" {
		// Backward compatible until deployment opts into the explicit node-state
		// file. Once configured, missing or invalid content fails closed.
		return true
	}
	info, err := os.Lstat(s.trafficStateFile)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64 {
		return false
	}
	state, err := os.ReadFile(s.trafficStateFile)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(state)) == trafficStateAccepting
}
