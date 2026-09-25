package service

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// 带工具的请求最多暂扣输出的时长与字节数。Cloudflare 等前置代理约 100 秒未收到
// 响应头就会断开，超过上限后放行，退化为"首个产出即放行"。
const (
	bpsToolHoldLimit    = 45 * time.Second
	bpsToolHoldMaxBytes = 8 << 20
)

// bpsPrimedBody 包装 basispoints 转换后的 SSE 流：先预读到可以安全放行的位置，
// 预读的字节原样缓冲并在之后优先返回。预读期间尚未向客户端写出任何字节，
// 上游失败（包括 basispoints_protocol_error）可以安全回退到原路径。
//
// holdForTools 为 true 时（请求声明了工具），不在首个文本产出时放行，而是等到
// 首个工具调用转换成功或响应完成：工具调用转换失败最常见，且通常发生在开场白
// 文字之后，暂扣可以让它仍在输出前失败，从而回退原路径而不是让客户端中断。
type bpsPrimedBody struct {
	upstream     io.ReadCloser
	reader       *bufio.Reader
	primed       bytes.Buffer
	holdForTools bool
	holdLimit    time.Duration
	now          func() time.Time
}

func newBPSPrimedBody(upstream io.ReadCloser) *bpsPrimedBody {
	return &bpsPrimedBody{upstream: upstream, reader: bufio.NewReaderSize(upstream, 64*1024), holdLimit: bpsToolHoldLimit, now: time.Now}
}

func (b *bpsPrimedBody) Read(p []byte) (int, error) {
	if b.primed.Len() > 0 {
		return b.primed.Read(p)
	}
	return b.reader.Read(p)
}

func (b *bpsPrimedBody) Close() error {
	return b.upstream.Close()
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

// primeUntilOutput 逐个读取 SSE 事件直到可以放行（见 holdForTools）或终态。
// 返回 false 表示上游在向客户端输出任何内容前就失败或断开。
func (b *bpsPrimedBody) primeUntilOutput() (bool, string) {
	eventName := ""
	started := b.now()
	for {
		line, err := b.reader.ReadString('\n')
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
			case eventType == "response.completed", bpsIsToolCallDone(eventType, data):
				return true, ""
			case bpsIsOutputEvent(eventType) && (!b.holdForTools || b.primed.Len() > bpsToolHoldMaxBytes || b.now().Sub(started) > b.holdLimit):
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
		if err != nil {
			if err == io.EOF {
				return false, "stream ended before output"
			}
			return false, "stream error before output: " + err.Error()
		}
	}
}

var _ io.ReadCloser = (*bpsPrimedBody)(nil)
