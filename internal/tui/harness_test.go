// Bubbletea test harness (PRI-58).
//
// Provides a tiny mock Source and a `feed` helper that drives the
// Model through tea.KeyMsg sequences without booting an actual
// terminal. Tests written against this harness can assert on tree
// shape, cursor position, overlay state and error toasts after a
// keystroke pipeline — covering the bulk of Update() that pure
// helper-only tests can't reach.
//
// The harness deliberately avoids tea.Program. We invoke `Init()`,
// drain the resulting `tea.Cmd` exactly once to land the seeded
// `itemsLoadedMsg`, then feed `tea.KeyMsg{}` values into Update one
// at a time. That mirrors the real event flow tightly enough to
// catch logic bugs while staying fully synchronous.

package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/sources"
)

// mockSource returns a fixed slice of items. Used as a stand-in for
// the real Claude/Codex/Gemini adapters so tests can pin the tree
// shape without touching the filesystem.
type mockSource struct {
	name  string
	items []model.Item
}

func (m mockSource) Name() string { return m.name }

func (m mockSource) List(_ context.Context, _ string) ([]model.Item, error) {
	return append([]model.Item(nil), m.items...), nil
}

// newTestModel builds a Model preloaded with the given items, drains
// the initial loadCmd, and resizes to a reasonable terminal size.
// Returns the model ready for `feed` calls.
func newTestModel(t *testing.T, items []model.Item, projectDir string) Model {
	t.Helper()
	src := mockSource{name: "test", items: items}
	m := New([]sources.Source{src}, projectDir)
	m.width = 120
	m.height = 40

	// Run Init() → loadCmd() inline; the resulting Msg gets fed back
	// into Update so the model's items / tree settle before the test
	// starts pressing keys.
	cmd := m.Init()
	if cmd != nil {
		msg := cmd()
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}
	return m
}

// feed pushes a single key into Update and returns the resulting
// model. Use the bubbletea `tea.KeyMsg` form: alphabetic keys land
// as tea.KeyRunes; "esc" / "enter" / "tab" go through tea.KeyType.
func feed(t *testing.T, m Model, key string) Model {
	t.Helper()
	msg := keyMsg(key)
	updated, cmd := m.Update(msg)
	out := updated.(Model)
	// Drain a follow-up command if it produced one synchronously
	// (e.g. m.loadCmd() after `r` reload). Most paths return nil.
	if cmd != nil {
		if reply := cmd(); reply != nil {
			updated, _ := out.Update(reply)
			out = updated.(Model)
		}
	}
	return out
}

// keyMsg maps a string key name into the tea.KeyMsg the model expects.
// "tab", "enter", "esc", "space", arrow names go through the typed
// constants; everything else is treated as a single-rune press so the
// test reads naturally.
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	}
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	// Multi-char fallback (unlikely in tests). Treat as a sequence of
	// individual rune presses by sending the first one — tests that
	// need more press in a loop.
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(s[0])}}
}

// fixtureItems returns a small but representative item set used by
// most tests: two skills + one agent + one MCP entry under Claude
// global, plus one Gemini skill so cross-origin tree traversal can
// be exercised.
func fixtureItems() []model.Item {
	return []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal, Name: "alpha", Path: "/tmp/alpha/SKILL.md", Description: "first skill", Storage: model.StorageDir},
		{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal, Name: "beta", Path: "/tmp/beta/SKILL.md", Description: "second skill", Storage: model.StorageDir},
		{Origin: model.OriginClaude, Kind: model.KindAgent, Scope: model.ScopeGlobal, Name: "reviewer", Path: "/tmp/reviewer.md", Description: "PR reviewer", Storage: model.StorageFile},
		{Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal, Name: "linear", Path: "/tmp/.claude.json", ConfigKey: "mcpServers/linear", Storage: model.StorageEntry, Description: "npx mcp"},
		{Origin: model.OriginGemini, Kind: model.KindSkill, Scope: model.ScopeGlobal, Name: "echo", Path: "/tmp/echo/SKILL.md", Description: "gemini skill", Storage: model.StorageDir},
	}
}
