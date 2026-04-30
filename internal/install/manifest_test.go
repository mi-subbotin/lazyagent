package install

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifest_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installed.toml")

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if len(m.Installs) != 0 {
		t.Errorf("missing-file Load returned %d installs, want 0", len(m.Installs))
	}

	m.Add(Install{
		Name:         "cool",
		Kind:         "skill",
		OriginURL:    "github.com/foo/bar/tree/main/skills/cool",
		SHA:          "abc123",
		TargetOrigin: "claude",
		TargetScope:  "global",
		TargetPath:   "/home/foo/.claude/skills/cool/SKILL.md",
		SourceRel:    "skills/cool/SKILL.md",
	})
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Installs) != 1 {
		t.Fatalf("got %d installs, want 1", len(got.Installs))
	}
	if got.Installs[0].Name != "cool" || got.Installs[0].SHA != "abc123" {
		t.Errorf("round-trip mismatch: %+v", got.Installs[0])
	}
	if got.Installs[0].InstalledAt.IsZero() {
		t.Errorf("InstalledAt should be set by Add")
	}
	if time.Since(got.Installs[0].InstalledAt) > time.Hour {
		t.Errorf("InstalledAt looks wrong: %v", got.Installs[0].InstalledAt)
	}
}

func TestManifest_AddReplacesByTargetPath(t *testing.T) {
	m := &Manifest{}
	m.Add(Install{Name: "x", SHA: "old", TargetPath: "/p"})
	m.Add(Install{Name: "x", SHA: "new", TargetPath: "/p"})
	if len(m.Installs) != 1 {
		t.Fatalf("got %d, want 1 (re-install must replace)", len(m.Installs))
	}
	if m.Installs[0].SHA != "new" {
		t.Errorf("SHA = %q, want \"new\"", m.Installs[0].SHA)
	}
}

func TestManifest_RemoveAndShas(t *testing.T) {
	m := &Manifest{}
	m.Add(Install{Name: "a", SHA: "111", TargetPath: "/a"})
	m.Add(Install{Name: "b", SHA: "222", TargetPath: "/b"})
	m.Add(Install{Name: "c", SHA: "111", TargetPath: "/c"}) // duplicate sha

	if !m.Remove("/a") {
		t.Fatal("Remove /a returned false")
	}
	if m.Remove("/missing") {
		t.Error("Remove /missing returned true")
	}

	shas := m.Shas()
	if len(shas) != 2 {
		t.Errorf("Shas() = %v, want 2 distinct entries", shas)
	}
	if _, ok := shas["111"]; !ok {
		t.Error("expected sha 111 to remain (kept by /c)")
	}
}

func TestManifest_Save_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "installed.toml")
	m := &Manifest{}
	m.Add(Install{Name: "x", TargetPath: "/x"})
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	// MkdirAll happened: parent must exist.
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("parent dir not created: %v", err)
	}
	// No leftover .installed.toml.* temp files.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if e.Name() != "installed.toml" {
			t.Errorf("stray file in dir: %s", e.Name())
		}
	}
}
