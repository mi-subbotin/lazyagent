package main

import (
	"os"
	"path/filepath"
	"testing"
)

// detectProject must reject $HOME even when it contains ~/.claude /
// ~/.codex / ~/.gemini — those are global config dirs and treating
// $HOME as a project root double-counts every global item.
func TestDetectProjectRejectsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectProject(home); got != "" {
		t.Fatalf("detectProject(home)=%q, want empty (HOME must not be a project root)", got)
	}
}

func TestDetectProjectRejectsRoot(t *testing.T) {
	if got := detectProject(string(filepath.Separator)); got != "" {
		t.Fatalf("detectProject(/) = %q, want empty", got)
	}
}

// A real project dir (not $HOME) with a marker file is accepted.
func TestDetectProjectAcceptsRealProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := filepath.Join(home, "Projects", "myapp")
	if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectProject(proj); got != proj {
		t.Fatalf("detectProject(%s) = %q, want %s", proj, got, proj)
	}
}

// A directory without any markers returns "".
func TestDetectProjectNoMarkers(t *testing.T) {
	dir := t.TempDir()
	if got := detectProject(dir); got != "" {
		t.Fatalf("detectProject(empty) = %q, want empty", got)
	}
}
