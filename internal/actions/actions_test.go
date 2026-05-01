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

func TestCopy_StorageFile_GlobalToLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Place a Claude global agent
	globalDir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	src := filepath.Join(globalDir, "echo.md")
	if err := os.WriteFile(src, []byte("body"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	it := model.Item{
		Origin:  model.OriginClaude,
		Kind:    model.KindAgent,
		Scope:   model.ScopeGlobal,
		Storage: model.StorageFile,
		Path:    src,
		Name:    "echo",
	}

	if err := Copy(it, project); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	dst := filepath.Join(project, ".claude", "agents", "echo.md")
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("dst missing: %v", err)
	}
}

func TestCopy_RefusesOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, "proj")
	for _, p := range []string{
		filepath.Join(home, ".claude", "agents", "echo.md"),
		filepath.Join(project, ".claude", "agents", "echo.md"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	it := model.Item{
		Origin:  model.OriginClaude,
		Kind:    model.KindAgent,
		Scope:   model.ScopeGlobal,
		Storage: model.StorageFile,
		Path:    filepath.Join(home, ".claude", "agents", "echo.md"),
		Name:    "echo",
	}
	err := Copy(it, project)
	if !errors.Is(err, ErrTargetExists) {
		t.Errorf("err = %v, want ErrTargetExists", err)
	}
}

func TestMove_CopyThenDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, "proj")
	src := filepath.Join(home, ".claude", "agents", "echo.md")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin:  model.OriginClaude,
		Kind:    model.KindAgent,
		Scope:   model.ScopeGlobal,
		Storage: model.StorageFile,
		Path:    src,
		Name:    "echo",
	}
	if err := Move(it, project); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("src still exists after Move")
	}
	dst := filepath.Join(project, ".claude", "agents", "echo.md")
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("dst missing: %v", err)
	}
}

func TestCopy_LocalNeedsProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	it := model.Item{
		Origin:  model.OriginClaude,
		Kind:    model.KindAgent,
		Scope:   model.ScopeGlobal,
		Storage: model.StorageFile,
		Path:    "/tmp/echo.md",
		Name:    "echo",
	}
	err := Copy(it, "")
	if !errors.Is(err, ErrNoProject) {
		t.Errorf("err = %v, want ErrNoProject", err)
	}
}
