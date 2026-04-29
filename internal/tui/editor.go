package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// editorState owns the bubbles textarea and the metadata the save flow
// needs (open-time mtime for conflict detection, original bytes for
// dirty tracking, and a conflict flag that triggers the resolution
// overlay).
type editorState struct {
	item    model.Item
	path    string
	ta      textarea.Model
	openMT  time.Time
	initial string

	// conflict is set when SaveFile returned ErrConflict; the next
	// keystroke is interpreted by the conflict-resolution branch
	// rather than passed to the textarea.
	conflict bool
}

// newEditorState reads the file at it.Path into a textarea ready to
// edit. Returns an error if the file can't be read or stat'd. The
// caller should call resize once the terminal dimensions are known.
func newEditorState(it model.Item) (*editorState, error) {
	data, err := os.ReadFile(it.Path)
	if err != nil {
		return nil, err
	}
	mt, err := actionsFileMtime(it.Path)
	if err != nil {
		return nil, err
	}

	ta := textarea.New()
	ta.SetValue(string(data))
	ta.ShowLineNumbers = true
	ta.Focus()
	// Move cursor to top — bubbles defaults to end-of-buffer which is
	// jarring for a "just opened a config" feel.
	ta.CursorStart()

	return &editorState{
		item:    it,
		path:    it.Path,
		ta:      ta,
		openMT:  mt,
		initial: string(data),
	}, nil
}

// dirty reports whether the buffer differs from what we read at open.
// Used by the cancel flow to ask "discard changes?" before throwing
// edits away.
func (e *editorState) dirty() bool {
	return e.ta.Value() != e.initial
}

func (e *editorState) resize(w, h int) {
	// Reserve two rows for header + footer hints.
	if h < 6 {
		h = 6
	}
	e.ta.SetWidth(w)
	e.ta.SetHeight(h - 4)
}

// editorView renders the full-screen editor body: header with path and
// dirty marker, the textarea itself, and a footer hint line. The
// conflict overlay (when active) takes over the body entirely.
func editorView(e *editorState) string {
	dirty := ""
	if e.dirty() {
		dirty = " *"
	}
	header := titleStyle.Render(fmt.Sprintf("edit %s · %s · %s%s",
		e.item.Origin, e.item.Kind, e.item.Scope, dirty)) + "\n" +
		dimStyle.Render("  "+e.path) + "\n"

	if e.conflict {
		body := strings.Join([]string{
			"",
			titleStyle.Render("  conflict — file changed on disk since you opened it"),
			"",
			"  [o] overwrite (lose the on-disk version)",
			"  [r] reload (lose your edits)",
			"  [esc] keep editing (try save again later)",
			"",
		}, "\n")
		return header + body
	}

	footer := dimStyle.Render(
		"  ctrl+s save · esc cancel · ctrl+r reload from disk")
	return header + e.ta.View() + "\n" + footer
}

// actionsFileMtime is a thin wrapper so tui doesn't import actions just
// for one helper — the import would create a cycle once actions starts
// importing tui types in later phases.
func actionsFileMtime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}
