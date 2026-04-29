package claude

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

// dirOrLinkToDir reports whether a DirEntry returned by os.ReadDir
// points at a directory — including the case where it's a symlink
// whose target is a directory. fs.DirEntry.IsDir() returns false for
// symlinks (it doesn't follow), so without this helper shared-store
// projections silently drop out of the per-tool listings.
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

// Source reads skills, subagents, slash commands and MCP server entries from
// the standard Claude Code locations. Missing files are silently skipped —
// the user is allowed to have any subset of these configured.
type Source struct{}

func (Source) Name() string { return "claude" }

func (s Source) List(_ context.Context, projectDir string) ([]model.Item, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	var out []model.Item

	// Global
	globalRoot := filepath.Join(home, ".claude")
	out = append(out, scanSkills(globalRoot, model.ScopeGlobal)...)
	out = append(out, scanAgents(globalRoot, model.ScopeGlobal)...)
	out = append(out, scanCommands(globalRoot, model.ScopeGlobal)...)
	out = append(out, scanMCPFile(filepath.Join(home, ".claude.json"), "", model.ScopeGlobal)...)
	out = append(out, scanMCPFile(filepath.Join(globalRoot, "settings.json"), "", model.ScopeGlobal)...)
	if mem := readMemory(filepath.Join(globalRoot, "CLAUDE.md"), model.ScopeGlobal); mem != nil {
		out = append(out, *mem)
	}
	out = append(out, scanSessions(globalRoot)...)

	// Project-local (only if a project root was detected)
	if projectDir != "" {
		localRoot := filepath.Join(projectDir, ".claude")
		out = append(out, scanSkills(localRoot, model.ScopeLocal)...)
		out = append(out, scanAgents(localRoot, model.ScopeLocal)...)
		out = append(out, scanCommands(localRoot, model.ScopeLocal)...)
		out = append(out, scanMCPFile(filepath.Join(projectDir, ".mcp.json"), "", model.ScopeLocal)...)
		out = append(out, scanMCPFile(filepath.Join(localRoot, "settings.json"), "", model.ScopeLocal)...)
		// Per-project mcpServers nested under `projects.<absPath>` in ~/.claude.json
		out = append(out, scanMCPFile(filepath.Join(home, ".claude.json"), projectDir, model.ScopeLocal)...)
		if mem := readMemory(filepath.Join(projectDir, "CLAUDE.md"), model.ScopeLocal); mem != nil {
			out = append(out, *mem)
		}
	}

	return out, nil
}

// readMemory reads a CLAUDE.md memory file (global or project-local) and
// returns it as a single KindMemory item. Returns nil if the file is
// absent.
func readMemory(path string, scope model.Scope) *model.Item {
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
		Origin:      model.OriginClaude,
		Kind:        model.KindMemory,
		Scope:       scope,
		Name:        "CLAUDE.md",
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

// scanSkills looks for `<root>/skills/<name>/SKILL.md`.
func scanSkills(root string, scope model.Scope) []model.Item {
	dir := filepath.Join(root, "skills")
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
			Origin:      model.OriginClaude,
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

// scanAgents looks for `<root>/agents/*.md`.
func scanAgents(root string, scope model.Scope) []model.Item {
	dir := filepath.Join(root, "agents")
	return scanFlatMarkdown(dir, scope, model.KindAgent)
}

// scanCommands looks for `<root>/commands/**/*.md` recursively.
func scanCommands(root string, scope model.Scope) []model.Item {
	dir := filepath.Join(root, "commands")
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
		out = append(out, parseMarkdownItem(path, scope, model.KindPrompt, dir))
		return nil
	})
	return out
}

func scanFlatMarkdown(dir string, scope model.Scope, kind model.Kind) []model.Item {
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
		out = append(out, parseMarkdownItem(filepath.Join(dir, e.Name()), scope, kind, dir))
	}
	return out
}

func parseMarkdownItem(path string, scope model.Scope, kind model.Kind, baseDir string) model.Item {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.Item{
			Origin: model.OriginClaude,
			Kind:   kind,
			Scope:  scope,
			Name:   strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
			Path:   path,
		}
	}
	fm := parse.Parse(string(data))
	name := fm.Fields["name"]
	if name == "" {
		// For nested commands prefer "<subdir>/<file>" as a name so users can
		// distinguish duplicates.
		rel, err := filepath.Rel(baseDir, path)
		if err == nil {
			name = strings.TrimSuffix(rel, filepath.Ext(rel))
		} else {
			name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
	}
	return model.Item{
		Origin:      model.OriginClaude,
		Kind:        kind,
		Scope:       scope,
		Name:        name,
		Path:        path,
		Description: fm.Fields["description"],
		Body:        fm.Body,
		Meta:        fm.Fields,
		Storage:     model.StorageFile,
		Shared:      store.ResolvesToStore(path),
	}
}

// scanMCPFile reads a JSON file and emits one Item per entry under
// `mcpServers`. If `projectKey` is non-empty, it instead reads
// `projects.<projectKey>.mcpServers` (the per-project section in
// ~/.claude.json). Missing files / sections produce no error and no items.
func scanMCPFile(path, projectKey string, scope model.Scope) []model.Item {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}

	var servers map[string]any
	if projectKey == "" {
		s, ok := raw["mcpServers"].(map[string]any)
		if !ok {
			return nil
		}
		servers = s
	} else {
		projects, ok := raw["projects"].(map[string]any)
		if !ok {
			return nil
		}
		proj, ok := projects[projectKey].(map[string]any)
		if !ok {
			return nil
		}
		s, ok := proj["mcpServers"].(map[string]any)
		if !ok {
			return nil
		}
		servers = s
	}

	out := make([]model.Item, 0, len(servers))
	for name, entry := range servers {
		key := "mcpServers/" + name
		if projectKey != "" {
			key = "projects/" + projectKey + "/mcpServers/" + name
		}
		out = append(out, model.Item{
			Origin:      model.OriginClaude,
			Kind:        model.KindMCP,
			Scope:       scope,
			Name:        name,
			Path:        path,
			Description: mcpDescription(entry),
			RawJSON:     parse.MCPToJSON(entry),
			RawTOML:     parse.MCPToTOML(entry),
			Storage:     model.StorageEntry,
			ConfigKey:   key,
		})
	}
	return out
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
