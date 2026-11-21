package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
)

// OperationalAlert carries internal identifiers and stable reason codes only.
// Never pass provider messages, customer content, or credential/object URLs.
type OperationalAlert struct {
	Component string
	Stage     string
	SubjectID string
	Code      string
}

type OperationalAlertDelivery struct {
	Alert OperationalAlert
	ID    int64
	Claim string
}

type OperationalAlertStore interface {
	EnqueueOperationalAlert(context.Context, OperationalAlert) error
	ClaimOperationalAlert(context.Context) (*OperationalAlertDelivery, error)
	FinishOperationalAlert(context.Context, OperationalAlertDelivery, bool) error
}

// OperationalAlerts isolates Feishu delivery and its retries from the workflow
// raising the incident. Each component can report through this same outbox.
type OperationalAlerts struct {
	store  OperationalAlertStore
	sender FeishuPaymentTextSender
	cancel context.CancelFunc
	done   chan struct{}
}

var operationalAlertIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,96}$`)

func ProvideOperationalAlerts(store OperationalAlertStore, cfg *config.Config) *OperationalAlerts {
	alerts := &OperationalAlerts{}
	if cfg != nil && cfg.FeishuPaymentAlerts.Enabled && store != nil {
		alerts.store = store
		alerts.sender = NewFeishuPaymentTextSender()
		ctx, cancel := context.WithCancel(context.Background())
		alerts.cancel = cancel
		alerts.done = make(chan struct{})
		go alerts.run(ctx)
	}
	return alerts
}

// Notify enqueues a durable, deduplicated alert, without calling Feishu inline.
func (s *OperationalAlerts) Notify(ctx context.Context, alert OperationalAlert) error {
	if s == nil || s.store == nil {
		return nil
	}
	for _, value := range []string{alert.Component, alert.Stage, alert.SubjectID, alert.Code} {
		if !operationalAlertIdentifier.MatchString(value) {
			return errors.New("invalid operational alert identifier")
		}
	}
	return s.store.EnqueueOperationalAlert(ctx, alert)
}

func (s *OperationalAlerts) observeBatchImage(ctx context.Context, batchID, stage, code string) {
	reportCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.Notify(reportCtx, OperationalAlert{Component: "image_studio", Stage: stage, SubjectID: batchID, Code: code}); err != nil {
		slog.WarnContext(ctx, "batch_image.alert_enqueue_failed", "batch_id", batchID, "stage", stage, "code", code)
	}
}

func (s *OperationalAlerts) Stop() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
	<-s.done
}

func (s *OperationalAlerts) run(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !runtimegate.SharedWorkAllowed() {
				continue
			}
			passCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			if err := s.deliverOne(passCtx); err != nil && passCtx.Err() == nil {
				slog.WarnContext(ctx, "operational_alert.delivery_pass_failed")
			}
			cancel()
		}
	}
}

func (s *OperationalAlerts) deliverOne(ctx context.Context) error {
	delivery, err := s.store.ClaimOperationalAlert(ctx)
	if err != nil || delivery == nil {
		return err
	}
	if !runtimegate.SharedWorkAllowed() || ctx.Err() != nil {
		return ctx.Err()
	}
	alert := delivery.Alert
	message := fmt.Sprintf("[Sub2API 异常通知 #%d]\n模块：%s\n阶段：%s\n记录：%s\n错误码：%s", delivery.ID, alert.Component, alert.Stage, alert.SubjectID, alert.Code)
	sendErr := s.sender.SendText(ctx, message)
	if sendErr != nil {
		slog.WarnContext(ctx, "operational_alert.feishu_retry", "alert_id", delivery.ID)
	}
	return s.store.FinishOperationalAlert(ctx, *delivery, sendErr == nil)
}
