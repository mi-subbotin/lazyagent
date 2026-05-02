package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/budget"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

// budgetOverlay is the read-only context-budget summary opened with
// `b` (PRI-66). It estimates the passive token cost of every loaded
// item — Skills, Agents, Memory, Prompts, and a proxy for MCP — and
// rolls them up by Origin × Kind × Scope so the user can see where
// their context window goes.
//
// The reference window is 200k tokens (Claude default). Tab cycles
// the reference between Claude 200k → Codex 256k → Gemini 1M → back.
// Numbers don't change across windows; only the % bar does.
type budgetOverlay struct {
	window budgetWindow
}

type budgetWindow int

const (
	budgetWindowClaude budgetWindow = iota
	budgetWindowCodex
	budgetWindowGemini
)

func (w budgetWindow) String() string {
	switch w {
	case budgetWindowCodex:
		return "Codex 256k"
	case budgetWindowGemini:
		return "Gemini 1M"
	default:
		return "Claude 200k"
	}
}

func (w budgetWindow) limit() int {
	switch w {
	case budgetWindowCodex:
		return 256_000
	case budgetWindowGemini:
		return 1_000_000
	default:
		return 200_000
	}
}

func newBudgetOverlay() *budgetOverlay {
	return &budgetOverlay{window: budgetWindowClaude}
}

func (m Model) updateBudgetOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter", "q", "b":
		m.budgeting = nil
		return m, nil
	case "tab":
		m.budgeting.window = (m.budgeting.window + 1) % 3
		return m, nil
	case "shift+tab":
		m.budgeting.window = (m.budgeting.window + 2) % 3
		return m, nil
	}
	return m, nil
}

// budgetOverlayText renders the body. Pure for unit testing — the
// caller wraps it in the shared overlay frame at View() time.
func budgetOverlayText(sum budget.Summary, win budgetWindow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Context budget · %s\n", win.String())
	b.WriteString(strings.Repeat("─", 60) + "\n")
	if sum.Items == 0 {
		b.WriteString("No installed items in passive context.\n\n")
		b.WriteString("Tab: cycle reference window · esc: close\n")
		return b.String()
	}
	pct := percentOfLimit(sum.Total, win.limit())
	fmt.Fprintf(&b, "Total      %s tokens across %d items  (%s of limit)\n",
		budget.FormatTokens(sum.Total), sum.Items, pct)
	if sum.Lossy > 0 {
		fmt.Fprintf(&b, "           %d item(s) couldn't be measured (read error)\n", sum.Lossy)
	}
	b.WriteString("\n" + budgetBar(sum.Total, win.limit()) + "\n\n")

	// Sort groups by tokens desc for "where's the weight" reading.
	groups := make([]budget.Group, len(sum.Groups))
	copy(groups, sum.Groups)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Tokens > groups[j].Tokens })

	b.WriteString("By origin × kind × scope\n")
	for _, g := range groups {
		share := percentOfLimit(g.Tokens, win.limit())
		fmt.Fprintf(&b, "  %-10s %-10s %-7s  %7s tok  %5s  (%d)\n",
			truncate(originShort(g.Origin), 10),
			truncate(kindShort(g.Kind), 10),
			truncate(scopeShort(g.Scope), 7),
			budget.FormatTokens(g.Tokens),
			share,
			g.Items,
		)
	}
	b.WriteString("\nTab: cycle window · esc: close\n")
	return b.String()
}

// percentOfLimit returns "12.3%" / "0.4%" — kept as a helper so the
// rounding stays consistent between the headline and the per-group
// share column.
func percentOfLimit(n, limit int) string {
	if limit <= 0 {
		return "—"
	}
	pct := float64(n) * 100.0 / float64(limit)
	if pct >= 10 {
		return fmt.Sprintf("%.0f%%", pct)
	}
	return fmt.Sprintf("%.1f%%", pct)
}

// budgetBar draws a 40-char usage bar. Capped at 100% — overflowing
// installs (e.g. someone hammering 500k of skills against a 200k
// window) get a `>>>` suffix instead of the bar wrapping.
func budgetBar(n, limit int) string {
	const width = 40
	if limit <= 0 {
		return strings.Repeat("░", width)
	}
	frac := float64(n) / float64(limit)
	overflow := false
	if frac > 1 {
		frac = 1
		overflow = true
	}
	filled := int(frac * float64(width))
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	if overflow {
		bar += " >>>"
	}
	return "[" + bar + "]"
}

func kindShort(k model.Kind) string {
	switch k {
	case model.KindSkill:
		return "Skill"
	case model.KindAgent:
		return "Agent"
	case model.KindMCP:
		return "MCP"
	case model.KindPrompt:
		return "Command"
	case model.KindMemory:
		return "Memory"
	case model.KindHook:
		return "Hook"
	case model.KindSession:
		return "Session"
	default:
		return k.String()
	}
}

func scopeShort(s model.Scope) string {
	switch s {
	case model.ScopeGlobal:
		return "Global"
	case model.ScopeLocal:
		return "Local"
	default:
		return ""
	}
}
