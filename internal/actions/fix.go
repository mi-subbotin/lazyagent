// Auto-fix for items flagged invalid by the adapters (PRI-73).
//
// Validators in PRI-18 (frontmatter) and PRI-61 (hooks) populate
// `Item.ParseError` so the TUI can render `(invalid)` next to the row.
// This file turns those diagnostics into deterministic on-disk fixes:
//
//   - Skill / Agent / Prompt (markdown + YAML frontmatter): the common
//     failure is a multi-line `description:` value that spills across
//     several lines without quoting, so each spillover line trips the
//     `missing-colon` diagnostic. fixFrontmatter merges the spillover
//     into the previous key's value, normalises tabs, and synthesises
//     `name` / `description` from filepath / body when entirely missing.
//
//   - Hook entries: add `"type": "command"` if missing, drop a non-
//     positive / non-numeric `timeout`. An empty `command` is left
//     unfixable — the user has to write the shell line themselves.
//
// MCP entries are validated by the JSON/TOML parser at adapter level —
// if they made it into a `model.Item` they're already structurally
// valid, so there is nothing to do here.
//
// The two halves are intentionally split: Fix returns a FixPlan with
// before/after bytes (so the TUI can show a diff and let the user say
// no), and ApplyFix writes them to disk atomically and re-validates the
// result, restoring the original on failure.

package actions

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

// FixPlan describes a single deterministic fix the user is about to
// apply. Path is the file that will be written; Before / After let the
// TUI show a diff and let ApplyFix roll back when re-validation fails.
type FixPlan struct {
	Item   model.Item
	Path   string
	Before []byte
	After  []byte
	Reason string // the original ParseError that triggered the fix
}

// Empty reports whether the plan would not change the file.
func (p FixPlan) Empty() bool {
	return len(p.After) == 0 || bytes.Equal(p.Before, p.After)
}

// ErrNothingToFix means the item is already valid (caller should not
// have called Fix), or the specific failure mode has no auto-fix.
var ErrNothingToFix = fmt.Errorf("nothing to fix")

// ErrUnfixable means we recognise the failure but cannot resolve it
// without user input (e.g. a hook with an empty command).
var ErrUnfixable = fmt.Errorf("unfixable")

// Fix returns a FixPlan describing what would be written, or
// ErrNothingToFix / ErrUnfixable when the item is fine or beyond
// deterministic repair.
func Fix(it model.Item) (FixPlan, error) {
	if it.ParseError == "" {
		return FixPlan{}, fmt.Errorf("%w: %s has no parse error", ErrNothingToFix, it.Name)
	}
	if it.Storage == model.StorageEntry {
		if it.Kind == model.KindHook {
			return fixHookEntry(it)
		}
		return FixPlan{}, fmt.Errorf("%w: no fixer for %s entries", ErrUnfixable, it.Kind)
	}
	switch it.Kind {
	case model.KindSkill, model.KindAgent, model.KindPrompt, model.KindMemory:
		return fixFrontmatter(it)
	}
	return FixPlan{}, fmt.Errorf("%w: no fixer for %s", ErrUnfixable, it.Kind)
}

// ApplyFix writes plan.After atomically, then re-runs the relevant
// validator. If the result is still invalid we restore plan.Before and
// return the validator's complaint — better to leave the user with the
// original mess than half-fixed bytes.
func ApplyFix(plan FixPlan) error {
	if plan.Empty() {
		return fmt.Errorf("%w: empty plan", ErrNothingToFix)
	}
	tmp := plan.Path + ".tmp"
	if err := os.WriteFile(tmp, plan.After, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, plan.Path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := revalidate(plan); err != nil {
		// Roll back to the original bytes so the user is no worse off.
		if rbErr := os.WriteFile(plan.Path, plan.Before, 0o644); rbErr != nil {
			return fmt.Errorf("re-validation failed (%v) and rollback also failed: %w", err, rbErr)
		}
		return fmt.Errorf("re-validation failed, reverted: %w", err)
	}
	return nil
}

func revalidate(plan FixPlan) error {
	switch plan.Item.Kind {
	case model.KindSkill, model.KindAgent, model.KindPrompt, model.KindMemory:
		fm := parse.Parse(string(plan.After))
		if len(fm.Errors) > 0 {
			return fmt.Errorf("%s", fm.Errors[0])
		}
	}
	return nil
}

// fixFrontmatter rewrites a markdown file's YAML frontmatter, merging
// continuation lines into the preceding key's value and quoting where
// necessary. It also fills in `name` / `description` from filepath /
// body when entirely absent — the validator flags both as required.
func fixFrontmatter(it model.Item) (FixPlan, error) {
	raw, err := os.ReadFile(it.Path)
	if err != nil {
		return FixPlan{}, fmt.Errorf("read %s: %w", it.Path, err)
	}
	rebuilt, ok := rewriteFrontmatter(raw, it)
	if !ok {
		return FixPlan{}, fmt.Errorf("%w: cannot rewrite frontmatter for %s", ErrUnfixable, it.Name)
	}
	if bytes.Equal(raw, rebuilt) {
		return FixPlan{}, fmt.Errorf("%w: no change required for %s", ErrNothingToFix, it.Name)
	}
	return FixPlan{
		Item:   it,
		Path:   it.Path,
		Before: raw,
		After:  rebuilt,
		Reason: it.ParseError,
	}, nil
}

var keyLineRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)\s*:\s*(.*)$`)

// rewriteFrontmatter takes the raw bytes of a markdown file and returns
// (rewritten, true) when it could parse and re-emit a clean header. The
// `it` argument is only consulted when synthesising defaults for
// missing keys.
func rewriteFrontmatter(raw []byte, it model.Item) ([]byte, bool) {
	s := strings.TrimPrefix(string(raw), "\ufeff")
	hasMarker := strings.HasPrefix(s, "---")
	var header, body string
	if !hasMarker {
		// No frontmatter at all. Synthesise a minimal one so required
		// fields (name / description) are present.
		body = s
	} else {
		rest := strings.TrimLeft(s[3:], "\r")
		if !strings.HasPrefix(rest, "\n") {
			return nil, false
		}
		rest = rest[1:]
		end := strings.Index(rest, "\n---")
		if end == -1 {
			// Unterminated: treat the entire remainder as header. We
			// reconstruct a closing marker below; this is the least-bad
			// way to recover when a stray `---` was deleted by hand.
			header = rest
			body = ""
		} else {
			header = rest[:end]
			body = rest[end+len("\n---"):]
			if i := strings.Index(body, "\n"); i >= 0 {
				body = body[i+1:]
			} else {
				body = ""
			}
		}
	}

	keys, fields := parseFrontmatterLoose(header)

	if _, ok := fields["name"]; !ok {
		fields["name"] = deriveName(it)
		keys = prependKey(keys, "name")
	}
	if d, ok := fields["description"]; !ok || strings.TrimSpace(d) == "" {
		fields["description"] = deriveDescription(body)
		if _, present := indexOf(keys, "description"); !present {
			keys = append(keys, "description")
		}
	}

	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range keys {
		v, ok := fields[k]
		if !ok {
			continue
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(yamlScalar(v))
		b.WriteString("\n")
	}
	b.WriteString("---\n")
	b.WriteString(body)
	return []byte(b.String()), true
}

// parseFrontmatterLoose is the forgiving cousin of parse.Parse: lines
// without a `key:` prefix are treated as continuations of the previous
// key and merged with a single space. Tabs in indentation are silently
// converted. Returns the keys in insertion order plus the field map.
func parseFrontmatterLoose(header string) ([]string, map[string]string) {
	fields := map[string]string{}
	var keys []string
	var lastKey string
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimRight(line, "\r")
		line = strings.ReplaceAll(line, "\t", "  ")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if m := keyLineRe.FindStringSubmatch(trimmed); m != nil {
			key := m[1]
			val := stripOuterQuotes(strings.TrimSpace(m[2]))
			if _, exists := fields[key]; !exists {
				keys = append(keys, key)
			}
			fields[key] = val
			lastKey = key
			continue
		}
		if lastKey != "" {
			cur := fields[lastKey]
			if cur != "" {
				cur += " "
			}
			fields[lastKey] = cur + trimmed
		}
	}
	return keys, fields
}

func stripOuterQuotes(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// yamlScalar emits a value that round-trips through parse.Parse without
// triggering a diagnostic. Most descriptions are plain ASCII; we only
// reach for quotes when the value contains characters our restricted
// parser would mis-handle (a leading `"`, `'` or `-`, or a `:` / `#`
// that would look like structure).
func yamlScalar(v string) string {
	if v == "" {
		return `""`
	}
	v = strings.ReplaceAll(v, "\n", " ")
	v = strings.TrimSpace(v)
	needsQuote := false
	if strings.ContainsAny(v, ":#") {
		needsQuote = true
	}
	if v[0] == ' ' || v[0] == '"' || v[0] == '\'' || v[0] == '-' || v[0] == '[' || v[0] == '{' {
		needsQuote = true
	}
	if !needsQuote {
		return v
	}
	if !strings.Contains(v, `"`) {
		return `"` + v + `"`
	}
	if !strings.Contains(v, `'`) {
		return `'` + v + `'`
	}
	// Both quote forms appear — fall back to single quotes with the
	// inner ones doubled, which is real-YAML compliant and round-trips
	// through parse.Parse's outer-only unquote without breaking.
	escaped := strings.ReplaceAll(v, `'`, `''`)
	return `'` + escaped + `'`
}

func prependKey(keys []string, k string) []string {
	for _, existing := range keys {
		if existing == k {
			return keys
		}
	}
	return append([]string{k}, keys...)
}

func indexOf(keys []string, k string) (int, bool) {
	for i, existing := range keys {
		if existing == k {
			return i, true
		}
	}
	return -1, false
}

// deriveName falls back to the on-disk identity when frontmatter omits
// `name` entirely. Skills sit in `<root>/skills/<name>/SKILL.md` so we
// take the parent directory; agents and prompts use the basename.
func deriveName(it model.Item) string {
	if it.Name != "" {
		return it.Name
	}
	if it.Kind == model.KindSkill {
		return filepath.Base(filepath.Dir(it.Path))
	}
	return strings.TrimSuffix(filepath.Base(it.Path), filepath.Ext(it.Path))
}

// deriveDescription synthesises a placeholder summary from the first
// non-empty body line. Truncated to 200 chars to keep the YAML compact.
func deriveDescription(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if len(t) > 200 {
			t = t[:200]
		}
		return t
	}
	return "(no description)"
}

// fixHookEntry applies the structural repairs the hook validator looks
// for: synthesise `type: command` when missing, drop an invalid
// `timeout`. An empty / missing `command` is treated as unfixable.
func fixHookEntry(it model.Item) (FixPlan, error) {
	raw, err := os.ReadFile(it.Path)
	if err != nil {
		return FixPlan{}, fmt.Errorf("read %s: %w", it.Path, err)
	}
	val, format, err := parse.ReadEntry(it.Path, it.ConfigKey)
	if err != nil {
		return FixPlan{}, fmt.Errorf("read entry %s: %w", it.ConfigKey, err)
	}
	inner, ok := val.(map[string]any)
	if !ok {
		return FixPlan{}, fmt.Errorf("%w: hook %s is not an object", ErrUnfixable, it.Name)
	}
	cmd, _ := inner["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		return FixPlan{}, fmt.Errorf("%w: %s has empty command — fix manually", ErrUnfixable, it.Name)
	}
	changed := false
	if t, _ := inner["type"].(string); strings.TrimSpace(t) == "" {
		inner["type"] = "command"
		changed = true
	}
	if v, ok := inner["timeout"]; ok {
		valid := false
		switch tv := v.(type) {
		case float64:
			valid = tv > 0
		case int:
			valid = tv > 0
		case int64:
			valid = tv > 0
		}
		if !valid {
			delete(inner, "timeout")
			changed = true
		}
	}
	if !changed {
		return FixPlan{}, fmt.Errorf("%w: %s has no auto-fixable issue", ErrUnfixable, it.Name)
	}
	data, _, err := parse.Read(it.Path)
	if err != nil {
		return FixPlan{}, err
	}
	parse.Set(data, it.ConfigKey, inner)
	after, err := parse.Marshal(data, format)
	if err != nil {
		return FixPlan{}, err
	}
	if bytes.Equal(raw, after) {
		return FixPlan{}, fmt.Errorf("%w: marshal produced identical bytes", ErrNothingToFix)
	}
	return FixPlan{
		Item:   it,
		Path:   it.Path,
		Before: raw,
		After:  after,
		Reason: it.ParseError,
	}, nil
}
