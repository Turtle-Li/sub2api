package basispoints

import "fmt"

// allowEncrypted 只对 agent_message 开启：原通道会加密代理间消息正文，BPS 能直接解密，
// 因此原样透传，既不猜测为明文也不丢弃。
func validateHistoryContent(value any, inputIndex int, field string, allowEncrypted bool) error {
	content, _ := value.([]any)
	for index, rawPart := range content {
		part, _ := rawPart.(object)
		switch kind := text(part["type"]); {
		case kind == "input_text", kind == "output_text", kind == "text", kind == "refusal":
		case kind == "encrypted_content" && allowEncrypted:
			if text(part["encrypted_content"]) == "" {
				return fmt.Errorf("basispoints agent message encrypted content must be a non-empty string (path=input[%d].%s[%d])", inputIndex, field, index)
			}
		case kind == "input_image":
			if err := validateImage(part); err != nil {
				return fmt.Errorf("%w (path=input[%d].%s[%d])", err, inputIndex, field, index)
			}
		default:
			return fmt.Errorf("basispoints supports text and HTTPS input_image content only (path=input[%d].%s[%d]; type=%s)", inputIndex, field, index, contentTypeDiagnostic(part))
		}
	}
	return nil
}

// Report only fixed protocol labels. A caller-controlled type can itself contain
// private data or log injection, so unrecognized values are never echoed.
func contentTypeDiagnostic(part object) string {
	if part == nil {
		return "non_object"
	}
	value, present := part["type"]
	if !present {
		return "missing"
	}
	kind, ok := value.(string)
	if !ok {
		return "non_string"
	}
	switch kind {
	case "image", "image_url", "input_file", "file", "document",
		"input_audio", "output_audio", "audio", "reasoning_text", "summary_text",
		"tool_use", "tool_result", "thinking", "redacted_thinking", "encrypted_content":
		return kind
	default:
		return "unknown"
	}
}
