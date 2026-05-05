package guardrails

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MemoryBloat warns when CLAUDE.md (global or project-local) crosses
// MaxBytes. Memory files are loaded into every session prompt verbatim,
// so a 50KB CLAUDE.md is just a per-session token tax most users don't
// realise they're paying.
type MemoryBloat struct {
	MaxBytes int
}

func (MemoryBloat) Name() string { return "memory-bloat" }

func (MemoryBloat) Description() string {
	return "Warn when CLAUDE.md (global or project) is large enough to noticeably bloat every session prompt."
}

func (g MemoryBloat) Evaluate(ctx EvalContext) Result {
	limit := g.MaxBytes
	if limit <= 0 {
		limit = 8192
	}

	var oversized []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		check(filepath.Join(home, ".claude", "CLAUDE.md"), limit, &oversized)
	}
	if ctx.ProjectDir != "" {
		check(filepath.Join(ctx.ProjectDir, "CLAUDE.md"), limit, &oversized)
	}
	if len(oversized) == 0 {
		return Result{Action: ActionAllow}
	}
	return Result{
		Action:  ActionWarn,
		Message: "lazyagent guardrail: oversized memory file(s) above " + sizeStr(limit) + ": " + strings.Join(oversized, ", "),
	}
}

func check(path string, limit int, out *[]string) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return
	}
	if info.Size() > int64(limit) {
		*out = append(*out, fmt.Sprintf("%s (%s)", path, sizeStr(int(info.Size()))))
	}
}

func sizeStr(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1fMB", float64(n)/1024/1024)
	case n >= 1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func init() {
	Register(MemoryBloat{})
}
