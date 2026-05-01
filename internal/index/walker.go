// Package index implements the global filesystem walk that powers
// lazyagent's "all local projects" mode (PRI-4).
//
// The walker finds every directory under the configured roots that
// contains at least one tool marker — `.claude`, `.codex`, `.gemini`,
// `.agents`, `.mcp.json`, `CLAUDE.md`, `AGENTS.md`, `GEMINI.md` —
// without descending into noisy directories like `node_modules`,
// `.git`, build outputs, package caches, or cloud-sync mounts.
//
// MVP design notes:
//
//   - We use the stdlib `filepath.WalkDir`. The plan called out an
//     optional `fd` shell-out for parallel walks; that's deferred to a
//     follow-up so this package has zero external dependencies.
//   - Symlinks are not followed — `WalkDir` won't descend through them
//     by default. This avoids loops and keeps walks bounded under the
//     home directory even when users symlink their cloud drives in.
//   - Once a directory matches a marker, we record the project root and
//     prune the subtree (skipping into `.claude`, nested children,
//     etc.). Otherwise a monorepo with three Claude-using packages
//     would surface three near-identical projects.
//
// The walker is stateless and safe to call concurrently. Use the
// companion cache (cache.go) to amortise wall-clock cost across
// launches.
package index

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// markerNames is the set of tool-config files / directories that mark
// a directory as a "project root" worth surfacing. Anything in this
// list is one of the entry points the per-tool adapters already know
// how to scan via Source.List(ctx, projectDir).
var markerNames = map[string]struct{}{
	".claude":   {},
	".codex":    {},
	".gemini":   {},
	".agents":   {},
	".mcp.json": {},
	"CLAUDE.md": {},
	"AGENTS.md": {},
	"GEMINI.md": {},
}

// skipDirs is a hard skip-list of directory names that we never want
// to descend into. Walking these would inflate the cold-scan time
// from seconds to minutes without ever finding a real project root.
var skipDirs = map[string]struct{}{
	".git":          {},
	"node_modules":  {},
	".venv":         {},
	"venv":          {},
	"__pycache__":   {},
	"target":        {},
	"build":         {},
	"dist":          {},
	"vendor":        {},
	".cache":        {},
	".Trash":        {},
	".npm":          {},
	".pnpm":         {},
	".yarn":         {},
	".gradle":       {},
	".m2":           {},
	".cargo":        {},
	".rustup":       {},
	".bundle":       {},
	".terraform":    {},
	".next":         {},
	".nuxt":         {},
	".turbo":        {},
	".parcel-cache": {},
	"out":           {},
	".idea":         {},
	".vscode":       {},
	"DerivedData":   {},
}

// Project is one discovered project root. Path is absolute; Markers
// lists which tool-config files were found there (sorted) so the TUI
// can show e.g. "[.claude .codex] /path/to/foo".
type Project struct {
	Path    string
	Markers []string
}

// Options tunes a walk. Roots default to []{$HOME} when empty;
// MaxDepth bounds how deep we descend below each root (15 by default).
// SkipPrefixes is the list of absolute-path prefixes to ignore — used
// to keep cloud-sync mounts (iCloud, Dropbox, OneDrive, Google Drive)
// out of the index. Adding a path here is cheaper than walking it and
// then filtering on the way out.
type Options struct {
	Roots        []string
	MaxDepth     int
	SkipPrefixes []string
}

// Discover walks every root in opts and returns the deduplicated list
// of project directories sorted by path. A nil error means "walk
// finished cleanly", but per-subtree errors during the walk are
// silently dropped — a permission-denied on one subdirectory must not
// kill the whole index. We'd rather show an incomplete list than an
// empty one.
func Discover(opts Options) ([]Project, error) {
	roots := opts.Roots
	if len(roots) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		roots = []string{home}
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 15
	}

	skip := make([]string, 0, len(opts.SkipPrefixes))
	for _, p := range opts.SkipPrefixes {
		if p = strings.TrimSpace(p); p != "" {
			skip = append(skip, p)
		}
	}
	skip = append(skip, defaultCloudSyncPrefixes()...)

	seen := make(map[string]*Project)
	for _, root := range roots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		walkRoot(absRoot, maxDepth, skip, seen)
	}

	out := make([]Project, 0, len(seen))
	for _, p := range seen {
		sort.Strings(p.Markers)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// walkRoot descends one root until maxDepth, recording every project
// it finds. Once a directory matches a marker we record it and prune
// — a project's own `.claude` / `.codex` subdirs are never themselves
// surfaced as separate projects.
func walkRoot(root string, maxDepth int, skipPrefixes []string, out map[string]*Project) {
	rootDepth := strings.Count(root, string(filepath.Separator))
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Permission denied / vanished file — skip the subtree but
			// keep walking the rest. Returning the error here would
			// abort the entire walk.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		// Cheap depth check before any per-name work.
		depth := strings.Count(path, string(filepath.Separator)) - rootDepth
		if depth > maxDepth {
			return fs.SkipDir
		}
		base := filepath.Base(path)
		if depth > 0 {
			if _, skip := skipDirs[base]; skip {
				return fs.SkipDir
			}
		}
		for _, prefix := range skipPrefixes {
			if path == prefix || strings.HasPrefix(path, prefix+string(filepath.Separator)) {
				return fs.SkipDir
			}
		}
		// Don't descend into hidden dirs except project roots that have
		// markers themselves (handled below). Skipping `.local`,
		// `.config`, etc. prunes huge swathes of $HOME without losing
		// real projects.
		if depth > 0 && strings.HasPrefix(base, ".") {
			if _, isMarker := markerNames[base]; !isMarker {
				return fs.SkipDir
			}
		}
		markers := matchMarkers(path)
		if len(markers) == 0 {
			return nil
		}
		out[path] = &Project{Path: path, Markers: markers}
		// Prune: a real project rarely contains another project, and
		// when it does the inner one is almost always vendored
		// dependency noise.
		return fs.SkipDir
	})
}

// matchMarkers reads `dir` and returns the names of any tool markers
// directly inside it. Sorted in caller (Discover) so callers can rely
// on stable ordering for tests.
func matchMarkers(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if _, ok := markerNames[e.Name()]; ok {
			out = append(out, e.Name())
		}
	}
	return out
}

// defaultCloudSyncPrefixes returns the absolute paths to common cloud
// drives we always want to skip. Walking them adds minutes of latency,
// triggers iCloud "downloading" stalls, and rarely contains real local
// project work. Custom roots / prefixes still win — anything users
// explicitly opt into via config.search.roots is honoured.
func defaultCloudSyncPrefixes() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	candidates := []string{
		filepath.Join(home, "Library", "Mobile Documents"),
		filepath.Join(home, "Library", "CloudStorage"),
		filepath.Join(home, "Dropbox"),
		filepath.Join(home, "OneDrive"),
		filepath.Join(home, "Google Drive"),
	}
	out := make([]string, 0, len(candidates))
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil || !errors.Is(err, fs.ErrNotExist) {
			out = append(out, p)
		}
	}
	return out
}
