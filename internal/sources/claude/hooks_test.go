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

// PRI-61: validateHookEntry surfaces malformed entries via Item.ParseError
// so the TUI shows them as `(invalid)`. Three classes of problem are
// flagged: missing/empty command, bad timeout, missing type.
func TestValidateHookEntry(t *testing.T) {
	cases := []struct {
		name     string
		inner    map[string]any
		wantSubs []string
	}{
		{
			name:     "valid",
			inner:    map[string]any{"type": "command", "command": "echo hi", "timeout": float64(5)},
			wantSubs: nil,
		},
		{
			name:     "missing command",
			inner:    map[string]any{"type": "command"},
			wantSubs: []string{"missing or empty command"},
		},
		{
			name:     "empty command",
			inner:    map[string]any{"type": "command", "command": "   "},
			wantSubs: []string{"missing or empty command"},
		},
		{
			name:     "negative timeout",
			inner:    map[string]any{"type": "command", "command": "x", "timeout": float64(-1)},
			wantSubs: []string{"timeout must be > 0"},
		},
		{
			name:     "string timeout",
			inner:    map[string]any{"type": "command", "command": "x", "timeout": "5s"},
			wantSubs: []string{"timeout must be a number"},
		},
		{
			name:     "missing type",
			inner:    map[string]any{"command": "x"},
			wantSubs: []string{"missing type"},
		},
		{
			name:     "stacked",
			inner:    map[string]any{},
			wantSubs: []string{"missing or empty command", "missing type"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateHookEntry(tc.inner)
			if len(tc.wantSubs) == 0 {
				if got != "" {
					t.Errorf("want clean, got %q", got)
				}
				return
			}
			for _, sub := range tc.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("got %q, missing substring %q", got, sub)
				}
			}
		})
	}
}

// PRI-61: scanned items carry the validator's verdict on
// Item.ParseError. A settings.json with one valid + one missing-command
// hook produces two items with the right ParseError shape.
func TestScanHooksFile_PopulatesParseError(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	mustWriteFile(t, settings, `{
		"hooks": {
			"PreToolUse": [
				{"matcher": "Bash", "hooks": [
					{"type": "command", "command": "echo ok"},
					{"type": "command"}
				]}
			]
		}
	}`)
	got := scanHooksFile(settings, model.ScopeGlobal)
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2", len(got))
	}
	var clean, broken *model.Item
	for i := range got {
		if got[i].Meta["command"] == "echo ok" {
			clean = &got[i]
		} else {
			broken = &got[i]
		}
	}
	if clean == nil || broken == nil {
		t.Fatalf("missing clean/broken: %+v", got)
	}
	if clean.ParseError != "" {
		t.Errorf("valid hook should have empty ParseError, got %q", clean.ParseError)
	}
	if !strings.Contains(broken.ParseError, "command") {
		t.Errorf("broken hook should flag missing command, got %q", broken.ParseError)
	}
}
