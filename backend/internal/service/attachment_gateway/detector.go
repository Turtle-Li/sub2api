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

type imageURLVisitor func(rawURL string) string

type jsonStringReplacement struct {
	start int
	end   int
	value []byte
}

// rewriteImageURLs visits only known Responses-style image fields. It splices
// changed JSON string tokens into the original body instead of decoding the
// complete document into map[string]any. This preserves unrelated number
// lexemes and avoids a second full-body buffer inside json.Decoder.
func rewriteImageURLs(body []byte, visitor imageURLVisitor) ([]byte, bool, error) {
	if !json.Valid(body) {
		return body, false, errors.New("attachment gateway: invalid JSON body")
	}

	root := gjson.ParseBytes(body)
	replacements := make([]jsonStringReplacement, 0, 4)
	seen := make(map[[2]int]struct{})
	var rewriteErr error

	visitString := func(value gjson.Result) {
		if rewriteErr != nil || value.Type != gjson.String {
			return
		}
		rawURL := value.String()
		rewritten := visitor(rawURL)
		if rewritten == rawURL {
			return
		}
		start := value.Index
		end := start + len(value.Raw)
		if start < 0 || end > len(body) || end <= start || !bytes.Equal(body[start:end], []byte(value.Raw)) {
			rewriteErr = errors.New("attachment gateway: invalid JSON string location")
			return
		}
		key := [2]int{start, end}
		if _, exists := seen[key]; exists {
			return
		}
		encoded, err := json.Marshal(rewritten)
		if err != nil {
			rewriteErr = errors.New("attachment gateway: encode rewritten image URL")
			return
		}
		seen[key] = struct{}{}
		replacements = append(replacements, jsonStringReplacement{start: start, end: end, value: encoded})
	}

	var walk func(gjson.Result)
	walk = func(value gjson.Result) {
		if rewriteErr != nil {
			return
		}
		if value.IsArray() {
			value.ForEach(func(_, child gjson.Result) bool {
				walk(child)
				return rewriteErr == nil
			})
			return
		}
		if !value.IsObject() {
			return
		}

		var partType string
		var imageURL gjson.Result
		children := make([]gjson.Result, 0, 4)
		value.ForEach(func(key, child gjson.Result) bool {
			switch key.String() {
			case "type":
				if child.Type == gjson.String {
					partType = strings.TrimSpace(child.String())
				}
			case "image_url":
				imageURL = child
			}
			if child.IsArray() || child.IsObject() {
				children = append(children, child)
			}
			return true
		})

		switch partType {
		case "input_image":
			visitString(imageURL)
		case "image_url":
			if imageURL.Type == gjson.String {
				visitString(imageURL)
			} else if imageURL.IsObject() {
				visitString(imageURL.Get("url"))
			}
		}
		for _, child := range children {
			walk(child)
			if rewriteErr != nil {
				return
			}
		}
	}
	walk(root)
	if rewriteErr != nil {
		return body, false, rewriteErr
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
