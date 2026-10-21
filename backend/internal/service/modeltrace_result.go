package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

func (s *ModelTraceService) saveProbeResult(ctx context.Context, accountID int64, modelID string, record *ModelTraceRecord) (*ModelTraceRecord, error) {
	ctx = context.WithoutCancel(ctx)
	saveCtx, saveCancel := context.WithTimeout(ctx, 5*time.Second)
	defer saveCancel()
	mu := s.getConfigLock(accountID)
	mu.Lock()
	defer mu.Unlock()
	account, err := s.accountRepo.GetByID(saveCtx, accountID)
	if err != nil {
		return nil, err
	}
	cfg := DecodeModelTraceConfig(account.Extra)
	if cfg == nil {
		logger.FromContext(ctx).Info("modeltrace.result_discarded", zap.String("reason", "monitor_removed"))
		return record, nil
	}
	model, exists := cfg.Models[modelID]
	if !exists {
		logger.FromContext(ctx).Info("modeltrace.result_discarded", zap.String("reason", "model_removed"))
		return record, nil
	}
	if model == nil {
		model = &ModelTraceModelConfig{}
		cfg.Models[modelID] = model
	}
	policy := s.GetGlobalConfig(saveCtx)
	record.MatchStatus = modelTraceMatchStatus(record, policy)
	if !modelTraceAccountSupported(account) || !strings.EqualFold(strings.TrimSpace(account.GetMappedModel(modelID)), record.ExpectedModel) {
		record.MatchStatus = "inconclusive"
		record.ActionError = "Model mapping changed during probe; repeat the probe"
	}
	if model.Latest != nil {
		model.History = append([]ModelTraceRecord{*model.Latest}, model.History...)
		if len(model.History) > ModelTraceMaxHistory {
			model.History = model.History[:ModelTraceMaxHistory]
		}
	}
	model.Latest = record
	if record.MatchStatus == "match" {
		model.Alert = nil
	}
	now := time.Now()
	cfg.LastRunAt = &now
	if err := s.accountRepo.UpdateExtra(saveCtx, accountID, map[string]any{ModelTraceExtraKey: cfg}); err != nil {
		return nil, fmt.Errorf("save modeltrace result: %w", err)
	}
	if record.MatchStatus == "mismatch" && (policy.NotifyOnMismatch || policy.PauseOnMismatch) {
		actionCtx, actionCancel := context.WithTimeout(ctx, 15*time.Second)
		defer actionCancel()
		s.applyMismatchPolicy(actionCtx, account, model, policy)
		outcomeCtx, outcomeCancel := context.WithTimeout(ctx, 5*time.Second)
		defer outcomeCancel()
		if err := s.accountRepo.UpdateExtra(outcomeCtx, accountID, map[string]any{ModelTraceExtraKey: cfg}); err != nil {
			return nil, fmt.Errorf("save modeltrace action outcome: %w", err)
		}
	}
	logger.FromContext(ctx).Info("modeltrace.result_saved", zap.Bool("success", record.Error == ""),
		zap.String("match_status", record.MatchStatus), zap.Bool("account_paused", record.AccountPaused),
		zap.String("notification_status", record.NotificationStatus), zap.String("action_error", record.ActionError))
	return record, nil
}
