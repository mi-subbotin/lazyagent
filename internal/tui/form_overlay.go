// Form-mode editor for StorageEntry items (PRI-75).
//
// The form overlay is opened from the entry-mode JSON editor with
// ctrl+f, or directly when E is pressed on a kind we have a schema
// for. Each kind (MCP / Codex profile / Hook) provides a schema —
// an ordered list of field descriptors — that drives the rendering
// and the round-trip to/from a typed Go value the entry already uses
// in parse.WriteEntry.
//
// Why have a form at all when the JSON textarea already works:
// users who don't write JSON daily still need to add an MCP server
// or tweak a hook command without remembering trailing-comma rules.
// The form fronts the schema, validates per-field, and falls back to
// the JSON textarea when an entry's shape doesn't match the schema
// (legacy / hand-edited configs).
//
// State lives entirely in formOverlay; the textarea backing each
// scalar / multi-line field is a bubbles textinput.Model or
// textarea.Model, depending on shape. ListMode (lines vs fields)
// switches between the two presentations for args/env/headers and
// persists in state.json so the user only chooses once.

package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/state"
)

// fieldKind classifies a single form field's input shape. The
// rendering and key handling fan out from this.
type fieldKind int

const (
	fieldString    fieldKind = iota // single-line textinput
	fieldEnum                       // small set of choices, arrows cycle
	fieldStringList                 // []string — args
	fieldStringMap                  // map[string]string — env / headers
	fieldInt                        // single-line textinput, int-validated
)

// fieldSpec is the schema entry for one field. The form overlay walks
// a []fieldSpec at build time and again at save time.
type fieldSpec struct {
	Name     string   // JSON key as it appears in the entry
	Label    string   // user-facing label
	Kind     fieldKind
	Required bool
	Choices  []string // for fieldEnum
	Help     string   // one-line hint shown below the field when focused
	// VisibleIf allows fields to hide based on the current value of a
	// sibling field — used for MCP transport switching (command/args
	// only when type=stdio, url/headers only for sse/http).
	VisibleIf func(values map[string]any) bool
	// Validate returns "" on pass, a warning string on soft failure.
	// Hard failures (JSON-shape) are caught at save time, not here.
	Validate func(value any) string
}

// formSchema bundles a per-kind field list with a title and a
// from/to-entry transform pair. shapeMatches() reports whether an
// existing entry value matches the schema closely enough to populate
// the form; non-matching entries fall back to the JSON textarea.
type formSchema struct {
	Title  string
	Fields []fieldSpec
	// shapeMatches inspects an entry value (typically map[string]any)
	// and returns true if all required fields are present in
	// schema-expected types. Non-strict — extra fields are OK.
	shapeMatches func(entry any) bool
}

// formField is the live state for one rendered field. The overlay
// holds a []formField; index correlates with formSchema.Fields.
type formField struct {
	spec    fieldSpec
	visible bool

	// Exactly one of these is populated based on Kind.
	input    textinput.Model // string / int / enum (enum is textinput with arrows-only)
	textarea textarea.Model  // stringList/stringMap in lines mode
	// rows hold per-element inputs in fields mode. For stringList
	// each row is a single textinput; for stringMap each row is
	// (key, value) pair stored in keys/values.
	rowsKeys   []textinput.Model // for stringMap fields mode
	rowsValues []textinput.Model // for stringList: only this is used (rowsKeys nil)

	// enumIndex tracks the current selection for fieldEnum. Mirrors
	// input.Value() but used directly so the value doesn't have to
	// round-trip through string parsing.
	enumIndex int

	// warning is the latest soft-validation message for this field.
	// Re-computed on every keystroke and displayed in dim text.
	warning string
}

// formOverlay drives the form editor. focused is the index into
// fields; rowFocus is the index within a focused list/map field
// when in fields mode; modeFields toggles A↔B presentation.
type formOverlay struct {
	item     model.Item
	schema   formSchema
	fields   []formField
	focused  int
	rowFocus int

	// listMode mirrors state.EditorListMode so toggling within the
	// session takes effect immediately and the new value is written
	// back to state on close.
	listMode string // "lines" | "fields"

	// Save metadata — same shape as editorState. PRI-74 entry mode.
	path     string
	entryKey string

	// saveErr is the last hard error from saveEntry — surfaces as a
	// red footer. dirty tracks whether anything changed vs open-time
	// snapshot (used for the cancel-confirm).
	saveErr error
	initial map[string]any
}

// newFormOverlay builds a form for it using its schema. Returns
// (nil, false) if no schema matches — caller should fall back to the
// JSON textarea editor.
func newFormOverlay(it model.Item) (*formOverlay, bool) {
	sch, ok := schemaFor(it)
	if !ok {
		return nil, false
	}
	val, _, err := parse.ReadEntry(it.Path, it.ConfigKey)
	if err != nil {
		return nil, false
	}
	if !sch.shapeMatches(val) {
		return nil, false
	}
	st, _ := state.Load()
	mode := st.EditorListMode
	if mode != "fields" {
		mode = "lines"
	}
	values, _ := val.(map[string]any)
	if values == nil {
		values = map[string]any{}
	}
	f := &formOverlay{
		item:     it,
		schema:   sch,
		path:     it.Path,
		entryKey: it.ConfigKey,
		listMode: mode,
		initial:  cloneMap(values),
	}
	f.fields = buildFields(sch, values, mode)
	f.recomputeVisibility()
	f.validateAll()
	if len(f.fields) > 0 {
		f.fields[f.firstVisible()].input.Focus()
		f.focused = f.firstVisible()
	}
	return f, true
}

// schemaFor picks a schema by (Origin, Kind). Returns ok=false when
// no schema exists for the combination — JSON fallback is the right
// move there.
func schemaFor(it model.Item) (formSchema, bool) {
	switch it.Kind {
	case model.KindMCP:
		return mcpSchema(), true
	case model.KindHook:
		return hookSchema(), true
	}
	// Codex profiles share Kind=KindAgent? Need to check at impl
	// time — for now no schema returned, JSON fallback handles them.
	return formSchema{}, false
}

// firstVisible returns the index of the first visible field. Caller
// should always have at least one — schemas always start with a
// non-conditional field.
func (f *formOverlay) firstVisible() int {
	for i, fld := range f.fields {
		if fld.visible {
			return i
		}
	}
	return 0
}

// recomputeVisibility walks each field's VisibleIf against the
// current values and toggles the visible flag. Called after any
// change to a field that other fields depend on (e.g. MCP type).
func (f *formOverlay) recomputeVisibility() {
	values := f.collectValues()
	for i := range f.fields {
		fld := &f.fields[i]
		if fld.spec.VisibleIf == nil {
			fld.visible = true
			continue
		}
		fld.visible = fld.spec.VisibleIf(values)
	}
}

// collectValues snapshots the current form into a map suitable for
// VisibleIf evaluation and saveEntry serialization.
func (f *formOverlay) collectValues() map[string]any {
	out := map[string]any{}
	for _, fld := range f.fields {
		if !fld.visible {
			continue
		}
		switch fld.spec.Kind {
		case fieldString:
			s := strings.TrimSpace(fld.input.Value())
			if s != "" {
				out[fld.spec.Name] = s
			}
		case fieldInt:
			s := strings.TrimSpace(fld.input.Value())
			if s == "" {
				continue
			}
			var n int
			if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
				out[fld.spec.Name] = n
			}
		case fieldEnum:
			if fld.enumIndex >= 0 && fld.enumIndex < len(fld.spec.Choices) {
				out[fld.spec.Name] = fld.spec.Choices[fld.enumIndex]
			}
		case fieldStringList:
			out[fld.spec.Name] = readStringList(fld, f.listMode)
		case fieldStringMap:
			out[fld.spec.Name] = readStringMap(fld, f.listMode)
		}
	}
	return out
}

// dirty reports whether the form differs from its open-time snapshot.
func (f *formOverlay) dirty() bool {
	return !mapsEqual(f.initial, f.collectValues())
}

// saveEntry round-trips the form to JSON and writes via WriteEntry.
// Hard errors (parse.WriteEntry returning anything) are stored on
// saveErr and surfaced in the footer; the overlay stays open.
func (f *formOverlay) saveEntry() error {
	values := f.collectValues()
	// Round-trip through JSON to make sure marshaling will work and
	// to drop any non-canonical types (e.g. int vs float64 from JSON
	// numbers).
	data, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("marshal form values: %w", err)
	}
	var clean any
	if err := json.Unmarshal(data, &clean); err != nil {
		return fmt.Errorf("unmarshal form values: %w", err)
	}
	if err := parse.WriteEntry(f.path, f.entryKey, clean); err != nil {
		return err
	}
	// Save the user's preferred list mode so the next form open uses
	// it as the default.
	st, _ := state.Load()
	if st.EditorListMode != f.listMode {
		st.EditorListMode = f.listMode
		_ = state.Save(st)
	}
	return nil
}

// updateForm dispatches a key event to the form. Returns the model
// (possibly with overlay nil after esc/enter+save) and any cmd to
// run — bubbles textinput / textarea each return their own cmds.
func (m Model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.forming
	if f == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.forming = nil
		return m, nil
	case "ctrl+s":
		if err := f.saveEntry(); err != nil {
			f.saveErr = err
			return m, nil
		}
		m.forming = nil
		m.setToast("entry saved")
		return m, m.loadCmd()
	case "ctrl+m":
		// Toggle args/env presentation A↔B and rebuild row state.
		next := "fields"
		if f.listMode == "fields" {
			next = "lines"
		}
		f.listMode = next
		f.fields = buildFields(f.schema, f.collectValues(), next)
		f.recomputeVisibility()
		if f.focused >= len(f.fields) {
			f.focused = f.firstVisible()
		}
		f.fields[f.focused].input.Focus()
		return m, nil
	case "tab":
		f.advanceFocus(+1)
		return m, nil
	case "shift+tab":
		f.advanceFocus(-1)
		return m, nil
	}
	// Field-level dispatch — enum arrows, list row navigation, plain typing.
	return m.updateFormField(msg)
}

// advanceFocus moves the focus ring across visible fields, wrapping.
// Blurs the previous and focuses the next.
func (f *formOverlay) advanceFocus(dir int) {
	if len(f.fields) == 0 {
		return
	}
	f.fields[f.focused].input.Blur()
	for step := 0; step < len(f.fields); step++ {
		f.focused = (f.focused + dir + len(f.fields)) % len(f.fields)
		if f.fields[f.focused].visible {
			break
		}
	}
	f.rowFocus = 0
	f.fields[f.focused].input.Focus()
	f.fields[f.focused].textarea.Focus()
}

// validateAll runs each visible field's Validate against the
// current values map and stores the result on field.warning. Called
// after every key tick so messages stay in sync with the buffer.
func (f *formOverlay) validateAll() {
	values := f.collectValues()
	for i := range f.fields {
		fld := &f.fields[i]
		if !fld.visible || fld.spec.Validate == nil {
			fld.warning = ""
			continue
		}
		v, ok := values[fld.spec.Name]
		if !ok {
			fld.warning = ""
			continue
		}
		fld.warning = fld.spec.Validate(v)
	}
}

// updateFormField is the per-field key handler. Stub for now; filled
// by buildFields-aware logic in subsequent phases.
func (m Model) updateFormField(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.forming
	if f == nil || len(f.fields) == 0 {
		return m, nil
	}
	fld := &f.fields[f.focused]
	switch fld.spec.Kind {
	case fieldEnum:
		switch msg.String() {
		case "left", "h":
			if fld.enumIndex > 0 {
				fld.enumIndex--
			}
			fld.input.SetValue(fld.spec.Choices[fld.enumIndex])
			f.recomputeVisibility()
			return m, nil
		case "right", "l", " ":
			if fld.enumIndex < len(fld.spec.Choices)-1 {
				fld.enumIndex++
			}
			fld.input.SetValue(fld.spec.Choices[fld.enumIndex])
			f.recomputeVisibility()
			return m, nil
		}
	case fieldStringList, fieldStringMap:
		// Lines mode: feed the textarea directly. Fields mode: the
		// row-aware handler in buildFields takes over.
		if f.listMode == "lines" {
			var cmd tea.Cmd
			fld.textarea, cmd = fld.textarea.Update(msg)
			f.validateAll()
			return m, cmd
		}
		// fields mode handled in updateRowFocus (TODO Phase 2.2).
	}
	var cmd tea.Cmd
	fld.input, cmd = fld.input.Update(msg)
	f.validateAll()
	return m, cmd
}

// formView renders the entire form body. Pure-ish: it doesn't
// directly read styles outside lipgloss helpers, so unit tests can
// snapshot the output.
func formView(f *formOverlay) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(f.schema.Title) + "\n")
	b.WriteString(dimStyle.Render("  "+f.item.Path+" · "+f.entryKey) + "\n\n")
	for i, fld := range f.fields {
		if !fld.visible {
			continue
		}
		focused := i == f.focused
		b.WriteString(renderField(fld, focused, f.listMode))
		b.WriteString("\n")
	}
	if f.saveErr != nil {
		b.WriteString("\n" + errStyle.Render("save: "+f.saveErr.Error()) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render(
		"  ctrl+s save · tab next · shift+tab prev · ctrl+m toggle list mode ("+
			f.listMode+") · esc cancel"))
	return b.String()
}

// errStyle uses the package-wide invalid (red) palette so save
// errors stand out from the dim help text below.
var errStyle = invalidStyle
