package actions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrConflict means the file on disk changed between when the editor
// opened it and the save attempt — another process (an IDE, another
// lazyagent, the user editing externally) wrote something we'd be
// about to overwrite. Callers should surface a merge confirm rather
// than silently clobber.
var ErrConflict = errors.New("file changed on disk since open — refusing to overwrite")

// FileMtime returns the modification time of path, or an error wrapped
// from os.Stat. Used by the editor to snapshot the on-open timestamp
// for later conflict detection.
func FileMtime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// SaveFile writes content to path atomically (tmp + rename). If
// expectedMtime is non-zero and the file's current mtime differs,
// returns ErrConflict so the caller can offer overwrite/discard/merge
// to the user. A zero expectedMtime opts out of the check (used when
// saving a brand-new file from the create flow). Parent directories
// are created as needed; permissions default to 0644.
func SaveFile(path string, content []byte, expectedMtime time.Time) error {
	if !expectedMtime.IsZero() {
		current, err := FileMtime(path)
		if err == nil && !current.Equal(expectedMtime) {
			return fmt.Errorf("%w: %s", ErrConflict, path)
		}
		// File missing is fine — caller may be saving over a deleted item.
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
