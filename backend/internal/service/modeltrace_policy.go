package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/modeltrace"
)

type ModelTraceAlertState struct {
	ExpectedModel string    `json:"expected_model"`
	DetectedModel string    `json:"detected_model"`
	NotifiedAt    time.Time `json:"notified_at"`
}

func modelTraceBankHasModel(id string) bool {
	for _, candidate := range modeltrace.GetActiveBank().Models {
		if candidate.ID == id {
			return true
		}
	}
	return false
}

func modelTraceMatchStatus(record *ModelTraceRecord, cfg ModelTraceGlobalConfig) string {
	if record.Error != "" || record.Probability <= 0 || record.Probability > 1 ||
		math.IsNaN(record.Probability) || math.IsInf(record.Probability, 0) {
		return "inconclusive"
	}
	expected := strings.ToLower(strings.TrimSpace(record.ExpectedModel))
	detected := strings.ToLower(strings.TrimSpace(record.DetectedModel))
	if !modelTraceBankHasModel(detected) {
		return "inconclusive"
	}
	if expected == detected {
		return "match"
	}
	allowed := cfg.AllowedDetections[expected]
	for _, candidate := range allowed {
		if candidate == detected {
			return "match"
		}
	}
	if !modelTraceBankHasModel(expected) && len(allowed) == 0 {
		return "uncovered"
	}
	return "mismatch"
}

func (s *ModelTraceService) applyMismatchPolicy(ctx context.Context, account *Account, model *ModelTraceModelConfig, cfg ModelTraceGlobalConfig) {
	record := model.Latest
	if cfg.PauseOnMismatch {
		if !account.Schedulable {
			record.AccountPaused = true
		} else if err := s.accountRepo.SetSchedulable(ctx, account.ID, false); err != nil {
			record.ActionError = "Could not pause account scheduling"
		} else {
			record.AccountPaused = true
		}
	}
	if !cfg.NotifyOnMismatch {
		return
	}
	if alert := model.Alert; alert != nil && alert.ExpectedModel == record.ExpectedModel &&
		alert.DetectedModel == record.DetectedModel &&
		record.TestedAt.Before(alert.NotifiedAt.Add(time.Duration(cfg.NotificationCooldownMinutes)*time.Minute)) {
		record.NotificationStatus = "cooldown"
		return
	}
	action := "仅记录异常"
	if record.AccountPaused {
		action = "账号业务调度已暂停，需管理员手动恢复"
	} else if record.ActionError != "" {
		action = "暂停账号失败，请人工处理"
	}
	message := fmt.Sprintf("【Sub2】ModelTrace 指纹异常\n账号 ID：%d\n预期模型：%q\n检测指纹：%q\n归因概率：%.1f%%\n处理：%s\n检测时间：%s",
		account.ID, record.ExpectedModel, record.DetectedModel, record.Probability*100, action, record.TestedAt.UTC().Format(time.RFC3339))
	if s.notificationSender == nil || s.notificationSender.SendText(ctx, message) != nil {
		record.NotificationStatus = "failed"
		return
	}
	record.NotificationStatus = "sent"
	model.Alert = &ModelTraceAlertState{
		ExpectedModel: record.ExpectedModel,
		DetectedModel: record.DetectedModel,
		NotifiedAt:    record.TestedAt,
	}
}
