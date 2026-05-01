package model

import "testing"

func TestOriginRoundTrip(t *testing.T) {
	cases := []struct {
		o    Origin
		want string
	}{
		{OriginClaude, "Claude"},
		{OriginCodex, "Codex"},
		{OriginGemini, "Gemini"},
		{OriginShared, "Shared"},
		{Origin(99), "?"},
	}
	for _, tc := range cases {
		if got := tc.o.String(); got != tc.want {
			t.Errorf("Origin(%d).String() = %q, want %q", tc.o, got, tc.want)
		}
	}
	for _, name := range []string{"Claude", "Codex", "Gemini", "Shared"} {
		o, ok := ParseOrigin(name)
		if !ok {
			t.Errorf("ParseOrigin(%q) returned ok=false", name)
		}
		if o.String() != name {
			t.Errorf("round-trip %q → %q", name, o.String())
		}
	}
	if _, ok := ParseOrigin("Mistral"); ok {
		t.Error("ParseOrigin should reject unknown labels")
	}
}

func TestKindRoundTrip(t *testing.T) {
	cases := []struct {
		k    Kind
		want string
	}{
		{KindSkill, "Skills"},
		{KindAgent, "Agents"},
		{KindMCP, "MCP"},
		{KindPrompt, "Prompts"},
		{KindMemory, "Memory"},
		{KindSession, "Sessions"},
		{KindHook, "Hooks"},
		{Kind(99), "?"},
	}
	for _, tc := range cases {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tc.k, got, tc.want)
		}
	}
	for _, name := range []string{"Skills", "Agents", "MCP", "Prompts", "Memory", "Sessions", "Hooks"} {
		k, ok := ParseKind(name)
		if !ok {
			t.Errorf("ParseKind(%q) returned ok=false", name)
		}
		if k.String() != name {
			t.Errorf("round-trip %q → %q", name, k.String())
		}
	}
	if _, ok := ParseKind("Whatever"); ok {
		t.Error("ParseKind should reject unknown labels")
	}
}

func TestScopeRoundTrip(t *testing.T) {
	if ScopeGlobal.String() != "Global" || ScopeLocal.String() != "Local" {
		t.Error("Scope String mismatch")
	}
	if Scope(99).String() != "?" {
		t.Error("invalid Scope should stringify as ?")
	}
	if g, ok := ParseScope("Global"); !ok || g != ScopeGlobal {
		t.Errorf("ParseScope(Global) = %v, %v", g, ok)
	}
	if l, ok := ParseScope("Local"); !ok || l != ScopeLocal {
		t.Errorf("ParseScope(Local) = %v, %v", l, ok)
	}
	if _, ok := ParseScope("Bogus"); ok {
		t.Error("ParseScope should reject unknown labels")
	}
}
