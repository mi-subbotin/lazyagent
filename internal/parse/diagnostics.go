package parse

import "strings"

// DiagnoseFrontmatter compresses a parsed Frontmatter into the two
// fields the model.Item carries: a single ParseError string (joined
// from all per-line FrontmatterErrors) and a list of ValidationWarnings
// for missing recommended frontmatter fields.
//
// Adapters call this helper instead of reimplementing the same loop in
// every scan* function. ParseError is empty when fm.Errors is empty;
// warnings is empty when every key in recommended is present and
// non-blank in fm.Fields.
func DiagnoseFrontmatter(fm Frontmatter, recommended []string) (parseErr string, warnings []string) {
	if len(fm.Errors) > 0 {
		msgs := make([]string, 0, len(fm.Errors))
		for _, e := range fm.Errors {
			msgs = append(msgs, e.String())
		}
		parseErr = strings.Join(msgs, "; ")
	}
	for _, key := range recommended {
		if strings.TrimSpace(fm.Fields[key]) == "" {
			warnings = append(warnings, "missing recommended field: "+key)
		}
	}
	return parseErr, warnings
}
