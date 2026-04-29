package actions

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestResumeCommandClaude(t *testing.T) {
	it := model.Item{
		Origin:    model.OriginClaude,
		Kind:      model.KindSession,
		ConfigKey: "abc-123",
		Meta:      map[string]string{"cwd": "/Users/foo/proj"},
	}
	cmd, err := ResumeCommand(it, ResumeContext{})
	if err != nil {
		t.Fatalf("ResumeCommand: %v", err)
	}
	if !strings.HasSuffix(cmd.Path, "claude") {
		t.Errorf("expected claude binary, got %q", cmd.Path)
	}
	if got := cmd.Args[1:]; len(got) != 2 || got[0] != "-r" || got[1] != "abc-123" {
		t.Errorf("expected [-r abc-123], got %v", got)
	}
	if cmd.Dir != "/Users/foo/proj" {
		t.Errorf("expected Cmd.Dir=cwd from Meta, got %q", cmd.Dir)
	}
}

// TestResumeCommandClaudeNoCwdLeavesDirEmpty — zombie sessions with no
// recorded cwd should still produce a runnable command (claude will
// just look in the current shell cwd).
func TestResumeCommandClaudeNoCwdLeavesDirEmpty(t *testing.T) {
	it := model.Item{
		Origin:    model.OriginClaude,
		Kind:      model.KindSession,
		ConfigKey: "abc-123",
	}
	cmd, err := ResumeCommand(it, ResumeContext{})
	if err != nil {
		t.Fatalf("ResumeCommand: %v", err)
	}
	if cmd.Dir != "" {
		t.Errorf("expected empty Cmd.Dir when Meta cwd missing, got %q", cmd.Dir)
	}
}

func TestResumeCommandGeminiLocal(t *testing.T) {
	it := model.Item{
		Origin: model.OriginGemini,
		Kind:   model.KindSession,
		Scope:  model.ScopeLocal,
		Meta:   map[string]string{"index": "3"},
	}
	cmd, err := ResumeCommand(it, ResumeContext{ProjectDir: "/Users/foo/proj"})
	if err != nil {
		t.Fatalf("ResumeCommand: %v", err)
	}
	if got := cmd.Args[1:]; len(got) != 2 || got[0] != "--resume" || got[1] != "3" {
		t.Errorf("expected [--resume 3], got %v", got)
	}
	if cmd.Dir != "/Users/foo/proj" {
		t.Errorf("expected Cmd.Dir=projectDir, got %q", cmd.Dir)
	}
}

// TestResumeCommandGeminiGlobalViaIndex covers the lookup path that
// rescues Global Gemini sessions: when a session's projectHash is in
// the Claude-derived hash→cwd map, ResumeCommand pins Cmd.Dir to
// that recovered cwd instead of refusing.
func TestResumeCommandGeminiGlobalViaIndex(t *testing.T) {
	cwd := "/Users/foo/other-proj"
	hash := sha256SumHex(cwd)

	it := model.Item{
		Origin: model.OriginGemini,
		Kind:   model.KindSession,
		Scope:  model.ScopeGlobal,
		Meta:   map[string]string{"index": "1", "projectHash": hash},
	}
	ctx := ResumeContext{
		ProjectDir:   "/Users/foo/proj-current",
		KnownHashCwd: map[string]string{hash: cwd},
	}
	cmd, err := ResumeCommand(it, ctx)
	if err != nil {
		t.Fatalf("ResumeCommand: %v", err)
	}
	if cmd.Dir != cwd {
		t.Errorf("Cmd.Dir = %q, want %q", cmd.Dir, cwd)
	}
}

func TestResumeCommandGeminiGlobalUnknownHash(t *testing.T) {
	it := model.Item{
		Origin: model.OriginGemini,
		Kind:   model.KindSession,
		Scope:  model.ScopeGlobal,
		Meta:   map[string]string{"index": "1", "projectHash": "deadbeef"},
	}
	if _, err := ResumeCommand(it, ResumeContext{}); !errors.Is(err, ErrResumeUnsupported) {
		t.Errorf("expected ErrResumeUnsupported, got %v", err)
	}
}

func TestResumeCommandGeminiBadIndex(t *testing.T) {
	it := model.Item{
		Origin: model.OriginGemini,
		Kind:   model.KindSession,
		Scope:  model.ScopeLocal,
		Meta:   map[string]string{"index": "abc"},
	}
	if _, err := ResumeCommand(it, ResumeContext{ProjectDir: "/x"}); !errors.Is(err, ErrResumeUnsupported) {
		t.Errorf("non-numeric index must return ErrResumeUnsupported, got %v", err)
	}
}

func TestResumeCommandCodexUnsupported(t *testing.T) {
	it := model.Item{Origin: model.OriginCodex, Kind: model.KindSession}
	if _, err := ResumeCommand(it, ResumeContext{}); !errors.Is(err, ErrResumeUnsupported) {
		t.Errorf("codex must return ErrResumeUnsupported, got %v", err)
	}
}

func TestResumeCommandNotASession(t *testing.T) {
	it := model.Item{Origin: model.OriginClaude, Kind: model.KindSkill}
	if _, err := ResumeCommand(it, ResumeContext{}); !errors.Is(err, ErrResumeUnsupported) {
		t.Errorf("non-session must return ErrResumeUnsupported, got %v", err)
	}
}

func TestBuildHashCwdIndexFromClaudeItems(t *testing.T) {
	cwd1 := "/Users/foo/projA"
	cwd2 := "/Users/foo/projB"
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindSession, Meta: map[string]string{"cwd": cwd1}},
		{Origin: model.OriginClaude, Kind: model.KindSession, Meta: map[string]string{"cwd": cwd2}},
		{Origin: model.OriginClaude, Kind: model.KindSession, Meta: map[string]string{"cwd": cwd1}}, // duplicate
		{Origin: model.OriginClaude, Kind: model.KindSession, Meta: map[string]string{}},           // missing cwd
		{Origin: model.OriginGemini, Kind: model.KindSession, Meta: map[string]string{"projectHash": "x"}},
		{Origin: model.OriginClaude, Kind: model.KindSkill, Meta: map[string]string{"cwd": "ignored"}},
	}
	// home="" disables the walker so the test stays hermetic and does
	// not pick up entries from the dev's real $HOME.
	got := buildHashCwdIndex(items, "")
	if len(got) != 2 {
		t.Fatalf("want 2 unique hashes, got %d: %v", len(got), got)
	}
	if got[sha256SumHex(cwd1)] != cwd1 || got[sha256SumHex(cwd2)] != cwd2 {
		t.Errorf("index missing entries: %v", got)
	}
}

// TestBuildHashCwdIndexWalksHome plants a directory tree under a fake
// $HOME and verifies the walker hashes the project subdir we care
// about, while skipping noise dirs (.git, node_modules) and dirs
// deeper than maxDepth.
func TestBuildHashCwdIndexWalksHome(t *testing.T) {
	fakeHome := t.TempDir()
	mustMkdir := func(rel string) string {
		full := fakeHome + "/" + rel
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		return full
	}
	want := mustMkdir("Projects/myapp")
	noise := mustMkdir("Projects/myapp/node_modules/lodash")
	hidden := mustMkdir("Projects/myapp/.git")
	deep := mustMkdir("a/b/c/d/e/f") // beyond maxDepth=4 from fakeHome

	got := buildHashCwdIndex(nil, fakeHome)
	if got[sha256SumHex(want)] != want {
		t.Errorf("expected %q in index", want)
	}
	if _, hit := got[sha256SumHex(noise)]; hit {
		t.Errorf("node_modules path should be skipped: %v", noise)
	}
	if _, hit := got[sha256SumHex(hidden)]; hit {
		t.Errorf(".git path should be skipped: %v", hidden)
	}
	if _, hit := got[sha256SumHex(deep)]; hit {
		t.Errorf("path beyond maxDepth should not be indexed: %v", deep)
	}
}

func TestResumeNewTabCommandReturnsOsascript(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	it := model.Item{
		Origin:    model.OriginClaude,
		Kind:      model.KindSession,
		ConfigKey: "ses-1",
	}
	cmd, err := ResumeNewTabCommand(it, ResumeContext{})
	if err != nil {
		t.Fatalf("ResumeNewTabCommand: %v", err)
	}
	if !strings.HasSuffix(cmd.Path, "osascript") {
		t.Errorf("expected osascript, got %q", cmd.Path)
	}
	if len(cmd.Args) < 3 || cmd.Args[1] != "-e" {
		t.Fatalf("expected [osascript -e <script>], got %v", cmd.Args)
	}
	script := cmd.Args[2]
	for _, want := range []string{"iTerm2", "create tab", "claude", "ses-1"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
}

func TestResumeNewTabCommandTerminalAppDefault(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	it := model.Item{
		Origin: model.OriginGemini,
		Kind:   model.KindSession,
		Scope:  model.ScopeLocal,
		Meta:   map[string]string{"index": "1"},
	}
	cmd, err := ResumeNewTabCommand(it, ResumeContext{ProjectDir: "/Users/foo/proj"})
	if err != nil {
		t.Fatalf("ResumeNewTabCommand: %v", err)
	}
	script := cmd.Args[2]
	for _, want := range []string{`tell application "Terminal"`, "do script", "cd '/Users/foo/proj'", "'gemini'", "'--resume'", "'1'"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
}
