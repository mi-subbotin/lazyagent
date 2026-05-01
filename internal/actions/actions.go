// Package actions performs file-system writes that mutate items.
//
// The single user-facing write action is Place (place.go), which moves
// item bytes into the library and projects them back to whichever
// (Origin, Scope) cells the caller picks. Delete removes the on-disk
// artifact for an item. Sessions are kind-specific and route through
// session_delete.go / resume.go.
//
// The legacy Copy / Move / Share / Reshare / CrossCopy entry points
// were folded into Place under PRI-69; their per-tool routing helpers
// (skillRoot, toolRoot, memoryTargetPath) live in share.go and are
// shared with Place via projectionPath.
package actions

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// ErrUnsupported is returned when the requested action does not apply
// to the item's storage type. The TUI surfaces this as a toast — it's
// not a programming bug.
var ErrUnsupported = errors.New("action not supported for this item")

// ErrNoProject is returned when a target's Local scope was requested
// but no project directory was provided.
var ErrNoProject = errors.New("no project local scope (cwd has no .claude/.codex/.gemini)")

// ErrTargetExists is returned when promoting bytes into the library
// would overwrite an existing entry of the same (kind, name). The
// caller should pick a different name or delete the conflicting
// library entry first.
var ErrTargetExists = errors.New("target already exists")

// Delete removes the on-disk artifact backing the item. For directory-
// backed skills the entire `<root>/skills/<name>` directory is removed
// (including auxiliary assets that live alongside SKILL.md). For
// config-entry items (MCP servers, Codex profiles, hooks) the entry is
// removed from its config file while leaving the surrounding
// configuration intact. Sessions are kind-specific (jsonl/json file
// removal for Claude/Gemini, SQLite archive for Codex) and routed
// through DeleteSession before the Storage switch.
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
