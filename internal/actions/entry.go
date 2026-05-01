package actions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

// deleteEntry removes the entry identified by Item.ConfigKey from the
// JSON/TOML config at Item.Path, leaving the rest of the file intact. If
// the entry is already absent we succeed silently (idempotent delete).
func deleteEntry(it model.Item) error {
	return parse.DeleteEntry(it.Path, it.ConfigKey)
}

// copyEntry duplicates the entry into the equivalent location in the
// opposite scope. The target config file may not exist yet — we create
// it (and any parent directories) with just this entry. Refuses to
// overwrite an existing entry of the same name.
//
// Hook entries (KindHook) live inside arrays under
// `hooks/<event>[i].hooks[j]` so they can't be addressed by a map-only
// Set. They go through copyHookEntry which appends a new matcher group
// containing just the inner hook to the target file's hooks/<event>
// array.
func copyEntry(it model.Item, projectDir string) error {
	if it.Kind == model.KindHook {
		return copyHookEntry(it, projectDir)
	}
	val, _, err := parse.ReadEntry(it.Path, it.ConfigKey)
	if err != nil {
		return err
	}

	targetPath, targetKey, err := remapEntryToOtherScope(it, projectDir)
	if err != nil {
		return err
	}

	// Refuse to clobber an existing target entry — the caller can
	// delete first if that is intentional.
	if _, _, err := parse.ReadEntry(targetPath, targetKey); err == nil {
		return fmt.Errorf("%w: %s in %s", ErrTargetExists, targetKey, targetPath)
	} else if !errors.Is(err, fs.ErrNotExist) && !strings.Contains(err.Error(), "not found") {
		// Real I/O or parse error — surface it. The "not found" guard
		// covers the case where the file exists but the entry doesn't,
		// which is the happy path here.
		return err
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	return parse.WriteEntry(targetPath, targetKey, val)
}

// remapEntryToOtherScope decides where a config entry lands when it
// crosses Global ↔ Local within the same tool. The TARGET key uses the
// canonical shape for that origin's destination scope, which may differ
// from the source key (Claude per-project mcpServers can live either at
// projects.<dir>.mcpServers in ~/.claude.json or at the top level of
// <project>/.mcp.json — we always write to the latter for clarity).
func remapEntryToOtherScope(it model.Item, projectDir string) (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	name := lastSegment(it.ConfigKey)

	switch it.Origin {
	case model.OriginClaude:
		if it.Scope == model.ScopeGlobal {
			if projectDir == "" {
				return "", "", ErrNoProject
			}
			return filepath.Join(projectDir, ".mcp.json"), "mcpServers/" + name, nil
		}
		return filepath.Join(home, ".claude.json"), "mcpServers/" + name, nil

	case model.OriginCodex:
		section := firstSegment(it.ConfigKey) // "mcp_servers" or "profiles"
		if it.Scope == model.ScopeGlobal {
			if projectDir == "" {
				return "", "", ErrNoProject
			}
			return filepath.Join(projectDir, ".codex", "config.toml"), section + "/" + name, nil
		}
		return filepath.Join(home, ".codex", "config.toml"), section + "/" + name, nil

	case model.OriginGemini:
		if it.Scope == model.ScopeGlobal {
			if projectDir == "" {
				return "", "", ErrNoProject
			}
			return filepath.Join(projectDir, ".gemini", "settings.json"), "mcpServers/" + name, nil
		}
		return filepath.Join(home, ".gemini", "settings.json"), "mcpServers/" + name, nil
	}
	return "", "", fmt.Errorf("unknown origin %v", it.Origin)
}

// copyHookEntry copies a single hook entry across scopes. Hook entries
// live inside `hooks/<event>` arrays in the tool's settings.json. We
// rebuild a fresh matcher group on the target side containing just the
// inner hook being copied (preserving the source matcher string), then
// append that group to target hooks/<event>. The target file is
// created if it doesn't exist; an existing file is preserved with only
// the appended entry added.
func copyHookEntry(it model.Item, projectDir string) error {
	parts := parse.SplitKey(it.ConfigKey)
	// Expected shape: hooks/<event>/<matcherIdx>/hooks/<hookIdx>
	if len(parts) != 5 || parts[0] != "hooks" || parts[3] != "hooks" {
		return fmt.Errorf("hook ConfigKey %q has unexpected shape", it.ConfigKey)
	}
	event := parts[1]

	src, _, err := parse.Read(it.Path)
	if err != nil {
		return err
	}
	matcherEntryAny, ok := parse.Get(src, strings.Join(parts[:3], "/"))
	if !ok {
		return fmt.Errorf("hook source matcher entry %q not found", strings.Join(parts[:3], "/"))
	}
	matcherEntry, ok := matcherEntryAny.(map[string]any)
	if !ok {
		return fmt.Errorf("hook source matcher entry not a map: %T", matcherEntryAny)
	}
	innerAny, ok := parse.Get(src, it.ConfigKey)
	if !ok {
		return fmt.Errorf("hook source entry %q not found", it.ConfigKey)
	}

	targetPath, err := remapHookTargetPath(it, projectDir)
	if err != nil {
		return err
	}

	target, format, err := parse.Read(targetPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		target = map[string]any{}
		format = parse.FormatFromExt(targetPath)
	}

	newGroup := map[string]any{"hooks": []any{innerAny}}
	if matcher, ok := matcherEntry["matcher"].(string); ok && matcher != "" {
		newGroup["matcher"] = matcher
	}
	if !parse.Append(target, "hooks/"+event, newGroup) {
		return fmt.Errorf("hook target %s/hooks/%s is not an array", targetPath, event)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	return parse.Write(targetPath, target, format)
}

// remapHookTargetPath returns the settings.json path on the opposite
// scope side for a hook item. Hooks are currently a Claude/Gemini
// concept — Codex hooks layout is unverified and routes here as
// ErrUnsupported until the adapter lands.
func remapHookTargetPath(it model.Item, projectDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	subdir := ""
	switch it.Origin {
	case model.OriginClaude:
		subdir = ".claude"
	case model.OriginGemini:
		subdir = ".gemini"
	default:
		return "", fmt.Errorf("%w: hooks for origin %v", ErrUnsupported, it.Origin)
	}
	if it.Scope == model.ScopeGlobal {
		if projectDir == "" {
			return "", ErrNoProject
		}
		return filepath.Join(projectDir, subdir, "settings.json"), nil
	}
	return filepath.Join(home, subdir, "settings.json"), nil
}

func firstSegment(k string) string {
	if i := strings.Index(k, "/"); i >= 0 {
		return k[:i]
	}
	return k
}

func lastSegment(k string) string {
	if i := strings.LastIndex(k, "/"); i >= 0 {
		return k[i+1:]
	}
	return k
}
