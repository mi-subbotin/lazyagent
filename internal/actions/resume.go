package actions

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
// opens a fresh tab in the user's terminal (iTerm2, Warp, or Apple
// Terminal, auto-detected via $TERM_PROGRAM) and runs the resume
// there. The TUI keeps running in the original pane — handy for users
// who want to keep the lazyagent tree open while the conversation
// resumes elsewhere.
func ResumeNewTabCommand(it model.Item, ctx ResumeContext) (*exec.Cmd, error) {
	plan, err := planResume(it, ctx)
	if err != nil {
		return nil, err
	}
	script, err := newTabAppleScript(plan)
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
		sid := it.ConfigKey
		if sid == "" {
			return resumePlan{}, fmt.Errorf("%w: missing sessionId", ErrResumeUnsupported)
		}
		// `codex resume <UUID>` accepts a session id directly. Codex's
		// default picker filters by current cwd, and `resume <UUID>`
		// inherits the same filter — pin Cmd.Dir to the recorded cwd so
		// the lookup doesn't refuse a session that lives in another
		// project. Empty Meta["cwd"] (zombie / corrupt rows in
		// state_5.sqlite) leaves Dir blank and codex falls back to its
		// own behaviour.
		return resumePlan{
			Argv: []string{"codex", "resume", sid},
			Dir:  it.Meta["cwd"],
		}, nil
	}
	return resumePlan{}, fmt.Errorf("%w: unknown origin", ErrResumeUnsupported)
}

// geminiResumeDir picks the cwd to spawn gemini --resume from, in
// order of confidence:
//  1. Meta["cwd"] stamped by the adapter from a `.project_root`
//     marker file (newer Gemini layout, ≥0.40). Strongest signal.
//  2. Local-bucket sessions: cwd == projectDir (by construction —
//     projectHash == sha256(projectDir)).
//  3. Look up Meta["projectHash"] in the Claude-derived hash→cwd
//     index. Covers the common case where the user has touched the
//     same project with claude at some point.
func geminiResumeDir(it model.Item, ctx ResumeContext) (string, bool) {
	if cwd := it.Meta["cwd"]; cwd != "" {
		return cwd, true
	}
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
// the user's terminal and runs the resume command there. Auto-detects
// iTerm2, Warp, and Apple Terminal via $TERM_PROGRAM; anything we
// don't recognise falls back to Terminal.app since macOS always has
// one. Each backend takes a slightly different shape because their
// scripting surfaces don't agree:
//
//   - iTerm2 has first-class AppleScript: "create tab" + "write text".
//   - Warp has a URL scheme for the new tab + cwd, but no documented
//     way to inject a command, so we keystroke it via System Events.
//     macOS will prompt for Accessibility permission on first use.
//   - Apple Terminal's `do script` accepts the full shell line (cd &&
//     cmd), so we use it as-is.
func newTabAppleScript(plan resumePlan) (string, error) {
	bareCmd := plan.bareCmd()
	shellLine := plan.shellLine()
	osaShellLine := osaQuote(shellLine)
	osaBareCmd := osaQuote(bareCmd)

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
end tell`, osaShellLine), nil

	case "WarpTerminal":
		// Warp's URL scheme handles cwd; the command itself is typed
		// via System Events because Warp doesn't expose a documented
		// "open new tab AND run X" entry point. The 0.5s delay lets
		// the new tab finish booting its shell before we inject keys —
		// shorter values race the prompt and lose characters on
		// slower machines.
		urlPart := "warp://action/new_tab"
		if plan.Dir != "" {
			urlPart += "?path=" + url.QueryEscape(plan.Dir)
		}
		return fmt.Sprintf(`tell application "Warp" to activate
do shell script "open %s"
delay 0.5
tell application "System Events"
    keystroke "%s"
    key code 36
end tell`, osaQuote(urlPart), osaBareCmd), nil

	default:
		// Apple Terminal. `do script` opens a new window in the front
		// app and runs the line in it; with `activate` it raises focus.
		return fmt.Sprintf(`tell application "Terminal"
    activate
    do script "%s"
end tell`, osaShellLine), nil
	}
}

// bareCmd returns just the resume invocation — no leading `cd`. Warp
// sets the cwd via the URL scheme, so we type the command standalone.
func (p resumePlan) bareCmd() string {
	quoted := make([]string, len(p.Argv))
	for i, a := range p.Argv {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// osaQuote escapes a string for embedding inside an AppleScript
// double-quoted literal. AppleScript only treats `\` and `"` as
// special inside a string, so escaping those two is sufficient.
func osaQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// BuildHashCwdIndex returns a sha256(cwd) → cwd map used by
// ResumeContext to recover the original cwd of a Gemini session
// (whose on-disk projectHash is one-way). The map is populated from
// three sources, in order of confidence:
//
//  1. Claude jsonl transcripts that record cwd directly. Highest
//     confidence — those are paths the user has already used Claude
//     in.
//  2. Gemini ≥0.40 `.project_root` marker files under
//     ~/.gemini/tmp/<basename>/. Each holds an absolute cwd; the same
//     cwd may also have a sibling sha256-named bucket left over from
//     older releases, and that older bucket is exactly the case the
//     hash index needs to rescue.
//  3. Best-effort walk of $HOME up to depth 4, skipping noise
//     directories (node_modules, vendor, .git). Catches projects the
//     user has touched only with Gemini in old layout, at the cost
//     of a few ms of stat traffic at startup.
//  4. Item Meta["cwd"] from any session origin (Codex sqlite rows,
//     Gemini sessions stamped via .project_root). Cheap and folds in
//     paths the walker would have missed (external volumes, etc.).
func BuildHashCwdIndex(items []model.Item) map[string]string {
	home, _ := os.UserHomeDir()
	return buildHashCwdIndex(items, home)
}

// buildHashCwdIndex is the testable inner — pass home="" from tests to
// keep them hermetic (no walk over the dev's actual $HOME).
func buildHashCwdIndex(items []model.Item, home string) map[string]string {
	out := map[string]string{}
	add := func(cwd string) {
		if cwd == "" {
			return
		}
		h := sha256SumHex(cwd)
		if _, exists := out[h]; !exists {
			out[h] = cwd
		}
	}
	for _, it := range items {
		if it.Kind != model.KindSession {
			continue
		}
		if it.Origin == model.OriginClaude || it.Origin == model.OriginGemini || it.Origin == model.OriginCodex {
			add(it.Meta["cwd"])
		}
	}
	if home != "" {
		readGeminiProjectRoots(filepath.Join(home, ".gemini", "tmp"), add)
		walkLikelyCwds(home, 4, add)
	}
	return out
}

// readGeminiProjectRoots scans ~/.gemini/tmp/<bucket>/.project_root
// files and feeds each absolute cwd to visit. Cheap — at most a few
// dozen tiny reads. We don't try to filter; visit dedupes itself via
// the closure-captured map.
func readGeminiProjectRoots(tmpDir string, visit func(string)) {
	ents, err := os.ReadDir(tmpDir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, e.Name(), ".project_root"))
		if err != nil {
			continue
		}
		visit(strings.TrimSpace(string(data)))
	}
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
