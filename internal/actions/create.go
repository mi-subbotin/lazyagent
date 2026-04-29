package actions

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

//go:embed templates/*.md
var createTemplates embed.FS

// ErrCreateUnsupported is returned by Create when the requested
// (Origin, Kind) combination is StorageEntry-shaped (Codex profile, MCP
// server, Memory file) and so cannot be scaffolded as a fresh file.
// Those need entry-aware editors handled separately.
var ErrCreateUnsupported = errors.New("create not supported for this Origin/Kind — use the external editor")

var slugRe = regexp.MustCompile(`[^a-z0-9_-]+`)

// Slugify normalises a free-form display name into a filesystem-safe
// slug. Lowercase, ASCII letters / digits / underscore / hyphen, with
// runs of disallowed characters collapsed into a single `-`. Returns
// empty if nothing usable remains, which the caller should reject.
func Slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-_")
	return s
}

// Create scaffolds a new item of (origin, kind, scope) named name. For
// Skill it creates `<root>/skills/<slug>/SKILL.md`; for Agent and
// Prompt it creates a single `.md` file. The returned path points at
// the file the caller should open in $EDITOR. Refuses to overwrite
// existing files. projectDir is required when scope is Local.
func Create(origin model.Origin, kind model.Kind, scope model.Scope, name, projectDir string) (string, error) {
	slug := Slugify(name)
	if slug == "" {
		return "", fmt.Errorf("invalid name (need at least one letter or digit)")
	}
	if scope == model.ScopeLocal && projectDir == "" {
		return "", ErrNoProject
	}

	path, err := createPath(origin, kind, scope, slug, projectDir)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%w: %s", ErrTargetExists, path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	tmplName, ok := templateForKind(kind)
	if !ok {
		return "", ErrCreateUnsupported
	}
	body, err := renderCreateTemplate(tmplName, name, slug)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// createPath resolves where a new (origin, kind, scope) item should
// land. Returns ErrCreateUnsupported for combinations that aren't
// file-shaped (Codex profiles, MCP servers, memory files); those need
// dedicated entry / single-file editors handled separately.
func createPath(origin model.Origin, kind model.Kind, scope model.Scope, slug, projectDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	// rootFor returns the on-disk root that owns kind for origin in the
	// requested scope. For Codex skills the root is `.agents/`, not
	// `.codex/`, per OpenAI's docs.
	rootFor := func(subdir string) string {
		switch origin {
		case model.OriginClaude:
			if scope == model.ScopeGlobal {
				return filepath.Join(home, ".claude", subdir)
			}
			return filepath.Join(projectDir, ".claude", subdir)
		case model.OriginCodex:
			if kind == model.KindSkill {
				if scope == model.ScopeGlobal {
					return filepath.Join(home, ".agents", subdir)
				}
				return filepath.Join(projectDir, ".agents", subdir)
			}
			if scope == model.ScopeGlobal {
				return filepath.Join(home, ".codex", subdir)
			}
			return filepath.Join(projectDir, ".codex", subdir)
		case model.OriginGemini:
			if scope == model.ScopeGlobal {
				return filepath.Join(home, ".gemini", subdir)
			}
			return filepath.Join(projectDir, ".gemini", subdir)
		}
		return ""
	}

	switch kind {
	case model.KindSkill:
		return filepath.Join(rootFor("skills"), slug, "SKILL.md"), nil
	case model.KindAgent:
		// Codex agents are entry-shaped (profiles in config.toml) — not
		// supported as file-create.
		if origin == model.OriginCodex {
			return "", ErrCreateUnsupported
		}
		return filepath.Join(rootFor("agents"), slug+".md"), nil
	case model.KindPrompt:
		// Gemini "commands" are TOML, not Markdown — that needs a
		// separate template / wizard, deferred.
		if origin == model.OriginGemini {
			return "", ErrCreateUnsupported
		}
		// Claude "commands", Codex "prompts" — both Markdown.
		subdir := "commands"
		if origin == model.OriginCodex {
			subdir = "prompts"
		}
		return filepath.Join(rootFor(subdir), slug+".md"), nil
	case model.KindMCP, model.KindMemory:
		// MCP entries live inside shared config files; Memory is a
		// singleton per scope. Both require dedicated handling — out
		// of scope here.
		return "", ErrCreateUnsupported
	}
	return "", fmt.Errorf("unknown kind %v", kind)
}

func templateForKind(k model.Kind) (string, bool) {
	switch k {
	case model.KindSkill:
		return "skill.md", true
	case model.KindAgent:
		return "agent.md", true
	case model.KindPrompt:
		return "prompt.md", true
	}
	return "", false
}

func renderCreateTemplate(name, displayName, slug string) ([]byte, error) {
	raw, err := createTemplates.ReadFile("templates/" + name)
	if err != nil {
		return nil, err
	}
	t, err := template.New(name).Parse(string(raw))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]string{"Name": displayName, "Slug": slug}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
