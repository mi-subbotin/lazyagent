package codex

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

// dirOrLinkToDir treats a symlink-to-directory the same as a real
// directory. Without it, shared-store projections (which arrive as
// symlinks) silently disappear from the per-tool listings.
func dirOrLinkToDir(parent string, e fs.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&fs.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(parent, e.Name()))
	if err != nil {
		return false
	}
	return info.IsDir()
}

// Source reads MCP servers, profiles, prompts, skills and AGENTS.md from
// the standard Codex CLI locations. Missing files are silently skipped.
//
// Layout assumed:
//
//	~/.codex/config.toml             -> [mcp_servers.<name>], [profiles.<name>]
//	~/.codex/prompts/*.md            -> saved prompts (KindPrompt)
//	~/.codex/AGENTS.md               -> global agent instructions (KindAgent)
//	~/.agents/skills/<name>/SKILL.md -> global skills (KindSkill, same shape
//	                                    as Claude / Gemini)
//	<project>/AGENTS.md              -> project agent instructions
//	<project>/.codex/config.toml     -> project-local MCP/profiles
//	<project>/.codex/prompts/        -> project-local prompts
//	<project>/.agents/skills/<name>/ -> project-local skills
//
// Note: Codex skills live under `.agents/`, not `.codex/` — see
// https://developers.openai.com/codex/skills.
type Source struct{}

func (Source) Name() string { return "codex" }

func (s Source) List(_ context.Context, projectDir string) ([]model.Item, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	var out []model.Item
	codexHome := filepath.Join(home, ".codex")

	// Global
	out = append(out, scanConfig(filepath.Join(codexHome, "config.toml"), model.ScopeGlobal)...)
	out = append(out, scanPrompts(filepath.Join(codexHome, "prompts"), model.ScopeGlobal)...)
	out = append(out, scanSkills(filepath.Join(home, ".agents", "skills"), model.ScopeGlobal)...)
	if a := readAgentsMD(filepath.Join(codexHome, "AGENTS.md"), model.ScopeGlobal); a != nil {
		out = append(out, *a)
	}
	out = append(out, scanSessions(codexHome, projectDir)...)

	// Project-local
	if projectDir != "" {
		out = append(out, scanConfig(filepath.Join(projectDir, ".codex", "config.toml"), model.ScopeLocal)...)
		out = append(out, scanPrompts(filepath.Join(projectDir, ".codex", "prompts"), model.ScopeLocal)...)
		out = append(out, scanSkills(filepath.Join(projectDir, ".agents", "skills"), model.ScopeLocal)...)
		if a := readAgentsMD(filepath.Join(projectDir, "AGENTS.md"), model.ScopeLocal); a != nil {
			out = append(out, *a)
		}
	}

	return out, nil
}

// scanSkills walks `<root>/<name>/SKILL.md`, identical to Claude/Gemini.
func scanSkills(dir string, scope model.Scope) []model.Item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []model.Item
	for _, e := range entries {
		if !dirOrLinkToDir(dir, e) {
			continue
		}
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("codex: skip skill", "path", path, "err", err)
			continue
		}
		fm := parse.Parse(string(data))
		name := fm.Fields["name"]
		if name == "" {
			name = e.Name()
		}
		parseErr, warnings := parse.DiagnoseFrontmatter(fm, []string{"name", "description"})
		if parseErr != "" {
			slog.Warn("codex: skill frontmatter has errors", "path", path, "errors", parseErr)
		}
		out = append(out, model.Item{
			Origin:             model.OriginCodex,
			Kind:               model.KindSkill,
			Scope:              scope,
			Name:               name,
			Path:               path,
			Description:        fm.Fields["description"],
			Body:               fm.Body,
			Meta:               fm.Fields,
			Storage:            model.StorageDir,
			Shared:             store.ResolvesToStore(filepath.Join(dir, e.Name())),
			ParseError:         parseErr,
			ValidationWarnings: warnings,
		})
	}
	return out
}

// scanConfig parses a Codex config.toml and returns one Item per MCP server
// (`[mcp_servers.<name>]`) plus one Item per profile (`[profiles.<name>]`),
// the latter exposed as KindAgent since profiles are the closest analogue
// of a "subagent" in Codex.
func scanConfig(path string, scope model.Scope) []model.Item {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil
	}

	var out []model.Item

	if servers, ok := raw["mcp_servers"].(map[string]any); ok {
		for name, entry := range servers {
			out = append(out, model.Item{
				Origin:      model.OriginCodex,
				Kind:        model.KindMCP,
				Scope:       scope,
				Name:        name,
				Path:        path,
				Description: mcpDescription(entry),
				RawJSON:     parse.MCPToJSON(entry),
				RawTOML:     parse.MCPToTOML(entry),
				Storage:     model.StorageEntry,
				ConfigKey:   parse.JoinKey("mcp_servers", name),
			})
		}
	}

	if profiles, ok := raw["profiles"].(map[string]any); ok {
		for name, entry := range profiles {
			out = append(out, model.Item{
				Origin:      model.OriginCodex,
				Kind:        model.KindAgent,
				Scope:       scope,
				Name:        name,
				Path:        path,
				Description: profileDescription(entry),
				Body:        profileBody(entry),
				RawJSON:     parse.MCPToJSON(entry),
				RawTOML:     parse.MCPToTOML(entry),
				Storage:     model.StorageEntry,
				ConfigKey:   parse.JoinKey("profiles", name),
			})
		}
	}

	return out
}

// scanPrompts reads `*.md` files from a Codex prompts directory.
func scanPrompts(dir string, scope model.Scope) []model.Item {
	var out []model.Item
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("codex: read prompt", "path", path, "err", err)
			return nil
		}
		fm := parse.Parse(string(data))
		name := fm.Fields["name"]
		if name == "" {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				rel = filepath.Base(path)
			}
			name = strings.TrimSuffix(rel, filepath.Ext(rel))
		}
		parseErr, warnings := parse.DiagnoseFrontmatter(fm, []string{"name", "description"})
		if parseErr != "" {
			slog.Warn("codex: prompt frontmatter has errors", "path", path, "errors", parseErr)
		}
		out = append(out, model.Item{
			Origin:             model.OriginCodex,
			Kind:               model.KindPrompt,
			Scope:              scope,
			Name:               name,
			Path:               path,
			Description:        fm.Fields["description"],
			Body:               fm.Body,
			Meta:               fm.Fields,
			Storage:            model.StorageFile,
			Shared:             store.ResolvesToStore(path),
			ParseError:         parseErr,
			ValidationWarnings: warnings,
		})
		return nil
	})
	return out
}

// readAgentsMD turns an AGENTS.md file into a single Memory item —
// these files are global / project instructions, not subagent
// definitions.
func readAgentsMD(path string, scope model.Scope) *model.Item {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	body := string(data)
	desc := firstNonEmptyLine(body)
	if len(desc) > 200 {
		desc = desc[:200] + "…"
	}
	return &model.Item{
		Origin:      model.OriginCodex,
		Kind:        model.KindMemory,
		Scope:       scope,
		Name:        "AGENTS.md",
		Path:        path,
		Description: desc,
		Body:        body,
		Storage:     model.StorageFile,
		Shared:      store.ResolvesToStore(path),
	}
}

func firstNonEmptyLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(strings.TrimLeft(ln, "#"))
		if t != "" {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

func mcpDescription(entry any) string {
	m, ok := entry.(map[string]any)
	if !ok {
		return ""
	}
	if cmd, ok := m["command"].(string); ok && cmd != "" {
		return cmd
	}
	if url, ok := m["url"].(string); ok && url != "" {
		return url
	}
	return ""
}

func profileDescription(entry any) string {
	m, ok := entry.(map[string]any)
	if !ok {
		return ""
	}
	if mp, ok := m["model"].(string); ok && mp != "" {
		return "model: " + mp
	}
	return ""
}

func profileBody(entry any) string {
	m, ok := entry.(map[string]any)
	if !ok {
		return ""
	}
	if instr, ok := m["instructions"].(string); ok {
		return instr
	}
	return ""
}
