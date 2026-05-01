package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// PRI-65: legacy c/m/x/s overlays were collapsed into a single `p`
// (place) overlay backed by ~/.lazyagent/library. The original four
// tests are replaced by the placePicker tests below.

func TestLegacyCopyKeyIsNoLongerWired(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "/tmp/proj")
	m.expanded["Claude/Skills/Global"] = true
	m.rebuildTree()
	moveToFirstLeaf(&m)
	before := m.pending
	m = feed(t, m, "c")
	if m.pending != before {
		t.Errorf("c must no longer open pending copy; pending=%v", m.pending)
	}
}

func TestLegacyShareKeyIsNoLongerWired(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "/tmp/proj")
	m.expanded["Claude/Skills/Global"] = true
	m.rebuildTree()
	moveToFirstLeaf(&m)
	m = feed(t, m, "s")
	if m.placePicker != nil {
		t.Error("s must not open place picker — only `p` should")
	}
}

// `p` on a file-shaped skill must open the place picker.
func TestPlacePickerOpensOnSkill(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "/tmp/proj")
	m.expanded["Claude/Skills/Global"] = true
	m.rebuildTree()
	moveToFirstLeaf(&m)
	m = feed(t, m, "p")
	if m.placePicker == nil {
		t.Fatal("p on a skill should open the place picker")
	}
	if len(m.placePicker.cells) != 3 {
		t.Errorf("expected 3 origin rows, got %d", len(m.placePicker.cells))
	}
	m = feed(t, m, "esc")
	if m.placePicker != nil {
		t.Error("esc should close the place picker")
	}
}

// Arrow keys move the cursor across the matrix; space toggles the
// current cell. We exercise a small navigation script instead of
// trusting indexing alone.
func TestPlacePickerArrowsAndSpaceToggle(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "/tmp/proj")
	m.expanded["Claude/Skills/Global"] = true
	m.rebuildTree()
	moveToFirstLeaf(&m)
	m = feed(t, m, "p")
	if m.placePicker == nil {
		t.Fatal("place picker should open")
	}
	startRow := m.placePicker.cursorRow
	startCol := m.placePicker.cursorCol
	startChecked := m.placePicker.cells[startRow][startCol].checked

	m = feed(t, m, " ")
	if m.placePicker.cells[startRow][startCol].checked == startChecked {
		t.Errorf("space should toggle cell at (%d,%d)", startRow, startCol)
	}

	m = feed(t, m, "right")
	if m.placePicker.cursorCol == startCol {
		t.Errorf("right arrow should move cursor; col stayed at %d", startCol)
	}
	m = feed(t, m, "down")
	if m.placePicker.cursorRow == startRow {
		t.Errorf("down arrow should move cursor; row stayed at %d", startRow)
	}
}

// Local-scope cells are disabled when there is no project root —
// space on a disabled cell is ignored.
func TestPlacePickerLocalDisabledWithoutProjectDir(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	m.expanded["Claude/Skills/Global"] = true
	m.rebuildTree()
	moveToFirstLeaf(&m)
	m = feed(t, m, "p")
	if m.placePicker == nil {
		t.Fatal("place picker should open")
	}
	for r, row := range m.placePicker.cells {
		for c, cell := range row {
			if cell.target.Scope == model.ScopeLocal && cell.enabled {
				t.Errorf("local cell at (%d,%d) should be disabled without project dir", r, c)
			}
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

func TestModeBTogglesPerProjectGrouping(t *testing.T) {
	// Seed two distinct local projects so Mode B has something to group.
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeLocal, Name: "alpha", Path: "/tmp/proj1/.claude/skills/alpha/SKILL.md", Storage: model.StorageDir, Meta: map[string]string{"project": "/tmp/proj1"}},
		{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeLocal, Name: "beta", Path: "/tmp/proj2/.claude/skills/beta/SKILL.md", Storage: model.StorageDir, Meta: map[string]string{"project": "/tmp/proj2"}},
	}
	m := newTestModel(t, items, "")
	// Activate all-local mode without going through `A` (which kicks a
	// reload that re-fans through the source); just flip the field and
	// rebuild manually so the test stays synchronous.
	m.allLocal = true
	m.expanded["Claude/Skills/Local"] = true
	m.rebuildTree()

	m = feed(t, m, "B")
	if !m.allLocalModeB {
		t.Fatal("B should toggle Mode B on when all-local is active")
	}

	// In Mode B, the Local section should expose two project subgroups
	// at depth 3 (between "Local" at depth 2 and items at depth 4).
	var projectNodes int
	for _, n := range m.tree {
		if n.isGroup && n.depth == 3 && (strings.Contains(n.label, "/Local/") || strings.HasSuffix(n.label, "/proj1") || strings.HasSuffix(n.label, "/proj2")) {
			projectNodes++
		}
	}
	if projectNodes < 2 {
		t.Errorf("Mode B should render >= 2 project subgroups, got %d:\n%s", projectNodes, treeDump(m))
	}

	// Toggle back to Mode A — flat list, no project subgroups.
	m = feed(t, m, "B")
	if m.allLocalModeB {
		t.Fatal("B again should toggle Mode B off")
	}
}

func TestModeBRefusedWhenAllLocalOff(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	if m.allLocal {
		t.Fatal("test precondition: allLocal must default off")
	}
	m = feed(t, m, "B")
	if m.allLocalModeB {
		t.Error("B without A should not enable Mode B")
	}
}

func TestUsageFooterAggregatesPricedSessions(t *testing.T) {
	// Isolate $HOME so a stale state.json from another test (notably
	// the H privacy toggle) can't leak into this test's model.
	t.Setenv("HOME", t.TempDir())
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z07:00")
	sevenDaysOld := time.Now().Add(-3 * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05Z07:00")
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindSession, Scope: model.ScopeGlobal, Name: "today",
			Storage: model.StorageFile, Path: "/tmp/a.jsonl",
			Meta: map[string]string{"cost_usd": "1.50", "lastUpdated": now}},
		{Origin: model.OriginClaude, Kind: model.KindSession, Scope: model.ScopeGlobal, Name: "older",
			Storage: model.StorageFile, Path: "/tmp/b.jsonl",
			Meta: map[string]string{"cost_usd": "2.00", "lastUpdated": sevenDaysOld}},
	}
	m := newTestModel(t, items, "")
	footer := m.usageFooter()
	if !strings.Contains(footer, "today $1.50") {
		t.Errorf("footer missing today total: %q", footer)
	}
	if !strings.Contains(footer, "7d $3.50") {
		t.Errorf("footer should sum today + 3d-old in 7d window: %q", footer)
	}
}

func TestUsageFooterHiddenWhenPrivacyOn(t *testing.T) {
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindSession, Scope: model.ScopeGlobal, Name: "x",
			Storage: model.StorageFile,
			Meta:    map[string]string{"cost_usd": "5.00", "lastUpdated": time.Now().UTC().Format("2006-01-02T15:04:05Z07:00")}},
	}
	m := newTestModel(t, items, "")
	m.hidePrivateSessions = true
	if got := m.usageFooter(); got != "" {
		t.Errorf("usageFooter should return empty when privacy is on, got %q", got)
	}
}

func TestUsageFooterEmptyWhenNoPricedSessions(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	if got := m.usageFooter(); got != "" {
		t.Errorf("usageFooter on fixture without sessions should be empty, got %q", got)
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
