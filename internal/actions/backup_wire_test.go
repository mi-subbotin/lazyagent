package actions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/backup"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

// TestDelete_CreatesBackup verifies the Delete wire-in: a Skill is
// snapshotted before its on-disk directory is removed, and the
// snapshot bytes match the original SKILL.md content.
func TestDelete_CreatesBackup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	bodyPath := stageClaudeSkill(t, home, "echo", "hi from delete\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)
	if err := Delete(it); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(bodyPath)); !os.IsNotExist(err) {
		t.Fatalf("skill dir should be gone after Delete: err=%v", err)
	}
	snaps, err := backup.List()
	if err != nil {
		t.Fatalf("backup.List: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snaps))
	}
	s := snaps[0]
	if s.Op != "delete" {
		t.Errorf("Op = %q, want delete", s.Op)
	}
	if len(s.Items) != 1 {
		t.Fatalf("snapshot items = %d, want 1", len(s.Items))
	}
	if s.Items[0].Mode != "dir" {
		t.Errorf("Mode = %q, want dir", s.Items[0].Mode)
	}
	// Restore the snapshot and confirm bytes survive the round-trip.
	if err := backup.RestoreAll(s.ID); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}
	got, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(got) != "hi from delete\n" {
		t.Errorf("restored body = %q, want hi from delete\\n", got)
	}
}

// TestPlace_OverwriteCreatesBackup wires Place's overwrite branch to
// the snapshotter: a conflicting projection at the target path must be
// captured before it's removed and replaced with a fresh symlink.
func TestPlace_OverwriteCreatesBackup(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	// Source skill in Claude global.
	bodyPath := stageClaudeSkill(t, home, "echo", "canonical\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	// Stage an unrelated directory at the Codex global target so Place
	// flags it as a conflict on the Claude → Codex projection step.
	codexDir := filepath.Join(home, ".agents", "skills", "echo")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	driftedBody := "displaced bytes\n"
	if err := os.WriteFile(filepath.Join(codexDir, "SKILL.md"), []byte(driftedBody), 0o644); err != nil {
		t.Fatal(err)
	}

	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginCodex, model.ScopeGlobal},
	}
	if err := Place(it, targets, PlaceOpts{Overwrite: true}); err != nil {
		t.Fatalf("Place overwrite: %v", err)
	}

	snaps, err := backup.List()
	if err != nil {
		t.Fatalf("backup.List: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snaps))
	}
	if snaps[0].Op != "place-overwrite" {
		t.Errorf("Op = %q, want place-overwrite", snaps[0].Op)
	}
	if len(snaps[0].Items) == 0 {
		t.Fatal("snapshot has no items")
	}
}

// TestResyncCanonicalWins_CreatesBackup drifts a Codex projection then
// runs Resync canonical-wins. The drifted bytes must be captured into
// a "resync-canonical" snapshot before the link is replaced.
func TestResyncCanonicalWins_CreatesBackup(t *testing.T) {
	home := t.TempDir()
	storeDir := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_STORE", storeDir)

	canonical := filepath.Join(storeDir, "skills", "foo")
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalBody := []byte("canonical body\n")
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), canonicalBody, 0o644); err != nil {
		t.Fatal(err)
	}

	// Drifted Codex copy at the global skills location.
	codexDir := filepath.Join(home, ".agents", "skills", "foo")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	driftedBody := []byte("drifted in codex\n")
	if err := os.WriteFile(filepath.Join(codexDir, "SKILL.md"), driftedBody, 0o644); err != nil {
		t.Fatal(err)
	}

	shared := model.Item{
		Origin: model.OriginShared, Kind: model.KindSkill, Scope: model.ScopeGlobal,
		Name: "foo", Path: filepath.Join(canonical, "SKILL.md"), Storage: model.StorageDir,
		Shared: true, Body: string(canonicalBody),
	}
	if err := Resync(shared, ResyncCanonicalWins); err != nil {
		t.Fatalf("Resync canonical wins: %v", err)
	}

	snaps, err := backup.List()
	if err != nil {
		t.Fatalf("backup.List: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snaps))
	}
	if snaps[0].Op != "resync-canonical" {
		t.Errorf("Op = %q, want resync-canonical", snaps[0].Op)
	}
	if len(snaps[0].Items) == 0 {
		t.Fatal("snapshot has no items")
	}
	// The captured body should hold the drifted bytes — not the
	// canonical ones — because we snapshot pre-overwrite.
	id := snaps[0].ID
	root, err := backup.Root()
	if err != nil {
		t.Fatal(err)
	}
	captured := filepath.Join(root, id, snaps[0].Items[0].BodyPath, "SKILL.md")
	got, err := os.ReadFile(captured)
	if err != nil {
		t.Fatalf("read captured: %v", err)
	}
	if string(got) != string(driftedBody) {
		t.Errorf("captured body = %q, want %q", got, driftedBody)
	}
}
