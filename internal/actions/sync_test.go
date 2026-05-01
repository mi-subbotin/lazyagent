package actions

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestSyncAll_ImportsUnsharedItem(t *testing.T) {
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal,
			Name: "alpha", Path: "/tmp/.claude/skills/alpha/SKILL.md", Storage: model.StorageDir},
	}
	plan := SyncAll(items)
	if len(plan.Ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(plan.Ops))
	}
	op := plan.Ops[0]
	if op.Action != ActionImport {
		t.Errorf("action = %v, want Import", op.Action)
	}
	// Skills project to all three tools — none of them require format conversion.
	if len(op.Targets) != 3 {
		t.Errorf("expected 3 targets for skill, got %d", len(op.Targets))
	}
}

func TestSyncAll_DedupesAcrossTools(t *testing.T) {
	// Same skill name surfaced by all three tool adapters → still one
	// PlanOp. Claude wins as the source of truth (lowest origin rank).
	items := []model.Item{
		{Origin: model.OriginGemini, Kind: model.KindSkill, Scope: model.ScopeGlobal,
			Name: "alpha", Storage: model.StorageDir, Path: "/g/a/SKILL.md"},
		{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal,
			Name: "alpha", Storage: model.StorageDir, Path: "/c/a/SKILL.md"},
		{Origin: model.OriginCodex, Kind: model.KindSkill, Scope: model.ScopeGlobal,
			Name: "alpha", Storage: model.StorageDir, Path: "/x/a/SKILL.md"},
	}
	plan := SyncAll(items)
	if len(plan.Ops) != 1 {
		t.Fatalf("got %d ops, want 1 (deduped)", len(plan.Ops))
	}
	if plan.Ops[0].Item.Origin != model.OriginClaude {
		t.Errorf("source-of-truth origin = %v, want Claude", plan.Ops[0].Item.Origin)
	}
}

func TestSyncAll_CanonicalWinsOverFreshTool(t *testing.T) {
	// One copy lives in the store (Shared=true) — that wins regardless
	// of which other tools surface the same name.
	items := []model.Item{
		{Origin: model.OriginGemini, Kind: model.KindSkill, Scope: model.ScopeGlobal,
			Name: "alpha", Storage: model.StorageDir, Shared: true, Path: "/store/a/SKILL.md"},
		{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal,
			Name: "alpha", Storage: model.StorageDir, Path: "/c/a/SKILL.md"},
	}
	plan := SyncAll(items)
	if len(plan.Ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(plan.Ops))
	}
	if !plan.Ops[0].Item.Shared {
		t.Errorf("canonical-wins broken: source = %+v", plan.Ops[0].Item)
	}
}

func TestSyncAll_SkipsLocal(t *testing.T) {
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeLocal,
			Name: "local-only", Storage: model.StorageDir, Path: "/p/.claude/skills/x/SKILL.md"},
	}
	plan := SyncAll(items)
	if len(plan.Ops) != 0 {
		t.Errorf("local items should not appear in plan; got %d ops", len(plan.Ops))
	}
}

func TestSyncAll_MarksUnsupportedKindAsSkip(t *testing.T) {
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
			Name: "linear", Storage: model.StorageEntry, Path: "/tmp/.claude.json", ConfigKey: "mcpServers/linear"},
	}
	plan := SyncAll(items)
	if len(plan.Ops) != 1 {
		t.Fatalf("MCP entry should produce one Skip op, got %d", len(plan.Ops))
	}
	if plan.Ops[0].Action != ActionSkip {
		t.Errorf("MCP action = %v, want Skip", plan.Ops[0].Action)
	}
	if plan.Ops[0].Reason == "" {
		t.Error("Skip op should carry a reason")
	}
}

func TestSyncAll_CountsMatchOps(t *testing.T) {
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal,
			Name: "alpha", Storage: model.StorageDir, Path: "/c/a/SKILL.md"},
		{Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
			Name: "linear", Storage: model.StorageEntry, Path: "/tmp/x.json", ConfigKey: "mcpServers/linear"},
	}
	plan := SyncAll(items)
	counts := plan.Counts()
	if counts[ActionImport] != 1 {
		t.Errorf("import count = %d, want 1", counts[ActionImport])
	}
	if counts[ActionSkip] != 1 {
		t.Errorf("skip count = %d, want 1", counts[ActionSkip])
	}
	if !plan.Mutating() {
		t.Error("Mutating() should be true when at least one Import is present")
	}
}

func TestSyncAll_AllSkippedIsNotMutating(t *testing.T) {
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
			Name: "linear", Storage: model.StorageEntry, Path: "/tmp/x.json", ConfigKey: "mcpServers/linear"},
	}
	plan := SyncAll(items)
	if plan.Mutating() {
		t.Error("plan with only skips should report Mutating=false")
	}
}

func TestPlanActionString(t *testing.T) {
	cases := map[PlanAction]string{
		ActionImport:  "import",
		ActionProject: "project",
		ActionResync:  "resync",
		ActionSkip:    "skip",
	}
	for a, want := range cases {
		if got := a.String(); got != want {
			t.Errorf("PlanAction(%d).String() = %q, want %q", a, got, want)
		}
	}
}

// TestApplyPlan_ImportsViaPlace stages a Claude-only skill, builds a
// plan with SyncAll, and verifies ApplyPlan promotes the bytes to the
// library and projects them back to all three tools — the same shape a
// per-item `p` would produce. Guards the sync-via-Place migration.
func TestApplyPlan_ImportsViaPlace(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "echo", "hi\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	plan := SyncAll([]model.Item{it})
	if errs := ApplyPlan(plan, false); len(errs) != 0 {
		t.Fatalf("ApplyPlan errors: %v", errs)
	}
	canonicalDir := filepath.Join(lib, "skills", "echo")
	if _, err := os.Stat(filepath.Join(canonicalDir, "SKILL.md")); err != nil {
		t.Fatalf("canonical body missing: %v", err)
	}
	for _, p := range []string{
		filepath.Join(home, ".claude", "skills", "echo"),
		filepath.Join(home, ".agents", "skills", "echo"),
		filepath.Join(home, ".gemini", "skills", "echo"),
	} {
		if got, err := os.Readlink(p); err != nil || got != canonicalDir {
			t.Errorf("%s -> %s err=%v, want %s", p, got, err, canonicalDir)
		}
	}
}

// TestApplyPlan_ConflictReturnsErrPlaceConflicts confirms the conflict
// sentinel propagates from Place through ApplyPlan unchanged. The CLI's
// --yes flag and the sync-confirm overlay both rely on this.
func TestApplyPlan_ConflictReturnsErrPlaceConflicts(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "echo", "fresh\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	// Pre-existing unrelated content at a target path forces a conflict.
	codexExisting := filepath.Join(home, ".agents", "skills", "echo")
	if err := os.MkdirAll(codexExisting, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexExisting, "SKILL.md"), []byte("not ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := SyncAll([]model.Item{it})
	errs := ApplyPlan(plan, false)
	if len(errs) == 0 {
		t.Fatalf("expected conflict error, got none")
	}
	var hit bool
	for _, e := range errs {
		if errors.Is(e, ErrPlaceConflicts) {
			hit = true
		}
	}
	if !hit {
		t.Errorf("none of %v wraps ErrPlaceConflicts", errs)
	}
	if !IsSyncConflict(errs) {
		t.Errorf("IsSyncConflict should detect the conflict; errs=%v", errs)
	}
}

func TestIsSyncConflict(t *testing.T) {
	if IsSyncConflict(nil) {
		t.Error("nil errs should not report conflict")
	}
	if !IsSyncConflict([]error{ErrPlaceConflicts}) {
		t.Error("ErrPlaceConflicts should be detected")
	}
}
