package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestTarget_Validate(t *testing.T) {
	cases := []struct {
		name    string
		t       Target
		wantErr string
	}{
		{name: "ok claude global", t: Target{Origin: "claude", Scope: "global"}},
		{name: "default scope", t: Target{Origin: "Claude", Scope: ""}},
		{name: "local needs project", t: Target{Origin: "claude", Scope: "local"}, wantErr: "project directory"},
		{name: "shared local rejected", t: Target{Origin: "shared", Scope: "local", ProjectDir: "/tmp"}, wantErr: "shared store"},
		{name: "unknown origin", t: Target{Origin: "openai"}, wantErr: "unknown target origin"},
		{name: "missing origin", t: Target{}, wantErr: "target origin not set"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.t.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestResolvePath(t *testing.T) {
	t.Setenv("HOME", "/home/user")
	cases := []struct {
		name string
		c    Candidate
		t    Target
		want string
	}{
		{
			name: "claude skill global",
			c:    Candidate{Kind: model.KindSkill, Name: "cool", Storage: model.StorageDir},
			t:    Target{Origin: "claude", Scope: "global"},
			want: "/home/user/.claude/skills/cool/SKILL.md",
		},
		{
			name: "claude agent local",
			c:    Candidate{Kind: model.KindAgent, Name: "rev", Storage: model.StorageFile},
			t:    Target{Origin: "claude", Scope: "local", ProjectDir: "/proj"},
			want: "/proj/.claude/agents/rev.md",
		},
		{
			name: "codex skill goes to ~/.agents",
			c:    Candidate{Kind: model.KindSkill, Name: "x", Storage: model.StorageDir},
			t:    Target{Origin: "codex", Scope: "global"},
			want: "/home/user/.agents/skills/x/SKILL.md",
		},
		{
			name: "codex prompt",
			c:    Candidate{Kind: model.KindPrompt, Name: "deploy", Storage: model.StorageFile},
			t:    Target{Origin: "codex", Scope: "global"},
			want: "/home/user/.codex/prompts/deploy.md",
		},
		{
			name: "gemini agent",
			c:    Candidate{Kind: model.KindAgent, Name: "rev", Storage: model.StorageFile},
			t:    Target{Origin: "gemini", Scope: "global"},
			want: "/home/user/.gemini/agents/rev.md",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.t.Validate(); err != nil {
				t.Fatal(err)
			}
			got, err := ResolvePath(tt.c, tt.t)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolvePath_Rejects(t *testing.T) {
	t.Setenv("HOME", "/home/user")
	cases := []struct {
		name string
		c    Candidate
		t    Target
		want string
	}{
		{
			name: "codex agent rejected",
			c:    Candidate{Kind: model.KindAgent, Name: "x", Storage: model.StorageFile},
			t:    Target{Origin: "codex", Scope: "global"},
			want: "no per-agent layout",
		},
		{
			name: "gemini prompt rejected (TOML mismatch)",
			c:    Candidate{Kind: model.KindPrompt, Name: "x", Storage: model.StorageFile},
			t:    Target{Origin: "gemini", Scope: "global"},
			want: "TOML",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.t.Validate(); err != nil {
				t.Fatal(err)
			}
			_, err := ResolvePath(tt.c, tt.t)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestApply_FileAndDir(t *testing.T) {
	cache := t.TempDir()
	plant(t, cache, "skills/cool/SKILL.md", "---\nname: cool\n---\nbody\n")
	plant(t, cache, "skills/cool/asset.txt", "asset")
	plant(t, cache, "agents/rev.md", "---\nname: rev\n---\nbody\n")

	home := t.TempDir()
	t.Setenv("HOME", home)

	tg := Target{Origin: "claude", Scope: "global"}
	if err := tg.Validate(); err != nil {
		t.Fatal(err)
	}

	skill := Candidate{Kind: model.KindSkill, Name: "cool", Storage: model.StorageDir, SourceRel: "skills/cool/SKILL.md"}
	in, err := Apply(cache, skill, tg, "github.com/foo/bar", "abc123", ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply skill: %v", err)
	}
	wantSkill := filepath.Join(home, ".claude", "skills", "cool", "SKILL.md")
	if in.TargetPath != wantSkill {
		t.Errorf("skill TargetPath = %q, want %q", in.TargetPath, wantSkill)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "cool", "asset.txt")); err != nil {
		t.Errorf("asset.txt not copied alongside SKILL.md: %v", err)
	}

	agent := Candidate{Kind: model.KindAgent, Name: "rev", Storage: model.StorageFile, SourceRel: "agents/rev.md"}
	in, err = Apply(cache, agent, tg, "github.com/foo/bar", "abc123", ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply agent: %v", err)
	}
	wantAgent := filepath.Join(home, ".claude", "agents", "rev.md")
	if in.TargetPath != wantAgent {
		t.Errorf("agent TargetPath = %q, want %q", in.TargetPath, wantAgent)
	}

	// Re-apply without overwrite returns ErrAlreadyExists.
	if _, err := Apply(cache, agent, tg, "github.com/foo/bar", "abc123", ApplyOptions{}); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("re-apply without overwrite: err = %v, want ErrAlreadyExists", err)
	}

	// With Overwrite=true the destination is replaced.
	plant(t, cache, "agents/rev.md", "---\nname: rev\n---\nNEW BODY\n")
	if _, err := Apply(cache, agent, tg, "github.com/foo/bar", "def456", ApplyOptions{Overwrite: true}); err != nil {
		t.Fatalf("Apply agent overwrite: %v", err)
	}
	body, err := os.ReadFile(wantAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "NEW BODY") {
		t.Errorf("overwrite did not replace bytes: %q", body)
	}
}

func TestUninstall(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, ".claude", "skills", "cool", "SKILL.md")
	plant(t, dir, ".claude/skills/cool/SKILL.md", "---\nname: cool\n---\n")
	plant(t, dir, ".claude/skills/cool/asset.txt", "asset")
	if err := Uninstall(Install{TargetPath: skillPath}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(skillPath)); !os.IsNotExist(err) {
		t.Errorf("skill dir not removed: err=%v", err)
	}

	// Uninstalling an already-missing path is a no-op.
	if err := Uninstall(Install{TargetPath: skillPath}); err != nil {
		t.Errorf("re-uninstall returned %v, want nil", err)
	}

	// Single-file uninstall removes just the file.
	agentPath := filepath.Join(dir, ".claude", "agents", "rev.md")
	plant(t, dir, ".claude/agents/rev.md", "body")
	if err := Uninstall(Install{TargetPath: agentPath}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agentPath); !os.IsNotExist(err) {
		t.Errorf("agent.md still present: %v", err)
	}
	// Parent dir must remain (other items may live there).
	if _, err := os.Stat(filepath.Dir(agentPath)); err != nil {
		t.Errorf("agents dir was removed by single-file uninstall: %v", err)
	}
}
