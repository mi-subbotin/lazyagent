package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/BurntSushi/toml"

	"github.com/mi-subbotin/lazyagent/internal/config"
)

// runConfigSubcommand dispatches `lazyagent config <verb>`.
//
// Verbs:
//
//	init      write defaults to ~/.lazyagent/config.toml
//	show      print effective config (defaults + user overlay)
//	edit      open the config in $EDITOR (falls back to vi)
//	validate  parse the file and report unknown keys / enum errors
func runConfigSubcommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lazyagent config <init|show|edit|validate>")
	}
	switch args[0] {
	case "init":
		return configInit(args[1:])
	case "show":
		return configShow(args[1:])
	case "edit":
		return configEdit(args[1:])
	case "validate":
		return configValidate(args[1:])
	default:
		return fmt.Errorf("unknown config subcommand %q (try: init | show | edit | validate)", args[0])
	}
}

func configInit(args []string) error {
	fs := flag.NewFlagSet("config init", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite an existing config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil && !*force {
		return fmt.Errorf("config already exists at %s; pass --force to overwrite", path)
	}
	if err := config.Save(path, config.Default()); err != nil {
		return err
	}
	fmt.Printf("wrote default config to %s\n", path)
	return nil
}

func configShow(args []string) error {
	fs := flag.NewFlagSet("config show", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, report, err := config.Load(path)
	if err != nil {
		return err
	}
	if report.Path == "" {
		fmt.Printf("# config file not found at %s — showing defaults\n", path)
	} else {
		fmt.Printf("# loaded from %s\n", report.Path)
	}
	for _, w := range report.UnknownKeys {
		fmt.Printf("# warning: unknown key %s\n", w)
	}
	for _, w := range report.ValidationErrors {
		fmt.Printf("# warning: %s\n", w)
	}
	enc := toml.NewEncoder(os.Stdout)
	return enc.Encode(cfg)
}

func configEdit(args []string) error {
	fs := flag.NewFlagSet("config edit", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := config.Save(path, config.Default()); err != nil {
			return fmt.Errorf("seed default config: %w", err)
		}
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func configValidate(args []string) error {
	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	pathFlag := fs.String("path", "", "config file to check (defaults to ~/.lazyagent/config.toml)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := *pathFlag
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return err
		}
		path = p
	}
	_, report, err := config.Load(path)
	if err != nil {
		return err
	}
	if report.Path == "" {
		fmt.Printf("no config file at %s (using defaults — nothing to validate)\n", path)
		return nil
	}
	fmt.Printf("checked %s\n", report.Path)
	if len(report.UnknownKeys) == 0 && len(report.ValidationErrors) == 0 {
		fmt.Println("ok")
		return nil
	}
	for _, k := range report.UnknownKeys {
		fmt.Printf("  unknown key: %s\n", k)
	}
	for _, m := range report.ValidationErrors {
		fmt.Printf("  invalid:     %s\n", m)
	}
	return fmt.Errorf("config has %d unknown key(s) and %d validation error(s)", len(report.UnknownKeys), len(report.ValidationErrors))
}
