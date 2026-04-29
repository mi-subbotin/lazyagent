package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestIsDriftedSymlinkInSync(t *testing.T) {
	home := t.TempDir()
	storeDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_STORE", storeDir)

	// Plant canonical body and a symlink projection.
	canonical := filepath.Join(storeDir, "skills", "foo")
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(home, ".claude", "skills", "foo")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canonical, link); err != nil {
		t.Fatal(err)
	}

	// Read body through the symlink so the Item carries canonical bytes.
	data, err := os.ReadFile(filepath.Join(link, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindSkill,
		Name: "foo", Path: filepath.Join(link, "SKILL.md"),
		Body: string(data), Shared: true,
	}
	if IsDrifted(it) {
		t.Fatal("symlink projection reported drifted")
	}
}

func TestIsDriftedCopyDiverged(t *testing.T) {
	home := t.TempDir()
	storeDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_STORE", storeDir)

	canonical := filepath.Join(storeDir, "skills", "foo")
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("canonical body"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fake a copy projection by leaving the projected path under the
	// store root (so ResolvesToStore is true) but feeding the Item a
	// different Body — that's the drifted state.
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindSkill,
		Name: "foo", Path: filepath.Join(canonical, "SKILL.md"),
		Body: "drifted edits", Shared: true,
	}
	if !IsDrifted(it) {
		t.Fatal("copy projection with diverged bytes not detected as drift")
	}
}

func TestIsDriftedNotShared(t *testing.T) {
	it := model.Item{Shared: false, Body: "anything"}
	if IsDrifted(it) {
		t.Fatal("non-shared item reported drifted")
	}
}
