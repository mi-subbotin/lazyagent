package actions

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// ErrResumeUnsupported means we can't construct a resume command for
// this combination of (origin, scope) — usually because the upstream
// CLI requires a cwd we don't have. The TUI surfaces the message as a
// toast rather than failing silently.
var ErrResumeUnsupported = errors.New("resume not supported")

// ResumeCommand builds an *exec.Cmd that re-enters a recorded session.
// The TUI hands it to tea.ExecProcess so the upstream CLI takes over
// the terminal until the user exits, at which point we come back.
//
// projectDir is the lazyagent project root, used as cwd for Gemini
// resumes (Gemini's --resume <index> resolves against the current cwd
// — for Local-bucket sessions we know that's the project root because
// projectHash == sha256(projectDir)). Pass "" when there's no project
// detected; non-Local Gemini resumes return ErrResumeUnsupported.
func ResumeCommand(it model.Item, projectDir string) (*exec.Cmd, error) {
	if it.Kind != model.KindSession {
		return nil, fmt.Errorf("%w: not a session", ErrResumeUnsupported)
	}
	switch it.Origin {
	case model.OriginClaude:
		sid := it.ConfigKey
		if sid == "" {
			return nil, fmt.Errorf("%w: missing sessionId", ErrResumeUnsupported)
		}
		// `claude -r <id>` looks the session up by ID and works from any
		// cwd, so we don't pin Cmd.Dir.
		return exec.Command("claude", "-r", sid), nil

	case model.OriginGemini:
		idx := it.Meta["index"]
		if idx == "" {
			return nil, fmt.Errorf("%w: missing session index", ErrResumeUnsupported)
		}
		// Gemini uses a per-cwd index, so we can only resume Local
		// sessions (where cwd == projectDir by construction). Global /
		// Private sessions have an opaque hash; reversing it isn't
		// possible without keeping a path-to-hash map we don't have.
		if it.Scope != model.ScopeLocal || projectDir == "" {
			return nil, fmt.Errorf("%w: gemini resume needs the original cwd; rerun lazyagent from that project", ErrResumeUnsupported)
		}
		cmd := exec.Command("gemini", "--resume", idx)
		cmd.Dir = projectDir
		return cmd, nil

	case model.OriginCodex:
		return nil, fmt.Errorf("%w: codex resume coming in a later slice", ErrResumeUnsupported)
	}
	return nil, fmt.Errorf("%w: unknown origin", ErrResumeUnsupported)
}
