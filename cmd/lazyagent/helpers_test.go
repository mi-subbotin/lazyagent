package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/actions"
	"github.com/mi-subbotin/lazyagent/internal/index"
	"github.com/mi-subbotin/lazyagent/internal/install"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestMultiFlagSetAccumulates(t *testing.T) {
	var m multiFlag
	if err := m.Set("a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Set("b"); err != nil {
		t.Fatal(err)
	}
	if got, want := m.String(), "a,b"; got != want {
		t.Errorf("String()=%q, want %q", got, want)
	}
	if len(m) != 2 {
		t.Errorf("len=%d, want 2", len(m))
	}
}

func TestResolveSearchRootsExpandsAndDedupes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgRoots := []string{"~/foo", " ~/bar ", ""}
	cli := multiFlag{"~/foo", "~/baz"}

	got := resolveSearchRoots(cfgRoots, cli)

	wantSuffixes := []string{"/foo", "/bar", "/baz"}
	if len(got) != len(wantSuffixes) {
		t.Fatalf("got %d roots, want %d: %v", len(got), len(wantSuffixes), got)
	}
	for i, suffix := range wantSuffixes {
		if !strings.HasSuffix(got[i], suffix) {
			t.Errorf("got[%d]=%q, want suffix %q", i, got[i], suffix)
		}
		if !filepath.IsAbs(got[i]) {
			t.Errorf("got[%d]=%q must be absolute", i, got[i])
		}
	}
}

func TestResolveSearchRootsEmpty(t *testing.T) {
	if got := resolveSearchRoots(nil, nil); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestProjectsFromCache(t *testing.T) {
	c := index.Cache{
		Projects: []index.Project{
			{Path: "/a"},
			{Path: "/b"},
		},
	}
	got := projectsFromCache(c)
	if len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Errorf("projectsFromCache = %v", got)
	}
	if len(projectsFromCache(index.Cache{})) != 0 {
		t.Errorf("empty cache should yield empty slice")
	}
}

func TestFilterIgnoredNilPassthrough(t *testing.T) {
	in := []string{"/a", "/b"}
	got := filterIgnored(in, nil)
	if len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Errorf("nil ignore must passthrough, got %v", got)
	}
}

func TestFilterIgnoredAppliesRules(t *testing.T) {
	dir := t.TempDir()
	rules := filepath.Join(dir, "ignore")
	if err := os.WriteFile(rules, []byte("/secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ig, err := index.LoadIgnoreFile(rules)
	if err != nil {
		t.Fatal(err)
	}
	in := []string{"/secret/proj", "/public/proj"}
	got := filterIgnored(in, ig)
	if len(got) != 1 || got[0] != "/public/proj" {
		t.Errorf("expected only /public/proj, got %v", got)
	}
}

func TestDetectInstallSourceLDFlag(t *testing.T) {
	prev := installSource
	t.Cleanup(func() { installSource = prev })
	installSource = "brew"
	if got := detectInstallSource(); got != "brew" {
		t.Errorf("ldflag override: got %q, want brew", got)
	}
}

func TestDetectInstallSourceGoInstall(t *testing.T) {
	prev := installSource
	t.Cleanup(func() { installSource = prev })
	installSource = ""

	// detectInstallSource compares filepath.Dir(os.Executable()) against
	// $GOPATH/bin. The test binary's exe path won't equal that, but we
	// can at least exercise the no-marker path: it must be one of the
	// three known values.
	got := detectInstallSource()
	switch got {
	case "brew", "go-install", "unknown":
	default:
		t.Errorf("unexpected install source %q", got)
	}
}

func TestFilterByTarget(t *testing.T) {
	in := []install.Install{
		{TargetOrigin: "claude"},
		{TargetOrigin: "codex"},
		{TargetOrigin: "claude"},
	}
	got := filterByTarget(in, "Claude")
	if len(got) != 2 {
		t.Errorf("expected 2 claude rows, got %d: %v", len(got), got)
	}
	if got := filterByTarget(in, "missing"); len(got) != 0 {
		t.Errorf("unknown origin must yield empty, got %v", got)
	}
}

func TestFilterByScope(t *testing.T) {
	in := []install.Install{
		{TargetScope: "global"},
		{TargetScope: "local"},
		{TargetScope: "global"},
	}
	got := filterByScope(in, "Global")
	if len(got) != 2 {
		t.Errorf("expected 2 global rows, got %d: %v", len(got), got)
	}
}

func TestDefaultCacheDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := defaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".lazyagent", "cache")
	if got != want {
		t.Errorf("defaultCacheDir = %q, want %q", got, want)
	}
}

func TestReadLastLines(t *testing.T) {
	body := "a\nb\nc\nd\ne\n"
	got, err := readLastLines(strings.NewReader(body), 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"c", "d", "e"}
	if len(got) != len(want) {
		t.Fatalf("got %d, want %d: %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
	if got, _ := readLastLines(strings.NewReader(""), 5); len(got) != 0 {
		t.Errorf("empty input should yield empty slice")
	}
	if got, _ := readLastLines(strings.NewReader("x\ny\n"), 0); got != nil {
		t.Errorf("n=0 should yield nil")
	}
}

func TestResolveLogPathFallsBackToDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := resolveLogPath()
	if got == "" {
		t.Fatal("resolveLogPath returned empty")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
	if !strings.Contains(got, ".lazyagent") {
		t.Errorf("expected default path under .lazyagent, got %q", got)
	}
}

func TestLoadConfigOrWarn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := loadConfigOrWarn()
	if cfg == nil {
		t.Fatal("expected non-nil config (default fallback)")
	}
}

func TestLogsCleanRemovesPrimaryAndRotated(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "lazyagent.log")
	rotatedPlain := filepath.Join(dir, "lazyagent-2026-04-30T12-00-00.000.log")
	rotatedGz := filepath.Join(dir, "lazyagent-2026-04-30T12-00-00.000.log.gz")
	unrelated := filepath.Join(dir, "other.txt")

	for _, p := range []string{primary, rotatedPlain, rotatedGz, unrelated} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := logsClean(primary); err != nil {
		t.Fatalf("logsClean: %v", err)
	}
	for _, gone := range []string{primary, rotatedPlain, rotatedGz} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("expected %s removed, err=%v", gone, err)
		}
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("unrelated file must survive: %v", err)
	}
}

func TestLogsCleanMissingIsNoop(t *testing.T) {
	dir := t.TempDir()
	if err := logsClean(filepath.Join(dir, "nope.log")); err != nil {
		t.Errorf("missing primary should not error, got %v", err)
	}
}

func TestRunIgnoreSubcommandUsage(t *testing.T) {
	if err := runIgnoreSubcommand(nil); err == nil {
		t.Error("expected usage error for empty args")
	}
	if err := runIgnoreSubcommand([]string{"add"}); err == nil {
		t.Error("expected error: add without pattern")
	}
	if err := runIgnoreSubcommand([]string{"unknown"}); err == nil {
		t.Error("expected error for unknown subcommand")
	}
}

func TestRunIgnoreAddListPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Initially no rules.
	r, w, _ := os.Pipe()
	prev := os.Stdout
	os.Stdout = w
	if err := runIgnoreList(); err != nil {
		os.Stdout = prev
		t.Fatal(err)
	}
	w.Close()
	os.Stdout = prev
	var buf bytes.Buffer
	io.Copy(&buf, r)
	if !strings.Contains(buf.String(), "no ignore rules") {
		t.Errorf("expected empty-state message, got %q", buf.String())
	}

	// Add a rule.
	if err := runIgnoreAdd("/secret/*"); err != nil {
		t.Fatalf("runIgnoreAdd: %v", err)
	}

	// List again must show the rule.
	r2, w2, _ := os.Pipe()
	os.Stdout = w2
	if err := runIgnoreList(); err != nil {
		os.Stdout = prev
		t.Fatal(err)
	}
	w2.Close()
	os.Stdout = prev
	var buf2 bytes.Buffer
	io.Copy(&buf2, r2)
	if !strings.Contains(buf2.String(), "/secret/*") {
		t.Errorf("expected rule in list output, got %q", buf2.String())
	}

	// Path printer.
	r3, w3, _ := os.Pipe()
	os.Stdout = w3
	if err := runIgnorePath(); err != nil {
		os.Stdout = prev
		t.Fatal(err)
	}
	w3.Close()
	os.Stdout = prev
	var buf3 bytes.Buffer
	io.Copy(&buf3, r3)
	if !strings.Contains(buf3.String(), home) {
		t.Errorf("path should be under fake $HOME, got %q", buf3.String())
	}
}

func TestRunCompletionSubcommand(t *testing.T) {
	r, w, _ := os.Pipe()
	prev := os.Stdout
	os.Stdout = w
	if err := runCompletionSubcommand([]string{"bash"}); err != nil {
		os.Stdout = prev
		t.Fatal(err)
	}
	w.Close()
	os.Stdout = prev
	var buf bytes.Buffer
	io.Copy(&buf, r)
	if buf.Len() == 0 {
		t.Error("bash completion produced empty output")
	}

	if err := runCompletionSubcommand(nil); err == nil {
		t.Error("expected usage error on no args")
	}
	if err := runCompletionSubcommand([]string{"powershell"}); err == nil {
		t.Error("unknown shell must error")
	}
}

func TestRunConfigSubcommandUsage(t *testing.T) {
	if err := runConfigSubcommand(nil); err == nil {
		t.Error("expected usage error on empty")
	}
	if err := runConfigSubcommand([]string{"unknown"}); err == nil {
		t.Error("unknown verb must error")
	}
}

func TestConfigInitAndShow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Init writes defaults.
	r, w, _ := os.Pipe()
	prev := os.Stdout
	os.Stdout = w
	if err := configInit(nil); err != nil {
		os.Stdout = prev
		t.Fatal(err)
	}
	w.Close()
	os.Stdout = prev
	var buf bytes.Buffer
	io.Copy(&buf, r)
	if !strings.Contains(buf.String(), "wrote default config") {
		t.Errorf("init output: %q", buf.String())
	}

	// Re-init without --force errors.
	if err := configInit(nil); err == nil {
		t.Error("expected error when config exists without --force")
	}
	// With --force succeeds.
	if err := configInit([]string{"--force"}); err != nil {
		t.Errorf("--force should overwrite, got %v", err)
	}

	// Show prints something containing the config path.
	r2, w2, _ := os.Pipe()
	os.Stdout = w2
	if err := configShow(nil); err != nil {
		os.Stdout = prev
		t.Fatal(err)
	}
	w2.Close()
	os.Stdout = prev
	var buf2 bytes.Buffer
	io.Copy(&buf2, r2)
	if !strings.Contains(buf2.String(), home) {
		t.Errorf("show output should reference $HOME-rooted path: %q", buf2.String())
	}
}

func TestConfigValidateMissing(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.toml")
	if err := configValidate([]string{"--path", missing}); err != nil {
		// Missing file is acceptable — config.Load handles it as
		// "no overlay". The function may also error; either is fine.
		// We just want to exercise the dispatch + flag parsing.
		_ = err
	}
}

func TestPrintSyncPlan(t *testing.T) {
	plan := actions.Plan{
		Ops: []actions.PlanOp{
			{Action: actions.ActionImport, Item: model.Item{Kind: model.KindSkill, Name: "alpha"}},
			{Action: actions.ActionProject, Item: model.Item{Kind: model.KindAgent, Name: "beta"}},
			{Action: actions.ActionResync, Item: model.Item{Kind: model.KindSkill, Name: "gamma"}, Reason: "drifted"},
			{Action: actions.ActionSkip, Item: model.Item{Kind: model.KindSkill, Name: "delta"}, Reason: "ok"},
		},
	}
	r, w, _ := os.Pipe()
	prev := os.Stdout
	os.Stdout = w
	printSyncPlan(plan)
	w.Close()
	os.Stdout = prev
	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()
	for _, want := range []string{"Plan:", "alpha", "beta", "gamma", "delta", "drifted"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestPrintCandidates(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prevStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = prevStdout })

	cs := []install.Candidate{
		{Kind: model.KindSkill, Name: "a", Description: "first"},
		{Kind: model.KindAgent, Name: "b", ParseError: "bad frontmatter"},
	}
	printCandidates(cs)
	w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Skills", "Agents", "first", "bad frontmatter", "(no description)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
