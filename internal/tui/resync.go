package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/actions"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

// resyncPicker is the single-key overlay opened with `R` on a
// drifted Shared item. Only two paths matter: pick canonical bytes
// (overwrite drifted projections) or pick tool bytes (promote the
// drifted copy to canonical and reproject). esc cancels.
type resyncPicker struct {
	item model.Item
}

func newResyncPicker(it model.Item) *resyncPicker {
	return &resyncPicker{item: it}
}

func (m Model) updateResyncPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.resyncPicker = nil
		return m, nil
	case "c", "C":
		return m.commitResync(actions.ResyncCanonicalWins, "canonical")
	case "t", "T":
		return m.commitResync(actions.ResyncToolWins, "tool")
	}
	return m, nil
}

func (m Model) commitResync(dir actions.ResyncDirection, label string) (tea.Model, tea.Cmd) {
	it := m.resyncPicker.item
	m.resyncPicker = nil
	if err := actions.Resync(it, dir); err != nil {
		m.setToast("resync: " + err.Error())
		return m, nil
	}
	m.setToast(fmt.Sprintf("resynced %s — %s wins", it.Name, label))
	m.invalidateBodyCache(it.Path)
	m.loading = true
	return m, m.loadCmd()
}

func resyncPickerText(p resyncPicker) string {
	return fmt.Sprintf(
		"Resync %s (%s)\n\n"+
			"  [c] canonical wins — overwrite drifted tool copies with store bytes\n"+
			"  [t] tool wins      — promote this tool's bytes to canonical, reproject peers\n\n"+
			"[esc] cancel",
		p.item.Name, p.item.Kind,
	)
}
