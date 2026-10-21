//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func claudeCompatTestFrames() [][2]string {
	return [][2]string{
		{"message_start", `{"type":"message_start","message":{"id":"msg_compat","type":"message","role":"assistant","content":[],"model":"claude-test","usage":{"input_tokens":12,"cache_read_input_tokens":3}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`},
		{"message_stop", `{"type":"message_stop"}`},
	}
}

func TestClaudeChatCompletionsSSEFrames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	formats := []struct {
		name   string
		format func(string, string) string
	}{
		{"id and comments", func(event, data string) string {
			return fmt.Sprintf("event: %s\nid: upstream-id\n: heartbeat\nretry: 1000\ndata: %s\n\n", event, data)
		}},
		{"data only", func(_, data string) string { return "data: " + data + "\n\n" }},
		{"data before event", func(event, data string) string {
			return fmt.Sprintf("data: %s\nid: upstream-id\nevent: %s\n\n", data, event)
		}},
		{"multiline data", func(event, data string) string {
			return fmt.Sprintf("event: %s\ndata: %s\n\n", event, strings.Replace(data, ",", ",\ndata: ", 1))
		}},
		{"event supplies type", func(event, data string) string {
			data = strings.Replace(data, `"type":"`+event+`"`, `"fixture":true`, 1)
			return fmt.Sprintf("event: %s\nid: upstream-id\ndata: %s\n\n", event, data)
		}},
	}
	for _, format := range formats {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", format.name, stream), func(t *testing.T) {
				var upstream strings.Builder
				for _, frame := range claudeCompatTestFrames()[:5] {
					upstream.WriteString(format.format(frame[0], frame[1]))
				}
				resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(strings.TrimRight(upstream.String(), "\n")))}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				svc := newStreamingResponseTestGatewayService()
				svc.cfg.Gateway.MaxLineSize = 512
				var result *ForwardResult
				var err error
				if stream {
					result, err = svc.handleCCStreamingFromAnthropic(resp, c, "claude-test", "claude-test", nil, time.Now())
				} else {
					result, err = svc.handleCCBufferedFromAnthropic(resp, c, "claude-test", "claude-test", nil, time.Now())
				}
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, rec.Code)
				require.Equal(t, 12, result.Usage.InputTokens)
				require.Equal(t, 7, result.Usage.OutputTokens)
				require.Equal(t, 3, result.Usage.CacheReadInputTokens)
				if stream {
					var text strings.Builder
					forEachOpenAISSEFrame(rec.Body.String(), func(_ string, data []byte) {
						text.WriteString(gjson.GetBytes(data, "choices.0.delta.content").String())
					})
					require.Equal(t, "OK", text.String())
					require.Equal(t, 1, strings.Count(rec.Body.String(), "data: [DONE]"))
					require.NotNil(t, result.FirstTokenMs)
				} else {
					require.Equal(t, "OK", gjson.GetBytes(rec.Body.Bytes(), "choices.0.message.content").String())
				}
			})
		}
	}
}

func TestClaudeNativeFiltersWebMessageLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	quota := `{"type":"message_limit","message_limit":{"type":"within_limit","windows":{"5h":{"utilization":0.08}}}}`
	frames := []struct{ name, frame string }{
		{"named", "event: message_limit\nid: private-quota-id\ndata: " + quota + "\n\n"},
		{"data only", "id: private-quota-id\ndata: " + quota + "\n\n"},
		{"data before event", "data: " + quota + "\nid: private-quota-id\nevent: message_limit\n\n"},
		{"multiline", "event: message_limit\nid: private-quota-id\ndata: " + strings.Replace(quota, ",", ",\ndata: ", 1) + "\n\n"},
		{"multiline data only", "data: {\ndata: " + strings.TrimPrefix(quota, "{") + "\n\n"},
		{"event supplies type", "event: message_limit\nid: private-quota-id\ndata: {\"message_limit\":{\"type\":\"within_limit\"}}\n\n"},
		{"event without data", "event: message_limit\nid: private-quota-id\n\n"},
	}
	var standard strings.Builder
	for _, frame := range claudeCompatTestFrames() {
		fmt.Fprintf(&standard, "event: %s\nid: normal-id\ndata: %s\n\n", frame[0], frame[1])
	}
	for _, tc := range frames {
		for _, passthrough := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/passthrough=%t", tc.name, passthrough), func(t *testing.T) {
				upstream := tc.frame + standard.String() + strings.TrimRight(tc.frame, "\n")
				resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(upstream))}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				svc := newStreamingResponseTestGatewayService()
				var result *streamingResult
				var err error
				if passthrough {
					result, err = svc.handleStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "claude-test")
				} else {
					result, err = svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "claude-test", "claude-test", false)
				}
				require.NoError(t, err)
				require.Equal(t, 12, result.usage.InputTokens)
				require.Equal(t, 7, result.usage.OutputTokens)
				require.Equal(t, 3, result.usage.CacheReadInputTokens)
				require.NotContains(t, rec.Body.String(), "message_limit")
				require.NotContains(t, rec.Body.String(), "private-quota-id")
				require.Contains(t, rec.Body.String(), `"text":"OK"`)
				require.Contains(t, rec.Body.String(), `"type":"message_stop"`)
				if passthrough {
					require.Equal(t, standard.String(), rec.Body.String(), "ordinary frames retain their passthrough bytes")
				}
			})
		}
	}
}

func TestClaudeNativeMessageLimitDoesNotHideErrorsOrExtensions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, eventType := range []string{"error", "future_extension"} {
		for _, passthrough := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/passthrough=%t", eventType, passthrough), func(t *testing.T) {
				quota := "event: message_limit\ndata: {\"type\":\"message_limit\"}\n\n"
				payload := `{"type":"` + eventType + `","error":{"type":"overloaded_error","message":"test upstream error"}}`
				name := eventType
				if eventType == "error" {
					name = "message_limit"
				}
				retained := "event: " + name + "\ndata: " + payload + "\n\n"
				upstream := quota + "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n" + retained
				resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(upstream))}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				svc := newStreamingResponseTestGatewayService()
				if passthrough {
					_, err := svc.handleStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "claude-test")
					require.NoError(t, err)
				} else {
					_, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "claude-test", "claude-test", false)
					require.NoError(t, err)
				}
				require.NotContains(t, rec.Body.String(), `"type":"message_limit"`)
				require.Contains(t, rec.Body.String(), payload)
			})
		}
	}
	t.Run("named error takes precedence", func(t *testing.T) {
		resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("event: error\ndata: {\"type\":\"message_limit\"}\n\n"))}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		_, err := newStreamingResponseTestGatewayService().handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "claude-test", "claude-test", false)
		var streamErr *sseStreamErrorEventError
		require.True(t, errors.As(err, &streamErr))
	})
}

func TestClaudeNativeQuotaOnlyIsNotACompletedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%t", passthrough), func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("event: message_limit\ndata: {\"type\":\"message_limit\"}\n\n"))}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			svc := newStreamingResponseTestGatewayService()
			var result *streamingResult
			var err error
			if passthrough {
				result, err = svc.handleStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "claude-test")
			} else {
				result, err = svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "claude-test", "claude-test", false)
			}
			require.ErrorContains(t, err, "missing terminal event")
			require.Empty(t, rec.Body.String())
			require.Nil(t, result.firstTokenMs)
			require.Zero(t, result.usage.InputTokens)
			require.Zero(t, result.usage.OutputTokens)
		})
	}
}

func TestClaudePassthroughBoundsPendingFrame(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStreamingResponseTestGatewayService()
	svc.cfg.Gateway.MaxLineSize = 128
	resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(strings.Repeat(": "+strings.Repeat("x", 60)+"\n", 3)))}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	_, err := svc.handleStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "claude-test")
	require.ErrorContains(t, err, "SSE frame exceeds maximum size")
	require.Empty(t, rec.Body.String())
}

func TestClaudeChatBoundsPendingFrame(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			svc := newStreamingResponseTestGatewayService()
			svc.cfg.Gateway.MaxLineSize = 128
			resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(strings.Repeat("data: "+strings.Repeat("x", 30)+"\n", 4)))}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			var err error
			if stream {
				_, err = svc.handleCCStreamingFromAnthropic(resp, c, "claude-test", "claude-test", nil, time.Now())
			} else {
				_, err = svc.handleCCBufferedFromAnthropic(resp, c, "claude-test", "claude-test", nil, time.Now())
			}
			require.ErrorContains(t, err, "SSE frame exceeds maximum size")
			if stream {
				require.NotContains(t, rec.Body.String(), "[DONE]")
			} else {
				require.Equal(t, http.StatusBadGateway, rec.Code)
			}
		})
	}
}
