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
// PRI-26 caveat: hook entries live inside arrays under
// `hooks/<event>[i].hooks[j]` and can't yet round-trip through
// parse.WriteEntry (which only knows map paths). We refuse rather
// than silently corrupting the destination file. Direct edit via
// `e` (open the whole settings.json in $EDITOR) still works.
func copyEntry(it model.Item, projectDir string) error {
	if it.Kind == model.KindHook {
		return fmt.Errorf("%w: copy/move not yet supported for hooks; use 'e' to edit settings.json directly", ErrUnsupported)
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
