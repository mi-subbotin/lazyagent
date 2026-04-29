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
		// `claude -r <id>` is project-scoped: it looks under
		// ~/.claude/projects/<encoded-cwd>/ and only finds the session
		// when run from the matching cwd. Empirically reproducible —
		// the help text doesn't say so but the binary refuses
		// cross-project lookups. We pin Cmd.Dir to the recorded cwd.
		// Empty Meta["cwd"] (zombie sessions) leaves Dir blank and
		// claude will fall back to its current behaviour.
		return resumePlan{
			Argv: []string{"claude", "-r", sid},
			Dir:  it.Meta["cwd"],
		}, nil

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

// BuildHashCwdIndex returns a sha256(cwd) → cwd map used by
// ResumeContext to recover the original cwd of a Gemini session
// (whose on-disk projectHash is one-way). The map is populated from
// two sources, in order of confidence:
//
//  1. Claude jsonl transcripts that record cwd directly. Highest
//     confidence — those are paths the user has already used Claude
//     in.
//  2. Best-effort walk of $HOME up to depth 4, skipping noise
//     directories (node_modules, vendor, .git). Catches projects the
//     user has touched only with Gemini, at the cost of a few ms of
//     stat traffic at startup.
func BuildHashCwdIndex(items []model.Item) map[string]string {
	home, _ := os.UserHomeDir()
	return buildHashCwdIndex(items, home)
}

// buildHashCwdIndex is the testable inner — pass home="" from tests to
// keep them hermetic (no walk over the dev's actual $HOME).
func buildHashCwdIndex(items []model.Item, home string) map[string]string {
	out := map[string]string{}
	for _, it := range items {
		if it.Origin != model.OriginClaude || it.Kind != model.KindSession {
			continue
		}
		cwd := it.Meta["cwd"]
		if cwd == "" {
			continue
		}
		out[sha256SumHex(cwd)] = cwd
	}
	if home != "" {
		walkLikelyCwds(home, 4, func(path string) {
			h := sha256SumHex(path)
			if _, exists := out[h]; !exists {
				out[h] = path
			}
		})
	}
	return out
}

// walkLikelyCwds visits root and every descendant directory up to
// maxDepth, calling visit on each. Skips dirs that obviously aren't
// project roots: hidden dirs (with a small allowlist for orchestrator
// roots like .claude-squad) and well-known noise (node_modules, vendor,
// .git, .cache). Depth is counted from root=0; a maxDepth of 4 covers
// `~/conductor/workspaces/<proj>/<branch>` and the like.
func walkLikelyCwds(root string, maxDepth int, visit func(string)) {
	visit(root)
	if maxDepth <= 0 {
		return
	}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if isNoiseDir(name) {
				continue
			}
			if strings.HasPrefix(name, ".") && !isAllowedHiddenDir(name) {
				continue
			}
			child := dir + string(os.PathSeparator) + name
			visit(child)
			if depth+1 < maxDepth {
				walk(child, depth+1)
			}
		}
	}
	walk(root, 0)
}

func isNoiseDir(name string) bool {
	switch name {
	case "node_modules", "vendor", ".git", ".cache", ".terraform",
		".venv", "venv", "__pycache__", "target", "build", "dist":
		return true
	}
	return false
}

func isAllowedHiddenDir(name string) bool {
	switch name {
	case ".claude-squad", ".config", ".local":
		return true
	}
	return false
}
