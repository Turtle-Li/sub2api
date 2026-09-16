package service

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var openAIRequestTimezoneValidationCache sync.Map

var openAIRequestTimezoneSetOptions = sjson.Options{Optimistic: true}

const (
	openAIEnvironmentContextOpen  = "<environment_context>"
	openAIEnvironmentContextClose = "</environment_context>"
	openAITimezoneOpen            = "<timezone>"
	openAITimezoneClose           = "</timezone>"
)

// normalizeOpenAIRequestTimezone accepts canonical IANA location names (plus
// UTC) and rejects fixed-offset labels such as UTC+8. Location names are kept
// verbatim because the upstream prompt is descriptive context rather than a
// timestamp conversion API.
func normalizeOpenAIRequestTimezone(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("timezone is empty")
	}
	if len(value) > 64 || value == "Local" || (value != "UTC" && !strings.Contains(value, "/")) {
		return "", fmt.Errorf("timezone %q is not a canonical IANA location", value)
	}
	if _, ok := openAIRequestTimezoneValidationCache.Load(value); ok {
		return value, nil
	}
	if _, err := time.LoadLocation(value); err != nil {
		return "", fmt.Errorf("load timezone %q: %w", value, err)
	}
	openAIRequestTimezoneValidationCache.Store(value, struct{}{})
	return value, nil
}

// rewriteOpenAIRequestTimezone replaces only timezone tags nested in trusted
// developer/system fields. User-role content is deliberately excluded even if
// it contains lookalike XML. The function never inserts a missing tag.
func rewriteOpenAIRequestTimezone(body []byte, timezone string) ([]byte, bool, error) {
	timezone, err := normalizeOpenAIRequestTimezone(timezone)
	if err != nil {
		return nil, false, err
	}
	if !gjson.ValidBytes(body) {
		return nil, false, fmt.Errorf("invalid OpenAI request JSON")
	}
	// Most OpenAI-compatible clients do not send Codex environment context.
	// Avoid walking and decoding their potentially large input/message arrays.
	// A JSON unicode escape could encode any part of the marker, so only take
	// this fast path when neither the plain marker nor an escape is present.
	if !bytes.Contains(body, []byte("timezone")) && !bytes.Contains(body, []byte(`\u`)) {
		return body, false, nil
	}

	out := body
	changed := false
	for _, path := range []string{"instructions", "system", "response.instructions", "response.system", "session.instructions", "session.system"} {
		var pathChanged bool
		out, pathChanged, err = rewriteOpenAITimezoneTextContainer(out, path, timezone)
		if err != nil {
			return nil, false, err
		}
		changed = changed || pathChanged
	}
	for _, path := range []string{"input", "messages", "response.input", "response.messages", "session.input", "session.messages"} {
		var pathChanged bool
		out, pathChanged, err = rewriteOpenAITimezoneRoleArray(out, path, timezone)
		if err != nil {
			return nil, false, err
		}
		changed = changed || pathChanged
	}
	return out, changed, nil
}

func rewriteOpenAIRequestTimezoneForAccount(account *Account, body []byte) ([]byte, error) {
	timezone := account.EffectiveOpenAIRequestTimezone()
	if timezone == "" {
		return body, nil
	}
	rewritten, changed, err := rewriteOpenAIRequestTimezone(body, timezone)
	if err != nil || !changed {
		return body, err
	}
	return rewritten, nil
}

func rewriteOpenAITimezoneRoleArray(body []byte, path, timezone string) ([]byte, bool, error) {
	items := getOpenAIRequestTimezoneJSON(body, path)
	if !items.Exists() || !items.IsArray() {
		return body, false, nil
	}
	out := body
	changed := false
	for index, item := range items.Array() {
		role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
		if role != "developer" && role != "system" {
			continue
		}
		for _, suffix := range []string{"content", "text"} {
			var pathChanged bool
			var err error
			out, pathChanged, err = rewriteOpenAITimezoneTextContainer(
				out,
				fmt.Sprintf("%s.%d.%s", path, index, suffix),
				timezone,
			)
			if err != nil {
				return nil, false, err
			}
			changed = changed || pathChanged
		}
	}
	return out, changed, nil
}

func rewriteOpenAITimezoneTextContainer(body []byte, path, timezone string) ([]byte, bool, error) {
	value := getOpenAIRequestTimezoneJSON(body, path)
	if !value.Exists() {
		return body, false, nil
	}
	if value.Type == gjson.String {
		return rewriteOpenAITimezoneTextPath(body, path, value.String(), timezone)
	}
	if !value.IsArray() {
		return body, false, nil
	}

	out := body
	changed := false
	for index, item := range value.Array() {
		itemPath := fmt.Sprintf("%s.%d", path, index)
		if item.Type == gjson.String {
			var itemChanged bool
			var err error
			out, itemChanged, err = rewriteOpenAITimezoneTextPath(out, itemPath, item.String(), timezone)
			if err != nil {
				return nil, false, err
			}
			changed = changed || itemChanged
			continue
		}
		textPath := itemPath + ".text"
		text := getOpenAIRequestTimezoneJSON(out, textPath)
		if text.Type != gjson.String {
			continue
		}
		var itemChanged bool
		var err error
		out, itemChanged, err = rewriteOpenAITimezoneTextPath(out, textPath, text.String(), timezone)
		if err != nil {
			return nil, false, err
		}
		changed = changed || itemChanged
	}
	return out, changed, nil
}

// getOpenAIRequestTimezoneJSON borrows body only for the duration of the
// synchronous lookup. gjson.GetBytes intentionally copies large object/array
// results so they can outlive the input; these callers never retain a Result,
// and avoiding that copy keeps request-path memory bounded to the rewritten
// output rather than another full request body.
func getOpenAIRequestTimezoneJSON(body []byte, path string) gjson.Result {
	return gjson.Get(*(*string)(unsafe.Pointer(&body)), path)
}

func rewriteOpenAITimezoneTextPath(body []byte, path, text, timezone string) ([]byte, bool, error) {
	rewritten, changed := rewriteOpenAIEnvironmentTimezoneText(text, timezone)
	if !changed {
		return body, false, nil
	}
	out, err := sjson.SetBytesOptions(body, path, rewritten, &openAIRequestTimezoneSetOptions)
	if err != nil {
		return nil, false, fmt.Errorf("rewrite OpenAI request timezone at %s: %w", path, err)
	}
	return out, true, nil
}

func rewriteOpenAIEnvironmentTimezoneText(text, timezone string) (string, bool) {
	var out strings.Builder
	cursor := 0
	changed := false
	for cursor < len(text) {
		openRelative := strings.Index(text[cursor:], openAIEnvironmentContextOpen)
		if openRelative < 0 {
			break
		}
		openStart := cursor + openRelative
		contentStart := openStart + len(openAIEnvironmentContextOpen)
		closeRelative := strings.Index(text[contentStart:], openAIEnvironmentContextClose)
		if closeRelative < 0 {
			break
		}
		contentEnd := contentStart + closeRelative
		rewritten, contextChanged := rewriteOpenAITimezoneTags(text[contentStart:contentEnd], timezone)
		out.WriteString(text[cursor:contentStart])
		out.WriteString(rewritten)
		out.WriteString(openAIEnvironmentContextClose)
		cursor = contentEnd + len(openAIEnvironmentContextClose)
		changed = changed || contextChanged
	}
	if !changed {
		return text, false
	}
	out.WriteString(text[cursor:])
	return out.String(), true
}

func rewriteOpenAITimezoneTags(content, timezone string) (string, bool) {
	var out strings.Builder
	cursor := 0
	changed := false
	for cursor < len(content) {
		openRelative := strings.Index(content[cursor:], openAITimezoneOpen)
		if openRelative < 0 {
			break
		}
		openStart := cursor + openRelative
		valueStart := openStart + len(openAITimezoneOpen)
		closeRelative := strings.Index(content[valueStart:], openAITimezoneClose)
		if closeRelative < 0 {
			break
		}
		valueEnd := valueStart + closeRelative
		if strings.TrimSpace(content[valueStart:valueEnd]) == timezone {
			out.WriteString(content[cursor : valueEnd+len(openAITimezoneClose)])
			cursor = valueEnd + len(openAITimezoneClose)
			continue
		}
		out.WriteString(content[cursor:valueStart])
		out.WriteString(timezone)
		out.WriteString(openAITimezoneClose)
		cursor = valueEnd + len(openAITimezoneClose)
		changed = true
	}
	if !changed {
		return content, false
	}
	out.WriteString(content[cursor:])
	return out.String(), true
}

// withOpenAIWSRequestTimezone applies the first-frame rewrite and composes the
// same behavior after any existing per-turn transformer. It copies hooks so a
// failover attempt cannot mutate callbacks shared with another account.
func withOpenAIWSRequestTimezone(account *Account, firstMessage []byte, hooks *OpenAIWSIngressHooks) ([]byte, *OpenAIWSIngressHooks, error) {
	timezone := account.EffectiveOpenAIRequestTimezone()
	if timezone == "" {
		return firstMessage, hooks, nil
	}
	firstMessage, _, err := rewriteOpenAIRequestTimezone(firstMessage, timezone)
	if err != nil {
		return nil, nil, err
	}

	composed := &OpenAIWSIngressHooks{}
	if hooks != nil {
		*composed = *hooks
	}
	originalTransform := composed.TransformRequest
	originalTransformWithReplayBaseline := composed.transformRequestTimezoneReplayBaseline
	transformWithReplayBaseline := func(turn int, payload []byte, originalModel string) ([]byte, []byte, error) {
		baseline := payload
		if originalTransformWithReplayBaseline != nil {
			var err error
			baseline, _, err = originalTransformWithReplayBaseline(turn, payload, originalModel)
			if err != nil {
				return nil, nil, err
			}
		} else if originalTransform != nil {
			var err error
			baseline, err = originalTransform(turn, payload, originalModel)
			if err != nil {
				return nil, nil, err
			}
		}
		transformed, _, err := rewriteOpenAIRequestTimezone(baseline, timezone)
		if err != nil {
			return nil, nil, err
		}
		return baseline, transformed, nil
	}
	composed.transformRequestTimezoneReplayBaseline = transformWithReplayBaseline
	composed.TransformRequest = func(turn int, payload []byte, originalModel string) ([]byte, error) {
		_, transformed, err := transformWithReplayBaseline(turn, payload, originalModel)
		return transformed, err
	}
	return firstMessage, composed, nil
}
