package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.HidePrivateSessions {
		t.Errorf("expected default false, got %+v", got)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := Save(State{HidePrivateSessions: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// File lands at $HOME/.lazyagent/state.json with parent dir auto-created.
	if _, err := os.Stat(filepath.Join(home, ".lazyagent", "state.json")); err != nil {
		t.Fatalf("state.json missing: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.HidePrivateSessions {
		t.Errorf("round-trip lost field, got %+v", got)
	}
}

func TestLoadCorruptReturnsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".lazyagent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Errorf("expected parse error from corrupt file")
	}
}
