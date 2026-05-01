package index

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDiscoverFindsMarkers(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "alpha", ".claude"))
	mustWrite(t, filepath.Join(root, "alpha", ".claude", "settings.json"), "{}")
	mustWrite(t, filepath.Join(root, "beta", "AGENTS.md"), "# beta")
	mustMkdirAll(t, filepath.Join(root, "beta", ".gemini"))
	// Decoy: monorepo with a vendored project — should be pruned by
	// the parent project marker (alpha/.claude).
	mustMkdirAll(t, filepath.Join(root, "alpha", "node_modules", "foo", ".claude"))
	// Decoy: skip-listed dir at top level should be ignored entirely.
	mustMkdirAll(t, filepath.Join(root, "node_modules", "bar", ".claude"))

	got, err := Discover(Options{Roots: []string{root}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	gotPaths := make([]string, 0, len(got))
	for _, p := range got {
		gotPaths = append(gotPaths, filepath.Base(p.Path))
	}
	want := []string{"alpha", "beta"}
	if !reflect.DeepEqual(gotPaths, want) {
		t.Errorf("project bases = %v, want %v", gotPaths, want)
	}
}

func TestDiscoverMarkersSorted(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "p")
	mustMkdirAll(t, filepath.Join(dir, ".claude"))
	mustMkdirAll(t, filepath.Join(dir, ".codex"))
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), "x")
	got, err := Discover(Options{Roots: []string{root}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d projects, want 1: %+v", len(got), got)
	}
	want := []string{".claude", ".codex", "AGENTS.md"}
	if !reflect.DeepEqual(got[0].Markers, want) {
		t.Errorf("markers = %v, want %v", got[0].Markers, want)
	}
}

func TestDiscoverDepthBound(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c", "d", "e")
	mustMkdirAll(t, filepath.Join(deep, ".claude"))
	got, err := Discover(Options{Roots: []string{root}, MaxDepth: 2})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected depth bound to hide deep project, got %+v", got)
	}
}

func TestCacheRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := Cache{
		GeneratedAt: time.Now().Unix(),
		Roots:       []string{"/tmp"},
		Projects:    []Project{{Path: "/tmp/foo", Markers: []string{".claude"}}},
	}
	if _, err := SaveCache(c); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	got, err := LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if got.GeneratedAt != c.GeneratedAt || len(got.Projects) != 1 || got.Projects[0].Path != "/tmp/foo" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestIsFresh(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	if IsFresh(Cache{}, now, time.Hour) {
		t.Error("empty cache must not be fresh")
	}
	if !IsFresh(Cache{GeneratedAt: now.Add(-30 * time.Minute).Unix()}, now, time.Hour) {
		t.Error("30m-old cache should be fresh under 1h TTL")
	}
	if IsFresh(Cache{GeneratedAt: now.Add(-2 * time.Hour).Unix()}, now, time.Hour) {
		t.Error("2h-old cache should not be fresh under 1h TTL")
	}
}

func mustMkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", p, err)
	}
}
