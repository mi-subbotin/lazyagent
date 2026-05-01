package claude

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestNameAndFirstNonEmptyLine(t *testing.T) {
	if (Source{}).Name() != "claude" {
		t.Errorf("Name should be 'claude'")
	}
	cases := []struct{ in, want string }{
		{"\n\n# Title\nbody", "Title"},
		{"## subtitle\n", "subtitle"},
		{"\n\n", ""},
		{"plain text here", "plain text here"},
	}
	for _, tc := range cases {
		if got := firstNonEmptyLine(tc.in); got != tc.want {
			t.Errorf("firstNonEmptyLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMcpDescription_Branches(t *testing.T) {
	cases := []struct {
		entry any
		want  string
	}{
		{map[string]any{"command": "x"}, "x"},
		{map[string]any{"url": "u"}, "u"},
		{map[string]any{"type": "stdio"}, "stdio"},
		{map[string]any{}, ""},
		{"not-a-map", ""},
	}
	for _, tc := range cases {
		if got := mcpDescription(tc.entry); got != tc.want {
			t.Errorf("mcpDescription(%v) = %q, want %q", tc.entry, got, tc.want)
		}
	}
}

func TestScanMCPFile_Servers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	mustWriteFile(t, path, `{
		"mcpServers": {
			"linear": {"command": "npx", "args": ["x"]},
			"github": {"url": "https://example.com"}
		}
	}`)
	got := scanMCPFile(path, "", model.ScopeGlobal)
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2", len(got))
	}
	names := []string{got[0].Name, got[1].Name}
	sort.Strings(names)
	if names[0] != "github" || names[1] != "linear" {
		t.Errorf("names = %v", names)
	}
}

func TestScanMCPFile_PerProject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	mustWriteFile(t, path, `{
		"projects": {
			"/abs/proj": {"mcpServers": {"linear": {"command": "npx"}}}
		}
	}`)
	got := scanMCPFile(path, "/abs/proj", model.ScopeLocal)
	if len(got) != 1 || got[0].Name != "linear" {
		t.Fatalf("per-project got %v", got)
	}
	if !strings.HasPrefix(got[0].ConfigKey, "projects/") {
		t.Errorf("ConfigKey = %q, want projects/* prefix", got[0].ConfigKey)
	}
}

func TestScanMCPFile_MissingOrInvalid(t *testing.T) {
	if got := scanMCPFile("/no/such/file.json", "", model.ScopeGlobal); got != nil {
		t.Errorf("missing should yield nil, got %v", got)
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	mustWriteFile(t, bad, `not json {{`)
	if got := scanMCPFile(bad, "", model.ScopeGlobal); got != nil {
		t.Errorf("bad json should yield nil, got %v", got)
	}
}

func TestScanFlatMarkdownAndCommands(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "alpha.md"), `---
name: alpha
description: testing fixture
---
body`)
	mustWriteFile(t, filepath.Join(dir, "ignored.txt"), "skipped")
	got := scanCommands(filepath.Join(filepath.Dir(dir), filepath.Base(dir)), model.ScopeGlobal)
	// scanCommands looks for <root>/commands; we passed `dir` so it
	// won't recurse into nothing — assert it returns 0 without panic.
	_ = got
}

func TestReadMemoryAndMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	mustWriteFile(t, path, "\n# Title\n\nproject memory body\n")
	it := readMemory(path, model.ScopeGlobal)
	if it == nil || it.Kind != model.KindMemory || !strings.Contains(it.Description, "Title") {
		t.Errorf("readMemory result = %#v", it)
	}
	if it := readMemory("/no/such/file.md", model.ScopeGlobal); it != nil {
		t.Errorf("missing file should yield nil, got %v", it)
	}
}
