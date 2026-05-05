package actions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

// Lossy projections (PRI-68) generate the target format from the
// canonical .md on demand instead of symlinking. Two cases today:
//
//   - md → codex profile entry: agent .md frontmatter+body collapses
//     into a `[profiles.<name>]` map under `~/.codex/config.toml` (or
//     <project>/.codex/config.toml). instructions = body, model =
//     frontmatter.model when present.
//
//   - md → gemini TOML file: prompt .md becomes a `<name>.toml` file
//     under .gemini/commands/ with `description` + multiline `prompt`
//     fields.
//
// "Lossy" because the round-trip drops most of the markdown frontmatter
// (Codex profiles only keep two fields; Gemini TOML keeps description +
// prompt). Resync canonical-wins regenerates from the library copy and
// silently overwrites any target-side edits — Place's UX should make
// this clear, see the picker's `(lossy)` marker.

// projectLossy materialises a generated target for one (Origin, Scope)
// cell. `sourcePath` is the canonical body path inside the library
// (e.g. `<lib>/agents/<name>/agent.md`). The function dispatches on
// (kind, target.Origin); unsupported combos return ErrPlaceUnsupported.
func projectLossy(it model.Item, sourcePath string, t ProjectionTarget, projectDir string) error {
	body, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read source %s: %w", sourcePath, err)
	}
	switch {
	case it.Kind == model.KindAgent && t.Origin == model.OriginCodex:
		return writeCodexProfile(body, it.Name, t.Scope, projectDir)
	case it.Kind == model.KindPrompt && t.Origin == model.OriginGemini:
		return writeGeminiCommandFile(body, it.Name, t.Scope, projectDir)
	}
	return fmt.Errorf("%w: no lossy projector for %s → %s", ErrPlaceUnsupported, it.Kind, t.Origin)
}

// unprojectLossy is the cleanup half: removes the generated target for
// one cell. For file-shaped generated projections (Gemini TOML) we
// os.Remove the file. For entry-shaped ones (Codex profile) we delete
// the entry from the surrounding config.toml. Idempotent: missing file
// or missing key is treated as already-gone.
func unprojectLossy(it model.Item, t ProjectionTarget, projectDir string) error {
	switch {
	case it.Kind == model.KindAgent && t.Origin == model.OriginCodex:
		path, key, err := codexProfilePath(it.Name, t.Scope, projectDir)
		if err != nil {
			return err
		}
		err = parse.DeleteEntry(path, key)
		if err != nil && !os.IsNotExist(err) && !isMissingKey(err) {
			return err
		}
		return nil
	case it.Kind == model.KindPrompt && t.Origin == model.OriginGemini:
		path, err := geminiCommandPath(it.Name, t.Scope, projectDir)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return fmt.Errorf("%w: no lossy unprojector for %s → %s", ErrPlaceUnsupported, it.Kind, t.Origin)
}

// hasLossyProjection reports whether the target cell currently holds a
// generated projection — used by CurrentPlaceProjections so the picker
// pre-checks the right cells. The check is presence-only (file exists
// or entry key resolves); content equality is a Resync concern, not a
// CurrentProjections one.
func hasLossyProjection(it model.Item, t ProjectionTarget, projectDir string) bool {
	switch {
	case it.Kind == model.KindAgent && t.Origin == model.OriginCodex:
		path, key, err := codexProfilePath(it.Name, t.Scope, projectDir)
		if err != nil {
			return false
		}
		_, _, err = parse.ReadEntry(path, key)
		return err == nil
	case it.Kind == model.KindPrompt && t.Origin == model.OriginGemini:
		path, err := geminiCommandPath(it.Name, t.Scope, projectDir)
		if err != nil {
			return false
		}
		_, err = os.Stat(path)
		return err == nil
	}
	return false
}

// renderCodexProfileEntry builds the `[profiles.<name>]` map that
// writeCodexProfile and the drift detector both produce from the
// canonical agent .md bytes. Pure: no filesystem touch. Frontmatter
// `model` (if present) carries through; everything else collapses
// into the `instructions` field.
func renderCodexProfileEntry(body []byte) map[string]any {
	fm := parse.Parse(string(body))
	entry := map[string]any{"instructions": fm.Body}
	if v := fm.Fields["model"]; v != "" {
		entry["model"] = v
	}
	return entry
}

// renderGeminiCommandFile produces the byte-for-byte content
// writeGeminiCommandFile would write for a given canonical prompt .md.
// Pure: no filesystem touch. Multi-line bodies use TOML literal-string
// syntax so embedded backticks / quotes / newlines round-trip
// unchanged.
func renderGeminiCommandFile(body []byte) []byte {
	fm := parse.Parse(string(body))
	desc := fm.Fields["description"]
	var b strings.Builder
	if desc != "" {
		fmt.Fprintf(&b, "description = %q\n", desc)
	}
	if strings.ContainsRune(fm.Body, '\n') {
		b.WriteString("prompt = '''\n")
		b.WriteString(fm.Body)
		if !strings.HasSuffix(fm.Body, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("'''\n")
	} else {
		fmt.Fprintf(&b, "prompt = %q\n", fm.Body)
	}
	return []byte(b.String())
}

// writeCodexProfile renders the canonical agent .md as a Codex profile
// entry inside the appropriate config.toml. Existing entries at the
// same key are replaced.
func writeCodexProfile(body []byte, name string, scope model.Scope, projectDir string) error {
	entry := renderCodexProfileEntry(body)
	path, key, err := codexProfilePath(name, scope, projectDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, format, err := parse.Read(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		data = map[string]any{}
		format = parse.FormatFromExt(path)
	}
	setNestedEntry(data, key, entry)
	return parse.Write(path, data, format)
}

// writeGeminiCommandFile renders the canonical prompt .md as a Gemini
// commands/<name>.toml.
func writeGeminiCommandFile(body []byte, name string, scope model.Scope, projectDir string) error {
	target, err := geminiCommandPath(name, scope, projectDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, renderGeminiCommandFile(body), 0o644)
}

// LossyProjectionDrift reports whether `it` is a lossy projection
// whose on-disk bytes no longer match what regenerating from the
// canonical .md in `canonicalDir` would produce. PRI-72.
//
// Only meaningful for items whose (Kind, Origin) pair maps to a lossy
// projector — for everything else returns false (no drift to report).
// Returns false on any read error: surfacing transient I/O hiccups as
// drift would be noise on a partially-installed library.
//
// Codex profile (KindAgent + Origin=Codex): the canonical entry value
// is built via renderCodexProfileEntry, then JSON-marshalled with
// sorted keys for stable byte equality against the parsed on-disk
// entry. Comparing parsed map[string]any directly is brittle because
// BurntSushi/toml decodes scalars as `interface{}` types whose Go
// equality depends on which numeric variant the decoder picked.
//
// Gemini TOML (KindPrompt + Origin=Gemini): straight byte-compare of
// the file against renderGeminiCommandFile output. Whitespace matters
// — a hand-edited file that round-trips through the same renderer
// won't drift, but stylistic edits (extra blank line, reordered
// keys) will. That is the intended semantics: any edit the user made
// at the projection is something Resync canonical-wins will overwrite.
func LossyProjectionDrift(it model.Item, canonicalDir, projectDir string) bool {
	if canonicalDir == "" {
		return false
	}
	if !isLossyProjection(it.Kind, it.Origin) {
		return false
	}
	bodyName, ok := store.CanonicalBodyName(it.Kind)
	if !ok {
		return false
	}
	body, err := os.ReadFile(filepath.Join(canonicalDir, bodyName))
	if err != nil {
		return false
	}
	switch {
	case it.Kind == model.KindAgent && it.Origin == model.OriginCodex:
		path, key, err := codexProfilePath(it.Name, it.Scope, projectDir)
		if err != nil {
			return false
		}
		actual, _, err := parse.ReadEntry(path, key)
		if err != nil {
			return false
		}
		want := renderCodexProfileEntry(body)
		return !entryDeepEqual(want, actual)
	case it.Kind == model.KindPrompt && it.Origin == model.OriginGemini:
		path, err := geminiCommandPath(it.Name, it.Scope, projectDir)
		if err != nil {
			return false
		}
		actual, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		return !bytes.Equal(actual, renderGeminiCommandFile(body))
	}
	return false
}

// entryDeepEqual compares two parsed config-entry values via canonical
// JSON. BurntSushi/toml's interface{} decoded values aren't directly
// comparable across re-parses (string vs []byte for some scalars,
// int64 vs int), so we marshal both sides with json.Marshal — which
// sorts map keys — and byte-compare the result.
func entryDeepEqual(a, b any) bool {
	aj, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aj, bj)
}

// codexProfilePath returns the (config.toml, "profiles/<name>") pair
// for a (scope, projectDir) combination. Mirrors the legacy
// crossCopyAgent routing so existing on-disk profiles stay reachable.
func codexProfilePath(name string, scope model.Scope, projectDir string) (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	if scope == model.ScopeLocal && projectDir == "" {
		return "", "", ErrNoProject
	}
	base := home
	if scope == model.ScopeLocal {
		base = projectDir
	}
	return filepath.Join(base, ".codex", "config.toml"), "profiles/" + name, nil
}

// geminiCommandPath returns the Gemini TOML command file for a
// (scope, projectDir) combination. Reuses toolRoot for consistency
// with the lossless prompt projection path.
func geminiCommandPath(name string, scope model.Scope, projectDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root, err := toolRoot(home, projectDir, model.OriginGemini, scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "commands", name+".toml"), nil
}
