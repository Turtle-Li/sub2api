package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func (s *OpenAIGatewayService) upscaleOpenAIImageResults(
	ctx context.Context,
	parsed *OpenAIImagesRequest,
	results []openAIResponsesImageResult,
) error {
	if parsed == nil {
		return imageUpscaleError("INVALID_REQUEST", 0, false, nil)
	}
	if _, required := RequestedImageUpscaleScale(parsed.Size); !required {
		return nil
	}
	if s == nil || s.imageUpscaler == nil || !s.imageUpscaler.Active() {
		return imageUpscaleError("UNAVAILABLE", 0, false, nil)
	}
	upscaleCtx, cancelUpscale := imageUpscaleOperationContext(ctx, s.imageUpscaler.cfg, len(results))
	defer cancelUpscale()
	workClass := imageUpscaleWorkInteractive
	if len(results) > 1 {
		workClass = imageUpscaleWorkBatch
	}
	for i := range results {
		actualSize := detectOpenAIImageResultSize(results[i].Result)
		width, height, ok := parseImageBillingDimensions(actualSize)
		if !ok {
			return imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, nil)
		}
		scale, required := imageUpscaleScaleForDimensions(parsed.Size, width, height)
		if !required {
			results[i].Size = actualSize
			continue
		}
		var result *ImageUpscaleResult
		var err error
		if workClass == imageUpscaleWorkBatch {
			result, err = s.imageUpscaler.UpscaleBase64Batch(upscaleCtx, results[i].Result, scale)
		} else {
			result, err = s.imageUpscaler.UpscaleBase64(upscaleCtx, results[i].Result, scale)
		}
		if err != nil {
			return err
		}
		results[i].Result = base64.StdEncoding.EncodeToString(result.Data)
		results[i].OutputFormat = strings.TrimPrefix(result.MimeType, "image/")
		results[i].Size = fmt.Sprintf("%dx%d", result.Width, result.Height)
		clearBytes(result.Data)
	}
	return nil
}

func (s *OpenAIGatewayService) upscaleOpenAIImagesResponse(
	ctx context.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
	body []byte,
) ([]byte, error) {
	if s == nil || s.imageUpscaler == nil || !s.imageUpscaler.Active() {
		return nil, imageUpscaleError("UNAVAILABLE", 0, false, nil)
	}
	if parsed == nil {
		return nil, imageUpscaleError("INVALID_REQUEST", 0, false, nil)
	}
	_, required := RequestedImageUpscaleScale(parsed.Size)
	if !required {
		return body, nil
	}
	if !gjson.ValidBytes(body) {
		return nil, imageUpscaleError("INVALID_IMAGE_RESPONSE", 0, false, nil)
	}
	items := gjson.GetBytes(body, "data")
	itemList := items.Array()
	if !items.IsArray() || len(itemList) == 0 {
		return nil, imageUpscaleError("MISSING_IMAGE_OUTPUT", 0, false, nil)
	}
	expectedCount := parsed.N
	if expectedCount <= 0 {
		expectedCount = 1
	}
	if len(itemList) != expectedCount {
		return nil, imageUpscaleError(
			"INVALID_IMAGE_COUNT",
			0,
			false,
			fmt.Errorf("upstream returned %d images, expected %d", len(itemList), expectedCount),
		)
	}
	upscaleCtx, cancelUpscale := imageUpscaleOperationContext(ctx, s.imageUpscaler.cfg, len(itemList))
	defer cancelUpscale()
	workClass := imageUpscaleWorkInteractive
	if len(itemList) > 1 {
		workClass = imageUpscaleWorkBatch
	}
	updated := body
	for index, item := range itemList {
		encoded := strings.TrimSpace(item.Get("b64_json").String())
		rawURL := strings.TrimSpace(item.Get("url").String())
		if encoded == "" && rawURL == "" {
			return nil, imageUpscaleError("MISSING_IMAGE_OUTPUT", 0, false, nil)
		}
		resultEncoded := encoded
		resultURL := ""
		var resultMimeType string
		resultWidth, resultHeight := 0, 0
		if encoded != "" {
			normalized, normalizeErr := prepareUpscaleSourceBase64(encoded, imageUpscaleMaxSourceBytes)
			if normalizeErr != nil {
				return nil, normalizeErr
			}
			source, decodeErr := decodePreparedUpscaleSourceBase64(normalized)
			if decodeErr != nil {
				return nil, decodeErr
			}
			width, height, detectedMime, configErr := decodeUpscaleImageConfig(source)
			clearBytes(source)
			if configErr != nil {
				return nil, imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, configErr)
			}
			resultWidth, resultHeight = width, height
			resultMimeType = detectedMime
			if scale, required := imageUpscaleScaleForDimensions(parsed.Size, width, height); required {
				var result *ImageUpscaleResult
				var upscaleErr error
				if workClass == imageUpscaleWorkBatch {
					result, upscaleErr = s.imageUpscaler.UpscaleBase64Batch(upscaleCtx, encoded, scale)
				} else {
					result, upscaleErr = s.imageUpscaler.UpscaleBase64(upscaleCtx, encoded, scale)
				}
				if upscaleErr != nil {
					return nil, upscaleErr
				}
				resultEncoded = base64.StdEncoding.EncodeToString(result.Data)
				resultURL = result.URL
				resultMimeType = result.MimeType
				resultWidth, resultHeight = result.Width, result.Height
				clearBytes(result.Data)
			}
		} else {
			result, upscaleErr := s.imageUpscaler.upscaleBase64FromLoaderToRequestedSize(upscaleCtx, parsed.Size, workClass, func(loadCtx context.Context) (string, error) {
				if len(rawURL) >= len("data:") && strings.EqualFold(rawURL[:len("data:")], "data:") {
					return rawURL, nil
				}
				loaded, loadErr := s.fetchOpenAIImageURLBase64(loadCtx, account, rawURL)
				if loadErr != nil {
					return "", imageUpscaleError("SOURCE_DOWNLOAD_FAILED", 0, true, loadErr)
				}
				return loaded, nil
			})
			if upscaleErr != nil {
				return nil, upscaleErr
			}
			resultEncoded = base64.StdEncoding.EncodeToString(result.Data)
			resultURL = result.URL
			resultMimeType = result.MimeType
			resultWidth, resultHeight = result.Width, result.Height
			clearBytes(result.Data)
		}
		if resultURL == "" && (parsed == nil || parsed.ResponseFormat != "url" || item.Get("b64_json").Exists()) {
			var err error
			updated, err = sjson.SetBytes(updated, fmt.Sprintf("data.%d.b64_json", index), resultEncoded)
			if err != nil {
				return nil, imageUpscaleError("RESPONSE_REWRITE_FAILED", 0, false, err)
			}
		}
		if (parsed != nil && parsed.ResponseFormat == "url") || item.Get("url").Exists() || resultURL != "" {
			dataURL := resultURL
			if dataURL == "" {
				dataURL = "data:" + resultMimeType + ";base64," + resultEncoded
			}
			var err error
			updated, err = sjson.SetBytes(updated, fmt.Sprintf("data.%d.url", index), dataURL)
			if err != nil {
				return nil, imageUpscaleError("RESPONSE_REWRITE_FAILED", 0, false, err)
			}
		}
		updated, _ = sjson.SetBytes(updated, fmt.Sprintf("data.%d.size", index), fmt.Sprintf("%dx%d", resultWidth, resultHeight))
		if index == 0 {
			updated, _ = sjson.SetBytes(updated, "size", fmt.Sprintf("%dx%d", resultWidth, resultHeight))
		}
	}
	return updated, nil
}
