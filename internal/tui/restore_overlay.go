package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mi-subbotin/lazyagent/internal/backup"
)

// restorePhase enumerates the visual states of the restore overlay.
type restorePhase int

const (
	restorePhaseList restorePhase = iota
	restorePhaseDetail
	restorePhaseConfirmOverwrite
	restorePhaseConfirmDelete
)

// restoreOverlay is the modal state for browsing and restoring backup
// snapshots written by internal/backup. Z opens it from the tree.
type restoreOverlay struct {
	phase     restorePhase
	snapshots []backup.Snapshot

	listCursor   int
	detailCursor int

	// loadErr captures a List() failure so the overlay can show it
	// instead of silently rendering an empty list.
	loadErr error

	// pendingRestoreIdx is the item index inside the focused snapshot
	// whose restore is awaiting overwrite confirmation.
	pendingRestoreIdx int
}

// newRestoreOverlay reads snapshots from disk and returns the overlay
// in its initial list-view state. Errors from backup.List are kept on
// the overlay so the renderer can show them.
func newRestoreOverlay() *restoreOverlay {
	o := &restoreOverlay{phase: restorePhaseList}
	snaps, err := backup.List()
	if err != nil {
		o.loadErr = err
		return o
	}
	o.snapshots = snaps
	return o
}

func (m Model) updateRestoreOverlay(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	o := m.restoreOverlay
	switch o.phase {
	case restorePhaseConfirmDelete:
		return m.updateRestoreConfirmDelete(keyMsg)
	case restorePhaseConfirmOverwrite:
		return m.updateRestoreConfirmOverwrite(keyMsg)
	case restorePhaseDetail:
		return m.updateRestoreDetail(keyMsg)
	default:
		return m.updateRestoreList(keyMsg)
	}
}

func (m Model) updateRestoreList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := m.restoreOverlay
	switch msg.String() {
	case "esc", "q":
		m.restoreOverlay = nil
		return m, nil
	case "down", "j":
		if o.listCursor < len(o.snapshots)-1 {
			o.listCursor++
		}
		return m, nil
	case "up", "k":
		if o.listCursor > 0 {
			o.listCursor--
		}
		return m, nil
	case "enter":
		if len(o.snapshots) == 0 {
			return m, nil
		}
		o.phase = restorePhaseDetail
		o.detailCursor = 0
		return m, nil
	case "D":
		if len(o.snapshots) == 0 {
			return m, nil
		}
		o.phase = restorePhaseConfirmDelete
		return m, nil
	}
	return m, nil
}

func (m Model) updateRestoreDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := m.restoreOverlay
	if o.listCursor >= len(o.snapshots) {
		o.phase = restorePhaseList
		return m, nil
	}
	snap := o.snapshots[o.listCursor]
	switch msg.String() {
	case "esc":
		o.phase = restorePhaseList
		return m, nil
	case "q":
		m.restoreOverlay = nil
		return m, nil
	case "down", "j":
		if o.detailCursor < len(snap.Items)-1 {
			o.detailCursor++
		}
		return m, nil
	case "up", "k":
		if o.detailCursor > 0 {
			o.detailCursor--
		}
		return m, nil
	case "r":
		if o.detailCursor >= len(snap.Items) {
			return m, nil
		}
		it := snap.Items[o.detailCursor]
		if pathOccupied(it.Path) {
			o.pendingRestoreIdx = o.detailCursor
			o.phase = restorePhaseConfirmOverwrite
			return m, nil
		}
		return m.commitRestoreItem(snap.ID, o.detailCursor)
	case "R":
		return m.commitRestoreAll(snap.ID)
	}
	return m, nil
}

func (m Model) updateRestoreConfirmOverwrite(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := m.restoreOverlay
	switch msg.String() {
	case "esc", "n", "N":
		o.phase = restorePhaseDetail
		return m, nil
	case "q":
		m.restoreOverlay = nil
		return m, nil
	case "y", "Y", "enter":
		idx := o.pendingRestoreIdx
		snapID := o.snapshots[o.listCursor].ID
		return m.commitRestoreItem(snapID, idx)
	}
	return m, nil
}

func (m Model) updateRestoreConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := m.restoreOverlay
	switch msg.String() {
	case "esc", "n", "N":
		o.phase = restorePhaseList
		return m, nil
	case "q":
		m.restoreOverlay = nil
		return m, nil
	case "y", "Y", "enter":
		if o.listCursor >= len(o.snapshots) {
			o.phase = restorePhaseList
			return m, nil
		}
		id := o.snapshots[o.listCursor].ID
		if err := backup.Delete(id); err != nil {
			m.setToast("backup delete: " + err.Error())
			o.phase = restorePhaseList
			return m, nil
		}
		snaps, err := backup.List()
		if err != nil {
			o.loadErr = err
		} else {
			o.snapshots = snaps
			o.loadErr = nil
		}
		if o.listCursor >= len(o.snapshots) {
			o.listCursor = len(o.snapshots) - 1
			if o.listCursor < 0 {
				o.listCursor = 0
			}
		}
		o.phase = restorePhaseList
		m.setToast("snapshot deleted")
		return m, nil
	}
	return m, nil
}

func (m Model) commitRestoreItem(id string, idx int) (tea.Model, tea.Cmd) {
	o := m.restoreOverlay
	if o == nil || idx < 0 || idx >= len(o.snapshots[o.listCursor].Items) {
		return m, nil
	}
	it := o.snapshots[o.listCursor].Items[idx]
	if err := backup.Restore(id, idx); err != nil {
		m.setToast("restore: " + err.Error())
		o.phase = restorePhaseDetail
		return m, nil
	}
	m.invalidateBodyCache(it.Path)
	m.setToast(fmt.Sprintf("restored %s/%s", it.Kind, it.Name))
	o.phase = restorePhaseList
	m.loading = true
	return m, m.loadCmd()
}

func (m Model) commitRestoreAll(id string) (tea.Model, tea.Cmd) {
	o := m.restoreOverlay
	if err := backup.RestoreAll(id); err != nil {
		m.setToast("restore: " + err.Error())
		o.phase = restorePhaseDetail
		return m, nil
	}
	for _, it := range o.snapshots[o.listCursor].Items {
		m.invalidateBodyCache(it.Path)
	}
	m.setToast(fmt.Sprintf("restored %d items", len(o.snapshots[o.listCursor].Items)))
	o.phase = restorePhaseList
	m.loading = true
	return m, m.loadCmd()
}

// pathOccupied reports whether p currently exists on disk. Used to
// decide if a restore needs an overwrite confirmation.
func pathOccupied(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Lstat(p)
	if err == nil {
		return true
	}
	return !errors.Is(err, os.ErrNotExist)
}

// restoreOverlayText renders the overlay body for the current phase.
func restoreOverlayText(o restoreOverlay) string {
	switch o.phase {
	case restorePhaseConfirmDelete:
		return restoreConfirmDeleteText(o)
	case restorePhaseConfirmOverwrite:
		return restoreConfirmOverwriteText(o)
	case restorePhaseDetail:
		return restoreDetailText(o)
	default:
		return restoreListText(o)
	}
}

func restoreListText(o restoreOverlay) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("undo / restore — snapshots") + "\n\n")
	if o.loadErr != nil {
		b.WriteString(invalidStyle.Render("error: "+o.loadErr.Error()) + "\n\n")
		b.WriteString(dimStyle.Render("[esc] close"))
		return b.String()
	}
	if len(o.snapshots) == 0 {
		b.WriteString("No snapshots yet. Destructive actions auto-snapshot.\n\n")
		b.WriteString(dimStyle.Render("[esc] close"))
		return b.String()
	}
	now := time.Now()
	for i, s := range o.snapshots {
		cursor := "  "
		if i == o.listCursor {
			cursor = "> "
		}
		opStr := colorOp(s.Op)
		first := ""
		if len(s.Items) > 0 {
			first = s.Items[0].Name
		}
		line := fmt.Sprintf("%s%-10s  %s  %d items  %s",
			cursor, relTime(s.Created, now), opStr, len(s.Items), first)
		if i == o.listCursor {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("[enter] open  [D] delete  [esc/q] close"))
	return b.String()
}

func restoreDetailText(o restoreOverlay) string {
	var b strings.Builder
	if o.listCursor >= len(o.snapshots) {
		return "no snapshot selected"
	}
	s := o.snapshots[o.listCursor]
	b.WriteString(titleStyle.Render("snapshot "+s.ID) + "\n")
	b.WriteString(fmt.Sprintf("op %s  ·  %s  ·  %d items\n\n",
		colorOp(s.Op), relTime(s.Created, time.Now()), len(s.Items)))
	if len(s.Items) == 0 {
		b.WriteString(dimStyle.Render("(empty snapshot)") + "\n\n")
		b.WriteString(dimStyle.Render("[esc] back  [q] close"))
		return b.String()
	}
	for i, it := range s.Items {
		cursor := "  "
		if i == o.detailCursor {
			cursor = "> "
		}
		head := fmt.Sprintf("%s%s · %s · %s · %s", cursor, it.Kind, it.Name, it.Origin, it.Scope)
		if i == o.detailCursor {
			head = selectedStyle.Render(head)
		}
		b.WriteString(head + "\n")
		path := it.Path
		if it.ConfigKey != "" {
			path = path + " :: " + it.ConfigKey
		}
		b.WriteString("    " + dimStyle.Render(path) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("[r] restore item  [R] restore all  [esc] back  [q] close"))
	return b.String()
}

func restoreConfirmOverwriteText(o restoreOverlay) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("overwrite existing?") + "\n\n")
	if o.listCursor < len(o.snapshots) && o.pendingRestoreIdx < len(o.snapshots[o.listCursor].Items) {
		it := o.snapshots[o.listCursor].Items[o.pendingRestoreIdx]
		b.WriteString("  " + it.Name + dimStyle.Render(fmt.Sprintf("  (%s · %s · %s)", it.Origin, it.Kind, it.Scope)) + "\n")
		b.WriteString("  " + dimStyle.Render(it.Path) + "\n\n")
	}
	b.WriteString("Path is currently occupied. Restore will replace it.\n\n")
	b.WriteString(dimStyle.Render("y to confirm · n / esc to cancel"))
	return b.String()
}

func restoreConfirmDeleteText(o restoreOverlay) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("delete snapshot?") + "\n\n")
	if o.listCursor < len(o.snapshots) {
		s := o.snapshots[o.listCursor]
		b.WriteString(fmt.Sprintf("  %s  %s  %d items\n",
			s.ID, colorOp(s.Op), len(s.Items)))
		b.WriteString("  " + dimStyle.Render(relTime(s.Created, time.Now())) + "\n\n")
	}
	b.WriteString("Snapshot bytes will be removed. The original artifacts on disk are unaffected.\n\n")
	b.WriteString(dimStyle.Render("y to confirm · n / esc to cancel"))
	return b.String()
}

// colorOp paints the op tag with a hint colour matching its severity.
func colorOp(op string) string {
	switch op {
	case "delete":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#f7768e")).Render(op)
	case "place-overwrite":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68")).Render(op)
	case "resync-canonical", "resync-tool":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Render(op)
	default:
		return op
	}
}

// relTime renders a human-friendly approximation of t relative to now.
func relTime(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	if d < 0 {
		d = -d
	}
	switch {
	case d < 30*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 48*time.Hour:
		return "yesterday"
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}
