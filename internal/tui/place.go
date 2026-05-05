package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/actions"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

type placePhase int

const (
	placePhasePick    placePhase = iota // user is editing the target matrix
	placePhaseConfirm                   // pre-flight surfaced conflicts; user decides overwrite
)

// placePicker drives the unified place overlay opened with `p`.
//
// Replaces four legacy overlays (copy / move / cross / share) with a
// single matrix of (Origin × Scope) checkboxes. The library is
// implicit — every Place enrolls bytes into ~/.lazyagent/library and
// projects them back to each ticked cell. Unticking a cell removes
// that projection; an empty matrix is the "git stash" state (item
// lives only in the library).
//
// Two-phase like the legacy share flow: pick → optional confirm when
// PlaceConflicts surfaces unrelated content at any target path.
type placePicker struct {
	item      model.Item
	origins   []model.Origin
	scopes    []model.Scope
	cells     [][]placeCell // [row=origin][col=scope]
	cursorRow int
	cursorCol int
	phase     placePhase
	conflicts []actions.ShareConflict
	pending   []actions.ProjectionTarget
}

// placeCell is one (Origin, Scope) checkbox in the picker matrix.
// Disabled cells display the `reason` next to the cell instead of the
// checkbox; user input on them is ignored. Lossy cells stay enabled
// after PRI-68 but carry a `lossy` flag the renderer adds as a
// suffix on the row's reason column.
type placeCell struct {
	target  actions.ProjectionTarget
	enabled bool
	checked bool
	lossy   bool
	reason  string
}

// newPlacePicker builds the overlay from the current on-disk state.
// Pre-checks every cell that already projects to the same canonical
// item; for first-time place (item not yet in library) the source's
// own (Origin, Scope) is pre-checked so the user does not accidentally
// orphan the source by hitting enter immediately.
func newPlacePicker(it model.Item, projectDir string) (*placePicker, error) {
	if !actions.CanPlace(it) {
		return nil, fmt.Errorf("can't place %s items of this shape (yet)", it.Kind)
	}

	current := map[actions.ProjectionTarget]bool{}
	for _, t := range actions.CurrentPlaceProjections(it, projectDir) {
		current[t] = true
	}

	origins := []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini}
	scopes := []model.Scope{model.ScopeGlobal, model.ScopeLocal}
	// Reverse-lossy sources (codex profile, gemini TOML) wear a
	// StorageEntry / StorageFile-with-toml shape but Place takes them
	// down the lossless library path via promoteToLibrary, so the
	// picker treats them like non-entry items. PRI-71.
	isEntry := it.Storage == model.StorageEntry && !actions.IsLossyReverseSource(it)
	cells := make([][]placeCell, len(origins))
	for r, o := range origins {
		cells[r] = make([]placeCell, len(scopes))
		for c, s := range scopes {
			target := actions.ProjectionTarget{Origin: o, Scope: s}
			cell := placeCell{target: target}
			switch {
			case isEntry && !actions.CanPlaceEntryTo(it, o):
				cell.reason = "cross-tool entry: PRI-68"
			case !isEntry && !actions.CanPlaceTo(it.Kind, o):
				cell.reason = "no projection for this combo"
			case s == model.ScopeLocal && projectDir == "":
				cell.reason = "no project local scope"
			default:
				cell.enabled = true
				cell.checked = current[target]
				cell.lossy = !isEntry && actions.IsLossyProjection(it.Kind, o)
			}
			cells[r][c] = cell
		}
	}

	// First-time place: keep the source's own cell pre-checked so a
	// thoughtless enter doesn't move the item out from under the user.
	if len(current) == 0 {
		for r, o := range origins {
			if o != it.Origin {
				continue
			}
			for c, s := range scopes {
				if s != it.Scope {
					continue
				}
				if cells[r][c].enabled {
					cells[r][c].checked = true
				}
			}
		}
	}

	cr, cc := firstEnabledCell(cells)
	return &placePicker{
		item:      it,
		origins:   origins,
		scopes:    scopes,
		cells:     cells,
		cursorRow: cr,
		cursorCol: cc,
	}, nil
}

func firstEnabledCell(cells [][]placeCell) (int, int) {
	for r := range cells {
		for c := range cells[r] {
			if cells[r][c].enabled {
				return r, c
			}
		}
	}
	return 0, 0
}

// updatePlacePicker fans out by phase: the pick phase is the matrix
// editor, the confirm phase asks for overwrite.
func (m Model) updatePlacePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.placePicker.phase == placePhaseConfirm {
		return m.updatePlaceConfirm(msg)
	}
	return m.updatePlacePick(msg)
}

func (m Model) updatePlacePick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.placePicker
	switch msg.String() {
	case "esc", "q":
		m.placePicker = nil
		return m, nil
	case "up", "k":
		if p.cursorRow > 0 {
			p.cursorRow--
		}
		return m, nil
	case "down", "j":
		if p.cursorRow < len(p.cells)-1 {
			p.cursorRow++
		}
		return m, nil
	case "left", "h":
		if p.cursorCol > 0 {
			p.cursorCol--
		}
		return m, nil
	case "right", "l":
		if p.cursorCol < len(p.cells[p.cursorRow])-1 {
			p.cursorCol++
		}
		return m, nil
	case " ", "x":
		cell := &p.cells[p.cursorRow][p.cursorCol]
		if cell.enabled {
			cell.checked = !cell.checked
		}
		return m, nil
	case "enter":
		var targets []actions.ProjectionTarget
		for _, row := range p.cells {
			for _, c := range row {
				if c.enabled && c.checked {
					targets = append(targets, c.target)
				}
			}
		}
		opts := actions.PlaceOpts{ProjectDir: m.projectDir}
		cs, err := actions.PlaceConflicts(p.item, targets, opts)
		if err != nil {
			m.setToast("place: " + err.Error())
			return m, nil
		}
		if len(cs) > 0 {
			p.phase = placePhaseConfirm
			p.conflicts = cs
			p.pending = targets
			return m, nil
		}
		return m.commitPlace(targets, false)
	}
	return m, nil
}

func (m Model) updatePlaceConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "n", "N":
		// Return to the picker so the user can untick the
		// conflicting cell instead of cancelling outright.
		m.placePicker.phase = placePhasePick
		m.placePicker.conflicts = nil
		m.placePicker.pending = nil
		return m, nil
	case "q":
		m.placePicker = nil
		return m, nil
	case "o", "O", "y", "Y", "enter":
		targets := m.placePicker.pending
		return m.commitPlace(targets, true)
	}
	return m, nil
}

func (m Model) commitPlace(targets []actions.ProjectionTarget, overwrite bool) (tea.Model, tea.Cmd) {
	it := m.placePicker.item
	m.placePicker = nil

	opts := actions.PlaceOpts{ProjectDir: m.projectDir, Overwrite: overwrite}
	if err := actions.Place(it, targets, opts); err != nil {
		m.setToast("place: " + err.Error())
		return m, nil
	}
	if len(targets) == 0 {
		m.setToast(fmt.Sprintf("placed %s — library only", it.Name))
	} else {
		m.setToast(fmt.Sprintf("placed %s → %s", it.Name, joinPlaceTargets(targets)))
	}
	m.invalidateBodyCache(it.Path)
	m.loading = true
	return m, m.loadCmd()
}

func joinPlaceTargets(ts []actions.ProjectionTarget) string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.String())
	}
	return strings.Join(names, ", ")
}

// placePickerText renders the overlay body. Two layouts: the picking
// phase shows the Origin×Scope matrix with per-cell checkboxes; the
// confirm phase lists the conflicts that overwrite would replace.
func placePickerText(p placePicker) string {
	if p.phase == placePhaseConfirm {
		return placeConfirmText(p)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Place %s (%s):\n\n", p.item.Name, p.item.Kind)
	if p.item.Storage == model.StorageEntry {
		b.WriteString("Library: n/a  (entries live inside per-tool config files)\n\n")
	} else {
		b.WriteString("Library: yes  (canonical bytes — for optimization)\n\n")
	}

	// Header row.
	fmt.Fprintf(&b, "  %-9s", "")
	for _, s := range p.scopes {
		fmt.Fprintf(&b, " %-8s", s)
	}
	b.WriteString("\n")

	// Cells.
	for r, row := range p.cells {
		fmt.Fprintf(&b, "  %-9s", p.origins[r])
		for c, cell := range row {
			cursor := " "
			if r == p.cursorRow && c == p.cursorCol {
				cursor = "▸"
			}
			mark := "[ ]"
			switch {
			case !cell.enabled:
				mark = "  -"
			case cell.checked:
				mark = "[x]"
			}
			fmt.Fprintf(&b, " %s%-7s", cursor, mark)
		}
		// Disabled-cell reason — append once per row if any cell
		// disabled with a reason; keeps the matrix tight. Lossy cells
		// stay enabled but get a `(lossy)` annotation so users know
		// edits at the target don't survive Resync canonical-wins.
		if reason := firstReason(row); reason != "" {
			b.WriteString("  — " + reason)
		} else if anyLossy(row) {
			b.WriteString("  — (lossy)")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n[arrows] move  [space] toggle  [enter] apply  [esc] cancel")
	return b.String()
}

func firstReason(row []placeCell) string {
	for _, c := range row {
		if !c.enabled && c.reason != "" {
			return c.reason
		}
	}
	return ""
}

func anyLossy(row []placeCell) bool {
	for _, c := range row {
		if c.lossy {
			return true
		}
	}
	return false
}

func placeConfirmText(p placePicker) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Overwrite %d existing item(s)?\n\n", len(p.conflicts))
	for _, c := range p.conflicts {
		fmt.Fprintf(&b, "  %s — %s at %s\n", c.Target, c.Kind, c.Path)
	}
	b.WriteString("\nThe library copy of ")
	b.WriteString(p.item.Name)
	b.WriteString(" will replace each.\n\n")
	b.WriteString("[o] overwrite all  [esc] back to picker  [q] cancel")
	return b.String()
}
