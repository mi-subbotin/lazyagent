package guardrails

import (
	"strings"
	"testing"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func skillItems(n int) []model.Item {
	out := make([]model.Item, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, model.Item{
			Kind: model.KindSkill,
			Name: "skill-" + itoa(i),
		})
	}
	return out
}

// avoid pulling in strconv just for one usage
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

func TestTooManySkills_UnderThreshold_Allow(t *testing.T) {
	g := TooManySkills{Threshold: 10}
	r := g.Evaluate(EvalContext{Items: skillItems(5)})
	if r.Action != ActionAllow {
		t.Fatalf("expected allow, got %v (msg=%q)", r.Action, r.Message)
	}
}

func TestTooManySkills_OverThreshold_Warn(t *testing.T) {
	items := skillItems(11)
	// stamp a couple so top-idle has something to surface
	items[0].LastSeen = time.Now().Add(-100 * 24 * time.Hour)
	g := TooManySkills{Threshold: 10}
	r := g.Evaluate(EvalContext{Items: items})
	if r.Action != ActionWarn {
		t.Fatalf("expected warn, got %v", r.Action)
	}
	if !strings.Contains(r.Message, "11 skills") {
		t.Errorf("message missing skill count: %q", r.Message)
	}
	if !strings.Contains(r.Message, "threshold 10") {
		t.Errorf("message missing threshold: %q", r.Message)
	}
	if !strings.Contains(r.Message, "Top idle") {
		t.Errorf("message missing idle list: %q", r.Message)
	}
}

func TestTooManySkills_DoubleThreshold_Block(t *testing.T) {
	g := TooManySkills{Threshold: 10}
	r := g.Evaluate(EvalContext{Items: skillItems(21)})
	if r.Action != ActionBlock {
		t.Fatalf("expected block, got %v", r.Action)
	}
}

func TestTooManySkills_IgnoresNonSkill(t *testing.T) {
	items := skillItems(5)
	for i := 0; i < 50; i++ {
		items = append(items, model.Item{Kind: model.KindMemory, Name: "m" + itoa(i)})
		items = append(items, model.Item{Kind: model.KindSession, Name: "s" + itoa(i)})
	}
	g := TooManySkills{Threshold: 10}
	r := g.Evaluate(EvalContext{Items: items})
	if r.Action != ActionAllow {
		t.Fatalf("expected allow, got %v", r.Action)
	}
}

func TestTooManySkills_DefaultThreshold(t *testing.T) {
	// zero threshold falls back to default 100 — 50 skills must allow.
	g := TooManySkills{}
	r := g.Evaluate(EvalContext{Items: skillItems(50)})
	if r.Action != ActionAllow {
		t.Fatalf("expected allow with default threshold, got %v", r.Action)
	}
}
