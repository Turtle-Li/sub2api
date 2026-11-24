package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Respond once, before any operator-defined passthrough can see a hidden
// upstream error. A previously committed SSE stream must not receive JSON.
func writeHiddenUpstreamBalance(c *gin.Context, failure *service.UpstreamFailoverError, platform string, streamStarted bool, write func(bool)) bool {
	if !failure.MustHideUpstreamBalance(platform) {
		return false
	}
	if failoverClientGone(c) {
		return true
	}
	streamStarted = service.StopOpenAICompactSSEKeepaliveCommitted(c) || streamStarted ||
		(c.Writer.Written() && strings.HasPrefix(c.Writer.Header().Get("Content-Type"), "text/event-stream"))
	if _, terminal := service.GetOpsStreamError(c); terminal || (!streamStarted && (service.IsResponseCommitted(c) || c.Writer.Written())) {
		return true
	}
	service.SetOpsUpstreamError(c, failure.StatusCode, "", "")
	if streamStarted {
		service.MarkOpsStreamError(c, "upstream_error", service.UpstreamBalanceClientMessage, http.StatusBadGateway)
	} else {
		service.MarkResponseCommitted(c)
	}
	write(streamStarted)
	return true
}

func writeNeutralOpenAIChatSSE(c *gin.Context) {
	writeNeutralBalanceSSE(c, "data: {\"error\":{\"type\":\"upstream_error\",\"message\":%q}}\n\n")
}

func writeNeutralAnthropicSSE(c *gin.Context) {
	writeNeutralBalanceSSE(c, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"upstream_error\",\"message\":%q}}\n\n")
}

func writeNeutralBalanceSSE(c *gin.Context, format string) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}
	if _, err := fmt.Fprintf(c.Writer, format, service.UpstreamBalanceClientMessage); err != nil {
		_ = c.Error(err)
		return
	}
	flusher.Flush()
}

func (h *OpenAIGatewayHandler) handleChatCompletionsFailoverExhausted(c *gin.Context, failure *service.UpstreamFailoverError, streamStarted bool) {
	if writeHiddenUpstreamBalance(c, failure, service.PlatformOpenAI, streamStarted, func(stream bool) {
		if stream {
			writeNeutralOpenAIChatSSE(c)
			return
		}
		h.handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", service.UpstreamBalanceClientMessage, false)
	}) {
		return
	}
	h.handleFailoverExhausted(c, failure, streamStarted)
}
