package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

// dirOrLinkToDir treats a symlink-to-directory the same as a real
// directory. Without this, shared-store projections (which arrive as
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

// Source reads skills, subagents, MCP servers, custom commands and
// GEMINI.md from the standard Gemini CLI locations. Missing files are
// silently skipped.
//
// Layout assumed:
//
//	~/.gemini/settings.json       -> top-level mcpServers (KindMCP)
//	~/.gemini/skills/<name>/SKILL.md
//	~/.gemini/agents/*.md         -> subagents (KindAgent), see
//	                                 https://geminicli.com/docs/core/subagents/
//	~/.gemini/commands/**/*.toml  -> custom commands (KindPrompt)
//	~/.gemini/GEMINI.md           -> global memory (KindMemory)
//	<project>/.gemini/agents/*.md
//	<project>/.gemini/settings.json
//	<project>/.gemini/commands/
//	<project>/GEMINI.md
type Source struct{}

func (Source) Name() string { return "gemini" }

func (s Source) List(_ context.Context, projectDir string) ([]model.Item, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	var out []model.Item
	geminiHome := filepath.Join(home, ".gemini")

	// Global
	out = append(out, scanSkills(filepath.Join(geminiHome, "skills"), model.ScopeGlobal)...)
	out = append(out, scanAgents(filepath.Join(geminiHome, "agents"), model.ScopeGlobal)...)
	out = append(out, scanMCPSettings(filepath.Join(geminiHome, "settings.json"), model.ScopeGlobal)...)
	out = append(out, scanCommands(filepath.Join(geminiHome, "commands"), model.ScopeGlobal)...)
	if a := readGeminiMD(filepath.Join(geminiHome, "GEMINI.md"), model.ScopeGlobal); a != nil {
		out = append(out, *a)
	}
	out = append(out, scanSessions(geminiHome)...)

	// Project-local
	if projectDir != "" {
		out = append(out, scanSkills(filepath.Join(projectDir, ".gemini", "skills"), model.ScopeLocal)...)
		out = append(out, scanAgents(filepath.Join(projectDir, ".gemini", "agents"), model.ScopeLocal)...)
		out = append(out, scanMCPSettings(filepath.Join(projectDir, ".gemini", "settings.json"), model.ScopeLocal)...)
		out = append(out, scanCommands(filepath.Join(projectDir, ".gemini", "commands"), model.ScopeLocal)...)
		if a := readGeminiMD(filepath.Join(projectDir, "GEMINI.md"), model.ScopeLocal); a != nil {
			out = append(out, *a)
		}
	}

	return out, nil
}

// scanAgents reads .md subagent definitions from the given directory.
// Same shape as Claude's agents/ — flat directory of .md files with
// YAML frontmatter (name, description, plus tool-specific extras).
func scanAgents(dir string, scope model.Scope) []model.Item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []model.Item
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fm := parse.Parse(string(data))
		name := fm.Fields["name"]
		if name == "" {
			name = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		}
		out = append(out, model.Item{
			Origin:      model.OriginGemini,
			Kind:        model.KindAgent,
			Scope:       scope,
			Name:        name,
			Path:        path,
			Description: fm.Fields["description"],
			Body:        fm.Body,
			Meta:        fm.Fields,
			Storage:     model.StorageFile,
			Shared:      store.ResolvesToStore(path),
		})
	}
	return out
}

// scanSkills walks `<root>/<name>/SKILL.md`, same shape as Claude's skills.
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
			continue
		}
		fm := parse.Parse(string(data))
		name := fm.Fields["name"]
		if name == "" {
			name = e.Name()
		}
		out = append(out, model.Item{
			Origin:      model.OriginGemini,
			Kind:        model.KindSkill,
			Scope:       scope,
			Name:        name,
			Path:        path,
			Description: fm.Fields["description"],
			Body:        fm.Body,
			Meta:        fm.Fields,
			Storage:     model.StorageDir,
			Shared:      store.ResolvesToStore(filepath.Join(dir, e.Name())),
		})
	}
	return out
}

// scanMCPSettings extracts MCP servers from a settings.json `mcpServers` map.
func scanMCPSettings(path string, scope model.Scope) []model.Item {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	servers, ok := raw["mcpServers"].(map[string]any)
	if !ok {
		return nil
	}
	out := make([]model.Item, 0, len(servers))
	for name, entry := range servers {
		out = append(out, model.Item{
			Origin:      model.OriginGemini,
			Kind:        model.KindMCP,
			Scope:       scope,
			Name:        name,
			Path:        path,
			Description: mcpDescription(entry),
			RawJSON:     parse.MCPToJSON(entry),
			RawTOML:     parse.MCPToTOML(entry),
			Storage:     model.StorageEntry,
			ConfigKey:   "mcpServers/" + name,
		})
	}
	return out
}

// scanCommands walks `<root>/**/*.toml`. Each file is a Gemini custom command
// with at minimum a `prompt` (and optionally `description`) field.
func scanCommands(dir string, scope model.Scope) []model.Item {
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
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".toml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var raw map[string]any
		if err := toml.Unmarshal(data, &raw); err != nil {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = filepath.Base(path)
		}
		name := strings.TrimSuffix(rel, filepath.Ext(rel))
		desc, _ := raw["description"].(string)
		body, _ := raw["prompt"].(string)
		meta := map[string]string{}
		for k, v := range raw {
			if sv, ok := v.(string); ok {
				meta[k] = sv
			}
		}
		out = append(out, model.Item{
			Origin:      model.OriginGemini,
			Kind:        model.KindPrompt,
			Scope:       scope,
			Name:        name,
			Path:        path,
			Description: desc,
			Body:        body,
			Meta:        meta,
			Storage:     model.StorageFile,
			Shared:      store.ResolvesToStore(path),
		})
		return nil
	})
	return out
}

func readGeminiMD(path string, scope model.Scope) *model.Item {
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
		Origin:      model.OriginGemini,
		Kind:        model.KindMemory,
		Scope:       scope,
		Name:        "GEMINI.md",
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
	if t, ok := m["type"].(string); ok && t != "" {
		return t
	}
	return ""
}
