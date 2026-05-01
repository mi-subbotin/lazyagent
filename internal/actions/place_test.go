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

// Lossy combos (agent → codex profile, prompt → gemini toml) are
// out of scope for Place v1; the picker is expected to grey them out
// or fall back to CrossCopy. Verify Place rejects them with a clear
// error before any disk work.
func TestPlace_RejectsLossyAgentToCodex(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	if err := os.MkdirAll(filepath.Join(home, ".claude", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(home, ".claude", "agents", "reviewer.md")
	if err := os.WriteFile(bodyPath, []byte("---\nname: reviewer\n---\nbody\n"), 0o644); err != nil {
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

	err := Place(it, []ProjectionTarget{{model.OriginCodex, model.ScopeGlobal}}, PlaceOpts{})
	if !errors.Is(err, ErrPlaceUnsupported) {
		t.Fatalf("want ErrPlaceUnsupported for agent → codex, got %v", err)
	}
	// The lossless target stays accepted.
	if err := Place(it, []ProjectionTarget{{model.OriginGemini, model.ScopeGlobal}}, PlaceOpts{}); err != nil {
		t.Fatalf("agent → gemini should be accepted: %v", err)
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
