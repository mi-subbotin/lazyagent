package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/install"
)

// runInstallSubcommand handles `lazyagent install <url> [flags]`.
//
// Without --all or --name the command prints candidates and exits, so
// the user can see what a repo offers before pulling anything in. With
// --all every candidate is installed; with --name only candidates whose
// Name contains the substring are installed. --overwrite lets re-install
// replace an existing destination instead of erroring out.
func runInstallSubcommand(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	target := fs.String("target", "claude", "target origin: claude | codex | gemini | shared")
	scope := fs.String("scope", "global", "scope: global | local")
	name := fs.String("name", "", "install only candidates whose name contains this substring")
	all := fs.Bool("all", false, "install every candidate without prompting")
	overwrite := fs.Bool("overwrite", false, "replace an existing destination on conflict")
	list := fs.Bool("list", false, "list candidates and exit (no install)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return errors.New("install requires a URL argument")
	}
	rawURL := fs.Arg(0)

	spec, err := install.ParseURL(rawURL)
	if err != nil {
		return fmt.Errorf("url: %w", err)
	}

	cacheDir, err := defaultCacheDir()
	if err != nil {
		return err
	}
	client := install.NewClient(cacheDir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sha, err := client.Resolve(ctx, spec)
	if err != nil {
		return fmt.Errorf("resolve ref: %w", err)
	}
	fmt.Printf("resolved %s -> %s\n", rawURL, sha)

	repoDir, err := client.Fetch(ctx, spec, sha)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}

	candidates, err := install.Inspect(repoDir, spec)
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	if len(candidates) == 0 {
		return errors.New("no installable items found (expected skills/<n>/SKILL.md, agents/<n>.md or commands/<n>.md)")
	}

	if *list || (!*all && *name == "") {
		fmt.Printf("found %d candidate(s) in %s:\n", len(candidates), repoDir)
		printCandidates(candidates)
		fmt.Println("\nrun again with --all or --name <substring> to install.")
		return nil
	}

	selected := candidates[:0]
	for _, c := range candidates {
		if *name != "" && !strings.Contains(strings.ToLower(c.Name), strings.ToLower(*name)) {
			continue
		}
		selected = append(selected, c)
	}
	if len(selected) == 0 {
		return fmt.Errorf("no candidates matched --name=%q", *name)
	}

	cwd, _ := os.Getwd()
	tg := install.Target{Origin: *target, Scope: *scope, ProjectDir: cwd}
	if err := tg.Validate(); err != nil {
		return err
	}

	manifestPath, err := install.DefaultPath()
	if err != nil {
		return err
	}
	manifest, err := install.Load(manifestPath)
	if err != nil {
		return err
	}

	var installed int
	for _, c := range selected {
		entry, err := install.Apply(repoDir, c, tg, rawURL, sha, install.ApplyOptions{Overwrite: *overwrite})
		if err != nil {
			if errors.Is(err, install.ErrAlreadyExists) {
				fmt.Fprintf(os.Stderr, "skip %s: already installed (pass --overwrite to replace)\n", c.Name)
				continue
			}
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", c.Name, err)
			continue
		}
		manifest.Add(entry)
		installed++
		fmt.Printf("installed %s (%s) -> %s\n", entry.Name, entry.Kind, entry.TargetPath)
	}
	if installed > 0 {
		if err := manifest.Save(manifestPath); err != nil {
			return fmt.Errorf("save manifest: %w", err)
		}
	}
	fmt.Printf("done: %d installed, %d skipped\n", installed, len(selected)-installed)
	return nil
}

// runUninstallSubcommand handles `lazyagent uninstall <name>` — looks
// the name up in the manifest, removes the bytes from the user's
// disk, and drops the manifest entry. When the same name was
// installed into multiple targets the user must pass --target/--scope
// to disambiguate.
func runUninstallSubcommand(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	target := fs.String("target", "", "filter by target origin (claude/codex/gemini/shared)")
	scope := fs.String("scope", "", "filter by scope (global/local)")
	all := fs.Bool("all", false, "remove every install matching the name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return errors.New("uninstall requires a name argument")
	}
	name := fs.Arg(0)

	manifestPath, err := install.DefaultPath()
	if err != nil {
		return err
	}
	manifest, err := install.Load(manifestPath)
	if err != nil {
		return err
	}
	matches := manifest.FindByName(name)
	if *target != "" {
		matches = filterByTarget(matches, *target)
	}
	if *scope != "" {
		matches = filterByScope(matches, *scope)
	}
	if len(matches) == 0 {
		return fmt.Errorf("no installed item named %q", name)
	}
	if len(matches) > 1 && !*all {
		fmt.Fprintf(os.Stderr, "%d installs match %q — disambiguate with --target/--scope or pass --all:\n", len(matches), name)
		for _, in := range matches {
			fmt.Fprintf(os.Stderr, "  %s/%s -> %s\n", in.TargetOrigin, in.TargetScope, in.TargetPath)
		}
		return errors.New("ambiguous uninstall")
	}

	for _, in := range matches {
		if err := install.Uninstall(in); err != nil {
			fmt.Fprintf(os.Stderr, "uninstall %s: %v\n", in.TargetPath, err)
			continue
		}
		manifest.Remove(in.TargetPath)
		fmt.Printf("removed %s\n", in.TargetPath)
	}
	return manifest.Save(manifestPath)
}

// runCacheSubcommand dispatches `lazyagent cache <verb>`. Today only
// `gc` exists — sweeps tarballs whose sha is no longer referenced by
// any manifest entry.
func runCacheSubcommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: lazyagent cache <gc>")
	}
	switch args[0] {
	case "gc":
		return runCacheGC(args[1:])
	default:
		return fmt.Errorf("unknown cache subcommand %q", args[0])
	}
}

func runCacheGC(args []string) error {
	fset := flag.NewFlagSet("cache gc", flag.ContinueOnError)
	fset.SetOutput(os.Stderr)
	dryRun := fset.Bool("dry-run", false, "list what would be removed, don't delete")
	if err := fset.Parse(args); err != nil {
		return err
	}

	manifestPath, err := install.DefaultPath()
	if err != nil {
		return err
	}
	manifest, err := install.Load(manifestPath)
	if err != nil {
		return err
	}
	keep := manifest.Shas()

	cacheDir, err := defaultCacheDir()
	if err != nil {
		return err
	}
	if _, err := os.Stat(cacheDir); errors.Is(err, fs.ErrNotExist) {
		fmt.Println("cache dir does not exist, nothing to gc")
		return nil
	}

	var removed int
	err = filepath.WalkDir(cacheDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		// repo cache dirs are named "<repo>@<sha>"; ignore deeper dirs.
		base := d.Name()
		if !strings.Contains(base, "@") {
			return nil
		}
		sha := base[strings.LastIndex(base, "@")+1:]
		if _, ok := keep[sha]; ok {
			return filepath.SkipDir
		}
		if *dryRun {
			fmt.Printf("would remove %s\n", path)
		} else {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			fmt.Printf("removed %s\n", path)
		}
		removed++
		return filepath.SkipDir
	})
	if err != nil {
		return err
	}
	if removed == 0 {
		fmt.Println("cache is clean")
	}
	return nil
}

func filterByTarget(in []install.Install, origin string) []install.Install {
	var out []install.Install
	origin = strings.ToLower(origin)
	for _, i := range in {
		if i.TargetOrigin == origin {
			out = append(out, i)
		}
	}
	return out
}

func filterByScope(in []install.Install, scope string) []install.Install {
	var out []install.Install
	scope = strings.ToLower(scope)
	for _, i := range in {
		if i.TargetScope == scope {
			out = append(out, i)
		}
	}
	return out
}

func printCandidates(cs []install.Candidate) {
	for _, c := range cs {
		desc := c.Description
		if desc == "" {
			desc = "(no description)"
		}
		bad := ""
		if c.ParseError != "" {
			bad = "  [invalid: " + c.ParseError + "]"
		}
		fmt.Printf("  %-7s  %-30s  %s%s\n", c.Kind, c.Name, desc, bad)
	}
}

func defaultCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lazyagent", "cache"), nil
}
