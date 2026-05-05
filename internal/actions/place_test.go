package actions

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

// First-time Place: a per-tool skill with no library entry yet must
// have its bytes promoted into the library and a fresh symlink must
// land back at the original Claude path. Codex and Gemini global
// targets get parallel projections.
func TestPlaceSkill_PromotesAndProjectsToAllGlobalTools(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "echo", "hi\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginCodex, model.ScopeGlobal},
		{model.OriginGemini, model.ScopeGlobal},
	}
	if err := Place(it, targets, PlaceOpts{}); err != nil {
		t.Fatalf("Place: %v", err)
	}

	canonicalDir := filepath.Join(lib, "skills", "echo")
	if data, err := os.ReadFile(filepath.Join(canonicalDir, "SKILL.md")); err != nil || string(data) != "hi\n" {
		t.Fatalf("canonical body wrong: data=%q err=%v", data, err)
	}
	for _, p := range []string{
		filepath.Join(home, ".claude", "skills", "echo"),
		filepath.Join(home, ".agents", "skills", "echo"),
		filepath.Join(home, ".gemini", "skills", "echo"),
	} {
		got, err := os.Readlink(p)
		if err != nil {
			t.Fatalf("readlink %s: %v", p, err)
		}
		if got != canonicalDir {
			t.Errorf("%s -> %s, want %s", p, got, canonicalDir)
		}
	}
}

// Empty target set is the "git stash" state: bytes go into the
// library, no projections exist on disk. The original tool path must
// be gone (its bytes have moved). Library can serve them later.
func TestPlaceSkill_EmptyTargetsLeavesItemInLibraryOnly(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "echo", "hi\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	if err := Place(it, nil, PlaceOpts{}); err != nil {
		t.Fatalf("Place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, "skills", "echo", "SKILL.md")); err != nil {
		t.Errorf("library body missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "echo")); !os.IsNotExist(err) {
		t.Errorf("source path should be empty after promote with no targets, got err=%v", err)
	}
}

// Local-scope targets land at <projectDir>/.claude/skills/<name>
// (or sibling layouts for codex/.agents and gemini). projectDir must
// be threaded through PlaceOpts; without it, local targets must
// error before any disk write.
func TestPlaceSkill_LocalScopeTargetUnderProjectDir(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	proj := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "echo", "hi\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeLocal},
	}
	if err := Place(it, targets, PlaceOpts{ProjectDir: proj}); err != nil {
		t.Fatalf("Place: %v", err)
	}
	canonicalDir := filepath.Join(lib, "skills", "echo")
	got, err := os.Readlink(filepath.Join(proj, ".claude", "skills", "echo"))
	if err != nil {
		t.Fatalf("readlink local: %v", err)
	}
	if got != canonicalDir {
		t.Errorf("local projection -> %s, want %s", got, canonicalDir)
	}
}

// Local-scope targets without a ProjectDir must fail validation
// before mutating anything. Verify the library and original source
// path are both untouched on the rejection path.
func TestPlace_LocalTargetWithoutProjectDirFailsCleanly(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "echo", "hi\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	err := Place(it, []ProjectionTarget{{model.OriginClaude, model.ScopeLocal}}, PlaceOpts{})
	if !errors.Is(err, ErrNoProject) {
		t.Fatalf("want ErrNoProject, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, "skills", "echo")); !os.IsNotExist(err) {
		t.Errorf("library should be untouched on validation error; stat err=%v", err)
	}
	if _, err := os.Stat(bodyPath); err != nil {
		t.Errorf("source should be untouched: %v", err)
	}
}

// Re-running Place with a smaller target set must remove the
// projections that fell out and leave the kept ones untouched. This
// is the "Reshare" flow — the unified API must support it natively.
func TestPlace_ReducingTargetsUnprojectsRemovedOnes(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "echo", "hi\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	allThree := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginCodex, model.ScopeGlobal},
		{model.OriginGemini, model.ScopeGlobal},
	}
	if err := Place(it, allThree, PlaceOpts{}); err != nil {
		t.Fatalf("first Place: %v", err)
	}

	// Re-load it from the (now) shared origin so canonical resolution works.
	it2 := skillItem("echo", filepath.Join(lib, "skills", "echo", "SKILL.md"), model.OriginShared, model.ScopeGlobal)

	smaller := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginCodex, model.ScopeGlobal},
	}
	if err := Place(it2, smaller, PlaceOpts{}); err != nil {
		t.Fatalf("second Place: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".gemini", "skills", "echo")); !os.IsNotExist(err) {
		t.Errorf("Gemini projection should be gone, stat err=%v", err)
	}
	for _, p := range []string{
		filepath.Join(home, ".claude", "skills", "echo"),
		filepath.Join(home, ".agents", "skills", "echo"),
	} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("kept projection lost: %s err=%v", p, err)
		}
	}
}

// Re-running Place with the source's own cell unchecked is the new
// "move" flow: source disappears, item lives only in the other
// targets. We start with a per-tool item, place it to {Codex} only,
// and verify the original Claude path is gone.
func TestPlace_FirstTimeWithoutSourceCellRemovesSource(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "echo", "hi\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	if err := Place(it, []ProjectionTarget{{model.OriginCodex, model.ScopeGlobal}}, PlaceOpts{}); err != nil {
		t.Fatalf("Place: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "echo")); !os.IsNotExist(err) {
		t.Errorf("source claude dir should be gone, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "echo", "SKILL.md")); err != nil {
		t.Errorf("codex projection broken: %v", err)
	}
}

// A target path that already holds unrelated content must surface as
// a conflict. Without Overwrite, Place returns ErrPlaceConflicts and
// leaves the library + source untouched. PlaceConflicts returns the
// same list so the TUI can render the confirm overlay.
func TestPlaceSkill_ConflictAtTargetWithoutOverwrite(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "echo", "fresh\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	// Pre-existing unrelated codex skill at the target.
	codexExisting := filepath.Join(home, ".agents", "skills", "echo")
	if err := os.MkdirAll(codexExisting, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexExisting, "SKILL.md"), []byte("not ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginCodex, model.ScopeGlobal},
	}
	cs, err := PlaceConflicts(it, targets, PlaceOpts{})
	if err != nil {
		t.Fatalf("PlaceConflicts: %v", err)
	}
	if len(cs) != 1 || cs[0].Target != model.OriginCodex {
		t.Errorf("expected one Codex conflict, got %+v", cs)
	}
	if err := Place(it, targets, PlaceOpts{}); !errors.Is(err, ErrPlaceConflicts) {
		t.Fatalf("Place must return ErrPlaceConflicts, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, "skills", "echo")); !os.IsNotExist(err) {
		t.Errorf("library must be untouched on conflict refusal, err=%v", err)
	}
	if _, err := os.Stat(bodyPath); err != nil {
		t.Errorf("source must be untouched on conflict refusal: %v", err)
	}
}

// With Overwrite=true the conflicting on-disk content is replaced by
// a healthy projection of the canonical bytes.
func TestPlaceSkill_OverwriteReplacesConflictingTarget(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "echo", "fresh\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	codexExisting := filepath.Join(home, ".agents", "skills", "echo")
	if err := os.MkdirAll(codexExisting, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexExisting, "SKILL.md"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginCodex, model.ScopeGlobal},
	}
	if err := Place(it, targets, PlaceOpts{Overwrite: true}); err != nil {
		t.Fatalf("Place(Overwrite): %v", err)
	}
	canonicalDir := filepath.Join(lib, "skills", "echo")
	got, err := os.Readlink(codexExisting)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if got != canonicalDir {
		t.Errorf("codex projection -> %s, want %s", got, canonicalDir)
	}
}

// Memory items live at different relative paths per tool
// (CLAUDE.md / AGENTS.md / GEMINI.md). Place must thread that
// remapping through projectionPath. The library canonical lands at
// memory/<name>/memory.md.
func TestPlaceMemory_ProjectsWithRenamePerTool(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.WriteFile(bodyPath, []byte("# memo\n"), 0o644); err != nil {
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
	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginCodex, model.ScopeGlobal},
		{model.OriginGemini, model.ScopeGlobal},
	}
	if err := Place(it, targets, PlaceOpts{}); err != nil {
		t.Fatalf("Place: %v", err)
	}
	canonicalBody := filepath.Join(lib, "memory", "CLAUDE", "memory.md")
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
			t.Errorf("%s -> %s, want %s", p, dest, canonicalBody)
		}
	}
}

// MCP / StorageEntry items don't enter the library, but Place still
// projects them between scopes within the same tool. v1 only supports
// same-Origin targets; cross-tool MCP routing is tracked under PRI-68.
// Verifies a Claude/Global MCP can be projected to Claude/Local without
// promoting any bytes.
func TestPlace_EntryProjectsBetweenScopesSameTool(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	proj := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	srcPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(srcPath, []byte(`{"mcpServers":{"fs":{"command":"node"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin:    model.OriginClaude,
		Kind:      model.KindMCP,
		Scope:     model.ScopeGlobal,
		Name:      "fs",
		Path:      srcPath,
		Storage:   model.StorageEntry,
		ConfigKey: "mcpServers/fs",
	}

	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginClaude, model.ScopeLocal},
	}
	if err := Place(it, targets, PlaceOpts{ProjectDir: proj}); err != nil {
		t.Fatalf("Place: %v", err)
	}
	// Source intact.
	if _, err := os.Stat(srcPath); err != nil {
		t.Errorf("source clobbered: %v", err)
	}
	// Local projection: <proj>/.mcp.json contains mcpServers/fs.
	localPath := filepath.Join(proj, ".mcp.json")
	if _, _, err := parseReadEntry(t, localPath, "mcpServers/fs"); err != nil {
		t.Errorf("local projection missing: %v", err)
	}
	// Library untouched — entries don't use it.
	if _, err := os.Stat(filepath.Join(lib, "mcp")); !os.IsNotExist(err) {
		t.Errorf("library should be untouched for entries, err=%v", err)
	}
}

// Cross-tool entry projection is out of scope for v1; the picker greys
// those cells, and Place rejects them with ErrPlaceUnsupported even if
// a caller bypasses the picker.
func TestPlace_RejectsCrossToolEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(srcPath, []byte(`{"mcpServers":{"fs":{"command":"node"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin:    model.OriginClaude,
		Kind:      model.KindMCP,
		Scope:     model.ScopeGlobal,
		Name:      "fs",
		Path:      srcPath,
		Storage:   model.StorageEntry,
		ConfigKey: "mcpServers/fs",
	}
	err := Place(it, []ProjectionTarget{{model.OriginCodex, model.ScopeGlobal}}, PlaceOpts{})
	if !errors.Is(err, ErrPlaceUnsupported) {
		t.Fatalf("want ErrPlaceUnsupported for cross-tool entry, got %v", err)
	}
}

// Re-running entry Place with a smaller target set must remove the
// dropped projection's entry from the target config — the entry-side
// equivalent of TestPlace_ReducingTargetsUnprojectsRemovedOnes.
func TestPlace_EntryReducingTargetsRemovesProjection(t *testing.T) {
	home := t.TempDir()
	proj := canonicalTempDir(t)
	t.Setenv("HOME", home)

	srcPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(srcPath, []byte(`{"mcpServers":{"fs":{"command":"node"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin:    model.OriginClaude,
		Kind:      model.KindMCP,
		Scope:     model.ScopeGlobal,
		Name:      "fs",
		Path:      srcPath,
		Storage:   model.StorageEntry,
		ConfigKey: "mcpServers/fs",
	}

	both := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginClaude, model.ScopeLocal},
	}
	if err := Place(it, both, PlaceOpts{ProjectDir: proj}); err != nil {
		t.Fatalf("first Place: %v", err)
	}
	localPath := filepath.Join(proj, ".mcp.json")
	if _, _, err := parseReadEntry(t, localPath, "mcpServers/fs"); err != nil {
		t.Fatalf("local projection missing after first Place: %v", err)
	}

	globalOnly := []ProjectionTarget{{model.OriginClaude, model.ScopeGlobal}}
	if err := Place(it, globalOnly, PlaceOpts{ProjectDir: proj}); err != nil {
		t.Fatalf("second Place: %v", err)
	}
	if _, _, err := parseReadEntry(t, localPath, "mcpServers/fs"); err == nil {
		t.Errorf("local entry should be gone")
	}
	// Source still in place.
	if _, _, err := parseReadEntry(t, srcPath, "mcpServers/fs"); err != nil {
		t.Errorf("source entry should survive: %v", err)
	}
}

// PRI-68: agent → codex profile is now a supported lossy projection.
// Place reads the canonical agent.md, parses frontmatter+body, and
// upserts a [profiles.<name>] entry into ~/.codex/config.toml. The
// generated entry must carry the body as `instructions` and surface
// any frontmatter `model` field; the canonical .md stays untouched.
func TestPlace_LossyAgentToCodexGeneratesProfileEntry(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	if err := os.MkdirAll(filepath.Join(home, ".claude", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(home, ".claude", "agents", "reviewer.md")
	body := "---\nname: reviewer\nmodel: claude-opus-4-7\n---\nReview PRs carefully.\n"
	if err := os.WriteFile(bodyPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin:  model.OriginClaude,
		Kind:    model.KindAgent,
		Scope:   model.ScopeGlobal,
		Name:    "reviewer",
		Path:    bodyPath,
		Storage: model.StorageFile,
	}

	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginCodex, model.ScopeGlobal},
	}
	if err := Place(it, targets, PlaceOpts{}); err != nil {
		t.Fatalf("Place: %v", err)
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	val, _, err := parseReadEntry(t, configPath, "profiles/reviewer")
	if err != nil {
		t.Fatalf("codex profile entry missing: %v", err)
	}
	entry, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("entry is not a map: %T", val)
	}
	if entry["model"] != "claude-opus-4-7" {
		t.Errorf("model field lost: %v", entry["model"])
	}
	if instr, _ := entry["instructions"].(string); !strings.Contains(instr, "Review PRs carefully") {
		t.Errorf("instructions missing body: %q", instr)
	}
}

// PRI-68: prompt → gemini TOML generates a commands/<name>.toml file
// with description + multi-line prompt. The canonical .md remains
// untouched; the gemini target is a fresh-write file, not a symlink.
func TestPlace_LossyPromptToGeminiGeneratesTOML(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	if err := os.MkdirAll(filepath.Join(home, ".claude", "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(home, ".claude", "commands", "summarise.md")
	body := "---\nname: summarise\ndescription: \"summarise diff\"\n---\nSummarise the staged diff.\nKeep it short.\n"
	if err := os.WriteFile(bodyPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin:  model.OriginClaude,
		Kind:    model.KindPrompt,
		Scope:   model.ScopeGlobal,
		Name:    "summarise",
		Path:    bodyPath,
		Storage: model.StorageFile,
	}
	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginGemini, model.ScopeGlobal},
	}
	if err := Place(it, targets, PlaceOpts{}); err != nil {
		t.Fatalf("Place: %v", err)
	}
	tomlPath := filepath.Join(home, ".gemini", "commands", "summarise.toml")
	data, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatalf("gemini toml missing: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "description = \"summarise diff\"") {
		t.Errorf("description missing:\n%s", s)
	}
	if !strings.Contains(s, "Summarise the staged diff") {
		t.Errorf("prompt body missing:\n%s", s)
	}
	if !strings.Contains(s, "prompt = '''") {
		t.Errorf("multi-line literal-string syntax expected:\n%s", s)
	}
}

// PRI-68: re-running Place with a smaller target set must clean up
// the lossy projection — the codex profile entry should be deleted
// and the gemini TOML file removed.
func TestPlace_LossyReducingTargetsRemovesProjections(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	if err := os.MkdirAll(filepath.Join(home, ".claude", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(home, ".claude", "agents", "reviewer.md")
	if err := os.WriteFile(bodyPath, []byte("---\nname: reviewer\n---\nReview\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	it := skillItem("reviewer", bodyPath, model.OriginClaude, model.ScopeGlobal)
	it.Kind = model.KindAgent
	it.Storage = model.StorageFile

	full := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginCodex, model.ScopeGlobal},
	}
	if err := Place(it, full, PlaceOpts{}); err != nil {
		t.Fatalf("first Place: %v", err)
	}

	canonicalBody := filepath.Join(lib, "agents", "reviewer", "agent.md")
	it2 := model.Item{
		Origin: model.OriginShared, Kind: model.KindAgent, Scope: model.ScopeGlobal,
		Name: "reviewer", Path: canonicalBody, Storage: model.StorageFile,
	}
	smaller := []ProjectionTarget{{model.OriginClaude, model.ScopeGlobal}}
	if err := Place(it2, smaller, PlaceOpts{}); err != nil {
		t.Fatalf("second Place: %v", err)
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	if _, _, err := parseReadEntry(t, configPath, "profiles/reviewer"); err == nil {
		t.Errorf("codex profile entry should be gone")
	}
}

// CurrentPlaceProjections must reflect both global and local symlink
// projections so the TUI picker can pre-check the matrix accurately.
func TestCurrentPlaceProjections_GlobalAndLocal(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	proj := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "echo", "hi\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginGemini, model.ScopeLocal},
	}
	if err := Place(it, targets, PlaceOpts{ProjectDir: proj}); err != nil {
		t.Fatalf("Place: %v", err)
	}

	// After Place the source is now a symlink-resolved item; pick it
	// up via the canonical instead.
	shared := skillItem("echo", filepath.Join(lib, "skills", "echo", "SKILL.md"), model.OriginShared, model.ScopeGlobal)
	got := CurrentPlaceProjections(shared, proj)

	have := map[ProjectionTarget]bool{}
	for _, t := range got {
		have[t] = true
	}
	if !have[ProjectionTarget{model.OriginClaude, model.ScopeGlobal}] {
		t.Errorf("missing claude/global; got %+v", got)
	}
	if !have[ProjectionTarget{model.OriginGemini, model.ScopeLocal}] {
		t.Errorf("missing gemini/local; got %+v", got)
	}
}

// Manifest must record the projected_to set after Place so a later
// Resync (which falls back to manifest when on-disk state is gone)
// keeps working. Local projections are scanned on disk and not
// persisted — only globals appear in projected_to.
func TestPlace_WritesProjectedToInManifest(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	proj := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "echo", "hi\n")
	it := skillItem("echo", bodyPath, model.OriginClaude, model.ScopeGlobal)

	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginCodex, model.ScopeGlobal},
		{model.OriginGemini, model.ScopeLocal},
	}
	if err := Place(it, targets, PlaceOpts{ProjectDir: proj}); err != nil {
		t.Fatalf("Place: %v", err)
	}

	manifestPath := store.ManifestPath(filepath.Join(lib, "skills", "echo"))
	m, err := store.ReadManifest(manifestPath)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	got := strings.Join(m.ProjectedTo, ",")
	if !strings.Contains(got, "Claude") || !strings.Contains(got, "Codex") {
		t.Errorf("ProjectedTo must include Claude and Codex globals, got %v", m.ProjectedTo)
	}
	if strings.Contains(got, "Gemini") {
		t.Errorf("ProjectedTo must not include Gemini (it is only local), got %v", m.ProjectedTo)
	}
}

// --- helpers -------------------------------------------------------

// parseReadEntry wraps parse.ReadEntry for the entry-shape tests. The
// extra path passthrough mirrors the assertion call sites so a failure
// includes the file the test was poking at.
func parseReadEntry(t *testing.T, path, key string) (any, string, error) {
	t.Helper()
	v, _, err := parse.ReadEntry(path, key)
	return v, path, err
}

func stageClaudeSkill(t *testing.T, home, name, body string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(bodyPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return bodyPath
}

func skillItem(name, bodyPath string, origin model.Origin, scope model.Scope) model.Item {
	return model.Item{
		Origin:  origin,
		Kind:    model.KindSkill,
		Scope:   scope,
		Name:    name,
		Path:    bodyPath,
		Storage: model.StorageDir,
	}
}

// PRI-71: reverse-promotion of a Codex profile to a library agent.md.
// First-time Place on (KindAgent, OriginCodex, StorageEntry) must
// synthesise frontmatter (name + model) plus body (instructions),
// write <lib>/agents/<name>/agent.md, and leave the source profile
// entry intact (regenerated by projectLossy from the new canonical).
func TestPlace_ReverseLossyCodexProfileToLibrary(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := parse.Write(configPath, map[string]any{
		"profiles": map[string]any{
			"reviewer": map[string]any{
				"instructions": "Review carefully.",
				"model":        "gpt-5",
			},
		},
	}, parse.FormatTOML); err != nil {
		t.Fatal(err)
	}

	it := model.Item{
		Origin:    model.OriginCodex,
		Kind:      model.KindAgent,
		Scope:     model.ScopeGlobal,
		Name:      "reviewer",
		Path:      configPath,
		ConfigKey: "profiles/reviewer",
		Storage:   model.StorageEntry,
	}
	targets := []ProjectionTarget{
		{model.OriginCodex, model.ScopeGlobal},
		{model.OriginClaude, model.ScopeGlobal},
	}
	if err := Place(it, targets, PlaceOpts{}); err != nil {
		t.Fatalf("Place: %v", err)
	}

	canonicalBody := filepath.Join(lib, "agents", "reviewer", "agent.md")
	data, err := os.ReadFile(canonicalBody)
	if err != nil {
		t.Fatalf("library agent.md missing: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "name: reviewer") {
		t.Errorf("frontmatter missing name:\n%s", s)
	}
	if !strings.Contains(s, "model: gpt-5") {
		t.Errorf("frontmatter missing model:\n%s", s)
	}
	if !strings.Contains(s, "Review carefully.") {
		t.Errorf("body missing:\n%s", s)
	}

	// Codex profile entry survived (regenerated from canonical).
	val, _, err := parseReadEntry(t, configPath, "profiles/reviewer")
	if err != nil {
		t.Fatalf("codex profile entry gone: %v", err)
	}
	entry, _ := val.(map[string]any)
	if entry["model"] != "gpt-5" {
		t.Errorf("codex profile model lost: %v", entry["model"])
	}
	if instr, _ := entry["instructions"].(string); !strings.Contains(instr, "Review carefully") {
		t.Errorf("codex profile instructions lost: %q", instr)
	}

	// Claude side received the lossless symlink projection.
	claudePath := filepath.Join(home, ".claude", "agents", "reviewer.md")
	got, err := os.Readlink(claudePath)
	if err != nil {
		t.Fatalf("claude symlink missing: %v", err)
	}
	if got != canonicalBody {
		t.Errorf("claude -> %s, want %s", got, canonicalBody)
	}
}

// PRI-71: reverse-promotion of a Gemini TOML prompt to a library
// prompt.md. First-time Place on (KindPrompt, OriginGemini,
// StorageFile, *.toml) must extract description + prompt, synthesise
// frontmatter, write <lib>/prompts/<name>/prompt.md, and keep the
// source TOML in place after projectLossy regenerates it.
func TestPlace_ReverseLossyGeminiTOMLToLibrary(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	tomlPath := filepath.Join(home, ".gemini", "commands", "summarise.toml")
	if err := os.MkdirAll(filepath.Dir(tomlPath), 0o755); err != nil {
		t.Fatal(err)
	}
	tomlBody := "description = \"summarise diff\"\nprompt = '''\nSummarise the staged diff.\nKeep it short.\n'''\n"
	if err := os.WriteFile(tomlPath, []byte(tomlBody), 0o644); err != nil {
		t.Fatal(err)
	}

	it := model.Item{
		Origin:  model.OriginGemini,
		Kind:    model.KindPrompt,
		Scope:   model.ScopeGlobal,
		Name:    "summarise",
		Path:    tomlPath,
		Storage: model.StorageFile,
	}
	targets := []ProjectionTarget{
		{model.OriginGemini, model.ScopeGlobal},
		{model.OriginClaude, model.ScopeGlobal},
	}
	if err := Place(it, targets, PlaceOpts{}); err != nil {
		t.Fatalf("Place: %v", err)
	}

	canonicalBody := filepath.Join(lib, "prompts", "summarise", "prompt.md")
	data, err := os.ReadFile(canonicalBody)
	if err != nil {
		t.Fatalf("library prompt.md missing: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "name: summarise") {
		t.Errorf("frontmatter missing name:\n%s", s)
	}
	if !strings.Contains(s, "description: \"summarise diff\"") {
		t.Errorf("frontmatter missing description:\n%s", s)
	}
	if !strings.Contains(s, "Summarise the staged diff") {
		t.Errorf("body missing:\n%s", s)
	}

	// Gemini source still on disk after Place (regenerated by lossy).
	if _, err := os.Stat(tomlPath); err != nil {
		t.Errorf("gemini toml gone: %v", err)
	}

	// Claude side received the lossless symlink.
	claudePath := filepath.Join(home, ".claude", "commands", "summarise.md")
	got, err := os.Readlink(claudePath)
	if err != nil {
		t.Fatalf("claude symlink missing: %v", err)
	}
	if got != canonicalBody {
		t.Errorf("claude -> %s, want %s", got, canonicalBody)
	}
}

// PRI-71: full round-trip — Place a Codex profile into the library,
// then read the regenerated codex/claude projections and confirm the
// canonical agent.md + projection bytes converge to a stable form.
func TestPlace_ReverseLossyCodexProfile_RoundTrip(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := parse.Write(configPath, map[string]any{
		"profiles": map[string]any{
			"strict": map[string]any{
				"instructions": "Be strict.",
			},
		},
	}, parse.FormatTOML); err != nil {
		t.Fatal(err)
	}

	it := model.Item{
		Origin:    model.OriginCodex,
		Kind:      model.KindAgent,
		Scope:     model.ScopeGlobal,
		Name:      "strict",
		Path:      configPath,
		ConfigKey: "profiles/strict",
		Storage:   model.StorageEntry,
	}
	if err := Place(it, []ProjectionTarget{{model.OriginCodex, model.ScopeGlobal}}, PlaceOpts{}); err != nil {
		t.Fatalf("Place: %v", err)
	}

	// Drift check after round-trip should be clean.
	canonicalDir := filepath.Join(lib, "agents", "strict")
	if LossyProjectionDrift(it, canonicalDir, "") {
		t.Errorf("round-tripped codex profile should not drift")
	}

	// Manifest must record codex as a projection target.
	man, err := store.ReadManifest(store.ManifestPath(canonicalDir))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if len(man.ProjectedTo) != 1 || man.ProjectedTo[0] != "Codex" {
		t.Errorf("manifest projected_to = %v, want [Codex]", man.ProjectedTo)
	}
}

// PRI-71: CurrentPlaceProjections must surface the source's own cell
// for a not-yet-promoted reverse-lossy item, otherwise newPlacePicker
// can't pre-check it and an unwitting enter would un-project the
// source as part of the diff.
func TestCurrentPlaceProjections_ReverseLossyPrePromotion(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := parse.Write(configPath, map[string]any{
		"profiles": map[string]any{
			"p1": map[string]any{
				"instructions": "x",
			},
		},
	}, parse.FormatTOML); err != nil {
		t.Fatal(err)
	}

	it := model.Item{
		Origin:    model.OriginCodex,
		Kind:      model.KindAgent,
		Scope:     model.ScopeGlobal,
		Name:      "p1",
		Path:      configPath,
		ConfigKey: "profiles/p1",
		Storage:   model.StorageEntry,
	}
	got := CurrentPlaceProjections(it, "")
	want := []ProjectionTarget{{model.OriginCodex, model.ScopeGlobal}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("CurrentPlaceProjections = %+v, want %+v", got, want)
	}
}

