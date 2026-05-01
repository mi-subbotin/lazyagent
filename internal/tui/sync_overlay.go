package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/actions"
)

// syncOverlay drives the bulk `library sync` flow opened with `S`.
//
// The plan is computed once at open-time off `m.items` so the user sees
// a stable preview while they navigate. `j`/`k` scroll the ops list,
// `y` applies, `o` retries with overwrite=true after a conflict, `esc`
// cancels. PRI-64.
type syncOverlay struct {
	plan   actions.Plan
	cursor int
	// applied is set after a successful or partial apply so the body
	// switches to a result summary; the user closes with esc/enter.
	applied  bool
	errs     []error
	conflict bool // true when last apply hit ErrPlaceConflicts; `o` retries
}

// newSyncOverlay returns nil if the planner found nothing to do — the
// caller surfaces a toast in that case rather than opening an empty
// overlay. A plan with only Skip ops still opens (so the user can see
// why nothing is happening), but the apply action becomes a no-op.
func newSyncOverlay(plan actions.Plan) *syncOverlay {
	return &syncOverlay{plan: plan}
}

func (m Model) updateSyncOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.syncing
	if s.applied {
		switch msg.String() {
		case "esc", "enter", "q":
			m.syncing = nil
			return m, nil
		}
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.syncing = nil
		return m, nil
	case "down", "j":
		if s.cursor < len(s.plan.Ops)-1 {
			s.cursor++
		}
		return m, nil
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
		return m, nil
	case "y", "Y", "enter":
		if !s.plan.Mutating() {
			m.setToast("nothing to sync — every item is already in place")
			m.syncing = nil
			return m, nil
		}
		s.errs = actions.ApplyPlan(s.plan, false)
		s.conflict = actions.IsSyncConflict(s.errs)
		s.applied = true
		return m, m.afterSyncApply()
	case "o", "O":
		// Overwrite-on-conflict retry. Only meaningful when the user
		// hit `y` first and got conflicts back; otherwise no-op.
		if !s.conflict {
			return m, nil
		}
		s.errs = actions.ApplyPlan(s.plan, true)
		s.conflict = actions.IsSyncConflict(s.errs)
		s.applied = true
		return m, m.afterSyncApply()
	}
	return m, nil
}

// afterSyncApply queues a reload so the tree reflects the new on-disk
// state, plus a toast that summarises the run. The overlay stays open
// in result mode so the user can read errors before closing.
func (m Model) afterSyncApply() tea.Cmd {
	s := m.syncing
	if len(s.errs) == 0 {
		m.setToast(fmt.Sprintf("sync: applied %d ops", countMutating(s.plan)))
	} else {
		m.setToast(fmt.Sprintf("sync: %d ops, %d errors", countMutating(s.plan), len(s.errs)))
	}
	m.loading = true
	return m.loadCmd()
}

func countMutating(p actions.Plan) int {
	n := 0
	for _, op := range p.Ops {
		if op.Action != actions.ActionSkip {
			n++
		}
	}
	return n
}

// syncOverlayText renders the body for both phases. Pre-apply: header
// counts + scrollable ops list + footer hint. Post-apply: a summary
// with per-error lines and a single "press esc" hint.
func syncOverlayText(s syncOverlay) string {
	var b strings.Builder
	if s.applied {
		fmt.Fprintf(&b, "Sync complete: %d ops, %d errors\n\n", countMutating(s.plan), len(s.errs))
		if len(s.errs) > 0 {
			for _, e := range s.errs {
				fmt.Fprintf(&b, "  · %s\n", e.Error())
			}
			b.WriteString("\n")
			if s.conflict {
				b.WriteString("[o] retry with overwrite  ")
			}
		}
		b.WriteString("[esc] close")
		return b.String()
	}

	counts := s.plan.Counts()
	fmt.Fprintf(&b, "Plan: %d import · %d project · %d resync · %d skip\n\n",
		counts[actions.ActionImport], counts[actions.ActionProject],
		counts[actions.ActionResync], counts[actions.ActionSkip])

	if len(s.plan.Ops) == 0 {
		b.WriteString("(no items to consider)\n\n")
	}
	// Show a window of ops around the cursor so long lists don't blow
	// the overlay. 12 rows is enough to read a typical install.
	const window = 12
	start := s.cursor - window/2
	if start < 0 {
		start = 0
	}
	end := start + window
	if end > len(s.plan.Ops) {
		end = len(s.plan.Ops)
	}
	for i := start; i < end; i++ {
		op := s.plan.Ops[i]
		cursor := "  "
		if i == s.cursor {
			cursor = "▸ "
		}
		line := fmt.Sprintf("%s[%s] %s/%s — %s", cursor, op.Action, op.Item.Origin, op.Item.Kind, op.Item.Name)
		if op.Reason != "" {
			line += "  (" + op.Reason + ")"
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n[y]es apply · [esc] cancel")
	if s.plan.Mutating() {
		b.WriteString(" · [o] retry overwrite (after conflict)")
	}
	return b.String()
}
