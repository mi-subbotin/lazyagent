package model

type Origin int

const (
	OriginClaude Origin = iota
	OriginCodex
	OriginGemini
	// OriginShared is the lazyagent canonical store at ~/.lazyagent/store.
	// Items here are projected (via symlink or copy) into the per-tool
	// directories so a single canonical file feeds Claude, Codex and
	// Gemini at once. See internal/store and PRI-2.
	OriginShared
)

func (o Origin) String() string {
	switch o {
	case OriginClaude:
		return "Claude"
	case OriginCodex:
		return "Codex"
	case OriginGemini:
		return "Gemini"
	case OriginShared:
		return "Shared"
	}
	return "?"
}

// ParseOrigin is the inverse of Origin.String — returns false for any
// label not produced by the String method. Used by the TUI to decode a
// group-node label back into a typed Origin.
func ParseOrigin(s string) (Origin, bool) {
	switch s {
	case "Claude":
		return OriginClaude, true
	case "Codex":
		return OriginCodex, true
	case "Gemini":
		return OriginGemini, true
	case "Shared":
		return OriginShared, true
	}
	return 0, false
}

type Kind int

const (
	KindSkill Kind = iota
	KindAgent
	KindMCP
	KindPrompt
	// KindMemory is the per-tool "memory file" / global instructions:
	// CLAUDE.md, AGENTS.md, GEMINI.md. Singular per scope — there is at
	// most one global and one project-local file per origin.
	KindMemory
	// KindSession is a recorded conversation transcript on disk. Claude
	// stores them as .jsonl under ~/.claude/projects/<encoded-cwd>/,
	// Gemini as .json under ~/.gemini/tmp/<hash>/chats/, Codex inside
	// state_5.sqlite. Sessions don't have global/local scope — they're
	// always tied to a project (cwd) — but we keep the Scope field set
	// to Global to match the rest of the model and group by project name
	// using the existing tree machinery (project name lives in Item.Meta
	// under "project").
	KindSession
)

func (k Kind) String() string {
	switch k {
	case KindSkill:
		return "Skills"
	case KindAgent:
		return "Agents"
	case KindMCP:
		return "MCP"
	case KindPrompt:
		return "Prompts"
	case KindMemory:
		return "Memory"
	case KindSession:
		return "Sessions"
	}
	return "?"
}

// ParseKind reverses Kind.String. Returns false for unknown labels.
func ParseKind(s string) (Kind, bool) {
	switch s {
	case "Skills":
		return KindSkill, true
	case "Agents":
		return KindAgent, true
	case "MCP":
		return KindMCP, true
	case "Prompts":
		return KindPrompt, true
	case "Memory":
		return KindMemory, true
	case "Sessions":
		return KindSession, true
	}
	return 0, false
}

type Scope int

const (
	ScopeGlobal Scope = iota
	ScopeLocal
)

func (s Scope) String() string {
	switch s {
	case ScopeGlobal:
		return "Global"
	case ScopeLocal:
		return "Local"
	}
	return "?"
}

// ParseScope reverses Scope.String. Returns false for unknown labels.
func ParseScope(s string) (Scope, bool) {
	switch s {
	case "Global":
		return ScopeGlobal, true
	case "Local":
		return ScopeLocal, true
	}
	return 0, false
}

// Storage describes how an item is persisted on disk. Adapters set this so
// the actions package knows whether to copy a single file, a whole
// directory, or merge an entry inside a shared config file.
type Storage int

const (
	StorageFile  Storage = iota // a single file at Item.Path
	StorageDir                  // the directory at filepath.Dir(Item.Path)
	StorageEntry                // an entry inside the config file at Item.Path; key is in ConfigKey
)

// Item is the unified representation of a skill / agent / MCP server / prompt
// from any of the supported tools.
type Item struct {
	Origin      Origin
	Kind        Kind
	Scope       Scope
	Name        string
	Path        string            // absolute path to the source file (or config file holding this entry)
	Description string            // single-line summary for list rendering
	Body        string            // markdown / preview content for the detail panel
	Meta        map[string]string // frontmatter / config fields

	// Storage tells the actions package how to copy / delete this item.
	Storage Storage
	// ConfigKey is the slash-separated path inside Path to the entry, used
	// only when Storage == StorageEntry (e.g. "mcpServers/linear" or
	// "projects/<absDir>/mcpServers/linear" for Claude per-project MCP).
	ConfigKey string

	// RawJSON / RawTOML hold serialized representations for entries that live
	// inside a config file (mainly MCP). At least one is set; the detail panel
	// can toggle between them. For file-backed items both stay empty.
	RawJSON string
	RawTOML string

	// Shared is true when this item's bytes ultimately live in the
	// lazyagent shared store. For Origin == Shared it's always true; for
	// per-tool origins it's set when the path resolves (after following
	// symlinks) into ~/.lazyagent/store. The TUI uses it to render a
	// "(s)" badge so users see at a glance that an item is canonical.
	Shared bool

	// Drift is true when a Shared projection's bytes diverge from the
	// canonical body in ~/.lazyagent/store. Only applies to copy-mode
	// projections (cloud-sync volumes); symlink projections are always
	// in sync by construction. The TUI renders "(drift)" so users can
	// resolve before edits accidentally compound the divergence.
	Drift bool

	// ParseError is non-empty when the item's frontmatter or backing
	// config could not be parsed cleanly. Adapters still surface the
	// item — the TUI renders a red "(invalid)" badge and shows the
	// raw bytes plus this message in the detail panel — so the user
	// can see what is wrong instead of the file silently disappearing.
	ParseError string
	// ValidationWarnings collects non-fatal issues with an otherwise
	// parseable item: missing recommended fields, suspicious values,
	// etc. The TUI renders a yellow "(?)" badge when this slice is
	// non-empty.
	ValidationWarnings []string

	// Private flags items the user almost never resumes manually:
	// sessions started in /tmp, inside tool config dirs (~/.claude,
	// ~/.codex, ~/.gemini, ~/.lazyagent), or under orchestrators that
	// spawn one cwd per task (claude-squad worktrees, conductor
	// workspaces). The tree renders these in a separate "Private"
	// subgroup that's collapsed by default so they don't dominate the
	// listing. Currently only KindSession sets this.
	Private bool
}
