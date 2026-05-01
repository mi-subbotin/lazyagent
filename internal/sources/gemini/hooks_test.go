package gemini

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestScanHooksFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeFile(t, path, `{
		"hooks": {
			"PreToolUse": [
				{"matcher": "Bash", "hooks": [
					{"type": "command", "command": "echo before", "timeout": 5}
				]}
			],
			"SessionStart": [
				{"hooks": [{"command": "ls"}]}
			]
		}
	}`)
	got := scanHooksFile(path, model.ScopeGlobal)
	if len(got) != 2 {
		t.Fatalf("got %d hook items, want 2", len(got))
	}

	var seen map[string]bool = map[string]bool{}
	for _, it := range got {
		if it.Origin != model.OriginGemini {
			t.Errorf("origin = %v, want gemini", it.Origin)
		}
		if it.Kind != model.KindHook {
			t.Errorf("kind = %v, want KindHook", it.Kind)
		}
		if it.Storage != model.StorageEntry {
			t.Errorf("storage = %v, want StorageEntry", it.Storage)
		}
		if it.Path != path {
			t.Errorf("path = %q, want %q", it.Path, path)
		}
		seen[it.Name] = true
	}
	if !seen["PreToolUse:Bash"] {
		t.Errorf("missing PreToolUse:Bash entry; names: %v", seen)
	}
	if !seen["SessionStart"] {
		t.Errorf("missing SessionStart entry; names: %v", seen)
	}
}

func TestScanHooksFile_MissingFile(t *testing.T) {
	if got := scanHooksFile("/no/such/settings.json", model.ScopeGlobal); got != nil {
		t.Errorf("missing file should yield nil, got %v", got)
	}
}

func TestScanHooksFile_NoHooksSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeFile(t, path, `{"mcpServers": {"x": {"command": "y"}}}`)
	if got := scanHooksFile(path, model.ScopeGlobal); got != nil {
		t.Errorf("settings without hooks block should yield nil, got %v", got)
	}
}

func TestScanHooksFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeFile(t, path, `not-json {{`)
	if got := scanHooksFile(path, model.ScopeGlobal); got != nil {
		t.Errorf("invalid json should yield nil, got %v", got)
	}
}

func TestHookDescription_Truncates(t *testing.T) {
	short := "echo hello"
	if got := hookDescription(short); got != short {
		t.Errorf("short cmd = %q, want %q", got, short)
	}
	long := strings.Repeat("a", 200)
	got := hookDescription(long)
	// 80 chars + 3-byte UTF-8 ellipsis "…"
	if len(got) != 83 {
		t.Errorf("long cmd not truncated to 80+ellipsis: len=%d", len(got))
	}
}

func TestHookBody_RendersWarningAndCommand(t *testing.T) {
	body := hookBody("PreToolUse", "Bash", map[string]any{
		"type":    "command",
		"command": "echo X",
		"timeout": float64(5),
	})
	for _, want := range []string{"⚠ runs shell", "PreToolUse", "Bash", "echo X", "```sh"} {
		if !contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
