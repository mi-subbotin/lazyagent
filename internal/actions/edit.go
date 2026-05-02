package actions

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// EditorCommand builds an *exec.Cmd that opens path in the user's
// preferred editor. Selection order:
//
//  1. $VISUAL
//  2. $EDITOR
//  3. First available: micro, nvim, vim, nano, pico, vi
//
// The fallback list prefers modern modeless editors first: micro
// (Ctrl+S save, GUI-like) and nvim/vim before nano/pico because the
// Pico chord-style keymap (^G help, ^O write, ^X exit) is jarring
// for users on non-English keyboard layouts who have to switch
// layout for every shortcut.
//
// The editor env var may include flags (e.g. "code -w") — they are
// split on whitespace and passed as separate arguments so we never
// go through a shell.
func EditorCommand(path string) (*exec.Cmd, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		for _, candidate := range []string{"micro", "nvim", "vim", "nano", "pico", "vi"} {
			if _, err := exec.LookPath(candidate); err == nil {
				editor = candidate
				break
			}
		}
	}
	if editor == "" {
		return nil, fmt.Errorf("no editor found: set $EDITOR or install micro/nvim/vim/nano")
	}
	parts := strings.Fields(editor)
	args := append(parts[1:], path)
	return exec.Command(parts[0], args...), nil
}
