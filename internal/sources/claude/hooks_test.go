package claude

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestScanHooksFileBasic(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	mustWriteFile(t, settings, `{
		"hooks": {
			"PreToolUse": [
				{"matcher": "Bash", "hooks": [{"type": "command", "command": "echo before bash", "timeout": 5}]},
				{"matcher": "Read", "hooks": [
					{"type": "command", "command": "log read 1"},
					{"type": "command", "command": "log read 2"}
				]}
			],
			"SessionStart": [
				{"hooks": [{"type": "command", "command": "echo session"}]}
			]
		}
	}`)

	got := scanHooksFile(settings, model.ScopeGlobal)
	if len(got) != 4 {
		var names []string
		for _, it := range got {
			names = append(names, it.Name)
		}
		t.Fatalf("got %d items, want 4: %v", len(got), names)
	}

	byKey := map[string]model.Item{}
	for _, it := range got {
		if it.Kind != model.KindHook {
			t.Errorf("Kind = %v, want KindHook for %q", it.Kind, it.Name)
		}
		if it.Storage != model.StorageEntry {
			t.Errorf("Storage for %q = %v, want StorageEntry", it.Name, it.Storage)
		}
		byKey[it.ConfigKey] = it
	}

	bash, ok := byKey["hooks/PreToolUse/0/hooks/0"]
	if !ok {
		t.Fatalf("missing hooks/PreToolUse/0/hooks/0; keys: %v", keys(byKey))
	}
	if bash.Name != "PreToolUse:Bash" {
		t.Errorf("Name = %q, want PreToolUse:Bash", bash.Name)
	}
	if !strings.Contains(bash.Description, "echo before bash") {
		t.Errorf("Description = %q, want it to contain command", bash.Description)
	}
	if !strings.Contains(bash.Body, "⚠ runs shell") {
		t.Errorf("Body missing shell warning:\n%s", bash.Body)
	}
	if bash.Meta["matcher"] != "Bash" {
		t.Errorf("Meta[matcher] = %q, want Bash", bash.Meta["matcher"])
	}

	read1, ok := byKey["hooks/PreToolUse/1/hooks/0"]
	if !ok {
		t.Fatalf("missing hooks/PreToolUse/1/hooks/0")
	}
	if read1.Name != "PreToolUse:Read[0]" {
		t.Errorf("Name = %q, want PreToolUse:Read[0] (multi-hook disambiguation)", read1.Name)
	}

	session, ok := byKey["hooks/SessionStart/0/hooks/0"]
	if !ok {
		t.Fatalf("missing hooks/SessionStart/0/hooks/0")
	}
	if session.Name != "SessionStart" {
		t.Errorf("Name = %q, want SessionStart (no matcher)", session.Name)
	}
}

func TestScanHooksFileMissing(t *testing.T) {
	got := scanHooksFile("/nonexistent/path.json", model.ScopeGlobal)
	if got != nil {
		t.Errorf("missing file should return nil, got %v", got)
	}
}

func TestScanHooksFileEmpty(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	mustWriteFile(t, settings, `{}`)
	if got := scanHooksFile(settings, model.ScopeGlobal); got != nil {
		t.Errorf("empty settings should return nil, got %v", got)
	}
}

func keys(m map[string]model.Item) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
