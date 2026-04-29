package actions

import (
	"errors"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestResumeCommandClaude(t *testing.T) {
	it := model.Item{
		Origin:    model.OriginClaude,
		Kind:      model.KindSession,
		ConfigKey: "abc-123",
	}
	cmd, err := ResumeCommand(it, "")
	if err != nil {
		t.Fatalf("ResumeCommand: %v", err)
	}
	if !strings.HasSuffix(cmd.Path, "claude") {
		t.Errorf("expected claude binary, got %q", cmd.Path)
	}
	if got := cmd.Args[1:]; len(got) != 2 || got[0] != "-r" || got[1] != "abc-123" {
		t.Errorf("expected [-r abc-123], got %v", got)
	}
	if cmd.Dir != "" {
		t.Errorf("claude should not pin Cmd.Dir, got %q", cmd.Dir)
	}
}

func TestResumeCommandGeminiLocal(t *testing.T) {
	it := model.Item{
		Origin: model.OriginGemini,
		Kind:   model.KindSession,
		Scope:  model.ScopeLocal,
		Meta:   map[string]string{"index": "3"},
	}
	cmd, err := ResumeCommand(it, "/Users/foo/proj")
	if err != nil {
		t.Fatalf("ResumeCommand: %v", err)
	}
	if got := cmd.Args[1:]; len(got) != 2 || got[0] != "--resume" || got[1] != "3" {
		t.Errorf("expected [--resume 3], got %v", got)
	}
	if cmd.Dir != "/Users/foo/proj" {
		t.Errorf("expected Cmd.Dir=projectDir, got %q", cmd.Dir)
	}
}

func TestResumeCommandGeminiNonLocalRejected(t *testing.T) {
	cases := []struct {
		name string
		it   model.Item
		dir  string
	}{
		{
			name: "global scope",
			it:   model.Item{Origin: model.OriginGemini, Kind: model.KindSession, Scope: model.ScopeGlobal, Meta: map[string]string{"index": "1"}},
			dir:  "/some/proj",
		},
		{
			name: "no project dir",
			it:   model.Item{Origin: model.OriginGemini, Kind: model.KindSession, Scope: model.ScopeLocal, Meta: map[string]string{"index": "1"}},
			dir:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResumeCommand(c.it, c.dir)
			if !errors.Is(err, ErrResumeUnsupported) {
				t.Errorf("expected ErrResumeUnsupported, got %v", err)
			}
		})
	}
}

func TestResumeCommandCodexUnsupported(t *testing.T) {
	it := model.Item{Origin: model.OriginCodex, Kind: model.KindSession}
	if _, err := ResumeCommand(it, ""); !errors.Is(err, ErrResumeUnsupported) {
		t.Errorf("codex must return ErrResumeUnsupported, got %v", err)
	}
}

func TestResumeCommandNotASession(t *testing.T) {
	it := model.Item{Origin: model.OriginClaude, Kind: model.KindSkill}
	if _, err := ResumeCommand(it, ""); !errors.Is(err, ErrResumeUnsupported) {
		t.Errorf("non-session must return ErrResumeUnsupported, got %v", err)
	}
}
