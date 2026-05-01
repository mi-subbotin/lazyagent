package gemini

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestScanAgents_FromMarkdown(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha.md"), `---
name: AlphaAgent
description: testing fixture
---
body of alpha`)
	writeFile(t, filepath.Join(dir, "no-name.md"), `body without frontmatter`)
	writeFile(t, filepath.Join(dir, "ignored.txt"), "skipped")

	got := scanAgents(dir, model.ScopeGlobal)
	if len(got) != 2 {
		t.Fatalf("got %d agents, want 2: %+v", len(got), got)
	}
	names := []string{got[0].Name, got[1].Name}
	sort.Strings(names)
	if names[0] != "AlphaAgent" || names[1] != "no-name" {
		t.Errorf("names = %v, want [AlphaAgent no-name]", names)
	}
	for _, it := range got {
		if it.Origin != model.OriginGemini || it.Kind != model.KindAgent {
			t.Errorf("item Origin/Kind = %v/%v", it.Origin, it.Kind)
		}
	}
}

func TestScanAgents_MissingDir(t *testing.T) {
	if got := scanAgents("/no/such/dir", model.ScopeGlobal); got != nil {
		t.Errorf("missing dir should yield nil, got %v", got)
	}
}

func TestScanSkills_FromSkillMD(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "echo", "SKILL.md"), `---
name: echo
description: shouts back
---
echo body`)
	writeFile(t, filepath.Join(root, "broken", "SKILL.md"), `no frontmatter here`)
	got := scanSkills(root, model.ScopeGlobal)
	if len(got) != 2 {
		t.Fatalf("got %d skills, want 2", len(got))
	}
	for _, it := range got {
		if it.Storage != model.StorageDir {
			t.Errorf("Storage = %v, want StorageDir", it.Storage)
		}
	}
}

func TestScanMCPSettings_HappyPath(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	writeFile(t, settings, `{
		"mcpServers": {
			"linear": {"command": "npx", "args": ["x"]},
			"github": {"url": "https://example.com"}
		}
	}`)
	got := scanMCPSettings(settings, model.ScopeGlobal)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	for _, it := range got {
		if it.Storage != model.StorageEntry {
			t.Errorf("Storage = %v, want StorageEntry", it.Storage)
		}
		if !strings.HasPrefix(it.ConfigKey, "mcpServers/") {
			t.Errorf("ConfigKey = %q, want mcpServers/* prefix", it.ConfigKey)
		}
	}
}

func TestScanMCPSettings_MissingFile(t *testing.T) {
	if got := scanMCPSettings("/no/such/file.json", model.ScopeGlobal); got != nil {
		t.Errorf("missing file should yield nil, got %v", got)
	}
}

func TestScanMCPSettings_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	writeFile(t, settings, `not valid json {{{`)
	if got := scanMCPSettings(settings, model.ScopeGlobal); got != nil {
		t.Errorf("invalid JSON should yield nil, got %v", got)
	}
}

func TestScanMCPSettings_NoMcpServersSection(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	writeFile(t, settings, `{"theme": "dark"}`)
	if got := scanMCPSettings(settings, model.ScopeGlobal); got != nil {
		t.Errorf("no mcpServers should yield nil, got %v", got)
	}
}

func TestScanCommands_TomlPrompts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "summary.toml"), `description = "summarize"
prompt = "summarize the input"`)
	writeFile(t, filepath.Join(dir, "nested", "deep.toml"), `prompt = "deep"`)
	writeFile(t, filepath.Join(dir, "ignored.md"), `not toml`)

	got := scanCommands(dir, model.ScopeGlobal)
	if len(got) != 2 {
		t.Fatalf("got %d commands, want 2", len(got))
	}
	for _, it := range got {
		if it.Kind != model.KindPrompt {
			t.Errorf("Kind = %v, want KindPrompt", it.Kind)
		}
	}
}

func TestReadGeminiMD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "GEMINI.md")
	writeFile(t, path, "\n# Title\n\ngemini context body\n")
	it := readGeminiMD(path, model.ScopeGlobal)
	if it == nil {
		t.Fatal("expected non-nil for present file")
	}
	if it.Kind != model.KindMemory || it.Name != "GEMINI.md" {
		t.Errorf("Kind/Name = %v/%q", it.Kind, it.Name)
	}
	if !strings.Contains(it.Description, "Title") {
		t.Errorf("Description = %q, want first non-blank line", it.Description)
	}
}

func TestReadGeminiMD_Missing(t *testing.T) {
	if it := readGeminiMD("/nope.md", model.ScopeGlobal); it != nil {
		t.Errorf("missing file should yield nil, got %v", it)
	}
}

func TestSourceList_NoGeminiDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := (Source{}).List(nil, ""); err != nil {
		t.Fatalf("List: %v", err)
	}
}

func TestMcpDescription_Branches(t *testing.T) {
	cases := []struct {
		entry any
		want  string
	}{
		{map[string]any{"command": "x"}, "x"},
		{map[string]any{"url": "u"}, "u"},
		{map[string]any{}, ""},
		{"not-a-map", ""},
	}
	for _, tc := range cases {
		if got := mcpDescription(tc.entry); got != tc.want {
			t.Errorf("mcpDescription(%v) = %q, want %q", tc.entry, got, tc.want)
		}
	}
}
