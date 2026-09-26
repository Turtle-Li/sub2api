package service

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func bpsTestContinuationStream(t *testing.T, continuation bpsNativeContinuation, events ...string) *bpsPrimedBody {
	t.Helper()
	_, bridge, err := prepareBPSRequestBody(bpsTestShellBody(), &openAIBPSAttempt{upstreamModel: "gpt-6-astra", scope: "test:" + t.Name()})
	require.NoError(t, err)
	stream := newBPSBridgeStream(bridge, bpsTestSSE(events...), true, time.Now().Add(time.Minute), continuation)
	require.Equal(t, bpsHoldFirstOutput, stream.mode)
	t.Cleanup(func() { _ = stream.Close() })
	return stream
}

func bpsTestDataLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "data: ") {
			lines = append(lines, strings.TrimPrefix(line, "data: "))
		}
	}
	return lines
}

// 带工具的流式请求：首个文本即放行；随后工具转换失败时在同一条流里接续原路径，
// 客户端看不到 response.failed，原路径事件续编 sequence_number 与 output_index。
func TestBPSContinuationSplicesNativeAfterOutput(t *testing.T) {
	bogus := map[string]any{"type": "function_call", "id": "fc_1", "call_id": "c1", "name": "run_officejs", "arguments": `{"code":"Excel.run()"}`}
	var continued []string
	nativeCalls := 0
	stream := bpsTestContinuationStream(t, func() (io.ReadCloser, error) {
		nativeCalls++
		return bpsTestSSE(
			`{"type":"response.created","sequence_number":0,"response":{"id":"resp_native","output":[]}}`,
			`{"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_native"}}`,
			`{"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"type":"function_call","name":"shell"}}`,
			`{"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":{"type":"function_call","name":"shell","arguments":"{}"}}`,
			`{"type":"response.completed","sequence_number":4,"response":{"id":"resp_native","usage":{"input_tokens":5,"output_tokens":1}}}`,
		), nil
	},
		`{"type":"response.created","sequence_number":0,"response":{"id":"resp_1","output":[]}}`,
		`{"type":"response.output_text.delta","sequence_number":1,"output_index":0,"delta":"let me check"}`,
		bpsTestJSON(t, map[string]any{"type": "response.output_item.done", "sequence_number": 2, "output_index": 1, "item": bogus}),
		bpsTestJSON(t, map[string]any{"type": "response.completed", "sequence_number": 3, "response": map[string]any{"output": []any{bogus}}}),
	)
	stream.onContinue = func(reason string, err error) {
		require.NoError(t, err)
		continued = append(continued, reason)
	}
	ok, reason := stream.primeUntilOutput()
	require.True(t, ok, reason)
	out, err := io.ReadAll(stream)
	require.NoError(t, err)
	text := string(out)

	require.Equal(t, 1, nativeCalls)
	require.Len(t, continued, 1)
	require.Contains(t, continued[0], "response.failed after output")
	require.Contains(t, text, "let me check")
	require.NotContains(t, text, "response.failed")
	require.NotContains(t, text, "resp_native\",\"output\":[]")
	require.NotContains(t, text, "response.in_progress")

	var nativeEvents []string
	for _, data := range bpsTestDataLines(text) {
		if strings.Contains(data, `"shell"`) || strings.Contains(data, "resp_native") {
			nativeEvents = append(nativeEvents, data)
		}
	}
	require.Len(t, nativeEvents, 3)
	require.Equal(t, int64(2), gjson.Get(nativeEvents[0], "sequence_number").Int())
	require.Equal(t, int64(1), gjson.Get(nativeEvents[0], "output_index").Int())
	require.Equal(t, int64(3), gjson.Get(nativeEvents[1], "sequence_number").Int())
	require.Equal(t, "response.completed", gjson.Get(nativeEvents[2], "type").String())
	require.Equal(t, int64(5), gjson.Get(nativeEvents[2], "response.usage.input_tokens").Int())
}

// 原路径接续也失败时把 BPS 的失败原样转给客户端。
func TestBPSContinuationFailureForwardsOriginalError(t *testing.T) {
	bogus := map[string]any{"type": "function_call", "id": "fc_1", "call_id": "c1", "name": "run_officejs", "arguments": `{"code":"Excel.run()"}`}
	var continueErr error
	stream := bpsTestContinuationStream(t, func() (io.ReadCloser, error) {
		return nil, errors.New("native down")
	},
		`{"type":"response.output_text.delta","output_index":0,"delta":"let me check"}`,
		bpsTestJSON(t, map[string]any{"type": "response.output_item.done", "output_index": 1, "item": bogus}),
		bpsTestJSON(t, map[string]any{"type": "response.completed", "response": map[string]any{"output": []any{bogus}}}),
	)
	stream.onContinue = func(_ string, err error) { continueErr = err }
	ok, _ := stream.primeUntilOutput()
	require.True(t, ok)
	out, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.ErrorContains(t, continueErr, "native down")
	require.Contains(t, string(out), "response.failed")
}

// BPS 正常完成与首产出前失败都不触发接续：前者直接完成，后者仍整段回退原路径。
func TestBPSContinuationNotUsedWithoutLateFailure(t *testing.T) {
	calls := 0
	continuation := func() (io.ReadCloser, error) {
		calls++
		return bpsTestSSE(), nil
	}
	done := bpsTestContinuationStream(t, continuation,
		`{"type":"response.output_text.delta","output_index":0,"delta":"pong"}`,
		`{"type":"response.completed","response":{"output":[]}}`,
	)
	ok, _ := done.primeUntilOutput()
	require.True(t, ok)
	out, err := io.ReadAll(done)
	require.NoError(t, err)
	require.Contains(t, string(out), "response.completed")

	early := bpsTestContinuationStream(t, continuation,
		`{"type":"response.failed","response":{"error":{"message":"boom"}}}`,
	)
	ok, reason := early.primeUntilOutput()
	require.False(t, ok)
	require.Contains(t, reason, "boom")
	require.Zero(t, calls)
}

// 输出后上游断开（未见完成事件）同样接续原路径。
func TestBPSContinuationOnTruncatedStream(t *testing.T) {
	calls := 0
	stream := bpsTestContinuationStream(t, func() (io.ReadCloser, error) {
		calls++
		return bpsTestSSE(`{"type":"response.completed","response":{"id":"resp_native"}}`), nil
	},
		`{"type":"response.output_text.delta","output_index":0,"delta":"partial"}`,
	)
	ok, _ := stream.primeUntilOutput()
	require.True(t, ok)
	out, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Contains(t, string(out), "resp_native")
}
