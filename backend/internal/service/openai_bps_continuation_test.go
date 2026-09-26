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

func bpsTestRawContinuationStream(t *testing.T, upstream io.ReadCloser, continuation bpsNativeContinuation) *bpsPrimedBody {
	t.Helper()
	stream := newBPSPrimedBody(upstream)
	stream.deadline = time.Now().Add(time.Minute)
	stream.continuation = continuation
	t.Cleanup(func() { _ = stream.Close() })
	return stream
}

// 放行后 BPS 长时间无事件时写出 SSE 注释，响应处理器的数据间隔计时随之刷新。
func TestBPSContinuationKeepaliveDuringSilence(t *testing.T) {
	previous := bpsSilenceKeepalive
	bpsSilenceKeepalive = 20 * time.Millisecond
	t.Cleanup(func() { bpsSilenceKeepalive = previous })

	reader, writer := io.Pipe()
	stream := bpsTestRawContinuationStream(t, reader, func() (io.ReadCloser, error) {
		t.Fatal("continuation must not run for a completed stream")
		return nil, nil
	})
	go func() {
		_, _ = io.WriteString(writer, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"delta\":\"hi\"}\n\n")
		time.Sleep(120 * time.Millisecond)
		_, _ = io.WriteString(writer, "event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{}}\n\n")
		_ = writer.Close()
	}()
	out, err := io.ReadAll(stream)
	require.NoError(t, err)
	text := string(out)
	require.Contains(t, text, ": bps keepalive\n\n")
	require.Less(t, strings.Index(text, `"hi"`), strings.Index(text, ": bps keepalive"))
	require.Less(t, strings.Index(text, ": bps keepalive"), strings.Index(text, "response.completed"))
}

// 响应处理器关闭流之后读到的管道错误不再发起原路径请求。
func TestBPSContinuationNotStartedAfterClose(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	nativeCalls := 0
	stream := bpsTestRawContinuationStream(t, reader, func() (io.ReadCloser, error) {
		nativeCalls++
		return bpsTestSSE(`{"type":"response.completed","sequence_number":0,"response":{}}`), nil
	})
	require.NoError(t, stream.Close())
	buf := make([]byte, 256)
	for {
		if _, err := stream.Read(buf); err != nil {
			break
		}
	}
	require.Zero(t, nativeCalls)
}

// 同一请求的流与处理器先后记录失败时，熔断计数只推进一次。
func TestBPSAttemptCountsBreakerOncePerRequest(t *testing.T) {
	const accountID = 918273
	t.Cleanup(func() { bpsBreaker.recordSuccess(accountID) })
	attempt := &openAIBPSAttempt{accountID: accountID, scope: "test:" + t.Name()}
	attempt.recordFailure(bpsFailureStream, 0, "first", true)
	attempt.recordFailure(bpsFailureHandler, 0, "second", true)
	failures, openUntil := bpsBreaker.state(accountID)
	require.Equal(t, 1, failures)
	require.Nil(t, openUntil)
}

// 会话上下文达到上限后跳过 BPS，直到请求体明显缩小（客户端压缩了上下文）。
func TestBPSSessionContextSkipsUntilCompaction(t *testing.T) {
	store := &bpsSessionContextStore{sessions: map[string]bpsSessionContext{}, now: time.Now}
	store.record("s", bpsSessionContext{tokens: bpsContextTokenLimit - 1, bodyBytes: 1000})
	require.False(t, store.tooLarge("s", 1100))

	store.record("s", bpsSessionContext{tokens: bpsContextTokenLimit, bodyBytes: 1000})
	require.True(t, store.tooLarge("s", 1100))
	require.True(t, store.tooLarge("s", 900))
	require.False(t, store.tooLarge("s", 300))
	require.False(t, store.tooLarge("s", 1100), "compaction clears the record")

	store.record("t", bpsSessionContext{tokens: bpsContextStallTokens, bodyBytes: 1000, stalled: true})
	require.True(t, store.tooLarge("t", 1000))

	now := time.Now()
	store.now = func() time.Time { return now.Add(bpsSessionContextTTL) }
	require.False(t, store.tooLarge("t", 1000))
}

// 上下文过大导致的首产出超时只影响该会话，不推进账号熔断。
func TestBPSContextStallDoesNotCountTowardBreaker(t *testing.T) {
	const accountID = 918274
	t.Cleanup(func() { bpsBreaker.recordSuccess(accountID) })
	attempt := &openAIBPSAttempt{accountID: accountID, scope: "test:" + t.Name()}
	attempt.recordFailure(bpsFailureContextStall, 0, bpsNoOutputReason, false)
	failures, _ := bpsBreaker.state(accountID)
	require.Zero(t, failures)
	require.False(t, bpsSessionCooldowns.active(attempt.scope))
}

// BPS 成功时按会话记录上下文规模。
func TestBPSRecordSuccessTracksSessionContext(t *testing.T) {
	scope := "test:" + t.Name()
	attempt := &openAIBPSAttempt{accountID: 918275, scope: scope, body: make([]byte, 4096)}
	attempt.recordSuccess(&OpenAIUsage{InputTokens: 100, CacheReadInputTokens: bpsContextTokenLimit})
	entry, ok := bpsSessionContexts.get(scope)
	require.True(t, ok)
	require.Equal(t, bpsContextTokenLimit+100, entry.tokens)
	require.Equal(t, 4096, entry.bodyBytes)
	require.True(t, bpsSessionContexts.tooLarge(scope, 4096))
}
