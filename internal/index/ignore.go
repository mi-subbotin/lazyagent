// Privacy filter for the global indexer (PRI-10).
//
// Reads ~/.lazyagent/ignore — a gitignore-syntax file — and exposes a
// matcher the walker consults before recording a project. Use case:
// keep work / corporate / experimental project trees out of the
// "all-local" view without having to delete the underlying tool config.
//
// Scope is intentionally narrow — only the global indexer (PRI-4)
// honours these patterns. Cross-tool copy / install-from-github /
// shared store all operate on paths the user pointed at explicitly,
// so a privacy filter would only get in the way there.
//
// Pattern leniency: lines starting with "~/" or "$HOME/" are expanded
// to the user's home directory at load time. Everything else passes
// through to github.com/sabhiram/go-gitignore unchanged, so standard
// gitignore semantics (negations, globs, anchored paths) apply.

package index

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"
)

// Ignore wraps a compiled gitignore matcher. A nil *Ignore is a valid
// "no rules" state — Match returns false — so callers can hold and
// pass it through without nil-guards at every site.
type Ignore struct {
	matcher *ignore.GitIgnore
}

// IgnorePath returns the canonical location of the ignore file. Sits
// next to state.json, config.toml and index.json under ~/.lazyagent/.
func IgnorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lazyagent", "ignore"), nil
}

// LoadIgnore reads ~/.lazyagent/ignore. A missing file returns a nil
// *Ignore and no error — that is the normal first-run state, and the
// walker treats it as "filter disabled". Any other I/O or parse error
// is returned so callers can surface it in the log.
func LoadIgnore() (*Ignore, error) {
	path, err := IgnorePath()
	if err != nil {
		return nil, err
	}
	return LoadIgnoreFile(path)
}

// LoadIgnoreFile is the same as LoadIgnore but at an arbitrary path.
// Exposed for tests and for the eventual per-project `.lazyagentignore`.
func LoadIgnoreFile(path string) (*Ignore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	home, _ := os.UserHomeDir()
	raw := strings.Split(string(data), "\n")
	expanded := make([]string, 0, len(raw))
	for _, line := range raw {
		expanded = append(expanded, expandHome(line, home))
	}
	return &Ignore{matcher: ignore.CompileIgnoreLines(expanded...)}, nil
}

// Match reports whether the given absolute path is excluded by any
// pattern. Returns false on a nil receiver so the call site stays
// straightforward.
func (ig *Ignore) Match(path string) bool {
	if ig == nil || ig.matcher == nil {
		return false
	}
	return ig.matcher.MatchesPath(path)
}

// AppendPattern appends a single pattern to ~/.lazyagent/ignore,
// creating the file (and the parent directory) if needed. Returns the
// canonical path so CLI callers can echo it. Patterns are written
// verbatim — whitespace trimming and comment handling is the matcher's
// job, not the writer's.
func AppendPattern(pattern string) (string, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", errors.New("empty pattern")
	}
	path, err := IgnorePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return path, err
	}
	defer f.Close()
	// Drop a trailing newline if the existing file lacks one — keeps
	// the file readable and ensures the new pattern is on its own line.
	if needsLeadingNewline(path) {
		if _, err := f.WriteString("\n"); err != nil {
			return path, err
		}
	}
	if _, err := f.WriteString(pattern + "\n"); err != nil {
		return path, err
	}
	return path, nil
}

// ListPatterns returns the non-empty, non-comment lines from the
// ignore file. Comments and blank lines are stripped because the
// caller (CLI `lazyagent ignore list`) is showing rules, not the
// raw file. A missing file yields an empty slice and no error.
func ListPatterns() ([]string, error) {
	path, err := IgnorePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// expandHome rewrites a leading "~/" or "$HOME/" to the absolute home
// directory. Comments / blank lines / unanchored patterns pass through
// untouched. Returning the original line on a missing $HOME keeps the
// matcher running in degraded mode rather than dropping the pattern.
func expandHome(line, home string) string {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") || home == "" {
		return line
	}
	switch {
	case strings.HasPrefix(t, "~/"):
		return filepath.Join(home, t[2:])
	case strings.HasPrefix(t, "$HOME/"):
		return filepath.Join(home, t[6:])
	}
	return line
}

// needsLeadingNewline reports whether the existing file ends in a
// non-newline byte, in which case AppendPattern must prepend one to
// keep the new pattern on its own line.
func needsLeadingNewline(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err := f.Seek(-1, 2); err != nil {
		return false
	}
	var b [1]byte
	if _, err := f.Read(b[:]); err != nil {
		return false
	}
	return b[0] != '\n'
}
