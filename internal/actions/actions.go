// Package actions performs file-system writes that mutate items: delete,
// copy between scopes (Global ↔ Local), move (= copy + delete).
//
// Only StorageFile and StorageDir items are supported in this phase.
// StorageEntry items (MCP servers, Codex profiles) need JSON/TOML merging
// and will be handled in a follow-up.
package actions

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// ErrUnsupported is returned when the requested action does not apply to
// the item's storage type. The TUI surfaces this as a toast — it's not a
// programming bug.
var ErrUnsupported = errors.New("action not supported for this item")

// ErrNoProject is returned when copy/move would need a project-local scope
// but the user invoked lazyagent outside of any detected project.
var ErrNoProject = errors.New("no project local scope (cwd has no .claude/.codex/.gemini)")

// ErrTargetExists is returned by Copy when the destination path already
// has a file or directory. Refuse silently rather than overwrite.
var ErrTargetExists = errors.New("target already exists")

// Delete removes the on-disk artifact backing the item. For directory-backed
// skills the entire `<root>/skills/<name>` directory is removed (including
// auxiliary assets that live alongside SKILL.md). For config-entry items
// (MCP servers, Codex profiles) the entry is removed from its config file
// while leaving the surrounding configuration intact. Sessions are
// kind-specific (jsonl/json file removal for Claude/Gemini, SQLite archive
// for Codex) and routed through DeleteSession before the Storage switch.
func Delete(it model.Item) error {
	if it.Kind == model.KindSession {
		return DeleteSession(it)
	}
	switch it.Storage {
	case model.StorageFile:
		return os.Remove(it.Path)
	case model.StorageDir:
		return os.RemoveAll(filepath.Dir(it.Path))
	case model.StorageEntry:
		return deleteEntry(it)
	default:
		return ErrUnsupported
	}
}

// Copy duplicates the item into the other scope. If the item is currently
// Global, the copy lands in the project-local scope (and vice versa).
// projectDir must be non-empty when the *target* is Local; otherwise we
// return ErrNoProject so the caller can surface a friendlier message.
//
// Refuses to overwrite an existing target — the caller can delete first
// if that is intentional.
func Copy(it model.Item, projectDir string) error {
	if it.Storage == model.StorageEntry {
		return copyEntry(it, projectDir)
	}
	target, err := remapToOtherScope(it, projectDir)
	if err != nil {
		return err
	}

	src := it.Path
	dst := target
	if it.Storage == model.StorageDir {
		src = filepath.Dir(src)
		dst = filepath.Dir(dst)
	}
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%w: %s", ErrTargetExists, dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	if it.Storage == model.StorageFile {
		return copyFile(src, dst)
	}
	return copyDir(src, dst)
}

// Move = Copy then Delete on success. If Copy fails the source is
// untouched. If Delete fails after a successful Copy we still report the
// error — the caller can resolve manually since the copy already exists.
func Move(it model.Item, projectDir string) error {
	if err := Copy(it, projectDir); err != nil {
		return err
	}
	return Delete(it)
}

// remapToOtherScope computes the target path of an item if it were moved
// to the opposite scope. AGENTS.md and GEMINI.md live at different relative
// locations in global vs local scope (under the tool's home dir globally,
// at the project root locally), so they are special-cased.
func remapToOtherScope(it model.Item, projectDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	src := it.Path

	switch it.Origin {
	case model.OriginClaude:
		// CLAUDE.md sits at <home>/.claude/CLAUDE.md globally but at
		// project root (<projectDir>/CLAUDE.md) locally — different
		// relative path on each side, so handle it explicitly.
		if filepath.Base(src) == "CLAUDE.md" {
			if it.Scope == model.ScopeGlobal {
				if projectDir == "" {
					return "", ErrNoProject
				}
				return filepath.Join(projectDir, "CLAUDE.md"), nil
			}
			return filepath.Join(home, ".claude", "CLAUDE.md"), nil
		}
		return remapStandard(src, it.Scope, projectDir,
			filepath.Join(home, ".claude"), ".claude")

	case model.OriginCodex:
		if filepath.Base(src) == "AGENTS.md" {
			if it.Scope == model.ScopeGlobal {
				if projectDir == "" {
					return "", ErrNoProject
				}
				return filepath.Join(projectDir, "AGENTS.md"), nil
			}
			return filepath.Join(home, ".codex", "AGENTS.md"), nil
		}
		if it.Kind == model.KindSkill {
			// Codex skills live under .agents/, sibling of .codex/.
			return remapStandard(src, it.Scope, projectDir,
				filepath.Join(home, ".agents"), ".agents")
		}
		return remapStandard(src, it.Scope, projectDir,
			filepath.Join(home, ".codex"), ".codex")

	case model.OriginGemini:
		if filepath.Base(src) == "GEMINI.md" {
			if it.Scope == model.ScopeGlobal {
				if projectDir == "" {
					return "", ErrNoProject
				}
				return filepath.Join(projectDir, "GEMINI.md"), nil
			}
			return filepath.Join(home, ".gemini", "GEMINI.md"), nil
		}
		return remapStandard(src, it.Scope, projectDir,
			filepath.Join(home, ".gemini"), ".gemini")
	}
	return "", fmt.Errorf("unknown origin %v", it.Origin)
}

// remapStandard handles the common pattern: global root is `<homeRoot>`,
// local root is `<projectDir>/<localSubdir>`, and the relative path inside
// either side is the same.
func remapStandard(src string, scope model.Scope, projectDir, homeRoot, localSubdir string) (string, error) {
	if scope == model.ScopeGlobal {
		if projectDir == "" {
			return "", ErrNoProject
		}
		rel, err := filepath.Rel(homeRoot, src)
		if err != nil {
			return "", err
		}
		return filepath.Join(projectDir, localSubdir, rel), nil
	}
	if projectDir == "" {
		return "", ErrNoProject
	}
	localRoot := filepath.Join(projectDir, localSubdir)
	rel, err := filepath.Rel(localRoot, src)
	if err != nil {
		return "", err
	}
	return filepath.Join(homeRoot, rel), nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}
