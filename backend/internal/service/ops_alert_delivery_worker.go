package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	opsAlertFeishuRateLimit = 12 // Independent of the payment notification quota.
	opsAlertDeliveryLease   = 90 * time.Second
	opsAlertDeliveryPoll    = 2 * time.Second
)

// One shared rolling-hour quota survives active generation changes. Quota is
// consumed immediately before dispatch; transient failures count as attempts.
var opsAlertDeliveryRateScript = redis.NewScript(`
local now = tonumber(ARGV[1])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - 3600000)
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[2]) then return 0 end
redis.call('ZADD', KEYS[1], now, ARGV[3])
redis.call('PEXPIRE', KEYS[1], 3600000)
return 1
`)

func (s *OpsAlertEvaluatorService) planOpsAlertDeliveries(ctx context.Context, runtimeCfg *OpsAlertRuntimeSettings, rule *OpsAlertRule, event *OpsAlertEvent) ([]OpsAlertDeliveryTarget, error) {
	plan := make([]OpsAlertDeliveryTarget, 0, 2)
	if runtimeCfg != nil && isOpsAlertSilenced(time.Now(), rule, event, runtimeCfg.Silencing) {
		return plan, nil
	}
	if rule.NotifyEmail && s.emailService != nil && s.opsService != nil {
		cfg, err := s.opsService.GetEmailNotificationConfig(ctx)
		if err != nil {
			return nil, err
		}
		for _, recipient := range opsAlertEmailRecipients(cfg, rule, event.Severity) {
			plan = append(plan, OpsAlertDeliveryTarget{Channel: OpsAlertDeliveryEmail, Target: recipient})
		}
	}
	if rule.NotifyFeishu && s.feishuSender != nil {
		plan = append(plan, OpsAlertDeliveryTarget{Channel: OpsAlertDeliveryFeishu, Target: OpsAlertFeishuTarget})
	}
	return plan, nil
}

func opsAlertEmailRecipients(cfg *OpsEmailNotificationConfig, rule *OpsAlertRule, severity string) []string {
	if cfg == nil || rule == nil || !rule.NotifyEmail || !cfg.Alert.Enabled ||
		!shouldSendOpsAlertEmailByMinSeverity(cfg.Alert.MinSeverity, severity) {
		return nil
	}
	if len(rule.NotifyEmails) > 0 {
		addresses, err := NormalizeOpsAlertEmails(rule.NotifyEmails)
		if err != nil {
			return nil // Invalid saved rules never fall back to another audience.
		}
		return addresses
	}
	// The global list predates per-rule validation. Keep its valid recipients
	// while refusing malformed legacy values or header injection.
	var recipients []string
	seen := make(map[string]bool)
	for _, candidate := range cfg.Alert.Recipients {
		address, err := parseOpsAlertEmail(candidate)
		if key := strings.ToLower(address); err == nil && !seen[key] {
			seen[key] = true
			recipients = append(recipients, address)
		}
	}
	return recipients
}

func (s *OpsAlertEvaluatorService) runAlertDeliveries() {
	defer s.wg.Done()
	ticker := time.NewTicker(opsAlertDeliveryPoll)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			for range 8 {
				select {
				case <-s.stopCh:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				processed, err := s.processAlertDelivery(ctx)
				cancel()
				if err != nil {
					logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] delivery worker storage operation failed")
				}
				if !processed || err != nil {
					break
				}
			}
		}
	}
}

func (s *OpsAlertEvaluatorService) processAlertDelivery(ctx context.Context) (bool, error) {
	if s.opsRepo == nil || s.opsService == nil || !s.opsService.IsMonitoringEnabled(ctx) || !runtimegate.SharedWorkAllowed() {
		return false, nil
	}
	now := time.Now().UTC()
	delivery, err := s.opsRepo.ClaimAlertDelivery(ctx, now, uuid.NewString(), opsAlertDeliveryLease)
	if err != nil || delivery == nil {
		return false, err
	}
	return true, s.dispatchAlertDelivery(ctx, delivery, now)
}

func (s *OpsAlertEvaluatorService) dispatchAlertDelivery(ctx context.Context, delivery *OpsAlertDelivery, now time.Time) error {
	deadline := delivery.CreatedAt.Add(OpsAlertDeliveryLifetime)
	if !now.Before(deadline) || delivery.AttemptCount >= OpsAlertDeliveryMaxAttempts {
		return s.finishAlertDelivery(ctx, delivery, OpsAlertDeliveryFailed, now, "delivery_budget_exhausted")
	}
	deferPolicy := func(reason string) error {
		return s.finishAlertDelivery(ctx, delivery, OpsAlertDeliveryPending, minOpsAlertTime(now.Add(time.Minute), deadline), reason)
	}
	event, err := s.opsRepo.GetAlertEventByID(ctx, delivery.EventID)
	if err != nil || event == nil {
		return deferPolicy("event_unavailable")
	}
	rule, err := s.opsRepo.GetAlertRuleByID(ctx, event.RuleID)
	if err != nil {
		return deferPolicy("rule_unavailable")
	}
	if rule == nil || !rule.Enabled || rule.MetricType == OpsAlertMetricGoodPutVideoIncident {
		return s.finishAlertDelivery(ctx, delivery, OpsAlertDeliverySkipped, now, "rule_disabled")
	}
	silenced, err := s.opsAlertDeliverySilenced(ctx, rule, event, now)
	if err != nil {
		return deferPolicy("silence_unavailable")
	}
	if silenced {
		return s.finishAlertDelivery(ctx, delivery, OpsAlertDeliverySkipped, now, "silenced")
	}
	limit, enabled, err := s.opsAlertDeliveryPolicy(ctx, delivery, rule, event)
	if err != nil {
		return deferPolicy("channel_policy_unavailable")
	}
	if !enabled {
		return s.finishAlertDelivery(ctx, delivery, OpsAlertDeliverySkipped, now, "channel_disabled")
	}
	if !runtimegate.SharedWorkAllowed() || !s.opsService.IsMonitoringEnabled(ctx) || ctx.Err() != nil {
		return deferPolicy("standby")
	}
	allowed, err := s.allowOpsAlertDelivery(ctx, delivery.Channel, limit, time.Now().UTC())
	if err != nil || !allowed {
		return deferPolicy("rate_limited")
	}
	if !runtimegate.SharedWorkAllowed() || ctx.Err() != nil {
		return deferPolicy("standby")
	}
	started, err := s.opsRepo.StartAlertDelivery(ctx, delivery, time.Now().UTC())
	if err != nil || !started {
		return err
	}
	if !runtimegate.SharedWorkAllowed() || !s.opsService.IsMonitoringEnabled(ctx) || ctx.Err() != nil {
		return deferPolicy("standby")
	}
	sendCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if delivery.Channel == OpsAlertDeliveryFeishu {
		err = s.feishuSender.SendText(context.WithValue(sendCtx, opsAlertSharedSendKey{}, true), buildOpsAlertFeishuText(rule, event))
	} else {
		err = s.sendOpsAlertEmail(sendCtx, rule, event, delivery.Target)
	}
	now = time.Now().UTC()
	if err == nil {
		return s.finishAlertDelivery(ctx, delivery, OpsAlertDeliverySent, now, "")
	}
	if delivery.AttemptCount+1 >= OpsAlertDeliveryMaxAttempts || !now.Before(deadline) {
		return s.finishAlertDelivery(ctx, delivery, OpsAlertDeliveryFailed, now, "send_failed")
	}
	next := minOpsAlertTime(now.Add((10*time.Second)<<delivery.AttemptCount), deadline)
	return s.finishAlertDelivery(ctx, delivery, OpsAlertDeliveryPending, next, "send_failed")
}

func minOpsAlertTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func (s *OpsAlertEvaluatorService) finishAlertDelivery(ctx context.Context, delivery *OpsAlertDelivery, status string, next time.Time, code string) error {
	// An acknowledged remote send must be persisted despite attempt timeout or
	// an active-generation handoff. The claim token still fences the update.
	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	claimed, err := s.opsRepo.FinishAlertDelivery(ackCtx, delivery, OpsAlertDeliveryOutcome{
		Status: status, NextAttempt: next, ErrorCode: code, Now: time.Now().UTC(),
	})
	if err == nil && !claimed {
		return fmt.Errorf("ops alert delivery claim lost")
	}
	return err
}

func (s *OpsAlertEvaluatorService) opsAlertDeliverySilenced(ctx context.Context, rule *OpsAlertRule, event *OpsAlertEvent, now time.Time) (bool, error) {
	runtimeCfg, err := s.opsService.GetOpsAlertRuntimeSettings(ctx)
	if err != nil {
		return false, err
	}
	if runtimeCfg != nil && isOpsAlertSilenced(now, rule, event, runtimeCfg.Silencing) {
		return true, nil
	}
	platform, groupID, region := parseOpsAlertRuleScope(event.Dimensions)
	if platform == "" {
		return false, nil
	}
	return s.opsRepo.IsAlertSilenced(ctx, rule.ID, platform, groupID, region, now)
}

func (s *OpsAlertEvaluatorService) opsAlertDeliveryPolicy(ctx context.Context, delivery *OpsAlertDelivery, rule *OpsAlertRule, event *OpsAlertEvent) (int, bool, error) {
	switch delivery.Channel {
	case OpsAlertDeliveryFeishu:
		return opsAlertFeishuRateLimit, s.feishuSender != nil && rule.NotifyFeishu && delivery.Target == OpsAlertFeishuTarget, nil
	case OpsAlertDeliveryEmail:
		if s.emailService == nil || !rule.NotifyEmail {
			return 0, false, nil
		}
		cfg, err := s.opsService.GetEmailNotificationConfig(ctx)
		if err != nil || cfg == nil {
			return 0, false, err
		}
		return cfg.Alert.RateLimitPerHour, slices.Contains(opsAlertEmailRecipients(cfg, rule, event.Severity), delivery.Target), nil
	default:
		return 0, false, nil
	}
}

func (s *OpsAlertEvaluatorService) allowOpsAlertDelivery(ctx context.Context, channel string, limit int, now time.Time) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	if s.redisClient != nil {
		allowed, err := opsAlertDeliveryRateScript.Run(ctx, s.redisClient, []string{"ops:alert:delivery-rate:" + channel}, now.UnixMilli(), limit, uuid.NewString()).Int()
		return allowed == 1, err
	}
	limiter := s.emailLimiter
	if channel == OpsAlertDeliveryFeishu {
		limiter = s.feishuLimiter
	}
	if limiter == nil {
		return false, fmt.Errorf("ops delivery rate limiter unavailable")
	}
	limiter.SetLimit(limit)
	return limiter.Allow(now), nil
}

type opsAlertSharedSendKey struct{}
