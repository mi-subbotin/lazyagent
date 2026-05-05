package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/doctor"
	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/sources"
	"github.com/mi-subbotin/lazyagent/internal/sources/claude"
	"github.com/mi-subbotin/lazyagent/internal/sources/codex"
	"github.com/mi-subbotin/lazyagent/internal/sources/gemini"
	"github.com/mi-subbotin/lazyagent/internal/sources/lazyagent"
)

// runDoctor implements `lazyagent doctor [--cli=...] [--yes]`.
//
// Aggregates items from every adapter, picks an LLM CLI on PATH,
// runs it as a subprocess with the rendered prompt, parses the
// JSON response, and saves it under ~/.lazyagent/doctor-<id>.json.
func runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	cliFlag := fs.String("cli", "", "force a specific CLI: claude | codex | gemini")
	yes := fs.Bool("yes", false, "skip large-prompt confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}

	items, err := loadAllItems(ctx)
	if err != nil {
		return err
	}

	cli, err := pickCLI(items, *cliFlag)
	if err != nil {
		return err
	}

	prompt, err := doctor.BuildPrompt(items)
	if err != nil {
		return fmt.Errorf("build prompt: %w", err)
	}
	tokens := len(prompt) / 4
	if tokens > 50000 && !*yes {
		fmt.Printf("Estimated prompt size ~%d tokens. Run anyway? (y/n): ", tokens)
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return errors.New("aborted by user")
		}
	}

	fmt.Fprintf(os.Stderr, "Running %s on %d items...\n", cli.Name, len(items))
	id, rec, err := doctor.Run(ctx, items, cli)
	if err != nil {
		return err
	}
	fmt.Printf("Saved doctor-%s.json. Found %d duplicates, %d unused candidates, %d other notes.\n",
		id, len(rec.Duplicates), len(rec.Unused), len(rec.Other))
	return nil
}

// loadAllItems aggregates items across the real adapters, mirroring
// the TUI's source list. Errors from individual adapters are warned
// but do not abort the run.
func loadAllItems(ctx context.Context) ([]model.Item, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	projectDir := detectProject(cwd)

	srcs := []sources.Source{
		claude.Source{},
		codex.Source{},
		gemini.Source{},
		lazyagent.Source{},
	}
	var items []model.Item
	for _, s := range srcs {
		got, err := s.List(ctx, projectDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s list failed: %v\n", s.Name(), err)
			continue
		}
		items = append(items, got...)
	}
	return items, nil
}

// pickCLI honours an explicit --cli choice, otherwise returns the
// first detected CLI ranked by Detect().
func pickCLI(items []model.Item, want string) (doctor.CLI, error) {
	detected := doctor.Detect(items)
	if want != "" {
		for _, c := range detected {
			if c.Name == want {
				return c, nil
			}
		}
		return doctor.CLI{}, fmt.Errorf("CLI %q not found in PATH (detected: %s)", want, joinCLINames(detected))
	}
	if len(detected) == 0 {
		return doctor.CLI{}, doctor.ErrCLINotFound
	}
	return detected[0], nil
}

func joinCLINames(cs []doctor.CLI) string {
	if len(cs) == 0 {
		return "none"
	}
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}
