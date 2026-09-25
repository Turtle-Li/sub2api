package service

import (
	"bufio"
	"bytes"
	"io"
	"strings"

	"github.com/tidwall/gjson"
)

// bpsPrimedBody 包装 basispoints 转换后的 SSE 流：先预读到首个模型产出或终态，
// 预读的字节原样缓冲并在之后优先返回。预读期间尚未向客户端写出任何字节，
// 上游失败（包括 basispoints_protocol_error）可以安全回退到原路径。
type bpsPrimedBody struct {
	upstream io.ReadCloser
	reader   *bufio.Reader
	primed   bytes.Buffer
}

func newBPSPrimedBody(upstream io.ReadCloser) *bpsPrimedBody {
	return &bpsPrimedBody{upstream: upstream, reader: bufio.NewReaderSize(upstream, 64*1024)}
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

// primeUntilOutput 逐个读取 SSE 事件直到首个模型产出或终态。返回 false 表示
// 上游在产出任何内容前就失败或断开。
func (b *bpsPrimedBody) primeUntilOutput() (bool, string) {
	eventName := ""
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
			case bpsIsOutputEvent(eventType), eventType == "response.completed":
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
