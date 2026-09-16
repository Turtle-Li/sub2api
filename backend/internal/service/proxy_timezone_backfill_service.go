package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
)

const (
	proxyTimezoneBackfillConcurrency    = 3
	proxyTimezoneProbeTimeout           = 20 * time.Second
	proxyTimezoneBackfillActivationPoll = time.Second
)

// ProxyTimezoneBackfillService performs one bounded startup pass over active
// proxies whose exit timezone has not been detected yet. New and edited
// proxies are still handled by the admin proxy lifecycle; this service exists
// so proxies created before the timezone columns were introduced do not need a
// manual test click after deployment.
type ProxyTimezoneBackfillService struct {
	proxyRepo              ProxyRepository
	writer                 ProxyDetectedTimezoneRepository
	prober                 ProxyExitInfoProber
	leaderLock             *singletonJobLock
	stopCh                 chan struct{}
	startOnce              sync.Once
	stopOnce               sync.Once
	wg                     sync.WaitGroup
	activationPollInterval time.Duration
}

type proxyTimezoneBackfillStats struct {
	Candidates int
	Updated    int
	Failed     int
	Stale      int
}

type proxyTimezoneBackfillResult struct {
	proxyID int64
	updated bool
	stale   bool
	err     error
}

func NewProxyTimezoneBackfillService(proxyRepo ProxyRepository, prober ProxyExitInfoProber) *ProxyTimezoneBackfillService {
	writer, _ := proxyRepo.(ProxyDetectedTimezoneRepository)
	return &ProxyTimezoneBackfillService{
		proxyRepo:              proxyRepo,
		writer:                 writer,
		prober:                 prober,
		stopCh:                 make(chan struct{}),
		activationPollInterval: proxyTimezoneBackfillActivationPoll,
	}
}

// Start launches a single asynchronous pass. The pass is deliberately
// detached from HTTP startup and is cancellable through Stop. A standby
// generation waits for activation before making its one lock attempt. The
// winner keeps the lease until Stop so peers that start slightly later cannot
// repeat probes that the first pass intentionally left unresolved.
func (s *ProxyTimezoneBackfillService) Start() {
	if s == nil || s.proxyRepo == nil || s.writer == nil || s.prober == nil {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				select {
				case <-s.stopCh:
					cancel()
				case <-ctx.Done():
				}
			}()

			interval := s.activationPollInterval
			if interval <= 0 {
				interval = proxyTimezoneBackfillActivationPoll
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				if runtimegate.SharedWorkAllowed() {
					stats, leaseCtx, release, acquired, err := s.runOnceHoldingLease(ctx)
					if err != nil {
						if release != nil {
							release()
						}
						log.Printf("[ProxyTimezoneBackfill] startup pass failed: %v", err)
						return
					}
					if !acquired {
						return
					}
					defer release()
					log.Printf(
						"[ProxyTimezoneBackfill] startup pass complete: candidates=%d updated=%d failed=%d stale=%d",
						stats.Candidates,
						stats.Updated,
						stats.Failed,
						stats.Stale,
					)
					select {
					case <-ctx.Done():
					case <-leaseCtx.Done():
					}
					return
				}

				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

func (s *ProxyTimezoneBackfillService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *ProxyTimezoneBackfillService) runOnce(ctx context.Context) (proxyTimezoneBackfillStats, bool, error) {
	stats, _, release, acquired, err := s.runOnceHoldingLease(ctx)
	if release != nil {
		release()
	}
	return stats, acquired, err
}

func (s *ProxyTimezoneBackfillService) runOnceHoldingLease(ctx context.Context) (proxyTimezoneBackfillStats, context.Context, func(), bool, error) {
	var stats proxyTimezoneBackfillStats
	if s == nil || s.proxyRepo == nil || s.writer == nil || s.prober == nil {
		return stats, ctx, nil, false, nil
	}

	leaseCtx, release, acquired := s.leaderLock.try(ctx)
	if !acquired {
		return stats, ctx, nil, false, nil
	}

	proxies, err := s.proxyRepo.ListActive(leaseCtx)
	if err != nil {
		return stats, leaseCtx, release, true, fmt.Errorf("list active proxies: %w", err)
	}
	candidates := make([]Proxy, 0, len(proxies))
	for i := range proxies {
		if _, err := normalizeOpenAIRequestTimezone(proxies[i].DetectedTimezone); err == nil {
			continue
		}
		candidates = append(candidates, proxies[i])
	}
	stats.Candidates = len(candidates)
	if len(candidates) == 0 {
		return stats, leaseCtx, release, true, nil
	}

	workerCount := proxyTimezoneBackfillConcurrency
	if workerCount > len(candidates) {
		workerCount = len(candidates)
	}
	jobs := make(chan Proxy)
	results := make(chan proxyTimezoneBackfillResult, len(candidates))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for proxy := range jobs {
				results <- s.probeAndPersist(leaseCtx, &proxy)
			}
		}()
	}

	go func() {
		defer close(jobs)
		for i := range candidates {
			select {
			case jobs <- candidates[i]:
			case <-leaseCtx.Done():
				return
			}
		}
	}()
	workers.Wait()
	close(results)

	for result := range results {
		switch {
		case result.updated:
			stats.Updated++
		case result.stale:
			stats.Stale++
		default:
			stats.Failed++
			if result.err != nil {
				log.Printf("[ProxyTimezoneBackfill] proxy %d skipped: %v", result.proxyID, result.err)
			}
		}
	}
	return stats, leaseCtx, release, true, nil
}

func (s *ProxyTimezoneBackfillService) probeAndPersist(ctx context.Context, proxy *Proxy) proxyTimezoneBackfillResult {
	result := proxyTimezoneBackfillResult{proxyID: proxy.ID}
	probeCtx, cancel := context.WithTimeout(ctx, proxyTimezoneProbeTimeout)
	defer cancel()

	exitInfo, _, err := s.prober.ProbeProxy(probeCtx, proxy.URL())
	if err != nil {
		result.err = fmt.Errorf("probe exit: %w", err)
		return result
	}
	if exitInfo == nil {
		result.err = fmt.Errorf("probe returned no exit information")
		return result
	}
	timezone, err := normalizeOpenAIRequestTimezone(exitInfo.Timezone)
	if err != nil {
		result.err = fmt.Errorf("probe returned unusable timezone: %w", err)
		return result
	}
	accepted, err := s.writer.UpdateDetectedTimezone(probeCtx, proxy, timezone, time.Now().UTC())
	if err != nil {
		result.err = fmt.Errorf("persist detected timezone: %w", err)
		return result
	}
	if !accepted {
		result.stale = true
		return result
	}
	result.updated = true
	return result
}
