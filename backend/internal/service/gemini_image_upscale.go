package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/tidwall/sjson"
)

type geminiUpscaledImage struct {
	part        map[string]any
	inlineKey   string
	mimeKey     string
	dataKey     string
	data        []byte
	contentType string
}

func newSynchronousImageResultID() string {
	return "imgsync_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func rewriteGeminiImageSize(body []byte, size string) ([]byte, error) {
	rewritten, err := sjson.SetBytes(body, "generationConfig.imageConfig.imageSize", strings.TrimSpace(size))
	if err != nil {
		return nil, fmt.Errorf("rewrite Gemini image size: %w", err)
	}
	return rewritten, nil
}

func processGeminiImageGenerationResponse(
	ctx context.Context,
	upscaler *ImageUpscaleService,
	resolve ImageStorageResolver,
	body []byte,
	scale int,
	storagePath string,
) ([]byte, error) {
	if scale == 0 {
		return body, nil
	}
	if upscaler == nil || !upscaler.Active() {
		return nil, imageUpscaleError("UNAVAILABLE", 0, false, nil)
	}
	if scale != 2 && scale != 4 {
		return nil, imageUpscaleError("INVALID_SCALE", 0, false, nil)
	}
	requestedSize := ImageBillingSize2K
	if scale == 4 {
		requestedSize = ImageBillingSize4K
	}
	upscaleCtx, cancelUpscale := imageUpscaleLifecycleContext(ctx, upscaler.cfg)
	defer cancelUpscale()

	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, imageUpscaleError("INVALID_IMAGE_RESPONSE", 0, false, err)
	}

	images := make([]geminiUpscaledImage, 0, 1)
	candidates, _ := response["candidates"].([]any)
	for _, candidateValue := range candidates {
		candidate, _ := candidateValue.(map[string]any)
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, partValue := range parts {
			part, _ := partValue.(map[string]any)
			inlineKey := "inlineData"
			inline, _ := part[inlineKey].(map[string]any)
			if inline == nil {
				inlineKey = "inline_data"
				inline, _ = part[inlineKey].(map[string]any)
			}
			if inline == nil {
				continue
			}

			mimeKey, dataKey := "mimeType", "data"
			contentType, _ := inline[mimeKey].(string)
			if strings.TrimSpace(contentType) == "" {
				mimeKey = "mime_type"
				contentType, _ = inline[mimeKey].(string)
			}
			contentType = strings.TrimSpace(contentType)
			if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
				continue
			}
			encoded, _ := inline[dataKey].(string)
			if strings.TrimSpace(encoded) == "" {
				return nil, imageUpscaleError("MISSING_IMAGE_OUTPUT", 0, false, nil)
			}

			normalized, err := prepareUpscaleSourceBase64(encoded, imageUpscaleMaxSourceBytes)
			if err != nil {
				return nil, err
			}
			source, err := decodePreparedUpscaleSourceBase64(normalized)
			if err != nil {
				return nil, err
			}
			width, height, detectedMime, err := decodeUpscaleImageConfig(source)
			if err != nil {
				clearBytes(source)
				return nil, imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, err)
			}
			actualScale, upscaleRequired := imageUpscaleScaleForDimensions(requestedSize, width, height)
			resultData := source
			resultMime := detectedMime
			if upscaleRequired {
				result, upscaleErr := upscaler.UpscaleBase64(upscaleCtx, encoded, actualScale)
				clearBytes(source)
				if upscaleErr != nil {
					return nil, upscaleErr
				}
				resultData = result.Data
				resultMime = result.MimeType
			}
			images = append(images, geminiUpscaledImage{
				part: part, inlineKey: inlineKey, mimeKey: mimeKey, dataKey: dataKey,
				data: resultData, contentType: resultMime,
			})
		}
	}
	if len(images) == 0 {
		return nil, imageUpscaleError("MISSING_IMAGE_OUTPUT", 0, false, nil)
	}
	defer func() {
		for i := range images {
			clearBytes(images[i].data)
		}
	}()

	var uploader *ImageResultUploader
	storageEnabled := false
	if resolve != nil {
		uploader, storageEnabled = resolve()
	}
	if storageEnabled && uploader != nil {
		resultID := newSynchronousImageResultID()
		urls := make([]string, len(images))
		storageFailed := false
		for i := range images {
			image := &images[i]
			url, err := uploader.SaveBytes(ctx, resultID, i, image.contentType, image.data)
			if err != nil {
				logImageStorageFallback(storagePath, len(images), err)
				storageFailed = true
				break
			}
			urls[i] = url
		}
		if !storageFailed {
			for i := range images {
				image := &images[i]
				url := urls[i]
				fileKey, fileMimeKey, fileURIKey := "fileData", "mimeType", "fileUri"
				if image.inlineKey == "inline_data" {
					fileKey, fileMimeKey, fileURIKey = "file_data", "mime_type", "file_uri"
				}
				image.part[fileKey] = map[string]any{fileMimeKey: image.contentType, fileURIKey: url}
				delete(image.part, image.inlineKey)
			}
		} else {
			for i := range images {
				image := &images[i]
				inline, _ := image.part[image.inlineKey].(map[string]any)
				inline[image.mimeKey] = image.contentType
				inline[image.dataKey] = base64.StdEncoding.EncodeToString(image.data)
			}
		}
	} else {
		for i := range images {
			image := &images[i]
			inline, _ := image.part[image.inlineKey].(map[string]any)
			inline[image.mimeKey] = image.contentType
			inline[image.dataKey] = base64.StdEncoding.EncodeToString(image.data)
		}
	}

	out, err := json.Marshal(response)
	if err != nil {
		return nil, imageUpscaleError("RESPONSE_REWRITE_FAILED", 0, false, err)
	}
	return out, nil
}
