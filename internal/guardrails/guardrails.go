// Package guardrails defines the contract for AI-helper guardrails
// (PRI-67). A guardrail evaluates the aggregated model.Item set against a
// rule (too many skills, drifted shared items, oversized memory file) and
// returns an Action telling the caller whether to allow, warn or block.
//
// Guardrails are designed to be invoked by Claude Code via the SessionStart
// hook: a registered hook calls `lazyagent guardrail eval --name=<n>`,
// the CLI loads sources, runs the guardrail, and emits the
// Claude-protocol JSON envelope (continue/stopReason/additionalContext).
package guardrails

import (
	"github.com/mi-subbotin/lazyagent/internal/model"
)

// Action is the verdict a Guardrail returns. Mapped 1:1 to the Claude
// hook protocol on the CLI side: Allow → {"continue": true}, Warn →
// {"continue": true, "additionalContext": Message}, Block →
// {"continue": false, "stopReason": Message}.
type Action int

const (
	ActionAllow Action = iota
	ActionWarn
	ActionBlock
)

func (a Action) String() string {
	switch a {
	case ActionAllow:
		return "allow"
	case ActionWarn:
		return "warn"
	case ActionBlock:
		return "block"
	}
	return "?"
}

// Result is what Evaluate returns. Message is interpreted differently
// per Action — see the Action doc.
type Result struct {
	Action  Action
	Message string
}

// EvalContext is the input to Evaluate. RawInput carries whatever JSON
// envelope the host sent (Claude SessionStart hook payload). HookEvent
// is the hook_event_name extracted from that envelope, if any.
type EvalContext struct {
	Items      []model.Item
	ProjectDir string
	HookEvent  string
	RawInput   map[string]any
}

// Guardrail is the interface every rule implements. Name is the stable
// identifier used in CLI flags and as the install marker; Description is
// a single line shown by `lazyagent guardrail list`.
type Guardrail interface {
	Name() string
	Description() string
	Evaluate(ctx EvalContext) Result
}

// registry holds the active set of guardrails keyed by Name. Built-ins
// are registered from each rule file's init().
var registry = map[string]Guardrail{}

// Register adds g to the active registry. Panics on a duplicate Name —
// guardrail names are part of the install marker and must be unique.
func Register(g Guardrail) {
	if _, dup := registry[g.Name()]; dup {
		panic("guardrails: duplicate registration: " + g.Name())
	}
	registry[g.Name()] = g
}

// All returns every registered guardrail in deterministic name order.
func All() []Guardrail {
	out := make([]Guardrail, 0, len(registry))
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	// stable iteration
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	for _, n := range names {
		out = append(out, registry[n])
	}
	return out
}

// Get looks up a guardrail by Name.
func Get(name string) (Guardrail, bool) {
	g, ok := registry[name]
	return g, ok
}
