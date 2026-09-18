package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexPinIntegrationPreservesForkIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, transport := range []string{"http", "passthrough", "ws"} {
		t.Run(transport, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			build := func(id int64, pin any) http.Header {
				body := []byte(`{"model":"gpt-5.6-codex","stream":true,"prompt_cache_key":"client-session"}`)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
				c.Set("api_key_id", int64(77))
				c.Set("api_key", &APIKey{ID: 77})
				c.Request.Header.Set("x-codex-installation-id", "client-install")
				c.Request.Header.Set("session_id", "client-session")
				c.Request.Header.Set("thread-id", "client-thread")
				c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")
				c.Request.Header.Set("x-codex-turn-state", "caller-state")
				a := &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": fmt.Sprintf("sample-%d", id)}, Extra: map[string]any{}}
				if pin != nil {
					a.Extra["pinned_codex_turn_states"] = map[string]any{"gpt-5.6-codex": pin}
				}
				switch transport {
				case "ws":
					h, _, err := svc.buildOpenAIWSHeaders(context.Background(), c, a, "token", OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true, "caller-state", "", "client-session", "gpt-5.6-codex", "")
					require.NoError(t, err)
					return h
				case "passthrough":
					req, err := svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, a, body, "token")
					require.NoError(t, err)
					return req.Header
				default:
					req, err := svc.buildUpstreamRequest(context.Background(), c, a, body, "token", true, "client-session", true)
					require.NoError(t, err)
					return req.Header
				}
			}
			baseline := build(11, nil)
			pinned := build(11, map[string]any{"state": "pinned-state", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)})
			expired := build(11, map[string]any{"state": "expired-state", "expires_at": time.Now().Add(-time.Hour).Format(time.RFC3339)})
			other := build(19, nil)
			require.Equal(t, "pinned-state", pinned.Get("x-codex-turn-state"))
			require.Equal(t, baseline.Get("x-codex-turn-state"), expired.Get("x-codex-turn-state"))
			require.NotEqual(t, "pinned-state", other.Get("x-codex-turn-state"))
			for _, header := range []string{"session_id", "session-id", "thread-id", "x-codex-installation-id", "chatgpt-account-id", "x-codex-routing-hint"} {
				require.Equal(t, baseline.Get(header), pinned.Get(header), header)
			}
			require.NotEmpty(t, pinned.Get("session_id"))
			require.NotEqual(t, pinned.Get("session_id"), other.Get("session_id"))
		})
	}
}
