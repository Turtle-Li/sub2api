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
)

// BPS 预读最多占用的时长（从发起 BPS 请求起算）与字节数。Cloudflare 等前置代理
// 约 100 秒未收到首字节就会断开；到点后放行，交给响应处理器（其 keepalive 会接手），
// 之后的失败只能记为输出后中断。
const (
	bpsHoldLimit        = 45 * time.Second
	bpsToolHoldMaxBytes = 8 << 20
)

// bpsHoldMode 决定预读在什么时候放行给响应处理器。
type bpsHoldMode int

const (
	// bpsHoldFirstOutput：流式且未声明工具，首个模型产出即放行，保持低延迟。
	bpsHoldFirstOutput bpsHoldMode = iota
	// bpsHoldTools：流式且声明了工具。basispoints 会把工具事件扣到 response.completed
	// 才统一发出，工具调用转换失败最常见，所以扣到转换成功或终态（受截止时间约束），
	// 让失败仍发生在输出前，从而回退原路径。
	bpsHoldTools
	// bpsHoldTerminal：非流式，客户端本就等待完整响应，预读到终态再放行。
	bpsHoldTerminal
)

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
	closeOnce sync.Once
	onClose   func()

	primed  bytes.Buffer
	pending []byte
	err     error

	mode     bpsHoldMode
	deadline time.Time
	// fallbackOnDeadline 为 true 时截止时间到达即回退而非放行：截止时间取自首输出预算，
	// 放行后客户端仍拿不到输出，首输出超时照样会触发，且剩余预算已不足以走原路径。
	fallbackOnDeadline bool
}

// newBPSBridgeStream 转换 BPS 响应并按请求形态决定预读策略。BPS 请求体不带 tools
// （工具目录写在提示词里），须以 bridge 记录的客户端声明为准。
func newBPSBridgeStream(bridge *basispoints.Bridge, upstream io.ReadCloser, clientStream bool, deadline time.Time) *bpsPrimedBody {
	mode := bpsHoldFirstOutput
	switch {
	case !clientStream:
		mode = bpsHoldTerminal
	case bridge.HasClientTools():
		mode = bpsHoldTools
	}
	stream := newBPSPrimedBody(bridge.Stream(upstream))
	stream.mode = mode
	stream.deadline = deadline
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

func (b *bpsPrimedBody) Close() error {
	var err error
	b.closeOnce.Do(func() {
		close(b.done)
		err = b.upstream.Close()
		if b.onClose != nil {
			b.onClose()
		}
	})
	return err
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
// 或终态。返回 false 表示上游在向客户端输出任何内容前就失败或断开。
func (b *bpsPrimedBody) primeUntilOutput() (bool, string) {
	var timeout <-chan time.Time
	if b.mode != bpsHoldTerminal && !b.deadline.IsZero() {
		timer := time.NewTimer(time.Until(b.deadline))
		defer timer.Stop()
		timeout = timer.C
	}
	eventName := ""
	for {
		chunk, ok := b.next(timeout)
		if !ok {
			if b.fallbackOnDeadline {
				return false, "no output within the BPS share of the first-output budget"
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

var _ io.ReadCloser = (*bpsPrimedBody)(nil)
