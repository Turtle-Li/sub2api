package attachment_gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

var supportedImageMIMETypes = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/webp": {},
}

type dataURLImage struct {
	MIMEType string
	Bytes    []byte
}

type imageURLToken struct {
	start int
	end   int
}

type imageURLRewrite struct {
	value   string
	changed bool
}

type jsonStringReplacement struct {
	start int
	end   int
	value []byte
}

const maxTokenPreallocation = 64

// collectImageDataURLTokens returns at most limit known Responses-style image
// fields whose decoded value is a data:image URL. It stops after finding one
// further eligible field and reports truncated. That keeps per-request token,
// outcome and replacement allocations bounded by the configured image limit
// even if a valid 100 MB JSON document contains a huge number of tiny parts.
//
// It deliberately does not decode the complete document into map[string]any:
// unrelated number lexemes and formatting remain untouched for the later
// splice. The returned tokens are safe to process concurrently before calling
// rewriteImageURLTokens.
func collectImageDataURLTokens(body []byte, limit int) (tokens []imageURLToken, truncated bool, err error) {
	if limit <= 0 {
		return nil, false, errors.New("attachment gateway: image URL token limit must be positive")
	}
	if !json.Valid(body) {
		return nil, false, errors.New("attachment gateway: invalid JSON body")
	}

	root := gjson.ParseBytes(body)
	initialCapacity := min(limit, maxTokenPreallocation)
	tokens = make([]imageURLToken, 0, initialCapacity)
	seen := make(map[[2]int]struct{}, initialCapacity)
	var visitErr error

	visitString := func(value gjson.Result) bool {
		if value.Type != gjson.String {
			return true
		}
		start := value.Index
		end := start + len(value.Raw)
		if start < 0 || end > len(body) || end <= start || !bytes.Equal(body[start:end], []byte(value.Raw)) {
			visitErr = errors.New("attachment gateway: invalid JSON string location")
			return false
		}
		key := [2]int{start, end}
		if _, exists := seen[key]; exists {
			return true
		}
		rawURL, valueErr := imageURLTokenValue(body, imageURLToken{start: start, end: end})
		if valueErr != nil {
			visitErr = valueErr
			return false
		}
		if !isImageDataURL(rawURL) {
			return true
		}
		if len(tokens) >= limit {
			truncated = true
			return false
		}
		seen[key] = struct{}{}
		tokens = append(tokens, imageURLToken{start: start, end: end})
		return true
	}

	var walk func(gjson.Result) bool
	walk = func(value gjson.Result) bool {
		if value.IsArray() {
			continueWalk := true
			value.ForEach(func(_, child gjson.Result) bool {
				continueWalk = walk(child)
				return continueWalk
			})
			return continueWalk
		}
		if !value.IsObject() {
			return true
		}

		var partType string
		var imageURL gjson.Result
		value.ForEach(func(key, child gjson.Result) bool {
			switch key.String() {
			case "type":
				if child.Type == gjson.String {
					partType = strings.TrimSpace(child.String())
				}
			case "image_url":
				imageURL = child
			}
			return true
		})

		switch partType {
		case "input_image":
			if !visitString(imageURL) {
				return false
			}
		case "image_url":
			if imageURL.Type == gjson.String {
				if !visitString(imageURL) {
					return false
				}
			} else if imageURL.IsObject() {
				if !visitString(imageURL.Get("url")) {
					return false
				}
			}
		}
		continueWalk := true
		value.ForEach(func(_, child gjson.Result) bool {
			if child.IsArray() || child.IsObject() {
				continueWalk = walk(child)
			}
			return continueWalk
		})
		return continueWalk
	}
	walk(root)
	if visitErr != nil {
		return nil, false, visitErr
	}
	return tokens, truncated, nil
}

// rewriteImageURLTokens applies replacements collected from
// collectImageDataURLTokens. It preserves unrelated JSON bytes exactly, including
// large numeric lexemes and formatting.
func rewriteImageURLTokens(body []byte, tokens []imageURLToken, rewritten []imageURLRewrite) ([]byte, bool, error) {
	if len(tokens) != len(rewritten) {
		return body, false, errors.New("attachment gateway: image URL replacement count mismatch")
	}
	replacements := make([]jsonStringReplacement, 0, len(tokens))
	for index, token := range tokens {
		if !rewritten[index].changed {
			continue
		}
		if _, err := imageURLTokenValue(body, token); err != nil {
			return body, false, err
		}
		encoded, err := json.Marshal(rewritten[index].value)
		if err != nil {
			return body, false, errors.New("attachment gateway: encode rewritten image URL")
		}
		replacements = append(replacements, jsonStringReplacement{start: token.start, end: token.end, value: encoded})
	}
	if len(replacements) == 0 {
		return body, false, nil
	}

	sort.Slice(replacements, func(left, right int) bool {
		return replacements[left].start < replacements[right].start
	})
	outputSize := len(body)
	for _, replacement := range replacements {
		outputSize += len(replacement.value) - (replacement.end - replacement.start)
	}
	output := make([]byte, 0, outputSize)
	cursor := 0
	for _, replacement := range replacements {
		if replacement.start < cursor {
			return body, false, errors.New("attachment gateway: overlapping JSON replacements")
		}
		output = append(output, body[cursor:replacement.start]...)
		output = append(output, replacement.value...)
		cursor = replacement.end
	}
	output = append(output, body[cursor:]...)
	return output, true, nil
}

func imageURLTokenValue(body []byte, token imageURLToken) (string, error) {
	if token.start < 0 || token.end > len(body) || token.end <= token.start {
		return "", errors.New("attachment gateway: invalid JSON string location")
	}
	var value string
	if err := json.Unmarshal(body[token.start:token.end], &value); err != nil {
		return "", errors.New("attachment gateway: invalid JSON string location")
	}
	return value, nil
}

func parseImageDataURL(raw string, maxImageBytes int) (dataURLImage, bool, error) {
	if !isImageDataURL(raw) {
		return dataURLImage{}, false, nil
	}
	header, encoded, found := strings.Cut(raw, ",")
	if !found {
		return dataURLImage{}, true, errors.New("attachment gateway: malformed image data URL")
	}

	headerParts := strings.Split(header[len("data:"):], ";")
	mimeType := strings.ToLower(strings.TrimSpace(headerParts[0]))
	if _, ok := supportedImageMIMETypes[mimeType]; !ok {
		return dataURLImage{MIMEType: mimeType}, true, errUnsupportedMediaType
	}
	hasBase64 := false
	for _, parameter := range headerParts[1:] {
		if strings.EqualFold(strings.TrimSpace(parameter), "base64") {
			hasBase64 = true
			break
		}
	}
	if !hasBase64 {
		return dataURLImage{MIMEType: mimeType}, true, errors.New("attachment gateway: image data URL is not base64 encoded")
	}

	compact := removeASCIIWhitespace(encoded)
	maxEncodedBytes := ((maxImageBytes + 2) / 3 * 4) + 4
	if len(compact) > maxEncodedBytes {
		return dataURLImage{MIMEType: mimeType}, true, errImageTooLarge
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(compact)
	if err != nil {
		return dataURLImage{MIMEType: mimeType}, true, errors.New("attachment gateway: invalid base64 image")
	}
	if len(decoded) > maxImageBytes {
		return dataURLImage{MIMEType: mimeType}, true, errImageTooLarge
	}
	return dataURLImage{MIMEType: mimeType, Bytes: decoded}, true, nil
}

func isImageDataURL(raw string) bool {
	return strings.HasPrefix(strings.ToLower(raw), "data:image/")
}

func removeASCIIWhitespace(value string) string {
	if !strings.ContainsAny(value, " \t\r\n") {
		return value
	}
	builder := strings.Builder{}
	builder.Grow(len(value))
	for _, char := range value {
		switch char {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			_, _ = builder.WriteRune(char)
		}
	}
	return builder.String()
}
