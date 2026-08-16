package securityaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	ErrNoPromptText = errors.New("prompt audit request contains no user text")

	bearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+\-/]+=*`)
	apiKeyPattern = regexp.MustCompile(`(?i)\b(sk|rk|pk|api[_-]?key|token|secret|password)[-_:=\s]+[A-Za-z0-9._~+\-/]{8,}`)
	canaryPattern = regexp.MustCompile(`(?i)([A-Z]+_CANARY_)[A-Za-z0-9_-]+`)
	emailPattern  = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	phonePattern  = regexp.MustCompile(`(?:\+?\d[\d\s().-]{8,}\d)`)
)

const promptAuditPrioritySeparator = "\x00SUB2API_PROMPT_AUDIT_PRIORITY_END\x00"

const (
	// Codex sends rollout context as a string input to the Responses API. Keep
	// recognition deliberately narrow: ordinary user strings must stay on the
	// existing path even when they happen to contain similar prose.
	codexRolloutPromptPrefix       = "Analyze this rollout and produce JSON with"
	codexRolloutContextMarker      = "rollout_context:"
	codexRolloutConversationMarker = "rendered conversation (pre-rendered from rollout `.jsonl`; filtered response items):"
)

type promptSnapshotScope uint8

const (
	promptSnapshotScopeFull promptSnapshotScope = iota
	promptSnapshotScopeLatestUser
	promptSnapshotScopeLatestUserAndPreviousOutput
)

type promptSegment struct {
	text     string
	user     bool
	role     string
	boundary bool
}

func ExtractPromptSnapshot(req Request) (PromptSnapshot, error) {
	return extractPromptSnapshot(req, promptSnapshotScopeFull)
}

// ExtractAsyncPromptSnapshot builds the actionable async audit input. The
// current user turn is the only text allowed to produce a finding; older
// client-controlled context is intentionally excluded from this scan.
func ExtractAsyncPromptSnapshot(req Request) (PromptSnapshot, error) {
	return extractPromptSnapshot(req, promptSnapshotScopeLatestUser)
}

// ExtractBlockingPromptSnapshot builds the synchronous guard input. The
// latest-turn option is deliberately independent from asynchronous auditing.
func ExtractBlockingPromptSnapshot(req Request, latestTurnOnly bool) (PromptSnapshot, error) {
	scope := promptSnapshotScopeFull
	if latestTurnOnly {
		scope = promptSnapshotScopeLatestUserAndPreviousOutput
	}
	return extractPromptSnapshot(req, scope)
}

func extractPromptSnapshot(req Request, scope promptSnapshotScope) (PromptSnapshot, error) {
	var document any
	if err := json.Unmarshal(req.Body, &document); err != nil {
		return PromptSnapshot{}, errors.New("prompt audit request JSON is invalid")
	}
	extracted := extractProtocolSegments(req.Protocol, document)
	var segments []string
	switch scope {
	case promptSnapshotScopeLatestUser:
		segments = latestUserOnlySegments(extracted)
	case promptSnapshotScopeLatestUserAndPreviousOutput:
		segments = blockingSegmentsLatestUserAndPreviousOutput(extracted)
	default:
		segments = normalizeSegmentsLatestUserFirst(extracted)
	}
	if len(segments) == 0 {
		return PromptSnapshot{}, ErrNoPromptText
	}
	scanText, metadataText := buildPrioritizedScanText(segments)
	digest := sha256.Sum256([]byte(metadataText))
	stage := strings.TrimSpace(req.Stage)
	if stage == "" {
		stage = "http"
	}
	return PromptSnapshot{
		RequestID: req.RequestID, UserID: req.UserID, UsernameSnapshot: req.Username,
		UserEmailSnapshot: req.UserEmail, APIKeyID: req.APIKeyID, APIKeyNameSnapshot: req.APIKeyName,
		GroupID: cloneInt64Ptr(req.GroupID), GroupName: req.GroupName, Provider: req.Provider,
		Endpoint: req.Endpoint, Protocol: req.Protocol, Model: req.Model,
		PromptHash: hex.EncodeToString(digest[:]), RedactedPreview: BuildPromptPreview(metadataText, DefaultPromptPreviewMaxRunes),
		FullPrompt:   BuildFullPrompt(metadataText, DefaultFullPromptMaxRunes),
		PromptLength: utf8.RuneCountInString(metadataText), MessageCount: len(segments), Stage: stage,
		ScanText: scanText,
	}, nil
}

// DefaultPromptPreviewMaxRunes caps how much sanitized prompt text may be
// considered before BuildPromptPreview withholds the majority for storage/UI.
const DefaultPromptPreviewMaxRunes = 96

// DefaultFullPromptMaxRunes caps how much unredacted prompt text is persisted
// on an audit event for admin review. It is deliberately generous so realistic
// prompts are kept intact while bounding per-row storage.
const DefaultFullPromptMaxRunes = 65536

func extractProtocolSegments(protocol string, document any) []promptSegment {
	root, _ := document.(map[string]any)
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	switch protocol {
	case "openai_chat_completions", "openai_chat", "chat_completions":
		return extractChatLikeSegments(root)
	case "anthropic_messages", "claude_messages", "messages":
		return append(extractAnthropicSystem(root["system"]), extractMessages(root["messages"], clientInstructionRoles...)...)
	case "gemini", "gemini_generate_content":
		return extractGeminiRoot(root)
	case "openai_responses", "responses", "responses_websocket":
		if frameType := stringValue(root["type"]); frameType != "" || protocol == "responses_websocket" {
			if frameType != "response.create" {
				return nil
			}
			if input, exists := root["input"]; exists && input != nil {
				return append(extractInstructions(root["instructions"]), extractResponses(input)...)
			}
			if response, ok := root["response"].(map[string]any); ok {
				return append(extractInstructions(response["instructions"]), extractResponses(response["input"])...)
			}
			return extractInstructions(root["instructions"])
		}
		return append(extractInstructions(root["instructions"]), extractResponses(root["input"])...)
	case "openai_images", "grok_media", "media", "images":
		return userPromptSegments(extractMediaPrompts(root))
	default:
		if segments := extractChatLikeSegments(root); len(segments) > 0 {
			return segments
		}
		if responses := append(extractInstructions(root["instructions"]), extractResponsesValue(root["input"], false)...); len(responses) > 0 {
			return responses
		}
		if gemini := extractGeminiRoot(root); len(gemini) > 0 {
			return gemini
		}
		return userPromptSegments(extractMediaPrompts(root))
	}
}

// clientInstructionRoles are roles a client may freely populate. Attackers can
// place jailbreak/PII text in assistant/tool turns, so blocking audit must scan
// them too—not only user/system/developer instructions.
var clientInstructionRoles = []string{"user", "system", "developer", "assistant", "tool"}

func extractChatLikeSegments(root map[string]any) []promptSegment {
	if root == nil {
		return nil
	}
	return extractMessages(root["messages"], clientInstructionRoles...)
}

func extractMessages(value any, wantedRoles ...string) []promptSegment {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	wanted := make(map[string]struct{}, len(wantedRoles))
	for _, role := range wantedRoles {
		wanted[strings.ToLower(strings.TrimSpace(role))] = struct{}{}
	}
	result := make([]promptSegment, 0, len(items))
	for _, item := range items {
		message, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(stringValue(message["role"]))
		if _, match := wanted[role]; !match {
			// Even an unsupported role is a message boundary. Its text is not
			// part of this extractor's audit scope, but it must not let two user
			// messages collapse into one latest turn.
			result = append(result, promptSegment{role: role, boundary: true})
			continue
		}
		texts := contentTexts(message["content"])
		added := false
		for _, text := range texts {
			result = append(result, promptSegment{text: text, user: role == "user", role: role})
			if strings.TrimSpace(text) != "" {
				added = true
			}
		}
		if !added {
			// Preserve a role boundary for assistant/tool messages whose useful
			// payload is carried in tool_calls or another non-text field. Without
			// this marker, latest-user selection can bridge an older user turn into
			// the current one and recreate the historical false positive.
			result = append(result, promptSegment{user: role == "user", role: role, boundary: true})
		}
	}
	return result
}

func extractInstructions(value any) []promptSegment {
	switch typed := value.(type) {
	case string:
		if text := strings.TrimSpace(typed); text != "" {
			return []promptSegment{{text: text, role: "system"}}
		}
	case []any:
		return systemPromptSegments(contentTexts(typed))
	case map[string]any:
		return systemPromptSegments(contentTexts(typed))
	}
	return nil
}

func extractAnthropicSystem(value any) []promptSegment {
	switch typed := value.(type) {
	case string:
		if text := strings.TrimSpace(typed); text != "" {
			return []promptSegment{{text: text, role: "system"}}
		}
	case []any:
		return systemPromptSegments(contentTexts(typed))
	case map[string]any:
		return systemPromptSegments(contentTexts(typed))
	}
	return nil
}

func extractResponses(value any) []promptSegment {
	return extractResponsesValue(value, true)
}

func extractResponsesValue(value any, parseCodexRollout bool) []promptSegment {
	switch typed := value.(type) {
	case string:
		if parseCodexRollout {
			if segments, ok := extractCodexRolloutString(typed); ok {
				return segments
			}
		}
		return []promptSegment{{text: typed, user: true, role: "user"}}
	case []any:
		result := make([]promptSegment, 0, len(typed))
		for _, item := range typed {
			switch entry := item.(type) {
			case string:
				result = append(result, promptSegment{text: entry, user: true, role: "user"})
			case map[string]any:
				role := strings.ToLower(stringValue(entry["role"]))
				if role != "" && !isClientInstructionRole(role) {
					result = append(result, promptSegment{role: role, boundary: true})
					continue
				}
				added := false
				if content, exists := entry["content"]; exists {
					for _, text := range contentTexts(content) {
						result = append(result, promptSegment{text: text, user: role == "" || role == "user", role: role})
						if strings.TrimSpace(text) != "" {
							added = true
						}
					}
				} else if text := stringValue(entry["text"]); text != "" {
					result = append(result, promptSegment{text: text, user: role == "" || role == "user", role: role})
					added = true
				}
				if !added {
					// Responses function_call/function_call_output items commonly
					// omit role and carry their payload in non-text fields. Keep an
					// invisible boundary so they cannot join two user turns.
					result = append(result, promptSegment{user: role == "user", role: role, boundary: true})
				}
			}
		}
		return result
	case map[string]any:
		role := strings.ToLower(stringValue(typed["role"]))
		if role != "" && !isClientInstructionRole(role) {
			return []promptSegment{{role: role, boundary: true}}
		}
		texts := contentTexts(typed["content"])
		if !hasNonEmptyPromptText(texts) {
			return []promptSegment{{user: role == "user", role: role, boundary: role != ""}}
		}
		return promptSegmentsForRole(texts, role)
	default:
		return nil
	}
}

// extractCodexRolloutString recognizes the exact wrapper currently emitted by
// Codex for rollout-backed Responses requests. A failed or incomplete parse is
// intentionally reported as false so callers retain the original string input.
func extractCodexRolloutString(value string) ([]promptSegment, bool) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, codexRolloutPromptPrefix) {
		return nil, false
	}
	if strings.Count(trimmed, codexRolloutConversationMarker) != 1 {
		return nil, false
	}
	markerIndex := strings.Index(trimmed, codexRolloutConversationMarker)
	if markerIndex < len(codexRolloutPromptPrefix) {
		return nil, false
	}
	header := strings.TrimSpace(trimmed[:markerIndex])
	if !strings.Contains(header, codexRolloutContextMarker) {
		return nil, false
	}
	arrayText := strings.TrimSpace(trimmed[markerIndex+len(codexRolloutConversationMarker):])
	if arrayText == "" {
		return nil, false
	}
	var items []any
	if err := json.Unmarshal([]byte(arrayText), &items); err != nil {
		return nil, false
	}

	segments := make([]promptSegment, 0, len(items)+1)
	// Keep the generated header as an explicit boundary. It is context for the
	// nested messages, not the latest user turn itself.
	segments = append(segments, promptSegment{text: header, role: "rollout_context", boundary: true})
	hasMessageText := false
	hasUserText := false
	for index, item := range items {
		// Items are distinct rollout records even when two adjacent records carry
		// the same role. Keep multipart content within one record together, but
		// never let an older user record merge into the latest one.
		if index > 0 {
			segments = append(segments, promptSegment{boundary: true})
		}
		object, ok := item.(map[string]any)
		if !ok {
			segments = append(segments, promptSegment{boundary: true})
			continue
		}
		if strings.ToLower(stringValue(object["type"])) != "message" {
			// Function/tool-call items can carry large client-controlled payloads,
			// but they are not message text. Preserve their turn boundary without
			// accidentally auditing their arguments as a user message.
			segments = append(segments, promptSegment{role: strings.ToLower(stringValue(object["role"])), boundary: true})
			continue
		}
		role := strings.ToLower(stringValue(object["role"]))
		if !isClientInstructionRole(role) {
			segments = append(segments, promptSegment{role: role, boundary: true})
			continue
		}
		texts := contentTexts(object["content"])
		added := false
		for _, text := range texts {
			segments = append(segments, promptSegment{text: text, user: role == "user", role: role})
			if strings.TrimSpace(text) != "" {
				added = true
				hasMessageText = true
				if role == "user" {
					hasUserText = true
				}
			}
		}
		if !added {
			segments = append(segments, promptSegment{user: role == "user", role: role, boundary: true})
		}
	}
	if !hasMessageText || !hasUserText {
		return nil, false
	}
	return segments, true
}

func isClientInstructionRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user", "system", "developer", "assistant", "tool", "model":
		return true
	default:
		return false
	}
}

func extractGemini(value any) []promptSegment {
	var contents []any
	switch typed := value.(type) {
	case []any:
		contents = typed
	case map[string]any:
		contents = []any{typed}
	default:
		return nil
	}
	result := make([]promptSegment, 0, len(contents))
	for _, item := range contents {
		content, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(stringValue(content["role"]))
		if role != "" && !isClientInstructionRole(role) {
			result = append(result, promptSegment{role: role, boundary: true})
			continue
		}
		parts, _ := content["parts"].([]any)
		added := false
		for _, part := range parts {
			if object, ok := part.(map[string]any); ok {
				if text := stringValue(object["text"]); strings.TrimSpace(text) != "" {
					result = append(result, promptSegment{text: text, user: role == "" || role == "user", role: role})
					added = true
				}
			}
		}
		if !added {
			result = append(result, promptSegment{user: role == "user", role: role, boundary: true})
		}
	}
	return result
}

func extractGeminiRoot(root map[string]any) []promptSegment {
	if root == nil {
		return nil
	}
	result := extractGeminiSystemInstruction(root["systemInstruction"])
	result = append(result, extractGeminiSystemInstruction(root["system_instruction"])...)
	result = append(result, extractGemini(root["contents"])...)
	result = append(result, extractGemini(root["content"])...)
	result = append(result, extractGeminiInstances(root["instances"])...)
	if requests, ok := root["requests"].([]any); ok {
		for _, item := range requests {
			request, ok := item.(map[string]any)
			if !ok {
				continue
			}
			result = append(result, extractGeminiSystemInstruction(request["systemInstruction"])...)
			result = append(result, extractGeminiSystemInstruction(request["system_instruction"])...)
			result = append(result, extractGemini(request["contents"])...)
			result = append(result, extractGemini(request["content"])...)
			result = append(result, extractGeminiInstances(request["instances"])...)
		}
	}
	return result
}

func extractGeminiSystemInstruction(value any) []promptSegment {
	switch typed := value.(type) {
	case string:
		if text := strings.TrimSpace(typed); text != "" {
			return []promptSegment{{text: text, role: "system"}}
		}
	case map[string]any:
		if parts, ok := typed["parts"].([]any); ok {
			result := make([]promptSegment, 0, len(parts))
			for _, part := range parts {
				if object, ok := part.(map[string]any); ok {
					if text := stringValue(object["text"]); text != "" {
						result = append(result, promptSegment{text: text, role: "system"})
					}
				}
			}
			return result
		}
		return systemPromptSegments(contentTexts(typed))
	case []any:
		segments := extractGemini(typed)
		for index := range segments {
			segments[index].user = false
			segments[index].role = "system"
		}
		return segments
	}
	return nil
}

func extractGeminiInstances(value any) []promptSegment {
	instances, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]promptSegment, 0, len(instances))
	for _, item := range instances {
		if instance, ok := item.(map[string]any); ok {
			if prompt := stringValue(instance["prompt"]); prompt != "" {
				result = append(result, promptSegment{text: prompt, user: true, role: "user"})
			}
		}
	}
	return result
}

func extractMediaPrompts(root map[string]any) []string {
	if root == nil {
		return nil
	}
	result := make([]string, 0, 4)
	seen := map[string]struct{}{}
	var walk func(any, string)
	walk = func(value any, key string) {
		switch typed := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for childKey := range typed {
				keys = append(keys, childKey)
			}
			sort.Strings(keys)
			for _, childKey := range keys {
				walk(typed[childKey], childKey)
			}
		case []any:
			for _, item := range typed {
				walk(item, key)
			}
		case string:
			if !isMediaPromptKey(key) || looksLikeMediaPayload(typed) {
				return
			}
			text := strings.TrimSpace(typed)
			if text == "" {
				return
			}
			if _, duplicate := seen[text]; duplicate {
				return
			}
			seen[text] = struct{}{}
			result = append(result, text)
		}
	}
	walk(root, "")
	return result
}

func isMediaPromptKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	switch normalized {
	case "prompt", "inputprompt", "textprompt", "description", "query", "lyrics", "negativeprompt",
		"positiveprompt", "gptdescriptionprompt", "prompten", "finalprompt", "finalzhprompt",
		"origprompt", "actualprompt", "imageprompt", "input":
		return true
	default:
		return false
	}
}

func looksLikeMediaPayload(value string) bool {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "data:image/") || strings.HasPrefix(lower, "data:video/") ||
		strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return true
	}
	if len(trimmed) >= 256 {
		for _, r := range trimmed {
			alphaNumeric := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
			if !alphaNumeric && r != '+' && r != '/' && r != '=' {
				return false
			}
		}
		return true
	}
	return false
}

func contentTexts(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		result := make([]string, 0, len(typed))
		for _, part := range typed {
			object, ok := part.(map[string]any)
			if !ok {
				continue
			}
			typeName := strings.ToLower(stringValue(object["type"]))
			if typeName != "" && typeName != "text" && typeName != "input_text" && typeName != "output_text" {
				continue
			}
			if text := stringValue(object["text"]); text != "" {
				result = append(result, text)
			}
		}
		return result
	case map[string]any:
		if text := stringValue(typed["text"]); text != "" {
			return []string{text}
		}
	}
	return nil
}

func hasNonEmptyPromptText(texts []string) bool {
	for _, text := range texts {
		if strings.TrimSpace(text) != "" {
			return true
		}
	}
	return false
}

func normalizeSegmentsLatestUserFirst(values []promptSegment) []string {
	normalized := normalizedPromptSegments(values)
	if len(normalized) == 0 {
		return nil
	}
	priorityIndex := len(normalized) - 1
	for index := len(normalized) - 1; index >= 0; index-- {
		if isUserSegment(normalized[index]) {
			priorityIndex = index
			break
		}
	}
	result := make([]string, 0, len(normalized))
	result = append(result, normalized[priorityIndex].text)
	for index, segment := range normalized {
		if index != priorityIndex {
			result = append(result, segment.text)
		}
	}
	return result
}

func latestUserOnlySegments(values []promptSegment) []string {
	normalized := normalizedPromptSegmentsForTurnSelection(values)
	latestUserStart := latestUserSegmentStart(normalized)
	if latestUserStart < 0 {
		return nil
	}
	latestUserEnd := latestUserStart
	for latestUserEnd < len(normalized) && isUserSegment(normalized[latestUserEnd]) {
		latestUserEnd++
	}
	currentUserText := make([]string, 0, latestUserEnd-latestUserStart)
	for _, segment := range normalized[latestUserStart:latestUserEnd] {
		if !segment.boundary {
			currentUserText = append(currentUserText, segment.text)
		}
	}
	if strings.TrimSpace(strings.Join(currentUserText, "\n\n")) == "" {
		return nil
	}
	// Keep one segment so the async payload cannot accidentally treat a
	// multipart user turn as historical context during chunking.
	return []string{strings.Join(currentUserText, "\n\n")}
}

// blockingSegmentsLatestUserAndPreviousOutput limits synchronous guard input to
// the current user turn and the nearest preceding assistant/model turn. It is
// deliberately opt-in because full transcript scanning remains stronger at
// finding client-controlled content placed in older or non-user messages.
func blockingSegmentsLatestUserAndPreviousOutput(values []promptSegment) []string {
	normalized := normalizedPromptSegmentsForTurnSelection(values)
	latestUserStart := latestUserSegmentStart(normalized)
	if latestUserStart < 0 {
		// A request without user content cannot be narrowed safely. Preserve the
		// established full-snapshot behavior for unusual protocol payloads.
		return normalizeSegmentsLatestUserFirst(values)
	}
	latestUserEnd := latestUserStart
	for latestUserEnd < len(normalized) && isUserSegment(normalized[latestUserEnd]) {
		latestUserEnd++
	}
	currentUserText := make([]string, 0, latestUserEnd-latestUserStart)
	for _, segment := range normalized[latestUserStart:latestUserEnd] {
		if !segment.boundary {
			currentUserText = append(currentUserText, segment.text)
		}
	}
	if strings.TrimSpace(strings.Join(currentUserText, "\n\n")) == "" {
		return nil
	}
	// A single client turn may have several text content parts. Keep it in one
	// priority segment so every part of the latest input is scanned before the
	// prior output begins.
	selected := []promptSegment{{text: strings.Join(currentUserText, "\n\n"), user: true, role: "user"}}
	for index := latestUserStart - 1; index >= 0; index-- {
		if !isAssistantOutputSegment(normalized[index]) {
			continue
		}
		start := index
		for start > 0 && isAssistantOutputSegment(normalized[start-1]) {
			start--
		}
		selected = append(selected, normalized[start:index+1]...)
		break
	}
	return promptSegmentTexts(selected)
}

func normalizedPromptSegments(values []promptSegment) []promptSegment {
	normalized := make([]promptSegment, 0, len(values))
	for _, value := range values {
		value.text = strings.TrimSpace(value.text)
		if value.text != "" {
			normalized = append(normalized, value)
		}
	}
	return normalized
}

func normalizedPromptSegmentsForTurnSelection(values []promptSegment) []promptSegment {
	normalized := make([]promptSegment, 0, len(values))
	for _, value := range values {
		value.text = strings.TrimSpace(value.text)
		if value.text == "" && !value.boundary {
			continue
		}
		normalized = append(normalized, value)
	}
	return normalized
}

func latestUserSegmentStart(values []promptSegment) int {
	latest := -1
	for index := len(values) - 1; index >= 0; index-- {
		if isUserSegment(values[index]) {
			latest = index
			break
		}
	}
	for latest > 0 && isUserSegment(values[latest-1]) && !values[latest-1].boundary && !values[latest].boundary {
		latest--
	}
	return latest
}

func isUserSegment(segment promptSegment) bool {
	return segment.user || segment.role == "user"
}

func isAssistantOutputSegment(segment promptSegment) bool {
	return segment.role == "assistant" || segment.role == "model"
}

func promptSegmentTexts(values []promptSegment) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value.text != "" {
			result = append(result, value.text)
		}
	}
	return result
}

func buildPrioritizedScanText(segments []string) (scanText string, metadataText string) {
	metadataText = strings.Join(segments, "\n\n")
	if len(segments) <= 1 {
		return metadataText, metadataText
	}
	return segments[0] + promptAuditPrioritySeparator + strings.Join(segments[1:], "\n\n"), metadataText
}

func promptSegmentsForRole(texts []string, role string) []promptSegment {
	result := make([]promptSegment, 0, len(texts))
	for _, text := range texts {
		result = append(result, promptSegment{text: text, user: role == "" || role == "user", role: role})
	}
	return result
}

func userPromptSegments(texts []string) []promptSegment {
	return promptSegmentsForRole(texts, "user")
}

func systemPromptSegments(texts []string) []promptSegment {
	return promptSegmentsForRole(texts, "system")
}

func RedactPreview(value string, maxRunes int) string {
	value = bearerPattern.ReplaceAllString(value, "Bearer ***")
	value = apiKeyPattern.ReplaceAllStringFunc(value, func(match string) string {
		if index := strings.IndexAny(match, ":= \t"); index >= 0 {
			return match[:index+1] + "***"
		}
		return "***"
	})
	value = canaryPattern.ReplaceAllString(value, "${1}***")
	value = emailPattern.ReplaceAllString(value, "***@***")
	value = phonePattern.ReplaceAllString(value, "***PHONE***")
	return TrimRunes(value, maxRunes)
}

// BuildPromptPreview stores only a short, non-recoverable head of sanitized
// input. Ordinary confidential prompts must not land nearly intact in PostgreSQL
// or the admin UI merely because no secret regex matched.
func BuildPromptPreview(value string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = DefaultPromptPreviewMaxRunes
	}
	redacted := strings.TrimSpace(RedactPreview(value, maxRunes))
	if redacted == "" {
		return ""
	}
	runes := []rune(redacted)
	hadTruncation := strings.HasSuffix(redacted, "…")
	if hadTruncation && len(runes) > 0 {
		runes = runes[:len(runes)-1]
	}
	if len(runes) == 0 {
		return "***…"
	}
	// Short unlabelled secrets would otherwise leak a recoverable prefix (e.g.
	// 20 runes → 5 visible). Fully withhold anything below the keep threshold.
	const minLengthForPartialPreview = 32
	if len(runes) < minLengthForPartialPreview {
		if hadTruncation {
			return "***…"
		}
		return "***"
	}
	// Keep at most a quarter of the already-truncated text, and never more than
	// 24 runes, so the majority of prompt content is withheld by default.
	keep := len(runes) / 4
	if keep > 24 {
		keep = 24
	}
	preview := string(runes[:keep]) + "***"
	if hadTruncation || keep < len(runes) {
		preview += "…"
	}
	return preview
}

// BuildFullPrompt returns the complete prompt text for audit-event storage and
// admin review, without redaction. NUL bytes are stripped because PostgreSQL
// TEXT rejects them, and the result is capped at maxRunes.
func BuildFullPrompt(value string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = DefaultFullPromptMaxRunes
	}
	value = strings.ReplaceAll(value, "\x00", "")
	return TrimRunes(strings.TrimSpace(value), maxRunes)
}

// FullPromptFromScanText reconstructs the display prompt from the worker scan
// payload. buildPrioritizedScanText inserts exactly one priority separator
// between the prioritized segment and the remainder, so replacing it with the
// metadata joiner yields the original multi-segment text.
func FullPromptFromScanText(scanText string) string {
	return BuildFullPrompt(strings.ReplaceAll(scanText, promptAuditPrioritySeparator, "\n\n"), DefaultFullPromptMaxRunes)
}

// latestUserScanText narrows a legacy full-transcript async payload to the
// priority segment written by the previous async implementation. New
// latest-user payloads contain no separator and pass through unchanged.
func latestUserScanText(scanText string, legacyMessageCount int) (string, bool) {
	if legacyMessageCount <= 1 || strings.Count(scanText, promptAuditPrioritySeparator) != 1 {
		return scanText, false
	}
	latest, _, ok := strings.Cut(scanText, promptAuditPrioritySeparator)
	if !ok || strings.TrimSpace(latest) == "" {
		return scanText, false
	}
	return latest, true
}

func replaceSnapshotWithScanText(snapshot *PromptSnapshot, scanText string) {
	if snapshot == nil {
		return
	}
	metadataText := strings.ReplaceAll(scanText, promptAuditPrioritySeparator, "\n\n")
	digest := sha256.Sum256([]byte(metadataText))
	snapshot.PromptHash = hex.EncodeToString(digest[:])
	snapshot.RedactedPreview = BuildPromptPreview(metadataText, DefaultPromptPreviewMaxRunes)
	snapshot.FullPrompt = BuildFullPrompt(metadataText, DefaultFullPromptMaxRunes)
	snapshot.PromptLength = utf8.RuneCountInString(metadataText)
	if strings.TrimSpace(metadataText) == "" {
		snapshot.MessageCount = 0
	} else {
		snapshot.MessageCount = 1
	}
}

func TrimRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
