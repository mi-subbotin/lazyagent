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

func TestMtimesUnchanged_DetectsModification(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "alpha")
	mustMkdirAll(t, filepath.Join(proj, ".claude"))
	mustWrite(t, filepath.Join(proj, "CLAUDE.md"), "# alpha")

	got, err := Discover(Options{Roots: []string{dir}, DisableFd: true})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d projects, want 1", len(got))
	}
	if len(got[0].MarkerMtimes) == 0 {
		t.Fatal("MarkerMtimes should be populated after Discover")
	}

	c := Cache{Projects: got}
	if !MtimesUnchanged(c) {
		t.Errorf("freshly-discovered cache should report unchanged mtimes")
	}

	// Touch the marker file → mtime moves forward → cache must report stale.
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(filepath.Join(proj, "CLAUDE.md"), future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if MtimesUnchanged(c) {
		t.Errorf("after touching a marker, MtimesUnchanged should return false")
	}
}

func TestMtimesUnchanged_EmptyCache(t *testing.T) {
	if MtimesUnchanged(Cache{}) {
		t.Error("empty cache should never report unchanged mtimes")
	}
}

func TestMtimesUnchanged_LegacyCacheStale(t *testing.T) {
	// A cache produced before PRI-56 has Projects but no MarkerMtimes;
	// must report stale so the next launch re-walks and populates the
	// new field.
	c := Cache{Projects: []Project{{Path: "/tmp/foo", Markers: []string{".claude"}}}}
	if MtimesUnchanged(c) {
		t.Error("pre-PRI-56 cache without MarkerMtimes must be treated as stale")
	}
}

func TestDiscoverFdPath_WhenAvailable(t *testing.T) {
	if fdBinary() == "" {
		t.Skip("fd / fdfind not installed; skipping fd path test")
	}
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "alpha", ".claude"))
	mustWrite(t, filepath.Join(root, "alpha", ".claude", "settings.json"), "{}")
	mustWrite(t, filepath.Join(root, "beta", "AGENTS.md"), "# beta")
	mustMkdirAll(t, filepath.Join(root, "beta", ".gemini"))
	// Vendored decoy: fd's --exclude node_modules should drop this.
	mustMkdirAll(t, filepath.Join(root, "alpha", "node_modules", "foo", ".claude"))

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
		t.Errorf("project bases via fd = %v, want %v", gotPaths, want)
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
