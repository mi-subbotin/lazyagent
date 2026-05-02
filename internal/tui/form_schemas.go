// Per-kind form schemas (PRI-75). Each schema returns a fieldSpec
// list ordered the way users expect to fill them out, plus a
// shapeMatches predicate that decides whether an existing entry can
// populate the form (vs. fall back to the JSON textarea).

package tui

import (
	"net/url"
	"os/exec"
	"regexp"
	"strings"
)

// mcpSchema covers all three transports. The transport selector is
// the first field; downstream fields (command/args/env or url/headers)
// hide based on its value.
func mcpSchema() formSchema {
	transports := []string{"stdio", "sse", "http"}
	isStdio := func(v map[string]any) bool { return readEnumValue(v, "type") == "stdio" }
	isHTTPish := func(v map[string]any) bool {
		t := readEnumValue(v, "type")
		return t == "sse" || t == "http"
	}
	return formSchema{
		Title: "MCP server",
		Fields: []fieldSpec{
			{
				Name:    "type",
				Label:   "transport",
				Kind:    fieldEnum,
				Choices: transports,
				Help:    "stdio = local process; sse / http = remote HTTP server",
			},
			{
				Name:      "command",
				Label:     "command",
				Kind:      fieldString,
				Required:  true,
				Help:      "executable name; resolved against $PATH",
				VisibleIf: isStdio,
				Validate: func(v any) string {
					s, _ := v.(string)
					s = strings.TrimSpace(s)
					if s == "" {
						return ""
					}
					if _, err := exec.LookPath(s); err != nil {
						return "not found in $PATH"
					}
					return ""
				},
			},
			{
				Name:      "args",
				Label:     "args",
				Kind:      fieldStringList,
				Help:      "one argument per line",
				VisibleIf: isStdio,
			},
			{
				Name:      "env",
				Label:     "env",
				Kind:      fieldStringMap,
				Help:      "KEY=VALUE per line; KEY must match ^[A-Z_][A-Z0-9_]*$",
				VisibleIf: isStdio,
				Validate: func(v any) string {
					m, _ := v.(map[string]string)
					if m == nil {
						return ""
					}
					re := regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
					var bad []string
					for k := range m {
						if !re.MatchString(k) {
							bad = append(bad, k)
						}
					}
					if len(bad) > 0 {
						return "non-conforming env keys: " + strings.Join(bad, ", ")
					}
					return ""
				},
			},
			{
				Name:      "url",
				Label:     "url",
				Kind:      fieldString,
				Required:  true,
				Help:      "https://… endpoint",
				VisibleIf: isHTTPish,
				Validate: func(v any) string {
					s, _ := v.(string)
					s = strings.TrimSpace(s)
					if s == "" {
						return ""
					}
					u, err := url.Parse(s)
					if err != nil {
						return "url parse: " + err.Error()
					}
					if u.Scheme == "" || u.Host == "" {
						return "expected scheme://host"
					}
					return ""
				},
			},
			{
				Name:      "headers",
				Label:     "headers",
				Kind:      fieldStringMap,
				Help:      "Header-Name=value per line",
				VisibleIf: isHTTPish,
			},
		},
		shapeMatches: func(entry any) bool {
			m, ok := entry.(map[string]any)
			if !ok {
				return false
			}
			t := readEnumValue(m, "type")
			switch t {
			case "stdio", "":
				// stdio is the historical default; tolerate missing type.
				_, hasCmd := m["command"]
				return hasCmd
			case "sse", "http":
				_, hasURL := m["url"]
				return hasURL
			}
			return false
		},
	}
}

// hookSchema covers a single inner-hook entry (the leaf at
// hooks/<event>/<i>/hooks/<j>). matcher and event are siblings of the
// inner block; we surface them as read-only context, not editable
// fields, since changing matcher means *moving* the hook to another
// matcher block — outside the scope of an entry edit.
func hookSchema() formSchema {
	return formSchema{
		Title: "Hook entry",
		Fields: []fieldSpec{
			{
				Name:    "type",
				Label:   "type",
				Kind:    fieldEnum,
				Choices: []string{"command"},
			},
			{
				Name:     "command",
				Label:    "command",
				Kind:     fieldString,
				Required: true,
				Help:     "shell snippet run on the hook event",
			},
			{
				Name:  "timeout",
				Label: "timeout (s)",
				Kind:  fieldInt,
				Help:  "0 or omitted = use the tool default",
			},
		},
		shapeMatches: func(entry any) bool {
			m, ok := entry.(map[string]any)
			if !ok {
				return false
			}
			_, hasCmd := m["command"].(string)
			return hasCmd
		},
	}
}

// readEnumValue is a tolerant lookup for a string-valued field.
// Used by the VisibleIf closures so they don't have to repeat
// type-assertion boilerplate.
func readEnumValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
