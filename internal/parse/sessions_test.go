package parse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsPrivateSessionCwd(t *testing.T) {
	// Use a synthetic $HOME outside /var to avoid colliding with the
	// /var/folders prefix used by t.TempDir() on macOS — those would
	// be flagged as Private by design and the test wants to exercise
	// the *non*-private branch as well.
	home := "/Users/testfake"
	t.Setenv("HOME", home)

	cases := []struct {
		cwd  string
		want bool
		why  string
	}{
		{"", true, "empty cwd → zombie"},
		{"/private/tmp/session-foo", true, "system tmp"},
		{"/tmp/scratch", true, "system tmp"},
		{"/var/folders/abc/T/lazy", true, "macOS user tmp"},
		{"/var/tmp/whatever", true, "system tmp"},
		{filepath.Join(home, ".claude", "projects"), true, "tool internal"},
		{filepath.Join(home, ".codex", "any"), true, "tool internal"},
		{filepath.Join(home, ".gemini"), true, "tool internal exact match"},
		{filepath.Join(home, ".lazyagent", "store"), true, "tool internal"},
		{filepath.Join(home, ".claude-squad", "worktrees", "branch", "id"), true, "claude-squad orchestrator"},
		{filepath.Join(home, "conductor", "workspaces", "proj", "city"), true, "conductor orchestrator"},
		{filepath.Join(home, "Projects", "lazyagent"), false, "real project"},
		{filepath.Join(home, "Improvado", "ai-agent-improvado", "subdir"), false, "real subproject"},
		{"/private/tmp-not-really", false, "prefix lookalike must not match"},
	}
	for _, c := range cases {
		if got := IsPrivateSessionCwd(c.cwd); got != c.want {
			t.Errorf("IsPrivateSessionCwd(%q) = %v, want %v (%s)", c.cwd, got, c.want, c.why)
		}
	}
}

func TestSessionIsLocal(t *testing.T) {
	cases := []struct {
		cwd, projectDir string
		want            bool
	}{
		{"/Users/foo/Projects/app", "/Users/foo/Projects/app", true},
		{"/Users/foo/Projects/app/subpkg", "/Users/foo/Projects/app", true},
		{"/Users/foo/Projects/other", "/Users/foo/Projects/app", false},
		{"/Users/foo/Projects/appartment", "/Users/foo/Projects/app", false}, // prefix lookalike
		{"", "/Users/foo/Projects/app", false},
		{"/Users/foo/Projects/app", "", false},
	}
	for _, c := range cases {
		if got := SessionIsLocal(c.cwd, c.projectDir); got != c.want {
			t.Errorf("SessionIsLocal(%q,%q) = %v, want %v", c.cwd, c.projectDir, got, c.want)
		}
	}
}

func TestIsPrivateSessionCwd_NoHome(t *testing.T) {
	// Edge case: $HOME unset / unreadable. tmp roots still classify as
	// private; tool-internal checks are skipped without a usable home.
	t.Setenv("HOME", "")
	if !IsPrivateSessionCwd("/tmp/x") {
		t.Errorf("system tmp must classify as private without $HOME")
	}
	if IsPrivateSessionCwd("/Users/foo/code") {
		t.Errorf("regular path must not classify as private")
	}
	// sanity: skip if os.UserHomeDir succeeded somehow
	if h, _ := os.UserHomeDir(); h != "" {
		t.Skip("$HOME still resolved, skipping no-home assertion")
	}
}
