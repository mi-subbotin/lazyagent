package codex

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

func TestScanConfig_McpServersAndProfiles(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	writeFile(t, cfg, `
[mcp_servers.linear]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-linear"]

[mcp_servers.github]
url = "https://example.com/mcp"

[profiles.default]
model = "gpt-5"
instructions = "be concise"

[profiles.research]
model = "claude-opus-4-7"
`)
	got := scanConfig(cfg, model.ScopeGlobal)
	if len(got) != 4 {
		var names []string
		for _, it := range got {
			names = append(names, it.Name+"/"+it.Kind.String())
		}
		t.Fatalf("want 4 items, got %d: %v", len(got), names)
	}
	byName := map[string]model.Item{}
	for _, it := range got {
		byName[it.Kind.String()+"/"+it.Name] = it
	}

	mcp, ok := byName["MCP/linear"]
	if !ok || !strings.Contains(mcp.Description, "npx") {
		t.Errorf("MCP/linear: want description to mention npx, got %#v", mcp)
	}
	if mcp.ConfigKey != "mcp_servers/linear" {
		t.Errorf("ConfigKey = %q, want mcp_servers/linear", mcp.ConfigKey)
	}
	if mcp.Storage != model.StorageEntry {
		t.Errorf("Storage = %v, want StorageEntry", mcp.Storage)
	}

	prof, ok := byName["Agents/default"]
	if !ok || !strings.Contains(prof.Description, "gpt-5") {
		t.Errorf("Agents/default: want description to mention gpt-5, got %#v", prof)
	}
	if prof.ConfigKey != "profiles/default" {
		t.Errorf("ConfigKey = %q, want profiles/default", prof.ConfigKey)
	}
}

func TestScanConfig_MissingFile(t *testing.T) {
	if got := scanConfig("/nope/here.toml", model.ScopeGlobal); got != nil {
		t.Errorf("missing file should yield nil, got %v", got)
	}
}

func TestScanConfig_BadTOML(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	writeFile(t, cfg, `not valid TOML [[[`)
	if got := scanConfig(cfg, model.ScopeGlobal); got != nil {
		t.Errorf("invalid TOML should yield nil, got %v", got)
	}
}

func TestScanPrompts_NameFromFrontmatter(t *testing.T) {
	dir := t.TempDir()
	prompts := filepath.Join(dir, "prompts")
	writeFile(t, filepath.Join(prompts, "alpha.md"), `---
name: AlphaPrompt
description: testing fixture
---
body lines`)
	writeFile(t, filepath.Join(prompts, "no-name.md"), `body without frontmatter`)
	writeFile(t, filepath.Join(prompts, "ignored.txt"), `not a prompt`)

	got := scanPrompts(prompts, model.ScopeGlobal)
	if len(got) != 2 {
		t.Fatalf("got %d prompts, want 2", len(got))
	}
	names := []string{got[0].Name, got[1].Name}
	sort.Strings(names)
	if names[0] != "AlphaPrompt" || names[1] != "no-name" {
		t.Errorf("names = %v, want [AlphaPrompt no-name]", names)
	}
}

func TestScanPrompts_MissingDir(t *testing.T) {
	if got := scanPrompts("/no/such/dir", model.ScopeGlobal); got != nil {
		t.Errorf("missing dir should yield nil, got %v", got)
	}
}

func TestReadAgentsMD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	writeFile(t, path, "\n# Project\n\nlocal context for the agent\n")
	it := readAgentsMD(path, model.ScopeGlobal)
	if it == nil {
		t.Fatal("expected non-nil item for present file")
	}
	if it.Kind != model.KindMemory || it.Name != "AGENTS.md" {
		t.Errorf("Kind/Name = %v/%q", it.Kind, it.Name)
	}
	if !strings.Contains(it.Description, "Project") {
		t.Errorf("Description = %q, want first non-blank line", it.Description)
	}
}

func TestReadAgentsMD_Missing(t *testing.T) {
	if it := readAgentsMD("/nope.md", model.ScopeGlobal); it != nil {
		t.Errorf("missing file should yield nil, got %v", it)
	}
}

func TestScanSkills_FromSkillMD(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "skills")
	writeFile(t, filepath.Join(root, "echo", "SKILL.md"), `---
name: echo
description: shouts back
---
echo body`)
	got := scanSkills(root, model.ScopeGlobal)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Name != "echo" || got[0].Kind != model.KindSkill {
		t.Errorf("item = %#v", got[0])
	}
	if got[0].Storage != model.StorageDir {
		t.Errorf("Storage = %v, want StorageDir", got[0].Storage)
	}
}

func TestSourceList_NoCodexDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := Source{}
	if _, err := src.List(nil, ""); err != nil {
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
		{"string-not-map", ""},
	}
	for _, tc := range cases {
		if got := mcpDescription(tc.entry); got != tc.want {
			t.Errorf("mcpDescription(%v) = %q, want %q", tc.entry, got, tc.want)
		}
	}
}

func TestProfileDescription_Branches(t *testing.T) {
	if got := profileDescription(map[string]any{"model": "gpt-5"}); got != "model: gpt-5" {
		t.Errorf("got %q", got)
	}
	if got := profileDescription(map[string]any{}); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := profileDescription("not a map"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
