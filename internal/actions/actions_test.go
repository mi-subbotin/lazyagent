package actions

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestDelete_StorageFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.md")
	if err := os.WriteFile(path, []byte("body"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	it := model.Item{Origin: model.OriginClaude, Kind: model.KindAgent, Storage: model.StorageFile, Path: path}
	if err := Delete(it); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after Delete")
	}
}

func TestDelete_StorageDir(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("body"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	it := model.Item{Origin: model.OriginClaude, Kind: model.KindSkill, Storage: model.StorageDir, Path: filepath.Join(skillDir, "SKILL.md")}
	if err := Delete(it); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Errorf("skill dir still exists after Delete")
	}
}

func TestDelete_StorageEntry_Hook(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	body := `{
		"hooks": {
			"PreToolUse": [
				{"matcher": "Bash", "hooks": [
					{"command": "first"},
					{"command": "second"}
				]}
			]
		}
	}`
	if err := os.WriteFile(settings, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	it := model.Item{
		Origin:    model.OriginClaude,
		Kind:      model.KindHook,
		Storage:   model.StorageEntry,
		Path:      settings,
		ConfigKey: "hooks/PreToolUse/0/hooks/0",
	}
	if err := Delete(it); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, `"first"`) {
		t.Errorf("first hook should be gone:\n%s", s)
	}
	if !strings.Contains(s, `"second"`) {
		t.Errorf("second hook should remain:\n%s", s)
	}
}

// PRI-69: Place projects a Claude/Global hook entry to Claude/Local —
// equivalent of the legacy `c` (copy) on a hook with matcher
// preservation. Uses the canonical Claude settings.json layout for the
// source and verifies the local target file holds the picked inner
// hook plus its matcher, without dragging sibling hooks along.
func TestPlace_HookEntry_GlobalToLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}

	globalSettings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(globalSettings), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"command": "echo first"}, {"command": "echo second"}]}]}}`
	if err := os.WriteFile(globalSettings, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	it := model.Item{
		Origin:    model.OriginClaude,
		Kind:      model.KindHook,
		Scope:     model.ScopeGlobal,
		Storage:   model.StorageEntry,
		Path:      globalSettings,
		ConfigKey: "hooks/PreToolUse/0/hooks/1",
	}
	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeGlobal},
		{model.OriginClaude, model.ScopeLocal},
	}
	if err := Place(it, targets, PlaceOpts{ProjectDir: project}); err != nil {
		t.Fatalf("Place hook: %v", err)
	}

	target := filepath.Join(project, ".claude", "settings.json")
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, `"echo second"`) {
		t.Errorf("target should contain copied hook command:\n%s", s)
	}
	if strings.Contains(s, `"echo first"`) {
		t.Errorf("target should not contain sibling hook (only the selected one):\n%s", s)
	}
	if !strings.Contains(s, `"matcher"`) {
		t.Errorf("target should preserve matcher field:\n%s", s)
	}
}

// PRI-69: a Place that adds a hook to a target which already holds a
// different hook must append rather than overwrite — the legacy
// `c` semantics that placeHookProjection inherits via copyHookEntry.
func TestPlace_HookEntry_AppendsToExistingTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Source = local settings.json under a project; target = global
	project := filepath.Join(home, "proj")
	localSettings := filepath.Join(project, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(localSettings), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(localSettings, []byte(`{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"command": "added"}]}]}}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	globalSettings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(globalSettings), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(globalSettings, []byte(`{"hooks": {"PreToolUse": [{"matcher": "Read", "hooks": [{"command": "preexisting"}]}]}}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	it := model.Item{
		Origin:    model.OriginClaude,
		Kind:      model.KindHook,
		Scope:     model.ScopeLocal,
		Storage:   model.StorageEntry,
		Path:      localSettings,
		ConfigKey: "hooks/PreToolUse/0/hooks/0",
	}
	targets := []ProjectionTarget{
		{model.OriginClaude, model.ScopeLocal},
		{model.OriginClaude, model.ScopeGlobal},
	}
	if err := Place(it, targets, PlaceOpts{ProjectDir: project}); err != nil {
		t.Fatalf("Place hook: %v", err)
	}

	got, err := os.ReadFile(globalSettings)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `"added"`) {
		t.Errorf("target missing newly copied hook:\n%s", s)
	}
	if !strings.Contains(s, `"preexisting"`) {
		t.Errorf("target lost pre-existing hook:\n%s", s)
	}
}

// PRI-69: Place rejects local-scope targets without a project dir,
// preserving the legacy `c` ErrNoProject behaviour. Uses a minimal MCP
// item to exercise the entry path.
func TestPlace_LocalNeedsProject(t *testing.T) {
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
		Storage:   model.StorageEntry,
		Path:      srcPath,
		Name:      "fs",
		ConfigKey: "mcpServers/fs",
	}
	err := Place(it, []ProjectionTarget{{model.OriginClaude, model.ScopeLocal}}, PlaceOpts{})
	if !errors.Is(err, ErrNoProject) {
		t.Errorf("err = %v, want ErrNoProject", err)
	}
}
