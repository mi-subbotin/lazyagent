package actions

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func sha256SumHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ErrResumeUnsupported means we can't construct a resume command for
// this combination of (origin, scope) — usually because the upstream
// CLI requires a cwd we don't have. The TUI surfaces the message as a
// toast rather than failing silently.
var ErrResumeUnsupported = errors.New("resume not supported")

// ResumeContext carries everything the resume planner needs to
// reconstruct a working invocation. ProjectDir is the project root
// detected by main.go (empty when launched from outside any project).
// KnownHashCwd maps sha256(cwd) → cwd, populated from Claude jsonl
// transcripts which do record the cwd; this lets us reverse Gemini's
// projectHash (which can't be reversed cryptographically) for cwd-s
// the user has touched with Claude at least once.
type ResumeContext struct {
	ProjectDir   string
	KnownHashCwd map[string]string
}

// ResumeCommand builds an *exec.Cmd that re-enters a recorded session
// in the current pane. The TUI hands it to tea.ExecProcess so the
// upstream CLI takes over the terminal until the user exits.
func ResumeCommand(it model.Item, ctx ResumeContext) (*exec.Cmd, error) {
	plan, err := planResume(it, ctx)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(plan.Argv[0], plan.Argv[1:]...)
	cmd.Dir = plan.Dir
	return cmd, nil
}

// ResumeNewTabCommand wraps the resume invocation in an osascript that
// opens a fresh tab in the user's terminal (iTerm2 or Apple Terminal,
// auto-detected via $TERM_PROGRAM) and runs the resume there. The TUI
// keeps running in the original pane — handy for users who want to
// keep the lazyagent tree open while the conversation resumes
// elsewhere.
func ResumeNewTabCommand(it model.Item, ctx ResumeContext) (*exec.Cmd, error) {
	plan, err := planResume(it, ctx)
	if err != nil {
		return nil, err
	}
	shellLine := plan.shellLine()
	script, err := newTabAppleScript(shellLine)
	if err != nil {
		return nil, err
	}
	return exec.Command("osascript", "-e", script), nil
}

// resumePlan is the tool-agnostic intent: argv to spawn and (optional)
// cwd to spawn it in. shellLine() reflattens it into a single shell
// command suitable for embedding in osascript.
type resumePlan struct {
	Argv []string
	Dir  string
}

func (p resumePlan) shellLine() string {
	quoted := make([]string, len(p.Argv))
	for i, a := range p.Argv {
		quoted[i] = shellQuote(a)
	}
	cmdLine := strings.Join(quoted, " ")
	if p.Dir != "" {
		return "cd " + shellQuote(p.Dir) + " && " + cmdLine
	}
	return cmdLine
}

func planResume(it model.Item, ctx ResumeContext) (resumePlan, error) {
	if it.Kind != model.KindSession {
		return resumePlan{}, fmt.Errorf("%w: not a session", ErrResumeUnsupported)
	}
	switch it.Origin {
	case model.OriginClaude:
		sid := it.ConfigKey
		if sid == "" {
			return resumePlan{}, fmt.Errorf("%w: missing sessionId", ErrResumeUnsupported)
		}
		// `claude -r <id>` looks the session up by ID — works from any
		// cwd, so we don't pin Dir.
		return resumePlan{Argv: []string{"claude", "-r", sid}}, nil

	case model.OriginGemini:
		idx := it.Meta["index"]
		if idx == "" {
			return resumePlan{}, fmt.Errorf("%w: missing session index", ErrResumeUnsupported)
		}
		// Sanity-check the index — some adapter bug stamping "abc" here
		// would cause gemini to error mid-resume rather than refuse up
		// front.
		if _, err := strconv.Atoi(idx); err != nil {
			return resumePlan{}, fmt.Errorf("%w: bad session index %q", ErrResumeUnsupported, idx)
		}
		dir, ok := geminiResumeDir(it, ctx)
		if !ok {
			return resumePlan{}, fmt.Errorf("%w: gemini needs the original cwd; rerun lazyagent from that project (or run claude there once so its hash is known)", ErrResumeUnsupported)
		}
		return resumePlan{Argv: []string{"gemini", "--resume", idx}, Dir: dir}, nil

	case model.OriginCodex:
		return resumePlan{}, fmt.Errorf("%w: codex resume coming in a later slice", ErrResumeUnsupported)
	}
	return resumePlan{}, fmt.Errorf("%w: unknown origin", ErrResumeUnsupported)
}

// geminiResumeDir picks the cwd to spawn gemini --resume from:
//  1. Local-bucket sessions: cwd == projectDir (by construction —
//     projectHash == sha256(projectDir)).
//  2. Anything else: look up Meta["projectHash"] in the
//     Claude-derived hash→cwd index. Covers the common case where the
//     user has touched the same project with claude at some point.
func geminiResumeDir(it model.Item, ctx ResumeContext) (string, bool) {
	if it.Scope == model.ScopeLocal && ctx.ProjectDir != "" {
		return ctx.ProjectDir, true
	}
	if hash := it.Meta["projectHash"]; hash != "" && ctx.KnownHashCwd != nil {
		if cwd, ok := ctx.KnownHashCwd[hash]; ok {
			return cwd, true
		}
	}
	return "", false
}

// shellQuote single-quotes a token so it survives a sh -c expansion.
// Only single-quote escaping is needed since we always wrap in
// single-quotes; embedded single-quotes get the standard '\'' dance.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// newTabAppleScript renders an AppleScript that opens a new tab in
// the user's terminal and types the given shell line. We auto-detect
// iTerm2 vs Apple Terminal via $TERM_PROGRAM; everything else falls
// back to Terminal.app since macOS always has one. Returns an error
// only on unrecoverable cases (none today, but kept for parity with
// Linux/Windows variants we'll add later).
func newTabAppleScript(shellLine string) (string, error) {
	// AppleScript single-quotes badly; we escape double-quotes in the
	// shell line for inclusion inside a "..." string literal.
	escaped := strings.ReplaceAll(shellLine, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)

	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app":
		return fmt.Sprintf(`tell application "iTerm2"
    activate
    tell current window
        set newTab to (create tab with default profile)
        tell current session of newTab
            write text "%s"
        end tell
    end tell
end tell`, escaped), nil
	default:
		// Apple Terminal. `do script` opens a new window, but with
		// `tell application "Terminal" to activate` it raises focus.
		return fmt.Sprintf(`tell application "Terminal"
    activate
    do script "%s"
end tell`, escaped), nil
	}
}

// BuildHashCwdIndex scans items for Claude session metadata and
// returns the sha256(cwd) → cwd map needed by ResumeContext to handle
// non-Local Gemini resumes. Items without Meta["cwd"] are skipped.
// Pure on the input; safe to call repeatedly.
func BuildHashCwdIndex(items []model.Item) map[string]string {
	out := map[string]string{}
	for _, it := range items {
		if it.Origin != model.OriginClaude || it.Kind != model.KindSession {
			continue
		}
		cwd := it.Meta["cwd"]
		if cwd == "" {
			continue
		}
		h := sha256SumHex(cwd)
		out[h] = cwd
	}
	return out
}
