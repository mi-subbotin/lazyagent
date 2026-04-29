package actions

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// EditorCommand builds an *exec.Cmd that opens path in the user's preferred
// editor. Selection order: $VISUAL, $EDITOR, then `nano` or `vi` if either
// is in PATH. The editor env var may include flags (e.g. "code -w") — they
// are split on whitespace and passed as separate arguments so we never go
// through a shell.
func EditorCommand(path string) (*exec.Cmd, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		for _, candidate := range []string{"nano", "vi"} {
			if _, err := exec.LookPath(candidate); err == nil {
				editor = candidate
				break
			}
		}
	}
	if editor == "" {
		return nil, fmt.Errorf("no editor found: set $EDITOR or install nano/vi")
	}
	parts := strings.Fields(editor)
	args := append(parts[1:], path)
	return exec.Command(parts[0], args...), nil
}
