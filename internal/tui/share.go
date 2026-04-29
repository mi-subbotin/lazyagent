package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/actions"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

type sharePhase int

const (
	sharePhasePick    sharePhase = iota // user is choosing targets
	sharePhaseConfirm                   // pre-flight found conflicts; user confirms overwrite
)

// sharePicker drives the multi-select share overlay opened with `s`.
// The same overlay covers two flows:
//
//   - First share: the item lives entirely in a per-tool dir. All
//     compatible targets are pre-checked; enter calls actions.Share.
//   - Reshare: the item is already in the canonical store (Origin ==
//     Shared, or the per-tool path is a projection). Currently-active
//     targets are pre-checked from the filesystem; enter calls
//     actions.Reshare which diffs add/remove.
//
// Both flows funnel through ShareConflicts on enter — if any target
// has unrelated content already, the overlay flips to a confirm phase
// listing what would be replaced, and only proceeds on `o`.
type sharePicker struct {
	item      model.Item
	options   []shareOption
	cursor    int
	reshare   bool
	phase     sharePhase
	conflicts []actions.ShareConflict
	pending   []model.Origin // targets staged for the action call
}

type shareOption struct {
	target  model.Origin
	enabled bool   // selectable; otherwise dimmed and ignored on toggle
	checked bool
	reason  string // why disabled, if it is
}

func newSharePicker(it model.Item) (*sharePicker, error) {
	reshare := it.Shared
	if !reshare && !actions.CanShare(it) {
		return nil, fmt.Errorf("can't share %s items of this shape (yet)", it.Kind)
	}

	var current map[model.Origin]bool
	if reshare {
		current = map[model.Origin]bool{}
		for _, t := range actions.CurrentProjections(it) {
			current[t] = true
		}
	}

	var opts []shareOption
	for _, t := range []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini} {
		opt := shareOption{target: t}
		if actions.CanProjectTo(it.Kind, t) {
			opt.enabled = true
			if reshare {
				opt.checked = current[t]
			} else {
				opt.checked = true
			}
		} else {
			opt.reason = "needs format conversion (deferred)"
		}
		opts = append(opts, opt)
	}
	cursor := 0
	for i, o := range opts {
		if o.enabled {
			cursor = i
			break
		}
	}
	return &sharePicker{item: it, options: opts, cursor: cursor, reshare: reshare}, nil
}

func (m Model) updateSharePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.sharePicker.phase == sharePhaseConfirm {
		return m.updateShareConfirm(msg)
	}
	return m.updateSharePick(msg)
}

func (m Model) updateSharePick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.sharePicker
	switch msg.String() {
	case "esc", "q":
		m.sharePicker = nil
		return m, nil
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
		return m, nil
	case "down", "j":
		if p.cursor < len(p.options)-1 {
			p.cursor++
		}
		return m, nil
	case " ", "x":
		opt := &p.options[p.cursor]
		if opt.enabled {
			opt.checked = !opt.checked
		}
		return m, nil
	case "enter":
		var targets []model.Origin
		for _, o := range p.options {
			if o.enabled && o.checked {
				targets = append(targets, o.target)
			}
		}
		if !p.reshare && len(targets) == 0 {
			m.setToast("share: pick at least one target")
			return m, nil
		}
		// Pre-flight conflict check. For reshare we only care about
		// additions; for first-share, the action itself runs the same
		// scan but we want to show the user the list before mutating.
		var probe []model.Origin
		if p.reshare {
			current := map[model.Origin]bool{}
			for _, t := range actions.CurrentProjections(p.item) {
				current[t] = true
			}
			for _, t := range targets {
				if !current[t] {
					probe = append(probe, t)
				}
			}
		} else {
			probe = targets
		}
		conflicts := actions.ShareConflicts(p.item, probe)
		if len(conflicts) > 0 {
			p.phase = sharePhaseConfirm
			p.conflicts = conflicts
			p.pending = targets
			return m, nil
		}
		return m.commitShare(targets, false)
	}
	return m, nil
}

func (m Model) updateShareConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "n", "N":
		// Step back to picking; keeps the user's selections so they can
		// untick a target instead of cancelling outright.
		m.sharePicker.phase = sharePhasePick
		m.sharePicker.conflicts = nil
		m.sharePicker.pending = nil
		return m, nil
	case "q":
		m.sharePicker = nil
		return m, nil
	case "o", "O", "y", "Y", "enter":
		targets := m.sharePicker.pending
		return m.commitShare(targets, true)
	}
	return m, nil
}

func (m Model) commitShare(targets []model.Origin, overwrite bool) (tea.Model, tea.Cmd) {
	it := m.sharePicker.item
	reshare := m.sharePicker.reshare
	m.sharePicker = nil

	var err error
	verb := "shared"
	if reshare {
		err = actions.Reshare(it, targets, overwrite)
		verb = "reshared"
	} else {
		err = actions.Share(it, targets, overwrite)
	}
	if err != nil {
		m.setToast(verb + ": " + err.Error())
		return m, nil
	}
	if len(targets) == 0 {
		m.setToast(fmt.Sprintf("%s %s — no projections", verb, it.Name))
	} else {
		m.setToast(fmt.Sprintf("%s %s → %s", verb, it.Name, joinTargets(targets)))
	}
	m.invalidateBodyCache(it.Path)
	m.loading = true
	return m, m.loadCmd()
}

func joinTargets(ts []model.Origin) string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.String())
	}
	return strings.Join(names, ", ")
}

// sharePickerText renders the body of the share overlay. Two layouts:
// the picking phase shows the target checklist; the confirm phase
// lists the conflicts that overwrite would replace.
func sharePickerText(p sharePicker) string {
	if p.phase == sharePhaseConfirm {
		return shareConfirmText(p)
	}
	var b strings.Builder
	verb := "Share"
	if p.reshare {
		verb = "Reshare"
	}
	fmt.Fprintf(&b, "%s %s (%s) across tools:\n\n", verb, p.item.Name, p.item.Kind)
	for i, o := range p.options {
		mark := "[ ]"
		if o.checked {
			mark = "[x]"
		}
		cursor := "  "
		if i == p.cursor {
			cursor = "▸ "
		}
		line := fmt.Sprintf("%s%s %s", cursor, mark, o.target)
		if !o.enabled {
			line += "  — " + o.reason
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n[space] toggle  [enter] confirm  [esc] cancel")
	return b.String()
}

func shareConfirmText(p sharePicker) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Overwrite %d existing item(s)?\n\n", len(p.conflicts))
	for _, c := range p.conflicts {
		fmt.Fprintf(&b, "  %s — %s at %s\n", c.Target, c.Kind, c.Path)
	}
	b.WriteString("\nThe canonical version of ")
	b.WriteString(p.item.Name)
	b.WriteString(" will replace each.\n\n")
	b.WriteString("[o] overwrite all  [esc] back to picker  [q] cancel")
	return b.String()
}
