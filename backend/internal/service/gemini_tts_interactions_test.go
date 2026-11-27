package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// buildTestPCM16MonoWAV 构造一个合法的 24kHz mono 16-bit PCM WAV（可选附带 LIST metadata chunk）。
func buildTestPCM16MonoWAV(durationSec float64, withExtraChunk bool) ([]byte, []byte) {
	const (
		sampleRate    = 24000
		channels      = 1
		bitsPerSample = 16
		blockAlign    = channels * bitsPerSample / 8
		byteRate      = sampleRate * blockAlign
	)
	numSamples := int(math.Round(durationSec * sampleRate))
	pcm := make([]byte, numSamples*blockAlign)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}

	var chunks bytes.Buffer
	// fmt chunk
	chunks.WriteString("fmt ")
	_ = binary.Write(&chunks, binary.LittleEndian, uint32(16))
	_ = binary.Write(&chunks, binary.LittleEndian, uint16(1))
	_ = binary.Write(&chunks, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&chunks, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&chunks, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&chunks, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&chunks, binary.LittleEndian, uint16(bitsPerSample))

	if withExtraChunk {
		extra := []byte("sub2api-meta")
		chunks.WriteString("LIST")
		_ = binary.Write(&chunks, binary.LittleEndian, uint32(len(extra)))
		chunks.Write(extra)
	}

	// data chunk
	chunks.WriteString("data")
	_ = binary.Write(&chunks, binary.LittleEndian, uint32(len(pcm)))
	chunks.Write(pcm)

	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(4+chunks.Len()))
	wav.WriteString("WAVE")
	wav.Write(chunks.Bytes())
	return wav.Bytes(), pcm
}

func TestGemini38TTSPricingMatches2027StandardRates(t *testing.T) {
	cfg := &config.Config{}
	pricingSvc := NewPricingService(cfg, nil)
	billingSvc := NewBillingService(cfg, pricingSvc)

	// 1) PricingService 必须返回独立的 TTS 标准价（不能回落到普通 gemini-3.8-flash）
	flashTTS := pricingSvc.GetModelPricing("gemini-3.8-flash-tts")
	require.NotNil(t, flashTTS)
	require.InDelta(t, 1e-6, flashTTS.InputCostPerToken, 1e-12)
	require.InDelta(t, 18e-6, flashTTS.OutputCostPerToken, 1e-12)

	liteTTS := pricingSvc.GetModelPricing("gemini-3.8-flash-lite-tts")
	require.NotNil(t, liteTTS)
	require.InDelta(t, 1e-6, liteTTS.InputCostPerToken, 1e-12)
	require.InDelta(t, 12e-6, liteTTS.OutputCostPerToken, 1e-12)

	// 2) BillingService 计算 60 秒音频（1,500 output audio tokens）与 1,000 input text tokens：
	//    flash-tts:      1000 * $1/1M ($0.001) + 1500 * $18/1M ($0.027) = $0.028
	//    flash-lite-tts: 1000 * $1/1M ($0.001) + 1500 * $12/1M ($0.018) = $0.019
	flashCost, err := billingSvc.CalculateCost("gemini-3.8-flash-tts", UsageTokens{
		InputTokens:  1000,
		OutputTokens: 1500,
	}, 1.0)
	require.NoError(t, err)
	require.InDelta(t, 0.001, flashCost.InputCost, 1e-9)
	require.InDelta(t, 0.027, flashCost.OutputCost, 1e-9)
	require.InDelta(t, 0.028, flashCost.ActualCost, 1e-9)

	liteCost, err := billingSvc.CalculateCost("gemini-3.8-flash-lite-tts", UsageTokens{
		InputTokens:  1000,
		OutputTokens: 1500,
	}, 1.0)
	require.NoError(t, err)
	require.InDelta(t, 0.001, liteCost.InputCost, 1e-9)
	require.InDelta(t, 0.018, liteCost.OutputCost, 1e-9)
	require.InDelta(t, 0.019, liteCost.ActualCost, 1e-9)

	// 3) 即使定价服务为 nil（走纯 BillingService fallback），也不能落到普通 gemini-3.8-flash ($0.75/$3.75)
	fallbackOnlyBilling := NewBillingService(cfg, nil)
	fallbackPricing, err := fallbackOnlyBilling.GetModelPricing("gemini-3.8-flash-tts")
	require.NoError(t, err)
	require.InDelta(t, 1e-6, fallbackPricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 18e-6, fallbackPricing.OutputPricePerToken, 1e-12)

	plainFlashPricing, err := fallbackOnlyBilling.GetModelPricing("gemini-3.8-flash")
	require.NoError(t, err)
	require.InDelta(t, 3.75e-6, plainFlashPricing.OutputPricePerToken, 1e-12)
}

func TestParseWAVAndExtractPCMWithExtraChunk(t *testing.T) {
	wav, expectedPCM := buildTestPCM16MonoWAV(2.4, true)
	info, err := ParseWAVAudioInfo(wav)
	require.NoError(t, err)
	require.Equal(t, uint16(1), info.Channels)
	require.Equal(t, uint32(24000), info.SampleRate)
	require.Equal(t, uint32(48000), info.ByteRate)
	require.Equal(t, int64(2400), info.DurationMs)
	require.Equal(t, 60, EstimateWAVAudioTokens(info, "gemini-3.8-flash-tts"))
	require.Equal(t, 77, EstimateWAVAudioTokens(info, "gemini-2.5-flash-preview-tts"))

	pcm, err := ExtractPCMFromWAV(wav)
	require.NoError(t, err)
	require.Equal(t, expectedPCM, pcm)
}

func TestExtractGeminiInteractionUsagePriorityAndFallback(t *testing.T) {
	wav, _ := buildTestPCM16MonoWAV(10.0, false) // 10s -> 250 audio tokens
	info, err := ParseWAVAudioInfo(wav)
	require.NoError(t, err)

	reqBody := []byte(`{"model":"gemini-3.8-flash-tts","input":[{"type":"user_input","content":[{"type":"text","text":"大家好，欢迎来到小白学AI！","annotations":[{"type":"speech_metadata","style":"Natural Mandarin"}]}]}]}`)

	// 场景 1：上游返回完整 usage_metadata（且输出音频 token >= WAV 折算值），使用上游上报值
	respWithUsage := []byte(`{
		"usage_metadata": {
			"prompt_token_count": 42,
			"response_token_count": 255,
			"response_tokens_details": [{"modality": "AUDIO", "token_count": 255}]
		}
	}`)
	u1 := ExtractGeminiInteractionUsage("gemini-3.8-flash-tts", reqBody, respWithUsage, info)
	require.Equal(t, 42, u1.InputTokens)
	require.Equal(t, 255, u1.OutputTokens)

	// 场景 2：上游未返任何 usage，兜底按 WAV 时长算 250 audio tokens + 请求文本估算 input tokens
	respNoUsage := []byte(`{"steps":[]}`)
	u2 := ExtractGeminiInteractionUsage("gemini-3.8-flash-tts", reqBody, respNoUsage, info)
	require.Greater(t, u2.InputTokens, 0)
	require.Equal(t, 250, u2.OutputTokens)

	// 场景 3：上游少报 output_tokens（如仅报 10），WAV 兜底拉回 250 防少扣
	respUnderreported := []byte(`{"usage":{"total_input_tokens":30,"total_output_tokens":10}}`)
	u3 := ExtractGeminiInteractionUsage("gemini-3.8-flash-tts", reqBody, respUnderreported, info)
	require.Equal(t, 30, u3.InputTokens)
	require.Equal(t, 250, u3.OutputTokens)
}

func TestTranslateOpenAISpeechToGeminiInteraction(t *testing.T) {
	speed := 1.15
	body, format, err := TranslateOpenAISpeechToGeminiInteraction(OpenAISpeechRequest{
		Model:          "gemini-3.8-flash-tts",
		Input:          "今天我们讲 Gemini TTS 接入中转。",
		Voice:          "nova",
		Instructions:   "自然清晰、语速适中",
		ResponseFormat: "",
		Speed:          &speed,
	}, "gemini-3.8-flash-tts")
	require.NoError(t, err)
	require.Equal(t, "wav", format)
	require.Equal(t, "gemini-3.8-flash-tts", gjson.GetBytes(body, "model").String())
	require.Equal(t, "Kore", gjson.GetBytes(body, "generation_config.speech_config.0.voice").String())
	require.Equal(t, "今天我们讲 Gemini TTS 接入中转。", gjson.GetBytes(body, "input.0.content.0.text").String())
	require.Contains(t, gjson.GetBytes(body, "input.0.content.0.annotations.0.style").String(), "自然清晰、语速适中")
	require.Contains(t, gjson.GetBytes(body, "input.0.content.0.annotations.0.style").String(), "1.15x")

	// 支持 pcm 输出与原生 Gemini 音色（大小写无关）
	bodyPCM, fmtPCM, err := TranslateOpenAISpeechToGeminiInteraction(OpenAISpeechRequest{
		Model:          "gemini-3.8-flash-tts",
		Input:          "测试 PCM",
		Voice:          "charon",
		ResponseFormat: "pcm",
	}, "gemini-3.8-flash-tts")
	require.NoError(t, err)
	require.Equal(t, "pcm", fmtPCM)
	require.Equal(t, "Charon", gjson.GetBytes(bodyPCM, "generation_config.speech_config.0.voice").String())

	// 不支持的编码格式（如 mp3）必须明确报错，不伪装编码
	_, _, err = TranslateOpenAISpeechToGeminiInteraction(OpenAISpeechRequest{
		Model:          "gemini-3.8-flash-tts",
		Input:          "hello",
		ResponseFormat: "mp3",
	}, "gemini-3.8-flash-tts")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported response_format")
}

type testDirectHTTPUpstream struct {
	client *http.Client
}

func (u *testDirectHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(req)
}

func (u *testDirectHTTPUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.client.Do(req)
}

func TestForwardInteractionNativeAndOpenAICompat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	wav, _ := buildTestPCM16MonoWAV(4.0, false) // 4s -> 100 audio tokens
	wavB64 := base64.StdEncoding.EncodeToString(wav)

	var seenPath, seenKey, seenModel string
	var nextStatus int
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenKey = r.Header.Get("x-goog-api-key")
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if m, ok := payload["model"].(string); ok {
			seenModel = m
		}
		if nextStatus >= 400 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(nextStatus)
			_, _ = w.Write([]byte(`{"error":{"message":"simulated upstream error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-goog-request-id", "goog-tts-req-001")
		_, _ = w.Write([]byte(`{
			"steps": [{
				"type": "model_output",
				"content": [{"type": "audio", "mime_type": "audio/wav", "data": "` + wavB64 + `"}]
			}]
		}`))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowPrivateHosts: true,
			},
		},
	}
	svc := &GeminiMessagesCompatService{
		cfg:          cfg,
		httpUpstream: &testDirectHTTPUpstream{client: upstream.Client()},
	}
	account := &Account{
		ID:          101,
		Name:        "gemini-aistudio-paid",
		Platform:    PlatformGemini,
		Type:        AccountTypeAPIKey,
		Concurrency: 5,
		Credentials: map[string]any{
			"api_key":  "AIza-test-secret",
			"base_url": upstream.URL,
		},
	}

	// Code Assist OAuth（有 project_id）必须被排除在 interactions 账号之外
	codeAssistAcc := &Account{
		ID:       102,
		Platform: PlatformGemini,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"project_id": "my-code-assist-proj",
		},
	}
	require.True(t, IsGeminiInteractionsAccount(account))
	require.False(t, IsGeminiInteractionsAccount(codeAssistAcc))

	// 测试 1：原生透传模式
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/interactions", nil)
	reqBody := []byte(`{"model":"gemini-3.8-flash-tts","input":[{"type":"user_input","content":[{"type":"text","text":"测试原生配音"}]}]}`)

	res, err := svc.ForwardInteraction(context.Background(), c, account, "gemini-3.8-flash-tts", reqBody, true)
	require.NoError(t, err)
	require.Equal(t, "/v1beta/interactions", seenPath)
	require.Equal(t, "AIza-test-secret", seenKey)
	require.Equal(t, "gemini-3.8-flash-tts", seenModel)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "goog-tts-req-001", res.ForwardResult.RequestID)
	require.Equal(t, 100, res.ForwardResult.Usage.OutputTokens) // WAV 4s * 25 = 100 tokens 兜底命中
	require.Greater(t, res.ForwardResult.Usage.InputTokens, 0)
	require.Equal(t, int64(4000), res.AudioInfo.DurationMs)
	require.Equal(t, wav, res.WAVAudio)
	require.Contains(t, rec.Body.String(), `"model_output"`)

	// 测试 2：非透传模式（供 OpenAI /v1/audio/speech 转为二进制 WAV 输出）
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	translated, format, err := TranslateOpenAISpeechToGeminiInteraction(OpenAISpeechRequest{
		Model:        "gemini-3.8-flash-tts",
		Input:        "测试 OpenAI 兼容配音接口",
		Voice:        "Charon",
		Instructions: "自然播报",
	}, "gemini-3.8-flash-tts")
	require.NoError(t, err)
	require.Equal(t, "wav", format)

	res2, err := svc.ForwardInteraction(context.Background(), c2, account, "gemini-3.8-flash-tts", translated, false)
	require.NoError(t, err)
	require.Empty(t, rec2.Body.Bytes(), "非透传模式不应直接写 JSON 到响应，由 handler 输出二进制音频")
	require.Equal(t, wav, res2.WAVAudio)
	require.Equal(t, 100, res2.ForwardResult.Usage.OutputTokens)
	require.Equal(t, time.Duration(0) < res2.ForwardResult.Duration || res2.ForwardResult.Duration == 0, true)

	// 测试 3：上游 429 触发 UpstreamFailoverError（允许换号重试）
	nextStatus = http.StatusTooManyRequests
	rec3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(rec3)
	c3.Request = httptest.NewRequest(http.MethodPost, "/v1beta/interactions", nil)
	_, err = svc.ForwardInteraction(context.Background(), c3, account, "gemini-3.8-flash-tts", reqBody, true)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)

	// 测试 4：上游 400 参数错误返回 GeminiInteractionClientError，不换号
	nextStatus = http.StatusBadRequest
	rec4 := httptest.NewRecorder()
	c4, _ := gin.CreateTestContext(rec4)
	c4.Request = httptest.NewRequest(http.MethodPost, "/v1beta/interactions", nil)
	_, err = svc.ForwardInteraction(context.Background(), c4, account, "gemini-3.8-flash-tts", reqBody, true)
	var clientErr *GeminiInteractionClientError
	require.ErrorAs(t, err, &clientErr)
	require.Equal(t, http.StatusBadRequest, clientErr.StatusCode)
	require.Equal(t, http.StatusBadRequest, rec4.Code)
}
