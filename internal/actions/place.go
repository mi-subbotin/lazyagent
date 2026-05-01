package actions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

// ProjectionTarget is one (Origin, Scope) cell in the Place picker
// matrix. Place's API takes a list of these instead of the single
// origin / single scope each of the legacy actions accept; the unified
// matrix is what makes copy / move / cross / share collapse into one
// operation.
type ProjectionTarget struct {
	Origin model.Origin
	Scope  model.Scope
}

// String renders a target as "Origin/Scope" — used in error messages
// and toasts. It is intentionally short; the picker UI labels are
// rendered separately.
func (t ProjectionTarget) String() string {
	return fmt.Sprintf("%s/%s", t.Origin, t.Scope)
}

// PlaceOpts modifies how Place runs. ProjectDir is required when any
// target has Scope=Local; passing it for global-only target sets is
// harmless (ignored). Overwrite is the same toggle the share confirm
// overlay flips when the user accepts ErrPlaceConflicts.
type PlaceOpts struct {
	Overwrite  bool
	ProjectDir string
}

// ErrPlaceUnsupported is returned when an item shape, kind, or
// (kind, target-origin) combination cannot be projected without
// format conversion that Place does not perform in v1. The legacy
// CrossCopy keeps handling lossy combos for now; Phase 3 will decide
// whether to fold them in or surface them as disabled cells.
var ErrPlaceUnsupported = errors.New("place not supported for this item shape")

// ErrPlaceConflicts mirrors ErrShareConflicts: at least one target
// path holds unrelated content. Caller queries PlaceConflicts for the
// list and re-calls Place with Overwrite=true after user confirmation.
var ErrPlaceConflicts = errors.New("place conflicts with existing items")

// Place is the unified item-placement action. It guarantees that the
// item's canonical bytes live in the library at
// <library>/<kind>/<name>/<body> and that each (Origin, Scope) target
// in `targets` is a healthy projection of those bytes. Projections
// that exist now but are not in `targets` get unprojected. An empty
// targets slice is valid: the item ends up in the library with zero
// projections (the "git stash" state).
//
// Place is additive in Phase 2 — Share / Reshare / Copy / Move /
// CrossCopy still exist alongside it. Phase 3 routes the TUI through
// Place and removes the legacy entry points.
func Place(it model.Item, targets []ProjectionTarget, opts PlaceOpts) error {
	if it.Storage == model.StorageEntry {
		return placeEntry(it, targets, opts)
	}
	if err := checkPlaceSupport(it, targets, opts); err != nil {
		return err
	}
	canonical := canonicalForItem(it)

	add, remove, err := planPlaceDiff(it, canonical, targets, opts.ProjectDir)
	if err != nil {
		return err
	}

	conflicts := placeConflicts(it, canonical, add, opts.ProjectDir)
	if len(conflicts) > 0 && !opts.Overwrite {
		return ErrPlaceConflicts
	}

	if canonical == "" {
		if err := store.Init(); err != nil {
			return fmt.Errorf("init library: %w", err)
		}
		canonical, err = promoteToLibrary(it)
		if err != nil {
			return err
		}
	}

	bodyName, ok := store.CanonicalBodyName(it.Kind)
	if !ok {
		return ErrPlaceUnsupported
	}
	source := canonical
	if it.Storage != model.StorageDir {
		source = filepath.Join(canonical, bodyName)
	}

	for _, c := range conflicts {
		if err := os.RemoveAll(c.Path); err != nil {
			return fmt.Errorf("clean conflict at %s: %w", c.Path, err)
		}
	}
	for _, t := range add {
		target, err := projectionPath(it.Kind, it.Name, t.Origin, t.Scope, opts.ProjectDir)
		if err != nil {
			return fmt.Errorf("projection path for %s: %w", t, err)
		}
		mode := store.PickLinkMode(target)
		if err := store.EnsureLink(source, target, mode); err != nil {
			return fmt.Errorf("project to %s: %w", t, err)
		}
	}
	for _, t := range remove {
		target, err := projectionPath(it.Kind, it.Name, t.Origin, t.Scope, opts.ProjectDir)
		if err != nil {
			return fmt.Errorf("projection path for %s: %w", t, err)
		}
		if err := store.RemoveLink(source, target); err != nil {
			return fmt.Errorf("unproject %s: %w", t, err)
		}
	}

	return writePlaceManifest(it, canonical, targets)
}

// PlaceConflicts pre-flights what Place would replace if called now.
// Returns one entry per target whose existing content is unrelated to
// our canonical (i.e. not a projection of the same library item).
// Same shape as ShareConflicts so the TUI overlay can render either
// list with shared rendering code.
func PlaceConflicts(it model.Item, targets []ProjectionTarget, opts PlaceOpts) ([]ShareConflict, error) {
	if it.Storage == model.StorageEntry {
		if err := checkPlaceEntrySupport(it, targets, opts); err != nil {
			return nil, err
		}
		current := currentEntryProjections(it, opts.ProjectDir)
		add := diffTargets(uniqTargets(targets), current)
		return entryConflicts(it, add, opts.ProjectDir), nil
	}
	if err := checkPlaceSupport(it, targets, opts); err != nil {
		return nil, err
	}
	canonical := canonicalForItem(it)
	add, _, err := planPlaceDiff(it, canonical, targets, opts.ProjectDir)
	if err != nil {
		return nil, err
	}
	return placeConflicts(it, canonical, add, opts.ProjectDir), nil
}

// CurrentPlaceProjections returns every (Origin, Scope) cell that
// currently resolves into the same canonical library item as `it`.
// Used by the TUI picker to pre-check the matrix and by Place itself
// to compute the add/remove diff. Membership is symlink-based and
// covers both global and local scopes; copy-mode projections fall
// back on the manifest's projected_to list (global only) like
// CurrentProjections does.
func CurrentPlaceProjections(it model.Item, projectDir string) []ProjectionTarget {
	if it.Storage == model.StorageEntry {
		return currentEntryProjections(it, projectDir)
	}
	canonical := canonicalForItem(it)
	if canonical == "" {
		return nil
	}
	scopes := []model.Scope{model.ScopeGlobal}
	if projectDir != "" {
		scopes = append(scopes, model.ScopeLocal)
	}
	var out []ProjectionTarget
	for _, t := range []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini} {
		if !canProjectTo(it.Kind, t) {
			continue
		}
		for _, s := range scopes {
			p, err := projectionPath(it.Kind, it.Name, t, s, projectDir)
			if err != nil {
				continue
			}
			if _, err := os.Lstat(p); err != nil {
				continue
			}
			if store.CanonicalItemDir(p) == canonical {
				out = append(out, ProjectionTarget{Origin: t, Scope: s})
			}
		}
	}
	return out
}

// CanPlace reports whether `it`'s shape is eligible for Place at all.
// File / dir kinds enter the library; entry-shaped kinds (MCP, Hooks,
// Codex agent profiles) skip the library and project tool-to-tool with
// the entry-aware path in placeEntry.
func CanPlace(it model.Item) bool {
	if it.Storage == model.StorageEntry {
		return canPlaceEntryKind(it.Kind)
	}
	return canShareKind(it.Kind, it.Storage)
}

// CanPlaceTo reports whether (kind, target.Origin) has a lossless
// library projection. Lossy combos (agent → codex, prompt → gemini)
// are rejected by Place; the TUI picker uses this to grey out cells.
// Entry-shaped items are same-tool only in v1, so this returns true
// only when target == source origin — enforced by the picker via
// CanPlaceEntryTo.
func CanPlaceTo(k model.Kind, target model.Origin) bool {
	return canProjectTo(k, target)
}

// CanPlaceEntryTo reports whether an entry-shaped item (it) can project
// to (target.Origin, target.Scope). v1 only supports same-Origin
// different-Scope: cross-tool MCP / profiles need format conversion
// that's tracked under PRI-68.
func CanPlaceEntryTo(it model.Item, target model.Origin) bool {
	return target == it.Origin
}

// canPlaceEntryKind enumerates the entry-shaped kinds Place understands
// in v1. Hooks have a different array-based key shape but go through
// copyHookEntry's logic; everything else (MCP, Codex profiles) shares
// the simple read-value-write-value path.
func canPlaceEntryKind(k model.Kind) bool {
	switch k {
	case model.KindMCP, model.KindAgent, model.KindHook:
		return true
	}
	return false
}

// --- internals -----------------------------------------------------

// checkPlaceSupport rejects unsupported item shapes and target combos
// before any disk work. Centralised so Place and PlaceConflicts share
// the validation surface.
func checkPlaceSupport(it model.Item, targets []ProjectionTarget, opts PlaceOpts) error {
	if !canShareKind(it.Kind, it.Storage) {
		return fmt.Errorf("%w: kind %s, storage %v", ErrPlaceUnsupported, it.Kind, it.Storage)
	}
	for _, t := range targets {
		if !canProjectTo(it.Kind, t.Origin) {
			return fmt.Errorf("%w: %s cannot host %s without conversion", ErrPlaceUnsupported, t.Origin, it.Kind)
		}
		if t.Scope == model.ScopeLocal && opts.ProjectDir == "" {
			return fmt.Errorf("%w: target %s needs ProjectDir", ErrNoProject, t)
		}
	}
	return nil
}

// planPlaceDiff returns the targets to add and remove relative to the
// item's current set of projections. For an item not yet in the
// library (canonical=="") the current set is empty, so add == targets.
// Targets with Scope=Local are tolerated even when the item has no
// existing local projection — they simply land in the add set.
func planPlaceDiff(it model.Item, canonical string, targets []ProjectionTarget, projectDir string) (add, remove []ProjectionTarget, err error) {
	want := uniqTargets(targets)
	var current []ProjectionTarget
	if canonical != "" {
		current = CurrentPlaceProjections(it, projectDir)
	}
	add = diffTargets(want, current)
	remove = diffTargets(current, want)
	return add, remove, nil
}

// placeConflicts walks the add set and reports targets whose existing
// on-disk content is unrelated to the canonical we'd project. Filters
// out targets that already point at the right canonical (idempotent
// re-place is a no-op, not a conflict) and the source path itself
// when it hasn't been promoted yet (otherwise first-time place would
// always flag its own origin).
func placeConflicts(it model.Item, canonical string, add []ProjectionTarget, projectDir string) []ShareConflict {
	var sourceAbs string
	if canonical == "" {
		var sourcePath string
		if it.Storage == model.StorageDir {
			sourcePath = filepath.Dir(it.Path)
		} else {
			sourcePath = it.Path
		}
		sourceAbs, _ = filepath.Abs(sourcePath)
	}

	var out []ShareConflict
	for _, t := range add {
		target, err := projectionPath(it.Kind, it.Name, t.Origin, t.Scope, projectDir)
		if err != nil {
			continue
		}
		info, err := os.Lstat(target)
		if err != nil {
			continue
		}
		if absT, _ := filepath.Abs(target); sourceAbs != "" && absT == sourceAbs {
			// First-time place: the source's own (Origin, Scope) cell
			// will be subsumed by promotion + re-projection. Don't
			// flag it as a conflict against itself.
			continue
		}
		if canonical != "" && store.CanonicalItemDir(target) == canonical {
			// Already a healthy projection of the same canonical —
			// the EnsureLink call will be a no-op.
			continue
		}
		kind := "file"
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			kind = "symlink"
		case info.IsDir():
			kind = "directory"
		}
		out = append(out, ShareConflict{Target: t.Origin, Path: target, Kind: kind})
	}
	return out
}

// promoteToLibrary moves the item's bytes into the library and
// returns the new canonical directory. The caller must have already
// checked that no conflicting library entry exists with the same
// (kind, name); we still re-check here defensively because the
// library is shared global state.
func promoteToLibrary(it model.Item) (string, error) {
	storeDir, err := store.ItemDir(it.Kind, it.Name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(storeDir); err == nil {
		return "", fmt.Errorf("%w: %s already in library", ErrTargetExists, it.Name)
	}
	bodyName, ok := store.CanonicalBodyName(it.Kind)
	if !ok {
		return "", ErrPlaceUnsupported
	}
	if err := os.MkdirAll(filepath.Dir(storeDir), 0o755); err != nil {
		return "", err
	}
	switch it.Storage {
	case model.StorageDir:
		srcDir := filepath.Dir(it.Path)
		if err := moveDir(srcDir, storeDir); err != nil {
			return "", err
		}
	case model.StorageFile:
		if err := os.MkdirAll(storeDir, 0o755); err != nil {
			return "", err
		}
		if err := moveFile(it.Path, filepath.Join(storeDir, bodyName)); err != nil {
			return "", err
		}
	default:
		return "", ErrPlaceUnsupported
	}
	return storeDir, nil
}

// writePlaceManifest writes the library manifest with the new
// projection set. We record only Origin in projected_to (matching the
// existing format); local projections are scanned from disk via
// CurrentPlaceProjections rather than persisted, so the manifest
// stays compatible with Resync's read path.
func writePlaceManifest(it model.Item, canonical string, targets []ProjectionTarget) error {
	manifestPath := store.ManifestPath(canonical)
	m, err := store.ReadManifest(manifestPath)
	if err != nil {
		m = store.Manifest{Name: it.Name, Kind: it.Kind.String()}
	}
	seen := map[model.Origin]bool{}
	m.ProjectedTo = m.ProjectedTo[:0]
	for _, t := range targets {
		if t.Scope != model.ScopeGlobal {
			continue
		}
		if seen[t.Origin] {
			continue
		}
		seen[t.Origin] = true
		m.ProjectedTo = append(m.ProjectedTo, t.Origin.String())
	}
	return store.WriteManifest(manifestPath, m)
}

// --- entry-shaped items --------------------------------------------
//
// MCP servers, Codex agent profiles, and hook entries live inside per-
// tool config files (.claude.json, .codex/config.toml, settings.json).
// They have no library layout in v1 — bytes can't be promoted to a
// canonical home and symlinked back, because what we'd need to "share"
// is one entry inside a file the tool also reads other settings from.
//
// Place's entry path keeps the source value identical and reprojects it
// to each (Origin, Scope) cell the user picks. v1 supports same-tool
// different-scope only — the legacy `c` / `m` parity. Cross-tool
// (claude.json ↔ config.toml ↔ settings.json) is feasible via
// crossCopyMCP-style routing but tracked separately under PRI-68.

// checkPlaceEntrySupport mirrors checkPlaceSupport but for the entry
// path: enforces same-Origin-only and ProjectDir presence for local
// targets before any disk work.
func checkPlaceEntrySupport(it model.Item, targets []ProjectionTarget, opts PlaceOpts) error {
	if !canPlaceEntryKind(it.Kind) {
		return fmt.Errorf("%w: entry kind %s", ErrPlaceUnsupported, it.Kind)
	}
	for _, t := range targets {
		if t.Origin != it.Origin {
			return fmt.Errorf("%w: cross-tool entry projection (%s → %s) needs format conversion", ErrPlaceUnsupported, it.Origin, t.Origin)
		}
		if t.Scope == model.ScopeLocal && opts.ProjectDir == "" {
			return fmt.Errorf("%w: target %s needs ProjectDir", ErrNoProject, t)
		}
	}
	return nil
}

// placeEntry implements Place for StorageEntry items. The flow mirrors
// the file path: compute add/remove diff, surface conflicts, then
// write/delete entries to bring the on-disk state in line with
// `targets`. Hooks dispatch to the array-based copyHookEntry helper.
func placeEntry(it model.Item, targets []ProjectionTarget, opts PlaceOpts) error {
	if err := checkPlaceEntrySupport(it, targets, opts); err != nil {
		return err
	}
	want := uniqTargets(targets)
	current := currentEntryProjections(it, opts.ProjectDir)
	add := diffTargets(want, current)
	remove := diffTargets(current, want)

	conflicts := entryConflicts(it, add, opts.ProjectDir)
	if len(conflicts) > 0 && !opts.Overwrite {
		return ErrPlaceConflicts
	}

	for _, t := range add {
		if err := writeEntryProjection(it, t, opts.ProjectDir); err != nil {
			return fmt.Errorf("project %s: %w", t, err)
		}
	}
	for _, t := range remove {
		if err := deleteEntryProjection(it, t, opts.ProjectDir); err != nil {
			return fmt.Errorf("unproject %s: %w", t, err)
		}
	}
	return nil
}

// currentEntryProjections walks the same-Origin (Global, Local) cells
// and reports the ones that currently host this entry. For MCP /
// profile entries the test is keyPath presence in the target config.
// For hooks (which are array-appended and have no stable identity in
// the target file) we report only the source's own cell — anything
// else has to go through copyHookEntry's append-style write, which is
// always treated as a new projection.
func currentEntryProjections(it model.Item, projectDir string) []ProjectionTarget {
	if it.Kind == model.KindHook {
		return []ProjectionTarget{{Origin: it.Origin, Scope: it.Scope}}
	}
	scopes := []model.Scope{model.ScopeGlobal}
	if projectDir != "" {
		scopes = append(scopes, model.ScopeLocal)
	}
	var out []ProjectionTarget
	for _, s := range scopes {
		path, key, err := entryTargetPath(it, ProjectionTarget{Origin: it.Origin, Scope: s}, projectDir)
		if err != nil {
			continue
		}
		if _, _, err := parse.ReadEntry(path, key); err == nil {
			out = append(out, ProjectionTarget{Origin: it.Origin, Scope: s})
		}
	}
	return out
}

// entryConflicts reports add-set targets whose target config already
// holds something at the same key (different from a no-op rewrite).
// Hooks always append a new matcher group rather than replacing one in
// place, so we don't flag them as conflicts — overwrite=false will
// still produce duplicate entries on apply, but that mirrors the
// old `c` semantics that Phase B is restoring.
func entryConflicts(it model.Item, add []ProjectionTarget, projectDir string) []ShareConflict {
	if it.Kind == model.KindHook {
		return nil
	}
	var out []ShareConflict
	for _, t := range add {
		path, key, err := entryTargetPath(it, t, projectDir)
		if err != nil {
			continue
		}
		if path == it.Path && key == it.ConfigKey {
			continue
		}
		_, _, err = parse.ReadEntry(path, key)
		if err != nil {
			continue
		}
		out = append(out, ShareConflict{Target: t.Origin, Path: path + " :: " + key, Kind: "entry"})
	}
	return out
}

// writeEntryProjection adds the entry to a target (Origin, Scope) cell.
// The target file is created if missing; existing entries are
// overwritten because the placeEntry caller has already cleared
// conflicts (or the user opted in via Overwrite).
//
// We don't use parse.WriteEntry directly because parse.Set silently
// no-ops when a multi-segment keyPath traverses missing intermediate
// maps (e.g. writing "mcpServers/fs" into an empty `{}` document).
// setNestedEntry creates the intermediates so a fresh target file
// ends up with the entry actually present.
func writeEntryProjection(it model.Item, t ProjectionTarget, projectDir string) error {
	if it.Kind == model.KindHook {
		return placeHookProjection(it, t, projectDir)
	}
	path, key, err := entryTargetPath(it, t, projectDir)
	if err != nil {
		return err
	}
	if path == it.Path && key == it.ConfigKey {
		return nil
	}
	val, _, err := parse.ReadEntry(it.Path, it.ConfigKey)
	if err != nil {
		return fmt.Errorf("read source %s/%s: %w", it.Path, it.ConfigKey, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, format, err := parse.Read(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		data = map[string]any{}
		format = parse.FormatFromExt(path)
	}
	setNestedEntry(data, key, val)
	return parse.Write(path, data, format)
}

// setNestedEntry assigns value at keyPath inside m, creating any
// intermediate maps that are missing along the way. parse.Set requires
// the parent path to already exist; this wrapper is the fewer-bytes
// equivalent of parse.Append for a single-value set.
func setNestedEntry(m map[string]any, keyPath string, value any) {
	parts := parse.SplitKey(keyPath)
	if len(parts) == 0 {
		return
	}
	cur := m
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = value
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}

// deleteEntryProjection removes the entry from a target cell. Idempotent
// against missing files / missing keys so an "unproject" of something
// that's already gone is a no-op rather than an error.
func deleteEntryProjection(it model.Item, t ProjectionTarget, projectDir string) error {
	path, key, err := entryTargetPath(it, t, projectDir)
	if err != nil {
		return err
	}
	if path == it.Path && key == it.ConfigKey {
		// The source is being unprojected: this is the move semantic.
		// parse.DeleteEntry handles missing-key gracefully.
		err := parse.DeleteEntry(path, key)
		if errors.Is(err, fs.ErrNotExist) || isMissingKey(err) {
			return nil
		}
		return err
	}
	err = parse.DeleteEntry(path, key)
	if errors.Is(err, fs.ErrNotExist) || isMissingKey(err) {
		return nil
	}
	return err
}

// entryTargetPath returns the (file, key) the entry should land at for
// a given (Origin, Scope) cell. v1 enforces target.Origin == it.Origin
// upstream so we only need same-tool routing here.
func entryTargetPath(it model.Item, t ProjectionTarget, projectDir string) (string, string, error) {
	if t.Origin != it.Origin {
		return "", "", fmt.Errorf("%w: cross-tool entry routing not supported", ErrPlaceUnsupported)
	}
	if t.Scope == it.Scope {
		return it.Path, it.ConfigKey, nil
	}
	if it.Kind == model.KindHook {
		path, err := remapHookTargetPath(it, projectDir)
		return path, it.ConfigKey, err
	}
	return remapEntryToOtherScope(it, projectDir)
}

// placeHookProjection wraps copyHookEntry for use as a Place projection
// step. copyHookEntry already handles the array-append + matcher
// preservation; we only need to pick the destination path for the
// requested (Origin, Scope) target rather than "the other scope".
func placeHookProjection(it model.Item, t ProjectionTarget, projectDir string) error {
	if t.Scope == it.Scope {
		return nil // source cell — no-op.
	}
	return copyHookEntry(it, projectDir)
}

// isMissingKey detects parse package's "key not found" error string.
// parse doesn't export a sentinel for it, so we fall back to a string
// match — narrow scope, surrounding code is the only caller.
func isMissingKey(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not found")
}

// uniqTargets removes duplicate (Origin, Scope) pairs while preserving
// order, so callers can pass a list with accidental dupes (e.g. from
// repeated user clicks in the picker) without distorting the diff.
func uniqTargets(in []ProjectionTarget) []ProjectionTarget {
	seen := map[ProjectionTarget]bool{}
	out := make([]ProjectionTarget, 0, len(in))
	for _, t := range in {
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// diffTargets returns targets in a not present in b. Sized for the
// 3-origin × 2-scope matrix; quadratic comparison is fine.
func diffTargets(a, b []ProjectionTarget) []ProjectionTarget {
	bset := map[ProjectionTarget]bool{}
	for _, t := range b {
		bset[t] = true
	}
	var out []ProjectionTarget
	for _, t := range a {
		if !bset[t] {
			out = append(out, t)
		}
	}
	return out
}
