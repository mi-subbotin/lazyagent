package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/actions"
	"github.com/mi-subbotin/lazyagent/internal/doctor"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

// doctorPhase enumerates the visual states of the doctor overlay.
type doctorPhase int

const (
	doctorPhaseList doctorPhase = iota
	doctorPhaseConfirm
	doctorPhaseDone
)

// doctorSection groups rows under a header in the list view.
type doctorSection string

const (
	docSectionDuplicates doctorSection = "Duplicates"
	docSectionUnused     doctorSection = "Unused"
	docSectionOther      doctorSection = "Other"
)

// doctorRow is one suggestion line in the overlay. Actionable rows
// (Duplicates / Unused) carry the resolved items so apply can route
// straight into actions.Place / actions.Delete without re-matching.
// Other rows are display-only and can't be toggled.
type doctorRow struct {
	section    doctorSection
	title      string
	sub        string
	checked    bool
	actionable bool
	// For Duplicates: items that share the suggested duplicate names.
	// For Unused: items resolved from name (+ kind hint when present).
	items []model.Item
}

// doctorOverlay drives the `!` doctor recommendations overlay (PRI-97).
// Read-only by default: rows are pre-computed from the most recent
// `lazyagent doctor` run on disk; checkboxes opt rows in to apply.
// Confirmation is mandatory before any write.
type doctorOverlay struct {
	phase   doctorPhase
	rec     doctor.Recommendations
	id      string
	rows    []doctorRow
	cursor  int
	loadErr error

	// Apply summary, populated when phase = doctorPhaseDone.
	applied int
	failed  int
	errMsgs []string
}

// newDoctorOverlay reads the latest doctor recommendations from
// ~/.lazyagent and resolves each suggestion against the current items
// slice. Errors from doctor.Latest (including ErrNoRecommendations)
// are kept on the overlay so the renderer can show a useful message.
func newDoctorOverlay(items []model.Item) *doctorOverlay {
	o := &doctorOverlay{phase: doctorPhaseList}
	id, rec, err := doctor.Latest()
	if err != nil {
		o.loadErr = err
		return o
	}
	o.id = id
	o.rec = rec
	o.rows = buildDoctorRows(rec, items)
	return o
}

// buildDoctorRows resolves recommendation entries against the loaded
// items. A suggestion that doesn't match any item still gets a row, but
// it's marked non-actionable so it can't be checked.
func buildDoctorRows(rec doctor.Recommendations, items []model.Item) []doctorRow {
	var rows []doctorRow
	for _, d := range rec.Duplicates {
		var matches []model.Item
		seen := map[string]bool{}
		for _, name := range d.Names {
			for _, it := range items {
				if it.Name != name {
					continue
				}
				if it.Origin == model.OriginShared {
					continue
				}
				if it.Kind == model.KindSession || it.Kind == model.KindMemory {
					continue
				}
				key := fmt.Sprintf("%s|%s|%s|%s", it.Kind, it.Origin, it.Scope, it.Name)
				if seen[key] {
					continue
				}
				seen[key] = true
				matches = append(matches, it)
			}
		}
		rows = append(rows, doctorRow{
			section:    docSectionDuplicates,
			title:      strings.Join(d.Names, ", "),
			sub:        d.Reason,
			actionable: len(matches) >= 2,
			items:      matches,
		})
	}
	for _, u := range rec.Unused {
		var matches []model.Item
		for _, it := range items {
			if it.Name != u.Name {
				continue
			}
			if u.Kind != "" && !kindLabelMatches(it.Kind, u.Kind) {
				continue
			}
			if it.Kind == model.KindSession || it.Kind == model.KindMemory {
				continue
			}
			matches = append(matches, it)
		}
		title := u.Name
		if u.Kind != "" {
			title += " (" + u.Kind + ")"
		}
		rows = append(rows, doctorRow{
			section:    docSectionUnused,
			title:      title,
			sub:        u.Reason,
			actionable: len(matches) >= 1,
			items:      matches,
		})
	}
	for _, o := range rec.Other {
		rows = append(rows, doctorRow{
			section:    docSectionOther,
			title:      o.Title,
			sub:        o.Body,
			actionable: false,
		})
	}
	return rows
}

func (m Model) updateDoctorOverlay(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	o := m.doctorOverlay
	switch o.phase {
	case doctorPhaseConfirm:
		return m.updateDoctorConfirm(keyMsg)
	case doctorPhaseDone:
		return m.updateDoctorDone(keyMsg)
	default:
		return m.updateDoctorList(keyMsg)
	}
}

func (m Model) updateDoctorList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := m.doctorOverlay
	switch msg.String() {
	case "esc", "q":
		m.doctorOverlay = nil
		return m, nil
	case "down", "j":
		if o.cursor < len(o.rows)-1 {
			o.cursor++
		}
		return m, nil
	case "up", "k":
		if o.cursor > 0 {
			o.cursor--
		}
		return m, nil
	case " ", "x":
		if o.cursor >= len(o.rows) {
			return m, nil
		}
		row := &o.rows[o.cursor]
		if !row.actionable {
			return m, nil
		}
		row.checked = !row.checked
		return m, nil
	case "a":
		toggle := !allActionableChecked(o.rows)
		for i := range o.rows {
			if o.rows[i].actionable {
				o.rows[i].checked = toggle
			}
		}
		return m, nil
	case "y", "enter":
		if countChecked(o.rows) == 0 {
			m.setToast("doctor: nothing selected")
			return m, nil
		}
		o.phase = doctorPhaseConfirm
		return m, nil
	}
	return m, nil
}

func (m Model) updateDoctorConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := m.doctorOverlay
	switch msg.String() {
	case "esc", "n", "N":
		o.phase = doctorPhaseList
		return m, nil
	case "q":
		m.doctorOverlay = nil
		return m, nil
	case "y", "Y", "enter":
		applied, failed, errs := m.applyDoctorRows(o.rows)
		o.applied = applied
		o.failed = failed
		o.errMsgs = errs
		o.phase = doctorPhaseDone
		if applied > 0 {
			for k := range m.glamourCache {
				delete(m.glamourCache, k)
			}
		}
		return m, nil
	}
	return m, nil
}

func (m Model) updateDoctorDone(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := m.doctorOverlay
	switch msg.String() {
	case "esc", "q", "enter":
		applied := o.applied
		m.doctorOverlay = nil
		if applied > 0 {
			m.loading = true
			m.setToast(fmt.Sprintf("doctor: applied %d", applied))
			return m, m.loadCmd()
		}
		return m, nil
	}
	return m, nil
}

// applyDoctorRows walks the checked rows in order and dispatches each
// to actions.Place (Duplicates) or actions.Delete (Unused). Returns
// counts plus any error strings; per-row failures don't abort the run.
func (m Model) applyDoctorRows(rows []doctorRow) (int, int, []string) {
	var applied, failed int
	var errs []string
	for _, row := range rows {
		if !row.checked || !row.actionable {
			continue
		}
		switch row.section {
		case docSectionDuplicates:
			if err := m.applyDoctorDuplicate(row); err != nil {
				failed++
				errs = append(errs, fmt.Sprintf("dup %q: %s", row.title, err.Error()))
				continue
			}
			applied++
		case docSectionUnused:
			if err := m.applyDoctorUnused(row); err != nil {
				failed++
				errs = append(errs, fmt.Sprintf("unused %q: %s", row.title, err.Error()))
				continue
			}
			applied++
		}
	}
	return applied, failed, errs
}

// applyDoctorDuplicate picks a winner (first non-Shared match) and
// projects it to the union of every group member's (Origin, Scope).
// Place auto-snapshots displaced bytes via PRI-92.
func (m Model) applyDoctorDuplicate(row doctorRow) error {
	if len(row.items) < 2 {
		return errors.New("need at least 2 matched items")
	}
	winner := row.items[0]
	seen := map[actions.ProjectionTarget]bool{}
	var targets []actions.ProjectionTarget
	for _, it := range row.items {
		if it.Origin == model.OriginShared {
			continue
		}
		t := actions.ProjectionTarget{Origin: it.Origin, Scope: it.Scope}
		if seen[t] {
			continue
		}
		seen[t] = true
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		return errors.New("no projection targets resolved")
	}
	opts := actions.PlaceOpts{ProjectDir: m.projectDir, Overwrite: true}
	return actions.Place(winner, targets, opts)
}

// applyDoctorUnused deletes every matched item under one suggestion.
// actions.Delete auto-snapshots so a botched suggestion is recoverable
// from the Z overlay.
func (m Model) applyDoctorUnused(row doctorRow) error {
	if len(row.items) == 0 {
		return errors.New("no matched items")
	}
	for _, it := range row.items {
		if err := actions.Delete(it); err != nil {
			return err
		}
	}
	return nil
}

// kindLabelMatches accepts the LLM's freeform Kind hint loosely:
// case-insensitive, and tolerant of singular ↔ plural ("Agent" vs
// "Agents"). The lazyagent canonical form is plural ("Skills",
// "Agents", "Commands"), but doctor prompts don't pin it down so the
// LLM can drift to singular.
func kindLabelMatches(k model.Kind, hint string) bool {
	canonical := strings.ToLower(k.String())
	got := strings.ToLower(strings.TrimSpace(hint))
	if got == "" {
		return true
	}
	if got == canonical {
		return true
	}
	// "Agent" → "Agents" or "Agents" → "Agent" (single trailing s).
	if got+"s" == canonical || canonical+"s" == got {
		return true
	}
	return false
}

func countChecked(rows []doctorRow) int {
	n := 0
	for _, r := range rows {
		if r.checked && r.actionable {
			n++
		}
	}
	return n
}

func allActionableChecked(rows []doctorRow) bool {
	any := false
	for _, r := range rows {
		if !r.actionable {
			continue
		}
		any = true
		if !r.checked {
			return false
		}
	}
	return any
}

// doctorOverlayText renders the overlay body for the current phase.
func doctorOverlayText(o doctorOverlay) string {
	switch o.phase {
	case doctorPhaseConfirm:
		return doctorConfirmText(o)
	case doctorPhaseDone:
		return doctorDoneText(o)
	default:
		return doctorListText(o)
	}
}

func doctorListText(o doctorOverlay) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("doctor — recommendations") + "\n\n")
	if o.loadErr != nil {
		if errors.Is(o.loadErr, doctor.ErrNoRecommendations) {
			b.WriteString("No recommendations on disk yet.\n")
			b.WriteString(dimStyle.Render("Run `lazyagent doctor` to generate suggestions.") + "\n\n")
			b.WriteString(dimStyle.Render("[esc] close"))
			return b.String()
		}
		b.WriteString(invalidStyle.Render("error: "+o.loadErr.Error()) + "\n\n")
		b.WriteString(dimStyle.Render("[esc] close"))
		return b.String()
	}
	if len(o.rows) == 0 {
		b.WriteString("Doctor returned no suggestions. Tree looks clean.\n\n")
		b.WriteString(dimStyle.Render("[esc] close"))
		return b.String()
	}
	b.WriteString(dimStyle.Render(fmt.Sprintf("snapshot %s · cli %s", o.id, o.rec.CLI)) + "\n\n")

	var lastSection doctorSection
	for i, r := range o.rows {
		if r.section != lastSection {
			b.WriteString("\n" + titleStyle.Render(string(r.section)) + "\n")
			lastSection = r.section
		}
		cursor := "  "
		if i == o.cursor {
			cursor = "> "
		}
		check := "[ ]"
		if !r.actionable {
			check = "[-]"
		} else if r.checked {
			check = "[x]"
		}
		head := fmt.Sprintf("%s%s %s", cursor, check, r.title)
		if i == o.cursor {
			head = selectedStyle.Render(head)
		}
		b.WriteString(head + "\n")
		if r.sub != "" {
			b.WriteString("    " + dimStyle.Render(truncRunes(r.sub, 100)) + "\n")
		}
	}
	checked := countChecked(o.rows)
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf(
		"%d selected · [space/x] toggle · [a] toggle all · [y/enter] apply · [esc/q] close",
		checked,
	)))
	return b.String()
}

func doctorConfirmText(o doctorOverlay) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("apply doctor recommendations?") + "\n\n")
	dups, unused := 0, 0
	for _, r := range o.rows {
		if !r.checked || !r.actionable {
			continue
		}
		switch r.section {
		case docSectionDuplicates:
			dups++
		case docSectionUnused:
			unused++
		}
	}
	if dups > 0 {
		b.WriteString(fmt.Sprintf("  %d duplicate group(s) — winner overwrites peers via Place\n", dups))
	}
	if unused > 0 {
		b.WriteString(fmt.Sprintf("  %d unused suggestion(s) — items will be deleted\n", unused))
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Both ops auto-snapshot (PRI-92). Use Z to restore if needed.") + "\n\n")
	b.WriteString(dimStyle.Render("y to confirm · n / esc to go back"))
	return b.String()
}

func doctorDoneText(o doctorOverlay) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("doctor — done") + "\n\n")
	b.WriteString(fmt.Sprintf("  applied: %d\n", o.applied))
	b.WriteString(fmt.Sprintf("  failed:  %d\n\n", o.failed))
	if len(o.errMsgs) > 0 {
		b.WriteString(invalidStyle.Render("errors:") + "\n")
		for _, e := range o.errMsgs {
			b.WriteString("  " + e + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(dimStyle.Render("[esc/enter] close"))
	return b.String()
}
