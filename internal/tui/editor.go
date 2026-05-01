package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

// editorState owns the bubbles textarea and the metadata the save flow
// needs (open-time mtime for conflict detection, original bytes for
// dirty tracking, and a conflict flag that triggers the resolution
// overlay).
//
// PRI-61: when the item is a Hook entry the editor opens the inner-hook
// JSON map only — not the whole settings.json — so users edit just the
// command/timeout/type/etc fields they care about. Save parses the
// buffer back to JSON and routes through parse.WriteEntry instead of
// SaveFile.
type editorState struct {
	item    model.Item
	path    string
	ta      textarea.Model
	openMT  time.Time
	initial string

	// entryMode is set for StorageEntry items (currently only Hook).
	// entryKey carries the slash-joined ConfigKey used by parse.WriteEntry.
	entryMode bool
	entryKey  string

	// conflict is set when SaveFile returned ErrConflict; the next
	// keystroke is interpreted by the conflict-resolution branch
	// rather than passed to the textarea.
	conflict bool
}

// newEditorState reads the file at it.Path into a textarea ready to
// edit. Returns an error if the file can't be read or stat'd. The
// caller should call resize once the terminal dimensions are known.
//
// For Hook entries the buffer holds the inner-hook map's JSON instead
// of the full settings.json — fewer characters to skim, no risk of
// breaking unrelated hooks. Other StorageEntry kinds still fall through
// to the file-mode editor (via 'e') for now.
func newEditorState(it model.Item) (*editorState, error) {
	if it.Kind == model.KindHook && it.Storage == model.StorageEntry {
		return newHookEntryEditor(it)
	}
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

// newHookEntryEditor opens the editor in entry mode: the buffer is the
// JSON-encoded inner-hook map at ConfigKey, not the surrounding
// settings.json. Save will parse the buffer back to JSON and call
// parse.WriteEntry; mtime tracking still uses the underlying file so
// concurrent edits to other hooks in the same file get caught.
func newHookEntryEditor(it model.Item) (*editorState, error) {
	val, _, err := parse.ReadEntry(it.Path, it.ConfigKey)
	if err != nil {
		return nil, err
	}
	pretty, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return nil, err
	}
	mt, err := actionsFileMtime(it.Path)
	if err != nil {
		return nil, err
	}
	body := string(pretty) + "\n"

	ta := textarea.New()
	ta.SetValue(body)
	ta.ShowLineNumbers = true
	ta.Focus()
	ta.CursorStart()

	return &editorState{
		item:      it,
		path:      it.Path,
		ta:        ta,
		openMT:    mt,
		initial:   body,
		entryMode: true,
		entryKey:  it.ConfigKey,
	}, nil
}

// saveEntry parses the textarea buffer as JSON and writes the result
// back at entryKey via parse.WriteEntry. Returns a clear error string
// when the buffer is not valid JSON (rendered as a toast — the user
// keeps editing and tries again).
func (e *editorState) saveEntry() error {
	var v any
	if err := json.Unmarshal([]byte(e.ta.Value()), &v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return parse.WriteEntry(e.path, e.entryKey, v)
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
