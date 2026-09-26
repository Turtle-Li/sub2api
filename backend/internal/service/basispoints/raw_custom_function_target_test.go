package basispoints

import (
	"encoding/json"
	"strings"
	"testing"
)

func rawCustomTestNative(t *testing.T, name, code string, metadata any) object {
	t.Helper()
	outer, err := json.Marshal(object{
		"summary": customTransportPrefix + name, "code": code,
		"extended_summary": metadata, "destructive": false, "references": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return object{"type": "function_call", "id": "fc_raw", "call_id": "call_raw", "name": "run_officejs", "arguments": string(outer), "status": "completed"}
}

func TestRawCustomMarkerOnFunctionCodeToolUsesCodeArgument(t *testing.T) {
	source := testSource()
	source["tools"] = []any{functionCodeTestTool("run_code")}
	_, bridge := mustPrepare(t, source, "scope", nil)
	for _, name := range []string{"run_code", "functions.run_code"} {
		call, err := bridge.translateCall(rawCustomTestNative(t, name, "echo \"hi\"", "{\"description\":\"Run checks\"}"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		args := functionCodeTestArguments(t, call)
		if call["type"] != "function_call" || call["name"] != "run_code" || args["code"] != "echo \"hi\"" || args["description"] != "Run checks" {
			t.Fatalf("unexpected call: %#v", call)
		}
	}
	// 缺少必填参数时不猜测，维持原有失败。
	if _, err := bridge.translateCall(rawCustomTestNative(t, "run_code", "echo hi", "descriptive text")); err == nil || !strings.Contains(err.Error(), "raw transport requires a declared custom tool") {
		t.Fatalf("missing required metadata must still fail, got %v", err)
	}
}

func TestRawCustomMarkerOnPlainFunctionAcceptsOnlyJSONArguments(t *testing.T) {
	source := testSource()
	source["tools"] = []any{object{"type": "function", "name": "update_notes", "parameters": object{
		"type": "object", "properties": object{"text": object{"type": "string"}},
	}}}
	_, bridge := mustPrepare(t, source, "scope", nil)
	call, err := bridge.translateCall(rawCustomTestNative(t, "update_notes", `{"text":"hello"}`, ""))
	if err != nil {
		t.Fatal(err)
	}
	if args := functionCodeTestArguments(t, call); call["name"] != "update_notes" || args["text"] != "hello" {
		t.Fatalf("unexpected call: %#v", call)
	}
	if _, err := bridge.translateCall(rawCustomTestNative(t, "update_notes", "plain text", "")); err == nil || !strings.Contains(err.Error(), "raw transport requires a declared custom tool") {
		t.Fatalf("raw text for a plain function must still fail, got %v", err)
	}
}

func TestToolOutsideCatalogReportsBoundedName(t *testing.T) {
	source := testSource()
	source["tools"] = []any{functionCodeTestTool("run_code")}
	_, bridge := mustPrepare(t, source, "scope", nil)
	envelope, _ := json.Marshal(object{"name": "shell\nextra", "arguments": object{}})
	outer, _ := json.Marshal(object{"summary": "Run", "code": string(envelope), "extended_summary": "{}", "destructive": false, "references": []any{}})
	native := object{"type": "function_call", "id": "fc_x", "call_id": "call_x", "name": "run_officejs", "arguments": string(outer), "status": "completed"}
	_, err := bridge.translateCall(native)
	if err == nil || !strings.Contains(err.Error(), "outside the client's catalog (tool=shell_extra)") {
		t.Fatalf("unexpected error: %v", err)
	}
}
