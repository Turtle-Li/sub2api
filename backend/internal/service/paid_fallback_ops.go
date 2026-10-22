package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
)

const settingKeyPaidFallbackNotifications = "paid_fallback_notifications"

var errPaidFallbackNotificationSuppressed = errors.New("paid fallback notification no longer eligible")

type PaidFallbackNotificationSettings struct {
	Enabled                bool     `json:"enabled"`
	ReminderMinutes        int      `json:"reminder_minutes"`
	RecoveryQuietSeconds   int      `json:"recovery_quiet_seconds"`
	DailyCostUSD           *float64 `json:"daily_cost_usd"`
	UnfinishedAfterSeconds int      `json:"-"`
}

type PaidFallbackAttempt struct {
	ID               string    `json:"id"`
	InboundRequestID string    `json:"inbound_request_id"`
	APIKeyID         int64     `json:"api_key_id"`
	AccountID        int64     `json:"account_id"`
	GroupID          int64     `json:"group_id"`
	PublicModel      string    `json:"public_model"`
	ForwardModel     string    `json:"forward_model"`
	UpstreamModel    string    `json:"upstream_model"`
	Transport        string    `json:"transport"`
	Turn             int       `json:"turn"`
	StartedAt        time.Time `json:"started_at"`
	Outcome          string    `json:"outcome"`
	HTTPStatus       int       `json:"http_status"`
}

type PaidFallbackIncident struct {
	ID                  string     `json:"id"`
	GroupID             int64      `json:"group_id"`
	PublicModel         string     `json:"public_model"`
	Generation          int64      `json:"generation"`
	OpenedAt            time.Time  `json:"opened_at"`
	LastAttemptAt       time.Time  `json:"last_attempt_at"`
	ResolvedAt          *time.Time `json:"resolved_at"`
	Attempts            int64      `json:"attempts"`
	UnknownCostAttempts int64      `json:"unknown_cost_attempts"`
	RecordedCostUSD     float64    `json:"recorded_cost_usd"`
}

type PaidFallbackReport struct {
	Attempts  []PaidFallbackAttempt  `json:"attempts"`
	Incidents []PaidFallbackIncident `json:"incidents"`
}

type PaidFallbackDelivery struct {
	ID         string
	ClaimToken string
	Recipient  string
	Kind       string
	Summary    string
}

type PaidFallbackOpsStore interface {
	StartAttempt(context.Context, PaidFallbackAttempt) error
	FinishAttempt(context.Context, string, string, int) error
	Advance(context.Context, PaidFallbackNotificationSettings, []string) error
	Claim(context.Context, int) (*PaidFallbackDelivery, error)
	CanSend(context.Context, string, string) (bool, error)
	Complete(context.Context, string, string) error
	Suppress(context.Context, string, string) error
	Retry(context.Context, string, string) error
	Report(context.Context) (*PaidFallbackReport, error)
}

func (s *SettingService) GetPaidFallbackNotifications(ctx context.Context) (*PaidFallbackNotificationSettings, error) {
	result := &PaidFallbackNotificationSettings{ReminderMinutes: 30, RecoveryQuietSeconds: 60}
	raw, err := s.settingRepo.GetValue(ctx, settingKeyPaidFallbackNotifications)
	if errors.Is(err, ErrSettingNotFound) || err == nil && raw == "" {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), result); err != nil {
		return nil, err
	}
	if err := validatePaidFallbackNotifications(*result); err != nil {
		return nil, err
	}
	return result, nil
}

func validatePaidFallbackNotifications(value PaidFallbackNotificationSettings) error {
	if value.ReminderMinutes < 5 || value.ReminderMinutes > 1440 {
		return errors.New("reminder_minutes must be between 5 and 1440")
	}
	if value.RecoveryQuietSeconds < 30 || value.RecoveryQuietSeconds > 3600 {
		return errors.New("recovery_quiet_seconds must be between 30 and 3600")
	}
	if v := value.DailyCostUSD; v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v <= 0 || *v > 1e9) {
		return errors.New("daily_cost_usd must be positive or null")
	}
	return nil
}

func (s *SettingService) UpdatePaidFallbackNotifications(ctx context.Context, value PaidFallbackNotificationSettings) error {
	if err := validatePaidFallbackNotifications(value); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, settingKeyPaidFallbackNotifications, string(raw))
}

func (s *SettingService) PaidFallbackReport(ctx context.Context) (*PaidFallbackReport, error) {
	if s.paidFallbackOpsStore == nil {
		return nil, errors.New("paid fallback audit store unavailable")
	}
	return s.paidFallbackOpsStore.Report(ctx)
}

type PaidFallbackOpsService struct {
	store    PaidFallbackOpsStore
	settings *SettingService
	ops      *OpsService
	email    *NotificationEmailService
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewPaidFallbackOpsService(store PaidFallbackOpsStore, settings *SettingService, ops *OpsService, email *NotificationEmailService) *PaidFallbackOpsService {
	return &PaidFallbackOpsService{store: store, settings: settings, ops: ops, email: email}
}

func (s *PaidFallbackOpsService) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if !runtimegate.SharedWorkAllowed() {
				continue
			}
			tickCtx, tickCancel := context.WithTimeout(ctx, 20*time.Second)
			if err := s.tick(tickCtx); err != nil {
				slog.Warn("paid_fallback_ops_tick_failed", "error", err)
			}
			tickCancel()
		}
	}()
}

func (s *PaidFallbackOpsService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

func (s *PaidFallbackOpsService) tick(ctx context.Context) error {
	settings, err := s.settings.GetPaidFallbackNotifications(ctx)
	if err != nil {
		return err
	}
	settings.UnfinishedAfterSeconds = paidFallbackUnfinishedAfterSeconds(s.settings.cfg)
	config, err := s.ops.GetEmailNotificationConfig(ctx)
	if err != nil {
		return err
	}
	recipients := []string{}
	if settings.Enabled && config != nil && config.Alert.Enabled && shouldSendOpsAlertEmailByMinSeverity(config.Alert.MinSeverity, "warning") {
		for _, recipient := range config.Alert.Recipients {
			if value := strings.TrimSpace(recipient); value != "" {
				recipients = append(recipients, value)
			}
		}
	}
	if err := s.store.Advance(ctx, *settings, recipients); err != nil {
		return err
	}
	if len(recipients) == 0 || !runtimegate.SharedWorkAllowed() {
		return nil
	}
	delivery, err := s.store.Claim(ctx, config.Alert.RateLimitPerHour)
	if err != nil || delivery == nil {
		return err
	}
	allowedRecipient := false
	for _, recipient := range recipients {
		if recipient == delivery.Recipient {
			allowedRecipient = true
			break
		}
	}
	if !allowedRecipient || delivery.Kind == "recovered" && !config.Alert.IncludeResolvedAlerts {
		return s.store.Suppress(ctx, delivery.ID, delivery.ClaimToken)
	}
	sendCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-sendCtx.Done():
				return
			case <-ticker.C:
				if !runtimegate.SharedWorkAllowed() {
					cancel()
					return
				}
			}
		}
	}()
	err = s.email.Send(sendCtx, NotificationEmailSendInput{
		Event: NotificationEmailEventOpsAlert, SourceType: "paid_fallback_incident", SourceID: delivery.ID,
		RecipientEmail: delivery.Recipient,
		Variables:      map[string]string{"rule_name": "Paid fallback: " + delivery.Kind, "severity": "warning", "alert_status": delivery.Kind, "metric_type": "paid_fallback", "alert_description": delivery.Summary, "triggered_at": time.Now().UTC().Format(time.RFC3339)},
		BeforeSend: func(fenceCtx context.Context) error {
			if !runtimegate.SharedWorkAllowed() {
				return context.Canceled
			}
			current, err := s.settings.GetPaidFallbackNotifications(fenceCtx)
			if err != nil {
				return err
			}
			if !current.Enabled {
				return errPaidFallbackNotificationSuppressed
			}
			currentOps, err := s.ops.GetEmailNotificationConfig(fenceCtx)
			if err != nil {
				return err
			}
			if currentOps == nil || !currentOps.Alert.Enabled ||
				!shouldSendOpsAlertEmailByMinSeverity(currentOps.Alert.MinSeverity, "warning") ||
				delivery.Kind == "recovered" && !currentOps.Alert.IncludeResolvedAlerts {
				return errPaidFallbackNotificationSuppressed
			}
			stillRecipient := false
			for _, recipient := range currentOps.Alert.Recipients {
				if strings.TrimSpace(recipient) == delivery.Recipient {
					stillRecipient = true
					break
				}
			}
			if !stillRecipient {
				return errPaidFallbackNotificationSuppressed
			}
			allowed, err := s.store.CanSend(fenceCtx, delivery.ID, delivery.ClaimToken)
			if err != nil {
				return err
			}
			if !allowed {
				return errors.New("paid fallback delivery lease lost")
			}
			return nil
		},
	})
	resultCtx, resultCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer resultCancel()
	var postSend *notificationEmailPostSendError
	if errors.Is(err, errPaidFallbackNotificationSuppressed) {
		return s.store.Suppress(resultCtx, delivery.ID, delivery.ClaimToken)
	}
	if err == nil || errors.As(err, &postSend) {
		return s.store.Complete(resultCtx, delivery.ID, delivery.ClaimToken)
	}
	if retryErr := s.store.Retry(resultCtx, delivery.ID, delivery.ClaimToken); retryErr != nil {
		return fmt.Errorf("reschedule paid fallback notification: %w", retryErr)
	}
	return err
}

func paidFallbackUnfinishedAfterSeconds(cfg *config.Config) int {
	seconds := 900
	if cfg != nil {
		for _, value := range []int{cfg.Gateway.ResponseHeaderTimeout, cfg.Gateway.OpenAIResponseHeaderTimeout, cfg.Gateway.GrokResponseHeaderTimeout, cfg.Gateway.OpenAIWS.WriteTimeoutSeconds} {
			if value+120 > seconds {
				seconds = value + 120
			}
		}
	}
	return seconds
}
