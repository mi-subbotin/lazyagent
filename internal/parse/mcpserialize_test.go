package parse

import (
	"strings"
	"testing"
)

func TestMCPToJSON(t *testing.T) {
	entry := map[string]any{
		"command": "npx",
		"args":    []any{"-y", "@example/server"},
	}
	got := MCPToJSON(entry)
	if !strings.Contains(got, `"command": "npx"`) {
		t.Errorf("MCPToJSON missing command:\n%s", got)
	}
	// Bad input falls through to error fallback (not a panic).
	bad := MCPToJSON(make(chan int))
	if !strings.Contains(bad, "json error") {
		t.Errorf("expected json error fallback, got %q", bad)
	}
}

func TestMCPToTOML_Shapes(t *testing.T) {
	entry := map[string]any{
		"command": "npx",
		"args":    []any{"-y", "@example/server"},
		"port":    float64(8080),
		"port_f":  float64(8080.5),
		"enabled": true,
		"env": map[string]any{
			"KEY":  "secret",
			"ROLE": "primary",
		},
	}
	got := MCPToTOML(entry)
	for _, want := range []string{
		`command = "npx"`,
		`args = ["-y", "@example/server"]`,
		`port = 8080`,
		`port_f = 8080.5`,
		`enabled = true`,
		"[env]",
		`KEY = "secret"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("MCPToTOML missing %q in:\n%s", want, got)
		}
	}
}

func TestMCPToTOML_FallbackForNonMap(t *testing.T) {
	got := MCPToTOML("just a string")
	if !strings.Contains(got, "unsupported shape") {
		t.Errorf("expected fallback message, got %q", got)
	}
}

func TestTomlArrayMixedTypes(t *testing.T) {
	got := tomlArray([]any{"a", float64(1), true})
	if got != `["a", 1, true]` {
		t.Errorf("tomlArray = %q", got)
	}
}

func TestJsonInline_BadValue(t *testing.T) {
	if got := jsonInline(make(chan int)); got != "null" {
		t.Errorf("jsonInline on unmarshalable = %q, want null", got)
	}
}
