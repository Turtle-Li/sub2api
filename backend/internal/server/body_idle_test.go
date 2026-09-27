//go:build unit

package server

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// sendRawRequest 发送请求头后交给 writeBody 控制请求体的发送节奏，返回服务端的完整响应。
func sendRawRequest(t *testing.T, addr string, contentLength int, writeBody func(net.Conn)) string {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "POST /upload HTTP/1.1\r\nHost: test\r\nContent-Length: %d\r\n\r\n", contentLength)
	require.NoError(t, err)
	writeBody(conn)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return fmt.Sprintf("%d %s", resp.StatusCode, out)
}

func bodyEchoServer(t *testing.T, idle time.Duration, afterRead func(*http.Request)) (string, <-chan error) {
	t.Helper()
	readErr := make(chan error, 1)
	h := withRequestBodyIdleTimeout(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		readErr <- err
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		if afterRead != nil {
			afterRead(r)
		}
		_, _ = fmt.Fprintf(w, "got %d", len(body))
	}), idle)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String(), readErr
}

func TestRequestBodyIdleTimeoutKeepsSlowSteadyUpload(t *testing.T) {
	const idle = 300 * time.Millisecond
	addr, readErr := bodyEchoServer(t, idle, nil)

	// 总耗时约 4 倍空闲超时，但每次间隔都短于超时，不应被中断。
	chunks := 12
	resp := sendRawRequest(t, addr, chunks, func(conn net.Conn) {
		for i := 0; i < chunks; i++ {
			_, err := conn.Write([]byte("x"))
			require.NoError(t, err)
			time.Sleep(idle / 3)
		}
	})
	require.NoError(t, <-readErr)
	require.Equal(t, "200 got 12", resp)
}

func TestRequestBodyIdleTimeoutAbortsStalledUpload(t *testing.T) {
	const idle = 300 * time.Millisecond
	addr, readErr := bodyEchoServer(t, idle, nil)

	start := time.Now()
	resp := sendRawRequest(t, addr, 100, func(conn net.Conn) {
		_, err := conn.Write([]byte("partial"))
		require.NoError(t, err)
		// 之后不再发送任何数据，模拟上传卡死。
	})
	err := <-readErr
	require.True(t, errors.Is(err, pkghttputil.ErrRequestBodyStalled), "unexpected error: %v", err)
	require.Contains(t, err.Error(), "after 7 bytes")
	require.Less(t, time.Since(start), 5*idle)
	require.True(t, strings.HasPrefix(resp, "400 "), resp)
}

func TestRequestBodyIdleTimeoutDoesNotLimitResponseAfterBody(t *testing.T) {
	const idle = 300 * time.Millisecond
	ctxErr := make(chan error, 1)
	addr, readErr := bodyEchoServer(t, idle, func(r *http.Request) {
		// 读完请求体后长时间生成响应（如流式输出），连接不应因读截止时间被判定断开。
		time.Sleep(3 * idle)
		ctxErr <- r.Context().Err()
	})

	resp := sendRawRequest(t, addr, 5, func(conn net.Conn) {
		_, err := conn.Write([]byte("hello"))
		require.NoError(t, err)
	})
	require.NoError(t, <-readErr)
	require.NoError(t, <-ctxErr)
	require.Equal(t, "200 got 5", resp)
}

func TestProvideHTTPServerAppliesRequestBodyIdleTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := ingressTestConfig()
	cfg.Server.RequestBodyIdleTimeout = 1
	readErr := make(chan error, 1)
	r := gin.New()
	r.POST("/upload", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		readErr <- err
		c.String(http.StatusBadRequest, "Failed to read request body")
	})
	srv := ProvideHTTPServer(cfg, r)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	sendRawRequest(t, ln.Addr().String(), 100, func(conn net.Conn) {
		_, err := conn.Write([]byte("partial"))
		require.NoError(t, err)
	})
	require.True(t, errors.Is(<-readErr, pkghttputil.ErrRequestBodyStalled))
}

func TestRequestBodyIdleTimeoutKeepsH2CUploads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := ingressTestConfig()
	cfg.Server.RequestBodyIdleTimeout = 1
	cfg.Server.MaxRequestBodySize = 1 << 20
	cfg.Server.H2C = config.H2CConfig{Enabled: true, MaxConcurrentStreams: 10, IdleTimeout: 30, MaxReadFrameSize: 1 << 20, MaxUploadBufferPerConnection: 1 << 20, MaxUploadBufferPerStream: 1 << 20}
	r := gin.New()
	r.POST("/upload", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		// 读完请求体后超过空闲超时才响应，h2c 流不应被中断。
		time.Sleep(1500 * time.Millisecond)
		c.String(http.StatusOK, "got %d proto %d", len(body), c.Request.ProtoMajor)
	})
	srv := ProvideHTTPServer(cfg, r)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{Protocols: protocols}, Timeout: 10 * time.Second}
	pr, pw := io.Pipe()
	go func() {
		for i := 0; i < 4; i++ {
			_, _ = pw.Write([]byte(strings.Repeat("x", 1000)))
			time.Sleep(400 * time.Millisecond)
		}
		_ = pw.Close()
	}()
	resp, err := client.Post("http://"+ln.Addr().String()+"/upload", "application/octet-stream", pr)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(out))
	require.Equal(t, "got 4000 proto 2", string(out))
}
