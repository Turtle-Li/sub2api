package service

// Scheduling/claim separation adapted from ranxi2001/sub2api at
// 5f0b6321a50153a738aef3fbade57c969dbe710c (LGPL-3.0).
// Local difference: tiny, bounded retries without Pelican or customer usage.
import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
)

type channelMonitorV3ProbeExecutor interface {
	Check(context.Context, *Group, string) ChannelMonitorV3GroupProbeResult
}

type ChannelMonitorV3GroupProber struct {
	repo         ChannelMonitorV3Repository
	probes       ChannelMonitorV3GroupProbeRepository
	groups       channelMonitorV3Groups
	settings     channelMonitorRuntimeReader
	executor     channelMonitorV3ProbeExecutor
	lock         *singletonJobLock
	ops          *OpsService
	email        *NotificationEmailService
	emailLimiter *slidingWindowLimiter
	ctx          context.Context
	cancel       context.CancelFunc
	startOnce    sync.Once
	stopOnce     sync.Once
	tick         sync.Mutex
	wg           sync.WaitGroup
}

func NewChannelMonitorV3GroupProber(repo ChannelMonitorV3Repository, groups GroupRepository, settings *SettingService,
	gateway *GatewayService, openai *OpenAIGatewayService, concurrency *ConcurrencyService, db *sql.DB, ops *OpsService, email *NotificationEmailService) *ChannelMonitorV3GroupProber {
	ctx, cancel := context.WithCancel(context.Background())
	probes, _ := repo.(ChannelMonitorV3GroupProbeRepository)
	return &ChannelMonitorV3GroupProber{repo: repo, probes: probes, groups: groups, settings: settings,
		executor: &channelMonitorV3GatewayProbe{gateway: gateway, openai: openai, concurrency: concurrency},
		lock:     newSingletonJobLock(nil, db, "channel-monitor-v3-group-probes", 2*time.Minute), ops: ops, email: email,
		emailLimiter: newSlidingWindowLimiter(0, time.Hour), ctx: ctx, cancel: cancel}
}

func (p *ChannelMonitorV3GroupProber) Start() {
	if p == nil || p.probes == nil {
		return
	}
	p.startOnce.Do(func() {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-p.ctx.Done():
					return
				case now := <-ticker.C:
					p.RunDue(p.ctx, now)
				}
			}
		}()
	})
}

func (p *ChannelMonitorV3GroupProber) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() { p.cancel(); p.wg.Wait() })
}

func (p *ChannelMonitorV3GroupProber) RunDue(ctx context.Context, now time.Time) {
	if p == nil || p.probes == nil || p.executor == nil || !p.tick.TryLock() {
		return
	}
	defer p.tick.Unlock()
	ctx, release, ok := p.lock.try(ctx)
	if !ok {
		return
	}
	defer release()
	cfg, err := p.repo.GetConfig(ctx)
	if err != nil || !p.allowed(ctx, cfg) {
		return
	}
	slot := now.UTC().Truncate(ChannelMonitorV3ProbeInterval)
	for _, target := range cfg.GroupProbe.Groups {
		if ctx.Err() != nil || !runtimegate.SharedWorkAllowed() {
			return
		}
		if !target.Enabled || !target.IsListed() {
			continue
		}
		if p.authorizedGroup(ctx, target) == nil {
			continue
		}
		id, err := p.probes.ClaimGroupProbe(ctx, target, slot)
		if err != nil {
			slog.Error("group_probe_claim_failed", "group_id", target.GroupID)
			continue
		}
		if id == 0 {
			continue
		}
		result := p.checkWithRetries(ctx, id, target, slot)
		finished := time.Now().UTC()
		result.FinishedAt = &finished
		saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = p.probes.FinishGroupProbe(saveCtx, result)
		saveCancel()
		if err != nil {
			slog.Error("group_probe_save_failed", "group_id", target.GroupID)
			continue
		}
		if result.Outcome == "failure" {
			slog.Warn("group_probe_failed", "group_id", target.GroupID, "category", result.Category)
		}
		p.notify(ctx, result)
	}
	if err := p.probes.PruneGroupProbes(ctx, now.Add(-31*24*time.Hour)); err != nil {
		slog.Warn("group_probe_prune_failed")
	}
}

// Only the claim owner retries; progress is durable before another paid attempt.
// Operator withdrawal or loss of the execution fence leaves the slot unknown.
func (p *ChannelMonitorV3GroupProber) checkWithRetries(ctx context.Context, id int64, target ChannelMonitorV3GroupProbeTarget, slot time.Time) ChannelMonitorV3GroupProbeResult {
	result := ChannelMonitorV3GroupProbeResult{ID: id, GroupID: target.GroupID, Model: target.Model, Slot: slot, Outcome: "cancelled"}
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				result.Outcome, result.Category = "cancelled", ""
				return result
			case <-time.After(250 * time.Millisecond):
			}
		}
		group := p.authorizedGroup(ctx, target)
		if group == nil {
			result.Outcome, result.Category = "cancelled", ""
			return result
		}
		runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		receipt := p.executor.Check(runCtx, group, target.Model)
		cancel()
		result.AttemptCount++
		result.InputTokens += receipt.InputTokens
		result.OutputTokens += receipt.OutputTokens
		if receipt.InputTokens == 0 && receipt.OutputTokens == 0 {
			result.MissingUsageAttempts++
		}
		result.Outcome, result.Category = receipt.Outcome, receipt.Category
		result.LatencyMs, result.AccountID = receipt.LatencyMs, receipt.AccountID
		saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := p.probes.RecordGroupProbeAttempt(saveCtx, result)
		saveCancel()
		if err != nil {
			slog.Error("group_probe_progress_failed", "group_id", target.GroupID)
			result.Outcome, result.Category = "cancelled", ""
			return result
		}
		if p.authorizedGroup(ctx, target) == nil {
			result.Outcome, result.Category = "cancelled", ""
			return result
		}
		switch receipt.Outcome {
		case "success":
			return result
		case "failure":
			// Publish a failure only after all three attempts fail.
		default:
			result.Outcome, result.Category = "cancelled", ""
			return result
		}
	}
	return result
}

func (p *ChannelMonitorV3GroupProber) authorizedGroup(ctx context.Context, target ChannelMonitorV3GroupProbeTarget) *Group {
	if ctx.Err() != nil || !runtimegate.SharedWorkAllowed() {
		return nil
	}
	cfg, err := p.repo.GetConfig(ctx)
	if err != nil || !p.allowed(ctx, cfg) {
		return nil
	}
	for _, current := range cfg.GroupProbe.Groups {
		if !current.sameCheck(target) {
			continue
		}
		group, err := p.groups.GetByID(ctx, target.GroupID)
		if err == nil && group != nil && group.Status == StatusActive && channelMonitorV3ProbeGroupSupported(group) && group.ModelAllowlist.Allows(target.Model) && !channelMonitorV3ProbeMediaModel(target.Model) {
			return group
		}
	}
	return nil
}

func (p *ChannelMonitorV3GroupProber) allowed(ctx context.Context, cfg *ChannelMonitorV3Config) bool {
	return cfg != nil && cfg.GroupProbe.Enabled && len(cfg.GroupProbe.Groups) <= 10 && p.settings != nil && p.settings.GetChannelMonitorRuntime(ctx).V3Active() && runtimegate.SharedWorkAllowed()
}

// Use the existing ops recipient/template preferences. A group/outcome/hour
// delivery identity bounds reminders; recovery sends only after a failure.
func (p *ChannelMonitorV3GroupProber) notify(ctx context.Context, result ChannelMonitorV3GroupProbeResult) {
	if p.ops == nil || p.email == nil || result.Outcome == "cancelled" {
		return
	}
	previous, err := p.probes.GroupProbes(ctx, []int64{result.GroupID}, result.Slot.Add(-24*time.Hour), result.Slot)
	if err != nil {
		return
	}
	var last *ChannelMonitorV3GroupProbeResult
	for i := len(previous) - 1; i >= 0; i-- {
		if previous[i].Model == result.Model && (previous[i].Outcome == "success" || previous[i].Outcome == "failure") {
			last = &previous[i]
			break
		}
	}
	if result.Outcome == "success" && (last == nil || last.Outcome != "failure") {
		return
	}
	cfg, err := p.ops.GetEmailNotificationConfig(ctx)
	if err != nil || cfg == nil || !cfg.Alert.Enabled || !shouldSendOpsAlertEmailByMinSeverity(cfg.Alert.MinSeverity, "P1") {
		return
	}
	if result.Outcome == "success" && !cfg.Alert.IncludeResolvedAlerts {
		return
	}
	p.emailLimiter.SetLimit(cfg.Alert.RateLimitPerHour)
	summary := fmt.Sprintf("Group %d · %s · %s · scheduled connectivity check (%d attempts); real-user success rates are separate", result.GroupID, result.Model, result.Outcome, result.AttemptCount)
	for _, recipient := range cfg.Alert.Recipients {
		if recipient == "" || !p.emailLimiter.Allow(time.Now().UTC()) {
			continue
		}
		err = p.email.Send(ctx, NotificationEmailSendInput{Event: NotificationEmailEventOpsAlert, SourceType: "model_group_probe",
			SourceID: fmt.Sprintf("%d:%s:%d", result.GroupID, result.Outcome, result.Slot.Unix()/3600), RecipientEmail: recipient,
			Variables: map[string]string{"rule_name": "Model group availability", "severity": "warning", "alert_status": result.Outcome, "metric_type": "model_group_probe", "alert_description": summary, "triggered_at": result.Slot.Format(time.RFC3339)},
			BeforeSend: func(checkCtx context.Context) error {
				current, err := p.repo.GetConfig(checkCtx)
				if err != nil {
					return err
				}
				if !p.allowed(checkCtx, current) || !channelMonitorV3ProbeTargetEnabled(current, result.GroupID, result.Model) {
					return fmt.Errorf("probe notifications disabled")
				}
				preferences, err := p.ops.GetEmailNotificationConfig(checkCtx)
				if err != nil {
					return err
				}
				if preferences == nil || !preferences.Alert.Enabled || !shouldSendOpsAlertEmailByMinSeverity(preferences.Alert.MinSeverity, "P1") || result.Outcome == "success" && !preferences.Alert.IncludeResolvedAlerts {
					return fmt.Errorf("ops notifications disabled")
				}
				return nil
			},
		})
		if err != nil {
			slog.Warn("group_probe_notification_failed", "group_id", result.GroupID)
		}
	}
}

func channelMonitorV3ProbeTargetEnabled(cfg *ChannelMonitorV3Config, groupID int64, model string) bool {
	if cfg == nil || !cfg.GroupProbe.Enabled {
		return false
	}
	for _, target := range cfg.GroupProbe.Groups {
		if target.GroupID == groupID && target.Model == model && target.Enabled {
			return true
		}
	}
	return false
}
