package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	// Gemini 3.8 TTS 官方音频 token 换算率：25 tokens/秒（1,500 tokens/分钟）。
	gemini38AudioTokensPerSecond = 25
	// Gemini 2.5 TTS 官方音频 token 换算率：32 tokens/秒（1,920 tokens/分钟）。
	gemini25AudioTokensPerSecond = 32
	// Gemini TTS 默认采样率与默认音色。
	geminiTTSDefaultSampleRate = 24000
	geminiTTSDefaultVoice      = "Charon"
)

// geminiPresetVoicesCanonical 保存 Gemini TTS 官方 30 个预设音色的小写→规范名映射。
var geminiPresetVoicesCanonical = map[string]string{
	"achernar":      "Achernar",
	"achird":        "Achird",
	"algenib":       "Algenib",
	"algieba":       "Algieba",
	"alnilam":       "Alnilam",
	"aoede":         "Aoede",
	"autonoe":       "Autonoe",
	"callirrhoe":    "Callirrhoe",
	"charon":        "Charon",
	"despina":       "Despina",
	"enceladus":     "Enceladus",
	"erinome":       "Erinome",
	"fenrir":        "Fenrir",
	"gacrux":        "Gacrux",
	"iapetus":       "Iapetus",
	"kore":          "Kore",
	"laomedeia":     "Laomedeia",
	"leda":          "Leda",
	"orus":          "Orus",
	"puck":          "Puck",
	"pulcherrima":   "Pulcherrima",
	"rasalgethi":    "Rasalgethi",
	"sadachbia":     "Sadachbia",
	"sadaltager":    "Sadaltager",
	"schedar":       "Schedar",
	"sulafat":       "Sulafat",
	"umbriel":       "Umbriel",
	"vindemiatrix":  "Vindemiatrix",
	"zephyr":        "Zephyr",
	"zubenelgenubi": "Zubenelgenubi",
}

// openAIVoiceToGeminiVoice 为通用 OpenAI TTS 客户端提供平滑的音色别名映射。
var openAIVoiceToGeminiVoice = map[string]string{
	"alloy":   "Puck",
	"ash":     "Achird",
	"ballad":  "Callirrhoe",
	"coral":   "Leda",
	"echo":    "Charon",
	"fable":   "Sadaltager",
	"nova":    "Kore",
	"onyx":    "Charon",
	"sage":    "Schedar",
	"shimmer": "Aoede",
	"verse":   "Fenrir",
}

// WAVAudioInfo 描述解析后的标准 RIFF/WAV 音频元信息。
type WAVAudioInfo struct {
	AudioFormat   uint16
	Channels      uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
	DataOffset    int
	DataBytes     uint32
	DurationMs    int64
}

// GeminiInteractionResult 承载一次 /v1beta/interactions 转发的完整结果与计费信息。
type GeminiInteractionResult struct {
	ForwardResult   *ForwardResult
	RawResponseBody []byte
	WAVAudio        []byte
	AudioInfo       *WAVAudioInfo
	AudioMimeType   string
	StatusCode      int
}

// OpenAISpeechRequest 表示 OpenAI 兼容 /v1/audio/speech 的入站请求。
type OpenAISpeechRequest struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          string   `json:"voice"`
	Instructions   string   `json:"instructions"`
	ResponseFormat string   `json:"response_format"`
	Speed          *float64 `json:"speed"`
}

// IsGeminiInteractionsAccount 判断账号是否支持 Google AI Studio /v1beta/interactions。
// Code Assist OAuth（带 project_id 的 v1internal）不支持 interactions 端点，需跳过。
func IsGeminiInteractionsAccount(account *Account) bool {
	if account == nil || account.Platform != PlatformGemini {
		return false
	}
	switch account.Type {
	case AccountTypeAPIKey:
		return strings.TrimSpace(account.GetCredential("api_key")) != ""
	case AccountTypeOAuth:
		return strings.TrimSpace(account.GetCredential("project_id")) == ""
	default:
		return false
	}
}

// GeminiAudioTokensPerSecond 返回对应 Gemini TTS 模型的官方音频输出 token 速率。
func GeminiAudioTokensPerSecond(model string) int {
	lower := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(lower, "gemini-2.5") || strings.Contains(lower, "gemini-2-5") {
		return gemini25AudioTokensPerSecond
	}
	return gemini38AudioTokensPerSecond
}

// NormalizeGeminiTTSVoice 将客户端传入的音色名规整为 Gemini 音色名。
// 支持全部 30 个 Gemini 官方预设音色（大小写不敏感）、常见 OpenAI 音色别名，以及原样透传自定义/扩展音色 ID。
func NormalizeGeminiTTSVoice(voice string) string {
	trimmed := strings.TrimSpace(voice)
	if trimmed == "" {
		return geminiTTSDefaultVoice
	}
	lower := strings.ToLower(trimmed)
	if canonical, ok := geminiPresetVoicesCanonical[lower]; ok {
		return canonical
	}
	if mapped, ok := openAIVoiceToGeminiVoice[lower]; ok {
		return mapped
	}
	return trimmed
}

// ParseWAVAudioInfo 解析标准 RIFF/WAV 文件头，返回精确的音频参数、data 偏移与毫秒时长。
func ParseWAVAudioInfo(wav []byte) (*WAVAudioInfo, error) {
	if len(wav) < 44 || string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, errors.New("invalid RIFF/WAVE header")
	}
	info := &WAVAudioInfo{}
	var hasFmt, hasData bool
	offset := 12
	for offset+8 <= len(wav) {
		id := string(wav[offset : offset+4])
		size := binary.LittleEndian.Uint32(wav[offset+4 : offset+8])
		body := offset + 8
		if uint64(body)+uint64(size) > uint64(len(wav)) {
			return nil, errors.New("truncated WAV chunk")
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("WAV fmt chunk too short")
			}
			info.AudioFormat = binary.LittleEndian.Uint16(wav[body : body+2])
			info.Channels = binary.LittleEndian.Uint16(wav[body+2 : body+4])
			info.SampleRate = binary.LittleEndian.Uint32(wav[body+4 : body+8])
			info.ByteRate = binary.LittleEndian.Uint32(wav[body+8 : body+12])
			info.BlockAlign = binary.LittleEndian.Uint16(wav[body+12 : body+14])
			info.BitsPerSample = binary.LittleEndian.Uint16(wav[body+14 : body+16])
			hasFmt = true
		case "data":
			info.DataOffset = body
			info.DataBytes = size
			hasData = true
		}
		next := body + int(size)
		if size%2 == 1 {
			next++
		}
		offset = next
	}
	if !hasFmt || !hasData {
		return nil, errors.New("WAV missing fmt or data chunk")
	}
	if info.ByteRate == 0 || info.DataBytes == 0 {
		return nil, errors.New("WAV has zero byte rate or empty audio data")
	}
	info.DurationMs = int64(math.Round((float64(info.DataBytes) / float64(info.ByteRate)) * 1000.0))
	return info, nil
}

// ExtractPCMFromWAV 从标准 WAV 字节中提取原始 PCM data chunk（保留声道/采样率/位深）。
func ExtractPCMFromWAV(wav []byte) ([]byte, error) {
	info, err := ParseWAVAudioInfo(wav)
	if err != nil {
		return nil, err
	}
	end := info.DataOffset + int(info.DataBytes)
	if end > len(wav) {
		return nil, errors.New("WAV data chunk out of bounds")
	}
	return wav[info.DataOffset:end], nil
}

// EstimateWAVAudioTokens 按官方每秒音频 token 速率由 WAV dataBytes/byteRate 向上取整估算音频 token。
func EstimateWAVAudioTokens(info *WAVAudioInfo, model string) int {
	if info == nil || info.ByteRate == 0 || info.DataBytes == 0 {
		return 0
	}
	tokensPerSec := GeminiAudioTokensPerSecond(model)
	return int(math.Ceil((float64(info.DataBytes) * float64(tokensPerSec)) / float64(info.ByteRate)))
}

// ExtractInteractionAudioWAV 从 Google v1beta/interactions 响应 JSON 中提取最后一个 audio block 并解码。
// 同时兼容 steps[].content[] 与 outputs[] 两种结构。
func ExtractInteractionAudioWAV(respBody []byte) ([]byte, string, error) {
	if !gjson.ValidBytes(respBody) {
		return nil, "", errors.New("interactions response is not valid JSON")
	}
	var lastData, lastMime string
	collectAudio := func(content gjson.Result) {
		if !content.IsObject() {
			return
		}
		if strings.ToLower(strings.TrimSpace(content.Get("type").String())) != "audio" {
			return
		}
		data := strings.TrimSpace(content.Get("data").String())
		if data == "" {
			return
		}
		lastData = data
		lastMime = strings.TrimSpace(content.Get("mime_type").String())
	}

	gjson.GetBytes(respBody, "steps").ForEach(func(_, step gjson.Result) bool {
		if strings.ToLower(strings.TrimSpace(step.Get("type").String())) != "model_output" {
			return true
		}
		step.Get("content").ForEach(func(_, item gjson.Result) bool {
			collectAudio(item)
			return true
		})
		return true
	})

	if lastData == "" {
		gjson.GetBytes(respBody, "outputs").ForEach(func(_, out gjson.Result) bool {
			collectAudio(out)
			out.Get("content").ForEach(func(_, item gjson.Result) bool {
				collectAudio(item)
				return true
			})
			return true
		})
	}

	if lastData == "" {
		return nil, "", errors.New("Gemini interactions response did not contain an audio block")
	}
	raw, err := base64.StdEncoding.DecodeString(lastData)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(lastData)
		if err != nil {
			return nil, "", fmt.Errorf("failed to decode interaction audio base64: %w", err)
		}
	}
	if lastMime == "" {
		lastMime = "audio/wav"
	}
	return raw, lastMime, nil
}

// ExtractGeminiInteractionUsage 提取 /v1beta/interactions 的真实 token 用量，并以 WAV 真实时长作防漏兜底。
// 策略：
//  1. 优先读取上游返回的 usage / usage_metadata / usageMetadata 中的真实输入与音频输出 token；
//  2. 若解析到 WAV 音频，按官方速率（3.8 系列 25 tokens/s，2.5 系列 32 tokens/s）计算下界，
//     取 max(上游上报输出 token, WAV 时长折算 token)，防止上游漏返 usage 导致 0 扣费；
//  3. 若上游未返输入 token，按请求体中的正文与 style 文本做保守估算（至少 1 token）。
func ExtractGeminiInteractionUsage(model string, reqBody, respBody []byte, audioInfo *WAVAudioInfo) ClaudeUsage {
	inputTokens, outputTokens := parseGeminiInteractionReportedUsage(respBody)

	if wavTokens := EstimateWAVAudioTokens(audioInfo, model); wavTokens > outputTokens {
		outputTokens = wavTokens
	}
	if inputTokens <= 0 {
		inputTokens = estimateGeminiInteractionInputTokens(reqBody)
	}
	return ClaudeUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
}

func parseGeminiInteractionReportedUsage(respBody []byte) (inputTokens int, outputTokens int) {
	if !gjson.ValidBytes(respBody) {
		return 0, 0
	}
	var usageObj gjson.Result
	for _, key := range []string{"usage", "usage_metadata", "usageMetadata"} {
		if cand := gjson.GetBytes(respBody, key); cand.Exists() && cand.IsObject() {
			usageObj = cand
			break
		}
	}
	if !usageObj.Exists() {
		return 0, 0
	}

	for _, key := range []string{"total_input_tokens", "prompt_token_count", "promptTokenCount", "input_tokens"} {
		if v := usageObj.Get(key); v.Exists() && v.Int() > 0 {
			inputTokens = int(v.Int())
			break
		}
	}

	// 优先读取按模态拆分的 AUDIO 输出 token（若有），防止 text/audio 混淆；
	// 若无模态拆分，则回退到总输出 token。
	audioOutput := extractModalityAudioTokens(usageObj,
		"output_tokens_by_modality",
		"response_tokens_details",
		"candidates_tokens_details",
		"candidatesTokensDetails",
	)
	totalOutput := 0
	for _, key := range []string{"total_output_tokens", "response_token_count", "candidates_token_count", "candidatesTokenCount", "output_tokens"} {
		if v := usageObj.Get(key); v.Exists() && v.Int() > 0 {
			totalOutput = int(v.Int())
			break
		}
	}
	if audioOutput > 0 {
		outputTokens = audioOutput
	} else {
		outputTokens = totalOutput
	}
	return inputTokens, outputTokens
}

func extractModalityAudioTokens(usageObj gjson.Result, keys ...string) int {
	for _, key := range keys {
		arr := usageObj.Get(key)
		if !arr.Exists() || !arr.IsArray() {
			continue
		}
		sum := 0
		arr.ForEach(func(_, item gjson.Result) bool {
			if strings.EqualFold(strings.TrimSpace(item.Get("modality").String()), "AUDIO") {
				for _, tk := range []string{"tokens", "token_count", "tokenCount"} {
					if v := item.Get(tk); v.Exists() && v.Int() > 0 {
						sum += int(v.Int())
						break
					}
				}
			}
			return true
		})
		if sum > 0 {
			return sum
		}
	}
	return 0
}

func estimateGeminiInteractionInputTokens(reqBody []byte) int {
	if !gjson.ValidBytes(reqBody) {
		return 0
	}
	var sb strings.Builder
	input := gjson.GetBytes(reqBody, "input")
	switch {
	case input.Type == gjson.String:
		sb.WriteString(input.String())
	case input.IsArray():
		input.ForEach(func(_, turn gjson.Result) bool {
			if t := turn.Get("text").String(); t != "" {
				sb.WriteString(t)
			}
			turn.Get("content").ForEach(func(_, item gjson.Result) bool {
				if t := item.Get("text").String(); t != "" {
					sb.WriteString(t)
				}
				item.Get("annotations").ForEach(func(_, ann gjson.Result) bool {
					if style := ann.Get("style").String(); style != "" {
						sb.WriteString(" ")
						sb.WriteString(style)
					}
					return true
				})
				return true
			})
			return true
		})
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return 0
	}
	runes := utf8.RuneCountInString(text)
	byteEst := (len(text) + 2) / 3
	if byteEst > runes {
		return byteEst
	}
	return max(runes, 1)
}

// TranslateOpenAISpeechToGeminiInteraction 将 OpenAI /v1/audio/speech 请求转换为 Google /v1beta/interactions 请求体。
// 支持 response_format 为空、"wav"（默认）或 "pcm"；对需要额外编码器的格式（mp3/opus/aac/flac）返回明确错误。
func TranslateOpenAISpeechToGeminiInteraction(req OpenAISpeechRequest, upstreamModel string) ([]byte, string, error) {
	text := strings.TrimSpace(req.Input)
	if text == "" {
		return nil, "", errors.New("input text is required")
	}
	format := strings.ToLower(strings.TrimSpace(req.ResponseFormat))
	if format == "" {
		format = "wav"
	}
	switch format {
	case "wav", "pcm":
		// supported natively from Gemini 24kHz mono 16-bit PCM WAV output
	default:
		return nil, "", fmt.Errorf("unsupported response_format %q for Gemini TTS relay; supported formats are \"wav\" and \"pcm\"", format)
	}

	voice := NormalizeGeminiTTSVoice(req.Voice)
	style := strings.TrimSpace(req.Instructions)
	if req.Speed != nil && *req.Speed > 0 {
		speed := *req.Speed
		if speed < 0.25 || speed > 4.0 {
			return nil, "", errors.New("speed must be between 0.25 and 4.0")
		}
		if math.Abs(speed-1.0) >= 0.05 {
			formattedSpeed := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", speed), "0"), ".")
			speedHint := fmt.Sprintf("Speak at approximately %sx pace.", formattedSpeed)
			if style == "" {
				style = speedHint
			} else {
				style = style + " " + speedHint
			}
		}
	}

	textPart := map[string]any{
		"type": "text",
		"text": req.Input,
	}
	if style != "" {
		textPart["annotations"] = []map[string]any{
			{
				"type":  "speech_metadata",
				"style": style,
			},
		}
	}

	payload := map[string]any{
		"model": strings.TrimSpace(upstreamModel),
		"input": []map[string]any{
			{
				"type":    "user_input",
				"content": []map[string]any{textPart},
			},
		},
		"response_format": map[string]any{
			"type":        "audio",
			"mime_type":   "audio/wav",
			"sample_rate": geminiTTSDefaultSampleRate,
		},
		"generation_config": map[string]any{
			"speech_config": []map[string]any{
				{"voice": voice},
			},
		},
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal Gemini interaction request: %w", err)
	}
	return encoded, format, nil
}

// buildGeminiAIStudioInteractionsURL 构造 AI Studio /v1beta/interactions 上游地址。
func buildGeminiAIStudioInteractionsURL(baseURL string) (string, error) {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBase == "" {
		return "", errors.New("gemini base url is required")
	}
	return trimmedBase + "/v1beta/interactions", nil
}

// ForwardInteraction 将请求转发至 Google AI Studio POST /v1beta/interactions。
// passthroughToClient=true 时直接把上游 JSON 响应写回客户端（原生入口）；
// passthroughToClient=false 时把响应体与解出的 WAV 交由调用方转换为二进制音频输出（OpenAI 兼容入口）。
func (s *GeminiMessagesCompatService) ForwardInteraction(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	originalModel string,
	body []byte,
	passthroughToClient bool,
) (*GeminiInteractionResult, error) {
	startTime := time.Now()
	if strings.TrimSpace(originalModel) == "" {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Missing model in request body")
	}
	if len(body) == 0 {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Request body is empty")
	}
	if !IsGeminiInteractionsAccount(account) {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusServiceUnavailable,
			ResponseBody: []byte(`{"error":{"message":"Account does not support Gemini Interactions API"}}`),
		}
	}

	mappedModel := originalModel
	if account.Type == AccountTypeAPIKey || account.Type == AccountTypeServiceAccount {
		mappedModel = account.GetMappedModel(originalModel)
	}
	upstreamBody := body
	if mappedModel != "" && mappedModel != originalModel && gjson.ValidBytes(body) {
		if updated, err := sjson.SetBytes(body, "model", mappedModel); err == nil {
			upstreamBody = updated
		}
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	buildReq := func(reqCtx context.Context) (*http.Request, error) {
		baseURL := account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
		normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, err
		}
		fullURL, err := buildGeminiAIStudioInteractionsURL(normalizedBaseURL)
		if err != nil {
			return nil, err
		}
		upstreamReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, fullURL, bytes.NewReader(upstreamBody))
		if err != nil {
			return nil, err
		}
		upstreamReq.Header.Set("Content-Type", "application/json")
		switch account.Type {
		case AccountTypeAPIKey:
			apiKey := strings.TrimSpace(account.GetCredential("api_key"))
			if apiKey == "" {
				return nil, errors.New("gemini api_key not configured")
			}
			upstreamReq.Header.Set("x-goog-api-key", apiKey)
		case AccountTypeOAuth:
			if s.tokenProvider == nil {
				return nil, errors.New("gemini token provider not configured")
			}
			accessToken, err := s.tokenProvider.GetAccessToken(reqCtx, account)
			if err != nil {
				return nil, err
			}
			upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)
		default:
			return nil, fmt.Errorf("unsupported account type for Gemini interactions: %s", account.Type)
		}
		account.ApplyHeaderOverrides(reqCtx, upstreamReq.Header)
		return upstreamReq, nil
	}

	upstreamReq, err := buildReq(ctx)
	if err != nil {
		return nil, s.writeGoogleError(c, http.StatusBadGateway, err.Error())
	}

	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			Kind:               "request_error",
			Message:            safeErr,
		})
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(fmt.Sprintf(`{"error":{"message":%q}}`, safeErr)),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	requestID := firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("x-goog-request-id"))
	if requestID != "" {
		c.Header("x-request-id", requestID)
	}

	if resp.StatusCode >= 400 {
		errBody := s.readUpstreamErrorBody(resp)
		if s.accountRepo != nil || s.rateLimitService != nil {
			s.handleGeminiUpstreamError(ctx, account, resp.StatusCode, resp.Header, errBody)
		}
		if resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusUnauthorized ||
			resp.StatusCode == http.StatusForbidden ||
			resp.StatusCode == http.StatusPaymentRequired ||
			resp.StatusCode >= http.StatusInternalServerError {
			return nil, &UpstreamFailoverError{
				StatusCode:   resp.StatusCode,
				ResponseBody: errBody,
			}
		}
		if passthroughToClient {
			contentType := resp.Header.Get("Content-Type")
			if contentType == "" {
				contentType = "application/json"
			}
			c.Data(resp.StatusCode, contentType, errBody)
		}
		return nil, &GeminiInteractionClientError{
			StatusCode:  resp.StatusCode,
			ContentType: resp.Header.Get("Content-Type"),
			Body:        errBody,
		}
	}

	tooLarge := openAITooLargeError
	if passthroughToClient {
		tooLarge = func(gc *gin.Context) {
			_ = s.writeGoogleError(gc, http.StatusBadGateway, "Upstream response too large")
		}
	}
	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, tooLarge)
	if err != nil {
		return nil, err
	}

	wav, audioMime, wavErr := ExtractInteractionAudioWAV(respBody)
	var audioInfo *WAVAudioInfo
	if wavErr == nil && len(wav) > 0 {
		if parsedInfo, parseErr := ParseWAVAudioInfo(wav); parseErr == nil {
			audioInfo = parsedInfo
		} else {
			logger.LegacyPrintf("service.gemini_tts", "Gemini interaction WAV parse warning (account=%d model=%s): %v", account.ID, mappedModel, parseErr)
		}
	}

	usage := ExtractGeminiInteractionUsage(mappedModel, upstreamBody, respBody, audioInfo)

	if passthroughToClient {
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		c.Data(resp.StatusCode, contentType, respBody)
	}

	return &GeminiInteractionResult{
		ForwardResult: &ForwardResult{
			RequestID:     requestID,
			Usage:         usage,
			Model:         originalModel,
			UpstreamModel: mappedModel,
			Stream:        false,
			Duration:      time.Since(startTime),
		},
		RawResponseBody: respBody,
		WAVAudio:        wav,
		AudioInfo:       audioInfo,
		AudioMimeType:   audioMime,
		StatusCode:      resp.StatusCode,
	}, nil
}

// GeminiInteractionClientError 表示上游返回的非重试类 4xx 客户端错误（如参数非法）。
type GeminiInteractionClientError struct {
	StatusCode  int
	ContentType string
	Body        []byte
}

func (e *GeminiInteractionClientError) Error() string {
	return fmt.Sprintf("gemini interactions upstream returned HTTP %d", e.StatusCode)
}

var _ = io.EOF
