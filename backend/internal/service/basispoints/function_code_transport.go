package basispoints

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

const functionCodeTransportPrefix = "codex2api.function_code/"

// functionCodeTransportFields lists, in priority order, the string parameters
// that may travel as raw native code: upstream's explicit code parameter and
// Codex exec_command's cmd, whose shell quoting is the most common source of
// double-escaping failures in the JSON envelope.
var functionCodeTransportFields = []string{"code", "cmd"}

// functionCodeTransportField returns the raw-code argument for a function, or
// "" when it keeps the ordinary JSON envelope representation.
func functionCodeTransportField(name, kind string, parameters any) string {
	if kind != "function" || name == "" || strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, unicode.IsSpace) >= 0 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return ""
	}
	schema, _ := parameters.(object)
	if schema["type"] != "object" {
		return ""
	}
	properties, _ := schema["properties"].(object)
	for _, field := range functionCodeTransportFields {
		if property, _ := properties[field].(object); property["type"] == "string" {
			return field
		}
	}
	return ""
}

func supportsFunctionCodeTransport(name, kind string, parameters any) bool {
	return functionCodeTransportField(name, kind, parameters) != ""
}

// Keep executable text in the native code string. extended_summary carries only
// the other JSON arguments; the server serializes the final client arguments.
// No source text is parsed, repaired, evaluated or treated as another tool call.
func (b *Bridge) functionCodeTransportEnvelope(arguments object) (object, bool, error) {
	summary, ok := arguments["summary"].(string)
	if !ok || !strings.HasPrefix(summary, functionCodeTransportPrefix) {
		return nil, false, nil
	}
	name := strings.TrimPrefix(summary, functionCodeTransportPrefix)
	info, allowed := b.tools[name]
	if !allowed {
		// 模型常把宿主展示用的 functions. 前缀带进标记。
		if trimmed := strings.TrimPrefix(name, "functions."); trimmed != name {
			name = trimmed
			info, allowed = b.tools[name]
		}
	}
	field := ""
	if allowed {
		field = functionCodeTransportField(name, info.Kind, info.Parameters)
	}
	code, codeOK := arguments["code"].(string)
	metadata, metadataOK := arguments["extended_summary"].(string)
	if field == "" {
		// 标记指向普通 FUNCTION 工具时，code 若是一个 JSON 对象（参数或完整信封）仍可按原协议还原。
		if allowed && info.Kind == "function" && codeOK {
			if envelope, ok := plainFunctionFromCode(name, code); ok {
				return envelope, true, nil
			}
		}
		target := "unknown"
		if allowed {
			target = info.Kind + "_without_code_parameter"
		}
		return nil, true, fmt.Errorf("basispoints function code transport requires an exact catalog function with a string code parameter (marker_target=%s)", target)
	}
	if !codeOK || !metadataOK {
		return nil, true, fmt.Errorf("basispoints function code transport requires string code and JSON arguments in extended_summary")
	}
	if len(code) > maxEnvelopeBytes || len(metadata) > maxEnvelopeBytes-len(code) {
		return nil, true, fmt.Errorf("basispoints function code transport exceeds the size limit")
	}
	var args object
	if decode([]byte(metadata), &args) != nil || args == nil {
		return nil, true, fmt.Errorf("basispoints function code transport extended_summary must contain one JSON object")
	}
	if _, exists := args[field]; exists {
		return nil, true, fmt.Errorf("basispoints function code transport must not duplicate code in extended_summary")
	}
	args[field] = code
	encoded, err := json.Marshal(args)
	if err != nil || len(encoded) > maxEnvelopeBytes {
		return nil, true, fmt.Errorf("basispoints function code transport arguments exceed the size limit")
	}
	return object{"name": name, "arguments": args}, true, nil
}

func encodeFunctionCodeTransport(name, field string, args object) (object, error) {
	metadata := make(object, len(args))
	for key, value := range args {
		if key != field {
			metadata[key] = value
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("basispoints history function metadata cannot be serialized")
	}
	return object{
		"summary": functionCodeTransportPrefix + name, "code": args[field],
		"extended_summary": string(encoded), "destructive": false, "references": []any{},
	}, nil
}

// recoverUnmarkedFunctionCode accepts a FUNCTION_CODE call whose summary lost the
// exact marker (the model wrote a descriptive summary but still sent raw code plus
// metadata JSON). It only applies when code is not a JSON envelope, extended_summary
// is one JSON object, and exactly one catalog FUNCTION_CODE tool fits that metadata
// (or exactly one of the fitting tools is named in the summary). Nothing is parsed
// out of the code itself.
func (b *Bridge) recoverUnmarkedFunctionCode(arguments object) (object, bool) {
	code, codeOK := arguments["code"].(string)
	metadata, metadataOK := arguments["extended_summary"].(string)
	trimmed := strings.TrimSpace(code)
	if !codeOK || !metadataOK || trimmed == "" || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "```") {
		return nil, false
	}
	var args object
	if decode([]byte(metadata), &args) != nil || args == nil {
		return nil, false
	}
	summary, _ := arguments["summary"].(string)
	var fits, named []string
	for name, info := range b.tools {
		field := functionCodeTransportField(name, info.Kind, info.Parameters)
		if field == "" || !functionCodeMetadataFits(info.Parameters, field, args) {
			continue
		}
		fits = append(fits, name)
		if strings.Contains(summary, name) {
			named = append(named, name)
		}
	}
	var name string
	switch {
	case len(named) == 1:
		name = named[0]
	case len(fits) == 1:
		name = fits[0]
	default:
		return nil, false
	}
	marked := make(object, len(arguments))
	for key, value := range arguments {
		marked[key] = value
	}
	marked["summary"] = functionCodeTransportPrefix + name
	envelope, _, err := b.functionCodeTransportEnvelope(marked)
	return envelope, err == nil
}

// functionCodeMetadataFits reports whether metadata uses only declared fields and
// supplies every required field other than the raw-code one.
func functionCodeMetadataFits(schema object, field string, metadata object) bool {
	properties, _ := schema["properties"].(object)
	for key := range metadata {
		if _, declared := properties[key]; !declared || key == field {
			return false
		}
	}
	required, _ := schema["required"].([]any)
	for _, item := range required {
		key, _ := item.(string)
		if _, present := metadata[key]; key != field && !present {
			return false
		}
	}
	return true
}

// transportArgumentsShape reports structural facts about the outer transport
// arguments for diagnostics, never their content.
func transportArgumentsShape(arguments object) string {
	summaryKind := "missing"
	if summary, ok := arguments["summary"].(string); ok {
		switch {
		case strings.Contains(summary, "codex2api"):
			summaryKind = "marker_variant"
		case strings.TrimSpace(summary) != "":
			summaryKind = "descriptive"
		default:
			summaryKind = "empty"
		}
	}
	metadataKind := "missing"
	if metadata, ok := arguments["extended_summary"].(string); ok {
		var decoded object
		if decode([]byte(metadata), &decoded) == nil && decoded != nil {
			metadataKind = fmt.Sprintf("json_object_%d_keys", len(decoded))
		} else if strings.TrimSpace(metadata) == "" {
			metadataKind = "empty"
		} else {
			metadataKind = "text"
		}
	}
	return "summary=" + summaryKind + "; extended_summary=" + metadataKind
}

// plainFunctionFromCode reads code as the tool's JSON arguments or as a complete
// {"name","arguments"} envelope for that same tool.
func plainFunctionFromCode(name, code string) (object, bool) {
	var value object
	if decode([]byte(strings.TrimSpace(code)), &value) != nil || value == nil {
		return nil, false
	}
	if envelopeName, ok := value["name"].(string); ok && len(value) == 2 {
		if strings.TrimPrefix(envelopeName, "functions.") != name {
			return nil, false
		}
		args, ok := value["arguments"].(object)
		if !ok {
			return nil, false
		}
		value = args
	}
	return object{"name": name, "arguments": value}, true
}
