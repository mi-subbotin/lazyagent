package completion

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedScripts_NotEmpty(t *testing.T) {
	cases := map[string]string{
		"bash": Bash,
		"zsh":  Zsh,
		"fish": Fish,
	}
	for shell, body := range cases {
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s completion script is empty", shell)
		}
	}
}

func TestBashScript_HasComplete(t *testing.T) {
	if !strings.Contains(Bash, "complete -F _lazyagent lazyagent") {
		t.Error("bash script does not register completion for lazyagent")
	}
}

func TestZshScript_HasCompdefMagicLine(t *testing.T) {
	// Auto-loaded zsh completion files must start with `#compdef <cmd>`.
	if !strings.HasPrefix(Zsh, "#compdef lazyagent") {
		t.Errorf("zsh script does not start with `#compdef lazyagent`; first line: %q", firstLine(Zsh))
	}
}

func TestFishScript_HasComplete(t *testing.T) {
	if !strings.Contains(Fish, "complete -c lazyagent") {
		t.Error("fish script never calls `complete -c lazyagent`")
	}
}

// TestSyntax runs the shell's own `-n` flag (parse only, no execute) on
// each embedded script when the corresponding interpreter is on PATH.
// Tests skip cleanly on systems without one of the shells — typical
// for fish on a vanilla macOS or zsh on a stripped-down Linux runner.
func TestSyntax(t *testing.T) {
	cases := []struct {
		shell  string
		body   string
		ext    string
		extras []string // extra args to pass before the script path
	}{
		{"bash", Bash, "bash", nil},
		{"zsh", Zsh, "zsh", nil},
		{"fish", Fish, "fish", nil},
	}
	for _, c := range cases {
		t.Run(c.shell, func(t *testing.T) {
			if _, err := exec.LookPath(c.shell); err != nil {
				t.Skipf("%s not on PATH; skipping syntax check", c.shell)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "lazyagent."+c.ext)
			if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			args := append(append([]string{}, c.extras...), "-n", path)
			out, err := exec.Command(c.shell, args...).CombinedOutput()
			if err != nil {
				t.Errorf("%s -n reported syntax error: %v\noutput:\n%s", c.shell, err, string(out))
			}
		})
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
