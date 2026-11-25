package attachment_gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"
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
	start          int
	end            int
	inspectedBytes int
	validated      bool
}

type imageURLRewrite struct {
	value   string
	webp    []byte
	changed bool
}

type jsonStringReplacement struct {
	start int
	end   int
	value []byte
	webp  []byte
}

const (
	maxTokenPreallocation = 64
	webpDataURLPrefix     = "data:image/webp;base64,"
)

// requestString aliases immutable request bytes only within request processing.
// Do not retain a substring in a cache or mutate the source while it is in use.
func requestString(body []byte) string {
	return unsafe.String(unsafe.SliceData(body), len(body))
}

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
func collectImageDataURLTokens(body []byte, limit int) ([]imageURLToken, bool, error) {
	if limit <= 0 {
		return nil, false, errors.New("attachment gateway: image URL token limit must be positive")
	}
	discovery, err := discoverAttachments(body, limit, false)
	return discovery.tokens, discovery.truncated, err
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
		if !imageURLTokenInBounds(body, token) {
			return body, false, errors.New("attachment gateway: invalid JSON string location")
		}
		if !token.validated {
			if _, err := imageURLTokenValue(body, token); err != nil {
				return body, false, err
			}
		}
		if rewritten[index].webp != nil {
			replacements = append(replacements, jsonStringReplacement{start: token.start, end: token.end, webp: rewritten[index].webp})
			continue
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
		replacementSize := len(replacement.value)
		if replacement.webp != nil {
			replacementSize = webpJSONSize(len(replacement.webp))
		}
		outputSize += replacementSize - (replacement.end - replacement.start)
	}
	output := make([]byte, 0, outputSize)
	cursor := 0
	for _, replacement := range replacements {
		if replacement.start < cursor {
			return body, false, errors.New("attachment gateway: overlapping JSON replacements")
		}
		output = append(output, body[cursor:replacement.start]...)
		if replacement.webp != nil {
			output = append(output, '"')
			output = append(output, webpDataURLPrefix...)
			output = base64.StdEncoding.AppendEncode(output, replacement.webp)
			output = append(output, '"')
		} else {
			output = append(output, replacement.value...)
		}
		cursor = replacement.end
	}
	output = append(output, body[cursor:]...)
	return output, true, nil
}

func webpJSONSize(size int) int {
	return 2 + len(webpDataURLPrefix) + base64.StdEncoding.EncodedLen(size)
}

func imageURLTokenInBounds(body []byte, token imageURLToken) bool {
	return token.start >= 0 && token.end <= len(body) && token.end >= token.start && token.end-token.start >= 2 &&
		body[token.start] == '"' && body[token.end-1] == '"'
}

const jsonStringSpecialBytes = "\\\"\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\b\t\n\u000b\f\r\u000e\u000f\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001a\u001b\u001c\u001d\u001e\u001f"

// Discovered tokens already passed whole-document JSON validation. An
// unescaped UTF-8 string needs neither unquoting nor a copy. Unmarshal still
// handles escapes, invalid UTF-8, and unvalidated token syntax as before.
func imageURLTokenValue(body []byte, token imageURLToken) (string, error) {
	if !imageURLTokenInBounds(body, token) {
		return "", errors.New("attachment gateway: invalid JSON string location")
	}
	inner := body[token.start+1 : token.end-1]
	if ((token.validated && bytes.IndexByte(inner, '\\') < 0) ||
		(!token.validated && !bytes.ContainsAny(inner, jsonStringSpecialBytes))) && utf8.Valid(inner) {
		if token.validated {
			return requestString(inner), nil
		}
		// Other callers (notably remote HTTP prefetch) may let the string
		// escape request processing. Do not give them a view of the body.
		return string(inner), nil
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

	mimeType, hasBase64 := dataURLHeaderParts(header)
	if _, ok := supportedImageMIMETypes[mimeType]; !ok {
		return dataURLImage{MIMEType: mimeType}, true, errUnsupportedMediaType
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
	// An admitted worker can outlive its request. Keep only the small MIME
	// label, never a substring backed by the original request body.
	return dataURLImage{MIMEType: strings.Clone(mimeType), Bytes: decoded}, true, nil
}

func isImageDataURL(raw string) bool {
	const prefix = "data:image/"
	if len(raw) >= len(prefix) && strings.EqualFold(raw[:len(prefix)], prefix) {
		return true
	}
	// Preserve the historical Unicode lowercasing of the prefix (e.g. İ)
	// without lowercasing or scanning the payload.
	for _, c := range prefix {
		r, n := utf8.DecodeRuneInString(raw)
		if unicode.ToLower(r) != c {
			return false
		}
		raw = raw[n:]
	}
	return true
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
