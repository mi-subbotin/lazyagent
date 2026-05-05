package tui

import (
	"fmt"

	"github.com/mi-subbotin/lazyagent/internal/actions"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

// applyMerge consolidates the duplicate group `it` belongs to by
// promoting `it` as canonical and projecting onto every (origin, scope)
// cell any group member currently occupies. Place auto-snapshots the
// displaced bytes via PRI-92, so callers don't need to backup first.
//
// Returns nil and a no-op when the focused item is not part of a group.
// Same-tool different-scope merges are supported for every shape; cross-
// tool entry routing is bounded by Place's own ErrPlaceUnsupported.
func (m Model) applyMerge(it model.Item) error {
	if it.DupGroup == "" {
		return fmt.Errorf("merge: item is not in a duplicate group")
	}
	targets := m.mergeTargets(it)
	if len(targets) == 0 {
		return fmt.Errorf("merge: no targets resolved for %s", it.Name)
	}
	opts := actions.PlaceOpts{ProjectDir: m.projectDir, Overwrite: true}
	if err := actions.Place(it, targets, opts); err != nil {
		return fmt.Errorf("merge: %w", err)
	}
	return nil
}

// mergeTargets is the union of (Origin, Scope) cells across the
// duplicate group `it` belongs to. Shared canonical members contribute
// nothing — they're handled implicitly by Place via the library.
func (m Model) mergeTargets(it model.Item) []actions.ProjectionTarget {
	seen := map[actions.ProjectionTarget]bool{}
	var out []actions.ProjectionTarget
	for _, other := range m.items {
		if other.DupGroup != it.DupGroup {
			continue
		}
		if other.Origin == model.OriginShared {
			continue
		}
		t := actions.ProjectionTarget{Origin: other.Origin, Scope: other.Scope}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
