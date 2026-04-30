package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/config"
	"github.com/mi-subbotin/lazyagent/internal/install"
	"github.com/mi-subbotin/lazyagent/internal/logging"
	"github.com/mi-subbotin/lazyagent/internal/sources"
	"github.com/mi-subbotin/lazyagent/internal/sources/claude"
	"github.com/mi-subbotin/lazyagent/internal/sources/codex"
	"github.com/mi-subbotin/lazyagent/internal/sources/gemini"
	"github.com/mi-subbotin/lazyagent/internal/sources/lazyagent"
	"github.com/mi-subbotin/lazyagent/internal/sources/mock"
	"github.com/mi-subbotin/lazyagent/internal/store"
	"github.com/mi-subbotin/lazyagent/internal/tui"
)

// version, commit and date are filled in at release time by goreleaser
// via -ldflags. `dev` is the placeholder for `go run` / `go install`
// builds where no metadata is wired up.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "shared" {
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

	useMock := flag.Bool("mock", false, "use the mock data source instead of real adapters")
	showVersion := flag.Bool("version", false, "print version information and exit")
	verbose := flag.Bool("verbose", false, "increase log verbosity to debug")
	flag.BoolVar(verbose, "v", false, "alias for --verbose")
	logFile := flag.String("log-file", "", "override the log file path (defaults to logging.file from config)")
	logFormat := flag.String("log-format", "", "log format: text or json (defaults to logging.format from config)")
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

	// Make sure ~/.lazyagent/store/{skills,agents,mcp,prompts,memory}
	// exists so the Shared origin and the `s` share action work without
	// a manual setup step. Init is idempotent (mkdir -p underneath) and
	// cheap on every launch. The Shared section in the tree is still
	// data-driven — it only appears once the user actually has shared
	// items, so an empty store doesn't add noise.
	if err := store.Init(); err != nil {
		fmt.Fprintln(os.Stderr, "lazyagent: cannot init shared store:", err)
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
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazyagent:", err)
		os.Exit(1)
	}
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

// runSharedSubcommand dispatches `lazyagent shared <verb>`. Today only
// `init` exists; future verbs (sync, status, push, pull) plug in here.
func runSharedSubcommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lazyagent shared <init>")
	}
	switch args[0] {
	case "init":
		if err := store.Init(); err != nil {
			return err
		}
		root, _ := store.Root()
		fmt.Printf("lazyagent shared store initialised at %s\n", root)
		return nil
	default:
		return fmt.Errorf("unknown shared subcommand %q (try: init)", args[0])
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
