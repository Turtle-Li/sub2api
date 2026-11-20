package service

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCtxPoolUnavailableUsesAuthoritativeTerminalOrFailsOnce(t *testing.T) {
	const bare = `{"type":"error","error":{"type":"server_error","code":"upstream_unavailable"}}`
	const correlated = `{"type":"error","status":503,"request_id":"req_gateway","headers":{"x-request-id":"req_provider","x-gateway-request-id":"req_gateway"},"error":{"type":"server_error","code":"upstream_unavailable","message":"Unavailable"}}`
	const failed = `{"type":"response.failed","response":{"id":"resp_A","status":"failed","error":{"code":"upstream_unavailable"},"usage":{"input_tokens":13,"output_tokens":2}}}`
	const created = `{"type":"response.created","response":{"id":"resp_A"}}`
	const completed = `{"type":"response.completed","response":{"id":"resp_A","status":"completed","usage":{"input_tokens":13,"output_tokens":2}}}`
	const cprURL = "https://configured-cpr.example"
	useCPRQuotaSettings(t, cprURL, cprTestAdminKey)
	for _, tc := range []struct {
		name, first, followup, terminal string
		delay                           time.Duration
		otherRelay                      bool
	}{
		{"failed_with_usage", bare, failed, "response.failed", 0, false},
		{"delayed_failed_with_usage", bare, failed, "response.failed", 2500 * time.Millisecond, false},
		{"other_relay_same_envelope", correlated, failed, "response.failed", 2500 * time.Millisecond, true},
		{"ambiguous_error_then_completed", bare, completed, "response.completed", 0, false},
		{"healthy_silent_then_completed", created, completed, "response.completed", 2500 * time.Millisecond, false},
		{"upstream_closes_after_error", bare, "", "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cfg := newOpenAIWSExecutionScopeTestConfig()
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 900
			up := newStagedPassthroughConn()
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&stagedPassthroughDialer{conn: up})
			defer pool.Close()
			svc := newPassthroughLifecycleService(cfg, up)
			svc.openaiWSPool = pool
			account := passthroughLifecycleAccount()
			if !tc.otherRelay {
				account.Credentials["base_url"] = cprURL
			}
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeCtxPool
			type settlement struct {
				result       *OpenAIForwardResult
				err          error
				markedFailed bool
			}
			settled := make(chan settlement, 2)
			server, ended := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(c *gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
					failure, marked := GetOpsStreamError(c)
					settled <- settlement{result, err, marked && failure.CountTowardsSLA}
				}}
			})
			defer server.Close()
			cli := dialPassthroughLifecycleClient(t, server)
			defer cli.CloseNow()
			up.Send(tc.first)
			first, err := readPassthroughLifecycleFrame(t, cli, 3*time.Second)
			require.NoError(t, err)
			require.JSONEq(t, tc.first, string(first))
			if tc.delay > 0 {
				select {
				case early := <-settled:
					t.Fatalf("turn settled before the authoritative terminal: result=%v, err=%v", early.result, early.err)
				case <-time.After(tc.delay):
				}
			}
			if tc.followup != "" {
				up.Send(tc.followup)
				last, err := readPassthroughLifecycleFrame(t, cli, 3*time.Second)
				require.NoError(t, err)
				require.JSONEq(t, tc.followup, string(last))
			} else {
				up.Fail(io.ErrUnexpectedEOF)
			}
			select {
			case turn := <-settled:
				if tc.followup == "" {
					require.ErrorIs(t, turn.err, io.ErrUnexpectedEOF)
					require.Nil(t, turn.result)
				} else {
					require.NoError(t, turn.err)
					require.NotNil(t, turn.result)
					require.Equal(t, tc.terminal, turn.result.UpstreamTerminalEvent)
					require.Equal(t, tc.terminal == "response.failed", turn.markedFailed)
					require.EqualValues(t, 13, turn.result.Usage.InputTokens)
					require.EqualValues(t, 2, turn.result.Usage.OutputTokens)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("turn did not settle")
			}
			_ = cli.CloseNow()
			select {
			case <-ended:
			case <-time.After(3 * time.Second):
				t.Fatal("handler did not exit")
			}
			require.Empty(t, settled)
		})
	}
}

func TestOpenAIWSCPRUnavailableRequestIDRequiresGatewayEvidence(t *testing.T) {
	const frame = `{"type":"error","status":502,"request_id":"req_A","headers":{"x-gateway-request-id":"req_A","x-request-id":"req_upstream"},"error":{"code":"upstream_unavailable","type":"server_error","message":"Unavailable"}}`
	const base = "https://cpr.example"
	useCPRQuotaSettings(t, base, cprTestAdminKey)
	account := newCPRTestAccount(1, base)
	require.Equal(t, "req_A", openAIWSCPRUnavailableRequestID(account, []byte(frame)))
	require.Equal(t, "req_A", openAIWSCPRUnavailableRequestID(account, []byte(strings.Replace(frame, `"x-request-id"`, `"x-oai-request-id"`, 1))))
	for name, altered := range map[string]string{
		"gateway id differs":         strings.Replace(frame, `"request_id":"req_A"`, `"request_id":"req_B"`, 1),
		"missing gateway id":         strings.Replace(frame, "x-gateway-request-id", "other", 1),
		"missing correlation header": strings.Replace(frame, "x-request-id", "other", 1),
		"empty id":                   strings.ReplaceAll(frame, "req_A", ""),
		"numeric id":                 strings.ReplaceAll(frame, `"req_A"`, `123`),
		"wrong status":               strings.Replace(frame, "502", "200", 1),
		"string status":              strings.Replace(frame, "502", `"502"`, 1),
		"different error type":       strings.Replace(frame, "server_error", "other", 1),
		"usage extension":            strings.Replace(frame, `"status":`, `"usage":{"input_tokens":9},"status":`, 1),
		"nested extension":           strings.Replace(frame, `"code":`, `"usage":{"input_tokens":9},"code":`, 1),
		"response extension":         strings.Replace(frame, `"status":`, `"response":{"id":"resp_A"},"status":`, 1),
	} {
		t.Run(name, func(t *testing.T) { require.Empty(t, openAIWSCPRUnavailableRequestID(account, []byte(altered))) })
	}
}
