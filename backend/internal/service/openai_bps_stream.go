package service

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// BPS 预读最多占用的时长（从发起 BPS 请求起算）与字节数。Cloudflare 等前置代理
// 约 100 秒未收到首字节就会断开；到点后放行，交给响应处理器（其 keepalive 会接手），
// 之后的失败只能记为输出后中断。
const (
	bpsHoldLimit        = 45 * time.Second
	bpsToolHoldMaxBytes = 8 << 20
)

// bpsSilenceKeepalive：放行后 basispoints 可能长时间不发事件（隐藏推理、扣留工具事件），
// 期间写出 SSE 注释，避免响应处理器的上游数据间隔超时把仍在进行的 BPS 响应判为中断。
var bpsSilenceKeepalive = 15 * time.Second

var bpsKeepaliveComment = []byte(": bps keepalive\n\n")

const bpsNoOutputReason = "no output within the BPS first-output budget"

// bpsHoldMode 决定预读在什么时候放行给响应处理器。
type bpsHoldMode int

const (
	// bpsHoldFirstOutput：流式且未声明工具，首个模型产出即放行，保持低延迟。
	bpsHoldFirstOutput bpsHoldMode = iota
	// bpsHoldTools：流式且声明了工具、但没有原路径接续。basispoints 会把工具事件扣到
	// response.completed 才统一发出，工具调用转换失败最常见，所以扣到转换成功或终态
	// （受截止时间约束），让失败仍发生在输出前，从而回退原路径。
	bpsHoldTools
	// bpsHoldTerminal：非流式，客户端本就等待完整响应，预读到终态再放行。
	bpsHoldTerminal
)

// bpsNativeContinuation 发起原路径请求并返回其 SSE 响应体，用于 BPS 输出后失败时在同一条流里接续。
type bpsNativeContinuation func() (io.ReadCloser, error)

type bpsStreamChunk struct {
	data []byte
	err  error
}

// bpsPrimedBody 包装 basispoints 转换后的 SSE 流：先预读到可以安全放行的位置，
// 预读的字节原样缓冲并在之后优先返回。预读期间尚未向客户端写出任何字节，
// 上游失败（包括 basispoints_protocol_error）可以安全回退到原路径。
//
// 读取由后台 goroutine 完成，预读可以按截止时间放行，而不会阻塞在上游读取上：
// basispoints 扣留工具事件期间管道里可能长时间没有任何事件。
type bpsPrimedBody struct {
	upstream  io.ReadCloser
	chunks    chan bpsStreamChunk
	done      chan struct{}
	stopOnce  sync.Once
	closeOnce sync.Once
	onClose   func()
	// mu 保护 closed 与 nativeBody：Close 来自响应处理器，接续发生在读取协程。
	mu     sync.Mutex
	closed bool

	primed  bytes.Buffer
	pending []byte
	err     error

	mode     bpsHoldMode
	deadline time.Time
	// outputDeadline 非零时，到点仍未见任何模型产出事件（推理、文本或工具）即回退原路径：
	// 此时 BPS 大概率排队或卡住，继续等只会拖长首字。已有产出则按 deadline 放行，不丢弃已完成的部分。
	outputDeadline time.Time

	// continuation 非 nil 时，放行后 BPS 再失败（response.failed、error 或未完成即断开）
	// 不把失败转给客户端，而是发起原路径请求并把它的事件接在已输出内容之后。
	continuation bpsNativeContinuation
	// onContinue 在接续发生时回调：err 非 nil 表示原路径也失败，失败事件已原样转给客户端。
	onContinue func(reason string, err error)
	native     *bufio.Reader
	nativeBody io.ReadCloser
	// lastSequence 与 nextOutputIndex 记录 BPS 已发出的事件序号与输出条目下标，接续事件在其后续编。
	lastSequence    int64
	nextOutputIndex int64
	completed       bool
}

// newBPSBridgeStream 转换 BPS 响应并按请求形态决定预读策略。BPS 请求体不带 tools
// （工具目录写在提示词里），须以 bridge 记录的客户端声明为准。
// 流式且有原路径接续时首个产出即放行：之后的失败由接续兜底，不必为工具转换扣住整段响应。
func newBPSBridgeStream(bridge *basispoints.Bridge, upstream io.ReadCloser, clientStream bool, deadline time.Time, continuation bpsNativeContinuation) *bpsPrimedBody {
	mode := bpsHoldFirstOutput
	switch {
	case !clientStream:
		mode = bpsHoldTerminal
		continuation = nil
	case bridge.HasClientTools() && continuation == nil:
		mode = bpsHoldTools
	}
	stream := newBPSPrimedBody(bridge.Stream(upstream))
	stream.mode = mode
	stream.deadline = deadline
	stream.continuation = continuation
	return stream
}

func newBPSPrimedBody(upstream io.ReadCloser) *bpsPrimedBody {
	b := &bpsPrimedBody{upstream: upstream, chunks: make(chan bpsStreamChunk, 16), done: make(chan struct{})}
	go b.pump()
	return b
}

func (b *bpsPrimedBody) pump() {
	reader := bufio.NewReaderSize(b.upstream, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		select {
		case b.chunks <- bpsStreamChunk{data: []byte(line), err: err}:
		case <-b.done:
			return
		}
		if err != nil {
			return
		}
	}
}

// next 取下一段上游数据；timeout 为 nil 表示不设截止。
func (b *bpsPrimedBody) next(timeout <-chan time.Time) (bpsStreamChunk, bool) {
	select {
	case chunk := <-b.chunks:
		return chunk, true
	case <-b.done:
		return bpsStreamChunk{err: io.ErrClosedPipe}, true
	case <-timeout:
		return bpsStreamChunk{}, false
	}
}

func (b *bpsPrimedBody) Read(p []byte) (int, error) {
	if b.continuation == nil && b.native == nil {
		return b.readRaw(p)
	}
	for b.primed.Len() == 0 {
		if b.err != nil {
			return 0, b.err
		}
		if b.native != nil {
			b.fillNative()
		} else {
			b.fillBPS()
		}
	}
	return b.primed.Read(p)
}

// readRaw 原样转发 BPS 流，用于没有原路径接续的请求。
func (b *bpsPrimedBody) readRaw(p []byte) (int, error) {
	if b.primed.Len() > 0 {
		return b.primed.Read(p)
	}
	for len(b.pending) == 0 {
		if b.err != nil {
			return 0, b.err
		}
		chunk, _ := b.next(nil)
		b.pending, b.err = chunk.data, chunk.err
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

// fillBPS 读取一个完整的 BPS SSE 事件放入缓冲；遇到失败终态或未完成即断开时改为接续原路径。
func (b *bpsPrimedBody) fillBPS() {
	var event bytes.Buffer
	eventName, eventType, data := "", "", ""
	for {
		var keepalive <-chan time.Time
		if event.Len() == 0 {
			timer := time.NewTimer(bpsSilenceKeepalive)
			defer timer.Stop()
			keepalive = timer.C
		}
		chunk, ok := b.next(keepalive)
		if !ok {
			b.primed.Write(bpsKeepaliveComment)
			return
		}
		line := string(chunk.data)
		event.WriteString(line)
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(trimmed, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
		case strings.HasPrefix(trimmed, "data:"):
			data = strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			if eventType = gjson.Get(data, "type").String(); eventType == "" {
				eventType = eventName
			}
		}
		if chunk.err != nil {
			reason := "stream ended before completion"
			if chunk.err != io.EOF {
				reason = "stream error after output: " + chunk.err.Error()
			}
			if bpsIsFailureEvent(eventType) {
				reason = bpsFailureMessage(eventType, data)
			}
			if b.completed && !bpsIsFailureEvent(eventType) || !b.startContinuation(reason) {
				b.primed.Write(event.Bytes())
				b.err = chunk.err
			}
			return
		}
		if trimmed != "" {
			continue
		}
		attempted := b.continuation != nil
		if bpsIsFailureEvent(eventType) && b.startContinuation(bpsFailureMessage(eventType, data)) {
			return
		}
		if data != "" {
			b.observe(eventType, data)
		}
		b.primed.Write(event.Bytes())
		if attempted && b.continuation == nil {
			// 接续失败：BPS 流已停止，失败事件即最后一个事件。
			b.err = io.EOF
		}
		return
	}
}

// startContinuation 停止 BPS 流并发起原路径请求。原路径失败时返回 false，由调用方把 BPS 的失败原样转给客户端。
func (b *bpsPrimedBody) startContinuation(reason string) bool {
	continuation := b.continuation
	if continuation == nil {
		return false
	}
	b.continuation = nil
	if b.isClosed() {
		// 响应处理器已放弃这条流（如客户端断开或处理器超时），不再发起原路径请求。
		return false
	}
	b.stopBPS()
	body, err := continuation()
	if b.onContinue != nil {
		b.onContinue(reason, err)
	}
	if err != nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		_ = body.Close()
		return false
	}
	b.nativeBody = body
	b.native = bufio.NewReaderSize(body, 64*1024)
	return true
}

func (b *bpsPrimedBody) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// fillNative 读取一个原路径 SSE 事件：丢弃 response.created / in_progress 等开头事件，
// 并把 sequence_number 与 output_index 续接到 BPS 已发出的编号之后。
func (b *bpsPrimedBody) fillNative() {
	var lines []string
	for {
		line, err := b.native.ReadString('\n')
		if line != "" {
			lines = append(lines, line)
		}
		if err != nil {
			b.writeNativeEvent(lines)
			b.err = err
			return
		}
		if strings.TrimRight(line, "\r\n") == "" {
			b.writeNativeEvent(lines)
			return
		}
	}
}

func (b *bpsPrimedBody) writeNativeEvent(lines []string) {
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r\n")
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		switch gjson.Get(data, "type").String() {
		case "response.created", "response.in_progress", "response.queued":
			return
		}
		if gjson.Get(data, "sequence_number").Exists() {
			b.lastSequence++
			data, _ = sjson.Set(data, "sequence_number", b.lastSequence)
		}
		if index := gjson.Get(data, "output_index"); index.Exists() {
			data, _ = sjson.Set(data, "output_index", index.Int()+b.nextOutputIndex)
		}
		lines[i] = "data: " + data + line[len(trimmed):]
	}
	for _, line := range lines {
		b.primed.WriteString(line)
	}
}

// observe 记录 BPS 已发出事件的编号，供接续时续编。
func (b *bpsPrimedBody) observe(eventType, data string) {
	if seq := gjson.Get(data, "sequence_number"); seq.Exists() && seq.Int() > b.lastSequence {
		b.lastSequence = seq.Int()
	}
	if index := gjson.Get(data, "output_index"); index.Exists() && index.Int() >= b.nextOutputIndex {
		b.nextOutputIndex = index.Int() + 1
	}
	if eventType == "response.completed" {
		b.completed = true
	}
}

func (b *bpsPrimedBody) stopBPS() {
	b.stopOnce.Do(func() {
		close(b.done)
		_ = b.upstream.Close()
		if b.onClose != nil {
			b.onClose()
		}
	})
}

func (b *bpsPrimedBody) Close() error {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		nativeBody := b.nativeBody
		b.mu.Unlock()
		b.stopBPS()
		if nativeBody != nil {
			_ = nativeBody.Close()
		}
	})
	return nil
}

func bpsIsFailureEvent(eventType string) bool {
	return eventType == "response.failed" || eventType == "error"
}

func bpsFailureMessage(eventType, data string) string {
	message := gjson.Get(data, "response.error.message").String()
	if message == "" {
		message = gjson.Get(data, "error.message").String()
	}
	if message == "" {
		message = gjson.Get(data, "message").String()
	}
	return eventType + " after output: " + message
}

// bpsIsOutputEvent 判断事件是否携带模型产出（推理、文本或工具调用）。
func bpsIsOutputEvent(eventType string) bool {
	for _, prefix := range []string{"response.output_", "response.reasoning", "response.content_part", "response.function_call", "response.custom_tool_call"} {
		if strings.HasPrefix(eventType, prefix) {
			return true
		}
	}
	return false
}

// bpsIsToolCallDone 判断事件是否为一个已完成（已转换为客户端工具）的调用。
func bpsIsToolCallDone(eventType, data string) bool {
	if eventType != "response.output_item.done" {
		return false
	}
	switch gjson.Get(data, "item.type").String() {
	case "", "message", "reasoning":
		return false
	}
	return true
}

// primeUntilOutput 逐个读取 SSE 事件直到可以放行（见 bpsHoldMode）、到达截止时间
// 或终态。返回 false 表示上游在向客户端输出任何内容前就失败、断开或超出首产出预算。
func (b *bpsPrimedBody) primeUntilOutput() (bool, string) {
	sawOutput := false
	var timer *time.Timer
	var timeout <-chan time.Time
	arm := func() {
		if timer != nil {
			timer.Stop()
			timer, timeout = nil, nil
		}
		if deadline := b.waitDeadline(sawOutput); !deadline.IsZero() {
			timer = time.NewTimer(time.Until(deadline))
			timeout = timer.C
		}
	}
	arm()
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	eventName := ""
	for {
		chunk, ok := b.next(timeout)
		if !ok {
			if !sawOutput && !b.outputDeadline.IsZero() {
				return false, bpsNoOutputReason
			}
			return true, ""
		}
		line := string(chunk.data)
		b.primed.WriteString(line)
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(trimmed, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
		case strings.HasPrefix(trimmed, "data:"):
			data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			eventType := gjson.Get(data, "type").String()
			if eventType == "" {
				eventType = eventName
			}
			b.observe(eventType, data)
			if !sawOutput && bpsIsOutputEvent(eventType) {
				sawOutput = true
				arm()
			}
			switch {
			case eventType == "response.completed":
				return true, ""
			case b.mode == bpsHoldTools && (bpsIsToolCallDone(eventType, data) || b.primed.Len() > bpsToolHoldMaxBytes):
				return true, ""
			case b.mode == bpsHoldFirstOutput && bpsIsOutputEvent(eventType):
				return true, ""
			case eventType == "response.failed", eventType == "response.incomplete", eventType == "error":
				message := gjson.Get(data, "response.error.message").String()
				if message == "" {
					message = gjson.Get(data, "error.message").String()
				}
				return false, "terminal " + eventType + " before output: " + message
			}
		case trimmed == "":
			eventName = ""
		}
		if chunk.err != nil {
			if chunk.err == io.EOF {
				return false, "stream ended before output"
			}
			return false, "stream error before output: " + chunk.err.Error()
		}
	}
}

// waitDeadline 返回当前阶段的等待截止：尚无产出时取首产出预算与放行截止中较早者；
// 已有产出后流式按放行截止，非流式等到终态。
func (b *bpsPrimedBody) waitDeadline(sawOutput bool) time.Time {
	var deadline time.Time
	if b.mode != bpsHoldTerminal {
		deadline = b.deadline
	}
	if !sawOutput && !b.outputDeadline.IsZero() && (deadline.IsZero() || b.outputDeadline.Before(deadline)) {
		deadline = b.outputDeadline
	}
	return deadline
}

var _ io.ReadCloser = (*bpsPrimedBody)(nil)
