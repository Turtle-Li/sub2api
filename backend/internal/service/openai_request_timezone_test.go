package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func BenchmarkRewriteOpenAIRequestTimezone(b *testing.B) {
	for _, tc := range []struct {
		name     string
		size     int
		withTags bool
	}{
		{name: "4KiBTagged", size: 4 << 10, withTags: true},
		{name: "128KiBTagged", size: 128 << 10, withTags: true},
		{name: "1MiBTagged", size: 1 << 20, withTags: true},
		{name: "128KiBWithoutTag", size: 128 << 10, withTags: false},
	} {
		b.Run(tc.name, func(b *testing.B) {
			body := benchmarkOpenAIRequestTimezoneBody(tc.size, tc.withTags)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				got, changed, err := rewriteOpenAIRequestTimezone(body, "Asia/Tokyo")
				if err != nil || changed != tc.withTags || len(got) == 0 {
					b.Fatalf("rewrite failed: changed=%v len=%d err=%v", changed, len(got), err)
				}
			}
		})
	}
}

func BenchmarkRewriteOpenAIRequestTimezoneParallel128KiB(b *testing.B) {
	body := benchmarkOpenAIRequestTimezoneBody(128<<10, true)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			got, changed, err := rewriteOpenAIRequestTimezone(body, "Asia/Tokyo")
			if err != nil || !changed || len(got) == 0 {
				b.Fatalf("rewrite failed: changed=%v len=%d err=%v", changed, len(got), err)
			}
		}
	})
}

func benchmarkOpenAIRequestTimezoneBody(size int, withTags bool) []byte {
	instructions := "normal developer instructions"
	if withTags {
		instructions = "<environment_context><timezone>Asia/Shanghai</timezone></environment_context>"
	}
	prefix := fmt.Sprintf(`{"model":"gpt-5.6-sol","instructions":%q,"input":[{"role":"user","content":`, instructions)
	suffix := `}]}`
	padding := size - len(prefix) - len(suffix) - 2
	if padding < 0 {
		padding = 0
	}
	return []byte(prefix + `"` + strings.Repeat("x", padding) + `"` + suffix)
}

func TestRewriteOpenAIRequestTimezone_RewritesOnlyDeveloperContext(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"instructions":"outside <timezone>Asia/Shanghai</timezone> root <environment_context><timezone>Asia/Shanghai</timezone></environment_context>",
		"input":[
			{"role":"developer","content":[{"type":"input_text","text":"dev <environment_context><timezone>Asia/Shanghai</timezone></environment_context>"}]},
			{"role":"system","content":"sys <environment_context><timezone>Asia/Shanghai</timezone></environment_context>"},
			{"role":"user","content":[{"type":"input_text","text":"user <environment_context><timezone>Asia/Shanghai</timezone></environment_context>"}]}
		],
		"messages":[
			{"role":"developer","content":"chat <environment_context><timezone>Asia/Shanghai</timezone></environment_context>"},
			{"role":"user","content":"keep <environment_context><timezone>Asia/Shanghai</timezone></environment_context>"}
		]
	}`)
	original := bytes.Clone(body)

	got, changed, err := rewriteOpenAIRequestTimezone(body, "Asia/Tokyo")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, original, body, "account-specific rewrite must not mutate the retry source body")
	require.Contains(t, gjson.GetBytes(got, "instructions").String(), "<timezone>Asia/Tokyo</timezone>")
	require.Contains(t, gjson.GetBytes(got, "instructions").String(), "outside <timezone>Asia/Shanghai</timezone>")
	require.Contains(t, gjson.GetBytes(got, "input.0.content.0.text").String(), "<timezone>Asia/Tokyo</timezone>")
	require.Contains(t, gjson.GetBytes(got, "input.1.content").String(), "<timezone>Asia/Tokyo</timezone>")
	require.Contains(t, gjson.GetBytes(got, "messages.0.content").String(), "<timezone>Asia/Tokyo</timezone>")
	require.Contains(t, gjson.GetBytes(got, "input.2.content.0.text").String(), "<timezone>Asia/Shanghai</timezone>")
	require.Contains(t, gjson.GetBytes(got, "messages.1.content").String(), "<timezone>Asia/Shanghai</timezone>")
}

func TestRewriteOpenAIRequestTimezone_HandlesWebSocketEnvelopeAndBoundaries(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"type":"response.create",
		"response":{
			"instructions":"<environment_context><timezone>Asia/Shanghai</timezone></environment_context>",
			"input":[{"role":"user","content":"<environment_context><timezone>Asia/Shanghai</timezone></environment_context>"}]
		}
	}`)

	got, changed, err := rewriteOpenAIRequestTimezone(body, "America/Los_Angeles")
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, gjson.GetBytes(got, "response.instructions").String(), "<timezone>America/Los_Angeles</timezone>")
	require.Contains(t, gjson.GetBytes(got, "response.input.0.content").String(), "<timezone>Asia/Shanghai</timezone>")

	unchanged, changed, err := rewriteOpenAIRequestTimezone(
		[]byte(`{"instructions":"timezone Asia/Shanghai without the environment tag"}`),
		"Asia/Tokyo",
	)
	require.NoError(t, err)
	require.False(t, changed)
	require.JSONEq(t, `{"instructions":"timezone Asia/Shanghai without the environment tag"}`, string(unchanged))

	escaped := []byte(`{"instructions":"\u003cenvironment_context\u003e\u003ctimezone\u003eAsia/Shanghai\u003c/timezone\u003e\u003c/environment_context\u003e"}`)
	escaped, changed, err = rewriteOpenAIRequestTimezone(escaped, "Asia/Tokyo")
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, gjson.GetBytes(escaped, "instructions").String(), "<timezone>Asia/Tokyo</timezone>")

	_, _, err = rewriteOpenAIRequestTimezone(body, "UTC+9")
	require.Error(t, err)
}

func TestAccountEffectiveOpenAIRequestTimezone(t *testing.T) {
	t.Parallel()

	account := &Account{
		Platform: PlatformOpenAI,
		Proxy:    &Proxy{DetectedTimezone: "Asia/Tokyo"},
	}
	require.Equal(t, "Asia/Tokyo", account.EffectiveOpenAIRequestTimezone())

	account.Extra = map[string]any{OpenAIRequestTimezoneExtraKey: "America/Los_Angeles"}
	require.Equal(t, "America/Los_Angeles", account.EffectiveOpenAIRequestTimezone())

	account.Extra[OpenAIRequestTimezoneExtraKey] = "invalid timezone"
	require.Equal(t, "Asia/Tokyo", account.EffectiveOpenAIRequestTimezone())

	account.Extra[OpenAIRequestTimezoneExtraKey] = "off"
	require.Empty(t, account.EffectiveOpenAIRequestTimezone())
}

func TestOpenAIGatewayForward_SendsProxyTimezoneToUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":false,
		"instructions":"<environment_context><timezone>Asia/Shanghai</timezone></environment_context>",
		"input":[{"role":"user","content":"keep <environment_context><timezone>Asia/Shanghai</timezone></environment_context>"}]
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:          42,
		Name:        "japan-openai",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-account",
		},
		Proxy: &Proxy{ID: 7, DetectedTimezone: "Asia/Tokyo"},
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err)
	require.Nil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Contains(t, gjson.GetBytes(upstream.lastBody, "instructions").String(), "<timezone>Asia/Tokyo</timezone>")
	require.Contains(t, gjson.GetBytes(upstream.lastBody, "input.0.content").String(), "<timezone>Asia/Shanghai</timezone>")
}

func TestOpenAIGatewayChatForward_SendsAccountOverrideTimezoneToUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":false,
		"messages":[
			{"role":"system","content":"<environment_context><timezone>Asia/Shanghai</timezone></environment_context>"},
			{"role":"user","content":"keep <environment_context><timezone>Asia/Shanghai</timezone></environment_context>"}
		]
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:          43,
		Name:        "west-coast-openai",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-account",
		},
		Extra: map[string]any{OpenAIRequestTimezoneExtraKey: "America/Los_Angeles"},
		Proxy: &Proxy{ID: 8, DetectedTimezone: "Asia/Tokyo"},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.Error(t, err)
	require.Nil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Contains(t, gjson.GetBytes(upstream.lastBody, "instructions").String(), "<timezone>America/Los_Angeles</timezone>")
	require.Contains(t, gjson.GetBytes(upstream.lastBody, "input.0.content").String(), "<timezone>Asia/Shanghai</timezone>")
}

func TestWithOpenAIWSRequestTimezone_RewritesFirstAndLaterTurns(t *testing.T) {
	t.Parallel()

	account := &Account{Platform: PlatformOpenAI, Proxy: &Proxy{DetectedTimezone: "America/Los_Angeles"}}
	first := []byte(`{"type":"response.create","instructions":"<environment_context><timezone>Asia/Shanghai</timezone></environment_context>"}`)
	originalTransformCalls := 0
	hooks := &OpenAIWSIngressHooks{
		TransformRequest: func(_ int, payload []byte, _ string) ([]byte, error) {
			originalTransformCalls++
			return payload, nil
		},
	}

	gotFirst, gotHooks, err := withOpenAIWSRequestTimezone(account, first, hooks)
	require.NoError(t, err)
	require.Contains(t, gjson.GetBytes(gotFirst, "instructions").String(), "<timezone>America/Los_Angeles</timezone>")
	require.NotSame(t, hooks, gotHooks)

	later := []byte(`{"type":"response.create","instructions":"<environment_context><timezone>Asia/Shanghai</timezone></environment_context>"}`)
	gotLater, err := gotHooks.TransformRequest(2, later, "gpt-5.6-sol")
	require.NoError(t, err)
	require.Equal(t, 1, originalTransformCalls)
	require.Contains(t, gjson.GetBytes(gotLater, "instructions").String(), "<timezone>America/Los_Angeles</timezone>")
}
