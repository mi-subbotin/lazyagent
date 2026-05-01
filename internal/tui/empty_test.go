package tui

import (
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/sources"
)

func TestRenderEmptyStateContainsHint(t *testing.T) {
	got := renderEmptyState(80, 24)
	for _, want := range []string{
		"No skills, agents, MCPs, or memory found.",
		"Press ?",
		"Press i",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("empty-state missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestRenderEmptyStateNarrowDropsLogo(t *testing.T) {
	// 30 columns is below the logo width — we should still render the
	// hint, just without ASCII art so nothing wraps mid-letter.
	got := renderEmptyState(30, 12)
	if strings.Contains(got, "/_/") {
		t.Errorf("logo should be dropped at narrow width:\n%s", got)
	}
	if !strings.Contains(got, "No skills") {
		t.Errorf("hint must always render:\n%s", got)
	}
}

func TestEmptyStateViewSwitch(t *testing.T) {
	// With zero items in the loaded set, View() must short-circuit to
	// the empty-state body. We construct a Model directly so we don't
	// depend on the goroutine-based loadCmd.
	m := New([]sources.Source{}, "")
	m.loading = false
	m.items = nil
	m.width, m.height = 100, 30
	m.rebuildTree()

	out := m.View()
	if !strings.Contains(out, "No skills, agents, MCPs, or memory found.") {
		t.Errorf("View() at len(items)=0 should render empty-state body:\n%s", out)
	}
}

func TestPerSectionEmptyPlaceholder(t *testing.T) {
	// One Skill item under Claude → other sections (Agents/MCP/etc)
	// must surface a "no <kind> yet" placeholder when expanded.
	m := New([]sources.Source{}, "")
	m.loading = false
	m.items = []model.Item{{
		Origin: model.OriginClaude,
		Kind:   model.KindSkill,
		Scope:  model.ScopeGlobal,
		Name:   "demo",
		Path:   "/tmp/demo.md",
	}}
	m.rebuildTree()

	var found bool
	for _, n := range m.tree {
		if n.isEmpty && strings.Contains(n.label, "no agents yet") {
			found = true
			break
		}
	}
	if !found {
		var dump []string
		for _, n := range m.tree {
			dump = append(dump, n.label)
		}
		t.Errorf("expected `no agents yet` placeholder; tree:\n%s", strings.Join(dump, "\n"))
	}
}
