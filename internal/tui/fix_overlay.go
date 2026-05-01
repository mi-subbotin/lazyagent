package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/actions"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

// fixOverlay drives the bulk auto-fix flow opened with `F` (PRI-73
// Phase B). Mirrors the syncOverlay structure: every invalid item in
// `m.items` is enrolled at open-time with a precomputed FixPlan or an
// `unfixableReason` string, so navigation / applying never re-runs the
// expensive Fix path.
//
// `y` applies every fixable plan; unfixable rows are listed with their
// reason but skipped silently. After apply the overlay flips to result
// mode showing fixed / skipped / errored counts. The user closes with
// esc.
type fixOverlay struct {
	entries []fixEntry
	cursor  int
	// applied flips to true after the user hits y, switching the body
	// to the result summary.
	applied bool
	fixed   int
	errs    []error
}

// fixEntry is one row in the bulk overlay. Exactly one of plan / reason
// is meaningful: when reason is empty the plan is fixable; otherwise
// the row is read-only and reason explains why.
type fixEntry struct {
	item   model.Item
	plan   actions.FixPlan
	reason string // empty when entry is fixable
}

func (e fixEntry) fixable() bool { return e.reason == "" }

// newFixOverlay scans items for ParseError != "" and builds an entry
// per invalid item. Returns nil when nothing needs attention so the
// caller can toast instead of opening an empty overlay.
func newFixOverlay(items []model.Item) *fixOverlay {
	var entries []fixEntry
	for _, it := range items {
		if it.ParseError == "" {
			continue
		}
		entry := fixEntry{item: it}
		plan, err := actions.Fix(it)
		if err != nil {
			entry.reason = shortFixError(err)
		} else {
			entry.plan = plan
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil
	}
	return &fixOverlay{entries: entries}
}

// shortFixError trims the noisy package-prefix wrapping ("unfixable: ")
// off the typical Fix error message so the reason column stays
// readable. Falls back to the full string when the wrapper is missing.
func shortFixError(err error) string {
	s := err.Error()
	for _, prefix := range []string{"unfixable: ", "nothing to fix: "} {
		if strings.HasPrefix(s, prefix) {
			return strings.TrimPrefix(s, prefix)
		}
	}
	return s
}

func (m Model) updateFixOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.fixing
	if f.applied {
		switch msg.String() {
		case "esc", "enter", "q":
			m.fixing = nil
			return m, nil
		}
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.fixing = nil
		return m, nil
	case "down", "j":
		if f.cursor < len(f.entries)-1 {
			f.cursor++
		}
		return m, nil
	case "up", "k":
		if f.cursor > 0 {
			f.cursor--
		}
		return m, nil
	case "y", "Y", "enter":
		fixable := countFixable(f.entries)
		if fixable == 0 {
			m.setToast("fix-all: nothing applyable — all entries unfixable")
			m.fixing = nil
			return m, nil
		}
		for i := range f.entries {
			if !f.entries[i].fixable() {
				continue
			}
			if err := actions.ApplyFix(f.entries[i].plan); err != nil {
				f.errs = append(f.errs, fmt.Errorf("%s: %w", f.entries[i].item.Name, err))
				continue
			}
			f.fixed++
		}
		f.applied = true
		return m, m.afterFixApply()
	}
	return m, nil
}

// afterFixApply queues a reload so the tree's `(invalid)` markers clear
// for items that fixed cleanly, plus a toast that summarises the run.
func (m Model) afterFixApply() tea.Cmd {
	f := m.fixing
	if len(f.errs) == 0 {
		m.setToast(fmt.Sprintf("fix-all: fixed %d / %d", f.fixed, len(f.entries)))
	} else {
		m.setToast(fmt.Sprintf("fix-all: fixed %d, %d errors", f.fixed, len(f.errs)))
	}
	m.loading = true
	return m.loadCmd()
}

func countFixable(entries []fixEntry) int {
	n := 0
	for _, e := range entries {
		if e.fixable() {
			n++
		}
	}
	return n
}

func countUnfixable(entries []fixEntry) int {
	return len(entries) - countFixable(entries)
}

// fixOverlayText renders the body for both phases. Pre-apply: counts +
// scrollable list of (item, status). Post-apply: a summary with one
// line per error and a single "press esc" hint.
func fixOverlayText(f fixOverlay) string {
	var b strings.Builder
	if f.applied {
		fmt.Fprintf(&b, "Fix-all complete: fixed %d / %d, %d errors\n\n",
			f.fixed, len(f.entries), len(f.errs))
		if len(f.errs) > 0 {
			for _, e := range f.errs {
				fmt.Fprintf(&b, "  · %s\n", e.Error())
			}
			b.WriteString("\n")
		}
		unfix := countUnfixable(f.entries)
		if unfix > 0 {
			fmt.Fprintf(&b, "Skipped %d unfixable item(s) — see list above (esc and `f` each manually).\n\n", unfix)
		}
		b.WriteString("[esc] close")
		return b.String()
	}

	fixable := countFixable(f.entries)
	unfix := len(f.entries) - fixable
	fmt.Fprintf(&b, "Invalid items: %d  (fixable: %d, unfixable: %d)\n\n",
		len(f.entries), fixable, unfix)

	const window = 12
	start := f.cursor - window/2
	if start < 0 {
		start = 0
	}
	end := start + window
	if end > len(f.entries) {
		end = len(f.entries)
	}
	for i := start; i < end; i++ {
		e := f.entries[i]
		cursor := "  "
		if i == f.cursor {
			cursor = "▸ "
		}
		status := "[fix]"
		tail := ""
		if !e.fixable() {
			status = "[skip]"
			tail = "  — " + e.reason
		}
		fmt.Fprintf(&b, "%s%s %s/%s — %s%s\n",
			cursor, status, e.item.Origin, e.item.Kind, e.item.Name, tail)
	}
	if len(f.entries) > window {
		fmt.Fprintf(&b, "\n  (%d / %d shown)\n", end-start, len(f.entries))
	}
	b.WriteString("\n[y]es apply fixable · [esc] cancel")
	return b.String()
}

