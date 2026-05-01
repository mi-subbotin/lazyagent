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
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
//
// MarkerMtimes is the unix-seconds mtime of each marker at walk time.
// Cache.MtimesUnchanged stats them again on launch and returns true
// when nothing has shifted, letting us skip a re-walk indefinitely
// instead of re-walking $HOME every 24h. Empty when the walk pre-dates
// PRI-56 — old caches are still loadable, they just always look stale.
type Project struct {
	Path         string           `json:"Path"`
	Markers      []string         `json:"Markers"`
	MarkerMtimes map[string]int64 `json:"MarkerMtimes,omitempty"`
}

// Options tunes a walk. Roots default to []{$HOME} when empty;
// MaxDepth bounds how deep we descend below each root (15 by default).
// SkipPrefixes is the list of absolute-path prefixes to ignore — used
// to keep cloud-sync mounts (iCloud, Dropbox, OneDrive, Google Drive)
// out of the index. Adding a path here is cheaper than walking it and
// then filtering on the way out.
//
// Ignore (PRI-10) is a user-supplied gitignore-style filter applied
// during the walk: any directory matching a pattern is pruned, and a
// late re-check before recording catches pattern shapes that match
// the project root but not its parents. Nil disables the filter.
type Options struct {
	Roots        []string
	MaxDepth     int
	SkipPrefixes []string
	Ignore       *Ignore
	// DisableFd forces the stdlib walker even when `fd` is available in
	// PATH. Used by tests so the deterministic walker shape is exercised
	// regardless of the host machine.
	DisableFd bool
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
	useFd := !opts.DisableFd && fdBinary() != ""
	for _, root := range roots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if useFd {
			if walkedWithFd(absRoot, maxDepth, skip, opts.Ignore, seen) {
				continue
			}
			// fd unavailable / failed for this root — fall back below.
		}
		walkRoot(absRoot, maxDepth, skip, opts.Ignore, seen)
	}

	out := make([]Project, 0, len(seen))
	for _, p := range seen {
		sort.Strings(p.Markers)
		p.MarkerMtimes = readMarkerMtimes(p.Path, p.Markers)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// readMarkerMtimes stats each marker under dir and returns a map from
// marker name to unix-seconds mtime. Missing markers (a race where the
// marker disappeared between the walk and this stat) are silently
// dropped — the next walk would catch it via MtimesUnchanged returning
// false.
func readMarkerMtimes(dir string, markers []string) map[string]int64 {
	if len(markers) == 0 {
		return nil
	}
	out := make(map[string]int64, len(markers))
	for _, m := range markers {
		info, err := os.Stat(filepath.Join(dir, m))
		if err != nil {
			continue
		}
		out[m] = info.ModTime().Unix()
	}
	return out
}

// walkRoot descends one root until maxDepth, recording every project
// it finds. Once a directory matches a marker we record it and prune
// — a project's own `.claude` / `.codex` subdirs are never themselves
// surfaced as separate projects.
func walkRoot(root string, maxDepth int, skipPrefixes []string, ig *Ignore, out map[string]*Project) {
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
		// PRI-10: prune anything matching the user's ignore file
		// before any per-name work. Same semantics as skipPrefixes —
		// the cheapest place to drop a subtree is before reading it.
		if ig.Match(path) {
			return fs.SkipDir
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
		// Defensive late re-check: a pattern shaped like `**/private-*`
		// may not match the parent on the way down (depending on the
		// matcher's anchoring) but still wants the leaf out of the
		// index. Cheap, and clearer than relying on prune semantics.
		if ig.Match(path) {
			return fs.SkipDir
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

// fdBinary locates `fd` (or its Debian-renamed `fdfind`) in PATH and
// returns the absolute path to the executable, or "" when neither is
// available. We probe both names so the same code works on macOS /
// Arch / Fedora (where it's `fd`) and Debian / Ubuntu (where the
// official package ships as `fdfind` to avoid a conflict).
func fdBinary() string {
	for _, name := range []string{"fd", "fdfind"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// walkedWithFd performs the marker-discovery pass via `fd`, populating
// `out` with one Project per directory whose entries include any of
// markerNames. Returns true on success; false signals the caller to
// fall back to the stdlib walker.
//
// fd is run with --hidden so dotfiles surface, --no-ignore so the user's
// gitignore patterns don't accidentally hide real projects (we run our
// own ignore pass via opts.Ignore), --max-depth from opts, and the
// skipDirs list mapped onto -E (--exclude) flags. The pattern is a
// regex anchored to the basename: only direct marker filenames match.
//
// Output lines are absolute paths to marker entries; the project root
// is filepath.Dir(line). We dedupe by parent so a project with several
// markers (e.g. .claude + AGENTS.md) lands as one entry.
func walkedWithFd(root string, maxDepth int, skipPrefixes []string, ig *Ignore, out map[string]*Project) bool {
	bin := fdBinary()
	if bin == "" {
		return false
	}
	args := []string{
		"--hidden",
		"--no-ignore",
		"--absolute-path",
		"--max-depth", strconv.Itoa(maxDepth),
	}
	for name := range skipDirs {
		args = append(args, "--exclude", name)
	}
	pattern := `^(\.claude|\.codex|\.gemini|\.agents|\.mcp\.json|CLAUDE\.md|AGENTS\.md|GEMINI\.md)$`
	args = append(args, pattern, root)

	cmd := exec.Command(bin, args...)
	stdout, err := cmd.Output()
	if err != nil {
		return false
	}

	type pending struct {
		dir     string
		markers map[string]struct{}
	}
	pendings := make(map[string]*pending)

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		base := filepath.Base(line)
		if _, ok := markerNames[base]; !ok {
			continue
		}
		dir := filepath.Dir(line)
		// Cloud-sync / explicit skip prefix filtering: the stdlib walker
		// short-circuits these via fs.SkipDir, but fd has no equivalent
		// hook, so we filter on the way out.
		skipped := false
		for _, prefix := range skipPrefixes {
			if dir == prefix || strings.HasPrefix(dir, prefix+string(filepath.Separator)) {
				skipped = true
				break
			}
		}
		if skipped {
			continue
		}
		if ig.Match(dir) {
			continue
		}
		p, ok := pendings[dir]
		if !ok {
			p = &pending{dir: dir, markers: map[string]struct{}{}}
			pendings[dir] = p
		}
		p.markers[base] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return false
	}

	// Vendored-project pruning: when a project sits inside another
	// project (typical: monorepo .claude at the root, plus a vendored
	// .claude under `node_modules` that fd's --exclude already dropped,
	// or a deeper sub-package), keep only the shallowest ancestor. The
	// stdlib walker gets this for free via fs.SkipDir; we replay it.
	dirs := make([]string, 0, len(pendings))
	for d := range pendings {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		if _, dropped := pendings[d]; !dropped {
			continue
		}
		// Drop any pending entry that's a strict descendant of d.
		for _, child := range dirs {
			if child == d {
				continue
			}
			if strings.HasPrefix(child, d+string(filepath.Separator)) {
				delete(pendings, child)
			}
		}
	}

	for d, p := range pendings {
		markers := make([]string, 0, len(p.markers))
		for m := range p.markers {
			markers = append(markers, m)
		}
		out[d] = &Project{Path: d, Markers: markers}
	}
	return true
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
