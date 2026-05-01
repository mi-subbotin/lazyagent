package tui

import (
	"strings"
	"testing"
)

func TestPendingCopyAndCancel(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "/tmp/proj")
	m.expanded["Claude/Skills/Global"] = true
	m.rebuildTree()
	moveToFirstLeaf(&m)
	m = feed(t, m, "c")
	if m.pending == nil || m.pending.kind != pendCopy {
		t.Fatalf("c should open pending copy; pending=%v", m.pending)
	}
	m = feed(t, m, "esc")
	if m.pending != nil {
		t.Error("esc should cancel pending copy")
	}
}

func TestPendingMoveAndCancel(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "/tmp/proj")
	m.expanded["Claude/Skills/Global"] = true
	m.rebuildTree()
	moveToFirstLeaf(&m)
	m = feed(t, m, "m")
	if m.pending == nil || m.pending.kind != pendMove {
		t.Fatalf("m should open pending move; pending=%v", m.pending)
	}
	// y on a Move with valid item still triggers actions.Move which
	// will fail because the source path doesn't exist; the toast or
	// follow-up Msg surfaces the error. The harness just verifies the
	// confirm overlay doesn't crash.
	m = feed(t, m, "n")
	if m.pending != nil {
		t.Error("n should cancel pending move")
	}
}

func TestCrossPickerOnSkill(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "/tmp/proj")
	m.expanded["Claude/Skills/Global"] = true
	m.rebuildTree()
	moveToFirstLeaf(&m)
	m = feed(t, m, "x")
	if m.crossPicker == nil {
		t.Fatal("x on a skill should open cross-picker")
	}
	m = feed(t, m, "esc")
	if m.crossPicker != nil {
		t.Error("esc should close cross-picker")
	}
}

func TestSharePickerOpens(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "/tmp/proj")
	m.expanded["Claude/Skills/Global"] = true
	m.rebuildTree()
	moveToFirstLeaf(&m)
	m = feed(t, m, "s")
	// Share picker either opens or sets a toast (depends on whether
	// the item has any shareable target). Just verify no panic and
	// that further keystrokes still process.
	if m.sharePicker != nil {
		m = feed(t, m, "esc")
		if m.sharePicker != nil {
			t.Error("esc should close share picker")
		}
	}
}

func TestInstallOverlayOpensWithI(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	m = feed(t, m, "i")
	if m.installing == nil {
		t.Fatal("i should open install overlay")
	}
	m = feed(t, m, "esc")
	if m.installing != nil {
		t.Error("esc should close install overlay")
	}
}

func TestReloadKeepsItems(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	before := len(m.items)
	m = feed(t, m, "r")
	if len(m.items) != before {
		t.Errorf("reload changed item count: %d → %d", before, len(m.items))
	}
}

func TestQuitReturnsCmd(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	// Calling Update with "q" should return tea.Quit; we can't compare
	// the function directly, but feed() will run cmd() which returns
	// tea.QuitMsg{}; harness's drain handles a single follow-up.
	updated, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q should produce a tea.Cmd")
	}
	_ = updated
}

func TestPrivateSessionsToggleH(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	before := m.hidePrivateSessions
	m = feed(t, m, "H")
	if m.hidePrivateSessions == before {
		t.Error("H should toggle hidePrivateSessions")
	}
}

func TestEmptyKindShowsPlaceholder(t *testing.T) {
	// fixture has no Codex items, so Codex/Skills should be expanded
	// (default) but contain only "no skills yet".
	m := newTestModel(t, fixtureItems(), "")
	var foundPlaceholder bool
	for _, n := range m.tree {
		if n.isEmpty && strings.Contains(n.label, "no skills yet") {
			foundPlaceholder = true
			break
		}
	}
	if !foundPlaceholder {
		t.Errorf("expected `no skills yet` placeholder under Codex; tree:\n%s", treeDump(m))
	}
}

func TestInstallSourceSetterAndModel(t *testing.T) {
	m := newTestModel(t, nil, "")
	m.SetInstallSource("brew")
	if m.installSource != "brew" {
		t.Errorf("SetInstallSource didn't stick: %q", m.installSource)
	}
}

func TestSetDiscoveredProjectsFiltersCwd(t *testing.T) {
	m := newTestModel(t, nil, "/tmp/cwd-proj")
	m.SetDiscoveredProjects([]string{"/tmp/cwd-proj", "/tmp/other", ""}, false)
	if len(m.discoveredProjects) != 1 || m.discoveredProjects[0] != "/tmp/other" {
		t.Errorf("discovered should filter cwd + empty: %v", m.discoveredProjects)
	}
}

// moveToFirstLeaf advances the cursor through groups until it lands
// on a non-group, non-empty leaf. Used by tests that need to operate
// on a real Item.
func moveToFirstLeaf(m *Model) {
	for i := 0; i < len(m.tree); i++ {
		n := m.tree[i]
		if !n.isGroup && !n.isEmpty {
			m.cursor = i
			return
		}
	}
}

func treeDump(m Model) string {
	var b strings.Builder
	for _, n := range m.tree {
		b.WriteString(strings.Repeat("  ", n.depth))
		if n.isGroup {
			b.WriteString("[g] ")
		} else if n.isEmpty {
			b.WriteString("[e] ")
		} else {
			b.WriteString("    ")
		}
		b.WriteString(n.label)
		b.WriteString("\n")
	}
	return b.String()
}

