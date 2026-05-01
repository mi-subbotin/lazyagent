package store

import (
	"os"
	"path/filepath"
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
	t.Setenv("LAZYAGENT_STORE", tmp)
	got, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if filepath.Base(got) != filepath.Base(tmp) {
		t.Errorf("Root = %q, want under %q", got, tmp)
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
	t.Setenv("LAZYAGENT_STORE", tmp)
	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	dir, _ := ItemDir(model.KindSkill, "echo")
	m := Manifest{
		Name:     "echo",
		Kind:     "Skill",
		Version:  "1.0",
		SharedTo: []string{"claude", "gemini"},
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
	if len(got.SharedTo) != 2 || got.SharedTo[1] != "gemini" {
		t.Errorf("SharedTo = %v", got.SharedTo)
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

