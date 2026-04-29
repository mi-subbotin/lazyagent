package parse

import (
	"strings"
)

// Frontmatter holds the parsed YAML-ish header of a markdown file.
// We only support flat scalar fields (the form Claude / Codex / Gemini
// actually use); nested structures fall through into Body.
type Frontmatter struct {
	Fields map[string]string
	Body   string
}

// Parse extracts a YAML-like frontmatter block delimited by `---` lines from
// the start of a markdown document. If no frontmatter is found, Fields is
// empty and Body is the original content.
//
// Only flat `key: value` lines are recognized. Quoted values (single or
// double) have their quotes stripped. Multi-line block scalars and nested
// structures are not supported — for the MVP all known SKILL.md / agent
// frontmatter formats are flat, and the adapters fall back gracefully when
// a field is missing.
func Parse(src string) Frontmatter {
	fm := Frontmatter{Fields: map[string]string{}, Body: src}
	s := src
	// Trim a leading UTF-8 BOM if present.
	s = strings.TrimPrefix(s, "\ufeff")
	if !strings.HasPrefix(s, "---") {
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

	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		colon := strings.Index(trimmed, ":")
		if colon <= 0 {
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
