package actions

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello World", "hello-world"},
		{"  whitespace  ", "whitespace"},
		{"camelCase", "camelcase"},
		{"---weird___edges---", "weird___edges"},
		{"!@#$%", ""},
		{"emoji 🚀 rocket", "emoji-rocket"},
		{"with.dots.in.name", "with-dots-in-name"},
	}
	for _, tc := range cases {
		if got := Slugify(tc.in); got != tc.want {
			t.Errorf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCreate_RefusesEmptySlug(t *testing.T) {
	_, err := Create(model.OriginClaude, model.KindSkill, model.ScopeGlobal, "!!!", "")
	if err == nil {
		t.Fatal("expected error on empty slug")
	}
}

func TestCreate_LocalNeedsProject(t *testing.T) {
	_, err := Create(model.OriginClaude, model.KindSkill, model.ScopeLocal, "echo", "")
	if !errors.Is(err, ErrNoProject) {
		t.Errorf("err = %v, want ErrNoProject", err)
	}
}

func TestCreate_SkillScaffold(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := Create(model.OriginClaude, model.KindSkill, model.ScopeGlobal, "Echo Demo", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join("echo-demo", "SKILL.md")) {
		t.Errorf("path = %q, want suffix echo-demo/SKILL.md", got)
	}
	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "Echo Demo") {
		t.Errorf("body missing display name; body = %s", body)
	}
}

func TestCreate_RefusesExisting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := Create(model.OriginClaude, model.KindSkill, model.ScopeGlobal, "echo", ""); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := Create(model.OriginClaude, model.KindSkill, model.ScopeGlobal, "echo", "")
	if !errors.Is(err, ErrTargetExists) {
		t.Errorf("err = %v, want ErrTargetExists", err)
	}
}

func TestCreate_UnsupportedKind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := Create(model.OriginClaude, model.KindMCP, model.ScopeGlobal, "x", "")
	if !errors.Is(err, ErrCreateUnsupported) {
		t.Errorf("err = %v, want ErrCreateUnsupported", err)
	}
}

func TestCreate_AgentClaude(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := Create(model.OriginClaude, model.KindAgent, model.ScopeGlobal, "Reviewer", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasSuffix(got, "reviewer.md") {
		t.Errorf("path = %q, want suffix reviewer.md", got)
	}
}
