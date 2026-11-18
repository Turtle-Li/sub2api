package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
	"github.com/Wei-Shaw/sub2api/internal/pkg/lmdetector"
)

const (
	modelTraceLMDetector             = "lm-detector"
	modelTraceProbabilityUnavailable = "unavailable"
)

func (s *ModelTraceService) executeLMDetectorProbe(ctx context.Context, account *Account, modelID string) *ModelTraceRecord {
	record := &ModelTraceRecord{
		Detector:          modelTraceLMDetector,
		DetectedModel:     "Unknown",
		ProbabilityStatus: modelTraceProbabilityUnavailable,
		MatchStatus:       "inconclusive",
	}
	startedAt := time.Now()
	defer func() {
		record.TestedAt = time.Now()
		record.LatencyMs = record.TestedAt.Sub(startedAt).Milliseconds()
	}()
	if s.accountTestSvc == nil {
		record.Error = "accountTestService is not available"
		return record
	}
	detector, err := lmdetector.Load()
	if err != nil {
		record.Error = fmt.Sprintf("load LM Detector: %v", err)
		return record
	}
	cfg := s.GetGlobalConfig(ctx)
	challenges, err := lmfpd.GenerateChallenges(min(cfg.ProbeCount, 3))
	if err != nil {
		record.Error = err.Error()
		return record
	}
	ctx = context.WithValue(ctx, modelTraceRequestsKey{}, &record.Requests)
	interval := time.Duration(cfg.IntervalSeconds) * time.Second
	effort := modelTraceReasoningEffortFor(account, modelID, cfg.ReasoningEffort)
	outputs := make([]lmfpd.Output, 0, len(challenges))
	var lastErr error
	received := 0
	for index, challenge := range challenges {
		if ctx.Err() != nil {
			return record
		}
		if index > 0 {
			select {
			case <-ctx.Done():
				return record
			case <-time.After(interval):
			}
			if err := s.checkModelTraceMonitor(ctx, account.ID, modelID); err != nil {
				record.Error = err.Error()
				return record
			}
		}
		text, err := s.requestCompletion(ctx, account, modelID, challenge.Prompt, effort)
		if err != nil {
			lastErr = err
			text = ""
		} else {
			received++
		}
		outputs = append(outputs, lmfpd.Output{Text: text, ExpectedCount: challenge.ExpectedCount})
		if modelTraceTerminalStatus(err) {
			break
		}
	}
	if received == 0 && lastErr != nil {
		record.Error = lastErr.Error()
		return record
	}
	analysis, err := detector.Analyze(outputs, false)
	if err != nil {
		record.Error = err.Error()
		return record
	}
	for _, diagnostic := range analysis.Diagnostics {
		request := &record.Requests[diagnostic.Index]
		request.ValidSample = diagnostic.Accepted
		if !diagnostic.Accepted && request.Error == "" {
			request.Error = "有效数字不足"
		}
	}
	if analysis.Prediction == "" {
		return record
	}
	record.DetectedModel = analysis.Prediction
	if analysis.Decision == "scored" && analysis.ProbabilityStatus == "reference_calibrated" && analysis.Probability != nil &&
		*analysis.Probability > 0 && *analysis.Probability <= 1 {
		record.Probability = *analysis.Probability
		record.ProbabilityStatus = analysis.ProbabilityStatus
	}
	expected := strings.ToLower(strings.TrimSpace(account.GetMappedModel(modelID)))
	if !slices.ContainsFunc(analysis.Results, func(candidate lmfpd.AnalysisResult) bool { return candidate.Model == expected }) {
		record.MatchStatus = "uncovered"
	} else if record.ProbabilityStatus == "reference_calibrated" && record.DetectedModel == expected {
		record.MatchStatus = "match"
	}
	return record
}
