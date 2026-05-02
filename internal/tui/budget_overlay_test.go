package tui

import (
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/budget"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestBudgetWindowLimit(t *testing.T) {
	cases := []struct {
		w    budgetWindow
		lim  int
		name string
	}{
		{budgetWindowClaude, 200_000, "Claude 200k"},
		{budgetWindowCodex, 256_000, "Codex 256k"},
		{budgetWindowGemini, 1_000_000, "Gemini 1M"},
	}
	for _, c := range cases {
		if c.w.limit() != c.lim {
			t.Errorf("limit(%v)=%d; want %d", c.w, c.w.limit(), c.lim)
		}
		if c.w.String() != c.name {
			t.Errorf("String(%v)=%q; want %q", c.w, c.w.String(), c.name)
		}
	}
}

func TestBudgetOverlayTextEmpty(t *testing.T) {
	body := budgetOverlayText(budget.Summary{}, budgetWindowClaude)
	if !strings.Contains(body, "No installed items") {
		t.Errorf("empty summary missing fallback text: %q", body)
	}
	if !strings.Contains(body, "Claude 200k") {
		t.Errorf("expected window label in header: %q", body)
	}
}

func TestBudgetOverlayTextRendersGroups(t *testing.T) {
	sum := budget.Summary{
		Total: 50_000, Items: 3,
		Groups: []budget.Group{
			{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal, Tokens: 30_000, Items: 2},
			{Origin: model.OriginClaude, Kind: model.KindAgent, Scope: model.ScopeGlobal, Tokens: 20_000, Items: 1},
		},
	}
	body := budgetOverlayText(sum, budgetWindowClaude)
	if !strings.Contains(body, "50.0k") {
		t.Errorf("expected total 50.0k, got: %s", body)
	}
	// 50k / 200k = 25%
	if !strings.Contains(body, "25%") {
		t.Errorf("expected 25%% of limit, got: %s", body)
	}
	if !strings.Contains(body, "Skill") || !strings.Contains(body, "Agent") {
		t.Errorf("expected Skill and Agent rows, got: %s", body)
	}
}

func TestBudgetOverlayTextOverflow(t *testing.T) {
	sum := budget.Summary{
		Total: 500_000, Items: 1,
		Groups: []budget.Group{{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal, Tokens: 500_000, Items: 1}},
	}
	body := budgetOverlayText(sum, budgetWindowClaude)
	// 500k vs 200k → 250% — overflow indicator present
	if !strings.Contains(body, ">>>") {
		t.Errorf("expected overflow marker on bar: %s", body)
	}
}

func TestPercentOfLimit(t *testing.T) {
	cases := []struct {
		n, lim int
		want   string
	}{
		{0, 100, "0.0%"},
		{50, 100, "50%"},
		{1, 100, "1.0%"},
		{100, 0, "—"},
	}
	for _, c := range cases {
		if got := percentOfLimit(c.n, c.lim); got != c.want {
			t.Errorf("percentOfLimit(%d, %d) = %q; want %q", c.n, c.lim, got, c.want)
		}
	}
}

func TestBudgetBarFractions(t *testing.T) {
	bar := budgetBar(50, 100)
	if !strings.Contains(bar, "█") || !strings.Contains(bar, "░") {
		t.Errorf("expected mixed fill+empty: %q", bar)
	}
	full := budgetBar(100, 100)
	if !strings.Contains(full, "█") {
		t.Errorf("100%% bar should be all-filled: %q", full)
	}
}

func TestKindAndScopeShort(t *testing.T) {
	if kindShort(model.KindSkill) != "Skill" {
		t.Error()
	}
	if kindShort(model.KindMCP) != "MCP" {
		t.Error()
	}
	if scopeShort(model.ScopeGlobal) != "Global" {
		t.Error()
	}
	if scopeShort(model.ScopeLocal) != "Local" {
		t.Error()
	}
}
