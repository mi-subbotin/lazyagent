// Hooks adapter for Claude Code (PRI-26).
//
// Hooks live inside the same settings.json files Claude already uses
// for MCP servers: `~/.claude/settings.json` (global) and
// `<project>/.claude/settings.json` (local). The shape is:
//
//   {
//     "hooks": {
//       "PreToolUse": [
//         {"matcher": "Bash", "hooks": [{"type": "command", "command": "...", "timeout": 5}]}
//       ],
//       "PostToolUse": [...],
//       "SessionStart": [...]
//     }
//   }
//
// We emit one Item per inner hook entry. The ConfigKey carries the
// full path inside the JSON — including the second `hooks` key under
// the matcher group — so parse.Get / Delete / Set walk straight to the
// element without any translation:
//
//   hooks/<event>/<matcher-idx>/hooks/<hook-idx>
//
// Name is "<event>:<matcher>" — events without a matcher show just the
// event. Description is the first line of the command so the user can
// scan the list without expanding each row. The full command lives in
// Body and the detail panel surfaces a "⚠ runs shell" warning above
// it so the security implication is unmissable.

package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

// scanHooksFile reads a Claude settings.json and emits one Item per
// inner hook entry under `hooks.<event>[i].hooks[j]`. Missing files,
// missing sections and malformed JSON all return nil — the user is
// allowed to have any subset configured, and a parse error in one
// section must not blank out the rest of the tree.
func scanHooksFile(path string, scope model.Scope) []model.Item {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	hooksAny, ok := raw["hooks"].(map[string]any)
	if !ok {
		return nil
	}

	var out []model.Item
	for event, eventEntriesAny := range hooksAny {
		eventEntries, ok := eventEntriesAny.([]any)
		if !ok {
			continue
		}
		for matcherIdx, eventEntryAny := range eventEntries {
			eventEntry, ok := eventEntryAny.(map[string]any)
			if !ok {
				continue
			}
			matcher, _ := eventEntry["matcher"].(string)
			innerHooks, ok := eventEntry["hooks"].([]any)
			if !ok {
				continue
			}
			for hookIdx, innerAny := range innerHooks {
				inner, ok := innerAny.(map[string]any)
				if !ok {
					continue
				}
				command, _ := inner["command"].(string)
				name := event
				if matcher != "" {
					name = event + ":" + matcher
				}
				if len(innerHooks) > 1 {
					name = fmt.Sprintf("%s[%d]", name, hookIdx)
				}
				configKey := fmt.Sprintf("hooks/%s/%d/hooks/%d", event, matcherIdx, hookIdx)
				out = append(out, model.Item{
					Origin:      model.OriginClaude,
					Kind:        model.KindHook,
					Scope:       scope,
					Name:        name,
					Path:        path,
					Description: hookDescription(command),
					Body:        hookBody(event, matcher, inner),
					RawJSON:     parse.MCPToJSON(inner),
					RawTOML:     parse.MCPToTOML(inner),
					Storage:     model.StorageEntry,
					ConfigKey:   configKey,
					ParseError:  validateHookEntry(inner),
					Meta: map[string]string{
						"event":   event,
						"matcher": matcher,
						"command": command,
					},
				})
			}
		}
	}
	return out
}

// validateHookEntry returns a non-empty string when the inner hook
// map looks malformed enough that running it would either crash the
// shell or silently no-op. The TUI surfaces this as the `(invalid)`
// marker next to the item via Item.ParseError. v1 checks (PRI-61):
//
//   - empty or missing `command` (nothing to run);
//   - `timeout` present but not a positive number (string, negative,
//     non-numeric — the engine ignores or rejects these inconsistently
//     across releases, so we flag any of them);
//   - `type` missing entirely — Claude requires "type": "command" today
//     and silently ignores entries without it.
//
// Multiple problems collapse into one string; the user gets enough to
// know what to fix and the detail panel still renders the raw JSON.
func validateHookEntry(inner map[string]any) string {
	var problems []string
	if cmd, ok := inner["command"].(string); !ok || strings.TrimSpace(cmd) == "" {
		problems = append(problems, "missing or empty command")
	}
	if v, ok := inner["timeout"]; ok {
		switch t := v.(type) {
		case float64:
			if t <= 0 {
				problems = append(problems, "timeout must be > 0")
			}
		case int:
			if t <= 0 {
				problems = append(problems, "timeout must be > 0")
			}
		default:
			problems = append(problems, "timeout must be a number")
		}
	}
	if t, ok := inner["type"].(string); !ok || strings.TrimSpace(t) == "" {
		problems = append(problems, "missing type")
	}
	return strings.Join(problems, "; ")
}

// hookDescription returns a single-line summary of the command so the
// list view stays readable. Long commands are truncated; multi-line
// commands lose everything past the first non-blank line.
func hookDescription(command string) string {
	for _, ln := range strings.Split(command, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if len(t) > 80 {
			return t[:80] + "…"
		}
		return t
	}
	return ""
}

// hookBody renders a markdown preview for the detail panel. The
// "⚠ runs shell" warning is plain text so it shows up regardless of
// whether glamour is on or off; the styled red highlight comes from
// the renderer (tui.styles).
func hookBody(event, matcher string, inner map[string]any) string {
	var b strings.Builder
	b.WriteString("# Hook\n\n")
	b.WriteString("⚠ runs shell\n\n")
	b.WriteString(fmt.Sprintf("- event: `%s`\n", event))
	if matcher != "" {
		b.WriteString(fmt.Sprintf("- matcher: `%s`\n", matcher))
	}
	if t, ok := inner["type"].(string); ok && t != "" {
		b.WriteString(fmt.Sprintf("- type: `%s`\n", t))
	}
	if to, ok := inner["timeout"].(float64); ok {
		b.WriteString(fmt.Sprintf("- timeout: `%g`s\n", to))
	}
	if cmd, ok := inner["command"].(string); ok && cmd != "" {
		b.WriteString("\n## command\n\n")
		b.WriteString("```sh\n")
		b.WriteString(cmd)
		if !strings.HasSuffix(cmd, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n")
	}
	return b.String()
}
