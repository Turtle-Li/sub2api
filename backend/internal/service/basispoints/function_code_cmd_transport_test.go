package basispoints

import (
	"encoding/json"
	"strings"
	"testing"
)

// Codex exec_command declares its shell command as cmd rather than code.
func execCommandTestTool() object {
	return object{"type": "function", "name": "exec_command", "parameters": object{
		"type": "object", "required": []any{"cmd"},
		"properties": object{"cmd": object{"type": "string"}, "workdir": object{"type": "string"}, "yield_time_ms": object{"type": "number"}},
	}}
}

func TestFunctionCodeTransportCarriesExecCommandCmd(t *testing.T) {
	source := testSource()
	source["tools"] = []any{execCommandTestTool()}
	body, bridge := mustPrepare(t, source, "scope", new(ReplayCache))
	items := mustTestValue[[]any](t, body["input"])
	protocol := text(mustTestValue[object](t, mustTestValue[[]any](t, mustTestValue[object](t, items[1])["content"])[0])["text"])
	for _, want := range []string{functionCodeTransportPrefix + "exec_command", "Put the exact cmd argument", "Do not include cmd"} {
		if !strings.Contains(protocol, want) {
			t.Fatalf("missing transport contract: %s", want)
		}
	}

	const cmd = "rg -n \"func \\\"quoted\\\"\" -g '*.go' | Write-Output \"done\"\r\n"
	call, err := bridge.translateCall(functionCodeTestNative(t, "exec_command", cmd, "{\"workdir\":\"/repo\",\"yield_time_ms\":1000}"))
	if err != nil {
		t.Fatal(err)
	}
	args := functionCodeTestArguments(t, call)
	if call["name"] != "exec_command" || args["cmd"] != cmd || args["workdir"] != "/repo" || args["code"] != nil {
		t.Fatalf("cmd transport changed arguments: %v", args)
	}

	if _, err := bridge.translateCall(functionCodeTestNative(t, "exec_command", cmd, "{\"cmd\":\"other\"}")); err == nil {
		t.Fatal("duplicated cmd in extended_summary must be rejected")
	}

	// Cache-miss history replays exec_command with the raw cmd transport.
	source["input"] = []any{message("user", "continue"), call, object{"type": "function_call_output", "call_id": "call_code", "output": "ok"}}
	next, replayBridge := mustPrepare(t, source, "scope", new(ReplayCache))
	input := mustTestValue[[]any](t, next["input"])
	restored := mustTestValue[object](t, input[len(input)-2])
	outer := functionCodeTestArguments(t, restored)
	if outer["summary"] != functionCodeTransportPrefix+"exec_command" || outer["code"] != cmd || strings.Contains(text(outer["extended_summary"]), "cmd") {
		t.Fatalf("history did not use raw cmd transport: %v", outer)
	}
	if roundTrip, err := replayBridge.translateCall(restored); err != nil || functionCodeTestArguments(t, roundTrip)["cmd"] != cmd {
		t.Fatal("history round trip changed cmd")
	}
}

func TestFunctionCodeTransportFieldPriority(t *testing.T) {
	both := object{"type": "object", "properties": object{"cmd": object{"type": "string"}, "code": object{"type": "string"}}}
	if field := functionCodeTransportField("tool", "function", both); field != "code" {
		t.Fatalf("code must win over cmd, got %q", field)
	}
	arrayCmd := object{"type": "object", "properties": object{"cmd": object{"type": "array"}}}
	if functionCodeTransportField("shell", "function", arrayCmd) != "" || functionCodeTransportField("x", "custom", both) != "" {
		t.Fatal("non-string cmd and custom tools keep their existing transports")
	}
}

func TestTransportFailureNamesOnlyCatalogTools(t *testing.T) {
	source := testSource()
	source["tools"] = []any{object{"type": "function", "name": "update_plan", "parameters": object{"type": "object", "properties": object{"plan": object{"type": "array"}}}}}
	_, bridge := mustPrepare(t, source, "scope", nil)
	native := func(code string) object {
		outer, _ := json.Marshal(object{"summary": "Plan", "code": code, "extended_summary": "", "destructive": false, "references": []any{}})
		return object{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "run_officejs", "arguments": string(outer), "status": "completed"}
	}
	_, err := bridge.translateCall(native(`{"name":"update_plan","arguments":{"plan":[{"step":"a"}`))
	if err == nil || !strings.Contains(err.Error(), "json_failure=unexpected_eof") || !strings.HasSuffix(err.Error(), "; tool=update_plan") {
		t.Fatalf("expected catalog tool hint, got %v", err)
	}
	_, err = bridge.translateCall(native(`{"name":"secret_value","arguments":{`))
	if err == nil || strings.Contains(err.Error(), "secret_value") {
		t.Fatalf("unknown names must not be echoed, got %v", err)
	}
}
