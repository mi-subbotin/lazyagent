package actions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

// ConvertSkillToAgent rewrites a Skill's SKILL.md as an agent body
// under the lazyagent library. Phase A of PRI-74: mechanical, in-code,
// no LLM. Frontmatter carries `name`, `description` (when set on the
// source) and `model: inherit` (Claude subagent default — keeps the
// agent on the parent session's model). The body is copied verbatim;
// callers should re-place the new library agent through the standard
// `p` picker to project it onto a tool.
//
// The library entry is created at <library>/agents/<name>/agent.md
// alongside a fresh manifest.toml. We refuse to overwrite an existing
// agent dir — the caller is expected to handle the conflict via the
// usual pre-flight pattern (delete the old agent first, or pick a new
// name) rather than have us silently clobber bytes.
func ConvertSkillToAgent(it model.Item) (model.Item, error) {
	if it.Kind != model.KindSkill || it.Storage != model.StorageDir {
		return model.Item{}, fmt.Errorf("%w: convert needs a Skill dir, got %s/%v", ErrPlaceUnsupported, it.Kind, it.Storage)
	}

	skillDir := filepath.Dir(it.Path)
	srcPath := filepath.Join(skillDir, "SKILL.md")
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return model.Item{}, fmt.Errorf("read skill body: %w", err)
	}
	fm := parse.Parse(string(data))

	name := fm.Fields["name"]
	if name == "" {
		name = it.Name
	}
	desc := fm.Fields["description"]

	if err := store.Init(); err != nil {
		return model.Item{}, fmt.Errorf("init library: %w", err)
	}
	agentDir, err := store.ItemDir(model.KindAgent, name)
	if err != nil {
		return model.Item{}, err
	}
	if _, err := os.Stat(agentDir); err == nil {
		return model.Item{}, fmt.Errorf("agent %q already exists in library", name)
	}

	bodyName, ok := store.CanonicalBodyName(model.KindAgent)
	if !ok {
		return model.Item{}, ErrPlaceUnsupported
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return model.Item{}, fmt.Errorf("mkdir agent dir: %w", err)
	}

	out := buildAgentMarkdown(name, desc, fm.Body)
	bodyPath := filepath.Join(agentDir, bodyName)
	if err := os.WriteFile(bodyPath, []byte(out), 0o644); err != nil {
		return model.Item{}, fmt.Errorf("write agent body: %w", err)
	}

	manifest := store.Manifest{Name: name, Kind: model.KindAgent.String()}
	if err := store.WriteManifest(store.ManifestPath(agentDir), manifest); err != nil {
		return model.Item{}, fmt.Errorf("write agent manifest: %w", err)
	}

	return model.Item{
		Origin:      model.OriginShared,
		Kind:        model.KindAgent,
		Scope:       model.ScopeGlobal,
		Name:        name,
		Path:        bodyPath,
		Description: desc,
		Body:        out,
		Storage:     model.StorageFile,
	}, nil
}

// buildAgentMarkdown assembles the agent.md bytes. We hand-roll the
// frontmatter rather than reusing parse.Parse's structure: parse only
// returns Fields as a flat map, and we want a deterministic key order
// (name, description, model) for diff-friendly output. `description` is
// emitted quoted to survive any colons or special chars carried over
// from the skill (`description: "Use for X: Y"`); `model: inherit`
// follows Claude Code's subagent convention — agent inherits the
// parent session's model unless explicitly overridden.
func buildAgentMarkdown(name, desc, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	if desc != "" {
		fmt.Fprintf(&b, "description: %q\n", desc)
	}
	b.WriteString("model: inherit\n")
	b.WriteString("---\n")
	b.WriteString(body)
	if body != "" && !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}
