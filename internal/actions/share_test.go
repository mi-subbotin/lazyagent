package actions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// TestShareSkillProjectsAcrossTools sets up a fake Claude global skill,
// shares it to all three tools, and verifies:
//   - the canonical bytes ended up in the store with a manifest;
//   - the original Claude path is now a symlink back to the store;
//   - Codex and Gemini paths are symlinks to the same canonical dir.
func TestShareSkillProjectsAcrossTools(t *testing.T) {
	home := t.TempDir()
	store := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_STORE", store)

	// Stage a Claude-shaped skill: ~/.claude/skills/foo/SKILL.md.
	claudeSkillDir := filepath.Join(home, ".claude", "skills", "foo")
	if err := os.MkdirAll(claudeSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(claudeSkillDir, "SKILL.md")
	body := []byte("---\nname: foo\n---\nhello\n")
	if err := os.WriteFile(bodyPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	it := model.Item{
		Origin:  model.OriginClaude,
		Kind:    model.KindSkill,
		Scope:   model.ScopeGlobal,
		Name:    "foo",
		Path:    bodyPath,
		Storage: model.StorageDir,
	}

	targets := []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini}
	if err := Share(it, targets, false); err != nil {
		t.Fatalf("Share: %v", err)
	}

	// Canonical store layout.
	canonicalDir := filepath.Join(store, "skills", "foo")
	if data, err := os.ReadFile(filepath.Join(canonicalDir, "SKILL.md")); err != nil {
		t.Fatalf("canonical body: %v", err)
	} else if string(data) != string(body) {
		t.Fatalf("body mismatch: %q", data)
	}
	if _, err := os.Stat(filepath.Join(canonicalDir, "manifest.toml")); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	// Each projection should be a symlink to canonicalDir.
	for _, p := range []string{
		filepath.Join(home, ".claude", "skills", "foo"),
		filepath.Join(home, ".agents", "skills", "foo"),
		filepath.Join(home, ".gemini", "skills", "foo"),
	} {
		got, err := os.Readlink(p)
		if err != nil {
			t.Fatalf("readlink %s: %v", p, err)
		}
		if got != canonicalDir {
			t.Fatalf("%s -> %s, want %s", p, got, canonicalDir)
		}
	}
}

func TestShareMemoryProjectsToTools(t *testing.T) {
	home := t.TempDir()
	store := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_STORE", store)

	// Stage a Claude global memory file: ~/.claude/CLAUDE.md.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(home, ".claude", "CLAUDE.md")
	body := []byte("# Project memory\n")
	if err := os.WriteFile(bodyPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	it := model.Item{
		Origin:  model.OriginClaude,
		Kind:    model.KindMemory,
		Scope:   model.ScopeGlobal,
		Name:    "CLAUDE",
		Path:    bodyPath,
		Storage: model.StorageFile,
	}

	if err := Share(it, []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini}, false); err != nil {
		t.Fatalf("Share: %v", err)
	}

	canonicalBody := filepath.Join(store, "memory", "CLAUDE", "memory.md")
	got, err := os.ReadFile(canonicalBody)
	if err != nil {
		t.Fatalf("canonical body: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("body mismatch")
	}
	for _, p := range []string{
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".codex", "AGENTS.md"),
		filepath.Join(home, ".gemini", "GEMINI.md"),
	} {
		dest, err := os.Readlink(p)
		if err != nil {
			t.Fatalf("readlink %s: %v", p, err)
		}
		if dest != canonicalBody {
			t.Fatalf("%s -> %s, want %s", p, dest, canonicalBody)
		}
	}
}

// TestReshareDiffsAddAndRemove starts from a skill shared to all three
// tools, then reshares to {Claude, Codex} only — Gemini's projection
// must vanish, the others stay, and the manifest's shared_to list
// reflects the new set.
func TestReshareDiffsAddAndRemove(t *testing.T) {
	home := t.TempDir()
	storeDir := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_STORE", storeDir)

	claudeSkillDir := filepath.Join(home, ".claude", "skills", "foo")
	if err := os.MkdirAll(claudeSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeSkillDir, "SKILL.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := model.Item{
		Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal,
		Name: "foo", Path: filepath.Join(claudeSkillDir, "SKILL.md"), Storage: model.StorageDir,
	}
	if err := Share(src, []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini}, false); err != nil {
		t.Fatalf("initial Share: %v", err)
	}

	// Stand on the Shared-side Item — its Path is the canonical body.
	canonical := filepath.Join(storeDir, "skills", "foo")
	shared := model.Item{
		Origin: model.OriginShared, Kind: model.KindSkill, Scope: model.ScopeGlobal,
		Name: "foo", Path: filepath.Join(canonical, "SKILL.md"), Storage: model.StorageDir,
		Shared: true,
	}

	current := CurrentProjections(shared)
	if len(current) != 3 {
		t.Fatalf("CurrentProjections after share: got %v, want 3", current)
	}

	if err := Reshare(shared, []model.Origin{model.OriginClaude, model.OriginCodex}, false); err != nil {
		t.Fatalf("Reshare: %v", err)
	}

	for _, want := range []string{
		filepath.Join(home, ".claude", "skills", "foo"),
		filepath.Join(home, ".agents", "skills", "foo"),
	} {
		if _, err := os.Lstat(want); err != nil {
			t.Errorf("expected projection at %s: %v", want, err)
		}
	}
	gone := filepath.Join(home, ".gemini", "skills", "foo")
	if _, err := os.Lstat(gone); !os.IsNotExist(err) {
		t.Errorf("expected %s removed, err=%v", gone, err)
	}

	// Manifest reflects the new set.
	got := CurrentProjections(shared)
	if len(got) != 2 {
		t.Errorf("CurrentProjections after Reshare: got %v, want 2", got)
	}
}

func TestShareLocalScopeRejected(t *testing.T) {
	it := model.Item{
		Origin: model.OriginClaude,
		Kind:   model.KindSkill,
		Scope:  model.ScopeLocal,
	}
	if err := Share(it, []model.Origin{model.OriginClaude}, false); err != ErrShareLocalScope {
		t.Fatalf("want ErrShareLocalScope, got %v", err)
	}
}

// TestShareConflictAcrossTools is the scenario the user hit: a skill
// `foo` exists independently in two tools. Sharing from Gemini with
// Claude included must surface ErrShareConflicts up-front, leave the
// filesystem untouched, then succeed when retried with overwrite=true
// — replacing Claude's pre-existing copy with the canonical projection.
func TestShareConflictAcrossTools(t *testing.T) {
	home := t.TempDir()
	storeDir := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_STORE", storeDir)

	geminiDir := filepath.Join(home, ".gemini", "skills", "foo")
	if err := os.MkdirAll(geminiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	geminiBody := []byte("---\nname: foo\n---\nfrom gemini\n")
	if err := os.WriteFile(filepath.Join(geminiDir, "SKILL.md"), geminiBody, 0o644); err != nil {
		t.Fatal(err)
	}

	claudeDir := filepath.Join(home, ".claude", "skills", "foo")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeBody := []byte("---\nname: foo\n---\nold claude version\n")
	if err := os.WriteFile(filepath.Join(claudeDir, "SKILL.md"), claudeBody, 0o644); err != nil {
		t.Fatal(err)
	}

	geminiItem := model.Item{
		Origin: model.OriginGemini, Kind: model.KindSkill, Scope: model.ScopeGlobal,
		Name: "foo", Path: filepath.Join(geminiDir, "SKILL.md"), Storage: model.StorageDir,
	}
	targets := []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini}

	conflicts := ShareConflicts(geminiItem, targets)
	if len(conflicts) != 1 || conflicts[0].Target != model.OriginClaude {
		t.Fatalf("expected 1 conflict on Claude, got %+v", conflicts)
	}

	// First try without overwrite: must refuse and leave both source
	// and existing target untouched.
	if err := Share(geminiItem, targets, false); err != ErrShareConflicts {
		t.Fatalf("want ErrShareConflicts, got %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(geminiDir, "SKILL.md")); err != nil || string(data) != string(geminiBody) {
		t.Fatalf("source moved despite refusal: data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(claudeDir, "SKILL.md")); err != nil || string(data) != string(claudeBody) {
		t.Fatalf("Claude copy mutated despite refusal: data=%q err=%v", data, err)
	}

	// Now confirm overwrite — Gemini's bytes win, Claude's old copy is
	// replaced by a symlink to the canonical store entry.
	if err := Share(geminiItem, targets, true); err != nil {
		t.Fatalf("Share overwrite=true: %v", err)
	}
	canonical := filepath.Join(storeDir, "skills", "foo")
	if data, err := os.ReadFile(filepath.Join(canonical, "SKILL.md")); err != nil || string(data) != string(geminiBody) {
		t.Fatalf("canonical body wrong: data=%q err=%v", data, err)
	}
	for _, p := range []string{
		filepath.Join(home, ".claude", "skills", "foo"),
		filepath.Join(home, ".agents", "skills", "foo"),
		filepath.Join(home, ".gemini", "skills", "foo"),
	} {
		dest, err := os.Readlink(p)
		if err != nil {
			t.Fatalf("readlink %s: %v", p, err)
		}
		if dest != canonical {
			t.Fatalf("%s -> %s, want %s", p, dest, canonical)
		}
	}
}

// TestReshareConflictOnAddedTool covers reshare-side overwrite: a skill
// is shared to Claude+Codex, then the user reshares to add Gemini —
// but Gemini already has an unrelated `foo` directory. Reshare without
// overwrite refuses, with overwrite replaces Gemini's copy.
func TestReshareConflictOnAddedTool(t *testing.T) {
	home := t.TempDir()
	storeDir := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_STORE", storeDir)

	claudeDir := filepath.Join(home, ".claude", "skills", "foo")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "SKILL.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := model.Item{
		Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal,
		Name: "foo", Path: filepath.Join(claudeDir, "SKILL.md"), Storage: model.StorageDir,
	}
	if err := Share(src, []model.Origin{model.OriginClaude, model.OriginCodex}, false); err != nil {
		t.Fatalf("initial Share: %v", err)
	}

	// Plant an unrelated foo in Gemini before resharing.
	geminiDir := filepath.Join(home, ".gemini", "skills", "foo")
	if err := os.MkdirAll(geminiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(geminiDir, "SKILL.md"), []byte("stranger\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	canonical := filepath.Join(storeDir, "skills", "foo")
	shared := model.Item{
		Origin: model.OriginShared, Kind: model.KindSkill, Scope: model.ScopeGlobal,
		Name: "foo", Path: filepath.Join(canonical, "SKILL.md"), Storage: model.StorageDir,
		Shared: true,
	}
	newTargets := []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini}
	if err := Reshare(shared, newTargets, false); err != ErrShareConflicts {
		t.Fatalf("want ErrShareConflicts, got %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(geminiDir, "SKILL.md")); string(data) != "stranger\n" {
		t.Fatalf("gemini copy mutated under refusal: %q", data)
	}
	if err := Reshare(shared, newTargets, true); err != nil {
		t.Fatalf("Reshare overwrite: %v", err)
	}
	dest, err := os.Readlink(geminiDir)
	if err != nil {
		t.Fatalf("readlink gemini: %v", err)
	}
	if dest != canonical {
		t.Fatalf("gemini -> %s, want %s", dest, canonical)
	}
}
