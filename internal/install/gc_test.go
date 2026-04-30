package install

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutoGC_RemovesStaleAndKeepsLive(t *testing.T) {
	cache := t.TempDir()
	mustMkdir := func(p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustMkdir(filepath.Join(cache, "github.com", "foo", "bar@live123"))
	mustMkdir(filepath.Join(cache, "github.com", "foo", "bar@stale456"))
	mustMkdir(filepath.Join(cache, "github.com", "foo", "ignoredir")) // no @
	if err := os.WriteFile(filepath.Join(cache, "github.com", "foo", "bar@live123", "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(t.TempDir(), "installed.toml")
	m := &Manifest{}
	m.Add(Install{Name: "x", SHA: "live123", TargetPath: "/x"})
	if err := m.Save(manifestPath); err != nil {
		t.Fatal(err)
	}

	removed, err := AutoGC(cache, manifestPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(filepath.Join(cache, "github.com", "foo", "bar@live123")); err != nil {
		t.Errorf("live cache dir was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "github.com", "foo", "bar@stale456")); !os.IsNotExist(err) {
		t.Errorf("stale cache dir survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "github.com", "foo", "ignoredir")); err != nil {
		t.Errorf("non-@ dir was clobbered: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, ".last-gc")); err != nil {
		t.Errorf(".last-gc marker not written: %v", err)
	}
}

func TestAutoGC_RespectsInterval(t *testing.T) {
	cache := t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "installed.toml")

	// Pre-populate marker as fresh (< 1h ago).
	if err := os.WriteFile(filepath.Join(cache, ".last-gc"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cache, "github.com", "foo", "bar@stale"), 0o755); err != nil {
		t.Fatal(err)
	}

	removed, err := AutoGC(cache, manifestPath, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (interval not elapsed)", removed)
	}
	if _, err := os.Stat(filepath.Join(cache, "github.com", "foo", "bar@stale")); err != nil {
		t.Errorf("stale dir was swept despite fresh marker: %v", err)
	}
}

func TestAutoGC_MissingCacheDir(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "does-not-exist")
	manifestPath := filepath.Join(t.TempDir(), "installed.toml")
	removed, err := AutoGC(cache, manifestPath, 0)
	if err != nil {
		t.Fatalf("AutoGC on missing dir: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}
