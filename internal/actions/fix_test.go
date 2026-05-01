package actions

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

func TestFix_FrontmatterMergesSpilledDescription(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "skills", "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	in := "---\nname: demo\ndescription: This is line one.\nIt continues here.\nAnd a third line.\n---\nbody contents\n"
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}

	it := model.Item{
		Origin:     model.OriginClaude,
		Kind:       model.KindSkill,
		Name:       "demo",
		Path:       path,
		Storage:    model.StorageDir,
		ParseError: "line 4: expected `key: value`, got \"It continues here.\"",
	}
	plan, err := Fix(it)
	if err != nil {
		t.Fatalf("Fix returned error: %v", err)
	}
	if plan.Empty() {
		t.Fatal("plan should not be empty")
	}

	if err := ApplyFix(plan); err != nil {
		t.Fatalf("ApplyFix: %v", err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fm := parse.Parse(string(out))
	if len(fm.Errors) != 0 {
		t.Fatalf("rewritten frontmatter still has errors: %v\n---\n%s", fm.Errors, out)
	}
	want := "This is line one. It continues here. And a third line."
	if fm.Fields["description"] != want {
		t.Fatalf("description merge mismatch:\n  got %q\n want %q", fm.Fields["description"], want)
	}
	if fm.Fields["name"] != "demo" {
		t.Fatalf("name lost: %q", fm.Fields["name"])
	}
	if !strings.Contains(string(out), "body contents") {
		t.Fatalf("body lost:\n%s", out)
	}
}

func TestFix_FrontmatterQuotesValuesWithColons(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.md")
	in := "---\nname: a\ndescription: Useful when X happens.\nThen do: Y.\n---\nbody\n"
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}

	it := model.Item{
		Origin:     model.OriginClaude,
		Kind:       model.KindAgent,
		Name:       "a",
		Path:       path,
		Storage:    model.StorageFile,
		ParseError: "spillover",
	}
	plan, err := Fix(it)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if err := ApplyFix(plan); err != nil {
		t.Fatalf("ApplyFix: %v", err)
	}
	out, _ := os.ReadFile(path)
	fm := parse.Parse(string(out))
	if len(fm.Errors) != 0 {
		t.Fatalf("expected clean parse, got %v\n---\n%s", fm.Errors, out)
	}
	if !strings.Contains(fm.Fields["description"], "Then do: Y.") {
		t.Fatalf("colon-bearing line not preserved: %q", fm.Fields["description"])
	}
}

func TestFix_FrontmatterFillsMissingFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "skills", "lonely", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Frontmatter exists but is missing name and description entirely.
	in := "---\nmodel: opus\n---\nThis skill helps you do things.\n"
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin:     model.OriginClaude,
		Kind:       model.KindSkill,
		Path:       path,
		Storage:    model.StorageDir,
		ParseError: "missing-fields",
	}
	plan, err := Fix(it)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if err := ApplyFix(plan); err != nil {
		t.Fatalf("ApplyFix: %v", err)
	}
	out, _ := os.ReadFile(path)
	fm := parse.Parse(string(out))
	if fm.Fields["name"] != "lonely" {
		t.Fatalf("name not derived from dir: %q", fm.Fields["name"])
	}
	if fm.Fields["description"] == "" {
		t.Fatal("description not derived from body")
	}
	if fm.Fields["model"] != "opus" {
		t.Fatalf("existing field dropped: %v", fm.Fields)
	}
}

func TestFix_HookAddsMissingType(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	in := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{
							"command": "echo hi",
						},
					},
				},
			},
		},
	}
	writeJSON(t, path, in)

	it := model.Item{
		Origin:     model.OriginClaude,
		Kind:       model.KindHook,
		Name:       "PreToolUse:Bash",
		Path:       path,
		Storage:    model.StorageEntry,
		ConfigKey:  "hooks/PreToolUse/0/hooks/0",
		ParseError: "missing type",
	}
	plan, err := Fix(it)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if err := ApplyFix(plan); err != nil {
		t.Fatalf("ApplyFix: %v", err)
	}
	val, _, err := parse.ReadEntry(path, it.ConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	inner := val.(map[string]any)
	if inner["type"] != "command" {
		t.Fatalf("type not added: %v", inner)
	}
	if inner["command"] != "echo hi" {
		t.Fatalf("command mutated: %v", inner)
	}
}

func TestFix_HookDropsBadTimeout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	in := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "echo hi",
							"timeout": "five",
						},
					},
				},
			},
		},
	}
	writeJSON(t, path, in)

	it := model.Item{
		Origin:     model.OriginClaude,
		Kind:       model.KindHook,
		Name:       "PreToolUse",
		Path:       path,
		Storage:    model.StorageEntry,
		ConfigKey:  "hooks/PreToolUse/0/hooks/0",
		ParseError: "timeout must be a number",
	}
	plan, err := Fix(it)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if err := ApplyFix(plan); err != nil {
		t.Fatalf("ApplyFix: %v", err)
	}
	val, _, err := parse.ReadEntry(path, it.ConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	inner := val.(map[string]any)
	if _, ok := inner["timeout"]; ok {
		t.Fatalf("timeout not dropped: %v", inner)
	}
}

func TestFix_HookEmptyCommandUnfixable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	in := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"command": "",
						},
					},
				},
			},
		},
	}
	writeJSON(t, path, in)
	it := model.Item{
		Origin:     model.OriginClaude,
		Kind:       model.KindHook,
		Name:       "PreToolUse",
		Path:       path,
		Storage:    model.StorageEntry,
		ConfigKey:  "hooks/PreToolUse/0/hooks/0",
		ParseError: "missing or empty command; missing type",
	}
	_, err := Fix(it)
	if err == nil {
		t.Fatal("expected ErrUnfixable for empty command")
	}
	if !errors.Is(err, ErrUnfixable) {
		t.Fatalf("want ErrUnfixable, got %v", err)
	}
}

func TestFix_NothingToFixWhenAlreadyValid(t *testing.T) {
	t.Parallel()
	it := model.Item{Name: "fine"}
	_, err := Fix(it)
	if !errors.Is(err, ErrNothingToFix) {
		t.Fatalf("want ErrNothingToFix, got %v", err)
	}
}

func TestApplyFix_RollsBackWhenStillInvalid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.md")
	original := []byte("---\nname: a\n---\nbody\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	// Hand-crafted plan whose After bytes still trigger a parse error.
	plan := FixPlan{
		Item:   model.Item{Kind: model.KindAgent, Path: path},
		Path:   path,
		Before: original,
		After:  []byte("---\nbroken line\n---\nbody\n"),
	}
	err := ApplyFix(plan)
	if err == nil {
		t.Fatal("expected re-validation failure")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Fatalf("rollback failed:\ngot  %q\nwant %q", got, original)
	}
}

func writeJSON(t *testing.T, path string, data any) {
	t.Helper()
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
