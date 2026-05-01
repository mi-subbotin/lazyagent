package actions

import (
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

func TestIsSyncConflict(t *testing.T) {
	if IsSyncConflict(nil) {
		t.Error("nil errs should not report conflict")
	}
	if !IsSyncConflict([]error{ErrShareConflicts}) {
		t.Error("ErrShareConflicts should be detected")
	}
}
