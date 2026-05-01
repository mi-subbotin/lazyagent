package actions

import (
	"errors"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestSupportsCross_Matrix(t *testing.T) {
	cases := []struct {
		name string
		it   model.Item
		tgt  model.Origin
		want bool
	}{
		{"same origin refused", model.Item{Origin: model.OriginClaude, Kind: model.KindSkill}, model.OriginClaude, false},
		{"skill claude→codex", model.Item{Origin: model.OriginClaude, Kind: model.KindSkill}, model.OriginCodex, true},
		{"skill claude→gemini", model.Item{Origin: model.OriginClaude, Kind: model.KindSkill}, model.OriginGemini, true},
		{"mcp gemini→claude", model.Item{Origin: model.OriginGemini, Kind: model.KindMCP}, model.OriginClaude, true},
		{"agent codex→gemini", model.Item{Origin: model.OriginCodex, Kind: model.KindAgent}, model.OriginGemini, true},
		{"prompt claude→gemini", model.Item{Origin: model.OriginClaude, Kind: model.KindPrompt}, model.OriginGemini, true},
		{"memory claude→codex", model.Item{Origin: model.OriginClaude, Kind: model.KindMemory}, model.OriginCodex, true},
		{"hook unsupported", model.Item{Origin: model.OriginClaude, Kind: model.KindHook}, model.OriginCodex, false},
		{"session unsupported", model.Item{Origin: model.OriginClaude, Kind: model.KindSession}, model.OriginCodex, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SupportsCross(tc.it, tc.tgt); got != tc.want {
				t.Errorf("SupportsCross = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsLossyCross_AgentAndPrompt(t *testing.T) {
	cases := []struct {
		name string
		it   model.Item
		tgt  model.Origin
		want bool
	}{
		{"agent claude→gemini lossless", model.Item{Origin: model.OriginClaude, Kind: model.KindAgent}, model.OriginGemini, false},
		{"agent claude→codex lossy", model.Item{Origin: model.OriginClaude, Kind: model.KindAgent}, model.OriginCodex, true},
		{"agent codex→gemini lossy", model.Item{Origin: model.OriginCodex, Kind: model.KindAgent}, model.OriginGemini, true},
		{"prompt claude→codex lossless", model.Item{Origin: model.OriginClaude, Kind: model.KindPrompt}, model.OriginCodex, false},
		{"prompt claude→gemini lossy", model.Item{Origin: model.OriginClaude, Kind: model.KindPrompt}, model.OriginGemini, true},
		{"prompt gemini→codex lossy", model.Item{Origin: model.OriginGemini, Kind: model.KindPrompt}, model.OriginCodex, true},
		{"skill any pair lossless", model.Item{Origin: model.OriginClaude, Kind: model.KindSkill}, model.OriginGemini, false},
		{"unsupported never lossy", model.Item{Origin: model.OriginClaude, Kind: model.KindHook}, model.OriginCodex, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLossyCross(tc.it, tc.tgt); got != tc.want {
				t.Errorf("IsLossyCross = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCrossCopy_RefusesSameOrigin(t *testing.T) {
	it := model.Item{Origin: model.OriginClaude, Kind: model.KindSkill, Path: "/tmp/x", Name: "x"}
	err := CrossCopy(it, model.OriginClaude, model.ScopeGlobal, "")
	if err == nil {
		t.Fatal("CrossCopy to same origin should error")
	}
}

func TestCrossCopy_LocalNeedsProject(t *testing.T) {
	it := model.Item{Origin: model.OriginClaude, Kind: model.KindSkill, Path: "/tmp/x/SKILL.md", Name: "x"}
	err := CrossCopy(it, model.OriginCodex, model.ScopeLocal, "")
	if !errors.Is(err, ErrNoProject) {
		t.Errorf("err = %v, want ErrNoProject", err)
	}
}

func TestCrossCopy_UnsupportedKind(t *testing.T) {
	// Sessions are intentionally unsupported by CrossCopy. The dispatch
	// switch falls through to ErrUnsupported.
	it := model.Item{Origin: model.OriginClaude, Kind: model.KindSession, Path: "/tmp/s.jsonl"}
	err := CrossCopy(it, model.OriginGemini, model.ScopeGlobal, "")
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}
