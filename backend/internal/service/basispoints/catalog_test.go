package basispoints

import (
	"strings"
	"testing"
)

func TestCatalogKeepsNamespacedToolContractsInProse(t *testing.T) {
	source := testSource()
	source["tools"] = []any{object{"type": "namespace", "name": "functions", "tools": []any{
		object{"type": "function", "name": "shell", "description": "Inspect repository files.", "parameters": object{
			"type": "object", "required": []any{"cmd"}, "additionalProperties": false,
			"properties": object{"cmd": object{"type": "string", "description": "Command text."}, "mode": object{"type": "string", "enum": []any{"read", "check"}}},
		}},
		object{"type": "custom", "name": "patch", "format": object{"type": "grammar", "definition": "start: PATCH"}},
	}}}
	wire, _ := mustPrepare(t, source, "test", nil)
	items := mustTestValue[[]any](t, wire["input"])
	message := mustTestValue[object](t, items[1])
	content := mustTestValue[[]any](t, message["content"])
	protocol := text(mustTestValue[object](t, content[0])["text"])
	for _, want := range []string{`Client tool "functions.shell"`, `Field "cmd" (required)`, "Command text.", `"enum":["read","check"]`, `"additionalProperties":false`, "start: PATCH", "exact raw text", "codex2api.custom/functions.patch"} {
		if !strings.Contains(protocol, want) {
			t.Fatalf("catalog lost contract detail %q", want)
		}
	}
	if strings.Contains(protocol, `"properties"`) || strings.Contains(protocol, `"type":"function"`) {
		t.Fatal("tool schema was sent as a native-style JSON catalog")
	}
}

func TestCatalogPreservesComplexSchemaConstraints(t *testing.T) {
	schema := object{"type": "array", "items": object{"type": "object", "properties": object{"entry": object{"$ref": "#/$defs/entry"}}}, "minItems": 1, "$defs": object{"entry": object{"type": "string", "pattern": "^allowed$"}}}
	got := describeSchema(schema, 0)
	for _, want := range []string{"Each array item", `Field "entry" (optional)`, `"$ref":"#/$defs/entry"`, `"minItems":1`, `"pattern":"^allowed$"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("schema constraint lost: %s", want)
		}
	}
}

// 客户端未声明 update_plan 时协议提示不能引导模型调用它，否则会被目录校验拒绝。
func TestCatalogMentionsUpdatePlanOnlyWhenDeclared(t *testing.T) {
	shell := object{"type": "function", "name": "shell", "parameters": object{"type": "object", "properties": object{"cmd": object{"type": "string"}}}}
	plan := object{"type": "function", "name": "update_plan", "parameters": object{"type": "object", "properties": object{"plan": object{"type": "array"}}}}
	for _, declared := range []bool{false, true} {
		source := testSource()
		tools := []any{shell}
		if declared {
			tools = append(tools, plan)
		}
		source["tools"] = tools
		wire, _ := mustPrepare(t, source, "test", nil)
		items := mustTestValue[[]any](t, wire["input"])
		content := mustTestValue[[]any](t, mustTestValue[object](t, items[1])["content"])
		protocol := text(mustTestValue[object](t, content[0])["text"])
		if !strings.Contains(protocol, "Client tool catalog") {
			t.Fatal("protocol message not found")
		}
		if got := strings.Contains(protocol, "including update_plan"); got != declared {
			t.Fatalf("declared=%v but prompt mentions update_plan=%v", declared, got)
		}
	}
}
