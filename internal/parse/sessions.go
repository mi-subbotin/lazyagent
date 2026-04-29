package parse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// IsPrivateSessionCwd flags conversations the user almost never wants
// to resume by hand. The classification is path-based and intentionally
// conservative — false positives clutter the Global list a little, but
// false negatives bury real projects under hundreds of orchestrator
// runs. Buckets:
//
//   - System tmp: /tmp, /private/tmp, /var/tmp, /var/folders.
//   - Tool internals: $HOME/.{claude,codex,gemini,lazyagent}.
//     Sessions started while cd'd into a tool's own config tree are
//     usually scratch / debugging.
//   - Orchestrators: claude-squad worktrees and Conductor workspaces.
//     These spawn one disposable cwd per task — dozens accumulate fast.
//   - Empty cwd: the jsonl had no `cwd` field readable; without
//     anchoring info we treat it as scratch rather than show it next
//     to real projects.
func IsPrivateSessionCwd(cwd string) bool {
	if cwd == "" {
		return true
	}
	clean := filepath.Clean(cwd)
	for _, p := range []string{"/private/tmp", "/tmp", "/var/folders", "/var/tmp"} {
		if hasPathPrefix(clean, p) {
			return true
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		home = filepath.Clean(home)
		for _, sub := range []string{".claude", ".codex", ".gemini", ".lazyagent"} {
			if hasPathPrefix(clean, filepath.Join(home, sub)) {
				return true
			}
		}
	}
	// Orchestrator markers — fragments anywhere in the path. Matched as
	// substrings rather than prefixes because users can put their home
	// directory or worktree root under arbitrary paths.
	for _, marker := range []string{
		"/.claude-squad/worktrees/",
		"/conductor/workspaces/",
	} {
		if strings.Contains(clean, marker) {
			return true
		}
	}
	return false
}

// SessionIsLocal reports whether cwd belongs to the current project
// — exact match or a subdirectory of projectDir. Returns false when
// projectDir is empty, since a no-project run never has a "local"
// scope to speak of.
func SessionIsLocal(cwd, projectDir string) bool {
	if cwd == "" || projectDir == "" {
		return false
	}
	return hasPathPrefix(filepath.Clean(cwd), filepath.Clean(projectDir))
}

// hasPathPrefix is filepath.HasPrefix done right — splits on the path
// separator so /a/bc never matches prefix /a/b. Local copy because
// internal/store has the same helper but importing it here would
// create a parse → store → parse loop.
func hasPathPrefix(path, prefix string) bool {
	if prefix == "" {
		return false
	}
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+string(filepath.Separator))
}

// SessionPreview shortens a multi-line first-user-message to a single
// row suitable as a tree label. Returns the original (with newlines
// flattened) when shorter than n runes; otherwise truncates and appends
// an ellipsis.
func SessionPreview(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// SessionFriendlyTime formats t as a relative duration ("3m ago",
// "2h ago", "5d ago") for short ranges and a calendar date past 30
// days. Used in session list rows so the user can scan recency at a
// glance without parsing ISO timestamps.
func SessionFriendlyTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// SessionBody renders the session detail header — id, project, last
// updated and the first user message — as markdown that the existing
// glamour pipeline in renderDetail will style. Body is built once at
// adapter-list time and stored on Item.Body; lazyloading the full
// transcript is deferred to a later slice.
func SessionBody(firstUserMsg, project, sessionID string, lastUpdated time.Time) string {
	var b strings.Builder
	if sessionID != "" {
		fmt.Fprintf(&b, "**Session** `%s`\n\n", sessionID)
	}
	if project != "" {
		fmt.Fprintf(&b, "- **Project:** %s\n", project)
	}
	if !lastUpdated.IsZero() {
		fmt.Fprintf(&b, "- **Last updated:** %s\n", lastUpdated.Format("2006-01-02 15:04"))
	}
	b.WriteString("\n")
	if firstUserMsg != "" {
		b.WriteString("---\n\n### First user message\n\n")
		b.WriteString(firstUserMsg)
		b.WriteString("\n\n")
	}
	b.WriteString("---\n_Press `R` to resume this session._\n")
	return b.String()
}
