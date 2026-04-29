package actions

import (
	"path/filepath"
	"testing"
)

// canonicalTempDir returns t.TempDir() with symlinks resolved.
//
// On macOS t.TempDir() lives under /var/folders/... but /var is a
// symlink to /private/var. store.Root() resolves symlinks for the
// canonical store path, so an unresolved tmp path passed via
// LAZYAGENT_STORE will not match the Readlink output of projection
// symlinks. Tests use this helper to compare like-with-like.
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	real, err := filepath.EvalSymlinks(d)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", d, err)
	}
	return real
}
