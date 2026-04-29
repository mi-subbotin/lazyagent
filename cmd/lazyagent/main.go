package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

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

	useMock := flag.Bool("mock", false, "use the mock data source instead of real adapters")
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("lazyagent %s (commit %s, built %s)\n", version, commit, date)
		return
	}

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

	var srcs []sources.Source
	if *useMock {
		srcs = append(srcs, mock.Source{})
	} else {
		srcs = append(srcs, claude.Source{}, codex.Source{}, gemini.Source{}, lazyagent.Source{})
	}

	m := tui.New(srcs, projectDir)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazyagent:", err)
		os.Exit(1)
	}
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
func detectProject(cwd string) string {
	markers := []string{".claude", ".codex", ".gemini", ".agents", ".mcp.json", "AGENTS.md", "GEMINI.md", "CLAUDE.md"}
	for _, mk := range markers {
		if _, err := os.Stat(filepath.Join(cwd, mk)); err == nil {
			return cwd
		}
	}
	return ""
}
