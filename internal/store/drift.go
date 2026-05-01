package store

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// CanonicalBodyName returns the filename inside `<root>/<kind>/<name>/`
// that holds the item's body, mirroring what `actions.Place` writes
// and what `sources/lazyagent` reads. Centralised here so the layout
// has a single owner — adapters, Place, and drift detection all agree.
//
// Returns ok=false for kinds whose store layout isn't defined (today
// just MCP, which is entry-shaped and doesn't fit the per-item-dir
// model).
func CanonicalBodyName(k model.Kind) (string, bool) {
	switch k {
	case model.KindSkill:
		return "SKILL.md", true
	case model.KindAgent:
		return "agent.md", true
	case model.KindPrompt:
		return "prompt.md", true
	case model.KindMemory:
		return "memory.md", true
	}
	return "", false
}

// IsDriftedAgainst compares a per-tool item's body against a known
// canonical directory in the store. Use this when the caller already
// has a (kind, name) → canonicalDir index — typical for the TUI's
// load post-pass — to avoid re-walking the store for every item.
//
// v1 only compares the body file. For directory-shaped kinds (skills)
// added/removed asset files inside the projection are not detected
// yet — left for a follow-up so this stays a cheap O(1) check.
// Returns false on any read error: surfacing unknowns as drift would
// be noise on a half-installed store.
func IsDriftedAgainst(it model.Item, canonicalDir string) bool {
	if canonicalDir == "" {
		return false
	}
	bodyName, ok := CanonicalBodyName(it.Kind)
	if !ok {
		return false
	}
	canonicalBody, err := os.ReadFile(filepath.Join(canonicalDir, bodyName))
	if err != nil {
		return false
	}
	return !bytes.Equal(canonicalBody, []byte(it.Body))
}

// IsDrifted is the convenience wrapper for callers without an index:
// resolves the canonical dir from the item's path (works for symlink
// projections) and delegates. For copy-mode projections — which look
// like ordinary files — the path resolution yields nothing; use
// IsDriftedAgainst with a name lookup instead.
func IsDrifted(it model.Item) bool {
	if !it.Shared {
		return false
	}
	if it.Origin == model.OriginShared {
		// Canonical reads itself; comparing would be a tautology.
		return false
	}
	return IsDriftedAgainst(it, CanonicalItemDir(it.Path))
}
