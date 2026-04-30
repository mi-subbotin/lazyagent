package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

// Target picks where an installed Candidate lands. Origin maps to a
// tool root, Scope chooses global vs project-local, and ProjectDir
// must be set when Scope == "local".
type Target struct {
	Origin     string // "claude" / "codex" / "gemini" / "shared"
	Scope      string // "global" / "local"
	ProjectDir string // required when Scope == "local"
}

// Validate normalises empty / mixed-case fields and rejects obviously
// wrong combinations early — well before any file is copied.
func (t *Target) Validate() error {
	t.Origin = strings.ToLower(strings.TrimSpace(t.Origin))
	t.Scope = strings.ToLower(strings.TrimSpace(t.Scope))
	switch t.Origin {
	case "claude", "codex", "gemini", "shared":
	case "":
		return errors.New("target origin not set (use claude / codex / gemini / shared)")
	default:
		return fmt.Errorf("unknown target origin %q", t.Origin)
	}
	switch t.Scope {
	case "global":
	case "local":
		if t.ProjectDir == "" {
			return errors.New("scope=local requires a project directory")
		}
	case "":
		t.Scope = "global"
	default:
		return fmt.Errorf("unknown target scope %q", t.Scope)
	}
	if t.Origin == "shared" && t.Scope == "local" {
		return errors.New("shared store has no local scope (it lives in ~/.lazyagent/store)")
	}
	return nil
}

// ResolvePath returns the absolute destination for a candidate under
// the chosen target. The error explains combinations that simply
// don't have a layout (Codex agents, Gemini prompts) so the CLI can
// surface a helpful message instead of writing to a wrong place.
func ResolvePath(c Candidate, t Target) (string, error) {
	if t.Origin == "shared" {
		dir, err := store.ItemDir(c.Kind, c.Name)
		if err != nil {
			return "", err
		}
		body, ok := store.CanonicalBodyName(c.Kind)
		if !ok {
			return "", fmt.Errorf("shared store has no layout for kind %s", c.Kind)
		}
		return filepath.Join(dir, body), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := home
	if t.Scope == "local" {
		root = t.ProjectDir
	}

	switch t.Origin {
	case "claude":
		return claudePath(root, c)
	case "codex":
		return codexPath(root, c)
	case "gemini":
		return geminiPath(root, c)
	}
	return "", fmt.Errorf("unsupported target origin %q", t.Origin)
}

func claudePath(root string, c Candidate) (string, error) {
	base := filepath.Join(root, ".claude")
	switch c.Kind {
	case model.KindSkill:
		return filepath.Join(base, "skills", c.Name, "SKILL.md"), nil
	case model.KindAgent:
		return filepath.Join(base, "agents", c.Name+".md"), nil
	case model.KindPrompt:
		return filepath.Join(base, "commands", c.Name+".md"), nil
	}
	return "", fmt.Errorf("claude has no install layout for kind %s", c.Kind)
}

// codexPath: skills live under ~/.agents/ (per CLAUDE.md remap),
// prompts under ~/.codex/prompts. Codex doesn't have per-agent .md
// files — AGENTS.md is the shared memory file — so we reject that
// combo with a clear error rather than guessing.
func codexPath(root string, c Candidate) (string, error) {
	switch c.Kind {
	case model.KindSkill:
		return filepath.Join(root, ".agents", "skills", c.Name, "SKILL.md"), nil
	case model.KindPrompt:
		return filepath.Join(root, ".codex", "prompts", c.Name+".md"), nil
	case model.KindAgent:
		return "", fmt.Errorf("codex has no per-agent layout — use AGENTS.md or pick a different target")
	}
	return "", fmt.Errorf("codex has no install layout for kind %s", c.Kind)
}

// geminiPath: skills + agents are .md, but Gemini commands are TOML
// (different schema), so installing a Claude/community-shaped
// commands/<n>.md as a Gemini prompt would silently produce a broken
// command. Reject that combo.
func geminiPath(root string, c Candidate) (string, error) {
	base := filepath.Join(root, ".gemini")
	switch c.Kind {
	case model.KindSkill:
		return filepath.Join(base, "skills", c.Name, "SKILL.md"), nil
	case model.KindAgent:
		return filepath.Join(base, "agents", c.Name+".md"), nil
	case model.KindPrompt:
		return "", fmt.Errorf("gemini commands are TOML, not .md — install to claude/codex or convert manually")
	}
	return "", fmt.Errorf("gemini has no install layout for kind %s", c.Kind)
}

// ApplyOptions controls Apply's filesystem behaviour at the
// destination. Overwrite=false (the default) leaves an existing file
// alone and returns ErrAlreadyExists so the caller can surface a
// "[r]eplace / [k]eep / [s]kip" prompt.
type ApplyOptions struct {
	Overwrite bool
}

// ErrAlreadyExists is returned by Apply when the destination already
// has content and Overwrite is false.
var ErrAlreadyExists = errors.New("install destination already exists")

// Apply copies the candidate's bytes from cacheDir into the resolved
// destination and returns the manifest Install entry the CLI should
// add to ~/.lazyagent/installed.toml. originURL/sha pin the source
// version for later update / uninstall.
func Apply(cacheDir string, c Candidate, t Target, originURL, sha string, opts ApplyOptions) (Install, error) {
	if err := t.Validate(); err != nil {
		return Install{}, err
	}
	dst, err := ResolvePath(c, t)
	if err != nil {
		return Install{}, err
	}

	srcAbs := filepath.Join(cacheDir, filepath.FromSlash(c.SourceRel))
	if _, err := os.Stat(srcAbs); err != nil {
		return Install{}, fmt.Errorf("source missing: %w", err)
	}

	switch c.Storage {
	case model.StorageDir:
		if err := materializeDir(filepath.Dir(srcAbs), filepath.Dir(dst), opts.Overwrite); err != nil {
			return Install{}, err
		}
	case model.StorageFile:
		if err := materializeFile(srcAbs, dst, opts.Overwrite); err != nil {
			return Install{}, err
		}
	default:
		return Install{}, fmt.Errorf("install storage %v not supported", c.Storage)
	}

	return Install{
		Name:         c.Name,
		Kind:         kindString(c.Kind),
		OriginURL:    originURL,
		SHA:          sha,
		InstalledAt:  time.Now().UTC(),
		TargetOrigin: t.Origin,
		TargetScope:  t.Scope,
		TargetPath:   dst,
		SourceRel:    c.SourceRel,
	}, nil
}

// Uninstall removes the bytes referenced by an Install entry. Skills
// (StorageDir-style) drop the whole <name>/ directory; single-file
// kinds remove just the file. Returns nil if the path is already
// gone — uninstalling a manually-deleted item shouldn't fail.
func Uninstall(in Install) error {
	if in.TargetPath == "" {
		return errors.New("install entry has no target path")
	}
	// Decide whether the install was directory-shaped from the path
	// suffix: SKILL.md means we created its parent dir, so we must
	// remove the parent. Single .md files we created directly.
	if strings.EqualFold(filepath.Base(in.TargetPath), "SKILL.md") {
		dir := filepath.Dir(in.TargetPath)
		if err := os.RemoveAll(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.Remove(in.TargetPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func materializeFile(src, dst string, overwrite bool) error {
	if _, err := os.Stat(dst); err == nil {
		if !overwrite {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, dst)
		}
		if err := os.Remove(dst); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func materializeDir(src, dst string, overwrite bool) error {
	if _, err := os.Stat(dst); err == nil {
		if !overwrite {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, dst)
		}
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return materializeFile(path, target, true)
	})
}

func kindString(k model.Kind) string {
	switch k {
	case model.KindSkill:
		return "skill"
	case model.KindAgent:
		return "agent"
	case model.KindPrompt:
		return "prompt"
	case model.KindMCP:
		return "mcp"
	case model.KindMemory:
		return "memory"
	}
	return "unknown"
}
