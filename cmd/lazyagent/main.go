package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/actions"
	"github.com/mi-subbotin/lazyagent/internal/config"
	"github.com/mi-subbotin/lazyagent/internal/index"
	"github.com/mi-subbotin/lazyagent/internal/install"
	"github.com/mi-subbotin/lazyagent/internal/logging"
	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/sources"
	"github.com/mi-subbotin/lazyagent/internal/sources/claude"
	"github.com/mi-subbotin/lazyagent/internal/sources/codex"
	"github.com/mi-subbotin/lazyagent/internal/sources/gemini"
	"github.com/mi-subbotin/lazyagent/internal/sources/lazyagent"
	"github.com/mi-subbotin/lazyagent/internal/sources/mock"
	"github.com/mi-subbotin/lazyagent/internal/state"
	"github.com/mi-subbotin/lazyagent/internal/store"
	"github.com/mi-subbotin/lazyagent/internal/tui"
	"github.com/mi-subbotin/lazyagent/internal/updates"
)

// version, commit and date are filled in at release time by goreleaser
// via -ldflags. `dev` is the placeholder for `go run` / `go install`
// builds where no metadata is wired up.
//
// installSource (PRI-19) records how the binary was built so the update
// banner can suggest the right upgrade command. GoReleaser bakes "brew"
// in for tap builds; everything else falls back to go-install detection
// (binary path under $GOPATH/bin) or "unknown".
var (
	version       = "dev"
	commit        = "none"
	date          = "unknown"
	installSource = ""
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "library" || os.Args[1] == "shared") {
		if err := runSharedSubcommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "lazyagent:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "config" {
		if err := runConfigSubcommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "lazyagent:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "logs" {
		if err := runLogsSubcommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "lazyagent:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "completion" {
		if err := runCompletionSubcommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "lazyagent:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "install" {
		if err := runInstallSubcommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "lazyagent:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "uninstall" {
		if err := runUninstallSubcommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "lazyagent:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "cache" {
		if err := runCacheSubcommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "lazyagent:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "ignore" {
		if err := runIgnoreSubcommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "lazyagent:", err)
			os.Exit(1)
		}
		return
	}

	useMock := flag.Bool("mock", false, "use the mock data source instead of real adapters")
	showVersion := flag.Bool("version", false, "print version information and exit")
	verbose := flag.Bool("verbose", false, "increase log verbosity to debug")
	flag.BoolVar(verbose, "v", false, "alias for --verbose")
	logFile := flag.String("log-file", "", "override the log file path (defaults to logging.file from config)")
	logFormat := flag.String("log-format", "", "log format: text or json (defaults to logging.format from config)")
	noUpdateCheck := flag.Bool("no-update-check", false, "skip the weekly GitHub releases check (PRI-19)")
	allLocal := flag.Bool("all-local", false, "start in PRI-4 all-local mode (fold every discovered project's Local items into the tree)")
	noIndex := flag.Bool("no-index", false, "skip the global project indexer (PRI-4)")
	var extraRoots multiFlag
	flag.Var(&extraRoots, "root", "additional search root for the global indexer (repeatable)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("lazyagent %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	cfg := loadConfigOrWarn()

	logOpts := logging.Options{
		FileOverride:   *logFile,
		FormatOverride: *logFormat,
	}
	if *verbose {
		logOpts.LevelOverride = "debug"
	}
	logPath, logCloser, logErr := logging.Init(cfg, logOpts)
	if logErr != nil {
		// Logging is best-effort — the TUI still boots without it. Surface
		// the error to stderr so users notice misconfigured paths.
		fmt.Fprintln(os.Stderr, "lazyagent: logging disabled:", logErr)
	}
	defer logCloser.Close()

	configPath, _ := config.DefaultPath()
	slog.Info("starting lazyagent",
		"version", version,
		"commit", commit,
		"os", runtime.GOOS,
		"arch", runtime.GOARCH,
		"config", configPath,
		"log", logPath,
	)

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot read cwd:", err)
		os.Exit(1)
	}
	projectDir := detectProject(cwd)

	// Make sure ~/.lazyagent/library/{skills,agents,mcp,prompts,memory}
	// exists so the Shared origin and the share action work without a
	// manual setup step. Init is idempotent (mkdir -p underneath) and
	// cheap on every launch; it also performs a one-shot
	// store→library directory migration when upgrading. The Shared
	// section in the tree is still data-driven — it only appears once
	// the user actually has library items, so an empty library
	// doesn't add noise.
	if err := store.Init(); err != nil {
		fmt.Fprintln(os.Stderr, "lazyagent: cannot init library:", err)
		os.Exit(1)
	}

	// PRI-3.E: best-effort sweep of orphan tarballs on startup. Bounded
	// to once per 30 days via a `.last-gc` marker inside the cache, so
	// it never adds noticeable latency to subsequent launches. Errors
	// are logged but never block boot — `cache gc` from the CLI is the
	// authoritative manual override.
	if cacheDir, err := defaultCacheDir(); err == nil {
		if manifestPath, err := install.DefaultPath(); err == nil {
			if removed, err := install.AutoGC(cacheDir, manifestPath, 30*24*time.Hour); err != nil {
				slog.Warn("install: auto-gc failed", "err", err)
			} else if removed > 0 {
				slog.Info("install: auto-gc swept stale tarballs", "removed", removed)
			}
		}
	}

	var srcs []sources.Source
	if *useMock {
		srcs = append(srcs, mock.Source{})
	} else {
		if cfg.Tools.Claude {
			srcs = append(srcs, claude.Source{})
		}
		if cfg.Tools.Codex {
			srcs = append(srcs, codex.Source{})
		}
		if cfg.Tools.Gemini {
			srcs = append(srcs, gemini.Source{})
		}
		// The shared store source is always loaded — it returns no items
		// when nothing has been shared yet, so it is safe to keep on. The
		// `tools.shared` flag is reserved for the eventual opt-in projector.
		srcs = append(srcs, lazyagent.Source{})
	}

	m := tui.New(srcs, projectDir)
	m.SetInstallSource(detectInstallSource())

	// PRI-4: bootstrap the global project index. We honour the disk
	// cache when it is fresh enough (<24h) so first-paint stays fast.
	// The full re-walk runs in the background after tea starts so a
	// cold index never blocks the user.
	//
	// PRI-10: load the user's ignore file once and apply it both to
	// the cached projects (in case the user added a rule between
	// walks) and to the live re-walk goroutine.
	var initialProjects []string
	doIndex := !*noIndex && !*useMock
	ignoreFilter, ignoreErr := index.LoadIgnore()
	if ignoreErr != nil {
		slog.Warn("ignore: load failed", "err", ignoreErr)
	}
	// PRI-56: cache is valid if either the 24h TTL hasn't elapsed OR
	// every recorded marker mtime is unchanged. The mtime path lets a
	// quiet user skip the re-walk indefinitely; the 24h TTL is the
	// safety net that catches new projects created between launches.
	cacheValid := false
	if doIndex {
		if cached, err := index.LoadCache(); err == nil {
			if index.IsFresh(cached, time.Now(), 24*time.Hour) || index.MtimesUnchanged(cached) {
				initialProjects = filterIgnored(projectsFromCache(cached), ignoreFilter)
				cacheValid = true
			}
		}
	}
	m.SetDiscoveredProjects(initialProjects, *allLocal)

	// PRI-63: hydrate the persistent usage cache so cold launches don't
	// re-walk every multi-MB session jsonl. Best-effort — a missing or
	// stale file falls back to in-memory rebuild.
	_ = parse.LoadUsageCache()

	p := tea.NewProgram(m, tea.WithAltScreen())

	// PRI-19: spawn the weekly update poll in the background. The
	// goroutine uses Program.Send to deliver UpdateAvailableMsg into
	// the model — Bubble Tea is concurrency-safe for that. The check
	// is gated by `[updates] notify` and `--no-update-check`, and
	// piggy-backs on the same state.json the rest of the TUI uses.
	if cfg.Updates.Notify && !*noUpdateCheck {
		go runUpdateCheck(p, cfg.Updates.CheckIntervalDays, version)
	}

	// PRI-4: re-walk the configured roots in the background and
	// refresh the cache. The walk takes 2–10s on a typical $HOME, so
	// blocking startup on it would hurt the brew install experience.
	//
	// PRI-56: skip the background re-walk when the cache is still
	// valid (TTL fresh or every marker mtime unchanged). A quiet user
	// who hasn't touched any tool config since the last launch pays
	// nothing — the next change triggers the re-walk on the launch
	// after that.
	if doIndex && !cacheValid {
		go runProjectIndex(p, resolveSearchRoots(cfg.Search.Roots, extraRoots), ignoreFilter)
	}

	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazyagent:", err)
		os.Exit(1)
	}
	// PRI-63: persist any usage entries the session populated. Failure
	// is logged-only — users would resent a "save failed" surface on
	// a normal quit.
	if err := parse.SaveUsageCache(); err != nil {
		slog.Warn("usage cache save failed", "err", err)
	}
}

// runUpdateCheck polls api.github.com/releases/latest at most once per
// intervalDays. The function is intentionally silent on the success
// path when there's nothing newer — the user only sees output when an
// update banner appears in the TUI. Errors land in the slog log file
// at warn level so power users can chase a misconfigured corp proxy.
func runUpdateCheck(p *tea.Program, intervalDays int, current string) {
	st, err := state.Load()
	if err != nil {
		slog.Warn("update check: state load failed", "err", err)
		return
	}
	now := time.Now()
	if !updates.ShouldCheck(st, intervalDays, now) {
		// Cache is fresh — surface the previously-seen version if it
		// is still newer than the running build. Lets a returning user
		// see the banner immediately rather than waiting another week.
		if updates.IsNewer(current, st.LatestKnownVersion) && !updates.IsBannerDismissed(st, st.LatestKnownVersion, now) {
			p.Send(tui.UpdateAvailableMsg{Version: st.LatestKnownVersion})
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rel, err := updates.FetchLatest(ctx, updates.DefaultOwner, updates.DefaultRepo)
	if err != nil {
		if !errors.Is(err, updates.ErrNoReleases) {
			slog.Warn("update check: github request failed", "err", err)
		}
		return
	}
	updated, saveErr := updates.RecordCheck(st, rel.Version, now)
	if saveErr != nil {
		slog.Warn("update check: state save failed", "err", saveErr)
	}
	if !updates.IsNewer(current, rel.Version) {
		return
	}
	if updates.IsBannerDismissed(updated, rel.Version, now) {
		return
	}
	p.Send(tui.UpdateAvailableMsg{Version: rel.Version, URL: rel.URL})
}

// multiFlag implements flag.Value for repeatable string flags. Used
// for `--root` so users can pass multiple search roots without
// shell-quoted comma lists.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

// resolveSearchRoots merges roots from config.search.roots with any
// `--root` flags. Tokens like `$HOME` and `~` are expanded inline.
// Invalid / empty entries are dropped; on an empty result the walker
// falls back to $HOME via index.Discover.
func resolveSearchRoots(cfgRoots []string, cliRoots multiFlag) []string {
	combined := append([]string(nil), cfgRoots...)
	combined = append(combined, []string(cliRoots)...)
	out := make([]string, 0, len(combined))
	seen := make(map[string]struct{}, len(combined))
	for _, r := range combined {
		expanded := config.ExpandPath(strings.TrimSpace(r))
		if expanded == "" {
			continue
		}
		if abs, err := filepath.Abs(expanded); err == nil {
			expanded = abs
		}
		if _, dup := seen[expanded]; dup {
			continue
		}
		seen[expanded] = struct{}{}
		out = append(out, expanded)
	}
	return out
}

// projectsFromCache flattens an index.Cache into the slice of project
// directories the TUI needs.
func projectsFromCache(c index.Cache) []string {
	out := make([]string, 0, len(c.Projects))
	for _, p := range c.Projects {
		out = append(out, p.Path)
	}
	return out
}

// filterIgnored drops paths matching the user's ignore filter (PRI-10)
// before they reach the TUI. The walker already applies the same
// filter on a fresh re-walk; this is the same logic re-run against the
// disk cache so a user can add an ignore rule and see it take effect
// without waiting for the next cold walk.
func filterIgnored(paths []string, ig *index.Ignore) []string {
	if ig == nil {
		return paths
	}
	out := paths[:0]
	for _, p := range paths {
		if !ig.Match(p) {
			out = append(out, p)
		}
	}
	return out
}

// runProjectIndex performs the cold walk, persists the result, and
// pushes the refreshed list into the TUI via Program.Send. The walker
// already filters cloud-sync mounts and big skip-listed directories,
// so this typically lands inside 2–10s on a developer's $HOME.
//
// ignoreFilter (PRI-10) is the user's `~/.lazyagent/ignore` ruleset;
// nil means "no privacy filter".
func runProjectIndex(p *tea.Program, roots []string, ignoreFilter *index.Ignore) {
	projects, err := index.Discover(index.Options{Roots: roots, Ignore: ignoreFilter})
	if err != nil {
		slog.Warn("index: discover failed", "err", err)
		return
	}
	cache := index.Cache{
		GeneratedAt: time.Now().Unix(),
		Roots:       roots,
		Projects:    projects,
	}
	if path, err := index.SaveCache(cache); err != nil {
		slog.Warn("index: save cache failed", "path", path, "err", err)
	}
	out := make([]string, 0, len(projects))
	for _, pr := range projects {
		out = append(out, pr.Path)
	}
	p.Send(tui.ProjectsDiscoveredMsg{Projects: out})
}

// detectInstallSource returns "brew" when a goreleaser-built binary
// baked the marker in via ldflag, "go-install" when the binary lives
// under $GOPATH/bin, or "unknown" otherwise. Used by the PRI-19 banner
// to print the right upgrade hint.
func detectInstallSource() string {
	if installSource != "" {
		return installSource
	}
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			gopath = filepath.Join(home, "go")
		}
	}
	if gopath != "" {
		gobin := filepath.Join(gopath, "bin")
		if abs, err := filepath.Abs(exe); err == nil && filepath.Dir(abs) == gobin {
			return "go-install"
		}
	}
	return "unknown"
}

// loadConfigOrWarn reads ~/.lazyagent/config.toml and prints any non-fatal
// warnings (parse error, unknown keys, invalid enum values) to stderr,
// returning a usable Config either way. A real parse error falls back to
// Default() so the TUI still boots.
func loadConfigOrWarn() *config.Config {
	path, err := config.DefaultPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazyagent: cannot resolve config path:", err)
		return config.Default()
	}
	cfg, report, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazyagent: config load failed, using defaults:", err)
		return config.Default()
	}
	for _, k := range report.UnknownKeys {
		fmt.Fprintf(os.Stderr, "lazyagent: config: unknown key %s (ignored)\n", k)
	}
	for _, m := range report.ValidationErrors {
		fmt.Fprintln(os.Stderr, "lazyagent: config:", m)
	}
	return cfg
}

// runSharedSubcommand dispatches `lazyagent library <verb>` (and the
// historical `lazyagent shared <verb>` alias). Today `init` and `sync`
// exist; future verbs (status, push, pull) plug in here.
func runSharedSubcommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lazyagent library <init|sync> [flags]")
	}
	switch args[0] {
	case "init":
		if err := store.Init(); err != nil {
			return err
		}
		root, _ := store.Root()
		fmt.Printf("lazyagent library initialised at %s\n", root)
		return nil
	case "sync":
		return runSharedSyncCommand(args[1:])
	default:
		return fmt.Errorf("unknown library subcommand %q (try: init, sync)", args[0])
	}
}

// runSharedSyncCommand implements `lazyagent shared sync` — the
// headless equivalent of the eventual TUI `S` keystroke. Builds a
// Plan from every adapter's items, prints a human-readable preview,
// and (unless --dry-run) applies it. --yes auto-confirms when the
// plan would clobber unrelated content; without --yes a conflict
// returns an error so a script can re-run with the flag.
func runSharedSyncCommand(args []string) error {
	fs := flag.NewFlagSet("shared sync", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "preview the plan without mutating anything")
	yes := fs.Bool("yes", false, "auto-overwrite conflicting target paths")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := store.Init(); err != nil {
		return fmt.Errorf("init store: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir := detectProject(cwd)

	srcs := []sources.Source{claude.Source{}, codex.Source{}, gemini.Source{}, lazyagent.Source{}}
	ctx := context.Background()
	var allItems []model.Item
	for _, s := range srcs {
		items, err := s.List(ctx, projectDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s list failed: %v\n", s.Name(), err)
			continue
		}
		allItems = append(allItems, items...)
	}

	plan := actions.SyncAll(allItems)
	printSyncPlan(plan)

	if *dryRun {
		return nil
	}
	if !plan.Mutating() {
		fmt.Println("\nNothing to do.")
		return nil
	}

	errs := actions.ApplyPlan(plan, *yes)
	if len(errs) == 0 {
		fmt.Println("\nSync complete.")
		return nil
	}
	if actions.IsSyncConflict(errs) && !*yes {
		fmt.Fprintln(os.Stderr, "\nSome targets had unrelated content. Re-run with --yes to overwrite.")
	}
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "  ✗", e)
	}
	return fmt.Errorf("%d sync error(s)", len(errs))
}

// printSyncPlan renders a one-line header + table of ops to stdout.
func printSyncPlan(plan actions.Plan) {
	counts := plan.Counts()
	fmt.Printf("Plan: %d import · %d project · %d resync · %d skip\n",
		counts[actions.ActionImport],
		counts[actions.ActionProject],
		counts[actions.ActionResync],
		counts[actions.ActionSkip])
	for _, op := range plan.Ops {
		marker := "·"
		switch op.Action {
		case actions.ActionImport:
			marker = "+"
		case actions.ActionProject:
			marker = "→"
		case actions.ActionResync:
			marker = "↻"
		}
		line := fmt.Sprintf("  %s %s %-7s %s", marker, op.Action, op.Item.Kind.String(), op.Item.Name)
		if op.Reason != "" {
			line += "  (" + op.Reason + ")"
		}
		fmt.Println(line)
	}
}

// detectProject returns cwd if it contains any of the tool-specific markers,
// otherwise empty string. Local-scope nodes are hidden when no project is
// detected (per design decision).
//
// $HOME and "/" are never treated as project roots: $HOME naturally
// contains ~/.claude, ~/.codex, ~/.gemini — those are global config
// dirs, not project markers, and treating $HOME as a project would make
// every adapter re-scan its global tree as "local", duplicating every
// global item. Triggered by `brew install lazyagent` users who launch
// from a fresh shell sitting in $HOME.
func detectProject(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	abs = filepath.Clean(abs)
	if abs == string(filepath.Separator) {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if homeAbs, err := filepath.Abs(home); err == nil && filepath.Clean(homeAbs) == abs {
			return ""
		}
		// Cover the case where $HOME itself sits behind a symlink
		// (e.g. /home/foo → /Users/foo on some setups).
		if real, err := filepath.EvalSymlinks(home); err == nil {
			if realAbs, err := filepath.Abs(real); err == nil && filepath.Clean(realAbs) == abs {
				return ""
			}
		}
	}
	markers := []string{".claude", ".codex", ".gemini", ".agents", ".mcp.json", "AGENTS.md", "GEMINI.md", "CLAUDE.md"}
	for _, mk := range markers {
		if _, err := os.Stat(filepath.Join(cwd, mk)); err == nil {
			return cwd
		}
	}
	return ""
}
