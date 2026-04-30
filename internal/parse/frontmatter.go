package parse

import (
	"fmt"
	"strings"
)

// FrontmatterError describes a single parse-level diagnostic from
// Parse. It is non-fatal — Parse always returns a usable Frontmatter
// even when Errors is non-empty — but it lets callers surface a
// "(invalid)" badge / detailed message instead of silently skipping.
type FrontmatterError struct {
	// Line is the 1-based line number within the original source. A
	// value of 0 means the diagnostic applies to the file as a whole
	// (e.g. an unterminated frontmatter block).
	Line int
	// Kind is a short machine-readable tag: "unterminated",
	// "missing-colon", "empty-key".
	Kind string
	// Message is the human-readable explanation, suitable for showing
	// in the detail panel.
	Message string
}

func (e FrontmatterError) String() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Message)
	}
	return e.Message
}

// Frontmatter holds the parsed YAML-ish header of a markdown file.
// We only support flat scalar fields (the form Claude / Codex / Gemini
// actually use); nested structures fall through into Body.
type Frontmatter struct {
	Fields map[string]string
	Body   string
	// Errors is empty when parsing was clean. Non-empty values mean
	// "the frontmatter is partially or fully malformed" — adapters
	// should still surface the item but mark it as invalid.
	Errors []FrontmatterError
}

// Parse extracts a YAML-like frontmatter block delimited by `---` lines from
// the start of a markdown document. If no frontmatter is found, Fields is
// empty, Body is the original content, and Errors is nil.
//
// Only flat `key: value` lines are recognized. Quoted values (single or
// double) have their quotes stripped. Multi-line block scalars and nested
// structures are not supported — for the MVP all known SKILL.md / agent
// frontmatter formats are flat, and the adapters fall back gracefully when
// a field is missing.
//
// When the document opens with `---` but does not close (or contains
// malformed key: value lines inside the header), Parse fills Errors with
// per-line diagnostics so callers can render an "(invalid)" badge.
func Parse(src string) Frontmatter {
	fm := Frontmatter{Fields: map[string]string{}, Body: src}
	s := src
	// Trim a leading UTF-8 BOM if present.
	s = strings.TrimPrefix(s, "\ufeff")
	if !strings.HasPrefix(s, "---") {
		// No frontmatter at all is not an error — just a plain markdown
		// document. Adapters decide whether the kind requires it.
		return fm
	}
	// Skip the opening `---` line.
	rest := s[3:]
	// Allow CRLF or LF after the marker.
	rest = strings.TrimLeft(rest, "\r")
	if !strings.HasPrefix(rest, "\n") {
		return fm
	}
	rest = rest[1:]

	end := strings.Index(rest, "\n---")
	if end == -1 {
		fm.Errors = append(fm.Errors, FrontmatterError{
			Line:    0,
			Kind:    "unterminated",
			Message: "frontmatter opened with `---` but never closed; expected a `\\n---` terminator",
		})
		return fm
	}
	header := rest[:end]
	body := rest[end+len("\n---"):]
	// Skip the rest of the closing marker line.
	if i := strings.Index(body, "\n"); i >= 0 {
		body = body[i+1:]
	} else {
		body = ""
	}

	for idx, line := range strings.Split(header, "\n") {
		// Convert 0-based header offset to a 1-based original-source
		// line. The opening `---` is line 1, header lines start at 2.
		srcLine := idx + 2
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		colon := strings.Index(trimmed, ":")
		if colon < 0 {
			fm.Errors = append(fm.Errors, FrontmatterError{
				Line:    srcLine,
				Kind:    "missing-colon",
				Message: fmt.Sprintf("expected `key: value`, got %q", trimmed),
			})
			continue
		}
		if colon == 0 {
			fm.Errors = append(fm.Errors, FrontmatterError{
				Line:    srcLine,
				Kind:    "empty-key",
				Message: fmt.Sprintf("empty key in %q", trimmed),
			})
			continue
		}
		key := strings.TrimSpace(trimmed[:colon])
		val := strings.TrimSpace(trimmed[colon+1:])
		val = unquote(val)
		fm.Fields[key] = val
	}
	fm.Body = body
	return fm
}

func unquote(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
