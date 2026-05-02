package tui

import (
	"os"
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
	t.Setenv("HOME", t.TempDir())
	m := newTestModel(t, fixtureItems(), "")
	before := m.hidePrivateSessions
	m = feed(t, m, "H")
	if m.hidePrivateSessions == before {
		t.Error("H should toggle hidePrivateSessions")
	}
}

// PRI-70: dateBucket pins the calendar-day boundaries used for the
// Sessions sub-grouping. Boundaries are local-TZ midnight cuts:
// "Today" includes anything from local 00:00 onward, "Yesterday" the
// previous calendar day, "This week" the 5 calendar days before that,
// "Older" everything earlier or unparseable.
func TestDateBucketBoundaries(t *testing.T) {
	// Pin "now" in time.Local so the day/week boundaries the function
	// computes line up with the test's expected day-buckets regardless
	// of the host machine's TZ.
	now := time.Date(2026, 5, 1, 14, 30, 0, 0, time.Local)
	cases := []struct {
		name string
		ts   time.Time
		want string
	}{
		{"now", now, "Today"},
		{"local midnight today", time.Date(2026, 5, 1, 0, 0, 0, 0, time.Local), "Today"},
		{"one minute before midnight", time.Date(2026, 4, 30, 23, 59, 0, 0, time.Local), "Yesterday"},
		{"yesterday morning", time.Date(2026, 4, 30, 9, 0, 0, 0, time.Local), "Yesterday"},
		{"two days ago", time.Date(2026, 4, 29, 12, 0, 0, 0, time.Local), "This week"},
		{"6 days ago", time.Date(2026, 4, 25, 12, 0, 0, 0, time.Local), "This week"},
		{"7 days ago", time.Date(2026, 4, 24, 12, 0, 0, 0, time.Local), "Older"},
		{"a month ago", time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local), "Older"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dateBucket(tc.ts.Format(time.RFC3339), now)
			if got != tc.want {
				t.Errorf("dateBucket(%s) = %q, want %q", tc.ts, got, tc.want)
			}
		})
	}
}

func TestDateBucketEmptyOrInvalid(t *testing.T) {
	now := time.Now()
	if got := dateBucket("", now); got != "Older" {
		t.Errorf("empty timestamp should bucket to Older, got %q", got)
	}
	if got := dateBucket("not-a-timestamp", now); got != "Older" {
		t.Errorf("invalid timestamp should bucket to Older, got %q", got)
	}
}

// PRI-70: sessions split into <project>/<date-bucket> sub-groups.
// Two projects with mixed-age chats should produce 4 project sub-
// groups (myapp, other) and within each only the buckets that have
// items — empty buckets are dropped.
func TestRenderSessionLeavesGroupsByProjectAndBucket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now().UTC()
	rfc := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindSession, Scope: model.ScopeGlobal, Storage: model.StorageFile,
			Name: "myapp today", Path: "/tmp/a.jsonl",
			Meta: map[string]string{"project": "myapp", "lastUpdated": rfc(-1 * time.Hour)}},
		{Origin: model.OriginClaude, Kind: model.KindSession, Scope: model.ScopeGlobal, Storage: model.StorageFile,
			Name: "myapp yesterday", Path: "/tmp/b.jsonl",
			Meta: map[string]string{"project": "myapp", "lastUpdated": rfc(-26 * time.Hour)}},
		{Origin: model.OriginClaude, Kind: model.KindSession, Scope: model.ScopeGlobal, Storage: model.StorageFile,
			Name: "other today", Path: "/tmp/c.jsonl",
			Meta: map[string]string{"project": "other", "lastUpdated": rfc(-30 * time.Minute)}},
	}
	m := newTestModel(t, items, "")
	m.expanded["Claude"] = true
	m.expanded["Claude/Sessions"] = true
	m.expanded["Claude/Sessions/Global"] = true
	m.expanded["Claude/Sessions/Global/myapp"] = true
	m.expanded["Claude/Sessions/Global/myapp/Today"] = true
	m.expanded["Claude/Sessions/Global/myapp/Yesterday"] = true
	m.expanded["Claude/Sessions/Global/other"] = true
	m.expanded["Claude/Sessions/Global/other/Today"] = true
	m.rebuildTree()

	// Group labels we expect in the tree.
	wantGroups := map[string]bool{
		"Claude/Sessions/Global/myapp":           false,
		"Claude/Sessions/Global/myapp/Today":     false,
		"Claude/Sessions/Global/myapp/Yesterday": false,
		"Claude/Sessions/Global/other":           false,
		"Claude/Sessions/Global/other/Today":     false,
	}
	for _, n := range m.tree {
		if n.isGroup {
			if _, ok := wantGroups[n.label]; ok {
				wantGroups[n.label] = true
			}
		}
	}
	for label, seen := range wantGroups {
		if !seen {
			t.Errorf("expected group %q in tree; tree:\n%s", label, treeDump(m))
		}
	}
	// Empty bucket should NOT appear: there is no This-week or Older
	// item for either project.
	for _, n := range m.tree {
		if n.isGroup && (strings.HasSuffix(n.label, "/This week") || strings.HasSuffix(n.label, "/Older")) {
			t.Errorf("empty bucket should be skipped, got %q", n.label)
		}
	}
}

// PRI-70: an Item with Agent=true must be filtered out of the tree
// by default, and `G` toggles it back in. The fixture seeds one
// regular session and one subagent transcript; only the regular one
// should appear initially. After G, both should appear.
func TestAgentSessionsHiddenByDefaultAndToggleG(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	items := []model.Item{
		{
			Origin: model.OriginClaude, Kind: model.KindSession, Scope: model.ScopeGlobal,
			Name: "regular chat", Path: "/tmp/regular.jsonl", Storage: model.StorageFile,
			Meta: map[string]string{"lastUpdated": time.Now().UTC().Format("2006-01-02T15:04:05Z07:00")},
		},
		{
			Origin: model.OriginClaude, Kind: model.KindSession, Scope: model.ScopeGlobal,
			Name: "task spawn", Path: "/tmp/parent/subagents/agent-x.jsonl", Storage: model.StorageFile,
			Agent: true,
			Meta:  map[string]string{"lastUpdated": time.Now().UTC().Format("2006-01-02T15:04:05Z07:00")},
		},
	}
	m := newTestModel(t, items, "")
	// Expand every group so leaves surface in the visible tree.
	m.expanded["Claude"] = true
	m.expanded["Claude/Sessions"] = true
	m.expanded["Claude/Sessions/Global"] = true
	// PRI-70 sub-grouping: <Sessions/Global>/<project>/<date-bucket>.
	m.expanded["Claude/Sessions/Global/(no project)"] = true
	m.expanded["Claude/Sessions/Global/(no project)/Today"] = true
	m.rebuildTree()

	visibleNames := func() []string {
		var out []string
		for _, n := range m.tree {
			if !n.isGroup && !n.isEmpty {
				out = append(out, m.items[n.itemIdx].Name)
			}
		}
		return out
	}

	got := visibleNames()
	if len(got) != 1 || got[0] != "regular chat" {
		t.Errorf("default tree should contain only the regular chat; got %v", got)
	}

	m = feed(t, m, "G")
	if !m.showAgentSessions {
		t.Fatal("G should flip showAgentSessions on")
	}
	got = visibleNames()
	hasRegular := false
	hasAgent := false
	for _, n := range got {
		if n == "regular chat" {
			hasRegular = true
		}
		if n == "task spawn" {
			hasAgent = true
		}
	}
	if !hasRegular || !hasAgent {
		t.Errorf("after G, both sessions should be visible; got %v", got)
	}

	m = feed(t, m, "G")
	if m.showAgentSessions {
		t.Fatal("second G should flip showAgentSessions back off")
	}
	got = visibleNames()
	if len(got) != 1 || got[0] != "regular chat" {
		t.Errorf("after second G, agent should be hidden again; got %v", got)
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

// PRI-61: Hooks render under Origin/Hooks/<event>/<name>. Two hooks on
// different events should produce two event sub-groups; events stay
// expanded by default so the leaves are visible without an extra key.
func TestRenderHookLeavesGroupsByEvent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindHook, Scope: model.ScopeGlobal,
			Name: "PreToolUse:Bash", Path: "/tmp/settings.json", Storage: model.StorageEntry,
			ConfigKey: "hooks/PreToolUse/0/hooks/0",
			Meta:      map[string]string{"event": "PreToolUse", "matcher": "Bash"}},
		{Origin: model.OriginClaude, Kind: model.KindHook, Scope: model.ScopeGlobal,
			Name: "PostToolUse", Path: "/tmp/settings.json", Storage: model.StorageEntry,
			ConfigKey: "hooks/PostToolUse/0/hooks/0",
			Meta:      map[string]string{"event": "PostToolUse"}},
	}
	m := newTestModel(t, items, "")
	m.expanded["Claude"] = true
	m.expanded["Claude/Hooks"] = true
	m.expanded["Claude/Hooks/Global"] = true
	m.rebuildTree()

	wantGroups := map[string]bool{
		"Claude/Hooks/Global/PreToolUse":  false,
		"Claude/Hooks/Global/PostToolUse": false,
	}
	wantLeaves := map[string]bool{
		"PreToolUse:Bash": false,
		"PostToolUse":     false,
	}
	for _, n := range m.tree {
		if n.isGroup {
			if _, ok := wantGroups[n.label]; ok {
				wantGroups[n.label] = true
			}
		} else if _, ok := wantLeaves[n.label]; ok {
			wantLeaves[n.label] = true
		}
	}
	for label, seen := range wantGroups {
		if !seen {
			t.Errorf("missing group %q in tree:\n%s", label, treeDump(m))
		}
	}
	for label, seen := range wantLeaves {
		if !seen {
			t.Errorf("missing hook leaf %q in tree:\n%s", label, treeDump(m))
		}
	}
}

// PRI-61: pressing E on a Hook entry opens the editor in entry mode —
// buffer is the inner-hook JSON, not the whole settings.json. Saving
// parses the JSON back and writes via parse.WriteEntry; the rest of
// settings.json must survive untouched.
func TestEditorEntryModeOnHookSavesViaWriteEntry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	settings := dir + "/settings.json"
	body := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo old","timeout":5}]}]},"keep":"this"}`
	if err := os.WriteFile(settings, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindHook, Scope: model.ScopeGlobal,
		Name: "PreToolUse:Bash", Path: settings, Storage: model.StorageEntry,
		ConfigKey: "hooks/PreToolUse/0/hooks/0",
		Meta:      map[string]string{"event": "PreToolUse", "matcher": "Bash"},
	}
	ed, err := newEditorState(it)
	if err != nil {
		t.Fatalf("newEditorState: %v", err)
	}
	if !ed.entryMode {
		t.Fatal("hook editor should run in entryMode")
	}
	if !strings.Contains(ed.ta.Value(), "echo old") {
		t.Errorf("buffer should hold inner-hook JSON, got %q", ed.ta.Value())
	}
	if strings.Contains(ed.ta.Value(), "\"keep\"") {
		t.Errorf("buffer must not include unrelated settings.json keys, got %q", ed.ta.Value())
	}
	// Edit the command and save.
	edited := strings.Replace(ed.ta.Value(), "echo old", "echo new", 1)
	ed.ta.SetValue(edited)
	if err := ed.saveEntry(); err != nil {
		t.Fatalf("saveEntry: %v", err)
	}
	got, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "echo new") {
		t.Errorf("settings.json missing edit:\n%s", s)
	}
	if !strings.Contains(s, "\"keep\"") {
		t.Errorf("unrelated keys clobbered:\n%s", s)
	}
}

// PRI-74: pressing E on an MCP entry opens the editor in entry mode
// — buffer is just the inner mcpServers/<name> JSON, not the whole
// .claude.json. Saving routes through parse.WriteEntry, so unrelated
// keys survive. Mirrors the hook-entry test above; both paths share
// newEntryEditor now.
func TestEditorEntryModeOnMCPSavesViaWriteEntry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg := dir + "/.claude.json"
	body := `{"mcpServers":{"linear":{"command":"npx","args":["@linear/mcp"],"env":{},"type":"stdio"}},"otherKey":"keepMe"}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
		Name: "linear", Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "mcpServers/linear",
	}
	ed, err := newEditorState(it)
	if err != nil {
		t.Fatalf("newEditorState: %v", err)
	}
	if !ed.entryMode {
		t.Fatal("MCP editor should run in entryMode")
	}
	if !strings.Contains(ed.ta.Value(), "@linear/mcp") {
		t.Errorf("buffer should hold inner mcp JSON, got %q", ed.ta.Value())
	}
	if strings.Contains(ed.ta.Value(), "otherKey") {
		t.Errorf("buffer must not include unrelated .claude.json keys, got %q", ed.ta.Value())
	}
	edited := strings.Replace(ed.ta.Value(), "@linear/mcp", "@linear/mcp@1.2.3", 1)
	ed.ta.SetValue(edited)
	if err := ed.saveEntry(); err != nil {
		t.Fatalf("saveEntry: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	s := string(got)
	if !strings.Contains(s, "@linear/mcp@1.2.3") {
		t.Errorf("config missing edit:\n%s", s)
	}
	if !strings.Contains(s, "otherKey") {
		t.Errorf("unrelated keys clobbered:\n%s", s)
	}
}

// PRI-64: pressing S opens the sync overlay populated from SyncAll.
// The fixture has at least one shareable global item, so the plan is
// non-empty and the overlay surfaces it.
func TestSyncOverlayOpensWithS(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newTestModel(t, fixtureItems(), "")
	m = feed(t, m, "S")
	if m.syncing == nil {
		t.Fatal("S should open the sync overlay")
	}
	if len(m.syncing.plan.Ops) == 0 {
		t.Fatalf("plan must have at least one op for the fixture")
	}
	m = feed(t, m, "esc")
	if m.syncing != nil {
		t.Error("esc should close the sync overlay")
	}
}

// Empty input items → toast, no overlay. The overlay should never open
// with a blank plan (would be confusing and there's nothing to apply).
func TestSyncOverlayNoOpsToastsAndStaysClosed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newTestModel(t, nil, "")
	m = feed(t, m, "S")
	if m.syncing != nil {
		t.Error("empty plan should not open overlay")
	}
}

// Plan with only Skip ops (e.g. only an MCP entry) opens the overlay
// (so the user can read the reason) but `y` becomes a no-op + toast.
func TestSyncOverlayAllSkippedYIsNoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
			Name: "linear", Path: "/tmp/.claude.json", ConfigKey: "mcpServers/linear",
			Storage: model.StorageEntry},
	}
	m := newTestModel(t, items, "")
	m = feed(t, m, "S")
	if m.syncing == nil {
		t.Fatal("S should open even when only skips are present")
	}
	if m.syncing.plan.Mutating() {
		t.Error("plan with only MCP entry should be non-mutating")
	}
	m = feed(t, m, "y")
	if m.syncing != nil {
		t.Errorf("y on a non-mutating plan should close the overlay; still open")
	}
}

// PRI-73: f on a valid item is a no-op + toast (we should not open a
// confirm overlay for items that have nothing to fix).
func TestFixKeyOnValidItemTosts(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "/tmp/proj")
	m.expanded["Claude/Skills/Global"] = true
	m.rebuildTree()
	moveToFirstLeaf(&m)
	m = feed(t, m, "f")
	if m.pending != nil {
		t.Errorf("f on a valid item must not open the confirm overlay")
	}
	if !strings.Contains(m.toast, "nothing to fix") {
		t.Errorf("expected 'nothing to fix' toast, got %q", m.toast)
	}
}

// PRI-73: f on an invalid markdown item builds a FixPlan, parks it on
// the pending op, and the confirm overlay can render. Pressing y
// applies the plan and clears pending.
func TestFixKeyAppliesPlanOnInvalidItem(t *testing.T) {
	dir := t.TempDir()
	skillDir := dir + "/skills/broken"
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := skillDir + "/SKILL.md"
	in := "---\nname: broken\ndescription: line one.\nspillover here.\n---\nbody\n"
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	items := []model.Item{{
		Origin:     model.OriginClaude,
		Kind:       model.KindSkill,
		Scope:      model.ScopeGlobal,
		Name:       "broken",
		Path:       path,
		Storage:    model.StorageDir,
		ParseError: "line 4: expected `key: value`, got \"spillover here.\"",
	}}
	m := newTestModel(t, items, "")
	m.expanded["Claude/Skills/Global"] = true
	m.rebuildTree()
	moveToFirstLeaf(&m)

	m = feed(t, m, "f")
	if m.pending == nil {
		t.Fatalf("f should open the fix-confirm overlay (toast=%q)", m.toast)
	}
	if m.pending.kind != pendFix {
		t.Fatalf("pending kind should be pendFix, got %v", m.pending.kind)
	}
	if len(m.pending.fix.After) == 0 {
		t.Fatal("pending.fix.After should be populated")
	}

	m = feed(t, m, "y")
	if m.pending != nil {
		t.Error("y should clear pending")
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "spillover here.\n") && !strings.Contains(string(out), "line one. spillover here.") {
		t.Errorf("rewrite did not merge spillover:\n%s", out)
	}
}

// PRI-73 Phase B: F with no invalid items in the tree should toast and
// stay closed — opening an empty bulk overlay would be confusing.
func TestFixOverlayNoInvalidItemsTosts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newTestModel(t, fixtureItems(), "")
	m = feed(t, m, "F")
	if m.fixing != nil {
		t.Error("F with no invalid items should not open the overlay")
	}
	if !strings.Contains(m.toast, "no invalid items") {
		t.Errorf("expected 'no invalid items' toast, got %q", m.toast)
	}
}

// PRI-73 Phase B: F collects every invalid item, y applies the fixable
// subset and closes back to result mode showing fixed/total counts.
// Mixed input: one fixable Skill + one unfixable Hook (empty command).
func TestFixOverlayBulkAppliesFixableSubset(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	// Fixable: skill with multi-line description spillover.
	skillDir := dir + "/skills/broken"
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := skillDir + "/SKILL.md"
	skillIn := "---\nname: broken\ndescription: line one.\nspillover here.\n---\nbody\n"
	if err := os.WriteFile(skillPath, []byte(skillIn), 0o644); err != nil {
		t.Fatal(err)
	}

	// Unfixable: hook entry with empty command.
	settings := dir + "/settings.json"
	hookJSON := `{"hooks":{"PreToolUse":[{"hooks":[{"command":""}]}]}}`
	if err := os.WriteFile(settings, []byte(hookJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	items := []model.Item{
		{
			Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal,
			Name: "broken", Path: skillPath, Storage: model.StorageDir,
			ParseError: "spillover",
		},
		{
			Origin: model.OriginClaude, Kind: model.KindHook, Scope: model.ScopeGlobal,
			Name: "PreToolUse", Path: settings, Storage: model.StorageEntry,
			ConfigKey:  "hooks/PreToolUse/0/hooks/0",
			ParseError: "missing or empty command; missing type",
		},
	}
	m := newTestModel(t, items, "")

	m = feed(t, m, "F")
	if m.fixing == nil {
		t.Fatalf("F should open bulk overlay (toast=%q)", m.toast)
	}
	if len(m.fixing.entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(m.fixing.entries))
	}
	if countFixable(m.fixing.entries) != 1 {
		t.Errorf("want 1 fixable, got %d", countFixable(m.fixing.entries))
	}

	m = feed(t, m, "y")
	if m.fixing == nil {
		t.Fatal("after apply the overlay should still be open in result mode")
	}
	if !m.fixing.applied {
		t.Error("applied flag should be set after y")
	}
	if m.fixing.fixed != 1 {
		t.Errorf("want fixed=1, got %d (errs=%v)", m.fixing.fixed, m.fixing.errs)
	}

	// Verify the skill was actually rewritten on disk.
	out, _ := os.ReadFile(skillPath)
	if !strings.Contains(string(out), "line one. spillover here.") {
		t.Errorf("skill rewrite did not merge spillover:\n%s", out)
	}

	m = feed(t, m, "esc")
	if m.fixing != nil {
		t.Error("esc should close the overlay")
	}
}

// PRI-73 Phase B: an overlay where every entry is unfixable should
// toast on y and close — applying nothing is better than pretending.
func TestFixOverlayAllUnfixableY(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	settings := dir + "/settings.json"
	if err := os.WriteFile(settings, []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":""}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	items := []model.Item{
		{
			Origin: model.OriginClaude, Kind: model.KindHook, Scope: model.ScopeGlobal,
			Name: "PreToolUse", Path: settings, Storage: model.StorageEntry,
			ConfigKey:  "hooks/PreToolUse/0/hooks/0",
			ParseError: "missing or empty command",
		},
	}
	m := newTestModel(t, items, "")
	m = feed(t, m, "F")
	if m.fixing == nil {
		t.Fatal("F should open even when only unfixable entries exist")
	}
	m = feed(t, m, "y")
	if m.fixing != nil {
		t.Error("y on all-unfixable plan should close the overlay")
	}
	if !strings.Contains(m.toast, "unfixable") {
		t.Errorf("expected unfixable toast, got %q", m.toast)
	}
}
