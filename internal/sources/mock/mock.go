package mock

import (
	"context"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// Source returns a hand-crafted set of Items so the TUI shell can be
// developed before real adapters exist.
type Source struct{}

func (Source) Name() string { return "mock" }

func (Source) List(_ context.Context, projectDir string) ([]model.Item, error) {
	items := []model.Item{
		{
			Origin:      model.OriginClaude,
			Kind:        model.KindSkill,
			Scope:       model.ScopeGlobal,
			Name:        "podcast-generation",
			Path:        "~/.claude/skills/podcast-generation/SKILL.md",
			Description: "Generate podcast scripts from articles",
			Body:        "# podcast-generation\n\nTurns long-form articles into a two-host podcast script.",
			Meta:        map[string]string{"allowed-tools": "Read, Write"},
		},
		{
			Origin:      model.OriginClaude,
			Kind:        model.KindAgent,
			Scope:       model.ScopeGlobal,
			Name:        "code-reviewer",
			Path:        "~/.claude/agents/code-reviewer.md",
			Description: "Independent second-opinion code review",
			Body:        "You are a senior reviewer. Focus on correctness and clarity.",
		},
		{
			Origin:      model.OriginClaude,
			Kind:        model.KindMCP,
			Scope:       model.ScopeGlobal,
			Name:        "linear",
			Path:        "~/.claude/settings.json",
			Description: "Linear MCP server",
			RawJSON:     "{\n  \"command\": \"npx\",\n  \"args\": [\"-y\", \"@linear/mcp\"]\n}",
			RawTOML:     "command = \"npx\"\nargs = [\"-y\", \"@linear/mcp\"]\n",
		},
		{
			Origin:      model.OriginCodex,
			Kind:        model.KindPrompt,
			Scope:       model.ScopeGlobal,
			Name:        "review-pr",
			Path:        "~/.codex/prompts/review-pr.md",
			Description: "Review a GitHub PR",
			Body:        "Review the PR at $1 and report blockers.",
		},
		{
			Origin:      model.OriginCodex,
			Kind:        model.KindMCP,
			Scope:       model.ScopeGlobal,
			Name:        "github",
			Path:        "~/.codex/config.toml",
			Description: "GitHub MCP server",
			RawTOML:     "command = \"docker\"\nargs = [\"run\", \"-i\", \"--rm\", \"ghcr.io/github/github-mcp-server\"]\n",
			RawJSON:     "{\n  \"command\": \"docker\",\n  \"args\": [\"run\", \"-i\", \"--rm\", \"ghcr.io/github/github-mcp-server\"]\n}",
		},
		{
			Origin:      model.OriginGemini,
			Kind:        model.KindPrompt,
			Scope:       model.ScopeGlobal,
			Name:        "summarize",
			Path:        "~/.gemini/commands/summarize.toml",
			Description: "Summarize a file",
			Body:        "Summarize the file at $1 in 5 bullets.",
		},
	}

	if projectDir != "" {
		items = append(items, model.Item{
			Origin:      model.OriginClaude,
			Kind:        model.KindAgent,
			Scope:       model.ScopeLocal,
			Name:        "project-helper",
			Path:        projectDir + "/.claude/agents/project-helper.md",
			Description: "Project-specific helper",
			Body:        "Project agent body.",
		})
	}

	return items, nil
}
