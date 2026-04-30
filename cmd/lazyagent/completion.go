package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mi-subbotin/lazyagent/internal/completion"
)

// runCompletionSubcommand dispatches `lazyagent completion <shell>`.
// The chosen shell's script is written to stdout so users can pipe it
// into the right place (or let `brew` install it for them).
func runCompletionSubcommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lazyagent completion <bash|zsh|fish>")
	}
	var script string
	switch args[0] {
	case "bash":
		script = completion.Bash
	case "zsh":
		script = completion.Zsh
	case "fish":
		script = completion.Fish
	default:
		return fmt.Errorf("unknown shell %q (try: bash | zsh | fish)", args[0])
	}
	_, err := io.WriteString(os.Stdout, script)
	return err
}
