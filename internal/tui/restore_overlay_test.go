package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/backup"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

// openRestoreOverlay drives the model with `Z` and returns the
// resulting Model so each test starts from the same overlay state.
func openRestoreOverlay(t *testing.T, m Model) Model {
	t.Helper()
	out := feed(t, m, "Z")
	if out.restoreOverlay == nil {
		t.Fatalf("Z did not open restoreOverlay")
	}
	return out
}

func TestRestoreOverlay_EmptyList_RendersHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := newTestModel(t, fixtureItems(), "")
	m = openRestoreOverlay(t, m)
	body := restoreOverlayText(*m.restoreOverlay)
	if !strings.Contains(body, "No snapshots yet") {
		t.Errorf("expected empty-state hint, got:\n%s", body)
	}
	if !strings.Contains(body, "[esc]") {
		t.Errorf("expected close hint, got:\n%s", body)
	}
}

func TestRestoreOverlay_NavigatesAndOpensDetail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	pathA := filepath.Join(home, "scratch", "alpha.md")
	pathB := filepath.Join(home, "scratch", "beta.md")
	if err := os.MkdirAll(filepath.Dir(pathA), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathA, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte("beta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	itA := model.Item{
		Origin: model.OriginClaude, Kind: model.KindAgent, Scope: model.ScopeGlobal,
		Name: "alpha", Path: pathA, Storage: model.StorageFile,
	}
	itB := model.Item{
		Origin: model.OriginClaude, Kind: model.KindAgent, Scope: model.ScopeGlobal,
		Name: "beta", Path: pathB, Storage: model.StorageFile,
	}
	if _, err := backup.Create("delete", []model.Item{itA}); err != nil {
		t.Fatalf("Create A: %v", err)
	}
	// Force an ordering gap so the second snapshot has a strictly
	// later id.
	if _, err := backup.Create("place-overwrite", []model.Item{itB}); err != nil {
		t.Fatalf("Create B: %v", err)
	}

	m := newTestModel(t, fixtureItems(), "")
	m = openRestoreOverlay(t, m)
	if got := len(m.restoreOverlay.snapshots); got != 2 {
		t.Fatalf("snapshots=%d, want 2", got)
	}
	if c := m.restoreOverlay.listCursor; c != 0 {
		t.Errorf("initial listCursor=%d, want 0", c)
	}
	m = feed(t, m, "j")
	if c := m.restoreOverlay.listCursor; c != 1 {
		t.Errorf("after j listCursor=%d, want 1", c)
	}
	m = feed(t, m, "k")
	if c := m.restoreOverlay.listCursor; c != 0 {
		t.Errorf("after k listCursor=%d, want 0", c)
	}
	m = feed(t, m, "enter")
	if m.restoreOverlay.phase != restorePhaseDetail {
		t.Errorf("phase=%v, want detail", m.restoreOverlay.phase)
	}
	body := restoreOverlayText(*m.restoreOverlay)
	if !strings.Contains(body, "snapshot ") {
		t.Errorf("detail body missing snapshot header:\n%s", body)
	}
}

func TestRestoreOverlay_RestoreItem_RewritesPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path := filepath.Join(home, "scratch", "agent.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("agent body v1\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindAgent, Scope: model.ScopeGlobal,
		Name: "agent", Path: path, Storage: model.StorageFile,
	}
	if _, err := backup.Create("delete", []model.Item{it}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t, fixtureItems(), "")
	m = openRestoreOverlay(t, m)
	m = feed(t, m, "enter")
	if m.restoreOverlay.phase != restorePhaseDetail {
		t.Fatalf("phase=%v, want detail", m.restoreOverlay.phase)
	}
	m = feed(t, m, "r")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("restored bytes=%q, want %q", got, original)
	}
}

func TestRestoreOverlay_RestoreItem_PromptsOverwriteWhenOccupied(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path := filepath.Join(home, "scratch", "agent.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("v1\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindAgent, Scope: model.ScopeGlobal,
		Name: "agent", Path: path, Storage: model.StorageFile,
	}
	if _, err := backup.Create("delete", []model.Item{it}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t, fixtureItems(), "")
	m = openRestoreOverlay(t, m)
	m = feed(t, m, "enter")
	m = feed(t, m, "r")
	if m.restoreOverlay.phase != restorePhaseConfirmOverwrite {
		t.Fatalf("phase=%v, want confirm-overwrite", m.restoreOverlay.phase)
	}
	m = feed(t, m, "y")
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Errorf("after confirm-overwrite, file=%q want %q", got, original)
	}
}

func TestRestoreOverlay_DeleteSnapshot_DropsFromList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, name := range []string{"a", "b"} {
		path := filepath.Join(home, "scratch", name+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		it := model.Item{
			Origin: model.OriginClaude, Kind: model.KindAgent, Scope: model.ScopeGlobal,
			Name: name, Path: path, Storage: model.StorageFile,
		}
		if _, err := backup.Create("delete", []model.Item{it}); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	m := newTestModel(t, fixtureItems(), "")
	m = openRestoreOverlay(t, m)
	if got := len(m.restoreOverlay.snapshots); got != 2 {
		t.Fatalf("snapshots=%d, want 2", got)
	}
	m = feed(t, m, "D")
	if m.restoreOverlay.phase != restorePhaseConfirmDelete {
		t.Fatalf("phase=%v, want confirm-delete", m.restoreOverlay.phase)
	}
	m = feed(t, m, "y")
	if m.restoreOverlay.phase != restorePhaseList {
		t.Fatalf("phase=%v, want list after delete", m.restoreOverlay.phase)
	}
	if got := len(m.restoreOverlay.snapshots); got != 1 {
		t.Errorf("snapshots after delete=%d, want 1", got)
	}
}

func TestRelTime(t *testing.T) {
	parse := func(s string) time.Time {
		t.Helper()
		out, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("parse %s: %v", s, err)
		}
		return out
	}
	now := parse("2026-05-05T12:00:00Z")
	cases := []struct {
		t    string
		want string
	}{
		{"2026-05-05T11:59:50Z", "just now"},
		{"2026-05-05T11:55:00Z", "5m ago"},
		{"2026-05-05T10:00:00Z", "2h ago"},
		{"2026-05-04T12:00:00Z", "yesterday"},
		{"2026-05-02T12:00:00Z", "3d ago"},
	}
	for _, c := range cases {
		got := relTime(parse(c.t), now)
		if got != c.want {
			t.Errorf("relTime(%s) = %q, want %q", c.t, got, c.want)
		}
	}
}
