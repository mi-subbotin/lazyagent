package actions

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

func TestConvertSkillToAgent_HappyPath(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	body := "---\n" +
		"name: greeter\n" +
		"description: Greets the user politely.\n" +
		"---\n" +
		"Hello there!\n"
	bodyPath := stageClaudeSkill(t, home, "greeter", body)
	it := skillItem("greeter", bodyPath, model.OriginClaude, model.ScopeGlobal)

	out, err := ConvertSkillToAgent(it)
	if err != nil {
		t.Fatalf("ConvertSkillToAgent: %v", err)
	}

	wantPath := filepath.Join(lib, "agents", "greeter", "agent.md")
	if out.Path != wantPath {
		t.Errorf("Path = %s, want %s", out.Path, wantPath)
	}
	if out.Origin != model.OriginShared || out.Kind != model.KindAgent || out.Storage != model.StorageFile {
		t.Errorf("returned item shape wrong: %+v", out)
	}

	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read agent body: %v", err)
	}
	fm := parse.Parse(string(got))
	if fm.Fields["name"] != "greeter" {
		t.Errorf("name field = %q, want %q", fm.Fields["name"], "greeter")
	}
	if fm.Fields["description"] != "Greets the user politely." {
		t.Errorf("description field = %q, want %q", fm.Fields["description"], "Greets the user politely.")
	}
	if fm.Fields["model"] != "inherit" {
		t.Errorf("model field = %q, want %q", fm.Fields["model"], "inherit")
	}
	if !strings.Contains(fm.Body, "Hello there!") {
		t.Errorf("body missing source content; got %q", fm.Body)
	}

	// Manifest sits beside the body so the lazyagent source adapter
	// surfaces the new agent on next reload.
	manifestPath := filepath.Join(lib, "agents", "greeter", "manifest.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("manifest missing: %v", err)
	}
}

func TestConvertSkillToAgent_RejectsNonSkill(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	it := model.Item{
		Origin:  model.OriginClaude,
		Kind:    model.KindAgent,
		Scope:   model.ScopeGlobal,
		Name:    "some-agent",
		Path:    filepath.Join(home, ".claude", "agents", "some-agent.md"),
		Storage: model.StorageFile,
	}
	if _, err := ConvertSkillToAgent(it); !errors.Is(err, ErrPlaceUnsupported) {
		t.Fatalf("err = %v, want ErrPlaceUnsupported", err)
	}
}

func TestConvertSkillToAgent_RefusesOverwrite(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	bodyPath := stageClaudeSkill(t, home, "dup", "---\nname: dup\n---\nbody\n")
	it := skillItem("dup", bodyPath, model.OriginClaude, model.ScopeGlobal)

	// Pre-create the target so the second convert collides.
	preExisting := filepath.Join(lib, "agents", "dup")
	if err := os.MkdirAll(preExisting, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := ConvertSkillToAgent(it)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want already-exists error", err)
	}
}
