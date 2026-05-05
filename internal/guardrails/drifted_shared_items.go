package guardrails

import (
	"fmt"

	"github.com/mi-subbotin/lazyagent/internal/actions"
)

// DriftedSharedItems flags shared-store items whose tool-side projection
// has drifted from canonical. SyncAll's Plan classifies these as
// ActionResync; if any are present we warn so the user runs `S` /
// `library sync` before stale bytes load into the next session.
type DriftedSharedItems struct{}

func (DriftedSharedItems) Name() string { return "drifted-shared-items" }

func (DriftedSharedItems) Description() string {
	return "Warn when shared-store items have drifted from their tool-side projections."
}

func (DriftedSharedItems) Evaluate(ctx EvalContext) Result {
	plan := actions.SyncAll(ctx.Items)
	counts := plan.Counts()
	n := counts[actions.ActionResync]
	if n == 0 {
		return Result{Action: ActionAllow}
	}
	msg := fmt.Sprintf("lazyagent guardrail: %d shared item(s) have drifted from canonical. Run `lazyagent S` in TUI or `lazyagent library sync` to resolve.", n)
	return Result{Action: ActionWarn, Message: msg}
}

func init() {
	Register(DriftedSharedItems{})
}
