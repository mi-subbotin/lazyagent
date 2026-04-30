package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func plant(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInspect_FullRepo(t *testing.T) {
	cache := t.TempDir()
	plant(t, cache, "skills/cool/SKILL.md", "---\nname: cool\ndescription: nice skill\n---\nbody\n")
	plant(t, cache, "skills/cool/asset.txt", "asset")
	plant(t, cache, "agents/reviewer.md", "---\nname: reviewer\ndescription: code review\n---\nbody\n")
	plant(t, cache, "commands/deploy.md", "---\ndescription: deploy helper\n---\nbody\n")
	plant(t, cache, "README.md", "ignored")
	plant(t, cache, ".lazyagent-ok", "")

	got, err := Inspect(cache, &Spec{Kind: SpecKindRepo})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3: %+v", len(got), got)
	}

	byName := map[string]Candidate{}
	for _, c := range got {
		byName[c.Name] = c
	}

	skill, ok := byName["cool"]
	if !ok || skill.Kind != model.KindSkill || skill.Storage != model.StorageDir {
		t.Errorf("cool skill: got %+v, want KindSkill+StorageDir", skill)
	}
	if skill.Description != "nice skill" {
		t.Errorf("cool description = %q", skill.Description)
	}

	agent, ok := byName["reviewer"]
	if !ok || agent.Kind != model.KindAgent || agent.Storage != model.StorageFile {
		t.Errorf("reviewer agent: got %+v", agent)
	}

	// The deploy command has no `name:` in frontmatter — Inspect must
	// fall back to the filename (without extension).
	prompt, ok := byName["deploy"]
	if !ok || prompt.Kind != model.KindPrompt || prompt.Storage != model.StorageFile {
		t.Errorf("deploy prompt: got %+v", prompt)
	}
}

func TestInspect_Subtree(t *testing.T) {
	cache := t.TempDir()
	plant(t, cache, "library/skills/alpha/SKILL.md", "---\nname: alpha\n---\nbody\n")
	plant(t, cache, "library/skills/beta/SKILL.md", "---\nname: beta\n---\nbody\n")
	plant(t, cache, "other/agents/x.md", "---\nname: x\n---\n")

	got, err := Inspect(cache, &Spec{Kind: SpecKindSubtree, Path: "library/skills"})
	if err != nil {
		t.Fatal(err)
	}
	// Subtree narrows the walk: the agent under other/ must not appear.
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 (alpha+beta): %+v", len(got), got)
	}
	for _, c := range got {
		if c.Kind != model.KindSkill {
			t.Errorf("expected only skills, got %s for %s", c.Kind, c.Name)
		}
	}
}

func TestInspect_SingleFile(t *testing.T) {
	cache := t.TempDir()
	plant(t, cache, "deep/skills/solo/SKILL.md", "---\nname: solo\ndescription: single\n---\nbody\n")

	got, err := Inspect(cache, &Spec{Kind: SpecKindFile, Path: "deep/skills/solo/SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Kind != model.KindSkill || got[0].Name != "solo" {
		t.Errorf("got %+v", got[0])
	}
}

func TestInspect_BrokenFrontmatter(t *testing.T) {
	cache := t.TempDir()
	// Unterminated frontmatter — file should still surface, with a
	// ParseError set so the picker overlay can show the warning.
	plant(t, cache, "skills/broken/SKILL.md", "---\nname: broken\nno colon\n")

	got, err := Inspect(cache, &Spec{Kind: SpecKindRepo})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].ParseError == "" {
		t.Errorf("ParseError empty, want diagnostic for broken/SKILL.md")
	}
}
