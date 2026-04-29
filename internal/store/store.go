// Package store owns the lazyagent canonical store at
// `~/.lazyagent/store/`. The store is the source of truth for shared
// items (Skill/Agent/MCP/Prompt/Memory) that get projected into
// individual tool directories via symlink (preferred) or copy
// (fallback when the projection target lives under iCloud / Dropbox /
// OneDrive, where symlinks are unreliable).
//
// This file lays the foundation: filesystem layout helpers + manifest
// read/write. The projection / symlink manager and the lazyagent
// Source adapter live in separate files.
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// Root returns the on-disk root for the lazyagent shared store, with
// any leading symlinks resolved (e.g. macOS /var → /private/var) so
// every consumer agrees on a single canonical form — symlinks created
// by Share, comparisons in CanonicalItemDir, and reads via Readlink
// all match without ad-hoc canonicalisation at the call sites.
//
// Override via LAZYAGENT_STORE in the environment. When the directory
// doesn't exist yet (first Init / fresh install), EvalSymlinks fails
// and we fall back to filepath.Abs so callers can still mkdir against
// the path; the next call resolves cleanly once Init has run.
func Root() (string, error) {
	raw, err := rawRoot()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(raw); err == nil {
		return filepath.Abs(real)
	}
	return filepath.Abs(raw)
}

func rawRoot() (string, error) {
	if v := os.Getenv("LAZYAGENT_STORE"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lazyagent", "store"), nil
}

// KindDir returns the per-Kind subdirectory inside the store root for
// a given model.Kind. We pluralise to match the existing tool
// conventions (skills/, agents/, ...) so projections are 1:1.
func KindDir(k model.Kind) (string, bool) {
	switch k {
	case model.KindSkill:
		return "skills", true
	case model.KindAgent:
		return "agents", true
	case model.KindMCP:
		return "mcp", true
	case model.KindPrompt:
		return "prompts", true
	case model.KindMemory:
		return "memory", true
	}
	return "", false
}

// ItemDir returns `<root>/<kind>/<name>` — the directory that owns
// one shared item plus its manifest. Skills naturally live in their
// own dirs; for single-file Kinds (Agent / Prompt / Memory) we still
// use a directory so the manifest sits next to the body.
func ItemDir(k model.Kind, name string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	sub, ok := KindDir(k)
	if !ok {
		return "", fmt.Errorf("no store layout for kind %v", k)
	}
	return filepath.Join(root, sub, name), nil
}

// ManifestPath returns the manifest file path for an item directory.
// `<itemDir>/manifest.toml` — sibling of the item body (SKILL.md etc).
func ManifestPath(itemDir string) string {
	return filepath.Join(itemDir, "manifest.toml")
}

// Initialised reports whether the store has been set up at all (i.e.
// the root directory exists). Adapters use this as a fast no-op
// check: when false, the lazyagent Origin contributes zero items
// and the store is invisible to the rest of the TUI.
func Initialised() bool {
	root, err := Root()
	if err != nil {
		return false
	}
	info, err := os.Stat(root)
	return err == nil && info.IsDir()
}

// Init creates the store root and per-Kind subdirectories. Idempotent
// — re-running on a populated store leaves data untouched.
func Init() error {
	root, err := Root()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, k := range []model.Kind{
		model.KindSkill, model.KindAgent, model.KindMCP,
		model.KindPrompt, model.KindMemory,
	} {
		sub, _ := KindDir(k)
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Manifest is the shared-item descriptor co-located with each item in
// the store. Lives at `<itemDir>/manifest.toml`. Format choice: TOML
// (already a dependency, comment support, simple round-trip via
// BurntSushi/toml). The original PRI-2 plan called for YAML — TOML
// here avoids a new dep and reads about the same for this shape.
type Manifest struct {
	Name      string   `toml:"name"`
	Version   string   `toml:"version,omitempty"`
	Kind      string   `toml:"kind"` // Skill | Agent | MCP | Prompt | Memory
	SharedTo  []string `toml:"shared_to,omitempty"`
	SourceURL string   `toml:"source_url,omitempty"` // populated by PRI-3 github install
}

// ReadManifest parses the manifest at path. Returns fs.ErrNotExist
// when the file is missing — the adapter treats that as "this item
// isn't in the shared store" and skips it without raising.
func ReadManifest(path string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := toml.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// WriteManifest writes path atomically (tmp + rename). Parent dir is
// created as needed. The temp file lives in the same directory as
// path so the rename stays on one filesystem.
func WriteManifest(path string, m Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".manifest-*.toml")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := toml.NewEncoder(f).Encode(m); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ListItems walks the store and returns every Item directory along
// with its parsed Manifest, grouped per Kind. Items without a
// manifest (broken store state, partial migration) are skipped — the
// adapter logs a warning via PRI-17 once that lands.
func ListItems() (map[model.Kind][]ItemEntry, error) {
	out := map[model.Kind][]ItemEntry{}
	if !Initialised() {
		return out, nil
	}
	root, err := Root()
	if err != nil {
		return nil, err
	}
	for _, k := range []model.Kind{
		model.KindSkill, model.KindAgent, model.KindMCP,
		model.KindPrompt, model.KindMemory,
	} {
		sub, _ := KindDir(k)
		dir := filepath.Join(root, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			itemDir := filepath.Join(dir, e.Name())
			m, err := ReadManifest(ManifestPath(itemDir))
			if err != nil {
				// Skip silently for now; PRI-18 (validation) will
				// surface this as an `(invalid)` badge later.
				continue
			}
			out[k] = append(out[k], ItemEntry{
				Dir:      itemDir,
				Manifest: m,
			})
		}
	}
	return out, nil
}

// ItemEntry pairs an item's on-disk directory with its parsed
// manifest. Returned by ListItems for the lazyagent Source adapter.
type ItemEntry struct {
	Dir      string
	Manifest Manifest
}
