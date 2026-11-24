package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestResponsesWSCtxPool_PreambleFailoverAndReplayBoundaries(t *testing.T) {
	const create = `{"type":"response.create","model":"gpt-5.1","input":"hello"}`
	const continueTurn = `{"type":"response.create","model":"gpt-5.1","store":true,"previous_response_id":"resp_first","input":"continue"}`
	const codexLimits = `{"type":"codex.rate_limits","rate_limits":{"source":"account-a"}}`
	const updatedLimits = `{"type":"rate_limits.updated","rate_limits":[{"name":"requests","remaining":0}]}`
	const codexMetadata = `{"type":"codex.response.metadata","metadata":{"source":"account-a"}}`
	const relayMetadata = `{"type":"response.metadata","headers":{"x-request-id":"req_a"}}`
	const limited = `{"type":"error","error":{"code":"usage_limit_reached","type":"usage_limit_reached"}}`
	const limitedWithUsage = `{"type":"error","error":{"code":"usage_limit_reached","type":"usage_limit_reached"},"usage":{"input_tokens":13,"output_tokens":2}}`
	const overloaded = `{"type":"error","error":{"code":"server_is_overloaded","type":"server_error"}}`
	const genericError = `{"type":"error","error":{"code":"server_error","type":"server_error"}}`
	const rejected = `{"type":"error","status":400,"error":{"code":"unsupported_parameter","param":"max_output_tokens"}}`
	const created = `{"type":"response.created","response":{"id":"resp_first","model":"gpt-5.1"}}`
	const delta = `{"type":"response.output_text.delta","delta":"hello"}`
	const completed = `{"type":"response.completed","response":{"id":"resp_first","model":"gpt-5.1","usage":{"input_tokens":2,"output_tokens":1}}}`
	const failed = `{"type":"response.failed","response":{"id":"resp_failed","model":"gpt-5.1","usage":{"input_tokens":13,"output_tokens":2}}}`
	const bMetadata = `{"type":"codex.rate_limits","rate_limits":{"source":"account-b"}}`
	const bCompleted = `{"type":"response.completed","response":{"id":"resp_replacement","model":"gpt-5.1","usage":{"input_tokens":2,"output_tokens":1}}}`
	const unknown = `{"type":"provider.unknown","value":1}`
	const typeless = `{"provider_event":true}`

	// Each byte-limit test frame fits individually, but the sum exceeds 64 KiB.
	large := `{"type":"codex.response.metadata","value":"` + strings.Repeat("x", 36<<10) + `"}`
	tooManyFrames := make([]string, 0, 11)
	for i := range 9 {
		tooManyFrames = append(tooManyFrames, fmt.Sprintf(`{"type":"codex.rate_limits","sequence":%d}`, i))
	}
	tooManyFrames = append(tooManyFrames, limited, failed)

	for _, tc := range []struct {
		name            string
		requests        []string
		attemptsA       [][]string
		want            [][]string // Downstream frames for each client turn.
		wantB           bool
		allLimited      bool
		noCapacityRetry bool
		bindPrevious    bool
		wantLateUsage   bool
	}{
		{name: "discard_codex_rate_limits", attemptsA: [][]string{{codexLimits, limited}}, want: [][]string{{bMetadata, bCompleted}}, wantB: true},
		{name: "discard_rate_limits_updated", attemptsA: [][]string{{updatedLimits, limited}}, want: [][]string{{bMetadata, bCompleted}}, wantB: true},
		{name: "discard_relay_metadata_on_capacity_failover", attemptsA: [][]string{{relayMetadata, overloaded}}, want: [][]string{{bMetadata, bCompleted}}, wantB: true, noCapacityRetry: true},
		{name: "discard_codex_metadata_on_rejected_field_retry", requests: []string{`{"type":"response.create","model":"gpt-5.1","input":"hello","max_output_tokens":100}`},
			attemptsA: [][]string{{codexMetadata, rejected}, {completed}}, want: [][]string{{completed}}},
		{name: "flush_before_normal_output", attemptsA: [][]string{{codexLimits, relayMetadata, created, delta, completed}}, want: [][]string{{codexLimits, relayMetadata, created, delta, completed}}},
		{name: "never_replay_after_delta", attemptsA: [][]string{{codexLimits, delta, limited, failed}}, want: [][]string{{codexLimits, delta, limited, failed}}},
		{name: "never_replay_after_metadata_was_flushed", attemptsA: [][]string{{codexLimits, created, limited, failed}}, want: [][]string{{codexLimits, created, limited, failed}}},
		{name: "no_buffering_on_later_turn", requests: []string{create, create}, attemptsA: [][]string{{completed}, {codexLimits, limited, failed}}, want: [][]string{{completed}, {codexLimits, limited, failed}}},
		{name: "no_buffering_on_first_turn_with_previous_response", requests: []string{continueTurn}, bindPrevious: true,
			attemptsA: [][]string{{codexLimits, limited, failed}}, want: [][]string{{codexLimits, limited, failed}}},
		{name: "no_buffering_on_tool_output", requests: []string{`{"type":"response.create","model":"gpt-5.1","input":[{"type":"function_call","call_id":"call_tool","name":"check","arguments":"{}"},{"type":"function_call_output","call_id":"call_tool","output":"ok"}]}`},
			attemptsA: [][]string{{codexLimits, limited, failed}}, want: [][]string{{codexLimits, limited, failed}}},
		{name: "flush_before_unknown_type", attemptsA: [][]string{{codexLimits, unknown, limited, failed}}, want: [][]string{{codexLimits, unknown, limited, failed}}},
		{name: "typeless_frame_closes_replay_gate", attemptsA: [][]string{{typeless, limited, failed}}, want: [][]string{{typeless, limited, failed}}},
		{name: "flush_when_frame_limit_exceeded", attemptsA: [][]string{tooManyFrames}, want: [][]string{tooManyFrames}},
		{name: "flush_when_byte_limit_exceeded", attemptsA: [][]string{{large, large, limited, failed}}, want: [][]string{{large, large, limited, failed}}},
		{name: "flush_before_unreplayable_error_and_late_terminal", attemptsA: [][]string{{codexLimits, genericError, failed}}, want: [][]string{{codexLimits, genericError, failed}}, wantLateUsage: true},
		{name: "never_replay_error_with_usage", attemptsA: [][]string{{codexLimits, limitedWithUsage, failed}}, want: [][]string{{codexLimits, limitedWithUsage, failed}}, wantLateUsage: true},
		{name: "never_replay_error_with_usage_then_eof", attemptsA: [][]string{{limitedWithUsage, "__CLOSE__"}}, want: [][]string{{limitedWithUsage}}},
		{name: "all_accounts_limited", attemptsA: [][]string{{codexLimits, limited}}, want: [][]string{{}}, wantB: true, allLimited: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Both upstreams remain connected after sending even a bare error.
			upstreamA, countA := newOpenAIWSPreambleUpstream(t, tc.attemptsA)
			framesB := []string{bMetadata, bCompleted}
			if tc.allLimited {
				framesB = []string{bMetadata, limited}
			}
			upstreamB, countB := newOpenAIWSPreambleUpstream(t, [][]string{framesB})
			accounts := []service.Account{
				newOpenAIWSModeHandlerAccount(9951, upstreamA, service.OpenAIWSIngressModeCtxPool),
				newOpenAIWSModeHandlerAccount(9952, upstreamB, service.OpenAIWSIngressModeCtxPool),
			}
			accounts[0].Priority, accounts[1].Priority = 1, 2
			if tc.noCapacityRetry {
				accounts[0].Credentials["pool_mode"] = true
				accounts[0].Credentials["pool_mode_retry_count"] = 0
			}
			cache := &concurrencyCacheMock{
				acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
				acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
			}
			h := newOpenAIWSModeHandlerWithAccountsHarness(t, accounts, 900, cache)
			h.clientConn.SetReadLimit(128 << 10)
			requests := tc.requests
			if len(requests) == 0 {
				requests = []string{create}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if tc.bindPrevious {
				// Keep the initial previous_response_id on the forwarded frame.
				store := service.NewOpenAIWSStateStore(h.gatewayCache)
				require.NoError(t, store.BindResponseAccount(ctx, *h.apiKey.GroupID, "resp_first", accounts[0].ID, time.Minute))
			}
			for turn, request := range requests {
				require.NoError(t, h.clientConn.Write(ctx, coderws.MessageText, []byte(request)))
				for _, want := range tc.want[turn] {
					_, frame, err := h.clientConn.Read(ctx)
					require.NoError(t, err, "expected prompt delivery, not the 900s upstream timeout")
					require.Equal(t, want, string(frame))
				}
			}
			if tc.allLimited {
				_, frame, err := h.clientConn.Read(ctx)
				require.Equal(t, coderws.StatusTryAgainLater, coderws.CloseStatus(err), "must close with no failed-account frames, got frame=%q err=%v", frame, err)
				require.ErrorContains(t, err, "rate limit")
			}
			_ = h.clientConn.CloseNow()
			select {
			case <-h.handlerDone:
			case <-ctx.Done():
				t.Fatal("websocket handler did not exit")
			}
			if tc.wantLateUsage {
				select {
				case usage := <-h.usageLogs:
					require.Equal(t, accounts[0].ID, usage.AccountID)
					require.EqualValues(t, 13, usage.InputTokens)
					require.EqualValues(t, 2, usage.OutputTokens)
				default:
					t.Fatal("usage from the terminal event was not recorded")
				}
			}
			require.EqualValues(t, len(tc.attemptsA), countA.Load(), "unexpected upstream A submissions")
			if tc.wantB {
				require.EqualValues(t, 1, countB.Load(), "upstream B must receive one replay on the same client socket")
			} else {
				require.Zero(t, countB.Load(), "must not replay on upstream B")
			}
		})
	}
}

func newOpenAIWSPreambleUpstream(t *testing.T, attempts [][]string) (string, *atomic.Int32) {
	t.Helper()
	var submissions atomic.Int32
	ctx := t.Context()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
			attempt := int(submissions.Add(1)) - 1
			if attempt >= len(attempts) {
				return
			}
			for _, message := range attempts[attempt] {
				if message == "__CLOSE__" {
					return
				}
				if err := conn.Write(ctx, coderws.MessageText, []byte(message)); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(upstream.Close)
	return upstream.URL, &submissions
}
