//go:build integration

package repository

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2AggregatesHTTPFinalOutcomeOnce(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	group := mustCreateGroup(t, client, &service.Group{Name: "monitor-outcomes-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &group.ID})
	otherKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &group.ID})
	account := mustCreateAccount(t, client, &service.Account{Name: "monitor-outcomes-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	usage := NewUsageLogRepository(client, integrationDB)
	ops := NewOpsRepository(integrationDB)
	v2 := NewChannelMonitorV2Repository(integrationDB)
	v3 := NewChannelMonitorV3Repository(integrationDB)

	start := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	minuteEnd := start.Add(time.Minute)
	end := start.Add(5 * time.Minute)
	const model = "gpt-5.5"
	ttft, duration := 300, 1500
	failed, cancelled, recovered, retried := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, sample := range []struct {
		id    string
		keyID int64
	}{
		{"client:" + failed, key.ID},
		{"client:" + cancelled, key.ID},
		{"client:" + recovered, key.ID},
		{"client:" + retried, key.ID},
		{"client:" + failed, otherKey.ID},
	} {
		created, err := usage.Create(ctx, &service.UsageLog{
			UserID: user.ID, APIKeyID: sample.keyID, GroupID: &group.ID, AccountID: account.ID,
			RequestID: sample.id, Model: model, RequestedModel: model, RequestType: service.RequestTypeStream,
			ActualCost: 0.01, InputTokens: 10, OutputTokens: 20, FirstTokenMs: &ttft, DurationMs: &duration,
			CreatedAt: minuteEnd.Add(-time.Second),
		})
		require.NoError(t, err)
		require.True(t, created)
	}

	// Simulate terminal error logs becoming visible after the usage minute was computed.
	require.NoError(t, v2.RecomputeRange(ctx, start, minuteEnd))
	upstreamStatus := http.StatusBadGateway
	record := func(requestID, gatewayID string, status int, message string, at time.Time) {
		t.Helper()
		_, err := ops.InsertErrorLog(ctx, &service.OpsInsertErrorLogInput{
			RequestID: requestID, ClientRequestID: gatewayID, APIKeyID: &key.ID, UserID: &user.ID,
			GroupID: &group.ID, AccountID: &account.ID, Platform: service.PlatformOpenAI,
			Model: model, RequestedModel: model, Stream: true, ErrorPhase: "upstream", ErrorType: "api_error",
			ErrorOwner: "provider", ErrorSource: "upstream_http", StatusCode: status, Severity: "P1",
			ErrorMessage: message, UpstreamStatusCode: &upstreamStatus, CreatedAt: at,
		})
		require.NoError(t, err)
	}
	// The externally supplied X-Request-ID is not the ID of the gateway request.
	record(retried, failed, http.StatusBadGateway, "upstream failed", minuteEnd)
	record(retried, failed, http.StatusBadGateway, "upstream failed", minuteEnd.Add(time.Second))
	record(uuid.NewString(), cancelled, 499, "client cancelled", minuteEnd)
	record(uuid.NewString(), recovered, http.StatusOK, "recovered upstream failure", minuteEnd)

	require.NoError(t, v2.RecomputeRange(ctx, start, minuteEnd))
	readSuccess := func() int64 {
		t.Helper()
		facts, err := v3.SlotFacts(ctx, []int64{group.ID}, start, end, 5*time.Minute, true)
		require.NoError(t, err)
		require.Len(t, facts.Metrics, 1)
		return facts.Metrics[0].Success
	}
	require.EqualValues(t, 3, readSuccess(), "a later terminal error supersedes partial usage in the earlier minute")

	require.NoError(t, v2.RecomputeRange(ctx, start, end))
	facts, err := v3.SlotFacts(ctx, []int64{group.ID}, start, end, 5*time.Minute, true)
	require.NoError(t, err)
	require.Len(t, facts.Metrics, 1)
	require.EqualValues(t, 3, facts.Metrics[0].Success)
	require.EqualValues(t, 2, facts.Metrics[0].Errors)
	categories := map[string]int64{}
	for _, fact := range facts.Errors {
		categories[fact.Category] += fact.Errors
	}
	require.Equal(t, map[string]int64{"upstream_5xx": 1, "client_cancelled": 1}, categories)
	require.Len(t, facts.Latency, 1)
	require.EqualValues(t, 3, facts.Latency[0].Count)

	ranged, err := v3.RangeFacts(ctx, []int64{group.ID}, start, start.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, ranged.Metrics, 1)
	require.EqualValues(t, 3, ranged.Metrics[0].Success)
	require.EqualValues(t, 2, ranged.Metrics[0].Errors)

	var userSuccess, userErrors, outputTokens, userTTFT, userDuration int64
	err = integrationDB.QueryRowContext(ctx, `
		SELECT SUM(success_requests), SUM(error_requests), SUM(output_tokens), SUM(ttft_count), SUM(duration_count)
		FROM channel_monitor_v2_user_metrics_1m WHERE group_id = $1 AND user_id = $2
	`, group.ID, user.ID).Scan(&userSuccess, &userErrors, &outputTokens, &userTTFT, &userDuration)
	require.NoError(t, err)
	require.EqualValues(t, 3, userSuccess)
	require.EqualValues(t, 2, userErrors)
	require.EqualValues(t, 100, outputTokens, "partial output remains included in token volume")
	require.EqualValues(t, 3, userTTFT)
	require.EqualValues(t, 3, userDuration)
}
