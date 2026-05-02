// Field-rendering and -building helpers for the form overlay
// (PRI-75). Kept separate from form_overlay.go so the file with the
// state machine doesn't blur with the per-field UI plumbing.
//
// buildFields takes a schema + an existing values map and returns a
// []formField ready to render. It honors listMode at build time so
// list/map fields end up with either a textarea (lines) or a slice
// of textinputs (fields). The form rebuilds all fields when the
// listMode toggle fires, copying current values out of one shape and
// into the other.

package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
)

// buildFields constructs the live []formField for a schema.
// existing is the entry's current values (already validated against
// the schema by shapeMatches), listMode is the current preference
// for stringList/stringMap presentation.
func buildFields(sch formSchema, existing map[string]any, listMode string) []formField {
	out := make([]formField, 0, len(sch.Fields))
	for _, spec := range sch.Fields {
		fld := formField{spec: spec, visible: true}
		switch spec.Kind {
		case fieldString:
			fld.input = newInput(spec, existingString(existing, spec.Name))
		case fieldInt:
			fld.input = newInput(spec, fmt.Sprint(existingInt(existing, spec.Name)))
		case fieldEnum:
			cur := existingString(existing, spec.Name)
			fld.enumIndex = indexOf(spec.Choices, cur)
			if fld.enumIndex < 0 {
				fld.enumIndex = 0
			}
			fld.input = newInput(spec, spec.Choices[fld.enumIndex])
			// Enums are read-only inputs — the value comes from the
			// arrow handler. SetCharLimit(0) keeps users from typing.
			fld.input.CharLimit = 0
		case fieldStringList:
			items := existingStringList(existing, spec.Name)
			if listMode == "fields" {
				fld.rowsValues = newRowInputs(items)
			} else {
				fld.textarea = newTextarea(strings.Join(items, "\n"))
			}
		case fieldStringMap:
			m := existingStringMap(existing, spec.Name)
			if listMode == "fields" {
				keys := sortedKeys(m)
				fld.rowsKeys = newRowInputs(keys)
				vals := make([]string, len(keys))
				for i, k := range keys {
					vals[i] = m[k]
				}
				fld.rowsValues = newRowInputs(vals)
			} else {
				lines := make([]string, 0, len(m))
				for _, k := range sortedKeys(m) {
					lines = append(lines, k+"="+m[k])
				}
				fld.textarea = newTextarea(strings.Join(lines, "\n"))
			}
		}
		out = append(out, fld)
	}
	return out
}

func newInput(spec fieldSpec, value string) textinput.Model {
	ti := textinput.New()
	ti.SetValue(value)
	ti.Placeholder = spec.Label
	ti.CharLimit = 256
	ti.Width = 50
	return ti
}

func newTextarea(value string) textarea.Model {
	ta := textarea.New()
	ta.SetValue(value)
	ta.SetHeight(6)
	ta.SetWidth(50)
	ta.ShowLineNumbers = false
	return ta
}

func newRowInputs(values []string) []textinput.Model {
	out := make([]textinput.Model, len(values))
	for i, v := range values {
		ti := textinput.New()
		ti.SetValue(v)
		ti.CharLimit = 256
		ti.Width = 40
		out[i] = ti
	}
	return out
}

// renderField formats one field as text. focused shows a "▸ " cursor
// prefix; unfocused fields just align with two spaces.
func renderField(fld formField, focused bool, listMode string) string {
	prefix := "  "
	if focused {
		prefix = titleStyle.Render("▸ ")
	}
	var b strings.Builder
	label := fld.spec.Label
	if fld.spec.Required {
		label += dimStyle.Render(" *")
	}
	b.WriteString(prefix + label + "\n")
	switch fld.spec.Kind {
	case fieldString, fieldInt:
		b.WriteString("    " + fld.input.View())
	case fieldEnum:
		b.WriteString("    " + renderEnum(fld))
	case fieldStringList:
		b.WriteString(renderStringList(fld, listMode))
	case fieldStringMap:
		b.WriteString(renderStringMap(fld, listMode))
	}
	if focused && fld.spec.Help != "" {
		b.WriteString("\n    " + dimStyle.Render(fld.spec.Help))
	}
	if fld.warning != "" {
		b.WriteString("\n    " + dimStyle.Render("⚠ "+fld.warning))
	}
	return b.String()
}

func renderEnum(fld formField) string {
	parts := make([]string, len(fld.spec.Choices))
	for i, ch := range fld.spec.Choices {
		if i == fld.enumIndex {
			parts[i] = titleStyle.Render("[" + ch + "]")
		} else {
			parts[i] = dimStyle.Render(ch)
		}
	}
	return strings.Join(parts, "  ")
}

func renderStringList(fld formField, listMode string) string {
	if listMode == "fields" {
		var b strings.Builder
		for i, ti := range fld.rowsValues {
			fmt.Fprintf(&b, "    %2d  %s\n", i+1, ti.View())
		}
		b.WriteString("    " + dimStyle.Render("[+ enter to add a row]"))
		return b.String()
	}
	return "    " + indent(fld.textarea.View(), "    ")
}

func renderStringMap(fld formField, listMode string) string {
	if listMode == "fields" {
		var b strings.Builder
		for i := range fld.rowsKeys {
			fmt.Fprintf(&b, "    %s = %s\n", fld.rowsKeys[i].View(), fld.rowsValues[i].View())
		}
		b.WriteString("    " + dimStyle.Render("[+ enter to add a row]"))
		return b.String()
	}
	return "    " + dimStyle.Render("KEY=VALUE per line:") + "\n" +
		indent(fld.textarea.View(), "    ")
}

func indent(s, prefix string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

// readStringList collects a stringList field's current value as a
// []string regardless of listMode.
func readStringList(fld formField, listMode string) []string {
	if listMode == "fields" {
		out := make([]string, 0, len(fld.rowsValues))
		for _, ti := range fld.rowsValues {
			s := strings.TrimSpace(ti.Value())
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	out := []string{}
	for _, line := range strings.Split(fld.textarea.Value(), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// readStringMap collects a stringMap field's current value as a
// map[string]string regardless of listMode. Lines mode parses
// KEY=VALUE — anything without `=` is silently dropped.
func readStringMap(fld formField, listMode string) map[string]string {
	out := map[string]string{}
	if listMode == "fields" {
		for i := range fld.rowsKeys {
			k := strings.TrimSpace(fld.rowsKeys[i].Value())
			if k == "" {
				continue
			}
			out[k] = fld.rowsValues[i].Value()
		}
		return out
	}
	for _, line := range strings.Split(fld.textarea.Value(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.IndexRune(line, '=')
		if idx <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		if k != "" {
			out[k] = v
		}
	}
	return out
}

// Helpers for pulling existing values out of the entry map. They
// tolerate missing keys and wrong types — the schema's shapeMatches
// guarantee is "close enough", not "perfect".

func existingString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func existingInt(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func existingStringList(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, e := range arr {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func existingStringMap(m map[string]any, key string) map[string]string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch mm := v.(type) {
	case map[string]string:
		return mm
	case map[string]any:
		out := make(map[string]string, len(mm))
		for k, val := range mm {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
		return out
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func indexOf(slice []string, v string) int {
	for i, s := range slice {
		if s == v {
			return i
		}
	}
	return -1
}

// cloneMap returns a deep-ish copy of m suitable for the open-time
// snapshot. Nested maps/slices are also cloned so dirty() doesn't
// see false negatives from in-place edits.
func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneAny(v)
	}
	return out
}

func cloneAny(v any) any {
	switch vv := v.(type) {
	case map[string]any:
		return cloneMap(vv)
	case []any:
		out := make([]any, len(vv))
		for i, e := range vv {
			out[i] = cloneAny(e)
		}
		return out
	}
	return v
}

// mapsEqual is a deep-eq helper for the dirty() check. JSON-shaped
// values (map[string]any, []any, scalars) only.
func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || !valEqual(va, vb) {
			return false
		}
	}
	return true
}

func valEqual(a, b any) bool {
	switch va := a.(type) {
	case map[string]any:
		vb, ok := b.(map[string]any)
		return ok && mapsEqual(va, vb)
	case []any:
		vb, ok := b.([]any)
		if !ok || len(va) != len(vb) {
			return false
		}
		for i := range va {
			if !valEqual(va[i], vb[i]) {
				return false
			}
		}
		return true
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}
