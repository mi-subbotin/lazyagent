package guardrails

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// TooManySkills warns once a user has accumulated more than
// Threshold real skills (KindSkill, anything not Memory/Session). At
// 2x Threshold the action escalates to Block — past that, every new
// session pays a measurable token tax for skill descriptions.
type TooManySkills struct {
	Threshold int
}

func (TooManySkills) Name() string { return "too-many-skills" }

func (TooManySkills) Description() string {
	return "Warn when the active skill count gets large enough to bloat the context window."
}

func (g TooManySkills) Evaluate(ctx EvalContext) Result {
	threshold := g.Threshold
	if threshold <= 0 {
		threshold = 100
	}

	skills := make([]model.Item, 0)
	for _, it := range ctx.Items {
		if it.Kind != model.KindSkill {
			continue
		}
		skills = append(skills, it)
	}
	count := len(skills)
	if count <= threshold {
		return Result{Action: ActionAllow}
	}

	idle := topIdle(skills, 5)
	var b strings.Builder
	fmt.Fprintf(&b, "lazyagent guardrail: %d skills active (threshold %d). Each session reloads every skill description, so a large list eats into the context budget.", count, threshold)
	if len(idle) > 0 {
		b.WriteString(" Top idle candidates to retire:")
		for _, s := range idle {
			if s.LastSeen.IsZero() {
				fmt.Fprintf(&b, " %s (never seen);", s.Name)
			} else {
				days := int(time.Since(s.LastSeen).Hours() / 24)
				fmt.Fprintf(&b, " %s (%dd idle);", s.Name, days)
			}
		}
	}

	if count > threshold*2 {
		return Result{Action: ActionBlock, Message: b.String()}
	}
	return Result{Action: ActionWarn, Message: b.String()}
}

// topIdle returns the n skills with the oldest LastSeen, with
// never-seen items first (zero time sorts before any real time).
func topIdle(items []model.Item, n int) []model.Item {
	cp := append([]model.Item(nil), items...)
	sort.SliceStable(cp, func(i, j int) bool {
		ti, tj := cp[i].LastSeen, cp[j].LastSeen
		if ti.IsZero() && !tj.IsZero() {
			return true
		}
		if tj.IsZero() && !ti.IsZero() {
			return false
		}
		return ti.Before(tj)
	})
	if len(cp) > n {
		cp = cp[:n]
	}
	return cp
}

func init() {
	Register(TooManySkills{})
}
