// One-button sync-all (PRI-27).
//
// SyncAll plans the bulk equivalent of the per-item `s` flow: walk every
// shareable global item, decide whether it needs to be imported into
// the canonical store, projected to an additional tool, or left alone,
// and return a Plan the caller can preview before mutation. ApplyPlan
// then runs the plan, calling the existing Share / Reshare primitives
// so conflict-handling and manifest behaviour stay consistent.
//
// Scope decision (per the design doc): v1 ships the lossless subset —
// skills + memory across all three tools, agents/prompts where they
// don't need format conversion. Items needing conversion (Codex agent
// profiles, Gemini TOML prompts, MCP entries, hooks) surface as Skip
// ops with a Reason explaining why, so the user sees a complete
// picture instead of items silently disappearing from the plan.
//
// Source-of-truth policy ("hybrid (d)" from the design): items already
// in the store stay canonical and get projected to any missing tool;
// items not in the store are imported from whichever tool currently
// has them (de-duped by name, first-wins on a deterministic order).

package actions

import (
	"errors"
	"fmt"
	"sort"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// PlanAction enumerates the kinds of mutation a SyncAll Plan can carry.
type PlanAction int

const (
	// ActionSkip means the item is fine where it is — already in the
	// store and projected to every supported tool. The Reason field
	// explains *why* a reasonable-looking item was skipped (unsupported
	// kind, format conversion required, local scope).
	ActionSkip PlanAction = iota
	// ActionImport means the item is not yet in the canonical store;
	// SyncAll will move its bytes in and project to every supported tool.
	ActionImport
	// ActionProject means the item is already canonical but missing
	// from one or more tools; SyncAll will create the projection links.
	ActionProject
	// ActionResync means the item is shared but its tool projection has
	// drifted (copy-mode bytes diverge from canonical). SyncAll runs
	// Reshare with the current target set so canonical wins.
	ActionResync
)

func (a PlanAction) String() string {
	switch a {
	case ActionSkip:
		return "skip"
	case ActionImport:
		return "import"
	case ActionProject:
		return "project"
	case ActionResync:
		return "resync"
	}
	return "?"
}

// PlanOp is one entry in a Plan: a single intended mutation. Targets
// only matter for Import / Project (which tools to wire up); Reason is
// populated for Skip so the previewer can explain the decision.
type PlanOp struct {
	Item    model.Item
	Action  PlanAction
	Targets []model.Origin
	Reason  string
}

// Plan is the full result of SyncAll: one PlanOp per item considered.
// Sort order is stable: by Kind, then Name. Apply / preview consumers
// can iterate sequentially without re-sorting.
type Plan struct {
	Ops []PlanOp
}

// Counts returns the number of ops per action — useful for one-line
// preview headers ("2 import, 5 project, 1 resync, 12 skip").
func (p Plan) Counts() map[PlanAction]int {
	out := map[PlanAction]int{}
	for _, op := range p.Ops {
		out[op.Action]++
	}
	return out
}

// Mutating reports whether the plan has any non-skip ops. The TUI uses
// this to short-circuit the confirm overlay when there's nothing to do.
func (p Plan) Mutating() bool {
	for _, op := range p.Ops {
		if op.Action != ActionSkip {
			return true
		}
	}
	return false
}

// SyncAll inspects items and produces a Plan describing what SyncAll
// would do. Pure — never touches the filesystem. Caller passes the
// model.Items slice straight from the active TUI state (or the
// adapter-aggregate output for headless `lazyagent shared sync`).
//
// Items at Scope=Local are skipped (sync-all is global by design;
// per design doc decision 4). Unsupported kinds (MCP, Hook, Session)
// are skipped with a Reason. Items with format-conversion-required
// projections (Codex agents, Gemini prompts) are skipped on the
// per-tool side but still imported / projected to the supported
// targets — half-coverage is better than nothing.
func SyncAll(items []model.Item) Plan {
	allTargets := []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini}

	// First pass: build a name → source item table, deduping across
	// tools. Source preference is the tool that has the item already
	// in the store (canonical wins); falling back to OriginClaude
	// over Codex over Gemini for deterministic first-wins selection.
	type key struct {
		kind model.Kind
		name string
	}
	type seenItem struct {
		item      model.Item
		canonical bool
	}
	seen := map[key]seenItem{}
	originRank := map[model.Origin]int{
		model.OriginClaude: 0,
		model.OriginCodex:  1,
		model.OriginGemini: 2,
		model.OriginShared: -1,
	}

	for _, it := range items {
		if !CanShare(it) {
			continue
		}
		k := key{kind: it.Kind, name: it.Name}
		existing, ok := seen[k]
		if !ok {
			seen[k] = seenItem{item: it, canonical: it.Shared}
			continue
		}
		// Already-shared item is the canonical source for this name
		// regardless of which tool surfaced it second. Otherwise pick
		// the lower-rank origin (Claude beats Codex beats Gemini).
		if it.Shared && !existing.canonical {
			seen[k] = seenItem{item: it, canonical: true}
			continue
		}
		if !existing.canonical && originRank[it.Origin] < originRank[existing.item.Origin] {
			seen[k] = seenItem{item: it, canonical: false}
		}
	}

	// Second pass: classify each unique (kind, name) into a PlanOp.
	plan := Plan{}
	for _, s := range seen {
		it := s.item

		// Pick the subset of targets we can project to without format
		// conversion. Anything else gets a follow-up note rather than
		// being silently dropped.
		var projTargets []model.Origin
		var droppedTargets []model.Origin
		for _, t := range allTargets {
			if CanProjectTo(it.Kind, t) {
				projTargets = append(projTargets, t)
			} else {
				droppedTargets = append(droppedTargets, t)
			}
		}

		op := PlanOp{Item: it, Targets: projTargets}

		switch {
		case s.canonical && it.Drift:
			op.Action = ActionResync
		case s.canonical:
			// Already shared. Are all eligible projections already in
			// place? If so, skip; if not, project the missing ones.
			current := CurrentProjections(it)
			missing := diffOrigin(projTargets, current)
			if len(missing) == 0 {
				op.Action = ActionSkip
				op.Reason = "already projected to every supported tool"
			} else {
				op.Action = ActionProject
				op.Targets = missing
			}
		default:
			op.Action = ActionImport
		}

		if len(droppedTargets) > 0 && op.Action != ActionSkip {
			op.Reason = fmt.Sprintf("skipping %d unsupported target(s) (format conversion required)", len(droppedTargets))
		}
		plan.Ops = append(plan.Ops, op)
	}

	// Account for items SyncAll *could* have shared but the kind
	// itself isn't supported (MCP, Hook, etc.) — surface them as Skip
	// rows so the preview shows a complete count of considered items.
	type unsupportedKey struct {
		kind model.Kind
		name string
	}
	added := map[unsupportedKey]bool{}
	for _, it := range items {
		if it.Scope != model.ScopeGlobal {
			continue
		}
		if CanShare(it) {
			continue
		}
		uk := unsupportedKey{it.Kind, it.Name}
		if added[uk] {
			continue
		}
		added[uk] = true
		plan.Ops = append(plan.Ops, PlanOp{
			Item:   it,
			Action: ActionSkip,
			Reason: fmt.Sprintf("share unsupported for %s storage shape", it.Kind),
		})
	}

	sort.SliceStable(plan.Ops, func(i, j int) bool {
		if plan.Ops[i].Item.Kind != plan.Ops[j].Item.Kind {
			return plan.Ops[i].Item.Kind < plan.Ops[j].Item.Kind
		}
		return plan.Ops[i].Item.Name < plan.Ops[j].Item.Name
	})
	return plan
}

// ApplyPlan executes every non-Skip op in plan, returning the slice of
// per-op errors keyed by Item.Name (empty on success). Errors do not
// abort the run — a single bad item shouldn't block the rest of the
// sync. ErrShareConflicts is surfaced as-is so the caller can re-call
// with overwrite=true after confirmation.
func ApplyPlan(plan Plan, overwrite bool) []error {
	var errs []error
	for _, op := range plan.Ops {
		switch op.Action {
		case ActionSkip:
			continue
		case ActionImport:
			if err := Share(op.Item, op.Targets, overwrite); err != nil {
				errs = append(errs, fmt.Errorf("import %s: %w", op.Item.Name, err))
			}
		case ActionProject, ActionResync:
			// Reshare wants the *full* desired target set, not the
			// delta. CurrentProjections + planned additions = desired.
			current := CurrentProjections(op.Item)
			full := append([]model.Origin(nil), current...)
			for _, t := range op.Targets {
				dup := false
				for _, c := range current {
					if c == t {
						dup = true
						break
					}
				}
				if !dup {
					full = append(full, t)
				}
			}
			if err := Reshare(op.Item, full, overwrite); err != nil {
				errs = append(errs, fmt.Errorf("project %s: %w", op.Item.Name, err))
			}
		}
	}
	return errs
}

// IsSyncConflict reports whether any error from ApplyPlan is the
// share-conflict sentinel. Useful for the CLI to decide between
// "exit with explanatory message" and "retry with --yes".
func IsSyncConflict(errs []error) bool {
	for _, e := range errs {
		if errors.Is(e, ErrShareConflicts) {
			return true
		}
	}
	return false
}
