package attachment_gateway

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/tidwall/gjson"
)

// InlineAttachmentStats contains only aggregate counts and sizes. It never
// retains attachment contents, prompts, data URLs or content hashes.
type InlineAttachmentStats struct {
	Count                 int
	Bytes                 int
	UnsupportedCount      int
	OptimizableImageCount int
	OptimizableImageBytes int
}

// InspectInlineAttachments visits known Responses/Chat-style attachment
// fields. It intentionally does not treat data URLs embedded in ordinary text
// as attachments, avoiding false positives when a user discusses a data URL.
func InspectInlineAttachments(body []byte) (InlineAttachmentStats, error) {
	if !json.Valid(body) {
		return InlineAttachmentStats{}, errors.New("attachment gateway: invalid JSON body")
	}

	root := gjson.ParseBytes(body)
	stats := InlineAttachmentStats{}
	seen := make(map[[2]int]struct{})

	visit := func(value gjson.Result, allowBareBase64 bool, optimizableImageField bool) {
		if value.Type != gjson.String {
			return
		}
		raw := value.String()
		if raw == "" {
			return
		}
		isDataURL := strings.HasPrefix(strings.ToLower(raw), "data:")
		if !isDataURL && !allowBareBase64 {
			return
		}

		key := [2]int{value.Index, value.Index + len(value.Raw)}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}

		stats.Count++
		stats.Bytes += len(raw)
		mimeType, encoded, base64Encoded := inlineDataURLParts(raw)
		if optimizableImageField && base64Encoded {
			if _, supported := supportedImageMIMETypes[mimeType]; supported {
				stats.OptimizableImageCount++
				stats.OptimizableImageBytes += estimatedBase64DecodedBytes(encoded)
				return
			}
		}
		stats.UnsupportedCount++
	}

	var walk func(gjson.Result)
	walk = func(value gjson.Result) {
		if value.IsArray() {
			value.ForEach(func(_, child gjson.Result) bool {
				walk(child)
				return true
			})
			return
		}
		if !value.IsObject() {
			return
		}

		partType := strings.ToLower(strings.TrimSpace(value.Get("type").String()))
		imageURL := value.Get("image_url")
		switch partType {
		case "input_image":
			visit(imageURL, false, true)
		case "image_url":
			if imageURL.Type == gjson.String {
				visit(imageURL, false, true)
			} else if imageURL.IsObject() {
				visit(imageURL.Get("url"), false, true)
			}
		case "input_audio", "audio":
			visit(value.Get("data"), true, false)
			inputAudio := value.Get("input_audio")
			if inputAudio.IsObject() {
				visit(inputAudio.Get("data"), true, false)
			}
		case "input_video", "video_url":
			visit(value.Get("data"), true, false)
			visit(value.Get("video_url"), false, false)
		}

		children := make([]gjson.Result, 0, 4)
		value.ForEach(func(key, child gjson.Result) bool {
			switch key.String() {
			case "file_data":
				// Responses input_file accepts either a data URL or a bare base64
				// string. Phase 1 observes and budgets it but does not transform it.
				visit(child, true, false)
			}
			if child.IsArray() || child.IsObject() {
				children = append(children, child)
			}
			return true
		})
		for _, child := range children {
			walk(child)
		}
	}

	walk(root)
	return stats, nil
}

func inlineDataURLParts(raw string) (mimeType string, encoded string, base64Encoded bool) {
	header, payload, found := strings.Cut(raw, ",")
	if !found || !strings.HasPrefix(strings.ToLower(header), "data:") {
		return "", raw, false
	}
	headerParts := strings.Split(header[len("data:"):], ";")
	mimeType = strings.ToLower(strings.TrimSpace(headerParts[0]))
	for _, parameter := range headerParts[1:] {
		if strings.EqualFold(strings.TrimSpace(parameter), "base64") {
			return mimeType, payload, true
		}
	}
	return mimeType, payload, false
}

func estimatedBase64DecodedBytes(encoded string) int {
	length := 0
	padding := 0
	for index := len(encoded) - 1; index >= 0; index-- {
		switch encoded[index] {
		case ' ', '\t', '\r', '\n':
			continue
		case '=':
			if padding < 2 {
				padding++
			}
		default:
			index = -1
		}
	}
	for index := 0; index < len(encoded); index++ {
		switch encoded[index] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			length++
		}
	}
	if length == 0 {
		return 0
	}
	decoded := (length / 4) * 3
	if remainder := length % 4; remainder > 1 {
		decoded += remainder - 1
	}
	if padding > decoded {
		return 0
	}
	return decoded - padding
}
