package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestIsOpenAIWSTokenEvent_TerminalEventsExcluded 覆盖 isOpenAIWSTokenEvent 的回归用例。
// 重点验证终止事件（response.completed / response.done）不再被当作 token event，
// 否则当上游没有可识别的 delta 时，firstTokenMs 会被填到终止时刻，
// 等于把"总耗时"误报为"首 token 延迟"（issue #2651）。
func TestIsOpenAIWSTokenEvent_TerminalEventsExcluded(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		want      bool
	}{
		{name: "empty", eventType: "", want: false},
		{name: "whitespace_trimmed_empty", eventType: "   ", want: false},

		{name: "response.created", eventType: "response.created", want: false},
		{name: "response.in_progress", eventType: "response.in_progress", want: false},
		{name: "response.output_item.added", eventType: "response.output_item.added", want: false},
		{name: "response.output_item.done", eventType: "response.output_item.done", want: false},

		{name: "terminal_response.completed", eventType: "response.completed", want: false},
		{name: "terminal_response.done", eventType: "response.done", want: false},
		{name: "terminal_response.completed_padded", eventType: "  response.completed  ", want: false},
		{name: "terminal_response.done_padded", eventType: "  response.done  ", want: false},

		{name: "delta_text", eventType: "response.output_text.delta", want: true},
		{name: "delta_audio_transcript", eventType: "response.audio_transcript.delta", want: true},
		{name: "delta_function_call_arguments", eventType: "response.function_call_arguments.delta", want: true},

		{name: "output_text_done", eventType: "response.output_text.done", want: true},
		{name: "output_text_annotation_added", eventType: "response.output_text.annotation.added", want: true},

		{name: "output_audio_done", eventType: "response.output_audio.done", want: true},

		{name: "reasoning_summary_delta", eventType: "response.reasoning_summary_text.delta", want: true},

		{name: "unrelated_event_error", eventType: "error", want: false},
		{name: "unknown_event_without_match", eventType: "response.reasoning_summary_part.added", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := isOpenAIWSTokenEvent(tc.eventType)
			require.Equal(t, tc.want, got, "isOpenAIWSTokenEvent(%q)", tc.eventType)
		})
	}
}

func TestOpenAIWSIngressEventStartsUnsafeOutput(t *testing.T) {
	cases := []struct {
		eventType string
		want      bool
	}{
		{eventType: "response.created", want: false},
		{eventType: "response.in_progress", want: false},
		{eventType: "response.queued", want: false},
		{eventType: "error", want: false},
		{eventType: "response.output_text.delta", want: true},
		{eventType: "response.output_item.added", want: true},
		{eventType: "response.completed", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.eventType, func(t *testing.T) {
			require.Equal(t, tc.want, openAIWSIngressEventStartsUnsafeOutput(tc.eventType, nil))
		})
	}
}

func TestOpenAIWSCapacityShedErrorClassification(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		typ     string
		message string
		want    bool
	}{
		{name: "server overloaded code", code: "server_is_overloaded", want: true},
		{name: "slow down code", code: "slow_down", want: true},
		{name: "overloaded message", message: "Our servers are currently overloaded. Please try again later.", want: true},
		{name: "our servers overloaded message", message: "Our servers are overloaded. Please try again later.", want: true},
		{name: "singular server overloaded message", message: "The server is overloaded. Please retry.", want: true},
		{name: "slow down message", message: "Please slow down and retry later.", want: true},
		{name: "model capacity message", message: "Selected model is at capacity. Please try a different model.", want: true},
		{name: "ordinary server error", code: "server_error", typ: "server_error", message: "Internal error", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isOpenAIWSCapacityShedError(tc.code, tc.typ, tc.message))
		})
	}
	require.Equal(t, http.StatusServiceUnavailable, openAIWSErrorHTTPStatusFromRaw("server_is_overloaded", ""))
	require.Equal(t, http.StatusServiceUnavailable, openAIWSErrorHTTPStatusFromRaw("slow_down", ""))
	require.Equal(t, http.StatusServiceUnavailable, openAIWSErrorHTTPStatusFromEventFields("", "", "Our servers are overloaded. Please try again later."))
	require.Equal(t, http.StatusServiceUnavailable, openAIWSErrorHTTPStatus([]byte(`{"type":"error","error":{"message":"Our servers are overloaded. Please try again later."}}`)))
	require.Equal(t, http.StatusTooManyRequests, openAIWSErrorHTTPStatusFromEventFields("rate_limit_exceeded", "rate_limit_error", "Rate limit reached; please slow down."))
	require.False(t, isOpenAIWSRequestScopedCapacityShed("rate_limit_exceeded", "rate_limit_error", "Rate limit reached; please slow down."))
	require.True(t, isOpenAIWSRequestScopedCapacityShed("", "", "Our servers are overloaded. Please try again later."))
}

func TestOpenAIWSCapacityShedIsRecordedForOps(t *testing.T) {
	payload := []byte(`{"type":"error","error":{"code":"server_is_overloaded","type":"service_unavailable_err","message":"Our servers are currently overloaded. Please try again later."}}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	svc := &OpenAIGatewayService{}
	svc.recordOpenAIStreamUpstreamError(c, &Account{ID: 7, Platform: PlatformOpenAI, Name: "oauth-pro"}, false, "", "ws_error_event", payload, "Our servers are currently overloaded. Please try again later.")

	status, ok := c.Get(OpsUpstreamStatusCodeKey)
	require.True(t, ok)
	require.Equal(t, http.StatusServiceUnavailable, status)
	rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "ws_error_event", events[0].Kind)
	require.Equal(t, int64(7), events[0].AccountID)
}

// TestOpenAIWSCyberPolicyMark_ResponseFailed 验证 WS 路径 response.failed cyber_policy 标记逻辑。
//
// 全量转发循环（forwardOpenAIWSV2 / sendAndRelay）依赖真实 WebSocket 连接，
// 无法在单元测试中驱动。本测试通过直接调用转发循环内使用的两个函数
// detectOpenAICyberPolicy + MarkOpsCyberPolicy，覆盖「从 response.failed 帧
// 到 gin context 写入」的完整调用序列，等同于循环体内对应代码段的逻辑验证。
// 全量 WS 端到端覆盖由后续集成测试（Task 12 handler 编排）承担。
func TestOpenAIWSCyberPolicyMark_ResponseFailed(t *testing.T) {
	// 构造一个真实的 response.failed 帧（cyber_policy 命中路径）。
	payload := []byte(`{"type":"response.failed","response":{"id":"resp_abc","status":"failed","error":{"code":"cyber_policy","message":"Request blocked by content policy."}}}`)

	// 验证 detectOpenAICyberPolicy 能从 response.error.code 路径识别。
	hit, code, msg := detectOpenAICyberPolicy(payload)
	require.True(t, hit, "detectOpenAICyberPolicy should return true for cyber_policy payload")
	require.Equal(t, "cyber_policy", code)
	require.Equal(t, "Request blocked by content policy.", msg)

	// 构造 gin test context，模拟转发循环调用 MarkOpsCyberPolicy。
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	usage := OpenAIUsage{InputTokens: 42, OutputTokens: 7}
	MarkOpsCyberPolicy(c, CyberPolicyMark{
		Code:           code,
		Message:        msg,
		Body:           truncateString(string(payload), 4096),
		UpstreamStatus: 200,
		UpstreamInTok:  usage.InputTokens,
		UpstreamOutTok: usage.OutputTokens,
	})

	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark, "GetOpsCyberPolicy should return non-nil after MarkOpsCyberPolicy")
	require.Equal(t, "cyber_policy", mark.Code)
	require.Equal(t, "Request blocked by content policy.", mark.Message)
	require.Equal(t, 200, mark.UpstreamStatus)
	require.Equal(t, 42, mark.UpstreamInTok)
	require.Equal(t, 7, mark.UpstreamOutTok)

	// 验证幂等性：再次标记不覆盖首个。
	MarkOpsCyberPolicy(c, CyberPolicyMark{Code: "cyber_policy", Message: "second call"})
	require.Equal(t, "Request blocked by content policy.", GetOpsCyberPolicy(c).Message, "second MarkOpsCyberPolicy call must not overwrite first")
}

// TestOpenAIWSCyberPolicyMark_NonCyberPayload 验证非 cyber_policy 的 response.failed 不触发标记。
func TestOpenAIWSCyberPolicyMark_NonCyberPayload(t *testing.T) {
	payload := []byte(`{"type":"response.failed","response":{"id":"resp_xyz","status":"failed","error":{"code":"server_error","message":"Internal error"}}}`)

	hit, _, _ := detectOpenAICyberPolicy(payload)
	require.False(t, hit, "detectOpenAICyberPolicy should return false for non-cyber_policy error code")
}

func TestOpenAIForwardResultSucceededForScheduling_TerminalEvents(t *testing.T) {
	tests := []struct {
		name     string
		result   *OpenAIForwardResult
		expected bool
	}{
		{name: "nil legacy result", result: nil, expected: true},
		{name: "non websocket zero value", result: &OpenAIForwardResult{}, expected: true},
		{name: "websocket legacy empty terminal", result: &OpenAIForwardResult{OpenAIWSMode: true}, expected: true},
		{name: "completed", result: &OpenAIForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.completed"}, expected: true},
		{name: "done", result: &OpenAIForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.done"}, expected: true},
		{name: "failed", result: &OpenAIForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.failed"}, expected: false},
		{name: "incomplete", result: &OpenAIForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.incomplete"}, expected: false},
		{name: "cancelled", result: &OpenAIForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.cancelled"}, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.result.SucceededForScheduling())
		})
	}
}

func TestOpenAIWSTerminalEvent_ResponseFailedRecordsModelTransient(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 5201, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"server_error","message":"Internal error"}}}`)

	for range 2 {
		terminalEvent := svc.handleOpenAIWSTerminalTransientFailure(context.Background(), account, "gpt-5.5", http.Header{}, payload)
		require.Equal(t, "response.failed", terminalEvent)
	}

	require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.5"))
}

func TestOpenAIWSErrorEvent_ServerErrorRecordsModelTransient(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 5203, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	payload := []byte(`{"type":"error","error":{"code":"server_error","type":"server_error","message":"Internal error"}}`)

	for range 2 {
		svc.handleOpenAIWSErrorEventTransientFailure(context.Background(), account, "gpt-5.5", http.Header{}, payload)
	}

	require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.5"))
}

func TestOpenAIWSPayloadTransientStatus_Explicit529IsNotModelTransient(t *testing.T) {
	payload := []byte(`{"type":"response.failed","response":{"error":{"status_code":529,"code":"server_error","message":"overloaded"}}}`)

	require.Zero(t, openAIWSPayloadTransientStatus(payload))
}

func TestOpenAIWSDial5xxRecordsModelTransient(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.rateLimitService = NewRateLimitService(transientCooldownAccountRepo{}, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 5202, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	dialErr := &openAIWSDialError{
		StatusCode:      http.StatusBadGateway,
		ResponseHeaders: http.Header{"X-Request-Id": []string{"req-ws-502"}},
		ResponseBody:    []byte(`{"error":{"message":"bad gateway"}}`),
	}

	for range 2 {
		svc.handleOpenAIWSDialTransientFailure(context.Background(), account, "gpt-5.5", dialErr)
	}

	require.Eventually(t, func() bool {
		return svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.5")
	}, time.Second, 10*time.Millisecond)
}

// TestIsOpenAIWSTokenEvent_DisjointWithTerminal 守护「token 事件集合与终止事件集合互斥」的不变量。
// firstTokenMs 的计算依赖于 isTokenEvent && !isTerminalEvent；
// 若两者再次出现交集，则 issue #2651 描述的 latency 误报会重现。
func TestIsOpenAIWSTokenEvent_DisjointWithTerminal(t *testing.T) {
	terminalEvents := []string{
		"response.completed",
		"response.done",
		"response.failed",
		"response.incomplete",
		"response.cancelled",
		"response.canceled",
	}
	for _, ev := range terminalEvents {
		ev := ev
		t.Run(ev, func(t *testing.T) {
			require.True(t, isOpenAIWSTerminalEvent(ev), "expected terminal event %q to be classified as terminal", ev)
			require.False(t, isOpenAIWSTokenEvent(ev), "terminal event %q must NOT be classified as token event (issue #2651)", ev)
		})
	}
}
