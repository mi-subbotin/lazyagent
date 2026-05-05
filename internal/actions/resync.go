package actions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

// ResyncDirection picks who wins when a Shared projection has
// drifted from canonical. The TUI overlay maps `c` → CanonicalWins
// and `t` → ToolWins.
type ResyncDirection int

const (
	// ResyncCanonicalWins overwrites every projection (including the
	// drifted tool the user pressed `R` on) with bytes from the
	// canonical store. The store body is not touched.
	ResyncCanonicalWins ResyncDirection = iota

	// ResyncToolWins copies the bytes the tool currently holds into
	// the canonical body, then re-projects to every other tool
	// currently in the projection set. The drifted edits become the
	// new canonical version.
	ResyncToolWins
)

// ErrNotDrifted is returned when Resync is called on an item the
// detector says is in sync. Defensive — keeps the action idempotent
// and saves a wasted ReadAll/Write cycle when the TUI fires Resync
// after a stale tree state.
var ErrNotDrifted = errors.New("item is not drifted")

// Resync forces the projections of a shared item back into agreement.
// Direction picks the winner: canonical bytes win (overwrite the
// drifted tool copies with the store version) or tool bytes win
// (promote the drifted copy to canonical and reproject to peers).
//
// For ResyncToolWins the source tool's path must be readable as
// the new canonical body — if the user pressed R on a Shared-origin
// row it.Path is already the canonical body, which makes ToolWins a
// no-op except for the reprojection step.
func Resync(it model.Item, dir ResyncDirection) error {
	if !it.Shared {
		return fmt.Errorf("%w: %s", ErrPlaceUnsupported, "not a shared item")
	}
	canonical := canonicalForItem(it)
	if canonical == "" {
		return fmt.Errorf("%w: %s does not resolve into the shared store", ErrPlaceUnsupported, it.Path)
	}
	bodyName, ok := store.CanonicalBodyName(it.Kind)
	if !ok {
		return ErrPlaceUnsupported
	}
	canonicalBody := filepath.Join(canonical, bodyName)

	switch dir {
	case ResyncCanonicalWins:
		return resyncCanonicalWins(it, canonical, canonicalBody)
	case ResyncToolWins:
		return resyncToolWins(it, canonical, canonicalBody)
	}
	return fmt.Errorf("unknown resync direction %d", dir)
}

// canonicalForItem locates the store directory backing it, trying
// path-based resolution first (cheap, works for symlinks) and falling
// back to a name lookup (needed for copy-mode projections that look
// like ordinary files on disk).
func canonicalForItem(it model.Item) string {
	if dir := store.CanonicalItemDir(it.Path); dir != "" {
		return dir
	}
	groups, err := store.ListItems()
	if err != nil {
		return ""
	}
	for _, e := range groups[it.Kind] {
		if e.Manifest.Name == it.Name {
			return e.Dir
		}
	}
	return ""
}

// resyncCanonicalWins walks every per-tool projection path and brings
// it back into agreement with canonical. Healthy symlinks pointing at
// the store stay untouched; drifted copies and stale symlinks get
// cleanly replaced. Targets that don't exist on disk are left alone —
// the user explicitly removed those projections via reshare and
// resync shouldn't resurrect them.
//
// Lossy projections (PRI-72) are regenerated via projectLossy when
// the on-disk projection differs from what the renderer would produce
// today; presence-only projections that still match are left alone so
// we don't rewrite TOML files that haven't actually drifted.
func resyncCanonicalWins(it model.Item, canonical, canonicalBody string) error {
	source := canonical
	if it.Storage != model.StorageDir {
		source = canonicalBody
	}
	for _, t := range []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini} {
		if isLossyProjection(it.Kind, t) {
			pt := ProjectionTarget{Origin: t, Scope: model.ScopeGlobal}
			lossyIt := model.Item{Kind: it.Kind, Origin: t, Scope: model.ScopeGlobal, Name: it.Name}
			if !hasLossyProjection(lossyIt, pt, "") {
				continue
			}
			if !LossyProjectionDrift(lossyIt, canonical, "") {
				continue
			}
			if err := projectLossy(lossyIt, canonicalBody, pt, ""); err != nil {
				return fmt.Errorf("regenerate lossy %s: %w", t, err)
			}
			continue
		}
		target, err := projectionPath(it.Kind, it.Name, t, model.ScopeGlobal, "")
		if err != nil {
			continue
		}
		info, err := os.Lstat(target)
		if err != nil {
			continue
		}
		// Healthy symlink to the canonical we own → nothing to do.
		// Compare via CanonicalItemDir so /var/X and /private/var/X
		// (the macOS symlink pair) collapse to the same resolved form.
		if info.Mode()&os.ModeSymlink != 0 && store.CanonicalItemDir(target) == canonical {
			continue
		}
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("clean drifted %s: %w", target, err)
		}
		mode := store.PickLinkMode(target)
		if err := store.EnsureLink(source, target, mode); err != nil {
			return fmt.Errorf("reproject to %s: %w", t, err)
		}
	}
	return nil
}

// resyncToolWins promotes the bytes at it.Path to canonical and
// reprojects to all current targets. Because reading it.Path follows
// symlinks, calling this on an in-sync projection is a safe no-op:
// the bytes copied back equal the canonical we already have.
func resyncToolWins(it model.Item, canonical, canonicalBody string) error {
	if it.Origin == model.OriginShared {
		// Already canonical — Tool wins is a contradiction. Treat as
		// CanonicalWins so the user gets a useful action either way.
		return resyncCanonicalWins(it, canonical, canonicalBody)
	}
	data, err := os.ReadFile(it.Path)
	if err != nil {
		return fmt.Errorf("read tool body %s: %w", it.Path, err)
	}
	if err := os.MkdirAll(filepath.Dir(canonicalBody), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(canonicalBody), ".body-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), canonicalBody); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	// Now bring every projection back in line with the new canonical.
	return resyncCanonicalWins(it, canonical, canonicalBody)
}
