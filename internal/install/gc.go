package install

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AutoGC sweeps tarball cache directories whose sha isn't referenced
// by the manifest. Used by the TUI on startup so abandoned downloads
// don't accumulate forever; called more aggressively from the
// `lazyagent cache gc` CLI verb.
//
// AutoGC respects a `.last-gc` marker in cacheDir: if the marker
// exists and is younger than minInterval the call is a no-op. Pass
// minInterval=0 to force a run.
func AutoGC(cacheDir, manifestPath string, minInterval time.Duration) (removed int, err error) {
	if cacheDir == "" {
		return 0, errors.New("AutoGC: empty cacheDir")
	}
	if _, err := os.Stat(cacheDir); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	marker := filepath.Join(cacheDir, ".last-gc")
	if minInterval > 0 {
		if info, err := os.Stat(marker); err == nil {
			if time.Since(info.ModTime()) < minInterval {
				return 0, nil
			}
		}
	}

	manifest, err := Load(manifestPath)
	if err != nil {
		return 0, fmt.Errorf("load manifest: %w", err)
	}
	keep := manifest.Shas()

	err = filepath.WalkDir(cacheDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		base := d.Name()
		if !strings.Contains(base, "@") {
			return nil
		}
		sha := base[strings.LastIndex(base, "@")+1:]
		if _, ok := keep[sha]; ok {
			return filepath.SkipDir
		}
		if err := os.RemoveAll(path); err != nil {
			slog.Warn("install: AutoGC remove failed", "path", path, "err", err)
			return nil
		}
		removed++
		slog.Info("install: AutoGC removed stale cache", "path", path)
		return filepath.SkipDir
	})
	if err != nil {
		return removed, err
	}

	// Touch the marker either way — even on a clean run we don't want
	// to re-scan immediately on the next startup.
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		slog.Warn("install: AutoGC marker write failed", "path", marker, "err", err)
	}
	return removed, nil
}
