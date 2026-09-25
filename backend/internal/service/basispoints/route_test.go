package basispoints

import "testing"

func TestNativeFallbackReason(t *testing.T) {
	cases := []struct {
		body       string
		liveSearch bool
		want       string
	}{
		{`{"tools":[{"type":"image_generation"}]}`, false, "image_generation"},
		{`{"tools":[{"type":"image_generation"}]}`, true, "image_generation"},
		{`{"tools":[{"type":"web_search","external_web_access":true}]}`, false, "web_search"},
		{`{"tools":[{"type":"web_search","external_web_access":true}]}`, true, ""},
		{`{"tools":[{"type":"web_search","external_web_access":false}]}`, false, ""},
		{`{"tool_choice":{"type":"web_search","name":"web_search"}}`, true, "tool_choice"},
		{`{"tools":[{"type":"function","name":"exec"}]}`, false, ""},
	}
	for _, tc := range cases {
		if got := NativeFallbackReason([]byte(tc.body), tc.liveSearch); got != tc.want {
			t.Errorf("reason=%q want %q for %s", got, tc.want, tc.body)
		}
	}
}

// A live search declaration kept on BPS is omitted with a warning, while Codex's
// client-executed standalone search (namespace web, tool run) stays callable.
func TestPrepareKeepsStandaloneSearchAndOmitsHostedLiveSearch(t *testing.T) {
	source := testSource()
	source["tools"] = []any{
		object{"type": "web_search", "external_web_access": true},
		object{"type": "namespace", "name": "web", "tools": []any{
			object{"type": "function", "name": "run", "parameters": object{"type": "object"}},
		}},
	}
	_, bridge := mustPrepare(t, source, "", nil)
	if _, ok := bridge.tools["web.run"]; !ok {
		t.Fatalf("standalone web.run must stay in the client catalog: %v", bridge.tools)
	}
	if !bridge.unsupportedTools["web_search"] || len(bridge.Warnings) != 1 {
		t.Fatalf("hosted live search must be omitted with a warning: %v %v", bridge.unsupportedTools, bridge.Warnings)
	}
}
