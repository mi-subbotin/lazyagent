package actions

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

func TestResumeCommandCodex(t *testing.T) {
	it := model.Item{
		Origin:    model.OriginCodex,
		Kind:      model.KindSession,
		ConfigKey: "019de4e2-fa46-7b70-bb1b-9267d0903bb1",
		Meta:      map[string]string{"cwd": "/Users/foo/projC"},
	}
	cmd, err := ResumeCommand(it, ResumeContext{})
	if err != nil {
		t.Fatalf("ResumeCommand: %v", err)
	}
	if !strings.HasSuffix(cmd.Path, "codex") {
		t.Errorf("expected codex binary, got %q", cmd.Path)
	}
	if got := cmd.Args[1:]; len(got) != 2 || got[0] != "resume" || got[1] != "019de4e2-fa46-7b70-bb1b-9267d0903bb1" {
		t.Errorf("expected [resume <id>], got %v", got)
	}
	if cmd.Dir != "/Users/foo/projC" {
		t.Errorf("expected Cmd.Dir=cwd from Meta, got %q", cmd.Dir)
	}
}

func TestResumeCommandCodexMissingID(t *testing.T) {
	it := model.Item{Origin: model.OriginCodex, Kind: model.KindSession}
	if _, err := ResumeCommand(it, ResumeContext{}); !errors.Is(err, ErrResumeUnsupported) {
		t.Errorf("codex without sessionId must return ErrResumeUnsupported, got %v", err)
	}
}

// TestResumeCommandGeminiCwdMeta covers the newer Gemini layout where
// the adapter stamps Meta["cwd"] directly from a .project_root marker.
// Resume should pin Cmd.Dir straight from that, regardless of scope or
// hash-index state.
func TestResumeCommandGeminiCwdMeta(t *testing.T) {
	it := model.Item{
		Origin: model.OriginGemini,
		Kind:   model.KindSession,
		Scope:  model.ScopeGlobal,
		Meta:   map[string]string{"index": "1", "cwd": "/Users/foo/from-marker"},
	}
	cmd, err := ResumeCommand(it, ResumeContext{})
	if err != nil {
		t.Fatalf("ResumeCommand: %v", err)
	}
	if cmd.Dir != "/Users/foo/from-marker" {
		t.Errorf("Cmd.Dir = %q, want recovered cwd from marker", cmd.Dir)
	}
}

// TestPlanResumeCwdGone exercises the dedicated error path for
// sessions whose project dir was deleted. Must trip before any
// per-Origin logic so Codex/Claude/Gemini all share the same toast
// message.
func TestPlanResumeCwdGone(t *testing.T) {
	for _, origin := range []model.Origin{model.OriginClaude, model.OriginGemini, model.OriginCodex} {
		it := model.Item{
			Origin:    origin,
			Kind:      model.KindSession,
			ConfigKey: "x",
			Meta:      map[string]string{"cwd": "/no/such/path", "cwdGone": "1", "index": "1"},
		}
		_, err := ResumeCommand(it, ResumeContext{})
		if !errors.Is(err, ErrResumeUnsupported) {
			t.Errorf("%v: want ErrResumeUnsupported, got %v", origin, err)
		}
		if err == nil || !strings.Contains(err.Error(), "deleted") {
			t.Errorf("%v: error should mention 'deleted', got %q", origin, err)
		}
	}
}

func TestEnrichSessionCwdsResolvesAndFlagsMissing(t *testing.T) {
	existing := t.TempDir()
	missing := filepath.Join(existing, "nope-removed")
	hashOfMissing := sha256SumHex(missing)

	items := []model.Item{
		// Claude with cwd that exists — no cwdGone, untouched.
		{Origin: model.OriginClaude, Kind: model.KindSession, Meta: map[string]string{"cwd": existing}},
		// Claude with cwd that's gone — should be flagged.
		{Origin: model.OriginClaude, Kind: model.KindSession, Meta: map[string]string{"cwd": missing}},
		// Gemini with only projectHash — index supplies cwd, then
		// existence check trips since the dir is missing.
		{Origin: model.OriginGemini, Kind: model.KindSession, Meta: map[string]string{"projectHash": hashOfMissing}},
		// Non-session: ignored entirely.
		{Origin: model.OriginClaude, Kind: model.KindSkill, Meta: map[string]string{"cwd": missing}},
	}
	// Seed the hash-index source: a Claude item with the missing cwd
	// is enough — BuildHashCwdIndex pulls it from Meta["cwd"].
	items = append(items, model.Item{
		Origin: model.OriginClaude,
		Kind:   model.KindSession,
		Meta:   map[string]string{"cwd": missing},
	})

	EnrichSessionCwds(items)

	if items[0].Meta["cwdGone"] != "" {
		t.Errorf("session 0 (existing cwd) should not be flagged, got %q", items[0].Meta["cwdGone"])
	}
	if items[1].Meta["cwdGone"] != "1" {
		t.Errorf("session 1 (deleted cwd) should be flagged, got %q", items[1].Meta["cwdGone"])
	}
	if items[2].Meta["cwd"] != missing {
		t.Errorf("session 2 should have cwd resolved via hash, got %q", items[2].Meta["cwd"])
	}
	if items[2].Meta["cwdGone"] != "1" {
		t.Errorf("session 2 should be flagged after resolution, got %q", items[2].Meta["cwdGone"])
	}
	if items[3].Meta["cwdGone"] != "" {
		t.Errorf("non-session item must not be touched, got %q", items[3].Meta["cwdGone"])
	}
}

// TestEnrichSessionCwdsRewritesProjectLabel verifies that
// EnrichSessionCwds replaces the adapter-set short-hash project label
// with the basename of the resolved cwd. Tests the non-git path: when
// `git` isn't on PATH (or cwd isn't a repo), we fall back to
// filepath.Base(cwd).
func TestEnrichSessionCwdsRewritesProjectLabel(t *testing.T) {
	dir := t.TempDir()
	cwd := filepath.Join(dir, "myproj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	items := []model.Item{
		{
			Origin:      model.OriginGemini,
			Kind:        model.KindSession,
			Description: "076e7c55 · today",
			Meta: map[string]string{
				"cwd":         cwd,
				"project":     "076e7c55", // hash prefix from adapter
				"projectHash": "076e7c55b9606ec1a2e54e9405faf6a364f68eb5cffa16b9afe2e15078cabf4e",
			},
		},
	}
	EnrichSessionCwds(items)
	if items[0].Meta["project"] != "myproj" {
		t.Errorf("project=%q, want basename %q", items[0].Meta["project"], "myproj")
	}
	if !strings.HasPrefix(items[0].Description, "myproj ·") {
		t.Errorf("Description not patched: %q", items[0].Description)
	}
}

// TestEnrichSessionCwdsGroupsGitWorktrees plants a tiny git repo with
// a worktree, then verifies sessions for both the main worktree and
// the feature worktree end up under the same Meta["project"] label.
func TestEnrichSessionCwdsGroupsGitWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	main := filepath.Join(dir, "ai-agent-improvado")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(workdir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = workdir
		// Hermetic config — don't pick up the dev's user.email or pgp
		// signing settings.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(main, "init", "-b", "main")
	run(main, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(dir, "ai-agent-improvado-feature-AI-579")
	run(main, "worktree", "add", "-b", "feature/AI-579", worktree)

	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindSession, Meta: map[string]string{"cwd": main, "project": "ai-agent-improvado"}},
		{Origin: model.OriginClaude, Kind: model.KindSession, Meta: map[string]string{"cwd": worktree, "project": "ai-agent-improvado-feature-AI-579"}},
	}
	EnrichSessionCwds(items)

	if got := items[0].Meta["project"]; got != "ai-agent-improvado" {
		t.Errorf("main repo project=%q, want ai-agent-improvado", got)
	}
	if got := items[1].Meta["project"]; got != "ai-agent-improvado" {
		t.Errorf("worktree project=%q, want ai-agent-improvado (collapse with main)", got)
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

// TestBuildHashCwdIndexFromGeminiProjectRoots covers the Gemini ≥0.40
// rescue path: a `.project_root` marker under ~/.gemini/tmp/<bucket>/
// gives us back the absolute cwd, and we hash it so old-layout
// (sha256-named) buckets for the same project become resolvable.
func TestBuildHashCwdIndexFromGeminiProjectRoots(t *testing.T) {
	fakeHome := t.TempDir()
	cwd := "/Users/foo/Projects/myapp"
	bucket := fakeHome + "/.gemini/tmp/myapp"
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bucket+"/.project_root", []byte(cwd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := buildHashCwdIndex(nil, fakeHome)
	if got[sha256SumHex(cwd)] != cwd {
		t.Errorf("expected sha256(%q)→%q in index, got %v", cwd, cwd, got)
	}
}

// TestBuildHashCwdIndexFromGeminiSessionItems verifies that Meta["cwd"]
// stamped by the gemini adapter (from .project_root) folds into the
// hash index just like Claude cwds — and is enough to resolve another
// session in an old-layout bucket whose projectHash is sha256(cwd).
func TestBuildHashCwdIndexFromGeminiSessionItems(t *testing.T) {
	cwd := "/Users/foo/projG"
	items := []model.Item{
		{Origin: model.OriginGemini, Kind: model.KindSession, Meta: map[string]string{"cwd": cwd}},
	}
	got := buildHashCwdIndex(items, "")
	if got[sha256SumHex(cwd)] != cwd {
		t.Errorf("expected gemini cwd in index, got %v", got)
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

func TestResumeNewTabCommandWarp(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "WarpTerminal")
	it := model.Item{
		Origin: model.OriginGemini,
		Kind:   model.KindSession,
		Scope:  model.ScopeLocal,
		Meta:   map[string]string{"index": "1"},
	}
	cmd, err := ResumeNewTabCommand(it, ResumeContext{ProjectDir: "/Users/foo/proj space"})
	if err != nil {
		t.Fatalf("ResumeNewTabCommand: %v", err)
	}
	script := cmd.Args[2]
	for _, want := range []string{
		`tell application "Warp"`,
		"warp://action/new_tab?path=",
		"%2Fproj+space", // url-encoded segment
		"keystroke",
		"'gemini'",
		"key code 36",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("Warp script missing %q:\n%s", want, script)
		}
	}
	// Bare command path should NOT include `cd` (Warp sets cwd via the URL).
	if strings.Contains(script, "cd ") {
		t.Errorf("Warp script must not embed `cd` — relies on URL path:\n%s", script)
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
