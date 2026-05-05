package backup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

// TestSnapshot_FileItem_RestorableAfterDeletion captures a file-backed
// item, deletes it, and confirms Restore brings the bytes back.
func TestSnapshot_FileItem_RestorableAfterDeletion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path := filepath.Join(home, "scratch", "agent.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("agent body\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindAgent, Scope: model.ScopeGlobal,
		Name: "agent", Path: path, Storage: model.StorageFile,
	}
	id, err := Create("delete", []model.Item{it})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := Restore(id, 0); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("restored bytes = %q, want %q", got, original)
	}
}

// TestSnapshot_DirItem_RestorableAfterDeletion captures a Skill
// directory with nested assets, removes it, restores, and asserts the
// directory tree is intact.
func TestSnapshot_DirItem_RestorableAfterDeletion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	skillDir := filepath.Join(home, ".claude", "skills", "echo")
	if err := os.MkdirAll(filepath.Join(skillDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(bodyPath, []byte("# echo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "assets", "logo.txt"), []byte("logo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal,
		Name: "echo", Path: bodyPath, Storage: model.StorageDir,
	}
	id, err := Create("delete", []model.Item{it})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.RemoveAll(skillDir); err != nil {
		t.Fatal(err)
	}
	if err := RestoreAll(id); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}
	if got, err := os.ReadFile(bodyPath); err != nil || string(got) != "# echo\n" {
		t.Errorf("SKILL.md after restore: got=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(skillDir, "assets", "logo.txt")); err != nil || string(got) != "logo\n" {
		t.Errorf("logo.txt after restore: got=%q err=%v", got, err)
	}
}

// TestSnapshot_EntryItem_RestorableAfterDeletion captures a TOML
// profile entry, deletes it from the config, restores, and verifies
// the entry is back with its original key/value intact.
func TestSnapshot_EntryItem_RestorableAfterDeletion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[profiles.foo]\nmodel = \"gpt-5\"\nprompt = \"hello\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	it := model.Item{
		Origin: model.OriginCodex, Kind: model.KindAgent, Scope: model.ScopeGlobal,
		Name: "foo", Path: cfg, ConfigKey: "profiles/foo", Storage: model.StorageEntry,
	}
	id, err := Create("delete", []model.Item{it})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := parse.DeleteEntry(cfg, "profiles/foo"); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}
	if _, _, err := parse.ReadEntry(cfg, "profiles/foo"); err == nil {
		t.Fatal("entry should be gone before restore")
	}
	if err := Restore(id, 0); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	val, _, err := parse.ReadEntry(cfg, "profiles/foo")
	if err != nil {
		t.Fatalf("ReadEntry after restore: %v", err)
	}
	m, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("restored value not a map: %T", val)
	}
	if m["model"] != "gpt-5" {
		t.Errorf("restored model = %v, want gpt-5", m["model"])
	}
	if m["prompt"] != "hello" {
		t.Errorf("restored prompt = %v, want hello", m["prompt"])
	}
}

// TestList_NewestFirst writes three snapshots with explicit time
// spacing and asserts List returns them newest-first.
func TestList_NewestFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var ids []string
	for i := 0; i < 3; i++ {
		path := filepath.Join(home, "scratch.txt")
		if err := os.WriteFile(path, []byte("v"), 0o644); err != nil {
			t.Fatal(err)
		}
		it := model.Item{Kind: model.KindAgent, Name: "x", Path: path, Storage: model.StorageFile}
		id, err := Create("delete", []model.Item{it})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		ids = append(ids, id)
		// Sleep a millisecond so creation timestamps don't collide; the
		// id-based fallback would also work but Created is what List
		// sorts on first.
		time.Sleep(2 * time.Millisecond)
	}
	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List len = %d, want 3", len(got))
	}
	// Newest written last → should be first in the result.
	if got[0].ID != ids[2] {
		t.Errorf("List()[0].ID = %s, want %s (newest)", got[0].ID, ids[2])
	}
	if got[2].ID != ids[0] {
		t.Errorf("List()[2].ID = %s, want %s (oldest)", got[2].ID, ids[0])
	}
}

// TestPrune_KeepsLastN writes five snapshots, prunes to 3, and asserts
// only the three newest remain.
func TestPrune_KeepsLastN(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var ids []string
	for i := 0; i < 5; i++ {
		path := filepath.Join(home, "x.txt")
		if err := os.WriteFile(path, []byte("v"), 0o644); err != nil {
			t.Fatal(err)
		}
		it := model.Item{Kind: model.KindAgent, Name: "x", Path: path, Storage: model.StorageFile}
		id, err := Create("delete", []model.Item{it})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		time.Sleep(2 * time.Millisecond)
	}
	if err := Prune(3); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	left, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(left) != 3 {
		t.Fatalf("after prune len = %d, want 3", len(left))
	}
	want := map[string]bool{ids[2]: true, ids[3]: true, ids[4]: true}
	for _, s := range left {
		if !want[s.ID] {
			t.Errorf("unexpected snapshot retained: %s", s.ID)
		}
	}
}

// TestRestore_MissingID_ReturnsErrNotFound asserts Restore on a
// nonexistent id surfaces the ErrNotFound sentinel.
func TestRestore_MissingID_ReturnsErrNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	err := Restore("does-not-exist", 0)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Restore missing id err = %v, want ErrNotFound", err)
	}
	if err := RestoreAll("does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RestoreAll missing id err = %v, want ErrNotFound", err)
	}
}

// TestPrune_ZeroOrNegative_NoOp: keepLast <= 0 disables pruning so
// crash-recovery tooling can call Prune unconditionally without
// surprises when the user disabled retention.
func TestPrune_ZeroOrNegative_NoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for i := 0; i < 3; i++ {
		path := filepath.Join(home, "x.txt")
		if err := os.WriteFile(path, []byte("v"), 0o644); err != nil {
			t.Fatal(err)
		}
		it := model.Item{Kind: model.KindAgent, Name: "x", Path: path, Storage: model.StorageFile}
		if _, err := Create("delete", []model.Item{it}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Prune(0); err != nil {
		t.Fatal(err)
	}
	if err := Prune(-1); err != nil {
		t.Fatal(err)
	}
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("Prune(<=0) removed snapshots: have %d, want 3", len(got))
	}
}

// TestRoot_HonoursHOMEOverride sanity-checks the path-derivation rule
// the rest of the suite depends on for isolation.
func TestRoot_HonoursHOMEOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_BACKUPS", "")
	got, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".lazyagent", "backups")
	if got != want {
		t.Errorf("Root() = %s, want %s", got, want)
	}
	if !strings.HasPrefix(got, home) {
		t.Errorf("Root not under $HOME: %s", got)
	}
}
