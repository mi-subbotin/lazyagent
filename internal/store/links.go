// Symlink manager for projecting shared store items into per-tool
// directories (~/.claude/skills/foo, ~/.codex/agents/foo, ...).
//
// Strategy:
//   - Default: create a symlink target -> source.
//   - Fallback (copy): when the projection target lives under a known
//     cloud-sync root (iCloud / Dropbox / OneDrive / Google Drive),
//     symlinks are unreliable across devices and we copy bytes
//     instead. Drift detection (PRI-2 Phase 5) catches edits later.
//
// We never overwrite an unrelated existing file. EnsureLink returns
// ErrConflict when the target exists and is not a link to source (or
// in copy mode, has different content). The caller surfaces that as
// a UI prompt.
package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrLinkConflict means the target path exists but doesn't match the
// source — either it's a regular file with different content, a
// symlink pointing somewhere else, or a directory where we expected a
// file. The caller decides whether to overwrite or skip.
var ErrLinkConflict = errors.New("link conflict")

// LinkMode is how a single projection is materialised on disk.
type LinkMode int

const (
	LinkSymlink LinkMode = iota // symlink target -> source
	LinkCopy                    // bytes copied; target is a regular file/dir
)

// CloudSyncedPath reports whether path lives inside a directory that
// a cloud-sync agent watches (iCloud Drive, Dropbox, OneDrive, Google
// Drive). On those volumes symlinks either don't sync or sync as
// dangling references on other devices, so we fall back to a byte
// copy. Detection is best-effort and path-based — it never opens the
// file or queries an API.
func CloudSyncedPath(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	home, _ := os.UserHomeDir()

	// macOS iCloud Drive — both the canonical container path and the
	// user-facing alias under ~/.
	prefixes := []string{
		filepath.Join(home, "Library", "Mobile Documents"),
		filepath.Join(home, "Library", "CloudStorage"), // iCloud, Dropbox, OneDrive, Drive on modern macOS
	}
	for _, p := range prefixes {
		if hasPathPrefix(abs, p) {
			return true
		}
	}
	// Legacy / non-macOS layouts and user-renamed sync folders. We
	// match on the *first* path component under $HOME so we don't
	// trip on a random "Dropbox" deep inside an unrelated tree.
	if home != "" && hasPathPrefix(abs, home) {
		rel, err := filepath.Rel(home, abs)
		if err == nil {
			first := strings.SplitN(rel, string(os.PathSeparator), 2)[0]
			lower := strings.ToLower(first)
			switch {
			case lower == "dropbox",
				strings.HasPrefix(lower, "dropbox ("),    // "Dropbox (Personal)"
				lower == "onedrive",
				strings.HasPrefix(lower, "onedrive - "),   // "OneDrive - Acme"
				lower == "google drive",
				strings.HasPrefix(lower, "googledrive"),
				strings.HasPrefix(lower, "google drive"):
				return true
			}
		}
	}
	return false
}

// hasPathPrefix is filepath.HasPrefix done right — it splits on
// separators so /a/bc doesn't match prefix /a/b.
func hasPathPrefix(path, prefix string) bool {
	if prefix == "" {
		return false
	}
	prefix = filepath.Clean(prefix)
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+string(os.PathSeparator))
}

// PickLinkMode chooses symlink vs copy for a given target path.
// Symlinks are the default; copy wins only when the target sits under
// a cloud-sync root. The source path doesn't influence the choice —
// the store itself stays under $HOME/.lazyagent which we assume is
// non-synced.
func PickLinkMode(target string) LinkMode {
	if CloudSyncedPath(target) {
		return LinkCopy
	}
	return LinkSymlink
}

// EnsureLink projects source to target using mode. Behaviour:
//   - target's parent dir is created with 0o755.
//   - LinkSymlink: if target already points at source, no-op. If it
//     points elsewhere or is a regular file/dir, returns ErrLinkConflict.
//   - LinkCopy: if target exists with identical bytes, no-op. Otherwise
//     ErrLinkConflict for files; directories aren't auto-replaced.
//
// source may be a file or a directory; for LinkCopy on a directory we
// copy recursively.
func EnsureLink(source, target string, mode LinkMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	switch mode {
	case LinkSymlink:
		return ensureSymlink(source, target)
	case LinkCopy:
		return ensureCopy(source, target)
	}
	return fmt.Errorf("unknown link mode %d", mode)
}

func ensureSymlink(source, target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return os.Symlink(source, target)
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		current, err := os.Readlink(target)
		if err != nil {
			return err
		}
		if current == source {
			return nil
		}
		return fmt.Errorf("%w: %s -> %s (want %s)", ErrLinkConflict, target, current, source)
	}
	return fmt.Errorf("%w: %s already exists and is not a symlink", ErrLinkConflict, target)
}

func ensureCopy(source, target string) error {
	srcInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	if srcInfo.IsDir() {
		return copyDir(source, target)
	}
	return copyFileIfDifferent(source, target)
}

func copyFileIfDifferent(source, target string) error {
	srcData, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(target); err == nil {
		if bytes.Equal(existing, srcData) {
			return nil
		}
		return fmt.Errorf("%w: %s differs from source", ErrLinkConflict, target)
	} else if !os.IsNotExist(err) {
		return err
	}
	return atomicWrite(target, srcData, 0o644)
}

func copyDir(source, target string) error {
	if info, err := os.Stat(target); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%w: %s exists and is not a directory", ErrLinkConflict, target)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(source, e.Name())
		t := filepath.Join(target, e.Name())
		if e.IsDir() {
			if err := copyDir(s, t); err != nil {
				return err
			}
			continue
		}
		if err := copyFileIfDifferent(s, t); err != nil {
			return err
		}
	}
	return nil
}

// atomicWrite writes data to path via tmp + rename so partial writes
// never leave a half-baked file in place. Tmp lives in the same
// directory as path so the rename stays on a single filesystem.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".link-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ResolvesToStore reports whether path, after following symlinks,
// lives inside the lazyagent shared store. Adapters use this on every
// per-tool item so the TUI can flag projections with a (s) badge and
// route share actions to the canonical copy.
//
// Returns false on any filesystem error — a missing store, a broken
// link, or a permission denial all collapse to "not shared", which is
// the safe default.
func ResolvesToStore(path string) bool {
	root, err := Root()
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(real)
	if err != nil {
		return false
	}
	return hasPathPrefix(abs, root)
}

// CanonicalItemDir maps a per-tool path that's a projection of a
// shared item back to its canonical directory in the store
// (`<root>/<kind>/<name>`). Returns "" if path doesn't resolve into
// the store. Used by the share/reshare overlay so pressing `s` on
// either side of the projection lands on the same canonical file.
func CanonicalItemDir(path string) string {
	if !ResolvesToStore(path) {
		return ""
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	rootReal, err := Root()
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(rootReal, real)
	if err != nil {
		return ""
	}
	parts := strings.SplitN(rel, string(filepath.Separator), 3)
	if len(parts) < 2 {
		return ""
	}
	// parts = [kind, name, ...rest]
	return filepath.Join(rootReal, parts[0], parts[1])
}

// RemoveLink deletes target if and only if it points at (or is a copy
// of) source. Returns nil when target is already gone. Returns
// ErrLinkConflict when target exists but doesn't match source — the
// caller should surface that and let the user decide.
func RemoveLink(source, target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		current, err := os.Readlink(target)
		if err != nil {
			return err
		}
		if current != source {
			return fmt.Errorf("%w: %s -> %s (want %s)", ErrLinkConflict, target, current, source)
		}
		return os.Remove(target)
	}
	// Regular file / dir — only remove if content matches source. We
	// stat the source to decide between file vs dir comparison.
	srcInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	if srcInfo.IsDir() {
		// Directory: refuse to auto-delete; copies can drift and the
		// user might have edited assets. PRI-2 Phase 5 will resync
		// these explicitly.
		return fmt.Errorf("%w: %s is a directory copy; remove manually", ErrLinkConflict, target)
	}
	srcData, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	tgtData, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(srcData, tgtData) {
		return fmt.Errorf("%w: %s differs from source", ErrLinkConflict, target)
	}
	return os.Remove(target)
}
