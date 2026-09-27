package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func (s *OpenAIGatewayService) upscaleOpenAIImagesResponse(
	ctx context.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
	upstreamModel string,
	body []byte,
	scale int,
) ([]byte, error) {
	if s == nil || s.imageUpscaler == nil || !s.imageUpscaler.Active() {
		return nil, imageUpscaleError("UNAVAILABLE", 0, false, nil)
	}
	if parsed == nil {
		return nil, imageUpscaleError("INVALID_REQUEST", 0, false, nil)
	}
	expectedScale, required := image25RequestedUpscaleScale(upstreamModel, parsed.Size)
	if !required {
		return body, nil
	}
	if expectedScale != scale {
		return nil, imageUpscaleError("INVALID_SCALE", 0, false, nil)
	}
	if !gjson.ValidBytes(body) {
		return nil, imageUpscaleError("INVALID_IMAGE_RESPONSE", 0, false, nil)
	}
	items := gjson.GetBytes(body, "data")
	if !items.IsArray() || len(items.Array()) == 0 {
		return nil, imageUpscaleError("MISSING_IMAGE_OUTPUT", 0, false, nil)
	}
	expectedCount := parsed.N
	if expectedCount <= 0 {
		expectedCount = 1
	}
	if len(items.Array()) != expectedCount {
		return nil, imageUpscaleError(
			"INVALID_IMAGE_COUNT",
			0,
			false,
			fmt.Errorf("upstream returned %d images, expected %d", len(items.Array()), expectedCount),
		)
	}
	upscaleCtx, cancelUpscale := imageUpscaleLifecycleContext(ctx, s.imageUpscaler.cfg)
	defer cancelUpscale()
	updated := body
	for index, item := range items.Array() {
		encoded := strings.TrimSpace(item.Get("b64_json").String())
		rawURL := strings.TrimSpace(item.Get("url").String())
		if encoded == "" && rawURL == "" {
			return nil, imageUpscaleError("MISSING_IMAGE_OUTPUT", 0, false, nil)
		}
		var result *ImageUpscaleResult
		var err error
		if encoded != "" {
			result, err = s.imageUpscaler.UpscaleBase64(upscaleCtx, encoded, scale)
		} else {
			result, err = s.imageUpscaler.upscaleBase64FromLoader(upscaleCtx, scale, func(loadCtx context.Context) (string, error) {
				if len(rawURL) >= len("data:") && strings.EqualFold(rawURL[:len("data:")], "data:") {
					return rawURL, nil
				}
				loaded, loadErr := s.fetchOpenAIImageURLBase64(loadCtx, account, rawURL)
				if loadErr != nil {
					return "", imageUpscaleError("SOURCE_DOWNLOAD_FAILED", 0, true, loadErr)
				}
				return loaded, nil
			})
		}
		if err != nil {
			return nil, err
		}
		resultEncoded := base64.StdEncoding.EncodeToString(result.Data)
		clearBytes(result.Data)
		if parsed == nil || parsed.ResponseFormat != "url" || item.Get("b64_json").Exists() {
			updated, err = sjson.SetBytes(updated, fmt.Sprintf("data.%d.b64_json", index), resultEncoded)
			if err != nil {
				return nil, imageUpscaleError("RESPONSE_REWRITE_FAILED", 0, false, err)
			}
		}
		if (parsed != nil && parsed.ResponseFormat == "url") || item.Get("url").Exists() {
			dataURL := "data:" + result.MimeType + ";base64," + resultEncoded
			updated, err = sjson.SetBytes(updated, fmt.Sprintf("data.%d.url", index), dataURL)
			if err != nil {
				return nil, imageUpscaleError("RESPONSE_REWRITE_FAILED", 0, false, err)
			}
		}
	}
	return updated, nil
}
