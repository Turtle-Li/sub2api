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
	discovery, err := discoverAttachments(body, 0, true)
	return discovery.inline, err
}

type attachmentDiscovery struct {
	tokens    []imageURLToken
	truncated bool
	inline    InlineAttachmentStats
}

// One traversal serves both callers. The budget uses the first occurrence of
// a field and case-folds part types; the rewriter historically uses the last
// image_url and last string type, and matches the type case-sensitively.
func discoverAttachments(body []byte, limit int, inspect bool) (attachmentDiscovery, error) {
	if !json.Valid(body) {
		return attachmentDiscovery{}, errors.New("attachment gateway: invalid JSON body")
	}
	discovery := attachmentDiscovery{}
	if limit > 0 {
		discovery.tokens = make([]imageURLToken, 0, min(limit, maxTokenPreallocation))
	}
	// A string can be reached by both an audio part and a nested part. Keep
	// the same first-visit accounting and order as InspectInlineAttachments.
	seen := make(map[[2]int]struct{})
	track := func(value gjson.Result, bare, image bool) int {
		raw := value.Str
		if value.Type != gjson.String || raw == "" || (!bare && !hasDataURLPrefix(raw)) {
			return 0
		}
		key := [2]int{value.Index, value.Index + len(value.Raw)}
		if _, exists := seen[key]; exists {
			return 0
		}
		seen[key] = struct{}{}
		discovery.inline.Count++
		discovery.inline.Bytes += len(raw)
		if image {
			mimeType, encoded, base64Encoded := inlineDataURLParts(raw)
			if _, supported := supportedImageMIMETypes[mimeType]; supported && base64Encoded {
				discovery.inline.OptimizableImageCount++
				discovery.inline.OptimizableImageBytes += estimatedBase64DecodedBytes(encoded)
				return len(raw)
			}
		}
		discovery.inline.UnsupportedCount++
		return 0
	}

	var collectErr error
	var walk func(gjson.Result) bool
	walk = func(value gjson.Result) bool {
		if value.IsArray() {
			keepWalking := true
			value.ForEach(func(_, child gjson.Result) bool {
				keepWalking = walk(child)
				return keepWalking
			})
			return keepWalking
		}
		if !value.IsObject() {
			return true
		}
		var firstType, firstImage, audio, audioData, videoURL, lastImage gjson.Result
		var lastType string
		var children, files [4]gjson.Result
		childCount, fileCount := 0, 0
		value.ForEach(func(key, child gjson.Result) bool {
			switch key.Str {
			case "type":
				if !firstType.Exists() {
					firstType = child
				}
				if child.Type == gjson.String {
					lastType = strings.TrimSpace(child.Str)
				}
			case "image_url":
				if !firstImage.Exists() {
					firstImage = child
				}
				lastImage = child
			case "input_audio":
				if inspect && !audio.Exists() {
					audio = child
				}
			case "data":
				if inspect && !audioData.Exists() {
					audioData = child
				}
			case "video_url":
				if inspect && !videoURL.Exists() {
					videoURL = child
				}
			case "file_data":
				if inspect {
					if fileCount < len(files) {
						files[fileCount] = child
					}
					fileCount++
				}
			}
			if child.Type == gjson.JSON {
				if childCount < len(children) {
					children[childCount] = child
				}
				childCount++
			}
			return true
		})
		var inspectedImage gjson.Result
		var inspectedBytes int
		if inspect {
			switch strings.ToLower(strings.TrimSpace(firstType.Str)) {
			case "input_image":
				inspectedImage = firstImage
			case "image_url":
				inspectedImage = imageURLValue(firstImage)
			case "input_audio", "audio":
				track(audioData, true, false)
				if audio.IsObject() {
					track(audio.Get("data"), true, false)
				}
			case "input_video", "video_url":
				track(audioData, true, false)
				track(videoURL, false, false)
			}
			inspectedBytes = track(inspectedImage, false, true)
			if fileCount <= len(files) {
				for _, file := range files[:fileCount] {
					track(file, true, false)
				}
			} else {
				value.ForEach(func(key, child gjson.Result) bool {
					if key.Str == "file_data" {
						track(child, true, false)
					}
					return true
				})
			}
		}
		if limit > 0 && !discovery.truncated && collectErr == nil {
			var candidate gjson.Result
			switch lastType {
			case "input_image":
				candidate = lastImage
			case "image_url":
				candidate = imageURLValue(lastImage)
			}
			if candidate.Type == gjson.String {
				token := imageURLToken{start: candidate.Index, end: candidate.Index + len(candidate.Raw), validated: true}
				if !imageURLTokenInBounds(body, token) || requestString(body[token.start:token.end]) != candidate.Raw {
					collectErr = errors.New("attachment gateway: invalid JSON string location")
				} else if isImageDataURL(candidate.Str) {
					if len(discovery.tokens) == limit {
						discovery.truncated = true
					} else {
						if inspectedBytes != 0 && inspectedImage.Index == token.start {
							token.inspectedBytes = inspectedBytes
						}
						discovery.tokens = append(discovery.tokens, token)
					}
				}
			}
			if !inspect && (discovery.truncated || collectErr != nil) {
				return false
			}
		}
		if childCount > len(children) {
			keepWalking := true
			value.ForEach(func(_, child gjson.Result) bool {
				if child.Type == gjson.JSON {
					keepWalking = walk(child)
				}
				return keepWalking
			})
			return keepWalking
		}
		for _, child := range children[:childCount] {
			if !walk(child) {
				return false
			}
		}
		return true
	}
	walk(gjson.Parse(requestString(body)))
	return discovery, collectErr
}

func imageURLValue(value gjson.Result) gjson.Result {
	if value.IsObject() {
		return value.Get("url")
	}
	return value
}

func hasDataURLPrefix(raw string) bool {
	return len(raw) >= len("data:") && strings.EqualFold(raw[:len("data:")], "data:")
}

func inlineDataURLParts(raw string) (mimeType string, encoded string, base64Encoded bool) {
	if !hasDataURLPrefix(raw) {
		return "", raw, false
	}
	header, payload, found := strings.Cut(raw, ",")
	if !found {
		return "", raw, false
	}
	mimeType, base64Encoded = dataURLHeaderParts(header)
	return mimeType, payload, base64Encoded
}

func dataURLHeaderParts(header string) (string, bool) {
	mime, rest, more := strings.Cut(header[len("data:"):], ";")
	mime = strings.ToLower(strings.TrimSpace(mime))
	for more {
		var param string
		param, rest, more = strings.Cut(rest, ";")
		if strings.EqualFold(strings.TrimSpace(param), "base64") {
			return mime, true
		}
	}
	return mime, false
}

func estimatedBase64DecodedBytes(encoded string) int {
	length := len(encoded)
	if strings.IndexByte(encoded, ' ') >= 0 || strings.IndexByte(encoded, '\t') >= 0 ||
		strings.IndexByte(encoded, '\r') >= 0 || strings.IndexByte(encoded, '\n') >= 0 {
		length = 0
		for index := 0; index < len(encoded); index++ {
			switch encoded[index] {
			case ' ', '\t', '\r', '\n':
				continue
			default:
				length++
			}
		}
	}
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
