package service

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
)

type BatchImageWorkerRuntime struct {
	worker          *BatchImageWorker
	billingRecovery *BatchImageBillingRecoveryService
	queueRecovery   *BatchImageQueueRecoveryService
	cfg             *config.Config

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewBatchImageWorkerRuntime(worker *BatchImageWorker, cfg *config.Config) *BatchImageWorkerRuntime {
	return &BatchImageWorkerRuntime{worker: worker, cfg: cfg}
}

func ProvideBatchImageWorkerRuntime(
	repo BatchImageRepository,
	accountRepo AccountRepository,
	queue BatchImageQueue,
	billingRepo UsageBillingRepository,
	usageLogRepo UsageLogRepository,
	pricing *BatchImageModelPricingResolver,
	authCache APIKeyAuthCacheInvalidator,
	deliveryStore BatchImageDeliveryObjectStore,
	openAIGateway *OpenAIGatewayService,
	cfg *config.Config,
) *BatchImageWorkerRuntime {
	upscaler := SharedImageUpscaleService(cfg)
	upscaleStore, _ := deliveryStore.(BatchImageUpscaleObjectStore)
	highResolutionFinalizeConcurrency := batchImageHighResolutionFinalizeConcurrency(cfg)
	highResolutionFinalizeRequeue := defaultBatchImageHighResolutionFinalizeRequeue
	if cfg != nil {
		if cfg.BatchImage.HighResolutionFinalizeRequeueSeconds > 0 {
			highResolutionFinalizeRequeue = time.Duration(cfg.BatchImage.HighResolutionFinalizeRequeueSeconds) * time.Second
		}
	}
	processor := &BatchImagePipelineProcessor{
		ProviderProcessor: &BatchImageProviderProcessor{
			Repo:             repo,
			ProviderRegistry: NewBatchImageProviderRegistryWithRuntime(cfg, deliveryStore, openAIGateway),
			AccountResolver:  &BatchImageAccountRepositoryResolver{Repo: accountRepo},
			Delivery:         NewBatchImageDeliveryService(repo, deliveryStore, cfg),
			Indexer: &BatchImageResultIndexer{
				Repo:         repo,
				Config:       cfg,
				Upscaler:     upscaler,
				UpscaleStore: upscaleStore,
			},
			BillingRepo:                   billingRepo,
			AuthCache:                     authCache,
			HighResolutionFinalizer:       newBatchImageHighResolutionFinalizeSlots(highResolutionFinalizeConcurrency),
			HighResolutionFinalizeRequeue: highResolutionFinalizeRequeue,
		},
		SettlementService: &BatchImageSettlementService{
			Repo:         repo,
			BillingRepo:  billingRepo,
			UsageLogRepo: usageLogRepo,
			Pricing:      pricing,
			AuthCache:    authCache,
			Config:       cfg,
		},
	}
	workerOptions := NewBatchImageWorkerOptionsFromConfig(cfg)
	runtime := NewBatchImageWorkerRuntime(NewBatchImageWorker(queue, processor, workerOptions), cfg)
	runtime.billingRecovery = &BatchImageBillingRecoveryService{
		Repo:       repo,
		Billing:    billingRepo,
		AuthCache:  authCache,
		Queue:      queue,
		StaleAfter: workerOptions.StaleActiveAfter,
		Limit:      workerOptions.RecoverLimit,
	}
	if recoveryRepo, ok := repo.(BatchImageQueueRecoveryRepository); ok {
		if recoveryQueue, ok := queue.(BatchImageQueueEnsurer); ok {
			runtime.queueRecovery = &BatchImageQueueRecoveryService{
				Repo:    recoveryRepo,
				Queue:   recoveryQueue,
				Limit:   workerOptions.RecoverLimit,
				LockTTL: workerOptions.JobLockTTL,
			}
		}
	}
	runtime.Start()
	return runtime
}

func batchImageHighResolutionFinalizeConcurrency(cfg *config.Config) int {
	desired := 1
	workerConcurrency := 1
	if cfg != nil {
		if cfg.BatchImage.HighResolutionFinalizeConcurrency > 0 {
			desired = cfg.BatchImage.HighResolutionFinalizeConcurrency
		}
		if cfg.BatchImage.WorkerConcurrency > 0 {
			workerConcurrency = min(cfg.BatchImage.WorkerConcurrency, config.BatchImageWorkerConcurrencyMax)
		}
	}
	// Keep one worker available for provider polling, 1K jobs and settlement
	// whenever the runtime has more than one worker.
	return min(desired, max(1, workerConcurrency-1))
}

func (r *BatchImageWorkerRuntime) Start() {
	if r == nil || r.worker == nil || r.cfg == nil || !r.cfg.BatchImage.QueueEnabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.cancel = cancel
	r.done = done

	workerConcurrency := r.workerConcurrency()
	backgroundRoutines := 3
	if r.queueRecovery != nil {
		backgroundRoutines++
	}
	var wg sync.WaitGroup
	wg.Add(workerConcurrency + backgroundRoutines)
	for range workerConcurrency {
		go func() {
			defer wg.Done()
			r.worker.Run(ctx)
		}()
	}
	go func() {
		defer wg.Done()
		r.worker.RunDelayedMover(ctx)
	}()
	go func() {
		defer wg.Done()
		r.worker.RunStaleActiveRecovery(ctx)
	}()
	go func() {
		defer wg.Done()
		r.runBillingRecovery(ctx)
	}()
	if r.queueRecovery != nil {
		go func() {
			defer wg.Done()
			r.runQueueRecovery(ctx)
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()
}

func (r *BatchImageWorkerRuntime) workerConcurrency() int {
	if r == nil || r.cfg == nil || r.cfg.BatchImage.WorkerConcurrency <= 0 {
		return 1
	}
	if r.cfg.BatchImage.WorkerConcurrency > config.BatchImageWorkerConcurrencyMax {
		return config.BatchImageWorkerConcurrencyMax
	}
	return r.cfg.BatchImage.WorkerConcurrency
}

func (r *BatchImageWorkerRuntime) runBillingRecovery(ctx context.Context) {
	if r == nil || r.worker == nil || r.billingRecovery == nil {
		return
	}
	interval := r.worker.opts.RecoveryInterval
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if runtimegate.SharedWorkAllowed() {
			_, _ = r.billingRecovery.ReleaseStaleUnsubmittedOnce(ctx)
		}
		sleepOrDone(ctx, interval)
	}
}

func (r *BatchImageWorkerRuntime) runQueueRecovery(ctx context.Context) {
	if r == nil || r.worker == nil || r.queueRecovery == nil {
		return
	}
	interval := r.worker.opts.RecoveryInterval
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if runtimegate.SharedWorkAllowed() {
			_, _ = r.queueRecovery.ReconcileProviderSubmittedOnce(ctx)
		}
		sleepOrDone(ctx, interval)
	}
}

func (r *BatchImageWorkerRuntime) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	r.cancel = nil
	r.done = nil
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (r *BatchImageWorkerRuntime) Running() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancel != nil
}
