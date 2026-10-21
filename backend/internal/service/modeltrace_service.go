package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/modeltrace"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

const (
	ModelTraceExtraKey        = "modeltrace"
	ModelTraceDefaultCron     = "*/30 * * * *"
	ModelTraceMaxHistory      = 5
	ModelTraceGlobalConfigKey = "modeltrace_global_config"
	modelTraceConcurrency     = 4
	modelTraceTimeout         = 10 * time.Minute
	modelTraceMaxModels       = 10
	modelTraceMaxPending      = 128
)

// ModelTraceRecord represents one single probe execution record.
type ModelTraceRecord struct {
	DetectedModel      string                    `json:"detected_model"`
	Probability        float64                   `json:"probability"`
	LatencyMs          int64                     `json:"latency_ms"`
	TestedAt           time.Time                 `json:"tested_at"`
	Error              string                    `json:"error,omitempty"`
	Requests           []ModelTraceRequestRecord `json:"requests,omitempty"`
	ExpectedModel      string                    `json:"expected_model,omitempty"`
	MatchStatus        string                    `json:"match_status,omitempty"`
	AccountPaused      bool                      `json:"account_paused,omitempty"`
	NotificationStatus string                    `json:"notification_status,omitempty"`
	ActionError        string                    `json:"action_error,omitempty"`
}

type ModelTraceRequestRecord struct {
	ID            string    `json:"id"`
	StartedAt     time.Time `json:"started_at"`
	Endpoint      string    `json:"endpoint,omitempty"`
	LatencyMs     int64     `json:"latency_ms"`
	FirstTokenMs  *int      `json:"first_token_ms,omitempty"`
	InputTokens   int       `json:"input_tokens"`
	OutputTokens  int       `json:"output_tokens"`
	UsageReported bool      `json:"usage_reported"`
	Error         string    `json:"error,omitempty"`
	ValidSample   bool      `json:"valid_sample"`
}

type modelTraceRequestsKey struct{}

// ModelTraceModelConfig represents configuration and recent test state for a single model on an account.
type ModelTraceModelConfig struct {
	Latest  *ModelTraceRecord     `json:"latest,omitempty"`
	History []ModelTraceRecord    `json:"history,omitempty"`
	Task    *ModelTraceTask       `json:"task,omitempty"`
	Alert   *ModelTraceAlertState `json:"alert,omitempty"`
}

// ModelTraceTask is a snapshot of a manually submitted background probe.
type ModelTraceTask struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Error     string    `json:"error,omitempty"`
}

type modelTraceTaskKey struct {
	accountID int64
	modelID   string
}

// ModelTraceAccountConfig represents the configuration stored in account.Extra["modeltrace"].
type ModelTraceAccountConfig struct {
	LastRunAt *time.Time                        `json:"last_run_at,omitempty"`
	Models    map[string]*ModelTraceModelConfig `json:"models"` // model_id -> config
}

// ModelTraceGlobalConfig represents system-wide settings for ModelTrace.
type ModelTraceGlobalConfig struct {
	Enabled                     bool                `json:"enabled"`          // 全局监控总开关
	ProbeCount                  int                 `json:"probe_count"`      // 每个模型探测次数，默认 3
	IntervalSeconds             int                 `json:"interval_seconds"` // 探测间隔秒数，默认 1
	ReasoningEffort             string              `json:"reasoning_effort"` // 思考强度，默认 "low"
	CronExpression              string              `json:"cron_expression"`  // 全局调度周期
	NotifyOnMismatch            bool                `json:"notify_on_mismatch"`
	PauseOnMismatch             bool                `json:"pause_on_mismatch"`
	NotificationCooldownMinutes int                 `json:"notification_cooldown_minutes"`
	AllowedDetections           map[string][]string `json:"allowed_detections"`
}

// ModelTraceAccountViewItem is used by admin UI to inspect account monitor state.
type ModelTraceAccountViewItem struct {
	AccountID   int64                             `json:"account_id"`
	AccountName string                            `json:"account_name"`
	Platform    string                            `json:"platform"`
	Type        string                            `json:"type"`
	Schedulable bool                              `json:"schedulable"`
	LastRunAt   *time.Time                        `json:"last_run_at,omitempty"`
	Models      map[string]*ModelTraceModelConfig `json:"models"`
}

// UpdateAccountModelTraceRequest represents payload to configure modeltrace on an account.
type UpdateAccountModelTraceRequest struct {
	Models []string `json:"models"`
}

// ModelTraceService coordinates ModelTrace attribution probes and background checks.
type ModelTraceService struct {
	accountRepo        AccountRepository
	settingRepo        SettingRepository
	accountTestSvc     *AccountTestService
	cfg                *config.Config
	leaderLock         *singletonJobLock
	notificationSender FeishuPaymentTextSender

	cron         *cron.Cron
	startOnce    sync.Once
	stopOnce     sync.Once
	accountLocks sync.Map // accountID -> chan struct{}, cancellation-aware probe serialization
	slots        chan struct{}
	configLocks  sync.Map // serializes short config/result writes, not upstream requests
	runMu        sync.Mutex
	location     *time.Location
	runnerCancel context.CancelFunc
	taskCtx      context.Context
	taskCancel   context.CancelFunc
	tasksMu      sync.Mutex
	tasks        map[modelTraceTaskKey]ModelTraceTask
}

// NewModelTraceService creates a new ModelTraceService.
func NewModelTraceService(
	accountRepo AccountRepository,
	settingRepo SettingRepository,
	accountTestSvc *AccountTestService,
	cfg *config.Config,
) *ModelTraceService {
	taskCtx, taskCancel := context.WithCancel(context.Background())
	return &ModelTraceService{
		accountRepo:        accountRepo,
		settingRepo:        settingRepo,
		accountTestSvc:     accountTestSvc,
		cfg:                cfg,
		notificationSender: NewFeishuPaymentTextSender(),
		location:           time.Local,
		taskCtx:            taskCtx,
		taskCancel:         taskCancel,
		tasks:              make(map[modelTraceTaskKey]ModelTraceTask),
		slots:              make(chan struct{}, modelTraceConcurrency),
	}
}

// Start initiates the background cron runner.
func (s *ModelTraceService) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		loc := time.Local
		if s.cfg != nil {
			if parsed, err := time.LoadLocation(s.cfg.Timezone); err == nil && parsed != nil {
				loc = parsed
			}
		}

		c := cron.New(cron.WithParser(cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow)), cron.WithLocation(loc))
		s.location = loc
		runnerCtx, runnerCancel := context.WithCancel(context.Background())
		s.runnerCancel = runnerCancel
		_, err := c.AddFunc("* * * * *", func() {
			s.RunScheduledCycle(runnerCtx)
		})
		if err != nil {
			runnerCancel()
			logger.LegacyPrintf("service.modeltrace", "[ModelTrace] runner failed to schedule: %v", err)
			return
		}
		s.cron = c
		s.cron.Start()
		logger.LegacyPrintf("service.modeltrace", "[ModelTrace] background runner started (checking schedules every minute)")
	})
}

// Stop gracefully shuts down the background cron runner.
func (s *ModelTraceService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.taskCancel()
		if s.runnerCancel != nil {
			s.runnerCancel()
		}
		if s.cron != nil {
			ctx := s.cron.Stop()
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
				logger.LegacyPrintf("service.modeltrace", "[ModelTrace] runner stop timed out")
			}
		}
	})
}

func (s *ModelTraceService) acquireAccountLock(ctx context.Context, accountID int64) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	val, _ := s.accountLocks.LoadOrStore(accountID, make(chan struct{}, 1))
	lock, ok := val.(chan struct{})
	if !ok {
		panic("modeltrace: invalid account lock type")
	}
	select {
	case lock <- struct{}{}:
		select {
		case s.slots <- struct{}{}:
			return func() { <-s.slots; <-lock }, nil
		case <-ctx.Done():
			<-lock
			return nil, ctx.Err()
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *ModelTraceService) getConfigLock(accountID int64) *sync.Mutex {
	val, _ := s.configLocks.LoadOrStore(accountID, &sync.Mutex{})
	mu, ok := val.(*sync.Mutex)
	if !ok {
		panic("modeltrace: invalid config lock type")
	}
	return mu
}

func modelTraceSchedule(expression string) (cron.Schedule, error) {
	return cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(expression)
}

func modelTraceScheduleDue(expression string, now time.Time) bool {
	schedule, err := modelTraceSchedule(expression)
	if err != nil {
		return false
	}
	minute := now.Truncate(time.Minute)
	return schedule.Next(minute.Add(-time.Minute)).Equal(minute)
}

// GetGlobalConfig returns the system-wide ModelTrace configuration.
func (s *ModelTraceService) GetGlobalConfig(ctx context.Context) ModelTraceGlobalConfig {
	def := ModelTraceGlobalConfig{
		Enabled:                     false,
		ProbeCount:                  3,
		IntervalSeconds:             1,
		ReasoningEffort:             "low",
		CronExpression:              ModelTraceDefaultCron,
		NotificationCooldownMinutes: 60,
		AllowedDetections:           defaultModelTraceAllowedDetections(),
	}
	if s.settingRepo == nil {
		return def
	}
	raw, err := s.settingRepo.GetValue(ctx, ModelTraceGlobalConfigKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return def
	}
	cfg := def
	cfg.AllowedDetections = nil
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return def
	}
	if cfg.ProbeCount <= 0 {
		cfg.ProbeCount = 3
	}
	if cfg.ProbeCount > 10 {
		cfg.ProbeCount = 10
	}
	if cfg.IntervalSeconds < 0 {
		cfg.IntervalSeconds = 1
	}
	if cfg.IntervalSeconds > 60 {
		cfg.IntervalSeconds = 60
	}
	if cfg.ReasoningEffort == "" {
		cfg.ReasoningEffort = "low"
	}
	if strings.TrimSpace(cfg.CronExpression) == "" {
		cfg.CronExpression = ModelTraceDefaultCron
	}
	if err := normalizeModelTracePolicy(&cfg); err != nil {
		return def
	}
	return cfg
}

// UpdateGlobalConfig updates the system-wide ModelTrace configuration.
func (s *ModelTraceService) UpdateGlobalConfig(ctx context.Context, cfg ModelTraceGlobalConfig) error {
	if s.settingRepo == nil {
		return fmt.Errorf("setting repository is not configured")
	}
	if cfg.ProbeCount <= 0 {
		cfg.ProbeCount = 3
	}
	if cfg.IntervalSeconds < 0 {
		cfg.IntervalSeconds = 1
	}
	if cfg.ReasoningEffort == "" {
		cfg.ReasoningEffort = "low"
	}
	if strings.TrimSpace(cfg.CronExpression) == "" {
		cfg.CronExpression = ModelTraceDefaultCron
	}
	if _, err := modelTraceSchedule(cfg.CronExpression); err != nil {
		return infraerrors.BadRequest("MODELTRACE_CONFIG_INVALID", "invalid cron expression")
	}
	if cfg.ProbeCount > 10 || cfg.IntervalSeconds > 60 {
		return infraerrors.BadRequest("MODELTRACE_CONFIG_INVALID", "probe_count must be at most 10 and interval_seconds at most 60")
	}
	if cfg.ReasoningEffort != "low" && cfg.ReasoningEffort != "medium" && cfg.ReasoningEffort != "high" {
		return infraerrors.BadRequest("MODELTRACE_CONFIG_INVALID", "invalid reasoning effort")
	}
	if err := normalizeModelTracePolicy(&cfg); err != nil {
		return err
	}

	bytes, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, ModelTraceGlobalConfigKey, string(bytes))
}

// listMonitorAccounts uses a filtered query in production. The paginated fallback
// supports other repositories without truncating monitors at 1000 accounts.
func (s *ModelTraceService) listMonitorAccounts(ctx context.Context) ([]Account, error) {
	if repo, ok := s.accountRepo.(interface {
		ListModelTraceAccounts(context.Context) ([]Account, error)
	}); ok {
		return repo.ListModelTraceAccounts(ctx)
	}
	var accounts []Account
	for page := 1; ; page++ {
		batch, result, err := s.accountRepo.List(ctx, pagination.PaginationParams{Page: page, PageSize: 1000})
		if err != nil {
			return nil, err
		}
		for _, account := range batch {
			if DecodeModelTraceConfig(account.Extra) != nil {
				accounts = append(accounts, account)
			}
		}
		if len(batch) == 0 || (result != nil && page >= result.Pages) || (result == nil && len(batch) < 1000) {
			return accounts, nil
		}
	}
}

// ListMonitors lists only explicitly added monitoring accounts.
func (s *ModelTraceService) ListMonitors(ctx context.Context) ([]ModelTraceAccountViewItem, error) {
	accounts, err := s.listMonitorAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}

	items := make([]ModelTraceAccountViewItem, 0, len(accounts))
	for _, acc := range accounts {
		cfg := DecodeModelTraceConfig(acc.Extra)
		if cfg == nil {
			continue
		}
		models := cfg.Models
		if models == nil {
			models = make(map[string]*ModelTraceModelConfig)
		}
		s.tasksMu.Lock()
		for modelID, model := range models {
			if model == nil {
				model = &ModelTraceModelConfig{}
				models[modelID] = model
			}
			if task, ok := s.tasks[modelTraceTaskKey{acc.ID, modelID}]; ok {
				model.Task = &task
			}
		}
		s.tasksMu.Unlock()

		items = append(items, ModelTraceAccountViewItem{
			AccountID:   acc.ID,
			AccountName: acc.Name,
			Platform:    acc.Platform,
			Type:        acc.Type,
			Schedulable: acc.Schedulable,
			LastRunAt:   cfg.LastRunAt,
			Models:      models,
		})
	}

	return items, nil
}

// RemoveAccountMonitor removes monitoring without changing the upstream account.
func (s *ModelTraceService) RemoveAccountMonitor(ctx context.Context, accountID int64) error {
	mu := s.getConfigLock(accountID)
	mu.Lock()
	defer mu.Unlock()
	return s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{ModelTraceExtraKey: nil})
}

// UpdateAccountConfig sets only the monitored models for a single account.
func (s *ModelTraceService) UpdateAccountConfig(ctx context.Context, accountID int64, req UpdateAccountModelTraceRequest) error {
	if len(req.Models) > modelTraceMaxModels {
		return infraerrors.BadRequest("MODELTRACE_CONFIG_INVALID", fmt.Sprintf("configure at most %d models per account", modelTraceMaxModels))
	}
	mu := s.getConfigLock(accountID)
	mu.Lock()
	defer mu.Unlock()
	acc, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}
	if !modelTraceAccountSupported(acc) {
		return infraerrors.BadRequest("MODELTRACE_ACCOUNT_UNSUPPORTED", "ModelTrace requires an ordinary OpenAI OAuth or API-key account")
	}

	cfg := DecodeModelTraceConfig(acc.Extra)
	if cfg == nil {
		cfg = &ModelTraceAccountConfig{
			Models: make(map[string]*ModelTraceModelConfig),
		}
	}

	currentModels := make(map[string]*ModelTraceModelConfig)
	for _, m := range req.Models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if len(m) > 200 || !modelTraceModelSupported(acc, m) {
			return infraerrors.BadRequest("MODELTRACE_MODEL_UNSUPPORTED", "ModelTrace requires a text model ID of at most 200 bytes")
		}
		if existing, ok := cfg.Models[m]; ok {
			currentModels[m] = existing
		} else {
			currentModels[m] = &ModelTraceModelConfig{}
		}
	}
	cfg.Models = currentModels

	return s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{ModelTraceExtraKey: cfg})
}

// SubmitProbe queues a manual probe without tying its lifetime to the HTTP request.
func (s *ModelTraceService) SubmitProbe(ctx context.Context, accountID int64, modelID string) (*ModelTraceTask, error) {
	if err := s.taskCtx.Err(); err != nil {
		return nil, err
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !modelTraceAccountSupported(account) {
		return nil, infraerrors.BadRequest("MODELTRACE_ACCOUNT_UNSUPPORTED", "unsupported ModelTrace account")
	}
	cfg := DecodeModelTraceConfig(account.Extra)
	if cfg == nil {
		return nil, infraerrors.BadRequest("MODELTRACE_NOT_CONFIGURED", "account is not configured for modeltrace")
	}
	if _, ok := cfg.Models[modelID]; !ok {
		return nil, infraerrors.BadRequest("MODELTRACE_NOT_CONFIGURED", "model is not configured for modeltrace")
	}
	if !modelTraceModelSupported(account, modelID) {
		return nil, infraerrors.BadRequest("MODELTRACE_MODEL_UNSUPPORTED", "ModelTrace requires a supported OpenAI/GPT text model")
	}
	key := modelTraceTaskKey{accountID, modelID}
	s.tasksMu.Lock()
	if task, ok := s.tasks[key]; ok && (task.Status == "queued" || task.Status == "running") {
		s.tasksMu.Unlock()
		return &task, nil
	}
	pending := 0
	for _, task := range s.tasks {
		if task.Status == "queued" || task.Status == "running" {
			pending++
		}
	}
	if pending >= modelTraceMaxPending {
		s.tasksMu.Unlock()
		return nil, infraerrors.TooManyRequests("MODELTRACE_QUEUE_FULL", "ModelTrace queue is full; retry after a probe finishes")
	}
	now := time.Now()
	task := ModelTraceTask{ID: uuid.NewString(), Status: "queued", CreatedAt: now, UpdatedAt: now}
	s.tasks[key] = task
	s.tasksMu.Unlock()
	// Preserve request correlation values, while cancellation follows service shutdown.
	workerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), modelTraceTimeout)
	stop := context.AfterFunc(s.taskCtx, cancel)
	go func() {
		defer cancel()
		defer stop()
		unlock, lockErr := s.acquireAccountLock(workerCtx, accountID)
		if lockErr != nil {
			s.finishTask(key, "failed", lockErr.Error())
			return
		}
		defer unlock()
		s.finishTask(key, "running", "")
		record, probeErr := s.probeModelLocked(workerCtx, accountID, modelID)
		if probeErr != nil {
			s.finishTask(key, "failed", probeErr.Error())
		} else if record.Error != "" {
			s.finishTask(key, "failed", record.Error)
		} else {
			s.finishTask(key, "completed", "")
		}
	}()
	return &task, nil
}

func (s *ModelTraceService) finishTask(key modelTraceTaskKey, status, errText string) {
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	task := s.tasks[key]
	task.Status, task.Error, task.UpdatedAt = status, errText, time.Now()
	s.tasks[key] = task
}

// ProbeModel runs an on-demand ModelTrace probe for internal callers.
func (s *ModelTraceService) ProbeModel(ctx context.Context, accountID int64, modelID string) (*ModelTraceRecord, error) {
	logger.LegacyPrintf("service.modeltrace", "[ModelTrace] manual probe for account %d model %s waiting for probe lock", accountID, modelID)
	unlock, err := s.acquireAccountLock(ctx, accountID)
	if err != nil {
		logger.LegacyPrintf("service.modeltrace", "[ModelTrace] manual probe for account %d model %s stopped while waiting: %v", accountID, modelID, err)
		return nil, err
	}
	defer unlock()

	return s.probeModelLocked(ctx, accountID, modelID)
}

func (s *ModelTraceService) probeModelLocked(ctx context.Context, accountID int64, modelID string) (result *ModelTraceRecord, probeErr error) {
	ctx, probeCancel := context.WithTimeout(ctx, modelTraceTimeout)
	defer probeCancel()
	l := logger.FromContext(ctx).With(
		zap.String("component", "service.modeltrace"),
		zap.Int64("account_id", accountID),
		zap.String("requested_model", modelID),
		zap.String("probe_id", uuid.NewString()),
	)
	ctx = logger.IntoContext(ctx, l)
	l.Info("modeltrace.probe_started")
	startedAt := time.Now()
	defer func() {
		if probeErr != nil {
			l.Warn("modeltrace.probe_failed", zap.Error(probeErr), zap.Duration("elapsed", time.Since(startedAt)))
		}
	}()
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("account not found: %w", err)
	}
	if !modelTraceAccountSupported(account) {
		return nil, infraerrors.BadRequest("MODELTRACE_ACCOUNT_UNSUPPORTED", "unsupported ModelTrace account")
	}
	initialConfig := DecodeModelTraceConfig(account.Extra)
	if initialConfig == nil {
		return nil, fmt.Errorf("account is not configured for modeltrace")
	}
	if _, exists := initialConfig.Models[modelID]; !exists {
		return nil, fmt.Errorf("model is not configured for modeltrace")
	}
	if !modelTraceModelSupported(account, modelID) {
		return nil, infraerrors.BadRequest("MODELTRACE_MODEL_UNSUPPORTED", "ModelTrace requires a supported OpenAI/GPT text model")
	}

	expectedModel := strings.ToLower(strings.TrimSpace(account.GetMappedModel(modelID)))
	record := s.executeProbe(ctx, account, modelID)
	record.ExpectedModel = expectedModel
	if err := ctx.Err(); err != nil {
		// Keep a terminal failure visible when the caller cancels the probe.
		record.Error = err.Error()
		record.DetectedModel = "Unknown"
		record.Probability = 0
	}
	return s.saveProbeResult(ctx, accountID, modelID, record)
}

// executeProbe performs probe completion rounds with interval checking and ModelTrace attribution.
func (s *ModelTraceService) executeProbe(ctx context.Context, account *Account, modelID string) *ModelTraceRecord {
	var requests []ModelTraceRequestRecord
	ctx = context.WithValue(ctx, modelTraceRequestsKey{}, &requests)
	gCfg := s.GetGlobalConfig(ctx)
	targetCount := gCfg.ProbeCount
	if targetCount <= 0 {
		targetCount = 3
	}
	interval := time.Duration(gCfg.IntervalSeconds) * time.Second
	reasoningEffort := gCfg.ReasoningEffort
	if reasoningEffort == "" {
		reasoningEffort = "low"
	}
	reasoningEffort = modelTraceReasoningEffortFor(account, modelID, reasoningEffort)

	maxAttempts := targetCount + 3
	challenges := modeltrace.GenerateChallenges(maxAttempts)
	var outputs []modeltrace.OutputItem
	var lastErr error
	startedAt := time.Now()

probeLoop:
	for attempt, ch := range challenges {
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			break
		}
		l := logger.FromContext(ctx)
		l.Info("modeltrace.request_started", zap.Int("attempt", attempt+1), zap.Int("max_attempts", maxAttempts), zap.Int("valid_samples", len(outputs)), zap.Int("target_samples", targetCount))
		requestStartedAt := time.Now()
		validSample := false
		respText, err := s.requestCompletion(ctx, account, modelID, ch.Prompt, reasoningEffort)
		if err != nil {
			lastErr = err
		} else {
			nums := modeltrace.ParseNumbers(respText)
			minNums := int(math.Ceil(float64(ch.Count) * 0.55))
			if minNums < 80 {
				minNums = 80
			}
			if len(nums) >= minNums {
				validSample = true
				outputs = append(outputs, modeltrace.OutputItem{
					Text:          respText,
					ExpectedCount: ch.Count,
				})
			} else {
				lastErr = fmt.Errorf("有效数字不足: %d/%d", len(nums), minNums)
			}
		}
		l.Info("modeltrace.request_finished", zap.Int("attempt", attempt+1), zap.Bool("request_success", err == nil), zap.Int("valid_samples", len(outputs)), zap.Duration("elapsed", time.Since(requestStartedAt)))
		if len(requests) > 0 {
			r := &requests[len(requests)-1]
			r.ValidSample = validSample
			// A successful HTTP response can still be unusable for fingerprinting.
			if err == nil && !validSample && lastErr != nil {
				r.Error = lastErr.Error()
			}
		}

		if len(outputs) >= targetCount || modelTraceTerminalStatus(err) {
			break
		}

		// 间隔检查：每轮请求间暂停指定时间，保护上游速率限制
		select {
		case <-ctx.Done():
			lastErr = ctx.Err()
			break probeLoop
		case <-time.After(interval):
		}
	}

	latency := time.Since(startedAt).Milliseconds()
	if len(outputs) < targetCount {
		errStr := "no valid completion outputs received"
		if lastErr != nil {
			errStr = lastErr.Error()
		}
		errStr = fmt.Sprintf("incomplete samples: %d/%d; %s", len(outputs), targetCount, errStr)
		return &ModelTraceRecord{
			DetectedModel: "Unknown",
			Requests:      requests,
			Probability:   0.0,
			LatencyMs:     latency,
			TestedAt:      time.Now(),
			Error:         errStr,
		}
	}

	attr, err := modeltrace.AnalyzeOutputs(outputs)
	if err != nil {
		return &ModelTraceRecord{
			DetectedModel: "Unknown",
			Requests:      requests,
			Probability:   0.0,
			LatencyMs:     latency,
			TestedAt:      time.Now(),
			Error:         err.Error(),
		}
	}

	return &ModelTraceRecord{
		DetectedModel: attr.Prediction,
		Requests:      requests,
		Probability:   attr.Probability,
		LatencyMs:     latency,
		TestedAt:      time.Now(),
	}
}

// requestCompletion dispatches challenge prompt through the authentic AccountTestService pipeline,
// ensuring completely identical headers, client fingerprint, proxy, and auth handling.
func (s *ModelTraceService) requestCompletion(
	ctx context.Context,
	account *Account,
	modelID string,
	prompt string,
	reasoningEffort string,
) (string, error) {
	usage := &modelTraceUsageCollector{startedAt: time.Now()}
	requestID := uuid.NewString()
	ctx = logger.IntoContext(ctx, logger.FromContext(ctx).With(zap.String("probe_request_id", requestID)))
	ctx = context.WithValue(ctx, modelTraceUsageKey{}, usage)
	var text string
	var err error
	if s.accountTestSvc == nil {
		err = fmt.Errorf("accountTestService is not available")
	} else {
		text, err = s.accountTestSvc.RunModelTraceProbe(ctx, account.ID, modelID, prompt, reasoningEffort)
	}
	if requests, ok := ctx.Value(modelTraceRequestsKey{}).(*[]ModelTraceRequestRecord); ok {
		record := ModelTraceRequestRecord{ID: requestID, StartedAt: usage.startedAt, Endpoint: usage.endpoint, LatencyMs: time.Since(usage.startedAt).Milliseconds(), FirstTokenMs: usage.firstTokenMs, InputTokens: usage.tokens.InputTokens + usage.tokens.CacheReadTokens + usage.tokens.CacheCreationTokens, OutputTokens: usage.tokens.OutputTokens, UsageReported: usage.seen}
		if err != nil {
			record.Error = err.Error()
		}
		*requests = append(*requests, record)
	}
	return text, err
}

// RunScheduledCycle probes accounts concurrently and each account's models sequentially.
func (s *ModelTraceService) RunScheduledCycle(ctx context.Context) {
	if !s.runMu.TryLock() {
		logger.LegacyPrintf("service.modeltrace", "[ModelTrace] scheduled cycle skipped: previous cycle is still running")
		return
	}
	defer s.runMu.Unlock()

	// 整体监控总开关校验：若全局关闭，则跳过后台轮询
	globalConfig := s.GetGlobalConfig(ctx)
	if !globalConfig.Enabled {
		logger.LegacyPrintf("service.modeltrace", "[ModelTrace] scheduled cycle skipped: global monitoring is disabled")
		return
	}
	if !modelTraceScheduleDue(globalConfig.CronExpression, time.Now().In(s.location)) {
		return
	}
	leaseCtx, release, acquired := s.leaderLock.try(ctx)
	if !acquired {
		return
	}
	defer release()
	ctx = leaseCtx

	accounts, err := s.listMonitorAccounts(ctx)
	if err != nil {
		logger.LegacyPrintf("service.modeltrace", "[ModelTrace] scheduled cycle failed to list accounts: %v", err)
		return
	}

	var wg sync.WaitGroup
	logger.LegacyPrintf("service.modeltrace", "[ModelTrace] scheduled cycle started: %d monitored accounts", len(accounts))
	for _, acc := range accounts {
		if !modelTraceAccountSupported(&acc) {
			continue
		}
		if acc.Status != StatusActive {
			logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d skipped: status=%s", acc.ID, acc.Status)
			continue
		}
		cfg := DecodeModelTraceConfig(acc.Extra)
		if cfg == nil || len(cfg.Models) == 0 {
			logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d skipped: no monitored models", acc.ID)
			continue
		}

		wg.Add(1)
		accCopy := acc
		go func(a *Account, c *ModelTraceAccountConfig) {
			defer wg.Done()
			s.runAccountCycle(ctx, a, c)
		}(&accCopy, cfg)
	}
	wg.Wait()
	logger.LegacyPrintf("service.modeltrace", "[ModelTrace] scheduled cycle finished")
}

// runAccountCycle sequentially probes all configured models for a single account.
func (s *ModelTraceService) runAccountCycle(ctx context.Context, account *Account, cfg *ModelTraceAccountConfig) {
	logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d waiting for probe lock", account.ID)
	unlock, err := s.acquireAccountLock(ctx, account.ID)
	if err != nil {
		logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d stopped while waiting: %v", account.ID, err)
		return
	}
	defer unlock()
	logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d started: %d models", account.ID, len(cfg.Models))

	for modelID := range cfg.Models {
		select {
		case <-ctx.Done():
			logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d stopped: %v", account.ID, ctx.Err())
			return
		default:
		}
		latest, err := s.accountRepo.GetByID(ctx, account.ID)
		if err != nil {
			logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d stopped: lookup failed: %v", account.ID, err)
			return
		}
		if latest.Status != StatusActive {
			logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d stopped: status=%s", account.ID, latest.Status)
			return
		}
		if modelTraceCoolingDown(latest) {
			logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d stopped: account is cooling down", account.ID)
			return
		}
		if !modelTraceAccountSupported(latest) {
			logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d stopped: account type is no longer supported", account.ID)
			return
		}
		if !s.GetGlobalConfig(ctx).Enabled {
			logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d stopped: global monitoring disabled", account.ID)
			return
		}
		current := DecodeModelTraceConfig(latest.Extra)
		if current == nil {
			logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d stopped: monitor removed", account.ID)
			return
		}
		if _, exists := current.Models[modelID]; !exists {
			logger.LegacyPrintf("service.modeltrace", "[ModelTrace] account %d model %s skipped: model removed", account.ID, modelID)
			continue
		}

		_, probeErr := s.probeModelLocked(ctx, account.ID, modelID)
		if probeErr != nil {
			logger.LegacyPrintf("service.modeltrace", "[ModelTrace] probe failed for account %d model %s: %v", account.ID, modelID, probeErr)
		}
	}
}

// DecodeModelTraceConfig extracts ModelTraceAccountConfig from account Extra map.
func DecodeModelTraceConfig(extra map[string]any) *ModelTraceAccountConfig {
	if extra == nil {
		return nil
	}
	raw, ok := extra[ModelTraceExtraKey]
	if !ok || raw == nil {
		return nil
	}
	bytes, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var cfg ModelTraceAccountConfig
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return nil
	}
	return &cfg
}
