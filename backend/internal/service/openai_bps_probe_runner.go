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
	"time"

	"github.com/tidwall/gjson"
)

// runOpenAIProbe 发送一次降智测试提问。bps 路径与网关一致地经 basispoints 转换，
// 但不受总开关/名单/熔断影响，也不写入 BPS 监控统计；native 路径同账号连接测试。
func (s *AccountTestService) runOpenAIProbe(ctx context.Context, account *Account, path, model, effort, prompt string) (*bpsProbeOutput, error) {
	token := account.GetOpenAIAccessToken()
	if token == "" {
		return nil, errors.New("no access token available")
	}
	upstreamModel := normalizeOpenAIModelForUpstream(account, account.GetMappedModel(model))
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

	switch path {
	case BPSProbePathBPS:
		return s.runOpenAIProbeViaBPS(ctx, account, token, upstreamModel, effort, body)
	case BPSProbePathNative:
		return s.runOpenAIProbeNative(ctx, account, token, body)
	default:
		return nil, fmt.Errorf("unknown probe path: %s", path)
	}
}

func (s *AccountTestService) runOpenAIProbeViaBPS(ctx context.Context, account *Account, token, upstreamModel, effort string, body []byte) (*bpsProbeOutput, error) {
	if s.openaiGatewayService == nil {
		return nil, errors.New("openai gateway unavailable")
	}
	if !isBPSUpstreamModel(upstreamModel) {
		return nil, fmt.Errorf("model %s is not served by BPS", upstreamModel)
	}
	attempt := &openAIBPSAttempt{
		accountID:     account.ID,
		upstreamModel: upstreamModel,
		effort:        effort,
		scope:         fmt.Sprintf("probe:%d:%d", account.ID, time.Now().UnixNano()),
	}
	bpsBody, bridge, err := prepareBPSRequestBody(body, attempt)
	if err != nil {
		return nil, fmt.Errorf("prepare BPS request: %w", err)
	}
	resp, statusCode, errBody, err := s.openaiGatewayService.sendOpenAIBPSRequest(ctx, account, token, bpsBody)
	if err != nil {
		return nil, fmt.Errorf("BPS request failed: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("BPS returned %d: %s", statusCode, extractUpstreamErrorMessage(errBody))
	}
	stream := bridge.Stream(resp.Body)
	defer func() { _ = stream.Close() }()
	output, err := parseOpenAIProbeStream(stream)
	output.AppliedEffort = bridge.Effort
	return output, err
}

func (s *AccountTestService) runOpenAIProbeNative(ctx context.Context, account *Account, token string, body []byte) (*bpsProbeOutput, error) {
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

	resp, err := s.doOpenAIAccountTestUpstream(req, "", account, true)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, bpsErrorBodyLimit))
		return nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, extractUpstreamErrorMessage(errBody))
	}
	return parseOpenAIProbeStream(resp.Body)
}

// parseOpenAIProbeStream 汇总 Responses SSE 的文本与用量。返回值始终非 nil，出错时带部分内容。
func parseOpenAIProbeStream(body io.Reader) (*bpsProbeOutput, error) {
	output := &bpsProbeOutput{}
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
				if content.Len() < bpsProbeMaxContent {
					content.WriteString(event.Get("delta").String())
				}
			case "response.completed", "response.done":
				response := event.Get("response")
				usage := response.Get("usage")
				output.InputTokens = int(usage.Get("input_tokens").Int())
				output.OutputTokens = int(usage.Get("output_tokens").Int())
				output.ReasoningTokens = int(usage.Get("output_tokens_details.reasoning_tokens").Int())
				if content.Len() == 0 {
					for _, item := range response.Get("output").Array() {
						if item.Get("type").String() != "message" {
							continue
						}
						for _, part := range item.Get("content").Array() {
							if part.Get("type").String() == "output_text" {
								content.WriteString(part.Get("text").String())
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
