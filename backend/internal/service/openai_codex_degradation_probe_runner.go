package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// runCodexDegradationProbe 发送一次降智测试提问：出站身份同账号连接测试，
// 并与网关一致地注入该模型的固定 turn-state 票据与路由 Cookie。票据缺失时直接失败，不发无票据请求。
func (s *AccountTestService) runCodexDegradationProbe(ctx context.Context, account *Account, model, effort, prompt string) (*codexDegradationProbeOutput, error) {
	token := account.GetOpenAIAccessToken()
	if token == "" {
		return nil, errors.New("no access token available")
	}
	upstreamModel := codexDegradationProbeUpstreamModel(account, model)
	payload := createOpenAITestPayload(upstreamModel, true)
	payload["input"] = []map[string]any{{
		"role":    "user",
		"content": []map[string]any{{"type": "input_text", "text": prompt}},
	}}
	if effort != "" {
		payload["reasoning"] = map[string]any{"effort": effort}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileOpenAI), http.MethodPost, chatgptCodexAPIURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// 与 testOpenAIAccountConnection 的 OAuth 分支保持一致的出站身份。
	req.Host = "chatgpt.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	canonical := resolveCodexOutboundIdentity("")
	req.Header.Set("Originator", canonical.originator)
	if customUA := strings.TrimSpace(account.GetOpenAIUserAgent()); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	} else {
		req.Header.Set("User-Agent", canonical.userAgent)
	}
	setOpenAIChatGPTAccountHeaders(req.Header, account)
	enforceCodexIdentityHeadersWithUA(req.Header, account.GetOpenAIUserAgent())
	account.ApplyHeaderOverrides(req.Header)
	// 网关同样在账号级覆写之后注入，保证票据与 Cookie 不被覆盖。
	if !applyPinnedCodexTurnState(req.Header, account, upstreamModel) {
		return nil, errCodexDegradationProbeNoPinnedState
	}

	resp, err := s.doOpenAIAccountTestUpstream(req, "", account, true)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, codexDegradationProbeErrorBodyLimit))
		return nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, extractUpstreamErrorMessage(errBody))
	}
	return parseCodexDegradationProbeStream(resp.Body)
}

// parseCodexDegradationProbeStream 汇总 Responses SSE 的文本与用量。返回值始终非 nil，出错时带部分内容。
func parseCodexDegradationProbeStream(body io.Reader) (*codexDegradationProbeOutput, error) {
	output := &codexDegradationProbeOutput{}
	var content strings.Builder
	flush := func() { output.Content = content.String() }
	reader := bufio.NewReaderSize(body, 64*1024)
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			data = strings.TrimSpace(data)
			event := gjson.Parse(data)
			switch event.Get("type").String() {
			case "response.output_text.delta":
				if content.Len() < codexDegradationProbeMaxContent {
					_, _ = content.WriteString(event.Get("delta").String())
				}
			case "response.completed", "response.done":
				response := event.Get("response")
				usage := response.Get("usage")
				output.InputTokens = int(usage.Get("input_tokens").Int())
				output.OutputTokens = int(usage.Get("output_tokens").Int())
				output.ReasoningTokens = int(usage.Get("output_tokens_details.reasoning_tokens").Int())
				output.AppliedEffort = response.Get("reasoning.effort").String()
				if content.Len() == 0 {
					for _, item := range response.Get("output").Array() {
						if item.Get("type").String() != "message" {
							continue
						}
						for _, part := range item.Get("content").Array() {
							if part.Get("type").String() == "output_text" {
								_, _ = content.WriteString(part.Get("text").String())
							}
						}
					}
				}
				flush()
				return output, nil
			case "response.failed", "response.incomplete":
				flush()
				message := event.Get("response.error.message").String()
				if message == "" {
					message = event.Get("response.incomplete_details.reason").String()
				}
				if message == "" {
					message = event.Get("type").String()
				}
				return output, errors.New(message)
			case "error":
				flush()
				message := event.Get("error.message").String()
				if message == "" {
					message = event.Get("message").String()
				}
				if message == "" {
					message = "upstream stream error"
				}
				return output, errors.New(message)
			}
		}
		if readErr != nil {
			flush()
			if readErr == io.EOF {
				return output, errors.New("stream ended before response.completed")
			}
			return output, fmt.Errorf("stream read error: %w", readErr)
		}
	}
}
