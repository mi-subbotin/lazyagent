package guardrails

import (
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestDriftedSharedItems_Empty_Allow(t *testing.T) {
	r := DriftedSharedItems{}.Evaluate(EvalContext{})
	if r.Action != ActionAllow {
		t.Fatalf("expected allow on empty plan, got %v (msg=%q)", r.Action, r.Message)
	}
}

func TestDriftedSharedItems_NoDrift_Allow(t *testing.T) {
	// A plain global skill that's already canonical and not drifted —
	// SyncAll classifies as Project/Skip, never Resync.
	items := []model.Item{
		{
			Kind:    model.KindSkill,
			Origin:  model.OriginClaude,
			Scope:   model.ScopeGlobal,
			Name:    "demo",
			Storage: model.StorageDir,
			Shared:  true,
			Drift:   false,
		},
	}
	r := DriftedSharedItems{}.Evaluate(EvalContext{Items: items})
	if r.Action != ActionAllow {
		t.Fatalf("expected allow without drift, got %v (msg=%q)", r.Action, r.Message)
	}
}

func TestDriftedSharedItems_Drifted_Warn(t *testing.T) {
	items := []model.Item{
		{
			Kind:    model.KindSkill,
			Origin:  model.OriginClaude,
			Scope:   model.ScopeGlobal,
			Name:    "drifted",
			Storage: model.StorageDir,
			Shared:  true,
			Drift:   true,
		},
	}
	r := DriftedSharedItems{}.Evaluate(EvalContext{Items: items})
	if r.Action != ActionWarn {
		t.Fatalf("expected warn on drifted item, got %v", r.Action)
	}
	if !strings.Contains(r.Message, "1 shared item") {
		t.Errorf("message missing count: %q", r.Message)
	}
	if !strings.Contains(r.Message, "library sync") {
		t.Errorf("message missing remediation hint: %q", r.Message)
	}
}
