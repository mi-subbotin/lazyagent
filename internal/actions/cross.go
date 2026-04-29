package actions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

// CrossCopy duplicates an item into a different tool. Combinations that
// have no sensible mapping (e.g. Codex has no skills concept) return
// ErrUnsupported. Lossy combinations (where conversion drops fields the
// destination format can't represent) still succeed; the UI is expected
// to warn the user beforehand via IsLossyCross.
//
// Refuses to overwrite an existing target — same policy as same-tool Copy.
func CrossCopy(it model.Item, target model.Origin, targetScope model.Scope, projectDir string) error {
	if target == it.Origin {
		return errors.New("cross-copy needs a different target tool")
	}
	if targetScope == model.ScopeLocal && projectDir == "" {
		return ErrNoProject
	}

	switch it.Kind {
	case model.KindSkill:
		return crossCopySkill(it, target, targetScope, projectDir)
	case model.KindMCP:
		return crossCopyMCP(it, target, targetScope, projectDir)
	case model.KindAgent:
		return crossCopyAgent(it, target, targetScope, projectDir)
	case model.KindPrompt:
		return crossCopyPrompt(it, target, targetScope, projectDir)
	case model.KindMemory:
		return crossCopyMemory(it, target, targetScope, projectDir)
	}
	return ErrUnsupported
}

// SupportsCross reports whether the (source, target) origin pair has a
// defined mapping for the item's kind. The UI uses this to grey out
// invalid options in the cross-copy picker.
func SupportsCross(it model.Item, target model.Origin) bool {
	if target == it.Origin {
		return false
	}
	switch it.Kind {
	case model.KindSkill:
		// All three tools support SKILL.md; the only difference is the
		// directory each one watches (~/.claude/skills, ~/.agents/skills,
		// ~/.gemini/skills).
		return true
	case model.KindMCP:
		return true
	case model.KindAgent:
		// All three tools have named subagents now (Gemini per
		// https://geminicli.com/docs/core/subagents/, Codex via
		// [profiles.<name>] in config.toml, Claude under .claude/agents).
		return true
	case model.KindPrompt:
		return true
	case model.KindMemory:
		// CLAUDE.md / AGENTS.md / GEMINI.md are all plain markdown blobs
		// with the same purpose; copying between them is just a rename
		// + relocate.
		return true
	}
	return false
}

// IsLossyCross reports whether the conversion from it to target drops
// information the destination format can't carry. Used to warn the user
// in the picker.
func IsLossyCross(it model.Item, target model.Origin) bool {
	if !SupportsCross(it, target) {
		return false
	}
	switch it.Kind {
	case model.KindAgent:
		// .md ↔ .md (Claude ↔ Gemini) is a verbatim file copy — no
		// fields are dropped. Anything that crosses Codex's
		// [profiles.<name>] entry shape is lossy because the
		// frontmatter collapses into instructions + model only.
		return it.Origin == model.OriginCodex || target == model.OriginCodex
	case model.KindPrompt:
		// .md → .toml or .toml → .md transitions reshape frontmatter.
		// .md → .md (Claude ↔ Codex) is structurally lossless but the
		// frontmatter conventions still differ; flag as lossy if either
		// side touches Gemini.
		return it.Origin == model.OriginGemini || target == model.OriginGemini
	}
	return false
}

// --- Skill: directory copy across roots ---------------------------------

func crossCopySkill(it model.Item, target model.Origin, scope model.Scope, projectDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root, err := skillRoot(home, projectDir, target, scope)
	if err != nil {
		return err
	}
	targetDir := filepath.Join(root, it.Name)
	if _, err := os.Stat(targetDir); err == nil {
		return fmt.Errorf("%w: %s", ErrTargetExists, targetDir)
	}
	srcDir := filepath.Dir(it.Path)
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return err
	}
	return copyDir(srcDir, targetDir)
}

// skillRoot returns the directory that holds <name>/SKILL.md for a given
// origin and scope. Codex skills live under .agents/skills/ — a sibling
// of .codex/, not a subdirectory — so its layout differs from Claude
// (~/.claude/skills) and Gemini (~/.gemini/skills).
func skillRoot(home, projectDir string, origin model.Origin, scope model.Scope) (string, error) {
	if scope == model.ScopeLocal && projectDir == "" {
		return "", ErrNoProject
	}
	base := home
	if scope == model.ScopeLocal {
		base = projectDir
	}
	switch origin {
	case model.OriginClaude:
		return filepath.Join(base, ".claude", "skills"), nil
	case model.OriginCodex:
		return filepath.Join(base, ".agents", "skills"), nil
	case model.OriginGemini:
		return filepath.Join(base, ".gemini", "skills"), nil
	}
	return "", fmt.Errorf("unknown origin %v", origin)
}

// --- MCP: entry copy with format change (JSON ↔ TOML) -------------------

func crossCopyMCP(it model.Item, target model.Origin, scope model.Scope, projectDir string) error {
	val, _, err := parse.ReadEntry(it.Path, it.ConfigKey)
	if err != nil {
		return fmt.Errorf("source MCP %q: %w", it.Name, err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	targetPath, targetKey, err := mcpTargetForOrigin(target, scope, home, projectDir, it.Name)
	if err != nil {
		return err
	}

	if _, _, err := parse.ReadEntry(targetPath, targetKey); err == nil {
		return fmt.Errorf("%w: %s in %s", ErrTargetExists, targetKey, targetPath)
	} else if !errors.Is(err, fs.ErrNotExist) && !strings.Contains(err.Error(), "not found") {
		return err
	}
	return parse.WriteEntry(targetPath, targetKey, val)
}

func mcpTargetForOrigin(target model.Origin, scope model.Scope, home, projectDir, name string) (string, string, error) {
	switch target {
	case model.OriginClaude:
		if scope == model.ScopeGlobal {
			return filepath.Join(home, ".claude.json"), "mcpServers/" + name, nil
		}
		return filepath.Join(projectDir, ".mcp.json"), "mcpServers/" + name, nil
	case model.OriginCodex:
		if scope == model.ScopeGlobal {
			return filepath.Join(home, ".codex", "config.toml"), "mcp_servers/" + name, nil
		}
		return filepath.Join(projectDir, ".codex", "config.toml"), "mcp_servers/" + name, nil
	case model.OriginGemini:
		if scope == model.ScopeGlobal {
			return filepath.Join(home, ".gemini", "settings.json"), "mcpServers/" + name, nil
		}
		return filepath.Join(projectDir, ".gemini", "settings.json"), "mcpServers/" + name, nil
	}
	return "", "", fmt.Errorf("unknown target origin %v", target)
}

// --- Agent: subagents across the three tools -----------------------------
//
// File-shaped subagents (Claude .md, Gemini .md) and entry-shaped ones
// (Codex [profiles.<name>] in config.toml) need different conversions:
//
//   md  ↔ md   : same shape, frontmatter conventions only differ at the
//                edges; we copy the file verbatim and let the destination
//                tool ignore unknown frontmatter fields.
//   md  → entry: parse frontmatter+body, store body as `instructions` and
//                copy `model` if present. Other frontmatter is dropped.
//   entry → md : synthesize a minimal frontmatter (name, model) and use
//                the entry's `instructions` as body.

func crossCopyAgent(it model.Item, target model.Origin, scope model.Scope, projectDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	srcIsMD := it.Origin != model.OriginCodex
	tgtIsMD := target != model.OriginCodex

	switch {
	case srcIsMD && tgtIsMD:
		// Claude ↔ Gemini: direct file copy.
		root, err := toolRoot(home, projectDir, target, scope)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(root, "agents", it.Name+".md")
		if _, err := os.Stat(targetPath); err == nil {
			return fmt.Errorf("%w: %s", ErrTargetExists, targetPath)
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		return copyFile(it.Path, targetPath)

	case srcIsMD && !tgtIsMD:
		// Claude / Gemini → Codex profile.
		data, err := os.ReadFile(it.Path)
		if err != nil {
			return err
		}
		fm := parse.Parse(string(data))
		entry := map[string]any{"instructions": fm.Body}
		if v, ok := fm.Fields["model"]; ok && v != "" {
			entry["model"] = v
		}

		targetPath := codexConfigPath(home, projectDir, scope)
		key := "profiles/" + it.Name
		if _, _, err := parse.ReadEntry(targetPath, key); err == nil {
			return fmt.Errorf("%w: %s in %s", ErrTargetExists, key, targetPath)
		} else if !errors.Is(err, fs.ErrNotExist) && !strings.Contains(err.Error(), "not found") {
			return err
		}
		return parse.WriteEntry(targetPath, key, entry)

	case !srcIsMD && tgtIsMD:
		// Codex profile → Claude / Gemini.
		val, _, err := parse.ReadEntry(it.Path, it.ConfigKey)
		if err != nil {
			return fmt.Errorf("source profile %q: %w", it.Name, err)
		}
		entry, _ := val.(map[string]any)
		instructions, _ := entry["instructions"].(string)
		modelField, _ := entry["model"].(string)

		root, err := toolRoot(home, projectDir, target, scope)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(root, "agents", it.Name+".md")
		if _, err := os.Stat(targetPath); err == nil {
			return fmt.Errorf("%w: %s", ErrTargetExists, targetPath)
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}

		var b strings.Builder
		b.WriteString("---\n")
		b.WriteString("name: " + it.Name + "\n")
		b.WriteString("description: \"migrated from Codex profile\"\n")
		if modelField != "" {
			b.WriteString("model: " + modelField + "\n")
		}
		b.WriteString("---\n")
		b.WriteString(instructions)
		return os.WriteFile(targetPath, []byte(b.String()), 0o644)
	}
	return ErrUnsupported
}

// --- Prompt: any-to-any markdown / TOML conversion ----------------------

func crossCopyPrompt(it model.Item, target model.Origin, scope model.Scope, projectDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	desc, body, err := readPromptContent(it)
	if err != nil {
		return err
	}

	targetPath, err := promptTargetPath(target, scope, home, projectDir, it.Name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("%w: %s", ErrTargetExists, targetPath)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}

	if target == model.OriginGemini {
		return writeGeminiCommand(targetPath, desc, body)
	}
	return writeMarkdownPrompt(targetPath, it.Name, desc, body)
}

// readPromptContent extracts (description, body) from a prompt-shaped
// item regardless of source format.
func readPromptContent(it model.Item) (string, string, error) {
	data, err := os.ReadFile(it.Path)
	if err != nil {
		return "", "", err
	}
	if it.Origin == model.OriginGemini {
		var raw map[string]any
		if err := toml.Unmarshal(data, &raw); err != nil {
			return "", "", err
		}
		desc, _ := raw["description"].(string)
		body, _ := raw["prompt"].(string)
		return desc, body, nil
	}
	fm := parse.Parse(string(data))
	return fm.Fields["description"], fm.Body, nil
}

func promptTargetPath(target model.Origin, scope model.Scope, home, projectDir, name string) (string, error) {
	switch target {
	case model.OriginClaude:
		root, err := toolRoot(home, projectDir, target, scope)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "commands", name+".md"), nil
	case model.OriginCodex:
		// Codex prompts live under .codex/prompts/, not under root directly.
		if scope == model.ScopeGlobal {
			return filepath.Join(home, ".codex", "prompts", name+".md"), nil
		}
		return filepath.Join(projectDir, ".codex", "prompts", name+".md"), nil
	case model.OriginGemini:
		root, err := toolRoot(home, projectDir, target, scope)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "commands", name+".toml"), nil
	}
	return "", fmt.Errorf("unknown target origin %v", target)
}

func writeMarkdownPrompt(path, name, desc, body string) error {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + name + "\n")
	if desc != "" {
		b.WriteString(fmt.Sprintf("description: %q\n", desc))
	}
	b.WriteString("---\n")
	b.WriteString(body)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeGeminiCommand(path, desc, body string) error {
	var b strings.Builder
	if desc != "" {
		b.WriteString(fmt.Sprintf("description = %q\n", desc))
	}
	// Use TOML's multi-line literal-string form for the prompt body so
	// embedded backticks and quotes survive without escaping. Falls back
	// to a basic-string when the body has no newlines.
	if strings.ContainsRune(body, '\n') {
		b.WriteString("prompt = '''\n")
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("'''\n")
	} else {
		b.WriteString(fmt.Sprintf("prompt = %q\n", body))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// --- Memory: rename + relocate the global/project instructions file ----

func crossCopyMemory(it model.Item, target model.Origin, scope model.Scope, projectDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	targetPath, err := memoryTargetPath(target, scope, home, projectDir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("%w: %s", ErrTargetExists, targetPath)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	return copyFile(it.Path, targetPath)
}

// memoryTargetPath returns the canonical memory-file location for a
// (target, scope) pair. Each tool uses a different filename (CLAUDE.md /
// AGENTS.md / GEMINI.md) and a slightly different relative path.
func memoryTargetPath(target model.Origin, scope model.Scope, home, projectDir string) (string, error) {
	if scope == model.ScopeLocal && projectDir == "" {
		return "", ErrNoProject
	}
	switch target {
	case model.OriginClaude:
		if scope == model.ScopeGlobal {
			return filepath.Join(home, ".claude", "CLAUDE.md"), nil
		}
		return filepath.Join(projectDir, "CLAUDE.md"), nil
	case model.OriginCodex:
		if scope == model.ScopeGlobal {
			return filepath.Join(home, ".codex", "AGENTS.md"), nil
		}
		return filepath.Join(projectDir, "AGENTS.md"), nil
	case model.OriginGemini:
		if scope == model.ScopeGlobal {
			return filepath.Join(home, ".gemini", "GEMINI.md"), nil
		}
		return filepath.Join(projectDir, "GEMINI.md"), nil
	}
	return "", fmt.Errorf("unknown target origin %v", target)
}

// --- Helpers shared across cross-tool funcs ------------------------------

func toolRoot(home, projectDir string, origin model.Origin, scope model.Scope) (string, error) {
	if scope == model.ScopeLocal && projectDir == "" {
		return "", ErrNoProject
	}
	var sub string
	switch origin {
	case model.OriginClaude:
		sub = ".claude"
	case model.OriginCodex:
		sub = ".codex"
	case model.OriginGemini:
		sub = ".gemini"
	default:
		return "", fmt.Errorf("unknown origin %v", origin)
	}
	if scope == model.ScopeGlobal {
		return filepath.Join(home, sub), nil
	}
	return filepath.Join(projectDir, sub), nil
}

func codexConfigPath(home, projectDir string, scope model.Scope) string {
	if scope == model.ScopeGlobal {
		return filepath.Join(home, ".codex", "config.toml")
	}
	return filepath.Join(projectDir, ".codex", "config.toml")
}

