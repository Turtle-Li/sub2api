//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/modeltrace"
	"github.com/stretchr/testify/require"
)

func TestModelTraceProbeAppliesPolicyAfterRealAttribution(t *testing.T) {
	text := strings.Repeat("123 ", 332)
	prediction, err := modeltrace.AnalyzeOutputs([]modeltrace.OutputItem{{Text: text, ExpectedCount: 300}})
	require.NoError(t, err)
	for _, allowed := range []bool{false, true} {
		cfg := ModelTraceGlobalConfig{ProbeCount: 1, NotifyOnMismatch: true, PauseOnMismatch: true}
		expected := "gpt-5.4"
		if prediction.Prediction == expected {
			expected = "gpt-5.5"
		}
		if allowed {
			cfg.AllowedDetections = map[string][]string{expected: {prediction.Prediction}}
		}
		f := newModelTracePolicyFixture(t, cfg)
		f.repo.account.Credentials["model_mapping"] = map[string]any{"public-gpt": expected}
		upstream := &queuedHTTPUpstream{}
		upstream.responses = append(upstream.responses, modelTraceCompletion(text))
		f.svc.accountTestSvc = &AccountTestService{accountRepo: f.repo, httpUpstream: upstream}
		record, err := f.svc.ProbeModel(context.Background(), 55, "public-gpt")
		require.NoError(t, err)
		require.Empty(t, record.Error)
		require.Len(t, upstream.requests, 1)
		require.Equal(t, expected, record.ExpectedModel)
		if allowed {
			require.Equal(t, "match", record.MatchStatus)
			require.Empty(t, f.sender.messages)
			require.True(t, f.repo.account.Schedulable)
		} else {
			require.Equal(t, "mismatch", record.MatchStatus)
			require.Equal(t, "sent", record.NotificationStatus)
			require.False(t, f.repo.account.IsSchedulable())
		}
	}
}
