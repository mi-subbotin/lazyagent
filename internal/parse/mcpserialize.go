package parse

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// MCPToJSON renders an MCP server entry as a pretty-printed JSON object.
// `entry` is the value side of a `mcpServers` map (typically command/args/env).
func MCPToJSON(entry any) string {
	b, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Sprintf("// json error: %v", err)
	}
	return string(b)
}

// MCPToTOML renders the same entry as a flat TOML key/value block. We don't
// use a TOML library here because MCP entries are shallow (command, args,
// env, sometimes a few flags) and hand-rolling keeps the output
// predictable when we toggle between formats in the detail panel.
func MCPToTOML(entry any) string {
	m, ok := entry.(map[string]any)
	if !ok {
		// Fallback: stringify whatever it is.
		return fmt.Sprintf("# unsupported shape\nvalue = %q\n", fmt.Sprint(entry))
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var simple, tables strings.Builder
	for _, k := range keys {
		v := m[k]
		switch vv := v.(type) {
		case string:
			fmt.Fprintf(&simple, "%s = %q\n", k, vv)
		case bool:
			fmt.Fprintf(&simple, "%s = %t\n", k, vv)
		case float64:
			if vv == float64(int64(vv)) {
				fmt.Fprintf(&simple, "%s = %d\n", k, int64(vv))
			} else {
				fmt.Fprintf(&simple, "%s = %g\n", k, vv)
			}
		case []any:
			fmt.Fprintf(&simple, "%s = %s\n", k, tomlArray(vv))
		case map[string]any:
			fmt.Fprintf(&tables, "\n[%s]\n", k)
			subKeys := make([]string, 0, len(vv))
			for sk := range vv {
				subKeys = append(subKeys, sk)
			}
			sort.Strings(subKeys)
			for _, sk := range subKeys {
				switch sv := vv[sk].(type) {
				case string:
					fmt.Fprintf(&tables, "%s = %q\n", sk, sv)
				default:
					fmt.Fprintf(&tables, "%s = %s\n", sk, jsonInline(sv))
				}
			}
		default:
			fmt.Fprintf(&simple, "%s = %s\n", k, jsonInline(vv))
		}
	}
	return simple.String() + tables.String()
}

func tomlArray(arr []any) string {
	parts := make([]string, 0, len(arr))
	for _, e := range arr {
		switch ev := e.(type) {
		case string:
			parts = append(parts, fmt.Sprintf("%q", ev))
		default:
			parts = append(parts, jsonInline(ev))
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func jsonInline(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
