package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
)

// withRequestBodyIdleTimeout 在读取请求体时启用空闲超时：每读到数据就把连接读截止时间顺延 idle，
// 连续 idle 没有任何字节到达才判定连接已坏并中止读取。大请求体只要还在传输就不会被打断；
// 读完请求体后立即清除截止时间，不影响之后的长时间流式响应。
func withRequestBodyIdleTimeout(next http.Handler, idle time.Duration) http.Handler {
	if idle <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		body := &idleTimeoutBody{ReadCloser: r.Body, rc: http.NewResponseController(w), idle: idle}
		r.Body = body
		defer body.clear()
		next.ServeHTTP(w, r)
	})
}

type idleTimeoutBody struct {
	io.ReadCloser
	rc       *http.ResponseController
	idle     time.Duration
	armed    bool
	disabled bool
	received int64
	stalled  error
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	if b.stalled != nil {
		return 0, b.stalled
	}
	if !b.disabled {
		if err := b.rc.SetReadDeadline(time.Now().Add(b.idle)); err != nil {
			// 底层连接不支持读截止时间（如被劫持的连接）时退化为不限时读取。
			b.disabled = true
		} else {
			b.armed = true
		}
	}
	n, err := b.ReadCloser.Read(p)
	b.received += int64(n)
	if err != nil && errors.Is(err, os.ErrDeadlineExceeded) {
		// 保持截止时间为已过期：net/http 写响应前会尝试丢弃剩余请求体，
		// 若清除截止时间，这一步会在已坏的连接上继续卡住，错误响应也发不出去。
		b.armed = false
		log.Printf("request body idle for %s after %d bytes; aborting read", b.idle, b.received)
		b.stalled = fmt.Errorf("%w: no data for %s after %d bytes: %v", pkghttputil.ErrRequestBodyStalled, b.idle, b.received, err)
		return n, b.stalled
	}
	if err != nil {
		b.clear()
	}
	return n, err
}

func (b *idleTimeoutBody) clear() {
	if b.armed {
		b.armed = false
		_ = b.rc.SetReadDeadline(time.Time{})
	}
}
