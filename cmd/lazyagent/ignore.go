// `lazyagent ignore` CLI helpers (PRI-10).
//
// Mirrors the shape of `cache gc` / `install` — a tiny dispatch on the
// first verb, no flag library for the leaf verbs because the patterns
// the user passes can themselves contain `-` and `--` characters and
// shouldn't be parsed as flags.

package main

import (
	"errors"
	"fmt"

	"github.com/mi-subbotin/lazyagent/internal/index"
)

func runIgnoreSubcommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: lazyagent ignore <add|list|path>")
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			return errors.New("usage: lazyagent ignore add <pattern>")
		}
		return runIgnoreAdd(args[1])
	case "list":
		return runIgnoreList()
	case "path":
		return runIgnorePath()
	default:
		return fmt.Errorf("unknown ignore subcommand %q (try: add, list, path)", args[0])
	}
}

func runIgnoreAdd(pattern string) error {
	path, err := index.AppendPattern(pattern)
	if err != nil {
		return err
	}
	fmt.Printf("appended to %s: %s\n", path, pattern)
	return nil
}

func runIgnoreList() error {
	patterns, err := index.ListPatterns()
	if err != nil {
		return err
	}
	if len(patterns) == 0 {
		path, _ := index.IgnorePath()
		fmt.Printf("no ignore rules (%s missing or empty)\n", path)
		return nil
	}
	for _, p := range patterns {
		fmt.Println(p)
	}
	return nil
}

func runIgnorePath() error {
	path, err := index.IgnorePath()
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
