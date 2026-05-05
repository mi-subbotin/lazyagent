package guardrails

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempHome stages a temp dir as $HOME for the duration of the test
// so the global CLAUDE.md path lives under it.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestMemoryBloat_NoFiles_Allow(t *testing.T) {
	withTempHome(t)
	r := MemoryBloat{MaxBytes: 1024}.Evaluate(EvalContext{})
	if r.Action != ActionAllow {
		t.Fatalf("expected allow with no files, got %v (msg=%q)", r.Action, r.Message)
	}
}

func TestMemoryBloat_SmallFile_Allow(t *testing.T) {
	home := withTempHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("tiny\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := MemoryBloat{MaxBytes: 1024}.Evaluate(EvalContext{})
	if r.Action != ActionAllow {
		t.Fatalf("expected allow on small file, got %v (msg=%q)", r.Action, r.Message)
	}
}

func TestMemoryBloat_LargeGlobal_Warn(t *testing.T) {
	home := withTempHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("x"), 4096)
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	r := MemoryBloat{MaxBytes: 1024}.Evaluate(EvalContext{})
	if r.Action != ActionWarn {
		t.Fatalf("expected warn on large file, got %v", r.Action)
	}
	if !strings.Contains(r.Message, "CLAUDE.md") {
		t.Errorf("message missing filename: %q", r.Message)
	}
}

func TestMemoryBloat_LargeProject_Warn(t *testing.T) {
	withTempHome(t)
	proj := t.TempDir()
	big := bytes.Repeat([]byte("x"), 4096)
	if err := os.WriteFile(filepath.Join(proj, "CLAUDE.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	r := MemoryBloat{MaxBytes: 1024}.Evaluate(EvalContext{ProjectDir: proj})
	if r.Action != ActionWarn {
		t.Fatalf("expected warn on project file, got %v", r.Action)
	}
}

func TestMemoryBloat_DefaultLimit(t *testing.T) {
	home := withTempHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 8193 bytes — just over the 8192 default.
	big := bytes.Repeat([]byte("x"), 8193)
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	r := MemoryBloat{}.Evaluate(EvalContext{})
	if r.Action != ActionWarn {
		t.Fatalf("expected warn at default threshold, got %v", r.Action)
	}
}
