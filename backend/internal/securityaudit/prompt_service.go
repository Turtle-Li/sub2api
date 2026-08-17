package securityaudit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type PromptService struct {
	config    ConfigStore
	repo      *PostgreSQLRepository
	payload   *RedisPayloadStore
	enqueuer  *Enqueuer
	runner    *Runner
	evaluator *GuardEvaluator
	scanner   PromptScanner
	metrics   *AtomicMetrics
	clock     Clock
	circuit   *GuardCircuit
	shared    *sharedGuardCircuit

	lifecycleMu  sync.Mutex
	cancel       context.CancelFunc
	background   context.Context
	enqueueWG    sync.WaitGroup
	circuitWG    sync.WaitGroup
	enqueueSlots chan struct{}
	probeMu      sync.RWMutex
	probes       map[string]ProbeResult
}

func NewPromptService(
	config ConfigStore,
	repo *PostgreSQLRepository,
	payload *RedisPayloadStore,
	scanner PromptScanner,
	metrics *AtomicMetrics,
) *PromptService {
	clock := realClock{}
	circuit := newGuardCircuit(clock)
	var shared *sharedGuardCircuit
	if payload != nil {
		shared = newSharedGuardCircuit(newRedisSharedGuardCircuitStore(payload.client), clock)
	}
	enqueuer := NewEnqueuer(config, repo, payload, metrics)
	evaluator := newGuardEvaluatorWithCircuits(scanner, repo, metrics, 64, 16, circuit, shared)
	runner := newRunnerWithCircuits(config, repo, payload, scanner, metrics, circuit, shared)
	return &PromptService{
		config: config, repo: repo, payload: payload, scanner: scanner, metrics: metrics,
		enqueuer: enqueuer, evaluator: evaluator, runner: runner, clock: clock, circuit: circuit, shared: shared,
		enqueueSlots: make(chan struct{}, 128), probes: map[string]ProbeResult{},
	}
}

func (s *PromptService) Start(ctx context.Context) error {
	if s == nil || s.config == nil || s.runner == nil {
		return errors.New("prompt audit service unavailable")
	}
	s.lifecycleMu.Lock()
	if s.cancel != nil {
		s.lifecycleMu.Unlock()
		return nil
	}
	background, cancel := context.WithCancel(ctx)
	s.background, s.cancel = background, cancel
	s.lifecycleMu.Unlock()
	configErr := s.config.Start(background)
	workerErr := s.runner.Start(background)
	if (s.circuit != nil || s.shared != nil) && s.scanner != nil {
		s.circuitWG.Add(1)
		go s.circuitProbeLoop(background)
	}
	return errors.Join(configErr, workerErr)
}

func (s *PromptService) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.lifecycleMu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	var workerErr error
	if s.runner != nil {
		workerErr = s.runner.Shutdown(ctx)
	}
	done := make(chan struct{})
	go func() {
		s.enqueueWG.Wait()
		s.circuitWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		if workerErr == nil {
			workerErr = ctx.Err()
		}
	}
	var configErr error
	if s.config != nil {
		configErr = s.config.Shutdown(ctx)
	}
	if workerErr != nil {
		return workerErr
	}
	return configErr
}

func (s *PromptService) EffectiveMode() Mode {
	if s == nil || s.config == nil {
		return ModeOff
	}
	return s.config.EffectiveMode()
}

func (s *PromptService) Enqueue(_ context.Context, req Request) error {
	if s == nil || s.enqueuer == nil || s.EffectiveMode() != ModeAsync {
		return nil
	}
	select {
	case s.enqueueSlots <- struct{}{}:
	default:
		if s.metrics != nil {
			s.metrics.IncDropped()
		}
		LogWarn(EventEnqueueDropped, map[string]any{"request_id": req.RequestID, "status": "dropped", "error_code": "local_enqueue_busy"})
		return nil
	}
	s.lifecycleMu.Lock()
	background := s.background
	s.lifecycleMu.Unlock()
	if background == nil {
		<-s.enqueueSlots
		return errors.New("prompt audit service not started")
	}
	requestCopy := req.Clone()
	s.enqueueWG.Add(1)
	go func() {
		defer s.enqueueWG.Done()
		defer func() { <-s.enqueueSlots }()
		ctx, cancel := context.WithTimeout(background, 2*time.Second)
		defer cancel()
		_ = s.enqueuer.Enqueue(ctx, requestCopy)
	}()
	return nil
}

func (s *PromptService) Evaluate(ctx context.Context, req Request) (*PromptDecision, error) {
	if s == nil || s.config == nil || s.evaluator == nil {
		return nil, &GuardError{Code: ErrorCodeUnavailable}
	}
	if s.config.BlockingActivationDegraded() {
		return nil, &GuardError{Code: ErrorCodeUnavailable}
	}
	cfg, ok := s.config.Active()
	if !ok {
		if s.config.EffectiveMode() == ModeBlocking {
			return nil, &GuardError{Code: ErrorCodeUnavailable}
		}
		return &PromptDecision{Kind: DecisionAllow, AllowNextStage: true}, nil
	}
	if cfg.EffectiveMode() != ModeBlocking || !cfg.IncludesGroup(req.GroupID) {
		return &PromptDecision{Kind: DecisionAllow, AllowNextStage: true}, nil
	}
	snapshot, err := ExtractBlockingPromptSnapshot(req, cfg.BlockingLatestTurnOnly)
	if errors.Is(err, ErrNoPromptText) {
		return &PromptDecision{Kind: DecisionAllow, AllowNextStage: true}, nil
	}
	if err != nil {
		return nil, &GuardError{Code: ErrorCodeInvalidResponse, Cause: err}
	}
	return s.evaluator.Evaluate(ctx, cfg, snapshot)
}

func (s *PromptService) GetConfig() (PublicConfig, error) { return s.config.Public() }

func (s *PromptService) SaveConfig(ctx context.Context, req UpdateConfigRequest, actorID int64) (PublicConfig, error) {
	return s.config.Save(ctx, req, actorID)
}

func (s *PromptService) Runtime(ctx context.Context) RuntimeSnapshot {
	expected, activeVersion, loadedAt, loadError := s.config.RuntimeState()
	cfg, hasConfig := s.config.Active()
	mode := s.EffectiveMode()
	workerTotal, queueCapacity := 0, 0
	if hasConfig {
		workerTotal, queueCapacity = cfg.WorkerCount, cfg.QueueCapacity
	}
	circuits := map[string]GuardCircuitSnapshot{}
	if hasConfig && s.circuit != nil {
		circuits = s.circuit.Snapshot(cfg)
	}
	var sharedCircuitErr error
	if hasConfig && s.shared != nil {
		circuits, sharedCircuitErr = s.shared.Overlay(ctx, cfg, circuits)
	}
	runtime := RuntimeSnapshot{
		ProcessStatus: "disabled", EffectiveMode: mode, ExpectedConfigVersion: expected,
		ActiveConfigVersion: activeVersion, ConfigLoadedAt: loadedAt, ConfigLoadError: loadError,
		WorkerTotal: workerTotal, QueueCapacity: queueCapacity, DatabaseStatus: "ok", RedisStatus: "ok",
		Endpoints: s.probeSnapshot(), Circuits: circuits, GuardMetrics: s.metrics.Snapshot(),
	}
	if s.repo != nil {
		stats, err := s.repo.QueueStats(ctx)
		if err != nil {
			runtime.DatabaseStatus = "error"
			runtime.LastErrorCode = "database_unavailable"
		} else {
			runtime.Queue = stats
		}
	} else {
		runtime.DatabaseStatus = "error"
	}
	if s.payload == nil || s.payload.Ping(ctx) != nil {
		runtime.RedisStatus = "error"
		if runtime.LastErrorCode == "" {
			runtime.LastErrorCode = "payload_store_unavailable"
		}
	}
	if sharedCircuitErr != nil {
		runtime.RedisStatus = "error"
		if runtime.LastErrorCode == "" {
			runtime.LastErrorCode, runtime.LastErrorMessage = sanitizeStoredError("payload_store_unavailable")
		}
	}
	activeWorkers, processed, failed, heartbeat, lastProcessed, workerCode, workerMessage := s.runner.Snapshot()
	runtime.WorkerActive, runtime.ProcessedTotal, runtime.FailedTotal = activeWorkers, processed, failed
	if s.metrics != nil {
		auditMetrics := s.metrics.AuditSnapshot()
		runtime.EnqueuedTotal, runtime.DroppedTotal = auditMetrics.Enqueued, auditMetrics.Dropped
	}
	runtime.WorkerHeartbeatAt, runtime.LastProcessedAt = heartbeat, lastProcessed
	if workerCode != "" {
		runtime.LastErrorCode, runtime.LastErrorMessage = workerCode, workerMessage
	}
	if mode != ModeOff {
		runtime.ProcessStatus = "running"
		if loadError != "" || runtime.DatabaseStatus != "ok" || runtime.RedisStatus != "ok" || activeVersion != expected {
			runtime.ProcessStatus = "degraded"
		}
		if heartbeat == nil || s.clock.Now().Sub(*heartbeat) > 10*time.Second {
			runtime.ProcessStatus = "degraded"
		}
		if hasConfig && len(cfg.EnabledEndpoints()) > 0 {
			available, err := s.hasAvailableCircuitEndpoint(ctx, cfg)
			if err != nil {
				runtime.RedisStatus = "error"
				if runtime.LastErrorCode == "" {
					runtime.LastErrorCode, runtime.LastErrorMessage = sanitizeStoredError("payload_store_unavailable")
				}
			}
			if !available {
				runtime.ProcessStatus = "degraded"
				if runtime.LastErrorCode == "" {
					runtime.LastErrorCode, runtime.LastErrorMessage = sanitizeStoredError(ErrorCodeUnavailable)
				}
			}
		}
	}
	return runtime
}

func (s *PromptService) hasAvailableCircuitEndpoint(ctx context.Context, cfg ActiveConfig) (bool, error) {
	for _, endpoint := range cfg.EnabledEndpoints() {
		if s.circuit != nil && !s.circuit.Allows(cfg, endpoint) {
			continue
		}
		if s.shared != nil {
			allowed, err := s.shared.Allows(ctx, cfg, endpoint)
			if err != nil {
				return false, err
			}
			if !allowed {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}

type ProbeRequest struct {
	Endpoint UpdateEndpoint `json:"endpoint"`
}

func (s *PromptService) Probe(ctx context.Context, request ProbeRequest) ProbeResult {
	started := s.clock.Now()
	endpoint, tokenApplied, err := s.resolveProbeEndpoint(request.Endpoint)
	if err != nil {
		return s.finishProbe(request.Endpoint.ID, started, ProbeResult{Status: "failed", ErrorCode: "endpoint_invalid", Message: "审计节点配置无效"})
	}
	LogInfo(EventProbeStarted, map[string]any{"guard_endpoint_id": endpoint.ID, "status": "started"})
	client, err := NewSecureHTTPClient(endpoint)
	if err != nil {
		return s.finishProbe(endpoint.ID, started, ProbeResult{Status: "failed", ErrorCode: "endpoint_unsafe", Message: "审计节点地址不在允许范围", TokenApplied: tokenApplied})
	}
	modelsURL, _ := ModelsURL(endpoint.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return s.finishProbe(endpoint.ID, started, ProbeResult{Status: "failed", ErrorCode: "probe_request_invalid", Message: "无法创建探测请求", TokenApplied: tokenApplied})
	}
	if endpoint.Token != "" {
		req.Header.Set("Authorization", "Bearer "+endpoint.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		code := "connection_failed"
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			code = "timeout"
		}
		return s.finishProbe(endpoint.ID, started, ProbeResult{Status: "failed", ErrorCode: code, Message: "无法连接审计节点", Retryable: true, TokenApplied: tokenApplied})
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxGuardResponseBytes+1))
	_ = resp.Body.Close()
	if readErr != nil {
		return s.finishProbe(endpoint.ID, started, ProbeResult{Status: "failed", ErrorCode: "response_read_failed", Message: "审计节点响应读取失败", HTTPStatus: resp.StatusCode, Retryable: true, TokenApplied: tokenApplied})
	}
	if int64(len(responseBody)) > maxGuardResponseBytes {
		return s.finishProbe(endpoint.ID, started, ProbeResult{Status: "failed", ErrorCode: "response_too_large", Message: "审计节点响应无效", HTTPStatus: resp.StatusCode, TokenApplied: tokenApplied})
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && modelsResponseReady(responseBody, endpoint.Model) {
		return s.finishProbe(endpoint.ID, started, ProbeResult{OK: true, Status: "healthy", Message: "审计节点连接正常", HTTPStatus: resp.StatusCode, TokenApplied: tokenApplied})
	}
	if (resp.StatusCode >= 200 && resp.StatusCode < 300) || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		result, scanErr := s.scanner.Scan(ctx, endpoint, "Hello", AllScannerIDs)
		if scanErr == nil && result != nil {
			return s.finishProbe(endpoint.ID, started, ProbeResult{OK: true, Status: "healthy", Message: "审计节点模型调用正常", HTTPStatus: http.StatusOK, TokenApplied: tokenApplied})
		}
		code, status, retryable := guardErrorCode(scanErr), 0, false
		var guardErr *GuardError
		if errors.As(scanErr, &guardErr) {
			status, retryable = guardErr.HTTPStatus, guardErr.Retryable
		}
		if code == "" {
			code = ErrorCodeInvalidResponse
		}
		return s.finishProbe(endpoint.ID, started, ProbeResult{Status: "failed", ErrorCode: code, Message: "审计节点模型调用失败", HTTPStatus: status, Retryable: retryable, TokenApplied: tokenApplied})
	}
	code, retryable := "probe_http_error", resp.StatusCode == 429 || resp.StatusCode >= 500
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		code = "authentication_failed"
	}
	return s.finishProbe(endpoint.ID, started, ProbeResult{Status: "failed", ErrorCode: code, Message: "审计节点探测失败", HTTPStatus: resp.StatusCode, Retryable: retryable, TokenApplied: tokenApplied})
}

func (s *PromptService) circuitProbeLoop(ctx context.Context) {
	defer s.circuitWG.Done()
	reconcileTicker := time.NewTicker(guardCircuitSharedReconcileInterval)
	defer reconcileTicker.Stop()
	probeTicker := time.NewTicker(guardCircuitProbeInterval)
	defer probeTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-reconcileTicker.C:
			s.reconcileSharedOpenCircuits(ctx)
		case <-probeTicker.C:
			s.probeOpenCircuits(ctx)
		}
	}
}

// reconcileSharedOpenCircuits republishes a local failure if its initial
// best-effort Redis write failed. It never probes the model and can only clear
// local state after the shared fence proves a later background recovery.
func (s *PromptService) reconcileSharedOpenCircuits(ctx context.Context) {
	if s == nil || s.config == nil || s.circuit == nil || s.shared == nil {
		return
	}
	cfg, ok := s.config.Active()
	if !ok || cfg.EffectiveMode() == ModeOff {
		return
	}
	s.circuit.PruneInactive(cfg)
	s.shared.PruneInactive(cfg)
	snapshots := s.circuit.Snapshot(cfg)
	for _, endpoint := range cfg.EnabledEndpoints() {
		local := snapshots[endpoint.ID]
		if local.State != GuardCircuitOpen {
			continue
		}
		recovered, err := s.shared.ReconcileLocalOpen(ctx, cfg, endpoint, local)
		if err == nil && recovered {
			s.circuit.MarkRecoveredByBackgroundProbe(cfg, endpoint)
		}
	}
}

// probeOpenCircuits is the sole path that can transition an open Guard
// circuit back to service. Foreground traffic never doubles as a recovery
// probe, so opening the breaker eliminates request-path pressure on a failed
// model until this bounded heartbeat succeeds.
func (s *PromptService) probeOpenCircuits(ctx context.Context) {
	if s == nil || s.config == nil || s.scanner == nil || (s.circuit == nil && s.shared == nil) {
		return
	}
	cfg, ok := s.config.Active()
	if !ok || cfg.EffectiveMode() == ModeOff {
		return
	}
	if s.circuit != nil {
		s.circuit.PruneInactive(cfg)
	}
	if s.shared != nil {
		s.shared.PruneInactive(cfg)
	}
	for _, endpoint := range cfg.EnabledEndpoints() {
		localProbe, localGeneration, sharedLease := false, uint64(0), ""
		local := GuardCircuitSnapshot{}
		if s.circuit != nil {
			local = s.circuit.Snapshot(cfg)[endpoint.ID]
		}
		if s.shared != nil && s.circuit != nil {
			if local.State == GuardCircuitOpen {
				recovered, err := s.shared.ReconcileLocalOpen(ctx, cfg, endpoint, local)
				if err != nil {
					continue
				}
				if recovered {
					s.circuit.MarkRecoveredByBackgroundProbe(cfg, endpoint)
					continue
				}
			}
		}
		if s.shared != nil {
			probeState, leaseID, err := s.shared.TryBeginProbe(ctx, cfg, endpoint)
			if err != nil {
				// Async consumers remain stopped when the shared state cannot be
				// consulted. Do not fan out an uncoordinated recovery request.
				continue
			}
			switch probeState {
			case sharedGuardCircuitProbeAcquired:
				sharedLease = leaseID
				if s.circuit != nil {
					localProbe = s.circuit.BeginProbe(cfg, endpoint)
					if localProbe {
						localGeneration = local.Generation
					}
				}
			case sharedGuardCircuitProbeBusy:
				continue
			case sharedGuardCircuitProbeRecovered:
				// A local open was reconciled above. A closed marker that has not
				// passed that fence is not evidence that this process recovered.
				continue
			case sharedGuardCircuitProbeMissing:
				if s.circuit == nil || !s.circuit.BeginProbe(cfg, endpoint) {
					continue
				}
				localProbe = true
				localGeneration = local.Generation
			}
		} else if s.circuit != nil {
			localProbe = s.circuit.BeginProbe(cfg, endpoint)
			if localProbe {
				localGeneration = local.Generation
			}
		}
		if !localProbe && sharedLease == "" {
			continue
		}
		started := s.clock.Now()
		probeCtx, cancel := context.WithTimeout(ctx, guardCircuitProbeTimeout(endpoint))
		result, err := callPromptScanner(probeCtx, s.scanner, endpoint, guardCircuitProbeText, cfg.Scanners)
		cancel()
		if err == nil && result == nil {
			err = &GuardError{Code: ErrorCodeInvalidResponse}
		}
		if localProbe {
			s.circuit.FinishProbe(cfg, endpoint, err)
		}
		if sharedLease != "" {
			_ = s.shared.FinishLocalProbe(ctx, cfg, endpoint, sharedLease, err, localGeneration)
		} else if s.shared != nil && err == nil {
			_ = s.shared.FinishLocalProbe(ctx, cfg, endpoint, "", nil, localGeneration)
		} else if err != nil && s.shared != nil {
			if s.circuit != nil {
				_ = s.shared.PublishLocalOpen(ctx, cfg, endpoint, s.circuit.Snapshot(cfg)[endpoint.ID])
			} else {
				_ = s.shared.Open(ctx, cfg, endpoint)
			}
		}
		if err == nil {
			s.finishProbe(endpoint.ID, started, ProbeResult{OK: true, Status: "healthy", Message: "审计节点恢复探测成功"})
			continue
		}
		code := guardErrorCode(err)
		retryable := false
		var guardErr *GuardError
		if errors.As(err, &guardErr) {
			retryable = guardErr.Retryable
		}
		s.finishProbe(endpoint.ID, started, ProbeResult{Status: "failed", ErrorCode: code, Message: "审计节点恢复探测失败", Retryable: retryable})
	}
}

func guardCircuitProbeTimeout(endpoint ActiveEndpoint) time.Duration {
	timeout := time.Duration(endpoint.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultTimeoutMS * time.Millisecond
	}
	if timeout > guardCircuitProbeTimeoutMax {
		return guardCircuitProbeTimeoutMax
	}
	return timeout
}

func modelsResponseReady(body []byte, model string) bool {
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil || response.Data == nil {
		return false
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return true
	}
	for _, item := range response.Data {
		if strings.TrimSpace(item.ID) == model {
			return true
		}
	}
	return false
}

func (s *PromptService) resolveProbeEndpoint(input UpdateEndpoint) (ActiveEndpoint, bool, error) {
	baseURL, err := NormalizeBaseURL(input.BaseURL)
	if err != nil {
		return ActiveEndpoint{}, false, err
	}
	token := strings.TrimSpace(input.Token)
	if token == "" {
		if cfg, ok := s.config.Active(); ok {
			for _, endpoint := range cfg.Endpoints {
				if endpoint.ID != strings.TrimSpace(input.ID) {
					continue
				}
				// Reuse a stored credential only when the probe targets the same
				// normalized base URL. Otherwise an admin probe could exfiltrate
				// the Guard token to an attacker-controlled HTTPS host.
				if endpoint.BaseURL == baseURL {
					token = endpoint.Token
				}
				break
			}
		}
	}
	model := strings.TrimSpace(input.Model)
	if model == "" {
		model = DefaultGuardModel
	}
	timeout := input.TimeoutMS
	if timeout == 0 {
		timeout = DefaultTimeoutMS
	}
	limit := input.InputLimit
	if limit == 0 {
		limit = DefaultInputLimit
	}
	storage := storageConfig{Enabled: false, Strategy: "priority", WorkerCount: DefaultWorkerCount, QueueCapacity: DefaultQueueCapacity, Scanners: append([]string(nil), AllScannerIDs...), AllGroups: true,
		Endpoints: []StorageEndpoint{{ID: strings.TrimSpace(input.ID), Name: strings.TrimSpace(input.Name), Protocol: "openai_compatible", BaseURL: baseURL, Model: model, TimeoutMS: timeout, InputLimit: limit}}}
	if storage.Endpoints[0].ID == "" {
		storage.Endpoints[0].ID = "probe"
	}
	if storage.Endpoints[0].Name == "" {
		storage.Endpoints[0].Name = "Probe"
	}
	if err := validateStorageConfig(storage); err != nil {
		return ActiveEndpoint{}, false, err
	}
	return ActiveEndpoint{ID: storage.Endpoints[0].ID, Name: storage.Endpoints[0].Name, Protocol: "openai_compatible", BaseURL: baseURL, Model: model, Token: token, TimeoutMS: timeout, InputLimit: limit, Enabled: true}, token != "", nil
}

func (s *PromptService) finishProbe(id string, started time.Time, result ProbeResult) ProbeResult {
	result.CheckedAt = s.clock.Now()
	result.LatencyMS = int(result.CheckedAt.Sub(started).Milliseconds())
	if result.OK {
		LogInfo(EventProbeFinished, map[string]any{"guard_endpoint_id": id, "status": result.Status, "latency_ms": result.LatencyMS, "http_status": result.HTTPStatus})
	} else {
		LogWarn(EventProbeFailed, map[string]any{"guard_endpoint_id": id, "status": result.Status, "latency_ms": result.LatencyMS, "http_status": result.HTTPStatus, "error_code": result.ErrorCode, "retryable": result.Retryable})
	}
	s.probeMu.Lock()
	if s.probes == nil {
		s.probes = make(map[string]ProbeResult)
	}
	s.probes[id] = result
	s.probeMu.Unlock()
	return result
}

func (s *PromptService) probeSnapshot() map[string]ProbeResult {
	s.probeMu.RLock()
	defer s.probeMu.RUnlock()
	result := make(map[string]ProbeResult, len(s.probes))
	for id, probe := range s.probes {
		result[id] = probe
	}
	return result
}

func (s *PromptService) ListEvents(ctx context.Context, filter EventFilter, page, pageSize int) (*EventPage, error) {
	return s.repo.ListEvents(ctx, filter, page, pageSize)
}
func (s *PromptService) GetEvent(ctx context.Context, id int64) (*Event, error) {
	return s.repo.GetEvent(ctx, id)
}

func (s *PromptService) DeleteEvent(ctx context.Context, id int64) (*DeleteResult, error) {
	result, err := s.repo.DeleteEvent(ctx, id)
	if err == nil {
		s.deletePayloads(ctx, result.JobIDs)
	}
	return result, err
}
func (s *PromptService) DeleteEventsByIDs(ctx context.Context, ids []int64) (*DeleteResult, error) {
	result, err := s.repo.DeleteEventsByIDs(ctx, ids)
	if err == nil {
		s.deletePayloads(ctx, result.JobIDs)
	}
	return result, err
}

type deleteClaims struct {
	FilterHash    string    `json:"filter_hash"`
	SnapshotMaxID int64     `json:"snapshot_max_id"`
	AdminID       int64     `json:"admin_id"`
	IssuedAt      time.Time `json:"issued_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

func (s *PromptService) PreviewDelete(ctx context.Context, filter EventFilter, adminID int64) (*DeletePreview, error) {
	preview, err := s.repo.PreviewDelete(ctx, filter)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	expires := now.Add(5 * time.Minute)
	claimsRaw, _ := json.Marshal(deleteClaims{FilterHash: preview.FilterHash, SnapshotMaxID: preview.SnapshotMaxID, AdminID: adminID, IssuedAt: now, ExpiresAt: expires})
	token, err := s.config.Encrypt(string(claimsRaw))
	if err != nil {
		return nil, err
	}
	preview.ConfirmationToken, preview.ExpiresAt = token, expires
	LogInfo(EventDeletePreviewed, map[string]any{"user_id": adminID, "status": "previewed"})
	return preview, nil
}

type DeleteByFilterRequest struct {
	Filter            EventFilter `json:"filter"`
	SnapshotMaxID     int64       `json:"snapshot_max_id"`
	FilterHash        string      `json:"filter_hash"`
	ConfirmationToken string      `json:"confirmation_token"`
	Confirm           bool        `json:"confirm"`
}

func (s *PromptService) DeleteByFilter(ctx context.Context, request DeleteByFilterRequest, adminID int64) (*DeleteResult, error) {
	if !request.Confirm {
		return nil, errors.New("prompt audit filter delete requires confirm=true")
	}
	plain, err := s.config.Decrypt(strings.TrimSpace(request.ConfirmationToken))
	if err != nil {
		return nil, errors.New("prompt audit confirmation token invalid")
	}
	var claims deleteClaims
	if json.Unmarshal([]byte(plain), &claims) != nil {
		return nil, errors.New("prompt audit confirmation token invalid")
	}
	computed := FilterHash(request.Filter, request.SnapshotMaxID)
	if claims.AdminID != adminID || claims.SnapshotMaxID != request.SnapshotMaxID || claims.FilterHash != request.FilterHash || request.FilterHash != computed || !s.clock.Now().Before(claims.ExpiresAt) {
		return nil, errors.New("prompt audit confirmation token does not match deletion request")
	}
	result, err := s.repo.DeleteEventsByFilter(ctx, request.Filter, request.SnapshotMaxID, 200)
	if err == nil {
		s.deletePayloads(ctx, result.JobIDs)
		LogWarn(EventEventsFilterDeleted, map[string]any{"user_id": adminID, "status": "deleted"})
	}
	return result, err
}

func (s *PromptService) deletePayloads(ctx context.Context, jobIDs []int64) {
	for _, id := range jobIDs {
		_ = s.payload.Delete(ctx, id)
	}
}

func parseTimeQuery(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}
