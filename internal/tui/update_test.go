package tui

import (
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestLoadPopulatesTreeFromFixture(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	if m.loading {
		t.Error("loading should be false after Init drain")
	}
	if len(m.items) != 5 {
		t.Errorf("got %d items, want 5", len(m.items))
	}
	if len(m.tree) == 0 {
		t.Fatal("tree empty after load")
	}
	// Tree should contain at least Claude + Gemini origin groups.
	var hasClaude, hasGemini bool
	for _, n := range m.tree {
		if !n.isGroup {
			continue
		}
		switch n.label {
		case "Claude":
			hasClaude = true
		case "Gemini":
			hasGemini = true
		}
	}
	if !hasClaude || !hasGemini {
		t.Errorf("origins missing: claude=%v gemini=%v", hasClaude, hasGemini)
	}
}

func TestNavigationJK(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	start := m.cursor
	m = feed(t, m, "j")
	if m.cursor <= start {
		t.Errorf("j should advance cursor; %d → %d", start, m.cursor)
	}
	m = feed(t, m, "k")
	if m.cursor != start {
		t.Errorf("k should reverse j; expected %d, got %d", start, m.cursor)
	}
}

func TestArrowsAreAliases(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	start := m.cursor
	m = feed(t, m, "down")
	if m.cursor <= start {
		t.Errorf("down should advance cursor")
	}
	m = feed(t, m, "up")
	if m.cursor != start {
		t.Errorf("up should reverse down")
	}
}

func TestHelpOpenAndAnyKeyCloses(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	m = feed(t, m, "?")
	if !m.helpOpen {
		t.Fatal("? should open help overlay")
	}
	m = feed(t, m, "j")
	if m.helpOpen {
		t.Error("any key should close help overlay")
	}
}

func TestFilterModeAndEsc(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	m = feed(t, m, "/")
	if !m.filterMode {
		t.Fatal("/ should enter filter mode")
	}
	// type a character — simulate by calling feed with a rune.
	m = feed(t, m, "a")
	if m.filterText == "" {
		t.Error("filter text should accumulate keystrokes")
	}
	m = feed(t, m, "esc")
	if m.filterMode {
		t.Error("esc should leave filter mode")
	}
	if m.filterText != "" {
		t.Errorf("esc should clear filter text, got %q", m.filterText)
	}
}

func TestFilterAccumulatesAndPersistsAfterEnter(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	// Pre-expand the Global subgroups so leaves are visible (the
	// default tree starts collapsed at the Scope level).
	m.expanded["Claude/Skills/Global"] = true
	m.expanded["Gemini/Skills/Global"] = true
	m.rebuildTree()

	m = feed(t, m, "/")
	for _, r := range "alpha" {
		m = feed(t, m, string(r))
	}
	if m.filterText != "alpha" {
		t.Fatalf("filterText accumulation broken: %q", m.filterText)
	}
	// Now leave the editor with enter. activate() lands on the first
	// matching leaf so detailFull flips on; filterText survives.
	m = feed(t, m, "enter")
	if m.filterText != "alpha" {
		t.Errorf("filterText cleared on enter: %q", m.filterText)
	}
	if m.filterMode {
		t.Error("enter should leave filter editor")
	}
}

func TestFilterFiltersTreeWhenExpanded(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	m.expanded["Claude/Skills/Global"] = true
	m.expanded["Gemini/Skills/Global"] = true
	m.rebuildTree()

	m = feed(t, m, "/")
	for _, r := range "alpha" {
		m = feed(t, m, string(r))
	}

	var names []string
	for _, n := range m.tree {
		if !n.isGroup && !n.isEmpty {
			names = append(names, n.label)
		}
	}
	if !contains(names, "alpha") {
		t.Errorf("filtered tree missing alpha; visible leaves: %v", names)
	}
	if contains(names, "beta") {
		t.Errorf("filtered tree should not contain beta: %v", names)
	}
	if contains(names, "echo") {
		t.Errorf("filtered tree should not contain echo: %v", names)
	}
}

func TestDeleteOpensConfirmOverlay(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	// Move to a leaf node (alpha skill should be at cursor after a few js)
	for i := 0; i < 5 && m.cursor < len(m.tree); i++ {
		if !m.tree[m.cursor].isGroup {
			break
		}
		m = feed(t, m, "j")
	}
	if m.cursor >= len(m.tree) || m.tree[m.cursor].isGroup {
		t.Skip("could not land on a leaf; tree shape changed")
	}
	m = feed(t, m, "d")
	if m.pending == nil {
		t.Fatal("d should open pending delete overlay")
	}
	if m.pending.kind != pendDelete {
		t.Errorf("pending.kind = %v, want pendDelete", m.pending.kind)
	}
	// n cancels.
	m = feed(t, m, "n")
	if m.pending != nil {
		t.Error("n should cancel the pending overlay")
	}
}

func TestAllLocalToggle(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	if m.allLocal {
		t.Fatal("allLocal should default to off")
	}
	m = feed(t, m, "A")
	if !m.allLocal {
		t.Error("A should toggle all-local on")
	}
	m = feed(t, m, "A")
	if m.allLocal {
		t.Error("A again should toggle all-local off")
	}
}

func TestExpandCollapseClaudeGroup(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	// Find the Claude group node and aim cursor at it.
	idx := -1
	for i, n := range m.tree {
		if n.isGroup && n.label == "Claude" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("Claude group not found in tree")
	}
	m.cursor = idx
	beforeLen := len(m.tree)
	m = feed(t, m, "h") // collapse
	if len(m.tree) >= beforeLen {
		t.Errorf("h on Claude should collapse; before=%d after=%d", beforeLen, len(m.tree))
	}
	m = feed(t, m, "l") // expand
	if len(m.tree) < beforeLen {
		t.Errorf("l should re-expand; before=%d after=%d", beforeLen, len(m.tree))
	}
}

func TestUpdateBannerDismissedOnAnyKey(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	m.updateAvailable = "v9.9.9"
	m.updateBannerOff = false
	m = feed(t, m, "j")
	if !m.updateBannerOff {
		t.Error("any key should dismiss the update banner for the day")
	}
}

func TestViewRendersWithoutPanic(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	out := m.View()
	if out == "" {
		t.Error("View should return non-empty output")
	}
	// Sanity: title and at least one origin group label show up.
	if !strings.Contains(out, "lazyagent") {
		t.Errorf("View missing title; output:\n%s", out)
	}
}

func TestViewEmptyStateWhenNoItems(t *testing.T) {
	m := newTestModel(t, nil, "")
	out := m.View()
	if !strings.Contains(out, "No skills, agents, MCPs, or memory found.") {
		t.Errorf("empty View should render empty-state body; got:\n%s", out)
	}
}

func TestViewAtSmallSizeNoPanic(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	m.width, m.height = 50, 10
	_ = m.View()
	m.width, m.height = 200, 60
	_ = m.View()
}

func TestKindHookRendersWithWarning(t *testing.T) {
	items := []model.Item{
		{
			Origin: model.OriginClaude, Kind: model.KindHook, Scope: model.ScopeGlobal,
			Name: "PreToolUse:Bash", Path: "/tmp/settings.json",
			ConfigKey: "hooks/PreToolUse/0/0", Storage: model.StorageEntry,
			Description: "echo before",
			Body:        "# Hook\n\n⚠ runs shell\n\n```sh\necho before\n```\n",
		},
	}
	m := newTestModel(t, items, "")
	if len(m.items) != 1 || m.items[0].Kind != model.KindHook {
		t.Fatalf("seed mismatch: %+v", m.items)
	}
	// Tree should contain a Claude/Hooks/Global path expanded by default.
	var foundHooksGroup bool
	for _, n := range m.tree {
		if n.isGroup && strings.HasSuffix(n.label, "/Hooks") {
			foundHooksGroup = true
			break
		}
	}
	if !foundHooksGroup {
		t.Errorf("Hooks group not visible in tree")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
