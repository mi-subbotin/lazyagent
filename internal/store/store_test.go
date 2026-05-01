package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func statHelper(p string) (os.FileInfo, error) {
	return os.Stat(p)
}

func TestKindDir(t *testing.T) {
	cases := []struct {
		k    model.Kind
		want string
		ok   bool
	}{
		{model.KindSkill, "skills", true},
		{model.KindAgent, "agents", true},
		{model.KindMCP, "mcp", true},
		{model.KindPrompt, "prompts", true},
		{model.KindMemory, "memory", true},
		{model.KindSession, "", false},
		{model.KindHook, "", false},
	}
	for _, tc := range cases {
		got, ok := KindDir(tc.k)
		if ok != tc.ok || got != tc.want {
			t.Errorf("KindDir(%v) = %q,%v; want %q,%v", tc.k, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRootHonoursEnv(t *testing.T) {
	tmp := t.TempDir()
	// Legacy env var still works for back-compat: a fresh process
	// upgraded from a pre-PRI-65 build keeps reading the same dir.
	t.Setenv("LAZYAGENT_LIBRARY", "")
	t.Setenv("LAZYAGENT_STORE", tmp)
	got, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if filepath.Base(got) != filepath.Base(tmp) {
		t.Errorf("Root = %q, want under %q", got, tmp)
	}
}

// LAZYAGENT_LIBRARY is the canonical env var; if both are set the
// new one wins so users migrating their shell rc files can flip
// without removing the old export immediately.
func TestRootLibraryEnvWinsOverStoreEnv(t *testing.T) {
	libDir := t.TempDir()
	storeDir := t.TempDir()
	t.Setenv("LAZYAGENT_LIBRARY", libDir)
	t.Setenv("LAZYAGENT_STORE", storeDir)
	got, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(libDir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got != resolved {
		t.Errorf("Root = %q, want %q (LAZYAGENT_LIBRARY wins)", got, resolved)
	}
}

// On first launch after upgrade, an existing ~/.lazyagent/store
// must be renamed to ~/.lazyagent/library so users keep their
// previously-shared items. We point HOME at a temp dir, drop a
// fake legacy store with one item, run Init, and assert the
// rename happened. Env vars are explicitly cleared so legacyRoot
// kicks in.
func TestInitMigratesLegacyStoreDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", "")
	t.Setenv("LAZYAGENT_STORE", "")

	legacy := filepath.Join(home, ".lazyagent", "store")
	if err := os.MkdirAll(filepath.Join(legacy, "skills", "echo"), 0o755); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	marker := filepath.Join(legacy, "skills", "echo", "SKILL.md")
	if err := os.WriteFile(marker, []byte("legacy"), 0o644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}

	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	newRoot := filepath.Join(home, ".lazyagent", "library")
	if _, err := os.Stat(filepath.Join(newRoot, "skills", "echo", "SKILL.md")); err != nil {
		t.Errorf("migrated marker missing at new root: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy dir should be gone, stat err = %v", err)
	}
}

// If the user already has a library/ dir (e.g. they launched once
// before adding content under store/), Init must not clobber it —
// the migration is strictly "rename when target absent".
func TestInitSkipsMigrationWhenLibraryExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", "")
	t.Setenv("LAZYAGENT_STORE", "")

	legacy := filepath.Join(home, ".lazyagent", "store")
	if err := os.MkdirAll(filepath.Join(legacy, "skills", "old"), 0o755); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	newRoot := filepath.Join(home, ".lazyagent", "library")
	if err := os.MkdirAll(filepath.Join(newRoot, "skills", "new"), 0o755); err != nil {
		t.Fatalf("seed library: %v", err)
	}

	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := os.Stat(filepath.Join(newRoot, "skills", "new")); err != nil {
		t.Errorf("existing library content lost: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "skills", "old")); err != nil {
		t.Errorf("legacy preserved when migration is skipped (sanity); err = %v", err)
	}
}

// Env-overridden roots must not trigger the legacy migration — the
// user is in control of that path and we don't touch ~/.lazyagent/store
// in their stead.
func TestInitDoesNotMigrateWhenEnvOverridden(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacy := filepath.Join(home, ".lazyagent", "store")
	if err := os.MkdirAll(filepath.Join(legacy, "skills", "echo"), 0o755); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	custom := t.TempDir()
	t.Setenv("LAZYAGENT_LIBRARY", custom)

	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "skills", "echo")); err != nil {
		t.Errorf("legacy was touched despite env override: %v", err)
	}
}

func TestInitCreatesKindDirs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LAZYAGENT_STORE", tmp)
	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !Initialised() {
		t.Error("Initialised should be true after Init")
	}
	for _, sub := range []string{"skills", "agents", "mcp", "prompts", "memory"} {
		if _, err := statHelper(filepath.Join(tmp, sub)); err != nil {
			t.Errorf("missing kind dir %s: %v", sub, err)
		}
	}
}

func TestItemDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LAZYAGENT_STORE", tmp)
	got, err := ItemDir(model.KindSkill, "echo")
	if err != nil {
		t.Fatalf("ItemDir: %v", err)
	}
	// Root() resolves symlinks (macOS /var → /private/var), so do the
	// same to the expected value before comparing or this test fails on
	// CI runners whose TempDir lives behind a symlink.
	resolvedTmp, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	want := filepath.Join(resolvedTmp, "skills", "echo")
	if got != want {
		t.Errorf("ItemDir = %q, want %q", got, want)
	}
	if _, err := ItemDir(model.KindSession, "x"); err == nil {
		t.Error("ItemDir on KindSession should error")
	}
}

func TestManifestRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LAZYAGENT_LIBRARY", tmp)
	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	dir, _ := ItemDir(model.KindSkill, "echo")
	m := Manifest{
		Name:        "echo",
		Kind:        "Skill",
		Version:     "1.0",
		ProjectedTo: []string{"claude", "gemini"},
	}
	if err := WriteManifest(ManifestPath(dir), m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	got, err := ReadManifest(ManifestPath(dir))
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if got.Name != m.Name || got.Kind != m.Kind || got.Version != m.Version {
		t.Errorf("round-trip drift: got %+v, want %+v", got, m)
	}
	if len(got.ProjectedTo) != 2 || got.ProjectedTo[1] != "gemini" {
		t.Errorf("ProjectedTo = %v", got.ProjectedTo)
	}
	if len(got.SharedTo) != 0 {
		t.Errorf("SharedTo should be empty after read (legacy field cleared); got %v", got.SharedTo)
	}

	// On-disk form should not carry the legacy field — only `projected_to`.
	raw, err := os.ReadFile(ManifestPath(dir))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(raw), "shared_to") {
		t.Errorf("manifest should not emit shared_to anymore; got:\n%s", raw)
	}
	if !strings.Contains(string(raw), "projected_to") {
		t.Errorf("manifest should emit projected_to; got:\n%s", raw)
	}
}

// Old manifests (pre-PRI-65) used `shared_to`; ReadManifest must still
// surface the values via ProjectedTo so library items written by
// previous versions stay visible after upgrade.
func TestManifestLegacySharedToReadsAsProjectedTo(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LAZYAGENT_LIBRARY", tmp)
	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	dir, _ := ItemDir(model.KindSkill, "echo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	legacy := "" +
		"name = \"echo\"\n" +
		"kind = \"Skill\"\n" +
		"shared_to = [\"claude\", \"codex\"]\n"
	if err := os.WriteFile(ManifestPath(dir), []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := ReadManifest(ManifestPath(dir))
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if len(got.ProjectedTo) != 2 || got.ProjectedTo[0] != "claude" || got.ProjectedTo[1] != "codex" {
		t.Errorf("legacy shared_to not folded into ProjectedTo: %+v", got)
	}
	if len(got.SharedTo) != 0 {
		t.Errorf("SharedTo should be cleared after read; got %v", got.SharedTo)
	}
	// Re-writing should drop the legacy spelling on disk.
	if err := WriteManifest(ManifestPath(dir), got); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	raw, err := os.ReadFile(ManifestPath(dir))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(raw), "shared_to") {
		t.Errorf("re-written manifest still has shared_to:\n%s", raw)
	}
}

func TestListItems_EmptyStore(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LAZYAGENT_STORE", tmp)
	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	got, err := ListItems()
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for k, v := range got {
		if len(v) > 0 {
			t.Errorf("kind %v should be empty, got %v", k, v)
		}
	}
}

func TestListItems_PicksUpManifest(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LAZYAGENT_STORE", tmp)
	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	dir, _ := ItemDir(model.KindSkill, "echo")
	if err := WriteManifest(ManifestPath(dir), Manifest{Name: "echo", Kind: "Skill"}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	got, err := ListItems()
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	skills := got[model.KindSkill]
	if len(skills) != 1 || skills[0].Manifest.Name != "echo" {
		t.Errorf("skills = %+v", skills)
	}
}

