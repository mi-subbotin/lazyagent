package actions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mi-subbotin/lazyagent/internal/model"
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
// MCP entries are excluded (they live inside per-tool config files
// and have no library layout in v1). Same gate Share uses today.
func CanPlace(it model.Item) bool {
	return canShareKind(it.Kind, it.Storage)
}

// CanPlaceTo reports whether (kind, target.Origin) has a lossless
// library projection. Lossy combos (agent → codex, prompt → gemini)
// are rejected by Place; the TUI picker uses this to grey out cells.
func CanPlaceTo(k model.Kind, target model.Origin) bool {
	return canProjectTo(k, target)
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
