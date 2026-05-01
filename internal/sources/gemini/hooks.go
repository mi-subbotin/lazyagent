// Hooks adapter for Gemini CLI (PRI-57).
//
// Gemini stores hooks alongside its other settings in
// ~/.gemini/settings.json (global) and <project>/.gemini/settings.json
// (local). The expected shape is the same `hooks.<event>[i].hooks[j]`
// nested-array structure Claude uses, so we share the rendering and
// ConfigKey conventions:
//
//   hooks/<event>/<matcher-idx>/hooks/<hook-idx>
//
// If Gemini ends up adopting a different on-disk shape we'll branch
// here; for now Claude's adapter is the reference implementation.

package gemini

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

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
					Origin:      model.OriginGemini,
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

// validateHookEntry mirrors the Claude adapter's validator (PRI-61):
// flags missing/empty command, invalid timeout, and missing type. Lives
// here as a copy rather than a shared helper because Gemini may evolve
// its on-disk shape independently and we want each adapter to own its
// own checks.
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
